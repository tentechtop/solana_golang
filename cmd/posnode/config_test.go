package main

import "testing"

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
