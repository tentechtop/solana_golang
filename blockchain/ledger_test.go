package blockchain

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"solana_golang/consensus"
	"solana_golang/database"
	"solana_golang/programs/stake"
	"solana_golang/structure"
	"solana_golang/utils"
)

func TestBuildGenesisStateCreatesTreasuryAndValidators(t *testing.T) {
	genesis := testGenesis(t)
	state, head, err := BuildGenesisState(genesis)
	if err != nil {
		t.Fatalf("build genesis: %v", err)
	}
	treasury, err := consensus.HardcodedGenesisTreasuryPublicKey()
	if err != nil {
		t.Fatalf("treasury key: %v", err)
	}
	treasuryAccount := findAccount(t, state, treasury)
	if treasuryAccount.Account.Lamports == 0 {
		t.Fatalf("treasury balance is zero")
	}
	if head.Height != 0 || head.StateRoot.IsZero() {
		t.Fatalf("invalid genesis head: %+v", head)
	}
	if _, err := ValidatorSetFromState(state); err != nil {
		t.Fatalf("validator set from genesis: %v", err)
	}
}

func TestBuildGenesisStateCreatesInitialValidatorStakerRewardAccount(t *testing.T) {
	staker := testKeyPair(t, "genesis-unfunded-staker")
	validator := testKeyPair(t, "genesis-validator")
	consensusKey := testKeyPair(t, "genesis-consensus")
	genesis := GenesisConfig{
		ChainID:               "test-genesis-staker",
		InitialSupplyLamports: consensus.DefaultGenesisSupplyLamports,
		InitialValidators: []GenesisValidator{{
			StakerAddress:      staker.PublicKey,
			ValidatorAddress:   validator.PublicKey,
			ConsensusPublicKey: consensusKey.PublicKey,
			P2PPeerID:          testPeerID(t, "genesis-peer"),
			StakeLamports:      stake.MinimumStakeLamports,
		}},
	}

	state, _, err := BuildGenesisState(genesis)
	if err != nil {
		t.Fatalf("BuildGenesisState() error = %v", err)
	}
	stakerAccount := findAccount(t, state, staker.PublicKey)
	minimumLamports, err := structure.MinimumBalanceForRentExemption(0)
	if err != nil {
		t.Fatalf("MinimumBalanceForRentExemption() error = %v", err)
	}
	if stakerAccount.Account.Owner != structure.DefaultBuiltinProgramIDs.System {
		t.Fatalf("staker owner = %s, want system", stakerAccount.Account.Owner.String())
	}
	if stakerAccount.Account.Lamports != minimumLamports {
		t.Fatalf("staker lamports = %d, want %d", stakerAccount.Account.Lamports, minimumLamports)
	}
}

func TestLedgerCommitPersistsAndReloads(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	head := ledger.Head()
	proposal := consensus.BlockProposal{
		Header: consensus.BlockHeader{
			ChainID:            head.ChainID,
			Slot:               1,
			Height:             1,
			ParentHash:         head.BlockHash,
			PreviousQCHash:     head.QCHash,
			LeaderID:           consensus.NewValidatorID(testKeyPair(t, "consensus-a").PublicKey),
			EpochID:            0,
			TimestampUnixMilli: time.Now().UnixMilli(),
		},
	}
	nextState := ledger.State()
	stateRoot, err := nextState.RootHash()
	if err != nil {
		t.Fatalf("state root: %v", err)
	}
	proposal.Header.StateRoot = stateRoot
	proposal.Header.AccountRoot = stateRoot
	newHead, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposal, NextState: nextState})
	if err != nil {
		t.Fatalf("commit block: %v", err)
	}
	if newHead.Height != 1 {
		t.Fatalf("height = %d, want 1", newHead.Height)
	}

	reloaded, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("reload ledger: %v", err)
	}
	if reloaded.Head().Height != 1 || reloaded.Head().BlockHash != newHead.BlockHash {
		t.Fatalf("reloaded head mismatch: %+v != %+v", reloaded.Head(), newHead)
	}
}

func TestLedgerRejectsNonIncreasingSlot(t *testing.T) {
	ledger, err := NewLedgerFromGenesis(nil, testGenesis(t))
	if err != nil {
		t.Fatalf("new ledger: %v", err)
	}
	proposalOne, stateOne := testProposalFromHead(t, ledger.Head(), ledger.State(), 10, 1, "slot-one")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposalOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit first block: %v", err)
	}
	proposalTwo, stateTwo := testProposalFromHead(t, headOne, stateOne, 9, 2, "slot-two")
	if _, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposalTwo, NextState: stateTwo}); !errors.Is(err, ErrInvalidCommit) {
		t.Fatalf("commit non-increasing slot error = %v, want ErrInvalidCommit", err)
	}
}

func TestLedgerRejectsCommittedTransactionReplay(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	source := testKeyPair(t, "staker-a")
	destination := testKeyPair(t, "replay-destination")
	transaction, err := NewTransferTransaction(source, destination.PublicKey, 1_000_000, ledger.Head().BlockHash)
	if err != nil {
		t.Fatalf("NewTransferTransaction() error = %v", err)
	}
	transactionID, err := transaction.TxIDString()
	if err != nil {
		t.Fatalf("TxIDString() error = %v", err)
	}

	proposalOne, stateOne := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "replay-one")
	proposalOne.Transactions = []structure.Transaction{transaction}
	if _, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposalOne, NextState: stateOne}); err != nil {
		t.Fatalf("commit first transaction block: %v", err)
	}
	committed, err := ledger.HasCommittedTransaction(transactionID)
	if err != nil {
		t.Fatalf("HasCommittedTransaction() error = %v", err)
	}
	if !committed {
		t.Fatalf("transaction %s not marked committed", transactionID)
	}

	proposalTwo, stateTwo := testProposalFromHead(t, ledger.Head(), ledger.State(), 2, 2, "replay-two")
	proposalTwo.Transactions = []structure.Transaction{transaction}
	if _, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposalTwo, NextState: stateTwo}); !errors.Is(err, ErrInvalidCommit) {
		t.Fatalf("commit replay transaction error = %v, want ErrInvalidCommit", err)
	}

	reloaded, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("reload ledger: %v", err)
	}
	committedAfterReload, err := reloaded.HasCommittedTransaction(transactionID)
	if err != nil {
		t.Fatalf("HasCommittedTransaction(after reload) error = %v", err)
	}
	if !committedAfterReload {
		t.Fatalf("transaction %s not marked committed after reload", transactionID)
	}
}

