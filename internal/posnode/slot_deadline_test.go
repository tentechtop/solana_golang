package posnode

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestSlotSkipTimeoutCoversConsensusAndRecoveryStages(t *testing.T) {
	node := &posNode{config: nodeConfig{SlotMillis: 400}}

	if got := node.slotSkipTimeout(); got != 3200*time.Millisecond {
		t.Fatalf("slotSkipTimeout() = %s, want 3.2s", got)
	}

	veryShortSlotNode := &posNode{config: nodeConfig{SlotMillis: 200}}
	if got := veryShortSlotNode.slotSkipTimeout(); got != 2200*time.Millisecond {
		t.Fatalf("very short slot timeout = %s, want recovery floor 2.2s", got)
	}
}

func TestRoundLeaderTimeoutBacksOffOnlyAfterCertifiedTimeoutRounds(t *testing.T) {
	node := &posNode{config: nodeConfig{SlotMillis: 400}}
	tests := []struct {
		roundSlot uint64
		want      time.Duration
	}{
		{roundSlot: 11, want: 3200 * time.Millisecond},
		{roundSlot: 12, want: 6400 * time.Millisecond},
		{roundSlot: 13, want: 12800 * time.Millisecond},
		{roundSlot: 14, want: 25600 * time.Millisecond},
		{roundSlot: 20, want: 25600 * time.Millisecond},
	}
	for _, testCase := range tests {
		if got := node.roundLeaderTimeout(10, testCase.roundSlot); got != testCase.want {
			t.Fatalf("roundLeaderTimeout(10, %d) = %s, want %s", testCase.roundSlot, got, testCase.want)
		}
	}
}

