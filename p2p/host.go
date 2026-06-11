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

const (
	defaultDialTimeout       = 5 * time.Second
	defaultHeartbeatInterval = 15 * time.Second
	defaultConnectionIdle    = 45 * time.Second
	defaultMaxPeerFailures   = 3
	defaultMaxPeers          = 64
)

// HostConfig 保存 Host 配置 + 支持注入节点身份、协议优先级和日志。
type HostConfig struct {
	PeerID              string
	SecureIdentity      SecureSessionIdentity
	EnableSecureSession bool
	PreferredProtocols  []utils.MultiAddressProtocol
	DialTimeout         time.Duration
	HeartbeatInterval   time.Duration
	ConnectionIdle      time.Duration
	MaxPeerFailures     uint32
	MaxPeers            int
	Logger              *slog.Logger
	Registry            *ProtocolRegistry
	RoutingTable        *KADRoutingTable
	PeerStore           PeerStore
	PersistedPeerLimit  int
}

// Host 管理 P2P 节点运行态 + 统一处理传输、节点表和连接池。
type Host struct {
	mutex              sync.RWMutex
	peerID             string
	secureSession      bool
	secureIdentity     SecureSessionIdentity
	preferredProtocols []utils.MultiAddressProtocol
	dialTimeout        time.Duration
	heartbeatInterval  time.Duration
	connectionIdle     time.Duration
	maxPeerFailures    uint32
	maxPeers           int
	logger             *slog.Logger
	transports         map[utils.MultiAddressProtocol]Transport
	peers              map[string]Peer
	connections        map[string]Connection
	connectionStates   map[string]ConnectionState
	resumptionTickets  map[string]SecureSessionResumptionTicket
	registry           *ProtocolRegistry
	routingTable       *KADRoutingTable
	peerStore          PeerStore
	persistedPeerLimit int
	closed             bool
}

// ConnectionState 保存连接运行态 + 供心跳、监控和故障清理使用。
type ConnectionState struct {
	PeerID                 string
	ConnectionID           string
	Protocol               utils.MultiAddressProtocol
	LocalAddress           string
	RemoteAddress          string
	Encrypted              bool
	NetworkID              string
	RemoteSoftwareVersion  string
	NegotiatedProtocol     uint16
	ConnectedAtUnixMilli   int64
	LastReadUnixMilli      int64
	LastWriteUnixMilli     int64
	LastHeartbeatUnixMilli int64
	FailureCount           uint32
}

