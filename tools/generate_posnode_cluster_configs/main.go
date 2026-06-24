package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/programs/stake"
	runtimepkg "solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	defaultOutputDir                       = "deploy/generated-4"
	defaultChainID                         = "pos-4v-testnet"
	defaultValidatorCount                  = 4
	defaultWinValidatorCount               = 4
	defaultBootHost                        = "101.35.87.31"
	defaultWinHost                         = "192.168.121.225"
	defaultMacHost                         = "192.168.120.223"
	defaultValidatorStake                  = stake.MinimumStakeLamports
	defaultInitialSupply                   = uint64(1_000_000_000_000)
	defaultUserFund                        = uint64(2_000_000_000)
	defaultStakerExtraFund                 = uint64(200_000_000)
	defaultSlotMillis                      = 1200
	defaultEpochSlots                      = 600
	defaultFinalityDepth                   = 2
	defaultTurbineFanout                   = 4
	defaultTransactionLeaderForwardSlots   = 8
	defaultTransactionForwardValidators    = true
	defaultRPCRequestTimeoutMillis         = int64(8000)
	defaultRPCMaxBodyBytes                 = int64(1 << 20)
	defaultRPCMaxBatchSize                 = 32
	defaultRPCGatewayDialIntervalMillis    = int64(2000)
	defaultRPCGatewayMaxPeers              = 256
	defaultRPCGatewayMaxConnections        = 256
	defaultRPCGatewaySoftwareVersion       = "rpcnode/0.1.0"
	defaultContractDeploymentPolicyPayload = "contract_deployment_policy_v1|min_deposit=0|require_manifest=false|allow_upgradeable=false|deployers="
)

type nodeConfigFile struct {
	NodeMode                      string            `json:"node_mode"`
	ChainID                       string            `json:"chain_id"`
	Environment                   string            `json:"environment"`
	Production                    bool              `json:"production"`
	NodeName                      string            `json:"node_name"`
	DataPath                      string            `json:"data_path"`
	ListenIP                      string            `json:"listen_ip"`
	ListenPort                    int               `json:"listen_port"`
	AdvertisedIP                  string            `json:"advertised_ip"`
	AdvertisedPort                int               `json:"advertised_port"`
	RPCEnabled                    bool              `json:"rpc_enabled"`
	RPCListenIP                   string            `json:"rpc_listen_ip"`
	RPCPort                       int               `json:"rpc_port"`
	AllowInsecureP2P              bool              `json:"allow_insecure_p2p"`
	NodeRole                      string            `json:"node_role"`
	NodeRoles                     []string          `json:"node_roles"`
	NodeCapabilities              []string          `json:"node_capabilities,omitempty"`
	ValidatorEnabled              *bool             `json:"validator_enabled,omitempty"`
	ConsensusEnabled              *bool             `json:"consensus_enabled,omitempty"`
	PeerSeed                      string            `json:"peer_seed"`
	StakerSeed                    string            `json:"staker_seed,omitempty"`
	ValidatorSeed                 string            `json:"validator_seed,omitempty"`
	ConsensusSeed                 string            `json:"consensus_seed,omitempty"`
	StakeLamports                 uint64            `json:"stake_lamports,omitempty"`
	SlotMillis                    int               `json:"slot_millis"`
	GenesisStartUnixMillis        int64             `json:"genesis_start_unix_millis"`
	EpochSlots                    uint64            `json:"epoch_slots"`
	FinalityDepth                 uint64            `json:"finality_depth"`
	TurbineFanout                 int               `json:"turbine_fanout"`
	AutoRegister                  bool              `json:"auto_register"`
	MempoolMaxTransactions        int               `json:"mempool_max_transactions"`
	MempoolTransactionTTLMillis   int64             `json:"mempool_transaction_ttl_millis"`
	TransactionLeaderForwardSlots int               `json:"transaction_leader_forward_slots"`
	TransactionForwardValidators  bool              `json:"transaction_forward_validators"`
	TreasuryKeyPath               string            `json:"treasury_key_path,omitempty"`
	BootstrapPeers                []peerConfigFile  `json:"bootstrap_peers"`
	Genesis                       genesisConfigFile `json:"genesis"`
}

