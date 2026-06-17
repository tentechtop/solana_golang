package consensus

import (
	"bytes"
	"fmt"
	"sort"

	"solana_golang/programs/stake"
	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	DefaultRewardFinalityDepth         = uint64(2)
	DefaultVoteRewardLamportsPerCredit = uint64(1000)
	DefaultMaxVoteRewardDelaySlots     = uint64(64)
	DefaultMissedVoteJailThreshold     = uint64(16)
	DefaultMissedVoteJailEpochs        = uint64(1)
	DefaultMissedProposalJailThreshold = uint64(8)
	DefaultMissedProposalJailEpochs    = uint64(1)
	DefaultMaliciousSlashBasisPoints   = uint16(500)
	DefaultMaliciousJailEpochs         = uint64(4)
	MaxRewardQCsPerBlock               = 128
	MaxSlashingEvidencePerBlock        = 128
	MaxBlockRewards                    = 8192
	rewardBasisPointsDenominator       = uint64(10000)
)

type RewardType uint8

const (
	RewardTypeLeaderFee RewardType = iota + 1
	RewardTypeVoteCredit
	RewardTypeVotePayout
	RewardTypeCommission
	RewardTypeSlash
	RewardTypeJail
	RewardTypeMissedProposal
)

// RewardConfig 描述奖励参数 + 所有节点必须使用同一配置才能得到一致 state root。
type RewardConfig struct {
	FinalityDepth               uint64
	VoteRewardLamportsPerCredit uint64
	MaxVoteRewardDelaySlots     uint64
	MissedVoteJailThreshold     uint64
	MissedVoteJailEpochs        uint64
	MissedProposalJailThreshold uint64
	MissedProposalJailEpochs    uint64
	MaliciousSlashBasisPoints   uint16
	MaliciousJailEpochs         uint64
}

// BlockReward 描述区块奖励事件 + 进入 reward root 防止 leader 私自多发或漏发。
type BlockReward struct {
	Type           RewardType
	ValidatorID    string
	AccountAddress structure.PublicKey
	StakerAddress  structure.PublicKey
	EpochID        uint64
	Slot           uint64
	Lamports       uint64
	Credits        uint64
}

// BlockRewardInput 描述奖励状态转换输入 + 出块和验块必须传入完全相同的确定性数据。
type BlockRewardInput struct {
	Slot          uint64
	ParentSlot    uint64
	Height        uint64
	EpochID       uint64
	EpochSnapshot EpochSnapshot
	Schedule      LeaderSchedule
	Leader        ValidatorState
	FeeDetails    []structure.FeeDetails
	RewardQCs     []QuorumCertificate
	Evidence      []SlashingEvidence
	Config        RewardConfig
	RentConfig    structure.RentConfig
}

type stakeAccountEntry struct {
	Address structure.PublicKey
	Index   int
}

// DefaultRewardConfig 返回默认奖励策略 + 兼顾本地测试网可见收益和确定性惩罚。
func DefaultRewardConfig() RewardConfig {
	return RewardConfig{
		FinalityDepth:               DefaultRewardFinalityDepth,
		VoteRewardLamportsPerCredit: DefaultVoteRewardLamportsPerCredit,
		MaxVoteRewardDelaySlots:     DefaultMaxVoteRewardDelaySlots,
		MissedVoteJailThreshold:     DefaultMissedVoteJailThreshold,
		MissedVoteJailEpochs:        DefaultMissedVoteJailEpochs,
		MissedProposalJailThreshold: DefaultMissedProposalJailThreshold,
		MissedProposalJailEpochs:    DefaultMissedProposalJailEpochs,
		MaliciousSlashBasisPoints:   DefaultMaliciousSlashBasisPoints,
		MaliciousJailEpochs:         DefaultMaliciousJailEpochs,
	}
}

