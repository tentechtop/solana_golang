package posnode

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/p2p"
	"solana_golang/structure"
)

const maxOrphanProposals = 1024
const maxSyncBlocksPerRound = 32
const defaultBlockLocatorEntries = 32
const (
	peerStatusTimeoutSlotMultiplier = 3
	minPeerStatusTimeout            = time.Second
	maxPeerStatusTimeout            = 5 * time.Second
	productionSyncGateMaxTimeout    = 80 * time.Millisecond
	productionSyncGateMinRemaining  = 50 * time.Millisecond
	productionSyncGateMaxPeers      = 2
)

func (node *posNode) handleBlockByHashRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	_ = ctx
	request, err := unmarshalBlockHashRequestBinary(message.Payload)
	if err != nil {
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
		response.Proposal = proposal
	}
	return node.newProtocolResponse(message, p2p.ProtocolPoSBlockByHashV1, response)
}

func (node *posNode) handleBlockByHeightRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	_ = ctx
	request, err := unmarshalBlockHeightRequestBinary(message.Payload)
	if err != nil {
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
		response.Proposal = proposal
	}
	return node.newProtocolResponse(message, p2p.ProtocolPoSBlockByHeightV1, response)
}

func (node *posNode) handleStateSnapshotRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	_ = ctx
	request, err := unmarshalStateSnapshotRequestBinary(message.Payload)
	if err != nil {
		return p2p.Message{}, err
	}
	blockHash, err := structure.HashFromBase58(request.BlockHash)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: decode state snapshot request: %w", err)
	}
	state, found, err := node.ledger.StateSnapshotAtBlockHash(blockHash)
	response := stateSnapshotResponseEnvelope{
		Found:             found,
		ChainID:           node.config.ChainID,
		ChainIdentityHash: node.config.ChainIdentityHash,
		GenesisHash:       node.config.GenesisHash,
		BlockHash:         blockHash.String(),
	}
	if err != nil {
		response.Error = err.Error()
	}
	if found && err == nil {
		response, err = encodeStateSnapshotResponse(blockHash, state)
		if err != nil {
			response = stateSnapshotResponseEnvelope{
				Found:             false,
				ChainID:           node.config.ChainID,
				ChainIdentityHash: node.config.ChainIdentityHash,
				GenesisHash:       node.config.GenesisHash,
				BlockHash:         blockHash.String(),
				Error:             err.Error(),
			}
		} else {
			response.ChainID = node.config.ChainID
			response.ChainIdentityHash = node.config.ChainIdentityHash
			response.GenesisHash = node.config.GenesisHash
		}
	}
	return node.newProtocolResponse(message, p2p.ProtocolPoSStateSnapshotV1, response)
}

func (node *posNode) handleStatusRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	_ = ctx
	if err := unmarshalStatusRequestBinary(message.Payload); err != nil {
		return p2p.Message{}, err
	}
	return node.newProtocolResponse(message, p2p.ProtocolPoSStatusV1, node.statusSnapshot())
}

func (node *posNode) handleBlockLocatorRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	_ = ctx
	request, err := unmarshalBlockLocatorRequestBinary(message.Payload)
	if err != nil {
		return p2p.Message{}, err
	}
	entries, err := node.ledger.BlockLocator(request.MaxEntries)
	response := blockLocatorResponseEnvelope{}
	if err != nil {
		response.Error = err.Error()
	} else {
		response.Entries = encodeBlockLocatorEntries(entries)
	}
	return node.newProtocolResponse(message, p2p.ProtocolPoSBlockLocatorV1, response)
}

func (node *posNode) handleCommonAncestorRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	_ = ctx
	request, err := unmarshalCommonAncestorRequestBinary(message.Payload)
	if err != nil {
		return p2p.Message{}, err
	}
	locator, err := decodeBlockLocatorEntries(request.Locator)
	response := commonAncestorResponseEnvelope{}
	if err != nil {
		response.Error = err.Error()
		return node.newProtocolResponse(message, p2p.ProtocolPoSCommonAncestorV1, response)
	}
	ancestor, found, err := node.ledger.FindCommonAncestor(locator)
	response.Found = found
	if err != nil {
		response.Error = err.Error()
	}
	if found {
		response.Ancestor = blockLocatorEntryJSON{
			Height: ancestor.Height,
			Hash:   ancestor.BlockHash.String(),
		}
	}
	return node.newProtocolResponse(message, p2p.ProtocolPoSCommonAncestorV1, response)
}