func TestLedgerContractProgramsReturnsExecutableBPFPrograms(t *testing.T) {
	ledger, err := NewLedgerFromGenesis(nil, testGenesis(t))
	if err != nil {
		t.Fatalf("NewLedgerFromGenesis() error = %v", err)
	}
	programOne := testKeyPair(t, "contract-program-one").PublicKey
	programTwo := testKeyPair(t, "contract-program-two").PublicKey
	nonExecutableProgram := testKeyPair(t, "contract-program-draft").PublicKey
	systemExecutable := testKeyPair(t, "contract-system-executable").PublicKey

	nextState := ledger.State()
	nextState.Accounts = append(nextState.Accounts,
		structure.AddressedAccount{Address: programTwo, Account: testContractAccount(t, []byte("code-two"), structure.DefaultBuiltinProgramIDs.BPFLoader, true)},
		structure.AddressedAccount{Address: programOne, Account: testContractAccount(t, []byte("code-one"), structure.DefaultBuiltinProgramIDs.BPFLoader, true)},
		structure.AddressedAccount{Address: nonExecutableProgram, Account: testContractAccount(t, []byte("draft"), structure.DefaultBuiltinProgramIDs.BPFLoader, false)},
		structure.AddressedAccount{Address: systemExecutable, Account: testContractAccount(t, []byte("system"), structure.DefaultBuiltinProgramIDs.System, true)},
	)
	proposal, _ := testProposalFromHead(t, ledger.Head(), nextState, 1, 1, "contract-programs")
	if _, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposal, NextState: nextState}); err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}

	programs, err := ledger.ContractPrograms(10)
	if err != nil {
		t.Fatalf("ContractPrograms() error = %v", err)
	}
	if len(programs) != 2 {
		t.Fatalf("programs length = %d, want 2", len(programs))
	}
	if programs[0].Address.String() > programs[1].Address.String() {
		t.Fatalf("programs are not sorted: %s > %s", programs[0].Address.String(), programs[1].Address.String())
	}
	for _, program := range programs {
		if program.Owner != structure.DefaultBuiltinProgramIDs.BPFLoader {
			t.Fatalf("owner = %s, want BPFLoader", program.Owner.String())
		}
		if program.CodeHash == "" || program.DataLength == 0 {
			t.Fatalf("program = %+v, want code hash and data length", program)
		}
	}

	limitedPrograms, err := ledger.ContractPrograms(1)
	if err != nil {
		t.Fatalf("ContractPrograms(limit) error = %v", err)
	}
	if len(limitedPrograms) != 1 || limitedPrograms[0].Address != programs[0].Address {
		t.Fatalf("limited programs = %+v, want first sorted program", limitedPrograms)
	}
}

func TestLedgerTransactionByIDPersistsAcrossReload(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	source := testKeyPair(t, "transaction-lookup-source")
	destination := testKeyPair(t, "transaction-lookup-destination")
	transaction, err := NewTransferTransaction(source, destination.PublicKey, 1_000_000, ledger.Head().BlockHash)
	if err != nil {
		t.Fatalf("NewTransferTransaction() error = %v", err)
	}
	transactionID, err := transaction.TxIDString()
	if err != nil {
		t.Fatalf("TxIDString() error = %v", err)
	}

	proposal, nextState := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "transaction-lookup")
	proposal.Transactions = []structure.Transaction{transaction}
	committedHead, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposal, NextState: nextState})
	if err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}

	committedProposal, blockHash, found, err := ledger.TransactionByID(transactionID)
	if err != nil {
		t.Fatalf("TransactionByID() error = %v", err)
	}
	if !found {
		t.Fatalf("TransactionByID(%s) = not found, want found", transactionID)
	}
	if blockHash != committedHead.BlockHash {
		t.Fatalf("block hash = %s, want %s", blockHash.String(), committedHead.BlockHash.String())
	}
	if len(committedProposal.Transactions) != 1 {
		t.Fatalf("transactions len = %d, want 1", len(committedProposal.Transactions))
	}

	reloaded, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("reload ledger: %v", err)
	}
	reloadedProposal, reloadedBlockHash, found, err := reloaded.TransactionByID(transactionID)
	if err != nil {
		t.Fatalf("TransactionByID(after reload) error = %v", err)
	}
	if !found {
		t.Fatalf("TransactionByID(after reload) = not found, want found")
	}
	if reloadedBlockHash != committedHead.BlockHash {
		t.Fatalf("reloaded block hash = %s, want %s", reloadedBlockHash.String(), committedHead.BlockHash.String())
	}
	if len(reloadedProposal.Transactions) != 1 {
		t.Fatalf("reloaded transactions len = %d, want 1", len(reloadedProposal.Transactions))
	}
}

func TestLedgerWritesStructuredCommitAndQCLogs(t *testing.T) {
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, nil))
	ledger, err := NewLedgerFromGenesis(nil, testGenesis(t))
	if err != nil {
		t.Fatalf("new ledger: %v", err)
	}
	ledger.SetLogger(logger)

	proposal, nextState := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "log-block")
	head, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposal, NextState: nextState})
	if err != nil {
		t.Fatalf("commit block: %v", err)
	}
	qc := consensus.QuorumCertificate{
		Type:               consensus.VoteTypeConfirm,
		Slot:               proposal.Header.Slot,
		BlockHeight:        proposal.Header.Height,
		BlockHash:          head.BlockHash,
		ThresholdStake:     1,
		ConfirmedStake:     1,
		Voters:             []string{"validator-a"},
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	if _, err := ledger.SaveQC(qc); err != nil {
		t.Fatalf("save qc: %v", err)
	}

	logLine := logOutput.String()
	for _, expected := range []string{
		`"msg":"ledger commit block committed"`,
		`"msg":"ledger qc saved"`,
		`"block_hash"`,
		`"qc_hash"`,
		`"leader_id"`,
		`"duration_ms"`,
	} {
		if !strings.Contains(logLine, expected) {
			t.Fatalf("log output = %q, want %s", logLine, expected)
		}
	}
}

