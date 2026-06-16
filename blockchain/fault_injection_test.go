package blockchain

import (
	"testing"

	"solana_golang/database"
)

func TestFaultInjectionReorgPersistsAfterNodeRestart(t *testing.T) {
	dbPath := t.TempDir()
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   dbPath,
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	blockOne, stateOne := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "fault-main-1")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit block one: %v", err)
	}
	blockTwoMain, stateTwoMain := testProposalFromHead(t, headOne, stateOne, 2, 2, "fault-main-2")
	if _, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockTwoMain, NextState: stateTwoMain}); err != nil {
		t.Fatalf("commit main block two: %v", err)
	}

	blockTwoFork, stateTwoFork := testProposalFromParent(t, headOne, stateOne, 12, 2, "fault-fork-2")
	blockTwoForkHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: blockTwoFork, NextState: stateTwoFork})
	if err != nil {
		t.Fatalf("save fork block two: %v", err)
	}
	forkHead := Head{
		ChainID:   blockTwoFork.Header.ChainID,
		Height:    blockTwoFork.Header.Height,
		Slot:      blockTwoFork.Header.Slot,
		BlockHash: blockTwoForkHash,
		QCHash:    headOne.QCHash,
		EpochID:   blockTwoFork.Header.EpochID,
	}
	blockThreeFork, stateThreeFork := testProposalFromParent(t, forkHead, stateTwoFork, 13, 3, "fault-fork-3")
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
	if err := db.Close(); err != nil {
		t.Fatalf("close db before restart: %v", err)
	}

	reopened, err := database.NewDatabase(database.DatabaseConfig{
		Path:   dbPath,
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer reopened.Close()
	reloaded, err := LoadOrCreateLedger(reopened, testGenesis(t))
	if err != nil {
		t.Fatalf("reload ledger: %v", err)
	}
	head := reloaded.Head()
	if head.BlockHash != blockThreeForkHash || head.Height != 3 {
		t.Fatalf("reloaded head = %+v, want fork tip %s height 3", head, blockThreeForkHash.String())
	}
	if head.FinalizedHeight != 1 || head.FinalizedHash != headOne.BlockHash {
		t.Fatalf("reloaded finalized = height %d hash %s, want height 1 hash %s", head.FinalizedHeight, head.FinalizedHash.String(), headOne.BlockHash.String())
	}
}