func (node *posNode) newProtocolResponse(request p2p.Message, protocolID p2p.ProtocolID, value any) (p2p.Message, error) {
	payload, err := marshalProtocolResponseBinary(protocolID, value)
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

func marshalProtocolResponseBinary(protocolID p2p.ProtocolID, value any) ([]byte, error) {
	switch protocolID {
	case p2p.ProtocolPoSBlockByHashV1, p2p.ProtocolPoSBlockByHeightV1:
		response, ok := value.(blockResponseEnvelope)
		if !ok {
			return nil, fmt.Errorf("posnode: invalid block response type %T", value)
		}
		return marshalBlockResponseBinary(protocolID, response)
	case p2p.ProtocolPoSStateSnapshotV1:
		response, ok := value.(stateSnapshotResponseEnvelope)
		if !ok {
			return nil, fmt.Errorf("posnode: invalid snapshot response type %T", value)
		}
		return marshalStateSnapshotResponseBinary(response)
	case p2p.ProtocolPoSStatusV1:
		response, ok := value.(statusResponseEnvelope)
		if !ok {
			return nil, fmt.Errorf("posnode: invalid status response type %T", value)
		}
		return marshalStatusResponseBinary(response)
	case p2p.ProtocolPoSBlockLocatorV1:
		response, ok := value.(blockLocatorResponseEnvelope)
		if !ok {
			return nil, fmt.Errorf("posnode: invalid locator response type %T", value)
		}
		return marshalBlockLocatorResponseBinary(response)
	case p2p.ProtocolPoSCommonAncestorV1:
		response, ok := value.(commonAncestorResponseEnvelope)
		if !ok {
			return nil, fmt.Errorf("posnode: invalid ancestor response type %T", value)
		}
		return marshalCommonAncestorResponseBinary(response)
	default:
		return nil, fmt.Errorf("posnode: unsupported response protocol %d", protocolID)
	}
}

func (node *posNode) requestBlockByHash(ctx context.Context, peerID string, blockHash structure.Hash) (consensus.BlockProposal, bool, error) {
	requestPayload, err := marshalBlockHashRequestBinary(blockHashRequestEnvelope{Hash: blockHash.String()})
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
	envelope, err := unmarshalBlockResponseBinary(p2p.ProtocolPoSBlockByHashV1, response.Payload)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return consensus.BlockProposal{}, false, err
	}
	if envelope.Error != "" {
		return consensus.BlockProposal{}, false, fmt.Errorf("posnode: peer block response: %s", envelope.Error)
	}
	if !envelope.Found {
		return consensus.BlockProposal{}, false, nil
	}
	return envelope.Proposal, true, nil
}

func (node *posNode) requestBlockByHeight(ctx context.Context, peerID string, height uint64) (consensus.BlockProposal, structure.Hash, bool, error) {
	requestPayload, err := marshalBlockHeightRequestBinary(blockHeightRequestEnvelope{Height: height})
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
	envelope, err := unmarshalBlockResponseBinary(p2p.ProtocolPoSBlockByHeightV1, response.Payload)
	if err != nil {
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
	return envelope.Proposal, blockHash, true, nil
}

func (node *posNode) requestStateSnapshot(ctx context.Context, peerID string, blockHash structure.Hash) (consensus.ChainState, bool, error) {
	requestPayload, err := marshalStateSnapshotRequestBinary(stateSnapshotRequestEnvelope{BlockHash: blockHash.String()})
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
	envelope, err := unmarshalStateSnapshotResponseBinary(response.Payload)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return consensus.ChainState{}, false, err
	}
	if envelope.Error != "" {
		return consensus.ChainState{}, false, fmt.Errorf("posnode: peer snapshot response: %s", envelope.Error)
	}
	if !envelope.Found {
		return consensus.ChainState{}, false, nil
	}
	if err := validatePeerChainIdentity(node.config, peerID, envelope.ChainID, envelope.ChainIdentityHash, envelope.GenesisHash); err != nil {
		node.metrics.syncFailures.Add(1)
		return consensus.ChainState{}, false, err
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
	requestPayload := marshalStatusRequestBinary()
	request, err := p2p.NewRequestMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSStatusV1, requestPayload)
	if err != nil {
		return statusResponseEnvelope{}, err
	}
	node.metrics.syncRequests.Add(1)
	response, err := node.host.Request(ctx, peerID, request)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return statusResponseEnvelope{}, err
	}
	envelope, err := unmarshalStatusResponseBinary(response.Payload)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return statusResponseEnvelope{}, err
	}
	if err := validatePeerStatusChainIdentity(node.config, peerID, envelope); err != nil {
		node.metrics.syncFailures.Add(1)
		return statusResponseEnvelope{}, err
	}
	return envelope, nil
}

