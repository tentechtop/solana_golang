package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/p2p"
	"solana_golang/structure"
)

const maxOrphanProposals = 1024
const maxSyncBlocksPerRound = 32
const productionPeerStatusTimeout = 200 * time.Millisecond

func (node *posNode) handleBlockByHashRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	_ = ctx
	request := blockHashRequestEnvelope{}
	if err := jsonUnmarshal(message.Payload, &request); err != nil {
		return p2p.Message{}, err
	}
	blockHash, err := structure.HashFromBase58(request.Hash)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: decode block hash request: %w", err)
	}
	proposal, found, err := node.ledger.BlockByHash(blockHash)
	response := blockResponseEnvelope{Found: found, Hash: blockHash.String()}
	if err != nil {
		response.Error = err.Error()
	}
	if found && err == nil {
		response.Proposal, err = proposalToJSON(proposal)
		if err != nil {
			response.Found = false
			response.Error = err.Error()
		}
	}
	return node.newProtocolResponse(message, p2p.ProtocolPoSBlockByHashV1, response)
}

func (node *posNode) handleBlockByHeightRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	_ = ctx
	request := blockHeightRequestEnvelope{}
	if err := jsonUnmarshal(message.Payload, &request); err != nil {
		return p2p.Message{}, err
	}
	proposal, blockHash, found, err := node.ledger.BlockByHeight(request.Height)
	response := blockResponseEnvelope{Found: found}
	if found {
		response.Hash = blockHash.String()
	}
	if err != nil {
		response.Error = err.Error()
	}
	if found && err == nil {
		response.Proposal, err = proposalToJSON(proposal)
		if err != nil {
			response.Found = false
			response.Error = err.Error()
		}
	}
	return node.newProtocolResponse(message, p2p.ProtocolPoSBlockByHeightV1, response)
}

func (node *posNode) handleStateSnapshotRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	_ = ctx
	request := stateSnapshotRequestEnvelope{}
	if err := jsonUnmarshal(message.Payload, &request); err != nil {
		return p2p.Message{}, err
	}
	blockHash, err := structure.HashFromBase58(request.BlockHash)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: decode state snapshot request: %w", err)
	}
	state, found, err := node.ledger.StateSnapshotAtBlockHash(blockHash)
	response := stateSnapshotResponseEnvelope{Found: found, BlockHash: blockHash.String()}
	if err != nil {
		response.Error = err.Error()
	}
	if found && err == nil {
		response, err = encodeStateSnapshotResponse(blockHash, state)
		if err != nil {
			response = stateSnapshotResponseEnvelope{Found: false, BlockHash: blockHash.String(), Error: err.Error()}
		}
	}
	return node.newProtocolResponse(message, p2p.ProtocolPoSStateSnapshotV1, response)
}

func (node *posNode) handleStatusRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	_ = ctx
	return node.newProtocolResponse(message, p2p.ProtocolPoSStatusV1, node.statusSnapshot())
}

func (node *posNode) newProtocolResponse(request p2p.Message, protocolID p2p.ProtocolID, value any) (p2p.Message, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: marshal protocol response: %w", err)
	}
	response, err := p2p.NewResponseMessage(node.peerKeyPair.peerID, protocolID, request.ID, payload)
	if err != nil {
		return p2p.Message{}, err
	}
	response.ToPeerID = request.FromPeerID
	return response, nil
}

