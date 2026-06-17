package consensus

import (
	"context"
	"testing"

	"solana_golang/programs/stake"
	"solana_golang/runtime"
	"solana_golang/structure"
)

func TestStageBusinessValidatorRegisterActivatesAndWeightsNextEpoch(t *testing.T) {
	fixture := newPoSTestFixture(t)
	executor := newPoSTestExecutor(t)
	bootstrapSet := fixture.validatorSetFromIndexes(t, 0)
	epochZero := mustEpochSnapshot(t, 0, 1, 32, mustHashFromText(t, "stage-register-seed"), bootstrapSet)
	scheduleZero := mustLeaderSchedule(t, epochZero)
	leaderID, err := scheduleZero.LeaderForSlot(1)
	if err != nil {
		t.Fatalf("leader for slot: %v", err)
	}

	instruction, err := stake.NewRegisterValidatorInstruction(fixture.consensusKeys[1].PublicKey, "stage-peer-1", 0, fixture.stakes[1])
	if err != nil {
		t.Fatalf("register instruction: %v", err)
	}
	transaction := fixture.signStakeTransaction(t, 1, instruction)
	proposal, registeredState, err := (BlockProducer{ChainID: "stage-business", Executor: executor}).ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           1,
		Height:         1,
		EpochSnapshot:  epochZero,
		Schedule:       scheduleZero,
		ParentHash:     mustHashFromText(t, "stage-register-parent"),
		PreviousQCHash: mustHashFromText(t, "stage-register-qc"),
		ParentState:    fixture.state,
		Transactions:   []structure.Transaction{transaction},
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, leaderID),
	})
	if err != nil {
		t.Fatalf("produce register block: %v", err)
	}

	verifiedState, err := (ProposalVerifier{ChainID: "stage-business", Executor: executor}).VerifyProposal(context.Background(), VerifyProposalRequest{
		Proposal:       proposal,
		EpochSnapshot:  epochZero,
		Schedule:       scheduleZero,
		ParentHash:     mustHashFromText(t, "stage-register-parent"),
		ParentState:    fixture.state,
		BlockhashQueue: fixture.blockhashQueue,
		Leader:         fixture.validatorByID(t, epochZero, leaderID),
	})
	if err != nil {
		t.Fatalf("verify register block: %v", err)
	}
	assertSameStateRoot(t, registeredState, verifiedState)

	validatorAccount := mustFindAccount(t, verifiedState, fixture.validatorKeys[1].PublicKey)
	stakeState, err := stake.UnmarshalValidatorStateBinary(validatorAccount.Account.Data)
	if err != nil {
		t.Fatalf("decode stake state: %v", err)
	}
	if stakeState.PendingStake != fixture.stakes[1] || stakeState.ActiveStake != 0 || stakeState.ActivationEpoch != 1 {
		t.Fatalf("stake state after register = %+v", stakeState)
	}
	currentStake, err := stake.EffectiveStakeAtEpoch(stakeState, 0)
	if err != nil {
		t.Fatalf("current effective stake: %v", err)
	}
	nextStake, err := stake.EffectiveStakeAtEpoch(stakeState, 1)
	if err != nil {
		t.Fatalf("next effective stake: %v", err)
	}
	if currentStake != 0 || nextStake != fixture.stakes[1] {
		t.Fatalf("effective stake current=%d next=%d want 0/%d", currentStake, nextStake, fixture.stakes[1])
	}
	nextSet := fixture.validatorSetFromRegisteredStakeAccounts(t, verifiedState, 1)
	nextSnapshot := mustEpochSnapshot(t, 1, 33, 32, proposalHashOrFail(t, proposal), nextSet)
	if nextSnapshot.TotalActiveStake != fixture.stakes[1] {
		t.Fatalf("total active stake = %d, want %d", nextSnapshot.TotalActiveStake, fixture.stakes[1])
	}
	if len(nextSnapshot.Validators) != 1 {
		t.Fatalf("validator count = %d, want 1", len(nextSnapshot.Validators))
	}
}

