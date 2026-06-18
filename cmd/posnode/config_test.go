package main

import (
	"path/filepath"
	"testing"

	"solana_golang/p2p"
	runtimepkg "solana_golang/runtime"
)

func TestNormalizeNodeConfigRejectsProductionInsecureP2P(t *testing.T) {
	allowInsecure := true
	config := minimalNodeConfigForValidation()
	config.Production = true
	config.TreasuryKeyPath = "treasury.json"
	config.AllowInsecureP2P = &allowInsecure
	config.PeerKeyPath = "peer.json"
	config.StakerKeyPath = "staker.json"
	config.ValidatorKeyPath = "validator.json"
	config.ConsensusKeyPath = "consensus.json"
	config.BLSKeyPath = "bls.json"
	config = withProductionGenesisPublicKeys(config)

	_, err := normalizeNodeConfig(config)
	if err == nil {
		t.Fatal("normalizeNodeConfig() error = nil, want production insecure p2p rejection")
	}
}

func TestNormalizeNodeConfigAllowsProductionSecureP2P(t *testing.T) {
	allowInsecure := false
	config := minimalNodeConfigForValidation()
	config.Production = true
	config.TreasuryKeyPath = "treasury.json"
	config.AllowInsecureP2P = &allowInsecure
	config.PeerKeyPath = "peer.json"
	config.StakerKeyPath = "staker.json"
	config.ValidatorKeyPath = "validator.json"
	config.ConsensusKeyPath = "consensus.json"
	config.BLSKeyPath = "bls.json"
	config = withProductionGenesisPublicKeys(config)

	if _, err := normalizeNodeConfig(config); err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
}

func TestNormalizeNodeConfigRejectsProductionWithoutKeyPaths(t *testing.T) {
	allowInsecure := false
	config := minimalNodeConfigForValidation()
	config.Production = true
	config.TreasuryKeyPath = "treasury.json"
	config.AllowInsecureP2P = &allowInsecure

	if _, err := normalizeNodeConfig(config); err == nil {
		t.Fatal("normalizeNodeConfig() error = nil, want production key path rejection")
	}
}

func TestNormalizeNodeConfigDefaultsAdvertisedPort(t *testing.T) {
	config := minimalNodeConfigForValidation()
	config.AdvertisedIP = "192.0.2.10"

	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	if normalized.AdvertisedPort != normalized.ListenPort {
		t.Fatalf("advertised port = %d, want listen port %d", normalized.AdvertisedPort, normalized.ListenPort)
	}
}

func TestNormalizeNodeConfigResolvesChainIdentityAndDataPath(t *testing.T) {
	config := minimalNodeConfigForValidation()
	config.DataPath = "data/chain-identity-test"
	config.GenesisStartMs = 1_700_000_000_000

	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	if normalized.ChainIdentityHash == "" {
		t.Fatal("ChainIdentityHash = empty, want non-empty")
	}
	if normalized.GenesisHash == "" {
		t.Fatal("GenesisHash = empty, want non-empty")
	}
	if normalized.P2PNetworkID != normalized.ChainIdentityHash {
		t.Fatalf("P2PNetworkID = %q, want %q", normalized.P2PNetworkID, normalized.ChainIdentityHash)
	}
	wantRootPath := filepath.Clean(config.DataPath)
	if normalized.DataRootPath != wantRootPath {
		t.Fatalf("DataRootPath = %q, want %q", normalized.DataRootPath, wantRootPath)
	}
	wantDataPath := filepath.Join(wantRootPath, "chains", normalized.ChainIdentityHash)
	if normalized.DataPath != wantDataPath {
		t.Fatalf("DataPath = %q, want %q", normalized.DataPath, wantDataPath)
	}
}

func TestNormalizeNodeConfigChainIdentityChangesWithGenesisStart(t *testing.T) {
	firstConfig := minimalNodeConfigForValidation()
	firstConfig.DataPath = "data/chain-identity-first"
	firstConfig.GenesisStartMs = 1_700_000_000_000

	secondConfig := minimalNodeConfigForValidation()
	secondConfig.DataPath = firstConfig.DataPath
	secondConfig.GenesisStartMs = firstConfig.GenesisStartMs + 1

	firstNormalized, err := normalizeNodeConfig(firstConfig)
	if err != nil {
		t.Fatalf("normalizeNodeConfig(first) error = %v", err)
	}
	secondNormalized, err := normalizeNodeConfig(secondConfig)
	if err != nil {
		t.Fatalf("normalizeNodeConfig(second) error = %v", err)
	}
	if firstNormalized.ChainIdentityHash == secondNormalized.ChainIdentityHash {
		t.Fatal("ChainIdentityHash mismatch check failed, want different hashes for different genesis start")
	}
	if firstNormalized.DataPath == secondNormalized.DataPath {
		t.Fatal("DataPath mismatch check failed, want different data paths for different chain identities")
	}
}

