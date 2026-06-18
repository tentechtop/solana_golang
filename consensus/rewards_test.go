package consensus

import (
	"testing"

	"solana_golang/programs/stake"
	"solana_golang/structure"
)

func TestApplyBlockRewardsCreditsLeaderFee(t *testing.T) {
	fixture := newPoSTestFixture(t)
	leader := fixture.validatorByID(t, mustEpochSnapshot(t, 0, 1, 8, mustHashFromText(t, "reward-seed"), fixture.validatorSetFromGenesis(t)), NewValidatorID(fixture.consensusKeys[0].PublicKey))
	before := mustFindAccount(t, fixture.state, leader.AccountAddress).Account.Lamports

	nextState, rewards, err := ApplyBlockRewards(fixture.state, BlockRewardInput{
		Slot:          1,
		Height:        1,
		EpochID:       0,
		EpochSnapshot: mustEpochSnapshot(t, 0, 1, 8, mustHashFromText(t, "reward-seed"), fixture.validatorSetFromGenesis(t)),
		Leader:        leader,
		FeeDetails:    []structure.FeeDetails{{ValidatorFee: 7000}},
	})
	if err != nil {
		t.Fatalf("ApplyBlockRewards() error = %v", err)
	}
	after := mustFindAccount(t, nextState, leader.AccountAddress).Account.Lamports
	if after != before+7000 {
		t.Fatalf("leader lamports = %d, want %d", after, before+7000)
	}
	if len(rewards) != 1 || rewards[0].Type != RewardTypeLeaderFee {
		t.Fatalf("rewards = %+v, want leader fee reward", rewards)
	}
}

func TestApplyBlockRewardsSettlesVoteCreditsAtEpochBoundary(t *testing.T) {
	state, snapshot, validators := newRewardTestState(t, []uint64{70, 30}, []uint16{1000, 0})
	voterIndex := highestStakeValidatorIndex(snapshot)
	qc := testRewardQC(snapshot, 1, 1, []int{voterIndex})
	creditedState, _, err := ApplyBlockRewards(state, BlockRewardInput{
		Slot:          3,
		Height:        3,
		EpochID:       0,
		EpochSnapshot: snapshot,
		Leader:        validators[voterIndex],
		RewardQCs:     []QuorumCertificate{qc},
	})
	if err != nil {
		t.Fatalf("credit rewards: %v", err)
	}
	validatorState := mustStakeState(t, creditedState, validators[voterIndex].AccountAddress)
	if validatorState.VoteCredits != 1 {
		t.Fatalf("vote credits = %d, want 1", validatorState.VoteCredits)
	}

	stakerBefore := mustFindAccount(t, creditedState, validatorState.StakerAccount).Account.Lamports
	validatorBefore := mustFindAccount(t, creditedState, validators[voterIndex].AccountAddress).Account.Lamports
	settledState, rewards, err := ApplyBlockRewards(creditedState, BlockRewardInput{
		Slot:          9,
		Height:        9,
		EpochID:       1,
		EpochSnapshot: snapshot,
		Leader:        validators[voterIndex],
	})
	if err != nil {
		t.Fatalf("settle rewards: %v", err)
	}
	settledValidator := mustStakeState(t, settledState, validators[voterIndex].AccountAddress)
	if settledValidator.VoteCredits != 0 || settledValidator.LastRewardEpoch != 1 {
		t.Fatalf("settled validator = %+v", settledValidator)
	}
	stakerAfter := mustFindAccount(t, settledState, validatorState.StakerAccount).Account.Lamports
	validatorAfter := mustFindAccount(t, settledState, validators[voterIndex].AccountAddress).Account.Lamports
	if stakerAfter != stakerBefore+183 || validatorAfter != validatorBefore+20 {
		t.Fatalf("reward split staker=%d validator=%d", stakerAfter-stakerBefore, validatorAfter-validatorBefore)
	}
	if len(rewards) == 0 {
		t.Fatal("settlement rewards are empty")
	}
}