// NewHost 创建 Host + 默认注册 TCP 和 QUIC 传输边界。
func NewHost(config HostConfig, transports ...Transport) (*Host, error) {
	if err := validatePeerID(config.PeerID); err != nil {
		return nil, err
	}
	routingTable, err := normalizeRoutingTable(config.RoutingTable, config.PeerID)
	if err != nil {
		return nil, err
	}
	secureSession, secureIdentity, err := normalizeSecureSessionIdentity(config)
	if err != nil {
		return nil, err
	}

	host := &Host{
		peerID:             config.PeerID,
		secureSession:      secureSession,
		secureIdentity:     secureIdentity,
		preferredProtocols: normalizedProtocolOrder(config.PreferredProtocols),
		dialTimeout:        normalizeDialTimeout(config.DialTimeout),
		heartbeatInterval:  normalizeHeartbeatInterval(config.HeartbeatInterval),
		connectionIdle:     normalizeConnectionIdle(config.ConnectionIdle),
		maxPeerFailures:    normalizeMaxPeerFailures(config.MaxPeerFailures),
		maxPeers:           normalizeMaxPeers(config.MaxPeers),
		logger:             normalizeLogger(config.Logger),
		transports:         make(map[utils.MultiAddressProtocol]Transport),
		peers:              make(map[string]Peer),
		connections:        make(map[string]Connection),
		connectionStates:   make(map[string]ConnectionState),
		resumptionTickets:  make(map[string]SecureSessionResumptionTicket),
		registry:           normalizeRegistry(config.Registry),
		routingTable:       routingTable,
		peerStore:          normalizePeerStore(config.PeerStore),
		persistedPeerLimit: normalizePeerStoreLimit(config.PersistedPeerLimit),
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
	if err := host.registerDefaultProtocolHandlers(); err != nil {
		return nil, err
	}
	return host, nil
}

// PeerID 返回本节点身份 + 供消息路由和日志标识使用。
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

// HandleMessage 处理入站消息 + 按消息协议 ID 分发到注册表处理器。
func (host *Host) HandleMessage(ctx context.Context, message Message) (ProtocolHandleResult, error) {
	return host.registry.Handle(ctx, message)
}

func (host *Host) registerDefaultProtocolHandlers() error {
	if _, ok := host.registry.Spec(ProtocolFindNodeRequestV1); ok {
		return nil
	}
	spec := ProtocolSpec{
		ID:          ProtocolFindNodeRequestV1,
		Name:        "/p2p/find-node/request/1.0.0",
		HasResponse: true,
		Priority:    MessagePriorityNormal,
	}
	return host.RegisterResultHandler(spec, host.handleFindNodeRequest)
}

func (host *Host) handleFindNodeRequest(ctx context.Context, message Message) (Message, error) {
	request, err := UnmarshalKADFindNodeRequestBinary(message.Payload)
	if err != nil {
		return Message{}, err
	}
	peers, err := host.ClosestPeers(request.TargetPeerID, int(request.Limit))
	if err != nil {
		return Message{}, err
	}
	responsePayload, err := NewKADFindNodeResponse(request.TargetPeerID, peers)
	if err != nil {
		return Message{}, err
	}
	payload, err := responsePayload.MarshalBinary()
	if err != nil {
		return Message{}, err
	}
	return responseFor(message, host.peerID, ProtocolFindNodeResponseV1, payload)
}

// HandleConnection 管理连接读循环 + 自动处理心跳并分发业务协议。
func (host *Host) HandleConnection(ctx context.Context, connection Connection) {
	defer host.removeConnectionByID(connection.ID())
	defer connection.Close()
	for {
		message, err := connection.ReadMessage(ctx)
		if err != nil {
			host.recordConnectionError(connection, err)
			return
		}
		host.markConnectionRead(connection, message.FromPeerID)
		if host.handleHeartbeatMessage(ctx, connection, message) {
			continue
		}
		result, err := host.HandleMessage(ctx, message)
		if err != nil {
			host.logger.Warn("p2p message rejected",
				slog.String("connection_id", connection.ID()),
				slog.String("message_id", message.ID),
				slog.Any("error", err),
			)
			continue
		}
		if result.HasResponse {
			if err := host.writeConnectionMessage(ctx, connection, message.FromPeerID, result.Message); err != nil {
				host.recordConnectionError(connection, err)
				return
			}
		}
	}
}

// AddPeer 添加或更新节点 + 校验地址归属后写入节点表。
func (host *Host) AddPeer(peer Peer) error {
	return host.addPeer(peer, true)
}

func (host *Host) addPeer(peer Peer, persist bool) error {
	if err := peer.Validate(); err != nil {
		return err
	}

	var storedPeer Peer
	host.mutex.Lock()
	if host.closed {
		host.mutex.Unlock()
		return ErrHostClosed
	}
	if _, ok := host.peers[peer.ID]; !ok && len(host.peers) >= host.maxPeers {
		host.mutex.Unlock()
		return fmt.Errorf("%w: %d", ErrMaxPeersReached, host.maxPeers)
	}
	if current, ok := host.peers[peer.ID]; ok {
		if err := current.Merge(peer); err != nil {
			host.mutex.Unlock()
			return err
		}
		host.peers[peer.ID] = current
		host.addPeerToRoutingTableLocked(current)
		storedPeer = current.Clone()
	} else {
		host.peers[peer.ID] = peer.Clone()
		host.addPeerToRoutingTableLocked(peer)
		storedPeer = peer.Clone()
	}
	host.mutex.Unlock()

	if persist {
		return host.savePeer(context.Background(), storedPeer)
	}
	return nil
}

// Peer 查询节点 + 返回副本避免外部修改内部状态。
func (host *Host) Peer(peerID string) (Peer, bool) {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	peer, ok := host.peers[peerID]
	return peer.Clone(), ok
}

// ClosestPeers 查询 KAD 最近节点 + 用于 find-node 协议和连接候选选择。
func (host *Host) ClosestPeers(targetPeerID string, limit int) ([]Peer, error) {
	if err := validateKADRoutingTable(host.routingTable); err != nil {
		return nil, err
	}
	return host.routingTable.ClosestPeers(targetPeerID, limit)
}

// RoutingTableHealth 查询 KAD 健康状态 + 供监控和调试使用。
func (host *Host) RoutingTableHealth() KADRoutingTableHealthSnapshot {
	if host.routingTable == nil {
		return KADRoutingTableHealthSnapshot{}
	}
	return host.routingTable.HealthSnapshot()
}

// Listen 启动监听 + 根据地址协议选择对应传输实现。
// LoadStoredPeers 恢复持久化节点 + 启动时先填充 peer 表和 KAD 路由表。
func (host *Host) LoadStoredPeers(ctx context.Context, limit int) (int, error) {
	if host.peerStore == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = host.persistedPeerLimit
	}
	peers, err := host.peerStore.LoadPeers(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("p2p: load stored peers: %w", err)
	}

	loaded := 0
	for _, peer := range peers {
		if peer.ID == host.peerID {
			continue
		}
		if err := host.addPeer(peer, false); err != nil {
			return loaded, fmt.Errorf("p2p: restore peer %s: %w", peer.ID, err)
		}
		loaded++
	}
	return loaded, nil
}

