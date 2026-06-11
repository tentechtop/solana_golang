package p2p

import (
	"context"
	"errors"
	"fmt"
	"time"
)

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

// writeConnectionMessage 写入连接消息 + 同步更新连接活跃时间和错误计数。
func (host *Host) writeConnectionMessage(ctx context.Context, connection Connection, peerID string, message Message) error {
	if err := connection.WriteMessage(ctx, message); err != nil {
		host.recordConnectionError(connection, err)
		return err
	}
	host.metrics.messagesWritten.Add(1)
	host.markConnectionWrite(connection, peerID)
	return nil
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
