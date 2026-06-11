package p2p

import (
	"bytes"
	"context"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestQUICTransportSendMessage(t *testing.T) {
	peerID := testPeerID(7)
	address := testAddress(t, utils.ProtocolQUIC, freeUDPPort(t), peerID)
	serverTransport := newInsecureQUICTransportForTest(t)
	clientTransport := newInsecureQUICTransportForTest(t)
	defer serverTransport.Close()
	defer clientTransport.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan Message, 1)
	handlerErrors := make(chan error, 1)
	listenErrors := make(chan error, 1)
	go func() {
		listenErrors <- serverTransport.Listen(ctx, address, func(ctx context.Context, connection Connection) {
			defer connection.Close()
			readContext, readCancel := context.WithTimeout(context.Background(), time.Second)
			defer readCancel()
			message, err := connection.ReadMessage(readContext)
			if err != nil {
				handlerErrors <- err
				return
			}
			received <- message
		})
	}()

	connection := dialQUICEventually(t, clientTransport, address)
	defer connection.Close()

	dialContext, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	message, err := NewMessage(ProtocolPingV1, []byte("quic"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	if err := connection.WriteMessage(dialContext, message); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	select {
	case err := <-handlerErrors:
		t.Fatalf("handler error = %v", err)
	case decoded := <-received:
		if !bytes.Equal(decoded.Payload, []byte("quic")) {
			t.Fatalf("Payload = %q, want quic", decoded.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}

	cancel()
	if err := <-listenErrors; err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
}

func TestQUICTransportUsesTemporaryTLSAndSkipsCertificateChain(t *testing.T) {
	transport, err := NewQUICTransport()
	if err != nil {
		t.Fatalf("NewQUICTransport() error = %v", err)
	}
	defer transport.Close()

	tlsConfig, err := transport.clientTLSConfig()
	if err != nil {
		t.Fatalf("clientTLSConfig() error = %v", err)
	}
	if !tlsConfig.InsecureSkipVerify {
		t.Fatal("clientTLSConfig().InsecureSkipVerify = false, want true")
	}
}

func newInsecureQUICTransportForTest(t *testing.T) *QUICTransport {
	t.Helper()
	transport, err := NewQUICTransport()
	if err != nil {
		t.Fatalf("NewQUICTransport() error = %v", err)
	}
	return transport
}

func dialQUICEventually(t *testing.T, transport *QUICTransport, address utils.MultiAddress) Connection {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		connection, err := transport.Dial(ctx, address)
		cancel()
		if err == nil {
			return connection
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Dial() error = %v", lastErr)
	return nil
}