// ApplyBlockRewards 应用奖励和惩罚 + 与交易执行结果一起参与区块 state root。
func ApplyBlockRewards(state ChainState, input BlockRewardInput) (ChainState, []BlockReward, error) {
	normalizedInput := input.normalize()
	if err := normalizedInput.validate(); err != nil {
		return ChainState{}, nil, err
	}

	nextState := state.clone()
	accountIndexByAddress := accountIndexByAddress(nextState)
	rewards := make([]BlockReward, 0, len(normalizedInput.FeeDetails)+len(normalizedInput.RewardQCs))
	if err := applyLeaderFeeReward(&nextState, accountIndexByAddress, normalizedInput, &rewards); err != nil {
		return ChainState{}, nil, err
	}
	if err := applyMissedLeaderProposals(&nextState, accountIndexByAddress, normalizedInput, &rewards); err != nil {
		return ChainState{}, nil, err
	}
	if err := applyFinalizedVoteCredits(&nextState, accountIndexByAddress, normalizedInput, &rewards); err != nil {
		return ChainState{}, nil, err
	}
	if err := applySlashingEvidence(&nextState, accountIndexByAddress, normalizedInput, &rewards); err != nil {
		return ChainState{}, nil, err
	}
	if err := applyEpochRewardSettlement(&nextState, accountIndexByAddress, normalizedInput, &rewards); err != nil {
		return ChainState{}, nil, err
	}
	if len(rewards) > MaxBlockRewards {
		return ChainState{}, nil, fmt.Errorf("consensus: block rewards exceed limit")
	}
	return nextState, rewards, nil
}

// HashBlockRewards 计算奖励根 + 空奖励使用零哈希保持旧测试块兼容。
func HashBlockRewards(rewards []BlockReward) (structure.Hash, error) {
	if len(rewards) == 0 {
		return structure.NewHash(make([]byte, structure.HashSize))
	}
	encoded := make([]byte, 0, len(rewards)*160)
	for _, reward := range rewards {
		encoded = append(encoded, byte(reward.Type))
		encoded = append(encoded, []byte(reward.ValidatorID)...)
		encoded = append(encoded, 0)
		encoded = append(encoded, reward.AccountAddress[:]...)
		encoded = append(encoded, reward.StakerAddress[:]...)
		encoded = appendUint64ForHash(encoded, reward.EpochID)
		encoded = appendUint64ForHash(encoded, reward.Slot)
		encoded = appendUint64ForHash(encoded, reward.Lamports)
		encoded = appendUint64ForHash(encoded, reward.Credits)
	}
	return structure.NewHash(utils.SHA256(encoded))
}

// EqualBlockRewards 比较奖励列表 + 验块时确保提案展示内容与本地重算完全一致。
func EqualBlockRewards(left []BlockReward, right []BlockReward) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (input BlockRewardInput) normalize() BlockRewardInput {
	if input.Config == (RewardConfig{}) {
		input.Config = DefaultRewardConfig()
	}
	if input.Config.FinalityDepth == 0 {
		input.Config.FinalityDepth = DefaultRewardFinalityDepth
	}
	if input.Config.MaxVoteRewardDelaySlots == 0 {
		input.Config.MaxVoteRewardDelaySlots = DefaultMaxVoteRewardDelaySlots
	}
	if input.Config.MissedVoteJailThreshold == 0 {
		input.Config.MissedVoteJailThreshold = DefaultMissedVoteJailThreshold
	}
	if input.Config.MissedVoteJailEpochs == 0 {
		input.Config.MissedVoteJailEpochs = DefaultMissedVoteJailEpochs
	}
	if input.Config.MissedProposalJailThreshold == 0 {
		input.Config.MissedProposalJailThreshold = DefaultMissedProposalJailThreshold
	}
	if input.Config.MissedProposalJailEpochs == 0 {
		input.Config.MissedProposalJailEpochs = DefaultMissedProposalJailEpochs
	}
	if input.Config.MaliciousSlashBasisPoints == 0 {
		input.Config.MaliciousSlashBasisPoints = DefaultMaliciousSlashBasisPoints
	}
	if input.Config.MaliciousJailEpochs == 0 {
		input.Config.MaliciousJailEpochs = DefaultMaliciousJailEpochs
	}
	if input.RentConfig == (structure.RentConfig{}) {
		input.RentConfig = structure.DefaultRentConfig
	}
	return input
}