func (node *posNode) requestBlockLocator(ctx context.Context, peerID string, maxEntries int) ([]blockchain.BlockLocatorEntry, error) {
	requestPayload, err := marshalBlockLocatorRequestBinary(blockLocatorRequestEnvelope{MaxEntries: maxEntries})
	if err != nil {
		return nil, err
	}
	request, err := p2p.NewRequestMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSBlockLocatorV1, requestPayload)
	if err != nil {
		return nil, err
	}
	node.metrics.syncRequests.Add(1)
	response, err := node.host.Request(ctx, peerID, request)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return nil, err
	}
	envelope, err := unmarshalBlockLocatorResponseBinary(response.Payload)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return nil, err
	}
	if envelope.Error != "" {
		return nil, fmt.Errorf("posnode: peer locator response: %s", envelope.Error)
	}
	entries, err := decodeBlockLocatorEntries(envelope.Entries)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return nil, err
	}
	return entries, nil
}

func (node *posNode) requestCommonAncestor(
	ctx context.Context,
	peerID string,
	locator []blockchain.BlockLocatorEntry,
) (blockchain.Head, bool, error) {
	requestPayload, err := marshalCommonAncestorRequestBinary(commonAncestorRequestEnvelope{Locator: encodeBlockLocatorEntries(locator)})
	if err != nil {
		return blockchain.Head{}, false, err
	}
	request, err := p2p.NewRequestMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSCommonAncestorV1, requestPayload)
	if err != nil {
		return blockchain.Head{}, false, err
	}
	node.metrics.syncRequests.Add(1)
	response, err := node.host.Request(ctx, peerID, request)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return blockchain.Head{}, false, err
	}
	envelope, err := unmarshalCommonAncestorResponseBinary(response.Payload)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return blockchain.Head{}, false, err
	}
	if envelope.Error != "" {
		return blockchain.Head{}, false, fmt.Errorf("posnode: peer common ancestor response: %s", envelope.Error)
	}
	if !envelope.Found {
		return blockchain.Head{}, false, nil
	}
	blockHash, err := structure.HashFromBase58(envelope.Ancestor.Hash)
	if err != nil {
		node.metrics.syncFailures.Add(1)
		return blockchain.Head{}, false, err
	}
	localHash, found, err := node.ledger.MainChainHashAtHeight(envelope.Ancestor.Height)
	if err != nil {
		return blockchain.Head{}, false, err
	}
	if !found || localHash != blockHash {
		return blockchain.Head{}, false, fmt.Errorf("posnode: peer %s returned non-local ancestor at height %d", peerID, envelope.Ancestor.Height)
	}
	return blockchain.Head{
		ChainID:   node.config.ChainID,
		Height:    envelope.Ancestor.Height,
		BlockHash: blockHash,
	}, true, nil
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
	for _, peerID := range node.syncPeerIDsSnapshot() {
		status, err := node.requestStatus(ctx, peerID)
		if err != nil {
			node.logger.Debug("posnode status sync failed", slog.String("peer_id", peerID), slog.Any("error", err))
			continue
		}
		if !peerNeedsBlockSync(localHead, status) {
			continue
		}
		if shouldImportFinalizedSnapshotBeforeBlockSync(status, localHead) {
			imported, err := node.importFinalizedSnapshotFromPeer(ctx, peerID, status)
			if err != nil {
				node.metrics.syncFailures.Add(1)
				node.logger.Warn("posnode finalized snapshot sync failed", slog.String("peer_id", peerID), slog.Any("error", err))
			}
			if imported {
				localHead = node.ledger.Head()
			}
		}
		localHead = node.ledger.Head()
		if !peerNeedsBlockSync(localHead, status) {
			continue
		}
		startHeight, err := node.determineSyncStartHeight(ctx, peerID, localHead, status)
		if err != nil {
			node.metrics.syncFailures.Add(1)
			if shouldImportFinalizedSnapshotAfterSyncStartError(err, status, localHead) {
				imported, importErr := node.importFinalizedSnapshotFromPeer(ctx, peerID, status)
				if importErr != nil {
					node.metrics.syncFailures.Add(1)
					node.logger.Warn("posnode finalized snapshot recovery failed",
						slog.String("peer_id", peerID),
						slog.Any("sync_start_error", err),
						slog.Any("error", importErr),
					)
					continue
				}
				if imported {
					node.logger.Warn("posnode finalized snapshot recovered sync boundary",
						slog.String("peer_id", peerID),
						slog.Any("sync_start_error", err),
						slog.Uint64("peer_finalized_height", status.FinalizedHeight),
						slog.Uint64("local_height", localHead.Height),
						slog.Uint64("local_finalized_height", localHead.FinalizedHeight),
					)
					localHead = node.ledger.Head()
					continue
				}
			}
			node.logger.Warn("posnode sync start height failed", slog.String("peer_id", peerID), slog.Any("error", err))
			continue
		}
		if startHeight == 0 || startHeight > status.HeadHeight {
			continue
		}
		if err := node.syncBlocksFromPeer(ctx, peerID, startHeight, status.HeadHeight); err != nil {
			node.metrics.syncFailures.Add(1)
			currentHead := node.ledger.Head()
			if shouldImportFinalizedSnapshotAfterBlockSyncError(err, status, currentHead) {
				imported, importErr := node.importFinalizedSnapshotFromPeer(ctx, peerID, status)
				if importErr != nil {
					node.metrics.syncFailures.Add(1)
					node.logger.Warn("posnode finalized snapshot recovery after block sync failed",
						slog.String("peer_id", peerID),
						slog.Any("block_sync_error", err),
						slog.Any("error", importErr),
					)
					continue
				}
				if imported {
					node.logger.Warn("posnode finalized snapshot recovered block sync",
						slog.String("peer_id", peerID),
						slog.Any("block_sync_error", err),
						slog.Uint64("peer_finalized_height", status.FinalizedHeight),
						slog.Uint64("local_height", currentHead.Height),
						slog.Uint64("local_finalized_height", currentHead.FinalizedHeight),
					)
					localHead = node.ledger.Head()
					continue
				}
			}
			node.logger.Warn("posnode block sync failed", slog.String("peer_id", peerID), slog.Any("error", err))
		}
		localHead = node.ledger.Head()
	}
}

