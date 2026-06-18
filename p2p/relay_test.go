package p2p

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestRelayMessageSignedRoundTrip(t *testing.T) {
	identity := testRelayIdentity(t)
	host, err := NewHost(HostConfig{
		PeerID:              identity.PeerID,
		SecureIdentity:      identity,
		EnableSecureSession: true,
		Relay:               RelayConfig{RequireSignature: true},
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	targetPeerID := testPeerID(101)
	innerMessage, err := NewMessage(ProtocolPingV1, []byte("relay"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	relayMessage, err := host.newRelayMessage(targetPeerID, innerMessage, "")
	if err != nil {
		t.Fatalf("newRelayMessage() error = %v", err)
	}

	encoded, err := relayMessage.MarshalBinary(defaultRelayPayloadSize)
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalRelayMessageBinary(encoded, defaultRelayPayloadSize)
	if err != nil {
		t.Fatalf("UnmarshalRelayMessageBinary() error = %v", err)
	}
	if err := decoded.Validate(RelayConfig{RequireSignature: true}); err != nil {
		t.Fatalf("Validate(require signature) error = %v", err)
	}
	if decoded.SourcePeerID != identity.PeerID || decoded.TargetPeerID != targetPeerID {
		t.Fatalf("relay route = %s -> %s, want %s -> %s", decoded.SourcePeerID, decoded.TargetPeerID, identity.PeerID, targetPeerID)
	}
}

func TestRelayMessageRejectsTamperedPayload(t *testing.T) {
	identity := testRelayIdentity(t)
	host, err := NewHost(HostConfig{
		PeerID:              identity.PeerID,
		SecureIdentity:      identity,
		EnableSecureSession: true,
		Relay:               RelayConfig{RequireSignature: true},
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	innerMessage, err := NewMessage(ProtocolPingV1, []byte("relay"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	relayMessage, err := host.newRelayMessage(testPeerID(102), innerMessage, "")
	if err != nil {
		t.Fatalf("newRelayMessage() error = %v", err)
	}
	relayMessage.Payload[len(relayMessage.Payload)-1] ^= 1

	if err := relayMessage.Validate(RelayConfig{RequireSignature: true}); !errors.Is(err, ErrSecureSession) {
		t.Fatalf("Validate(tampered) error = %v, want ErrSecureSession", err)
	}
}

func TestRelayServiceRejectsDuplicateAndExpiredTTL(t *testing.T) {
	service := newRelayService(RelayConfig{})
	message := testRelayEnvelope(t, testPeerID(103), testPeerID(104), ProtocolPingV1, []byte("relay"))

	if err := service.accept(message); err != nil {
		t.Fatalf("accept(first) error = %v", err)
	}
	if err := service.accept(message); !errors.Is(err, ErrDuplicateMessage) {
		t.Fatalf("accept(duplicate) error = %v, want ErrDuplicateMessage", err)
	}

	expired := message
	expired.ID = testMessageID(t)
	expired.TTL = 0
	if err := service.accept(expired); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("accept(expired ttl) error = %v, want ErrInvalidMessage", err)
	}
}

func TestHostForwardRelayMessageUsesRegisteredRoute(t *testing.T) {
	sourcePeerID := testPeerID(105)
	relayPeerID := testPeerID(106)
	targetPeerID := testPeerID(107)
	host, err := NewHost(HostConfig{PeerID: relayPeerID, AllowInsecure: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	nextHopConnection := newScriptedConnection(utils.ProtocolTCP, targetPeerID, nil)
	setHostConnectionForTest(host, targetPeerID, nextHopConnection)

	relayMessage := testRelayEnvelope(t, sourcePeerID, targetPeerID, ProtocolPingV1, []byte("relay"))
	relayMessage.PreviousHopPeerID = sourcePeerID
	if err := host.forwardRelayMessage(context.Background(), relayMessage); err != nil {
		t.Fatalf("forwardRelayMessage() error = %v", err)
	}

	written := nextHopConnection.waitWrite(t)
	if written.Type != ProtocolRelayMessageV1 {
		t.Fatalf("written type = %d, want relay", written.Type)
	}
	forwarded, err := UnmarshalRelayMessageBinary(written.Payload, defaultRelayPayloadSize)
	if err != nil {
		t.Fatalf("UnmarshalRelayMessageBinary() error = %v", err)
	}
	if forwarded.TTL != relayMessage.TTL-1 {
		t.Fatalf("forwarded ttl = %d, want %d", forwarded.TTL, relayMessage.TTL-1)
	}
}

func TestHostSendRelayMessageUsesConnectedRelayPeer(t *testing.T) {
	sourcePeerID := testPeerID(130)
	relayPeerID := testPeerID(131)
	targetPeerID := testPeerID(132)
	host, err := NewHost(HostConfig{PeerID: sourcePeerID, AllowInsecure: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	relayPeer := mustTestPeer(t, relayPeerID, utils.ProtocolQUIC, 5131)
	relayPeer.Capabilities = PeerCapabilityRelay
	if err := host.AddPeer(relayPeer); err != nil {
		t.Fatalf("AddPeer(relay) error = %v", err)
	}
	relayConnection := newScriptedConnection(utils.ProtocolQUIC, relayPeerID, nil)
	setHostConnectionForTest(host, relayPeerID, relayConnection)

	message, err := NewMessage(ProtocolPingV1, []byte("relay fallback"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	if err := host.SendRelayMessage(context.Background(), targetPeerID, message); err != nil {
		t.Fatalf("SendRelayMessage() error = %v", err)
	}

	written := relayConnection.waitWrite(t)
	if written.Type != ProtocolRelayMessageV1 {
		t.Fatalf("written type = %d, want relay", written.Type)
	}
	relayMessage, err := UnmarshalRelayMessageBinary(written.Payload, defaultRelayPayloadSize)
	if err != nil {
		t.Fatalf("UnmarshalRelayMessageBinary() error = %v", err)
	}
	if relayMessage.SourcePeerID != sourcePeerID || relayMessage.TargetPeerID != targetPeerID {
		t.Fatalf("relay route = %s -> %s, want %s -> %s", relayMessage.SourcePeerID, relayMessage.TargetPeerID, sourcePeerID, targetPeerID)
	}
}

func TestHostSendFallsBackToConnectedRelayPeer(t *testing.T) {
	sourcePeerID := testPeerID(133)
	relayPeerID := testPeerID(134)
	targetPeerID := testPeerID(135)
	host, err := NewHost(HostConfig{PeerID: sourcePeerID, AllowInsecure: true, DialTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	targetPeer := mustTestPeer(t, targetPeerID, utils.ProtocolTCP, 5135)
	if err := host.AddPeer(targetPeer); err != nil {
		t.Fatalf("AddPeer(target) error = %v", err)
	}
	relayPeer := mustTestPeer(t, relayPeerID, utils.ProtocolTCP, 5134)
	relayPeer.Capabilities = PeerCapabilityRelay
	if err := host.AddPeer(relayPeer); err != nil {
		t.Fatalf("AddPeer(relay) error = %v", err)
	}
	relayConnection := newScriptedConnection(utils.ProtocolTCP, relayPeerID, nil)
	setHostConnectionForTest(host, relayPeerID, relayConnection)

	message, err := NewMessage(ProtocolPingV1, []byte("auto relay fallback"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	if err := host.Send(context.Background(), targetPeerID, message); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	written := relayConnection.waitWrite(t)
	if written.Type != ProtocolRelayMessageV1 {
		t.Fatalf("written type = %d, want relay", written.Type)
	}
}

func TestHostConsumesRelayMessage(t *testing.T) {
	sourcePeerID := testPeerID(108)
	targetPeerID := testPeerID(109)
	host, err := NewHost(HostConfig{PeerID: targetPeerID, AllowInsecure: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	received := make(chan Message, 1)
	spec := ProtocolSpec{
		ID:          ProtocolID(1001),
		Name:        "/test/relay/consume/1.0.0",
		HasResponse: false,
		Priority:    MessagePriorityNormal,
		Concurrency: ProtocolConcurrencyStateless,
	}
	if err := host.RegisterVoidHandler(spec, func(ctx context.Context, message Message) error {
		received <- message
		return nil
	}); err != nil {
		t.Fatalf("RegisterVoidHandler() error = %v", err)
	}

	relayMessage := testRelayEnvelope(t, sourcePeerID, targetPeerID, spec.ID, []byte("relay"))
	if err := host.consumeRelayMessage(context.Background(), relayMessage); err != nil {
		t.Fatalf("consumeRelayMessage() error = %v", err)
	}

	select {
	case message := <-received:
		if message.FromPeerID != sourcePeerID || message.ToPeerID != targetPeerID {
			t.Fatalf("inner route = %s -> %s, want %s -> %s", message.FromPeerID, message.ToPeerID, sourcePeerID, targetPeerID)
		}
		if !bytes.Equal(message.Payload, []byte("relay")) {
			t.Fatalf("inner payload = %q, want relay", message.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for consumed relay message")
	}
}

func testRelayIdentity(t *testing.T) SecureSessionIdentity {
	t.Helper()
	keyPair, err := utils.GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPair() error = %v", err)
	}
	return SecureSessionIdentity{
		PeerID:          utils.Base58Encode(keyPair.PublicKey),
		PublicKey:       keyPair.PublicKey,
		PrivateKey:      keyPair.PrivateKey,
		NetworkID:       "testnet",
		SoftwareVersion: "test",
	}
}

func testRelayEnvelope(t *testing.T, sourcePeerID string, targetPeerID string, protocolID ProtocolID, payload []byte) RelayMessage {
	t.Helper()
	innerMessage, err := NewMessage(protocolID, payload)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	innerMessage.FromPeerID = sourcePeerID
	innerMessage.ToPeerID = targetPeerID
	encodedInnerMessage, err := innerMessage.MarshalBinary(DefaultMaxMessageSize)
	if err != nil {
		t.Fatalf("MarshalBinary(inner) error = %v", err)
	}
	relayMessage := RelayMessage{
		Version:            RelayMessageVersion,
		ID:                 innerMessage.ID,
		SourcePeerID:       sourcePeerID,
		TargetPeerID:       targetPeerID,
		TTL:                defaultRelayMaxTTL,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
		ProtocolID:         protocolID,
		Payload:            encodedInnerMessage,
	}
	if err := relayMessage.Validate(RelayConfig{}); err != nil {
		t.Fatalf("Validate(relay) error = %v", err)
	}
	return relayMessage
}

func testMessageID(t *testing.T) string {
	t.Helper()
	messageID, err := newMessageID()
	if err != nil {
		t.Fatalf("newMessageID() error = %v", err)
	}
	return messageID
}
