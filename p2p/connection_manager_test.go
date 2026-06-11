package p2p

import (
	"context"
	"errors"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestHostHandleConnectionRespondsPong(t *testing.T) {
	localPeerID := testPeerID(21)
	remotePeerID := testPeerID(22)
	host, err := NewHost(HostConfig{
		PeerID:        localPeerID,
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	request, err := NewRequestMessage(remotePeerID, MessageTypePing, nil)
	if err != nil {
		t.Fatalf("NewRequestMessage() error = %v", err)
	}
	request.ToPeerID = localPeerID
	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, []Message{request})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go host.HandleConnection(ctx, connection)

	response := connection.waitWrite(t)
	if response.Type != MessageTypePong {
		t.Fatalf("response.Type = %d, want %d", response.Type, MessageTypePong)
	}
	if response.RequestID != request.ID {
		t.Fatalf("response.RequestID = %q, want %q", response.RequestID, request.ID)
	}
	if response.ToPeerID != remotePeerID {
		t.Fatalf("response.ToPeerID = %q, want %q", response.ToPeerID, remotePeerID)
	}
	if _, ok := host.ConnectionState(remotePeerID); !ok {
		t.Fatal("ConnectionState() ok = false, want true")
	}
}

func TestHostHeartbeatWritesPing(t *testing.T) {
	localPeerID := testPeerID(23)
	remotePeerID := testPeerID(24)
	host, err := NewHost(HostConfig{
		PeerID:        localPeerID,
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, nil)
	if err := host.storeConnection(remotePeerID, connection); err != nil {
		t.Fatalf("storeConnection() error = %v", err)
	}
	host.heartbeatOnce(context.Background())

	message := connection.waitWrite(t)
	if message.Type != MessageTypePing {
		t.Fatalf("message.Type = %d, want %d", message.Type, MessageTypePing)
	}
	if message.FromPeerID != localPeerID {
		t.Fatalf("message.FromPeerID = %q, want %q", message.FromPeerID, localPeerID)
	}
	if message.ToPeerID != remotePeerID {
		t.Fatalf("message.ToPeerID = %q, want %q", message.ToPeerID, remotePeerID)
	}
	state, ok := host.ConnectionState(remotePeerID)
	if !ok {
		t.Fatal("ConnectionState() ok = false, want true")
	}
	if state.LastHeartbeatUnixMilli == 0 {
		t.Fatal("LastHeartbeatUnixMilli = 0, want heartbeat timestamp")
	}
}

func TestHostHeartbeatClosesExpiredConnection(t *testing.T) {
	localPeerID := testPeerID(25)
	remotePeerID := testPeerID(26)
	host, err := NewHost(HostConfig{
		PeerID:         localPeerID,
		AllowInsecure:  true,
		ConnectionIdle: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, nil)
	if err := host.storeConnection(remotePeerID, connection); err != nil {
		t.Fatalf("storeConnection() error = %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	host.heartbeatOnce(context.Background())

	if _, ok := host.Connection(remotePeerID); ok {
		t.Fatal("Connection() ok = true, want expired connection removed")
	}
	if !connection.closed {
		t.Fatal("connection.closed = false, want true")
	}
}

func TestHostRejectsConnectionsOverLimit(t *testing.T) {
	host, err := NewHost(HostConfig{
		PeerID:         testPeerID(27),
		AllowInsecure:  true,
		MaxConnections: 1,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	firstPeerID := testPeerID(28)
	secondPeerID := testPeerID(29)
	firstConnection := newScriptedConnection(utils.ProtocolTCP, firstPeerID, nil)
	secondConnection := newScriptedConnection(utils.ProtocolTCP, secondPeerID, nil)

	if err := host.storeConnection(firstPeerID, firstConnection); err != nil {
		t.Fatalf("storeConnection(first) error = %v", err)
	}
	if err := host.storeConnection(secondPeerID, secondConnection); !errors.Is(err, ErrMaxConnectionsReached) {
		t.Fatalf("storeConnection(second) error = %v, want ErrMaxConnectionsReached", err)
	}
}

func TestHostRejectsSpoofedConnectionPeer(t *testing.T) {
	localPeerID := testPeerID(30)
	remotePeerID := testPeerID(31)
	spoofedPeerID := testPeerID(32)
	host, err := NewHost(HostConfig{PeerID: localPeerID, AllowInsecure: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	message, err := NewMessage(MessageTypeTransaction, nil)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	message.FromPeerID = spoofedPeerID
	message.ToPeerID = localPeerID
	if err := message.Validate(DefaultMaxMessageSize); err != nil {
		t.Fatalf("message.Validate() error = %v", err)
	}
	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, []Message{message})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	host.HandleConnection(ctx, connection)

	if _, ok := host.Connection(spoofedPeerID); ok {
		t.Fatal("spoofed peer connection stored, want rejection")
	}
}

func TestHostRequestIgnoresInterleavedPing(t *testing.T) {
	localPeerID := testPeerID(33)
	remotePeerID := testPeerID(34)
	host, err := NewHost(HostConfig{PeerID: localPeerID, AllowInsecure: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	connection := newResponsiveConnection(remotePeerID, localPeerID)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go host.HandleConnection(ctx, connection)

	requestPayload, err := NewKADFindNodeRequest(localPeerID, 1)
	if err != nil {
		t.Fatalf("NewKADFindNodeRequest() error = %v", err)
	}
	payload, err := requestPayload.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	request, err := NewRequestMessage(localPeerID, ProtocolFindNodeRequestV1, payload)
	if err != nil {
		t.Fatalf("NewRequestMessage() error = %v", err)
	}
	request.ToPeerID = remotePeerID

	response, err := host.requestOnConnection(ctx, connection, remotePeerID, request)
	if err != nil {
		t.Fatalf("requestOnConnection() error = %v", err)
	}
	if response.Type != ProtocolFindNodeResponseV1 {
		t.Fatalf("response.Type = %d, want %d", response.Type, ProtocolFindNodeResponseV1)
	}
	if response.RequestID != request.ID {
		t.Fatalf("response.RequestID = %q, want %q", response.RequestID, request.ID)
	}

	pong := connection.waitWrite(t)
	if pong.Type != MessageTypePong {
		t.Fatalf("interleaved response Type = %d, want pong", pong.Type)
	}
}

func TestRequestManagerRequiresPeerAndResponseType(t *testing.T) {
	localPeerID := testPeerID(38)
	remotePeerID := testPeerID(39)
	otherPeerID := testPeerID(40)
	manager := newRequestManager()
	request, err := NewRequestMessage(localPeerID, ProtocolFindNodeRequestV1, nil)
	if err != nil {
		t.Fatalf("NewRequestMessage() error = %v", err)
	}

	waiter, unregister, err := manager.register(request.ID, remotePeerID, ProtocolFindNodeResponseV1, true)
	if err != nil {
		t.Fatalf("register() error = %v", err)
	}
	defer unregister()

	wrongPeerResponse, err := NewResponseMessage(otherPeerID, ProtocolFindNodeResponseV1, request.ID, nil)
	if err != nil {
		t.Fatalf("NewResponseMessage(wrong peer) error = %v", err)
	}
	if manager.fulfill(wrongPeerResponse) {
		t.Fatal("fulfill(wrong peer) = true, want false")
	}
	assertNoRequestResponse(t, waiter)

	wrongTypeResponse, err := NewResponseMessage(remotePeerID, ProtocolIdentifyResponseV1, request.ID, nil)
	if err != nil {
		t.Fatalf("NewResponseMessage(wrong type) error = %v", err)
	}
	if manager.fulfill(wrongTypeResponse) {
		t.Fatal("fulfill(wrong type) = true, want false")
	}
	assertNoRequestResponse(t, waiter)

	expectedResponse, err := NewResponseMessage(remotePeerID, ProtocolFindNodeResponseV1, request.ID, nil)
	if err != nil {
		t.Fatalf("NewResponseMessage(expected) error = %v", err)
	}
	if !manager.fulfill(expectedResponse) {
		t.Fatal("fulfill(expected) = false, want true")
	}
	select {
	case response := <-waiter:
		if response.ID != expectedResponse.ID {
			t.Fatalf("response.ID = %q, want %q", response.ID, expectedResponse.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for expected response")
	}
}

type scriptedConnection struct {
	protocol     utils.MultiAddressProtocol
	remotePeerID string
	reads        chan Message
	writes       chan Message
	closed       bool
}

type responsiveConnection struct {
	*scriptedConnection
	localPeerID string
}

func newResponsiveConnection(remotePeerID string, localPeerID string) *responsiveConnection {
	return &responsiveConnection{
		scriptedConnection: newScriptedConnection(utils.ProtocolTCP, remotePeerID, nil),
		localPeerID:        localPeerID,
	}
}

func (connection *responsiveConnection) WriteMessage(ctx context.Context, message Message) error {
	if message.Type != ProtocolFindNodeRequestV1 {
		return connection.scriptedConnection.WriteMessage(ctx, message)
	}

	ping, err := NewRequestMessage(connection.remotePeerID, MessageTypePing, nil)
	if err != nil {
		return err
	}
	ping.ToPeerID = connection.localPeerID
	connection.reads <- ping

	responsePayload, err := NewKADFindNodeResponse(connection.localPeerID, nil)
	if err != nil {
		return err
	}
	payload, err := responsePayload.MarshalBinary()
	if err != nil {
		return err
	}
	response, err := NewResponseMessage(connection.remotePeerID, ProtocolFindNodeResponseV1, message.ID, payload)
	if err != nil {
		return err
	}
	response.ToPeerID = connection.localPeerID
	connection.reads <- response
	return nil
}

func newScriptedConnection(protocol utils.MultiAddressProtocol, remotePeerID string, reads []Message) *scriptedConnection {
	connection := &scriptedConnection{
		protocol:     protocol,
		remotePeerID: remotePeerID,
		reads:        make(chan Message, len(reads)),
		writes:       make(chan Message, 8),
	}
	for _, message := range reads {
		connection.reads <- message
	}
	return connection
}

func (connection *scriptedConnection) ID() string {
	return "scripted-" + connection.remotePeerID
}

func (connection *scriptedConnection) Protocol() utils.MultiAddressProtocol {
	return connection.protocol
}

func (connection *scriptedConnection) RemotePeerID() string {
	return connection.remotePeerID
}

func (connection *scriptedConnection) LocalAddress() string {
	return "127.0.0.1:1000"
}

func (connection *scriptedConnection) RemoteAddress() string {
	return "127.0.0.1:1001"
}

func (connection *scriptedConnection) ReadMessage(ctx context.Context) (Message, error) {
	select {
	case message := <-connection.reads:
		return message, nil
	case <-ctx.Done():
		return Message{}, ctx.Err()
	case <-time.After(time.Second):
		return Message{}, errors.New("scripted read timeout")
	}
}

func (connection *scriptedConnection) WriteMessage(ctx context.Context, message Message) error {
	select {
	case connection.writes <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (connection *scriptedConnection) Close() error {
	connection.closed = true
	return nil
}

func (connection *scriptedConnection) waitWrite(t *testing.T) Message {
	t.Helper()
	select {
	case message := <-connection.writes:
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for written message")
		return Message{}
	}
}

func assertNoRequestResponse(t *testing.T, waiter <-chan Message) {
	t.Helper()
	select {
	case response := <-waiter:
		t.Fatalf("unexpected response fulfilled: %+v", response)
	default:
	}
}
