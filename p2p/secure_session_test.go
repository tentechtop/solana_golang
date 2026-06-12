package p2p

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestSecureSessionHandshakeDerivesMatchingSession(t *testing.T) {
	initiatorIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.0")
	responderIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.1")
	initiatorState, responderState := testSecureSessionStates(t, initiatorIdentity, responderIdentity)

	initiatorSession, err := initiatorState.Finalize(responderState.Handshake(), responderIdentity.PeerID)
	if err != nil {
		t.Fatalf("initiator Finalize() error = %v", err)
	}
	responderSession, err := responderState.Finalize(initiatorState.Handshake(), initiatorIdentity.PeerID)
	if err != nil {
		t.Fatalf("responder Finalize() error = %v", err)
	}

	if !bytes.Equal(initiatorSession.SessionID(), responderSession.SessionID()) {
		t.Fatal("SessionID() mismatch")
	}
	if initiatorSession.NetworkID() != "localnet" {
		t.Fatalf("NetworkID() = %q, want localnet", initiatorSession.NetworkID())
	}
	if initiatorSession.ProtocolVersion() != MessageProtocolVersion {
		t.Fatalf("ProtocolVersion() = %d, want %d", initiatorSession.ProtocolVersion(), MessageProtocolVersion)
	}
	if initiatorSession.RemoteSoftwareVersion() != responderIdentity.SoftwareVersion {
		t.Fatalf("RemoteSoftwareVersion() = %q, want %q", initiatorSession.RemoteSoftwareVersion(), responderIdentity.SoftwareVersion)
	}
}