func (host *Host) Listen(ctx context.Context, address utils.MultiAddress, handler ConnectionHandler) error {
	transport, err := host.transport(address.Protocol)
	if err != nil {
		return err
	}
	host.logger.Info("p2p host listen",
		slog.String("address", address.String()),
		slog.String("protocol", string(address.Protocol)),
	)
	return transport.Listen(ctx, address, host.secureConnectionHandler(handler))
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
	securedConnection, err := host.secureOutboundConnection(dialContext, connection)
	if err != nil {
		_ = connection.Close()
		host.recordPeerError(address.PeerID, err)
		return nil, err
	}
	connection = securedConnection
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

	addresses := host.dialCandidateAddresses(peer)
	var dialErrors []error
	for index, address := range addresses {
		attemptContext, cancel := host.withDialAttemptTimeout(ctx, len(addresses)-index)
		connection, err := host.DialAddress(attemptContext, address)
		cancel()
		if err == nil {
			go host.HandleConnection(context.Background(), connection)
			return connection, nil
		}
		dialErrors = append(dialErrors, err)
	}
	if len(dialErrors) == 0 {
		return nil, fmt.Errorf("p2p: dial peer %s: no usable address", peerID)
	}
	return nil, fmt.Errorf("p2p: dial peer %s: %w", peerID, errors.Join(dialErrors...))
}

// dialCandidateAddresses 生成拨号候选地址 + 按协议优先级支持 QUIC 到 TCP 降级。
func (host *Host) dialCandidateAddresses(peer Peer) []utils.MultiAddress {
	if !peerDialable(peer, host.maxPeerFailures) {
		return nil
	}
	addresses := make([]utils.MultiAddress, 0, len(host.preferredProtocols))
	for _, protocol := range host.preferredProtocols {
		address, ok := peer.firstAddressByProtocol(protocol)
		if ok {
			addresses = append(addresses, address)
		}
	}
	return addresses
}

// Connection 查询已建立连接 + 只暴露连接接口不暴露内部连接池。
func (host *Host) Connection(peerID string) (Connection, bool) {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	connection, ok := host.connections[peerID]
	return connection, ok
}