func peerNeedsBlockSync(localHead blockchain.Head, status statusResponseEnvelope) bool {
	if status.FinalizedHeight > localHead.FinalizedHeight {
		return true
	}
	if status.HeadHeight > localHead.Height {
		return true
	}
	return status.HeadHeight == localHead.Height && status.HeadHash != "" && status.HeadHash != localHead.BlockHash.String()
}

type finalizedBoundarySyncError struct {
	PeerID          string
	Scope           string
	AncestorHeight  uint64
	FinalizedHeight uint64
}

func (err finalizedBoundarySyncError) Error() string {
	if err.Scope == "" {
		return fmt.Sprintf(
			"posnode: peer %s common ancestor %d below finalized height %d",
			err.PeerID,
			err.AncestorHeight,
			err.FinalizedHeight,
		)
	}
	return fmt.Sprintf(
		"posnode: peer %s %s ancestor %d below finalized height %d",
		err.PeerID,
		err.Scope,
		err.AncestorHeight,
		err.FinalizedHeight,
	)
}

func shouldImportFinalizedSnapshotAfterSyncStartError(
	err error,
	status statusResponseEnvelope,
	localHead blockchain.Head,
) bool {
	boundaryError := finalizedBoundarySyncError{}
	if !errors.As(err, &boundaryError) {
		return false
	}
	if status.FinalizedHeight <= localHead.FinalizedHeight {
		return false
	}
	return strings.TrimSpace(status.FinalizedHash) != ""
}

// shouldImportFinalizedSnapshotBeforeBlockSync 快速同步安全检查点 + peer finalized 已超过本地 head 时逐块回放没有收益。
func shouldImportFinalizedSnapshotBeforeBlockSync(status statusResponseEnvelope, localHead blockchain.Head) bool {
	if status.FinalizedHeight <= localHead.Height {
		return false
	}
	return strings.TrimSpace(status.FinalizedHash) != ""
}

// shouldImportFinalizedSnapshotAfterBlockSyncError 恢复 epoch 视图分裂 + 本地验证失败时以更高 finalized 检查点收敛。
func shouldImportFinalizedSnapshotAfterBlockSyncError(
	err error,
	status statusResponseEnvelope,
	localHead blockchain.Head,
) bool {
	if err == nil {
		return false
	}
	if status.FinalizedHeight <= localHead.FinalizedHeight {
		return false
	}
	return strings.TrimSpace(status.FinalizedHash) != ""
}

func validatePeerStatusChainIdentity(config nodeConfig, peerID string, status statusResponseEnvelope) error {
	return validatePeerChainIdentity(config, peerID, status.ChainID, status.ChainIdentityHash, status.GenesisHash)
}

func validatePeerChainIdentity(
	config nodeConfig,
	peerID string,
	peerChainID string,
	peerChainIdentityHash string,
	peerGenesisHash string,
) error {
	if strings.TrimSpace(peerChainIdentityHash) == "" {
		return fmt.Errorf("posnode: peer %s missing chain identity hash", peerID)
	}
	if peerChainIdentityHash != config.ChainIdentityHash {
		return fmt.Errorf(
			"posnode: peer %s chain identity mismatch local=%s remote=%s",
			peerID,
			config.ChainIdentityHash,
			peerChainIdentityHash,
		)
	}
	if strings.TrimSpace(peerChainID) != "" && peerChainID != config.ChainID {
		return fmt.Errorf("posnode: peer %s chain id mismatch local=%s remote=%s", peerID, config.ChainID, peerChainID)
	}
	if strings.TrimSpace(peerGenesisHash) != "" && peerGenesisHash != config.GenesisHash {
		return fmt.Errorf(
			"posnode: peer %s genesis hash mismatch local=%s remote=%s",
			peerID,
			config.GenesisHash,
			peerGenesisHash,
		)
	}
	return nil
}

