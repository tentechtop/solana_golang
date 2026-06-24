package posnode

import (
	"testing"
	"time"

	"solana_golang/consensus"
	"solana_golang/structure"
)

func TestPendingEvidenceSnapshotSkipsFutureEvidence(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	evidenceSlot := node.epochSnapshot.StartSlot + 5
	evidence := signedDoubleVoteEvidenceForTest(t, node, evidenceSlot)
	added, err := node.addPendingSlashingEvidence(evidence)
	if err != nil {
		t.Fatalf("addPendingSlashingEvidence() error = %v", err)
	}
	if !added {
		t.Fatal("addPendingSlashingEvidence() added = false, want true")
	}

	state := node.ledger.State()
	node.mutex.Lock()
	earlyEvidence := node.pendingEvidenceSnapshotForSlotLocked(evidenceSlot-1, state)
	currentEvidence := node.pendingEvidenceSnapshotForSlotLocked(evidenceSlot, state)
	node.mutex.Unlock()
	if len(earlyEvidence) != 0 {
		t.Fatalf("early evidence count = %d, want 0", len(earlyEvidence))
	}
	if len(currentEvidence) != 1 {
		t.Fatalf("current evidence count = %d, want 1", len(currentEvidence))
	}
}

func TestPendingEvidenceSnapshotKeepsEvidenceUntilFinalizedSlash(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	evidenceSlot := node.epochSnapshot.StartSlot + 2
	evidence := signedDoubleVoteEvidenceForTest(t, node, evidenceSlot)
	added, err := node.addPendingSlashingEvidence(evidence)
	if err != nil {
		t.Fatalf("addPendingSlashingEvidence() error = %v", err)
	}
	if !added {
		t.Fatal("addPendingSlashingEvidence() added = false, want true")
	}

	slashedState := applyEvidenceToStateForTest(t, node, evidence, evidenceSlot)
	canonicalStateBeforeSlash := node.ledger.State()
	node.mutex.Lock()
	appliedEvidence := node.pendingEvidenceSnapshotForSlotLocked(evidenceSlot, slashedState)
	reorgEvidence := node.pendingEvidenceSnapshotForSlotLocked(evidenceSlot, canonicalStateBeforeSlash)
	pendingCount := len(node.pendingEvidence)
	node.mutex.Unlock()
	if len(appliedEvidence) != 0 {
		t.Fatalf("applied evidence count = %d, want 0", len(appliedEvidence))
	}
	if len(reorgEvidence) != 1 {
		t.Fatalf("reorg evidence count = %d, want 1", len(reorgEvidence))
	}
	if pendingCount != 1 {
		t.Fatalf("pending evidence count = %d, want retained evidence", pendingCount)
	}
}

func signedDoubleVoteEvidenceForTest(
	t *testing.T,
	node *posNode,
	slot uint64,
) consensus.SlashingEvidence {
	t.Helper()
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(node.epochSnapshot)
	if !active || stakeValue == 0 {
		t.Fatalf("local validator active=%v stake=%d, want active stake", active, stakeValue)
	}
	firstVote := consensus.Vote{
		Type:               consensus.VoteTypeConfirm,
		Slot:               slot,
		BlockHeight:        1,
		BlockHash:          testHashFromText(t, "pending-evidence-first"),
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	secondVote := firstVote
	secondVote.BlockHash = testHashFromText(t, "pending-evidence-second")
	secondVote.CreatedAtUnixMilli = firstVote.CreatedAtUnixMilli + 1
	return consensus.SlashingEvidence{
		Type: consensus.SlashingEvidenceTypeDoubleVote,
		DoubleVote: &consensus.SignedDoubleVoteEvidence{
			FirstVote:  signedVoteForEvidenceTest(t, node.consensusKeyPair, firstVote),
			SecondVote: signedVoteForEvidenceTest(t, node.consensusKeyPair, secondVote),
		},
	}
}

func signedVoteForEvidenceTest(
	t *testing.T,
	keyPair structure.SolanaKeyPair,
	vote consensus.Vote,
) consensus.SignedVote {
	t.Helper()
	voteBytes, err := vote.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	signature, err := keyPair.Sign(voteBytes)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return consensus.SignedVote{
		Vote:      vote,
		PublicKey: keyPair.PublicKey,
		Signature: signature,
	}
}

func applyEvidenceToStateForTest(
	t *testing.T,
	node *posNode,
	evidence consensus.SlashingEvidence,
	slot uint64,
) consensus.ChainState {
	t.Helper()
	leaderID, err := node.leaderSchedule.LeaderForSlot(slot)
	if err != nil {
		t.Fatalf("LeaderForSlot() error = %v", err)
	}
	leader, exists := node.epochSnapshot.ValidatorByID(leaderID)
	if !exists {
		t.Fatalf("leader %s missing from snapshot", leaderID)
	}
	nextState, rewards, err := consensus.ApplyBlockRewards(node.ledger.State(), consensus.BlockRewardInput{
		Slot:          slot,
		ParentSlot:    slot - 1,
		Height:        1,
		EpochID:       node.epochSnapshot.EpochID,
		EpochSnapshot: node.epochSnapshot,
		Schedule:      node.leaderSchedule,
		Leader:        leader,
		Evidence:      []consensus.SlashingEvidence{evidence},
		Config:        consensus.DefaultRewardConfig(),
	})
	if err != nil {
		t.Fatalf("ApplyBlockRewards() error = %v", err)
	}
	if !rewardListContainsType(rewards, consensus.RewardTypeSlash) {
		t.Fatalf("rewards = %+v, want slash reward", rewards)
	}
	return nextState
}

func rewardListContainsType(rewards []consensus.BlockReward, rewardType consensus.RewardType) bool {
	for _, reward := range rewards {
		if reward.Type == rewardType {
			return true
		}
	}
	return false
}
