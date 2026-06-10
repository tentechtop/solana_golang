package p2p

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultKADBucketSize   = 20
	defaultKADFindNodeSize = 20
)

// PeerQualityState 表示节点质量分层 + 用于桶满淘汰和健康快照。
type PeerQualityState string

const (
	PeerQualityGood         PeerQualityState = "good"
	PeerQualityQuestionable PeerQualityState = "questionable"
	PeerQualityBad          PeerQualityState = "bad"
)

// KADRoutingTableConfig 保存路由表配置 + 支持测试和部署调节 K 桶容量。
type KADRoutingTableConfig struct {
	LocalPeerID  string
	BucketSize   int
	FindNodeSize int
}

// KADCandidatePeerSnapshot 保存候选节点快照 + 桶满时延迟重试使用。
type KADCandidatePeerSnapshot struct {
	PeerID               string
	SourcePeerID         string
	FirstSeenUnixMilli   int64
	LastSeenUnixMilli    int64
	Attempts             int
	NextAttemptUnixMilli int64
	LastFailureReason    string
}

// KADPeerScoreSnapshot 保存节点质量快照 + 便于监控和淘汰决策解释。
type KADPeerScoreSnapshot struct {
	PeerID               string
	State                PeerQualityState
	Score                int
	SuccessCount         uint64
	FailureCount         uint64
	ConsecutiveFailures  uint32
	LastSeenUnixMilli    int64
	LastSuccessUnixMilli int64
	LastFailureUnixMilli int64
}

// KADRoutingTableHealthSnapshot 保存路由表健康状态 + 用于监控桶覆盖和节点质量。
type KADRoutingTableHealthSnapshot struct {
	TotalPeers         int
	OnlinePeers        int
	CandidatePeers     int
	GoodPeers          int
	QuestionablePeers  int
	BadPeers           int
	BucketCount        int
	NonEmptyBuckets    int
	SparseBuckets      int
	AverageBucketFill  float64
	LookupSuccessCount uint64
	LookupFailureCount uint64
}

type kadCandidatePeer struct {
	peer                 Peer
	sourcePeerID         string
	firstSeenUnixMilli   int64
	lastSeenUnixMilli    int64
	attempts             int
	nextAttemptUnixMilli int64
	lastFailureReason    string
}

type kadPeerScore struct {
	successCount         uint64
	failureCount         uint64
	consecutiveFailures  uint32
	lastSeenUnixMilli    int64
	lastSuccessUnixMilli int64
	lastFailureUnixMilli int64
}

// KADRoutingTable 管理 Kademlia 路由表 + 使用 256 个 K 桶覆盖 32 字节节点空间。
type KADRoutingTable struct {
	mutex              sync.RWMutex
	localPeerID        string
	localID            [peerIDByteSize]byte
	bucketSize         int
	findNodeSize       int
	buckets            []*kadBucket
	candidates         map[string]kadCandidatePeer
	scores             map[string]kadPeerScore
	lookupSuccessCount atomic.Uint64
	lookupFailureCount atomic.Uint64
}

// NewKADRoutingTable 创建 KAD 路由表 + 按本节点 ID 初始化固定桶集合。
func NewKADRoutingTable(config KADRoutingTableConfig) (*KADRoutingTable, error) {
	localID, err := KADPeerIDBytes(config.LocalPeerID)
	if err != nil {
		return nil, err
	}
	bucketSize := normalizeKADBucketSize(config.BucketSize)
	table := &KADRoutingTable{
		localPeerID:  config.LocalPeerID,
		localID:      localID,
		bucketSize:   bucketSize,
		findNodeSize: normalizeKADFindNodeSize(config.FindNodeSize),
		buckets:      make([]*kadBucket, kadIdentifierBits),
		candidates:   make(map[string]kadCandidatePeer),
		scores:       make(map[string]kadPeerScore),
	}
	for index := range table.buckets {
		table.buckets[index] = newKADBucket(index, bucketSize)
	}
	return table, nil
}

// AddPeer 添加或更新节点 + 桶满时按质量淘汰或进入候选集合。
func (table *KADRoutingTable) AddPeer(peer Peer) error {
	if err := peer.Validate(); err != nil {
		return err
	}
	if peer.ID == table.localPeerID {
		return nil
	}
	peerID, err := KADPeerIDBytes(peer.ID)
	if err != nil {
		return err
	}
	bucketIndex, ok := KADBucketIndex(table.localID, peerID)
	if !ok {
		return nil
	}

	table.mutex.Lock()
	defer table.mutex.Unlock()
	bucket := table.buckets[bucketIndex]
	table.recordPeerSuccessLocked(peer.ID, peer)
	if bucket.contains(peer.ID) || bucket.size() < table.bucketSize {
		bucket.upsertFront(peer)
		delete(table.candidates, peer.ID)
		return nil
	}

	candidate, exists := bucket.evictionCandidate()
	if exists && comparePeerEviction(peer, candidate) > 0 {
		bucket.remove(candidate.ID)
		bucket.upsertFront(peer)
		delete(table.candidates, peer.ID)
		table.recordPeerFailureLocked(candidate.ID)
		return nil
	}
	table.rememberCandidateLocked(peer, "", "bucket-full")
	return nil
}