// ConnectionCount 返回当前连接数量 + 供 bootstrap 判断是否需要补足出站连接。
func (host *Host) ConnectionCount() int {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	return len(host.connections)
}

func (host *Host) hasConnection(peerID string) bool {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	_, ok := host.connections[peerID]
	return ok
}

// ConnectionState 查询连接状态 + 返回副本避免外部修改内部状态。
func (host *Host) ConnectionState(peerID string) (ConnectionState, bool) {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	state, ok := host.connectionStates[peerID]
	return state, ok
}

// SecureSessionTicket 查询安全会话恢复票据 + 返回副本避免外部修改 Host 内部恢复材料。
func (host *Host) SecureSessionTicket(peerID string) (SecureSessionResumptionTicket, bool) {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	ticket, ok := host.resumptionTickets[peerID]
	return ticket.Clone(), ok
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
	return host.writeConnectionMessage(ctx, connection, peerID, outbound)
}

// StartHeartbeat 启动心跳循环 + 定期探活并清理失效连接。
func (host *Host) StartHeartbeat(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(host.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			host.heartbeatOnce(ctx)
		}
	}
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
	storedPeers := make([]Peer, 0, len(host.peers))
	host.connections = make(map[string]Connection)
	host.connectionStates = make(map[string]ConnectionState)
	host.resumptionTickets = make(map[string]SecureSessionResumptionTicket)
	for peerID, peer := range host.peers {
		peer.MarkDisconnected()
		host.peers[peerID] = peer
		storedPeers = append(storedPeers, peer.Clone())
	}
	host.mutex.Unlock()

	for _, peer := range storedPeers {
		host.savePeerBestEffort(peer)
	}

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

// transport 获取指定协议传输 + 持读锁同时检查 Host 是否已关闭。
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

// storeConnection 写入连接池 + 连接建立成功后同步更新节点在线状态。
func (host *Host) addPeerToRoutingTableLocked(peer Peer) {
	if !peerShareableInDHT(peer) {
		return
	}
	_ = host.routingTable.AddPeer(peer)
}

func (host *Host) savePeer(ctx context.Context, peer Peer) error {
	if host.peerStore == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := host.peerStore.SavePeer(ctx, peer); err != nil {
		return fmt.Errorf("p2p: save peer %s: %w", peer.ID, err)
	}
	return nil
}

func (host *Host) savePeerBestEffort(peer Peer) {
	if err := host.savePeer(context.Background(), peer); err != nil {
		host.logger.Warn("p2p peer store save failed",
			slog.String("peer_id", peer.ID),
			slog.Any("error", err),
		)
	}
}

func (host *Host) storeConnection(peerID string, connection Connection) {
	var storedPeer Peer
	shouldPersist := false
	host.mutex.Lock()
	if host.closed {
		host.mutex.Unlock()
		_ = connection.Close()
		return
	}
	if existing := host.connections[peerID]; existing != nil && existing.ID() != connection.ID() {
		_ = existing.Close()
	}
	host.connections[peerID] = connection
	now := time.Now().UnixMilli()
	security := secureConnectionState(connection)
	host.connectionStates[peerID] = ConnectionState{
		PeerID:                peerID,
		ConnectionID:          connection.ID(),
		Protocol:              connection.Protocol(),
		LocalAddress:          connection.LocalAddress(),
		RemoteAddress:         connection.RemoteAddress(),
		Encrypted:             security.encrypted,
		NetworkID:             security.networkID,
		RemoteSoftwareVersion: security.remoteSoftwareVersion,
		NegotiatedProtocol:    security.protocolVersion,
		ConnectedAtUnixMilli:  now,
		LastReadUnixMilli:     now,
		LastWriteUnixMilli:    now,
	}
	host.storeResumptionTicketLocked(connection)
	if peer, ok := host.peers[peerID]; ok {
		peer.MarkConnected()
		host.peers[peerID] = peer
		host.addPeerToRoutingTableLocked(peer)
		storedPeer = peer.Clone()
		shouldPersist = true
	} else if peerID != "" && len(host.peers) < host.maxPeers {
		peer, err := NewPeer(peerID, nil)
		if err == nil {
			peer.MarkConnected()
			host.peers[peerID] = peer
			storedPeer = peer.Clone()
			shouldPersist = true
		}
	}
	host.mutex.Unlock()

	if shouldPersist {
		host.savePeerBestEffort(storedPeer)
	}
}

