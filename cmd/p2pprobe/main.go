// p2pprobe runs isolated P2P nodes with a loopback-only test controller.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/tentechtop/ordexus-core/p2p"
	"github.com/tentechtop/ordexus-core/utils"
)

const echoProtocol p2p.ProtocolID = 100001

type probe struct {
	host     *p2p.Host
	identity p2p.SecureSessionIdentity
	address  utils.MultiAddress
	relay    string
	mu       sync.Mutex
	busy     bool
}

type loadOptions struct {
	Target          string `json:"target"`
	Mode            string `json:"mode"`
	DurationSeconds int    `json:"duration_seconds"`
	Concurrency     int    `json:"concurrency"`
	PayloadBytes    int    `json:"payload_bytes"`
	TimeoutMillis   int    `json:"timeout_millis"`
	IntervalMillis  int    `json:"interval_millis"`
}

type loadResult struct {
	Options                loadOptions     `json:"options"`
	StartedAt              time.Time       `json:"started_at"`
	ElapsedSeconds         float64         `json:"elapsed_seconds"`
	Succeeded              int             `json:"succeeded"`
	Failed                 int             `json:"failed"`
	PayloadMiBPerSecond    float64         `json:"payload_mib_per_second"`
	P50Millis              float64         `json:"p50_ms"`
	P95Millis              float64         `json:"p95_ms"`
	P99Millis              float64         `json:"p99_ms"`
	MaxMillis              float64         `json:"max_ms"`
	Errors                 map[string]int  `json:"errors"`
	FirstErrorAt           *time.Time      `json:"first_error_at,omitempty"`
	LastErrorAt            *time.Time      `json:"last_error_at,omitempty"`
	FirstSuccessAfterError *time.Time      `json:"first_success_after_error,omitempty"`
	DirectBefore           bool            `json:"direct_before"`
	DirectAfter            bool            `json:"direct_after"`
	Metrics                p2p.HostMetrics `json:"metrics"`
}

