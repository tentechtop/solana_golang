package main

import "testing"

func TestNormalizeNodeConfigRejectsProductionInsecureP2P(t *testing.T) {
	config := minimalNodeConfigForValidation()
	config.Production = true
	config.TreasuryKeyPath = "treasury.json"

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

	if _, err := normalizeNodeConfig(config); err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
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
