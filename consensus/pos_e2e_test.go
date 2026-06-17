package consensus

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"solana_golang/programs/stake"
	"solana_golang/programs/system"
	"solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
)

func TestPoSRealAccountStakeVoteAndBlockFlow(t *testing.T) {
	fixture := newPoSTestFixture(t)
	executor := newPoSTestExecutor(t)
	genesisSet := fixture.validatorSetFromGenesis(t)
	seed := mustHashFromText(t, "epoch-0-seed")
	epochZero := mustEpochSnapshot(t, 0, 1, 32, seed, genesisSet)
	scheduleZero := mustLeaderSchedule(t, epochZero)
	leaderID, err := scheduleZero.LeaderForSlot(1)
	if err != nil {
		t.Fatalf("leader for slot 1: %v", err)
	}
	leaderKeyPair := fixture.consensusKeyPairByID(t, leaderID)
	registerTransactions := fixture.registerTransactions(t)

	producer := BlockProducer{ChainID: "pos-e2e", Executor: executor}
	proposal, registeredState, err := producer.ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           1,
		Height:         1,
		EpochSnapshot:  epochZero,
		Schedule:       scheduleZero,
		ParentHash:     mustHashFromText(t, "genesis"),
		PreviousQCHash: mustHashFromText(t, "genesis-qc"),
		ParentState:    fixture.state,
		Transactions:   registerTransactions,
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  leaderKeyPair,
	})
	if err != nil {
		t.Fatalf("produce registration block: %v", err)
	}
	if len(proposal.Transactions) != len(registerTransactions) {
		t.Fatalf("registered transactions = %d, want %d", len(proposal.Transactions), len(registerTransactions))
	}

	verifier := ProposalVerifier{ChainID: "pos-e2e", Executor: executor}
	verifiedState, err := verifier.VerifyProposal(context.Background(), VerifyProposalRequest{
		Proposal:       proposal,
		EpochSnapshot:  epochZero,
		Schedule:       scheduleZero,
		ParentHash:     mustHashFromText(t, "genesis"),
		ParentState:    fixture.state,
		BlockhashQueue: fixture.blockhashQueue,
		Leader:         fixture.validatorByID(t, epochZero, leaderID),
	})
	if err != nil {
		t.Fatalf("verify registration block: %v", err)
	}
	assertSameStateRoot(t, registeredState, verifiedState)

	proposalHash, err := proposal.Hash()
	if err != nil {
		t.Fatalf("hash proposal: %v", err)
	}
	confirmQC := collectQC(t, epochZero, VoteTypeConfirm, 1, 1, proposalHash)
	if confirmQC.ThresholdStake != expectedTwoThirdsThreshold(fixture.stakes) || confirmQC.ConfirmedStake < confirmQC.ThresholdStake {
		t.Fatalf("confirm qc = %+v, want two thirds threshold", confirmQC)
	}

	nextSet := fixture.validatorSetFromStakeAccounts(t, verifiedState, 1)
	epochOne := mustEpochSnapshot(t, 1, 33, 32, proposalHash, nextSet)
	scheduleOne := mustLeaderSchedule(t, epochOne)
	transferTransaction := fixture.transferTransaction(t, 5_000_000)
	nextLeaderID, err := scheduleOne.LeaderForSlot(33)
	if err != nil {
		t.Fatalf("leader for slot 33: %v", err)
	}
	nextLeaderKeyPair := fixture.consensusKeyPairByID(t, nextLeaderID)

	transferProposal, transferState, err := producer.ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           33,
		Height:         2,
		EpochSnapshot:  epochOne,
		Schedule:       scheduleOne,
		ParentHash:     proposalHash,
		PreviousQCHash: mustHashFromText(t, "registration-qc"),
		ParentState:    verifiedState,
		Transactions:   []structure.Transaction{transferTransaction},
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  nextLeaderKeyPair,
	})
	if err != nil {
		t.Fatalf("produce transfer block: %v", err)
	}
	if len(transferProposal.Transactions) != 1 {
		t.Fatalf("transfer transactions = %d, want 1", len(transferProposal.Transactions))
	}
	if transferProposal.Transactions[0].Fee != structure.LamportsPerSignature {
		t.Fatalf("transfer transaction fee = %d, want %d", transferProposal.Transactions[0].Fee, structure.LamportsPerSignature)
	}
	if !accountBalanceIncreased(verifiedState, transferState, fixture.stakerKeys[1].PublicKey, 5_000_000) {
		t.Fatalf("destination balance was not increased by transfer amount")
	}

	skipQC := collectQC(t, epochOne, VoteTypeSkip, 34, 0, structure.Hash{})
	if skipQC.Type != VoteTypeSkip || skipQC.ThresholdStake != expectedTwoThirdsThreshold(fixture.stakes) {
		t.Fatalf("skip qc = %+v, want two thirds threshold", skipQC)
	}

	emptyProposal, _, err := producer.ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           33,
		Height:         3,
		EpochSnapshot:  epochOne,
		Schedule:       scheduleOne,
		ParentHash:     proposalHash,
		PreviousQCHash: mustHashFromText(t, "registration-qc-2"),
		ParentState:    verifiedState,
		Transactions:   nil,
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  nextLeaderKeyPair,
	})
	if err != nil {
		t.Fatalf("produce conflicting block: %v", err)
	}
	evidence := DoubleProposalEvidence{FirstProposal: transferProposal, SecondProposal: emptyProposal}
	if err := evidence.Validate(nextLeaderKeyPair.PublicKey); err != nil {
		t.Fatalf("validate double proposal evidence: %v", err)
	}
}

