package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"solana_golang/p2p"
	"solana_golang/utils"
)

const (
	stressProtocolID        p2p.ProtocolID = 100000
	defaultNetworkID                       = "solana_golang:p2p-stress:00000000000000000000000000000000"
	defaultSoftware                        = "solana_golang/p2pstress/0.1.0"
	defaultRequestTimeout                  = 5 * time.Second
	defaultReportInterval                  = 5 * time.Second
	maxLatencySamples                      = 300000
	stressPayloadHeaderSize                = 24
)

type stressConfig struct {
	protocol           utils.MultiAddressProtocol
	preferredProtocols []utils.MultiAddressProtocol
	listenIP           string
	advertisedIP       string
	port               int
	identityFile       string
	networkID          string
	bootstrap          []string
	targets            []string
	duration           time.Duration
	concurrency        int
	payloadBytes       int
	rateLimit          int
	requestTimeout     time.Duration
	reportInterval     time.Duration
	startDelay         time.Duration
	linger             time.Duration
	warmup             bool
	maxRequests        uint64
	maxPeers           int
	maxConnections     int
	maxMessageSize     int
	writeQueueSize     int
	protocolWorkers    int
	serverDelay        time.Duration
	inboundRate        int
	profileDir         string
}

type identityFile struct {
	PeerID     string `json:"peer_id"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

type stressStats struct {
	startedAt       time.Time
	sent            atomic.Uint64
	succeeded       atomic.Uint64
	failed          atomic.Uint64
	bytesReceived   atomic.Uint64
	latencyMutex    sync.Mutex
	latenciesMicros []int64
	errorMutex      sync.Mutex
	errors          map[string]uint64
}

type requestRateLimiter struct {
	tokens <-chan time.Time
	stop   func()
}

func main() {
	if err := run(); err != nil {
		slog.Error("p2p stress exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	config, err := parseConfig()
	if err != nil {
		return err
	}
	enableRuntimeProfiles(config.profileDir)
	identity, err := loadOrCreateIdentity(config.identityFile, config.networkID)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	host, address, err := newStressHost(config, identity, logger)
	if err != nil {
		return err
	}
	defer host.Close()

	rootContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := registerStressHandler(host, identity.PeerID, config.serverDelay, config.maxMessageSize); err != nil {
		return err
	}
	go func() {
		if err := host.Listen(rootContext, address, host.HandleConnection); err != nil && rootContext.Err() == nil {
			logger.Error("stress listen failed", slog.Any("error", err))
			stop()
		}
	}()
	go host.StartHeartbeat(rootContext)
	time.Sleep(500 * time.Millisecond)

	if err := addBootstrapPeers(host, config.bootstrap); err != nil {
		return err
	}
	if len(config.bootstrap) > 0 {
		_, err := host.Bootstrap(rootContext, p2p.BootstrapConfig{
			Bootnodes:            parseAddresses(config.bootstrap),
			MinOutboundPeers:     minInt(len(config.bootstrap), 1),
			DialTimeout:          config.requestTimeout,
			StartConnectionLoops: true,
		})
		if err != nil {
			logger.Warn("stress bootstrap completed with error", slog.Any("error", err))
		}
	}
	targetPeerIDs, err := addTargetPeers(host, config.targets)
	if err != nil {
		return err
	}

	logger.Info("stress node ready",
		slog.String("peer_id", identity.PeerID),
		slog.String("address", address.String()),
		slog.String("protocol", string(config.protocol)),
		slog.Any("preferred_protocols", config.preferredProtocols),
		slog.Int("targets", len(targetPeerIDs)),
		slog.Int("concurrency", config.concurrency),
		slog.Int("payload_bytes", config.payloadBytes),
		slog.Int("max_message_size", config.maxMessageSize),
		slog.Int("rate_limit", config.rateLimit),
		slog.Int("inbound_rate_limit", config.inboundRate),
		slog.Bool("warmup", config.warmup),
	)
	if len(targetPeerIDs) == 0 || config.concurrency <= 0 || config.duration <= 0 {
		<-rootContext.Done()
		return nil
	}
	if config.startDelay > 0 {
		select {
		case <-time.After(config.startDelay):
		case <-rootContext.Done():
			return nil
		}
	}
	if config.warmup {
		warmed := warmupTargetConnections(rootContext, host, targetPeerIDs, config.requestTimeout, logger)
		logger.Info("stress warmup completed",
			slog.Int("succeeded", warmed),
			slog.Int("targets", len(targetPeerIDs)),
		)
	}

	loadErr := runLoad(rootContext, host, identity.PeerID, targetPeerIDs, config, logger)
	if config.linger > 0 {
		select {
		case <-time.After(config.linger):
		case <-rootContext.Done():
		}
	}
	return loadErr
}

func parseConfig() (stressConfig, error) {
	protocolValue := flag.String("protocol", "quic", "transport protocol: quic or tcp")
	preferredProtocolsValue := flag.String("preferred-protocols", "", "comma separated dial protocol order; empty uses -protocol only")
	listenIP := flag.String("listen-ip", "0.0.0.0", "listen ip")
	advertisedIP := flag.String("advertised-ip", "127.0.0.1", "advertised ip")
	port := flag.Int("port", 5002, "listen and advertised port")
	identityFile := flag.String("identity", "stress_identity.json", "identity file")
	networkID := flag.String("network-id", defaultNetworkID, "secure session network id")
	bootstrap := flag.String("bootstrap", "", "comma separated bootnode multi-addresses")
	targets := flag.String("targets", "", "comma separated target multi-addresses")
	duration := flag.Duration("duration", 0, "load duration; 0 means server only")
	concurrency := flag.Int("concurrency", 0, "request worker count")
	payloadBytes := flag.Int("payload", 1024, "request payload bytes")
	rateLimit := flag.Int("rate-limit", 0, "global request rate limit per second; 0 means unlimited")
	requestTimeout := flag.Duration("request-timeout", defaultRequestTimeout, "request timeout")
	reportInterval := flag.Duration("report-interval", defaultReportInterval, "report interval")
	startDelay := flag.Duration("start-delay", 0, "delay before load starts")
	linger := flag.Duration("linger", 0, "keep serving after load finishes")
	warmup := flag.Bool("warmup", false, "dial targets before load starts")
	maxRequests := flag.Uint64("max-requests", 0, "stop after total requests; 0 disables limit")
	maxPeers := flag.Int("max-peers", 512, "max peer count")
	maxConnections := flag.Int("max-connections", 512, "max connection count")
	writeQueueSize := flag.Int("write-queue", 4096, "async write queue size")
	maxMessageSize := flag.Int("max-message-size", p2p.DefaultMaxMessageSize, "max p2p message size")
	protocolWorkers := flag.Int("protocol-workers", 0, "protocol worker count; 0 uses default")
	serverDelay := flag.Duration("server-delay", 0, "server handler artificial delay")
	inboundRate := flag.Int("inbound-rate-limit", 0, "max inbound messages per second; 0 uses p2p default")
	profileDir := flag.String("profile-dir", "", "directory for heap, goroutine, mutex and block profiles")
	flag.Parse()

	protocol, err := utils.ParseMultiAddressProtocol(*protocolValue)
	if err != nil {
		return stressConfig{}, err
	}
	preferredProtocols, err := parsePreferredProtocols(*preferredProtocolsValue, protocol)
	if err != nil {
		return stressConfig{}, err
	}
	if *port < 1 || *port > 65535 {
		return stressConfig{}, fmt.Errorf("invalid port %d", *port)
	}
	if *maxMessageSize <= 0 || *maxMessageSize > p2p.MaxConfigurableMessageSize {
		return stressConfig{}, fmt.Errorf("invalid max message size %d", *maxMessageSize)
	}
	if *payloadBytes < stressPayloadHeaderSize || *payloadBytes > maxStressPayloadBytes(*maxMessageSize) {
		return stressConfig{}, fmt.Errorf("invalid payload size %d", *payloadBytes)
	}
	if *rateLimit < 0 {
		return stressConfig{}, fmt.Errorf("invalid rate limit %d", *rateLimit)
	}
	if *inboundRate < 0 {
		return stressConfig{}, fmt.Errorf("invalid inbound rate limit %d", *inboundRate)
	}
	return stressConfig{
		protocol:           protocol,
		preferredProtocols: preferredProtocols,
		listenIP:           *listenIP,
		advertisedIP:       *advertisedIP,
		port:               *port,
		identityFile:       *identityFile,
		networkID:          *networkID,
		bootstrap:          splitCSV(*bootstrap),
		targets:            splitCSV(*targets),
		duration:           *duration,
		concurrency:        *concurrency,
		payloadBytes:       *payloadBytes,
		rateLimit:          *rateLimit,
		requestTimeout:     *requestTimeout,
		reportInterval:     *reportInterval,
		startDelay:         *startDelay,
		linger:             *linger,
		warmup:             *warmup,
		maxRequests:        *maxRequests,
		maxPeers:           *maxPeers,
		maxConnections:     *maxConnections,
		maxMessageSize:     *maxMessageSize,
		writeQueueSize:     *writeQueueSize,
		protocolWorkers:    *protocolWorkers,
		serverDelay:        *serverDelay,
		inboundRate:        *inboundRate,
		profileDir:         strings.TrimSpace(*profileDir),
	}, nil
}

func parsePreferredProtocols(rawValue string, fallback utils.MultiAddressProtocol) ([]utils.MultiAddressProtocol, error) {
	values := splitCSV(rawValue)
	if len(values) == 0 {
		return []utils.MultiAddressProtocol{fallback}, nil
	}
	protocols := make([]utils.MultiAddressProtocol, 0, len(values))
	seen := make(map[utils.MultiAddressProtocol]struct{}, len(values))
	for _, value := range values {
		protocol, err := utils.ParseMultiAddressProtocol(value)
		if err != nil {
			return nil, fmt.Errorf("invalid preferred protocol %q: %w", value, err)
		}
		if _, ok := seen[protocol]; ok {
			continue
		}
		protocols = append(protocols, protocol)
		seen[protocol] = struct{}{}
	}
	return protocols, nil
}

func loadOrCreateIdentity(path string, networkID string) (p2p.SecureSessionIdentity, error) {
	if strings.TrimSpace(path) == "" {
		return p2p.SecureSessionIdentity{}, errors.New("identity file is required")
	}
	if data, err := os.ReadFile(path); err == nil {
		return decodeIdentity(data, networkID)
	}
	publicKey, privateKey, err := utils.GenerateEd25519KeyPairBytes()
	if err != nil {
		return p2p.SecureSessionIdentity{}, fmt.Errorf("generate identity: %w", err)
	}
	file := identityFile{
		PeerID:     utils.Base58Encode(publicKey),
		PublicKey:  base64.StdEncoding.EncodeToString(publicKey),
		PrivateKey: base64.StdEncoding.EncodeToString(privateKey),
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return p2p.SecureSessionIdentity{}, fmt.Errorf("marshal identity: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return p2p.SecureSessionIdentity{}, fmt.Errorf("write identity: %w", err)
	}
	return decodeIdentity(data, networkID)
}

func decodeIdentity(data []byte, networkID string) (p2p.SecureSessionIdentity, error) {
	var file identityFile
	if err := json.Unmarshal(data, &file); err != nil {
		return p2p.SecureSessionIdentity{}, fmt.Errorf("decode identity: %w", err)
	}
	publicKey, err := base64.StdEncoding.DecodeString(file.PublicKey)
	if err != nil {
		return p2p.SecureSessionIdentity{}, fmt.Errorf("decode public key: %w", err)
	}
	privateKey, err := base64.StdEncoding.DecodeString(file.PrivateKey)
	if err != nil {
		return p2p.SecureSessionIdentity{}, fmt.Errorf("decode private key: %w", err)
	}
	identity := p2p.SecureSessionIdentity{
		PeerID:             file.PeerID,
		PublicKey:          publicKey,
		PrivateKey:         privateKey,
		NetworkID:          networkID,
		SoftwareVersion:    defaultSoftware,
		MinProtocolVersion: p2p.MessageProtocolVersion,
		MaxProtocolVersion: p2p.MessageProtocolVersion,
	}
	if err := identity.Validate(); err != nil {
		return p2p.SecureSessionIdentity{}, err
	}
	return identity, nil
}

func newStressHost(
	config stressConfig,
	identity p2p.SecureSessionIdentity,
	logger *slog.Logger,
) (*p2p.Host, utils.MultiAddress, error) {
	listenAddress, err := utils.BuildMultiAddress(utils.MultiAddressIP4, config.listenIP, config.protocol, config.port, identity.PeerID)
	if err != nil {
		return nil, utils.MultiAddress{}, err
	}
	advertisedAddress, err := utils.BuildMultiAddress(utils.MultiAddressIP4, config.advertisedIP, config.protocol, config.port, identity.PeerID)
	if err != nil {
		return nil, utils.MultiAddress{}, err
	}
	host, err := p2p.NewHost(p2p.HostConfig{
		PeerID:              identity.PeerID,
		SecureIdentity:      identity,
		EnableSecureSession: true,
		PreferredProtocols:  config.preferredProtocols,
		DialTimeout:         config.requestTimeout,
		HandshakeTimeout:    config.requestTimeout,
		HeartbeatInterval:   10 * time.Second,
		ConnectionIdle:      60 * time.Second,
		MaxPeers:            config.maxPeers,
		MaxConnections:      config.maxConnections,
		MaxPendingInbound:   config.maxConnections,
		MaxConnectionsPerIP: config.maxConnections,
		MaxMessageSize:      config.maxMessageSize,
		AsyncWrite: p2p.AsyncWriteConfig{
			QueueSize:    config.writeQueueSize,
			WriteTimeout: config.requestTimeout,
		},
		ProtocolScheduler: p2p.ProtocolSchedulerConfig{
			WorkerCount:     config.protocolWorkers,
			PartitionCount:  config.protocolWorkers,
			HighQueueSize:   config.writeQueueSize,
			NormalQueueSize: config.writeQueueSize,
			LowQueueSize:    config.writeQueueSize,
			JobTimeout:      config.requestTimeout,
		},
		PeerProtection: p2p.PeerProtectionConfig{
			MaxInboundMessagesPerSecond: config.inboundRate,
		},
		AdvertisedAddresses: []utils.MultiAddress{advertisedAddress},
		Logger:              logger,
	})
	if err != nil {
		return nil, utils.MultiAddress{}, err
	}
	return host, listenAddress, nil
}

func registerStressHandler(host *p2p.Host, peerID string, delay time.Duration, maxMessageSize int) error {
	spec := p2p.ProtocolSpec{
		ID:          stressProtocolID,
		Name:        "/p2p/stress/request/1.0.0",
		HasResponse: true,
		Priority:    p2p.MessagePriorityNormal,
	}
	return host.RegisterResultHandler(spec, func(ctx context.Context, message p2p.Message) (p2p.Message, error) {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return p2p.Message{}, ctx.Err()
			}
		}
		response, err := p2p.NewResponseMessageWithMaxSize(peerID, stressProtocolID, message.ID, message.Payload, maxMessageSize)
		if err != nil {
			return p2p.Message{}, err
		}
		response.ToPeerID = message.FromPeerID
		return response, nil
	})
}

func addBootstrapPeers(host *p2p.Host, rawAddresses []string) error {
	for _, rawAddress := range rawAddresses {
		address, err := utils.ParseMultiAddress(rawAddress)
		if err != nil {
			return err
		}
		peer, err := p2p.NewPeer(address.PeerID, []utils.MultiAddress{address})
		if err != nil {
			return err
		}
		if err := host.AddPeer(peer); err != nil {
			return err
		}
	}
	return nil
}

func addTargetPeers(host *p2p.Host, rawAddresses []string) ([]string, error) {
	peerIDs := make([]string, 0, len(rawAddresses))
	for _, rawAddress := range rawAddresses {
		address, err := utils.ParseMultiAddress(rawAddress)
		if err != nil {
			return nil, err
		}
		peer, err := p2p.NewPeer(address.PeerID, []utils.MultiAddress{address})
		if err != nil {
			return nil, err
		}
		if err := host.AddPeer(peer); err != nil {
			return nil, err
		}
		peerIDs = append(peerIDs, address.PeerID)
	}
	return peerIDs, nil
}

func warmupTargetConnections(
	ctx context.Context,
	host *p2p.Host,
	targetPeerIDs []string,
	timeout time.Duration,
	logger *slog.Logger,
) int {
	succeeded := 0
	for _, peerID := range targetPeerIDs {
		dialContext, cancel := context.WithTimeout(ctx, timeout)
		_, err := host.DialPeer(dialContext, peerID)
		cancel()
		if err != nil {
			logger.Warn("stress warmup dial failed", slog.String("peer_id", peerID), slog.Any("error", err))
			continue
		}
		succeeded++
	}
	return succeeded
}

func runLoad(
	parent context.Context,
	host *p2p.Host,
	localPeerID string,
	targetPeerIDs []string,
	config stressConfig,
	logger *slog.Logger,
) error {
	loadContext, cancel := context.WithTimeout(parent, config.duration)
	defer cancel()
	stats := &stressStats{
		startedAt:       time.Now(),
		latenciesMicros: make([]int64, 0, minInt(maxLatencySamples, 65536)),
		errors:          make(map[string]uint64),
	}
	rateLimiter := makeRateLimiter(config.rateLimit)
	if rateLimiter.stop != nil {
		defer rateLimiter.stop()
	}
	var workers sync.WaitGroup
	for workerID := 0; workerID < config.concurrency; workerID++ {
		workers.Add(1)
		go stressWorker(loadContext, &workers, host, localPeerID, targetPeerIDs, config, stats, workerID, rateLimiter.tokens)
	}
	reportDone := make(chan struct{})
	go reportLoop(loadContext, reportDone, stats, config, logger)
	workers.Wait()
	cancel()
	<-reportDone
	printSummary(stats, config, logger)
	if config.profileDir != "" {
		dumpRuntimeProfiles(config.profileDir, logger)
	}
	if stats.failed.Load() > 0 {
		return fmt.Errorf("stress completed with %d failures", stats.failed.Load())
	}
	return nil
}

func makeRateLimiter(rateLimit int) requestRateLimiter {
	if rateLimit <= 0 {
		return requestRateLimiter{}
	}
	interval := time.Second / time.Duration(rateLimit)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	ticker := time.NewTicker(interval)
	return requestRateLimiter{
		tokens: ticker.C,
		stop:   ticker.Stop,
	}
}

func enableRuntimeProfiles(profileDir string) {
	if strings.TrimSpace(profileDir) == "" {
		return
	}
	runtime.SetMutexProfileFraction(10)
	runtime.SetBlockProfileRate(int(time.Millisecond))
}

func dumpRuntimeProfiles(profileDir string, logger *slog.Logger) {
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		logger.Warn("stress profile directory create failed", slog.String("dir", profileDir), slog.Any("error", err))
		return
	}
	names := []string{"heap", "goroutine", "mutex", "block"}
	for _, name := range names {
		path := profileDir + string(os.PathSeparator) + name + ".pprof"
		file, err := os.Create(path)
		if err != nil {
			logger.Warn("stress profile create failed", slog.String("path", path), slog.Any("error", err))
			continue
		}
		profile := pprof.Lookup(name)
		if profile == nil {
			_ = file.Close()
			continue
		}
		writeError := profile.WriteTo(file, 0)
		closeError := file.Close()
		if writeError != nil || closeError != nil {
			logger.Warn("stress profile write failed", slog.String("path", path), slog.Any("write_error", writeError), slog.Any("close_error", closeError))
			continue
		}
		logger.Info("stress profile written", slog.String("path", path))
	}
}

func stressWorker(
	ctx context.Context,
	workers *sync.WaitGroup,
	host *p2p.Host,
	localPeerID string,
	targetPeerIDs []string,
	config stressConfig,
	stats *stressStats,
	workerID int,
	rateLimiter <-chan time.Time,
) {
	defer workers.Done()
	payload := make([]byte, config.payloadBytes)
	copy(payload[stressPayloadHeaderSize:], []byte("solana_golang_p2p_stress_payload"))
	for {
		if ctx.Err() != nil {
			return
		}
		if rateLimiter != nil {
			select {
			case <-ctx.Done():
				return
			case <-rateLimiter:
			}
		}
		requestNumber := stats.sent.Add(1)
		if config.maxRequests > 0 && requestNumber > config.maxRequests {
			return
		}
		targetPeerID := targetPeerIDs[int(requestNumber)%len(targetPeerIDs)]
		startedAt := time.Now()
		fillPayload(payload, uint64(workerID), requestNumber, startedAt)
		request, err := p2p.NewRequestMessageWithMaxSize(localPeerID, stressProtocolID, payload, config.maxMessageSize)
		if err != nil {
			stats.recordError(err)
			continue
		}
		requestContext, cancel := context.WithTimeout(context.Background(), config.requestTimeout)
		response, err := host.Request(requestContext, targetPeerID, request)
		cancel()
		if err != nil {
			stats.failed.Add(1)
			stats.recordError(err)
			continue
		}
		if !samePayload(payload, response.Payload) {
			stats.failed.Add(1)
			stats.recordError(errors.New("payload mismatch"))
			continue
		}
		elapsed := time.Since(startedAt)
		stats.succeeded.Add(1)
		stats.bytesReceived.Add(uint64(len(response.Payload)))
		stats.recordLatency(elapsed)
	}
}

func fillPayload(payload []byte, workerID uint64, requestNumber uint64, startedAt time.Time) {
	binary.LittleEndian.PutUint64(payload[0:8], workerID)
	binary.LittleEndian.PutUint64(payload[8:16], requestNumber)
	binary.LittleEndian.PutUint64(payload[16:24], uint64(startedAt.UnixNano()))
}

func samePayload(expected []byte, actual []byte) bool {
	if len(expected) != len(actual) {
		return false
	}
	return bytes.Equal(expected, actual)
}

func maxStressPayloadBytes(maxMessageSize int) int {
	const protocolOverheadReserve = 64 * 1024
	normalizedMaxMessageSize := maxMessageSize
	if normalizedMaxMessageSize > p2p.MaxConfigurableMessageSize {
		normalizedMaxMessageSize = p2p.MaxConfigurableMessageSize
	}
	limit := normalizedMaxMessageSize - protocolOverheadReserve
	if limit < stressPayloadHeaderSize {
		return 0
	}
	return limit
}

func reportLoop(
	ctx context.Context,
	done chan<- struct{},
	stats *stressStats,
	config stressConfig,
	logger *slog.Logger,
) {
	defer close(done)
	ticker := time.NewTicker(config.reportInterval)
	defer ticker.Stop()
	var lastOK uint64
	var lastTime = time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			ok := stats.succeeded.Load()
			deltaOK := ok - lastOK
			elapsed := now.Sub(lastTime).Seconds()
			lastOK = ok
			lastTime = now
			logger.Info("stress progress",
				slog.Uint64("sent", stats.sent.Load()),
				slog.Uint64("succeeded", ok),
				slog.Uint64("failed", stats.failed.Load()),
				slog.Float64("recent_rps", float64(deltaOK)/math.Max(elapsed, 0.001)),
				slog.Any("memory", memorySnapshot()),
			)
		}
	}
}

func printSummary(stats *stressStats, config stressConfig, logger *slog.Logger) {
	elapsed := time.Since(stats.startedAt).Seconds()
	p50, p95, p99, maxLatency := stats.latencySummary()
	logger.Info("STRESS_SUMMARY",
		slog.String("protocol", string(config.protocol)),
		slog.Duration("duration", time.Since(stats.startedAt)),
		slog.Int("concurrency", config.concurrency),
		slog.Int("payload_bytes", config.payloadBytes),
		slog.Int("max_message_size", config.maxMessageSize),
		slog.Int("rate_limit", config.rateLimit),
		slog.Int("inbound_rate_limit", config.inboundRate),
		slog.Bool("warmup", config.warmup),
		slog.Uint64("sent", stats.sent.Load()),
		slog.Uint64("succeeded", stats.succeeded.Load()),
		slog.Uint64("failed", stats.failed.Load()),
		slog.Float64("rps", float64(stats.succeeded.Load())/math.Max(elapsed, 0.001)),
		slog.Duration("p50", p50),
		slog.Duration("p95", p95),
		slog.Duration("p99", p99),
		slog.Duration("max_latency", maxLatency),
		slog.Any("errors", stats.errorSnapshot()),
		slog.Any("memory", memorySnapshot()),
	)
}

func (stats *stressStats) recordLatency(elapsed time.Duration) {
	stats.latencyMutex.Lock()
	if len(stats.latenciesMicros) < maxLatencySamples {
		stats.latenciesMicros = append(stats.latenciesMicros, elapsed.Microseconds())
	}
	stats.latencyMutex.Unlock()
}

func (stats *stressStats) latencySummary() (time.Duration, time.Duration, time.Duration, time.Duration) {
	stats.latencyMutex.Lock()
	values := append([]int64(nil), stats.latenciesMicros...)
	stats.latencyMutex.Unlock()
	if len(values) == 0 {
		return 0, 0, 0, 0
	}
	sort.Slice(values, func(left int, right int) bool { return values[left] < values[right] })
	return percentile(values, 50), percentile(values, 95), percentile(values, 99), time.Duration(values[len(values)-1]) * time.Microsecond
}

func percentile(values []int64, percentileValue int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := (len(values)*percentileValue + 99) / 100
	if index <= 0 {
		index = 1
	}
	if index > len(values) {
		index = len(values)
	}
	return time.Duration(values[index-1]) * time.Microsecond
}

func (stats *stressStats) recordError(err error) {
	key := normalizeError(err)
	stats.errorMutex.Lock()
	stats.errors[key]++
	stats.errorMutex.Unlock()
}

func (stats *stressStats) errorSnapshot() map[string]uint64 {
	stats.errorMutex.Lock()
	defer stats.errorMutex.Unlock()
	snapshot := make(map[string]uint64, len(stats.errors))
	for key, value := range stats.errors {
		snapshot[key] = value
	}
	return snapshot
}

func normalizeError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "context deadline exceeded"):
		return "context deadline exceeded"
	case strings.Contains(message, "connection closed"):
		return "connection closed"
	case strings.Contains(message, "queue full"):
		return "queue full"
	case strings.Contains(message, "rate limited"):
		return "rate limited"
	case strings.Contains(message, "peer unavailable"):
		return "peer unavailable"
	default:
		if len(message) > 160 {
			return message[:160]
		}
		return message
	}
}

func memorySnapshot() map[string]uint64 {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return map[string]uint64{
		"alloc":      memory.Alloc,
		"sys":        memory.Sys,
		"heap_alloc": memory.HeapAlloc,
		"rss":        processRSSBytes(),
		"goroutines": uint64(runtime.NumGoroutine()),
	}
}

func processRSSBytes() uint64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "VmRSS:" {
			continue
		}
		kib, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			return 0
		}
		return kib * 1024
	}
	return 0
}

func parseAddresses(rawAddresses []string) []utils.MultiAddress {
	addresses := make([]utils.MultiAddress, 0, len(rawAddresses))
	for _, rawAddress := range rawAddresses {
		address, err := utils.ParseMultiAddress(rawAddress)
		if err == nil {
			addresses = append(addresses, address)
		}
	}
	return addresses
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
