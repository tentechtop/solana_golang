package p2p

import (
	"context"
	"fmt"
	"log/slog"
)

// HandleConnection 管理连接读循环 + 自动处理心跳并分发业务协议。
func (host *Host) HandleConnection(ctx context.Context, connection Connection) {
	defer host.removeConnectionByID(connection.ID())
	defer connection.Close()
	if ctx == nil {
		ctx = host.lifecycleContext
	}
	for {
		message, err := connection.ReadMessage(ctx)
		if err != nil {
			host.recordConnectionError(connection, err)
			return
		}
		host.metrics.messagesRead.Add(1)
		if err := host.validateConnectionMessage(connection, message); err != nil {
			host.recordConnectionError(connection, err)
			host.metrics.messagesRejected.Add(1)
			host.logger.Warn("p2p message peer mismatch",
				slog.String("connection_id", connection.ID()),
				slog.String("from_peer_id", message.FromPeerID),
				slog.String("remote_peer_id", connection.RemotePeerID()),
				slog.Any("error", err),
			)
			return
		}
		if err := host.markConnectionRead(connection, message.FromPeerID); err != nil {
			host.metrics.messagesRejected.Add(1)
			host.logger.Warn("p2p connection rejected",
				slog.String("connection_id", connection.ID()),
				slog.String("peer_id", message.FromPeerID),
				slog.Any("error", err),
			)
			return
		}
		if host.handleHeartbeatMessage(ctx, connection, message) {
			continue
		}
		if host.requests.fulfill(message) {
			continue
		}
		result, err := host.HandleMessage(ctx, message)
		if err != nil {
			host.metrics.messagesRejected.Add(1)
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

func (host *Host) validateConnectionMessage(connection Connection, message Message) error {
	remotePeerID := connection.RemotePeerID()
	if remotePeerID != "" && message.FromPeerID != "" && message.FromPeerID != remotePeerID {
		return fmt.Errorf("%w: message sender does not match connection peer", ErrInvalidMessage)
	}
	if message.ToPeerID != "" && message.ToPeerID != host.peerID {
		return fmt.Errorf("%w: message target does not match local peer", ErrInvalidMessage)
	}
	return nil
}
