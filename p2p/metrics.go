package p2p

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var requestLatencyBucketMicros = [...]uint64{
	1_000,
	5_000,
	10_000,
	25_000,
	50_000,
	100_000,
	250_000,
	500_000,
	1_000_000,
	2_000_000,
	5_000_000,
	10_000_000,
	^uint64(0),
}

type p2pMetrics struct {
	inboundRejected            atomic.Uint64
	maxConnectionRejected      atomic.Uint64
	perIPRejected              atomic.Uint64
	secureHandshakeOK          atomic.Uint64
	secureHandshakeFailed      atomic.Uint64
	messagesRead               atomic.Uint64
	messagesWritten            atomic.Uint64
	messagesRejected           atomic.Uint64
	messagesRateLimited        atomic.Uint64
	controlMessagesRateLimited atomic.Uint64
	dataMessagesRateLimited    atomic.Uint64
	duplicateMessages          atomic.Uint64
	peerBlocks                 atomic.Uint64
	dialBackoffs               atomic.Uint64
	slowProtocolHandlers       atomic.Uint64
	broadcastPeersDropped      atomic.Uint64
	writeQueueEnqueued         atomic.Uint64
	writeQueueFlushed          atomic.Uint64
	writeQueueDropped          atomic.Uint64
	writeQueueErrors           atomic.Uint64
	protocolJobsQueued         atomic.Uint64
	protocolJobsHandled        atomic.Uint64
	protocolJobsFailed         atomic.Uint64
	protocolJobsDropped        atomic.Uint64
	requestsStarted            atomic.Uint64
	requestsSucceeded          atomic.Uint64
	requestsFailed             atomic.Uint64
	requestLatencyBuckets      [len(requestLatencyBucketMicros)]atomic.Uint64
	requestLatencyMaxMicros    atomic.Uint64
	identifyStarted            atomic.Uint64
	identifySucceeded          atomic.Uint64
	identifyFailed             atomic.Uint64
	peerRecordsAccepted        atomic.Uint64
	peerRecordsRejected        atomic.Uint64
	findNodeQueries            atomic.Uint64
	findNodeFailures           atomic.Uint64
}

// HostMetrics 保存 P2P 运行指标快照 + 供 RPC、日志和压测工具读取。
type HostMetrics struct {
	PeerCount                  uint64
	ConnectionCount            uint64
	InboundRejected            uint64
	MaxConnectionRejected      uint64
	PerIPRejected              uint64
	SecureHandshakeOK          uint64
	SecureHandshakeFailed      uint64
	MessagesRead               uint64
	MessagesWritten            uint64
	MessagesRejected           uint64
	MessagesRateLimited        uint64
	ControlMessagesRateLimited uint64
	DataMessagesRateLimited    uint64
	DuplicateMessages          uint64
	PeerBlocks                 uint64
	DialBackoffs               uint64
	SlowProtocolHandlers       uint64
	BroadcastPeersDropped      uint64
	WriteQueueEnqueued         uint64
	WriteQueueFlushed          uint64
	WriteQueueDropped          uint64
	WriteQueueErrors           uint64
	WriteQueueDepth            uint64
	ProtocolJobsQueued         uint64
	ProtocolJobsHandled        uint64
	ProtocolJobsFailed         uint64
	ProtocolJobsDropped        uint64
	ProtocolQueueHigh          uint64
	ProtocolQueueNormal        uint64
	ProtocolQueueLow           uint64
	RequestsStarted            uint64
	RequestsSucceeded          uint64
	RequestsFailed             uint64
	PendingRequests            uint64
	RequestLatencyP50Millis    float64
	RequestLatencyP95Millis    float64
	RequestLatencyP99Millis    float64
	RequestLatencyMaxMillis    float64
	IdentifyStarted            uint64
	IdentifySucceeded          uint64
	IdentifyFailed             uint64
	PeerRecordsAccepted        uint64
	PeerRecordsRejected        uint64
	FindNodeQueries            uint64
	FindNodeFailures           uint64
	RuntimeGoroutines          uint64
	RuntimeAllocBytes          uint64
	RuntimeHeapAllocBytes      uint64
	RuntimeSysBytes            uint64
	RuntimeRSSBytes            uint64
}