func (input BlockRewardInput) validate() error {
	if input.Height == 0 || input.Slot == 0 {
		return fmt.Errorf("consensus: reward input height and slot must be positive")
	}
	if input.Leader.ValidatorID == "" || input.Leader.AccountAddress.IsZero() {
		return fmt.Errorf("consensus: reward leader is invalid")
	}
	if len(input.RewardQCs) > MaxRewardQCsPerBlock {
		return fmt.Errorf("consensus: reward qc count exceeds limit")
	}
	if len(input.Evidence) > MaxSlashingEvidencePerBlock {
		return fmt.Errorf("consensus: slashing evidence count exceeds limit")
	}
	if input.Config.MaliciousSlashBasisPoints > 10000 {
		return fmt.Errorf("consensus: malicious slash bps exceeds 10000")
	}
	return input.RentConfig.Validate()
}

func applyLeaderFeeReward(
	state *ChainState,
	accountIndexByAddress map[structure.PublicKey]int,
	input BlockRewardInput,
	rewards *[]BlockReward,
) error {
	validatorFee, err := sumValidatorFees(input.FeeDetails)
	if err != nil {
		return err
	}
	if validatorFee == 0 {
		return nil
	}
	if err := creditAccountLamports(state, accountIndexByAddress, input.Leader.AccountAddress, validatorFee, input.RentConfig); err != nil {
		return fmt.Errorf("consensus: credit leader fee reward: %w", err)
	}
	*rewards = append(*rewards, BlockReward{
		Type:           RewardTypeLeaderFee,
		ValidatorID:    string(input.Leader.ValidatorID),
		AccountAddress: input.Leader.AccountAddress,
		EpochID:        input.EpochID,
		Slot:           input.Slot,
		Lamports:       validatorFee,
	})
	return nil
}

func applyMissedLeaderProposals(
	state *ChainState,
	accountIndexByAddress map[structure.PublicKey]int,
	input BlockRewardInput,
	rewards *[]BlockReward,
) error {
	if input.ParentSlot == 0 || input.ParentSlot+1 >= input.Slot {
		return nil
	}
	schedule := input.Schedule
	if len(schedule.SlotToLeader) == 0 {
		generatedSchedule, err := NewLeaderSchedule(input.EpochSnapshot)
		if err != nil {
			return err
		}
		schedule = generatedSchedule
	}
	for slot := input.ParentSlot + 1; slot < input.Slot; slot++ {
		if slot < input.EpochSnapshot.StartSlot || slot > input.EpochSnapshot.EndSlot {
			continue
		}
		leaderID, err := schedule.LeaderForSlot(slot)
		if err != nil {
			return err
		}
		leader, exists := input.EpochSnapshot.ValidatorByID(leaderID)
		if !exists {
			return fmt.Errorf("consensus: missed proposal leader missing from snapshot")
		}
		if err := recordMissedLeaderProposal(state, accountIndexByAddress, leader, input, slot, rewards); err != nil {
			return err
		}
	}
	return nil
}

func recordMissedLeaderProposal(
	state *ChainState,
	accountIndexByAddress map[structure.PublicKey]int,
	leader ValidatorState,
	input BlockRewardInput,
	slot uint64,
	rewards *[]BlockReward,
) error {
	stakeState, account, index, err := loadStakeStateByAddress(*state, accountIndexByAddress, leader.AccountAddress)
	if err != nil {
		return err
	}
	if stakeState.MissedProposalCount == ^uint64(0) {
		return fmt.Errorf("consensus: missed proposal count overflow")
	}
	stakeState.MissedProposalCount++
	*rewards = append(*rewards, BlockReward{
		Type:           RewardTypeMissedProposal,
		ValidatorID:    string(leader.ValidatorID),
		AccountAddress: leader.AccountAddress,
		StakerAddress:  stakeState.StakerAccount,
		EpochID:        input.EpochID,
		Slot:           slot,
		Credits:        1,
	})
	return writeStakeState(state, index, account, stakeState, input.RentConfig)
}

func applyFinalizedVoteCredits(
	state *ChainState,
	accountIndexByAddress map[structure.PublicKey]int,
	input BlockRewardInput,
	rewards *[]BlockReward,
) error {
	rewardQCs, err := normalizedRewardQCs(input.RewardQCs)
	if err != nil {
		return err
	}
	for _, qc := range rewardQCs {
		if !isRewardEligibleQC(qc, input) {
			continue
		}
		if err := applySingleQCVoteCredit(state, accountIndexByAddress, input, qc, rewards); err != nil {
			return err
		}
	}
	return nil
}

