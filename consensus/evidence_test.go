package consensus

import (
	"strings"
	"testing"
)

func TestDoubleVoteEvidenceTreatsCrossPhaseVoteAsConflict(t *testing.T) {
	confirmVote := testVote("alice", 34, testHash(1))
	timeoutVote := testSkipVote("alice", 34)
	timeoutVote.Slot = confirmVote.Slot
	timeoutVote.BlockHeight = confirmVote.BlockHeight

	if err := (DoubleVoteEvidence{FirstVote: confirmVote, SecondVote: timeoutVote}).Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDoubleVoteEvidenceAllowsPrepareThenConfirm(t *testing.T) {
	prepareVote := testVote("alice", 34, testHash(1))
	prepareVote.Type = VoteTypePrepare
	confirmVote := prepareVote
	confirmVote.Type = VoteTypeConfirm

	err := (DoubleVoteEvidence{FirstVote: prepareVote, SecondVote: confirmVote}).Validate()
	if err == nil || !strings.Contains(err.Error(), "different voting phases") {
		t.Fatalf("Validate() error = %v, want different voting phases", err)
	}
}
