package p2p

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"solana_golang/utils"
)

const (
	defaultWriteQueueSize = 1024
	maxWriteQueueSize     = 16384
	defaultWriteTimeout   = 5 * time.Second
)

// AsyncWriteConfig 保存异步写配置 + 通过有界队列隔离业务 worker 和网络写阻塞。
type AsyncWriteConfig struct {
	QueueSize    int
	WriteTimeout time.Duration
}

type queuedConnectionConfig struct {
	queueSize    int
	writeTimeout time.Duration
	metrics      *p2pMetrics
	logger       *slog.Logger
	priority     func(Message) MessagePriority
	onWrite      func(Connection, Message)
	onError      func(Connection, error)
}

type queuedConnection struct {
	inner        Connection
	highWrites   chan queuedWrite
	normalWrites chan queuedWrite
	lowWrites    chan queuedWrite
	done         chan struct{}
	closeOnce    sync.Once
	closed       atomic.Bool
	closeErr     error
	writeTimeout time.Duration
	metrics      *p2pMetrics
	logger       *slog.Logger
	priority     func(Message) MessagePriority
	onWrite      func(Connection, Message)
	onError      func(Connection, error)
}

type queuedWrite struct {
	message    Message
	enqueuedAt time.Time
}

func newQueuedConnection(inner Connection, config queuedConnectionConfig) Connection {
	if inner == nil {
		return nil
	}
	if _, ok := inner.(*queuedConnection); ok {
		return inner
	}
	queueSize := normalizeWriteQueueSize(config.queueSize)
	highQueueSize, normalQueueSize, lowQueueSize := splitWriteQueueSize(queueSize)
	connection := &queuedConnection{
		inner:        inner,
		highWrites:   make(chan queuedWrite, highQueueSize),
		normalWrites: make(chan queuedWrite, normalQueueSize),
		lowWrites:    make(chan queuedWrite, lowQueueSize),
		done:         make(chan struct{}),
		writeTimeout: normalizeWriteTimeout(config.writeTimeout),
		metrics:      config.metrics,
		logger:       normalizeLogger(config.logger),
		priority:     config.priority,
		onWrite:      config.onWrite,
		onError:      config.onError,
	}
	go connection.writeLoop()
	return connection
}

func normalizeWriteQueueSize(size int) int {
	if size <= 0 {
		return defaultWriteQueueSize
	}
	if size > maxWriteQueueSize {
		return maxWriteQueueSize
	}
	return size
}

func normalizeWriteTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultWriteTimeout
	}
	return timeout
}

func (connection *queuedConnection) ID() string {
	return connection.inner.ID()
}

func (connection *queuedConnection) Protocol() utils.MultiAddressProtocol {
	return connection.inner.Protocol()
}

func (connection *queuedConnection) RemotePeerID() string {
	return connection.inner.RemotePeerID()
}

func (connection *queuedConnection) LocalAddress() string {
	return connection.inner.LocalAddress()
}

func (connection *queuedConnection) RemoteAddress() string {
	return connection.inner.RemoteAddress()
}

func (connection *queuedConnection) ReadMessage(ctx context.Context) (Message, error) {
	return connection.inner.ReadMessage(ctx)
}

// WriteMessage 写入异步队列 + 消息入队后由连接 writer 独占加密和网络写入。
func (connection *queuedConnection) WriteMessage(ctx context.Context, message Message) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if connection.closed.Load() {
		return ErrConnectionClosed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-connection.done:
		return ErrConnectionClosed
	default:
	}

	write := queuedWrite{
		message:    message,
		enqueuedAt: time.Now(),
	}
	queue := connection.queue(connection.messagePriority(message))
	if connection.closed.Load() {
		return ErrConnectionClosed
	}
	select {
	case queue <- write:
		if connection.closed.Load() {
			return ErrConnectionClosed
		}
		connection.addMetric(func(metrics *p2pMetrics) {
			metrics.writeQueueEnqueued.Add(1)
		})
		return nil
	case <-connection.done:
		return ErrConnectionClosed
	default:
		connection.addMetric(func(metrics *p2pMetrics) {
			metrics.writeQueueDropped.Add(1)
		})
		return fmt.Errorf("%w: connection %s", ErrWriteQueueFull, connection.ID())
	}
}

