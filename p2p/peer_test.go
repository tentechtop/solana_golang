package p2p

import (
	"testing"

	"solana_golang/utils"
)

func TestPeerBestAddressUsesProtocolOrder(t *testing.T) {
	peerID := testPeerID(1)
	tcpAddress := testAddress(t, utils.ProtocolTCP, 3001, peerID)
	quicAddress := testAddress(t, utils.ProtocolQUIC, 3002, peerID)

	peer, err := NewPeer(peerID, []utils.MultiAddress{tcpAddress, quicAddress})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}

	address, ok := peer.BestAddress([]utils.MultiAddressProtocol{utils.ProtocolQUIC, utils.ProtocolTCP})
	if !ok {
		t.Fatal("BestAddress() ok = false, want true")
	}
	if address.Protocol != utils.ProtocolQUIC {
		t.Fatalf("Protocol = %q, want %q", address.Protocol, utils.ProtocolQUIC)
	}
}

func TestPeerRejectsAddressMismatch(t *testing.T) {
	peerID := testPeerID(2)
	otherPeerID := testPeerID(3)
	address := testAddress(t, utils.ProtocolTCP, 3001, otherPeerID)

	if _, err := NewPeer(peerID, []utils.MultiAddress{address}); err == nil {
		t.Fatal("NewPeer(mismatch) error = nil, want error")
	}
}

func TestPeerMergeAndSnapshot(t *testing.T) {
	peerID := testPeerID(13)
	tcpAddress := testAddress(t, utils.ProtocolTCP, 3001, peerID)
	quicAddress := testAddress(t, utils.ProtocolQUIC, 3002, peerID)

	peer, err := NewPeer(peerID, []utils.MultiAddress{tcpAddress})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	next, err := NewPeer(peerID, []utils.MultiAddress{quicAddress})
	if err != nil {
		t.Fatalf("NewPeer(next) error = %v", err)
	}
	next.Role = PeerRoleValidator
	next.Capabilities = PeerCapabilityValidator | PeerCapabilityDHT
	next.LatestSlot = 99
	next.Validator = true
	next.StakeLamports = 1000

	if err := peer.Merge(next); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if len(peer.Addresses) != 2 {
		t.Fatalf("address count = %d, want 2", len(peer.Addresses))
	}
	if peer.Role != PeerRoleValidator {
		t.Fatalf("Role = %q, want %q", peer.Role, PeerRoleValidator)
	}
	if peer.LatestSlot != 99 {
		t.Fatalf("LatestSlot = %d, want 99", peer.LatestSlot)
	}

	snapshot := peer.Snapshot()
	snapshot.Addresses[0].Port = 1
	if peer.Addresses[0].Port == 1 {
		t.Fatal("Snapshot() shared addresses, want isolated copy")
	}
}

func TestPeerStateTransitions(t *testing.T) {
	peerID := testPeerID(14)
	address := testAddress(t, utils.ProtocolTCP, 3001, peerID)
	peer, err := NewPeer(peerID, []utils.MultiAddress{address})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}

	peer.RecordError(assertError("dial failed"))
	if peer.FailureCount != 1 {
		t.Fatalf("FailureCount = %d, want 1", peer.FailureCount)
	}
	peer.MarkConnected()
	if peer.Status != PeerStatusConnected {
		t.Fatalf("Status = %q, want connected", peer.Status)
	}
	if peer.FailureCount != 0 {
		t.Fatalf("FailureCount = %d, want 0", peer.FailureCount)
	}
	peer.MarkDisconnected()
	if peer.Status != PeerStatusDisconnected {
		t.Fatalf("Status = %q, want disconnected", peer.Status)
	}
}

type assertError string

func (err assertError) Error() string {
	return string(err)
}
