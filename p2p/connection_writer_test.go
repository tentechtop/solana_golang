package p2p

import (
	"context"
	"sync"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestQueuedConnectionUsesIndependentPriorityWriters(t *testing.T) {
	base := newPriorityIsolatedBlockingConnection()
	connection := newQueuedConnection(base, queuedConnectionConfig{
		queueSize:    8,
		writeTimeout: time.Second,
		priority: func(message Message) MessagePriority {
			return defaultProtocolPriority(message.Type)
		},
	})
	defer connection.Close()
	defer base.releaseLow()

	lowMessage := Message{Type: ProtocolBlockV1}
	highMessage := Message{Type: ProtocolHotStuffVoteV1}
	if err := connection.WriteMessage(context.Background(), lowMessage); err != nil {
		t.Fatalf("WriteMessage(low) error = %v", err)
	}
	select {
	case <-base.lowStartedC:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for low priority write")
	}

	if err := connection.WriteMessage(context.Background(), highMessage); err != nil {
		t.Fatalf("WriteMessage(high) error = %v", err)
	}
	select {
	case protocolID := <-base.writes:
		if protocolID != ProtocolHotStuffVoteV1 {
			t.Fatalf("first completed write = %d, want high priority protocol", protocolID)
		}
	case <-time.After(time.Second):
		t.Fatal("high priority write was blocked by low priority write")
	}

	base.releaseLow()
	select {
	case protocolID := <-base.writes:
		if protocolID != ProtocolBlockV1 {
			t.Fatalf("second completed write = %d, want low priority protocol", protocolID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for released low priority write")
	}
}

type priorityIsolatedBlockingConnection struct {
	lowStartedOnce sync.Once
	releaseOnce    sync.Once
	writes         chan ProtocolID
	lowStartedC    chan struct{}
	releaseLowC    chan struct{}
	closed         chan struct{}
}

func newPriorityIsolatedBlockingConnection() *priorityIsolatedBlockingConnection {
	return &priorityIsolatedBlockingConnection{
		writes:      make(chan ProtocolID, 4),
		lowStartedC: make(chan struct{}),
		releaseLowC: make(chan struct{}),
		closed:      make(chan struct{}),
	}
}

func (connection *priorityIsolatedBlockingConnection) ID() string {
	return "priority-isolated-blocking"
}

func (connection *priorityIsolatedBlockingConnection) Protocol() utils.MultiAddressProtocol {
	return utils.ProtocolQUIC
}

func (connection *priorityIsolatedBlockingConnection) RemotePeerID() string {
	return ""
}

func (connection *priorityIsolatedBlockingConnection) LocalAddress() string {
	return "127.0.0.1:1000"
}

func (connection *priorityIsolatedBlockingConnection) RemoteAddress() string {
	return "127.0.0.1:1001"
}

func (connection *priorityIsolatedBlockingConnection) ReadMessage(ctx context.Context) (Message, error) {
	return Message{}, ErrConnectionClosed
}

func (connection *priorityIsolatedBlockingConnection) WriteMessage(ctx context.Context, message Message) error {
	if message.Type == ProtocolBlockV1 {
		connection.lowStartedOnce.Do(func() {
			close(connection.lowStartedC)
		})
		select {
		case <-connection.releaseLowC:
		case <-ctx.Done():
			return ctx.Err()
		case <-connection.closed:
			return ErrConnectionClosed
		}
	}
	select {
	case connection.writes <- message.Type:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-connection.closed:
		return ErrConnectionClosed
	}
}

func (connection *priorityIsolatedBlockingConnection) PriorityIsolatedWrites() bool {
	return true
}

func (connection *priorityIsolatedBlockingConnection) Close() error {
	select {
	case <-connection.closed:
	default:
		close(connection.closed)
	}
	return nil
}

func (connection *priorityIsolatedBlockingConnection) releaseLow() {
	connection.releaseOnce.Do(func() {
		close(connection.releaseLowC)
	})
}
