package p2p

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// requestManager 管理等待中的请求 + 用 request_id 将读循环中的响应路由回调用方。
type requestManager struct {
	mutex   sync.Mutex
	waiters map[string]pendingRequest
}

type pendingRequest struct {
	waiter              chan Message
	peerID              string
	responseType        MessageType
	requireResponseType bool
}

func newRequestManager() *requestManager {
	return &requestManager{
		waiters: make(map[string]pendingRequest),
	}
}

func (manager *requestManager) register(requestID string, peerID string, responseType MessageType, requireResponseType bool) (<-chan Message, func(), error) {
	normalizedID := strings.ToLower(strings.TrimSpace(requestID))
	if !isValidMessageID(normalizedID) {
		return nil, nil, fmt.Errorf("%w: invalid pending request id", ErrInvalidMessage)
	}

	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if _, exists := manager.waiters[normalizedID]; exists {
		return nil, nil, fmt.Errorf("%w: duplicated pending request id", ErrInvalidMessage)
	}

	waiter := make(chan Message, 1)
	manager.waiters[normalizedID] = pendingRequest{
		waiter:              waiter,
		peerID:              peerID,
		responseType:        responseType,
		requireResponseType: requireResponseType,
	}
	unregister := func() {
		manager.unregister(normalizedID)
	}
	return waiter, unregister, nil
}

func (manager *requestManager) unregister(requestID string) {
	manager.mutex.Lock()
	delete(manager.waiters, strings.ToLower(requestID))
	manager.mutex.Unlock()
}

func (manager *requestManager) fulfill(response Message) bool {
	if !response.IsResponse() {
		return false
	}

	requestID := strings.ToLower(response.RequestID)
	manager.mutex.Lock()
	pending, exists := manager.waiters[requestID]
	if exists && pending.matches(response) {
		delete(manager.waiters, requestID)
	}
	manager.mutex.Unlock()
	if !exists || !pending.matches(response) {
		return false
	}

	pending.waiter <- response
	return true
}

func (pending pendingRequest) matches(response Message) bool {
	if pending.peerID != "" && response.FromPeerID != pending.peerID {
		return false
	}
	if pending.requireResponseType && response.Type != pending.responseType {
		return false
	}
	return true
}

// Request 发送请求并等待响应 + 统一复用连接读循环防止多个调用方并发抢读。
func (host *Host) Request(ctx context.Context, peerID string, request Message) (Message, error) {
	connection, ok := host.Connection(peerID)
	if !ok {
		var err error
		connection, err = host.DialPeer(ctx, peerID)
		if err != nil {
			return Message{}, err
		}
	}
	return host.requestOnConnection(ctx, connection, peerID, request)
}

func (host *Host) requestOnConnection(ctx context.Context, connection Connection, peerID string, request Message) (Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if connection == nil {
		return Message{}, fmt.Errorf("%w: nil request connection", ErrConnectionClosed)
	}

	outbound, err := host.prepareOutboundMessage(peerID, request)
	if err != nil {
		return Message{}, err
	}
	if !outbound.IsRequest() {
		return Message{}, fmt.Errorf("%w: request flag required", ErrInvalidMessage)
	}
	host.metrics.requestsStarted.Add(1)

	responseType, requireResponseType := expectedResponseType(outbound.Type)
	waiter, unregister, err := host.requests.register(outbound.ID, peerID, responseType, requireResponseType)
	if err != nil {
		host.metrics.requestsFailed.Add(1)
		return Message{}, err
	}
	defer unregister()

	if err := host.writeConnectionMessage(ctx, connection, peerID, outbound); err != nil {
		host.logger.Warn("p2p request write failed",
			slog.String("peer_id", peerID),
			slog.String("request_id", outbound.ID),
			slog.Uint64("protocol_id", uint64(outbound.Type)),
			slog.Any("error", err),
		)
		host.metrics.requestsFailed.Add(1)
		return Message{}, err
	}

	select {
	case response := <-waiter:
		host.metrics.requestsSucceeded.Add(1)
		host.logger.Debug("p2p request completed",
			slog.String("peer_id", peerID),
			slog.String("request_id", outbound.ID),
			slog.String("response_id", response.ID),
			slog.Uint64("protocol_id", uint64(outbound.Type)),
		)
		return response, nil
	case <-ctx.Done():
		host.logger.Warn("p2p request timed out",
			slog.String("peer_id", peerID),
			slog.String("request_id", outbound.ID),
			slog.Uint64("protocol_id", uint64(outbound.Type)),
			slog.Any("error", ctx.Err()),
		)
		host.metrics.requestsFailed.Add(1)
		return Message{}, fmt.Errorf("p2p: wait request %s response: %w", outbound.ID, ctx.Err())
	}
}

func expectedResponseType(requestType MessageType) (MessageType, bool) {
	switch requestType {
	case ProtocolFindNodeRequestV1:
		return ProtocolFindNodeResponseV1, true
	case ProtocolIdentifyRequestV1:
		return ProtocolIdentifyResponseV1, true
	default:
		return 0, false
	}
}
