package p2p

import (
	"testing"
	"time"

	"solana_golang/utils"
)

func TestQUICHolePunchMessageRoundTrip(t *testing.T) {
	sourcePeerID := testPeerID(121)
	targetPeerID := testPeerID(122)
	relayPeerID := testPeerID(123)
	address := testAddress(t, utils.ProtocolQUIC, 41123, targetPeerID)
	message := QUICHolePunchMessage{
		Version:            QUICHolePunchVersion,
		Kind:               quicHolePunchKindIntroduce,
		SessionID:          testMessageID(t),
		SourcePeerID:       sourcePeerID,
		TargetPeerID:       targetPeerID,
		RelayPeerID:        relayPeerID,
		ObservedAddress:    address.String(),
		StartAtUnixMilli:   time.Now().Add(time.Second).UnixMilli(),
		ExpiresAtUnixMilli: time.Now().Add(2 * time.Second).UnixMilli(),
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}

	encoded, err := message.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalQUICHolePunchMessageBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalQUICHolePunchMessageBinary() error = %v", err)
	}
	if decoded.SessionID != message.SessionID {
		t.Fatalf("SessionID = %q, want %q", decoded.SessionID, message.SessionID)
	}
	introducedAddress, err := decoded.IntroducedAddress()
	if err != nil {
		t.Fatalf("IntroducedAddress() error = %v", err)
	}
	if introducedAddress.String() != address.String() {
		t.Fatalf("introduced address = %q, want %q", introducedAddress.String(), address.String())
	}
}

func TestMultiAddressFromObservedRejectsTCP(t *testing.T) {
	peerID := testPeerID(124)
	address, err := multiAddressFromObserved("192.0.2.10:5101", peerID)
	if err != nil {
		t.Fatalf("multiAddressFromObserved() error = %v", err)
	}
	if address.Protocol != utils.ProtocolQUIC {
		t.Fatalf("Protocol = %q, want quic", address.Protocol)
	}
	if address.PeerID != peerID {
		t.Fatalf("PeerID = %q, want %q", address.PeerID, peerID)
	}
}

func TestConnectedHolePunchRelaysFiltersRelayQUICPeers(t *testing.T) {
	localPeerID := testPeerID(125)
	relayPeerID := testPeerID(126)
	targetPeerID := testPeerID(127)
	tcpRelayPeerID := testPeerID(128)
	host, err := NewHost(HostConfig{PeerID: localPeerID, AllowInsecure: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	relayPeer := mustTestPeer(t, relayPeerID, utils.ProtocolQUIC, 5111)
	relayPeer.Capabilities = PeerCapabilityRelay
	if err := host.AddPeer(relayPeer); err != nil {
		t.Fatalf("AddPeer(relay) error = %v", err)
	}
	tcpRelayPeer := mustTestPeer(t, tcpRelayPeerID, utils.ProtocolTCP, 5112)
	tcpRelayPeer.Capabilities = PeerCapabilityRelay
	if err := host.AddPeer(tcpRelayPeer); err != nil {
		t.Fatalf("AddPeer(tcp relay) error = %v", err)
	}
	setHostConnectionForTest(host, relayPeerID, newScriptedConnection(utils.ProtocolQUIC, relayPeerID, nil))
	setHostConnectionForTest(host, tcpRelayPeerID, newScriptedConnection(utils.ProtocolTCP, tcpRelayPeerID, nil))

	relays := host.connectedHolePunchRelays(targetPeerID)
	if len(relays) != 1 || relays[0] != relayPeerID {
		t.Fatalf("relays = %v, want [%s]", relays, relayPeerID)
	}
}

func mustTestPeer(t *testing.T, peerID string, protocol utils.MultiAddressProtocol, port int) Peer {
	t.Helper()
	peer, err := NewPeer(peerID, []utils.MultiAddress{testAddress(t, protocol, port, peerID)})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	return peer
}
