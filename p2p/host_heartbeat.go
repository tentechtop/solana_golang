package p2p

import (
	"context"
	"log/slog"
	"time"
)

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

// handleHeartbeatMessage 处理心跳消息 + ping 立即回复 pong，pong 仅刷新活跃时间。
func (host *Host) handleHeartbeatMessage(ctx context.Context, connection Connection, message Message) bool {
	if message.Type == ProtocolPongV1 {
		return true
	}
	if message.Type != ProtocolPingV1 {
		return false
	}
	response, err := responseFor(message, host.peerID, ProtocolPongV1, nil)
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
		message, err := NewRequestMessage(host.peerID, ProtocolPingV1, nil)
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

// markHeartbeat 记录心跳发送时间 + 便于监控连接活跃度。
func (host *Host) markHeartbeat(peerID string) {
	host.mutex.Lock()
	defer host.mutex.Unlock()
	state := host.connectionStates[peerID]
	state.LastHeartbeatUnixMilli = time.Now().UnixMilli()
	host.connectionStates[peerID] = state
}
