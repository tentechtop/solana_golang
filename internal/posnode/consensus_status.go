package posnode

import (
	"context"
	"math/big"
	"sort"
	"time"

	"solana_golang/consensus"
	"solana_golang/programs/stake"
	"solana_golang/structure"
)

type consensusStakeRecord struct {
	AccountAddress structure.PublicKey
	State          stake.ValidatorState
	EffectiveStake uint64
}

func (node *posNode) GetConsensusStatus(ctx context.Context) (any, error) {
	_ = ctx
	livenessGate := node.refreshLivenessGate(time.Now())
	node.mutex.Lock()
	status := node.consensusStatusForSlotLocked(node.currentRoutingSlotLocked())
	status.Liveness = livenessGate
	node.mutex.Unlock()
	return status, nil
}

func (node *posNode) consensusStatusForSlotLocked(slot uint64) consensusStatusJSON {
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	status := consensusStatusJSON{
		Slot:             slot,
		LocalValidatorID: string(localValidatorID),
		LocalValidator: consensusValidatorStatusJSON{
			ValidatorID:  string(localValidatorID),
			TurbineLayer: -1,
		},
		Liveness: node.livenessGate,
	}
	if node.config.EpochSlots == 0 || len(node.epochSnapshot.Validators) == 0 {
		return status
	}
	epochContextValue, err := node.epochContextForSlotLocked(slot)
	if err != nil {
		return status
	}

	status.Available = true
	status.EpochID = epochContextValue.EpochID
	status.EpochStartSlot = epochContextValue.Snapshot.StartSlot
	status.EpochEndSlot = epochContextValue.Snapshot.EndSlot
	status.TotalActiveStake = epochContextValue.Snapshot.TotalActiveStake
	stakeRecords := node.consensusStakeRecords(status.EpochID)
	currentValidators := currentValidatorMap(epochContextValue.Snapshot)
	tree, treeAvailable := node.turbineTreeForStatusLocked(slot, epochContextValue)
	validatorIDs := sortedConsensusValidatorIDs(currentValidators, stakeRecords)
	status.Validators = make([]consensusValidatorStatusJSON, 0, len(validatorIDs))
	for _, validatorID := range validatorIDs {
		validatorStatus := node.consensusValidatorStatus(
			validatorID,
			currentValidators,
			stakeRecords,
			tree,
			treeAvailable,
			status.TotalActiveStake,
		)
		status.Validators = append(status.Validators, validatorStatus)
		if validatorID == localValidatorID {
			status.LocalValidator = validatorStatus
		}
	}
	status.ValidatorCount = len(status.Validators)
	return status
}

func (node *posNode) turbineTreeForStatusLocked(slot uint64, epochContextValue epochContext) (consensus.TurbineTree, bool) {
	leaderID, err := epochContextValue.Schedule.LeaderForSlot(slot)
	if err != nil {
		return consensus.TurbineTree{}, false
	}
	tree, err := consensus.NewTurbineTree(epochContextValue.Snapshot, slot, leaderID, node.config.TurbineFanout)
	if err != nil {
		return consensus.TurbineTree{}, false
	}
	return tree, true
}

func (node *posNode) consensusStakeRecords(epochID uint64) map[consensus.ValidatorID]consensusStakeRecord {
	records := make(map[consensus.ValidatorID]consensusStakeRecord)
	if node.ledger == nil {
		return records
	}
	state := node.ledger.State()
	for _, account := range state.Accounts {
		if account.Account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Account.Data) == 0 {
			continue
		}
		stakeState, err := stake.UnmarshalValidatorStateBinary(account.Account.Data)
		if err != nil {
			continue
		}
		validatorID := consensus.NewValidatorID(stakeState.ConsensusPublicKey)
		effectiveStake, active, err := validatorEffectiveStakeForEpoch(stakeState, validatorID, epochID)
		if err != nil {
			continue
		}
		if !active {
			effectiveStake = 0
		}
		records[validatorID] = consensusStakeRecord{
			AccountAddress: account.Address,
			State:          stakeState,
			EffectiveStake: effectiveStake,
		}
	}
	return records
}

func currentValidatorMap(snapshot consensus.EpochSnapshot) map[consensus.ValidatorID]consensus.ValidatorState {
	validators := make(map[consensus.ValidatorID]consensus.ValidatorState, len(snapshot.Validators))
	for _, validator := range snapshot.Validators {
		validators[validator.ValidatorID] = validator
	}
	return validators
}

func sortedConsensusValidatorIDs(currentValidators map[consensus.ValidatorID]consensus.ValidatorState, stakeRecords map[consensus.ValidatorID]consensusStakeRecord) []consensus.ValidatorID {
	seen := make(map[consensus.ValidatorID]struct{}, len(currentValidators)+len(stakeRecords))
	validatorIDs := make([]consensus.ValidatorID, 0, len(currentValidators)+len(stakeRecords))
	for validatorID := range currentValidators {
		seen[validatorID] = struct{}{}
		validatorIDs = append(validatorIDs, validatorID)
	}
	for validatorID := range stakeRecords {
		if _, exists := seen[validatorID]; exists {
			continue
		}
		validatorIDs = append(validatorIDs, validatorID)
	}
	sort.Slice(validatorIDs, func(leftIndex int, rightIndex int) bool {
		return validatorIDs[leftIndex] < validatorIDs[rightIndex]
	})
	return validatorIDs
}

