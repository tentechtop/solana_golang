package p2p

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

const (
	defaultConnectionParseWorkers = 4
	maxConnectionParseWorkers     = 64
	defaultConnectionParseQueue   = 64
	maxConnectionParseQueue       = 4096
)

type connectionParseJob struct {
	message  Message
	orderKey string
	orderSeq uint64
}

type connectionStop struct {
	err    error
	record bool
	stop   bool
}

type connectionOrderState struct {
	next     uint64
	pending  map[uint64]Message
	flushing bool
}

type connectionMessageSequencer struct {
	mutex           sync.Mutex
	states          map[string]*connectionOrderState
	dispatchMessage func(Message) connectionStop
}

// HandleConnection 管理连接读循环 + 自动处理心跳并分发业务协议。
func (host *Host) HandleConnection(ctx context.Context, connection Connection) {
	if connection == nil {
		return
	}
	connection = host.connectionWriterFor(connection)
	defer host.removeConnectionByID(connection.ID())
	defer connection.Close()
	if ctx == nil {
		ctx = host.lifecycleContext
	}
	host.handleConnectionMessages(ctx, connection)
}

func (host *Host) handleConnectionMessages(ctx context.Context, connection Connection) {
	parseContext, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan connectionParseJob, host.connectionParseQueueSize())
	stops := make(chan connectionStop, 1)
	sequencer := newConnectionMessageSequencer(func(message Message) connectionStop {
		return host.dispatchParsedConnectionMessage(parseContext, connection, message)
	})

	var workers sync.WaitGroup
	for workerID := 0; workerID < host.connectionParseWorkerCount(); workerID++ {
		workers.Add(1)
		go host.connectionParseWorker(parseContext, &workers, connection, jobs, stops, sequencer)
	}

	readDone := make(chan connectionStop, 1)
	go func() {
		readDone <- host.connectionReadLoop(parseContext, connection, jobs)
	}()

	select {
	case stop := <-stops:
		if stop.record && stop.err != nil {
			host.recordConnectionError(connection, stop.err)
		}
	case readStop := <-readDone:
		workers.Wait()
		select {
		case stop := <-stops:
			if stop.record && stop.err != nil {
				host.recordConnectionError(connection, stop.err)
			}
		default:
			if readStop.record && readStop.err != nil {
				host.recordConnectionError(connection, readStop.err)
			}
		}
		return
	case <-ctx.Done():
	}
	cancel()
	_ = connection.Close()
	<-readDone
	workers.Wait()
}

func (host *Host) connectionReadLoop(ctx context.Context, connection Connection, jobs chan<- connectionParseJob) connectionStop {
	defer close(jobs)
	orderSequences := make(map[string]uint64)
	for {
		readContext, cancelRead := host.connectionReadContext(ctx, connection)
		message, err := host.readConnectionMessage(readContext, connection)
		cancelRead()
		if err != nil {
			return connectionStop{err: err, record: true, stop: true}
		}

		job := connectionParseJob{message: message}
		if orderKey := host.connectionOrderKey(message); orderKey != "" {
			job.orderKey = orderKey
			job.orderSeq = orderSequences[orderKey]
			orderSequences[orderKey]++
		}
		select {
		case jobs <- job:
		case <-ctx.Done():
			return connectionStop{}
		}
	}
}

func (host *Host) connectionParseWorker(
	ctx context.Context,
	workers *sync.WaitGroup,
	connection Connection,
	jobs <-chan connectionParseJob,
	stops chan<- connectionStop,
	sequencer *connectionMessageSequencer,
) {
	defer workers.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-jobs:
			if !ok {
				return
			}
			message, err := host.parseConnectionMessage(connection, job.message)
			if err != nil {
				sendConnectionStop(stops, connectionStop{err: err, record: true, stop: true})
				return
			}
			stop := sequencer.dispatch(job, message)
			if stop.stop {
				sendConnectionStop(stops, stop)
				return
			}
		}
	}
}

func (host *Host) readConnectionMessage(ctx context.Context, connection Connection) (Message, error) {
	secureConnection, ok := unwrapSecureConnection(connection)
	if ok {
		return secureConnection.ReadEncryptedMessage(ctx)
	}
	return connection.ReadMessage(ctx)
}

func (host *Host) parseConnectionMessage(connection Connection, message Message) (Message, error) {
	secureConnection, ok := unwrapSecureConnection(connection)
	if ok {
		return secureConnection.OpenMessage(message)
	}
	if err := message.Validate(host.maxMessageSize); err != nil {
		return Message{}, err
	}
	return message, nil
}

func (host *Host) dispatchParsedConnectionMessage(ctx context.Context, connection Connection, message Message) connectionStop {
	host.metrics.messagesRead.Add(1)
	if err := host.validateConnectionMessage(connection, message); err != nil {
		host.metrics.messagesRejected.Add(1)
		host.logger.Warn("p2p message peer mismatch",
			slog.String("connection_id", connection.ID()),
			slog.String("from_peer_id", message.FromPeerID),
			slog.String("remote_peer_id", connection.RemotePeerID()),
			slog.Any("error", err),
		)
		return connectionStop{err: err, record: true, stop: true}
	}
	if err := host.acceptInboundMessage(message); err != nil {
		host.metrics.messagesRejected.Add(1)
		if peerProtectionErrorClosesConnection(err) {
			return connectionStop{err: err, stop: true}
		}
		return connectionStop{}
	}
	if err := host.markConnectionRead(connection, message.FromPeerID); err != nil {
		host.metrics.messagesRejected.Add(1)
		host.logger.Warn("p2p connection rejected",
			slog.String("connection_id", connection.ID()),
			slog.String("peer_id", message.FromPeerID),
			slog.Any("error", err),
		)
		return connectionStop{err: err, record: true, stop: true}
	}
	if host.handleHeartbeatMessage(ctx, connection, message) {
		return connectionStop{}
	}
	if host.requests.fulfill(message) {
		return connectionStop{}
	}
	if message.IsResponse() {
		host.logger.Debug("p2p unmatched response dropped",
			slog.String("connection_id", connection.ID()),
			slog.String("message_id", message.ID),
			slog.String("request_id", message.RequestID),
			slog.Uint64("protocol_id", uint64(message.Type)),
		)
		return connectionStop{}
	}
	if err := host.enqueueProtocolMessage(connection, message); err != nil {
		host.metrics.messagesRejected.Add(1)
		host.logger.Warn("p2p message rejected",
			slog.String("connection_id", connection.ID()),
			slog.String("message_id", message.ID),
			slog.Any("error", err),
		)
	}
	return connectionStop{}
}

