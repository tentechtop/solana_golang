package consensus

import (
	"fmt"

	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
)

// EpochConfig 描述 epoch 参数 + 固定 slot 数和 quorum 使本地测试可复现。
type EpochConfig struct {
	EpochSlots uint64
	Quorum     Quorum
}

// EpochSnapshot 固化 epoch 验证者权重 + 当前 epoch 内 stake 变化不立即生效。
type EpochSnapshot struct {
	EpochID           uint64
	StartSlot         uint64
	EndSlot           uint64
	RandomSeed        structure.Hash
	TotalActiveStake  uint64
	Validators        []ValidatorState
	SnapshotStateRoot structure.Hash
}

// NewEpochSnapshot 创建 epoch 快照 + 所有共识输入必须来自确定性验证者集合。
func NewEpochSnapshot(epochID uint64, startSlot uint64, epochSlots uint64, seed structure.Hash, set ValidatorSet) (EpochSnapshot, error) {
	if epochSlots == 0 {
		return EpochSnapshot{}, fmt.Errorf("%w: epoch slots is zero", ErrInvalidQuorum)
	}
	if startSlot > ^uint64(0)-(epochSlots-1) {
		return EpochSnapshot{}, fmt.Errorf("%w: epoch slot range overflows", ErrInvalidQuorum)
	}

	activeValidators := set.ActiveValidators(epochID)
	if len(activeValidators) == 0 {
		return EpochSnapshot{}, fmt.Errorf("%w: no active validators", ErrInvalidVote)
	}

	var totalStake uint64
	for _, validator := range activeValidators {
		if ^uint64(0)-totalStake < validator.StakeLamports {
			return EpochSnapshot{}, fmt.Errorf("%w: active stake overflow", ErrInvalidVote)
		}
		totalStake += validator.StakeLamports
	}

	stateRoot, err := hashEpochSnapshot(epochID, startSlot, epochSlots, seed, activeValidators)
	if err != nil {
		return EpochSnapshot{}, err
	}
	return EpochSnapshot{
		EpochID:           epochID,
		StartSlot:         startSlot,
		EndSlot:           startSlot + epochSlots - 1,
		RandomSeed:        seed,
		TotalActiveStake:  totalStake,
		Validators:        activeValidators,
		SnapshotStateRoot: stateRoot,
	}, nil
}

// Validate 重算 epoch 快照承诺，防止验证者地址、公钥或权重被替换后继续用于奖励和处罚。
func (snapshot EpochSnapshot) Validate() error {
	if snapshot.EndSlot < snapshot.StartSlot || snapshot.EndSlot-snapshot.StartSlot == ^uint64(0) {
		return fmt.Errorf("consensus: invalid epoch snapshot slot range")
	}
	validatorSet, err := NewValidatorSet(snapshot.Validators)
	if err != nil {
		return err
	}
	recomputed, err := NewEpochSnapshot(
		snapshot.EpochID,
		snapshot.StartSlot,
		snapshot.EndSlot-snapshot.StartSlot+1,
		snapshot.RandomSeed,
		validatorSet,
	)
	if err != nil {
		return err
	}
	if recomputed.TotalActiveStake != snapshot.TotalActiveStake ||
		recomputed.SnapshotStateRoot != snapshot.SnapshotStateRoot {
		return fmt.Errorf("consensus: epoch snapshot commitment mismatch")
	}
	return nil
}

// StakeMap 返回快照 stake 表 + 防止投票消息自报权重。
func (snapshot EpochSnapshot) StakeMap() map[string]uint64 {
	stakeByValidator := make(map[string]uint64, len(snapshot.Validators))
	for _, validator := range snapshot.Validators {
		stakeByValidator[string(validator.ValidatorID)] = validator.StakeLamports
	}
	return stakeByValidator
}

// ValidatorOrder 返回快照验证者顺序 + voter bitmap 使用同一顺序才能跨节点验证一致。
func (snapshot EpochSnapshot) ValidatorOrder() []string {
	order := make([]string, len(snapshot.Validators))
	for index, validator := range snapshot.Validators {
		order[index] = string(validator.ValidatorID)
	}
	return order
}

// BLSPublicKeys 返回 BLS 公钥表 + QC 聚合验证通过 voter bitmap 找到公钥集合。
func (snapshot EpochSnapshot) BLSPublicKeys() map[string][]byte {
	publicKeysByValidator := make(map[string][]byte, len(snapshot.Validators))
	for _, validator := range snapshot.Validators {
		if len(validator.BLSPublicKey) == 0 {
			continue
		}
		publicKey := make([]byte, len(validator.BLSPublicKey))
		copy(publicKey, validator.BLSPublicKey)
		publicKeysByValidator[string(validator.ValidatorID)] = publicKey
	}
	return publicKeysByValidator
}

// ValidatorByID 查询验证者 + 验块时取公钥验证 leader 签名。
func (snapshot EpochSnapshot) ValidatorByID(validatorID ValidatorID) (ValidatorState, bool) {
	for _, validator := range snapshot.Validators {
		if validator.ValidatorID == validatorID {
			return validator, true
		}
	}
	return ValidatorState{}, false
}

func hashEpochSnapshot(epochID uint64, startSlot uint64, epochSlots uint64, seed structure.Hash, validators []ValidatorState) (structure.Hash, error) {
	encoded := make([]byte, 0, len(validators)*96)
	encoded = appendUint64ForHash(encoded, epochID)
	encoded = appendUint64ForHash(encoded, startSlot)
	encoded = appendUint64ForHash(encoded, epochSlots)
	encoded = append(encoded, seed[:]...)
	for _, validator := range validators {
		encoded = append(encoded, []byte(validator.ValidatorID)...)
		encoded = append(encoded, validator.AccountAddress[:]...)
		encoded = append(encoded, validator.ConsensusPublicKey[:]...)
		encoded = appendUint64ForHash(encoded, uint64(len(validator.BLSPublicKey)))
		encoded = append(encoded, validator.BLSPublicKey...)
		encoded = appendUint64ForHash(encoded, uint64(len(validator.P2PPeerID)))
		encoded = append(encoded, []byte(validator.P2PPeerID)...)
		encoded = appendUint64ForHash(encoded, validator.StakeLamports)
		encoded = append(encoded, byte(validator.Status))
		encoded = appendUint64ForHash(encoded, uint64(validator.CommissionBps))
		encoded = appendUint64ForHash(encoded, validator.JailUntilEpoch)
	}
	return structure.NewHash(utils.SHA256(encoded))
}

func appendUint64ForHash(encoded []byte, value uint64) []byte {
	for shift := 0; shift < 8; shift++ {
		encoded = append(encoded, byte(value>>(uint(shift)*8)))
	}
	return encoded
}