func calculateSyncStartHeightFromAncestor(ancestorHeight uint64) uint64 {
	if ancestorHeight == 0 {
		return 1
	}
	return ancestorHeight + 1
}

func (node *posNode) determineSyncStartHeight(
	ctx context.Context,
	peerID string,
	localHead blockchain.Head,
	status statusResponseEnvelope,
) (uint64, error) {
	if !peerNeedsBlockSync(localHead, status) {
		return 0, nil
	}
	locator, err := node.ledger.BlockLocator(defaultBlockLocatorEntries)
	if err != nil {
		return 0, err
	}
	ancestor, found, err := node.requestCommonAncestor(ctx, peerID, locator)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("posnode: peer %s returned no common ancestor", peerID)
	}
	if ancestor.Height < localHead.FinalizedHeight {
		return 0, finalizedBoundarySyncError{
			PeerID:          peerID,
			AncestorHeight:  ancestor.Height,
			FinalizedHeight: localHead.FinalizedHeight,
		}
	}
	startHeight := calculateSyncStartHeightFromAncestor(ancestor.Height)
	if ancestor.Height == localHead.Height && ancestor.BlockHash == localHead.BlockHash {
		return startHeight, nil
	}
	node.logger.Info("posnode sync rewound to common ancestor",
		slog.String("peer_id", peerID),
		slog.Uint64("local_height", localHead.Height),
		slog.String("local_hash", localHead.BlockHash.String()),
		slog.Uint64("ancestor_height", ancestor.Height),
		slog.String("ancestor_hash", ancestor.BlockHash.String()),
		slog.Uint64("finalized_height", localHead.FinalizedHeight),
		slog.String("finalized_hash", localHead.FinalizedHash.String()),
		slog.Uint64("start_height", startHeight),
	)
	return startHeight, nil
}

func (node *posNode) syncProposalBranch(ctx context.Context, preferredPeerID string, proposal consensus.BlockProposal) error {
	if node.host == nil || proposal.Header.Height == 0 {
		return nil
	}
	head := node.ledger.Head()
	if proposal.Header.ParentHash == head.BlockHash {
		return nil
	}
	resolved, err := node.ledger.BranchResolved(proposal.Header.ParentHash)
	if err != nil {
		return err
	}
	if resolved {
		return nil
	}
	syncPeerIDs := node.syncPeerIDsSnapshot()
	peerIDs := make([]string, 0, len(syncPeerIDs)+1)
	if preferredPeerID != "" {
		peerIDs = append(peerIDs, preferredPeerID)
	}
	for _, peerID := range syncPeerIDs {
		if peerID == preferredPeerID {
			continue
		}
		peerIDs = append(peerIDs, peerID)
	}
	if len(peerIDs) == 0 {
		return nil
	}
	targetHeight := proposal.Header.Height - 1
	for _, peerID := range uniquePeerIDs(peerIDs) {
		startHeight, err := node.determineBranchSyncStartHeight(ctx, peerID, head)
		if err != nil {
			node.logger.Debug("posnode proposal branch start height failed",
				slog.String("peer_id", peerID),
				slog.Uint64("proposal_height", proposal.Header.Height),
				slog.Any("error", err),
			)
			continue
		}
		if startHeight == 0 || startHeight > targetHeight {
			continue
		}
		if err := node.syncBlocksFromPeer(ctx, peerID, startHeight, targetHeight); err != nil {
			node.logger.Debug("posnode proposal branch sync round failed",
				slog.String("peer_id", peerID),
				slog.Uint64("start_height", startHeight),
				slog.Uint64("target_height", targetHeight),
				slog.Any("error", err),
			)
			continue
		}
		resolved, err = node.ledger.BranchResolved(proposal.Header.ParentHash)
		if err != nil {
			return err
		}
		if resolved {
			node.logger.Info("posnode proposal branch synced",
				slog.String("peer_id", peerID),
				slog.Uint64("proposal_height", proposal.Header.Height),
				slog.Uint64("proposal_slot", proposal.Header.Slot),
				slog.String("parent_hash", proposal.Header.ParentHash.String()),
				slog.Uint64("start_height", startHeight),
				slog.Uint64("target_height", targetHeight),
			)
			return nil
		}
	}
	return fmt.Errorf("posnode: proposal branch unresolved for parent %s", proposal.Header.ParentHash.String())
}

