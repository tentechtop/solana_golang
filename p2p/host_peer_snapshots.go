package p2p

import "sort"

// PeerSnapshots 导出节点快照 + 让上层在不持有 Host 内部锁的情况下刷新路由目标。
func (host *Host) PeerSnapshots() []PeerSnapshot {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	peerIDs := make([]string, 0, len(host.peers))
	for peerID := range host.peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)
	snapshots := make([]PeerSnapshot, 0, len(peerIDs))
	for _, peerID := range peerIDs {
		snapshots = append(snapshots, host.peers[peerID].Snapshot())
	}
	return snapshots
}
