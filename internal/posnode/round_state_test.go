package posnode

import (
	"context"
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestDurableLocalProposalRestoresExactChoiceAndRejectsConflict(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	prepareProposalTestNode(t, node)
	proposal := produceProposalForLocalLeader(t, node)
	persisted, err := node.prepareDurableLocalProposal(proposal)
	if err != nil {
		t.Fatalf("prepareDurableLocalProposal() error = %v", err)
	}

	recovered := recoveredRoundStateTestNode(node, db)
	if err := recovered.loadPersistedLocalProposals(); err != nil {
		t.Fatalf("loadPersistedLocalProposals() error = %v", err)
	}
	if recovered.lastProducedSlot != proposal.Header.Slot {
		t.Fatalf("lastProducedSlot = %d, want %d", recovered.lastProducedSlot, proposal.Header.Slot)
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	restored, exists := recovered.proposalChoices[proposalChoiceKey(proposal.Header.Slot, validatorID)]
	if !exists {
		t.Fatal("persisted local proposal choice was not restored")
	}
	same, err := sameProposalChoice(persisted, restored)
	if err != nil || !same {
		t.Fatalf("restored proposal differs from persisted proposal: same=%v error=%v", same, err)
	}

	conflicting, _, err := recovered.buildConflictingProposal(proposal)
	if err != nil {
		t.Fatalf("buildConflictingProposal() error = %v", err)
	}
	if _, err := recovered.prepareDurableLocalProposal(conflicting); err == nil {
		t.Fatal("prepareDurableLocalProposal(conflict) error = nil")
	}
}

func TestCachedProposalReplaysOriginalDurableVote(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	prepareProposalTestNode(t, node)
	proposal := produceProposalForLocalLeader(t, node)
	proposalHash, err := proposal.Hash()
	if err != nil {
		t.Fatalf("proposal.Hash() error = %v", err)
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(node.epochSnapshot)
	if !active || stakeValue == 0 {
		t.Fatalf("local validator active=%v stake=%d, want active stake", active, stakeValue)
	}
	vote := consensus.Vote{
		Type:               consensus.VoteTypePrepare,
		Slot:               proposal.Header.Slot,
		BlockHeight:        proposal.Header.Height,
		BlockHash:          proposalHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	envelope, err := node.newLocalVoteEnvelope(vote)
	if err != nil {
		t.Fatalf("newLocalVoteEnvelope() error = %v", err)
	}
	persisted, err := node.prepareDurableLocalVoteEnvelope(envelope)
	if err != nil {
		t.Fatalf("prepareDurableLocalVoteEnvelope() error = %v", err)
	}

	handled, err := node.replayCachedLocalVoteForProposal(context.Background(), proposal)
	if err != nil {
		t.Fatalf("replayCachedLocalVoteForProposal() error = %v", err)
	}
	if !handled {
		t.Fatal("replayCachedLocalVoteForProposal() handled = false")
	}
	node.mutex.Lock()
	replayed, exists := node.cachedLocalPrepareVoteEnvelopeLocked(vote.Slot, vote.BlockHash, validatorID)
	node.mutex.Unlock()
	if !exists || replayed.Signature != persisted.Signature || !sameVoteChoice(replayed.Vote, persisted.Vote) {
		t.Fatal("cached proposal replay changed the durable vote choice or signature")
	}
}

func TestDurablePrepareVoteAllowsSameSlotTimeoutAndRestoresBoth(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(node.epochSnapshot)
	if !active || stakeValue == 0 {
		t.Fatalf("local validator active=%v stake=%d, want active stake", active, stakeValue)
	}
	slot := node.epochSnapshot.StartSlot
	prepareVote := consensus.Vote{
		Type:               consensus.VoteTypePrepare,
		Slot:               slot,
		BlockHeight:        node.ledger.Head().Height + 1,
		BlockHash:          testHashFromText(t, "durable-prepare"),
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	timeoutVote := prepareVote
	timeoutVote.Type = consensus.VoteTypeSkip
	timeoutVote.BlockHash = node.ledger.Head().BlockHash
	timeoutVote.CreatedAtUnixMilli++
	for _, vote := range []consensus.Vote{prepareVote, timeoutVote} {
		envelope, envelopeErr := node.newLocalVoteEnvelope(vote)
		if envelopeErr != nil {
			t.Fatalf("newLocalVoteEnvelope(%d) error = %v", vote.Type, envelopeErr)
		}
		if _, persistErr := node.prepareDurableLocalVoteEnvelope(envelope); persistErr != nil {
			t.Fatalf("prepareDurableLocalVoteEnvelope(%d) error = %v", vote.Type, persistErr)
		}
	}

	recovered := recoveredRoundStateTestNode(node, db)
	if err := recovered.loadPersistedLocalVoteEnvelopes(); err != nil {
		t.Fatalf("loadPersistedLocalVoteEnvelopes() error = %v", err)
	}
	if recovered.lastPreparedSlot != slot || recovered.lastTimeoutVotedSlot != slot {
		t.Fatalf("recovered watermarks prepare=%d timeout=%d, want %d/%d", recovered.lastPreparedSlot, recovered.lastTimeoutVotedSlot, slot, slot)
	}
	recovered.mutex.Lock()
	_, hasPrepare := recovered.cachedLocalPrepareVoteEnvelopeLocked(slot, prepareVote.BlockHash, validatorID)
	decision, hasDecision := recovered.cachedDecisionLocalVoteEnvelopeLocked(slot, validatorID)
	recovered.mutex.Unlock()
	if !hasPrepare || !hasDecision || decision.Vote.Type != consensus.VoteTypeSkip {
		t.Fatalf("restored prepare=%v decision=%v type=%d, want prepare and timeout", hasPrepare, hasDecision, decision.Vote.Type)
	}
}

func TestConfirmedVoteTimeoutReplaysCandidateAfterRestart(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	prepareProposalTestNode(t, node)
	proposal, nextState := produceProposalAndStateForLocalLeader(t, node)
	proposalHash, err := node.ledger.SaveBlockCandidate(blockchain.CommitBlockRequest{
		Proposal:  proposal,
		NextState: nextState,
	})
	if err != nil {
		t.Fatalf("SaveBlockCandidate() error = %v", err)
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(node.epochSnapshot)
	if !active || stakeValue == 0 {
		t.Fatalf("local validator active=%v stake=%d, want active stake", active, stakeValue)
	}
	vote := consensus.Vote{
		Type:               consensus.VoteTypeConfirm,
		Slot:               proposal.Header.Slot,
		BlockHeight:        proposal.Header.Height,
		BlockHash:          proposalHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	envelope, err := node.newLocalVoteEnvelope(vote)
	if err != nil {
		t.Fatalf("newLocalVoteEnvelope() error = %v", err)
	}
	if _, err := node.prepareDurableLocalVoteEnvelope(envelope); err != nil {
		t.Fatalf("prepareDurableLocalVoteEnvelope() error = %v", err)
	}

	recovered := recoveredRoundStateTestNode(node, db)
	if err := recovered.loadPersistedLocalVoteEnvelopes(); err != nil {
		t.Fatalf("loadPersistedLocalVoteEnvelopes() error = %v", err)
	}
	head := recovered.ledger.Head()
	if proposal.TimeoutQC != nil {
		timeoutQC := *proposal.TimeoutQC
		recovered.latestTimeoutQC = &timeoutQC
	}
	recovered.roundSlot = proposal.Header.Slot
	recovered.roundParentHash = head.BlockHash
	recovered.roundStartedAt = time.Now().Add(-recovered.roundLeaderTimeout(head.Slot, proposal.Header.Slot))
	if !recovered.replayConfirmedProposalAfterTimeout(context.Background(), proposal.Header.Slot, head, time.Now()) {
		t.Fatal("replayConfirmedProposalAfterTimeout() = false for durable confirm vote")
	}
	if recovered.roundLockedProposalReplayAt.IsZero() {
		t.Fatal("locked proposal replay timestamp was not recorded")
	}
	if recovered.lastTimeoutVotedSlot != 0 {
		t.Fatalf("lastTimeoutVotedSlot = %d, want no cross-phase timeout vote", recovered.lastTimeoutVotedSlot)
	}
}

func TestDurableLocalVoteRestoresChoiceAndRejectsConflict(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(node.epochSnapshot)
	if !active || stakeValue == 0 {
		t.Fatalf("local validator active=%v stake=%d, want active stake", active, stakeValue)
	}
	vote := consensus.Vote{
		Type:               consensus.VoteTypeConfirm,
		Slot:               node.epochSnapshot.StartSlot,
		BlockHeight:        node.ledger.Head().Height + 1,
		BlockHash:          testHashFromText(t, "durable-local-vote"),
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	envelope, err := node.newLocalVoteEnvelope(vote)
	if err != nil {
		t.Fatalf("newLocalVoteEnvelope() error = %v", err)
	}
	if _, err := node.prepareDurableLocalVoteEnvelope(envelope); err != nil {
		t.Fatalf("prepareDurableLocalVoteEnvelope() error = %v", err)
	}

	recovered := recoveredRoundStateTestNode(node, db)
	if err := recovered.loadPersistedLocalVoteEnvelopes(); err != nil {
		t.Fatalf("loadPersistedLocalVoteEnvelopes() error = %v", err)
	}
	if recovered.lastVotedSlot != vote.Slot {
		t.Fatalf("lastVotedSlot = %d, want %d", recovered.lastVotedSlot, vote.Slot)
	}
	recovered.mutex.Lock()
	restoredEnvelope, exists := recovered.cachedLocalVoteEnvelopeLocked(vote.Slot, vote.BlockHash, validatorID)
	recovered.mutex.Unlock()
	if !exists || !sameVoteChoice(restoredEnvelope.Vote, vote) {
		t.Fatal("persisted local vote choice was not restored")
	}

	conflictingVote := vote
	conflictingVote.BlockHash = testHashFromText(t, "conflicting-durable-local-vote")
	conflictingEnvelope, err := recovered.newLocalVoteEnvelope(conflictingVote)
	if err != nil {
		t.Fatalf("newLocalVoteEnvelope(conflict) error = %v", err)
	}
	if _, err := recovered.prepareDurableLocalVoteEnvelope(conflictingEnvelope); err == nil {
		t.Fatal("prepareDurableLocalVoteEnvelope(conflict) error = nil")
	}
	crossPhaseVote := vote
	crossPhaseVote.Type = consensus.VoteTypeSkip
	crossPhaseVote.BlockHash = node.ledger.Head().BlockHash
	crossPhaseEnvelope, err := recovered.newLocalVoteEnvelope(crossPhaseVote)
	if err != nil {
		t.Fatalf("newLocalVoteEnvelope(cross phase) error = %v", err)
	}
	if _, err := recovered.prepareDurableLocalVoteEnvelope(crossPhaseEnvelope); err == nil {
		t.Fatal("prepareDurableLocalVoteEnvelope(cross phase) error = nil")
	}
}

func TestDurableVoteWatermarksSurviveJournalPruning(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(node.epochSnapshot)
	if !active || stakeValue == 0 {
		t.Fatalf("local validator active=%v stake=%d, want active stake", active, stakeValue)
	}
	slot := node.epochSnapshot.StartSlot
	votes := []consensus.Vote{
		{
			Type:               consensus.VoteTypeConfirm,
			Slot:               slot,
			BlockHeight:        node.ledger.Head().Height + 1,
			BlockHash:          testHashFromText(t, "watermark-confirm"),
			VoterID:            string(validatorID),
			Stake:              stakeValue,
			CreatedAtUnixMilli: time.Now().UnixMilli(),
		},
		{
			Type:               consensus.VoteTypeSkip,
			Slot:               slot + 1,
			BlockHeight:        node.ledger.Head().Height + 1,
			BlockHash:          node.ledger.Head().BlockHash,
			VoterID:            string(validatorID),
			Stake:              stakeValue,
			CreatedAtUnixMilli: time.Now().UnixMilli(),
		},
	}
	for _, vote := range votes {
		envelope, envelopeErr := node.newLocalVoteEnvelope(vote)
		if envelopeErr != nil {
			t.Fatalf("newLocalVoteEnvelope(%d) error = %v", vote.Type, envelopeErr)
		}
		if _, prepareErr := node.prepareDurableLocalVoteEnvelope(envelope); prepareErr != nil {
			t.Fatalf("prepareDurableLocalVoteEnvelope(%d) error = %v", vote.Type, prepareErr)
		}
		if deleteErr := db.Delete(database.TableCheckpoint, localVoteEnvelopeStateKey(vote)); deleteErr != nil {
			t.Fatalf("Delete(local vote %d) error = %v", vote.Type, deleteErr)
		}
	}

	recovered := recoveredRoundStateTestNode(node, db)
	if err := recovered.loadPersistedLocalVoteEnvelopes(); err != nil {
		t.Fatalf("loadPersistedLocalVoteEnvelopes() error = %v", err)
	}
	if recovered.lastVotedSlot != slot || recovered.lastTimeoutVotedSlot != slot+1 {
		t.Fatalf("recovered watermarks = confirm %d timeout %d, want %d/%d", recovered.lastVotedSlot, recovered.lastTimeoutVotedSlot, slot, slot+1)
	}
	for _, vote := range votes {
		envelope, envelopeErr := recovered.newLocalVoteEnvelope(vote)
		if envelopeErr != nil {
			t.Fatalf("newLocalVoteEnvelope(recovered %d) error = %v", vote.Type, envelopeErr)
		}
		if _, prepareErr := recovered.prepareDurableLocalVoteEnvelope(envelope); prepareErr == nil {
			t.Fatalf("prepareDurableLocalVoteEnvelope(pruned %d) error = nil", vote.Type)
		}
	}
}

func recoveredRoundStateTestNode(node *posNode, db database.Database) *posNode {
	return &posNode{
		config:              node.config,
		logger:              node.logger,
		db:                  db,
		ledger:              node.ledger,
		consensusKeyPair:    node.consensusKeyPair,
		blsKeyPair:          node.blsKeyPair,
		epochSnapshot:       node.epochSnapshot,
		leaderSchedule:      node.leaderSchedule,
		proposalChoices:     make(map[string]consensus.BlockProposal),
		voteEnvelopeChoices: make(map[string]voteEnvelope),
		voteCollectors:      make(map[uint64]*consensus.VoteCollector),
		prepareQCs:          make(map[structure.Hash]consensus.QuorumCertificate),
		epochSnapshots:      make(map[uint64]consensus.EpochSnapshot),
		leaderSchedules:     make(map[uint64]consensus.LeaderSchedule),
	}
}

func TestLocalVoteBroadcastRateLimitSeparatesRecoveryRoute(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	vote := consensus.Vote{
		Type:               consensus.VoteTypeConfirm,
		Slot:               9,
		BlockHeight:        8,
		BlockHash:          structure.Hash{1},
		VoterID:            string(consensus.NewValidatorID(node.consensusKeyPair.PublicKey)),
		Stake:              10,
		CreatedAtUnixMilli: 1,
	}
	envelope, err := node.newLocalVoteEnvelope(vote)
	if err != nil {
		t.Fatalf("newLocalVoteEnvelope() error = %v", err)
	}
	now := time.Unix(100, 0)
	retryAfter := 3 * time.Second

	regular, recovery := node.claimLocalVoteBroadcastRoutes(envelope, false, now, retryAfter)
	if !regular || recovery {
		t.Fatalf("first normal claim = %t/%t, want true/false", regular, recovery)
	}
	regular, recovery = node.claimLocalVoteBroadcastRoutes(envelope, false, now.Add(time.Second), retryAfter)
	if regular || recovery {
		t.Fatalf("duplicate normal claim = %t/%t, want false/false", regular, recovery)
	}
	regular, recovery = node.claimLocalVoteBroadcastRoutes(envelope, true, now.Add(time.Second), retryAfter)
	if regular || !recovery {
		t.Fatalf("first recovery claim = %t/%t, want false/true", regular, recovery)
	}
	regular, recovery = node.claimLocalVoteBroadcastRoutes(envelope, true, now.Add(2*time.Second), retryAfter)
	if regular || recovery {
		t.Fatalf("duplicate recovery claim = %t/%t, want false/false", regular, recovery)
	}
	regular, recovery = node.claimLocalVoteBroadcastRoutes(envelope, true, now.Add(4*time.Second), retryAfter)
	if !regular || !recovery {
		t.Fatalf("expired claim = %t/%t, want true/true", regular, recovery)
	}

	prepareVote := vote
	prepareVote.Type = consensus.VoteTypePrepare
	prepareEnvelope, err := node.newLocalVoteEnvelope(prepareVote)
	if err != nil {
		t.Fatalf("newLocalVoteEnvelope(prepare) error = %v", err)
	}
	regular, recovery = node.claimLocalVoteBroadcastRoutes(prepareEnvelope, false, now.Add(4*time.Second), retryAfter)
	if !regular || recovery {
		t.Fatalf("separate prepare phase claim = %t/%t, want true/false", regular, recovery)
	}
}
