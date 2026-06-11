package p2p

import (
	"fmt"
	"sync"
	"time"
)

const (
	defaultControlMessagesPerSecond      = 128
	defaultDataMessagesPerSecond         = 2048
	defaultAutoDataMessagesPerConnection = 128
	defaultMaxAutoDataMessagesPerSecond  = 65536
	defaultInboundMessagesPerSecond      = defaultControlMessagesPerSecond
	defaultMessageRateWindow             = time.Second
	defaultDuplicateMessageTTL           = 2 * time.Minute
	defaultMaxSeenMessageIDs             = 4096
	defaultDialBackoffBase               = 2 * time.Second
	defaultDialBackoffMax                = 2 * time.Minute
	defaultPeerBlockDuration             = 10 * time.Minute
	defaultPeerMinScore                  = -100
	defaultPeerMaxScore                  = 100
	defaultPeerBlockScore                = -80
	defaultInvalidMessagePenalty         = 20
	defaultRateLimitPenalty              = 15
	defaultDuplicateMessagePenalty       = 5
	defaultDialFailurePenalty            = 10
	defaultPeerSuccessReward             = 1
	defaultSlowHandlerThreshold          = 250 * time.Millisecond
)

// PeerProtectionConfig 保存 P2P 防护配置 + 集中控制限速、退避、评分和广播边界。
type PeerProtectionConfig struct {
	MaxInboundMessagesPerSecond      int
	MaxControlMessagesPerSecond      int
	MaxDataMessagesPerSecond         int
	AutoDataMessagesPerConnection    int
	AutoDataMessagesPerSecondFloor   int
	AutoDataMessagesPerSecondCeiling int
	MessageRateWindow                time.Duration
	DuplicateMessageTTL              time.Duration
	MaxSeenMessageIDs                int
	DialBackoffBase                  time.Duration
	DialBackoffMax                   time.Duration
	BlockDuration                    time.Duration
	MinScore                         int
	MaxScore                         int
	BlockScore                       int
	InvalidMessagePenalty            int
	RateLimitPenalty                 int
	DuplicateMessagePenalty          int
	DialFailurePenalty               int
	SuccessReward                    int
	MaxBroadcastPeers                int
	SlowHandlerThreshold             time.Duration
}

type peerProtection struct {
	config PeerProtectionConfig
	mutex  sync.Mutex
	peers  map[string]peerProtectionState
	seen   map[string]int64
	order  []string
}

type peerProtectionState struct {
	score                     int
	controlRateWindow         peerRateWindow
	dataRateWindow            peerRateWindow
	blockedUntilUnixMilli     int64
	dialBackoffUntilUnixMilli int64
	dialFailures              uint32
	lastPenaltyReason         string
}

type peerRateWindow struct {
	startUnixMilli int64
	count          int
}

type peerProtectionSnapshot struct {
	PeerID                    string
	Score                     int
	BlockedUntilUnixMilli     int64
	DialBackoffUntilUnixMilli int64
	LastPenaltyReason         string
	Blocked                   bool
}

func newPeerProtection(config PeerProtectionConfig, maxConnections ...int) *peerProtection {
	return &peerProtection{
		config: normalizePeerProtectionConfig(config, maxConnections...),
		peers:  make(map[string]peerProtectionState),
		seen:   make(map[string]int64),
	}
}