func (node *posNode) determineBranchSyncStartHeight(
	ctx context.Context,
	peerID string,
	localHead blockchain.Head,
) (uint64, error) {
	statusContext, cancel := context.WithTimeout(ctx, node.peerStatusTimeout())
	_, err := node.requestStatus(statusContext, peerID)
	cancel()
	if err != nil {
		return 0, err
	}
	locator, err := node.ledger.BlockLocator(defaultBlockLocatorEntries)
	if err != nil {
		return 0, err
	}
	ancestor, found, err := node.requestCommonAncestor(ctx, peerID, locator)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("posnode: peer %s returned no branch ancestor", peerID)
	}
	if ancestor.Height < localHead.FinalizedHeight {
		return 0, finalizedBoundarySyncError{
			PeerID:          peerID,
			Scope:           "branch",
			AncestorHeight:  ancestor.Height,
			FinalizedHeight: localHead.FinalizedHeight,
		}
	}
	return calculateSyncStartHeightFromAncestor(ancestor.Height), nil
}

func (node *posNode) shouldPauseProductionForSync(ctx context.Context, localHead blockchain.Head, slotDeadline time.Time) bool {
	if node.host == nil {
		return false
	}
	peerIDs := node.productionSyncGatePeerIDs(localHead.Height)
	if len(peerIDs) == 0 && node.requiresConnectedValidatorPeerForProduction() {
		node.logger.Warn("posnode production paused without validator peer",
			slog.Uint64("local_height", localHead.Height),
			slog.String("local_hash", localHead.BlockHash.String()),
		)
		return true
	}
	for _, peerID := range peerIDs {
		statusTimeout := productionSyncGateTimeout(slotDeadline)
		if statusTimeout == 0 {
			node.logger.Warn("posnode production paused near sync gate deadline",
				slog.Uint64("local_height", localHead.Height),
				slog.Time("slot_deadline", slotDeadline),
			)
			return true
		}
		statusContext, cancel := context.WithTimeout(ctx, statusTimeout)
		status, err := node.requestStatus(statusContext, peerID)
		cancel()
		if err != nil {
			node.logger.Warn("posnode production sync gate status failed",
				slog.String("peer_id", peerID),
				slog.Uint64("local_height", localHead.Height),
				slog.Any("error", err),
			)
			continue
		}
		if peerNeedsBlockSync(localHead, status) {
			return true
		}
		return false
	}
	node.logger.Warn("posnode production paused because validator status unavailable",
		slog.Uint64("local_height", localHead.Height),
		slog.String("local_hash", localHead.BlockHash.String()),
		slog.Int("peer_count", len(peerIDs)),
	)
	return true
}

// productionSyncGatePeerIDs 选择出块前探测节点 + 只探测少量已连接验证者避免拖过 slot deadline。
func (node *posNode) productionSyncGatePeerIDs(localHeight uint64) []string {
	connectedPeerIDs := node.connectedValidatorPeerIDsSnapshot(true)
	return rotateLimitedPeerIDs(connectedPeerIDs, localHeight, productionSyncGateMaxPeers)
}

// connectedValidatorPeerIDsSnapshot 获取已连接验证者节点 + 出块门禁和恢复广播必须只依赖当前在线验证者。
func (node *posNode) connectedValidatorPeerIDsSnapshot(excludeLocal bool) []string {
	if node.host == nil {
		return nil
	}
	peerIDs := node.validatorPeerIDsSnapshot(excludeLocal)
	connectedPeerIDs := make([]string, 0, len(peerIDs))
	for _, peerID := range peerIDs {
		if _, connected := node.host.ConnectionState(peerID); connected {
			connectedPeerIDs = append(connectedPeerIDs, peerID)
		}
	}
	return connectedPeerIDs
}

// rotateLimitedPeerIDs 轮转抽样节点 + 避免长期固定探测同一批慢节点影响 leader 出块。
func rotateLimitedPeerIDs(peerIDs []string, seed uint64, limit int) []string {
	if len(peerIDs) == 0 || limit <= 0 {
		return nil
	}
	if len(peerIDs) <= limit {
		result := make([]string, len(peerIDs))
		copy(result, peerIDs)
		return result
	}
	result := make([]string, 0, limit)
	startIndex := int(seed % uint64(len(peerIDs)))
	for offset := 0; offset < len(peerIDs) && len(result) < limit; offset++ {
		result = append(result, peerIDs[(startIndex+offset)%len(peerIDs)])
	}
	return result
}

