package posnode

import (
	"context"
	"fmt"
	"testing"
	"time"

	"solana_golang/consensus"
	"solana_golang/structure"
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

func TestSlotProductionBudgetUsesBoundedRemainingTime(t *testing.T) {
	shortSlotNode := &posNode{config: nodeConfig{SlotMillis: 400}}
	if got := shortSlotNode.slotProductionMinRemaining(); got != 400*time.Millisecond/3 {
		t.Fatalf("short slot production remaining = %s, want %s", got, 400*time.Millisecond/3)
	}

	longSlotNode := &posNode{config: nodeConfig{SlotMillis: 5000}}
	if got := longSlotNode.slotProductionMinRemaining(); got != 750*time.Millisecond {
		t.Fatalf("long slot production remaining = %s, want 750ms", got)
	}
}

func TestSlotProductionBudgetAvailableRequiresVoteBudget(t *testing.T) {
	startedAt := time.UnixMilli(1_700_000_000_000)
	node := &posNode{
		config: nodeConfig{
			SlotMillis:     400,
			GenesisStartMs: startedAt.UnixMilli(),
		},
	}
	slotStart := node.slotStartTime(2)

	if !node.slotProductionBudgetAvailable(2, slotStart.Add(226*time.Millisecond)) {
		t.Fatal("slotProductionBudgetAvailable() early = false, want true")
	}
	if node.slotProductionBudgetAvailable(2, slotStart.Add(227*time.Millisecond)) {
		t.Fatal("slotProductionBudgetAvailable() at exact budget boundary = true, want false")
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

func TestProductionSyncGateTimeoutKeepsSlotDeadlineBudget(t *testing.T) {
	shortDeadline := time.Now().Add(40 * time.Millisecond)
	if got := productionSyncGateTimeout(shortDeadline); got != 0 {
		t.Fatalf("short deadline sync gate timeout = %s, want 0", got)
	}

	normalDeadline := time.Now().Add(400 * time.Millisecond)
	if got := productionSyncGateTimeout(normalDeadline); got != productionSyncGateMaxTimeout {
		t.Fatalf("normal deadline sync gate timeout = %s, want %s", got, productionSyncGateMaxTimeout)
	}
}

func TestRotateLimitedPeerIDsUsesSeedAndLimit(t *testing.T) {
	peers := []string{"a", "b", "c", "d"}
	got := rotateLimitedPeerIDs(peers, 2, 2)
	if fmt.Sprint(got) != "[c d]" {
		t.Fatalf("rotated peers = %v, want [c d]", got)
	}

	got = rotateLimitedPeerIDs(peers, 3, 3)
	if fmt.Sprint(got) != "[d a b]" {
		t.Fatalf("wrapped peers = %v, want [d a b]", got)
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

func TestPublicRPCNodeStoresProposalWithoutVoting(t *testing.T) {
	producerNode := newConsensusStatusTestNode(t)
	publicNode := newConsensusStatusTestNode(t)
	disableValidator := false
	publicNode.config.ValidatorEnabled = &disableValidator
	publicNode.config.ConsensusEnabled = &disableValidator
	prepareProposalTestNode(t, producerNode)
	prepareProposalTestNode(t, publicNode)

	proposal := produceProposalForLocalLeader(t, producerNode)
	headBefore := publicNode.ledger.Head()
	if err := publicNode.voteForProposal(context.Background(), proposal); err != nil {
		t.Fatalf("voteForProposal() error = %v", err)
	}
	headAfter := publicNode.ledger.Head()
	if headAfter.Height != headBefore.Height {
		t.Fatalf("head height = %d, want %d", headAfter.Height, headBefore.Height)
	}
	if got := publicNode.metrics.proposalsAccepted.Load(); got != 0 {
		t.Fatalf("proposalsAccepted = %d, want 0", got)
	}
	if got := publicNode.metrics.votesSent.Load(); got != 0 {
		t.Fatalf("votesSent = %d, want 0", got)
	}
	if publicNode.lastVotedSlot != 0 {
		t.Fatalf("lastVotedSlot = %d, want 0", publicNode.lastVotedSlot)
	}
}

func prepareProposalTestNode(t *testing.T, node *posNode) {
	t.Helper()
	executor, err := newRuntimeExecutor(node.logger)
	if err != nil {
		t.Fatalf("newRuntimeExecutor() error = %v", err)
	}
	head := node.ledger.Head()
	node.executor = executor
	node.blockhashQueue = structure.NewBlockhashQueue(150)
	if err := node.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     head.BlockHash,
		Slot:          head.Slot,
		FeeCalculator: structure.DefaultFeeCalculator(),
		TimestampUnix: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("blockhash add: %v", err)
	}
}

func produceProposalForLocalLeader(t *testing.T, node *posNode) consensus.BlockProposal {
	t.Helper()
	slot := firstLocalLeaderSlot(t, node)
	head := node.ledger.Head()
	request := consensus.ProduceBlockRequest{
		Slot:           slot,
		ParentSlot:     head.Slot,
		Height:         head.Height + 1,
		EpochSnapshot:  node.epochSnapshot,
		Schedule:       node.leaderSchedule,
		ParentHash:     head.BlockHash,
		PreviousQCHash: head.QCHash,
		ParentState:    node.ledger.State(),
		BlockhashQueue: node.blockhashQueue,
		LeaderKeyPair:  node.consensusKeyPair,
		RewardConfig:   consensus.DefaultRewardConfig(),
	}
	producer := consensus.BlockProducer{ChainID: node.config.ChainID, Executor: node.executor}
	proposal, _, err := producer.ProduceBlock(context.Background(), request)
	if err != nil {
		t.Fatalf("ProduceBlock() error = %v", err)
	}
	return proposal
}

func firstLocalLeaderSlot(t *testing.T, node *posNode) uint64 {
	t.Helper()
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	for slot := node.epochSnapshot.StartSlot; slot <= node.epochSnapshot.EndSlot; slot++ {
		leaderID, err := node.leaderSchedule.LeaderForSlot(slot)
		if err != nil {
			t.Fatalf("LeaderForSlot(%d) error = %v", slot, err)
		}
		if leaderID == localValidatorID {
			return slot
		}
	}
	t.Fatalf("local validator %s has no leader slot", localValidatorID)
	return 0
}
