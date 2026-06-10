package p2p

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"solana_golang/utils"
)

const defaultDialTimeout = 5 * time.Second

// HostConfig 保存 Host 配置 + 支持注入节点身份、协议优先级和日志。
type HostConfig struct {
	PeerID             string
	PreferredProtocols []utils.MultiAddressProtocol
	DialTimeout        time.Duration
	Logger             *slog.Logger
	Registry           *ProtocolRegistry
}

// Host 管理 P2P 节点运行态 + 统一处理传输、节点表和连接池。
type Host struct {
	mutex              sync.RWMutex
	peerID             string
	preferredProtocols []utils.MultiAddressProtocol
	dialTimeout        time.Duration
	logger             *slog.Logger
	transports         map[utils.MultiAddressProtocol]Transport
	peers              map[string]Peer
	connections        map[string]Connection
	registry           *ProtocolRegistry
	closed             bool
}

// NewHost 创建 Host + 默认注册 TCP 和 QUIC 传输边界。
func NewHost(config HostConfig, transports ...Transport) (*Host, error) {
	if err := validatePeerID(config.PeerID); err != nil {
		return nil, err
	}

	host := &Host{
		peerID:             config.PeerID,
		preferredProtocols: normalizedProtocolOrder(config.PreferredProtocols),
		dialTimeout:        normalizeDialTimeout(config.DialTimeout),
		logger:             normalizeLogger(config.Logger),
		transports:         make(map[utils.MultiAddressProtocol]Transport),
		peers:              make(map[string]Peer),
		connections:        make(map[string]Connection),
		registry:           normalizeRegistry(config.Registry),
	}

	if len(transports) == 0 {
		transports = []Transport{
			NewQUICTransport(),
			NewTCPTransportWithConfig(TCPTransportConfig{Logger: host.logger}),
		}
	}
	for _, transport := range transports {
		if err := host.RegisterTransport(transport); err != nil {
			return nil, err
		}
	}
	return host, nil
}

// PeerID 返回本地节点 ID + 供消息路由默认填充来源。
func (host *Host) PeerID() string {
	return host.peerID
}

// RegisterTransport 注册传输实现 + 允许按协议替换 TCP 或 QUIC 实现。
func (host *Host) RegisterTransport(transport Transport) error {
	if transport == nil {
		return ErrNilTransport
	}

	host.mutex.Lock()
	defer host.mutex.Unlock()
	if host.closed {
		return ErrHostClosed
	}
	host.transports[transport.Protocol()] = transport
	return nil
}

// RegisterVoidHandler 注册无响应协议 + 将协议处理能力绑定到当前 Host。
func (host *Host) RegisterVoidHandler(spec ProtocolSpec, handler VoidProtocolHandler) error {
	return host.registry.RegisterVoidHandler(spec, handler)
}

// RegisterResultHandler 注册有响应协议 + 将请求响应处理能力绑定到当前 Host。
func (host *Host) RegisterResultHandler(spec ProtocolSpec, handler ResultProtocolHandler) error {
	return host.registry.RegisterResultHandler(spec, handler)
}

// HandleMessage 处理入站消息 + 通过协议注册表执行对应处理器。
func (host *Host) HandleMessage(ctx context.Context, message Message) (ProtocolHandleResult, error) {
	return host.registry.Handle(ctx, message)
}

// AddPeer 添加或更新节点 + 校验地址归属后写入节点表。
func (host *Host) AddPeer(peer Peer) error {
	if err := peer.Validate(); err != nil {
		return err
	}

	host.mutex.Lock()
	defer host.mutex.Unlock()
	if host.closed {
		return ErrHostClosed
	}
	if current, ok := host.peers[peer.ID]; ok {
		if err := current.Merge(peer); err != nil {
			return err
		}
		host.peers[peer.ID] = current
		return nil
	}
	host.peers[peer.ID] = peer.Clone()
	return nil
}

// Peer 查询节点 + 返回副本避免外部修改内部状态。
func (host *Host) Peer(peerID string) (Peer, bool) {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	peer, ok := host.peers[peerID]
	return peer.Clone(), ok
}

// Listen 启动监听 + 根据地址协议选择对应传输实现。
func (host *Host) Listen(ctx context.Context, address utils.MultiAddress, handler ConnectionHandler) error {
	transport, err := host.transport(address.Protocol)
	if err != nil {
		return err
	}
	host.logger.Info("p2p host listen",
		slog.String("address", address.String()),
		slog.String("protocol", string(address.Protocol)),
	)
	return transport.Listen(ctx, address, handler)
}

// DialAddress 拨号指定地址 + 成功后将连接放入连接池。
func (host *Host) DialAddress(ctx context.Context, address utils.MultiAddress) (Connection, error) {
	transport, err := host.transport(address.Protocol)
	if err != nil {
		return nil, err
	}

	dialContext, cancel := host.withDialTimeout(ctx)
	defer cancel()

	connection, err := transport.Dial(dialContext, address)
	if err != nil {
		host.recordPeerError(address.PeerID, err)
		return nil, err
	}
	host.storeConnection(address.PeerID, connection)
	host.logger.Info("p2p host connected",
		slog.String("peer_id", address.PeerID),
		slog.String("protocol", string(address.Protocol)),
	)
	return connection, nil
}