// writeConnectionMessage 写入连接消息 + 同步更新连接活跃时间和错误计数。
func (host *Host) writeConnectionMessage(ctx context.Context, connection Connection, peerID string, message Message) error {
	if err := connection.WriteMessage(ctx, message); err != nil {
		host.recordConnectionError(connection, err)
		return err
	}
	host.markConnectionWrite(connection, peerID)
	return nil
}

// handleHeartbeatMessage 处理心跳消息 + ping 立即回复 pong，pong 仅刷新活跃时间。
func (host *Host) handleHeartbeatMessage(ctx context.Context, connection Connection, message Message) bool {
	if message.Type == MessageTypePong {
		return true
	}
	if message.Type != MessageTypePing {
		return false
	}
	response, err := responseFor(message, host.peerID, MessageTypePong, nil)
	if err != nil {
		host.recordConnectionError(connection, err)
		return true
	}
	if err := host.writeConnectionMessage(ctx, connection, message.FromPeerID, response); err != nil {
		host.recordConnectionError(connection, err)
	}
	return true
}

// heartbeatOnce 执行一次心跳检查 + 向活跃连接发送 ping 并清理超时连接。
func (host *Host) heartbeatOnce(ctx context.Context) {
	connections := host.connectionSnapshots()
	now := time.Now()
	for peerID, connection := range connections {
		if host.connectionExpired(peerID, now) {
			host.closePeerConnection(peerID)
			continue
		}
		message, err := NewRequestMessage(host.peerID, MessageTypePing, nil)
		if err != nil {
			host.recordPeerError(peerID, err)
			continue
		}
		message.ToPeerID = peerID
		writeContext, cancel := context.WithTimeout(ctx, host.dialTimeout)
		err = host.writeConnectionMessage(writeContext, connection, peerID, message)
		cancel()
		if err != nil {
			host.logger.Warn("p2p heartbeat failed", slog.String("peer_id", peerID), slog.Any("error", err))
			host.closePeerConnection(peerID)
			continue
		}
		host.markHeartbeat(peerID)
	}
}

// connectionSnapshots 复制连接池 + 避免心跳持锁执行网络写入。
func (host *Host) connectionSnapshots() map[string]Connection {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	connections := make(map[string]Connection, len(host.connections))
	for peerID, connection := range host.connections {
		connections[peerID] = connection
	}
	return connections
}

// connectionExpired 判断连接是否过期 + 结合空闲时间和连续失败次数。
func (host *Host) connectionExpired(peerID string, now time.Time) bool {
	host.mutex.RLock()
	state, ok := host.connectionStates[peerID]
	host.mutex.RUnlock()
	if !ok {
		return true
	}
	if state.FailureCount >= host.maxPeerFailures {
		return true
	}
	lastRead := state.LastReadUnixMilli
	if lastRead == 0 {
		lastRead = state.ConnectedAtUnixMilli
	}
	return now.Sub(time.UnixMilli(lastRead)) > host.connectionIdle
}