func normalizePeerProtectionConfig(config PeerProtectionConfig, maxConnections ...int) PeerProtectionConfig {
	connectionLimit := defaultMaxPeers
	if len(maxConnections) > 0 && maxConnections[0] > 0 {
		connectionLimit = maxConnections[0]
	}
	if config.MaxInboundMessagesPerSecond > 0 {
		if config.MaxControlMessagesPerSecond <= 0 {
			config.MaxControlMessagesPerSecond = config.MaxInboundMessagesPerSecond
		}
		if config.MaxDataMessagesPerSecond <= 0 {
			config.MaxDataMessagesPerSecond = config.MaxInboundMessagesPerSecond
		}
	}
	if config.MaxControlMessagesPerSecond <= 0 {
		config.MaxControlMessagesPerSecond = defaultControlMessagesPerSecond
	}
	if config.AutoDataMessagesPerConnection <= 0 {
		config.AutoDataMessagesPerConnection = defaultAutoDataMessagesPerConnection
	}
	if config.AutoDataMessagesPerSecondFloor <= 0 {
		config.AutoDataMessagesPerSecondFloor = defaultDataMessagesPerSecond
	}
	if config.AutoDataMessagesPerSecondCeiling <= 0 {
		config.AutoDataMessagesPerSecondCeiling = defaultMaxAutoDataMessagesPerSecond
	}
	if config.AutoDataMessagesPerSecondCeiling < config.AutoDataMessagesPerSecondFloor {
		config.AutoDataMessagesPerSecondCeiling = config.AutoDataMessagesPerSecondFloor
	}
	if config.MaxDataMessagesPerSecond <= 0 {
		config.MaxDataMessagesPerSecond = autoDataMessagesPerSecond(connectionLimit, config)
	}
	if config.MaxInboundMessagesPerSecond <= 0 {
		config.MaxInboundMessagesPerSecond = config.MaxControlMessagesPerSecond
	}
	if config.MessageRateWindow <= 0 {
		config.MessageRateWindow = defaultMessageRateWindow
	}
	if config.DuplicateMessageTTL <= 0 {
		config.DuplicateMessageTTL = defaultDuplicateMessageTTL
	}
	if config.MaxSeenMessageIDs <= 0 {
		config.MaxSeenMessageIDs = defaultMaxSeenMessageIDs
	}
	if config.DialBackoffBase <= 0 {
		config.DialBackoffBase = defaultDialBackoffBase
	}
	if config.DialBackoffMax <= 0 {
		config.DialBackoffMax = defaultDialBackoffMax
	}
	if config.BlockDuration <= 0 {
		config.BlockDuration = defaultPeerBlockDuration
	}
	if config.MinScore == 0 {
		config.MinScore = defaultPeerMinScore
	}
	if config.MaxScore == 0 {
		config.MaxScore = defaultPeerMaxScore
	}
	if config.BlockScore == 0 {
		config.BlockScore = defaultPeerBlockScore
	}
	if config.InvalidMessagePenalty <= 0 {
		config.InvalidMessagePenalty = defaultInvalidMessagePenalty
	}
	if config.RateLimitPenalty <= 0 {
		config.RateLimitPenalty = defaultRateLimitPenalty
	}
	if config.DuplicateMessagePenalty <= 0 {
		config.DuplicateMessagePenalty = defaultDuplicateMessagePenalty
	}
	if config.DialFailurePenalty <= 0 {
		config.DialFailurePenalty = defaultDialFailurePenalty
	}
	if config.SuccessReward <= 0 {
		config.SuccessReward = defaultPeerSuccessReward
	}
	if config.SlowHandlerThreshold <= 0 {
		config.SlowHandlerThreshold = defaultSlowHandlerThreshold
	}
	if config.MinScore > config.BlockScore {
		config.MinScore = config.BlockScore
	}
	if config.MaxScore < 0 {
		config.MaxScore = defaultPeerMaxScore
	}
	return config
}

func autoDataMessagesPerSecond(connectionLimit int, config PeerProtectionConfig) int {
	if connectionLimit <= 0 {
		connectionLimit = defaultMaxPeers
	}
	rateLimit := connectionLimit * config.AutoDataMessagesPerConnection
	if rateLimit < config.AutoDataMessagesPerSecondFloor {
		return config.AutoDataMessagesPerSecondFloor
	}
	if rateLimit > config.AutoDataMessagesPerSecondCeiling {
		return config.AutoDataMessagesPerSecondCeiling
	}
	return rateLimit
}

func (protection *peerProtection) acceptInboundMessage(peerID string, messageID string, trafficClass ProtocolClass, now time.Time) (peerProtectionSnapshot, error) {
	if protection == nil || peerID == "" {
		return peerProtectionSnapshot{}, nil
	}

	protection.mutex.Lock()
	defer protection.mutex.Unlock()

	state := protection.peers[peerID]
	if blockedUntil := activeBlockUntil(state, now); blockedUntil > 0 {
		return snapshotPeerProtection(peerID, state, now), fmt.Errorf("%w: until %d", ErrPeerBlocked, blockedUntil)
	}
	state = protection.resetExpiredBlock(state, now)
	state, allowed := protection.allowMessageRate(state, trafficClass, now)
	if !allowed {
		state = protection.penalizeState(state, protection.config.RateLimitPenalty, rateLimitPenaltyReason(trafficClass), now)
		protection.peers[peerID] = state
		return snapshotPeerProtection(peerID, state, now), ErrRateLimited
	}
	if protection.isDuplicateMessage(messageID, now) {
		state = protection.penalizeState(state, protection.config.DuplicateMessagePenalty, "duplicate-message", now)
		protection.peers[peerID] = state
		return snapshotPeerProtection(peerID, state, now), ErrDuplicateMessage
	}
	protection.rememberMessage(messageID, now)
	protection.peers[peerID] = state
	return snapshotPeerProtection(peerID, state, now), nil
}