func TestSecureSessionEncryptDecrypt(t *testing.T) {
	initiatorSession, responderSession := testSecureSessionPair(t, "localnet")

	payload, err := initiatorSession.Seal([]byte("secret payload"), []byte("aad"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	plaintext, err := responderSession.Open(payload, []byte("aad"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(plaintext, []byte("secret payload")) {
		t.Fatalf("plaintext = %q, want secret payload", plaintext)
	}
}

func TestSecureSessionRejectsTamperedCiphertext(t *testing.T) {
	initiatorSession, responderSession := testSecureSessionPair(t, "localnet")

	payload, err := initiatorSession.Seal([]byte("secret payload"), []byte("aad"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	payload.Ciphertext[0] ^= 0xff

	if _, err := responderSession.Open(payload, []byte("aad")); !errors.Is(err, ErrSecureSession) {
		t.Fatalf("Open(tampered) error = %v, want ErrSecureSession", err)
	}
}

func TestSecureSessionRejectsReplay(t *testing.T) {
	initiatorSession, responderSession := testSecureSessionPair(t, "localnet")

	payload, err := initiatorSession.Seal([]byte("secret payload"), []byte("aad"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if _, err := responderSession.Open(payload, []byte("aad")); err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	if _, err := responderSession.Open(payload, []byte("aad")); !errors.Is(err, ErrSecureSession) {
		t.Fatalf("Open(replay) error = %v, want ErrSecureSession", err)
	}
}

func TestSecureSessionAcceptsOutOfOrderWithinReplayWindow(t *testing.T) {
	initiatorSession, responderSession := testSecureSessionPair(t, "localnet")

	firstPayload, err := initiatorSession.Seal([]byte("first"), []byte("aad-1"))
	if err != nil {
		t.Fatalf("Seal(first) error = %v", err)
	}
	secondPayload, err := initiatorSession.Seal([]byte("second"), []byte("aad-2"))
	if err != nil {
		t.Fatalf("Seal(second) error = %v", err)
	}
	secondPlaintext, err := responderSession.Open(secondPayload, []byte("aad-2"))
	if err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	if !bytes.Equal(secondPlaintext, []byte("second")) {
		t.Fatalf("second plaintext = %q, want second", secondPlaintext)
	}
	firstPlaintext, err := responderSession.Open(firstPayload, []byte("aad-1"))
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	if !bytes.Equal(firstPlaintext, []byte("first")) {
		t.Fatalf("first plaintext = %q, want first", firstPlaintext)
	}
	if _, err := responderSession.Open(secondPayload, []byte("aad-2")); !errors.Is(err, ErrSecureSession) {
		t.Fatalf("Open(second replay) error = %v, want ErrSecureSession", err)
	}
}

func TestSecureSessionRejectsSequenceOutsideReplayWindow(t *testing.T) {
	initiatorSession, responderSession := testSecureSessionPair(t, "localnet")

	payload, err := initiatorSession.Seal([]byte("payload"), []byte("aad"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	payload.Sequence = secureSessionReplayWindow + 1
	if _, err := responderSession.Open(payload, []byte("aad")); !errors.Is(err, ErrSecureSession) {
		t.Fatalf("Open(outside window) error = %v, want ErrSecureSession", err)
	}
}

func TestSecureSessionRejectsNetworkMismatch(t *testing.T) {
	initiatorIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.0")
	responderIdentity := testSecureSessionIdentity(t, "othernet", "node/1.0.0")
	initiatorState, responderState := testSecureSessionStates(t, initiatorIdentity, responderIdentity)

	_, err := initiatorState.Finalize(responderState.Handshake(), responderIdentity.PeerID)
	if !errors.Is(err, ErrSecureSession) {
		t.Fatalf("Finalize(network mismatch) error = %v, want ErrSecureSession", err)
	}
}

func TestSecureConnectionHandshakeAndEncryptedMessage(t *testing.T) {
	clientIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.0")
	serverIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.1")
	clientRaw, serverRaw := newSecureSessionTestConnectionPair(clientIdentity.PeerID, serverIdentity.PeerID)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	serverResult := make(chan secureConnectionResult, 1)
	go func() {
		connection, err := SecureAcceptConnection(ctx, serverRaw, serverIdentity)
		serverResult <- secureConnectionResult{connection: connection, err: err}
	}()

	clientSecure, err := SecureDialConnection(ctx, clientRaw, clientIdentity)
	if err != nil {
		t.Fatalf("SecureDialConnection() error = %v", err)
	}
	result := <-serverResult
	if result.err != nil {
		t.Fatalf("SecureAcceptConnection() error = %v", result.err)
	}
	serverSecure := result.connection

	message, err := NewMessage(ProtocolPingV1, []byte("encrypted ping"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	message.FromPeerID = clientIdentity.PeerID
	message.ToPeerID = serverIdentity.PeerID
	if err := clientSecure.WriteMessage(ctx, message); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	received, err := serverSecure.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if !bytes.Equal(received.Payload, []byte("encrypted ping")) {
		t.Fatalf("received payload = %q, want encrypted ping", received.Payload)
	}
}

func TestHostSecureHandlerStoresConnectionStateAndTicket(t *testing.T) {
	clientIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.0")
	serverIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.1")
	host, err := NewHost(HostConfig{
		PeerID:              serverIdentity.PeerID,
		SecureIdentity:      serverIdentity,
		EnableSecureSession: true,
		PreferredProtocols:  []utils.MultiAddressProtocol{utils.ProtocolTCP},
		HeartbeatInterval:   time.Hour,
		ConnectionIdle:      time.Hour,
		MaxPeerFailures:     3,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	clientRaw, serverRaw := newSecureSessionTestConnectionPair(clientIdentity.PeerID, serverIdentity.PeerID)
	handledConnection := make(chan Connection, 1)
	handler := host.secureConnectionHandler(func(ctx context.Context, connection Connection) {
		handledConnection <- connection
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go handler(ctx, serverRaw)

	clientSecure, err := SecureDialConnection(ctx, clientRaw, clientIdentity)
	if err != nil {
		t.Fatalf("SecureDialConnection() error = %v", err)
	}
	defer clientSecure.Close()

	select {
	case connection := <-handledConnection:
		if _, ok := connection.(*SecureConnection); !ok {
			t.Fatalf("handler connection type = %T, want *SecureConnection", connection)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for secure handler")
	}

	state, ok := host.ConnectionState(clientIdentity.PeerID)
	if !ok {
		t.Fatal("ConnectionState() ok = false, want true")
	}
	if !state.Encrypted {
		t.Fatal("ConnectionState.Encrypted = false, want true")
	}
	if state.NetworkID != "localnet" {
		t.Fatalf("ConnectionState.NetworkID = %q, want localnet", state.NetworkID)
	}
	if state.RemoteSoftwareVersion != clientIdentity.SoftwareVersion {
		t.Fatalf("RemoteSoftwareVersion = %q, want %q", state.RemoteSoftwareVersion, clientIdentity.SoftwareVersion)
	}

	ticket, ok := host.SecureSessionTicket(clientIdentity.PeerID)
	if !ok {
		t.Fatal("SecureSessionTicket() ok = false, want true")
	}
	if err := ticket.Validate(); err != nil {
		t.Fatalf("ticket.Validate() error = %v", err)
	}
}

func TestHostSecureHandlerTimesOutIdleHandshake(t *testing.T) {
	serverIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.1")
	host, err := NewHost(HostConfig{
		PeerID:              serverIdentity.PeerID,
		SecureIdentity:      serverIdentity,
		EnableSecureSession: true,
		HandshakeTimeout:    10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	connection := newScriptedConnection(utils.ProtocolTCP, "", nil)
	handledConnection := make(chan Connection, 1)
	handler := host.secureConnectionHandler(func(ctx context.Context, connection Connection) {
		handledConnection <- connection
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	handler(ctx, connection)

	select {
	case <-handledConnection:
		t.Fatal("handler was called, want idle handshake rejection")
	default:
	}
	if !connection.closed {
		t.Fatal("connection.closed = false, want timeout close")
	}
}

type secureConnectionResult struct {
	connection *SecureConnection
	err        error
}

func testSecureSessionPair(t *testing.T, networkID string) (*SecureSession, *SecureSession) {
	t.Helper()
	initiatorIdentity := testSecureSessionIdentity(t, networkID, "node/1.0.0")
	responderIdentity := testSecureSessionIdentity(t, networkID, "node/1.0.1")
	initiatorState, responderState := testSecureSessionStates(t, initiatorIdentity, responderIdentity)

	initiatorSession, err := initiatorState.Finalize(responderState.Handshake(), responderIdentity.PeerID)
	if err != nil {
		t.Fatalf("initiator Finalize() error = %v", err)
	}
	responderSession, err := responderState.Finalize(initiatorState.Handshake(), initiatorIdentity.PeerID)
	if err != nil {
		t.Fatalf("responder Finalize() error = %v", err)
	}
	return initiatorSession, responderSession
}

func testSecureSessionStates(t *testing.T, initiatorIdentity SecureSessionIdentity, responderIdentity SecureSessionIdentity) (*SecureSessionState, *SecureSessionState) {
	t.Helper()
	initiatorState, err := NewSecureSessionState(initiatorIdentity, SecureSessionRoleInitiator)
	if err != nil {
		t.Fatalf("NewSecureSessionState(initiator) error = %v", err)
	}
	responderState, err := NewSecureSessionState(responderIdentity, SecureSessionRoleResponder)
	if err != nil {
		t.Fatalf("NewSecureSessionState(responder) error = %v", err)
	}
	return initiatorState, responderState
}

func testSecureSessionIdentity(t *testing.T, networkID string, softwareVersion string) SecureSessionIdentity {
	t.Helper()
	keyPair, err := utils.GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPair() error = %v", err)
	}
	return SecureSessionIdentity{
		PeerID:             utils.Base58Encode(keyPair.PublicKey),
		PublicKey:          keyPair.PublicKey,
		PrivateKey:         keyPair.PrivateKey,
		NetworkID:          networkID,
		SoftwareVersion:    softwareVersion,
		MinProtocolVersion: MessageProtocolVersion,
		MaxProtocolVersion: MessageProtocolVersion,
	}
}

type secureSessionTestConnection struct {
	id           string
	protocol     utils.MultiAddressProtocol
	remotePeerID string
	inbound      chan Message
	outbound     chan Message
	closed       chan struct{}
}

func newSecureSessionTestConnectionPair(clientPeerID string, serverPeerID string) (*secureSessionTestConnection, *secureSessionTestConnection) {
	clientInbound := make(chan Message, 8)
	serverInbound := make(chan Message, 8)
	client := &secureSessionTestConnection{
		id:           "client",
		protocol:     utils.ProtocolTCP,
		remotePeerID: serverPeerID,
		inbound:      clientInbound,
		outbound:     serverInbound,
		closed:       make(chan struct{}),
	}
	server := &secureSessionTestConnection{
		id:           "server",
		protocol:     utils.ProtocolTCP,
		remotePeerID: clientPeerID,
		inbound:      serverInbound,
		outbound:     clientInbound,
		closed:       make(chan struct{}),
	}
	return client, server
}

func (connection *secureSessionTestConnection) ID() string {
	return connection.id
}

func (connection *secureSessionTestConnection) Protocol() utils.MultiAddressProtocol {
	return connection.protocol
}

func (connection *secureSessionTestConnection) RemotePeerID() string {
	return connection.remotePeerID
}

func (connection *secureSessionTestConnection) LocalAddress() string {
	return "127.0.0.1:1000"
}

func (connection *secureSessionTestConnection) RemoteAddress() string {
	return "127.0.0.1:1001"
}

func (connection *secureSessionTestConnection) ReadMessage(ctx context.Context) (Message, error) {
	select {
	case message := <-connection.inbound:
		return message, nil
	case <-connection.closed:
		return Message{}, ErrConnectionClosed
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

func (connection *secureSessionTestConnection) WriteMessage(ctx context.Context, message Message) error {
	select {
	case connection.outbound <- message:
		return nil
	case <-connection.closed:
		return ErrConnectionClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (connection *secureSessionTestConnection) Close() error {
	select {
	case <-connection.closed:
	default:
		close(connection.closed)
	}
	return nil
}
