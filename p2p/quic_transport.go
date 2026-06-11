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
	"log/slog"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"solana_golang/utils"
)

const (
	quicApplicationProtocol = "solana-golang-p2p/1"
	quicCloseCode           = quic.ApplicationErrorCode(0)
	defaultQUICStreamAccept = 5 * time.Second
)

// QUICTransportConfig 保存 QUIC 配置 + 支持注入 TLS、quic-go 配置和日志。
type QUICTransportConfig struct {
	MaxMessageSize      int
	MaxPendingInbound   int
	MaxConnectionsPerIP int
	TLSConfig           *tls.Config
	QUICConfig          *quic.Config
	Logger              *slog.Logger
}

// QUICTransport 实现 QUIC 传输 + 基于 quic-go 提供低延迟多路复用连接。
type QUICTransport struct {
	mutex          sync.Mutex
	listeners      map[string]*quic.Listener
	closed         bool
	maxMessageSize int
	inboundLimiter *transportInboundLimiter
	tlsConfig      *tls.Config
	quicConfig     *quic.Config
	logger         *slog.Logger
}

// NewQUICTransport 创建默认 QUIC 传输 + 使用安全消息上限和临时 TLS 证书。
func NewQUICTransport() *QUICTransport {
	return NewQUICTransportWithConfig(QUICTransportConfig{})
}

// NewQUICTransportWithConfig 创建 QUIC 传输 + 允许生产环境注入可信 TLS 配置。
func NewQUICTransportWithConfig(config QUICTransportConfig) *QUICTransport {
	return &QUICTransport{
		listeners:      make(map[string]*quic.Listener),
		maxMessageSize: normalizeMaxMessageSize(config.MaxMessageSize),
		inboundLimiter: newTransportInboundLimiter(config.MaxPendingInbound, config.MaxConnectionsPerIP),
		tlsConfig:      normalizeQUICTLSConfig(config.TLSConfig),
		quicConfig:     normalizeQUICConfig(config.QUICConfig),
		logger:         utils.EnsureLogger(config.Logger),
	}
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
	connection, err := quic.DialAddr(ctx, joinAddress(address), clientQUICTLSConfig(transport.tlsConfig), transport.quicConfig.Clone())
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
	return newQUICConnection(connection, stream, address.PeerID, transport.maxMessageSize), nil
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
	handler(ctx, newQUICConnection(connection, stream, "", transport.maxMessageSize))
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

// QUICConnection 封装 QUIC 双向流 + 复用统一消息帧协议。
type QUICConnection struct {
	id             string
	connection     *quic.Conn
	stream         *quic.Stream
	remotePeerID   string
	maxMessageSize int
	readMutex      sync.Mutex
	writeMutex     sync.Mutex
	closeOnce      sync.Once
	closeErr       error
}

// newQUICConnection 创建 QUIC 连接包装 + 生成连接 ID 并保存首个双向流。
func newQUICConnection(connection *quic.Conn, stream *quic.Stream, remotePeerID string, maxMessageSize int) *QUICConnection {
	connectionID, err := newMessageID()
	if err != nil {
		connectionID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return &QUICConnection{
		id:             connectionID,
		connection:     connection,
		stream:         stream,
		remotePeerID:   remotePeerID,
		maxMessageSize: normalizeMaxMessageSize(maxMessageSize),
	}
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

// ReadMessage 读取 QUIC 消息 + 使用流读取 deadline 响应上下文取消。
func (connection *QUICConnection) ReadMessage(ctx context.Context) (Message, error) {
	connection.readMutex.Lock()
	defer connection.readMutex.Unlock()

	stopDeadline := armConnectionDeadline(ctx, connection.stream.SetReadDeadline)
	defer stopDeadline()

	message, err := readMessageFrame(connection.stream, connection.maxMessageSize)
	if err != nil {
		return Message{}, normalizeConnectionError("read", err)
	}
	return message, nil
}

// WriteMessage 写入 QUIC 消息 + 写锁保证单流帧顺序一致。
func (connection *QUICConnection) WriteMessage(ctx context.Context, message Message) error {
	connection.writeMutex.Lock()
	defer connection.writeMutex.Unlock()

	stopDeadline := armConnectionDeadline(ctx, connection.stream.SetWriteDeadline)
	defer stopDeadline()

	if err := writeMessageFrame(connection.stream, message, connection.maxMessageSize); err != nil {
		return normalizeConnectionError("write", err)
	}
	return nil
}

// Close 关闭 QUIC 连接 + 保证流和连接只释放一次。
func (connection *QUICConnection) Close() error {
	connection.closeOnce.Do(func() {
		streamErr := connection.stream.Close()
		connectionErr := connection.connection.CloseWithError(quicCloseCode, "closed")
		connection.closeErr = errors.Join(streamErr, connectionErr)
	})
	return connection.closeErr
}

// normalizeQUICTLSConfig 归一化 TLS 配置 + 默认生成临时证书保证 QUIC 可启动。
func normalizeQUICTLSConfig(config *tls.Config) *tls.Config {
	if config != nil {
		cloned := config.Clone()
		ensureQUICNextProtos(cloned)
		return cloned
	}
	certificate, err := generateQUICCertificate()
	if err != nil {
		panic(fmt.Errorf("p2p: generate quic certificate: %w", err))
	}
	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		NextProtos:   []string{quicApplicationProtocol},
		MinVersion:   tls.VersionTLS13,
	}
}

// clientQUICTLSConfig 生成客户端 TLS 配置 + 默认跳过自签证书校验支持节点自发现。
func clientQUICTLSConfig(config *tls.Config) *tls.Config {
	cloned := config.Clone()
	cloned.Certificates = nil
	if cloned.ServerName == "" {
		cloned.ServerName = quicApplicationProtocol
	}
	if cloned.RootCAs == nil && cloned.VerifyPeerCertificate == nil {
		cloned.InsecureSkipVerify = true
	}
	ensureQUICNextProtos(cloned)
	return cloned
}

// normalizeQUICConfig 归一化 quic-go 配置 + 设置保守窗口避免异常内存占用。
func normalizeQUICConfig(config *quic.Config) *quic.Config {
	if config != nil {
		return config.Clone()
	}
	return &quic.Config{
		HandshakeIdleTimeout:           5 * time.Second,
		MaxIdleTimeout:                 30 * time.Second,
		KeepAlivePeriod:                10 * time.Second,
		MaxIncomingStreams:             256,
		MaxIncomingUniStreams:          16,
		MaxStreamReceiveWindow:         uint64(DefaultMaxMessageSize),
		MaxConnectionReceiveWindow:     uint64(DefaultMaxMessageSize * 4),
		InitialStreamReceiveWindow:     256 * 1024,
		InitialConnectionReceiveWindow: 512 * 1024,
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
