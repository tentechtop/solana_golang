package p2p

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
)

func TestPeerBinaryRoundTrip(t *testing.T) {
	peer := kadTestPeer(t, 0x41, 4021)
	verifiedAddress := testAddress(t, utils.ProtocolTCP, 4121, peer.ID)
	peer.AddVerifiedAddress(verifiedAddress)
	peer.Status = PeerStatusConnected
	peer.Role = PeerRoleValidator
	peer.ProtocolVersion = "1"
	peer.SoftwareVersion = "test/0.1.0"
	peer.PreferredProtocols = []utils.MultiAddressProtocol{utils.ProtocolTCP, utils.ProtocolQUIC}
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
	if len(decoded.PreferredProtocols) != 2 || decoded.PreferredProtocols[0] != utils.ProtocolTCP {
		t.Fatalf("PreferredProtocols = %+v, want tcp first", decoded.PreferredProtocols)
	}
	if len(decoded.AdvertisedAddresses) != len(peer.AdvertisedAddresses) {
		t.Fatalf("AdvertisedAddresses = %+v, want %+v", decoded.AdvertisedAddresses, peer.AdvertisedAddresses)
	}
	if len(decoded.VerifiedAddresses) != 1 || decoded.VerifiedAddresses[0].String() != verifiedAddress.String() {
		t.Fatalf("VerifiedAddresses = %+v, want %s", decoded.VerifiedAddresses, verifiedAddress.String())
	}
}

func TestPeerBinaryRoundTripPreservesSignedRecord(t *testing.T) {
	peer := signedKADTestPeer(t, 4027)

	encoded, err := peer.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalPeerBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalPeerBinary() error = %v", err)
	}

	if !bytes.Equal(decoded.SignedRecord, peer.SignedRecord) {
		t.Fatal("SignedRecord was not preserved")
	}
}

func TestUnmarshalPeerBinaryAcceptsVersionOneWithoutSignedRecord(t *testing.T) {
	peer := kadTestPeer(t, 0x47, 4028)
	encoded := marshalPeerStoreVersionOne(t, peer)

	decoded, err := UnmarshalPeerBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalPeerBinary(v1) error = %v", err)
	}
	if decoded.ID != peer.ID {
		t.Fatalf("ID = %q, want %q", decoded.ID, peer.ID)
	}
	if len(decoded.SignedRecord) != 0 {
		t.Fatalf("len(SignedRecord) = %d, want 0", len(decoded.SignedRecord))
	}
}

func marshalPeerStoreVersionOne(t *testing.T, peer Peer) []byte {
	t.Helper()
	peer = normalizePeerForStorage(peer)
	writer := borsh.NewWriter(DefaultMaxMessageSize)
	writer.WriteUint16(1)
	if err := writer.WriteString(peer.ID); err != nil {
		t.Fatalf("marshal peer id: %v", err)
	}
	if err := writePeerAddressSlice(writer, peer.Addresses); err != nil {
		t.Fatalf("marshal peer addresses: %v", err)
	}
	if err := writer.WriteString(string(peer.Status)); err != nil {
		t.Fatalf("marshal peer status: %v", err)
	}
	if err := writer.WriteString(string(peer.Role)); err != nil {
		t.Fatalf("marshal peer role: %v", err)
	}
	writer.WriteUint64(uint64(peer.Capabilities))
	if err := writer.WriteString(peer.ProtocolVersion); err != nil {
		t.Fatalf("marshal peer protocol version: %v", err)
	}
	if err := writer.WriteString(peer.SoftwareVersion); err != nil {
		t.Fatalf("marshal peer software version: %v", err)
	}
	writer.WriteUint64(peer.LatestSlot)
	writer.WriteUint64(peer.BlockHeight)
	if err := writer.WriteString(peer.BestBlockHash); err != nil {
		t.Fatalf("marshal peer best block hash: %v", err)
	}
	writer.WriteBool(peer.Validator)
	writer.WriteUint64(peer.StakeLamports)
	writer.WriteInt64(int64(peer.Score))
	writer.WriteInt64(peer.FirstSeenUnixMilli)
	writer.WriteInt64(peer.LastSeenUnixMilli)
	writer.WriteInt64(peer.LastConnectedUnixMilli)
	writer.WriteInt64(peer.LastDisconnectedUnixMilli)
	writer.WriteInt64(peer.LastErrorUnixMilli)
	if err := writer.WriteString(peer.LastError); err != nil {
		t.Fatalf("marshal peer last error: %v", err)
	}
	writer.WriteUint32(peer.FailureCount)
	writer.WriteUint64(peer.SentBytes)
	writer.WriteUint64(peer.ReceivedBytes)
	writer.WriteInt64(peer.LastRoundTripTimeMilli)
	if err := writePeerMetadata(writer, peer.Metadata); err != nil {
		t.Fatalf("marshal peer metadata: %v", err)
	}
	return writer.BytesView()
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
		PeerID:        localPeerID,
		AllowInsecure: true,
		PeerStore:     store,
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

func TestHostLoadsStoredPeerWithExpiredSignedRecord(t *testing.T) {
	localIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.0")
	remoteIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.1")
	remoteAddress := testAddress(t, utils.ProtocolTCP, 4029, remoteIdentity.PeerID)
	storedPeer, err := NewPeer(remoteIdentity.PeerID, []utils.MultiAddress{remoteAddress})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	storedPeer.SignedRecord = expiredSignedPeerRecordBytes(t, storedPeer, remoteIdentity)

	store := NewMemoryPeerStore()
	if err := store.SavePeer(context.Background(), storedPeer); err != nil {
		t.Fatalf("SavePeer(stored) error = %v", err)
	}
	host, err := NewHost(HostConfig{
		PeerID:              localIdentity.PeerID,
		SecureIdentity:      localIdentity,
		EnableSecureSession: true,
		PeerStore:           store,
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
	loadedPeer, ok := host.Peer(remoteIdentity.PeerID)
	if !ok {
		t.Fatal("stored peer was not restored")
	}
	if len(loadedPeer.SignedRecord) != 0 {
		t.Fatal("expired SignedRecord was restored, want stripped")
	}

	peers, err := store.LoadPeers(context.Background(), 10)
	if err != nil {
		t.Fatalf("LoadPeers() error = %v", err)
	}
	if len(peers) != 1 || len(peers[0].SignedRecord) != 0 {
		t.Fatalf("persisted SignedRecord length = %d, want 0", len(peers[0].SignedRecord))
	}
}

func TestHostRejectsPeersOverLimit(t *testing.T) {
	host, err := NewHost(HostConfig{
		PeerID:        kadTestPeerID(0),
		AllowInsecure: true,
		MaxPeers:      1,
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
