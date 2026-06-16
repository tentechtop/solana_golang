package main

import (
	"context"
	"log/slog"

	"solana_golang/consensus"
	"solana_golang/structure"
)

func (node *posNode) markProposalSeen(blockHash structure.Hash) bool {
	key := blockHash.String()
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if _, exists := node.seenProposals[key]; exists {
		return false
	}
	if node.seenProposals == nil {
		node.seenProposals = make(map[string]struct{})
	}
	node.seenProposals[key] = struct{}{}
	return true
}

func (node *posNode) turbineChildPeerIDs(ctx context.Context, slot uint64, leaderID consensus.ValidatorID, excludedPeerID string) ([]string, turbinePositionJSON, error) {
	childNodes, position, err := node.turbineChildNodes(slot, leaderID)
	if err != nil {
		return nil, position, err
	}
	peerIDs := make([]string, 0, len(childNodes))
	for _, child := range childNodes {
		if child.P2PPeerID == "" || child.P2PPeerID == node.peerKeyPair.peerID || child.P2PPeerID == excludedPeerID {
			continue
		}
		if !node.ensureRoutablePeer(ctx, child.P2PPeerID) {
			node.logger.Warn("posnode turbine child unresolved",
				slog.Uint64("slot", slot),
				slog.String("leader_id", string(leaderID)),
				slog.String("child_validator_id", string(child.ValidatorID)),
				slog.String("child_peer_id", child.P2PPeerID),
			)
			continue
		}
		peerIDs = append(peerIDs, child.P2PPeerID)
	}
	return uniquePeerIDs(peerIDs), position, nil
}

func (node *posNode) turbineChildNodes(slot uint64, leaderID consensus.ValidatorID) ([]consensus.TurbineNode, turbinePositionJSON, error) {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if err := node.ensureEpochForSlotLocked(slot); err != nil {
		return nil, turbinePositionJSON{}, err
	}
	tree, err := consensus.NewTurbineTree(node.epochSnapshot, slot, leaderID, node.config.TurbineFanout)
	if err != nil {
		return nil, turbinePositionJSON{}, err
	}
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	position, found := tree.NodeByValidator(localValidatorID)
	children := tree.ChildrenOf(localValidatorID)
	return children, node.turbinePositionJSONLocked(tree, position, found), nil
}

func (node *posNode) turbinePositionForSlotLocked(slot uint64) turbinePositionJSON {
	if node.config.EpochSlots == 0 || len(node.epochSnapshot.Validators) == 0 {
		return turbinePositionJSON{Slot: slot, Fanout: node.config.TurbineFanout, Layer: -1}
	}
	if err := node.ensureEpochForSlotLocked(slot); err != nil {
		return turbinePositionJSON{Slot: slot, Fanout: node.config.TurbineFanout, Layer: -1}
	}
	leaderID, err := node.leaderSchedule.LeaderForSlot(slot)
	if err != nil {
		return turbinePositionJSON{Slot: slot, Fanout: node.config.TurbineFanout, Layer: -1}
	}
	tree, err := consensus.NewTurbineTree(node.epochSnapshot, slot, leaderID, node.config.TurbineFanout)
	if err != nil {
		return turbinePositionJSON{Slot: slot, Fanout: node.config.TurbineFanout, Layer: -1}
	}
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	position, found := tree.NodeByValidator(localValidatorID)
	return node.turbinePositionJSONLocked(tree, position, found)
}

func (node *posNode) turbinePositionJSONLocked(tree consensus.TurbineTree, position consensus.TurbineNode, found bool) turbinePositionJSON {
	result := turbinePositionJSON{
		Slot:             tree.Slot,
		Fanout:           tree.Fanout,
		Layer:            -1,
		LeaderID:         string(tree.LeaderID),
		ValidatorInTree:  found,
		TurbineAvailable: true,
	}
	if leader, exists := tree.NodeByValidator(tree.LeaderID); exists {
		result.LeaderPeerID = leader.P2PPeerID
	}
	if !found {
		return result
	}
	result.Layer = position.Layer
	result.ParentValidator = string(position.ParentID)
	result.ParentPeerID = position.ParentPeerID
	children := tree.ChildrenOf(position.ValidatorID)
	result.ChildValidators = make([]string, 0, len(children))
	result.ChildPeerIDs = make([]string, 0, len(children))
	for _, child := range children {
		result.ChildValidators = append(result.ChildValidators, string(child.ValidatorID))
		result.ChildPeerIDs = append(result.ChildPeerIDs, child.P2PPeerID)
	}
	return result
}

func (node *posNode) forwardProposalByTurbine(ctx context.Context, proposal consensus.BlockProposal, proposalHash structure.Hash, fromPeerID string) {
	if node.host == nil {
		return
	}
	peerIDs, position, err := node.turbineChildPeerIDs(ctx, proposal.Header.Slot, proposal.Header.LeaderID, fromPeerID)
	if err != nil {
		node.logger.Warn("posnode turbine proposal route failed",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("block_hash", proposalHash.String()),
			slog.Any("error", err),
		)
		return
	}
	if len(peerIDs) == 0 {
		node.logger.Debug("posnode turbine proposal leaf",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.String("block_hash", proposalHash.String()),
			slog.Int("turbine_layer", position.Layer),
		)
		return
	}
	message, err := encodeProposalMessage(proposal)
	if err != nil {
		node.logger.Error("posnode encode turbine proposal failed", slog.Any("error", err))
		return
	}
	if err := node.host.Broadcast(ctx, peerIDs, message); err != nil {
		node.logger.Warn("posnode turbine proposal forward failed",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.String("block_hash", proposalHash.String()),
			slog.Int("turbine_layer", position.Layer),
			slog.Int("child_count", len(peerIDs)),
			slog.Any("error", err),
		)
		return
	}
	node.logger.Debug("posnode turbine proposal forwarded",
		slog.Uint64("slot", proposal.Header.Slot),
		slog.String("block_hash", proposalHash.String()),
		slog.Int("turbine_layer", position.Layer),
		slog.Int("child_count", len(peerIDs)),
	)
}
