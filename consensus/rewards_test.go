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
	if stakerAfter != stakerBefore+900 || validatorAfter != validatorBefore+100 {
		t.Fatalf("reward split staker=%d validator=%d", stakerAfter-stakerBefore, validatorAfter-validatorBefore)
	}
	if len(rewards) == 0 {
		t.Fatal("settlement rewards are empty")
	}
}

func TestApplyBlockRewardsJailsAndSlashesMissedVotes(t *testing.T) {
	state, snapshot, validators := newRewardTestState(t, []uint64{90, 10}, []uint16{0, 0})
	voterIndex := highestStakeValidatorIndex(snapshot)
	missedIndex := lowestStakeValidatorIndex(snapshot)
	qcs := make([]QuorumCertificate, DefaultMissedVoteJailThreshold)
	for index := range qcs {
		slot := uint64(index + 1)
		qcs[index] = testRewardQC(snapshot, slot, slot, []int{voterIndex})
	}
	before := mustFindAccount(t, state, validators[missedIndex].AccountAddress).Account.Lamports

	nextState, rewards, err := ApplyBlockRewards(state, BlockRewardInput{
		Slot:          32,
		Height:        64,
		EpochID:       1,
		EpochSnapshot: snapshot,
		Leader:        validators[voterIndex],
		RewardQCs:     qcs,
	})
	if err != nil {
		t.Fatalf("ApplyBlockRewards() error = %v", err)
	}
	jailedState := mustStakeState(t, nextState, validators[missedIndex].AccountAddress)
	if jailedState.Status != stake.ValidatorStatusJailed || jailedState.JailUntilEpoch != 2 {
		t.Fatalf("jailed state = %+v", jailedState)
	}
	after := mustFindAccount(t, nextState, validators[missedIndex].AccountAddress).Account.Lamports
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

func containsRewardType(rewards []BlockReward, rewardType RewardType) bool {
	for _, reward := range rewards {
		if reward.Type == rewardType {
			return true
		}
	}
	return false
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