// Metrics 返回 P2P 指标快照 + 使用原子计数避免阻塞网络热路径。
func (host *Host) Metrics() HostMetrics {
	host.mutex.RLock()
	peerCount := uint64(len(host.peers))
	connectionCount := uint64(len(host.connections))
	host.mutex.RUnlock()
	highQueueDepth, normalQueueDepth, lowQueueDepth := host.protocolDispatcher.queueDepths()
	writeQueueDepth := host.writeQueueDepth()
	pendingRequests := uint64(0)
	if host.requests != nil {
		pendingRequests = host.requests.pendingCount()
	}
	runtimeMetrics := runtimeMetricsSnapshot()

	return HostMetrics{
		PeerCount:                  peerCount,
		ConnectionCount:            connectionCount,
		InboundRejected:            host.metrics.inboundRejected.Load(),
		MaxConnectionRejected:      host.metrics.maxConnectionRejected.Load(),
		PerIPRejected:              host.metrics.perIPRejected.Load(),
		SecureHandshakeOK:          host.metrics.secureHandshakeOK.Load(),
		SecureHandshakeFailed:      host.metrics.secureHandshakeFailed.Load(),
		MessagesRead:               host.metrics.messagesRead.Load(),
		MessagesWritten:            host.metrics.messagesWritten.Load(),
		MessagesRejected:           host.metrics.messagesRejected.Load(),
		MessagesRateLimited:        host.metrics.messagesRateLimited.Load(),
		ControlMessagesRateLimited: host.metrics.controlMessagesRateLimited.Load(),
		DataMessagesRateLimited:    host.metrics.dataMessagesRateLimited.Load(),
		DuplicateMessages:          host.metrics.duplicateMessages.Load(),
		PeerBlocks:                 host.metrics.peerBlocks.Load(),
		DialBackoffs:               host.metrics.dialBackoffs.Load(),
		SlowProtocolHandlers:       host.metrics.slowProtocolHandlers.Load(),
		BroadcastPeersDropped:      host.metrics.broadcastPeersDropped.Load(),
		WriteQueueEnqueued:         host.metrics.writeQueueEnqueued.Load(),
		WriteQueueFlushed:          host.metrics.writeQueueFlushed.Load(),
		WriteQueueDropped:          host.metrics.writeQueueDropped.Load(),
		WriteQueueErrors:           host.metrics.writeQueueErrors.Load(),
		WriteQueueDepth:            writeQueueDepth,
		ProtocolJobsQueued:         host.metrics.protocolJobsQueued.Load(),
		ProtocolJobsHandled:        host.metrics.protocolJobsHandled.Load(),
		ProtocolJobsFailed:         host.metrics.protocolJobsFailed.Load(),
		ProtocolJobsDropped:        host.metrics.protocolJobsDropped.Load(),
		ProtocolQueueHigh:          highQueueDepth,
		ProtocolQueueNormal:        normalQueueDepth,
		ProtocolQueueLow:           lowQueueDepth,
		RequestsStarted:            host.metrics.requestsStarted.Load(),
		RequestsSucceeded:          host.metrics.requestsSucceeded.Load(),
		RequestsFailed:             host.metrics.requestsFailed.Load(),
		PendingRequests:            pendingRequests,
		RequestLatencyP50Millis:    host.metrics.requestLatencyPercentileMillis(50),
		RequestLatencyP95Millis:    host.metrics.requestLatencyPercentileMillis(95),
		RequestLatencyP99Millis:    host.metrics.requestLatencyPercentileMillis(99),
		RequestLatencyMaxMillis:    float64(host.metrics.requestLatencyMaxMicros.Load()) / 1000,
		IdentifyStarted:            host.metrics.identifyStarted.Load(),
		IdentifySucceeded:          host.metrics.identifySucceeded.Load(),
		IdentifyFailed:             host.metrics.identifyFailed.Load(),
		PeerRecordsAccepted:        host.metrics.peerRecordsAccepted.Load(),
		PeerRecordsRejected:        host.metrics.peerRecordsRejected.Load(),
		FindNodeQueries:            host.metrics.findNodeQueries.Load(),
		FindNodeFailures:           host.metrics.findNodeFailures.Load(),
		RuntimeGoroutines:          runtimeMetrics.goroutines,
		RuntimeAllocBytes:          runtimeMetrics.allocBytes,
		RuntimeHeapAllocBytes:      runtimeMetrics.heapAllocBytes,
		RuntimeSysBytes:            runtimeMetrics.sysBytes,
		RuntimeRSSBytes:            runtimeMetrics.rssBytes,
	}
}

func (metrics *p2pMetrics) recordRequestLatency(elapsed time.Duration) {
	if metrics == nil || elapsed < 0 {
		return
	}
	elapsedMicros := uint64(elapsed.Microseconds())
	if elapsed > 0 && elapsedMicros == 0 {
		elapsedMicros = 1
	}
	for index, upperBoundMicros := range requestLatencyBucketMicros {
		if elapsedMicros <= upperBoundMicros {
			metrics.requestLatencyBuckets[index].Add(1)
			break
		}
	}
	for {
		currentMax := metrics.requestLatencyMaxMicros.Load()
		if elapsedMicros <= currentMax {
			return
		}
		if metrics.requestLatencyMaxMicros.CompareAndSwap(currentMax, elapsedMicros) {
			return
		}
	}
}

func (metrics *p2pMetrics) requestLatencyPercentileMillis(percentile uint64) float64 {
	if metrics == nil || percentile == 0 {
		return 0
	}
	total := uint64(0)
	for index := range metrics.requestLatencyBuckets {
		total += metrics.requestLatencyBuckets[index].Load()
	}
	if total == 0 {
		return 0
	}
	target := (total*percentile + 99) / 100
	if target == 0 {
		target = 1
	}
	cumulative := uint64(0)
	for index, upperBoundMicros := range requestLatencyBucketMicros {
		cumulative += metrics.requestLatencyBuckets[index].Load()
		if cumulative < target {
			continue
		}
		if upperBoundMicros == ^uint64(0) {
			return float64(metrics.requestLatencyMaxMicros.Load()) / 1000
		}
		return float64(upperBoundMicros) / 1000
	}
	return float64(metrics.requestLatencyMaxMicros.Load()) / 1000
}

type runtimeMetrics struct {
	goroutines     uint64
	allocBytes     uint64
	heapAllocBytes uint64
	sysBytes       uint64
	rssBytes       uint64
}

func runtimeMetricsSnapshot() runtimeMetrics {
	var memoryStats runtime.MemStats
	runtime.ReadMemStats(&memoryStats)
	return runtimeMetrics{
		goroutines:     uint64(runtime.NumGoroutine()),
		allocBytes:     memoryStats.Alloc,
		heapAllocBytes: memoryStats.HeapAlloc,
		sysBytes:       memoryStats.Sys,
		rssBytes:       processRSSBytes(),
	}
}

func processRSSBytes() uint64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "VmRSS:" {
			continue
		}
		kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kilobytes * 1024
	}
	return 0
}
