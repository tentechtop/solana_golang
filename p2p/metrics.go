package p2p

import "sync/atomic"

type p2pMetrics struct {
	inboundRejected       atomic.Uint64
	maxConnectionRejected atomic.Uint64
	secureHandshakeOK     atomic.Uint64
	secureHandshakeFailed atomic.Uint64
	messagesRead          atomic.Uint64
	messagesWritten       atomic.Uint64
	messagesRejected      atomic.Uint64
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
	SecureHandshakeOK     uint64
	SecureHandshakeFailed uint64
	MessagesRead          uint64
	MessagesWritten       uint64
	MessagesRejected      uint64
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

	return HostMetrics{
		PeerCount:             peerCount,
		ConnectionCount:       connectionCount,
		InboundRejected:       host.metrics.inboundRejected.Load(),
		MaxConnectionRejected: host.metrics.maxConnectionRejected.Load(),
		SecureHandshakeOK:     host.metrics.secureHandshakeOK.Load(),
		SecureHandshakeFailed: host.metrics.secureHandshakeFailed.Load(),
		MessagesRead:          host.metrics.messagesRead.Load(),
		MessagesWritten:       host.metrics.messagesWritten.Load(),
		MessagesRejected:      host.metrics.messagesRejected.Load(),
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