func applySingleQCVoteCredit(
	state *ChainState,
	accountIndexByAddress map[structure.PublicKey]int,
	input BlockRewardInput,
	qc QuorumCertificate,
	rewards *[]BlockReward,
) error {
	voted := voterSet(qc.Voters)
	for _, validator := range input.EpochSnapshot.Validators {
		stakeState, account, index, err := loadStakeStateByAddress(*state, accountIndexByAddress, validator.AccountAddress)
		if err != nil {
			return err
		}
		if stakeState.LastRewardedSlot >= qc.Slot {
			continue
		}
		stakeState.LastRewardedSlot = qc.Slot
		if _, ok := voted[string(validator.ValidatorID)]; ok {
			if stakeState.VoteCredits == ^uint64(0) {
				return fmt.Errorf("consensus: vote credits overflow")
			}
			stakeState.VoteCredits++
			stakeState.LastVoteSlot = qc.Slot
			*rewards = append(*rewards, BlockReward{
				Type:           RewardTypeVoteCredit,
				ValidatorID:    string(validator.ValidatorID),
				AccountAddress: validator.AccountAddress,
				EpochID:        input.EpochID,
				Slot:           qc.Slot,
				Credits:        1,
			})
		} else {
			if stakeState.MissedVoteCount == ^uint64(0) {
				return fmt.Errorf("consensus: missed vote count overflow")
			}
			stakeState.MissedVoteCount++
		}
		if err := writeStakeState(state, index, account, stakeState, input.RentConfig); err != nil {
			return err
		}
	}
	return nil
}

type slashingEvidenceItem struct {
	Validator ValidatorState
	Slot      uint64
	Key       string
}

func applySlashingEvidence(
	state *ChainState,
	accountIndexByAddress map[structure.PublicKey]int,
	input BlockRewardInput,
	rewards *[]BlockReward,
) error {
	items, err := normalizeSlashingEvidence(input.Evidence, input.EpochSnapshot)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := applySingleSlashingEvidence(state, accountIndexByAddress, input, item, rewards); err != nil {
			return err
		}
	}
	return nil
}

func normalizeSlashingEvidence(
	evidences []SlashingEvidence,
	snapshot EpochSnapshot,
) ([]slashingEvidenceItem, error) {
	items := make([]slashingEvidenceItem, 0, len(evidences))
	seen := make(map[string]struct{}, len(evidences))
	for index, evidence := range evidences {
		validator, slot, err := evidence.Validate(snapshot)
		if err != nil {
			return nil, fmt.Errorf("consensus: slashing evidence %d: %w", index, err)
		}
		key, err := slashingEvidenceKey(evidence, validator, slot)
		if err != nil {
			return nil, fmt.Errorf("consensus: slashing evidence %d key: %w", index, err)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, slashingEvidenceItem{
			Validator: validator,
			Slot:      slot,
			Key:       key,
		})
	}
	sort.Slice(items, func(leftIndex int, rightIndex int) bool {
		left := items[leftIndex]
		right := items[rightIndex]
		if left.Slot != right.Slot {
			return left.Slot < right.Slot
		}
		if left.Validator.ValidatorID != right.Validator.ValidatorID {
			return left.Validator.ValidatorID < right.Validator.ValidatorID
		}
		return left.Key < right.Key
	})
	return items, nil
}

func slashingEvidenceKey(evidence SlashingEvidence, validator ValidatorState, slot uint64) (string, error) {
	switch evidence.Type {
	case SlashingEvidenceTypeDoubleProposal:
		firstHash, err := evidence.DoubleProposal.FirstProposal.Hash()
		if err != nil {
			return "", err
		}
		secondHash, err := evidence.DoubleProposal.SecondProposal.Hash()
		if err != nil {
			return "", err
		}
		first, second := orderedStrings(firstHash.String(), secondHash.String())
		return fmt.Sprintf("%d/%s/%d/%s/%s", evidence.Type, validator.ValidatorID, slot, first, second), nil
	case SlashingEvidenceTypeDoubleVote:
		firstVote := evidence.DoubleVote.FirstVote.Vote
		secondVote := evidence.DoubleVote.SecondVote.Vote
		first := fmt.Sprintf("%d/%d/%s", firstVote.Type, firstVote.BlockHeight, firstVote.BlockHash.String())
		second := fmt.Sprintf("%d/%d/%s", secondVote.Type, secondVote.BlockHeight, secondVote.BlockHash.String())
		first, second = orderedStrings(first, second)
		return fmt.Sprintf("%d/%s/%d/%s/%s", evidence.Type, validator.ValidatorID, slot, first, second), nil
	default:
		return "", fmt.Errorf("consensus: unsupported slashing evidence type %d", evidence.Type)
	}
}

