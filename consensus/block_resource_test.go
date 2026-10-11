package consensus

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	runtimepkg "github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestBlockProducerStopsBeforeEstimatedResourceLimit(t *testing.T) {
	leaderKeyPair, snapshot, schedule, leader := blockResourceConsensusFixture(t)
	targetAddress := parallelTestPublicKey(180)
	transactionCount := int(MaxBlockComputeUnits/structure.DefaultContractCallComputeUnits) + 1
	transactions := blockResourceTestTransactions(transactionCount, targetAddress)
	executor := &blockResourceTestExecutor{}

	proposal, state, err := (BlockProducer{ChainID: "block-resource-test", Executor: executor}).ProduceBlock(
		context.Background(),
		ProduceBlockRequest{
			Slot:           1,
			Height:         1,
			EpochSnapshot:  snapshot,
			Schedule:       schedule,
			ParentHash:     parallelTestHash(90),
			PreviousQCHash: parallelTestHash(91),
			ParentState:    blockResourceTestState(targetAddress),
			Transactions:   transactions,
			LeaderKeyPair:  leaderKeyPair,
		},
	)
	if err != nil {
		t.Fatalf("ProduceBlock() error = %v", err)
	}
	expectedIncludedCount := transactionCount - 1
	if len(proposal.Transactions) != expectedIncludedCount {
		t.Fatalf("proposal transactions = %d, want %d", len(proposal.Transactions), expectedIncludedCount)
	}
	if calls := int(executor.calls.Load()); calls != expectedIncludedCount {
		t.Fatalf("executor calls = %d, want %d", calls, expectedIncludedCount)
	}
	assertParallelTestLamports(t, state, targetAddress, blockResourceTestInitialLamports+uint64(expectedIncludedCount))
	if proposal.Header.LeaderID != leader.ValidatorID {
		t.Fatalf("proposal leader = %s, want %s", proposal.Header.LeaderID, leader.ValidatorID)
	}
}

func TestProposalVerifierRejectsEstimatedResourceLimitBeforeExecution(t *testing.T) {
	leaderKeyPair, snapshot, schedule, leader := blockResourceConsensusFixture(t)
	parentHash := parallelTestHash(92)
	targetAddress := parallelTestPublicKey(181)
	transactionCount := int(MaxBlockComputeUnits/structure.DefaultContractCallComputeUnits) + 1
	header := BlockHeader{
		ChainID:           "block-resource-test",
		Slot:              1,
		Height:            1,
		ParentHash:        parentHash,
		LeaderID:          leader.ValidatorID,
		EpochID:           snapshot.EpochID,
		EpochSnapshotRoot: snapshot.SnapshotStateRoot,
	}
	signBytes, err := header.SignBytes()
	if err != nil {
		t.Fatalf("header SignBytes() error = %v", err)
	}
	leaderSignature, err := leaderKeyPair.Sign(signBytes)
	if err != nil {
		t.Fatalf("leader Sign() error = %v", err)
	}
	executor := &blockResourceTestExecutor{}
	proposal := BlockProposal{
		Header:          header,
		Transactions:    blockResourceTestTransactions(transactionCount, targetAddress),
		LeaderSignature: leaderSignature,
	}

	_, err = (ProposalVerifier{ChainID: "block-resource-test", Executor: executor}).VerifyProposal(
		context.Background(),
		VerifyProposalRequest{
			Proposal:      proposal,
			EpochSnapshot: snapshot,
			Schedule:      schedule,
			ParentHash:    parentHash,
			ParentState:   blockResourceTestState(targetAddress),
			Leader:        leader,
		},
	)
	if err == nil || !strings.Contains(err.Error(), "proposal resource demand exceeds block limit") {
		t.Fatalf("VerifyProposal() error = %v, want block resource limit", err)
	}
	if calls := executor.calls.Load(); calls != 0 {
		t.Fatalf("executor calls = %d, want zero", calls)
	}
}

