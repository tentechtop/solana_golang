package posnode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/programs/stake"
	"github.com/tentechtop/ordexus-core/structure"
)

const consensusModeStateVersion uint16 = 1

var consensusModeStateKey = []byte("posnode:consensus:network-mode:v1")

type consensusSafetyCheckpoint struct {
	FinalizedHeight  uint64 `json:"finalized_height"`
	FinalizedHash    string `json:"finalized_hash"`
	EpochID          uint64 `json:"epoch_id"`
	ValidatorSetRoot string `json:"validator_set_root"`
}

type persistedConsensusModeState struct {
	Version                       uint16                    `json:"version"`
	Mode                          consensus.NetworkMode     `json:"mode"`
	Sequence                      uint64                    `json:"sequence"`
	MultiValidatorActivated       bool                      `json:"multi_validator_activated"`
	MultiValidatorActivationEpoch uint64                    `json:"multi_validator_activation_epoch,omitempty"`
	FounderValidatorID            string                    `json:"founder_validator_id,omitempty"`
	ElderValidatorID              string                    `json:"elder_validator_id,omitempty"`
	ActivationSlot                uint64                    `json:"activation_slot,omitempty"`
	Safety                        consensusSafetyCheckpoint `json:"safety"`
}

func (node *posNode) loadConsensusModeState() error {
	if node.db == nil {
		return nil
	}
	data, err := node.db.Get(database.TableCheckpoint, consensusModeStateKey)
	if err != nil {
		return fmt.Errorf("posnode: load consensus mode: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var state persistedConsensusModeState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("posnode: decode consensus mode: %w", err)
	}
	if state.Version != consensusModeStateVersion {
		return fmt.Errorf("posnode: unsupported consensus mode state version")
	}
	node.consensusMode = state.Mode
	node.recoverySequence = state.Sequence
	node.multiValidatorActivated = state.MultiValidatorActivated
	node.multiValidatorActivationEpoch = state.MultiValidatorActivationEpoch
	node.founderElderValidatorID = consensus.ValidatorID(strings.TrimSpace(state.FounderValidatorID))
	node.recoveryElderValidatorID = consensus.ValidatorID(strings.TrimSpace(state.ElderValidatorID))
	node.recoveryActivationSlot = state.ActivationSlot
	node.safetyCheckpoint = state.Safety
	if node.ledger != nil {
		node.ledger.SetFinalitySuspended(state.Mode == consensus.NetworkModeRecoveryElder)
	}
	return nil
}

func (node *posNode) persistConsensusModeStateLocked() error {
	if node.db == nil {
		return nil
	}
	state := persistedConsensusModeState{
		Version:                       consensusModeStateVersion,
		Mode:                          node.consensusMode,
		Sequence:                      node.recoverySequence,
		MultiValidatorActivated:       node.multiValidatorActivated,
		MultiValidatorActivationEpoch: node.multiValidatorActivationEpoch,
		FounderValidatorID:            string(node.founderElderValidatorID),
		ElderValidatorID:              string(node.recoveryElderValidatorID),
		ActivationSlot:                node.recoveryActivationSlot,
		Safety:                        node.safetyCheckpoint,
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("posnode: encode consensus mode: %w", err)
	}
	if err := node.db.Put(database.TableCheckpoint, consensusModeStateKey, data); err != nil {
		return fmt.Errorf("posnode: persist consensus mode: %w", err)
	}
	return nil
}

// effectiveValidatorSetLocked 根据链上验证者数量选择启动、多验证者或临时长老模式。
// reachable peer 数量不参与该决策，网络分区只能通过权益证书进入恢复模式。
func (node *posNode) effectiveValidatorSetLocked(rawSet consensus.ValidatorSet, epochID uint64, anchor structure.Hash) (consensus.ValidatorSet, error) {
	validators := rawSet.Validators()
	threshold := node.config.ConsensusPolicy.MultiValidatorThreshold
	if threshold == 0 {
		threshold = consensus.DefaultMultiValidatorThreshold
	}

	if node.consensusMode == consensus.NetworkModeRecoveryElder && node.recoveryElderValidatorID != "" {
		elder, err := node.validatorForRecoveryLocked(rawSet, node.recoveryElderValidatorID)
		if err != nil {
			return consensus.ValidatorSet{}, err
		}
		node.activeRecoveryValidator = &elder
		node.ledger.SetFinalitySuspended(true)
		return consensus.SingleValidatorSet(elder)
	}

	if len(validators) >= threshold && !node.multiValidatorActivated && node.ledger.Head().Height > 0 {
		epochSlots := node.config.EpochSlots
		if epochSlots == 0 {
			epochSlots = 1
		}
		delayEpochs := (consensus.DefaultMultiValidatorActivationDelayBlocks + epochSlots - 1) / epochSlots
		thresholdEpoch, err := node.multiValidatorThresholdEpochLocked(epochID, threshold)
		if err != nil {
			return consensus.ValidatorSet{}, err
		}
		if thresholdEpoch > ^uint64(0)-delayEpochs {
			return consensus.ValidatorSet{}, fmt.Errorf("posnode: multi-validator activation epoch overflow")
		}
		node.multiValidatorActivationEpoch = thresholdEpoch + delayEpochs
		if epochID < node.multiValidatorActivationEpoch || !node.multiValidatorActivationReadyLocked() {
			elder, err := node.preferredElderLocked(rawSet, anchor)
			if err != nil {
				return consensus.ValidatorSet{}, err
			}
			effectiveSet, err := consensus.SingleValidatorSet(elder)
			if err != nil {
				return consensus.ValidatorSet{}, err
			}
			node.consensusMode = consensus.NetworkModeBootstrapElder
			node.activeRecoveryValidator = &elder
			node.recoveryElderValidatorID = elder.ValidatorID
			node.ledger.SetFinalitySuspended(false)
			if err := node.updateSafetyCheckpointLocked(effectiveSet, epochID); err != nil {
				return consensus.ValidatorSet{}, err
			}
			if err := node.persistConsensusModeStateLocked(); err != nil {
				return consensus.ValidatorSet{}, err
			}
			return effectiveSet, nil
		}
	}

	if len(validators) >= threshold {
		node.consensusMode = consensus.NetworkModeMultiValidator
		node.multiValidatorActivated = true
		node.activeRecoveryValidator = nil
		node.recoveryElderValidatorID = ""
		node.recoveryActivationSlot = 0
		node.ledger.SetFinalitySuspended(false)
		if err := node.updateSafetyCheckpointLocked(rawSet, epochID); err != nil {
			return consensus.ValidatorSet{}, err
		}
		if err := node.persistConsensusModeStateLocked(); err != nil {
			return consensus.ValidatorSet{}, err
		}
		return rawSet, nil
	}

	elder, err := node.preferredElderLocked(rawSet, anchor)
	if err != nil {
		return consensus.ValidatorSet{}, err
	}
	node.activeRecoveryValidator = &elder
	node.recoveryElderValidatorID = elder.ValidatorID
	if node.multiValidatorActivated {
		node.consensusMode = consensus.NetworkModeRecoveryElder
		node.ledger.SetFinalitySuspended(true)
	} else {
		node.multiValidatorActivationEpoch = 0
		node.consensusMode = consensus.NetworkModeBootstrapElder
		node.ledger.SetFinalitySuspended(false)
		effectiveSet, err := consensus.SingleValidatorSet(elder)
		if err != nil {
			return consensus.ValidatorSet{}, err
		}
		if err := node.updateSafetyCheckpointLocked(effectiveSet, epochID); err != nil {
			return consensus.ValidatorSet{}, err
		}
	}
	if err := node.persistConsensusModeStateLocked(); err != nil {
		return consensus.ValidatorSet{}, err
	}
	return consensus.SingleValidatorSet(elder)
}

// multiValidatorThresholdEpochLocked 从 finalized 质押状态计算第 threshold 个验证者的激活 epoch，避免各节点按本地观察时间得出不同交接点。
// reconcileConsensusModeForInstalledSnapshotLocked 让已认证 epoch 快照和本地最终性模式保持一致。
// 快照证明同步不能只替换投票集合，否则落后节点可能沿用多验证者模式并错误最终化单长老临时块。
func (node *posNode) reconcileConsensusModeForInstalledSnapshotLocked(snapshot consensus.EpochSnapshot) error {
	threshold := node.config.ConsensusPolicy.MultiValidatorThreshold
	if threshold == 0 {
		threshold = consensus.DefaultMultiValidatorThreshold
	}
	validatorSet, err := consensus.NewValidatorSet(snapshot.Validators)
	if err != nil {
		return err
	}
	validators := validatorSet.Validators()
	if len(validators) >= threshold {
		node.consensusMode = consensus.NetworkModeMultiValidator
		node.multiValidatorActivated = true
		node.activeRecoveryValidator = nil
		node.recoveryElderValidatorID = ""
		node.recoveryActivationSlot = 0
		node.ledger.SetFinalitySuspended(false)
		if err := node.updateSafetyCheckpointLocked(validatorSet, snapshot.EpochID); err != nil {
			return err
		}
		return node.persistConsensusModeStateLocked()
	}
	if len(validators) != 1 {
		return fmt.Errorf("posnode: authenticated below-threshold epoch snapshot must contain exactly one elder")
	}

	elder := validators[0]
	node.activeRecoveryValidator = &elder
	node.recoveryElderValidatorID = elder.ValidatorID
	if node.multiValidatorActivated ||
		node.consensusMode == consensus.NetworkModeMultiValidator ||
		node.consensusMode == consensus.NetworkModeRecoveryElder {
		node.multiValidatorActivated = true
		node.consensusMode = consensus.NetworkModeRecoveryElder
		node.ledger.SetFinalitySuspended(true)
		return node.persistConsensusModeStateLocked()
	}

	node.multiValidatorActivationEpoch = 0
	node.consensusMode = consensus.NetworkModeBootstrapElder
	node.ledger.SetFinalitySuspended(false)
	if err := node.updateSafetyCheckpointLocked(validatorSet, snapshot.EpochID); err != nil {
		return err
	}
	return node.persistConsensusModeStateLocked()
}

func (node *posNode) multiValidatorThresholdEpochLocked(epochID uint64, threshold int) (uint64, error) {
	if node.ledger == nil || threshold <= 0 {
		return 0, fmt.Errorf("posnode: invalid multi-validator threshold state")
	}
	head := node.ledger.Head()
	state, err := node.ledger.StateAtBlockHash(head.FinalizedHash)
	if err != nil {
		return 0, err
	}
	activationEpochs := make([]uint64, 0, threshold)
	for _, account := range state.Accounts {
		if account.Account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Account.Data) == 0 {
			continue
		}
		validatorState, err := stake.UnmarshalValidatorStateBinary(account.Account.Data)
		if err != nil || validatorState.Status == stake.ValidatorStatusJailed {
			continue
		}
		effectiveStake, err := stake.EffectiveStakeAtEpoch(validatorState, epochID)
		if err != nil || effectiveStake == 0 {
			continue
		}
		if validatorState.Status == stake.ValidatorStatusExiting && validatorState.DeactivationEpoch <= epochID {
			continue
		}
		activationEpochs = append(activationEpochs, validatorState.ActivationEpoch)
	}
	if len(activationEpochs) < threshold {
		return 0, fmt.Errorf("posnode: finalized validator count %d is below threshold %d", len(activationEpochs), threshold)
	}
	sort.Slice(activationEpochs, func(left int, right int) bool {
		return activationEpochs[left] < activationEpochs[right]
	})
	return activationEpochs[threshold-1], nil
}