// markConnectionRead 刷新读活跃时间 + 首次识别远端节点后写入连接池。
func (host *Host) markConnectionRead(connection Connection, peerID string) {
	if peerID == "" {
		peerID = connection.RemotePeerID()
	}
	if peerID == "" {
		return
	}
	host.mutex.Lock()
	defer host.mutex.Unlock()
	if host.closed {
		return
	}
	if _, ok := host.connections[peerID]; !ok {
		host.connections[peerID] = connection
	}
	state := host.connectionStates[peerID]
	if state.PeerID == "" {
		now := time.Now().UnixMilli()
		security := secureConnectionState(connection)
		state = ConnectionState{
			PeerID:                peerID,
			ConnectionID:          connection.ID(),
			Protocol:              connection.Protocol(),
			LocalAddress:          connection.LocalAddress(),
			RemoteAddress:         connection.RemoteAddress(),
			Encrypted:             security.encrypted,
			NetworkID:             security.networkID,
			RemoteSoftwareVersion: security.remoteSoftwareVersion,
			NegotiatedProtocol:    security.protocolVersion,
			ConnectedAtUnixMilli:  now,
		}
	}
	state.LastReadUnixMilli = time.Now().UnixMilli()
	state.FailureCount = 0
	host.connectionStates[peerID] = state
	host.storeResumptionTicketLocked(connection)
	if peer, ok := host.peers[peerID]; ok {
		peer.MarkConnected()
		host.peers[peerID] = peer
		host.addPeerToRoutingTableLocked(peer)
		host.routingTable.TouchPeer(peerID)
	}
}

// markConnectionWrite 刷新写活跃时间 + 成功发送后清零连续失败计数。
func (host *Host) markConnectionWrite(connection Connection, peerID string) {
	if peerID == "" {
		peerID = connection.RemotePeerID()
	}
	if peerID == "" {
		return
	}
	host.mutex.Lock()
	defer host.mutex.Unlock()
	state := host.connectionStates[peerID]
	if state.PeerID == "" {
		now := time.Now().UnixMilli()
		security := secureConnectionState(connection)
		state = ConnectionState{
			PeerID:                peerID,
			ConnectionID:          connection.ID(),
			Protocol:              connection.Protocol(),
			LocalAddress:          connection.LocalAddress(),
			RemoteAddress:         connection.RemoteAddress(),
			Encrypted:             security.encrypted,
			NetworkID:             security.networkID,
			RemoteSoftwareVersion: security.remoteSoftwareVersion,
			NegotiatedProtocol:    security.protocolVersion,
			ConnectedAtUnixMilli:  now,
		}
	}
	state.LastWriteUnixMilli = time.Now().UnixMilli()
	state.FailureCount = 0
	host.connectionStates[peerID] = state
}

// markHeartbeat 记录心跳发送时间 + 便于监控连接活跃度。
func (host *Host) markHeartbeat(peerID string) {
	host.mutex.Lock()
	defer host.mutex.Unlock()
	state := host.connectionStates[peerID]
	state.LastHeartbeatUnixMilli = time.Now().UnixMilli()
	host.connectionStates[peerID] = state
}

// recordConnectionError 记录连接错误 + 达到阈值后由心跳清理。
func (host *Host) recordConnectionError(connection Connection, err error) {
	if err == nil {
		return
	}
	var storedPeer Peer
	shouldPersist := false
	host.mutex.Lock()
	for peerID, state := range host.connectionStates {
		if state.ConnectionID != connection.ID() {
			continue
		}
		state.FailureCount++
		host.connectionStates[peerID] = state
		if peer, ok := host.peers[peerID]; ok {
			peer.RecordError(err)
			host.peers[peerID] = peer
			storedPeer = peer.Clone()
			shouldPersist = true
		}
		host.routingTable.RecordPeerFailure(peerID)
		host.mutex.Unlock()
		if shouldPersist {
			host.savePeerBestEffort(storedPeer)
		}
		return
	}
	host.mutex.Unlock()
}

// removeConnectionByID 移除指定连接 + 读循环退出时保持连接池准确。
func (host *Host) removeConnectionByID(connectionID string) {
	var storedPeer Peer
	shouldPersist := false
	host.mutex.Lock()
	for peerID, connection := range host.connections {
		if connection.ID() != connectionID {
			continue
		}
		delete(host.connections, peerID)
		delete(host.connectionStates, peerID)
		if peer, ok := host.peers[peerID]; ok {
			peer.MarkDisconnected()
			host.peers[peerID] = peer
			host.addPeerToRoutingTableLocked(peer)
			storedPeer = peer.Clone()
			shouldPersist = true
		}
		host.mutex.Unlock()
		if shouldPersist {
			host.savePeerBestEffort(storedPeer)
		}
		return
	}
	host.mutex.Unlock()
}

