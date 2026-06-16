package main

import (
	"context"
	"sync/atomic"
	"time"
)

type nodeMetrics struct {
	blocksProduced    atomic.Uint64
	proposalsAccepted atomic.Uint64
	votesSent         atomic.Uint64
	qcFormed          atomic.Uint64
	qcReceived        atomic.Uint64
	forkDecisions     atomic.Uint64
	reorgs            atomic.Uint64
	orphanStored      atomic.Uint64
	syncRequests      atomic.Uint64
	syncFailures      atomic.Uint64
	transactionsIn    atomic.Uint64
	transactionsDrop  atomic.Uint64
	evidenceReceived  atomic.Uint64
}

type nodeMetricsSnapshot struct {
	BlocksProduced    uint64 `json:"blocks_produced"`
	ProposalsAccepted uint64 `json:"proposals_accepted"`
	VotesSent         uint64 `json:"votes_sent"`
	QCFormed          uint64 `json:"qc_formed"`
	QCReceived        uint64 `json:"qc_received"`
	ForkDecisions     uint64 `json:"fork_decisions"`
	Reorgs            uint64 `json:"reorgs"`
	OrphanStored      uint64 `json:"orphan_stored"`
	SyncRequests      uint64 `json:"sync_requests"`
	SyncFailures      uint64 `json:"sync_failures"`
	TransactionsIn    uint64 `json:"transactions_in"`
	TransactionsDrop  uint64 `json:"transactions_drop"`
	EvidenceReceived  uint64 `json:"evidence_received"`
}

type nodeOperationalMetrics struct {
	NodeName          string              `json:"node_name"`
	PeerID            string              `json:"peer_id"`
	CurrentSlot       uint64              `json:"current_slot"`
	CurrentLeader     string              `json:"current_leader,omitempty"`
	HeadHeight        uint64              `json:"head_height"`
	HeadSlot          uint64              `json:"head_slot"`
	BlockHash         string              `json:"block_hash"`
	StateRoot         string              `json:"state_root"`
	QCHash            string              `json:"qc_hash"`
	QCHeight          uint64              `json:"qc_height"`
	FinalizedHeight   uint64              `json:"finalized_height"`
	FinalityDepth     uint64              `json:"finality_depth"`
	MempoolSize       int                 `json:"mempool_size"`
	ValidatorCount    int                 `json:"validator_count"`
	KnownPeerCount    int                 `json:"known_peer_count"`
	P2PSecure         bool                `json:"p2p_secure_session"`
	P2PConnections    uint64              `json:"p2p_connections"`
	P2PHandshakeOK    uint64              `json:"p2p_secure_handshake_ok"`
	P2PHandshakeFail  uint64              `json:"p2p_secure_handshake_failed"`
	P2PRateLimited    uint64              `json:"p2p_messages_rate_limited"`
	RuntimeGoroutines uint64              `json:"runtime_goroutines"`
	RuntimeRSSBytes   uint64              `json:"runtime_rss_bytes"`
	ForkCount         uint64              `json:"fork_count"`
	ReorgCount        uint64              `json:"reorg_count"`
	TurbineLayer      int                 `json:"turbine_layer"`
	TurbineFanout     int                 `json:"turbine_fanout"`
	TurbineParentPeer string              `json:"turbine_parent_peer,omitempty"`
	TurbineChildCount int                 `json:"turbine_child_count"`
	FastPathPeerCount int                 `json:"transaction_fast_path_peer_count"`
	FastPathLeaderNum int                 `json:"transaction_fast_path_leader_count"`
	VoteRatePerMinute float64             `json:"vote_rate_per_minute"`
	UptimeSeconds     int64               `json:"uptime_seconds"`
	Counters          nodeMetricsSnapshot `json:"counters"`
}

func (metrics *nodeMetrics) snapshot() nodeMetricsSnapshot {
	return nodeMetricsSnapshot{
		BlocksProduced:    metrics.blocksProduced.Load(),
		ProposalsAccepted: metrics.proposalsAccepted.Load(),
		VotesSent:         metrics.votesSent.Load(),
		QCFormed:          metrics.qcFormed.Load(),
		QCReceived:        metrics.qcReceived.Load(),
		ForkDecisions:     metrics.forkDecisions.Load(),
		Reorgs:            metrics.reorgs.Load(),
		OrphanStored:      metrics.orphanStored.Load(),
		SyncRequests:      metrics.syncRequests.Load(),
		SyncFailures:      metrics.syncFailures.Load(),
		TransactionsIn:    metrics.transactionsIn.Load(),
		TransactionsDrop:  metrics.transactionsDrop.Load(),
		EvidenceReceived:  metrics.evidenceReceived.Load(),
	}
}