func main() {
	if err := run(); err != nil {
		slog.Error("probe exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	protocolText := flag.String("protocol", "quic", "quic or tcp")
	ip := flag.String("listen-ip", "0.0.0.0", "P2P listen IP")
	advertise := flag.String("advertised-ip", "127.0.0.1", "advertised IP")
	port := flag.Int("port", 55101, "P2P port")
	control := flag.String("control", "127.0.0.1:55111", "loopback HTTP controller")
	key := flag.String("identity", "probe-identity.json", "temporary test identity file")
	network := flag.String("network-id", "ordexus-core:p2p-wan-probe:v1", "isolated secure network ID")
	upstream := flag.String("relay", "", "public relay multi-address; reconnect only this peer")
	relayServer := flag.Bool("relay-server", false, "advertise relay capability")
	flag.Parse()
	controlIP, _, err := net.SplitHostPort(*control)
	if err != nil || !net.ParseIP(controlIP).IsLoopback() {
		return errors.New("control must bind a loopback IP")
	}
	protocol, err := utils.ParseMultiAddressProtocol(*protocolText)
	if err != nil {
		return err
	}
	identity, err := loadIdentity(*key, *network)
	if err != nil {
		return err
	}
	address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, *ip, protocol, *port, identity.PeerID)
	if err != nil {
		return err
	}
	advertised, err := utils.BuildMultiAddress(utils.MultiAddressIP4, *advertise, protocol, *port, identity.PeerID)
	if err != nil {
		return err
	}
	var capabilities p2p.PeerCapability
	if *relayServer {
		capabilities = p2p.PeerCapabilityRelay
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	host, err := p2p.NewHost(p2p.HostConfig{
		PeerID: identity.PeerID, SecureIdentity: identity, EnableSecureSession: true,
		Capabilities: capabilities, PreferredProtocols: []utils.MultiAddressProtocol{protocol},
		AdvertisedAddresses: []utils.MultiAddress{advertised}, Logger: logger,
		DialTimeout: 3 * time.Second, RequestTimeout: 5 * time.Second, HeartbeatInterval: 5 * time.Second,
		ConnectionIdle: 30 * time.Second, MaxPeers: 16, MaxConnections: 16, MaxConnectionsPerIP: 8,
		Relay: p2p.RelayConfig{RequireSignature: true},
	})
	if err != nil {
		return err
	}
	defer host.Close()
	p := &probe{host: host, identity: identity, address: advertised}
	err = host.RegisterResultHandler(p2p.ProtocolSpec{ID: echoProtocol, Name: "/p2p/probe/echo/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityNormal, Concurrency: p2p.ProtocolConcurrencyStateless}, func(ctx context.Context, message p2p.Message) (p2p.Message, error) {
		response, err := p2p.NewResponseMessage(identity.PeerID, echoProtocol, message.ID, message.Payload)
		response.ToPeerID = message.FromPeerID
		return response, err
	})
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	errorsCh := make(chan error, 2)
	go func() { errorsCh <- host.Listen(ctx, address, host.HandleConnection) }()
	go host.StartHeartbeat(ctx)
	if *upstream != "" {
		a, err := utils.ParseMultiAddress(*upstream)
		if err != nil {
			return err
		}
		peer, err := p2p.NewPeer(a.PeerID, []utils.MultiAddress{a})
		if err != nil {
			return err
		}
		peer.Capabilities = p2p.PeerCapabilityRelay
		if err := host.AddPeer(peer); err != nil {
			return err
		}
		p.relay = a.PeerID
		go func() {
			// Listening first preserves the QUIC socket used for NAT traversal.
			timer := time.NewTimer(500 * time.Millisecond)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
				if _, ok := host.Connection(a.PeerID); !ok {
					dialCtx, done := context.WithTimeout(ctx, 3*time.Second)
					_, err := host.DialPeer(dialCtx, a.PeerID)
					done()
					if err != nil {
						logger.Warn("probe relay reconnect", "error", err)
					}
				}
				timer.Reset(time.Second)
			}
		}()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, p.status()) })
	mux.HandleFunc("POST /load", p.load)
	mux.HandleFunc("POST /punch", p.punch)
	mux.HandleFunc("POST /disconnect", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Peer string `json:"peer"`
		}
		if err := decodeBody(w, r, &body); err != nil {
			return
		}
		connection, ok := host.Connection(body.Peer)
		if !ok {
			http.Error(w, "connection not found", 404)
			return
		}
		if err := connection.Close(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]bool{"closed": true})
	})
	mux.HandleFunc("POST /dial", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Address string `json:"address"`
		}
		if err := decodeBody(w, r, &body); err != nil {
			return
		}
		a, err := utils.ParseMultiAddress(body.Address)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		peer, err := p2p.NewPeer(a.PeerID, []utils.MultiAddress{a})
		if err == nil {
			err = host.AddPeer(peer)
		}
		if err == nil {
			var connection p2p.Connection
			connection, err = host.DialAddress(r.Context(), a)
			if err == nil {
				go host.HandleConnection(ctx, connection)
			}
		}
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		writeJSON(w, p.status())
	})
	server := &http.Server{Addr: *control, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() { errorsCh <- server.ListenAndServe() }()
	logger.Info("probe ready", "peer_id", identity.PeerID, "address", advertised.String(), "control", *control, "relay", p.relay)
	select {
	case <-ctx.Done():
		return nil
	case err := <-errorsCh:
		return err
	}
}

func loadIdentity(path, network string) (p2p.SecureSessionIdentity, error) {
	var identity p2p.SecureSessionIdentity
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &identity); err != nil {
			return identity, err
		}
	} else if !os.IsNotExist(err) {
		return identity, err
	} else {
		public, private, err := utils.GenerateEd25519KeyPairBytes()
		if err != nil {
			return identity, err
		}
		identity.PeerID, identity.PublicKey, identity.PrivateKey = utils.Base58Encode(public), public, private
	}
	identity.NetworkID, identity.SoftwareVersion = network, "github.com/tentechtop/ordexus-core/p2pprobe/1.0.0"
	identity.MinProtocolVersion, identity.MaxProtocolVersion = p2p.MessageProtocolVersion, p2p.MessageProtocolVersion
	if err := identity.Validate(); err != nil {
		return identity, err
	}
	if data == nil {
		encoded, err := json.MarshalIndent(identity, "", "  ")
		if err != nil {
			return identity, err
		}
		if err := os.WriteFile(path, encoded, 0600); err != nil {
			return identity, err
		}
	}
	return identity, nil
}

func (p *probe) status() any {
	connections := make([]p2p.ConnectionState, 0)
	for _, peer := range p.host.PeerSnapshots() {
		if state, ok := p.host.ConnectionState(peer.ID); ok {
			connections = append(connections, state)
		}
	}
	return map[string]any{"peer_id": p.identity.PeerID, "address": p.address.String(), "relay": p.relay, "connections": connections, "metrics": p.host.Metrics()}
}