func TestStageBusinessUnstakeWithdrawAndRestakeLifecycle(t *testing.T) {
	fixture := newPoSTestFixture(t)
	executor := newPoSTestExecutor(t)
	state, parentHash := stageRegisteredState(t, fixture, executor)
	epochOne := mustEpochSnapshot(t, 1, 33, 32, parentHash, fixture.validatorSetFromStakeAccounts(t, state, 1))
	scheduleOne := mustLeaderSchedule(t, epochOne)
	leaderID, err := scheduleOne.LeaderForSlot(33)
	if err != nil {
		t.Fatalf("leader for unstake slot: %v", err)
	}
	amount := stake.MinimumStakeLamports
	unstakeInstruction, err := stake.NewUnstakeInstruction(amount, 2)
	if err != nil {
		t.Fatalf("unstake instruction: %v", err)
	}
	unstakeTransaction := fixture.signStakeTransaction(t, 0, unstakeInstruction)
	unstakeProposal, unstakedState, err := (BlockProducer{ChainID: "stage-business", Executor: executor}).ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           33,
		Height:         2,
		EpochSnapshot:  epochOne,
		Schedule:       scheduleOne,
		ParentHash:     parentHash,
		PreviousQCHash: mustHashFromText(t, "stage-unstake-qc"),
		ParentState:    state,
		Transactions:   []structure.Transaction{unstakeTransaction},
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, leaderID),
	})
	if err != nil {
		t.Fatalf("produce unstake block: %v", err)
	}
	unstaked := mustStakeState(t, unstakedState, fixture.validatorKeys[0].PublicKey)
	if unstaked.UnlockingStake != amount || unstaked.DeactivationEpoch != 2 {
		t.Fatalf("unstaked state = %+v", unstaked)
	}
	effectiveNow, err := stake.EffectiveStakeAtEpoch(unstaked, 1)
	if err != nil {
		t.Fatalf("effective now: %v", err)
	}
	effectiveNext, err := stake.EffectiveStakeAtEpoch(unstaked, 2)
	if err != nil {
		t.Fatalf("effective next: %v", err)
	}
	if effectiveNow != fixture.stakes[0] || effectiveNext != fixture.stakes[0]-amount {
		t.Fatalf("effective stake now=%d next=%d", effectiveNow, effectiveNext)
	}

	stakerBeforeWithdraw := mustFindAccount(t, unstakedState, fixture.stakerKeys[0].PublicKey).Account.Lamports
	withdrawInstruction, err := stake.NewWithdrawUnstakedInstruction(2)
	if err != nil {
		t.Fatalf("withdraw instruction: %v", err)
	}
	withdrawParentHash := proposalHashOrFail(t, unstakeProposal)
	epochTwo := mustEpochSnapshot(t, 2, 65, 32, withdrawParentHash, fixture.validatorSetFromStakeAccounts(t, unstakedState, 2))
	scheduleTwo := mustLeaderSchedule(t, epochTwo)
	withdrawLeaderID, err := scheduleTwo.LeaderForSlot(65)
	if err != nil {
		t.Fatalf("leader for withdraw slot: %v", err)
	}
	withdrawProposal, withdrawnState, err := (BlockProducer{ChainID: "stage-business", Executor: executor}).ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           65,
		Height:         3,
		EpochSnapshot:  epochTwo,
		Schedule:       scheduleTwo,
		ParentHash:     withdrawParentHash,
		PreviousQCHash: mustHashFromText(t, "stage-withdraw-qc"),
		ParentState:    unstakedState,
		Transactions:   []structure.Transaction{fixture.signStakeTransaction(t, 0, withdrawInstruction)},
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, withdrawLeaderID),
	})
	if err != nil {
		t.Fatalf("produce withdraw block: %v", err)
	}
	stakerAfterWithdraw := mustFindAccount(t, withdrawnState, fixture.stakerKeys[0].PublicKey).Account.Lamports
	expectedWithdrawDelta := amount - structure.DefaultFeeCalculator().LamportsPerSignature
	if stakerAfterWithdraw != stakerBeforeWithdraw+expectedWithdrawDelta {
		t.Fatalf("withdraw delta = %d, want %d", stakerAfterWithdraw-stakerBeforeWithdraw, expectedWithdrawDelta)
	}
	withdrawn := mustStakeState(t, withdrawnState, fixture.validatorKeys[0].PublicKey)
	if withdrawn.UnlockingStake != 0 {
		t.Fatalf("unlocking stake after withdraw = %d, want 0", withdrawn.UnlockingStake)
	}

	restakeInstruction, err := stake.NewStakeInstruction(amount)
	if err != nil {
		t.Fatalf("restake instruction: %v", err)
	}
	restakeParentHash := proposalHashOrFail(t, withdrawProposal)
	restakeLeaderID, err := scheduleTwo.LeaderForSlot(66)
	if err != nil {
		t.Fatalf("leader for restake slot: %v", err)
	}
	restakeProposal, restakedState, err := (BlockProducer{ChainID: "stage-business", Executor: executor}).ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           66,
		Height:         4,
		EpochSnapshot:  epochTwo,
		Schedule:       scheduleTwo,
		ParentHash:     restakeParentHash,
		PreviousQCHash: mustHashFromText(t, "stage-restake-qc"),
		ParentState:    withdrawnState,
		Transactions:   []structure.Transaction{fixture.signStakeTransaction(t, 0, restakeInstruction)},
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, restakeLeaderID),
	})
	if err != nil {
		t.Fatalf("produce restake block: %v", err)
	}
	_ = restakeProposal
	restaked := mustStakeState(t, restakedState, fixture.validatorKeys[0].PublicKey)
	if restaked.PendingStake != amount || restaked.ActivationEpoch != 3 {
		t.Fatalf("restaked state = %+v", restaked)
	}
	effectiveAtEpochThree, err := stake.EffectiveStakeAtEpoch(restaked, 3)
	if err != nil {
		t.Fatalf("effective restake: %v", err)
	}
	if effectiveAtEpochThree != fixture.stakes[0] {
		t.Fatalf("effective at epoch 3 = %d, want %d", effectiveAtEpochThree, fixture.stakes[0])
	}
}

