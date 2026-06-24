package p2p

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"runtime"
	"strings"
	"time"
)

const (
	defaultProtocolWorkerCount = 4
	defaultProtocolPartitions  = 4
	maxProtocolWorkerCount     = 1024
	defaultProtocolQueueSize   = 1024
	maxProtocolQueueSize       = 16384
	defaultProtocolJobTimeout  = 10 * time.Second
)

// ProtocolSchedulerConfig 保存协议调度配置 + 用优先级队列隔离慢业务处理和连接读循环。
type ProtocolSchedulerConfig struct {
	WorkerCount     int
	PartitionCount  int
	HighQueueSize   int
	NormalQueueSize int
	LowQueueSize    int
	JobTimeout      time.Duration
}

type protocolDispatcher struct {
	host       *Host
	config     ProtocolSchedulerConfig
	partitions []*protocolPartition
}

type protocolPartition struct {
	highQueue   chan protocolJob
	normalQueue chan protocolJob
	lowQueue    chan protocolJob
}

type protocolJob struct {
	connection Connection
	message    Message
	priority   MessagePriority
	enqueuedAt time.Time
}

func newProtocolDispatcher(host *Host, config ProtocolSchedulerConfig) *protocolDispatcher {
	normalized := normalizeProtocolSchedulerConfig(config)
	dispatcher := &protocolDispatcher{
		host:       host,
		config:     normalized,
		partitions: make([]*protocolPartition, normalized.PartitionCount),
	}
	for index := range dispatcher.partitions {
		dispatcher.partitions[index] = newProtocolPartition(normalized)
	}
	return dispatcher
}

func newProtocolPartition(config ProtocolSchedulerConfig) *protocolPartition {
	return &protocolPartition{
		highQueue:   make(chan protocolJob, config.HighQueueSize),
		normalQueue: make(chan protocolJob, config.NormalQueueSize),
		lowQueue:    make(chan protocolJob, config.LowQueueSize),
	}
}

func normalizeProtocolSchedulerConfig(config ProtocolSchedulerConfig) ProtocolSchedulerConfig {
	if config.WorkerCount <= 0 {
		config.WorkerCount = minInt(maxInt(runtime.NumCPU(), defaultProtocolWorkerCount), 16)
	}
	if config.WorkerCount > maxProtocolWorkerCount {
		config.WorkerCount = maxProtocolWorkerCount
	}
	if config.PartitionCount <= 0 {
		config.PartitionCount = config.WorkerCount
	}
	if config.PartitionCount > maxProtocolWorkerCount {
		config.PartitionCount = maxProtocolWorkerCount
	}
	if config.PartitionCount != config.WorkerCount {
		config.WorkerCount = config.PartitionCount
	}
	if config.PartitionCount <= 0 {
		config.PartitionCount = 1
		config.WorkerCount = 1
	}
	config.HighQueueSize = normalizeProtocolQueueSize(config.HighQueueSize, config.WorkerCount)
	config.NormalQueueSize = normalizeProtocolQueueSize(config.NormalQueueSize, config.WorkerCount)
	config.LowQueueSize = normalizeProtocolQueueSize(config.LowQueueSize, config.WorkerCount)
	if config.JobTimeout <= 0 {
		config.JobTimeout = defaultProtocolJobTimeout
	}
	return config
}

func normalizeProtocolQueueSize(size int, workerCount int) int {
	if size > maxProtocolQueueSize {
		return maxProtocolQueueSize
	}
	if size > 0 {
		return size
	}
	autoSize := workerCount * 256
	if autoSize < defaultProtocolQueueSize {
		return defaultProtocolQueueSize
	}
	if autoSize > maxProtocolQueueSize {
		return maxProtocolQueueSize
	}
	return autoSize
}

func (dispatcher *protocolDispatcher) start(ctx context.Context) {
	if dispatcher == nil {
		return
	}
	for workerID := 0; workerID < dispatcher.config.WorkerCount; workerID++ {
		partition := dispatcher.partitions[workerID%len(dispatcher.partitions)]
		go dispatcher.worker(ctx, workerID, partition)
	}
}