func TestBlockLocatorAndCommonAncestorPreferHighestMainChainMatch(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	blockOne, stateOne := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "locator-main-1")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit block one: %v", err)
	}
	blockTwoMain, stateTwoMain := testProposalFromHead(t, headOne, stateOne, 2, 2, "locator-main-2")
	headTwoMain, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockTwoMain, NextState: stateTwoMain})
	if err != nil {
		t.Fatalf("commit block two: %v", err)
	}
	blockThreeMain, stateThreeMain := testProposalFromHead(t, headTwoMain, stateTwoMain, 3, 3, "locator-main-3")
	headThreeMain, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockThreeMain, NextState: stateThreeMain})
	if err != nil {
		t.Fatalf("commit block three: %v", err)
	}
	blockTwoFork, stateTwoFork := testProposalFromParent(t, headOne, stateOne, 12, 2, "locator-fork-2")
	forkTwoHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: blockTwoFork, NextState: stateTwoFork})
	if err != nil {
		t.Fatalf("save fork block two: %v", err)
	}
	forkTwoHead := Head{
		ChainID:   blockTwoFork.Header.ChainID,
		Height:    blockTwoFork.Header.Height,
		Slot:      blockTwoFork.Header.Slot,
		BlockHash: forkTwoHash,
		QCHash:    blockTwoFork.Header.PreviousQCHash,
		StateRoot: blockTwoFork.Header.StateRoot,
		EpochID:   blockTwoFork.Header.EpochID,
	}
	blockThreeFork, stateThreeFork := testProposalFromParent(t, forkTwoHead, stateTwoFork, 13, 3, "locator-fork-3")
	forkThreeHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: blockThreeFork, NextState: stateThreeFork})
	if err != nil {
		t.Fatalf("save fork block three: %v", err)
	}

	locator, err := ledger.BlockLocator(8)
	if err != nil {
		t.Fatalf("BlockLocator() error = %v", err)
	}
	if len(locator) == 0 {
		t.Fatal("BlockLocator() returned no entries")
	}
	if locator[0].Height != headThreeMain.Height || locator[0].BlockHash != headThreeMain.BlockHash {
		t.Fatalf("locator head = %+v, want height %d hash %s", locator[0], headThreeMain.Height, headThreeMain.BlockHash.String())
	}

	commonAncestor, found, err := ledger.FindCommonAncestor([]BlockLocatorEntry{
		{Height: 3, BlockHash: forkThreeHash},
		{Height: 2, BlockHash: forkTwoHash},
		{Height: 1, BlockHash: headOne.BlockHash},
	})
	if err != nil {
		t.Fatalf("FindCommonAncestor() error = %v", err)
	}
	if !found {
		t.Fatal("FindCommonAncestor() = not found, want found")
	}
	if commonAncestor.Height != 1 || commonAncestor.BlockHash != headOne.BlockHash {
		t.Fatalf("common ancestor = %+v, want height 1 hash %s", commonAncestor, headOne.BlockHash.String())
	}
}

func TestBranchResolvedDetectsMissingAncestor(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	blockOne, stateOne := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "branch-main-1")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit block one: %v", err)
	}
	unresolvedProposal, unresolvedState := testProposalFromParent(t, headOne, stateOne, 22, 2, "branch-unresolved")
	unresolvedProposal.Header.ParentHash = testHash(t, "branch-missing-parent")
	unresolvedHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: unresolvedProposal, NextState: unresolvedState})
	if err != nil {
		t.Fatalf("save unresolved branch block: %v", err)
	}
	resolved, err := ledger.BranchResolved(unresolvedHash)
	if err != nil {
		t.Fatalf("BranchResolved(unresolved) error = %v", err)
	}
	if resolved {
		t.Fatal("BranchResolved(unresolved) = true, want false")
	}

	forkProposal, forkState := testProposalFromParent(t, headOne, stateOne, 12, 2, "branch-resolved")
	forkHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: forkProposal, NextState: forkState})
	if err != nil {
		t.Fatalf("save resolved branch block: %v", err)
	}
	resolved, err = ledger.BranchResolved(forkHash)
	if err != nil {
		t.Fatalf("BranchResolved(resolved) error = %v", err)
	}
	if !resolved {
		t.Fatal("BranchResolved(resolved) = false, want true")
	}
}

func TestRewardQCsUseMainChainBestSlot(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	blockOne, stateOne := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "reward-main-1")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit block one: %v", err)
	}
	blockTwo, stateTwo := testProposalFromHead(t, headOne, stateOne, 2, 2, "reward-main-2")
	headTwo, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockTwo, NextState: stateTwo})
	if err != nil {
		t.Fatalf("commit block two: %v", err)
	}
	forkTwo, forkState := testProposalFromParent(t, headOne, stateOne, 12, 2, "reward-fork-2")
	forkTwoHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: forkTwo, NextState: forkState})
	if err != nil {
		t.Fatalf("save fork block: %v", err)
	}

	qcs := []consensus.QuorumCertificate{
		testRewardLedgerQC(1, 1, headOne.BlockHash, 1, []string{"validator-a"}, 1),
		testRewardLedgerQC(1, 1, headOne.BlockHash, 2, []string{"validator-a", "validator-b"}, 2),
		testRewardLedgerQC(2, 2, headTwo.BlockHash, 2, []string{"validator-a", "validator-b"}, 3),
		testRewardLedgerQC(12, 2, forkTwoHash, 2, []string{"validator-a", "validator-c"}, 4),
	}
	for _, qc := range qcs {
		if _, err := ledger.SaveQC(qc); err != nil {
			t.Fatalf("save qc slot %d hash %s: %v", qc.Slot, qc.BlockHash.String(), err)
		}
	}

	rewardQCs, err := ledger.RewardQCs(2, 10)
	if err != nil {
		t.Fatalf("RewardQCs() error = %v", err)
	}
	if len(rewardQCs) != 2 {
		t.Fatalf("RewardQCs() len = %d, want 2: %+v", len(rewardQCs), rewardQCs)
	}
	if rewardQCs[0].Slot != 1 || rewardQCs[0].BlockHash != headOne.BlockHash || rewardQCs[0].ConfirmedStake != 2 {
		t.Fatalf("slot 1 reward qc = %+v, want best main-chain qc", rewardQCs[0])
	}
	if rewardQCs[1].Slot != 2 || rewardQCs[1].BlockHash != headTwo.BlockHash {
		t.Fatalf("slot 2 reward qc = %+v, want main-chain qc", rewardQCs[1])
	}
}

func TestRewardQCsSkipAlreadyRewardedSlot(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	blockOne, stateOne := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "reward-skip-1")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit block one: %v", err)
	}
	blockTwo, stateTwo := testProposalFromHead(t, headOne, stateOne, 2, 2, "reward-skip-2")
	headTwo, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockTwo, NextState: stateTwo})
	if err != nil {
		t.Fatalf("commit block two: %v", err)
	}
	slotOneQC := testRewardLedgerQC(1, 1, headOne.BlockHash, 2, []string{"validator-a", "validator-b"}, 1)
	slotTwoQC := testRewardLedgerQC(2, 2, headTwo.BlockHash, 2, []string{"validator-a", "validator-b"}, 2)
	for _, qc := range []consensus.QuorumCertificate{slotOneQC, slotTwoQC} {
		if _, err := ledger.SaveQC(qc); err != nil {
			t.Fatalf("save qc slot %d: %v", qc.Slot, err)
		}
	}
	rewardBlock, rewardState := testProposalFromHead(t, headTwo, stateTwo, 3, 3, "reward-skip-3")
	rewardBlock.RewardQCs = []consensus.QuorumCertificate{slotOneQC}
	if _, err := ledger.CommitBlock(CommitBlockRequest{Proposal: rewardBlock, NextState: rewardState}); err != nil {
		t.Fatalf("commit reward block: %v", err)
	}

	rewardQCs, err := ledger.RewardQCs(2, 10)
	if err != nil {
		t.Fatalf("RewardQCs() error = %v", err)
	}
	if len(rewardQCs) != 1 || rewardQCs[0].Slot != 2 {
		t.Fatalf("RewardQCs() = %+v, want only slot 2", rewardQCs)
	}
}

