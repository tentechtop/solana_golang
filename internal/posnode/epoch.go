package posnode

import (
	"fmt"
	"log/slog"

	"solana_golang/consensus"
	"solana_golang/structure"
)

type epochContext struct {
	EpochID       uint64
	StartSlot     uint64
	Snapshot      consensus.EpochSnapshot
	Schedule      consensus.LeaderSchedule
	VoteCollector *consensus.VoteCollector
}

func (node *posNode) ensureEpochForSlotLocked(slot uint64) error {
	contextValue, err := node.epochContextForSlotLocked(slot)
	if err != nil {
		return err
	}
	if contextValue.EpochID < node.epochSnapshot.EpochID {
		return nil
	}
	if node.epochSnapshot.EpochID == contextValue.EpochID &&
		node.epochSnapshot.StartSlot == contextValue.StartSlot &&
		slot <= node.epochSnapshot.EndSlot {
		return nil
	}
	node.epochSnapshot = contextValue.Snapshot
	node.leaderSchedule = contextValue.Schedule
	node.voteCollector = contextValue.VoteCollector
	node.logger.Info("posnode epoch ready",
		slog.Uint64("epoch", contextValue.EpochID),
		slog.Uint64("start_slot", contextValue.Snapshot.StartSlot),
		slog.Uint64("end_slot", contextValue.Snapshot.EndSlot),
		slog.Int("validators", len(contextValue.Snapshot.Validators)),
		slog.Uint64("total_stake", contextValue.Snapshot.TotalActiveStake),
	)
	return nil
}

// epochContextForSlotLocked 构造指定 slot 的共识上下文 + 验证旧消息时不能回滚节点全局 epoch。
func (node *posNode) epochContextForSlotLocked(slot uint64) (epochContext, error) {
	epochID, startSlot := node.epochForSlot(slot)
	if node.epochSnapshot.EpochID == epochID &&
		node.epochSnapshot.StartSlot == startSlot &&
		slot <= node.epochSnapshot.EndSlot &&
		len(node.epochSnapshot.Validators) > 0 {
		schedule := node.leaderSchedule
		if schedule.EpochID != epochID || schedule.SlotToLeader == nil {
			nextSchedule, err := consensus.NewLeaderSchedule(node.epochSnapshot)
			if err != nil {
				return epochContext{}, err
			}
			schedule = nextSchedule
			node.leaderSchedule = schedule
		}
		collector := node.voteCollector
		if collector == nil {
			nextCollector, err := node.voteCollectorForEpochLocked(epochID, node.epochSnapshot)
			if err != nil {
				return epochContext{}, err
			}
			collector = nextCollector
			node.voteCollector = collector
		}
		if node.epochSnapshots == nil {
			node.epochSnapshots = make(map[uint64]consensus.EpochSnapshot)
		}
		if node.leaderSchedules == nil {
			node.leaderSchedules = make(map[uint64]consensus.LeaderSchedule)
		}
		node.epochSnapshots[epochID] = node.epochSnapshot
		node.leaderSchedules[epochID] = schedule
		return epochContext{
			EpochID:       epochID,
			StartSlot:     startSlot,
			Snapshot:      node.epochSnapshot,
			Schedule:      schedule,
			VoteCollector: collector,
		}, nil
	}
	if node.epochSnapshots == nil {
		node.epochSnapshots = make(map[uint64]consensus.EpochSnapshot)
	}
	if node.leaderSchedules == nil {
		node.leaderSchedules = make(map[uint64]consensus.LeaderSchedule)
	}
	snapshot, exists := node.epochSnapshots[epochID]
	if !exists || snapshot.StartSlot != startSlot || slot > snapshot.EndSlot {
		if node.ledger == nil {
			return epochContext{}, fmt.Errorf("posnode: ledger unavailable for epoch context")
		}
		validatorSet, err := node.ledger.ValidatorSetFromFinalizedStateAtEpoch(epochID)
		if err != nil {
			return epochContext{}, err
		}
		nextSnapshot, err := consensus.NewEpochSnapshot(epochID, startSlot, node.config.EpochSlots, node.epochSeed(epochID), validatorSet)
		if err != nil {
			return epochContext{}, err
		}
		snapshot = nextSnapshot
		node.epochSnapshots[epochID] = snapshot
		delete(node.leaderSchedules, epochID)
	}
	schedule, exists := node.leaderSchedules[epochID]
	if !exists {
		nextSchedule, err := consensus.NewLeaderSchedule(snapshot)
		if err != nil {
			return epochContext{}, err
		}
		schedule = nextSchedule
		node.leaderSchedules[epochID] = schedule
	}
	collector, err := node.voteCollectorForEpochLocked(epochID, snapshot)
	if err != nil {
		return epochContext{}, err
	}
	return epochContext{
		EpochID:       epochID,
		StartSlot:     startSlot,
		Snapshot:      snapshot,
		Schedule:      schedule,
		VoteCollector: collector,
	}, nil
}

// pruneFutureEpochContextCachesLocked 清理未来 epoch 缓存 + 区块提交后未来验证者集合可能因质押或罚没发生变化。
func (node *posNode) pruneFutureEpochContextCachesLocked(committedEpochID uint64) {
	for epochID := range node.epochSnapshots {
		if epochID > committedEpochID {
			delete(node.epochSnapshots, epochID)
		}
	}
	for epochID := range node.leaderSchedules {
		if epochID > committedEpochID {
			delete(node.leaderSchedules, epochID)
		}
	}
	for epochID := range node.voteCollectors {
		if epochID > committedEpochID {
			delete(node.voteCollectors, epochID)
		}
	}
}

func (node *posNode) epochForSlot(slot uint64) (uint64, uint64) {
	if slot == 0 {
		return 0, 1
	}
	epochID := (slot - 1) / node.config.EpochSlots
	return epochID, epochID*node.config.EpochSlots + 1
}

func (node *posNode) epochSeed(epochID uint64) structure.Hash {
	return mustHash(fmt.Sprintf("%s-epoch-%d", node.config.ChainID, epochID))
}
