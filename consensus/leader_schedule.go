package consensus

import (
	"fmt"

	"solana_golang/structure"
	"solana_golang/utils"
)

// LeaderSchedule 保存 slot 到 leader 的确定性映射 + 交易转发和出块共用同一结果。
type LeaderSchedule struct {
	EpochID       uint64
	SlotToLeader  map[uint64]ValidatorID
	LeaderToSlots map[ValidatorID][]uint64
	SeedHash      structure.Hash
}

// NewLeaderSchedule 生成 leader 表 + 使用 seed 和 stake 权重做确定性选择。
func NewLeaderSchedule(snapshot EpochSnapshot) (LeaderSchedule, error) {
	if snapshot.TotalActiveStake == 0 || len(snapshot.Validators) == 0 {
		return LeaderSchedule{}, fmt.Errorf("%w: empty snapshot", ErrInvalidVote)
	}

	schedule := LeaderSchedule{
		EpochID:       snapshot.EpochID,
		SlotToLeader:  make(map[uint64]ValidatorID),
		LeaderToSlots: make(map[ValidatorID][]uint64),
		SeedHash:      snapshot.RandomSeed,
	}
	for slot := snapshot.StartSlot; slot <= snapshot.EndSlot; slot++ {
		leaderID := selectLeaderForSlot(snapshot, slot)
		schedule.SlotToLeader[slot] = leaderID
		schedule.LeaderToSlots[leaderID] = append(schedule.LeaderToSlots[leaderID], slot)
		if slot == ^uint64(0) {
			break
		}
	}
	return schedule, nil
}

// LeaderForSlot 查询 slot leader + 非 epoch 范围 slot 直接拒绝。
func (schedule LeaderSchedule) LeaderForSlot(slot uint64) (ValidatorID, error) {
	leaderID, exists := schedule.SlotToLeader[slot]
	if !exists {
		return "", fmt.Errorf("%w: slot %d not in schedule", ErrUnknownValidator, slot)
	}
	return leaderID, nil
}

func selectLeaderForSlot(snapshot EpochSnapshot, slot uint64) ValidatorID {
	randomInput := append(snapshot.RandomSeed[:], byte(slot), byte(slot>>8), byte(slot>>16), byte(slot>>24), byte(slot>>32), byte(slot>>40), byte(slot>>48), byte(slot>>56))
	randomBytes := utils.SHA256(randomInput)
	target := uint64FromHash(randomBytes) % snapshot.TotalActiveStake

	var accumulatedStake uint64
	for _, validator := range snapshot.Validators {
		accumulatedStake += validator.StakeLamports
		if target < accumulatedStake {
			return validator.ValidatorID
		}
	}
	return snapshot.Validators[len(snapshot.Validators)-1].ValidatorID
}

func uint64FromHash(value []byte) uint64 {
	var result uint64
	for index := 0; index < 8 && index < len(value); index++ {
		result |= uint64(value[index]) << (uint(index) * 8)
	}
	return result
}