func TestRewardQCIndexRollsBackOnReorg(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	blockOne, stateOne := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "reward-reorg-1")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit block one: %v", err)
	}
	blockTwo, stateTwo := testProposalFromHead(t, headOne, stateOne, 2, 2, "reward-reorg-2")
	headTwo, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockTwo, NextState: stateTwo})
	if err != nil {
		t.Fatalf("commit block two: %v", err)
	}
	slotOneQC := testRewardLedgerQC(1, 1, headOne.BlockHash, 2, []string{"validator-a", "validator-b"}, 1)
	if _, err := ledger.SaveQC(slotOneQC); err != nil {
		t.Fatalf("save slot one qc: %v", err)
	}
	oldRewardBlock, oldRewardState := testProposalFromHead(t, headTwo, stateTwo, 3, 3, "reward-reorg-old-3")
	oldRewardBlock.RewardQCs = []consensus.QuorumCertificate{slotOneQC}
	if _, err := ledger.CommitBlock(CommitBlockRequest{Proposal: oldRewardBlock, NextState: oldRewardState}); err != nil {
		t.Fatalf("commit old reward block: %v", err)
	}
	if rewardQCs, err := ledger.RewardQCs(1, 10); err != nil || len(rewardQCs) != 0 {
		t.Fatalf("RewardQCs() after reward = %+v err=%v, want none", rewardQCs, err)
	}

	forkThree, forkState := testProposalFromParent(t, headTwo, stateTwo, 13, 3, "reward-reorg-new-3")
	forkThreeHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: forkThree, NextState: forkState})
	if err != nil {
		t.Fatalf("save fork three: %v", err)
	}
	forkFour, forkState := testProposalFromParent(t, Head{
		ChainID:   forkThree.Header.ChainID,
		Height:    forkThree.Header.Height,
		Slot:      forkThree.Header.Slot,
		BlockHash: forkThreeHash,
		QCHash:    headTwo.QCHash,
		EpochID:   forkThree.Header.EpochID,
	}, forkState, 14, 4, "reward-reorg-new-4")
	forkFourHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: forkFour, NextState: forkState})
	if err != nil {
		t.Fatalf("save fork four: %v", err)
	}
	decision, err := ledger.ReorganizeTo(forkFourHash)
	if err != nil {
		t.Fatalf("reorganize: %v", err)
	}
	if !decision.Accepted || !decision.Reorganized {
		t.Fatalf("decision = %+v, want accepted reorg", decision)
	}

	rewardQCs, err := ledger.RewardQCs(1, 10)
	if err != nil {
		t.Fatalf("RewardQCs() error = %v", err)
	}
	if len(rewardQCs) != 1 || rewardQCs[0].Slot != 1 {
		t.Fatalf("RewardQCs() after reorg = %+v, want slot 1 restored", rewardQCs)
	}
}

func TestSaveQCPromotesOnlyBestMainChainCertificate(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	blockOne, stateOne := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "saveqc-main-1")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit block one: %v", err)
	}
	blockTwoMain, stateTwoMain := testProposalFromHead(t, headOne, stateOne, 2, 2, "saveqc-main-2")
	headTwoMain, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockTwoMain, NextState: stateTwoMain})
	if err != nil {
		t.Fatalf("commit block two: %v", err)
	}
	blockTwoFork, stateTwoFork := testProposalFromHead(t, headOne, stateOne, 2, 2, "saveqc-fork-2")
	forkTwoHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: blockTwoFork, NextState: stateTwoFork})
	if err != nil {
		t.Fatalf("save fork block: %v", err)
	}

	bestMainQC := testRewardLedgerQC(2, 2, headTwoMain.BlockHash, 6, []string{"validator-a", "validator-b", "validator-c"}, 10)
	bestMainHash, err := HashCanonicalQC(bestMainQC)
	if err != nil {
		t.Fatalf("hash best main qc: %v", err)
	}
	if _, err := ledger.SaveQC(bestMainQC); err != nil {
		t.Fatalf("save best main qc: %v", err)
	}
	if ledger.Head().QCHash != bestMainHash {
		t.Fatalf("head qc hash = %s, want %s", ledger.Head().QCHash.String(), bestMainHash.String())
	}

	worseMainQC := testRewardLedgerQC(2, 2, headTwoMain.BlockHash, 4, []string{"validator-a", "validator-b"}, 1)
	worseMainHash, err := HashCanonicalQC(worseMainQC)
	if err != nil {
		t.Fatalf("hash worse main qc: %v", err)
	}
	if worseMainHash != bestMainHash {
		t.Fatalf("canonical qc hash mismatch: worse=%s best=%s", worseMainHash.String(), bestMainHash.String())
	}
	if _, err := ledger.SaveQC(worseMainQC); err != nil {
		t.Fatalf("save worse main qc: %v", err)
	}
	if ledger.Head().QCHash != bestMainHash {
		t.Fatalf("head qc hash after worse qc = %s, want %s", ledger.Head().QCHash.String(), bestMainHash.String())
	}

	sideForkQC := testRewardLedgerQC(2, 2, forkTwoHash, 8, []string{"validator-a", "validator-b", "validator-c", "validator-d"}, 20)
	if _, err := ledger.SaveQC(sideForkQC); err != nil {
		t.Fatalf("save side fork qc: %v", err)
	}
	if ledger.Head().QCHash != bestMainHash {
		t.Fatalf("head qc hash after side fork = %s, want %s", ledger.Head().QCHash.String(), bestMainHash.String())
	}

	reloaded, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("reload ledger: %v", err)
	}
	if reloaded.Head().QCHash != bestMainHash {
		t.Fatalf("reloaded head qc hash = %s, want %s", reloaded.Head().QCHash.String(), bestMainHash.String())
	}
}

