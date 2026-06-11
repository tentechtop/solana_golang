package p2p

import (
	"context"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestHostIdentifyHandlerReturnsSignedRecord(t *testing.T) {
	localIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.0")
	remoteIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.1")
	address := testAddress(t, utils.ProtocolTCP, 5021, localIdentity.PeerID)
	host, err := NewHost(HostConfig{
		PeerID:              localIdentity.PeerID,
		SecureIdentity:      localIdentity,
		EnableSecureSession: true,
		AdvertisedAddresses: []utils.MultiAddress{address},
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	payload, err := NewIdentifyRequest().MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	message, err := NewRequestMessage(remoteIdentity.PeerID, ProtocolIdentifyRequestV1, payload)
	if err != nil {
		t.Fatalf("NewRequestMessage() error = %v", err)
	}
	message.ToPeerID = localIdentity.PeerID

	result, err := host.HandleMessage(context.Background(), message)
	if err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}
	if !result.HasResponse {
		t.Fatal("HasResponse = false, want true")
	}
	response, err := UnmarshalIdentifyResponseBinary(result.Message.Payload)
	if err != nil {
		t.Fatalf("UnmarshalIdentifyResponseBinary() error = %v", err)
	}
	record, err := UnmarshalSignedPeerRecordBinary(response.Record)
	if err != nil {
		t.Fatalf("UnmarshalSignedPeerRecordBinary() error = %v", err)
	}
	if record.PeerID != localIdentity.PeerID {
		t.Fatalf("record.PeerID = %q, want %q", record.PeerID, localIdentity.PeerID)
	}
	if len(record.Addresses) != 1 || record.Addresses[0] != address.String() {
		t.Fatalf("record.Addresses = %+v, want %s", record.Addresses, address.String())
	}
}

func TestHostPeerHintsImportsSignedRecord(t *testing.T) {
	localIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.0")
	remoteIdentity := testSecureSessionIdentity(t, "localnet", "node/1.0.1")
	remoteAddress := testAddress(t, utils.ProtocolTCP, 5022, remoteIdentity.PeerID)
	remotePeer, err := NewPeer(remoteIdentity.PeerID, []utils.MultiAddress{remoteAddress})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	remotePeer.Role = PeerRoleFull
	remotePeer.Capabilities = PeerCapabilityDHT
	record, err := NewSignedPeerRecord(remotePeer, remoteIdentity, time.Hour)
	if err != nil {
		t.Fatalf("NewSignedPeerRecord() error = %v", err)
	}
	encodedRecord, err := record.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(record) error = %v", err)
	}
	payload, err := NewPeerHintsPayload([][]byte{encodedRecord})
	if err != nil {
		t.Fatalf("NewPeerHintsPayload() error = %v", err)
	}
	encodedPayload, err := payload.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(payload) error = %v", err)
	}

	host, err := NewHost(HostConfig{PeerID: localIdentity.PeerID})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()
	message, err := NewMessage(ProtocolPeerHintsV1, encodedPayload)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	message.FromPeerID = remoteIdentity.PeerID
	message.ToPeerID = localIdentity.PeerID

	if _, err := host.HandleMessage(context.Background(), message); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}
	peer, ok := host.Peer(remoteIdentity.PeerID)
	if !ok {
		t.Fatal("Peer() ok = false, want imported peer")
	}
	if len(peer.SignedRecord) == 0 {
		t.Fatal("SignedRecord was not stored")
	}
	if metrics := host.Metrics(); metrics.PeerRecordsAccepted != 1 {
		t.Fatalf("PeerRecordsAccepted = %d, want 1", metrics.PeerRecordsAccepted)
	}
}
