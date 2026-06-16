package main

import (
	"testing"

	"solana_golang/p2p"
	"solana_golang/utils"
)

func TestBuildPeerNetworkResultMarksConnectedValidator(t *testing.T) {
	validatorPeerID := testPeerIDForNode(1)
	validatorAddress := testNodeAddress(t, utils.ProtocolTCP, 5101, validatorPeerID)
	verifiedAddress := testNodeAddress(t, utils.ProtocolQUIC, 5102, validatorPeerID)
	validatorSnapshot := p2p.PeerSnapshot{
		ID:                     validatorPeerID,
		Status:                 p2p.PeerStatusConnected,
		Role:                   p2p.PeerRoleValidator,
		Validator:              true,
		AdvertisedAddresses:    []utils.MultiAddress{validatorAddress},
		VerifiedAddresses:      []utils.MultiAddress{verifiedAddress},
		PreferredProtocols:     []utils.MultiAddressProtocol{utils.ProtocolQUIC, utils.ProtocolTCP},
		LastConnectedUnixMilli: 200,
	}
	connectionState := p2p.ConnectionState{
		Protocol:              utils.ProtocolQUIC,
		RemoteAddress:         verifiedAddress.String(),
		ObservedRemoteAddress: "/ip4/192.168.1.10/tcp/5102",
		Encrypted:             true,
		ConnectedAtUnixMilli:  100,
		LastReadUnixMilli:     120,
		LastWriteUnixMilli:    130,
		FailureCount:          1,
	}
	disconnectedPeerID := testPeerIDForNode(2)
	disconnectedAddress := testNodeAddress(t, utils.ProtocolTCP, 5201, disconnectedPeerID)
	disconnectedSnapshot := p2p.PeerSnapshot{
		ID:                        disconnectedPeerID,
		Status:                    p2p.PeerStatusDisconnected,
		Role:                      p2p.PeerRoleFull,
		AdvertisedAddresses:       []utils.MultiAddress{disconnectedAddress},
		LastDisconnectedUnixMilli: 300,
		LastError:                 "dial timeout",
	}

	result := buildPeerNetworkResult(
		testPeerIDForNode(9),
		[]p2p.PeerSnapshot{disconnectedSnapshot, validatorSnapshot},
		map[string]p2p.ConnectionState{validatorPeerID: connectionState},
	)

	if result.LocalPeerID != testPeerIDForNode(9) {
		t.Fatalf("LocalPeerID = %q, want %q", result.LocalPeerID, testPeerIDForNode(9))
	}
	if len(result.Peers) != 2 {
		t.Fatalf("peer count = %d, want 2", len(result.Peers))
	}

	connectedPeer := result.Peers[0]
	if connectedPeer.PeerID != validatorPeerID {
		t.Fatalf("first peer = %q, want connected validator", connectedPeer.PeerID)
	}
	if !connectedPeer.Connected {
		t.Fatal("connected peer marked disconnected")
	}
	if connectedPeer.BestAddress != verifiedAddress.String() {
		t.Fatalf("BestAddress = %q, want %q", connectedPeer.BestAddress, verifiedAddress.String())
	}
	if connectedPeer.Connection == nil {
		t.Fatal("Connection = nil, want details")
	}
	if connectedPeer.Connection.Protocol != string(utils.ProtocolQUIC) {
		t.Fatalf("Connection.Protocol = %q, want %q", connectedPeer.Connection.Protocol, utils.ProtocolQUIC)
	}

	disconnectedPeer := result.Peers[1]
	if disconnectedPeer.PeerID != disconnectedPeerID {
		t.Fatalf("second peer = %q, want %q", disconnectedPeer.PeerID, disconnectedPeerID)
	}
	if disconnectedPeer.Connected {
		t.Fatal("disconnected peer marked connected")
	}
	if disconnectedPeer.Connection != nil {
		t.Fatal("disconnected peer returned connection details")
	}
	if disconnectedPeer.BestAddress != disconnectedAddress.String() {
		t.Fatalf("disconnected BestAddress = %q, want %q", disconnectedPeer.BestAddress, disconnectedAddress.String())
	}
	if disconnectedPeer.LastError != "dial timeout" {
		t.Fatalf("LastError = %q, want dial timeout", disconnectedPeer.LastError)
	}
}

func testPeerIDForNode(lastByte byte) string {
	seed := make([]byte, 32)
	seed[31] = lastByte
	return utils.Base58Encode(seed)
}

func testNodeAddress(
	t *testing.T,
	protocol utils.MultiAddressProtocol,
	port int,
	peerID string,
) utils.MultiAddress {
	t.Helper()
	address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, "127.0.0.1", protocol, port, peerID)
	if err != nil {
		t.Fatalf("BuildMultiAddress() error = %v", err)
	}
	return address
}