func TestCommitBlockAttachesPreviouslyStoredQC(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	blockOne, stateOne := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "attach-qc-main-1")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit block one: %v", err)
	}
	blockTwo, stateTwo := testProposalFromHead(t, headOne, stateOne, 2, 2, "attach-qc-main-2")
	blockTwoHash, err := blockTwo.Hash()
	if err != nil {
		t.Fatalf("hash block two: %v", err)
	}
	qc := testRewardLedgerQC(2, 2, blockTwoHash, 6, []string{"validator-a", "validator-b", "validator-c"}, 10)
	qcHash, err := HashCanonicalQC(qc)
	if err != nil {
		t.Fatalf("hash qc: %v", err)
	}
	if _, err := ledger.SaveQC(qc); err != nil {
		t.Fatalf("save future qc: %v", err)
	}
	if ledger.Head().QCHash == qcHash {
		t.Fatalf("future qc promoted before block is on main chain")
	}
	headTwo, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockTwo, NextState: stateTwo})
	if err != nil {
		t.Fatalf("commit block two: %v", err)
	}
	if headTwo.QCHash != qcHash {
		t.Fatalf("head qc hash = %s, want stored qc %s", headTwo.QCHash.String(), qcHash.String())
	}
	headQC, exists, err := ledger.HeadQC()
	if err != nil {
		t.Fatalf("HeadQC() error = %v", err)
	}
	if !exists || headQC.BlockHash != blockTwoHash || headQC.BlockHeight != 2 {
		t.Fatalf("head qc = %+v exists=%v, want block two qc", headQC, exists)
	}
}

func TestLedgerReorganizeToBetterFork(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	blockOne, stateOne := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "block-one")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit block one: %v", err)
	}

	blockTwoMain, stateTwoMain := testProposalFromHead(t, headOne, stateOne, 2, 2, "block-two-main")
	headTwoMain, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockTwoMain, NextState: stateTwoMain})
	if err != nil {
		t.Fatalf("commit main block two: %v", err)
	}

	blockTwoFork, stateTwoFork := testProposalFromParent(t, headOne, stateOne, 12, 2, "block-two-fork")
	blockTwoForkHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: blockTwoFork, NextState: stateTwoFork})
	if err != nil {
		t.Fatalf("save fork block two: %v", err)
	}
	blockThreeFork, stateThreeFork := testProposalFromParent(t, Head{
		ChainID:   blockTwoFork.Header.ChainID,
		Height:    blockTwoFork.Header.Height,
		Slot:      blockTwoFork.Header.Slot,
		BlockHash: blockTwoForkHash,
		QCHash:    headOne.QCHash,
		EpochID:   blockTwoFork.Header.EpochID,
	}, stateTwoFork, 13, 3, "block-three-fork")
	blockThreeForkHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: blockThreeFork, NextState: stateThreeFork})
	if err != nil {
		t.Fatalf("save fork block three: %v", err)
	}

	decision, err := ledger.ReorganizeTo(blockThreeForkHash)
	if err != nil {
		t.Fatalf("reorganize: %v", err)
	}
	if !decision.Accepted || !decision.Reorganized {
		t.Fatalf("decision = %+v, want accepted reorg", decision)
	}
	if decision.CommonAncestor.BlockHash != headOne.BlockHash {
		t.Fatalf("common ancestor = %s, want %s", decision.CommonAncestor.BlockHash.String(), headOne.BlockHash.String())
	}
	if ledger.Head().Height != 3 || ledger.Head().BlockHash != blockThreeForkHash {
		t.Fatalf("head = %+v, want fork height 3", ledger.Head())
	}
	if ledger.Head().FinalizedHeight != 1 || ledger.Head().FinalizedHash != headOne.BlockHash {
		t.Fatalf("finalized head = %+v, want height 1 hash %s", ledger.Head(), headOne.BlockHash.String())
	}
	heightTwoHash, err := db.Get(database.TableHeightToHash, uint64Key(2))
	if err != nil {
		t.Fatalf("read height index: %v", err)
	}
	if string(heightTwoHash) != string(blockTwoForkHash[:]) {
		t.Fatalf("height 2 hash = %x, want %x", heightTwoHash, blockTwoForkHash[:])
	}
	if string(headTwoMain.BlockHash[:]) == string(blockTwoForkHash[:]) {
		t.Fatalf("test setup produced identical fork hash")
	}
}

func TestLedgerReorganizeStopsAtImportedFinalizedSnapshot(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("LoadOrCreateLedger() error = %v", err)
	}
	genesisHead := ledger.Head()
	genesisState := ledger.State()
	importProposal, importState := testProposalFromParent(t, Head{
		ChainID:   genesisHead.ChainID,
		Height:    9,
		Slot:      99,
		BlockHash: testHash(t, "missing-import-parent"),
		QCHash:    genesisHead.QCHash,
		EpochID:   3,
	}, genesisState, 100, 10, "import-finalized-reorg")
	importedHead, err := ledger.ImportFinalizedSnapshot(ImportSnapshotRequest{Proposal: importProposal, State: importState})
	if err != nil {
		t.Fatalf("ImportFinalizedSnapshot() error = %v", err)
	}

	oldEleven, oldState := testProposalFromHead(t, importedHead, importState, 101, 11, "old-after-import-11")
	oldHead, err := ledger.CommitBlock(CommitBlockRequest{Proposal: oldEleven, NextState: oldState})
	if err != nil {
		t.Fatalf("commit old eleven: %v", err)
	}
	oldTwelve, oldState := testProposalFromHead(t, oldHead, oldState, 102, 12, "old-after-import-12")
	oldHead, err = ledger.CommitBlock(CommitBlockRequest{Proposal: oldTwelve, NextState: oldState})
	if err != nil {
		t.Fatalf("commit old twelve: %v", err)
	}

	newEleven, newState := testProposalFromParent(t, importedHead, importState, 111, 11, "new-after-import-11")
	newElevenHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: newEleven, NextState: newState})
	if err != nil {
		t.Fatalf("save new eleven: %v", err)
	}
	newTwelve, newState := testProposalFromParent(t, Head{
		ChainID:   newEleven.Header.ChainID,
		Height:    newEleven.Header.Height,
		Slot:      newEleven.Header.Slot,
		BlockHash: newElevenHash,
		QCHash:    importedHead.QCHash,
		EpochID:   newEleven.Header.EpochID,
	}, newState, 112, 12, "new-after-import-12")
	newTwelveHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: newTwelve, NextState: newState})
	if err != nil {
		t.Fatalf("save new twelve: %v", err)
	}
	newThirteen, newState := testProposalFromParent(t, Head{
		ChainID:   newTwelve.Header.ChainID,
		Height:    newTwelve.Header.Height,
		Slot:      newTwelve.Header.Slot,
		BlockHash: newTwelveHash,
		QCHash:    importedHead.QCHash,
		EpochID:   newTwelve.Header.EpochID,
	}, newState, 113, 13, "new-after-import-13")
	newThirteenHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: newThirteen, NextState: newState})
	if err != nil {
		t.Fatalf("save new thirteen: %v", err)
	}

	decision, err := ledger.ReorganizeTo(newThirteenHash)
	if err != nil {
		t.Fatalf("ReorganizeTo() error = %v", err)
	}
	if !decision.Accepted || !decision.Reorganized {
		t.Fatalf("decision = %+v, want accepted reorg", decision)
	}
	if decision.CommonAncestor.BlockHash != importedHead.BlockHash {
		t.Fatalf("common ancestor = %s, want imported finalized %s", decision.CommonAncestor.BlockHash.String(), importedHead.BlockHash.String())
	}
	if ledger.Head().BlockHash != newThirteenHash || ledger.Head().Height != 13 {
		t.Fatalf("head = %+v, want new height 13", ledger.Head())
	}
	if ledger.Head().FinalizedHeight != 11 || ledger.Head().FinalizedHash != newElevenHash {
		t.Fatalf("finalized head = %+v, want new block eleven %s", ledger.Head(), newElevenHash.String())
	}
	if oldHead.BlockHash == ledger.Head().BlockHash {
		t.Fatalf("old head still selected after reorg")
	}
}