func productionSyncGateTimeout(slotDeadline time.Time) time.Duration {
	remaining := time.Until(slotDeadline)
	if remaining <= productionSyncGateMinRemaining {
		return 0
	}
	timeout := productionSyncGateMaxTimeout
	remainingBudget := remaining - productionSyncGateMinRemaining
	if remainingBudget < timeout {
		timeout = remainingBudget
	}
	if timeout <= 0 {
		return 0
	}
	return timeout
}

func (node *posNode) requiresConnectedValidatorPeerForProduction() bool {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	return node.requiresConnectedValidatorPeerForProductionLocked()
}

func (node *posNode) requiresConnectedValidatorPeerForProductionLocked() bool {
	return len(node.epochSnapshot.Validators) > 1
}

func (node *posNode) hasMinimumActiveValidatorsForProductionLocked() bool {
	return hasMinimumActiveValidatorCount(len(node.epochSnapshot.Validators))
}

func hasMinimumActiveValidatorCount(activeValidatorCount int) bool {
	return activeValidatorCount >= minActiveValidatorsForProduction
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
	epochContextValue, err := node.epochContextForSlotLocked(proposal.Header.Slot)
	if err != nil {
		node.mutex.Unlock()
		return err
	}
	leader, exists := epochContextValue.Snapshot.ValidatorByID(proposal.Header.LeaderID)
	if !exists {
		node.mutex.Unlock()
		return fmt.Errorf("posnode: synced proposal leader not in snapshot")
	}
	epochSnapshot := epochContextValue.Snapshot
	leaderSchedule := epochContextValue.Schedule
	blockhashQueue := node.blockhashQueue
	node.mutex.Unlock()

	parentState, parentReady := node.ensureParentAvailable(ctx, proposal.Header.ParentHash)
	if !parentReady {
		node.storeOrphanProposal(proposal)
		return nil
	}
	parentSlot, err := node.parentSlotForProposal(proposal.Header.ParentHash)
	if err != nil {
		return err
	}
	verifier := consensus.ProposalVerifier{ChainID: node.config.ChainID, Executor: node.executor}
	nextState, err := verifier.VerifyProposal(ctx, consensus.VerifyProposalRequest{
		Proposal:       proposal,
		ParentSlot:     parentSlot,
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
	if _, err := node.ledger.SaveBlockCandidate(commitRequest); err != nil {
		return err
	}
	if err := node.promoteBlockIfCertified(ctx, proposalHash); err != nil {
		node.logger.Warn("posnode synced candidate promotion check failed",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("block_hash", proposalHash.String()),
			slog.Any("error", err),
		)
	}
	node.retryOrphanChildren(ctx, proposalHash)
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
	for _, peerID := range node.syncPeerIDsSnapshot() {
		statusContext, cancel := context.WithTimeout(ctx, node.peerStatusTimeout())
		_, statusErr := node.requestStatus(statusContext, peerID)
		cancel()
		if statusErr != nil {
			node.logSyncMiss(peerID, parentHash, statusErr)
			continue
		}
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

func (node *posNode) parentSlotForProposal(parentHash structure.Hash) (uint64, error) {
	if parentHash.String() == node.config.GenesisHash {
		return 0, nil
	}
	head := node.ledger.Head()
	if parentHash == head.BlockHash {
		return head.Slot, nil
	}
	parentProposal, found, err := node.ledger.BlockByHash(parentHash)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("posnode: parent block %s not found", parentHash.String())
	}
	return parentProposal.Header.Slot, nil
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

// syncPeerIDsSnapshot 选择状态同步节点 + 本地 epoch 可能过期所以合并引导、已知和验证者节点。
func (node *posNode) syncPeerIDsSnapshot() []string {
	node.refreshKnownPeersFromHost()
	node.mutex.Lock()
	defer node.mutex.Unlock()
	peerIDs := make([]string, 0, len(node.config.BootstrapPeers)+len(node.knownPeerIDs)+len(node.epochSnapshot.Validators))
	for _, peerConfig := range node.config.BootstrapPeers {
		if peerConfig.PeerID == "" {
			continue
		}
		peerIDs = append(peerIDs, peerConfig.PeerID)
	}
	peerIDs = append(peerIDs, node.knownPeerIDs...)
	peerIDs = append(peerIDs, node.validatorPeerIDsLocked()...)
	return node.filterTransactionRoutePeersLocked(peerIDs, true)
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
	livenessGate := node.refreshLivenessGate(time.Now())
	if node.ledger == nil {
		node.mutex.Lock()
		mempoolSize := len(node.mempool)
		knownPeerCount := len(node.knownPeerIDs)
		metrics := node.metrics.snapshot()
		node.mutex.Unlock()
		p2pSecure := false
		if node.host != nil {
			p2pSecure = node.host.SecureSessionEnabled()
		}
		return statusResponseEnvelope{
			ChainID:           node.config.ChainID,
			ChainIdentityHash: node.config.ChainIdentityHash,
			GenesisHash:       node.config.GenesisHash,
			NodeName:          node.config.NodeName,
			PeerID:            node.peerKeyPair.peerID,
			NodeMode:          "bootstrap",
			NodeRole:          string(node.config.ResolvedNodeRole),
			NodeRoles:         p2p.PeerRolesNames(node.config.ResolvedNodeRoles, node.config.ResolvedNodeCapabilities),
			NodeCapabilities:  uint64(node.config.ResolvedNodeCapabilities),
			CapabilityNames:   p2p.PeerCapabilityNames(node.config.ResolvedNodeCapabilities),
			ValidatorEnabled:  node.config.validatorEnabled(),
			ConsensusEnabled:  node.config.consensusEnabled(),
			MempoolSize:       mempoolSize,
			KnownPeerCount:    knownPeerCount,
			P2PSecure:         p2pSecure,
			P2PInsecure:       node.config.allowInsecureP2P(),
			RPCForwarding:     false,
			StateRecovery:     false,
			Liveness:          livenessGate,
			Metrics:           metrics,
		}
	}
	head := node.ledger.Head()
	finalizedSlot := node.finalizedSlotForHead(head)
	node.mutex.Lock()
	currentLeader := ""
	startSlot := node.currentRoutingSlotLocked()
	p2pSecure := false
	if node.host != nil {
		p2pSecure = node.host.SecureSessionEnabled()
	}
	if node.epochSnapshot.StartSlot <= startSlot && startSlot <= node.epochSnapshot.EndSlot {
		if leader, err := node.leaderSchedule.LeaderForSlot(startSlot); err == nil {
			currentLeader = string(leader)
		}
	}
	turbine := node.turbinePositionForSlotLocked(startSlot)
	transactionFastPath := node.transactionFastPathForSlotLocked(startSlot, true)
	consensusStatus := node.consensusStatusForSlotLocked(startSlot)
	consensusStatus.Liveness = livenessGate
	status := statusResponseEnvelope{
		ChainID:           node.config.ChainID,
		ChainIdentityHash: node.config.ChainIdentityHash,
		GenesisHash:       node.config.GenesisHash,
		NodeName:          node.config.NodeName,
		PeerID:            node.peerKeyPair.peerID,
		NodeMode:          "posnode",
		NodeRole:          string(node.config.ResolvedNodeRole),
		NodeRoles:         p2p.PeerRolesNames(node.config.ResolvedNodeRoles, node.config.ResolvedNodeCapabilities),
		NodeCapabilities:  uint64(node.config.ResolvedNodeCapabilities),
		CapabilityNames:   p2p.PeerCapabilityNames(node.config.ResolvedNodeCapabilities),
		ValidatorEnabled:  node.config.validatorEnabled(),
		ConsensusEnabled:  node.config.consensusEnabled(),
		HeadHeight:        head.Height,
		HeadSlot:          head.Slot,
		HeadHash:          head.BlockHash.String(),
		HeadQCHash:        head.QCHash.String(),
		FinalizedHeight:   head.FinalizedHeight,
		FinalizedHash:     head.FinalizedHash.String(),
		FinalizedSlot:     finalizedSlot,
		FinalityDepth:     node.ledger.FinalityDepth(),
		EpochID:           node.epochSnapshot.EpochID,
		MempoolSize:       len(node.mempool),
		ValidatorCount:    len(node.epochSnapshot.Validators),
		KnownPeerCount:    len(node.knownPeerIDs),
		P2PSecure:         p2pSecure,
		P2PInsecure:       node.config.allowInsecureP2P(),
		RPCForwarding:     node.config.publicRPCMode() && node.config.transactionForwardEnabled(),
		StateRecovery:     !node.config.DisableStateRecovery,
		CurrentLeader:     currentLeader,
		UpcomingLeaders:   node.upcomingLeadersLocked(startSlot, node.config.TransactionLeaderForwardSlots+1),
		Turbine:           turbine,
		TransactionFast:   transactionFastPath,
		Liveness:          livenessGate,
		Consensus:         consensusStatus,
		Metrics:           node.metrics.snapshot(),
	}
	node.mutex.Unlock()
	return status
}

func (node *posNode) finalizedSlotForHead(head blockchain.Head) uint64 {
	if head.FinalizedHeight == 0 || head.FinalizedHash.IsZero() {
		return 0
	}
	proposal, blockHash, found, err := node.ledger.BlockByHeight(head.FinalizedHeight)
	if err != nil || !found || blockHash != head.FinalizedHash {
		return 0
	}
	return proposal.Header.Slot
}