func TestValidatorJoinsOnlyAfterRegisterStakeTransaction(t *testing.T) {
	fixture := newPoSTestFixture(t)
	executor := newPoSTestExecutor(t)
	if count := fixture.registeredValidatorCount(t, fixture.state); count != 0 {
		t.Fatalf("registered validators before transaction = %d, want 0", count)
	}

	bootstrapSet := fixture.validatorSetFromIndexes(t, 0)
	seed := mustHashFromText(t, "bootstrap-seed")
	epochZero := mustEpochSnapshot(t, 0, 1, 32, seed, bootstrapSet)
	scheduleZero := mustLeaderSchedule(t, epochZero)
	leaderID, err := scheduleZero.LeaderForSlot(1)
	if err != nil {
		t.Fatalf("leader for slot 1: %v", err)
	}
	registerInstruction, err := stake.NewRegisterValidatorInstruction(
		fixture.consensusKeys[1].PublicKey,
		"peer-join-1",
		0,
		fixture.stakes[1],
	)
	if err != nil {
		t.Fatalf("new register instruction: %v", err)
	}
	registerTransaction := fixture.signStakeTransaction(t, 1, registerInstruction)
	producer := BlockProducer{ChainID: "pos-e2e", Executor: executor}
	proposal, registeredState, err := producer.ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           1,
		Height:         1,
		EpochSnapshot:  epochZero,
		Schedule:       scheduleZero,
		ParentHash:     mustHashFromText(t, "join-genesis"),
		PreviousQCHash: mustHashFromText(t, "join-genesis-qc"),
		ParentState:    fixture.state,
		Transactions:   []structure.Transaction{registerTransaction},
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, leaderID),
	})
	if err != nil {
		t.Fatalf("produce register block: %v", err)
	}

	verifier := ProposalVerifier{ChainID: "pos-e2e", Executor: executor}
	verifiedState, err := verifier.VerifyProposal(context.Background(), VerifyProposalRequest{
		Proposal:       proposal,
		EpochSnapshot:  epochZero,
		Schedule:       scheduleZero,
		ParentHash:     mustHashFromText(t, "join-genesis"),
		ParentState:    fixture.state,
		BlockhashQueue: fixture.blockhashQueue,
		Leader:         fixture.validatorByID(t, epochZero, leaderID),
	})
	if err != nil {
		t.Fatalf("verify register block: %v", err)
	}
	assertSameStateRoot(t, registeredState, verifiedState)

	currentSet := fixture.validatorSetFromRegisteredStakeAccounts(t, verifiedState, 0)
	if len(currentSet.Validators()) != 0 {
		t.Fatalf("current epoch joined validator count = %d, want 0", len(currentSet.Validators()))
	}
	joinedSet := fixture.validatorSetFromRegisteredStakeAccounts(t, verifiedState, 1)
	joinedValidators := joinedSet.Validators()
	if len(joinedValidators) != 1 {
		t.Fatalf("joined validator count = %d, want 1", len(joinedValidators))
	}
	joinedID := NewValidatorID(fixture.consensusKeys[1].PublicKey)
	if joinedValidators[0].ValidatorID != joinedID {
		t.Fatalf("joined validator = %s, want %s", joinedValidators[0].ValidatorID, joinedID)
	}
	if joinedValidators[0].StakeLamports != fixture.stakes[1] {
		t.Fatalf("joined stake = %d, want %d", joinedValidators[0].StakeLamports, fixture.stakes[1])
	}
}

