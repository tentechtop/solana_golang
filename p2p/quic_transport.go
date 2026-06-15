package p2p

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"solana_golang/utils"
)

const (
	quicApplicationProtocol                   = "solana-golang-p2p/1"
	quicCloseCode                             = quic.ApplicationErrorCode(0)
	quicStreamCancelCode                      = quic.StreamErrorCode(0)
	defaultQUICStreamAccept                   = 5 * time.Second
	defaultQUICReadBufferBytes                = 16 * 1024 * 1024
	maxQUICReadBufferSize                     = 64
	defaultQUICInitialStreamReceiveWindow     = 1024 * 1024
	defaultQUICInitialConnectionReceiveWindow = 4 * 1024 * 1024
	defaultQUICPriorityStreamPoolSize         = 8
	maxQUICPriorityStreamPoolSize             = 32
)

// QUICTransportConfig 保存 QUIC 配置 + 支持注入 TLS、quic-go 配置和日志。
type QUICTransportConfig struct {
	MaxMessageSize                 int
	MaxPendingInbound              int
	MaxConnectionsPerIP            int
	StreamPoolSize                 int
	InitialStreamReceiveWindow     uint64
	InitialConnectionReceiveWindow uint64
	MaxStreamReceiveWindow         uint64
	MaxConnectionReceiveWindow     uint64
	MessagePriority                func(Message) MessagePriority
	MessageConcurrency             func(Message) ProtocolConcurrencyMode
	MessagePartitionKey            func(Message) string
	TLSConfig                      *tls.Config
	QUICConfig                     *quic.Config
	Logger                         *slog.Logger
}

// QUICTransport 实现 QUIC 传输 + 基于 quic-go 提供低延迟多路复用连接。
type QUICTransport struct {
	mutex               sync.Mutex
	listeners           map[string]*quic.Listener
	closed              bool
	maxMessageSize      int
	streamPoolSize      int
	inboundLimiter      *transportInboundLimiter
	tlsConfig           *tls.Config
	quicConfig          *quic.Config
	messagePriority     func(Message) MessagePriority
	messageConcurrency  func(Message) ProtocolConcurrencyMode
	messagePartitionKey func(Message) string
	logger              *slog.Logger
}

// NewQUICTransport 创建默认 QUIC 传输 + 使用临时自签证书满足 QUIC 握手要求。
func NewQUICTransport() (*QUICTransport, error) {
	return NewQUICTransportWithConfig(QUICTransportConfig{})
}

// NewQUICTransportWithConfig 创建 QUIC 传输 + 节点身份和业务加密由 SecureSession 统一保证。
func NewQUICTransportWithConfig(config QUICTransportConfig) (*QUICTransport, error) {
	tlsConfig, err := normalizeQUICTLSConfig(config)
	if err != nil {
		return nil, err
	}
	maxMessageSize := normalizeMaxMessageSize(config.MaxMessageSize)
	return &QUICTransport{
		listeners:           make(map[string]*quic.Listener),
		maxMessageSize:      maxMessageSize,
		streamPoolSize:      normalizeQUICStreamPoolSize(config.StreamPoolSize),
		inboundLimiter:      newTransportInboundLimiter(config.MaxPendingInbound, config.MaxConnectionsPerIP),
		tlsConfig:           tlsConfig,
		quicConfig:          normalizeQUICConfig(config.QUICConfig, config, maxMessageSize),
		messagePriority:     normalizeQUICMessagePriority(config.MessagePriority),
		messageConcurrency:  normalizeQUICMessageConcurrency(config.MessageConcurrency),
		messagePartitionKey: normalizeQUICMessagePartitionKey(config.MessagePartitionKey),
		logger:              utils.EnsureLogger(config.Logger),
	}, nil
}

func (transport *QUICTransport) Protocol() utils.MultiAddressProtocol {
	return utils.ProtocolQUIC
}

