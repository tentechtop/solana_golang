package bootstrapnode

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"solana_golang/p2p"
	"solana_golang/utils"
)

func TestNormalizeConfigRequiresSecureNetworkID(t *testing.T) {
	allowInsecure := false
	_, err := normalizeConfig(bootstrapConfig{
		NodeName:         "boot",
		ListenIP:         "0.0.0.0",
		ListenPort:       5100,
		PeerSeed:         "boot-seed",
		AllowInsecureP2P: &allowInsecure,
	})
	if err == nil {
		t.Fatal("normalizeConfig() error = nil, want secure network id error")
	}
}

func TestNewStaticPeerParsesValidatorCapabilities(t *testing.T) {
	keyPair, err := rawKeyPairFromSeed("validator-peer")
	if err != nil {
		t.Fatalf("rawKeyPairFromSeed() error = %v", err)
	}
	peer, err := newStaticPeer(peerConfig{
		PeerID: keyPair.peerID,
		IP:     "127.0.0.1",
		Port:   5101,
		Role:   "validator",
	})
	if err != nil {
		t.Fatalf("newStaticPeer() error = %v", err)
	}
	if peer.Role != p2p.PeerRoleValidator {
		t.Fatalf("Role = %q, want validator", peer.Role)
	}
	if peer.Capabilities&p2p.PeerCapabilityValidator == 0 {
		t.Fatalf("validator capability missing: %d", peer.Capabilities)
	}
	if peer.Capabilities&p2p.PeerCapabilityRelay == 0 {
		t.Fatalf("relay capability missing: %d", peer.Capabilities)
	}
}

func TestNewStaticPeerUsesConfiguredQUICNetwork(t *testing.T) {
	keyPair, err := rawKeyPairFromSeed("validator-peer-quic")
	if err != nil {
		t.Fatalf("rawKeyPairFromSeed() error = %v", err)
	}
	peer, err := newStaticPeer(peerConfig{
		PeerID:  keyPair.peerID,
		IP:      "127.0.0.1",
		Port:    5101,
		Network: "quic",
		Role:    "validator",
	})
	if err != nil {
		t.Fatalf("newStaticPeer() error = %v", err)
	}
	if len(peer.Addresses) != 1 {
		t.Fatalf("Addresses length = %d, want 1", len(peer.Addresses))
	}
	if peer.Addresses[0].Protocol != utils.ProtocolQUIC {
		t.Fatalf("Protocol = %q, want quic", peer.Addresses[0].Protocol)
	}
}

func TestNormalizeConfigDefaultsNetworkToTCP(t *testing.T) {
	normalized, err := normalizeConfig(bootstrapConfig{
		NodeName:   "boot",
		ListenIP:   "0.0.0.0",
		ListenPort: 5100,
		PeerSeed:   "boot-seed",
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if normalized.Network != string(utils.ProtocolTCP) {
		t.Fatalf("Network = %q, want tcp", normalized.Network)
	}
}

func TestLoadPeerKeyPairFromKeystore(t *testing.T) {
	seed := utils.SHA256([]byte("bootstrap-keystore"))
	path := filepath.Join(t.TempDir(), "peer.json")
	content := `{"private_key_base64":"` + base64.StdEncoding.EncodeToString(seed) + `"}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write keystore: %v", err)
	}
	keyPair, err := loadPeerKeyPair(bootstrapConfig{PeerKeyPath: path})
	if err != nil {
		t.Fatalf("loadPeerKeyPair() error = %v", err)
	}
	expected, err := rawKeyPairFromPrivateKey(seed)
	if err != nil {
		t.Fatalf("rawKeyPairFromPrivateKey() error = %v", err)
	}
	if keyPair.peerID != expected.peerID {
		t.Fatalf("peer id = %s, want %s", keyPair.peerID, expected.peerID)
	}
}
