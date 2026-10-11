package posnode

import (
	"fmt"
	"sort"

	"github.com/tentechtop/ordexus-core/consensus"
)

// consensusProofSnapshotsLocked 收集区块内 QC/处罚证据发生时的历史验证者快照。
// 调用方持有 node.mutex，保证快照与当前验块视图一致。
func (node *posNode) consensusProofSnapshotsLocked(
	rewardQCs []consensus.QuorumCertificate,
	evidences []consensus.SlashingEvidence,
	timeoutQC *consensus.QuorumCertificate,
	current consensus.EpochSnapshot,
) ([]consensus.EpochSnapshot, error) {
	slots := make(map[uint64]struct{}, len(rewardQCs)+len(evidences)+1)
	for _, certificate := range rewardQCs {
		slots[certificate.Slot] = struct{}{}
	}
	for index, evidence := range evidences {
		slot, err := evidence.Slot()
		if err != nil {
			return nil, fmt.Errorf("posnode: slashing evidence %d slot: %w", index, err)
		}
		slots[slot] = struct{}{}
	}
	if timeoutQC != nil {
		slots[timeoutQC.Slot] = struct{}{}
	}

	activations, err := node.loadActivationProofs()
	if err != nil {
		return nil, err
	}
	activationSlots := make(map[uint64]uint64, len(activations))
	for _, certificate := range activations {
		_, slot, err := node.activationAnchor(certificate.TargetHeight, certificate.TargetHash)
		if err != nil {
			return nil, err
		}
		activationSlots[certificate.Sequence] = slot
	}
	byView := make(map[string]consensus.EpochSnapshot)
	byRoot := make(map[string]consensus.EpochSnapshot)
	bySlot := make(map[uint64]consensus.EpochSnapshot)
	for slot := range slots {
		epochID, _ := node.epochForSlot(slot)
		var sequence uint64
		for _, certificate := range activations {
			if certificate.TargetEpochID == epochID && slot > activationSlots[certificate.Sequence] {
				sequence = certificate.Sequence
			}
		}
		view := fmt.Sprintf("%d/%d", epochID, sequence)
		snapshot, exists := byView[view]
		if !exists {
			if len(activations) == 0 {
				if slot >= current.StartSlot && slot <= current.EndSlot {
					continue
				}
				contextValue, err := node.epochContextForSlotLocked(slot)
				if err != nil {
					return nil, fmt.Errorf("posnode: proof snapshot for slot %d: %w", slot, err)
				}
				snapshot = contextValue.Snapshot
			} else {
				var err error
				snapshot, err = node.authenticatedEpochSnapshotForSlot(slot)
				if err != nil {
					return nil, err
				}
			}
			byView[view] = snapshot
		}
		if snapshot.SnapshotStateRoot != current.SnapshotStateRoot {
			byRoot[snapshot.SnapshotStateRoot.String()] = snapshot
		}
		bySlot[slot] = snapshot
	}
	if len(activations) > 0 {
		// Overlapping canonical epoch ranges must never let a newer committee
		// authenticate a QC from before its certified activation checkpoint.
		for _, qc := range rewardQCs {
			if err := consensus.VerifyQuorumCertificateForSnapshot(qc, bySlot[qc.Slot], consensus.DefaultRewardConfig().RewardQCQuorum); err != nil {
				return nil, fmt.Errorf("posnode: reward qc committee at slot %d: %w", qc.Slot, err)
			}
		}
		if timeoutQC != nil {
			if err := consensus.VerifyQuorumCertificateForSnapshot(*timeoutQC, bySlot[timeoutQC.Slot], consensus.Quorum{Numerator: 2, Denominator: 3, Strict: true}); err != nil {
				return nil, err
			}
		}
		for _, evidence := range evidences {
			slot, err := evidence.Slot()
			if err != nil {
				return nil, err
			}
			if _, _, err := evidence.Validate(bySlot[slot]); err != nil {
				return nil, err
			}
		}
	}
	snapshots := make([]consensus.EpochSnapshot, 0, len(byRoot))
	for _, snapshot := range byRoot {
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].EpochID != snapshots[j].EpochID {
			return snapshots[i].EpochID < snapshots[j].EpochID
		}
		return snapshots[i].SnapshotStateRoot.String() < snapshots[j].SnapshotStateRoot.String()
	})
	return snapshots, nil
}