// Listen 监听 QUIC 地址 + 接收连接并将首个双向流交给上层处理。
func (transport *QUICTransport) Listen(ctx context.Context, address utils.MultiAddress, handler ConnectionHandler) error {
	if err := validateListenInput(address, utils.ProtocolQUIC, handler); err != nil {
		return err
	}

	listener, err := quic.ListenAddr(joinAddress(address), transport.tlsConfig.Clone(), transport.quicConfig.Clone())
	if err != nil {
		return fmt.Errorf("p2p: listen quic %s: %w", address.String(), err)
	}
	if err := transport.addListener(address.String(), listener); err != nil {
		_ = listener.Close()
		return err
	}
	defer transport.removeListener(address.String())

	transport.logger.Info("p2p quic listen",
		slog.String("address", address.String()),
		slog.String("protocol", string(address.Protocol)),
	)
	return transport.acceptLoop(ctx, listener, handler)
}

// Dial 拨号 QUIC 地址 + 建立双向流作为消息收发通道。
func (transport *QUICTransport) Dial(ctx context.Context, address utils.MultiAddress) (Connection, error) {
	if err := validateDialInput(address, utils.ProtocolQUIC); err != nil {
		return nil, err
	}

	if ctx == nil {
		ctx = context.Background()
	}
	tlsConfig, err := transport.clientTLSConfig()
	if err != nil {
		return nil, err
	}
	connection, err := quic.DialAddr(ctx, joinAddress(address), tlsConfig, transport.quicConfig.Clone())
	if err != nil {
		return nil, fmt.Errorf("p2p: dial quic %s: %w", address.String(), err)
	}

	stream, err := connection.OpenStreamSync(ctx)
	if err != nil {
		_ = connection.CloseWithError(quicCloseCode, "open stream failed")
		return nil, fmt.Errorf("p2p: open quic stream %s: %w", address.String(), err)
	}

	transport.logger.Info("p2p quic dial",
		slog.String("address", address.String()),
		slog.String("peer_id", address.PeerID),
	)
	return newQUICConnection(
		connection,
		stream,
		address.PeerID,
		transport.maxMessageSize,
		transport.streamPoolSize,
		transport.messagePriority,
		transport.messageConcurrency,
		transport.messagePartitionKey,
		transport.logger,
	), nil
}

// Close 关闭 QUIC 传输 + 释放所有监听 UDP 端口。
func (transport *QUICTransport) Close() error {
	transport.mutex.Lock()
	if transport.closed {
		transport.mutex.Unlock()
		return nil
	}
	transport.closed = true
	listeners := make([]*quic.Listener, 0, len(transport.listeners))
	for _, listener := range transport.listeners {
		listeners = append(listeners, listener)
	}
	transport.listeners = make(map[string]*quic.Listener)
	transport.mutex.Unlock()

	var closeErrors []error
	for _, listener := range listeners {
		if err := listener.Close(); err != nil &&
			!errors.Is(err, quic.ErrServerClosed) &&
			!errors.Is(err, net.ErrClosed) {
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
}

// acceptLoop 持续接收 QUIC 连接 + 每个连接独立协程等待业务双向流。
func (transport *QUICTransport) acceptLoop(ctx context.Context, listener *quic.Listener, handler ConnectionHandler) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		connection, err := listener.Accept(ctx)
		if err != nil {
			return transport.acceptError(ctx, err)
		}
		release, err := transport.inboundLimiter.acquire(connection.RemoteAddr().String())
		if err != nil {
			_ = connection.CloseWithError(quicCloseCode, "inbound limit reached")
			transport.logger.Warn("p2p quic inbound rejected",
				slog.String("remote_address", connection.RemoteAddr().String()),
				slog.Any("error", err),
			)
			continue
		}
		go func() {
			defer release()
			transport.acceptStream(ctx, connection, handler)
		}()
	}
}

// acceptStream 接收入站双向流 + 将 quic-go 连接包装为统一 Connection。
func (transport *QUICTransport) acceptStream(ctx context.Context, connection *quic.Conn, handler ConnectionHandler) {
	streamContext, cancel := context.WithTimeout(ctx, transport.streamAcceptTimeout())
	defer cancel()

	stream, err := connection.AcceptStream(streamContext)
	if err != nil {
		_ = connection.CloseWithError(quicCloseCode, "accept stream failed")
		transport.logger.Warn("p2p quic accept stream failed", slog.String("error", err.Error()))
		return
	}
	handler(ctx, newQUICConnection(
		connection,
		stream,
		"",
		transport.maxMessageSize,
		transport.streamPoolSize,
		transport.messagePriority,
		transport.messageConcurrency,
		transport.messagePartitionKey,
		transport.logger,
	))
}