func (node *posNode) GetMetrics(ctx context.Context) (any, error) {
	_ = ctx
	return node.metricsSnapshot(), nil
}

func (node *posNode) metricsSnapshot() nodeOperationalMetrics {
	node.refreshKnownPeersFromHost()
	head := node.ledger.Head()
	counters := node.metrics.snapshot()
	p2pSecure := false
	p2pConnections := uint64(0)
	p2pHandshakeOK := uint64(0)
	p2pHandshakeFail := uint64(0)
	p2pRateLimited := uint64(0)
	runtimeGoroutines := uint64(0)
	runtimeRSSBytes := uint64(0)
	if node.host != nil {
		hostMetrics := node.host.Metrics()
		p2pSecure = node.host.SecureSessionEnabled()
		p2pConnections = hostMetrics.ConnectionCount
		p2pHandshakeOK = hostMetrics.SecureHandshakeOK
		p2pHandshakeFail = hostMetrics.SecureHandshakeFailed
		p2pRateLimited = hostMetrics.MessagesRateLimited
		runtimeGoroutines = hostMetrics.RuntimeGoroutines
		runtimeRSSBytes = hostMetrics.RuntimeRSSBytes
	}
	uptimeSeconds := int64(0)
	if !node.startedAt.IsZero() {
		uptimeSeconds = int64(time.Since(node.startedAt).Seconds())
	}
	voteRatePerMinute := 0.0
	if uptimeSeconds > 0 {
		voteRatePerMinute = float64(counters.VotesSent) * 60 / float64(uptimeSeconds)
	}

	node.mutex.Lock()
	defer node.mutex.Unlock()
	currentSlot := head.Slot + 1
	if node.config.SlotMillis > 0 {
		currentSlot = node.currentRoutingSlotLocked()
	}
	currentLeader := ""
	if node.epochSnapshot.StartSlot <= currentSlot && currentSlot <= node.epochSnapshot.EndSlot {
		if leader, err := node.leaderSchedule.LeaderForSlot(currentSlot); err == nil {
			currentLeader = string(leader)
		}
	}
	turbine := node.turbinePositionForSlotLocked(currentSlot)
	transactionFastPath := node.transactionFastPathForSlotLocked(currentSlot, true)
	qcHeight := uint64(0)
	if !head.QCHash.IsZero() {
		qcHeight = head.Height
	}
	return nodeOperationalMetrics{
		NodeName:          node.config.NodeName,
		PeerID:            node.peerKeyPair.peerID,
		CurrentSlot:       currentSlot,
		CurrentLeader:     currentLeader,
		HeadHeight:        head.Height,
		HeadSlot:          head.Slot,
		BlockHash:         head.BlockHash.String(),
		StateRoot:         head.StateRoot.String(),
		QCHash:            head.QCHash.String(),
		QCHeight:          qcHeight,
		FinalizedHeight:   head.FinalizedHeight,
		FinalityDepth:     node.ledger.FinalityDepth(),
		MempoolSize:       len(node.mempool),
		ValidatorCount:    len(node.epochSnapshot.Validators),
		KnownPeerCount:    len(node.knownPeerIDs),
		P2PSecure:         p2pSecure,
		P2PConnections:    p2pConnections,
		P2PHandshakeOK:    p2pHandshakeOK,
		P2PHandshakeFail:  p2pHandshakeFail,
		P2PRateLimited:    p2pRateLimited,
		RuntimeGoroutines: runtimeGoroutines,
		RuntimeRSSBytes:   runtimeRSSBytes,
		ForkCount:         counters.ForkDecisions,
		ReorgCount:        counters.Reorgs,
		TurbineLayer:      turbine.Layer,
		TurbineFanout:     turbine.Fanout,
		TurbineParentPeer: turbine.ParentPeerID,
		TurbineChildCount: len(turbine.ChildPeerIDs),
		FastPathPeerCount: len(transactionFastPath.PreferredPeerIDs),
		FastPathLeaderNum: len(transactionFastPath.LeaderSlots),
		VoteRatePerMinute: voteRatePerMinute,
		UptimeSeconds:     uptimeSeconds,
		Counters:          counters,
	}
}