func TestLedgerImportFinalizedSnapshotAndContinueCommit(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("LoadOrCreateLedger() error = %v", err)
	}
	genesisHead := ledger.Head()
	genesisState := ledger.State()
	importProposal, importState := testProposalFromParent(t, Head{
		ChainID:   genesisHead.ChainID,
		Height:    9,
		Slot:      99,
		BlockHash: testHash(t, "snapshot-parent"),
		QCHash:    genesisHead.QCHash,
		EpochID:   3,
	}, genesisState, 100, 10, "import-finalized")

	importedHead, err := ledger.ImportFinalizedSnapshot(ImportSnapshotRequest{Proposal: importProposal, State: importState})
	if err != nil {
		t.Fatalf("ImportFinalizedSnapshot() error = %v", err)
	}
	if importedHead.Height != 10 || importedHead.FinalizedHeight != 10 {
		t.Fatalf("imported head = %+v, want height/finalized 10", importedHead)
	}
	nextProposal, nextState := testProposalFromHead(t, importedHead, importState, 101, 11, "after-import")
	nextHead, err := ledger.CommitBlock(CommitBlockRequest{Proposal: nextProposal, NextState: nextState})
	if err != nil {
		t.Fatalf("CommitBlock(after import) error = %v", err)
	}
	if nextHead.Height != 11 {
		t.Fatalf("next height = %d, want 11", nextHead.Height)
	}
	if nextHead.FinalizedHeight != importedHead.FinalizedHeight {
		t.Fatalf("finalized height = %d, want %d", nextHead.FinalizedHeight, importedHead.FinalizedHeight)
	}
}

func TestLedgerImportFinalizedSnapshotAdvancesCheckpointOnMainChain(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("LoadOrCreateLedger() error = %v", err)
	}
	genesisHead := ledger.Head()
	genesisState := ledger.State()
	blockOne, stateOne := testProposalFromHead(t, genesisHead, genesisState, 1, 1, "advance-main-one")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit block one: %v", err)
	}
	blockTwo, stateTwo := testProposalFromHead(t, headOne, stateOne, 2, 2, "advance-main-two")
	headTwo, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockTwo, NextState: stateTwo})
	if err != nil {
		t.Fatalf("commit block two: %v", err)
	}
	blockThree, stateThree := testProposalFromHead(t, headTwo, stateTwo, 3, 3, "advance-main-three")
	headThree, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockThree, NextState: stateThree})
	if err != nil {
		t.Fatalf("commit block three: %v", err)
	}
	if headThree.FinalizedHeight != 1 {
		t.Fatalf("initial finalized height = %d, want 1", headThree.FinalizedHeight)
	}

	advancedHead, err := ledger.ImportFinalizedSnapshot(ImportSnapshotRequest{Proposal: blockTwo, State: stateTwo})
	if err != nil {
		t.Fatalf("ImportFinalizedSnapshot(main chain) error = %v", err)
	}
	if advancedHead.Height != headThree.Height || advancedHead.BlockHash != headThree.BlockHash {
		t.Fatalf("head changed to %+v, want height/hash from %+v", advancedHead, headThree)
	}
	blockTwoHash, err := blockTwo.Hash()
	if err != nil {
		t.Fatalf("block two hash: %v", err)
	}
	if advancedHead.FinalizedHeight != 2 || advancedHead.FinalizedHash != blockTwoHash {
		t.Fatalf("finalized checkpoint = %+v, want block two %s", advancedHead, blockTwoHash.String())
	}
	if ledger.Head().StateRoot != headThree.StateRoot {
		t.Fatalf("state root changed after finalized checkpoint advance")
	}
}

func TestLedgerImportFinalizedSnapshotRewindsConflictingOptimisticHead(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("LoadOrCreateLedger() error = %v", err)
	}
	genesisHead := ledger.Head()
	genesisState := ledger.State()
	blockOne, stateOne := testProposalFromHead(t, genesisHead, genesisState, 1, 1, "rewind-one")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit block one: %v", err)
	}
	oldTwo, oldState := testProposalFromHead(t, headOne, stateOne, 2, 2, "rewind-old-two")
	oldHead, err := ledger.CommitBlock(CommitBlockRequest{Proposal: oldTwo, NextState: oldState})
	if err != nil {
		t.Fatalf("commit old two: %v", err)
	}
	oldThree, oldState := testProposalFromHead(t, oldHead, oldState, 3, 3, "rewind-old-three")
	oldHead, err = ledger.CommitBlock(CommitBlockRequest{Proposal: oldThree, NextState: oldState})
	if err != nil {
		t.Fatalf("commit old three: %v", err)
	}
	if oldHead.FinalizedHeight != 1 {
		t.Fatalf("initial finalized height = %d, want 1", oldHead.FinalizedHeight)
	}

	forkTwo, forkState := testProposalFromParent(t, headOne, stateOne, 20, 2, "rewind-finalized-two")
	forkHash, err := forkTwo.Hash()
	if err != nil {
		t.Fatalf("fork hash: %v", err)
	}
	importedHead, err := ledger.ImportFinalizedSnapshot(ImportSnapshotRequest{Proposal: forkTwo, State: forkState})
	if err != nil {
		t.Fatalf("ImportFinalizedSnapshot(conflicting) error = %v", err)
	}
	if importedHead.Height != 2 || importedHead.BlockHash != forkHash {
		t.Fatalf("imported head = %+v, want fork height 2 hash %s", importedHead, forkHash.String())
	}
	if importedHead.FinalizedHeight != 2 || importedHead.FinalizedHash != forkHash {
		t.Fatalf("imported finalized = %+v, want fork finalized %s", importedHead, forkHash.String())
	}
	if _, _, found, err := ledger.BlockByHeight(3); err != nil || found {
		t.Fatalf("BlockByHeight(3) found=%v err=%v, want old optimistic height removed", found, err)
	}
	if _, found, err := ledger.MainChainHashAtHeight(3); err != nil || found {
		t.Fatalf("MainChainHashAtHeight(3) found=%v err=%v, want old optimistic height removed", found, err)
	}

	nextProposal, nextState := testProposalFromHead(t, importedHead, forkState, 21, 3, "rewind-after-import")
	nextHead, err := ledger.CommitBlock(CommitBlockRequest{Proposal: nextProposal, NextState: nextState})
	if err != nil {
		t.Fatalf("CommitBlock(after conflicting import) error = %v", err)
	}
	if nextHead.Height != 3 || nextHead.FinalizedHeight != 2 {
		t.Fatalf("next head = %+v, want height 3 finalized 2", nextHead)
	}
}