// streamAcceptTimeout 限制首个业务流等待时间 + 防止 QUIC 空连接长期占用 goroutine。
func (transport *QUICTransport) streamAcceptTimeout() time.Duration {
	if transport.quicConfig != nil && transport.quicConfig.HandshakeIdleTimeout > 0 {
		return transport.quicConfig.HandshakeIdleTimeout
	}
	return defaultQUICStreamAccept
}

// acceptError 归一化接收错误 + 上下文取消和主动关闭不作为异常返回。
func (transport *QUICTransport) acceptError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return nil
	}
	if transport.isClosed() || errors.Is(err, quic.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("p2p: accept quic connection: %w", err)
}

// addListener 注册 QUIC 监听器 + 持锁防止关闭和新增监听并发冲突。
func (transport *QUICTransport) addListener(key string, listener *quic.Listener) error {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	if transport.closed {
		return ErrTransportUnavailable
	}
	transport.listeners[key] = listener
	return nil
}

// removeListener 移除 QUIC 监听器索引 + 监听退出后保持内部状态准确。
func (transport *QUICTransport) removeListener(key string) {
	transport.mutex.Lock()
	delete(transport.listeners, key)
	transport.mutex.Unlock()
}

// isClosed 读取 QUIC 传输关闭状态 + 持锁避免与 Close 并发读写。
func (transport *QUICTransport) isClosed() bool {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	return transport.closed
}

type quicReadResult struct {
	message Message
	err     error
}

type quicPriorityStream struct {
	stream *quic.Stream
	mutex  sync.Mutex
}

type quicPriorityStreamPool struct {
	streams []*quicPriorityStream
	next    uint64
}

// QUICConnection 封装 QUIC 多 stream 连接 + 按消息优先级隔离共识、业务和同步流量。
type QUICConnection struct {
	id                  string
	connection          *quic.Conn
	remotePeerID        string
	maxMessageSize      int
	streamPoolSize      int
	highReads           chan quicReadResult
	normalReads         chan quicReadResult
	lowReads            chan quicReadResult
	readNotify          chan struct{}
	done                chan struct{}
	streamMutex         sync.Mutex
	writeStreams        map[MessagePriority]*quicPriorityStreamPool
	allStreams          map[*quic.Stream]struct{}
	closed              bool
	fatalErr            error
	closeOnce           sync.Once
	closeErr            error
	messagePriority     func(Message) MessagePriority
	messageConcurrency  func(Message) ProtocolConcurrencyMode
	messagePartitionKey func(Message) string
	logger              *slog.Logger
}

