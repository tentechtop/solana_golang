package p2p

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	requestManagerShardCount          = 32
	maxRequestConnectionRetryAttempts = 1
)

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
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateOutboundPeerID(peerID); err != nil {
		return Message{}, err
	}
	if err := host.checkPeerDialAllowed(peerID); err != nil {
		return Message{}, peerProtectionDialError(peerID, err)
	}

	var lastError error
	for attempt := 0; attempt <= maxRequestConnectionRetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastError != nil {
				return Message{}, lastError
			}
			return Message{}, err
		}

		connection, err := host.connectionForRequest(ctx, peerID)
		if err != nil {
			if !host.shouldRetryRequestDial(err, attempt) {
				return Message{}, err
			}
			lastError = err
			host.logRequestRetry(peerID, "", request.Type, attempt, err)
			continue
		}

		attemptRequest := request
		if attempt > 0 {
			attemptRequest = resetRequestIdentity(request)
		}
		response, err := host.requestOnConnection(ctx, connection, peerID, attemptRequest)
		if err == nil {
			return response, nil
		}
		if !host.shouldRetryRequestAttempt(peerID, connection, err, attempt) {
			return Message{}, err
		}
		lastError = err
		host.logRequestRetry(peerID, connection.ID(), request.Type, attempt, err)
	}
	return Message{}, lastError
}

func (host *Host) requestOnConnection(ctx context.Context, connection Connection, peerID string, request Message) (Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestContext, cancelRequest := host.withRequestTimeout(ctx)
	defer cancelRequest()

	if connection == nil {
		return Message{}, fmt.Errorf("%w: nil request connection", ErrConnectionClosed)
	}
	if err := validateOutboundPeerID(peerID); err != nil {
		return Message{}, err
	}
	if err := host.checkPeerDialAllowed(peerID); err != nil {
		return Message{}, peerProtectionDialError(peerID, err)
	}

	outbound, err := host.prepareOutboundRequestMessage(peerID, request)
	if err != nil {
		return Message{}, err
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

	if err := host.writePeerMessage(requestContext, peerID, connection, outbound); err != nil {
		host.logger.Warn("p2p request write failed",
			slog.String("peer_id", peerID),
			slog.String("request_id", outbound.ID),
			slog.Uint64("protocol_id", uint64(outbound.Type)),
			slog.Any("error", err),
		)
		host.metrics.requestsFailed.Add(1)
		return Message{}, WithErrorInfo(err, ErrorInfo{
			Operation: "request_write",
			PeerID:    peerID,
		})
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
	case <-requestContext.Done():
		contextError := requestContext.Err()
		host.logger.Warn("p2p request timed out",
			slog.String("peer_id", peerID),
			slog.String("request_id", outbound.ID),
			slog.Uint64("protocol_id", uint64(outbound.Type)),
			slog.Any("error", contextError),
		)
		host.metrics.requestsFailed.Add(1)
		return Message{}, WithErrorInfo(
			fmt.Errorf("p2p: wait request %s response: %w", outbound.ID, contextError),
			ErrorInfo{
				Operation: "request_wait_response",
				PeerID:    peerID,
				Timeout:   errors.Is(contextError, context.DeadlineExceeded),
				Temporary: errors.Is(contextError, context.DeadlineExceeded),
				Retryable: errors.Is(contextError, context.DeadlineExceeded),
			},
		)
	}
}

func (host *Host) connectionForRequest(ctx context.Context, peerID string) (Connection, error) {
	connection, ok := host.Connection(peerID)
	if ok {
		return connection, nil
	}
	return host.DialPeer(ctx, peerID)
}

func (host *Host) prepareOutboundRequestMessage(peerID string, request Message) (Message, error) {
	if request.Flag != MessageFlagRequest {
		return Message{}, fmt.Errorf("%w: request flag required", ErrInvalidMessage)
	}
	outbound := request.Clone()
	if outbound.ID == "" {
		messageID, err := newMessageID()
		if err != nil {
			return Message{}, err
		}
		outbound.ID = messageID
	}
	if outbound.RequestID != "" && !strings.EqualFold(outbound.RequestID, outbound.ID) {
		return Message{}, fmt.Errorf("%w: request id must match message id", ErrInvalidMessage)
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
	outbound.MarkAsRequest()
	if err := outbound.Validate(host.maxMessageSize); err != nil {
		return Message{}, err
	}
	return outbound, nil
}

func resetRequestIdentity(request Message) Message {
	retryRequest := request.Clone()
	retryRequest.ID = ""
	retryRequest.RequestID = ""
	retryRequest.CreatedAtUnixMilli = 0
	return retryRequest
}

func (host *Host) shouldRetryRequestDial(err error, attempt int) bool {
	return attempt < maxRequestConnectionRetryAttempts && requestConnectionRetryableError(err)
}

func (host *Host) shouldRetryRequestAttempt(peerID string, connection Connection, err error, attempt int) bool {
	if attempt >= maxRequestConnectionRetryAttempts || !requestConnectionRetryableError(err) {
		return false
	}
	info, _ := ErrorInfoOf(err)
	if info.Operation == "request_write" {
		return true
	}
	return info.Operation == "request_wait_response" && info.Timeout && host.requestConnectionChanged(peerID, connection)
}

func requestConnectionRetryableError(err error) bool {
	return errors.Is(err, ErrConnectionClosed) ||
		errors.Is(err, ErrDuplicateConnection) ||
		errors.Is(err, context.DeadlineExceeded)
}

func (host *Host) requestConnectionChanged(peerID string, connection Connection) bool {
	if peerID == "" || connection == nil {
		return false
	}
	host.mutex.RLock()
	currentConnection := host.connections[peerID]
	host.mutex.RUnlock()
	if currentConnection == nil {
		return true
	}
	return currentConnection.ID() != connection.ID()
}

func (host *Host) logRequestRetry(peerID string, connectionID string, protocolID ProtocolID, attempt int, err error) {
	host.logger.Warn("p2p request retrying after transient connection error",
		slog.String("peer_id", peerID),
		slog.String("connection_id", connectionID),
		slog.Uint64("protocol_id", uint64(protocolID)),
		slog.Int("next_attempt", attempt+2),
		slog.Any("error", err),
	)
}

// withRequestTimeout 构造请求上下文 + 调用方未设置 deadline 时使用 Host 默认请求超时。
func (host *Host) withRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, host.requestTimeout)
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
