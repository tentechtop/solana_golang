package peerstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"solana_golang/database"
	"solana_golang/p2p"
	"solana_golang/utils"
)

func TestDatabasePeerStoreSaveLoadDelete(t *testing.T) {
	store, db := newTestDatabasePeerStore(t)
	peer := testPeer(t, 5101)

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

	exists, err := db.Exists(database.TablePeer, databasePeerStoreKey(peer.ID))
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if !exists {
		t.Fatal("saved peer key does not exist")
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

func TestDatabasePeerStoreLoadLimit(t *testing.T) {
	store, _ := newTestDatabasePeerStore(t)
	for index := 0; index < 3; index++ {
		peer := testPeer(t, 5110+index)
		if err := store.SavePeer(context.Background(), peer); err != nil {
			t.Fatalf("SavePeer(%d) error = %v", index, err)
		}
	}

	peers, err := store.LoadPeers(context.Background(), 2)
	if err != nil {
		t.Fatalf("LoadPeers(limit) error = %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("len(peers) = %d, want 2", len(peers))
	}
	for _, peer := range peers {
		if err := peer.Validate(); err != nil {
			t.Fatalf("loaded peer invalid: %v", err)
		}
	}
}

func TestDatabasePeerStoreHonorsCanceledContext(t *testing.T) {
	store, _ := newTestDatabasePeerStore(t)
	peer := testPeer(t, 5121)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.SavePeer(ctx, peer); !errors.Is(err, context.Canceled) {
		t.Fatalf("SavePeer(canceled) error = %v, want context.Canceled", err)
	}
	peers, err := store.LoadPeers(context.Background(), 10)
	if err != nil {
		t.Fatalf("LoadPeers(after canceled save) error = %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("len(peers) = %d, want 0", len(peers))
	}
	if _, err := store.LoadPeers(ctx, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadPeers(canceled) error = %v, want context.Canceled", err)
	}
	if err := store.DeletePeer(ctx, peer.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeletePeer(canceled) error = %v, want context.Canceled", err)
	}
}

func TestDatabasePeerStoreReportsCorruptPeerData(t *testing.T) {
	store, db := newTestDatabasePeerStore(t)
	key := databasePeerStoreKey(testPeer(t, 5131).ID)
	if err := db.Put(database.TablePeer, key, []byte("bad-peer-binary")); err != nil {
		t.Fatalf("Put(corrupt) error = %v", err)
	}

	_, err := store.LoadPeers(context.Background(), 10)
	if err == nil {
		t.Fatal("LoadPeers(corrupt) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "decode peer") {
		t.Fatalf("LoadPeers(corrupt) error = %v, want decode context", err)
	}
}

func TestDatabasePeerStoreConcurrentSaveSamePeer(t *testing.T) {
	store, _ := newTestDatabasePeerStore(t)
	peer := testPeer(t, 5141)

	var workers sync.WaitGroup
	for index := 0; index < 16; index++ {
		workers.Add(1)
		go func(workerID int) {
			defer workers.Done()
			nextPeer := peer.Clone()
			nextPeer.Score = workerID
			nextPeer.LastError = "worker"
			if err := store.SavePeer(context.Background(), nextPeer); err != nil {
				t.Errorf("SavePeer(%d) error = %v", workerID, err)
			}
		}(index)
	}
	workers.Wait()

	peers, err := store.LoadPeers(context.Background(), 10)
	if err != nil {
		t.Fatalf("LoadPeers() error = %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("len(peers) = %d, want 1", len(peers))
	}
	if peers[0].ID != peer.ID {
		t.Fatalf("peer ID = %q, want %q", peers[0].ID, peer.ID)
	}
}

func newTestDatabasePeerStore(t *testing.T) (p2p.PeerStore, database.Database) {
	t.Helper()
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	store := NewDatabasePeerStore(db)
	if store == nil {
		t.Fatal("NewDatabasePeerStore() = nil")
	}
	return store, db
}

func testPeer(t *testing.T, port int) p2p.Peer {
	t.Helper()
	keyPair, err := utils.GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPair() error = %v", err)
	}
	peerID := utils.Base58Encode(keyPair.PublicKey)
	address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, "127.0.0.1", utils.ProtocolTCP, port, peerID)
	if err != nil {
		t.Fatalf("BuildMultiAddress() error = %v", err)
	}
	peer, err := p2p.NewPeer(peerID, []utils.MultiAddress{address})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	return peer
}