func applySingleSlashingEvidence(
	state *ChainState,
	accountIndexByAddress map[structure.PublicKey]int,
	input BlockRewardInput,
	item slashingEvidenceItem,
	rewards *[]BlockReward,
) error {
	stakeState, account, index, err := loadStakeStateByAddress(*state, accountIndexByAddress, item.Validator.AccountAddress)
	if err != nil {
		return err
	}
	if stakeState.LastSlashedSlot >= item.Slot {
		return nil
	}
	slashLamports, err := calculateSlashLamports(stakeState, input.Config.MaliciousSlashBasisPoints)
	if err != nil {
		return err
	}
	burnedLamports, nextAccount, nextStakeState, err := burnSlashFromValidator(account, stakeState, slashLamports, input)
	if err != nil {
		return err
	}
	jailUntilEpoch := input.EpochID + input.Config.MaliciousJailEpochs
	if nextStakeState.JailUntilEpoch > jailUntilEpoch {
		jailUntilEpoch = nextStakeState.JailUntilEpoch
	}
	nextStakeState.Status = stake.ValidatorStatusJailed
	nextStakeState.JailUntilEpoch = jailUntilEpoch
	nextStakeState.UnlockEpoch = jailUntilEpoch
	nextStakeState.LastSlashedSlot = item.Slot
	nextStakeState.VoteCredits = 0
	nextStakeState.MissedVoteCount = 0
	nextStakeState.MissedProposalCount = 0
	effectiveStake, err := stake.EffectiveStakeAtEpoch(nextStakeState, input.EpochID)
	if err != nil {
		return fmt.Errorf("consensus: refresh slashed effective stake: %w", err)
	}
	nextStakeState.LastEffectiveStake = effectiveStake
	validatorID := string(NewValidatorID(nextStakeState.ConsensusPublicKey))
	if burnedLamports > 0 {
		*rewards = append(*rewards, BlockReward{
			Type:           RewardTypeSlash,
			ValidatorID:    validatorID,
			AccountAddress: item.Validator.AccountAddress,
			StakerAddress:  nextStakeState.StakerAccount,
			EpochID:        input.EpochID,
			Slot:           item.Slot,
			Lamports:       burnedLamports,
		})
	}
	*rewards = append(*rewards, BlockReward{
		Type:           RewardTypeJail,
		ValidatorID:    validatorID,
		AccountAddress: item.Validator.AccountAddress,
		StakerAddress:  nextStakeState.StakerAccount,
		EpochID:        input.EpochID,
		Slot:           item.Slot,
	})
	return writeStakeState(state, index, nextAccount, nextStakeState, input.RentConfig)
}

func orderedStrings(left string, right string) (string, string) {
	if left <= right {
		return left, right
	}
	return right, left
}

func applyEpochRewardSettlement(
	state *ChainState,
	accountIndexByAddress map[structure.PublicKey]int,
	input BlockRewardInput,
	rewards *[]BlockReward,
) error {
	if input.EpochID == 0 {
		return nil
	}
	for _, entry := range sortedStakeAccountEntries(*state) {
		stakeState, account, _, err := loadStakeStateByAddress(*state, accountIndexByAddress, entry.Address)
		if err != nil {
			return err
		}
		if stakeState.LastRewardEpoch >= input.EpochID {
			continue
		}
		if shouldJailForMissedPerformance(stakeState, input.Config) {
			if err := settleJailedValidator(state, entry, account, stakeState, input, rewards); err != nil {
				return err
			}
			continue
		}
		if err := settleRewardedValidator(state, accountIndexByAddress, entry, account, stakeState, input, rewards); err != nil {
			return err
		}
	}
	return nil
}