type rpcNodeConfigFile struct {
	NodeMode                string           `json:"node_mode"`
	Environment             string           `json:"environment"`
	Production              bool             `json:"production"`
	NodeName                string           `json:"node_name"`
	ListenIP                string           `json:"listen_ip"`
	AdvertisedIP            string           `json:"advertised_ip"`
	ListenPort              int              `json:"listen_port"`
	PeerSeed                string           `json:"peer_seed"`
	AllowInsecureP2P        bool             `json:"allow_insecure_p2p"`
	NetworkID               string           `json:"network_id"`
	SoftwareVersion         string           `json:"software_version"`
	NodeRole                string           `json:"node_role"`
	NodeRoles               []string         `json:"node_roles"`
	MaxPeers                int              `json:"max_peers"`
	MaxConnections          int              `json:"max_connections"`
	DialIntervalMillis      int64            `json:"dial_interval_millis"`
	StaticPeers             []peerConfigFile `json:"static_peers"`
	RPCListenIP             string           `json:"rpc_listen_ip"`
	RPCPort                 int              `json:"rpc_port"`
	RPCRequestTimeoutMillis int64            `json:"rpc_request_timeout_millis"`
	RPCMaxBodyBytes         int64            `json:"rpc_max_body_bytes"`
	RPCMaxBatchSize         int              `json:"rpc_max_batch_size"`
}

type peerConfigFile struct {
	PeerID       string   `json:"peer_id"`
	IP           string   `json:"ip"`
	Port         int      `json:"port"`
	Network      string   `json:"network"`
	Role         string   `json:"role"`
	Roles        []string `json:"roles"`
	Capabilities []string `json:"capabilities"`
}

type genesisConfigFile struct {
	InitialSupplyLamports uint64                 `json:"initial_supply_lamports"`
	TreasuryAddress       string                 `json:"treasury_address"`
	FundedAccounts        []genesisAccountFile   `json:"funded_accounts"`
	InitialValidators     []genesisValidatorFile `json:"initial_validators"`
}

type genesisAccountFile struct {
	Seed     string `json:"seed"`
	Lamports uint64 `json:"lamports"`
}

type genesisValidatorFile struct {
	StakerSeed    string `json:"staker_seed"`
	ValidatorSeed string `json:"validator_seed"`
	ConsensusSeed string `json:"consensus_seed"`
	PeerID        string `json:"peer_id"`
	StakeLamports uint64 `json:"stake_lamports"`
}

type nodeManifest struct {
	Name          string `json:"name"`
	HostGroup     string `json:"host_group"`
	ConfigPath    string `json:"config_path"`
	RemoteConfig  string `json:"remote_config,omitempty"`
	DataPath      string `json:"data_path"`
	PeerSeed      string `json:"peer_seed"`
	PeerID        string `json:"peer_id"`
	StakerSeed    string `json:"staker_seed,omitempty"`
	StakerAddress string `json:"staker_address,omitempty"`
	ValidatorSeed string `json:"validator_seed,omitempty"`
	ValidatorAddr string `json:"validator_address,omitempty"`
	ConsensusSeed string `json:"consensus_seed,omitempty"`
	ValidatorID   string `json:"validator_id,omitempty"`
	AdvertisedIP  string `json:"advertised_ip"`
	P2PPort       int    `json:"p2p_port"`
	RPCURL        string `json:"rpc_url"`
	Role          string `json:"role"`
}

type clusterManifest struct {
	GeneratedAtUnixMillis int64          `json:"generated_at_unix_millis"`
	ChainID               string         `json:"chain_id"`
	GenesisStartMillis    int64          `json:"genesis_start_unix_millis"`
	SlotMillis            int            `json:"slot_millis"`
	EpochSlots            uint64         `json:"epoch_slots"`
	ValidatorStake        uint64         `json:"validator_stake_lamports"`
	Bootnode              nodeManifest   `json:"bootnode"`
	Validators            []nodeManifest `json:"validators"`
	UserSeeds             []string       `json:"user_seeds"`
	RPCURLs               []string       `json:"rpc_urls"`
}

type keyInfo struct {
	AccountPublicKey string
	PeerID           string
	ValidatorID      string
}

