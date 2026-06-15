package main

import "sync/atomic"

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
