package p2p

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Send 发送消息到节点 + 自动拨号并补齐消息路由字段。
func (host *Host) Send(ctx context.Context, peerID string, message Message) error {
	if err := validateOutboundPeerID(peerID); err != nil {
		return err
	}
	if err := host.checkPeerDialAllowed(peerID); err != nil {
		return peerProtectionDialError(peerID, err)
	}

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
	if ctx == nil {
		ctx = context.Background()
	}
	normalizedPeers := host.normalizeBroadcastPeers(peerIDs)
	if len(normalizedPeers) == 0 {
		return nil
	}
	baseMessage, err := host.prepareBroadcastBaseMessage(message)
	if err != nil {
		return err
	}

	workerCount := minInt(host.broadcastConcurrency, len(normalizedPeers))
	jobs := make(chan string)
	results := make(chan error, len(normalizedPeers))
	var workers sync.WaitGroup
	for workerID := 0; workerID < workerCount; workerID++ {
		workers.Add(1)
		go host.broadcastWorker(ctx, &workers, jobs, results, baseMessage)
	}
	var enqueueError error
enqueueLoop:
	for _, peerID := range normalizedPeers {
		select {
		case jobs <- peerID:
		case <-ctx.Done():
			enqueueError = ctx.Err()
			break enqueueLoop
		}
	}
	close(jobs)
	workers.Wait()
	close(results)

	var sendErrors []error
	for err := range results {
		if err != nil {
			sendErrors = append(sendErrors, err)
		}
	}
	if enqueueError != nil {
		sendErrors = append(sendErrors, enqueueError)
	}
	return errors.Join(sendErrors...)
}

// writeConnectionMessage 写入连接消息 + 同步更新连接活跃时间和错误计数。
func (host *Host) writeConnectionMessage(ctx context.Context, connection Connection, peerID string, message Message) error {
	if connection == nil {
		return fmt.Errorf("%w: nil write connection", ErrConnectionClosed)
	}
	if err := connection.WriteMessage(ctx, message); err != nil {
		host.recordConnectionError(connection, err)
		return err
	}
	if _, ok := connection.(*queuedConnection); !ok {
		host.metrics.messagesWritten.Add(1)
		host.markConnectionWrite(connection, peerID)
	}
	return nil
}

// prepareOutboundMessage 补齐出站消息路由字段 + 发送前统一做协议边界校验。
func (host *Host) prepareOutboundMessage(peerID string, message Message) (Message, error) {
	outbound := message.Clone()
	return host.prepareOutboundMessageFields(peerID, outbound, false)
}

func (host *Host) prepareBroadcastBaseMessage(message Message) (Message, error) {
	outbound := message.Clone()
	return host.prepareOutboundMessageFields("", outbound, true)
}

func (host *Host) prepareOutboundMessageFields(peerID string, outbound Message, forcePeerTarget bool) (Message, error) {
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
	if forcePeerTarget {
		outbound.ToPeerID = peerID
	} else if outbound.ToPeerID == "" {
		outbound.ToPeerID = peerID
	}
	if err := outbound.Validate(DefaultMaxMessageSize); err != nil {
		return Message{}, err
	}
	return outbound, nil
}

func (host *Host) broadcastWorker(ctx context.Context, workers *sync.WaitGroup, jobs <-chan string, results chan<- error, baseMessage Message) {
	defer workers.Done()
	for peerID := range jobs {
		results <- host.broadcastToPeer(ctx, peerID, baseMessage)
	}
}

func (host *Host) broadcastToPeer(ctx context.Context, peerID string, baseMessage Message) error {
	if err := validateOutboundPeerID(peerID); err != nil {
		return fmt.Errorf("%s: %w", peerID, err)
	}
	if err := host.checkPeerDialAllowed(peerID); err != nil {
		return fmt.Errorf("%s: %w", peerID, peerProtectionDialError(peerID, err))
	}
	connection, ok := host.Connection(peerID)
	if !ok {
		var err error
		connection, err = host.DialPeer(ctx, peerID)
		if err != nil {
			return fmt.Errorf("%s: %w", peerID, err)
		}
	}
	outbound := baseMessage
	outbound.ToPeerID = peerID
	if err := outbound.Validate(DefaultMaxMessageSize); err != nil {
		return fmt.Errorf("%s: %w", peerID, err)
	}
	if err := host.writeConnectionMessage(ctx, connection, peerID, outbound); err != nil {
		return fmt.Errorf("%s: %w", peerID, err)
	}
	return nil
}

func validateOutboundPeerID(peerID string) error {
	if err := validatePeerID(peerID); err != nil {
		return fmt.Errorf("%w: invalid outbound peer id: %w", ErrInvalidMessage, err)
	}
	return nil
}
