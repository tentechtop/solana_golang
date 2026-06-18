package posnode

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"solana_golang/p2p"
)

const kadTransactionRouteQueryLimit = 8

func (node *posNode) transactionRouteTargets(ctx context.Context) ([]string, []string) {
	preferredPeerIDs := node.preferredTransactionPeerIDs()
	resolvedPreferred := make([]string, 0, len(preferredPeerIDs))
	seenPreferred := make(map[string]struct{}, len(preferredPeerIDs))
	for _, peerID := range preferredPeerIDs {
		if peerID == "" || peerID == node.peerKeyPair.peerID {
			continue
		}
		if _, exists := seenPreferred[peerID]; exists {
			continue
		}
		seenPreferred[peerID] = struct{}{}
		if node.ensureRoutablePeer(ctx, peerID) {
			resolvedPreferred = append(resolvedPreferred, peerID)
		}
	}

	fallbackPeerIDs := make([]string, 0)
	for _, peerID := range node.validatorPeerIDsSnapshot(true) {
		if peerID == "" || peerID == node.peerKeyPair.peerID {
			continue
		}
		if _, exists := seenPreferred[peerID]; exists {
			continue
		}
		fallbackPeerIDs = append(fallbackPeerIDs, peerID)
	}
	return resolvedPreferred, fallbackPeerIDs
}

func (node *posNode) preferredTransactionPeerIDs() []string {
	node.mutex.Lock()
	defer node.mutex.Unlock()

	startSlot := node.currentRoutingSlotLocked()
	fastPath := node.transactionFastPathForSlotLocked(startSlot, false)
	if fastPath.FastPathAvailable {
		return fastPath.PreferredPeerIDs
	}
	node.logger.Debug("posnode transaction route epoch unavailable", slog.Uint64("slot", startSlot))
	return fastPath.PreferredPeerIDs
}

func (node *posNode) transactionFastPathForSlotLocked(startSlot uint64, excludeLocalPeer bool) transactionFastJSON {
	forwardSlots := node.config.TransactionLeaderForwardSlots
	if forwardSlots < 0 {
		forwardSlots = 0
	}
	fastPath := transactionFastJSON{
		StartSlot:         startSlot,
		ForwardSlots:      forwardSlots,
		ForwardValidators: node.config.forwardTransactionsToValidators(),
	}
	if node.config.EpochSlots == 0 || len(node.epochSnapshot.Validators) == 0 {
		validatorPeerIDs := node.validatorPeerIDsLocked()
		fastPath.ValidatorPeerIDs = append(fastPath.ValidatorPeerIDs, validatorPeerIDs...)
		fastPath.PreferredPeerIDs = node.filterTransactionRoutePeersLocked(validatorPeerIDs, excludeLocalPeer)
		return fastPath
	}
	if err := node.ensureEpochForSlotLocked(startSlot); err != nil {
		validatorPeerIDs := node.validatorPeerIDsLocked()
		fastPath.ValidatorPeerIDs = append(fastPath.ValidatorPeerIDs, validatorPeerIDs...)
		fastPath.PreferredPeerIDs = node.filterTransactionRoutePeersLocked(validatorPeerIDs, excludeLocalPeer)
		return fastPath
	}

	validatorPeerIDs := node.validatorPeerIDsLocked()
	fastPath.ValidatorPeerIDs = append(fastPath.ValidatorPeerIDs, validatorPeerIDs...)
	peerIDs := make([]string, 0, len(node.epochSnapshot.Validators)+forwardSlots+1)
	for offset := 0; offset <= forwardSlots; offset++ {
		slot := startSlot + uint64(offset)
		if slot > node.epochSnapshot.EndSlot {
			break
		}
		leaderID, err := node.leaderSchedule.LeaderForSlot(slot)
		if err != nil {
			continue
		}
		leader, exists := node.epochSnapshot.ValidatorByID(leaderID)
		if exists {
			fastPath.LeaderSlots = append(fastPath.LeaderSlots, leaderSlotJSON{
				Slot:        slot,
				ValidatorID: string(leaderID),
				PeerID:      leader.P2PPeerID,
			})
			peerIDs = append(peerIDs, leader.P2PPeerID)
		}
	}
	if fastPath.ForwardValidators {
		peerIDs = append(peerIDs, validatorPeerIDs...)
	}
	fastPath.FastPathAvailable = true
	fastPath.PreferredPeerIDs = node.filterTransactionRoutePeersLocked(peerIDs, excludeLocalPeer)
	return fastPath
}

func (node *posNode) filterTransactionRoutePeersLocked(peerIDs []string, excludeLocalPeer bool) []string {
	if !excludeLocalPeer {
		return uniquePeerIDs(peerIDs)
	}
	filteredPeerIDs := make([]string, 0, len(peerIDs))
	for _, peerID := range peerIDs {
		if peerID == node.peerKeyPair.peerID {
			continue
		}
		filteredPeerIDs = append(filteredPeerIDs, peerID)
	}
	return uniquePeerIDs(filteredPeerIDs)
}

func (node *posNode) validatorPeerIDsLocked() []string {
	peerIDs := make([]string, 0, len(node.epochSnapshot.Validators))
	for _, validator := range node.epochSnapshot.Validators {
		if validator.P2PPeerID != "" {
			peerIDs = append(peerIDs, validator.P2PPeerID)
		}
	}
	return peerIDs
}

