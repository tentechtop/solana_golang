package posnode

import (
	"context"
	"log/slog"
	"time"

	"solana_golang/consensus"
	"solana_golang/structure"
)

const maxFaultInjectionProposalDelayMillis = int64(60_000)

// applyFaultInjectedProposalDelay 延迟本节点出块 + 用于压测 leader 超时和网络自愈路径。
func (node *posNode) applyFaultInjectedProposalDelay(ctx context.Context, slot uint64) bool {
	delayMillis := node.config.FaultInjection.ProposalDelayMillis
	if delayMillis <= 0 {
		return true
	}
	delay := time.Duration(delayMillis) * time.Millisecond
	node.logger.Warn("posnode fault injection proposal delay",
		slog.Uint64("slot", slot),
		slog.Duration("delay", delay),
	)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// injectDoubleVoteFault 发送一次冲突投票 + 验证双签证据收集和链上罚没路径。
func (node *posNode) injectDoubleVoteFault(ctx context.Context, originalVote consensus.Vote) {
	if !node.shouldInjectDoubleVoteFault(originalVote) {
		return
	}
	conflictingVote := originalVote
	conflictingVote.Type = consensus.VoteTypeSkip
	conflictingVote.BlockHeight = 0
	conflictingVote.BlockHash = structure.Hash{}
	conflictingVote.CreatedAtUnixMilli = time.Now().UnixMilli()
	envelope, err := node.newLocalVoteEnvelope(conflictingVote)
	if err != nil {
		node.logger.Warn("posnode fault injection double vote failed", slog.Any("error", err))
		return
	}
	node.logger.Warn("posnode fault injection double vote emitted",
		slog.Uint64("slot", conflictingVote.Slot),
		slog.String("validator_id", conflictingVote.VoterID),
	)
	node.broadcastVoteEnvelope(ctx, envelope)
	if err := node.handleLocalVoteEnvelope(ctx, envelope); err != nil {
		node.logger.Warn("posnode fault injection double vote local handle failed", slog.Any("error", err))
	}
}

func (node *posNode) shouldInjectDoubleVoteFault(vote consensus.Vote) bool {
	if !node.config.FaultInjection.DoubleVoteOnce || vote.Type != consensus.VoteTypeConfirm {
		return false
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if node.doubleVoteInjected {
		return false
	}
	node.doubleVoteInjected = true
	return true
}

// injectDoubleProposalFault 广播一次冲突提案证据 + 覆盖 HotStuff 恶意 leader 罚没路径。
func (node *posNode) injectDoubleProposalFault(ctx context.Context, proposal consensus.BlockProposal) {
	if !node.shouldInjectDoubleProposalFault(proposal) {
		return
	}
	conflictingProposal, conflictingHash, err := node.buildConflictingProposal(proposal)
	if err != nil {
		node.logger.Warn("posnode fault injection double proposal failed", slog.Any("error", err))
		return
	}
	originalHash, err := proposal.Hash()
	if err != nil {
		node.logger.Warn("posnode fault injection original proposal hash failed", slog.Any("error", err))
		return
	}
	evidence := consensus.SlashingEvidence{
		Type: consensus.SlashingEvidenceTypeDoubleProposal,
		DoubleProposal: &consensus.DoubleProposalEvidence{
			FirstProposal:  proposal,
			SecondProposal: conflictingProposal,
		},
	}
	added, err := node.addPendingSlashingEvidence(evidence)
	if err != nil {
		node.logger.Warn("posnode fault injection double proposal evidence rejected", slog.Any("error", err))
	} else if added {
		node.broadcastEvidence(ctx, evidence, "")
	}
	node.logger.Warn("posnode fault injection double proposal emitted",
		slog.Uint64("slot", proposal.Header.Slot),
		slog.Uint64("height", proposal.Header.Height),
		slog.String("leader_id", string(proposal.Header.LeaderID)),
		slog.String("first_hash", originalHash.String()),
		slog.String("second_hash", conflictingHash.String()),
	)
	node.broadcastProposal(ctx, conflictingProposal)
}

func (node *posNode) shouldInjectDoubleProposalFault(proposal consensus.BlockProposal) bool {
	if !node.config.FaultInjection.DoubleProposalOnce {
		return false
	}
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	if proposal.Header.LeaderID != localValidatorID {
		return false
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if node.doubleProposalInjected {
		return false
	}
	node.doubleProposalInjected = true
	return true
}

func (node *posNode) buildConflictingProposal(proposal consensus.BlockProposal) (consensus.BlockProposal, structure.Hash, error) {
	conflictingProposal := proposal
	if proposal.Header.TimestampUnixMilli < 1<<63-1 {
		conflictingProposal.Header.TimestampUnixMilli = proposal.Header.TimestampUnixMilli + 1
	} else {
		conflictingProposal.Header.TxRoot[0] ^= 1
	}
	signBytes, err := conflictingProposal.Header.SignBytes()
	if err != nil {
		return consensus.BlockProposal{}, structure.Hash{}, err
	}
	signature, err := node.consensusKeyPair.Sign(signBytes)
	if err != nil {
		return consensus.BlockProposal{}, structure.Hash{}, err
	}
	conflictingProposal.LeaderSignature = signature
	conflictingHash, err := conflictingProposal.Hash()
	if err != nil {
		return consensus.BlockProposal{}, structure.Hash{}, err
	}
	return conflictingProposal, conflictingHash, nil
}