func TestApplyBlockRewardsDistributesDelegationPayouts(t *testing.T) {
	state, snapshot, validators := newRewardTestState(t, []uint64{2}, []uint16{1000})
	validator := validators[0]
	delegator := mustKeyPair(t, "reward-delegator-a")
	validatorState := mustStakeState(t, state, validator.AccountAddress)
	validatorState.VoteCredits = 5
	validatorState.ActiveStake = 2 * stake.MinimumStakeLamports
	validatorState.LastEffectiveStake = validatorState.ActiveStake
	validatorState.Delegations = []stake.DelegationState{{
		DelegatorAccount: delegator.PublicKey,
		ActiveStake:      stake.MinimumStakeLamports,
	}}
	state = replaceStakeState(t, state, validator.AccountAddress, validatorState)
	state.Accounts = append(state.Accounts, newTestAccount(t, delegator.PublicKey, 1_000_000, structure.DefaultBuiltinProgramIDs.System, false, nil))

	stakerBefore := mustFindAccount(t, state, validatorState.StakerAccount).Account.Lamports
	delegatorBefore := mustFindAccount(t, state, delegator.PublicKey).Account.Lamports
	validatorBefore := mustFindAccount(t, state, validator.AccountAddress).Account.Lamports

	nextState, rewards, err := ApplyBlockRewards(state, BlockRewardInput{
		Slot:          65,
		Height:        65,
		EpochID:       1,
		EpochSnapshot: snapshot,
		Leader:        validator,
	})
	if err != nil {
		t.Fatalf("ApplyBlockRewards() error = %v", err)
	}
	stakerAfter := mustFindAccount(t, nextState, validatorState.StakerAccount).Account.Lamports
	delegatorAfter := mustFindAccount(t, nextState, delegator.PublicKey).Account.Lamports
	validatorAfter := mustFindAccount(t, nextState, validator.AccountAddress).Account.Lamports
	if validatorAfter != validatorBefore+8 {
		t.Fatalf("commission = %d, want 8", validatorAfter-validatorBefore)
	}
	if stakerAfter != stakerBefore+37 {
		t.Fatalf("staker payout = %d, want 37", stakerAfter-stakerBefore)
	}
	if delegatorAfter != delegatorBefore+37 {
		t.Fatalf("delegator payout = %d, want 37", delegatorAfter-delegatorBefore)
	}
	settledState := mustStakeState(t, nextState, validator.AccountAddress)
	if settledState.Delegations[0].RewardLamports != 37 {
		t.Fatalf("delegation reward = %d, want 37", settledState.Delegations[0].RewardLamports)
	}
	if !containsRewardType(rewards, RewardTypeCommission) || !containsRewardType(rewards, RewardTypeVotePayout) {
		t.Fatalf("rewards = %+v, want commission and vote payout", rewards)
	}
}

func TestApplyBlockRewardsJailsMissedVotesWithoutSlash(t *testing.T) {
	state, snapshot, validators := newRewardTestState(t, []uint64{90, 10}, []uint16{0, 0})
	voterIndex := highestStakeValidatorIndex(snapshot)
	missedIndex := lowestStakeValidatorIndex(snapshot)
	qcs := make([]QuorumCertificate, DefaultMissedVoteJailThreshold)
	for index := range qcs {
		slot := uint64(index + 1)
		qcs[index] = testRewardQC(snapshot, slot, slot, []int{voterIndex})
	}
	before := mustFindAccount(t, state, validators[missedIndex].AccountAddress).Account.Lamports
	rewardSlot := DefaultMissedVoteJailThreshold + DefaultRewardFinalityDepth

	nextState, rewards, err := ApplyBlockRewards(state, BlockRewardInput{
		Slot:          rewardSlot,
		Height:        rewardSlot,
		EpochID:       1,
		EpochSnapshot: snapshot,
		Leader:        validators[voterIndex],
		RewardQCs:     qcs,
		Config: RewardConfig{
			MaxVoteRewardDelaySlots:                 rewardSlot,
			MinActiveValidatorsAfterPerformanceJail: 1,
		},
	})
	if err != nil {
		t.Fatalf("ApplyBlockRewards() error = %v", err)
	}
	jailedState := mustStakeState(t, nextState, validators[missedIndex].AccountAddress)
	if jailedState.Status != stake.ValidatorStatusJailed || jailedState.JailUntilEpoch != 2 {
		t.Fatalf("jailed state = %+v", jailedState)
	}
	after := mustFindAccount(t, nextState, validators[missedIndex].AccountAddress).Account.Lamports
	if after != before {
		t.Fatalf("validator lamports changed on missed votes: before=%d after=%d", before, after)
	}
	if containsRewardType(rewards, RewardTypeSlash) || !containsRewardType(rewards, RewardTypeJail) {
		t.Fatalf("rewards = %+v, want jail without slash", rewards)
	}
}