func (node *posNode) requestBlockByHash(ctx context.Context, peerID string, blockHash structure.Hash) (consensus.BlockProposal, bool, error) {
	requestPayload, err := json.Marshal(blockHashRequestEnvelope{Hash: blockHash.String()})
	if err != nil {
		return consensus.BlockProposal{}, false, err
	}
	request, err := p2p.NewRequestMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSBlockByHashV1, requestPayload)
	if err != nil {
		return consensus.BlockProposal{}, false, err
	}
	node.metrics.syncRequests.Add(1)
	response, err := node.host.Request(ctx, peerID, request)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return consensus.BlockProposal{}, false, err
	}
	envelope := blockResponseEnvelope{}
	if err := jsonUnmarshal(response.Payload, &envelope); err != nil {
		node.metrics.syncFailures.Add(1)
		return consensus.BlockProposal{}, false, err
	}
	if envelope.Error != "" {
		return consensus.BlockProposal{}, false, fmt.Errorf("posnode: peer block response: %s", envelope.Error)
	}
	if !envelope.Found {
		return consensus.BlockProposal{}, false, nil
	}
	proposal, err := proposalFromJSON(envelope.Proposal)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return consensus.BlockProposal{}, false, err
	}
	return proposal, true, nil
}

func (node *posNode) requestBlockByHeight(ctx context.Context, peerID string, height uint64) (consensus.BlockProposal, structure.Hash, bool, error) {
	requestPayload, err := json.Marshal(blockHeightRequestEnvelope{Height: height})
	if err != nil {
		return consensus.BlockProposal{}, structure.Hash{}, false, err
	}
	request, err := p2p.NewRequestMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSBlockByHeightV1, requestPayload)
	if err != nil {
		return consensus.BlockProposal{}, structure.Hash{}, false, err
	}
	node.metrics.syncRequests.Add(1)
	response, err := node.host.Request(ctx, peerID, request)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return consensus.BlockProposal{}, structure.Hash{}, false, err
	}
	envelope := blockResponseEnvelope{}
	if err := jsonUnmarshal(response.Payload, &envelope); err != nil {
		node.metrics.syncFailures.Add(1)
		return consensus.BlockProposal{}, structure.Hash{}, false, err
	}
	if envelope.Error != "" {
		return consensus.BlockProposal{}, structure.Hash{}, false, fmt.Errorf("posnode: peer height response: %s", envelope.Error)
	}
	if !envelope.Found {
		return consensus.BlockProposal{}, structure.Hash{}, false, nil
	}
	blockHash, err := structure.HashFromBase58(envelope.Hash)
	if err != nil {
		return consensus.BlockProposal{}, structure.Hash{}, false, err
	}
	proposal, err := proposalFromJSON(envelope.Proposal)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return consensus.BlockProposal{}, structure.Hash{}, false, err
	}
	return proposal, blockHash, true, nil
}

func (node *posNode) requestStateSnapshot(ctx context.Context, peerID string, blockHash structure.Hash) (consensus.ChainState, bool, error) {
	requestPayload, err := json.Marshal(stateSnapshotRequestEnvelope{BlockHash: blockHash.String()})
	if err != nil {
		return consensus.ChainState{}, false, err
	}
	request, err := p2p.NewRequestMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSStateSnapshotV1, requestPayload)
	if err != nil {
		return consensus.ChainState{}, false, err
	}
	node.metrics.syncRequests.Add(1)
	response, err := node.host.Request(ctx, peerID, request)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return consensus.ChainState{}, false, err
	}
	envelope := stateSnapshotResponseEnvelope{}
	if err := jsonUnmarshal(response.Payload, &envelope); err != nil {
		node.metrics.syncFailures.Add(1)
		return consensus.ChainState{}, false, err
	}
	if envelope.Error != "" {
		return consensus.ChainState{}, false, fmt.Errorf("posnode: peer snapshot response: %s", envelope.Error)
	}
	if !envelope.Found {
		return consensus.ChainState{}, false, nil
	}
	responseHash, state, err := decodeStateSnapshotResponse(envelope)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return consensus.ChainState{}, false, err
	}
	if responseHash != blockHash {
		return consensus.ChainState{}, false, fmt.Errorf("posnode: snapshot block hash mismatch")
	}
	return state, true, nil
}

