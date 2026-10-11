package posnode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/rpc"
)

func nodeAppManifest(t *testing.T) rpc.BootstrapManifestResult {
	t.Helper()
	source := minimalNodeConfigForValidation()
	source.ChainID = "ordexus-node-app-isolated-test"
	source.GenesisStartMs = 1750000000000
	source.Genesis.TreasuryAddress = "9kwu18BR43jxaL5GUH7ZwWXXYAoGEAXTv82KJFupEMyP"
	source.Genesis.InitialSupplyLamports = 10000000000000000000
	source.Genesis.FundedAccounts = []genesisAccountConfig{{Address: source.Genesis.TreasuryAddress, Lamports: 10000000000000000000 - 10000000}}
	source.StakeRewardExactRatioEpoch = 32
	source.ConsensusPolicy.MultiValidatorThreshold = 4
	peer, err := PeerIDFromSeed("node-app-test-validator-peer")
	if err != nil {
		t.Fatal(err)
	}
	staker, validator, consensus := mustStructureKeyPair("node-app-test-staker"), mustStructureKeyPair("node-app-test-validator"), mustStructureKeyPair("node-app-test-consensus")
	bls, err := genesisBLSPublicKey("", "node-app-test-consensus")
	if err != nil {
		t.Fatal(err)
	}
	source.Genesis.InitialValidators = []genesisValidatorConfig{{StakerAddress: staker.PublicKey.String(), ValidatorAddress: validator.PublicKey.String(), ConsensusPublicKey: consensus.PublicKey.String(), BLSPublicKeyBase64: base64.StdEncoding.EncodeToString(bls), PeerID: peer, StakeLamports: 10000000}}
	source.BootstrapPeers = []peerConfig{{PeerID: peer, IP: "203.0.113.7", Port: 19509, Network: "tcp"}}
	source, err = normalizeNodeConfig(source)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := bootstrapManifestFromConfig(source, true, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestNodeAppManifestPreservesParametersAndPairingUpgrade(t *testing.T) {
	manifest := nodeAppManifest(t)
	if manifest.NetworkParameters == nil || manifest.NetworkParameters.StakeRewardExactRatioEpoch != 32 {
		t.Fatal("network parameters missing")
	}
	data, err := PrepareFullNodeDeploymentConfig(manifest, FullNodeDeploymentOptions{Name: "app-test", DataPath: t.TempDir(), PeerSeed: "node-app-isolated-peer", P2PPort: 19501, RPCPort: 29201, WalletPairing: true})
	if err != nil {
		t.Fatal(err)
	}
	var config nodeConfig
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.StakeRewardExactRatioEpoch != 32 || config.ConsensusPolicy.MultiValidatorThreshold != 4 || config.ChainIdentityHash != "" { // derived hashes are recomputed on load
		t.Fatal("network parameters did not survive export")
	}
	config, err = normalizeNodeConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if config.ChainIdentityHash != manifest.ChainIdentityHash || !config.validatorPairingEnabled() || config.validatorEnabled() {
		t.Fatal("full node pairing identity mismatch")
	}
	// The existing pairing completion rewrites these flags. The legacy feature
	// switch must not force pairing back on after the config is rewritten.
	enabled, disabled := true, false
	config.NodeRole, config.ValidatorEnabled, config.ConsensusEnabled = "validator", &enabled, &enabled
	config.ValidatorPairing.Enabled = &disabled
	config.StakerAddress = manifest.Genesis.InitialValidators[0].StakerAddress
	config.ValidatorSeed, config.ConsensusSeed = "node-app-validator-after-pairing", "node-app-consensus-after-pairing"
	if _, err = normalizeNodeConfig(config); err != nil {
		t.Fatalf("pairing upgrade cannot reload: %v", err)
	}
	if fixture := os.Getenv("ORDEXUS_NODE_APP_FIXTURE"); fixture != "" {
		if err = os.MkdirAll(filepath.Dir(fixture), 0700); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.MarshalIndent(manifest, "", "  ")
		if err = os.WriteFile(fixture, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func freeNodeAppPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func TestEmbeddedNodeCancelStopAndRestartKeepsLedger(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RunContext(canceled, "missing.json", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	manifest := nodeAppManifest(t)
	manifest.BootstrapPeers = nil // use an isolated full node, no network dialing
	// Deployment needs a manifest peer. Remove it from the generated local config.
	manifest.BootstrapPeers = []rpc.BootstrapPeerConfigResult{{PeerID: manifest.Genesis.InitialValidators[0].PeerID, IP: "203.0.113.7", Port: 19509, Network: "tcp"}}
	root := t.TempDir()
	rpcPort, p2pPort := freeNodeAppPort(t), freeNodeAppPort(t)
	data, err := PrepareFullNodeDeploymentConfig(manifest, FullNodeDeploymentOptions{Name: "embedded-test", DataPath: root, PeerSeed: "node-app-test-full-peer", P2PPort: p2pPort, RPCPort: rpcPort})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	_ = json.Unmarshal(data, &config)
	config["bootstrap_peers"] = []any{}
	config["listen_ip"] = "127.0.0.1"
	data, _ = json.Marshal(config)
	path := filepath.Join(root, "node.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for iteration := 0; iteration < 2; iteration++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- RunContext(ctx, path, logger) }()
		ready := false
		deadline := time.Now().Add(8 * time.Second)
		client := &http.Client{Timeout: 200 * time.Millisecond}
		for time.Now().Before(deadline) {
			select {
			case err := <-done:
				cancel()
				t.Fatalf("engine failed early: %v", err)
			default:
			}
			response, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", fmtPort(rpcPort)))
			if err == nil {
				response.Body.Close()
				ready = true
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		select {
		case err = <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("embedded node stop timed out")
		}
		if !ready {
			t.Fatal("RPC did not start")
		}
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmtPort(rpcPort)))
		if err != nil {
			t.Fatalf("RPC listener leaked: %v", err)
		}
		listener.Close()
		listener, err = net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmtPort(p2pPort)))
		if err != nil {
			t.Fatalf("P2P listener leaked: %v", err)
		}
		listener.Close()
	}
}

func fmtPort(port int) string { return fmt.Sprint(port) }