func TestApplyBlockRewardsKeepsOneActiveValidatorForPerformanceJail(t *testing.T) {
	state, snapshot, validators := newRewardTestState(t, []uint64{30, 30, 30}, []uint16{0, 0, 0})
	for _, validator := range validators {
		validatorState := mustStakeState(t, state, validator.AccountAddress)
		validatorState.MissedVoteCount = DefaultMissedVoteJailThreshold
		state = replaceStakeState(t, state, validator.AccountAddress, validatorState)
	}

	nextState, _, err := ApplyBlockRewards(state, BlockRewardInput{
		Slot:          65,
		Height:        65,
		EpochID:       1,
		EpochSnapshot: snapshot,
		Leader:        validators[0],
	})
	if err != nil {
		t.Fatalf("ApplyBlockRewards() error = %v", err)
	}

	activeCount := 0
	jailedCount := 0
	for _, validator := range validators {
		validatorState := mustStakeState(t, nextState, validator.AccountAddress)
		if validatorState.Status == stake.ValidatorStatusActive {
			activeCount++
		}
		if validatorState.Status == stake.ValidatorStatusJailed {
			jailedCount++
		}
	}
	if activeCount != 1 || jailedCount != 2 {
		t.Fatalf("active=%d jailed=%d, want active=1 jailed=2", activeCount, jailedCount)
	}
}

func TestApplyBlockRewardsRecordsMissedLeaderProposals(t *testing.T) {
	state, snapshot, validators := newRewardTestState(t, []uint64{50, 50}, []uint16{0, 0})
	schedule, err := NewLeaderSchedule(snapshot)
	if err != nil {
		t.Fatalf("NewLeaderSchedule() error = %v", err)
	}
	expectedMisses := make(map[structure.PublicKey]uint64)
	for slot := uint64(3); slot < 5; slot++ {
		leaderID, err := schedule.LeaderForSlot(slot)
		if err != nil {
			t.Fatalf("LeaderForSlot(%d) error = %v", slot, err)
		}
		leader, exists := snapshot.ValidatorByID(leaderID)
		if !exists {
			t.Fatalf("leader %s missing", leaderID)
		}
		expectedMisses[leader.AccountAddress]++
	}

	nextState, rewards, err := ApplyBlockRewards(state, BlockRewardInput{
		Slot:          5,
		ParentSlot:    2,
		Height:        5,
		EpochID:       0,
		EpochSnapshot: snapshot,
		Schedule:      schedule,
		Leader:        validators[0],
	})
	if err != nil {
		t.Fatalf("ApplyBlockRewards() error = %v", err)
	}
	for address, expected := range expectedMisses {
		stakeState := mustStakeState(t, nextState, address)
		if stakeState.MissedProposalCount != expected {
			t.Fatalf("missed proposals for %s = %d, want %d", address.String(), stakeState.MissedProposalCount, expected)
		}
	}
	if countRewardType(rewards, RewardTypeMissedProposal) != 2 {
		t.Fatalf("rewards = %+v, want 2 missed proposal events", rewards)
	}
}