type posTestFixture struct {
	stakerKeys     []structure.SolanaKeyPair
	validatorKeys  []structure.SolanaKeyPair
	consensusKeys  []structure.SolanaKeyPair
	stakes         []uint64
	state          ChainState
	blockhash      structure.Hash
	blockhashQueue structure.BlockhashQueue
}

func newPoSTestFixture(t *testing.T) posTestFixture {
	t.Helper()
	fixture := posTestFixture{
		stakerKeys:    make([]structure.SolanaKeyPair, 4),
		validatorKeys: make([]structure.SolanaKeyPair, 4),
		consensusKeys: make([]structure.SolanaKeyPair, 4),
		stakes:        []uint64{34, 33, 22, 11},
		blockhash:     mustHashFromText(t, "recent-blockhash"),
	}
	for index := 0; index < 4; index++ {
		fixture.stakerKeys[index] = mustKeyPair(t, fmt.Sprintf("staker-%d", index))
		fixture.validatorKeys[index] = mustKeyPair(t, fmt.Sprintf("validator-%d", index))
		fixture.consensusKeys[index] = mustKeyPair(t, fmt.Sprintf("consensus-%d", index))
		fixture.stakes[index] = fixture.stakes[index] * stake.MinimumStakeLamports
	}
	fixture.blockhashQueue = structure.NewBlockhashQueue(150)
	if err := fixture.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     fixture.blockhash,
		Slot:          1,
		FeeCalculator: structure.DefaultFeeCalculator(),
		TimestampUnix: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("add blockhash: %v", err)
	}
	fixture.state = fixture.initialState(t)
	return fixture
}

func newPoSTestExecutor(t *testing.T) runtime.FixedExecutor {
	t.Helper()
	executor, err := runtime.NewFixedExecutor(
		system.NewProgram(structure.DefaultBuiltinProgramIDs.System),
		stake.NewProgram(structure.DefaultBuiltinProgramIDs.Stake),
	)
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	return executor
}

func (fixture posTestFixture) initialState(t *testing.T) ChainState {
	t.Helper()
	accounts := make([]structure.AddressedAccount, 0, 10)
	for _, staker := range fixture.stakerKeys {
		accounts = append(accounts, newTestAccount(t, staker.PublicKey, 2_000_000_000, structure.DefaultBuiltinProgramIDs.System, false, nil))
	}
	for _, validator := range fixture.validatorKeys {
		minimumBalance, err := structure.MinimumBalanceForRentExemption(0)
		if err != nil {
			t.Fatalf("minimum balance: %v", err)
		}
		accounts = append(accounts, newTestAccount(t, validator.PublicKey, minimumBalance, structure.DefaultBuiltinProgramIDs.Stake, false, nil))
	}
	accounts = append(accounts, newTestAccount(t, structure.DefaultBuiltinProgramIDs.System, 10_000_000, structure.DefaultBuiltinProgramIDs.NativeLoader, true, nil))
	accounts = append(accounts, newTestAccount(t, structure.DefaultBuiltinProgramIDs.Stake, 10_000_000, structure.DefaultBuiltinProgramIDs.NativeLoader, true, nil))
	return ChainState{Accounts: accounts}
}

