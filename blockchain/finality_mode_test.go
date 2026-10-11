package blockchain

import "testing"

func TestSuspendedFinalityNeedsExplicitCheckpointRatification(t *testing.T) {
	ledger, err := NewLedgerFromGenesisWithConfig(nil, testGenesis(t), LedgerConfig{FinalityDepth: 1})
	if err != nil {
		t.Fatalf("NewLedgerFromGenesisWithConfig() error = %v", err)
	}
	ledger.SetFinalitySuspended(true)
	state := ledger.State()
	head := ledger.Head()
	for height := uint64(1); height <= 3; height++ {
		proposal, nextState := testProposalFromHead(t, head, state, height, height, "provisional")
		head, err = ledger.CommitBlock(CommitBlockRequest{Proposal: proposal, NextState: nextState})
		if err != nil {
			t.Fatalf("CommitBlock(%d) error = %v", height, err)
		}
		state = nextState
	}
	if head.FinalizedHeight != 0 || !ledger.FinalitySuspended() {
		t.Fatalf("provisional head finalized_height=%d suspended=%t", head.FinalizedHeight, ledger.FinalitySuspended())
	}
	ratified, err := ledger.AdvanceFinalizedCheckpoint(head.Height, head.BlockHash)
	if err != nil {
		t.Fatalf("AdvanceFinalizedCheckpoint() error = %v", err)
	}
	if ratified.FinalizedHeight != 3 || ratified.FinalizedHash != head.BlockHash {
		t.Fatalf("ratified head = %+v", ratified)
	}
	ledger.SetFinalitySuspended(false)
	proposal, nextState := testProposalFromHead(t, ratified, state, 4, 4, "resumed")
	resumed, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposal, NextState: nextState})
	if err != nil {
		t.Fatalf("CommitBlock(resumed) error = %v", err)
	}
	if resumed.FinalizedHeight < ratified.FinalizedHeight {
		t.Fatalf("finalized height regressed from %d to %d", ratified.FinalizedHeight, resumed.FinalizedHeight)
	}
}
