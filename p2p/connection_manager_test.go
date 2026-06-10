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
		PeerID: localPeerID,
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
		PeerID: localPeerID,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, nil)
	host.storeConnection(remotePeerID, connection)
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
		ConnectionIdle: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, nil)
	host.storeConnection(remotePeerID, connection)
	time.Sleep(2 * time.Millisecond)
	host.heartbeatOnce(context.Background())

	if _, ok := host.Connection(remotePeerID); ok {
		t.Fatal("Connection() ok = true, want expired connection removed")
	}
	if !connection.closed {
		t.Fatal("connection.closed = false, want true")
	}
}

type scriptedConnection struct {
	protocol     utils.MultiAddressProtocol
	remotePeerID string
	reads        chan Message
	writes       chan Message
	closed       bool
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