func (fixture posTestFixture) validatorSetFromGenesis(t *testing.T) ValidatorSet {
	t.Helper()
	validators := make([]ValidatorState, 4)
	for index := range validators {
		validators[index] = ValidatorState{
			AccountAddress:     fixture.validatorKeys[index].PublicKey,
			ConsensusPublicKey: fixture.consensusKeys[index].PublicKey,
			P2PPeerID:          fmt.Sprintf("peer-%d", index),
			StakeLamports:      fixture.stakes[index],
			Status:             ValidatorStatusActive,
		}
	}
	set, err := NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("new genesis validator set: %v", err)
	}
	return set
}

func (fixture posTestFixture) validatorSetFromIndexes(t *testing.T, indexes ...int) ValidatorSet {
	t.Helper()
	validators := make([]ValidatorState, 0, len(indexes))
	for _, index := range indexes {
		validators = append(validators, ValidatorState{
			AccountAddress:     fixture.validatorKeys[index].PublicKey,
			ConsensusPublicKey: fixture.consensusKeys[index].PublicKey,
			P2PPeerID:          fmt.Sprintf("peer-%d", index),
			StakeLamports:      fixture.stakes[index],
			Status:             ValidatorStatusActive,
		})
	}
	set, err := NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("new indexed validator set: %v", err)
	}
	return set
}

func (fixture posTestFixture) validatorSetFromStakeAccounts(t *testing.T, state ChainState, epochID uint64) ValidatorSet {
	t.Helper()
	validators := make([]ValidatorState, 0, len(fixture.validatorKeys))
	for _, validator := range fixture.validatorKeys {
		account := mustFindAccount(t, state, validator.PublicKey)
		stakeState, err := stake.UnmarshalValidatorStateBinary(account.Account.Data)
		if err != nil {
			t.Fatalf("decode stake state: %v", err)
		}
		effectiveStake, err := stake.EffectiveStakeAtEpoch(stakeState, epochID)
		if err != nil {
			t.Fatalf("effective stake: %v", err)
		}
		if effectiveStake == 0 {
			continue
		}
		validators = append(validators, ValidatorState{
			AccountAddress:     validator.PublicKey,
			ConsensusPublicKey: stakeState.ConsensusPublicKey,
			P2PPeerID:          stakeState.P2PPeerID,
			StakeLamports:      effectiveStake,
			Status:             ValidatorStatusActive,
			CommissionBps:      stakeState.CommissionBps,
		})
	}
	if len(validators) == 0 {
		return ValidatorSet{validators: make(map[ValidatorID]ValidatorState)}
	}
	set, err := NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("new stake validator set: %v", err)
	}
	return set
}

func (fixture posTestFixture) validatorSetFromRegisteredStakeAccounts(t *testing.T, state ChainState, epochID uint64) ValidatorSet {
	t.Helper()
	validators := make([]ValidatorState, 0, len(fixture.validatorKeys))
	for _, validator := range fixture.validatorKeys {
		account := mustFindAccount(t, state, validator.PublicKey)
		if len(account.Account.Data) == 0 {
			continue
		}
		stakeState, err := stake.UnmarshalValidatorStateBinary(account.Account.Data)
		if err != nil {
			t.Fatalf("decode stake state: %v", err)
		}
		effectiveStake, err := stake.EffectiveStakeAtEpoch(stakeState, epochID)
		if err != nil {
			t.Fatalf("effective stake: %v", err)
		}
		if effectiveStake == 0 {
			continue
		}
		validators = append(validators, ValidatorState{
			AccountAddress:     validator.PublicKey,
			ConsensusPublicKey: stakeState.ConsensusPublicKey,
			P2PPeerID:          stakeState.P2PPeerID,
			StakeLamports:      effectiveStake,
			Status:             ValidatorStatusActive,
			CommissionBps:      stakeState.CommissionBps,
		})
	}
	if len(validators) == 0 {
		return ValidatorSet{validators: make(map[ValidatorID]ValidatorState)}
	}
	set, err := NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("new registered validator set: %v", err)
	}
	return set
}