type chainIdentityPayload struct {
	ChainID                       string `json:"chain_id"`
	GenesisHash                   string `json:"genesis_hash"`
	GenesisStartMs                int64  `json:"genesis_start_unix_millis"`
	SlotMillis                    int    `json:"slot_millis"`
	EpochSlots                    uint64 `json:"epoch_slots"`
	FinalityDepth                 uint64 `json:"finality_depth"`
	TurbineFanout                 int    `json:"turbine_fanout"`
	TransactionLeaderForwardSlots int    `json:"transaction_leader_forward_slots"`
	TransactionForwardValidators  bool   `json:"transaction_forward_validators"`
	PrivacyExecutionMode          string `json:"privacy_execution_mode"`
	ProgramExecutionPolicy        string `json:"program_execution_policy"`
	ContractDeploymentPolicy      string `json:"contract_deployment_policy"`
}

func main() {
	outputDir := flag.String("output", defaultOutputDir, "output directory")
	chainID := flag.String("chain-id", defaultChainID, "chain id")
	validatorCount := flag.Int("validator-count", defaultValidatorCount, "initial validator count")
	winCount := flag.Int("win-count", defaultWinValidatorCount, "validators generated for the windows host before mac")
	bootHost := flag.String("boot-host", defaultBootHost, "public bootnode host")
	winHost := flag.String("win-host", defaultWinHost, "windows advertised host")
	macHost := flag.String("mac-host", defaultMacHost, "mac advertised host")
	genesisStart := flag.Int64("genesis-start", time.Now().Add(10*time.Second).UnixMilli(), "genesis start millis")
	flag.Parse()

	options := clusterOptions{
		OutputDir:      *outputDir,
		ChainID:        *chainID,
		ValidatorCount: *validatorCount,
		WinCount:       *winCount,
		BootHost:       *bootHost,
		WinHost:        *winHost,
		MacHost:        *macHost,
		GenesisStart:   *genesisStart,
	}
	if err := generateCluster(options); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

type clusterOptions struct {
	OutputDir      string
	ChainID        string
	ValidatorCount int
	WinCount       int
	BootHost       string
	WinHost        string
	MacHost        string
	GenesisStart   int64
}

func generateCluster(options clusterOptions) error {
	options.OutputDir = filepath.Clean(strings.TrimSpace(options.OutputDir))
	options.ChainID = strings.TrimSpace(options.ChainID)
	if options.ValidatorCount < 1 {
		return fmt.Errorf("validator count must be positive")
	}
	if options.WinCount < 0 || options.WinCount > options.ValidatorCount {
		return fmt.Errorf("windows validator count must be between 0 and validator count")
	}
	if options.ChainID == "" {
		return fmt.Errorf("chain id is required")
	}
	outputDir := options.OutputDir
	if outputDir == "." || outputDir == string(filepath.Separator) {
		return fmt.Errorf("output directory is unsafe: %s", outputDir)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	chainTag := chainTagFromID(options.ChainID)
	userSeeds := buildUserSeeds(chainTag, 32)
	validators, initialValidators, err := buildValidators(options.WinHost, options.MacHost, outputDir, chainTag, options.ValidatorCount, options.WinCount)
	if err != nil {
		return err
	}
	fundedAccounts := buildFundedAccounts(userSeeds, initialValidators)
	genesis := genesisConfigFile{
		InitialSupplyLamports: defaultInitialSupply,
		TreasuryAddress:       "4vgAxQAXeKXhyrJyQ5XDXzr1wR92NaS631GEkDjdhRn9",
		FundedAccounts:        fundedAccounts,
		InitialValidators:     initialValidators,
	}
	bootnode, err := buildBootnode(outputDir, options.BootHost, chainTag)
	if err != nil {
		return err
	}
	networkID, err := buildChainNetworkID(options.ChainID, options.GenesisStart, genesis)
	if err != nil {
		return err
	}

	allPeers := buildAllPeers(bootnode, *validators)
	for index := range *validators {
		node := &(*validators)[index]
		config := buildValidatorConfig(*node, allPeers, options.ChainID, options.GenesisStart, genesis)
		config.BootstrapPeers = filterSelfPeer(config.BootstrapPeers, node.PeerID)
		if err := writeJSON(node.ConfigPath, config); err != nil {
			return err
		}
	}
	rpcNodeConfig := buildRPCNodeConfig(bootnode, *validators, networkID)
	if err := writeJSON(bootnode.ConfigPath, rpcNodeConfig); err != nil {
		return err
	}

	manifest := clusterManifest{
		GeneratedAtUnixMillis: time.Now().UnixMilli(),
		ChainID:               options.ChainID,
		GenesisStartMillis:    options.GenesisStart,
		SlotMillis:            defaultSlotMillis,
		EpochSlots:            defaultEpochSlots,
		ValidatorStake:        defaultValidatorStake,
		Bootnode:              bootnode,
		Validators:            *validators,
		UserSeeds:             userSeeds,
		RPCURLs:               buildRPCURLs(bootnode, *validators),
	}
	if err := writeJSON(filepath.Join(outputDir, "manifest.json"), manifest); err != nil {
		return err
	}
	if err := writeRPCURLFile(filepath.Join(outputDir, "rpc_urls.txt"), manifest.RPCURLs); err != nil {
		return err
	}
	fmt.Printf("generated %d validators in %s\n", len(*validators), outputDir)
	return nil
}

func buildValidators(
	winHost string,
	macHost string,
	outputDir string,
	chainTag string,
	validatorCount int,
	winCount int,
) (*[]nodeManifest, []genesisValidatorFile, error) {
	validators := make([]nodeManifest, 0, validatorCount)
	initialValidators := make([]genesisValidatorFile, 0, validatorCount)
	for index := 1; index <= validatorCount; index++ {
		hostGroup := "win"
		host := winHost
		p2pPort := 5210 + index - 1
		rpcPort := 8910 + index - 1
		dataPath := fmt.Sprintf("F:/workSpace2029/solana_golang/data/posnode-%s-win-%02d", chainTag, index)
		configPath := filepath.Join(outputDir, fmt.Sprintf("posnode-win-%02d.json", index))
		remoteConfig := ""
		if index > winCount {
			macIndex := index - winCount
			hostGroup = "mac"
			host = macHost
			p2pPort = 5310 + macIndex - 1
			rpcPort = 9010 + macIndex - 1
			dataPath = fmt.Sprintf("/Users/mac/solana_golang/data/posnode-%s-mac-%02d", chainTag, macIndex)
			configPath = filepath.Join(outputDir, fmt.Sprintf("posnode-mac-%02d.json", macIndex))
			remoteConfig = fmt.Sprintf("/Users/mac/solana_golang/config/posnode-%s-mac-%02d.json", chainTag, macIndex)
		}
		peerSeed := fmt.Sprintf("node-%s-%02d", chainTag, index)
		stakerSeed := fmt.Sprintf("staker-%s-%02d", chainTag, index)
		validatorSeed := fmt.Sprintf("validator-%s-%02d", chainTag, index)
		consensusSeed := fmt.Sprintf("consensus-%s-%02d", chainTag, index)
		peerInfo, err := deriveKeyInfo(peerSeed)
		if err != nil {
			return nil, nil, err
		}
		stakerInfo, err := deriveKeyInfo(stakerSeed)
		if err != nil {
			return nil, nil, err
		}
		validatorInfo, err := deriveKeyInfo(validatorSeed)
		if err != nil {
			return nil, nil, err
		}
		consensusInfo, err := deriveKeyInfo(consensusSeed)
		if err != nil {
			return nil, nil, err
		}
		name := fmt.Sprintf("node-%s-%02d", chainTag, index)
		node := nodeManifest{
			Name:          name,
			HostGroup:     hostGroup,
			ConfigPath:    filepath.ToSlash(configPath),
			RemoteConfig:  remoteConfig,
			DataPath:      dataPath,
			PeerSeed:      peerSeed,
			PeerID:        peerInfo.PeerID,
			StakerSeed:    stakerSeed,
			StakerAddress: stakerInfo.AccountPublicKey,
			ValidatorSeed: validatorSeed,
			ValidatorAddr: validatorInfo.AccountPublicKey,
			ConsensusSeed: consensusSeed,
			ValidatorID:   consensusInfo.ValidatorID,
			AdvertisedIP:  host,
			P2PPort:       p2pPort,
			RPCURL:        fmt.Sprintf("http://%s:%d/", host, rpcPort),
			Role:          "validator",
		}
		validators = append(validators, node)
		initialValidators = append(initialValidators, genesisValidatorFile{
			StakerSeed:    stakerSeed,
			ValidatorSeed: validatorSeed,
			ConsensusSeed: consensusSeed,
			PeerID:        peerInfo.PeerID,
			StakeLamports: defaultValidatorStake,
		})
	}
	return &validators, initialValidators, nil
}

func buildBootnode(outputDir string, bootHost string, chainTag string) (nodeManifest, error) {
	peerSeed := fmt.Sprintf("node-%s-boot-101", chainTag)
	peerInfo, err := deriveKeyInfo(peerSeed)
	if err != nil {
		return nodeManifest{}, err
	}
	return nodeManifest{
		Name:         fmt.Sprintf("rpcnode-%s-101", chainTag),
		HostGroup:    "boot",
		ConfigPath:   filepath.ToSlash(filepath.Join(outputDir, "rpcnode-101.json")),
		RemoteConfig: "/opt/solana_golang/config/rpcnode-101.json",
		DataPath:     fmt.Sprintf("/opt/solana_golang/data/rpcnode-%s-101", chainTag),
		PeerSeed:     peerSeed,
		PeerID:       peerInfo.PeerID,
		AdvertisedIP: bootHost,
		P2PPort:      5101,
		RPCURL:       fmt.Sprintf("http://%s:8899/", bootHost),
		Role:         "public_rpc",
	}, nil
}

func buildValidatorConfig(node nodeManifest, peers []peerConfigFile, chainID string, genesisStart int64, genesis genesisConfigFile) nodeConfigFile {
	return nodeConfigFile{
		NodeMode:                      "posnode",
		ChainID:                       chainID,
		Environment:                   "stage",
		Production:                    false,
		NodeName:                      node.Name,
		DataPath:                      node.DataPath,
		ListenIP:                      "0.0.0.0",
		ListenPort:                    node.P2PPort,
		AdvertisedIP:                  node.AdvertisedIP,
		AdvertisedPort:                node.P2PPort,
		RPCEnabled:                    true,
		RPCListenIP:                   "0.0.0.0",
		RPCPort:                       rpcPortFromURL(node.RPCURL),
		AllowInsecureP2P:              false,
		NodeRole:                      "validator",
		NodeRoles:                     []string{"validator", "full"},
		PeerSeed:                      node.PeerSeed,
		StakerSeed:                    node.StakerSeed,
		ValidatorSeed:                 node.ValidatorSeed,
		ConsensusSeed:                 node.ConsensusSeed,
		StakeLamports:                 defaultValidatorStake,
		SlotMillis:                    defaultSlotMillis,
		GenesisStartUnixMillis:        genesisStart,
		EpochSlots:                    defaultEpochSlots,
		FinalityDepth:                 defaultFinalityDepth,
		TurbineFanout:                 defaultTurbineFanout,
		AutoRegister:                  false,
		MempoolMaxTransactions:        20000,
		MempoolTransactionTTLMillis:   180000,
		TransactionLeaderForwardSlots: defaultTransactionLeaderForwardSlots,
		TransactionForwardValidators:  defaultTransactionForwardValidators,
		TreasuryKeyPath:               treasuryPath(node.HostGroup),
		BootstrapPeers:                peers,
		Genesis:                       genesis,
	}
}

func buildRPCNodeConfig(node nodeManifest, validators []nodeManifest, networkID string) rpcNodeConfigFile {
	return rpcNodeConfigFile{
		NodeMode:                "rpcnode",
		Environment:             "stage",
		Production:              false,
		NodeName:                node.Name,
		ListenIP:                "0.0.0.0",
		AdvertisedIP:            node.AdvertisedIP,
		ListenPort:              node.P2PPort,
		PeerSeed:                node.PeerSeed,
		AllowInsecureP2P:        false,
		NetworkID:               networkID,
		SoftwareVersion:         defaultRPCGatewaySoftwareVersion,
		NodeRole:                "public_rpc",
		NodeRoles:               []string{"public_rpc"},
		MaxPeers:                defaultRPCGatewayMaxPeers,
		MaxConnections:          defaultRPCGatewayMaxConnections,
		DialIntervalMillis:      defaultRPCGatewayDialIntervalMillis,
		StaticPeers:             buildValidatorPeerConfigs(validators),
		RPCListenIP:             "0.0.0.0",
		RPCPort:                 8899,
		RPCRequestTimeoutMillis: defaultRPCRequestTimeoutMillis,
		RPCMaxBodyBytes:         defaultRPCMaxBodyBytes,
		RPCMaxBatchSize:         defaultRPCMaxBatchSize,
	}
}

func buildBootnodeConfig(node nodeManifest, peers []peerConfigFile, chainID string, genesisStart int64, genesis genesisConfigFile) nodeConfigFile {
	disabled := false
	return nodeConfigFile{
		NodeMode:                      "posnode",
		ChainID:                       chainID,
		Environment:                   "stage",
		Production:                    false,
		NodeName:                      node.Name,
		DataPath:                      node.DataPath,
		ListenIP:                      "0.0.0.0",
		ListenPort:                    node.P2PPort,
		AdvertisedIP:                  node.AdvertisedIP,
		AdvertisedPort:                node.P2PPort,
		RPCEnabled:                    true,
		RPCListenIP:                   "0.0.0.0",
		RPCPort:                       8899,
		AllowInsecureP2P:              false,
		NodeRole:                      "bootnode",
		NodeRoles:                     []string{"bootnode"},
		NodeCapabilities:              []string{"relay", "dht"},
		ValidatorEnabled:              &disabled,
		ConsensusEnabled:              &disabled,
		PeerSeed:                      node.PeerSeed,
		SlotMillis:                    defaultSlotMillis,
		GenesisStartUnixMillis:        genesisStart,
		EpochSlots:                    defaultEpochSlots,
		FinalityDepth:                 defaultFinalityDepth,
		TurbineFanout:                 defaultTurbineFanout,
		AutoRegister:                  false,
		MempoolMaxTransactions:        20000,
		MempoolTransactionTTLMillis:   180000,
		TransactionLeaderForwardSlots: defaultTransactionLeaderForwardSlots,
		TransactionForwardValidators:  defaultTransactionForwardValidators,
		BootstrapPeers:                peers,
		Genesis:                       genesis,
	}
}

func buildAllPeers(bootnode nodeManifest, validators []nodeManifest) []peerConfigFile {
	peers := make([]peerConfigFile, 0, len(validators)+1)
	peers = append(peers, peerConfigFile{
		PeerID:       bootnode.PeerID,
		IP:           bootnode.AdvertisedIP,
		Port:         bootnode.P2PPort,
		Network:      "tcp",
		Role:         "public_rpc",
		Roles:        []string{"public_rpc"},
		Capabilities: []string{"relay", "dht"},
	})
	peers = append(peers, buildValidatorPeerConfigs(validators)...)
	return peers
}

func buildValidatorPeerConfigs(validators []nodeManifest) []peerConfigFile {
	peers := make([]peerConfigFile, 0, len(validators))
	for _, validator := range validators {
		peers = append(peers, peerConfigFile{
			PeerID:       validator.PeerID,
			IP:           validator.AdvertisedIP,
			Port:         validator.P2PPort,
			Network:      "tcp",
			Role:         "validator",
			Roles:        []string{"validator", "full"},
			Capabilities: []string{"validator", "relay", "state_sync", "dht"},
		})
	}
	return peers
}

func buildChainNetworkID(chainID string, genesisStart int64, genesis genesisConfigFile) (string, error) {
	genesisConfig, err := buildBlockchainGenesisConfig(chainID, genesis)
	if err != nil {
		return "", err
	}
	_, head, err := blockchain.BuildGenesisState(genesisConfig)
	if err != nil {
		return "", fmt.Errorf("build genesis state: %w", err)
	}
	privacyMode, err := runtimepkg.NormalizePrivacyExecutionMode("")
	if err != nil {
		return "", fmt.Errorf("normalize privacy execution mode: %w", err)
	}
	programExecutionPolicy, err := defaultProgramExecutionPolicyFingerprint(privacyMode)
	if err != nil {
		return "", err
	}
	payload := chainIdentityPayload{
		ChainID:                       chainID,
		GenesisHash:                   head.BlockHash.String(),
		GenesisStartMs:                genesisStart,
		SlotMillis:                    defaultSlotMillis,
		EpochSlots:                    defaultEpochSlots,
		FinalityDepth:                 defaultFinalityDepth,
		TurbineFanout:                 defaultTurbineFanout,
		TransactionLeaderForwardSlots: defaultTransactionLeaderForwardSlots,
		TransactionForwardValidators:  defaultTransactionForwardValidators,
		PrivacyExecutionMode:          string(privacyMode),
		ProgramExecutionPolicy:        programExecutionPolicy,
		ContractDeploymentPolicy:      defaultContractDeploymentPolicyPayload,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal chain identity: %w", err)
	}
	identityHash, err := structure.NewHash(utils.SHA256(payloadBytes))
	if err != nil {
		return "", fmt.Errorf("hash chain identity: %w", err)
	}
	return identityHash.String(), nil
}

func buildBlockchainGenesisConfig(chainID string, genesis genesisConfigFile) (blockchain.GenesisConfig, error) {
	privacyMode, err := runtimepkg.NormalizePrivacyExecutionMode("")
	if err != nil {
		return blockchain.GenesisConfig{}, fmt.Errorf("normalize privacy execution mode: %w", err)
	}
	programExecutionPolicy, err := defaultProgramExecutionPolicyFingerprint(privacyMode)
	if err != nil {
		return blockchain.GenesisConfig{}, err
	}
	config := blockchain.GenesisConfig{
		ChainID:                chainID,
		InitialSupplyLamports:  genesis.InitialSupplyLamports,
		PrivacyExecutionMode:   string(privacyMode),
		ProgramExecutionPolicy: programExecutionPolicy,
		FundedAccounts:         make([]blockchain.GenesisAccount, 0, len(genesis.FundedAccounts)),
		InitialValidators:      make([]blockchain.GenesisValidator, 0, len(genesis.InitialValidators)),
	}
	if strings.TrimSpace(genesis.TreasuryAddress) != "" {
		treasuryAddress, err := structure.PublicKeyFromBase58(genesis.TreasuryAddress)
		if err != nil {
			return blockchain.GenesisConfig{}, fmt.Errorf("decode treasury address: %w", err)
		}
		config.TreasuryAddress = treasuryAddress
	}
	for _, account := range genesis.FundedAccounts {
		address, err := publicKeyFromSeed(account.Seed, "funded account")
		if err != nil {
			return blockchain.GenesisConfig{}, err
		}
		config.FundedAccounts = append(config.FundedAccounts, blockchain.GenesisAccount{
			Address:  address,
			Lamports: account.Lamports,
		})
	}
	for _, validator := range genesis.InitialValidators {
		validatorConfig, err := buildBlockchainGenesisValidator(validator)
		if err != nil {
			return blockchain.GenesisConfig{}, err
		}
		config.InitialValidators = append(config.InitialValidators, validatorConfig)
	}
	return config, nil
}

func buildBlockchainGenesisValidator(validator genesisValidatorFile) (blockchain.GenesisValidator, error) {
	stakerAddress, err := publicKeyFromSeed(validator.StakerSeed, "validator staker")
	if err != nil {
		return blockchain.GenesisValidator{}, err
	}
	validatorAddress, err := publicKeyFromSeed(validator.ValidatorSeed, "validator account")
	if err != nil {
		return blockchain.GenesisValidator{}, err
	}
	consensusPublicKey, err := publicKeyFromSeed(validator.ConsensusSeed, "validator consensus")
	if err != nil {
		return blockchain.GenesisValidator{}, err
	}
	blsPublicKey, err := blsPublicKeyFromSeed(validator.ConsensusSeed)
	if err != nil {
		return blockchain.GenesisValidator{}, err
	}
	return blockchain.GenesisValidator{
		StakerAddress:      stakerAddress,
		ValidatorAddress:   validatorAddress,
		ConsensusPublicKey: consensusPublicKey,
		BLSPublicKey:       blsPublicKey,
		P2PPeerID:          validator.PeerID,
		StakeLamports:      validator.StakeLamports,
	}, nil
}

func publicKeyFromSeed(seedText string, fieldName string) (structure.PublicKey, error) {
	seedText = strings.TrimSpace(seedText)
	if seedText == "" {
		return structure.PublicKey{}, fmt.Errorf("%s seed is empty", fieldName)
	}
	keyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(seedText)))
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("derive %s key: %w", fieldName, err)
	}
	return keyPair.PublicKey, nil
}