func (node *posNode) requestStatus(ctx context.Context, peerID string) (statusResponseEnvelope, error) {
	request, err := p2p.NewRequestMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSStatusV1, []byte("{}"))
	if err != nil {
		return statusResponseEnvelope{}, err
	}
	node.metrics.syncRequests.Add(1)
	response, err := node.host.Request(ctx, peerID, request)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return statusResponseEnvelope{}, err
	}
	envelope := statusResponseEnvelope{}
	if err := jsonUnmarshal(response.Payload, &envelope); err != nil {
		node.metrics.syncFailures.Add(1)
		return statusResponseEnvelope{}, err
	}
	return envelope, nil
}

func (node *posNode) blockSyncLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			node.syncOneRound(ctx)
		}
	}
}

func (node *posNode) syncOneRound(ctx context.Context) {
	localHead := node.ledger.Head()
	for _, peerID := range node.validatorPeerIDsSnapshot(true) {
		status, err := node.requestStatus(ctx, peerID)
		if err != nil {
			node.logger.Debug("posnode status sync failed", slog.String("peer_id", peerID), slog.Any("error", err))
			continue
		}
		if status.HeadHeight <= localHead.Height {
			continue
		}
		if status.FinalizedHeight > localHead.Height+maxSyncBlocksPerRound {
			imported, err := node.importFinalizedSnapshotFromPeer(ctx, peerID, status)
			if err != nil {
				node.metrics.syncFailures.Add(1)
				node.logger.Warn("posnode finalized snapshot sync failed", slog.String("peer_id", peerID), slog.Any("error", err))
				continue
			}
			if imported {
				localHead = node.ledger.Head()
			}
		}
		if status.HeadHeight <= localHead.Height {
			continue
		}
		if err := node.syncBlocksFromPeer(ctx, peerID, localHead.Height+1, status.HeadHeight); err != nil {
			node.metrics.syncFailures.Add(1)
			node.logger.Warn("posnode block sync failed", slog.String("peer_id", peerID), slog.Any("error", err))
		}
		localHead = node.ledger.Head()
	}
}

func (node *posNode) hasAheadValidatorPeer(ctx context.Context, localHeight uint64) bool {
	if node.host == nil {
		return false
	}
	for _, peerID := range node.validatorPeerIDsSnapshot(true) {
		statusContext, cancel := context.WithTimeout(ctx, productionPeerStatusTimeout)
		status, err := node.requestStatus(statusContext, peerID)
		cancel()
		if err != nil {
			continue
		}
		if status.HeadHeight > localHeight {
			return true
		}
	}
	return false
}

func (node *posNode) importFinalizedSnapshotFromPeer(ctx context.Context, peerID string, status statusResponseEnvelope) (bool, error) {
	if status.FinalizedHash == "" || status.FinalizedHeight == 0 {
		return false, nil
	}
	finalizedHash, err := structure.HashFromBase58(status.FinalizedHash)
	if err != nil {
		return false, fmt.Errorf("posnode: decode finalized hash: %w", err)
	}
	proposal, blockHash, found, err := node.requestBlockByHeight(ctx, peerID, status.FinalizedHeight)
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("posnode: finalized block height %d not found", status.FinalizedHeight)
	}
	if blockHash != finalizedHash {
		return false, fmt.Errorf("posnode: finalized block hash mismatch")
	}
	snapshot, found, err := node.requestStateSnapshot(ctx, peerID, finalizedHash)
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("posnode: finalized snapshot not found")
	}
	stateRoot, err := snapshot.RootHash()
	if err != nil {
		return false, err
	}
	if stateRoot != proposal.Header.StateRoot {
		return false, fmt.Errorf("posnode: finalized snapshot state root mismatch")
	}
	importedHead, err := node.ledger.ImportFinalizedSnapshot(blockchain.ImportSnapshotRequest{Proposal: proposal, State: snapshot})
	if err != nil {
		return false, err
	}
	node.recordCommittedBlockhash(proposal.Header.Slot, finalizedHash)
	node.refreshEpochAfterSnapshotImport(importedHead.Slot)
	node.logger.Info("posnode finalized snapshot synced",
		slog.String("peer_id", peerID),
		slog.Uint64("height", importedHead.Height),
		slog.Uint64("slot", importedHead.Slot),
		slog.String("block_hash", importedHead.BlockHash.String()),
	)
	return true, nil
}

