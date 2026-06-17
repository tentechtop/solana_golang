package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"solana_golang/consensus"
	"solana_golang/structure"
	"solana_golang/utils"
)

const maxPendingEvidence = consensus.MaxSlashingEvidencePerBlock * 2

func (node *posNode) pendingEvidenceSnapshotLocked() []consensus.SlashingEvidence {
	if len(node.pendingEvidence) == 0 {
		return nil
	}
	limit := len(node.pendingEvidence)
	if limit > consensus.MaxSlashingEvidencePerBlock {
		limit = consensus.MaxSlashingEvidencePerBlock
	}
	return append([]consensus.SlashingEvidence(nil), node.pendingEvidence[:limit]...)
}

func (node *posNode) removePendingEvidence(included []consensus.SlashingEvidence) {
	if len(included) == 0 {
		return
	}
	includedKeys := make(map[string]struct{}, len(included))
	for _, evidence := range included {
		key, err := slashingEvidenceLocalKey(evidence)
		if err != nil {
			continue
		}
		includedKeys[key] = struct{}{}
	}
	if len(includedKeys) == 0 {
		return
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	remaining := node.pendingEvidence[:0]
	for _, evidence := range node.pendingEvidence {
		key, err := slashingEvidenceLocalKey(evidence)
		if err != nil {
			continue
		}
		if _, exists := includedKeys[key]; exists {
			continue
		}
		remaining = append(remaining, evidence)
	}
	node.pendingEvidence = remaining
}

func (node *posNode) observeProposalForEvidence(
	ctx context.Context,
	proposal consensus.BlockProposal,
	proposalHash structure.Hash,
) {
	if err := node.verifyProposalSignatureForEvidence(proposal); err != nil {
		node.logger.Debug("posnode proposal skipped for evidence",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.String("leader_id", string(proposal.Header.LeaderID)),
			slog.Any("error", err),
		)
		return
	}
	choiceKey := fmt.Sprintf("proposal/%d/%s", proposal.Header.Slot, proposal.Header.LeaderID)
	node.mutex.Lock()
	if node.proposalChoices == nil {
		node.proposalChoices = make(map[string]consensus.BlockProposal)
	}
	existingProposal, exists := node.proposalChoices[choiceKey]
	if !exists {
		node.proposalChoices[choiceKey] = proposal
		node.mutex.Unlock()
		return
	}
	node.mutex.Unlock()

	existingHash, err := existingProposal.Hash()
	if err != nil || existingHash == proposalHash {
		return
	}
	evidence := consensus.SlashingEvidence{
		Type: consensus.SlashingEvidenceTypeDoubleProposal,
		DoubleProposal: &consensus.DoubleProposalEvidence{
			FirstProposal:  existingProposal,
			SecondProposal: proposal,
		},
	}
	added, err := node.addPendingSlashingEvidence(evidence)
	if err != nil {
		node.logger.Warn("posnode double proposal evidence rejected",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.String("leader_id", string(proposal.Header.LeaderID)),
			slog.Any("error", err),
		)
		return
	}
	if added {
		node.logger.Warn("posnode double proposal evidence queued",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.String("leader_id", string(proposal.Header.LeaderID)),
			slog.String("first_hash", existingHash.String()),
			slog.String("second_hash", proposalHash.String()),
		)
		node.broadcastEvidence(ctx, evidence, "")
	}
}

func (node *posNode) verifyProposalSignatureForEvidence(proposal consensus.BlockProposal) error {
	node.mutex.Lock()
	if err := node.ensureEpochForSlotLocked(proposal.Header.Slot); err != nil {
		node.mutex.Unlock()
		return err
	}
	leader, exists := node.epochSnapshot.ValidatorByID(proposal.Header.LeaderID)
	node.mutex.Unlock()
	if !exists {
		return fmt.Errorf("posnode: proposal leader not in snapshot")
	}
	return proposal.VerifyLeaderSignature(leader.ConsensusPublicKey)
}

func (node *posNode) observeSignedVoteForEvidence(ctx context.Context, envelope voteEnvelope) {
	signedVote := consensus.SignedVote{
		Vote:      envelope.Vote,
		PublicKey: envelope.PublicKey,
		Signature: envelope.Signature,
	}
	if err := signedVote.Validate(); err != nil {
		node.logger.Debug("posnode signed vote skipped for evidence", slog.Any("error", err))
		return
	}
	choiceKey := fmt.Sprintf("vote/%d/%s", envelope.Vote.Slot, envelope.Vote.VoterID)
	node.mutex.Lock()
	if node.signedVoteChoices == nil {
		node.signedVoteChoices = make(map[string]consensus.SignedVote)
	}
	existingVote, exists := node.signedVoteChoices[choiceKey]
	if !exists {
		node.signedVoteChoices[choiceKey] = signedVote
		node.mutex.Unlock()
		return
	}
	node.mutex.Unlock()
	if sameVoteChoice(existingVote.Vote, signedVote.Vote) {
		return
	}
	evidence := consensus.SlashingEvidence{
		Type: consensus.SlashingEvidenceTypeDoubleVote,
		DoubleVote: &consensus.SignedDoubleVoteEvidence{
			FirstVote:  existingVote,
			SecondVote: signedVote,
		},
	}
	added, err := node.addPendingSlashingEvidence(evidence)
	if err != nil {
		node.logger.Warn("posnode double vote evidence rejected",
			slog.Uint64("slot", envelope.Vote.Slot),
			slog.String("validator_id", envelope.Vote.VoterID),
			slog.Any("error", err),
		)
		return
	}
	if added {
		node.logger.Warn("posnode double vote evidence queued",
			slog.Uint64("slot", envelope.Vote.Slot),
			slog.String("validator_id", envelope.Vote.VoterID),
			slog.String("first_hash", existingVote.Vote.BlockHash.String()),
			slog.String("second_hash", envelope.Vote.BlockHash.String()),
		)
		node.broadcastEvidence(ctx, evidence, "")
	}
}

func (node *posNode) addPendingSlashingEvidence(evidence consensus.SlashingEvidence) (bool, error) {
	node.mutex.Lock()
	snapshot := node.epochSnapshot
	node.mutex.Unlock()
	if _, _, err := evidence.Validate(snapshot); err != nil {
		return false, err
	}
	key, err := slashingEvidenceLocalKey(evidence)
	if err != nil {
		return false, err
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if node.seenEvidence == nil {
		node.seenEvidence = make(map[string]struct{})
	}
	if _, exists := node.seenEvidence[key]; exists {
		return false, nil
	}
	if len(node.pendingEvidence) >= maxPendingEvidence {
		return false, fmt.Errorf("posnode: pending evidence pool full")
	}
	node.seenEvidence[key] = struct{}{}
	node.pendingEvidence = append(node.pendingEvidence, evidence)
	return true, nil
}

func (node *posNode) broadcastEvidence(
	ctx context.Context,
	evidence consensus.SlashingEvidence,
	excludedPeerID string,
) {
	if node.host == nil {
		return
	}
	message, err := encodeEvidenceMessage(evidence)
	if err != nil {
		node.logger.Warn("posnode encode evidence failed", slog.Any("error", err))
		return
	}
	peerIDs := node.validatorPeerIDsSnapshot(true)
	targets := make([]string, 0, len(peerIDs))
	for _, peerID := range peerIDs {
		if peerID == "" || peerID == excludedPeerID {
			continue
		}
		targets = append(targets, peerID)
	}
	if len(targets) == 0 {
		return
	}
	if err := node.host.Broadcast(ctx, targets, message); err != nil {
		node.logger.Warn("posnode broadcast evidence failed",
			slog.Int("peer_count", len(targets)),
			slog.Any("error", err),
		)
	}
}

func slashingEvidenceLocalKey(evidence consensus.SlashingEvidence) (string, error) {
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	return utils.BytesToHex(utils.SHA256(encoded)), nil
}

func sameVoteChoice(left consensus.Vote, right consensus.Vote) bool {
	return left.Type == right.Type &&
		left.Slot == right.Slot &&
		left.BlockHeight == right.BlockHeight &&
		left.BlockHash == right.BlockHash &&
		left.VoterID == right.VoterID
}
