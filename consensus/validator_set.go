package consensus

import (
	"fmt"
	"sort"
	"strings"

	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	MaxValidatorsPerSet         = 4096
	MaxValidatorBLSPublicKeyLen = 128
)

type ValidatorStatus uint8

const (
	ValidatorStatusInactive ValidatorStatus = iota
	ValidatorStatusActive
	ValidatorStatusJailed
	ValidatorStatusExiting
)

// ValidatorID 标识验证者 + 使用共识公钥哈希避免依赖可变网络地址。
type ValidatorID string

// ValidatorState 描述验证者状态 + 将质押账户、共识密钥和 P2P 身份分离。
type ValidatorState struct {
	ValidatorID         ValidatorID
	AccountAddress      structure.PublicKey
	ConsensusPublicKey  structure.PublicKey
	BLSPublicKey        []byte
	P2PPeerID           string
	StakeLamports       uint64
	Status              ValidatorStatus
	CommissionBps       uint16
	LastVotedSlot       uint64
	MissedProposalCount uint64
	MissedVoteCount     uint64
	JailUntilEpoch      uint64
}

// ValidatorSet 保存验证者集合 + 排序后快照保证所有节点选择 leader 一致。
type ValidatorSet struct {
	validators map[ValidatorID]ValidatorState
}

// NewValidatorID 从共识公钥生成稳定 ID + 避免配置中手写 ID 出错。
func NewValidatorID(consensusPublicKey structure.PublicKey) ValidatorID {
	hash := utils.SHA256(consensusPublicKey[:])
	return ValidatorID(utils.BytesToHex(hash[:16]))
}

// NewValidatorSet 创建验证者集合 + 拒绝重复、空 stake 和非法佣金。
func NewValidatorSet(validators []ValidatorState) (ValidatorSet, error) {
	if len(validators) == 0 || len(validators) > MaxValidatorsPerSet {
		return ValidatorSet{}, fmt.Errorf("%w: invalid validator count", ErrInvalidVote)
	}

	set := ValidatorSet{validators: make(map[ValidatorID]ValidatorState, len(validators))}
	for index, validator := range validators {
		normalized, err := normalizeValidatorState(validator)
		if err != nil {
			return ValidatorSet{}, fmt.Errorf("consensus: validator %d: %w", index, err)
		}
		if _, exists := set.validators[normalized.ValidatorID]; exists {
			return ValidatorSet{}, fmt.Errorf("%w: duplicate validator id", ErrInvalidVote)
		}
		set.validators[normalized.ValidatorID] = normalized
	}
	return set, nil
}

// Validators 返回排序后的验证者 + 为快照和测试提供确定性顺序。
func (set ValidatorSet) Validators() []ValidatorState {
	validators := make([]ValidatorState, 0, len(set.validators))
	for _, validator := range set.validators {
		validators = append(validators, validator)
	}
	sort.Slice(validators, func(leftIndex int, rightIndex int) bool {
		return validators[leftIndex].ValidatorID < validators[rightIndex].ValidatorID
	})
	return validators
}

// ActiveValidators 返回活跃验证者 + jail 和退出节点不能参与当前 epoch。
func (set ValidatorSet) ActiveValidators(epochID uint64) []ValidatorState {
	allValidators := set.Validators()
	activeValidators := make([]ValidatorState, 0, len(allValidators))
	for _, validator := range allValidators {
		if validator.Status != ValidatorStatusActive {
			continue
		}
		if validator.JailUntilEpoch > epochID {
			continue
		}
		activeValidators = append(activeValidators, validator)
	}
	return activeValidators
}

// ActiveStakeMap 返回可信 stake 表 + VoteCollector 只能使用快照权重。
func (set ValidatorSet) ActiveStakeMap(epochID uint64) map[string]uint64 {
	activeValidators := set.ActiveValidators(epochID)
	stakeByValidator := make(map[string]uint64, len(activeValidators))
	for _, validator := range activeValidators {
		stakeByValidator[string(validator.ValidatorID)] = validator.StakeLamports
	}
	return stakeByValidator
}

func normalizeValidatorState(validator ValidatorState) (ValidatorState, error) {
	if validator.ConsensusPublicKey.IsZero() {
		return ValidatorState{}, fmt.Errorf("%w: consensus public key is empty", ErrInvalidVote)
	}
	if len(validator.BLSPublicKey) > MaxValidatorBLSPublicKeyLen {
		return ValidatorState{}, fmt.Errorf("%w: bls public key too long", ErrInvalidVote)
	}
	if err := ValidateBLSPublicKey(validator.BLSPublicKey); err != nil {
		return ValidatorState{}, fmt.Errorf("%w: invalid bls public key", ErrInvalidVote)
	}
	if validator.ValidatorID == "" {
		validator.ValidatorID = NewValidatorID(validator.ConsensusPublicKey)
	}
	if strings.TrimSpace(string(validator.ValidatorID)) == "" || len(validator.ValidatorID) > maxConsensusTextLength {
		return ValidatorState{}, fmt.Errorf("%w: invalid validator id", ErrInvalidVote)
	}
	if validator.StakeLamports == 0 {
		return ValidatorState{}, fmt.Errorf("%w: zero stake", ErrInvalidVote)
	}
	if validator.CommissionBps > 10000 {
		return ValidatorState{}, fmt.Errorf("%w: commission exceeds 10000 bps", ErrInvalidVote)
	}
	validator.BLSPublicKey = append([]byte(nil), validator.BLSPublicKey...)
	if validator.Status == ValidatorStatusInactive {
		validator.Status = ValidatorStatusActive
	}
	return validator, nil
}