func (node *posNode) refreshEpochAfterSnapshotImport(importedSlot uint64) {
	targetSlot := node.currentWallSlot()
	if importedSlot > targetSlot {
		targetSlot = importedSlot
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	epochID, startSlot := node.epochForSlot(targetSlot)
	if err := node.rebuildEpochLocked(epochID, startSlot, node.epochSeed(epochID)); err != nil {
		node.logger.Warn("posnode epoch refresh after snapshot failed", slog.Uint64("slot", targetSlot), slog.Any("error", err))
	}
}

func (node *posNode) syncBlocksFromPeer(ctx context.Context, peerID string, startHeight uint64, targetHeight uint64) error {
	limitHeight := startHeight + maxSyncBlocksPerRound - 1
	if targetHeight > limitHeight {
		targetHeight = limitHeight
	}
	for height := startHeight; height <= targetHeight; height++ {
		proposal, blockHash, found, err := node.requestBlockByHeight(ctx, peerID, height)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("height %d not found", height)
		}
		if err := node.applySyncedProposal(ctx, proposal, blockHash); err != nil {
			return err
		}
	}
	return nil
}

func (node *posNode) applySyncedProposal(ctx context.Context, proposal consensus.BlockProposal, expectedHash structure.Hash) error {
	proposalHash, err := proposal.Hash()
	if err != nil {
		return err
	}
	if proposalHash != expectedHash {
		return fmt.Errorf("posnode: synced block hash mismatch")
	}
	head := node.ledger.Head()
	if proposalHash == head.BlockHash {
		return nil
	}
	node.mutex.Lock()
	if err := node.ensureEpochForSlotLocked(proposal.Header.Slot); err != nil {
		node.mutex.Unlock()
		return err
	}
	leader, exists := node.epochSnapshot.ValidatorByID(proposal.Header.LeaderID)
	if !exists {
		node.mutex.Unlock()
		return fmt.Errorf("posnode: synced proposal leader not in snapshot")
	}
	epochSnapshot := node.epochSnapshot
	leaderSchedule := node.leaderSchedule
	blockhashQueue := node.blockhashQueue
	node.mutex.Unlock()

	parentState, parentReady := node.ensureParentAvailable(ctx, proposal.Header.ParentHash)
	if !parentReady {
		node.storeOrphanProposal(proposal)
		return nil
	}
	verifier := consensus.ProposalVerifier{ChainID: node.config.ChainID, Executor: node.executor}
	nextState, err := verifier.VerifyProposal(ctx, consensus.VerifyProposalRequest{
		Proposal:       proposal,
		EpochSnapshot:  epochSnapshot,
		Schedule:       leaderSchedule,
		ParentHash:     proposal.Header.ParentHash,
		ParentState:    parentState,
		BlockhashQueue: blockhashQueue,
		Leader:         leader,
		RewardConfig:   consensus.DefaultRewardConfig(),
	})
	if err != nil {
		return err
	}
	commitRequest := blockchain.CommitBlockRequest{Proposal: proposal, NextState: nextState}
	head = node.ledger.Head()
	if proposal.Header.ParentHash == head.BlockHash && proposal.Header.Height == head.Height+1 {
		if _, err := node.ledger.CommitBlock(commitRequest); err != nil {
			return err
		}
		node.recordCommittedBlockhash(proposal.Header.Slot, proposalHash)
		node.metrics.proposalsAccepted.Add(1)
		node.retryOrphanChildren(ctx, proposalHash)
		return nil
	}
	if _, err := node.ledger.SaveBlockCandidate(commitRequest); err != nil {
		return err
	}
	decision, err := node.ledger.ReorganizeTo(proposalHash)
	if err != nil {
		return err
	}
	node.metrics.forkDecisions.Add(1)
	if decision.Reorganized {
		node.metrics.reorgs.Add(1)
	}
	node.logger.Info("posnode synced fork decision",
		slog.Bool("accepted", decision.Accepted),
		slog.Bool("reorganized", decision.Reorganized),
		slog.String("reason", decision.Reason),
		slog.Uint64("slot", proposal.Header.Slot),
		slog.Uint64("height", proposal.Header.Height),
		slog.String("block_hash", proposalHash.String()),
		slog.String("common_ancestor_hash", decision.CommonAncestor.BlockHash.String()),
		slog.Uint64("common_ancestor_height", decision.CommonAncestor.Height),
		slog.Any("old_chain_blocks", hashesToStrings(decision.OldBlocks)),
		slog.Any("new_chain_blocks", hashesToStrings(decision.NewBlocks)),
	)
	if decision.Accepted {
		node.recordCommittedBlockhash(proposal.Header.Slot, proposalHash)
		node.metrics.proposalsAccepted.Add(1)
		node.retryOrphanChildren(ctx, proposalHash)
	}
	return nil
}

