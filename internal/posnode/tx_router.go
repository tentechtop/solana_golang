package posnode

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"solana_golang/consensus"
	"solana_golang/p2p"
)

const kadTransactionRouteQueryLimit = 8
const defaultTransactionMaxHops uint8 = 2
const defaultVoteMaxHops uint8 = 4
const defaultQCMaxHops uint8 = 8
const consensusFallbackFanout = 2

type transactionRouteEnvelope struct {
	OriginPeerID string
	HopCount     uint8
	MaxHops      uint8
}

type voteRouteEnvelope struct {
	OriginPeerID string
	HopCount     uint8
	MaxHops      uint8
}

func (route voteRouteEnvelope) normalized(localPeerID string) voteRouteEnvelope {
	if route.OriginPeerID == "" {
		route.OriginPeerID = localPeerID
	}
	if route.MaxHops == 0 {
		route.MaxHops = defaultVoteMaxHops
	}
	return route
}

func (route voteRouteEnvelope) nextHop(localPeerID string) (voteRouteEnvelope, bool) {
	route = route.normalized(localPeerID)
	if route.HopCount >= route.MaxHops {
		return route, false
	}
	route.HopCount++
	return route, true
}

func (route transactionRouteEnvelope) normalized(localPeerID string) transactionRouteEnvelope {
	if route.OriginPeerID == "" {
		route.OriginPeerID = localPeerID
	}
	if route.MaxHops == 0 {
		route.MaxHops = defaultTransactionMaxHops
	}
	return route
}

func (route transactionRouteEnvelope) nextHop(localPeerID string) (transactionRouteEnvelope, bool) {
	route = route.normalized(localPeerID)
	if route.HopCount >= route.MaxHops {
		return route, false
	}
	route.HopCount++
	return route, true
}

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

	fallbackPeerIDs := node.transactionLayeredFallbackPeerIDs(ctx, seenPreferred, "")
	return resolvedPreferred, fallbackPeerIDs
}

