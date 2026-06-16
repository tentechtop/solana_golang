package blockchain

import (
	"strings"
	"testing"

	"solana_golang/database"
)

func TestStageBusinessFinalizedBlocksRejectDeepReorg(t *testing.T) {
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
	blockOne, stateOne := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "stage-main-1")
	headOne, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockOne, NextState: stateOne})
	if err != nil {
		t.Fatalf("commit main block one: %v", err)
	}
	blockTwo, stateTwo := testProposalFromHead(t, headOne, stateOne, 2, 2, "stage-main-2")
	headTwo, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockTwo, NextState: stateTwo})
	if err != nil {
		t.Fatalf("commit main block two: %v", err)
	}
	blockThree, stateThree := testProposalFromHead(t, headTwo, stateTwo, 3, 3, "stage-main-3")
	headThree, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockThree, NextState: stateThree})
	if err != nil {
		t.Fatalf("commit main block three: %v", err)
	}
	blockFour, stateFour := testProposalFromHead(t, headThree, stateThree, 4, 4, "stage-main-4")
	headFour, err := ledger.CommitBlock(CommitBlockRequest{Proposal: blockFour, NextState: stateFour})
	if err != nil {
		t.Fatalf("commit main block four: %v", err)
	}
	if headFour.FinalizedHeight != 2 {
		t.Fatalf("finalized height = %d, want 2", headFour.FinalizedHeight)
	}

	forkTwo, forkState := testProposalFromParent(t, headOne, stateOne, 12, 2, "stage-fork-2")
	forkTwoHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: forkTwo, NextState: forkState})
	if err != nil {
		t.Fatalf("save fork two: %v", err)
	}
	forkHead := Head{ChainID: forkTwo.Header.ChainID, Height: 2, Slot: 12, BlockHash: forkTwoHash, QCHash: headOne.QCHash, EpochID: forkTwo.Header.EpochID}
	forkThree, forkState := testProposalFromParent(t, forkHead, forkState, 13, 3, "stage-fork-3")
	forkThreeHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: forkThree, NextState: forkState})
	if err != nil {
		t.Fatalf("save fork three: %v", err)
	}
	forkHead = Head{ChainID: forkThree.Header.ChainID, Height: 3, Slot: 13, BlockHash: forkThreeHash, QCHash: headOne.QCHash, EpochID: forkThree.Header.EpochID}
	forkFour, forkState := testProposalFromParent(t, forkHead, forkState, 14, 4, "stage-fork-4")
	forkFourHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: forkFour, NextState: forkState})
	if err != nil {
		t.Fatalf("save fork four: %v", err)
	}
	forkHead = Head{ChainID: forkFour.Header.ChainID, Height: 4, Slot: 14, BlockHash: forkFourHash, QCHash: headOne.QCHash, EpochID: forkFour.Header.EpochID}
	forkFive, forkState := testProposalFromParent(t, forkHead, forkState, 15, 5, "stage-fork-5")
	forkFiveHash, err := ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: forkFive, NextState: forkState})
	if err != nil {
		t.Fatalf("save fork five: %v", err)
	}

	if _, err := ledger.ReorganizeTo(forkFiveHash); err == nil || !strings.Contains(err.Error(), "finalized") {
		t.Fatalf("reorganize error = %v, want finalized protection", err)
	}
	if ledger.Head().BlockHash != headFour.BlockHash || ledger.Head().FinalizedHeight != 2 {
		t.Fatalf("head changed after rejected reorg: %+v", ledger.Head())
	}
}