func settleRewardedValidator(
	state *ChainState,
	accountIndexByAddress map[structure.PublicKey]int,
	entry stakeAccountEntry,
	account structure.Account,
	stakeState stake.ValidatorState,
	input BlockRewardInput,
	rewards *[]BlockReward,
) error {
	if err := stake.MatureStakeForEpoch(&stakeState, input.EpochID); err != nil {
		return fmt.Errorf("consensus: mature rewarded stake: %w", err)
	}
	rewardCredits := effectiveRewardCredits(stakeState)
	rewardLamports, err := calculateVoteReward(rewardCredits, input.Config)
	if err != nil {
		return err
	}
	commissionLamports := rewardLamports * uint64(stakeState.CommissionBps) / rewardBasisPointsDenominator
	stakerLamports := rewardLamports - commissionLamports
	validatorID := string(NewValidatorID(stakeState.ConsensusPublicKey))
	if commissionLamports > 0 {
		if err := account.CreditLamports(commissionLamports); err != nil {
			return err
		}
		*rewards = append(*rewards, BlockReward{
			Type:           RewardTypeCommission,
			ValidatorID:    validatorID,
			AccountAddress: entry.Address,
			StakerAddress:  stakeState.StakerAccount,
			EpochID:        input.EpochID,
			Slot:           input.Slot,
			Lamports:       commissionLamports,
		})
	}
	if stakerLamports > 0 {
		if stakeState.StakerAccount == entry.Address {
			if err := account.CreditLamports(stakerLamports); err != nil {
				return err
			}
		} else {
			if err := creditAccountLamports(state, accountIndexByAddress, stakeState.StakerAccount, stakerLamports, input.RentConfig); err != nil {
				return fmt.Errorf("consensus: credit staker vote reward: %w", err)
			}
		}
		*rewards = append(*rewards, BlockReward{
			Type:           RewardTypeVotePayout,
			ValidatorID:    validatorID,
			AccountAddress: stakeState.StakerAccount,
			StakerAddress:  stakeState.StakerAccount,
			EpochID:        input.EpochID,
			Slot:           input.Slot,
			Lamports:       stakerLamports,
			Credits:        rewardCredits,
		})
	}
	if ^uint64(0)-stakeState.RewardLamports < rewardLamports {
		return fmt.Errorf("consensus: validator reward overflow")
	}
	stakeState.RewardLamports += rewardLamports
	stakeState.VoteCredits = 0
	stakeState.MissedVoteCount = 0
	stakeState.MissedProposalCount = 0
	stakeState.LastRewardEpoch = input.EpochID
	return writeStakeState(state, entry.Index, account, stakeState, input.RentConfig)
}

func settleJailedValidator(
	state *ChainState,
	entry stakeAccountEntry,
	account structure.Account,
	stakeState stake.ValidatorState,
	input BlockRewardInput,
	rewards *[]BlockReward,
) error {
	if err := stake.MatureStakeForEpoch(&stakeState, input.EpochID); err != nil {
		return fmt.Errorf("consensus: mature jailed stake: %w", err)
	}
	validatorID := string(NewValidatorID(stakeState.ConsensusPublicKey))
	nextAccount := account
	nextStakeState := stakeState
	nextStakeState.Status = stake.ValidatorStatusJailed
	nextStakeState.JailUntilEpoch = input.EpochID + missedPerformanceJailEpochs(stakeState, input.Config)
	nextStakeState.UnlockEpoch = nextStakeState.JailUntilEpoch
	nextStakeState.VoteCredits = 0
	nextStakeState.MissedVoteCount = 0
	nextStakeState.MissedProposalCount = 0
	nextStakeState.LastRewardEpoch = input.EpochID
	effectiveStake, err := stake.EffectiveStakeAtEpoch(nextStakeState, input.EpochID)
	if err != nil {
		return fmt.Errorf("consensus: refresh jailed effective stake: %w", err)
	}
	nextStakeState.LastEffectiveStake = effectiveStake
	*rewards = append(*rewards, BlockReward{
		Type:           RewardTypeJail,
		ValidatorID:    validatorID,
		AccountAddress: entry.Address,
		StakerAddress:  nextStakeState.StakerAccount,
		EpochID:        input.EpochID,
		Slot:           input.Slot,
	})
	return writeStakeState(state, entry.Index, nextAccount, nextStakeState, input.RentConfig)
}