func (node *posNode) transactionForwardTargets(ctx context.Context, excludedPeerID string) []string {
	preferredPeerIDs := node.preferredTransactionPeerIDs()
	resolvedPreferred := make([]string, 0, len(preferredPeerIDs))
	seenPreferred := make(map[string]struct{}, len(preferredPeerIDs))
	for _, peerID := range preferredPeerIDs {
		if peerID == "" || peerID == node.peerKeyPair.peerID || peerID == excludedPeerID {
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
	fallbackPeerIDs := node.transactionLayeredFallbackPeerIDs(ctx, seenPreferred, excludedPeerID)
	return append(resolvedPreferred, fallbackPeerIDs...)
}

func (node *posNode) voteRouteTargets(ctx context.Context, vote consensus.Vote, fromPeerID string, originPeerID string) []string {
	leaderPeerID, leaderID, found := node.leaderPeerForSlot(vote.Slot)
	if !found {
		return nil
	}
	targets := make([]string, 0, 2)
	if leaderPeerID != "" {
		targets = append(targets, leaderPeerID)
	}
	if parentPeerID, _, ok := node.turbineParentPeerID(vote.Slot, leaderID); ok {
		targets = append(targets, parentPeerID)
	}
	return removePeerIDs(targets, node.peerKeyPair.peerID, fromPeerID, originPeerID)
}

func (node *posNode) leaderPeerForSlot(slot uint64) (string, consensus.ValidatorID, bool) {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	epochContextValue, err := node.epochContextForSlotLocked(slot)
	if err != nil {
		return "", "", false
	}
	leaderID, err := epochContextValue.Schedule.LeaderForSlot(slot)
	if err != nil {
		return "", "", false
	}
	leader, exists := epochContextValue.Snapshot.ValidatorByID(leaderID)
	if !exists {
		return "", "", false
	}
	return leader.P2PPeerID, leaderID, true
}

func (node *posNode) validatorIDByPeerID(peerID string) (consensus.ValidatorID, bool) {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	for _, validator := range node.epochSnapshot.Validators {
		if validator.P2PPeerID == peerID {
			return validator.ValidatorID, true
		}
	}
	return "", false
}

func (node *posNode) transactionLayeredFallbackPeerIDs(ctx context.Context, excluded map[string]struct{}, excludedPeerID string) []string {
	node.mutex.Lock()
	startSlot := node.currentRoutingSlotLocked()
	epochContextValue, contextErr := node.epochContextForSlotLocked(startSlot)
	leaderID := consensus.ValidatorID("")
	if contextErr == nil {
		leaderID, contextErr = epochContextValue.Schedule.LeaderForSlot(startSlot)
	}
	node.mutex.Unlock()
	if contextErr == nil {
		peerIDs, _, childErr := node.turbineChildPeerIDsForRoot(ctx, startSlot, leaderID, excludedPeerID)
		if childErr == nil && len(peerIDs) > 0 {
			return filterExcludedPeerIDs(peerIDs, excluded)
		}
	}
	limit := node.config.TurbineFanout
	if limit <= 0 {
		limit = consensusFallbackFanout
	}
	candidates := make([]string, 0)
	for _, peerID := range node.validatorPeerIDsSnapshot(true) {
		if peerID == "" || peerID == excludedPeerID {
			continue
		}
		if _, exists := excluded[peerID]; exists {
			continue
		}
		candidates = append(candidates, peerID)
	}
	return rotateLimitedPeerIDs(uniquePeerIDs(candidates), startSlot, limit)
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
	epochContextValue, err := node.epochContextForSlotLocked(startSlot)
	if err != nil {
		validatorPeerIDs := node.validatorPeerIDsLocked()
		fastPath.ValidatorPeerIDs = append(fastPath.ValidatorPeerIDs, validatorPeerIDs...)
		fastPath.PreferredPeerIDs = node.filterTransactionRoutePeersLocked(validatorPeerIDs, excludeLocalPeer)
		return fastPath
	}

	validatorPeerIDs := validatorPeerIDsFromSnapshot(epochContextValue.Snapshot)
	fastPath.ValidatorPeerIDs = append(fastPath.ValidatorPeerIDs, validatorPeerIDs...)
	peerIDs := make([]string, 0, len(epochContextValue.Snapshot.Validators)+forwardSlots+1)
	for offset := 0; offset <= forwardSlots; offset++ {
		slot := startSlot + uint64(offset)
		if slot > epochContextValue.Snapshot.EndSlot {
			break
		}
		leaderID, err := epochContextValue.Schedule.LeaderForSlot(slot)
		if err != nil {
			continue
		}
		leader, exists := epochContextValue.Snapshot.ValidatorByID(leaderID)
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
	return validatorPeerIDsFromSnapshot(node.epochSnapshot)
}

func validatorPeerIDsFromSnapshot(snapshot consensus.EpochSnapshot) []string {
	peerIDs := make([]string, 0, len(snapshot.Validators))
	for _, validator := range snapshot.Validators {
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
	if node.host == nil {
		return false
	}
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
	epochContextValue, err := node.epochContextForSlotLocked(startSlot)
	if err != nil {
		return nil
	}
	leaders := make([]leaderSlotJSON, 0, limit)
	for offset := 0; offset < limit; offset++ {
		slot := startSlot + uint64(offset)
		if slot > epochContextValue.Snapshot.EndSlot {
			break
		}
		leaderID, err := epochContextValue.Schedule.LeaderForSlot(slot)
		if err != nil {
			continue
		}
		leader, exists := epochContextValue.Snapshot.ValidatorByID(leaderID)
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

func filterExcludedPeerIDs(peerIDs []string, excluded map[string]struct{}) []string {
	if len(excluded) == 0 {
		return uniquePeerIDs(peerIDs)
	}
	result := make([]string, 0, len(peerIDs))
	for _, peerID := range peerIDs {
		if _, exists := excluded[peerID]; exists {
			continue
		}
		result = append(result, peerID)
	}
	return uniquePeerIDs(result)
}

func removePeerIDs(peerIDs []string, removedPeerIDs ...string) []string {
	removed := make(map[string]struct{}, len(removedPeerIDs))
	for _, peerID := range removedPeerIDs {
		if peerID == "" {
			continue
		}
		removed[peerID] = struct{}{}
	}
	result := make([]string, 0, len(peerIDs))
	for _, peerID := range peerIDs {
		if _, exists := removed[peerID]; exists {
			continue
		}
		result = append(result, peerID)
	}
	return uniquePeerIDs(result)
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
