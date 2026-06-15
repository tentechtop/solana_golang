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

func TestQueuedConnectionUsesSamePriorityParallelWriters(t *testing.T) {
	base := newParallelPriorityBlockingConnection(4)
	connection := newQueuedConnection(base, queuedConnectionConfig{
		queueSize:    12,
		writeTimeout: time.Second,
		priority: func(message Message) MessagePriority {
			return MessagePriorityNormal
		},
		concurrency: func(message Message) ProtocolConcurrencyMode {
			return ProtocolConcurrencyStateless
		},
	})
	defer connection.Close()
	defer base.release()

	for index := 0; index < 4; index++ {
		message := Message{Type: ProtocolReceiveTransactionV1}
		if err := connection.WriteMessage(context.Background(), message); err != nil {
			t.Fatalf("WriteMessage(%d) error = %v", index, err)
		}
	}

	for index := 0; index < 2; index++ {
		select {
		case <-base.started:
		case <-time.After(time.Second):
			t.Fatal("same-priority write did not start in parallel")
		}
	}
}

func TestQueuedConnectionKeepsOrderedSamePriorityWritesSerial(t *testing.T) {
	base := newParallelPriorityBlockingConnection(4)
	connection := newQueuedConnection(base, queuedConnectionConfig{
		queueSize:    12,
		writeTimeout: time.Second,
		priority: func(message Message) MessagePriority {
			return MessagePriorityNormal
		},
		concurrency: func(message Message) ProtocolConcurrencyMode {
			return ProtocolConcurrencyOrdered
		},
	})
	defer connection.Close()
	defer base.release()

	for index := 0; index < 2; index++ {
		message := Message{Type: ProtocolReceiveTransactionV1}
		if err := connection.WriteMessage(context.Background(), message); err != nil {
			t.Fatalf("WriteMessage(%d) error = %v", index, err)
		}
	}

	select {
	case <-base.started:
	case <-time.After(time.Second):
		t.Fatal("first ordered write did not start")
	}
	select {
	case <-base.started:
		t.Fatal("second ordered write started before first completed")
	case <-time.After(100 * time.Millisecond):
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

type parallelPriorityBlockingConnection struct {
	parallelism int
	started     chan struct{}
	releaseC    chan struct{}
	closed      chan struct{}
	releaseOnce sync.Once
}

func newParallelPriorityBlockingConnection(parallelism int) *parallelPriorityBlockingConnection {
	return &parallelPriorityBlockingConnection{
		parallelism: parallelism,
		started:     make(chan struct{}, parallelism),
		releaseC:    make(chan struct{}),
		closed:      make(chan struct{}),
	}
}

func (connection *parallelPriorityBlockingConnection) ID() string {
	return "parallel-priority-blocking"
}

func (connection *parallelPriorityBlockingConnection) Protocol() utils.MultiAddressProtocol {
	return utils.ProtocolQUIC
}

func (connection *parallelPriorityBlockingConnection) RemotePeerID() string {
	return ""
}

func (connection *parallelPriorityBlockingConnection) LocalAddress() string {
	return "127.0.0.1:2000"
}

func (connection *parallelPriorityBlockingConnection) RemoteAddress() string {
	return "127.0.0.1:2001"
}

func (connection *parallelPriorityBlockingConnection) ReadMessage(ctx context.Context) (Message, error) {
	return Message{}, ErrConnectionClosed
}

func (connection *parallelPriorityBlockingConnection) WriteMessage(ctx context.Context, message Message) error {
	select {
	case connection.started <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-connection.closed:
		return ErrConnectionClosed
	}
	select {
	case <-connection.releaseC:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-connection.closed:
		return ErrConnectionClosed
	}
}

func (connection *parallelPriorityBlockingConnection) PriorityIsolatedWrites() bool {
	return true
}

func (connection *parallelPriorityBlockingConnection) PriorityWriteParallelism() int {
	return connection.parallelism
}

func (connection *parallelPriorityBlockingConnection) Close() error {
	select {
	case <-connection.closed:
	default:
		close(connection.closed)
	}
	return nil
}

func (connection *parallelPriorityBlockingConnection) release() {
	connection.releaseOnce.Do(func() {
		close(connection.releaseC)
	})
}