func TestStageBusinessProposalRewardTamperingIsRejected(t *testing.T) {
	fixture := newPoSTestFixture(t)
	executor := newPoSTestExecutor(t)
	state, parentHash := stageRegisteredState(t, fixture, executor)
	epochOne := mustEpochSnapshot(t, 1, 33, 32, parentHash, fixture.validatorSetFromStakeAccounts(t, state, 1))
	scheduleOne := mustLeaderSchedule(t, epochOne)
	leaderID, err := scheduleOne.LeaderForSlot(35)
	if err != nil {
		t.Fatalf("leader for reward slot: %v", err)
	}
	rewardQC := collectQC(t, epochOne, VoteTypeConfirm, 33, 2, mustHashFromText(t, "stage-finalized-block"))
	proposal, _, err := (BlockProducer{ChainID: "stage-business", Executor: executor}).ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           35,
		Height:         5,
		EpochSnapshot:  epochOne,
		Schedule:       scheduleOne,
		ParentHash:     parentHash,
		PreviousQCHash: mustHashFromText(t, "stage-reward-qc"),
		ParentState:    state,
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, leaderID),
		RewardQCs:      []QuorumCertificate{rewardQC},
	})
	if err != nil {
		t.Fatalf("produce reward block: %v", err)
	}
	if len(proposal.Rewards) == 0 {
		t.Fatal("reward proposal has no rewards")
	}
	proposal.Rewards = proposal.Rewards[:len(proposal.Rewards)-1]
	if _, err := (ProposalVerifier{ChainID: "stage-business", Executor: executor}).VerifyProposal(context.Background(), VerifyProposalRequest{
		Proposal:       proposal,
		EpochSnapshot:  epochOne,
		Schedule:       scheduleOne,
		ParentHash:     parentHash,
		ParentState:    state,
		BlockhashQueue: fixture.blockhashQueue,
		Leader:         fixture.validatorByID(t, epochOne, leaderID),
	}); err == nil {
		t.Fatal("VerifyProposal() error = nil, want reward tamper rejection")
	}
}

