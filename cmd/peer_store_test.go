package main

import (
	"context"
	"testing"

	"solana_golang/p2p"
	p2ppeerstore "solana_golang/p2p/peerstore"
	"solana_golang/utils"
)

func TestDatabasePeerStoreSaveLoadDelete(t *testing.T) {
	databaseInstance := openTestDatabase(t)
	defer databaseInstance.Close()
	store := p2ppeerstore.NewDatabasePeerStore(databaseInstance)
	peerID := testNodeIdentity(t).PeerID
	address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, "127.0.0.1", utils.ProtocolTCP, 4101, peerID)
	if err != nil {
		t.Fatalf("BuildMultiAddress() error = %v", err)
	}
	peer, err := p2p.NewPeer(peerID, []utils.MultiAddress{address})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	peer.Role = p2p.PeerRoleBootnode
	peer.Capabilities = p2p.PeerCapabilityDHT
	peer.SoftwareVersion = "test/0.1.0"

	if err := store.SavePeer(context.Background(), peer); err != nil {
		t.Fatalf("SavePeer() error = %v", err)
	}
	peers, err := store.LoadPeers(context.Background(), 10)
	if err != nil {
		t.Fatalf("LoadPeers() error = %v", err)
	}
	if len(peers) != 1 || peers[0].ID != peerID {
		t.Fatalf("LoadPeers() = %+v, want saved peer", peers)
	}
	if peers[0].Role != p2p.PeerRoleBootnode {
		t.Fatalf("Role = %q, want bootnode", peers[0].Role)
	}

	if err := store.DeletePeer(context.Background(), peerID); err != nil {
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
