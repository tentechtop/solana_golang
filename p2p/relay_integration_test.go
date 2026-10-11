package p2p

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/utils"
)

func startRelayTestHost(t *testing.T, relay bool) (*Host, utils.MultiAddress) {
	t.Helper()
	identity := testRelayIdentity(t)
	address := testAddress(t, utils.ProtocolQUIC, freeUDPPort(t), identity.PeerID)
	var capabilities PeerCapability
	if relay {
		capabilities = PeerCapabilityRelay
	}
	host, err := NewHost(HostConfig{PeerID: identity.PeerID, SecureIdentity: identity, EnableSecureSession: true,
		Capabilities: capabilities, AdvertisedAddresses: []utils.MultiAddress{address}, Relay: RelayConfig{RequireSignature: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go host.Listen(ctx, address, host.HandleConnection)
	transport, _ := host.transport(utils.ProtocolQUIC)
	quicTransport := transport.(*QUICTransport)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		quicTransport.mutex.Lock()
		ready := len(quicTransport.listeners) > 0
		quicTransport.mutex.Unlock()
		if ready {
			return host, address
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("QUIC listener did not start")
	return nil, utils.MultiAddress{}
}

func connectRelayTestHost(t *testing.T, host *Host, address utils.MultiAddress) {
	t.Helper()
	peer, err := NewPeer(address.PeerID, []utils.MultiAddress{address})
	if err != nil {
		t.Fatal(err)
	}
	peer.Capabilities = PeerCapabilityRelay
	if err := host.AddPeer(peer); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := host.DialPeer(ctx, address.PeerID); err != nil {
		t.Fatal(err)
	}
}

func TestQUICRelayRoundTripAndHolePunch(t *testing.T) {
	relay, relayAddress := startRelayTestHost(t, true)
	a, aAddress := startRelayTestHost(t, false)
	b, bAddress := startRelayTestHost(t, false)
	const protocol ProtocolID = 100022
	for _, host := range []*Host{a, b} {
		local := host
		if err := host.RegisterResultHandler(ProtocolSpec{ID: protocol, Name: "/test/relay/echo/1", HasResponse: true, Concurrency: ProtocolConcurrencyStateless}, func(ctx context.Context, msg Message) (Message, error) {
			response, err := NewResponseMessage(local.peerID, protocol, msg.ID, msg.Payload)
			response.ToPeerID = msg.FromPeerID
			return response, err
		}); err != nil {
			t.Fatal(err)
		}
		connectRelayTestHost(t, host, relayAddress)
	}
	for _, address := range []utils.MultiAddress{aAddress, bAddress} {
		state, ok := relay.ConnectionState(address.PeerID)
		if !ok {
			t.Fatal("relay missing reverse connection")
		}
		_, port, err := net.SplitHostPort(state.ObservedRemoteAddress)
		if err != nil || port != strconv.Itoa(address.Port) {
			t.Fatalf("relay observed %q; want listener port %d", state.ObservedRemoteAddress, address.Port)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			source, target := a, b
			if i%2 == 1 {
				source, target = b, a
			}
			payload := bytes.Repeat([]byte{byte(i)}, 32768)
			request, err := NewRequestMessage(source.peerID, protocol, payload)
			if err != nil {
				failures <- err
				return
			}
			response, err := source.RequestRelay(ctx, relay.peerID, target.peerID, request)
			if err == nil && !bytes.Equal(payload, response.Payload) {
				err = errors.New("corrupt relay response")
			}
			if err != nil {
				failures <- err
			}
		}(i)
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if a.ConnectionCount() != 1 || b.ConnectionCount() != 1 {
		t.Fatal("relay test established an unexpected direct connection")
	}
	a.recordPeerDialFailure(b.peerID)
	backoffRequest, _ := NewRequestMessage(a.peerID, protocol, []byte("relay after failed direct dial"))
	if _, err := a.RequestRelay(ctx, relay.peerID, b.peerID, backoffRequest); err != nil {
		t.Fatalf("relay request during direct backoff: %v", err)
	}
	a.recordPeerProtectionSuccess(b.peerID)
	connection, err := a.PunchQUICPeer(ctx, relay.peerID, b.peerID)
	if err != nil {
		t.Fatal(err)
	}
	if connection.RemoteAddress() != net.JoinHostPort(bAddress.IPAddress, strconv.Itoa(bAddress.Port)) {
		t.Fatalf("unexpected punched remote %s", connection.RemoteAddress())
	}
	// Repeated coordination must reuse the existing read loop and connection.
	for i := 0; i < 8; i++ {
		if _, err := a.PunchQUICPeer(ctx, relay.peerID, b.peerID); err != nil {
			t.Fatal(err)
		}
		request, _ := NewRequestMessage(a.peerID, protocol, []byte(fmt.Sprint(i)))
		if _, err := a.Request(ctx, b.peerID, request); err != nil {
			t.Fatal(err)
		}
	}
	// Simultaneous dialing can replace a connection while its asynchronous
	// identify request is pending; allow that bounded request timeout to drain.
	settle := time.Now().Add(6 * time.Second)
	for time.Now().Before(settle) && (a.Metrics().PendingRequests != 0 || b.Metrics().PendingRequests != 0) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.Metrics().PendingRequests != 0 || b.Metrics().PendingRequests != 0 {
		t.Fatal("pending requests leaked")
	}
	request, _ := NewRequestMessage(a.peerID, protocol, []byte("cancel"))
	canceled, done := context.WithCancel(ctx)
	done()
	if _, err := a.RequestRelay(canceled, relay.peerID, b.peerID, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled relay error=%v", err)
	}
	if a.Metrics().PendingRequests != 0 {
		t.Fatal("canceled relay request leaked")
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close active QUIC connection: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("repeat QUIC close: %v", err)
	}
}

func TestHolePunchRejectsUntrustedIntroducer(t *testing.T) {
	host, address := startRelayTestHost(t, false)
	source := testPeerID(150)
	payload := QUICHolePunchMessage{Version: QUICHolePunchVersion, Kind: quicHolePunchKindIntroduce, SessionID: testMessageID(t),
		SourcePeerID: source, TargetPeerID: host.peerID, RelayPeerID: testPeerID(151), ObservedAddress: address.String(),
		CreatedAtUnixMilli: time.Now().UnixMilli(), StartAtUnixMilli: time.Now().Add(time.Second).UnixMilli(), ExpiresAtUnixMilli: time.Now().Add(2 * time.Second).UnixMilli()}
	encoded, err := payload.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	message, _ := NewMessage(ProtocolQUICHolePunchV1, encoded)
	future := payload
	future.CreatedAtUnixMilli += time.Hour.Milliseconds()
	future.StartAtUnixMilli += time.Hour.Milliseconds()
	future.ExpiresAtUnixMilli += time.Hour.Milliseconds()
	if err := future.Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatal("accepted far-future hole punch timer")
	}
	message.FromPeerID = source
	if err := host.handleQUICHolePunchMessage(context.Background(), message); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("untrusted introduce error=%v", err)
	}
}

func TestRelayRejectedMessageDoesNotPoisonRoute(t *testing.T) {
	host, _ := startRelayTestHost(t, true)
	// Secure hosts must authenticate relayed sources even with default relay config.
	host.relay = newRelayService(RelayConfig{})
	source := testRelayIdentity(t)
	envelope := testRelayEnvelope(t, source.PeerID, host.peerID, ProtocolPingV1, []byte("unsigned"))
	data, err := envelope.MarshalBinary(defaultRelayPayloadSize)
	if err != nil {
		t.Fatal(err)
	}
	message, _ := NewMessage(ProtocolRelayMessageV1, data)
	message.FromPeerID = testPeerID(152)
	if err := host.handleRelayMessage(context.Background(), message); !errors.Is(err, ErrSecureSession) {
		t.Fatalf("unsigned envelope error=%v", err)
	}
	if _, ok := host.relay.nextHop(source.PeerID); ok {
		t.Fatal("unauthenticated envelope installed a return route")
	}
}

func TestRetiredConnectionCleanupKeepsReplacement(t *testing.T) {
	host, err := NewHost(HostConfig{PeerID: testPeerID(153), AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	peerID := testPeerID(154)
	old := &testIdentifiedConnection{scriptedConnection: newScriptedConnection(utils.ProtocolTCP, peerID, nil), connectionID: "retired"}
	current := &testIdentifiedConnection{scriptedConnection: newScriptedConnection(utils.ProtocolTCP, peerID, nil), connectionID: "current"}
	setHostConnectionForTest(host, peerID, current)
	// A delayed successful write can reintroduce the retired connection index.
	host.markConnectionWrite(old, peerID)
	host.removeConnectionByID(old.ID())
	host.closePeerConnectionIfID(peerID, old.ID())
	if got, ok := host.Connection(peerID); !ok || got.ID() != current.ID() {
		t.Fatal("retired connection removed its replacement")
	}
}
