package p2p

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"solana_golang/utils"
)

// TCPTransportConfig 保存 TCP 配置 + 允许测试和部署调整消息上限与日志。
type TCPTransportConfig struct {
	MaxMessageSize int
	Logger         *slog.Logger
}

// TCPTransport 实现 TCP 传输 + 为 Host 提供监听和拨号能力。
type TCPTransport struct {
	mutex          sync.Mutex
	listeners      map[string]net.Listener
	closed         bool
	maxMessageSize int
	logger         *slog.Logger
}

// NewTCPTransport 创建默认 TCP 传输 + 使用安全的消息大小上限。
func NewTCPTransport() *TCPTransport {
	return NewTCPTransportWithConfig(TCPTransportConfig{})
}

// NewTCPTransportWithConfig 创建 TCP 传输 + 支持按环境注入配置。
func NewTCPTransportWithConfig(config TCPTransportConfig) *TCPTransport {
	return &TCPTransport{
		listeners:      make(map[string]net.Listener),
		maxMessageSize: normalizeMaxMessageSize(config.MaxMessageSize),
		logger:         utils.EnsureLogger(config.Logger),
	}
}

// Protocol 返回传输协议 + 供 Host 按 multi-address 分发请求。
func (transport *TCPTransport) Protocol() utils.MultiAddressProtocol {
	return utils.ProtocolTCP
}

// Listen 监听 TCP 地址 + 接收入站连接并交给上层处理器。
func (transport *TCPTransport) Listen(ctx context.Context, address utils.MultiAddress, handler ConnectionHandler) error {
	if err := validateListenInput(address, utils.ProtocolTCP, handler); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", joinAddress(address))
	if err != nil {
		return fmt.Errorf("p2p: listen tcp %s: %w", address.String(), err)
	}
	if err := transport.addListener(address.String(), listener); err != nil {
		_ = listener.Close()
		return err
	}
	defer transport.removeListener(address.String())

	transport.logger.Info("p2p tcp listen",
		slog.String("address", address.String()),
		slog.String("protocol", string(address.Protocol)),
	)
	go closeListenerOnContext(ctx, listener)
	return transport.acceptLoop(ctx, listener, handler)
}

// Dial 拨号 TCP 地址 + 返回统一连接接口供上层收发消息。
func (transport *TCPTransport) Dial(ctx context.Context, address utils.MultiAddress) (Connection, error) {
	if err := validateDialInput(address, utils.ProtocolTCP); err != nil {
		return nil, err
	}

	dialer := net.Dialer{}
	netConnection, err := dialer.DialContext(ctx, "tcp", joinAddress(address))
	if err != nil {
		return nil, fmt.Errorf("p2p: dial tcp %s: %w", address.String(), err)
	}

	transport.logger.Info("p2p tcp dial",
		slog.String("address", address.String()),
		slog.String("peer_id", address.PeerID),
	)
	return newTCPConnection(netConnection, address.PeerID, transport.maxMessageSize), nil
}

// Close 关闭 TCP 传输 + 释放全部监听端口。
func (transport *TCPTransport) Close() error {
	transport.mutex.Lock()
	if transport.closed {
		transport.mutex.Unlock()
		return nil
	}
	transport.closed = true
	listeners := make([]net.Listener, 0, len(transport.listeners))
	for _, listener := range transport.listeners {
		listeners = append(listeners, listener)
	}
	transport.listeners = make(map[string]net.Listener)
	transport.mutex.Unlock()

	var closeErrors []error
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
}

// acceptLoop 执行对应逻辑 + 保持函数职责清晰可维护。
func (transport *TCPTransport) acceptLoop(ctx context.Context, listener net.Listener, handler ConnectionHandler) error {
	for {
		netConnection, err := listener.Accept()
		if err != nil {
			return transport.acceptError(ctx, err)
		}
		connection := newTCPConnection(netConnection, "", transport.maxMessageSize)
		go handler(ctx, connection)
	}
}

// acceptError 执行对应逻辑 + 保持函数职责清晰可维护。
func (transport *TCPTransport) acceptError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return nil
	}
	if transport.isClosed() || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return fmt.Errorf("p2p: accept tcp connection: %w", err)
}

// addListener 执行对应逻辑 + 保持函数职责清晰可维护。
func (transport *TCPTransport) addListener(key string, listener net.Listener) error {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	if transport.closed {
		return ErrTransportUnavailable
	}
	transport.listeners[key] = listener
	return nil
}

// removeListener 执行对应逻辑 + 保持函数职责清晰可维护。
func (transport *TCPTransport) removeListener(key string) {
	transport.mutex.Lock()
	delete(transport.listeners, key)
	transport.mutex.Unlock()
}

// isClosed 执行对应逻辑 + 保持函数职责清晰可维护。
func (transport *TCPTransport) isClosed() bool {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	return transport.closed
}