func (protection *peerProtection) checkDial(peerID string, now time.Time) error {
	if protection == nil || peerID == "" {
		return nil
	}

	protection.mutex.Lock()
	defer protection.mutex.Unlock()

	state := protection.peers[peerID]
	if blockedUntil := activeBlockUntil(state, now); blockedUntil > 0 {
		return fmt.Errorf("%w: until %d", ErrPeerBlocked, blockedUntil)
	}
	state = protection.resetExpiredBlock(state, now)
	if state.dialBackoffUntilUnixMilli > now.UnixMilli() {
		protection.peers[peerID] = state
		return fmt.Errorf("%w: until %d", ErrPeerBackoff, state.dialBackoffUntilUnixMilli)
	}
	protection.peers[peerID] = state
	return nil
}

func (protection *peerProtection) recordDialFailure(peerID string, now time.Time) peerProtectionSnapshot {
	if protection == nil || peerID == "" {
		return peerProtectionSnapshot{}
	}

	protection.mutex.Lock()
	defer protection.mutex.Unlock()

	state := protection.peers[peerID]
	state.dialFailures++
	state.dialBackoffUntilUnixMilli = now.Add(protection.dialBackoff(state.dialFailures)).UnixMilli()
	state = protection.penalizeState(state, protection.config.DialFailurePenalty, "dial-failure", now)
	protection.peers[peerID] = state
	return snapshotPeerProtection(peerID, state, now)
}

func (protection *peerProtection) recordSuccess(peerID string, now time.Time) peerProtectionSnapshot {
	if protection == nil || peerID == "" {
		return peerProtectionSnapshot{}
	}

	protection.mutex.Lock()
	defer protection.mutex.Unlock()

	state := protection.peers[peerID]
	state = protection.resetExpiredBlock(state, now)
	state.dialFailures = 0
	state.dialBackoffUntilUnixMilli = 0
	state.score = minInt(protection.config.MaxScore, state.score+protection.config.SuccessReward)
	protection.peers[peerID] = state
	return snapshotPeerProtection(peerID, state, now)
}

func (protection *peerProtection) penalize(peerID string, penalty int, reason string, now time.Time) peerProtectionSnapshot {
	if protection == nil || peerID == "" {
		return peerProtectionSnapshot{}
	}

	protection.mutex.Lock()
	defer protection.mutex.Unlock()

	state := protection.peers[peerID]
	state = protection.penalizeState(state, penalty, reason, now)
	protection.peers[peerID] = state
	return snapshotPeerProtection(peerID, state, now)
}

func (protection *peerProtection) snapshot(peerID string, now time.Time) peerProtectionSnapshot {
	if protection == nil || peerID == "" {
		return peerProtectionSnapshot{}
	}

	protection.mutex.Lock()
	defer protection.mutex.Unlock()

	state := protection.resetExpiredBlock(protection.peers[peerID], now)
	protection.peers[peerID] = state
	return snapshotPeerProtection(peerID, state, now)
}

func (protection *peerProtection) slowHandlerThreshold() time.Duration {
	if protection == nil {
		return defaultSlowHandlerThreshold
	}
	return protection.config.SlowHandlerThreshold
}

func (protection *peerProtection) broadcastLimit() int {
	if protection == nil {
		return 0
	}
	return protection.config.MaxBroadcastPeers
}

func (protection *peerProtection) allowMessageRate(state peerProtectionState, trafficClass ProtocolClass, now time.Time) (peerProtectionState, bool) {
	if trafficClass == ProtocolClassControl {
		window, allowed := protection.allowRateWindow(state.controlRateWindow, protection.config.MaxControlMessagesPerSecond, now)
		state.controlRateWindow = window
		return state, allowed
	}
	window, allowed := protection.allowRateWindow(state.dataRateWindow, protection.config.MaxDataMessagesPerSecond, now)
	state.dataRateWindow = window
	return state, allowed
}