// DialPeer 拨号节点 + 按协议优先级支持 QUIC 到 TCP 的降级。
func (host *Host) DialPeer(ctx context.Context, peerID string) (Connection, error) {
	peer, ok := host.Peer(peerID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPeerNotFound, peerID)
	}

	var dialErrors []error
	for _, protocol := range host.preferredProtocols {
		address, ok := peer.firstAddressByProtocol(protocol)
		if !ok {
			continue
		}
		connection, err := host.DialAddress(ctx, address)
		if err == nil {
			return connection, nil
		}
		dialErrors = append(dialErrors, err)
	}
	if len(dialErrors) == 0 {
		return nil, fmt.Errorf("p2p: dial peer %s: no usable address", peerID)
	}
	return nil, fmt.Errorf("p2p: dial peer %s: %w", peerID, errors.Join(dialErrors...))
}

// Connection 查询连接 + 用于上层复用已经建立的连接。
func (host *Host) Connection(peerID string) (Connection, bool) {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	connection, ok := host.connections[peerID]
	return connection, ok
}

// Send 发送消息到节点 + 自动拨号并补齐消息路由字段。
func (host *Host) Send(ctx context.Context, peerID string, message Message) error {
	connection, ok := host.Connection(peerID)
	if !ok {
		var err error
		connection, err = host.DialPeer(ctx, peerID)
		if err != nil {
			return err
		}
	}

	outbound, err := host.prepareOutboundMessage(peerID, message)
	if err != nil {
		return err
	}
	return connection.WriteMessage(ctx, outbound)
}

// Broadcast 广播消息 + 对多个节点逐个发送并聚合错误。
func (host *Host) Broadcast(ctx context.Context, peerIDs []string, message Message) error {
	var sendErrors []error
	for _, peerID := range peerIDs {
		if err := host.Send(ctx, peerID, message); err != nil {
			sendErrors = append(sendErrors, fmt.Errorf("%s: %w", peerID, err))
		}
	}
	return errors.Join(sendErrors...)
}

// Close 关闭 Host + 释放连接池和全部传输资源。
func (host *Host) Close() error {
	host.mutex.Lock()
	if host.closed {
		host.mutex.Unlock()
		return nil
	}
	host.closed = true
	connections := copyConnections(host.connections)
	transports := copyTransports(host.transports)
	host.connections = make(map[string]Connection)
	for peerID, peer := range host.peers {
		peer.MarkDisconnected()
		host.peers[peerID] = peer
	}
	host.mutex.Unlock()

	var closeErrors []error
	for _, connection := range connections {
		if err := connection.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	for _, transport := range transports {
		if err := transport.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
}
func (host *Host) transport(protocol utils.MultiAddressProtocol) (Transport, error) {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	if host.closed {
		return nil, ErrHostClosed
	}
	transport, ok := host.transports[protocol]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedProtocol, protocol)
	}
	return transport, nil
}
func (host *Host) storeConnection(peerID string, connection Connection) {
	host.mutex.Lock()
	defer host.mutex.Unlock()
	if host.closed {
		_ = connection.Close()
		return
	}
	host.connections[peerID] = connection
	if peer, ok := host.peers[peerID]; ok {
		peer.MarkConnected()
		host.peers[peerID] = peer
	}
}
func (host *Host) recordPeerError(peerID string, err error) {
	host.mutex.Lock()
	defer host.mutex.Unlock()
	if peer, ok := host.peers[peerID]; ok {
		peer.RecordError(err)
		host.peers[peerID] = peer
	}
}
func (host *Host) prepareOutboundMessage(peerID string, message Message) (Message, error) {
	outbound := message.Clone()
	if outbound.ID == "" {
		messageID, err := newMessageID()
		if err != nil {
			return Message{}, err
		}
		outbound.ID = messageID
	}
	if outbound.CreatedAtUnixMilli == 0 {
		outbound.CreatedAtUnixMilli = time.Now().UnixMilli()
	}
	if outbound.FromPeerID == "" {
		outbound.FromPeerID = host.peerID
	}
	if outbound.ToPeerID == "" {
		outbound.ToPeerID = peerID
	}
	if err := outbound.Validate(DefaultMaxMessageSize); err != nil {
		return Message{}, err
	}
	return outbound, nil
}
func (host *Host) withDialTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, host.dialTimeout)
}
func normalizeDialTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultDialTimeout
	}
	return timeout
}
func normalizeLogger(logger *slog.Logger) *slog.Logger {
	return utils.EnsureLogger(logger)
}
func normalizeRegistry(registry *ProtocolRegistry) *ProtocolRegistry {
	if registry != nil {
		return registry
	}
	return NewProtocolRegistry()
}
func copyConnections(source map[string]Connection) []Connection {
	connections := make([]Connection, 0, len(source))
	for _, connection := range source {
		connections = append(connections, connection)
	}
	return connections
}
func copyTransports(source map[utils.MultiAddressProtocol]Transport) []Transport {
	transports := make([]Transport, 0, len(source))
	for _, transport := range source {
		transports = append(transports, transport)
	}
	return transports
}