func blsPublicKeyFromSeed(seedText string) ([]byte, error) {
	seedText = strings.TrimSpace(seedText)
	if seedText == "" {
		return nil, fmt.Errorf("validator bls seed is empty")
	}
	keyPair, err := consensus.BLSKeyPairFromSeed(utils.SHA256([]byte(seedText)))
	if err != nil {
		return nil, fmt.Errorf("derive validator bls key: %w", err)
	}
	return keyPair.PublicKey, nil
}

func defaultProgramExecutionPolicyFingerprint(privacyMode runtimepkg.PrivacyExecutionMode) (string, error) {
	policy, err := runtimepkg.NewDefaultProgramExecutionPolicy(structure.DefaultBuiltinProgramIDs, privacyMode)
	if err != nil {
		return "", fmt.Errorf("program execution policy: %w", err)
	}
	return policy.Fingerprint(), nil
}

func filterSelfPeer(peers []peerConfigFile, selfPeerID string) []peerConfigFile {
	filtered := make([]peerConfigFile, 0, len(peers))
	for _, peer := range peers {
		if peer.PeerID == selfPeerID {
			continue
		}
		filtered = append(filtered, peer)
	}
	return filtered
}

func buildFundedAccounts(userSeeds []string, validators []genesisValidatorFile) []genesisAccountFile {
	accounts := make([]genesisAccountFile, 0, len(userSeeds)+len(validators)+1)
	accounts = append(accounts, genesisAccountFile{
		Seed:     "genesis local staker win treasury validator test wallet access phrase backup seed",
		Lamports: defaultUserFund,
	})
	for _, seed := range userSeeds {
		accounts = append(accounts, genesisAccountFile{Seed: seed, Lamports: defaultUserFund})
	}
	for _, validator := range validators {
		accounts = append(accounts, genesisAccountFile{Seed: validator.StakerSeed, Lamports: defaultStakerExtraFund})
	}
	return accounts
}

