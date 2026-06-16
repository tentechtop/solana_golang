package p2p

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestFaultInjectionOutOfOrderMessagesFlushInSequence(t *testing.T) {
	dispatched := make([]string, 0, 3)
	sequencer := newConnectionMessageSequencer(func(message Message) connectionStop {
		dispatched = append(dispatched, message.ID)
		return connectionStop{}
	})

	for _, sequence := range []uint64{2, 0, 1} {
		message := Message{ID: fmt.Sprintf("message-%d", sequence)}
		sequencer.dispatch(connectionParseJob{orderKey: "peer-a/stream-1", orderSeq: sequence}, message)
	}

	want := []string{"message-0", "message-1", "message-2"}
	if fmt.Sprint(dispatched) != fmt.Sprint(want) {
		t.Fatalf("dispatched = %v, want %v", dispatched, want)
	}
}

func TestFaultInjectionClosedConnectionReportsWriteError(t *testing.T) {
	base := newPriorityIsolatedBlockingConnection()
	writeErrors := make(chan error, 1)
	connection := newQueuedConnection(base, queuedConnectionConfig{
		queueSize:    4,
		writeTimeout: time.Second,
		priority: func(message Message) MessagePriority {
			return MessagePriorityLow
		},
		onError: func(_ Connection, err error) {
			writeErrors <- err
		},
	})
	defer connection.Close()

	message := Message{ID: "blocked-block", Type: ProtocolBlockV1}
	if err := connection.WriteMessage(context.Background(), message); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	select {
	case <-base.lowStartedC:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked write")
	}

	if err := base.Close(); err != nil {
		t.Fatalf("base Close() error = %v", err)
	}
	select {
	case err := <-writeErrors:
		if !errors.Is(err, ErrConnectionClosed) {
			t.Fatalf("write error = %v, want ErrConnectionClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for disconnect write error")
	}
}

func TestFaultInjectionDelayedWriteTimesOut(t *testing.T) {
	base := newParallelPriorityBlockingConnection(1)
	writeErrors := make(chan error, 1)
	connection := newQueuedConnection(base, queuedConnectionConfig{
		queueSize:    4,
		writeTimeout: 20 * time.Millisecond,
		priority: func(message Message) MessagePriority {
			return MessagePriorityNormal
		},
		concurrency: func(message Message) ProtocolConcurrencyMode {
			return ProtocolConcurrencyStateless
		},
		onError: func(_ Connection, err error) {
			writeErrors <- err
		},
	})
	defer connection.Close()
	defer base.release()

	message := Message{ID: "delayed-transaction", Type: ProtocolReceiveTransactionV1}
	if err := connection.WriteMessage(context.Background(), message); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	select {
	case <-base.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delayed write")
	}
	select {
	case err := <-writeErrors:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("write error = %v, want context deadline", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for write timeout")
	}
}
