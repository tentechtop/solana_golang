package p2p

import (
	"context"
	"errors"
	"testing"
)

func TestPeerBinaryRoundTrip(t *testing.T) {
	peer := kadTestPeer(t, 0x41, 4021)
	peer.Status = PeerStatusConnected
	peer.Role = PeerRoleValidator
	peer.ProtocolVersion = "1"
	peer.SoftwareVersion = "test/0.1.0"
	peer.LatestSlot = 88
	peer.BlockHeight = 77
	peer.BestBlockHash = testPeerID(31)
	peer.Validator = true
	peer.StakeLamports = 123
	peer.Score = 9
	peer.LastConnectedUnixMilli = 1001
	peer.LastDisconnectedUnixMilli = 1002
	peer.LastErrorUnixMilli = 1003
	peer.LastError = "dial failed"
	peer.FailureCount = 2
	peer.SentBytes = 11
	peer.ReceivedBytes = 22
	peer.LastRoundTripTimeMilli = 33
	peer.Metadata = map[string]string{"zone": "local"}

	encoded, err := peer.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalPeerBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalPeerBinary() error = %v", err)
	}

	if decoded.ID != peer.ID {
		t.Fatalf("ID = %q, want %q", decoded.ID, peer.ID)
	}
	if decoded.Role != PeerRoleValidator {
		t.Fatalf("Role = %q, want validator", decoded.Role)
	}
	if decoded.FailureCount != peer.FailureCount {
		t.Fatalf("FailureCount = %d, want %d", decoded.FailureCount, peer.FailureCount)
	}
	if decoded.Metadata["zone"] != "local" {
		t.Fatalf("metadata zone = %q, want local", decoded.Metadata["zone"])
	}
}

func TestMemoryPeerStoreSaveLoadDelete(t *testing.T) {
	store := NewMemoryPeerStore()
	peer := kadTestPeer(t, 0x42, 4022)

	if err := store.SavePeer(context.Background(), peer); err != nil {
		t.Fatalf("SavePeer() error = %v", err)
	}
	peers, err := store.LoadPeers(context.Background(), 10)
	if err != nil {
		t.Fatalf("LoadPeers() error = %v", err)
	}
	if len(peers) != 1 || peers[0].ID != peer.ID {
		t.Fatalf("LoadPeers() = %+v, want saved peer", peers)
	}
	if err := store.DeletePeer(context.Background(), peer.ID); err != nil {
		t.Fatalf("DeletePeer() error = %v", err)
	}
	peers, err = store.LoadPeers(context.Background(), 10)
	if err != nil {
		t.Fatalf("LoadPeers(after delete) error = %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("len(peers) = %d, want 0", len(peers))
	}
}

func TestHostLoadsAndPersistsPeersThroughStore(t *testing.T) {
	localPeerID := kadTestPeerID(0)
	storedPeer := kadTestPeer(t, 0x43, 4023)
	newPeer := kadTestPeer(t, 0x44, 4024)
	store := NewMemoryPeerStore()
	if err := store.SavePeer(context.Background(), storedPeer); err != nil {
		t.Fatalf("SavePeer(stored) error = %v", err)
	}

	host, err := NewHost(HostConfig{
		PeerID:    localPeerID,
		PeerStore: store,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	loaded, err := host.LoadStoredPeers(context.Background(), 10)
	if err != nil {
		t.Fatalf("LoadStoredPeers() error = %v", err)
	}
	if loaded != 1 {
		t.Fatalf("loaded = %d, want 1", loaded)
	}
	if _, ok := host.Peer(storedPeer.ID); !ok {
		t.Fatal("stored peer was not restored")
	}
	if err := host.AddPeer(newPeer); err != nil {
		t.Fatalf("AddPeer() error = %v", err)
	}

	peers, err := store.LoadPeers(context.Background(), 10)
	if err != nil {
		t.Fatalf("LoadPeers() error = %v", err)
	}
	if !containsPeerID(peers, newPeer.ID) {
		t.Fatalf("store peers = %+v, want new peer", peers)
	}
}

func TestHostRejectsPeersOverLimit(t *testing.T) {
	host, err := NewHost(HostConfig{
		PeerID:   kadTestPeerID(0),
		MaxPeers: 1,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	if err := host.AddPeer(kadTestPeer(t, 0x45, 4025)); err != nil {
		t.Fatalf("AddPeer(first) error = %v", err)
	}
	if err := host.AddPeer(kadTestPeer(t, 0x46, 4026)); !errors.Is(err, ErrMaxPeersReached) {
		t.Fatalf("AddPeer(second) error = %v, want ErrMaxPeersReached", err)
	}
}

func containsPeerID(peers []Peer, peerID string) bool {
	for _, peer := range peers {
		if peer.ID == peerID {
			return true
		}
	}
	return false
}
