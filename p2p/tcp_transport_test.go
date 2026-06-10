package p2p

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestTCPTransportSendMessage(t *testing.T) {
	serverPeerID := testPeerID(4)
	address := testAddress(t, utils.ProtocolTCP, freeTCPPort(t), serverPeerID)
	serverTransport := NewTCPTransport()
	clientTransport := NewTCPTransport()
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
				if errors.Is(err, ErrConnectionClosed) {
					return
				}
				handlerErrors <- err
				return
			}
			received <- message
		})
	}()
	waitForTCP(t, address.Port)

	dialContext, dialCancel := context.WithTimeout(context.Background(), time.Second)
	defer dialCancel()
	connection, err := clientTransport.Dial(dialContext, address)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer connection.Close()

	message, err := NewMessage(MessageTypePing, []byte("tcp"))
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
		if !bytes.Equal(decoded.Payload, []byte("tcp")) {
			t.Fatalf("Payload = %q, want tcp", decoded.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}

	cancel()
	if err := <-listenErrors; err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
}
