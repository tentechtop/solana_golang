package consensus

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
)

const (
	DefaultMultiValidatorThreshold             = 4
	DefaultMultiValidatorActivationDelayBlocks = uint64(100)
)

// NetworkMode 是链上共识运行模式；节点可达性只能暂停出块，不能直接改变该模式。
type NetworkMode string

const (
	NetworkModeBootstrapElder NetworkMode = "bootstrap_elder"
	NetworkModeMultiValidator NetworkMode = "multi_validator"
	NetworkModeRecoveryElder  NetworkMode = "recovery_elder"
)

// NetworkPolicy 是创世身份的一部分，所有节点必须使用相同阈值。
type NetworkPolicy struct {
	MultiValidatorThreshold int
}

func (policy NetworkPolicy) Normalize() (NetworkPolicy, error) {
	if policy.MultiValidatorThreshold == 0 {
		policy.MultiValidatorThreshold = DefaultMultiValidatorThreshold
	}
	if policy.MultiValidatorThreshold < 2 || policy.MultiValidatorThreshold > MaxValidatorsPerSet {
		return NetworkPolicy{}, fmt.Errorf("consensus: multi-validator threshold must be between 2 and %d", MaxValidatorsPerSet)
	}
	return policy, nil
}

// SelectElder 选择权益最高的验证者；同权重时使用锚点和验证者 ID 哈希稳定破同票。
func SelectElder(set ValidatorSet, anchor structure.Hash, excluded map[ValidatorID]struct{}) (ValidatorState, error) {
	validators := set.Validators()
	candidates := make([]ValidatorState, 0, len(validators))
	for _, validator := range validators {
		if _, skip := excluded[validator.ValidatorID]; skip {
			continue
		}
		candidates = append(candidates, validator)
	}
	if len(candidates) == 0 {
		return ValidatorState{}, fmt.Errorf("consensus: no eligible elder")
	}
	sort.Slice(candidates, func(leftIndex int, rightIndex int) bool {
		left := candidates[leftIndex]
		right := candidates[rightIndex]
		if left.StakeLamports != right.StakeLamports {
			return left.StakeLamports > right.StakeLamports
		}
		leftTie := elderTieBreak(anchor, left.ValidatorID)
		rightTie := elderTieBreak(anchor, right.ValidatorID)
		if leftTie != rightTie {
			return strings.Compare(string(leftTie[:]), string(rightTie[:])) < 0
		}
		return left.ValidatorID < right.ValidatorID
	})
	return candidates[0], nil
}

// ValidatorSetRoot 提交排序后的验证者身份、公钥和权益，恢复证书不能替换签名委员会。
func ValidatorSetRoot(set ValidatorSet) (structure.Hash, error) {
	validators := set.Validators()
	if len(validators) == 0 {
		return structure.Hash{}, fmt.Errorf("consensus: empty validator set")
	}
	encoded := make([]byte, 0, len(validators)*128)
	encoded = append(encoded, []byte("pos-validator-set-v1")...)
	encoded = binary.LittleEndian.AppendUint64(encoded, uint64(len(validators)))
	for _, validator := range validators {
		validatorID := string(validator.ValidatorID)
		encoded = binary.LittleEndian.AppendUint64(encoded, uint64(len(validatorID)))
		encoded = append(encoded, validatorID...)
		encoded = append(encoded, validator.AccountAddress[:]...)
		encoded = append(encoded, validator.ConsensusPublicKey[:]...)
		encoded = binary.LittleEndian.AppendUint64(encoded, uint64(len(validator.BLSPublicKey)))
		encoded = append(encoded, validator.BLSPublicKey...)
		encoded = binary.LittleEndian.AppendUint64(encoded, validator.StakeLamports)
	}
	return structure.NewHash(utils.SHA256(encoded))
}

// SingleValidatorSet 将共识投票权临时收敛到已选长老，不改变链上质押账户。
func SingleValidatorSet(validator ValidatorState) (ValidatorSet, error) {
	return NewValidatorSet([]ValidatorState{validator})
}

func elderTieBreak(anchor structure.Hash, validatorID ValidatorID) structure.Hash {
	encoded := make([]byte, 0, len(anchor)+len(validatorID))
	encoded = append(encoded, anchor[:]...)
	encoded = append(encoded, validatorID...)
	hash, _ := structure.NewHash(utils.SHA256(encoded))
	return hash
}