func (protection *peerProtection) allowRateWindow(window peerRateWindow, maxMessages int, now time.Time) (peerRateWindow, bool) {
	windowStart := time.UnixMilli(window.startUnixMilli)
	if window.startUnixMilli == 0 || now.Sub(windowStart) >= protection.config.MessageRateWindow {
		window.startUnixMilli = now.UnixMilli()
		window.count = 0
	}
	if window.count >= maxMessages {
		return window, false
	}
	window.count++
	return window, true
}

func rateLimitPenaltyReason(trafficClass ProtocolClass) string {
	if trafficClass == ProtocolClassControl {
		return "control-message-rate-limit"
	}
	return "data-message-rate-limit"
}

func (protection *peerProtection) isDuplicateMessage(messageID string, now time.Time) bool {
	if messageID == "" {
		return false
	}
	protection.pruneSeenMessages(now)
	expiresAtUnixMilli, ok := protection.seen[messageID]
	return ok && expiresAtUnixMilli > now.UnixMilli()
}

func (protection *peerProtection) rememberMessage(messageID string, now time.Time) {
	if messageID == "" {
		return
	}
	protection.seen[messageID] = now.Add(protection.config.DuplicateMessageTTL).UnixMilli()
	protection.order = append(protection.order, messageID)
	protection.trimSeenMessages()
}

func (protection *peerProtection) pruneSeenMessages(now time.Time) {
	kept := protection.order[:0]
	for _, messageID := range protection.order {
		expiresAtUnixMilli, ok := protection.seen[messageID]
		if !ok || expiresAtUnixMilli <= now.UnixMilli() {
			delete(protection.seen, messageID)
			continue
		}
		kept = append(kept, messageID)
	}
	protection.order = kept
}

func (protection *peerProtection) trimSeenMessages() {
	for len(protection.order) > protection.config.MaxSeenMessageIDs {
		messageID := protection.order[0]
		protection.order = protection.order[1:]
		delete(protection.seen, messageID)
	}
}

func (protection *peerProtection) penalizeState(state peerProtectionState, penalty int, reason string, now time.Time) peerProtectionState {
	if penalty <= 0 {
		penalty = protection.config.InvalidMessagePenalty
	}
	state.score = maxInt(protection.config.MinScore, state.score-penalty)
	state.lastPenaltyReason = reason
	if state.score <= protection.config.BlockScore {
		state.blockedUntilUnixMilli = now.Add(protection.config.BlockDuration).UnixMilli()
	}
	return state
}

func (protection *peerProtection) resetExpiredBlock(state peerProtectionState, now time.Time) peerProtectionState {
	if state.blockedUntilUnixMilli == 0 || state.blockedUntilUnixMilli > now.UnixMilli() {
		return state
	}
	state.blockedUntilUnixMilli = 0
	if state.score <= protection.config.BlockScore {
		state.score = protection.config.BlockScore + 1
	}
	return state
}

func (protection *peerProtection) dialBackoff(failures uint32) time.Duration {
	backoff := protection.config.DialBackoffBase
	for index := uint32(1); index < failures && backoff < protection.config.DialBackoffMax; index++ {
		backoff *= 2
	}
	if backoff > protection.config.DialBackoffMax {
		return protection.config.DialBackoffMax
	}
	return backoff
}

func activeBlockUntil(state peerProtectionState, now time.Time) int64 {
	if state.blockedUntilUnixMilli > now.UnixMilli() {
		return state.blockedUntilUnixMilli
	}
	return 0
}

func snapshotPeerProtection(peerID string, state peerProtectionState, now time.Time) peerProtectionSnapshot {
	return peerProtectionSnapshot{
		PeerID:                    peerID,
		Score:                     state.score,
		BlockedUntilUnixMilli:     state.blockedUntilUnixMilli,
		DialBackoffUntilUnixMilli: state.dialBackoffUntilUnixMilli,
		LastPenaltyReason:         state.lastPenaltyReason,
		Blocked:                   state.blockedUntilUnixMilli > now.UnixMilli(),
	}
}