func TestAcceptedBlockResultIsDeterministicAcrossValidators(t *testing.T) {
	fixture := newPoSTestFixture(t)
	executor := newPoSTestExecutor(t)
	chainID := "accepted-determinism"
	registeredState, registeredHash := stageRegisteredState(t, fixture, executor)
	epochOne := mustEpochSnapshot(t, 1, 33, 32, registeredHash, fixture.validatorSetFromStakeAccounts(t, registeredState, 1))
	scheduleOne := mustLeaderSchedule(t, epochOne)
	producer := BlockProducer{ChainID: chainID, Executor: executor}

	block33, state33, err := produceDeterministicEmptyBlock(
		t,
		producer,
		fixture,
		epochOne,
		scheduleOne,
		33,
		1,
		2,
		registeredHash,
		registeredState,
	)
	if err != nil {
		t.Fatalf("produce block 33: %v", err)
	}
	block33Hash := proposalHashOrFail(t, block33)
	block34, state34, err := produceDeterministicEmptyBlock(
		t,
		producer,
		fixture,
		epochOne,
		scheduleOne,
		34,
		33,
		3,
		block33Hash,
		state33,
	)
	if err != nil {
		t.Fatalf("produce block 34: %v", err)
	}
	block34Hash := proposalHashOrFail(t, block34)
	slot := uint64(36)
	leaderID, err := scheduleOne.LeaderForSlot(slot)
	if err != nil {
		t.Fatalf("leader for accepted block: %v", err)
	}
	leader := fixture.validatorByID(t, epochOne, leaderID)
	slashedValidator := epochOne.Validators[lowestStakeValidatorIndex(epochOne)]
	slashedKeyPair := fixture.consensusKeyPairByID(t, slashedValidator.ValidatorID)
	evidence := SlashingEvidence{
		Type: SlashingEvidenceTypeDoubleVote,
		DoubleVote: &SignedDoubleVoteEvidence{
			FirstVote: signedTestVote(t, slashedKeyPair, Vote{
				Type:               VoteTypeConfirm,
				Slot:               35,
				BlockHeight:        4,
				BlockHash:          mustHashFromText(t, "accepted-double-vote-a"),
				VoterID:            string(slashedValidator.ValidatorID),
				Stake:              slashedValidator.StakeLamports,
				CreatedAtUnixMilli: 1710000000001,
			}),
			SecondVote: signedTestVote(t, slashedKeyPair, Vote{
				Type:               VoteTypeConfirm,
				Slot:               35,
				BlockHeight:        4,
				BlockHash:          mustHashFromText(t, "accepted-double-vote-b"),
				VoterID:            string(slashedValidator.ValidatorID),
				Stake:              slashedValidator.StakeLamports,
				CreatedAtUnixMilli: 1710000000002,
			}),
		},
	}
	rewardQC := collectQC(t, epochOne, VoteTypeConfirm, 33, 2, block33Hash)
	proposal, producedState, err := producer.ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           slot,
		ParentSlot:     34,
		Height:         4,
		EpochSnapshot:  epochOne,
		Schedule:       scheduleOne,
		ParentHash:     block34Hash,
		PreviousQCHash: mustHashFromText(t, "accepted-determinism-qc"),
		ParentState:    state34,
		Transactions:   []structure.Transaction{fixture.transferTransaction(t, 1_000_000)},
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, leaderID),
		RewardQCs:      []QuorumCertificate{rewardQC},
		Evidence:       []SlashingEvidence{evidence},
	})
	if err != nil {
		t.Fatalf("produce accepted block: %v", err)
	}
	producedRoot, err := producedState.RootHash()
	if err != nil {
		t.Fatalf("produced state root: %v", err)
	}
	rewardRoot, err := HashBlockRewards(proposal.Rewards)
	if err != nil {
		t.Fatalf("reward root: %v", err)
	}
	if proposal.Header.StateRoot != producedRoot || proposal.Header.AccountRoot != producedRoot {
		t.Fatalf("proposal state root = %s/%s, want %s", proposal.Header.StateRoot.String(), proposal.Header.AccountRoot.String(), producedRoot.String())
	}
	if proposal.Header.RewardRoot != rewardRoot {
		t.Fatalf("proposal reward root = %s, want %s", proposal.Header.RewardRoot.String(), rewardRoot.String())
	}
	if len(proposal.Transactions) != 1 ||
		!containsRewardType(proposal.Rewards, RewardTypeVoteCredit) ||
		!containsRewardType(proposal.Rewards, RewardTypeMissedProposal) ||
		!containsRewardType(proposal.Rewards, RewardTypeSlash) {
		t.Fatalf("proposal rewards/transactions not covering deterministic paths: tx=%d rewards=%+v", len(proposal.Transactions), proposal.Rewards)
	}

	for _, validator := range epochOne.Validators {
		verifiedState, err := (ProposalVerifier{ChainID: chainID, Executor: newPoSTestExecutor(t)}).VerifyProposal(context.Background(), VerifyProposalRequest{
			Proposal:       proposal,
			ParentSlot:     34,
			EpochSnapshot:  epochOne,
			Schedule:       scheduleOne,
			ParentHash:     block34Hash,
			ParentState:    state34.clone(),
			BlockhashQueue: fixture.blockhashQueue,
			Leader:         leader,
		})
		if err != nil {
			t.Fatalf("validator %s verify accepted block: %v", validator.ValidatorID, err)
		}
		verifiedRoot, err := verifiedState.RootHash()
		if err != nil {
			t.Fatalf("validator %s state root: %v", validator.ValidatorID, err)
		}
		if verifiedRoot != producedRoot {
			t.Fatalf("validator %s state root = %s, want %s", validator.ValidatorID, verifiedRoot.String(), producedRoot.String())
		}
		assertSameStateRoot(t, producedState, verifiedState)
	}
}