// newQUICConnection 创建 QUIC 连接包装 + 首个 stream 作为高优先级控制通道完成握手。
func newQUICConnection(
	connection *quic.Conn,
	stream *quic.Stream,
	remotePeerID string,
	maxMessageSize int,
	streamPoolSize int,
	messagePriority func(Message) MessagePriority,
	messageConcurrency func(Message) ProtocolConcurrencyMode,
	messagePartitionKey func(Message) string,
	logger *slog.Logger,
) *QUICConnection {
	connectionID, err := newMessageID()
	if err != nil {
		connectionID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	readBufferSize := quicReadBufferSize(maxMessageSize)
	quicConnection := &QUICConnection{
		id:                  connectionID,
		connection:          connection,
		remotePeerID:        remotePeerID,
		maxMessageSize:      normalizeMaxMessageSize(maxMessageSize),
		streamPoolSize:      normalizeQUICStreamPoolSize(streamPoolSize),
		highReads:           make(chan quicReadResult, readBufferSize),
		normalReads:         make(chan quicReadResult, readBufferSize),
		lowReads:            make(chan quicReadResult, readBufferSize),
		readNotify:          make(chan struct{}, 1),
		done:                make(chan struct{}),
		writeStreams:        make(map[MessagePriority]*quicPriorityStreamPool),
		allStreams:          make(map[*quic.Stream]struct{}),
		messagePriority:     normalizeQUICMessagePriority(messagePriority),
		messageConcurrency:  normalizeQUICMessageConcurrency(messageConcurrency),
		messagePartitionKey: normalizeQUICMessagePartitionKey(messagePartitionKey),
		logger:              utils.EnsureLogger(logger),
	}
	quicConnection.registerInitialStream(stream)
	go quicConnection.acceptStreamLoop()
	return quicConnection
}

func (connection *QUICConnection) ID() string {
	return connection.id
}

func (connection *QUICConnection) Protocol() utils.MultiAddressProtocol {
	return utils.ProtocolQUIC
}

func (connection *QUICConnection) RemotePeerID() string {
	return connection.remotePeerID
}

func (connection *QUICConnection) LocalAddress() string {
	return connection.connection.LocalAddr().String()
}

func (connection *QUICConnection) RemoteAddress() string {
	return connection.connection.RemoteAddr().String()
}

// ReadMessage 读取 QUIC 消息 + 优先返回高优先级 stream 上已完成解帧的消息。
func (connection *QUICConnection) ReadMessage(ctx context.Context) (Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if result, ok := connection.tryReadResult(); ok {
			return result.message, result.err
		}
		select {
		case <-connection.readNotify:
		case <-connection.done:
			return Message{}, connection.closedReadError()
		case <-ctx.Done():
			return Message{}, ctx.Err()
		}
	}
}

// WriteMessage 写入 QUIC 消息 + 默认按协议内置优先级选择独立 stream。
func (connection *QUICConnection) WriteMessage(ctx context.Context, message Message) error {
	return connection.WriteMessageWithSchedule(ctx, message, connection.messageWriteSchedule(message))
}

// WriteMessageWithPriority 写入指定优先级 stream + 让上层注册协议的优先级直接控制 QUIC lane。
func (connection *QUICConnection) WriteMessageWithPriority(ctx context.Context, message Message, priority MessagePriority) error {
	schedule := connection.messageWriteSchedule(message)
	schedule.priority = priority
	return connection.WriteMessageWithSchedule(ctx, message, schedule)
}

// WriteMessageWithSchedule 按协议调度策略写入 QUIC stream + 串行业务固定 stream，无状态流量使用 stream 池。
func (connection *QUICConnection) WriteMessageWithSchedule(ctx context.Context, message Message, schedule messageWriteSchedule) error {
	if ctx == nil {
		ctx = context.Background()
	}
	schedule = connection.normalizeWriteSchedule(message, schedule)
	priorityStream, err := connection.writerStream(ctx, schedule)
	if err != nil {
		return err
	}
	priorityStream.mutex.Lock()
	defer priorityStream.mutex.Unlock()

	stopDeadline := armConnectionDeadline(ctx, priorityStream.stream.SetWriteDeadline)
	defer stopDeadline()

	if err := writeMessageFrame(priorityStream.stream, message, connection.maxMessageSize); err != nil {
		return normalizeConnectionError("write", err)
	}
	return nil
}

func (connection *QUICConnection) messageWriteSchedule(message Message) messageWriteSchedule {
	return messageWriteSchedule{
		priority:     connection.messagePriority(message),
		concurrency:  connection.messageConcurrency(message),
		partitionKey: connection.messagePartitionKey(message),
	}
}

func (connection *QUICConnection) normalizeWriteSchedule(message Message, schedule messageWriteSchedule) messageWriteSchedule {
	schedule.priority = normalizeMessagePriority(schedule.priority)
	schedule.concurrency = normalizeProtocolConcurrency(schedule.concurrency)
	if schedule.partitionKey == "" {
		schedule.partitionKey = connection.messagePartitionKey(message)
	}
	return schedule
}

// PriorityIsolatedWrites 声明 QUIC 写入支持优先级隔离 + 允许上层队列按 lane 并发 flush。
func (connection *QUICConnection) PriorityIsolatedWrites() bool {
	return true
}

// PriorityWriteParallelism 返回同优先级写并行度 + 让异步写队列匹配 QUIC stream 池容量。
func (connection *QUICConnection) PriorityWriteParallelism() int {
	return connection.streamPoolSize
}