// TCPConnection 封装 TCP 连接 + 使用长度前缀消息帧保证读写边界。
type TCPConnection struct {
	id             string
	netConnection  net.Conn
	remotePeerID   string
	maxMessageSize int
	readMutex      sync.Mutex
	writeMutex     sync.Mutex
	closeOnce      sync.Once
	closeErr       error
}

// newTCPConnection 执行对应逻辑 + 保持函数职责清晰可维护。
func newTCPConnection(netConnection net.Conn, remotePeerID string, maxMessageSize int) *TCPConnection {
	connectionID, err := newMessageID()
	if err != nil {
		connectionID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return &TCPConnection{
		id:             connectionID,
		netConnection:  netConnection,
		remotePeerID:   remotePeerID,
		maxMessageSize: normalizeMaxMessageSize(maxMessageSize),
	}
}

// ID 返回连接 ID + 便于日志和连接池定位。
func (connection *TCPConnection) ID() string {
	return connection.id
}

// Protocol 返回连接协议 + 供上层统计不同传输的连接状态。
func (connection *TCPConnection) Protocol() utils.MultiAddressProtocol {
	return utils.ProtocolTCP
}

// RemotePeerID 返回远端节点 ID + 出站连接从拨号地址继承该值。
func (connection *TCPConnection) RemotePeerID() string {
	return connection.remotePeerID
}

// LocalAddress 返回本地地址 + 用于日志和连接诊断。
func (connection *TCPConnection) LocalAddress() string {
	return connection.netConnection.LocalAddr().String()
}

// RemoteAddress 返回远端地址 + 用于日志和连接诊断。
func (connection *TCPConnection) RemoteAddress() string {
	return connection.netConnection.RemoteAddr().String()
}

// ReadMessage 读取一条消息 + 使用帧长度避免粘包和半包问题。
func (connection *TCPConnection) ReadMessage(ctx context.Context) (Message, error) {
	connection.readMutex.Lock()
	defer connection.readMutex.Unlock()

	stopDeadline := armConnectionDeadline(ctx, connection.netConnection.SetReadDeadline)
	defer stopDeadline()

	message, err := readMessageFrame(connection.netConnection, connection.maxMessageSize)
	if err != nil {
		return Message{}, normalizeConnectionError("read", err)
	}
	return message, nil
}

// WriteMessage 写入一条消息 + 使用写锁防止并发写入交叉。
func (connection *TCPConnection) WriteMessage(ctx context.Context, message Message) error {
	connection.writeMutex.Lock()
	defer connection.writeMutex.Unlock()

	stopDeadline := armConnectionDeadline(ctx, connection.netConnection.SetWriteDeadline)
	defer stopDeadline()

	if err := writeMessageFrame(connection.netConnection, message, connection.maxMessageSize); err != nil {
		return normalizeConnectionError("write", err)
	}
	return nil
}

// Close 关闭连接 + 保证多次关闭不会重复操作底层连接。
func (connection *TCPConnection) Close() error {
	connection.closeOnce.Do(func() {
		connection.closeErr = connection.netConnection.Close()
	})
	return connection.closeErr
}

// validateListenInput 执行对应逻辑 + 保持函数职责清晰可维护。
func validateListenInput(address utils.MultiAddress, protocol utils.MultiAddressProtocol, handler ConnectionHandler) error {
	if err := validateDialInput(address, protocol); err != nil {
		return err
	}
	if handler == nil {
		return ErrNilHandler
	}
	return nil
}

// validateDialInput 执行对应逻辑 + 保持函数职责清晰可维护。
func validateDialInput(address utils.MultiAddress, protocol utils.MultiAddressProtocol) error {
	if address.Protocol != protocol {
		return fmt.Errorf("%w: want %s got %s", ErrUnsupportedProtocol, protocol, address.Protocol)
	}
	return nil
}

// joinAddress 执行对应逻辑 + 保持函数职责清晰可维护。
func joinAddress(address utils.MultiAddress) string {
	return net.JoinHostPort(address.IPAddress, strconv.Itoa(address.Port))
}

// closeListenerOnContext 执行对应逻辑 + 保持函数职责清晰可维护。
func closeListenerOnContext(ctx context.Context, listener net.Listener) {
	if ctx == nil {
		return
	}
	<-ctx.Done()
	_ = listener.Close()
}

// armConnectionDeadline 执行对应逻辑 + 保持函数职责清晰可维护。
func armConnectionDeadline(ctx context.Context, setDeadline func(time.Time) error) func() {
	if ctx == nil {
		ctx = context.Background()
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = setDeadline(deadline)
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = setDeadline(time.Now())
		case <-done:
		}
	}()

	return func() {
		close(done)
		_ = setDeadline(time.Time{})
	}
}

// normalizeConnectionError 执行对应逻辑 + 保持函数职责清晰可维护。
func normalizeConnectionError(operation string, err error) error {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %s", ErrConnectionClosed, operation)
	}
	return fmt.Errorf("p2p: %s message: %w", operation, err)
}