func (node *posNode) consensusValidatorStatus(
	validatorID consensus.ValidatorID,
	currentValidators map[consensus.ValidatorID]consensus.ValidatorState,
	stakeRecords map[consensus.ValidatorID]consensusStakeRecord,
	tree consensus.TurbineTree,
	treeAvailable bool,
	totalActiveStake uint64,
) consensusValidatorStatusJSON {
	validator, inCurrentEpoch := currentValidators[validatorID]
	stakeRecord, hasStakeRecord := stakeRecords[validatorID]
	result := consensusValidatorStatusJSON{
		ValidatorID:            string(validatorID),
		InCurrentEpoch:         inCurrentEpoch,
		TurbineLayer:           -1,
		EffectiveStakeLamports: stakeRecord.EffectiveStake,
		WeightBps:              stakeWeightBps(stakeRecord.EffectiveStake, totalActiveStake),
	}
	if inCurrentEpoch {
		result.AccountAddress = validator.AccountAddress.String()
		result.ConsensusPublicKey = validator.ConsensusPublicKey.String()
		result.P2PPeerID = validator.P2PPeerID
		result.Status = validatorStatusText(validator.Status)
		result.EffectiveStakeLamports = validator.StakeLamports
		result.WeightBps = stakeWeightBps(validator.StakeLamports, totalActiveStake)
		result.CommissionBps = validator.CommissionBps
	}
	if hasStakeRecord {
		result.AccountAddress = stakeRecord.AccountAddress.String()
		result.ConsensusPublicKey = stakeRecord.State.ConsensusPublicKey.String()
		result.P2PPeerID = stakeRecord.State.P2PPeerID
		result.Status = stakeValidatorStatusText(stakeRecord.State.Status)
		result.ActiveStakeLamports = stakeRecord.State.ActiveStake
		result.PendingStakeLamports = stakeRecord.State.PendingStake
		result.UnlockingStakeLamports = stakeRecord.State.UnlockingStake
		result.ActivationEpoch = stakeRecord.State.ActivationEpoch
		result.DeactivationEpoch = stakeRecord.State.DeactivationEpoch
		result.LastEffectiveStakeLamports = stakeRecord.State.LastEffectiveStake
		result.JailUntilEpoch = stakeRecord.State.JailUntilEpoch
		result.LastSlashedSlot = stakeRecord.State.LastSlashedSlot
		result.CommissionBps = stakeRecord.State.CommissionBps
		result.EffectiveStakeLamports = stakeRecord.EffectiveStake
		result.WeightBps = stakeWeightBps(stakeRecord.EffectiveStake, totalActiveStake)
	}
	if treeAvailable {
		node.applyTurbineStatus(&result, validatorID, tree)
	}
	return result
}

func (node *posNode) applyTurbineStatus(result *consensusValidatorStatusJSON, validatorID consensus.ValidatorID, tree consensus.TurbineTree) {
	turbineNode, found := tree.NodeByValidator(validatorID)
	if !found {
		return
	}
	result.InTurbineTree = true
	result.TurbineLayer = turbineNode.Layer
	result.TurbineParentValidatorID = string(turbineNode.ParentID)
	result.TurbineParentPeerID = turbineNode.ParentPeerID
	children := tree.ChildrenOf(validatorID)
	result.TurbineChildValidatorIDs = make([]string, 0, len(children))
	result.TurbineChildPeerIDs = make([]string, 0, len(children))
	for _, child := range children {
		result.TurbineChildValidatorIDs = append(result.TurbineChildValidatorIDs, string(child.ValidatorID))
		result.TurbineChildPeerIDs = append(result.TurbineChildPeerIDs, child.P2PPeerID)
	}
}

func stakeWeightBps(stakeLamports uint64, totalStakeLamports uint64) uint64 {
	if stakeLamports == 0 || totalStakeLamports == 0 {
		return 0
	}
	stakeValue := new(big.Int).SetUint64(stakeLamports)
	stakeValue.Mul(stakeValue, big.NewInt(10000))
	stakeValue.Div(stakeValue, new(big.Int).SetUint64(totalStakeLamports))
	return stakeValue.Uint64()
}

func stakeValidatorStatusText(status stake.ValidatorStatus) string {
	switch status {
	case stake.ValidatorStatusActive:
		return "active"
	case stake.ValidatorStatusExiting:
		return "exiting"
	case stake.ValidatorStatusJailed:
		return "jailed"
	default:
		return "inactive"
	}
}