func (host *Host) connectionOrderKey(message Message) string {
	if host.messageConcurrency(message) == ProtocolConcurrencyStateless {
		return ""
	}
	return fmt.Sprintf("%s/%d", message.FromPeerID, message.Type)
}

func (host *Host) connectionParseWorkerCount() int {
	workerCount := defaultConnectionParseWorkers
	if host.protocolDispatcher != nil && host.protocolDispatcher.config.WorkerCount > 0 {
		workerCount = host.protocolDispatcher.config.WorkerCount
	}
	if workerCount > maxConnectionParseWorkers {
		return maxConnectionParseWorkers
	}
	if workerCount < 1 {
		return 1
	}
	return workerCount
}

func (host *Host) connectionParseQueueSize() int {
	queueSize := host.connectionParseWorkerCount() * 64
	if queueSize < defaultConnectionParseQueue {
		return defaultConnectionParseQueue
	}
	if queueSize > maxConnectionParseQueue {
		return maxConnectionParseQueue
	}
	return queueSize
}

func newConnectionMessageSequencer(dispatch func(Message) connectionStop) *connectionMessageSequencer {
	return &connectionMessageSequencer{
		states:          make(map[string]*connectionOrderState),
		dispatchMessage: dispatch,
	}
}

func (sequencer *connectionMessageSequencer) dispatch(job connectionParseJob, message Message) connectionStop {
	if sequencer == nil || job.orderKey == "" {
		return sequencer.dispatchDirect(message)
	}
	return sequencer.dispatchOrdered(job.orderKey, job.orderSeq, message)
}

func (sequencer *connectionMessageSequencer) dispatchDirect(message Message) connectionStop {
	if sequencer == nil || sequencer.dispatchMessage == nil {
		return connectionStop{}
	}
	return sequencer.dispatchMessage(message)
}

func (sequencer *connectionMessageSequencer) dispatchOrdered(orderKey string, orderSeq uint64, message Message) connectionStop {
	sequencer.mutex.Lock()
	state := sequencer.state(orderKey)
	state.pending[orderSeq] = message
	if state.flushing {
		sequencer.mutex.Unlock()
		return connectionStop{}
	}
	state.flushing = true
	sequencer.mutex.Unlock()

	for {
		ready := sequencer.takeReady(orderKey)
		if len(ready) == 0 {
			sequencer.mutex.Lock()
			state = sequencer.state(orderKey)
			state.flushing = false
			sequencer.mutex.Unlock()
			return connectionStop{}
		}
		for _, readyMessage := range ready {
			if stop := sequencer.dispatchDirect(readyMessage); stop.stop {
				return stop
			}
		}
	}
}

func (sequencer *connectionMessageSequencer) state(orderKey string) *connectionOrderState {
	state := sequencer.states[orderKey]
	if state != nil {
		return state
	}
	state = &connectionOrderState{pending: make(map[uint64]Message)}
	sequencer.states[orderKey] = state
	return state
}

func (sequencer *connectionMessageSequencer) takeReady(orderKey string) []Message {
	sequencer.mutex.Lock()
	defer sequencer.mutex.Unlock()
	state := sequencer.state(orderKey)
	ready := make([]Message, 0)
	for {
		message, ok := state.pending[state.next]
		if !ok {
			return ready
		}
		delete(state.pending, state.next)
		state.next++
		ready = append(ready, message)
	}
}

func sendConnectionStop(stops chan<- connectionStop, stop connectionStop) {
	if !stop.stop && stop.err == nil {
		return
	}
	select {
	case stops <- stop:
	default:
	}
}

// connectionReadContext 限制未识别连接首帧读取时间 + 防止慢连接长期占用 goroutine 和文件描述符。
func (host *Host) connectionReadContext(ctx context.Context, connection Connection) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = host.lifecycleContext
	}
	if connection != nil && connection.RemotePeerID() != "" {
		return context.WithCancel(ctx)
	}
	if connection != nil {
		if _, ok := host.peerIDByConnectionID(connection.ID()); ok {
			return context.WithCancel(ctx)
		}
	}
	return host.withHandshakeTimeout(ctx)
}

func (host *Host) validateConnectionMessage(connection Connection, message Message) error {
	if message.FromPeerID == "" {
		return fmt.Errorf("%w: empty message sender", ErrInvalidMessage)
	}
	remotePeerID := connection.RemotePeerID()
	if remotePeerID != "" && message.FromPeerID != "" && message.FromPeerID != remotePeerID {
		return fmt.Errorf("%w: message sender does not match connection peer", ErrInvalidMessage)
	}
	if message.ToPeerID != "" && message.ToPeerID != host.peerID {
		return fmt.Errorf("%w: message target does not match local peer", ErrInvalidMessage)
	}
	return nil
}