func normalizedRewardQCs(qcs []QuorumCertificate) ([]QuorumCertificate, error) {
	normalized := make([]QuorumCertificate, 0, len(qcs))
	seenSlots := make(map[uint64]struct{}, len(qcs))
	for _, qc := range qcs {
		if err := qc.Validate(); err != nil {
			return nil, err
		}
		if qc.Type != VoteTypeConfirm {
			continue
		}
		if _, exists := seenSlots[qc.Slot]; exists {
			return nil, fmt.Errorf("%w: duplicate reward qc slot", ErrInvalidCertificate)
		}
		seenSlots[qc.Slot] = struct{}{}
		normalized = append(normalized, qc)
	}
	sort.Slice(normalized, func(leftIndex int, rightIndex int) bool {
		left := normalized[leftIndex]
		right := normalized[rightIndex]
		if left.BlockHeight != right.BlockHeight {
			return left.BlockHeight < right.BlockHeight
		}
		if left.Slot != right.Slot {
			return left.Slot < right.Slot
		}
		return left.BlockHash.String() < right.BlockHash.String()
	})
	return normalized, nil
}

func isRewardEligibleQC(qc QuorumCertificate, input BlockRewardInput) bool {
	if qc.BlockHeight+input.Config.FinalityDepth > input.Height {
		return false
	}
	if qc.Slot+input.Config.MaxVoteRewardDelaySlots < input.Slot {
		return false
	}
	return true
}

func effectiveRewardCredits(state stake.ValidatorState) uint64 {
	if state.MissedProposalCount >= state.VoteCredits {
		return 0
	}
	return state.VoteCredits - state.MissedProposalCount
}

func shouldJailForMissedPerformance(state stake.ValidatorState, config RewardConfig) bool {
	if state.Status != stake.ValidatorStatusActive {
		return false
	}
	return state.MissedVoteCount >= config.MissedVoteJailThreshold ||
		state.MissedProposalCount >= config.MissedProposalJailThreshold
}

func missedPerformanceJailEpochs(state stake.ValidatorState, config RewardConfig) uint64 {
	jailEpochs := uint64(0)
	if state.MissedVoteCount >= config.MissedVoteJailThreshold {
		jailEpochs = config.MissedVoteJailEpochs
	}
	if state.MissedProposalCount >= config.MissedProposalJailThreshold &&
		config.MissedProposalJailEpochs > jailEpochs {
		jailEpochs = config.MissedProposalJailEpochs
	}
	if jailEpochs == 0 {
		return 1
	}
	return jailEpochs
}

func sumValidatorFees(fees []structure.FeeDetails) (uint64, error) {
	var total uint64
	for _, fee := range fees {
		if ^uint64(0)-total < fee.ValidatorFee {
			return 0, fmt.Errorf("consensus: validator fee overflow")
		}
		total += fee.ValidatorFee
	}
	return total, nil
}

func calculateVoteReward(credits uint64, config RewardConfig) (uint64, error) {
	if credits == 0 || config.VoteRewardLamportsPerCredit == 0 {
		return 0, nil
	}
	if credits > ^uint64(0)/config.VoteRewardLamportsPerCredit {
		return 0, fmt.Errorf("consensus: vote reward overflow")
	}
	return credits * config.VoteRewardLamportsPerCredit, nil
}

func calculateSlashLamports(state stake.ValidatorState, basisPoints uint16) (uint64, error) {
	totalStake, err := validatorTotalStake(state)
	if err != nil {
		return 0, err
	}
	if basisPoints != 0 && totalStake > ^uint64(0)/uint64(basisPoints) {
		return 0, fmt.Errorf("consensus: slash amount overflow")
	}
	return totalStake * uint64(basisPoints) / rewardBasisPointsDenominator, nil
}