// Close 关闭 QUIC 连接 + 保证流和连接只释放一次。
func (connection *QUICConnection) Close() error {
	connection.closeOnce.Do(func() {
		streams := connection.closeTrackedStreams()
		close(connection.done)
		closeErrors := make([]error, 0, len(streams)+1)
		for _, stream := range streams {
			stream.CancelRead(quicStreamCancelCode)
			stream.CancelWrite(quicStreamCancelCode)
			if err := stream.Close(); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		connectionErr := connection.connection.CloseWithError(quicCloseCode, "closed")
		if connectionErr != nil {
			closeErrors = append(closeErrors, connectionErr)
		}
		connection.closeErr = errors.Join(closeErrors...)
	})
	return connection.closeErr
}

func (connection *QUICConnection) registerInitialStream(stream *quic.Stream) {
	if stream == nil {
		return
	}
	priorityStream := &quicPriorityStream{stream: stream}
	connection.streamMutex.Lock()
	connection.writeStreams[MessagePriorityHigh] = &quicPriorityStreamPool{
		streams: []*quicPriorityStream{priorityStream},
	}
	connection.allStreams[stream] = struct{}{}
	connection.streamMutex.Unlock()
	connection.startStreamReader(stream)
}

func (connection *QUICConnection) writerStream(ctx context.Context, schedule messageWriteSchedule) (*quicPriorityStream, error) {
	allowPool := schedule.concurrency == ProtocolConcurrencyStateless
	existing, shouldOpen, ok := connection.writerStreamDecision(schedule.priority, allowPool)
	if !ok {
		return nil, ErrConnectionClosed
	}
	if !shouldOpen {
		return existing, nil
	}
	stream, err := connection.connection.OpenStreamSync(ctx)
	if err != nil {
		if existing != nil && ctx.Err() == nil {
			return existing, nil
		}
		return nil, normalizeConnectionError("open quic stream", err)
	}
	priorityStream := &quicPriorityStream{stream: stream}
	registered, ok := connection.registerWriterStream(schedule.priority, priorityStream, allowPool)
	if !ok {
		closeUnusedQUICStream(stream)
		return nil, ErrConnectionClosed
	}
	if registered != priorityStream {
		closeUnusedQUICStream(stream)
		return registered, nil
	}
	connection.startStreamReader(stream)
	return priorityStream, nil
}

func (connection *QUICConnection) writerStreamDecision(priority MessagePriority, allowPool bool) (*quicPriorityStream, bool, bool) {
	connection.streamMutex.Lock()
	defer connection.streamMutex.Unlock()
	if connection.closed {
		return nil, false, false
	}
	pool := connection.writeStreams[priority]
	if pool == nil || len(pool.streams) == 0 {
		return nil, true, true
	}
	if !allowPool {
		return pool.streams[0], false, true
	}
	return pool.nextStream(), len(pool.streams) < connection.streamPoolSize, true
}

func (connection *QUICConnection) registerWriterStream(priority MessagePriority, priorityStream *quicPriorityStream, allowPool bool) (*quicPriorityStream, bool) {
	connection.streamMutex.Lock()
	defer connection.streamMutex.Unlock()
	if connection.closed {
		return nil, false
	}
	pool := connection.writeStreams[priority]
	if pool == nil {
		pool = &quicPriorityStreamPool{}
		connection.writeStreams[priority] = pool
	}
	if len(pool.streams) > 0 && !allowPool {
		return pool.streams[0], true
	}
	if len(pool.streams) >= connection.streamPoolSize {
		return pool.nextStream(), true
	}
	pool.streams = append(pool.streams, priorityStream)
	connection.allStreams[priorityStream.stream] = struct{}{}
	return priorityStream, true
}

func (pool *quicPriorityStreamPool) nextStream() *quicPriorityStream {
	if pool == nil || len(pool.streams) == 0 {
		return nil
	}
	index := int(pool.next % uint64(len(pool.streams)))
	pool.next++
	return pool.streams[index]
}

func (connection *QUICConnection) trackAcceptedStream(stream *quic.Stream) bool {
	connection.streamMutex.Lock()
	defer connection.streamMutex.Unlock()
	if connection.closed {
		return false
	}
	connection.allStreams[stream] = struct{}{}
	return true
}

func (connection *QUICConnection) startStreamReader(stream *quic.Stream) {
	go connection.readStreamLoop(stream)
}

func (connection *QUICConnection) acceptStreamLoop() {
	for {
		stream, err := connection.connection.AcceptStream(connection.connection.Context())
		if err != nil {
			connection.fail(normalizeConnectionError("accept quic stream", err))
			return
		}
		if !connection.trackAcceptedStream(stream) {
			closeUnusedQUICStream(stream)
			return
		}
		connection.startStreamReader(stream)
	}
}

func (connection *QUICConnection) readStreamLoop(stream *quic.Stream) {
	defer connection.unregisterStream(stream)
	for {
		message, err := readMessageFrame(stream, connection.maxMessageSize)
		if err != nil {
			if connection.isClosed() || isIgnorableQUICStreamReadError(err) {
				return
			}
			connection.fail(normalizeConnectionError("read", err))
			return
		}
		connection.deliverReadResult(quicReadResult{message: message})
	}
}

func (connection *QUICConnection) unregisterStream(stream *quic.Stream) {
	if stream == nil {
		return
	}
	connection.streamMutex.Lock()
	defer connection.streamMutex.Unlock()
	delete(connection.allStreams, stream)
	for priority, pool := range connection.writeStreams {
		if pool == nil {
			continue
		}
		pool.removeStream(stream)
		if len(pool.streams) == 0 {
			delete(connection.writeStreams, priority)
		}
	}
}

func (pool *quicPriorityStreamPool) removeStream(stream *quic.Stream) {
	if pool == nil || stream == nil {
		return
	}
	for index, priorityStream := range pool.streams {
		if priorityStream == nil || priorityStream.stream != stream {
			continue
		}
		pool.streams = append(pool.streams[:index], pool.streams[index+1:]...)
		if len(pool.streams) > 0 && pool.next >= uint64(len(pool.streams)) {
			pool.next = pool.next % uint64(len(pool.streams))
		}
		return
	}
}

func isIgnorableQUICStreamReadError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var streamError *quic.StreamError
	return errors.As(err, &streamError)
}