// multiValidatorActivationReadyLocked 只按已认证链头的下一轮判断交接点，未来消息不能提前改变全局共识模式。
func (node *posNode) multiValidatorActivationReadyLocked() bool {
	if node.multiValidatorActivationEpoch == 0 || node.ledger == nil {
		return false
	}
	head := node.ledger.Head()
	if head.Slot == ^uint64(0) {
		return false
	}
	readyEpoch, _ := node.epochForSlot(head.Slot + 1)
	return readyEpoch >= node.multiValidatorActivationEpoch
}

// preferredElderLocked 优先保持创世验证者连续出块；创世验证者已退出链上集合时才按权重确定性选举长老。
func (node *posNode) preferredElderLocked(rawSet consensus.ValidatorSet, anchor structure.Hash) (consensus.ValidatorState, error) {
	if node.founderElderValidatorID == "" && len(node.config.Genesis.InitialValidators) > 0 {
		founder := node.config.Genesis.InitialValidators[0]
		publicKey, err := genesisPublicKeyFromAddressOrSeed(
			founder.ConsensusPublicKey,
			founder.ConsensusSeed,
			"founder consensus",
		)
		if err != nil {
			return consensus.ValidatorState{}, err
		}
		node.founderElderValidatorID = consensus.NewValidatorID(publicKey)
	}
	if node.founderElderValidatorID != "" {
		for _, validator := range rawSet.Validators() {
			if validator.ValidatorID == node.founderElderValidatorID {
				return validator, nil
			}
		}
	}
	elder, err := consensus.SelectElder(rawSet, anchor, nil)
	if err != nil {
		return consensus.ValidatorState{}, err
	}
	if node.founderElderValidatorID == "" {
		node.founderElderValidatorID = elder.ValidatorID
	}
	return elder, nil
}

