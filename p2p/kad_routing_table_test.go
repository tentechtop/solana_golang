package p2p

import (
	"testing"

	"solana_golang/utils"
)

func TestKADBucketIndexAndDistance(t *testing.T) {
	localID := kadTestID(0)
	targetID := localID
	targetID[31] = 1

	index, ok := KADBucketIndex(localID, targetID)
	if !ok {
		t.Fatal("KADBucketIndex() ok = false, want true")
	}
	if index != 0 {
		t.Fatalf("KADBucketIndex() = %d, want 0", index)
	}

	distance := KADCalculateDistance(localID, targetID)
	if distance[31] != 1 {
		t.Fatalf("distance[31] = %d, want 1", distance[31])
	}
	if KADCompareDistance(distance, KADDistance{}) <= 0 {
		t.Fatal("KADCompareDistance() <= 0, want positive distance")
	}
}

func TestKADRoutingTableAddAndClosestPeers(t *testing.T) {
	localPeerID := kadTestPeerID(0)
	table, err := NewKADRoutingTable(KADRoutingTableConfig{
		LocalPeerID:  localPeerID,
		BucketSize:   20,
		FindNodeSize: 3,
	})
	if err != nil {
		t.Fatalf("NewKADRoutingTable() error = %v", err)
	}

	farPeer := kadTestPeer(t, 0x80, 3001)
	nearPeer := kadTestPeer(t, 0x01, 3002)
	midPeer := kadTestPeer(t, 0x10, 3003)
	for _, peer := range []Peer{farPeer, nearPeer, midPeer} {
		if err := table.AddPeer(peer); err != nil {
			t.Fatalf("AddPeer() error = %v", err)
		}
	}

	closest, err := table.ClosestPeers(nearPeer.ID, 2)
	if err != nil {
		t.Fatalf("ClosestPeers() error = %v", err)
	}
	if len(closest) != 2 {
		t.Fatalf("len(closest) = %d, want 2", len(closest))
	}
	if closest[0].ID != nearPeer.ID {
		t.Fatalf("closest[0] = %q, want %q", closest[0].ID, nearPeer.ID)
	}

	health := table.HealthSnapshot()
	if health.TotalPeers != 3 {
		t.Fatalf("TotalPeers = %d, want 3", health.TotalPeers)
	}
	if health.NonEmptyBuckets == 0 {
		t.Fatal("NonEmptyBuckets = 0, want non-empty routing table")
	}
}

func TestKADRoutingTableFullBucketDefersLowScoreCandidate(t *testing.T) {
	localPeerID := kadTestPeerID(0)
	table, err := NewKADRoutingTable(KADRoutingTableConfig{
		LocalPeerID:  localPeerID,
		BucketSize:   1,
		FindNodeSize: 20,
	})
	if err != nil {
		t.Fatalf("NewKADRoutingTable() error = %v", err)
	}

	keptPeer := kadTestPeer(t, 0x80, 3011)
	keptPeer.Status = PeerStatusConnected
	candidatePeer := kadTestPeer(t, 0x81, 3012)
	candidatePeer.FailureCount = 5

	if err := table.AddPeer(keptPeer); err != nil {
		t.Fatalf("AddPeer(kept) error = %v", err)
	}
	if err := table.AddPeer(candidatePeer); err != nil {
		t.Fatalf("AddPeer(candidate) error = %v", err)
	}

	closest, err := table.ClosestPeers(candidatePeer.ID, 10)
	if err != nil {
		t.Fatalf("ClosestPeers() error = %v", err)
	}
	if len(closest) != 1 || closest[0].ID != keptPeer.ID {
		t.Fatalf("closest = %+v, want only kept peer", closest)
	}
	candidates := table.CandidateSnapshots()
	if len(candidates) != 1 || candidates[0].PeerID != candidatePeer.ID {
		t.Fatalf("candidates = %+v, want deferred candidate", candidates)
	}
}

func TestHostAddsPeersToRoutingTable(t *testing.T) {
	localPeerID := kadTestPeerID(0)
	host, err := NewHost(HostConfig{PeerID: localPeerID, AllowInsecure: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	peer := kadTestPeer(t, 0x20, 3021)
	if err := host.AddPeer(peer); err != nil {
		t.Fatalf("AddPeer() error = %v", err)
	}

	closest, err := host.ClosestPeers(peer.ID, 1)
	if err != nil {
		t.Fatalf("ClosestPeers() error = %v", err)
	}
	if len(closest) != 1 || closest[0].ID != peer.ID {
		t.Fatalf("closest = %+v, want added peer", closest)
	}
	if host.RoutingTableHealth().TotalPeers != 1 {
		t.Fatalf("TotalPeers = %d, want 1", host.RoutingTableHealth().TotalPeers)
	}
}

func kadTestPeer(t *testing.T, lastByte byte, port int) Peer {
	t.Helper()
	peerID := kadTestPeerID(lastByte)
	address := testAddress(t, utils.ProtocolTCP, port, peerID)
	peer, err := NewPeer(peerID, []utils.MultiAddress{address})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	peer.Capabilities = PeerCapabilityDHT
	return peer
}

func kadTestPeerID(lastByte byte) string {
	return KADPeerIDFromBytes(kadTestID(lastByte))
}

func kadTestID(lastByte byte) [peerIDByteSize]byte {
	var id [peerIDByteSize]byte
	id[31] = lastByte
	return id
}