func (p *probe) punch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	started := time.Now()
	connection, err := p.host.PunchQUICPeer(r.Context(), p.relay, body.Target)
	result := map[string]any{"elapsed_ms": float64(time.Since(started).Microseconds()) / 1000, "success": err == nil}
	if err != nil {
		result["error"] = err.Error()
	} else {
		result["local_address"] = connection.LocalAddress()
		result["remote_address"] = connection.RemoteAddress()
	}
	result["status"] = p.status()
	writeJSON(w, result)
}

func (p *probe) load(w http.ResponseWriter, r *http.Request) {
	var options loadOptions
	if err := decodeBody(w, r, &options); err != nil {
		return
	}
	if options.DurationSeconds < 1 || options.DurationSeconds > 1800 || options.Concurrency < 1 || options.Concurrency > 64 || options.PayloadBytes < 1 || options.PayloadBytes > 512*1024 || options.TimeoutMillis < 100 || options.TimeoutMillis > 30000 || options.IntervalMillis < 0 || options.IntervalMillis > 60000 || (options.Mode != "relay" && options.Mode != "direct") {
		http.Error(w, "invalid load options", 400)
		return
	}
	p.mu.Lock()
	if p.busy {
		p.mu.Unlock()
		http.Error(w, "load already running", 409)
		return
	}
	p.busy = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.busy = false; p.mu.Unlock() }()
	_, direct := p.host.Connection(options.Target)
	if options.Mode == "relay" && (direct || p.relay == "") {
		http.Error(w, "relay test requires an upstream and no target connection", 409)
		return
	}
	if options.Mode == "direct" && !direct {
		http.Error(w, "direct test requires an established target connection", 409)
		return
	}
	result := loadResult{Options: options, StartedAt: time.Now().UTC(), Errors: make(map[string]int), DirectBefore: direct}
	deadline := time.Now().Add(time.Duration(options.DurationSeconds) * time.Second)
	var mu sync.Mutex
	var workers sync.WaitGroup
	var latencies []float64
	for worker := 0; worker < options.Concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			payload := make([]byte, options.PayloadBytes)
			if _, err := rand.Read(payload); err != nil {
				return
			}
			for time.Now().Before(deadline) && r.Context().Err() == nil {
				start := time.Now()
				request, err := p2p.NewRequestMessage(p.identity.PeerID, echoProtocol, payload)
				var response p2p.Message
				ctx, cancel := context.WithTimeout(r.Context(), time.Duration(options.TimeoutMillis)*time.Millisecond)
				if err == nil {
					if options.Mode == "relay" {
						response, err = p.host.RequestRelay(ctx, p.relay, options.Target, request)
					} else {
						if _, ok := p.host.Connection(options.Target); !ok {
							err = errors.New("direct connection lost")
						} else {
							response, err = p.host.Request(ctx, options.Target, request)
						}
					}
				}
				cancel()
				if err == nil && !bytes.Equal(payload, response.Payload) {
					err = errors.New("payload mismatch")
				}
				now := time.Now().UTC()
				mu.Lock()
				if err != nil {
					result.Failed++
					result.Errors[err.Error()]++
					if result.FirstErrorAt == nil {
						t := now
						result.FirstErrorAt = &t
					}
					t := now
					result.LastErrorAt = &t
				} else {
					result.Succeeded++
					if len(latencies) < 1000000 {
						latencies = append(latencies, float64(time.Since(start).Microseconds())/1000)
					}
					if result.FirstErrorAt != nil && result.FirstSuccessAfterError == nil {
						t := now
						result.FirstSuccessAfterError = &t
					}
				}
				mu.Unlock()
				if options.IntervalMillis > 0 {
					timer := time.NewTimer(time.Duration(options.IntervalMillis) * time.Millisecond)
					select {
					case <-r.Context().Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}
		}()
	}
	workers.Wait()
	result.ElapsedSeconds = time.Since(result.StartedAt).Seconds()
	result.PayloadMiBPerSecond = float64(result.Succeeded) * float64(options.PayloadBytes) / 1048576 / result.ElapsedSeconds
	sort.Float64s(latencies)
	if len(latencies) > 0 {
		result.P50Millis = latencies[(len(latencies)-1)*50/100]
		result.P95Millis = latencies[(len(latencies)-1)*95/100]
		result.P99Millis = latencies[(len(latencies)-1)*99/100]
		result.MaxMillis = latencies[len(latencies)-1]
	}
	_, result.DirectAfter = p.host.Connection(options.Target)
	result.Metrics = p.host.Metrics()
	writeJSON(w, result)
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		http.Error(w, err.Error(), 400)
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("probe response", "error", fmt.Sprint(err))
	}
}