func (node *posNode) ensureParentAvailable(ctx context.Context, parentHash structure.Hash) (consensus.ChainState, bool) {
	state, err := node.ledger.StateAtBlockHash(parentHash)
	if err == nil {
		return state, true
	}
	if node.host == nil {
		node.metrics.syncFailures.Add(1)
		return consensus.ChainState{}, false
	}
	for _, peerID := range node.validatorPeerIDsSnapshot(true) {
		proposal, foundBlock, blockErr := node.requestBlockByHash(ctx, peerID, parentHash)
		if blockErr != nil || !foundBlock {
			node.logSyncMiss(peerID, parentHash, blockErr)
			continue
		}
		proposalHash, err := proposal.Hash()
		if err != nil || proposalHash != parentHash {
			node.logSyncMiss(peerID, parentHash, fmt.Errorf("invalid parent block hash"))
			continue
		}
		snapshot, foundState, stateErr := node.requestStateSnapshot(ctx, peerID, parentHash)
		if stateErr != nil || !foundState {
			node.logSyncMiss(peerID, parentHash, stateErr)
			continue
		}
		stateRoot, err := snapshot.RootHash()
		if err != nil || stateRoot != proposal.Header.StateRoot {
			node.logSyncMiss(peerID, parentHash, fmt.Errorf("invalid parent state root"))
			continue
		}
		if _, err := node.ledger.SaveExternalBlockSnapshot(proposal, snapshot); err != nil {
			node.logSyncMiss(peerID, parentHash, err)
			continue
		}
		node.logger.Info("posnode parent snapshot synced",
			slog.String("peer_id", peerID),
			slog.Uint64("height", proposal.Header.Height),
			slog.Uint64("slot", proposal.Header.Slot),
			slog.String("hash", parentHash.String()),
		)
		return snapshot, true
	}
	node.metrics.syncFailures.Add(1)
	return consensus.ChainState{}, false
}

func (node *posNode) logSyncMiss(peerID string, blockHash structure.Hash, err error) {
	if err == nil {
		node.logger.Debug("posnode sync miss", slog.String("peer_id", peerID), slog.String("hash", blockHash.String()))
		return
	}
	node.logger.Debug("posnode sync request failed",
		slog.String("peer_id", peerID),
		slog.String("hash", blockHash.String()),
		slog.Any("error", err),
	)
}

func (node *posNode) peerIDsSnapshot() []string {
	node.refreshKnownPeersFromHost()
	node.mutex.Lock()
	defer node.mutex.Unlock()
	peerIDs := make([]string, len(node.knownPeerIDs))
	copy(peerIDs, node.knownPeerIDs)
	return peerIDs
}

func (node *posNode) validatorPeerIDsSnapshot(excludeLocal bool) []string {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	return node.filterTransactionRoutePeersLocked(node.validatorPeerIDsLocked(), excludeLocal)
}