func (fixture posTestFixture) registeredValidatorCount(t *testing.T, state ChainState) int {
	t.Helper()
	count := 0
	for _, validator := range fixture.validatorKeys {
		account := mustFindAccount(t, state, validator.PublicKey)
		if len(account.Account.Data) == 0 {
			continue
		}
		if _, err := stake.UnmarshalValidatorStateBinary(account.Account.Data); err != nil {
			t.Fatalf("decode registered validator: %v", err)
		}
		count++
	}
	return count
}

func (fixture posTestFixture) registerTransactions(t *testing.T) []structure.Transaction {
	t.Helper()
	transactions := make([]structure.Transaction, len(fixture.stakerKeys))
	for index := range fixture.stakerKeys {
		instruction, err := stake.NewRegisterValidatorInstruction(
			fixture.consensusKeys[index].PublicKey,
			fmt.Sprintf("peer-%d", index),
			0,
			fixture.stakes[index],
		)
		if err != nil {
			t.Fatalf("new register instruction: %v", err)
		}
		transactions[index] = fixture.signStakeTransaction(t, index, instruction)
	}
	return transactions
}

func (fixture posTestFixture) transferTransaction(t *testing.T, amount uint64) structure.Transaction {
	t.Helper()
	instruction, err := structure.NewTransferInstruction(structure.TransferParams{Lamports: amount})
	if err != nil {
		t.Fatalf("new transfer instruction: %v", err)
	}
	data, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal transfer instruction: %v", err)
	}
	transaction := structure.Transaction{
		Accounts: []structure.AccountMeta{
			{PublicKey: fixture.stakerKeys[0].PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: fixture.stakerKeys[1].PublicKey, IsSigner: false, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
		},
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: 2,
			AccountIndexes: []uint8{0, 1},
			Data:           data,
		}},
		RecentBlockhash: fixture.blockhash,
	}
	signed, err := transaction.Sign(map[structure.PublicKey][]byte{
		fixture.stakerKeys[0].PublicKey: fixture.stakerKeys[0].PrivateKey,
	})
	if err != nil {
		t.Fatalf("sign transfer transaction: %v", err)
	}
	return signed
}

func (fixture posTestFixture) signStakeTransaction(t *testing.T, validatorIndex int, instruction stake.Instruction) structure.Transaction {
	t.Helper()
	data, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal stake instruction: %v", err)
	}
	transaction := structure.Transaction{
		Accounts: []structure.AccountMeta{
			{PublicKey: fixture.stakerKeys[validatorIndex].PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: fixture.validatorKeys[validatorIndex].PublicKey, IsSigner: false, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.Stake, IsSigner: false, IsWritable: false},
		},
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: 2,
			AccountIndexes: []uint8{0, 1},
			Data:           data,
		}},
		RecentBlockhash: fixture.blockhash,
	}
	signed, err := transaction.Sign(map[structure.PublicKey][]byte{
		fixture.stakerKeys[validatorIndex].PublicKey: fixture.stakerKeys[validatorIndex].PrivateKey,
	})
	if err != nil {
		t.Fatalf("sign stake transaction: %v", err)
	}
	return signed
}

func (fixture posTestFixture) consensusKeyPairByID(t *testing.T, validatorID ValidatorID) structure.SolanaKeyPair {
	t.Helper()
	for _, keyPair := range fixture.consensusKeys {
		if NewValidatorID(keyPair.PublicKey) == validatorID {
			return keyPair
		}
	}
	t.Fatalf("consensus key pair not found for %s", validatorID)
	return structure.SolanaKeyPair{}
}

func (fixture posTestFixture) validatorByID(t *testing.T, snapshot EpochSnapshot, validatorID ValidatorID) ValidatorState {
	t.Helper()
	validator, exists := snapshot.ValidatorByID(validatorID)
	if !exists {
		t.Fatalf("validator not found: %s", validatorID)
	}
	return validator
}