// closePeerConnection 关闭并移除节点连接 + 心跳失败和过期清理共用。
func (host *Host) closePeerConnection(peerID string) {
	var storedPeer Peer
	shouldPersist := false
	host.mutex.Lock()
	connection := host.connections[peerID]
	delete(host.connections, peerID)
	delete(host.connectionStates, peerID)
	if peer, ok := host.peers[peerID]; ok {
		peer.MarkDisconnected()
		host.peers[peerID] = peer
		host.addPeerToRoutingTableLocked(peer)
		storedPeer = peer.Clone()
		shouldPersist = true
	}
	host.mutex.Unlock()
	if shouldPersist {
		host.savePeerBestEffort(storedPeer)
	}
	if connection != nil {
		_ = connection.Close()
	}
}

// recordPeerError 记录节点错误 + 将拨号失败沉淀到节点快照便于诊断。
func (host *Host) recordPeerError(peerID string, err error) {
	var storedPeer Peer
	shouldPersist := false
	host.mutex.Lock()
	if peer, ok := host.peers[peerID]; ok {
		peer.RecordError(err)
		host.peers[peerID] = peer
		storedPeer = peer.Clone()
		shouldPersist = true
	}
	host.routingTable.RecordPeerFailure(peerID)
	host.mutex.Unlock()

	if shouldPersist {
		host.savePeerBestEffort(storedPeer)
	}
}

// prepareOutboundMessage 补齐出站消息路由字段 + 发送前统一做协议边界校验。
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
	if outbound.Flag == MessageFlagUnknown && outbound.RequestID == "" {
		outbound.MarkAsNormal()
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

// withDialTimeout 构造拨号上下文 + 调用方未设置截止时间时使用 Host 默认超时。
func (host *Host) withDialTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, host.dialTimeout)
}

// withDialAttemptTimeout 构造单次拨号上下文 + 保留后续协议降级所需时间。
func (host *Host) withDialAttemptTimeout(ctx context.Context, remainingAttempts int) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if remainingAttempts <= 1 {
		return host.withDialTimeout(ctx)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithTimeout(ctx, host.dialTimeout/time.Duration(remainingAttempts))
	}
	remainingTime := time.Until(deadline)
	if remainingTime <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, remainingTime/time.Duration(remainingAttempts))
}

// normalizeDialTimeout 归一化拨号超时 + 防止零值导致拨号永久阻塞。
func normalizeDialTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultDialTimeout
	}
	return timeout
}

// normalizeHeartbeatInterval 归一化心跳间隔 + 防止零值导致后台循环异常。
func normalizeHeartbeatInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return defaultHeartbeatInterval
	}
	return interval
}

// normalizeConnectionIdle 归一化连接空闲时间 + 保证至少大于心跳间隔。
func normalizeConnectionIdle(idle time.Duration) time.Duration {
	if idle <= 0 {
		return defaultConnectionIdle
	}
	return idle
}

func normalizeSecureSessionIdentity(config HostConfig) (bool, SecureSessionIdentity, error) {
	enabled := config.EnableSecureSession || hasSecureSessionIdentity(config.SecureIdentity)
	if !enabled {
		return false, SecureSessionIdentity{}, nil
	}
	if config.SecureIdentity.PeerID != config.PeerID {
		return false, SecureSessionIdentity{}, fmt.Errorf("%w: secure identity peer id mismatch", ErrSecureSession)
	}
	if err := config.SecureIdentity.Validate(); err != nil {
		return false, SecureSessionIdentity{}, err
	}
	return true, config.SecureIdentity.Clone(), nil
}

func hasSecureSessionIdentity(identity SecureSessionIdentity) bool {
	return identity.PeerID != "" ||
		len(identity.PublicKey) > 0 ||
		len(identity.PrivateKey) > 0 ||
		identity.NetworkID != "" ||
		identity.SoftwareVersion != ""
}