// RemovePeer 删除节点 + 同时清理候选集合和质量统计。
func (table *KADRoutingTable) RemovePeer(peerID string) {
	table.mutex.Lock()
	defer table.mutex.Unlock()
	for _, bucket := range table.buckets {
		if bucket.contains(peerID) {
			bucket.remove(peerID)
			break
		}
	}
	delete(table.candidates, peerID)
	delete(table.scores, peerID)
}

// TouchPeer 标记节点成功交互 + 将节点移到桶头部提升活跃度。
func (table *KADRoutingTable) TouchPeer(peerID string) {
	table.mutex.Lock()
	defer table.mutex.Unlock()
	for _, bucket := range table.buckets {
		peer, ok := bucket.peers[peerID]
		if !ok {
			continue
		}
		table.recordPeerSuccessLocked(peerID, peer)
		bucket.upsertFront(peer)
		return
	}
}

// RecordPeerFailure 记录节点失败 + 连续失败过多时质量降级。
func (table *KADRoutingTable) RecordPeerFailure(peerID string) {
	table.mutex.Lock()
	defer table.mutex.Unlock()
	table.recordPeerFailureLocked(peerID)
}

// ClosestPeers 查询距离目标最近的节点 + 使用异或距离排序后截断。
func (table *KADRoutingTable) ClosestPeers(targetPeerID string, limit int) ([]Peer, error) {
	targetID, err := KADPeerIDBytes(targetPeerID)
	if err != nil {
		table.lookupFailureCount.Add(1)
		return nil, err
	}
	peers := table.allPeers()
	sort.SliceStable(peers, func(first int, second int) bool {
		firstID, firstErr := KADPeerIDBytes(peers[first].ID)
		secondID, secondErr := KADPeerIDBytes(peers[second].ID)
		if firstErr != nil || secondErr != nil {
			return firstErr == nil
		}
		firstDistance := KADCalculateDistance(targetID, firstID)
		secondDistance := KADCalculateDistance(targetID, secondID)
		return KADCompareDistance(firstDistance, secondDistance) < 0
	})

	if limit <= 0 || limit > table.findNodeSize {
		limit = table.findNodeSize
	}
	if len(peers) > limit {
		peers = peers[:limit]
	}
	table.lookupSuccessCount.Add(1)
	return peers, nil
}

// BucketIndex 查询节点所属桶 + 供测试和刷新任务定位。
func (table *KADRoutingTable) BucketIndex(peerID string) (int, bool, error) {
	targetID, err := KADPeerIDBytes(peerID)
	if err != nil {
		return 0, false, err
	}
	index, ok := KADBucketIndex(table.localID, targetID)
	return index, ok, nil
}

// CandidateSnapshots 返回候选节点快照 + 供后续探测任务消费。
func (table *KADRoutingTable) CandidateSnapshots() []KADCandidatePeerSnapshot {
	table.mutex.RLock()
	defer table.mutex.RUnlock()
	snapshots := make([]KADCandidatePeerSnapshot, 0, len(table.candidates))
	for peerID, candidate := range table.candidates {
		snapshots = append(snapshots, KADCandidatePeerSnapshot{
			PeerID:               peerID,
			SourcePeerID:         candidate.sourcePeerID,
			FirstSeenUnixMilli:   candidate.firstSeenUnixMilli,
			LastSeenUnixMilli:    candidate.lastSeenUnixMilli,
			Attempts:             candidate.attempts,
			NextAttemptUnixMilli: candidate.nextAttemptUnixMilli,
			LastFailureReason:    candidate.lastFailureReason,
		})
	}
	return snapshots
}

// PeerScoreSnapshots 返回节点质量快照 + 便于诊断路由表淘汰行为。
func (table *KADRoutingTable) PeerScoreSnapshots() []KADPeerScoreSnapshot {
	table.mutex.RLock()
	defer table.mutex.RUnlock()
	snapshots := make([]KADPeerScoreSnapshot, 0, len(table.scores))
	for peerID, score := range table.scores {
		snapshots = append(snapshots, KADPeerScoreSnapshot{
			PeerID:               peerID,
			State:                qualityState(score),
			Score:                scoreValue(score),
			SuccessCount:         score.successCount,
			FailureCount:         score.failureCount,
			ConsecutiveFailures:  score.consecutiveFailures,
			LastSeenUnixMilli:    score.lastSeenUnixMilli,
			LastSuccessUnixMilli: score.lastSuccessUnixMilli,
			LastFailureUnixMilli: score.lastFailureUnixMilli,
		})
	}
	return snapshots
}

