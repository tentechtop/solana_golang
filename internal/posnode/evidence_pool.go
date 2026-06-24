package posnode

import (
	"context"
	"fmt"
	"log/slog"

	"solana_golang/consensus"
	"solana_golang/programs/stake"
	"solana_golang/structure"
	"solana_golang/utils"
)

const maxPendingEvidence = consensus.MaxSlashingEvidencePerBlock * 2

func (node *posNode) pendingEvidenceSnapshotForSlotLocked(
	slot uint64,
	state consensus.ChainState,
) []consensus.SlashingEvidence {
	if len(node.pendingEvidence) == 0 {
		return nil
	}
	evidenceSnapshot := make([]consensus.SlashingEvidence, 0, consensus.MaxSlashingEvidencePerBlock)
	for _, evidence := range node.pendingEvidence {
		evidenceSlot, err := slashingEvidenceSlot(evidence)
		if err != nil {
			continue
		}
		if evidenceSlot > slot {
			continue
		}
		if node.slashingEvidenceAppliedToStateLocked(evidence, state) {
			continue
		}
		evidenceSnapshot = append(evidenceSnapshot, evidence)
		if len(evidenceSnapshot) >= consensus.MaxSlashingEvidencePerBlock {
			break
		}
	}
	return evidenceSnapshot
}

func (node *posNode) pruneFinalizedPendingEvidence() {
	head := node.ledger.Head()
	if head.FinalizedHash.IsZero() {
		return
	}
	finalizedState, err := node.ledger.StateAtBlockHash(head.FinalizedHash)
	if err != nil {
		node.logger.Debug("posnode finalized evidence prune skipped", slog.Any("error", err))
		return
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	remaining := node.pendingEvidence[:0]
	prunedCount := 0
	for _, evidence := range node.pendingEvidence {
		if node.slashingEvidenceAppliedToStateLocked(evidence, finalizedState) {
			prunedCount++
			continue
		}
		remaining = append(remaining, evidence)
	}
	node.pendingEvidence = remaining
	if prunedCount > 0 {
		node.logger.Info("posnode finalized slashing evidence pruned",
			slog.Int("pruned_count", prunedCount),
			slog.Int("remaining_count", len(node.pendingEvidence)),
			slog.Uint64("finalized_height", head.FinalizedHeight),
			slog.String("finalized_hash", head.FinalizedHash.String()),
		)
	}
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
	epochContextValue, err := node.epochContextForSlotLocked(proposal.Header.Slot)
	if err != nil {
		node.mutex.Unlock()
		return err
	}
	leader, exists := epochContextValue.Snapshot.ValidatorByID(proposal.Header.LeaderID)
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
	evidenceSlot, err := slashingEvidenceSlot(evidence)
	if err != nil {
		return false, err
	}
	node.mutex.Lock()
	epochContextValue, err := node.epochContextForSlotLocked(evidenceSlot)
	node.mutex.Unlock()
	if err != nil {
		return false, err
	}
	if _, _, err := evidence.Validate(epochContextValue.Snapshot); err != nil {
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

// slashingEvidenceSlot 提取证据 slot + 罚没验证必须绑定作恶发生时的 epoch 快照。
func slashingEvidenceSlot(evidence consensus.SlashingEvidence) (uint64, error) {
	switch evidence.Type {
	case consensus.SlashingEvidenceTypeDoubleProposal:
		if evidence.DoubleProposal == nil {
			return 0, fmt.Errorf("posnode: missing double proposal evidence")
		}
		return evidence.DoubleProposal.FirstProposal.Header.Slot, nil
	case consensus.SlashingEvidenceTypeDoubleVote:
		if evidence.DoubleVote == nil {
			return 0, fmt.Errorf("posnode: missing double vote evidence")
		}
		return evidence.DoubleVote.FirstVote.Vote.Slot, nil
	default:
		return 0, fmt.Errorf("posnode: unsupported slashing evidence type %v", evidence.Type)
	}
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
	encoded, err := marshalEvidenceEnvelopeBinary(evidenceEnvelope{Evidence: evidence})
	if err != nil {
		return "", err
	}
	return utils.BytesToHex(utils.SHA256(encoded)), nil
}

func (node *posNode) slashingEvidenceAppliedToStateLocked(
	evidence consensus.SlashingEvidence,
	state consensus.ChainState,
) bool {
	evidenceSlot, err := slashingEvidenceSlot(evidence)
	if err != nil {
		return false
	}
	epochContextValue, err := node.epochContextForSlotLocked(evidenceSlot)
	if err != nil {
		return false
	}
	validator, _, err := evidence.Validate(epochContextValue.Snapshot)
	if err != nil {
		return false
	}
	stakeState, found, err := stakeStateByAddress(state, validator.AccountAddress)
	if err != nil || !found {
		return false
	}
	return stakeState.LastSlashedSlot >= evidenceSlot
}

func stakeStateByAddress(
	state consensus.ChainState,
	address structure.PublicKey,
) (stake.ValidatorState, bool, error) {
	for _, account := range state.Accounts {
		if account.Address != address {
			continue
		}
		if len(account.Account.Data) == 0 {
			return stake.ValidatorState{}, false, nil
		}
		stakeState, err := stake.UnmarshalValidatorStateBinary(account.Account.Data)
		if err != nil {
			return stake.ValidatorState{}, false, err
		}
		return stakeState, true, nil
	}
	return stake.ValidatorState{}, false, nil
}

func sameVoteChoice(left consensus.Vote, right consensus.Vote) bool {
	return left.Type == right.Type &&
		left.Slot == right.Slot &&
		left.BlockHeight == right.BlockHeight &&
		left.BlockHash == right.BlockHash &&
		left.VoterID == right.VoterID
}