func TestNormalizeNodeConfigParsesNodeAttributes(t *testing.T) {
	config := minimalNodeConfigForValidation()
	config.NodeRole = "bootnode"
	config.NodeCapabilities = []string{"relay", "dht", "archive"}

	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	if normalized.ResolvedNodeRole != p2p.PeerRoleBootnode {
		t.Fatalf("ResolvedNodeRole = %q, want bootnode", normalized.ResolvedNodeRole)
	}
	if normalized.ResolvedNodeCapabilities&p2p.PeerCapabilityArchive == 0 {
		t.Fatal("ResolvedNodeCapabilities missing archive capability")
	}
	if normalized.ResolvedNodeCapabilities&p2p.PeerCapabilityDHT == 0 {
		t.Fatal("ResolvedNodeCapabilities missing dht capability")
	}
}

func TestNormalizeNodeConfigDefaultsPrivacyExecutionMode(t *testing.T) {
	config := minimalNodeConfigForValidation()

	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	if normalized.PrivacyExecutionMode != runtimepkg.PrivacyExecutionModeFixed {
		t.Fatalf("PrivacyExecutionMode = %q, want fixed", normalized.PrivacyExecutionMode)
	}
}

func TestNormalizeNodeConfigRejectsInvalidPrivacyExecutionMode(t *testing.T) {
	config := minimalNodeConfigForValidation()
	config.PrivacyExecutionMode = "bad-mode"

	if _, err := normalizeNodeConfig(config); err == nil {
		t.Fatal("normalizeNodeConfig() error = nil, want invalid privacy execution mode rejection")
	}
}

func TestNormalizeNodeConfigParsesBootstrapPeerAttributes(t *testing.T) {
	config := minimalNodeConfigForValidation()
	config.BootstrapPeers = []peerConfig{{
		PeerID:       testPeerIDForNode(7),
		IP:           "127.0.0.1",
		Port:         5101,
		Network:      "tcp",
		Role:         "bootstrap",
		Capabilities: []string{"relay", "dht"},
	}}

	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	peer := normalized.BootstrapPeers[0]
	if peer.ResolvedRole != p2p.PeerRoleBootnode {
		t.Fatalf("ResolvedRole = %q, want bootnode", peer.ResolvedRole)
	}
	if peer.ResolvedCapabilities&p2p.PeerCapabilityDHT == 0 {
		t.Fatal("ResolvedCapabilities missing dht capability")
	}
	if peer.ResolvedCapabilities&p2p.PeerCapabilityValidator != 0 {
		t.Fatal("ResolvedCapabilities includes validator, want bootnode only")
	}
}

func TestNormalizeNodeConfigRejectsInvalidNodeCapability(t *testing.T) {
	config := minimalNodeConfigForValidation()
	config.NodeCapabilities = []string{"archive", "bad-capability"}

	if _, err := normalizeNodeConfig(config); err == nil {
		t.Fatal("normalizeNodeConfig() error = nil, want invalid capability rejection")
	}
}

func minimalNodeConfigForValidation() nodeConfig {
	return nodeConfig{
		NodeName:      "config-test",
		ListenIP:      "127.0.0.1",
		ListenPort:    19001,
		PeerSeed:      "peer-seed",
		StakerSeed:    "staker-seed",
		ValidatorSeed: "validator-seed",
		ConsensusSeed: "consensus-seed",
		Genesis: genesisConfig{
			FundedAccounts: []genesisAccountConfig{{
				Seed:     "treasury",
				Lamports: 1_000_000_000,
			}},
		},
	}
}

func withProductionGenesisPublicKeys(config nodeConfig) nodeConfig {
	treasury := mustStructureKeyPair("treasury")
	config.Genesis.TreasuryAddress = treasury.PublicKey.String()
	for index := range config.Genesis.FundedAccounts {
		account := mustStructureKeyPair(config.Genesis.FundedAccounts[index].Seed)
		config.Genesis.FundedAccounts[index].Address = account.PublicKey.String()
		config.Genesis.FundedAccounts[index].Seed = ""
	}
	return config
}