func collectQC(t *testing.T, snapshot EpochSnapshot, voteType VoteType, slot uint64, blockHeight uint64, blockHash structure.Hash) QuorumCertificate {
	t.Helper()
	collector, err := NewVoteCollector(snapshot.StakeMap(), Quorum{Numerator: 2, Denominator: 3})
	if err != nil {
		t.Fatalf("new vote collector: %v", err)
	}
	validators := append([]ValidatorState(nil), snapshot.Validators...)
	sort.Slice(validators, func(leftIndex int, rightIndex int) bool {
		return validators[leftIndex].StakeLamports > validators[rightIndex].StakeLamports
	})
	for _, validator := range validators {
		vote := Vote{
			Type:               voteType,
			Slot:               slot,
			BlockHeight:        blockHeight,
			BlockHash:          blockHash,
			VoterID:            string(validator.ValidatorID),
			Stake:              validator.StakeLamports,
			CreatedAtUnixMilli: time.Now().UnixMilli(),
		}
		qc, formed, err := collector.AddVote(vote)
		if err != nil {
			t.Fatalf("add vote: %v", err)
		}
		if formed {
			return qc
		}
	}
	t.Fatalf("qc was not formed")
	return QuorumCertificate{}
}

func mustEpochSnapshot(t *testing.T, epochID uint64, startSlot uint64, epochSlots uint64, seed structure.Hash, set ValidatorSet) EpochSnapshot {
	t.Helper()
	snapshot, err := NewEpochSnapshot(epochID, startSlot, epochSlots, seed, set)
	if err != nil {
		t.Fatalf("new epoch snapshot: %v", err)
	}
	return snapshot
}

func mustLeaderSchedule(t *testing.T, snapshot EpochSnapshot) LeaderSchedule {
	t.Helper()
	schedule, err := NewLeaderSchedule(snapshot)
	if err != nil {
		t.Fatalf("new leader schedule: %v", err)
	}
	return schedule
}

func mustKeyPair(t *testing.T, label string) structure.SolanaKeyPair {
	t.Helper()
	seed := utils.SHA256([]byte(label))
	keyPair, err := structure.KeyPairFromSeed(seed)
	if err != nil {
		t.Fatalf("key pair from seed: %v", err)
	}
	return keyPair
}

func mustHashFromText(t *testing.T, text string) structure.Hash {
	t.Helper()
	hash, err := structure.NewHash(utils.SHA256([]byte(text)))
	if err != nil {
		t.Fatalf("new hash: %v", err)
	}
	return hash
}

func newTestAccount(t *testing.T, address structure.PublicKey, lamports uint64, owner structure.PublicKey, executable bool, data []byte) structure.AddressedAccount {
	t.Helper()
	account, err := structure.NewAccount(lamports, data, owner, executable, 0)
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	return structure.AddressedAccount{Address: address, Account: account}
}

func mustFindAccount(t *testing.T, state ChainState, address structure.PublicKey) structure.AddressedAccount {
	t.Helper()
	for _, account := range state.Accounts {
		if account.Address == address {
			return account
		}
	}
	t.Fatalf("account not found: %s", address.String())
	return structure.AddressedAccount{}
}

func assertSameStateRoot(t *testing.T, left ChainState, right ChainState) {
	t.Helper()
	leftRoot, err := left.RootHash()
	if err != nil {
		t.Fatalf("left state root: %v", err)
	}
	rightRoot, err := right.RootHash()
	if err != nil {
		t.Fatalf("right state root: %v", err)
	}
	if leftRoot != rightRoot {
		t.Fatalf("state root mismatch: %s != %s", leftRoot.String(), rightRoot.String())
	}
}

func accountBalanceIncreased(before ChainState, after ChainState, address structure.PublicKey, amount uint64) bool {
	beforeAccount := findAccountForCompare(before, address)
	afterAccount := findAccountForCompare(after, address)
	return afterAccount.Account.Lamports == beforeAccount.Account.Lamports+amount
}

func expectedTwoThirdsThreshold(stakes []uint64) uint64 {
	var totalStake uint64
	for _, value := range stakes {
		totalStake += value
	}
	return (totalStake*2 + 2) / 3
}

func findAccountForCompare(state ChainState, address structure.PublicKey) structure.AddressedAccount {
	for _, account := range state.Accounts {
		if account.Address == address {
			return account
		}
	}
	return structure.AddressedAccount{}
}