func (dispatcher *protocolDispatcher) enqueue(connection Connection, message Message) error {
	if dispatcher == nil {
		return ErrHostClosed
	}
	job := protocolJob{
		connection: connection,
		message:    message,
		priority:   dispatcher.priority(message),
		enqueuedAt: time.Now(),
	}
	partition := dispatcher.partition(message)
	queue := partition.queue(job.priority)
	select {
	case queue <- job:
		dispatcher.host.metrics.protocolJobsQueued.Add(1)
		return nil
	default:
		dispatcher.host.metrics.protocolJobsDropped.Add(1)
		return fmt.Errorf("%w: priority %d", ErrProtocolQueueFull, job.priority)
	}
}

func (dispatcher *protocolDispatcher) priority(message Message) MessagePriority {
	return dispatcher.host.messagePriority(message)
}

func (dispatcher *protocolDispatcher) partition(message Message) *protocolPartition {
	index := dispatcher.partitionIndex(message)
	return dispatcher.partitions[index]
}

func (dispatcher *protocolDispatcher) partitionIndex(message Message) int {
	spec, ok := dispatcher.host.registry.Spec(message.Type)
	if !ok {
		return protocolPartitionIndex(message, len(dispatcher.partitions))
	}
	return protocolPartitionIndexForSpec(message, len(dispatcher.partitions), spec)
}

func (partition *protocolPartition) queue(priority MessagePriority) chan protocolJob {
	switch priority {
	case MessagePriorityHigh:
		return partition.highQueue
	case MessagePriorityLow:
		return partition.lowQueue
	default:
		return partition.normalQueue
	}
}

func (dispatcher *protocolDispatcher) worker(ctx context.Context, workerID int, partition *protocolPartition) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		job, ok := dispatcher.nextJob(ctx, partition)
		if !ok {
			return
		}
		dispatcher.handleJob(ctx, workerID, job)
	}
}

func (dispatcher *protocolDispatcher) nextJob(ctx context.Context, partition *protocolPartition) (protocolJob, bool) {
	for {
		select {
		case <-ctx.Done():
			return protocolJob{}, false
		default:
		}
		if job, ok := tryReadProtocolJob(partition.highQueue); ok {
			return job, true
		}
		if job, ok := tryReadProtocolJob(partition.normalQueue); ok {
			return job, true
		}
		if job, ok := tryReadProtocolJob(partition.lowQueue); ok {
			return job, true
		}
		select {
		case <-ctx.Done():
			return protocolJob{}, false
		case job := <-partition.highQueue:
			return job, true
		case job := <-partition.normalQueue:
			return job, true
		case job := <-partition.lowQueue:
			return job, true
		}
	}
}

func tryReadProtocolJob(queue chan protocolJob) (protocolJob, bool) {
	select {
	case job := <-queue:
		return job, true
	default:
		return protocolJob{}, false
	}
}

func (dispatcher *protocolDispatcher) handleJob(parent context.Context, workerID int, job protocolJob) {
	host := dispatcher.host
	if queueDelay := time.Since(job.enqueuedAt); queueDelay > host.peerProtection.slowHandlerThreshold() {
		host.logger.Warn("p2p protocol job queue delay",
			slog.String("peer_id", job.message.FromPeerID),
			slog.String("message_id", job.message.ID),
			slog.Uint64("protocol_id", uint64(job.message.Type)),
			slog.Uint64("worker_id", uint64(workerID)),
			slog.Duration("delay", queueDelay),
		)
	}

	jobContext, cancel := context.WithTimeout(parent, dispatcher.config.JobTimeout)
	defer cancel()
	startedAt := time.Now()
	result, err := host.HandleMessage(jobContext, job.message)
	host.protocolHandlerDuration(startedAt, job.message)
	if err != nil {
		host.metrics.messagesRejected.Add(1)
		host.metrics.protocolJobsFailed.Add(1)
		if host.protocolJobFailureIsExpectedUnregisteredProtocol(err, job.message.Type) {
			host.logger.Debug("p2p protocol job ignored",
				slog.String("peer_id", job.message.FromPeerID),
				slog.String("message_id", job.message.ID),
				slog.Uint64("protocol_id", uint64(job.message.Type)),
				slog.Uint64("worker_id", uint64(workerID)),
				slog.Any("error", err),
			)
			return
		}
		host.logger.Warn("p2p protocol job failed",
			slog.String("peer_id", job.message.FromPeerID),
			slog.String("message_id", job.message.ID),
			slog.Uint64("protocol_id", uint64(job.message.Type)),
			slog.Uint64("worker_id", uint64(workerID)),
			slog.Any("error", err),
		)
		return
	}
	if result.HasResponse {
		if err := host.writeConnectionMessage(jobContext, job.connection, job.message.FromPeerID, result.Message); err != nil {
			host.metrics.protocolJobsFailed.Add(1)
			host.logger.Warn("p2p protocol response write failed",
				slog.String("peer_id", job.message.FromPeerID),
				slog.String("message_id", job.message.ID),
				slog.Uint64("protocol_id", uint64(job.message.Type)),
				slog.Uint64("worker_id", uint64(workerID)),
				slog.Any("error", err),
			)
			return
		}
	}
	host.metrics.protocolJobsHandled.Add(1)
}

