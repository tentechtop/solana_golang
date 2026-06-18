package posnode

import (
	"context"
	"testing"
	"time"

	"solana_golang/consensus"
)

func TestSlotDeadlinePassedUsesSharedBoundary(t *testing.T) {
	startedAt := time.UnixMilli(1_700_000_000_000)
	node := &posNode{
		config: nodeConfig{
			SlotMillis:     1000,
			GenesisStartMs: startedAt.UnixMilli(),
		},
	}

	deadline := startedAt.Add(2 * time.Second).Add(900 * time.Millisecond)
	if node.slotDeadlinePassed(3, deadline.Add(-time.Nanosecond)) {
		t.Fatal("slotDeadlinePassed() before deadline = true, want false")
	}
	if !node.slotDeadlinePassed(3, deadline) {
		t.Fatal("slotDeadlinePassed() at deadline = false, want true")
	}
}

func TestSlotSkipTimeoutUsesNinetyPercentWindow(t *testing.T) {
	node := &posNode{config: nodeConfig{SlotMillis: 400}}

	if got := node.slotSkipTimeout(); got != 360*time.Millisecond {
		t.Fatalf("slotSkipTimeout() = %s, want 360ms", got)
	}
}

func TestSlotTickIntervalBoundsShortAndLongSlots(t *testing.T) {
	shortSlotNode := &posNode{config: nodeConfig{SlotMillis: 400}}
	if got := shortSlotNode.slotTickInterval(); got != 100*time.Millisecond {
		t.Fatalf("short slot tick interval = %s, want 100ms", got)
	}

	longSlotNode := &posNode{config: nodeConfig{SlotMillis: 5000}}
	if got := longSlotNode.slotTickInterval(); got != 500*time.Millisecond {
		t.Fatalf("long slot tick interval = %s, want 500ms", got)
	}
}

func TestPeerStatusTimeoutUsesControlPlaneBounds(t *testing.T) {
	shortSlotNode := &posNode{config: nodeConfig{SlotMillis: 400}}
	if got := shortSlotNode.peerStatusTimeout(); got != 1200*time.Millisecond {
		t.Fatalf("short slot peer status timeout = %s, want 1.2s", got)
	}

	veryShortSlotNode := &posNode{config: nodeConfig{SlotMillis: 100}}
	if got := veryShortSlotNode.peerStatusTimeout(); got != time.Second {
		t.Fatalf("very short slot peer status timeout = %s, want 1s", got)
	}

	longSlotNode := &posNode{config: nodeConfig{SlotMillis: 3000}}
	if got := longSlotNode.peerStatusTimeout(); got != 5*time.Second {
		t.Fatalf("long slot peer status timeout = %s, want 5s", got)
	}
}

func TestVoteForProposalRejectsExpiredSlot(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	node.config.GenesisStartMs = time.Now().Add(-time.Minute).UnixMilli()
	headBefore := node.ledger.Head()
	proposal := consensus.BlockProposal{
		Header: consensus.BlockHeader{
			Slot:   1,
			Height: headBefore.Height + 1,
		},
	}

	if err := node.voteForProposal(context.Background(), proposal); err != nil {
		t.Fatalf("voteForProposal() error = %v", err)
	}
	if got := node.metrics.proposalsRejected.Load(); got != 1 {
		t.Fatalf("proposalsRejected = %d, want 1", got)
	}
	if got := node.metrics.votesSent.Load(); got != 0 {
		t.Fatalf("votesSent = %d, want 0", got)
	}
	if got := node.metrics.proposalsAccepted.Load(); got != 0 {
		t.Fatalf("proposalsAccepted = %d, want 0", got)
	}
	if got := node.ledger.Head().Height; got != headBefore.Height {
		t.Fatalf("head height = %d, want %d", got, headBefore.Height)
	}
}