func (node *posNode) currentRoutingSlotLocked() uint64 {
	wallSlot := uint64(1)
	startedAt := node.config.genesisStartTime()
	now := time.Now()
	if now.After(startedAt) {
		elapsedSlots := uint64(now.Sub(startedAt) / node.config.slotDuration())
		wallSlot = elapsedSlots + 1
	}
	headSlot := node.ledger.Head().Slot + 1
	if headSlot > wallSlot {
		return headSlot
	}
	return wallSlot
}

func (node *posNode) ensureRoutablePeer(ctx context.Context, targetPeerID string) bool {
	if _, exists := node.host.Peer(targetPeerID); exists {
		node.addKnownPeerID(targetPeerID)
		return true
	}
	if node.discoverPeerFromLocalKAD(targetPeerID) {
		node.addKnownPeerID(targetPeerID)
		return true
	}
	if node.discoverPeerByFindNode(ctx, targetPeerID) {
		node.addKnownPeerID(targetPeerID)
		return true
	}
	return false
}

func (node *posNode) discoverPeerFromLocalKAD(targetPeerID string) bool {
	peers, err := node.host.ClosestPeers(targetPeerID, kadTransactionRouteQueryLimit)
	if err != nil {
		return false
	}
	found := false
	for _, peer := range peers {
		if err := node.host.AddPeer(peer); err != nil {
			continue
		}
		if peer.ID == targetPeerID {
			found = true
		}
	}
	return found
}

func (node *posNode) discoverPeerByFindNode(ctx context.Context, targetPeerID string) bool {
	requestPayload, err := p2p.NewKADFindNodeRequest(targetPeerID, kadTransactionRouteQueryLimit)
	if err != nil {
		return false
	}
	payload, err := requestPayload.MarshalBinary()
	if err != nil {
		return false
	}

	for _, queryPeerID := range node.peerIDsSnapshot() {
		if queryPeerID == "" || queryPeerID == targetPeerID || queryPeerID == node.peerKeyPair.peerID {
			continue
		}
		if node.queryPeerForTarget(ctx, queryPeerID, targetPeerID, payload) {
			return true
		}
	}
	return false
}

func (node *posNode) queryPeerForTarget(ctx context.Context, queryPeerID string, targetPeerID string, payload []byte) bool {
	request, err := p2p.NewRequestMessage(node.peerKeyPair.peerID, p2p.ProtocolFindNodeRequestV1, payload)
	if err != nil {
		return false
	}
	response, err := node.host.Request(ctx, queryPeerID, request)
	if err != nil {
		node.logger.Debug("posnode kad route lookup failed",
			slog.String("query_peer", queryPeerID),
			slog.String("target_peer", targetPeerID),
			slog.Any("error", err),
		)
		return false
	}
	if response.Type != p2p.ProtocolFindNodeResponseV1 {
		node.logger.Debug("posnode kad route lookup invalid response", slog.String("target_peer", targetPeerID))
		return false
	}
	kadResponse, err := p2p.UnmarshalKADFindNodeResponseBinary(response.Payload)
	if err != nil {
		node.logger.Debug("posnode kad route decode failed", slog.String("target_peer", targetPeerID), slog.Any("error", err))
		return false
	}
	if kadResponse.TargetPeerID != targetPeerID {
		node.logger.Debug("posnode kad route target mismatch", slog.String("target_peer", targetPeerID))
		return false
	}

	found := false
	for _, hint := range kadResponse.Peers {
		peer, err := hint.ToPeer()
		if err != nil {
			continue
		}
		if err := node.host.AddPeer(peer); err != nil {
			continue
		}
		if peer.ID == targetPeerID {
			found = true
		}
	}
	return found
}

func (node *posNode) addKnownPeerID(peerID string) {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	for _, knownPeerID := range node.knownPeerIDs {
		if knownPeerID == peerID {
			return
		}
	}
	node.knownPeerIDs = append(node.knownPeerIDs, peerID)
}

func (node *posNode) upcomingLeadersLocked(startSlot uint64, limit int) []leaderSlotJSON {
	if limit <= 0 {
		return nil
	}
	leaders := make([]leaderSlotJSON, 0, limit)
	for offset := 0; offset < limit; offset++ {
		slot := startSlot + uint64(offset)
		if slot > node.epochSnapshot.EndSlot {
			break
		}
		leaderID, err := node.leaderSchedule.LeaderForSlot(slot)
		if err != nil {
			continue
		}
		leader, exists := node.epochSnapshot.ValidatorByID(leaderID)
		if !exists {
			continue
		}
		leaders = append(leaders, leaderSlotJSON{
			Slot:        slot,
			ValidatorID: string(leaderID),
			PeerID:      leader.P2PPeerID,
		})
	}
	return leaders
}

func uniquePeerIDs(peerIDs []string) []string {
	seen := make(map[string]struct{}, len(peerIDs))
	result := make([]string, 0, len(peerIDs))
	for _, peerID := range peerIDs {
		if peerID == "" {
			continue
		}
		if _, exists := seen[peerID]; exists {
			continue
		}
		seen[peerID] = struct{}{}
		result = append(result, peerID)
	}
	return result
}

func mergeRouteErrors(preferredError error, fallbackError error) error {
	if preferredError == nil {
		return fallbackError
	}
	if fallbackError == nil {
		return preferredError
	}
	return fmt.Errorf("preferred route: %v; fallback route: %w", preferredError, fallbackError)
}