func (connection *QUICConnection) deliverReadResult(result quicReadResult) {
	priority := normalizeMessagePriority(connection.messagePriority(result.message))
	switch priority {
	case MessagePriorityHigh:
		connection.sendReadResult(connection.highReads, result)
	case MessagePriorityLow:
		connection.sendReadResult(connection.lowReads, result)
	default:
		connection.sendReadResult(connection.normalReads, result)
	}
}

func (connection *QUICConnection) sendReadResult(queue chan quicReadResult, result quicReadResult) {
	select {
	case queue <- result:
		connection.notifyRead()
	case <-connection.done:
	}
}

func (connection *QUICConnection) notifyRead() {
	select {
	case connection.readNotify <- struct{}{}:
	default:
	}
}

func (connection *QUICConnection) tryReadResult() (quicReadResult, bool) {
	if result, ok := tryReadQUICResult(connection.highReads); ok {
		return result, true
	}
	if result, ok := tryReadQUICResult(connection.normalReads); ok {
		return result, true
	}
	return tryReadQUICResult(connection.lowReads)
}

func tryReadQUICResult(queue chan quicReadResult) (quicReadResult, bool) {
	select {
	case result := <-queue:
		return result, true
	default:
		return quicReadResult{}, false
	}
}

func (connection *QUICConnection) fail(err error) {
	if err == nil {
		return
	}
	connection.streamMutex.Lock()
	if connection.closed {
		connection.streamMutex.Unlock()
		return
	}
	if connection.fatalErr == nil {
		connection.fatalErr = err
	}
	connection.streamMutex.Unlock()
	_ = connection.Close()
}

func (connection *QUICConnection) isClosed() bool {
	select {
	case <-connection.done:
		return true
	default:
		return false
	}
}