func TestLedgerFinalityDepthConfig(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ledger, err := NewLedgerFromGenesisWithConfig(db, testGenesis(t), LedgerConfig{FinalityDepth: 3})
	if err != nil {
		t.Fatalf("NewLedgerFromGenesisWithConfig() error = %v", err)
	}
	if ledger.FinalityDepth() != 3 {
		t.Fatalf("FinalityDepth() = %d, want 3", ledger.FinalityDepth())
	}
	state := ledger.State()
	head := ledger.Head()
	for height := uint64(1); height <= 4; height++ {
		proposal, nextState := testProposalFromHead(t, head, state, height, height, "finality")
		head, err = ledger.CommitBlock(CommitBlockRequest{Proposal: proposal, NextState: nextState})
		if err != nil {
			t.Fatalf("CommitBlock(%d) error = %v", height, err)
		}
		state = nextState
	}
	if head.FinalizedHeight != 1 {
		t.Fatalf("finalized height = %d, want 1", head.FinalizedHeight)
	}
}

func TestValidatorSetFromFinalizedStateIgnoresSpeculativeHeadStake(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := NewLedgerFromGenesisWithConfig(db, testGenesis(t), LedgerConfig{FinalityDepth: 3})
	if err != nil {
		t.Fatalf("NewLedgerFromGenesisWithConfig() error = %v", err)
	}
	genesisSet, err := ledger.ValidatorSetFromFinalizedStateAtEpoch(0)
	if err != nil {
		t.Fatalf("ValidatorSetFromFinalizedStateAtEpoch(genesis) error = %v", err)
	}
	genesisStake := singleValidatorStakeLamports(t, genesisSet)

	speculativeState := ledger.State()
	increaseFirstValidatorStake(t, &speculativeState, stake.MinimumStakeLamports)
	proposal, nextState := testProposalFromHead(t, ledger.Head(), speculativeState, 1, 1, "speculative-stake")
	head, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposal, NextState: nextState})
	if err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}
	if head.FinalizedHeight != 0 {
		t.Fatalf("finalized height = %d, want genesis only", head.FinalizedHeight)
	}

	currentSet, err := ledger.ValidatorSetFromStateAtEpoch(0)
	if err != nil {
		t.Fatalf("ValidatorSetFromStateAtEpoch() error = %v", err)
	}
	currentStake := singleValidatorStakeLamports(t, currentSet)
	if currentStake != genesisStake+stake.MinimumStakeLamports {
		t.Fatalf("current stake = %d, want %d", currentStake, genesisStake+stake.MinimumStakeLamports)
	}

	finalizedSet, err := ledger.ValidatorSetFromFinalizedStateAtEpoch(0)
	if err != nil {
		t.Fatalf("ValidatorSetFromFinalizedStateAtEpoch() error = %v", err)
	}
	finalizedStake := singleValidatorStakeLamports(t, finalizedSet)
	if finalizedStake != genesisStake {
		t.Fatalf("finalized stake = %d, want %d", finalizedStake, genesisStake)
	}
}

func TestValidatorSetFromStateExcludesJailedStakeAccount(t *testing.T) {
	stakerA := testKeyPair(t, "jailed-staker-a")
	validatorA := testKeyPair(t, "jailed-validator-a")
	consensusA := testKeyPair(t, "jailed-consensus-a")
	stakerB := testKeyPair(t, "jailed-staker-b")
	validatorB := testKeyPair(t, "jailed-validator-b")
	consensusB := testKeyPair(t, "jailed-consensus-b")
	genesis := GenesisConfig{
		ChainID:               "test-jailed-validator-set",
		InitialSupplyLamports: consensus.DefaultGenesisSupplyLamports,
		InitialValidators: []GenesisValidator{
			{
				StakerAddress:      stakerA.PublicKey,
				ValidatorAddress:   validatorA.PublicKey,
				ConsensusPublicKey: consensusA.PublicKey,
				P2PPeerID:          testPeerID(t, "jailed-peer-a"),
				StakeLamports:      stake.MinimumStakeLamports,
			},
			{
				StakerAddress:      stakerB.PublicKey,
				ValidatorAddress:   validatorB.PublicKey,
				ConsensusPublicKey: consensusB.PublicKey,
				P2PPeerID:          testPeerID(t, "jailed-peer-b"),
				StakeLamports:      stake.MinimumStakeLamports,
			},
		},
	}
	state, _, err := BuildGenesisState(genesis)
	if err != nil {
		t.Fatalf("BuildGenesisState() error = %v", err)
	}
	jailValidatorStakeAccount(t, &state, consensusA.PublicKey, 0)

	validatorSet, err := ValidatorSetFromStateAtEpoch(state, 0)
	if err != nil {
		t.Fatalf("ValidatorSetFromStateAtEpoch() error = %v", err)
	}
	validators := validatorSet.Validators()
	if len(validators) != 1 {
		t.Fatalf("validator count = %d, want 1", len(validators))
	}
	if validators[0].ConsensusPublicKey != consensusB.PublicKey {
		t.Fatalf("validator consensus key = %s, want %s", validators[0].ConsensusPublicKey.String(), consensusB.PublicKey.String())
	}
}

func TestLoadOrCreateLedgerRecoversCorruptedAccountTable(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ledger, err := LoadOrCreateLedgerWithConfig(db, testGenesis(t), LedgerConfig{})
	if err != nil {
		t.Fatalf("LoadOrCreateLedgerWithConfig() error = %v", err)
	}
	proposal, nextState := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "recover")
	committedHead, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposal, NextState: nextState})
	if err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}

	account := nextState.Accounts[0]
	corruptedAccount := account.Account
	corruptedAccount.Lamports++
	corruptedBytes, err := corruptedAccount.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal corrupted account: %v", err)
	}
	if err := db.Put(database.TableAccount, account.Address[:], corruptedBytes); err != nil {
		t.Fatalf("corrupt account table: %v", err)
	}

	recoveredLedger, err := LoadOrCreateLedgerWithConfig(db, testGenesis(t), LedgerConfig{})
	if err != nil {
		t.Fatalf("LoadOrCreateLedgerWithConfig(recover) error = %v", err)
	}
	if recoveredLedger.Head().BlockHash != committedHead.BlockHash {
		t.Fatalf("head hash = %s, want %s", recoveredLedger.Head().BlockHash.String(), committedHead.BlockHash.String())
	}
	recoveredAccount := findAccount(t, recoveredLedger.State(), account.Address)
	if recoveredAccount.Account.Lamports != account.Account.Lamports {
		t.Fatalf("recovered lamports = %d, want %d", recoveredAccount.Account.Lamports, account.Account.Lamports)
	}
}

