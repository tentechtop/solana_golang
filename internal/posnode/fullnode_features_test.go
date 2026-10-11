package posnode

import (
	"testing"

	"github.com/tentechtop/ordexus-core/p2p"
)

func TestNormalizeNodeConfigEnablesFullNodeDiscoveryAndWalletPairing(t *testing.T) {
	bootnodeEnabled := true
	archiveEnabled := true
	walletPairingEnabled := true
	config := minimalNodeConfigForValidation()
	config.RPCEnabled = true
	config.RPCPort = 19002
	config.NodeFeatures = nodeFeaturesConfig{
		BootnodeEnabled:      &bootnodeEnabled,
		ArchiveEnabled:       &archiveEnabled,
		WalletPairingEnabled: &walletPairingEnabled,
	}

	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	if normalized.ResolvedNodeRole != p2p.PeerRoleFull {
		t.Fatalf("ResolvedNodeRole = %q, want full", normalized.ResolvedNodeRole)
	}
	if !containsPeerRole(normalized.ResolvedNodeRoles, p2p.PeerRoleBootnode) {
		t.Fatal("ResolvedNodeRoles missing bootnode")
	}
	if normalized.ResolvedNodeCapabilities&p2p.PeerCapabilityDHT == 0 {
		t.Fatal("ResolvedNodeCapabilities missing dht")
	}
	if !containsPeerRole(normalized.ResolvedNodeRoles, p2p.PeerRoleArchive) ||
		normalized.ResolvedNodeCapabilities&p2p.PeerCapabilityArchive == 0 {
		t.Fatal("resolved full node features missing archive capability")
	}
	if normalized.advertisedP2PRole() != p2p.PeerRoleBootnode {
		t.Fatalf("advertisedP2PRole() = %q, want bootnode", normalized.advertisedP2PRole())
	}
	if normalized.validatorEnabled() || normalized.consensusEnabled() {
		t.Fatal("full node wallet pairing must start without validator or consensus")
	}
	if !normalized.validatorPairingEnabled() {
		t.Fatal("validatorPairingEnabled() = false, want true")
	}
}

func TestNormalizeNodeConfigRejectsWalletPairingOnBootstrapCoordinator(t *testing.T) {
	walletPairingEnabled := true
	config := minimalNodeConfigForValidation()
	config.RPCEnabled = true
	config.RPCPort = 19002
	config.NodeFeatures = nodeFeaturesConfig{WalletPairingEnabled: &walletPairingEnabled}
	config.BootstrapCoordinator = bootstrapCoordinatorConfig{Enabled: true}

	if _, err := normalizeNodeConfig(config); err == nil {
		t.Fatal("normalizeNodeConfig() error = nil, want bootstrap coordinator conflict")
	}
}

func TestStatusSnapshotReportsUnifiedFullNodeMode(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	status := node.statusSnapshot()
	if status.NodeMode != "fullnode" {
		t.Fatalf("NodeMode = %q, want fullnode", status.NodeMode)
	}
}
