package p2p

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"solana_golang/utils"
)

// AddPeer 添加或更新节点 + 校验地址归属后写入节点表。
func (host *Host) AddPeer(peer Peer) error {
	return host.addPeer(peer, true)
}

func (host *Host) addPeer(peer Peer, persist bool) error {
	if err := peer.Validate(); err != nil {
		return err
	}
	if err := verifyPeerSignedRecord(peer, host.expectedPeerRecordNetworkID()); err != nil {
		return err
	}

	var storedPeer Peer
	host.mutex.Lock()
	if host.closed {
		host.mutex.Unlock()
		return ErrHostClosed
	}
	if _, ok := host.peers[peer.ID]; !ok && len(host.peers) >= host.maxPeers {
		host.mutex.Unlock()
		return fmt.Errorf("%w: %d", ErrMaxPeersReached, host.maxPeers)
	}
	if current, ok := host.peers[peer.ID]; ok {
		if err := current.Merge(peer); err != nil {
			host.mutex.Unlock()
			return err
		}
		host.peers[peer.ID] = current
		host.addPeerToRoutingTableLocked(current)
		storedPeer = current.Clone()
	} else {
		host.peers[peer.ID] = peer.Clone()
		host.addPeerToRoutingTableLocked(peer)
		storedPeer = peer.Clone()
	}
	host.mutex.Unlock()

	if persist {
		return host.savePeer(context.Background(), storedPeer)
	}
	return nil
}

// Peer 查询节点 + 返回副本避免外部修改内部状态。
func (host *Host) Peer(peerID string) (Peer, bool) {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	peer, ok := host.peers[peerID]
	return peer.Clone(), ok
}

// ClosestPeers 查询 KAD 最近节点 + 用于 find-node 协议和连接候选选择。
func (host *Host) ClosestPeers(targetPeerID string, limit int) ([]Peer, error) {
	if err := validateKADRoutingTable(host.routingTable); err != nil {
		return nil, err
	}
	return host.routingTable.ClosestPeers(targetPeerID, limit)
}

// RoutingTableHealth 查询 KAD 健康状态 + 供监控和调试使用。
func (host *Host) RoutingTableHealth() KADRoutingTableHealthSnapshot {
	if host.routingTable == nil {
		return KADRoutingTableHealthSnapshot{}
	}
	return host.routingTable.HealthSnapshot()
}

// LoadStoredPeers 恢复持久化节点 + 启动时先填充 peer 表和 KAD 路由表。
func (host *Host) LoadStoredPeers(ctx context.Context, limit int) (int, error) {
	if host.peerStore == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = host.persistedPeerLimit
	}
	peers, err := host.peerStore.LoadPeers(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("p2p: load stored peers: %w", err)
	}

	loaded := 0
	for _, peer := range peers {
		if peer.ID == host.peerID {
			continue
		}
		preparedPeer, ok := host.prepareStoredPeer(peer)
		if !ok {
			continue
		}
		if err := host.addPeer(preparedPeer, false); err != nil {
			return loaded, fmt.Errorf("p2p: restore peer %s: %w", preparedPeer.ID, err)
		}
		if len(peer.SignedRecord) > 0 && len(preparedPeer.SignedRecord) == 0 {
			host.savePeerBestEffort(preparedPeer)
		}
		loaded++
	}
	host.logger.Info("p2p stored peers loaded", slog.Int("loaded_peers", loaded), slog.Int("limit", limit))
	return loaded, nil
}

func (host *Host) addPeerToRoutingTableLocked(peer Peer) {
	if !peerShareableInDHT(peer) {
		return
	}
	_ = host.routingTable.AddPeer(peer)
}

// markPeerAddressVerified 标记验证地址 + 仅由主动拨号成功路径提升地址可信度。
func (host *Host) markPeerAddressVerified(peerID string, address utils.MultiAddress) {
	if address.PeerID != peerID {
		return
	}

	var storedPeer Peer
	shouldPersist := false
	host.mutex.Lock()
	if peer, ok := host.peers[peerID]; ok {
		peer.AddVerifiedAddress(address)
		host.peers[peerID] = peer
		host.addPeerToRoutingTableLocked(peer)
		storedPeer = peer.Clone()
		shouldPersist = true
	}
	host.mutex.Unlock()

	if shouldPersist {
		host.savePeerBestEffort(storedPeer)
	}
}

func (host *Host) savePeer(ctx context.Context, peer Peer) error {
	if host.peerStore == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := host.peerStore.SavePeer(ctx, peer); err != nil {
		return fmt.Errorf("p2p: save peer %s: %w", peer.ID, err)
	}
	return nil
}

func (host *Host) savePeerBestEffort(peer Peer) {
	if err := host.savePeer(context.Background(), peer); err != nil {
		host.logger.Warn("p2p peer store save failed",
			slog.String("peer_id", peer.ID),
			slog.Any("error", err),
		)
	}
}

func (host *Host) prepareStoredPeer(peer Peer) (Peer, bool) {
	if len(peer.SignedRecord) == 0 {
		return peer, true
	}
	if err := verifyPeerSignedRecord(peer, host.expectedPeerRecordNetworkID()); err != nil {
		if errors.Is(err, ErrPeerRecordExpired) {
			peer.SignedRecord = nil
			host.logger.Info("p2p stored peer record expired",
				slog.String("peer_id", peer.ID),
			)
			return peer, true
		}
		host.logger.Warn("p2p stored peer skipped",
			slog.String("peer_id", peer.ID),
			slog.Any("error", err),
		)
		return Peer{}, false
	}
	return peer, true
}
