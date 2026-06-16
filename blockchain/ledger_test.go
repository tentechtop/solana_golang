package blockchain

import (
	"bytes"
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