func increaseFirstValidatorStake(t *testing.T, state *consensus.ChainState, lamports uint64) {
	t.Helper()
	for index := range state.Accounts {
		if state.Accounts[index].Account.Owner != structure.DefaultBuiltinProgramIDs.Stake {
			continue
		}
		stakeState, err := stake.UnmarshalValidatorStateBinary(state.Accounts[index].Account.Data)
		if err != nil {
			continue
		}
		if ^uint64(0)-stakeState.ActiveStake < lamports {
			t.Fatalf("validator active stake overflow")
		}
		if ^uint64(0)-state.Accounts[index].Account.Lamports < lamports {
			t.Fatalf("validator account lamports overflow")
		}
		stakeState.ActiveStake += lamports
		stakeState.LastEffectiveStake = stakeState.ActiveStake
		data, err := stakeState.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary() error = %v", err)
		}
		state.Accounts[index].Account.Data = data
		state.Accounts[index].Account.Lamports += lamports
		return
	}
	t.Fatal("validator stake account not found")
}

func jailValidatorStakeAccount(t *testing.T, state *consensus.ChainState, consensusPublicKey structure.PublicKey, jailUntilEpoch uint64) {
	t.Helper()
	for index := range state.Accounts {
		if state.Accounts[index].Account.Owner != structure.DefaultBuiltinProgramIDs.Stake {
			continue
		}
		stakeState, err := stake.UnmarshalValidatorStateBinary(state.Accounts[index].Account.Data)
		if err != nil || stakeState.ConsensusPublicKey != consensusPublicKey {
			continue
		}
		stakeState.Status = stake.ValidatorStatusJailed
		stakeState.JailUntilEpoch = jailUntilEpoch
		stakeState.UnlockEpoch = jailUntilEpoch
		data, err := stakeState.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary() error = %v", err)
		}
		state.Accounts[index].Account.Data = data
		return
	}
	t.Fatal("validator stake account not found")
}

func singleValidatorStakeLamports(t *testing.T, set consensus.ValidatorSet) uint64 {
	t.Helper()
	validators := set.Validators()
	if len(validators) != 1 {
		t.Fatalf("validator count = %d, want 1", len(validators))
	}
	return validators[0].StakeLamports
}

func testGenesis(t *testing.T) GenesisConfig {
	t.Helper()
	staker := testKeyPair(t, "staker-a")
	validator := testKeyPair(t, "validator-a")
	consensusKey := testKeyPair(t, "consensus-a")
	return GenesisConfig{
		ChainID:               "test-chain",
		InitialSupplyLamports: consensus.DefaultGenesisSupplyLamports,
		FundedAccounts: []GenesisAccount{{
			Address:  staker.PublicKey,
			Lamports: 1_000_000_000,
		}},
		InitialValidators: []GenesisValidator{{
			StakerAddress:      staker.PublicKey,
			ValidatorAddress:   validator.PublicKey,
			ConsensusPublicKey: consensusKey.PublicKey,
			P2PPeerID:          testPeerID(t, "peer-a"),
			StakeLamports:      stake.MinimumStakeLamports,
		}},
	}
}

func testProposalFromHead(t *testing.T, head Head, state consensus.ChainState, slot uint64, height uint64, seed string) (consensus.BlockProposal, consensus.ChainState) {
	t.Helper()
	return testProposalFromParent(t, head, state, slot, height, seed)
}

func testProposalFromParent(t *testing.T, parent Head, state consensus.ChainState, slot uint64, height uint64, seed string) (consensus.BlockProposal, consensus.ChainState) {
	t.Helper()
	stateRoot, err := state.RootHash()
	if err != nil {
		t.Fatalf("state root: %v", err)
	}
	return consensus.BlockProposal{
		Header: consensus.BlockHeader{
			ChainID:            parent.ChainID,
			Slot:               slot,
			Height:             height,
			ParentHash:         parent.BlockHash,
			PreviousQCHash:     parent.QCHash,
			LeaderID:           consensus.NewValidatorID(testKeyPair(t, seed).PublicKey),
			EpochID:            parent.EpochID,
			StateRoot:          stateRoot,
			AccountRoot:        stateRoot,
			TimestampUnixMilli: time.Now().UnixMilli() + int64(slot),
		},
	}, state
}

func testKeyPair(t *testing.T, seed string) structure.SolanaKeyPair {
	t.Helper()
	keyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(seed)))
	if err != nil {
		t.Fatalf("key pair: %v", err)
	}
	return keyPair
}

func testPeerID(t *testing.T, seed string) string {
	t.Helper()
	publicKey, err := utils.DeriveEd25519PublicKeyFromPrivateKey(utils.SHA256([]byte(seed)))
	if err != nil {
		t.Fatalf("peer id: %v", err)
	}
	return utils.Base58Encode(publicKey)
}

func testHash(t *testing.T, seed string) structure.Hash {
	t.Helper()
	hash, err := structure.NewHash(utils.SHA256([]byte(seed)))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return hash
}

func testContractAccount(t *testing.T, data []byte, owner structure.PublicKey, executable bool) structure.Account {
	t.Helper()
	minimumLamports, err := structure.MinimumBalanceForRentExemption(len(data))
	if err != nil {
		t.Fatalf("MinimumBalanceForRentExemption() error = %v", err)
	}
	account, err := structure.NewAccount(minimumLamports, data, owner, executable, 0)
	if err != nil {
		t.Fatalf("NewAccount(contract) error = %v", err)
	}
	return account
}

func testRewardLedgerQC(slot uint64, height uint64, blockHash structure.Hash, stake uint64, voters []string, offset int64) consensus.QuorumCertificate {
	return consensus.QuorumCertificate{
		Type:               consensus.VoteTypeConfirm,
		Slot:               slot,
		BlockHeight:        height,
		BlockHash:          blockHash,
		ThresholdStake:     1,
		ConfirmedStake:     stake,
		Voters:             voters,
		CreatedAtUnixMilli: time.Now().UnixMilli() + offset,
	}
}

func findAccount(t *testing.T, state consensus.ChainState, address structure.PublicKey) structure.AddressedAccount {
	t.Helper()
	for _, account := range state.Accounts {
		if account.Address == address {
			return account
		}
	}
	t.Fatalf("account not found: %s", address.String())
	return structure.AddressedAccount{}
}