func (connection *QUICConnection) closedReadError() error {
	connection.streamMutex.Lock()
	defer connection.streamMutex.Unlock()
	if connection.fatalErr != nil {
		return connection.fatalErr
	}
	return ErrConnectionClosed
}

func (connection *QUICConnection) closeTrackedStreams() []*quic.Stream {
	connection.streamMutex.Lock()
	defer connection.streamMutex.Unlock()
	connection.closed = true
	streams := make([]*quic.Stream, 0, len(connection.allStreams))
	for stream := range connection.allStreams {
		streams = append(streams, stream)
	}
	connection.writeStreams = make(map[MessagePriority]*quicPriorityStreamPool)
	connection.allStreams = make(map[*quic.Stream]struct{})
	return streams
}

func closeUnusedQUICStream(stream *quic.Stream) {
	if stream == nil {
		return
	}
	stream.CancelRead(quicStreamCancelCode)
	stream.CancelWrite(quicStreamCancelCode)
	_ = stream.Close()
}

func quicReadBufferSize(maxMessageSize int) int {
	normalized := normalizeMaxMessageSize(maxMessageSize)
	size := defaultQUICReadBufferBytes / normalized
	if size < 1 {
		return 1
	}
	if size > maxQUICReadBufferSize {
		return maxQUICReadBufferSize
	}
	return size
}

