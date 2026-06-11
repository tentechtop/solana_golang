package p2p

import (
	"errors"
	"fmt"
	"log/slog"
	"time"
)

func (host *Host) acceptInboundMessage(message Message) error {
	trafficClass := host.messageTrafficClass(message)
	snapshot, err := host.peerProtection.acceptInboundMessage(message.FromPeerID, message.ID, trafficClass, time.Now())
	if err == nil {
		return nil
	}

	host.syncPeerProtectionSnapshot(snapshot)
	switch {
	case errors.Is(err, ErrRateLimited):
		host.metrics.messagesRateLimited.Add(1)
		if trafficClass == ProtocolClassControl {
			host.metrics.controlMessagesRateLimited.Add(1)
		} else {
			host.metrics.dataMessagesRateLimited.Add(1)
		}
	case errors.Is(err, ErrDuplicateMessage):
		host.metrics.duplicateMessages.Add(1)
	case errors.Is(err, ErrPeerBlocked):
		host.metrics.peerBlocks.Add(1)
	}
	host.logger.Warn("p2p peer protection rejected message",
		slog.String("peer_id", message.FromPeerID),
		slog.String("message_id", message.ID),
		slog.Uint64("protocol_id", uint64(message.Type)),
		slog.Uint64("protocol_class", uint64(trafficClass)),
		slog.Any("error", err),
	)
	return err
}

func (host *Host) checkPeerDialAllowed(peerID string) error {
	if err := host.peerProtection.checkDial(peerID, time.Now()); err == nil {
		return nil
	} else {
		if errors.Is(err, ErrPeerBackoff) {
			host.metrics.dialBackoffs.Add(1)
		}
		host.syncPeerProtectionSnapshot(host.peerProtection.snapshot(peerID, time.Now()))
		return err
	}
}

func (host *Host) recordPeerDialFailure(peerID string) {
	snapshot := host.peerProtection.recordDialFailure(peerID, time.Now())
	host.syncPeerProtectionSnapshot(snapshot)
	if snapshot.Blocked {
		host.metrics.peerBlocks.Add(1)
	}
	host.logger.Warn("p2p peer dial backoff updated",
		slog.String("peer_id", peerID),
		slog.Int("score", snapshot.Score),
		slog.Int64("backoff_until_unix_milli", snapshot.DialBackoffUntilUnixMilli),
		slog.Bool("blocked", snapshot.Blocked),
	)
}

func (host *Host) recordPeerProtectionSuccess(peerID string) {
	snapshot := host.peerProtection.recordSuccess(peerID, time.Now())
	host.syncPeerProtectionSnapshot(snapshot)
}

func (host *Host) penalizePeer(peerID string, penalty int, reason string) {
	snapshot := host.peerProtection.penalize(peerID, penalty, reason, time.Now())
	host.syncPeerProtectionSnapshot(snapshot)
	if snapshot.Blocked {
		host.metrics.peerBlocks.Add(1)
		host.logger.Warn("p2p peer blocked",
			slog.String("peer_id", peerID),
			slog.Int("score", snapshot.Score),
			slog.String("reason", reason),
			slog.Int64("blocked_until_unix_milli", snapshot.BlockedUntilUnixMilli),
		)
	}
}

func (host *Host) syncPeerProtectionSnapshot(snapshot peerProtectionSnapshot) {
	if snapshot.PeerID == "" {
		return
	}

	var storedPeer Peer
	changed := false
	host.mutex.Lock()
	if peer, ok := host.peers[snapshot.PeerID]; ok {
		changed = peer.Score != snapshot.Score
		changed = changed || (snapshot.Blocked && peer.Status != PeerStatusBlocked)
		changed = changed || (!snapshot.Blocked && peer.Status == PeerStatusBlocked)
		peer.Score = snapshot.Score
		if snapshot.Blocked {
			peer.Status = PeerStatusBlocked
		} else if peer.Status == PeerStatusBlocked {
			peer.Status = PeerStatusDisconnected
		}
		host.peers[snapshot.PeerID] = peer
		storedPeer = peer.Clone()
	}
	host.mutex.Unlock()

	if changed {
		host.savePeerBestEffort(storedPeer)
	}
}

func (host *Host) protocolHandlerDuration(start time.Time, message Message) {
	elapsed := time.Since(start)
	threshold := host.peerProtection.slowHandlerThreshold()
	if threshold <= 0 || elapsed < threshold {
		return
	}
	host.metrics.slowProtocolHandlers.Add(1)
	host.logger.Warn("p2p slow protocol handler",
		slog.String("peer_id", message.FromPeerID),
		slog.String("message_id", message.ID),
		slog.Uint64("protocol_id", uint64(message.Type)),
		slog.Duration("elapsed", elapsed),
	)
}

func (host *Host) normalizeBroadcastPeers(peerIDs []string) []string {
	limit := host.peerProtection.broadcastLimit()
	seen := make(map[string]struct{}, len(peerIDs))
	normalized := make([]string, 0, len(peerIDs))
	for _, peerID := range peerIDs {
		if _, exists := seen[peerID]; exists || peerID == "" || peerID == host.peerID {
			continue
		}
		if err := host.checkPeerDialAllowed(peerID); err != nil {
			continue
		}
		seen[peerID] = struct{}{}
		normalized = append(normalized, peerID)
		if limit > 0 && len(normalized) >= limit {
			break
		}
	}
	dropped := len(peerIDs) - len(normalized)
	if dropped > 0 {
		host.metrics.broadcastPeersDropped.Add(uint64(dropped))
	}
	return normalized
}

func (host *Host) enqueueProtocolMessage(connection Connection, message Message) error {
	if err := host.protocolDispatcher.enqueue(connection, message); err != nil {
		host.logger.Warn("p2p protocol queue rejected message",
			slog.String("peer_id", message.FromPeerID),
			slog.String("message_id", message.ID),
			slog.Uint64("protocol_id", uint64(message.Type)),
			slog.Any("error", err),
		)
		return err
	}
	return nil
}

func peerProtectionErrorClosesConnection(err error) bool {
	return errors.Is(err, ErrRateLimited) || errors.Is(err, ErrPeerBlocked)
}

func peerProtectionDialError(peerID string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("p2p: peer %s unavailable: %w", peerID, err)
}