func (host *Host) protocolJobFailureIsExpectedUnregisteredProtocol(err error, protocolID ProtocolID) bool {
	if !errors.Is(err, ErrProtocolNotFound) {
		return false
	}
	if host == nil {
		return false
	}
	_, ignored := host.ignoredUnregisteredProtocols[protocolID]
	return ignored
}

// isKnownProtocolID 判断协议号是否属于内置协议 + 非验证者节点未注册某些内置协议时只需要调试日志。
func isKnownProtocolID(protocolID ProtocolID) bool {
	for _, spec := range DefaultProtocolSpecs() {
		if spec.ID == protocolID {
			return true
		}
	}
	switch protocolID {
	case ProtocolPoSBlockByHashV1,
		ProtocolPoSBlockByHeightV1,
		ProtocolPoSStateSnapshotV1,
		ProtocolPoSStatusV1,
		ProtocolPoSEvidenceV1,
		ProtocolPoSBlockLocatorV1,
		ProtocolPoSCommonAncestorV1,
		ProtocolPoSRPCForwardV1:
		return true
	default:
		return false
	}
}

func (dispatcher *protocolDispatcher) queueDepths() (uint64, uint64, uint64) {
	if dispatcher == nil {
		return 0, 0, 0
	}
	highDepth := uint64(0)
	normalDepth := uint64(0)
	lowDepth := uint64(0)
	for _, partition := range dispatcher.partitions {
		highDepth += uint64(len(partition.highQueue))
		normalDepth += uint64(len(partition.normalQueue))
		lowDepth += uint64(len(partition.lowQueue))
	}
	return highDepth, normalDepth, lowDepth
}

func protocolPartitionIndex(message Message, partitionCount int) int {
	return protocolPartitionIndexWithShard(message, partitionCount, "")
}

func protocolPartitionIndexForSpec(message Message, partitionCount int, spec ProtocolSpec) int {
	shardKey, ok := protocolParallelShardKey(message, spec)
	if !ok {
		return protocolPartitionIndex(message, partitionCount)
	}
	return protocolPartitionIndexWithShard(message, partitionCount, shardKey)
}

func protocolPartitionIndexWithShard(message Message, partitionCount int, shardKey string) int {
	if partitionCount <= 1 {
		return 0
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(message.FromPeerID))
	var protocolBytes [4]byte
	binary.LittleEndian.PutUint32(protocolBytes[:], uint32(message.Type))
	_, _ = hash.Write(protocolBytes[:])
	if shardKey != "" {
		_, _ = hash.Write([]byte(shardKey))
	}
	return int(hash.Sum32() % uint32(partitionCount))
}

// protocolParallelShardKey 返回并行协议分片键 + 无状态按消息打散，有状态按业务键保序。
func protocolParallelShardKey(message Message, spec ProtocolSpec) (string, bool) {
	switch spec.Concurrency {
	case ProtocolConcurrencyStateless:
		return protocolStatelessShardKey(message), true
	case ProtocolConcurrencyStateKey:
		return protocolStateShardKey(message, spec)
	default:
		return "", false
	}
}

func protocolStatelessShardKey(message Message) string {
	if message.IsRequestResponse() && message.RequestID != "" {
		return strings.ToLower(message.RequestID)
	}
	return strings.ToLower(message.ID)
}

func protocolStateShardKey(message Message, spec ProtocolSpec) (shardKey string, ok bool) {
	if spec.PartitionKey == nil {
		return "", false
	}
	defer func() {
		if recover() != nil {
			shardKey = ""
			ok = false
		}
	}()
	shardKey = strings.TrimSpace(spec.PartitionKey(message))
	if shardKey == "" {
		return "", false
	}
	return strings.ToLower(shardKey), true
}