func (node *posNode) connectionPeerIDsSnapshot() []string {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	peerIDs := make([]string, 0, len(node.config.BootstrapPeers)+len(node.epochSnapshot.Validators))
	for _, peerConfig := range node.config.BootstrapPeers {
		if peerConfig.PeerID == "" || peerConfig.PeerID == node.peerKeyPair.peerID {
			continue
		}
		peerIDs = append(peerIDs, peerConfig.PeerID)
	}
	peerIDs = append(peerIDs, node.filterTransactionRoutePeersLocked(node.validatorPeerIDsLocked(), true)...)
	return uniquePeerIDs(peerIDs)
}

func (node *posNode) storeOrphanProposal(proposal consensus.BlockProposal) {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if node.orphanProposalCountLocked() >= maxOrphanProposals {
		node.metrics.transactionsDrop.Add(1)
		node.logger.Warn("posnode orphan proposal dropped", slog.Uint64("slot", proposal.Header.Slot))
		return
	}
	parentHash := proposal.Header.ParentHash
	node.orphanProposals[parentHash] = append(node.orphanProposals[parentHash], proposal)
	node.metrics.orphanStored.Add(1)
	node.logger.Info("posnode orphan proposal stored",
		slog.Uint64("slot", proposal.Header.Slot),
		slog.Uint64("height", proposal.Header.Height),
		slog.String("parent", parentHash.String()),
	)
}

func (node *posNode) retryOrphanChildren(ctx context.Context, parentHash structure.Hash) {
	node.mutex.Lock()
	children := node.orphanProposals[parentHash]
	delete(node.orphanProposals, parentHash)
	node.mutex.Unlock()
	for _, child := range children {
		if err := node.voteForProposal(ctx, child); err != nil {
			node.logger.Debug("posnode retry orphan proposal failed",
				slog.Uint64("slot", child.Header.Slot),
				slog.Any("error", err),
			)
		}
	}
}

func (node *posNode) orphanProposalCountLocked() int {
	total := 0
	for _, proposals := range node.orphanProposals {
		total += len(proposals)
	}
	return total
}

func (node *posNode) statusSnapshot() statusResponseEnvelope {
	node.refreshKnownPeersFromHost()
	head := node.ledger.Head()
	node.mutex.Lock()
	defer node.mutex.Unlock()
	currentLeader := ""
	startSlot := node.currentRoutingSlotLocked()
	if node.epochSnapshot.StartSlot <= startSlot && startSlot <= node.epochSnapshot.EndSlot {
		if leader, err := node.leaderSchedule.LeaderForSlot(startSlot); err == nil {
			currentLeader = string(leader)
		}
	}
	turbine := node.turbinePositionForSlotLocked(startSlot)
	transactionFastPath := node.transactionFastPathForSlotLocked(startSlot, true)
	consensusStatus := node.consensusStatusForSlotLocked(startSlot)
	return statusResponseEnvelope{
		NodeName:        node.config.NodeName,
		PeerID:          node.peerKeyPair.peerID,
		HeadHeight:      head.Height,
		HeadSlot:        head.Slot,
		HeadHash:        head.BlockHash.String(),
		HeadQCHash:      head.QCHash.String(),
		FinalizedHeight: head.FinalizedHeight,
		FinalizedHash:   head.FinalizedHash.String(),
		EpochID:         node.epochSnapshot.EpochID,
		MempoolSize:     len(node.mempool),
		ValidatorCount:  len(node.epochSnapshot.Validators),
		KnownPeerCount:  len(node.knownPeerIDs),
		CurrentLeader:   currentLeader,
		UpcomingLeaders: node.upcomingLeadersLocked(startSlot, node.config.TransactionLeaderForwardSlots+1),
		Turbine:         turbine,
		TransactionFast: transactionFastPath,
		Consensus:       consensusStatus,
		Metrics:         node.metrics.snapshot(),
	}
}