func (node *posNode) updateSafetyCheckpointLocked(set consensus.ValidatorSet, epochID uint64) error {
	root, err := consensus.ValidatorSetRoot(set)
	if err != nil {
		return err
	}
	head := node.ledger.Head()
	node.safetyCheckpoint = consensusSafetyCheckpoint{
		FinalizedHeight:  head.FinalizedHeight,
		FinalizedHash:    head.FinalizedHash.String(),
		EpochID:          epochID,
		ValidatorSetRoot: root.String(),
	}
	return nil
}

func (node *posNode) safetyValidatorSet() (consensus.ValidatorSet, error) {
	checkpoint := node.safetyCheckpoint
	if strings.TrimSpace(checkpoint.FinalizedHash) == "" || strings.TrimSpace(checkpoint.ValidatorSetRoot) == "" {
		return consensus.ValidatorSet{}, fmt.Errorf("posnode: safety validator checkpoint is unavailable")
	}
	blockHash, err := structure.HashFromBase58(checkpoint.FinalizedHash)
	if err != nil {
		return consensus.ValidatorSet{}, fmt.Errorf("posnode: decode safety checkpoint hash: %w", err)
	}
	state, err := node.ledger.StateAtBlockHash(blockHash)
	if err != nil {
		return consensus.ValidatorSet{}, err
	}
	set, err := blockchain.ValidatorSetFromStateAtEpoch(state, checkpoint.EpochID)
	if err == nil {
		root, rootErr := consensus.ValidatorSetRoot(set)
		if rootErr == nil && root.String() == checkpoint.ValidatorSetRoot {
			return set, nil
		}
	}
	// The safety commitment names the authenticated epoch committee. Mid-epoch
	// jailing or stake changes at its finalized block must not replace that set.
	snapshot, err := node.authenticatedEpochSnapshot(checkpoint.EpochID)
	if err != nil {
		return consensus.ValidatorSet{}, fmt.Errorf("posnode: authenticate safety epoch committee: %w", err)
	}
	set, err = consensus.NewValidatorSet(snapshot.Validators)
	if err != nil {
		return consensus.ValidatorSet{}, err
	}
	root, err := consensus.ValidatorSetRoot(set)
	if err != nil || root.String() != checkpoint.ValidatorSetRoot {
		return consensus.ValidatorSet{}, fmt.Errorf("posnode: safety validator set root mismatch")
	}
	return set, nil
}

func (node *posNode) validatorForRecoveryLocked(rawSet consensus.ValidatorSet, validatorID consensus.ValidatorID) (consensus.ValidatorState, error) {
	for _, validator := range rawSet.Validators() {
		if validator.ValidatorID == validatorID {
			return validator, nil
		}
	}
	safetySet, err := node.safetyValidatorSet()
	if err != nil {
		return consensus.ValidatorState{}, err
	}
	for _, validator := range safetySet.Validators() {
		if validator.ValidatorID == validatorID {
			return validator, nil
		}
	}
	return consensus.ValidatorState{}, fmt.Errorf("posnode: recovery elder is not in the safety validator set")
}