func burnSlashFromValidator(
	account structure.Account,
	state stake.ValidatorState,
	requestedSlash uint64,
	input BlockRewardInput,
) (uint64, structure.Account, stake.ValidatorState, error) {
	if requestedSlash == 0 {
		return 0, account, state, nil
	}
	minimumBalance, err := account.MinimumBalance(input.RentConfig)
	if err != nil {
		return 0, structure.Account{}, stake.ValidatorState{}, err
	}
	if account.Lamports <= minimumBalance {
		return 0, account, state, nil
	}
	burnedLamports := requestedSlash
	if burnedLamports > account.Lamports-minimumBalance {
		burnedLamports = account.Lamports - minimumBalance
	}
	if err := account.DebitLamports(burnedLamports, input.RentConfig); err != nil {
		return 0, structure.Account{}, stake.ValidatorState{}, err
	}
	remainingSlash := burnedLamports
	state.PendingStake, remainingSlash = subtractStakeBucket(state.PendingStake, remainingSlash)
	state.ActiveStake, remainingSlash = subtractStakeBucket(state.ActiveStake, remainingSlash)
	state.UnlockingStake, _ = subtractStakeBucket(state.UnlockingStake, remainingSlash)
	return burnedLamports, account, state, nil
}

func validatorTotalStake(state stake.ValidatorState) (uint64, error) {
	total := state.ActiveStake
	if ^uint64(0)-total < state.PendingStake {
		return 0, fmt.Errorf("consensus: validator stake overflow")
	}
	total += state.PendingStake
	if ^uint64(0)-total < state.UnlockingStake {
		return 0, fmt.Errorf("consensus: validator stake overflow")
	}
	return total + state.UnlockingStake, nil
}

func subtractStakeBucket(value uint64, remaining uint64) (uint64, uint64) {
	if remaining == 0 {
		return value, 0
	}
	if value >= remaining {
		return value - remaining, 0
	}
	return 0, remaining - value
}

func loadStakeStateByAddress(
	state ChainState,
	accountIndexByAddress map[structure.PublicKey]int,
	address structure.PublicKey,
) (stake.ValidatorState, structure.Account, int, error) {
	index, exists := accountIndexByAddress[address]
	if !exists {
		return stake.ValidatorState{}, structure.Account{}, 0, fmt.Errorf("consensus: stake account not found")
	}
	account := state.Accounts[index].Account.Clone()
	stakeState, err := stake.UnmarshalValidatorStateBinary(account.Data)
	if err != nil {
		return stake.ValidatorState{}, structure.Account{}, 0, err
	}
	return stakeState, account, index, nil
}

func writeStakeState(
	state *ChainState,
	index int,
	account structure.Account,
	stakeState stake.ValidatorState,
	rentConfig structure.RentConfig,
) error {
	data, err := stakeState.MarshalBinary()
	if err != nil {
		return err
	}
	if err := account.SetData(data, rentConfig); err != nil {
		return err
	}
	state.Accounts[index] = structure.AddressedAccount{
		Address: state.Accounts[index].Address,
		Account: account,
	}
	return nil
}

func creditAccountLamports(
	state *ChainState,
	accountIndexByAddress map[structure.PublicKey]int,
	address structure.PublicKey,
	lamports uint64,
	rentConfig structure.RentConfig,
) error {
	if lamports == 0 {
		return nil
	}
	index, exists := accountIndexByAddress[address]
	if !exists {
		return fmt.Errorf("consensus: account %s not found", address.String())
	}
	account := state.Accounts[index].Account.Clone()
	if err := account.CreditLamports(lamports); err != nil {
		return err
	}
	if err := account.ValidateWithRent(rentConfig); err != nil {
		return err
	}
	state.Accounts[index] = structure.AddressedAccount{Address: address, Account: account}
	return nil
}

func accountIndexByAddress(state ChainState) map[structure.PublicKey]int {
	indexByAddress := make(map[structure.PublicKey]int, len(state.Accounts))
	for index, account := range state.Accounts {
		indexByAddress[account.Address] = index
	}
	return indexByAddress
}

func sortedStakeAccountEntries(state ChainState) []stakeAccountEntry {
	entries := make([]stakeAccountEntry, 0)
	for index, account := range state.Accounts {
		if account.Account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Account.Data) == 0 {
			continue
		}
		entries = append(entries, stakeAccountEntry{Address: account.Address, Index: index})
	}
	sort.Slice(entries, func(leftIndex int, rightIndex int) bool {
		return bytes.Compare(entries[leftIndex].Address[:], entries[rightIndex].Address[:]) < 0
	})
	return entries
}

func voterSet(voters []string) map[string]struct{} {
	set := make(map[string]struct{}, len(voters))
	for _, voter := range voters {
		set[voter] = struct{}{}
	}
	return set
}
