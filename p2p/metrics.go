package p2p

import "sync/atomic"

type p2pMetrics struct {
	inboundRejected       atomic.Uint64
	maxConnectionRejected atomic.Uint64
	perIPRejected         atomic.Uint64
	secureHandshakeOK     atomic.Uint64
	secureHandshakeFailed atomic.Uint64
	messagesRead          atomic.Uint64
	messagesWritten       atomic.Uint64
	messagesRejected      atomic.Uint64
	messagesRateLimited   atomic.Uint64
	duplicateMessages     atomic.Uint64
	peerBlocks            atomic.Uint64
	dialBackoffs          atomic.Uint64
	slowProtocolHandlers  atomic.Uint64
	broadcastPeersDropped atomic.Uint64
	writeQueueEnqueued    atomic.Uint64
	writeQueueFlushed     atomic.Uint64
	writeQueueDropped     atomic.Uint64
	writeQueueErrors      atomic.Uint64
	protocolJobsQueued    atomic.Uint64
	protocolJobsHandled   atomic.Uint64
	protocolJobsFailed    atomic.Uint64
	protocolJobsDropped   atomic.Uint64
	requestsStarted       atomic.Uint64
	requestsSucceeded     atomic.Uint64
	requestsFailed        atomic.Uint64
	identifyStarted       atomic.Uint64
	identifySucceeded     atomic.Uint64
	identifyFailed        atomic.Uint64
	peerRecordsAccepted   atomic.Uint64
	peerRecordsRejected   atomic.Uint64
	findNodeQueries       atomic.Uint64
	findNodeFailures      atomic.Uint64
}

// HostMetrics 保存 P2P 运行指标快照 + 供 RPC、日志和压测工具读取。
type HostMetrics struct {
	PeerCount             uint64
	ConnectionCount       uint64
	InboundRejected       uint64
	MaxConnectionRejected uint64
	PerIPRejected         uint64
	SecureHandshakeOK     uint64
	SecureHandshakeFailed uint64
	MessagesRead          uint64
	MessagesWritten       uint64
	MessagesRejected      uint64
	MessagesRateLimited   uint64
	DuplicateMessages     uint64
	PeerBlocks            uint64
	DialBackoffs          uint64
	SlowProtocolHandlers  uint64
	BroadcastPeersDropped uint64
	WriteQueueEnqueued    uint64
	WriteQueueFlushed     uint64
	WriteQueueDropped     uint64
	WriteQueueErrors      uint64
	WriteQueueDepth       uint64
	ProtocolJobsQueued    uint64
	ProtocolJobsHandled   uint64
	ProtocolJobsFailed    uint64
	ProtocolJobsDropped   uint64
	ProtocolQueueHigh     uint64
	ProtocolQueueNormal   uint64
	ProtocolQueueLow      uint64
	RequestsStarted       uint64
	RequestsSucceeded     uint64
	RequestsFailed        uint64
	IdentifyStarted       uint64
	IdentifySucceeded     uint64
	IdentifyFailed        uint64
	PeerRecordsAccepted   uint64
	PeerRecordsRejected   uint64
	FindNodeQueries       uint64
	FindNodeFailures      uint64
}

// Metrics 返回 P2P 指标快照 + 使用原子计数避免阻塞网络热路径。
func (host *Host) Metrics() HostMetrics {
	host.mutex.RLock()
	peerCount := uint64(len(host.peers))
	connectionCount := uint64(len(host.connections))
	host.mutex.RUnlock()
	highQueueDepth, normalQueueDepth, lowQueueDepth := host.protocolDispatcher.queueDepths()
	writeQueueDepth := host.writeQueueDepth()

	return HostMetrics{
		PeerCount:             peerCount,
		ConnectionCount:       connectionCount,
		InboundRejected:       host.metrics.inboundRejected.Load(),
		MaxConnectionRejected: host.metrics.maxConnectionRejected.Load(),
		PerIPRejected:         host.metrics.perIPRejected.Load(),
		SecureHandshakeOK:     host.metrics.secureHandshakeOK.Load(),
		SecureHandshakeFailed: host.metrics.secureHandshakeFailed.Load(),
		MessagesRead:          host.metrics.messagesRead.Load(),
		MessagesWritten:       host.metrics.messagesWritten.Load(),
		MessagesRejected:      host.metrics.messagesRejected.Load(),
		MessagesRateLimited:   host.metrics.messagesRateLimited.Load(),
		DuplicateMessages:     host.metrics.duplicateMessages.Load(),
		PeerBlocks:            host.metrics.peerBlocks.Load(),
		DialBackoffs:          host.metrics.dialBackoffs.Load(),
		SlowProtocolHandlers:  host.metrics.slowProtocolHandlers.Load(),
		BroadcastPeersDropped: host.metrics.broadcastPeersDropped.Load(),
		WriteQueueEnqueued:    host.metrics.writeQueueEnqueued.Load(),
		WriteQueueFlushed:     host.metrics.writeQueueFlushed.Load(),
		WriteQueueDropped:     host.metrics.writeQueueDropped.Load(),
		WriteQueueErrors:      host.metrics.writeQueueErrors.Load(),
		WriteQueueDepth:       writeQueueDepth,
		ProtocolJobsQueued:    host.metrics.protocolJobsQueued.Load(),
		ProtocolJobsHandled:   host.metrics.protocolJobsHandled.Load(),
		ProtocolJobsFailed:    host.metrics.protocolJobsFailed.Load(),
		ProtocolJobsDropped:   host.metrics.protocolJobsDropped.Load(),
		ProtocolQueueHigh:     highQueueDepth,
		ProtocolQueueNormal:   normalQueueDepth,
		ProtocolQueueLow:      lowQueueDepth,
		RequestsStarted:       host.metrics.requestsStarted.Load(),
		RequestsSucceeded:     host.metrics.requestsSucceeded.Load(),
		RequestsFailed:        host.metrics.requestsFailed.Load(),
		IdentifyStarted:       host.metrics.identifyStarted.Load(),
		IdentifySucceeded:     host.metrics.identifySucceeded.Load(),
		IdentifyFailed:        host.metrics.identifyFailed.Load(),
		PeerRecordsAccepted:   host.metrics.peerRecordsAccepted.Load(),
		PeerRecordsRejected:   host.metrics.peerRecordsRejected.Load(),
		FindNodeQueries:       host.metrics.findNodeQueries.Load(),
		FindNodeFailures:      host.metrics.findNodeFailures.Load(),
	}
}
