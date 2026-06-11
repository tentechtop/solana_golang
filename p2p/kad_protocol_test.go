package p2p

import (
	"context"
	"errors"
	"testing"

	"solana_golang/utils"
)

func TestKADFindNodeRequestRoundTrip(t *testing.T) {
	targetPeerID := kadTestPeerID(7)
	request, err := NewKADFindNodeRequest(targetPeerID, 3)
	if err != nil {
		t.Fatalf("NewKADFindNodeRequest() error = %v", err)
	}

	encoded, err := request.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalKADFindNodeRequestBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalKADFindNodeRequestBinary() error = %v", err)
	}

	if decoded.TargetPeerID != targetPeerID {
		t.Fatalf("TargetPeerID = %q, want %q", decoded.TargetPeerID, targetPeerID)
	}
	if decoded.Limit != 3 {
		t.Fatalf("Limit = %d, want 3", decoded.Limit)
	}
}

func TestKADFindNodeResponseRoundTrip(t *testing.T) {
	targetPeerID := kadTestPeerID(8)
	peer := kadTestPeer(t, 0x30, 4011)
	peer.ProtocolVersion = "1"
	peer.SoftwareVersion = "test/0.1.0"
	peer.LatestSlot = 11
	peer.BlockHeight = 9
	peer.BestBlockHash = testPeerID(21)
	peer.Validator = true
	peer.StakeLamports = 100

	response, err := NewKADFindNodeResponse(targetPeerID, []Peer{peer})
	if err != nil {
		t.Fatalf("NewKADFindNodeResponse() error = %v", err)
	}
	encoded, err := response.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalKADFindNodeResponseBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalKADFindNodeResponseBinary() error = %v", err)
	}

	if decoded.TargetPeerID != targetPeerID {
		t.Fatalf("TargetPeerID = %q, want %q", decoded.TargetPeerID, targetPeerID)
	}
	if len(decoded.Peers) != 1 {
		t.Fatalf("len(Peers) = %d, want 1", len(decoded.Peers))
	}
	decodedPeer, err := decoded.Peers[0].ToPeer()
	if err != nil {
		t.Fatalf("ToPeer() error = %v", err)
	}
	if decodedPeer.ID != peer.ID {
		t.Fatalf("peer ID = %q, want %q", decodedPeer.ID, peer.ID)
	}
	if decodedPeer.StakeLamports != peer.StakeLamports {
		t.Fatalf("StakeLamports = %d, want %d", decodedPeer.StakeLamports, peer.StakeLamports)
	}
}

func TestKADPeerHintRejectsAddressOwnerMismatch(t *testing.T) {
	peerID := kadTestPeerID(9)
	otherPeerID := kadTestPeerID(10)
	address := testAddress(t, utils.ProtocolTCP, 4012, otherPeerID)
	hint := KADPeerHint{
		PeerID:    peerID,
		Addresses: []string{address.String()},
	}

	if err := hint.Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("Validate() error = %v, want ErrInvalidMessage", err)
	}
}

func TestHostFindNodeHandlerReturnsClosestPeers(t *testing.T) {
	localPeerID := kadTestPeerID(0)
	host, err := NewHost(HostConfig{PeerID: localPeerID})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	targetPeer := kadTestPeer(t, 0x01, 4013)
	otherPeer := kadTestPeer(t, 0x70, 4014)
	if err := host.AddPeer(targetPeer); err != nil {
		t.Fatalf("AddPeer(target) error = %v", err)
	}
	if err := host.AddPeer(otherPeer); err != nil {
		t.Fatalf("AddPeer(other) error = %v", err)
	}

	requestPayload, err := NewKADFindNodeRequest(targetPeer.ID, 1)
	if err != nil {
		t.Fatalf("NewKADFindNodeRequest() error = %v", err)
	}
	payload, err := requestPayload.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	message, err := NewRequestMessage(kadTestPeerID(99), ProtocolFindNodeRequestV1, payload)
	if err != nil {
		t.Fatalf("NewRequestMessage() error = %v", err)
	}
	message.ToPeerID = localPeerID

	result, err := host.HandleMessage(context.Background(), message)
	if err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}
	if !result.HasResponse {
		t.Fatal("HasResponse = false, want true")
	}
	response, err := UnmarshalKADFindNodeResponseBinary(result.Message.Payload)
	if err != nil {
		t.Fatalf("UnmarshalKADFindNodeResponseBinary() error = %v", err)
	}
	if len(response.Peers) != 1 {
		t.Fatalf("len(Peers) = %d, want 1", len(response.Peers))
	}
	if response.Peers[0].PeerID != targetPeer.ID {
		t.Fatalf("Peers[0] = %q, want %q", response.Peers[0].PeerID, targetPeer.ID)
	}
}