func TestRoundLeaderTimeoutDoesNotBackOffForKnownUnavailableLeader(t *testing.T) {
	baseTimeout := 4 * time.Second
	for skippedRounds := uint64(1); skippedRounds <= 8; skippedRounds++ {
		if got := roundLeaderTimeoutForAvailability(baseTimeout, skippedRounds, true); got != baseTimeout {
			t.Fatalf("unavailable leader timeout after %d skipped rounds = %s, want %s", skippedRounds, got, baseTimeout)
		}
	}
	if got := roundLeaderTimeoutForAvailability(baseTimeout, 3, false); got != 32*time.Second {
		t.Fatalf("reachable leader timeout after three skipped rounds = %s, want 32s backoff", got)
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

func TestRoundProposalReadyAtThrottlesSingleValidatorOnly(t *testing.T) {
	startedAt := time.Unix(100, 0)
	minimumInterval := 500 * time.Millisecond

	if roundProposalReadyAt(startedAt.Add(499*time.Millisecond), startedAt, minimumInterval, 1) {
		t.Fatal("single-validator proposal became ready before the minimum interval")
	}
	if !roundProposalReadyAt(startedAt.Add(minimumInterval), startedAt, minimumInterval, 1) {
		t.Fatal("single-validator proposal did not become ready at the minimum interval")
	}
	if !roundProposalReadyAt(startedAt, startedAt, minimumInterval, 2) {
		t.Fatal("multi-validator proposal was throttled by the single-validator interval")
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

func TestAcceptTimeoutQCIgnoresValidCertificateForStaleHead(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	certificate := collectTimeoutQCForTest(
		t,
		node.epochSnapshot,
		1,
		1,
		testHashFromText(t, "stale-timeout-parent"),
	)

	if err := node.acceptTimeoutQC(certificate); err != nil {
		t.Fatalf("acceptTimeoutQC(stale) error = %v, want nil", err)
	}
	if node.latestTimeoutQC != nil {
		t.Fatal("latestTimeoutQC was updated by stale certificate")
	}
}

func TestRoundTimerResetsWhileLivenessQuorumIsUnavailable(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	now := time.UnixMilli(1_710_000_000_000)
	node.roundStartedAt = now.Add(-time.Minute)

	node.onRoundTick(context.Background(), now)

	if !node.roundStartedAt.IsZero() {
		t.Fatalf("roundStartedAt = %s, want zero while quorum is unavailable", node.roundStartedAt)
	}
	if node.lastTimeoutVotedSlot != 0 {
		t.Fatalf("lastTimeoutVotedSlot = %d, want no timeout vote before quorum is ready", node.lastTimeoutVotedSlot)
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
	if publicNode.lastTimeoutVotedSlot != 0 {
		t.Fatalf("lastTimeoutVotedSlot = %d, want 0", publicNode.lastTimeoutVotedSlot)
	}
}

func TestProposalVoteRejectedAfterSameSlotTimeoutVote(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	localValidator, exists := node.epochSnapshot.ValidatorByID(validatorID)
	if !exists {
		t.Fatalf("local validator %s not found", validatorID)
	}
	validatorSet, err := consensus.NewValidatorSet([]consensus.ValidatorState{localValidator})
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	node.epochSnapshot, err = consensus.NewEpochSnapshot(0, 1, node.config.EpochSlots, node.epochSnapshot.RandomSeed, validatorSet)
	if err != nil {
		t.Fatalf("NewEpochSnapshot() error = %v", err)
	}
	node.leaderSchedule, err = consensus.NewLeaderSchedule(node.epochSnapshot)
	if err != nil {
		t.Fatalf("NewLeaderSchedule() error = %v", err)
	}
	prepareProposalTestNode(t, node)
	proposal := produceProposalForLocalLeader(t, node)
	head := node.ledger.Head()
	stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(node.epochSnapshot)
	if !active || stakeValue == 0 {
		t.Fatalf("local validator active=%v stake=%d, want active stake", active, stakeValue)
	}
	timeoutVote := consensus.Vote{
		Type:               consensus.VoteTypeSkip,
		Slot:               proposal.Header.Slot,
		BlockHeight:        head.Height + 1,
		BlockHash:          head.BlockHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	timeoutEnvelope, err := node.newLocalVoteEnvelope(timeoutVote)
	if err != nil {
		t.Fatalf("newLocalVoteEnvelope(timeout) error = %v", err)
	}
	if !node.rememberLocalVoteEnvelope(timeoutEnvelope) {
		t.Fatal("timeout vote was not cached")
	}
	node.observeSignedVoteForEvidence(context.Background(), timeoutEnvelope)
	node.lastTimeoutVotedSlot = proposal.Header.Slot

	if err := node.voteForProposal(context.Background(), proposal); err != nil {
		t.Fatalf("voteForProposal() after timeout error = %v", err)
	}
	if node.lastVotedSlot != 0 {
		t.Fatalf("lastVotedSlot = %d, want 0", node.lastVotedSlot)
	}
	if got := node.metrics.votesSent.Load(); got != 0 {
		t.Fatalf("votesSent = %d, want 0", got)
	}
	if len(node.pendingEvidence) != 0 {
		t.Fatalf("pendingEvidence = %d, local node must not create a cross-phase signature", len(node.pendingEvidence))
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
	proposal, _ := produceProposalAndStateForLocalLeader(t, node)
	return proposal
}

func produceProposalAndStateForLocalLeader(t *testing.T, node *posNode) (consensus.BlockProposal, consensus.ChainState) {
	t.Helper()
	head := node.ledger.Head()
	slot := firstLocalLeaderSlot(t, node)
	var timeoutQC *consensus.QuorumCertificate
	if head.Slot < slot-1 {
		certificate := collectTimeoutQCForTest(t, node.epochSnapshot, slot-1, head.Height+1, head.BlockHash)
		timeoutQC = &certificate
	}
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
		TimeoutQC:      timeoutQC,
	}
	producer := consensus.BlockProducer{ChainID: node.config.ChainID, Executor: node.executor}
	proposal, nextState, err := producer.ProduceBlock(context.Background(), request)
	if err != nil {
		t.Fatalf("ProduceBlock() error = %v", err)
	}
	return proposal, nextState
}

func collectTimeoutQCForTest(
	t *testing.T,
	snapshot consensus.EpochSnapshot,
	slot uint64,
	height uint64,
	parentHash structure.Hash,
) consensus.QuorumCertificate {
	t.Helper()
	return collectConsensusStatusQC(t, snapshot, consensus.VoteTypeSkip, slot, height, parentHash)
}

func firstLocalLeaderSlot(t *testing.T, node *posNode) uint64 {
	t.Helper()
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	startSlot := node.epochSnapshot.StartSlot
	head := node.ledger.Head()
	if head.Slot != ^uint64(0) && head.Slot >= startSlot {
		startSlot = head.Slot + 1
	}
	for slot := startSlot; slot <= node.epochSnapshot.EndSlot; slot++ {
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