// normalizeQUICTLSConfig 归一化 TLS 配置 + 使用临时自签证书避免生产部署依赖外部 CA。
func normalizeQUICTLSConfig(config QUICTransportConfig) (*tls.Config, error) {
	if config.TLSConfig != nil {
		cloned := config.TLSConfig.Clone()
		ensureQUICNextProtos(cloned)
		return cloned, nil
	}
	certificate, err := generateQUICCertificate()
	if err != nil {
		return nil, fmt.Errorf("p2p: generate quic certificate: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		NextProtos:   []string{quicApplicationProtocol},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// clientTLSConfig 生成客户端 TLS 配置 + 跳过证书链校验是因为 SecureSession 已绑定 peer 身份并加密消息。
func (transport *QUICTransport) clientTLSConfig() (*tls.Config, error) {
	if transport == nil || transport.tlsConfig == nil {
		return nil, fmt.Errorf("%w: nil quic tls config", ErrTransportUnavailable)
	}
	cloned := transport.tlsConfig.Clone()
	cloned.Certificates = nil
	if cloned.ServerName == "" {
		cloned.ServerName = quicApplicationProtocol
	}
	cloned.InsecureSkipVerify = true
	ensureQUICNextProtos(cloned)
	return cloned, nil
}

func normalizeQUICMessagePriority(priority func(Message) MessagePriority) func(Message) MessagePriority {
	if priority != nil {
		return func(message Message) (priorityValue MessagePriority) {
			priorityValue = defaultProtocolPriority(message.Type)
			defer func() {
				if recover() != nil {
					priorityValue = defaultProtocolPriority(message.Type)
				}
			}()
			return normalizeMessagePriority(priority(message))
		}
	}
	return func(message Message) MessagePriority {
		return defaultProtocolPriority(message.Type)
	}
}

func normalizeQUICMessageConcurrency(concurrency func(Message) ProtocolConcurrencyMode) func(Message) ProtocolConcurrencyMode {
	if concurrency != nil {
		return func(message Message) (concurrencyValue ProtocolConcurrencyMode) {
			concurrencyValue = ProtocolConcurrencyOrdered
			defer func() {
				if recover() != nil {
					concurrencyValue = ProtocolConcurrencyOrdered
				}
			}()
			return normalizeProtocolConcurrency(concurrency(message))
		}
	}
	return func(message Message) ProtocolConcurrencyMode {
		return defaultProtocolConcurrency(message.Type)
	}
}

func normalizeQUICMessagePartitionKey(partitionKey func(Message) string) func(Message) string {
	if partitionKey != nil {
		return func(message Message) (key string) {
			defer func() {
				if recover() != nil {
					key = ""
				}
			}()
			return partitionKey(message)
		}
	}
	return func(Message) string {
		return ""
	}
}

func normalizeMessagePriority(priority MessagePriority) MessagePriority {
	if priority > MessagePriorityHigh {
		return MessagePriorityNormal
	}
	return priority
}

func normalizeQUICStreamPoolSize(size int) int {
	if size <= 0 {
		return defaultQUICPriorityStreamPoolSize
	}
	if size > maxQUICPriorityStreamPoolSize {
		return maxQUICPriorityStreamPoolSize
	}
	return size
}

// normalizeQUICConfig 归一化 quic-go 配置 + 设置保守窗口避免异常内存占用。
func normalizeQUICConfig(config *quic.Config, transportConfig QUICTransportConfig, maxMessageSize int) *quic.Config {
	if config != nil {
		cloned := config.Clone()
		applyQUICReceiveWindows(cloned, transportConfig, maxMessageSize)
		return cloned
	}
	normalized := &quic.Config{
		HandshakeIdleTimeout:  5 * time.Second,
		MaxIdleTimeout:        30 * time.Second,
		KeepAlivePeriod:       10 * time.Second,
		MaxIncomingStreams:    256,
		MaxIncomingUniStreams: 16,
	}
	applyQUICReceiveWindows(normalized, transportConfig, maxMessageSize)
	return normalized
}

func applyQUICReceiveWindows(config *quic.Config, transportConfig QUICTransportConfig, maxMessageSize int) {
	if config == nil {
		return
	}
	maxStreamWindow := uint64(maxMessageSize)
	maxConnectionWindow := uint64(maxMessageSize * 4)
	if transportConfig.MaxStreamReceiveWindow > 0 {
		maxStreamWindow = transportConfig.MaxStreamReceiveWindow
	}
	if transportConfig.MaxConnectionReceiveWindow > 0 {
		maxConnectionWindow = transportConfig.MaxConnectionReceiveWindow
	}
	if config.MaxStreamReceiveWindow == 0 || transportConfig.MaxStreamReceiveWindow > 0 {
		config.MaxStreamReceiveWindow = maxStreamWindow
	}
	if config.MaxConnectionReceiveWindow == 0 || transportConfig.MaxConnectionReceiveWindow > 0 {
		config.MaxConnectionReceiveWindow = maxConnectionWindow
	}
	initialStreamWindow := uint64(defaultQUICInitialStreamReceiveWindow)
	initialConnectionWindow := uint64(defaultQUICInitialConnectionReceiveWindow)
	if transportConfig.InitialStreamReceiveWindow > 0 {
		initialStreamWindow = transportConfig.InitialStreamReceiveWindow
	}
	if transportConfig.InitialConnectionReceiveWindow > 0 {
		initialConnectionWindow = transportConfig.InitialConnectionReceiveWindow
	}
	if config.InitialStreamReceiveWindow == 0 || transportConfig.InitialStreamReceiveWindow > 0 {
		if initialStreamWindow > config.MaxStreamReceiveWindow {
			initialStreamWindow = config.MaxStreamReceiveWindow
		}
		config.InitialStreamReceiveWindow = initialStreamWindow
	}
	if config.InitialConnectionReceiveWindow == 0 || transportConfig.InitialConnectionReceiveWindow > 0 {
		if initialConnectionWindow > config.MaxConnectionReceiveWindow {
			initialConnectionWindow = config.MaxConnectionReceiveWindow
		}
		config.InitialConnectionReceiveWindow = initialConnectionWindow
	}
	if config.MaxConnectionReceiveWindow < config.MaxStreamReceiveWindow {
		config.MaxConnectionReceiveWindow = config.MaxStreamReceiveWindow
	}
}

// ensureQUICNextProtos 补齐 ALPN 协议 + 防止 TLS 握手因协议为空失败。
func ensureQUICNextProtos(config *tls.Config) {
	if len(config.NextProtos) == 0 {
		config.NextProtos = []string{quicApplicationProtocol}
	}
	if config.MinVersion == 0 {
		config.MinVersion = tls.VersionTLS13
	}
}

// generateQUICCertificate 生成临时自签证书 + 支持无外部证书的本地节点启动。
func generateQUICCertificate() (tls.Certificate, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: quicApplicationProtocol,
		},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}

	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse certificate: %w", err)
	}
	return certificate, nil
}