func TestStageBusinessDuplicateRewardQCIsRejected(t *testing.T) {
	fixture := newPoSTestFixture(t)
	state, parentHash := stageRegisteredState(t, fixture, newPoSTestExecutor(t))
	epochOne := mustEpochSnapshot(t, 1, 33, 32, parentHash, fixture.validatorSetFromStakeAccounts(t, state, 1))
	qc := collectQC(t, epochOne, VoteTypeConfirm, 33, 2, mustHashFromText(t, "stage-duplicate-qc"))
	leader := epochOne.Validators[0]
	if _, _, err := ApplyBlockRewards(state, BlockRewardInput{
		Slot:          35,
		Height:        5,
		EpochID:       1,
		EpochSnapshot: epochOne,
		Leader:        leader,
		RewardQCs:     []QuorumCertificate{qc, qc},
	}); err == nil {
		t.Fatal("ApplyBlockRewards() error = nil, want duplicate reward qc rejection")
	}
}

func TestStageBusinessMissedVotesJailAndExcludeValidator(t *testing.T) {
	state, snapshot, validators := newRewardTestState(t, []uint64{90, 10}, []uint16{0, 0})
	voterIndex := highestStakeValidatorIndex(snapshot)
	missedIndex := lowestStakeValidatorIndex(snapshot)
	qcs := make([]QuorumCertificate, DefaultMissedVoteJailThreshold)
	for index := range qcs {
		slot := uint64(index + 1)
		qcs[index] = testRewardQC(snapshot, slot, slot, []int{voterIndex})
	}
	nextState, _, err := ApplyBlockRewards(state, BlockRewardInput{
		Slot:          32,
		Height:        64,
		EpochID:       1,
		EpochSnapshot: snapshot,
		Leader:        validators[voterIndex],
		RewardQCs:     qcs,
		Config: RewardConfig{
			MinActiveValidatorsAfterPerformanceJail: 1,
		},
	})
	if err != nil {
		t.Fatalf("ApplyBlockRewards() error = %v", err)
	}
	jailed := mustStakeState(t, nextState, validators[missedIndex].AccountAddress)
	effectiveStake, err := stake.EffectiveStakeAtEpoch(jailed, 1)
	if err != nil {
		t.Fatalf("EffectiveStakeAtEpoch() error = %v", err)
	}
	if jailed.Status != stake.ValidatorStatusJailed || effectiveStake != 0 {
		t.Fatalf("jailed status=%d effective=%d", jailed.Status, effectiveStake)
	}
}

func stageRegisteredState(t *testing.T, fixture posTestFixture, executor runtime.TransactionExecutor) (ChainState, structure.Hash) {
	t.Helper()
	epochZero := mustEpochSnapshot(t, 0, 1, 32, mustHashFromText(t, "stage-register-all-seed"), fixture.validatorSetFromGenesis(t))
	scheduleZero := mustLeaderSchedule(t, epochZero)
	leaderID, err := scheduleZero.LeaderForSlot(1)
	if err != nil {
		t.Fatalf("leader for register-all slot: %v", err)
	}
	proposal, state, err := (BlockProducer{ChainID: "stage-business", Executor: executor}).ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           1,
		Height:         1,
		EpochSnapshot:  epochZero,
		Schedule:       scheduleZero,
		ParentHash:     mustHashFromText(t, "stage-register-all-parent"),
		PreviousQCHash: mustHashFromText(t, "stage-register-all-qc"),
		ParentState:    fixture.state,
		Transactions:   fixture.registerTransactions(t),
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, leaderID),
	})
	if err != nil {
		t.Fatalf("produce register-all block: %v", err)
	}
	return state, proposalHashOrFail(t, proposal)
}

func produceDeterministicEmptyBlock(
	t *testing.T,
	producer BlockProducer,
	fixture posTestFixture,
	snapshot EpochSnapshot,
	schedule LeaderSchedule,
	slot uint64,
	parentSlot uint64,
	height uint64,
	parentHash structure.Hash,
	parentState ChainState,
) (BlockProposal, ChainState, error) {
	t.Helper()
	leaderID, err := schedule.LeaderForSlot(slot)
	if err != nil {
		return BlockProposal{}, ChainState{}, err
	}
	return producer.ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           slot,
		ParentSlot:     parentSlot,
		Height:         height,
		EpochSnapshot:  snapshot,
		Schedule:       schedule,
		ParentHash:     parentHash,
		PreviousQCHash: mustHashFromText(t, "deterministic-empty-qc"),
		ParentState:    parentState,
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, leaderID),
	})
}

func proposalHashOrFail(t *testing.T, proposal BlockProposal) structure.Hash {
	t.Helper()
	hash, err := proposal.Hash()
	if err != nil {
		t.Fatalf("proposal hash: %v", err)
	}
	return hash
}
