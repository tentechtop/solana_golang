package posnode

import (
	"context"
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
)

func TestPrepareQCTriggersConfirmWithoutCommittingUntilConfirmQC(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	prepareProposalTestNode(t, node)
	proposal, nextState := produceProposalAndStateForLocalLeader(t, node)
	blockHash, err := node.ledger.SaveBlockCandidate(blockchain.CommitBlockRequest{
		Proposal:  proposal,
		NextState: nextState,
	})
	if err != nil {
		t.Fatalf("SaveBlockCandidate() error = %v", err)
	}
	headBefore := node.ledger.Head()
	node.roundSlot = proposal.Header.Slot
	node.roundParentHash = headBefore.BlockHash
	node.roundStartedAt = time.Now().Add(-time.Minute)
	phaseStartedAt := node.roundStartedAt
	prepareQC := collectConsensusStatusQC(
		t,
		node.epochSnapshot,
		consensus.VoteTypePrepare,
		proposal.Header.Slot,
		proposal.Header.Height,
		blockHash,
	)
	if err := node.acceptPrepareQC(context.Background(), prepareQC); err != nil {
		t.Fatalf("acceptPrepareQC() error = %v", err)
	}
	if headAfterPrepare := node.ledger.Head(); headAfterPrepare != headBefore {
		t.Fatalf("head changed after PrepareQC: got %+v, want %+v", headAfterPrepare, headBefore)
	}
	if !node.roundStartedAt.After(phaseStartedAt) {
		t.Fatalf("confirm phase deadline was not reset: got %s, previous %s", node.roundStartedAt, phaseStartedAt)
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	node.mutex.Lock()
	confirmEnvelope, hasConfirm := node.cachedLocalVoteEnvelopeLocked(proposal.Header.Slot, blockHash, validatorID)
	node.mutex.Unlock()
	if !hasConfirm || confirmEnvelope.Vote.Type != consensus.VoteTypeConfirm {
		t.Fatal("PrepareQC did not create the local Confirm vote")
	}

	confirmQC := collectConsensusStatusQC(
		t,
		node.epochSnapshot,
		consensus.VoteTypeConfirm,
		proposal.Header.Slot,
		proposal.Header.Height,
		blockHash,
	)
	message, err := encodeQCEnvelopeMessage(qcEnvelope{
		QC:              confirmQC,
		RootValidatorID: string(proposal.Header.LeaderID),
		OriginPeerID:    node.peerKeyPair.peerID,
		HopCount:        1,
		MaxHops:         1,
	})
	if err != nil {
		t.Fatalf("encodeQCEnvelopeMessage() error = %v", err)
	}
	message.FromPeerID = node.peerKeyPair.peerID
	if err := node.handleQCMessage(context.Background(), message); err != nil {
		t.Fatalf("handleQCMessage(confirm) error = %v", err)
	}
	headAfterConfirm := node.ledger.Head()
	if headAfterConfirm.Height != proposal.Header.Height || headAfterConfirm.BlockHash != blockHash {
		t.Fatalf("head after ConfirmQC = %d/%s, want %d/%s", headAfterConfirm.Height, headAfterConfirm.BlockHash.String(), proposal.Header.Height, blockHash.String())
	}
}
