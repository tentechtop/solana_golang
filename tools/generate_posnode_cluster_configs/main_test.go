package main

import "testing"

func TestBuildRPCNodeConfigUsesPublicGatewayRoleAndValidatorPeers(t *testing.T) {
	validators, _, err := buildValidators(
		defaultWinHost,
		defaultMacHost,
		t.TempDir(),
		chainTagFromID(defaultChainID),
		defaultValidatorCount,
		defaultWinValidatorCount,
	)
	if err != nil {
		t.Fatalf("buildValidators() error = %v", err)
	}
	bootnode, err := buildBootnode(t.TempDir(), defaultBootHost, chainTagFromID(defaultChainID))
	if err != nil {
		t.Fatalf("buildBootnode() error = %v", err)
	}

	config := buildRPCNodeConfig(bootnode, *validators, "test-network")
	if config.NodeMode != "rpcnode" {
		t.Fatalf("NodeMode = %q, want rpcnode", config.NodeMode)
	}
	if config.NodeRole != "public_rpc" {
		t.Fatalf("NodeRole = %q, want public_rpc", config.NodeRole)
	}
	if config.PeerSeed != "node-4v-boot-101" {
		t.Fatalf("PeerSeed = %q, want stable boot peer seed", config.PeerSeed)
	}
	if config.NetworkID != "test-network" {
		t.Fatalf("NetworkID = %q, want test-network", config.NetworkID)
	}
	if len(config.StaticPeers) != len(*validators) {
		t.Fatalf("StaticPeers length = %d, want %d", len(config.StaticPeers), len(*validators))
	}
	for _, peer := range config.StaticPeers {
		if peer.PeerID == bootnode.PeerID {
			t.Fatal("StaticPeers includes local public rpc peer")
		}
		if peer.Role != "validator" {
			t.Fatalf("Static peer role = %q, want validator", peer.Role)
		}
	}
}

func TestBuildValidatorsDefaultsToFourValidatorChain(t *testing.T) {
	validators, initialValidators, err := buildValidators(
		defaultWinHost,
		defaultMacHost,
		t.TempDir(),
		chainTagFromID(defaultChainID),
		defaultValidatorCount,
		defaultWinValidatorCount,
	)
	if err != nil {
		t.Fatalf("buildValidators() error = %v", err)
	}
	if len(*validators) != 4 {
		t.Fatalf("validator count = %d, want 4", len(*validators))
	}
	if len(initialValidators) != 4 {
		t.Fatalf("initial validator count = %d, want 4", len(initialValidators))
	}
	for _, validator := range *validators {
		if validator.HostGroup != "win" {
			t.Fatalf("validator host group = %q, want win", validator.HostGroup)
		}
	}
}

func TestBuildValidatorsCanGenerateEighteenValidatorChain(t *testing.T) {
	validators, initialValidators, err := buildValidators(defaultWinHost, defaultMacHost, t.TempDir(), "18v", 18, 10)
	if err != nil {
		t.Fatalf("buildValidators() error = %v", err)
	}
	if len(*validators) != 18 {
		t.Fatalf("validator count = %d, want 18", len(*validators))
	}
	if len(initialValidators) != 18 {
		t.Fatalf("initial validator count = %d, want 18", len(initialValidators))
	}
}

func TestBuildChainNetworkIDMatchesGeneratedFourValidatorChain(t *testing.T) {
	validators, initialValidators, err := buildValidators(
		defaultWinHost,
		defaultMacHost,
		t.TempDir(),
		chainTagFromID(defaultChainID),
		defaultValidatorCount,
		defaultWinValidatorCount,
	)
	if err != nil {
		t.Fatalf("buildValidators() error = %v", err)
	}
	userSeeds := buildUserSeeds(chainTagFromID(defaultChainID), 32)
	genesis := genesisConfigFile{
		InitialSupplyLamports: defaultInitialSupply,
		TreasuryAddress:       "4vgAxQAXeKXhyrJyQ5XDXzr1wR92NaS631GEkDjdhRn9",
		FundedAccounts:        buildFundedAccounts(userSeeds, initialValidators),
		InitialValidators:     initialValidators,
	}
	if len(*validators) != defaultValidatorCount {
		t.Fatalf("validator count = %d, want %d", len(*validators), defaultValidatorCount)
	}

	networkID, err := buildChainNetworkID(defaultChainID, 1781785671227, genesis)
	if err != nil {
		t.Fatalf("buildChainNetworkID() error = %v", err)
	}
	if networkID != "47YRzEuDFY1uSFbjLfDhCsBFdvefQ5VUSsfJ1dtbZuJD" {
		t.Fatalf("networkID = %q, want current generated-4 chain identity", networkID)
	}
}
