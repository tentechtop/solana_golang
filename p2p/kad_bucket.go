package p2p

import "time"

type kadBucket struct {
	id                int
	capacity          int
	peerIDs           []string
	peers             map[string]Peer
	lastSeenUnixMilli int64
}

func newKADBucket(id int, capacity int) *kadBucket {
	return &kadBucket{
		id:       id,
		capacity: capacity,
		peerIDs:  make([]string, 0, capacity),
		peers:    make(map[string]Peer),
	}
}

func (bucket *kadBucket) size() int {
	return len(bucket.peerIDs)
}

func (bucket *kadBucket) contains(peerID string) bool {
	_, ok := bucket.peers[peerID]
	return ok
}

func (bucket *kadBucket) upsertFront(peer Peer) {
	bucket.remove(peer.ID)
	peer.LastSeenUnixMilli = time.Now().UnixMilli()
	bucket.peerIDs = append([]string{peer.ID}, bucket.peerIDs...)
	bucket.peers[peer.ID] = peer.Clone()
	bucket.lastSeenUnixMilli = peer.LastSeenUnixMilli
}

func (bucket *kadBucket) remove(peerID string) (Peer, bool) {
	peer, ok := bucket.peers[peerID]
	if !ok {
		return Peer{}, false
	}
	delete(bucket.peers, peerID)
	for index, currentID := range bucket.peerIDs {
		if currentID != peerID {
			continue
		}
		bucket.peerIDs = append(bucket.peerIDs[:index], bucket.peerIDs[index+1:]...)
		break
	}
	bucket.lastSeenUnixMilli = time.Now().UnixMilli()
	return peer, true
}

func (bucket *kadBucket) peersSnapshot() []Peer {
	peers := make([]Peer, 0, len(bucket.peerIDs))
	for _, peerID := range bucket.peerIDs {
		peer, ok := bucket.peers[peerID]
		if ok {
			peers = append(peers, peer.Clone())
		}
	}
	return peers
}

func (bucket *kadBucket) evictionCandidate() (Peer, bool) {
	var selected Peer
	selectedIndex := -1
	for index, peerID := range bucket.peerIDs {
		peer, ok := bucket.peers[peerID]
		if !ok {
			continue
		}
		if selectedIndex < 0 || comparePeerEviction(peer, selected) < 0 {
			selected = peer
			selectedIndex = index
		}
	}
	return selected, selectedIndex >= 0
}

func comparePeerEviction(first Peer, second Peer) int {
	firstScore := scoreKADPeer(first)
	secondScore := scoreKADPeer(second)
	if firstScore < secondScore {
		return -1
	}
	if firstScore > secondScore {
		return 1
	}
	if first.LastSeenUnixMilli < second.LastSeenUnixMilli {
		return -1
	}
	if first.LastSeenUnixMilli > second.LastSeenUnixMilli {
		return 1
	}
	return 0
}

func scoreKADPeer(peer Peer) int {
	score := peer.Score
	if peer.Status == PeerStatusConnected {
		score += 100
	}
	if peer.Capabilities&PeerCapabilityDHT != 0 {
		score += 30
	}
	score -= int(peer.FailureCount) * 20
	return score
}
