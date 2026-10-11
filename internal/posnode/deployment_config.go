package posnode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tentechtop/ordexus-core/rpc"
)

// FullNodeDeploymentOptions contains only this node's identity and endpoints.
// The network's genesis and chain identity must come from an existing manifest.
type FullNodeDeploymentOptions struct {
	Name, DataPath, AdvertisedIP, PeerSeed, Network string
	P2PPort, RPCPort, MultiValidatorThreshold       int
	PublicRPC                                       bool
	WalletPairing                                   bool
}

// PrepareFullNodeDeploymentConfig reuses the node's manifest and identity
// validation; a deployment tool must never invent a replacement genesis.
func PrepareFullNodeDeploymentConfig(manifest rpc.BootstrapManifestResult, options FullNodeDeploymentOptions) ([]byte, error) {
	if err := validateBootstrapManifest(manifest); err != nil {
		return nil, err
	}
	if !manifest.Ready || len(manifest.BootstrapPeers) == 0 {
		return nil, fmt.Errorf("posnode: ready manifest with bootstrap peers required")
	}
	if strings.TrimSpace(options.DataPath) == "" {
		return nil, fmt.Errorf("posnode: deployment data path required")
	}
	disabled, enabled := false, true
	if options.Network == "" {
		options.Network = "tcp"
	}
	rpcIP := "127.0.0.1"
	if options.PublicRPC {
		rpcIP = "0.0.0.0"
	}
	config := nodeConfig{
		Environment: "stage", NodeName: options.Name,
		DataPath: options.DataPath, ListenIP: "0.0.0.0", ListenPort: options.P2PPort,
		Network: options.Network, AdvertisedIP: options.AdvertisedIP, AdvertisedPort: options.P2PPort,
		PeerSeed: options.PeerSeed, RPCEnabled: true, RPCListenIP: rpcIP, RPCPort: options.RPCPort,
		AllowInsecureP2P: &disabled, ValidatorEnabled: &disabled, ConsensusEnabled: &disabled,
		NodeFeatures:      nodeFeaturesConfig{BootnodeEnabled: &enabled, ArchiveEnabled: &enabled, WalletPairingEnabled: &disabled},
		ConsensusPolicy:   consensusPolicyConfig{MultiValidatorThreshold: options.MultiValidatorThreshold},
		EmergencyRecovery: emergencyRecoveryConfig{Enabled: true},
	}
	if options.WalletPairing {
		// Keep this legacy feature switch unset when wallet pairing may later
		// write validator_enabled and disable the pairing session.
		config.NodeFeatures.WalletPairingEnabled = nil
		config.ValidatorPairing.Enabled = &enabled
		config.ValidatorPairing.AutoWriteConfig = &enabled
	}
	joined, err := applyBootstrapManifest(config, manifest)
	if err != nil {
		return nil, err
	}
	// Normalization runs on Windows too. Store the input Linux data root, letting
	// the deployed Linux node calculate its own chain-specific ledger directory.
	joined.DataPath, joined.DataRootPath = options.DataPath, ""
	return json.MarshalIndent(joined, "", "  ")
}