func TestApplyBlockRewardsSettlesVoteCreditsWithMissedProposalPenalty(t *testing.T) {
	state, snapshot, validators := newRewardTestState(t, []uint64{70, 30}, []uint16{0, 0})
	validatorIndex := highestStakeValidatorIndex(snapshot)
	validatorState := mustStakeState(t, state, validators[validatorIndex].AccountAddress)
	validatorState.VoteCredits = 5
	validatorState.MissedProposalCount = 2
	state = replaceStakeState(t, state, validators[validatorIndex].AccountAddress, validatorState)
	stakerBefore := mustFindAccount(t, state, validatorState.StakerAccount).Account.Lamports

	settledState, rewards, err := ApplyBlockRewards(state, BlockRewardInput{
		Slot:          65,
		Height:        65,
		EpochID:       1,
		EpochSnapshot: snapshot,
		Leader:        validators[validatorIndex],
	})
	if err != nil {
		t.Fatalf("ApplyBlockRewards() error = %v", err)
	}
	settledValidator := mustStakeState(t, settledState, validators[validatorIndex].AccountAddress)
	if settledValidator.VoteCredits != 0 || settledValidator.MissedProposalCount != 0 {
		t.Fatalf("settled validator = %+v", settledValidator)
	}
	stakerAfter := mustFindAccount(t, settledState, validatorState.StakerAccount).Account.Lamports
	if stakerAfter != stakerBefore+203 {
		t.Fatalf("staker reward = %d, want 203", stakerAfter-stakerBefore)
	}
	payout := firstRewardType(rewards, RewardTypeVotePayout)
	if payout.Credits != 3 {
		t.Fatalf("payout credits = %d, want 3", payout.Credits)
	}
}

func TestApplyBlockRewardsRejectsInvalidEpochRewardSlotRange(t *testing.T) {
	state, snapshot, validators := newRewardTestState(t, []uint64{1}, []uint16{0})
	validatorState := mustStakeState(t, state, validators[0].AccountAddress)
	validatorState.VoteCredits = 1
	state = replaceStakeState(t, state, validators[0].AccountAddress, validatorState)
	snapshot.EndSlot = snapshot.StartSlot - 1

	_, _, err := ApplyBlockRewards(state, BlockRewardInput{
		Slot:          65,
		Height:        65,
		EpochID:       1,
		EpochSnapshot: snapshot,
		Leader:        validators[0],
	})
	if err == nil {
		t.Fatal("ApplyBlockRewards() error = nil, want invalid epoch slot range")
	}
}

func TestApplyBlockRewardsSlashesSignedDoubleVoteEvidence(t *testing.T) {
	state, snapshot, validators := newRewardTestState(t, []uint64{90, 10}, []uint16{0, 0})
	slashedIndex := highestStakeValidatorIndex(snapshot)
	consensusKey := rewardConsensusKeyForValidator(t, validators[slashedIndex], len(validators))
	evidenceSlot := uint64(7)
	evidence := SlashingEvidence{
		Type: SlashingEvidenceTypeDoubleVote,
		DoubleVote: &SignedDoubleVoteEvidence{
			FirstVote: signedTestVote(t, consensusKey, Vote{
				Type:               VoteTypeConfirm,
				Slot:               evidenceSlot,
				BlockHeight:        7,
				BlockHash:          mustHashFromTestText("double-vote-a"),
				VoterID:            string(validators[slashedIndex].ValidatorID),
				Stake:              validators[slashedIndex].StakeLamports,
				CreatedAtUnixMilli: 1710000000001,
			}),
			SecondVote: signedTestVote(t, consensusKey, Vote{
				Type:               VoteTypeConfirm,
				Slot:               evidenceSlot,
				BlockHeight:        7,
				BlockHash:          mustHashFromTestText("double-vote-b"),
				VoterID:            string(validators[slashedIndex].ValidatorID),
				Stake:              validators[slashedIndex].StakeLamports,
				CreatedAtUnixMilli: 1710000000002,
			}),
		},
	}
	before := mustFindAccount(t, state, validators[slashedIndex].AccountAddress).Account.Lamports

	nextState, rewards, err := ApplyBlockRewards(state, BlockRewardInput{
		Slot:          8,
		Height:        8,
		EpochID:       0,
		EpochSnapshot: snapshot,
		Leader:        validators[lowestStakeValidatorIndex(snapshot)],
		Evidence:      []SlashingEvidence{evidence},
	})
	if err != nil {
		t.Fatalf("ApplyBlockRewards() error = %v", err)
	}
	slashedState := mustStakeState(t, nextState, validators[slashedIndex].AccountAddress)
	if slashedState.Status != stake.ValidatorStatusJailed || slashedState.JailUntilEpoch != DefaultMaliciousJailEpochs {
		t.Fatalf("slashed state = %+v", slashedState)
	}
	if slashedState.LastSlashedSlot != evidenceSlot {
		t.Fatalf("last slashed slot = %d, want %d", slashedState.LastSlashedSlot, evidenceSlot)
	}
	after := mustFindAccount(t, nextState, validators[slashedIndex].AccountAddress).Account.Lamports
	if after >= before {
		t.Fatalf("validator was not slashed: before=%d after=%d", before, after)
	}
	if !containsRewardType(rewards, RewardTypeSlash) || !containsRewardType(rewards, RewardTypeJail) {
		t.Fatalf("rewards = %+v, want slash and jail", rewards)
	}
}