func TestBlockProducerRejectsActualResourceLimit(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		computeUnits uint64
		costUnits    uint64
	}{
		{name: "compute", computeUnits: structure.MaxComputeUnitsPerTransaction},
		{name: "cost", costUnits: structure.MaxComputeUnitsPerTransaction},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			leaderKeyPair, snapshot, schedule, _ := blockResourceConsensusFixture(t)
			targetAddress := parallelTestPublicKey(182)
			transactionCount := int(MaxBlockComputeUnits/structure.MaxComputeUnitsPerTransaction) + 1
			executor := &blockResourceTestExecutor{
				computeUnits: testCase.computeUnits,
				costUnits:    testCase.costUnits,
			}

			_, _, err := (BlockProducer{ChainID: "block-resource-test", Executor: executor}).ProduceBlock(
				context.Background(),
				ProduceBlockRequest{
					Slot:           1,
					Height:         1,
					EpochSnapshot:  snapshot,
					Schedule:       schedule,
					ParentHash:     parallelTestHash(93),
					PreviousQCHash: parallelTestHash(94),
					ParentState:    blockResourceTestState(targetAddress),
					Transactions:   blockResourceTestTransactions(transactionCount, targetAddress),
					LeaderKeyPair:  leaderKeyPair,
				},
			)
			if err == nil || !strings.Contains(err.Error(), "actual block resources exceed limit") {
				t.Fatalf("ProduceBlock() error = %v, want actual resource limit", err)
			}
		})
	}
}

type blockResourceTestExecutor struct {
	calls        atomic.Uint64
	computeUnits uint64
	costUnits    uint64
}

func (executor *blockResourceTestExecutor) ExecuteTransaction(
	_ context.Context,
	request runtimepkg.TransactionRequest,
) (runtimepkg.TransactionResult, error) {
	executor.calls.Add(1)
	targetAddress, err := parallelTestTargetAddress(request.Simulation.Transaction)
	if err != nil {
		return runtimepkg.TransactionResult{}, err
	}
	account, err := parallelTestAccount(request.Simulation.Accounts, targetAddress)
	if err != nil {
		return runtimepkg.TransactionResult{}, err
	}
	account.Lamports++
	return runtimepkg.TransactionResult{
		Mode: request.Mode,
		Execution: structure.TransactionExecutionResult{
			Status:               structure.TransactionStatusConfirmed,
			ComputeUnitsConsumed: executor.computeUnits,
			CostUnits:            executor.costUnits,
			WrittenAccounts: []structure.AddressedAccount{
				{Address: targetAddress, Account: account},
			},
		},
	}, nil
}

func blockResourceConsensusFixture(t *testing.T) (structure.OrdexusKeyPair, EpochSnapshot, LeaderSchedule, ValidatorState) {
	t.Helper()
	leaderKeyPair := mustKeyPair(t, "block-resource-leader")
	leader := ValidatorState{
		ValidatorID:        NewValidatorID(leaderKeyPair.PublicKey),
		AccountAddress:     parallelTestPublicKey(183),
		ConsensusPublicKey: leaderKeyPair.PublicKey,
		StakeLamports:      1,
		Status:             ValidatorStatusActive,
	}
	validatorSet, err := NewValidatorSet([]ValidatorState{leader})
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	snapshot := mustEpochSnapshot(t, 0, 1, 1, parallelTestHash(95), validatorSet)
	schedule := mustLeaderSchedule(t, snapshot)
	return leaderKeyPair, snapshot, schedule, snapshot.Validators[0]
}

func blockResourceTestTransactions(count int, targetAddress structure.PublicKey) []structure.Transaction {
	transactions := make([]structure.Transaction, count)
	for transactionIndex := range transactions {
		transactions[transactionIndex] = parallelTestTransaction(byte(transactionIndex+1), targetAddress)
	}
	return transactions
}

const blockResourceTestInitialLamports = uint64(1_000_000)

func blockResourceTestState(targetAddress structure.PublicKey) ChainState {
	return ChainState{Accounts: []structure.AddressedAccount{
		{
			Address: targetAddress,
			Account: structure.Account{
				Lamports: blockResourceTestInitialLamports,
				Owner:    structure.DefaultBuiltinProgramIDs.System,
			},
		},
	}}
}