func splitWriteQueueSize(totalSize int) (int, int, int) {
	totalSize = normalizeWriteQueueSize(totalSize)
	if totalSize == 1 {
		return 0, 1, 0
	}
	if totalSize == 2 {
		return 1, 1, 0
	}
	highQueueSize := maxInt(1, totalSize/4)
	lowQueueSize := maxInt(1, totalSize/4)
	normalQueueSize := totalSize - highQueueSize - lowQueueSize
	if normalQueueSize < 1 {
		normalQueueSize = 1
		lowQueueSize = totalSize - highQueueSize - normalQueueSize
	}
	return highQueueSize, normalQueueSize, lowQueueSize
}

func (connection *queuedConnection) Close() error {
	connection.closeOnce.Do(func() {
		connection.closed.Store(true)
		close(connection.done)
		connection.closeErr = connection.inner.Close()
	})
	return connection.closeErr
}

func (connection *queuedConnection) queueDepth() uint64 {
	return uint64(len(connection.highWrites) + len(connection.normalWrites) + len(connection.lowWrites))
}

func (connection *queuedConnection) writeLoop() {
	for {
		write, ok := connection.nextWrite()
		if !ok {
			return
		}
		connection.flush(write)
	}
}

func (connection *queuedConnection) nextWrite() (queuedWrite, bool) {
	for {
		select {
		case <-connection.done:
			return queuedWrite{}, false
		default:
		}
		if write, ok := tryReadQueuedWrite(connection.highWrites); ok {
			return write, true
		}
		if write, ok := tryReadQueuedWrite(connection.normalWrites); ok {
			return write, true
		}
		if write, ok := tryReadQueuedWrite(connection.lowWrites); ok {
			return write, true
		}
		select {
		case <-connection.done:
			return queuedWrite{}, false
		case write := <-connection.highWrites:
			return write, true
		case write := <-connection.normalWrites:
			return write, true
		case write := <-connection.lowWrites:
			return write, true
		}
	}
}

func tryReadQueuedWrite(queue chan queuedWrite) (queuedWrite, bool) {
	select {
	case write := <-queue:
		return write, true
	default:
		return queuedWrite{}, false
	}
}

func (connection *queuedConnection) queue(priority MessagePriority) chan queuedWrite {
	switch priority {
	case MessagePriorityHigh:
		return connection.highWrites
	case MessagePriorityLow:
		return connection.lowWrites
	default:
		return connection.normalWrites
	}
}

func (connection *queuedConnection) messagePriority(message Message) MessagePriority {
	if connection.priority == nil {
		return MessagePriorityNormal
	}
	return connection.priority(message)
}

func (connection *queuedConnection) flush(write queuedWrite) {
	ctx, cancel := connection.writeContext()
	defer cancel()

	if err := connection.inner.WriteMessage(ctx, write.message); err != nil {
		connection.recordWriteError(err)
		return
	}
	if connection.onWrite != nil {
		connection.onWrite(connection, write.message)
	}
	connection.addMetric(func(metrics *p2pMetrics) {
		metrics.writeQueueFlushed.Add(1)
	})
	if delay := time.Since(write.enqueuedAt); delay > connection.writeTimeout {
		connection.logger.Warn("p2p write queue delay",
			slog.String("connection_id", connection.ID()),
			slog.String("peer_id", write.message.ToPeerID),
			slog.String("message_id", write.message.ID),
			slog.Duration("delay", delay),
		)
	}
}

func (connection *queuedConnection) writeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), connection.writeTimeout)
}

func (connection *queuedConnection) recordWriteError(err error) {
	connection.addMetric(func(metrics *p2pMetrics) {
		metrics.writeQueueErrors.Add(1)
	})
	if connection.onError != nil {
		connection.onError(connection, err)
	}
	if isExpectedConnectionClose(err) {
		return
	}
	connection.logger.Warn("p2p async write failed",
		slog.String("connection_id", connection.ID()),
		slog.String("peer_id", connection.RemotePeerID()),
		slog.Any("error", err),
	)
}

func (connection *queuedConnection) addMetric(add func(*p2pMetrics)) {
	if connection.metrics == nil || add == nil {
		return
	}
	add(connection.metrics)
}