func (host *Host) secureConnectionHandler(handler ConnectionHandler) ConnectionHandler {
	if handler == nil {
		return nil
	}
	if !host.secureSession {
		return handler
	}
	return func(ctx context.Context, connection Connection) {
		secureConnection, err := SecureAcceptConnection(ctx, connection, host.secureIdentity)
		if err != nil {
			host.logger.Warn("p2p secure session accept failed",
				slog.String("connection_id", connection.ID()),
				slog.Any("error", err),
			)
			_ = connection.Close()
			return
		}
		host.storeConnection(secureConnection.RemotePeerID(), secureConnection)
		handler(ctx, secureConnection)
	}
}

func (host *Host) secureOutboundConnection(ctx context.Context, connection Connection) (Connection, error) {
	if !host.secureSession {
		return connection, nil
	}
	secureConnection, err := SecureDialConnection(ctx, connection, host.secureIdentity)
	if err != nil {
		return nil, err
	}
	return secureConnection, nil
}

func (host *Host) storeResumptionTicketLocked(connection Connection) {
	secureConnection, ok := connection.(*SecureConnection)
	if !ok {
		return
	}
	ticket, err := secureConnection.Session().ResumptionTicket()
	if err != nil {
		return
	}
	host.resumptionTickets[ticket.RemotePeerID] = ticket
}

type secureConnectionStateSnapshot struct {
	encrypted             bool
	networkID             string
	remoteSoftwareVersion string
	protocolVersion       uint16
}

func secureConnectionState(connection Connection) secureConnectionStateSnapshot {
	secureConnection, ok := connection.(*SecureConnection)
	if !ok {
		return secureConnectionStateSnapshot{}
	}
	session := secureConnection.Session()
	return secureConnectionStateSnapshot{
		encrypted:             true,
		networkID:             session.NetworkID(),
		remoteSoftwareVersion: session.RemoteSoftwareVersion(),
		protocolVersion:       session.ProtocolVersion(),
	}
}

// normalizeMaxPeerFailures 归一化失败阈值 + 防止零值导致首次错误即永久不可用。
func normalizeMaxPeerFailures(maxFailures uint32) uint32 {
	if maxFailures == 0 {
		return defaultMaxPeerFailures
	}
	return maxFailures
}

func normalizeMaxPeers(maxPeers int) int {
	if maxPeers <= 0 {
		return defaultMaxPeers
	}
	return maxPeers
}

// normalizeLogger 归一化日志器 + 使用默认日志器避免空指针分支散落业务代码。
func normalizeLogger(logger *slog.Logger) *slog.Logger {
	return utils.EnsureLogger(logger)
}

// normalizeRegistry 归一化协议注册表 + 允许测试注入同时保证生产默认可用。
func normalizeRegistry(registry *ProtocolRegistry) *ProtocolRegistry {
	if registry != nil {
		return registry
	}
	return NewProtocolRegistry()
}

// normalizeRoutingTable 归一化 KAD 路由表 + 默认启用本节点路由表。
func normalizeRoutingTable(table *KADRoutingTable, peerID string) (*KADRoutingTable, error) {
	if table != nil {
		return table, nil
	}
	return NewKADRoutingTable(KADRoutingTableConfig{LocalPeerID: peerID})
}

// copyConnections 复制连接集合 + 缩短 Host 锁持有时间后再关闭连接。
func copyConnections(source map[string]Connection) []Connection {
	connections := make([]Connection, 0, len(source))
	for _, connection := range source {
		connections = append(connections, connection)
	}
	return connections
}

// copyTransports 复制传输集合 + 缩短 Host 锁持有时间后再关闭监听资源。
func copyTransports(source map[utils.MultiAddressProtocol]Transport) []Transport {
	transports := make([]Transport, 0, len(source))
	for _, transport := range source {
		transports = append(transports, transport)
	}
	return transports
}
