package p2p

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"
)

const replacedConnectionCloseGraceMax = 10 * time.Second

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

func (host *Host) peerIDByConnectionID(connectionID string) (string, bool) {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	peerID, ok := host.connectionPeerIDs[connectionID]
	return peerID, ok
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

// Close 关闭 Host + 释放连接池和全部传输资源。
func (host *Host) Close() error {
	host.mutex.Lock()
	if host.closed {
		host.mutex.Unlock()
		return nil
	}
	host.closed = true
	host.lifecycleCancel()
	connections := copyConnections(host.connections)
	transports := copyTransports(host.transports)
	storedPeers := make([]Peer, 0, len(host.peers))
	host.connections = make(map[string]Connection)
	host.connectionPeerIDs = make(map[string]string)
	host.connectionStates = make(map[string]ConnectionState)
	host.resumptionTickets = make(map[string]SecureSessionResumptionTicket)
	for peerID, peer := range host.peers {
		if peer.Status != PeerStatusBlocked {
			peer.MarkDisconnected()
		}
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

// storeConnection 写入连接池 + 连接建立成功后同步更新节点在线状态。
func (host *Host) storeConnection(peerID string, connection Connection) error {
	if connection == nil {
		return fmt.Errorf("%w: nil connection", ErrConnectionClosed)
	}
	var storedPeer Peer
	var replacedConnection Connection
	shouldPersist := false
	host.mutex.Lock()
	if host.closed {
		host.mutex.Unlock()
		return ErrHostClosed
	}
	if peerID == "" {
		host.mutex.Unlock()
		return fmt.Errorf("%w: empty connection peer id", ErrInvalidMessage)
	}
	existing := host.connections[peerID]
	if existing == nil && len(host.connections) >= host.maxConnections {
		host.mutex.Unlock()
		host.metrics.maxConnectionRejected.Add(1)
		return fmt.Errorf("%w: %d", ErrMaxConnectionsReached, host.maxConnections)
	}
	if existing == nil && host.remoteIPConnectionCountLocked(connection.RemoteAddress()) >= host.maxConnectionsPerIP {
		host.mutex.Unlock()
		host.metrics.perIPRejected.Add(1)
		return fmt.Errorf("%w: %d", ErrPeerIPLimitReached, host.maxConnectionsPerIP)
	}
	if _, ok := host.peers[peerID]; !ok && len(host.peers) >= host.maxPeers {
		host.mutex.Unlock()
		return fmt.Errorf("%w: %d", ErrMaxPeersReached, host.maxPeers)
	}
	if existing != nil && existing.ID() == connection.ID() {
		connection = existing
	} else if existing != nil {
		if keepExistingConnection(host.peerID, peerID, existing, connection) {
			host.mutex.Unlock()
			return fmt.Errorf("%w: %s", ErrDuplicateConnection, peerID)
		}
		replacedConnection = existing
		delete(host.connectionPeerIDs, existing.ID())
	}
	if replacedConnection != nil || existing == nil {
		connection = host.wrapConnectionWriter(connection)
	}
	host.connections[peerID] = connection
	host.connectionPeerIDs[connection.ID()] = peerID
	now := time.Now().UnixMilli()
	security := secureConnectionState(connection)
	host.connectionStates[peerID] = ConnectionState{
		PeerID:                peerID,
		ConnectionID:          connection.ID(),
		Protocol:              connection.Protocol(),
		LocalAddress:          connection.LocalAddress(),
		ObservedRemoteAddress: connection.RemoteAddress(),
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

	if replacedConnection != nil {
		host.closeReplacedConnection(replacedConnection)
	}
	if shouldPersist {
		host.savePeerBestEffort(storedPeer)
	}
	return nil
}

// closeReplacedConnection 延迟关闭被替换连接 + 给已进入旧连接 writer 的请求留出完成窗口。
func (host *Host) closeReplacedConnection(connection Connection) {
	if connection == nil {
		return
	}
	grace := host.writeTimeout
	if grace <= 0 || grace > replacedConnectionCloseGraceMax {
		grace = replacedConnectionCloseGraceMax
	}
	go func() {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-host.lifecycleContext.Done():
		}
		_ = connection.Close()
	}()
}

func keepExistingConnection(localPeerID string, remotePeerID string, existing Connection, candidate Connection) bool {
	if existing == nil || candidate == nil {
		return false
	}
	if existing.ID() == candidate.ID() {
		return true
	}

	existingRole, existingSecure := secureConnectionRole(existing)
	candidateRole, candidateSecure := secureConnectionRole(candidate)
	if existingSecure && !candidateSecure {
		return true
	}
	if candidateSecure && !existingSecure {
		return false
	}
	if !existingSecure || !candidateSecure {
		return true
	}

	existingPreferred := preferredSecureConnectionRole(localPeerID, remotePeerID, existingRole)
	candidatePreferred := preferredSecureConnectionRole(localPeerID, remotePeerID, candidateRole)
	if candidatePreferred && !existingPreferred {
		return false
	}
	return true
}

func secureConnectionRole(connection Connection) (SecureSessionRole, bool) {
	secureConnection, ok := unwrapSecureConnection(connection)
	if !ok {
		return SecureSessionRoleUnknown, false
	}
	return secureConnection.Session().Role(), true
}

func preferredSecureConnectionRole(localPeerID string, remotePeerID string, role SecureSessionRole) bool {
	if localPeerID == "" || remotePeerID == "" || localPeerID == remotePeerID {
		return false
	}
	if localPeerID < remotePeerID {
		return role == SecureSessionRoleInitiator
	}
	return role == SecureSessionRoleResponder
}

func (host *Host) wrapConnectionWriter(connection Connection) Connection {
	return newQueuedConnection(connection, queuedConnectionConfig{
		queueSize:    host.writeQueueSize,
		writeTimeout: host.writeTimeout,
		metrics:      &host.metrics,
		logger:       host.logger,
		priority:     host.messagePriority,
		concurrency:  host.messageConcurrency,
		partitionKey: host.messagePartitionKey,
		onWrite:      host.recordConnectionWrite,
		onError:      host.recordConnectionError,
	})
}

func (host *Host) connectionWriterFor(connection Connection) Connection {
	if connection == nil {
		return nil
	}
	if _, ok := connection.(*queuedConnection); ok {
		return connection
	}
	peerID := connection.RemotePeerID()
	if peerID == "" {
		return host.wrapConnectionWriter(connection)
	}
	host.mutex.RLock()
	storedConnection := host.connections[peerID]
	host.mutex.RUnlock()
	if storedConnection != nil && storedConnection.ID() == connection.ID() {
		return storedConnection
	}
	return host.wrapConnectionWriter(connection)
}

func (host *Host) messagePriority(message Message) MessagePriority {
	spec, ok := host.registry.Spec(message.Type)
	if !ok {
		return defaultProtocolPriority(message.Type)
	}
	return spec.Priority
}

func (host *Host) messageConcurrency(message Message) ProtocolConcurrencyMode {
	spec, ok := host.registry.Spec(message.Type)
	if !ok {
		return defaultProtocolConcurrency(message.Type)
	}
	return normalizeProtocolConcurrency(spec.Concurrency)
}

func (host *Host) messagePartitionKey(message Message) string {
	spec, ok := host.registry.Spec(message.Type)
	if !ok {
		return ""
	}
	shardKey, ok := protocolParallelShardKey(message, spec)
	if !ok {
		return ""
	}
	return shardKey
}

func (host *Host) messageTrafficClass(message Message) ProtocolClass {
	spec, ok := host.registry.Spec(message.Type)
	if !ok {
		return defaultProtocolClass(message.Type)
	}
	return spec.EffectiveClass()
}

func (host *Host) writeQueueDepth() uint64 {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	total := uint64(0)
	for _, connection := range host.connections {
		if queued, ok := connection.(*queuedConnection); ok {
			total += queued.queueDepth()
		}
	}
	return total
}

func (host *Host) recordConnectionWrite(connection Connection, message Message) {
	host.metrics.messagesWritten.Add(1)
	host.markConnectionWrite(connection, message.ToPeerID)
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

// markConnectionRead 刷新读活跃时间 + 首次识别远端节点后写入连接池。
func (host *Host) markConnectionRead(connection Connection, peerID string) error {
	if peerID == "" {
		peerID = connection.RemotePeerID()
	}
	if peerID == "" {
		return nil
	}
	host.mutex.Lock()
	defer host.mutex.Unlock()
	if host.closed {
		return ErrHostClosed
	}
	if _, ok := host.connections[peerID]; !ok {
		if len(host.connections) >= host.maxConnections {
			host.metrics.maxConnectionRejected.Add(1)
			return fmt.Errorf("%w: %d", ErrMaxConnectionsReached, host.maxConnections)
		}
		if host.remoteIPConnectionCountLocked(connection.RemoteAddress()) >= host.maxConnectionsPerIP {
			host.metrics.perIPRejected.Add(1)
			return fmt.Errorf("%w: %d", ErrPeerIPLimitReached, host.maxConnectionsPerIP)
		}
		if _, exists := host.peers[peerID]; !exists && len(host.peers) >= host.maxPeers {
			return fmt.Errorf("%w: %d", ErrMaxPeersReached, host.maxPeers)
		}
		host.connections[peerID] = connection
	}
	host.connectionPeerIDs[connection.ID()] = peerID
	state := host.connectionStates[peerID]
	if state.PeerID == "" {
		now := time.Now().UnixMilli()
		security := secureConnectionState(connection)
		state = ConnectionState{
			PeerID:                peerID,
			ConnectionID:          connection.ID(),
			Protocol:              connection.Protocol(),
			LocalAddress:          connection.LocalAddress(),
			ObservedRemoteAddress: connection.RemoteAddress(),
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
	return nil
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
	host.connectionPeerIDs[connection.ID()] = peerID
	state := host.connectionStates[peerID]
	if state.PeerID == "" {
		now := time.Now().UnixMilli()
		security := secureConnectionState(connection)
		state = ConnectionState{
			PeerID:                peerID,
			ConnectionID:          connection.ID(),
			Protocol:              connection.Protocol(),
			LocalAddress:          connection.LocalAddress(),
			ObservedRemoteAddress: connection.RemoteAddress(),
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

// remoteIPConnectionCountLocked 统计同 IP 连接数 + 在持有 Host 锁时保护连接池容量。
func (host *Host) remoteIPConnectionCountLocked(remoteAddress string) int {
	remoteIP := remoteIPFromConnectionAddress(remoteAddress)
	if remoteIP == "" || host.maxConnectionsPerIP <= 0 {
		return 0
	}
	count := 0
	for _, state := range host.connectionStates {
		if remoteIPFromConnectionAddress(state.observedRemoteAddress()) == remoteIP {
			count++
		}
	}
	return count
}

func (state ConnectionState) observedRemoteAddress() string {
	if state.ObservedRemoteAddress != "" {
		return state.ObservedRemoteAddress
	}
	return state.RemoteAddress
}

func remoteIPFromConnectionAddress(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		parsedIP := net.ParseIP(remoteAddress)
		if parsedIP == nil {
			return ""
		}
		return parsedIP.String()
	}
	parsedIP := net.ParseIP(host)
	if parsedIP == nil {
		return ""
	}
	return parsedIP.String()
}

// recordConnectionError 记录连接错误 + 达到阈值后由心跳清理异常连接。
func (host *Host) recordConnectionError(connection Connection, err error) {
	if err == nil || connection == nil {
		return
	}
	if errors.Is(err, ErrWriteQueueFull) {
		return
	}
	unexpectedClose := !isExpectedConnectionClose(err)
	var storedPeer Peer
	shouldPersist := false
	connectionID := connection.ID()
	host.mutex.Lock()
	peerID := host.connectionPeerIDs[connectionID]
	state := host.connectionStates[peerID]
	if peerID == "" || state.ConnectionID != connectionID {
		host.mutex.Unlock()
		return
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
	if unexpectedClose {
		host.penalizePeer(peerID, host.peerProtection.config.InvalidMessagePenalty, "connection-error")
		host.logger.Warn("p2p connection error recorded",
			slog.String("peer_id", peerID),
			slog.String("connection_id", connectionID),
			slog.Uint64("failure_count", uint64(state.FailureCount)),
			slog.Any("error", err),
		)
		host.closePeerConnection(peerID)
	}
}

// removeConnectionByID 移除指定连接 + 读循环退出时保持连接池准确。
func (host *Host) removeConnectionByID(connectionID string) {
	var storedPeer Peer
	shouldPersist := false
	host.mutex.Lock()
	peerID := host.connectionPeerIDs[connectionID]
	if peerID == "" {
		host.mutex.Unlock()
		return
	}
	delete(host.connections, peerID)
	delete(host.connectionStates, peerID)
	delete(host.connectionPeerIDs, connectionID)
	if peer, ok := host.peers[peerID]; ok {
		if peer.Status != PeerStatusBlocked {
			peer.MarkDisconnected()
		}
		host.peers[peerID] = peer
		host.addPeerToRoutingTableLocked(peer)
		storedPeer = peer.Clone()
		shouldPersist = true
	}
	host.mutex.Unlock()
	host.logger.Debug("p2p connection removed",
		slog.String("peer_id", peerID),
		slog.String("connection_id", connectionID),
	)
	if shouldPersist {
		host.savePeerBestEffort(storedPeer)
	}
}

// closePeerConnection 关闭并移除节点连接 + 心跳失败和过期清理共用。
func (host *Host) closePeerConnection(peerID string) {
	var storedPeer Peer
	shouldPersist := false
	host.mutex.Lock()
	connection := host.connections[peerID]
	delete(host.connections, peerID)
	delete(host.connectionStates, peerID)
	if connection != nil {
		delete(host.connectionPeerIDs, connection.ID())
	}
	if peer, ok := host.peers[peerID]; ok {
		if peer.Status != PeerStatusBlocked {
			peer.MarkDisconnected()
		}
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
		host.logger.Info("p2p peer connection closed", slog.String("peer_id", peerID))
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

// recordPeerConnectionSuccess 标记连接已收敛 + 清理并发拨号失败留下的节点失败计数。
func (host *Host) recordPeerConnectionSuccess(peerID string) {
	var storedPeer Peer
	shouldPersist := false
	host.mutex.Lock()
	if peer, ok := host.peers[peerID]; ok {
		peer.MarkConnected()
		host.peers[peerID] = peer
		host.addPeerToRoutingTableLocked(peer)
		host.routingTable.TouchPeer(peerID)
		storedPeer = peer.Clone()
		shouldPersist = true
	}
	host.mutex.Unlock()

	if shouldPersist {
		host.savePeerBestEffort(storedPeer)
	}
}