func buildUserSeeds(chainTag string, count int) []string {
	seeds := make([]string, count)
	for index := range seeds {
		seeds[index] = fmt.Sprintf("user-%s-%02d", chainTag, index+1)
	}
	return seeds
}

func chainTagFromID(chainID string) string {
	chainID = strings.TrimSpace(chainID)
	parts := strings.Split(chainID, "-")
	for _, part := range parts {
		if strings.HasSuffix(part, "v") && len(part) > 1 {
			return part
		}
	}
	return "4v"
}

func buildRPCURLs(bootnode nodeManifest, validators []nodeManifest) []string {
	urls := make([]string, 0, len(validators)+1)
	urls = append(urls, bootnode.RPCURL)
	for _, validator := range validators {
		urls = append(urls, validator.RPCURL)
	}
	return urls
}

func writeRPCURLFile(path string, urls []string) error {
	content := strings.Join(urls, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func deriveKeyInfo(seedText string) (keyInfo, error) {
	seedText = strings.TrimSpace(seedText)
	accountKeyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(seedText)))
	if err != nil {
		return keyInfo{}, err
	}
	privateKey := utils.SHA256([]byte(seedText))
	publicKey, err := utils.DeriveEd25519PublicKeyFromPrivateKey(privateKey)
	if err != nil {
		return keyInfo{}, err
	}
	consensusKeyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(seedText)))
	if err != nil {
		return keyInfo{}, err
	}
	validatorID := consensus.NewValidatorID(consensusKeyPair.PublicKey)
	return keyInfo{
		AccountPublicKey: accountKeyPair.PublicKey.String(),
		PeerID:           utils.Base58Encode(publicKey),
		ValidatorID:      string(validatorID),
	}, nil
}

func rpcPortFromURL(value string) int {
	lastColon := strings.LastIndex(value, ":")
	if lastColon < 0 {
		return 0
	}
	var port int
	_, _ = fmt.Sscanf(strings.TrimRight(value[lastColon+1:], "/"), "%d", &port)
	return port
}

func treasuryPath(hostGroup string) string {
	if hostGroup == "mac" {
		return "/Users/mac/solana_golang/config/genesis-access-treasury.json"
	}
	return "F:/workSpace2029/solana_golang/tmp/genesis-access-treasury.json"
}
