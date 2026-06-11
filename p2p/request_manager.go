package p2p

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const requestManagerShardCount = 32

// requestManager 管理等待中的请求 + 用 request_id 将读循环中的响应路由回调用方。
type requestManager struct {
	shards  [requestManagerShardCount]requestManagerShard
	pending atomic.Int64
}

type requestManagerShard struct {
	mutex   sync.Mutex
	waiters map[string]pendingRequest
}

type pendingRequest struct {
	waiter              chan Message
	peerID              string
	responseType        ProtocolID
	requireResponseType bool
}

func newRequestManager() *requestManager {
	manager := &requestManager{}
	for index := range manager.shards {
		manager.shards[index].waiters = make(map[string]pendingRequest)
	}
	return manager
}

func (manager *requestManager) register(requestID string, peerID string, responseType ProtocolID, requireResponseType bool) (<-chan Message, func(), error) {
	normalizedID := strings.ToLower(strings.TrimSpace(requestID))
	if !isValidMessageID(normalizedID) {
		return nil, nil, fmt.Errorf("%w: invalid pending request id", ErrInvalidMessage)
	}

	shard := manager.shard(normalizedID)
	shard.mutex.Lock()
	defer shard.mutex.Unlock()
	if _, exists := shard.waiters[normalizedID]; exists {
		return nil, nil, fmt.Errorf("%w: duplicated pending request id", ErrInvalidMessage)
	}

	waiter := make(chan Message, 1)
	shard.waiters[normalizedID] = pendingRequest{
		waiter:              waiter,
		peerID:              peerID,
		responseType:        responseType,
		requireResponseType: requireResponseType,
	}
	manager.pending.Add(1)
	unregister := func() {
		manager.unregister(normalizedID)
	}
	return waiter, unregister, nil
}

func (manager *requestManager) unregister(requestID string) {
	normalizedID := strings.ToLower(strings.TrimSpace(requestID))
	shard := manager.shard(normalizedID)
	shard.mutex.Lock()
	if _, exists := shard.waiters[normalizedID]; exists {
		delete(shard.waiters, normalizedID)
		manager.pending.Add(-1)
	}
	shard.mutex.Unlock()
}

func (manager *requestManager) fulfill(response Message) bool {
	if !response.IsResponse() {
		return false
	}

	requestID := strings.ToLower(response.RequestID)
	shard := manager.shard(requestID)
	shard.mutex.Lock()
	pending, exists := shard.waiters[requestID]
	matched := exists && pending.matches(response)
	if matched {
		delete(shard.waiters, requestID)
		manager.pending.Add(-1)
	}
	shard.mutex.Unlock()
	if !matched {
		return false
	}

	pending.waiter <- response
	return true
}

func (manager *requestManager) pendingCount() uint64 {
	pending := manager.pending.Load()
	if pending < 0 {
		return 0
	}
	return uint64(pending)
}

func (manager *requestManager) shard(requestID string) *requestManagerShard {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(requestID))
	return &manager.shards[int(hash.Sum32()%requestManagerShardCount)]
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
	if err := validateOutboundPeerID(peerID); err != nil {
		return Message{}, err
	}
	if err := host.checkPeerDialAllowed(peerID); err != nil {
		return Message{}, peerProtectionDialError(peerID, err)
	}

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
	if err := validateOutboundPeerID(peerID); err != nil {
		return Message{}, err
	}
	if err := host.checkPeerDialAllowed(peerID); err != nil {
		return Message{}, peerProtectionDialError(peerID, err)
	}

	outbound, err := host.prepareOutboundMessage(peerID, request)
	if err != nil {
		return Message{}, err
	}
	if !outbound.IsRequest() {
		return Message{}, fmt.Errorf("%w: request flag required", ErrInvalidMessage)
	}
	requestStartedAt := time.Now()
	host.metrics.requestsStarted.Add(1)
	defer host.metrics.recordRequestLatency(time.Since(requestStartedAt))

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

func expectedResponseType(requestType ProtocolID) (ProtocolID, bool) {
	switch requestType {
	case ProtocolFindNodeRequestV1:
		return ProtocolFindNodeResponseV1, true
	case ProtocolIdentifyRequestV1:
		return ProtocolIdentifyResponseV1, true
	default:
		return 0, false
	}
}
