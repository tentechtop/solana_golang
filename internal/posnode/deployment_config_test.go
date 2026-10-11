package posnode

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestFullNodeDeploymentRetainsNetworkAndExcludesWalletKeys(t *testing.T) {
	source := minimalNodeConfigForValidation()
	peerID, err := PeerIDFromSeed("deployment-fixture-peer")
	if err != nil {
		t.Fatal(err)
	}
	staker := mustStructureKeyPair("deployment-fixture-staker")
	validator := mustStructureKeyPair("deployment-fixture-validator")
	consensus := mustStructureKeyPair("deployment-fixture-consensus")
	bls, err := genesisBLSPublicKey("", "deployment-fixture-consensus")
	if err != nil {
		t.Fatal(err)
	}
	source.Genesis.InitialValidators = []genesisValidatorConfig{{StakerAddress: staker.PublicKey.String(), ValidatorAddress: validator.PublicKey.String(), ConsensusPublicKey: consensus.PublicKey.String(), BLSPublicKeyBase64: base64.StdEncoding.EncodeToString(bls), PeerID: peerID, StakeLamports: 100000000}}
	source.BootstrapPeers = []peerConfig{{PeerID: peerID, IP: "203.0.113.7", Port: 5101, Network: "tcp"}}
	normalized, err := normalizeNodeConfig(source)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := bootstrapManifestFromConfig(normalized, true, len(normalized.Genesis.InitialValidators), 1)
	if err != nil {
		t.Fatal(err)
	}
	opts := FullNodeDeploymentOptions{Name: "remote-fullnode", DataPath: "/opt/ordexus-nodes/remote-fullnode/data", AdvertisedIP: "203.0.113.10", PeerSeed: strings.Repeat("a1", 32), P2PPort: 5102, RPCPort: 8902, MultiValidatorThreshold: 4}
	data, err := PrepareFullNodeDeploymentConfig(manifest, opts)
	if err != nil {
		t.Fatal(err)
	}
	var config nodeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.DataPath != opts.DataPath {
		t.Fatalf("Linux data root changed: %q", config.DataPath)
	}
	if config.StakerSeed != "" || config.ValidatorSeed != "" || config.ConsensusSeed != "" || config.TreasuryKeyPath != "" {
		t.Fatal("wallet or consensus keys included")
	}
	if config.validatorEnabled() || config.consensusEnabled() || config.validatorPairingEnabled() {
		t.Fatal("deployment unexpectedly signs or requests wallet pairing")
	}
	if config.RPCListenIP != "127.0.0.1" || config.allowInsecureP2P() {
		t.Fatal("default exposure or P2P authentication changed")
	}
	reloaded, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ChainIdentityHash != manifest.ChainIdentityHash || reloaded.GenesisHash != manifest.GenesisHash {
		t.Fatal("deployed node forked the network")
	}
	manifest.Genesis.InitialSupplyLamports++
	if _, err := PrepareFullNodeDeploymentConfig(manifest, opts); err == nil {
		t.Fatal("modified genesis was accepted")
	}
}
