package main

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

	deadline := startedAt.Add(2 * time.Second).Add(700 * time.Millisecond)
	if node.slotDeadlinePassed(3, deadline.Add(-time.Nanosecond)) {
		t.Fatal("slotDeadlinePassed() before deadline = true, want false")
	}
	if !node.slotDeadlinePassed(3, deadline) {
		t.Fatal("slotDeadlinePassed() at deadline = false, want true")
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