func newRewardTestState(t *testing.T, stakeWeights []uint64, commissions []uint16) (ChainState, EpochSnapshot, []ValidatorState) {
	t.Helper()
	accounts := make([]structure.AddressedAccount, 0, len(stakeWeights)*2+2)
	validators := make([]ValidatorState, len(stakeWeights))
	for index, weight := range stakeWeights {
		staker := mustKeyPair(t, "reward-staker-"+string(rune('a'+index)))
		validator := mustKeyPair(t, "reward-validator-"+string(rune('a'+index)))
		consensusKey := mustKeyPair(t, "reward-consensus-"+string(rune('a'+index)))
		stakeLamports := weight * stake.MinimumStakeLamports
		accounts = append(accounts, newTestAccount(t, staker.PublicKey, 2_000_000_000, structure.DefaultBuiltinProgramIDs.System, false, nil))
		accounts = append(accounts, newStakeAccount(t, validator.PublicKey, staker.PublicKey, consensusKey.PublicKey, stakeLamports, commissions[index]))
		validators[index] = ValidatorState{
			AccountAddress:     validator.PublicKey,
			ConsensusPublicKey: consensusKey.PublicKey,
			P2PPeerID:          "reward-peer",
			StakeLamports:      stakeLamports,
			Status:             ValidatorStatusActive,
			CommissionBps:      commissions[index],
		}
	}
	set, err := NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	snapshot := mustEpochSnapshot(t, 0, 1, 64, mustHashFromText(t, "reward-snapshot"), set)
	return ChainState{Accounts: accounts}, snapshot, snapshot.Validators
}

func newStakeAccount(
	t *testing.T,
	address structure.PublicKey,
	staker structure.PublicKey,
	consensusKey structure.PublicKey,
	lamports uint64,
	commissionBps uint16,
) structure.AddressedAccount {
	t.Helper()
	state := stake.ValidatorState{
		ConsensusPublicKey: consensusKey,
		StakerAccount:      staker,
		P2PPeerID:          "reward-peer",
		CommissionBps:      commissionBps,
		ActiveStake:        lamports,
		Status:             stake.ValidatorStatusActive,
		LastEffectiveStake: lamports,
	}
	data, err := state.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal stake state: %v", err)
	}
	accountLamports := lamports + 10_000_000
	return newTestAccount(t, address, accountLamports, structure.DefaultBuiltinProgramIDs.Stake, false, data)
}

