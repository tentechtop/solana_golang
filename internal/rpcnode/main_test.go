package rpcnode

import (
	"testing"

	"solana_golang/p2p"
	"solana_golang/rpc"
	"solana_golang/utils"
)

func TestNewStaticPeerMarksValidatorCapability(t *testing.T) {
	peerID := testPeerID(1)
	peer, err := newStaticPeer(peerConfig{
		PeerID:       peerID,
		IP:           "127.0.0.1",
		Port:         5101,
		Role:         "validator",
		Roles:        []string{"full"},
		Capabilities: []string{"validator", "relay", "state_sync"},
	})
	if err != nil {
		t.Fatalf("newStaticPeer() error = %v", err)
	}
	if peer.Role != p2p.PeerRoleValidator {
		t.Fatalf("Role = %q, want validator", peer.Role)
	}
	if !peer.Validator {
		t.Fatal("Validator = false, want true")
	}
	if peer.Capabilities&p2p.PeerCapabilityValidator == 0 {
		t.Fatalf("Capabilities = %d, want validator bit", peer.Capabilities)
	}
	if peer.Capabilities&p2p.PeerCapabilityStateSync == 0 {
		t.Fatalf("Capabilities = %d, want state sync bit", peer.Capabilities)
	}
}

func TestNewStaticPeerKeepsPublicRPCOutOfValidatorSet(t *testing.T) {
	peerID := testPeerID(2)
	peer, err := newStaticPeer(peerConfig{
		PeerID:       peerID,
		IP:           "127.0.0.1",
		Port:         5101,
		Role:         "public_rpc",
		Capabilities: []string{"relay", "dht"},
	})
	if err != nil {
		t.Fatalf("newStaticPeer() error = %v", err)
	}
	if peer.Role != p2p.PeerRolePublicRPC {
		t.Fatalf("Role = %q, want public_rpc", peer.Role)
	}
	if peer.Validator {
		t.Fatal("Validator = true, want false")
	}
	if peer.Capabilities&p2p.PeerCapabilityValidator != 0 {
		t.Fatalf("Capabilities = %d, want no validator bit", peer.Capabilities)
	}
}

func TestNormalizeConfigRequiresNetworkIDForSecureRPCNode(t *testing.T) {
	_, err := normalizeConfig(rpcNodeConfig{
		NodeName:         "rpc-101",
		ListenIP:         "0.0.0.0",
		ListenPort:       5101,
		PeerSeed:         "node-101",
		AllowInsecureP2P: boolPtr(false),
	})
	if err == nil {
		t.Fatal("normalizeConfig() error = nil, want secure network id error")
	}
}

func TestNormalizeConfigRejectsValidatorRoleForRPCNode(t *testing.T) {
	_, err := normalizeConfig(rpcNodeConfig{
		NodeName:         "rpc-101",
		ListenIP:         "0.0.0.0",
		ListenPort:       5101,
		PeerSeed:         "node-101",
		AllowInsecureP2P: boolPtr(true),
		NodeRoles:        []string{"public_rpc", "validator"},
	})
	if err == nil {
		t.Fatal("normalizeConfig() error = nil, want rpc node role rejection")
	}
}

func TestNewStaticPeerRejectsInvalidCapability(t *testing.T) {
	_, err := newStaticPeer(peerConfig{
		PeerID:       testPeerID(3),
		IP:           "127.0.0.1",
		Port:         5101,
		Role:         "validator",
		Capabilities: []string{"validator", "bad-capability"},
	})
	if err == nil {
		t.Fatal("newStaticPeer() error = nil, want invalid capability rejection")
	}
}

func TestForwardedMethodsDoNotExposeLocalValidatorIdentity(t *testing.T) {
	for _, method := range forwardedMethods() {
		if method == rpc.MethodGetLocalValidatorIdentity {
			t.Fatal("forwarded methods must not expose local validator identity")
		}
		if method == rpc.MethodRegisterValidator || method == rpc.MethodStake {
			t.Fatalf("forwarded methods must not expose wallet-local signing method %s", method)
		}
	}
}

func testPeerID(lastByte byte) string {
	seed := make([]byte, 32)
	seed[31] = lastByte
	return utils.Base58Encode(seed)
}

func boolPtr(value bool) *bool {
	return &value
}
