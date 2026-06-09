package p2p

import (
	"bytes"
	"context"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestHostSendFallsBackFromQUICToTCP(t *testing.T) {
	clientPeerID := testPeerID(5)
	serverPeerID := testPeerID(6)
	tcpAddress := testAddress(t, utils.ProtocolTCP, freeTCPPort(t), serverPeerID)
	quicAddress := testAddress(t, utils.ProtocolQUIC, tcpAddress.Port, serverPeerID)

	serverHost, err := NewHost(HostConfig{PeerID: serverPeerID}, NewTCPTransport())
	if err != nil {
		t.Fatalf("NewHost(server) error = %v", err)
	}
	clientHost, err := NewHost(HostConfig{PeerID: clientPeerID})
	if err != nil {
		t.Fatalf("NewHost(client) error = %v", err)
	}
	defer serverHost.Close()
	defer clientHost.Close()

	peer, err := NewPeer(serverPeerID, []utils.MultiAddress{quicAddress, tcpAddress})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	if err := clientHost.AddPeer(peer); err != nil {
		t.Fatalf("AddPeer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan Message, 1)
	listenErrors := make(chan error, 1)
	go func() {
		listenErrors <- serverHost.Listen(ctx, tcpAddress, func(ctx context.Context, connection Connection) {
			defer connection.Close()
			readContext, readCancel := context.WithTimeout(context.Background(), time.Second)
			defer readCancel()
			message, err := connection.ReadMessage(readContext)
			if err == nil {
				received <- message
			}
		})
	}()
	waitForTCP(t, tcpAddress.Port)

	message := Message{Type: MessageTypePing, Payload: []byte("host")}
	sendContext, sendCancel := context.WithTimeout(context.Background(), time.Second)
	defer sendCancel()
	if err := clientHost.Send(sendContext, serverPeerID, message); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	select {
	case decoded := <-received:
		if decoded.FromPeerID != clientPeerID {
			t.Fatalf("FromPeerID = %q, want %q", decoded.FromPeerID, clientPeerID)
		}
		if decoded.ToPeerID != serverPeerID {
			t.Fatalf("ToPeerID = %q, want %q", decoded.ToPeerID, serverPeerID)
		}
		if !bytes.Equal(decoded.Payload, []byte("host")) {
			t.Fatalf("Payload = %q, want host", decoded.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for host message")
	}

	connection, ok := clientHost.Connection(serverPeerID)
	if !ok {
		t.Fatal("Connection() ok = false, want true")
	}
	if connection.Protocol() != utils.ProtocolTCP {
		t.Fatalf("Protocol = %q, want %q", connection.Protocol(), utils.ProtocolTCP)
	}

	cancel()
	if err := <-listenErrors; err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
}
