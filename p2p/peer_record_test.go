package p2p

import (
	"bytes"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestSignedPeerRecordRoundTrip(t *testing.T) {
	identity := testSecureSessionIdentity(t, "localnet", "node/1.0.0")
	address := testAddress(t, utils.ProtocolTCP, 5011, identity.PeerID)
	peer, err := NewPeer(identity.PeerID, []utils.MultiAddress{address})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	peer.Role = PeerRoleValidator
	peer.Capabilities = PeerCapabilityDHT | PeerCapabilityValidator
	peer.StakeLamports = 99

	record, err := NewSignedPeerRecord(peer, identity, time.Hour)
	if err != nil {
		t.Fatalf("NewSignedPeerRecord() error = %v", err)
	}
	encoded, err := record.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalSignedPeerRecordBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalSignedPeerRecordBinary() error = %v", err)
	}
	decodedPeer, err := decoded.ToPeer()
	if err != nil {
		t.Fatalf("ToPeer() error = %v", err)
	}
	if decodedPeer.ID != peer.ID {
		t.Fatalf("peer id = %q, want %q", decodedPeer.ID, peer.ID)
	}
	if decodedPeer.StakeLamports != peer.StakeLamports {
		t.Fatalf("StakeLamports = %d, want %d", decodedPeer.StakeLamports, peer.StakeLamports)
	}
	if !bytes.Equal(decodedPeer.SignedRecord, encoded) {
		t.Fatal("SignedRecord was not preserved")
	}
}

func TestSignedPeerRecordRejectsTamperedData(t *testing.T) {
	identity := testSecureSessionIdentity(t, "localnet", "node/1.0.0")
	address := testAddress(t, utils.ProtocolTCP, 5012, identity.PeerID)
	peer, err := NewPeer(identity.PeerID, []utils.MultiAddress{address})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	record, err := NewSignedPeerRecord(peer, identity, time.Hour)
	if err != nil {
		t.Fatalf("NewSignedPeerRecord() error = %v", err)
	}
	encoded, err := record.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	encoded[len(encoded)-1] ^= 0x01
	if _, err := UnmarshalSignedPeerRecordBinary(encoded); err == nil {
		t.Fatal("UnmarshalSignedPeerRecordBinary(tampered) error = nil, want error")
	}
}