// HealthSnapshot 返回路由表健康快照 + 统计桶覆盖和节点质量。
func (table *KADRoutingTable) HealthSnapshot() KADRoutingTableHealthSnapshot {
	table.mutex.RLock()
	defer table.mutex.RUnlock()
	totalPeers := 0
	onlinePeers := 0
	nonEmptyBuckets := 0
	sparseBuckets := 0
	for _, bucket := range table.buckets {
		size := bucket.size()
		totalPeers += size
		if size > 0 {
			nonEmptyBuckets++
		}
		if size > 0 && size < maxInt(1, table.bucketSize/4) {
			sparseBuckets++
		}
		for _, peer := range bucket.peers {
			if peer.Status == PeerStatusConnected {
				onlinePeers++
			}
		}
	}
	goodPeers, questionablePeers, badPeers := table.countQualityLocked()
	averageFill := 0.0
	if len(table.buckets) > 0 {
		averageFill = float64(totalPeers) / float64(len(table.buckets))
	}
	return KADRoutingTableHealthSnapshot{
		TotalPeers:         totalPeers,
		OnlinePeers:        onlinePeers,
		CandidatePeers:     len(table.candidates),
		GoodPeers:          goodPeers,
		QuestionablePeers:  questionablePeers,
		BadPeers:           badPeers,
		BucketCount:        len(table.buckets),
		NonEmptyBuckets:    nonEmptyBuckets,
		SparseBuckets:      sparseBuckets,
		AverageBucketFill:  averageFill,
		LookupSuccessCount: table.lookupSuccessCount.Load(),
		LookupFailureCount: table.lookupFailureCount.Load(),
	}
}

func (table *KADRoutingTable) allPeers() []Peer {
	table.mutex.RLock()
	defer table.mutex.RUnlock()
	peers := make([]Peer, 0)
	for _, bucket := range table.buckets {
		peers = append(peers, bucket.peersSnapshot()...)
	}
	return peers
}

func (table *KADRoutingTable) rememberCandidateLocked(peer Peer, sourcePeerID string, reason string) {
	now := time.Now().UnixMilli()
	candidate := table.candidates[peer.ID]
	if candidate.firstSeenUnixMilli == 0 {
		candidate.firstSeenUnixMilli = now
	}
	candidate.peer = peer.Clone()
	candidate.sourcePeerID = sourcePeerID
	candidate.lastSeenUnixMilli = now
	candidate.attempts++
	candidate.nextAttemptUnixMilli = now + int64(time.Minute/time.Millisecond)
	candidate.lastFailureReason = reason
	table.candidates[peer.ID] = candidate
}

func (table *KADRoutingTable) recordPeerSuccessLocked(peerID string, peer Peer) {
	score := table.scores[peerID]
	now := time.Now().UnixMilli()
	score.successCount++
	score.consecutiveFailures = 0
	score.lastSeenUnixMilli = now
	score.lastSuccessUnixMilli = now
	if peer.LastSeenUnixMilli > 0 {
		score.lastSeenUnixMilli = peer.LastSeenUnixMilli
	}
	table.scores[peerID] = score
}

func (table *KADRoutingTable) recordPeerFailureLocked(peerID string) {
	score := table.scores[peerID]
	now := time.Now().UnixMilli()
	score.failureCount++
	score.consecutiveFailures++
	score.lastSeenUnixMilli = now
	score.lastFailureUnixMilli = now
	table.scores[peerID] = score
}

func (table *KADRoutingTable) countQualityLocked() (int, int, int) {
	goodPeers := 0
	questionablePeers := 0
	badPeers := 0
	for _, score := range table.scores {
		switch qualityState(score) {
		case PeerQualityGood:
			goodPeers++
		case PeerQualityQuestionable:
			questionablePeers++
		case PeerQualityBad:
			badPeers++
		}
	}
	return goodPeers, questionablePeers, badPeers
}

func qualityState(score kadPeerScore) PeerQualityState {
	if score.consecutiveFailures >= 3 {
		return PeerQualityBad
	}
	if score.consecutiveFailures > 0 || score.failureCount > score.successCount {
		return PeerQualityQuestionable
	}
	return PeerQualityGood
}

func scoreValue(score kadPeerScore) int {
	return int(score.successCount)*10 - int(score.failureCount)*20 - int(score.consecutiveFailures)*30
}

func normalizeKADBucketSize(size int) int {
	if size <= 0 {
		return defaultKADBucketSize
	}
	return size
}

func normalizeKADFindNodeSize(size int) int {
	if size <= 0 {
		return defaultKADFindNodeSize
	}
	return size
}

func maxInt(first int, second int) int {
	if first > second {
		return first
	}
	return second
}

func validateKADRoutingTable(table *KADRoutingTable) error {
	if table == nil {
		return fmt.Errorf("p2p: nil kad routing table")
	}
	return nil
}