func testRewardQC(snapshot EpochSnapshot, slot uint64, height uint64, voterIndexes []int) QuorumCertificate {
	voters := make([]string, len(voterIndexes))
	var confirmedStake uint64
	for index, voterIndex := range voterIndexes {
		validator := snapshot.Validators[voterIndex]
		voters[index] = string(validator.ValidatorID)
		confirmedStake += validator.StakeLamports
	}
	return QuorumCertificate{
		Type:               VoteTypeConfirm,
		Slot:               slot,
		BlockHeight:        height,
		BlockHash:          mustHashFromTestText("reward-block"),
		ThresholdStake:     (snapshot.TotalActiveStake*2 + 2) / 3,
		ConfirmedStake:     confirmedStake,
		Voters:             voters,
		CreatedAtUnixMilli: 1710000000000,
	}
}

func mustHashFromTestText(text string) structure.Hash {
	hash, err := structure.NewHash([]byte(text + "............................")[:structure.HashSize])
	if err != nil {
		panic(err)
	}
	return hash
}

func mustStakeState(t *testing.T, state ChainState, address structure.PublicKey) stake.ValidatorState {
	t.Helper()
	account := mustFindAccount(t, state, address)
	stakeState, err := stake.UnmarshalValidatorStateBinary(account.Account.Data)
	if err != nil {
		t.Fatalf("unmarshal stake state: %v", err)
	}
	return stakeState
}

func replaceStakeState(
	t *testing.T,
	state ChainState,
	address structure.PublicKey,
	stakeState stake.ValidatorState,
) ChainState {
	t.Helper()
	data, err := stakeState.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal stake state: %v", err)
	}
	nextState := state.clone()
	for index, account := range nextState.Accounts {
		if account.Address != address {
			continue
		}
		nextAccount := account.Account.Clone()
		nextAccount.Data = append([]byte(nil), data...)
		nextState.Accounts[index] = structure.AddressedAccount{Address: address, Account: nextAccount}
		return nextState
	}
	t.Fatalf("stake account %s not found", address.String())
	return ChainState{}
}

func signedTestVote(t *testing.T, keyPair structure.SolanaKeyPair, vote Vote) SignedVote {
	t.Helper()
	voteBytes, err := vote.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal vote: %v", err)
	}
	signature, err := keyPair.Sign(voteBytes)
	if err != nil {
		t.Fatalf("sign vote: %v", err)
	}
	return SignedVote{
		Vote:      vote,
		PublicKey: keyPair.PublicKey,
		Signature: signature,
	}
}

func rewardConsensusKeyForValidator(
	t *testing.T,
	validator ValidatorState,
	validatorCount int,
) structure.SolanaKeyPair {
	t.Helper()
	for index := 0; index < validatorCount; index++ {
		keyPair := mustKeyPair(t, "reward-consensus-"+string(rune('a'+index)))
		if keyPair.PublicKey == validator.ConsensusPublicKey {
			return keyPair
		}
	}
	t.Fatalf("consensus key for validator %s not found", validator.ValidatorID)
	return structure.SolanaKeyPair{}
}

func containsRewardType(rewards []BlockReward, rewardType RewardType) bool {
	for _, reward := range rewards {
		if reward.Type == rewardType {
			return true
		}
	}
	return false
}

func countRewardType(rewards []BlockReward, rewardType RewardType) int {
	count := 0
	for _, reward := range rewards {
		if reward.Type == rewardType {
			count++
		}
	}
	return count
}

func firstRewardType(rewards []BlockReward, rewardType RewardType) BlockReward {
	for _, reward := range rewards {
		if reward.Type == rewardType {
			return reward
		}
	}
	return BlockReward{}
}

func highestStakeValidatorIndex(snapshot EpochSnapshot) int {
	bestIndex := 0
	for index, validator := range snapshot.Validators {
		if validator.StakeLamports > snapshot.Validators[bestIndex].StakeLamports {
			bestIndex = index
		}
	}
	return bestIndex
}

func lowestStakeValidatorIndex(snapshot EpochSnapshot) int {
	bestIndex := 0
	for index, validator := range snapshot.Validators {
		if validator.StakeLamports < snapshot.Validators[bestIndex].StakeLamports {
			bestIndex = index
		}
	}
	return bestIndex
}
