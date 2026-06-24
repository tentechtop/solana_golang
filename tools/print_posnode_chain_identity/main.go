package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/programs/stake"
	runtimepkg "solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
)

type output struct {
	ChainID           string `json:"chain_id"`
	GenesisHash       string `json:"genesis_hash"`
	ChainIdentityHash string `json:"chain_identity_hash"`
	DataRootPath      string `json:"data_root_path"`
	DataPath          string `json:"data_path"`
	P2PNetworkID      string `json:"p2p_network_id"`
}

type nodeConfig struct {
	ChainID                       string                          `json:"chain_id"`
	Environment                   string                          `json:"environment"`
	Production                    bool                            `json:"production"`
	NodeName                      string                          `json:"node_name"`
	DataPath                      string                          `json:"data_path"`
	DataRootPath                  string                          `json:"-"`
	ListenIP                      string                          `json:"listen_ip"`
	ListenPort                    int                             `json:"listen_port"`
	AdvertisedIP                  string                          `json:"advertised_ip,omitempty"`
	AdvertisedPort                int                             `json:"advertised_port,omitempty"`
	RPCEnabled                    bool                            `json:"rpc_enabled"`
	RPCListenIP                   string                          `json:"rpc_listen_ip"`
	RPCPort                       int                             `json:"rpc_port"`
	AllowInsecureP2P              *bool                           `json:"allow_insecure_p2p,omitempty"`
	PeerSeed                      string                          `json:"peer_seed"`
	StakerSeed                    string                          `json:"staker_seed"`
	ValidatorSeed                 string                          `json:"validator_seed"`
	ConsensusSeed                 string                          `json:"consensus_seed"`
	PeerKeyPath                   string                          `json:"peer_key_path,omitempty"`
	StakerKeyPath                 string                          `json:"staker_key_path,omitempty"`
	ValidatorKeyPath              string                          `json:"validator_key_path,omitempty"`
	ConsensusKeyPath              string                          `json:"consensus_key_path,omitempty"`
	BLSKeyPath                    string                          `json:"bls_key_path,omitempty"`
	StakeLamports                 uint64                          `json:"stake_lamports"`
	BootstrapPeers                []peerConfig                    `json:"bootstrap_peers"`
	Genesis                       genesisConfig                   `json:"genesis"`
	SlotMillis                    int                             `json:"slot_millis"`
	GenesisStartMs                int64                           `json:"genesis_start_unix_millis"`
	EpochSlots                    uint64                          `json:"epoch_slots"`
	FinalityDepth                 uint64                          `json:"finality_depth"`
	DisableStateRecovery          bool                            `json:"disable_state_recovery,omitempty"`
	TurbineFanout                 int                             `json:"turbine_fanout"`
	AutoRegister                  bool                            `json:"auto_register"`
	MempoolMaxTransactions        int                             `json:"mempool_max_transactions"`
	MempoolTransactionTTLMillis   int64                           `json:"mempool_transaction_ttl_millis"`
	TransactionLeaderForwardSlots int                             `json:"transaction_leader_forward_slots"`
	TransactionForwardValidators  *bool                           `json:"transaction_forward_validators,omitempty"`
	TreasuryKeyPath               string                          `json:"treasury_key_path,omitempty"`
	AllowHardcodedTreasury        *bool                           `json:"allow_hardcoded_treasury,omitempty"`
	AutoTransfer                  *autoTransferConfig             `json:"auto_transfer,omitempty"`
	PrivacyExecutionMode          runtimepkg.PrivacyExecutionMode `json:"privacy_execution_mode,omitempty"`
	ContractDeploymentPolicy      contractDeploymentPolicyConfig  `json:"contract_deployment_policy,omitempty"`
	GenesisHash                   string                          `json:"-"`
	ChainIdentityHash             string                          `json:"-"`
	P2PNetworkID                  string                          `json:"-"`
}

type peerConfig struct {
	PeerID  string `json:"peer_id"`
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Network string `json:"network"`
}

type genesisConfig struct {
	InitialSupplyLamports uint64                          `json:"initial_supply_lamports"`
	TreasuryAddress       string                          `json:"treasury_address,omitempty"`
	FundedAccounts        []genesisAccountConfig          `json:"funded_accounts"`
	InitialValidators     []genesisValidatorConfig        `json:"initial_validators"`
	PrivacyExecutionMode  runtimepkg.PrivacyExecutionMode `json:"privacy_execution_mode,omitempty"`
}

type genesisAccountConfig struct {
	Address  string `json:"address,omitempty"`
	Seed     string `json:"seed"`
	Lamports uint64 `json:"lamports"`
}

type genesisValidatorConfig struct {
	StakerSeed         string `json:"staker_seed"`
	ValidatorSeed      string `json:"validator_seed"`
	ConsensusSeed      string `json:"consensus_seed"`
	StakerAddress      string `json:"staker_address,omitempty"`
	ValidatorAddress   string `json:"validator_address,omitempty"`
	ConsensusPublicKey string `json:"consensus_public_key,omitempty"`
	BLSPublicKeyBase64 string `json:"bls_public_key_base64,omitempty"`
	PeerID             string `json:"peer_id"`
	StakeLamports      uint64 `json:"stake_lamports"`
	CommissionBps      uint16 `json:"commission_bps,omitempty"`
}

type autoTransferConfig struct {
	ToSeed   string `json:"to_seed"`
	Lamports uint64 `json:"lamports"`
}

type contractDeploymentPolicyConfig struct {
	AllowedDeployers             []string              `json:"allowed_deployers,omitempty"`
	MinDeploymentDepositLamports uint64                `json:"min_deployment_deposit_lamports,omitempty"`
	RequireManifest              *bool                 `json:"require_manifest,omitempty"`
	AllowUpgradeableContracts    *bool                 `json:"allow_upgradeable_contracts,omitempty"`
	ResolvedAllowedDeployers     []structure.PublicKey `json:"-"`
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

const (
	defaultChainID                           = "pos-localnet"
	defaultSlotMillis                        = 1000
	defaultEpochSlots                        = 8
	defaultInitialSupply                     = uint64(1_000_000_000_000_000_000)
	defaultProductionContractDeploymentStake = uint64(10_000_000)
)

func main() {
	configPath := flag.String("config", "", "posnode config path")
	flag.Parse()
	if strings.TrimSpace(*configPath) == "" {
		exitError("print_posnode_chain_identity: -config is required")
	}
	config, err := loadNodeConfig(*configPath)
	if err != nil {
		exitError("print_posnode_chain_identity: load config: %v", err)
	}
	result := output{
		ChainID:           config.ChainID,
		GenesisHash:       config.GenesisHash,
		ChainIdentityHash: config.ChainIdentityHash,
		DataRootPath:      config.DataRootPath,
		DataPath:          config.DataPath,
		P2PNetworkID:      config.P2PNetworkID,
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		exitError("print_posnode_chain_identity: marshal output: %v", err)
	}
	fmt.Println(string(encoded))
}

func loadNodeConfig(path string) (nodeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nodeConfig{}, fmt.Errorf("read config: %w", err)
	}
	config := nodeConfig{}
	if err := json.Unmarshal(data, &config); err != nil {
		return nodeConfig{}, fmt.Errorf("decode config: %w", err)
	}
	return normalizeNodeConfig(config)
}

func normalizeNodeConfig(config nodeConfig) (nodeConfig, error) {
	if strings.TrimSpace(config.ChainID) == "" {
		config.ChainID = defaultChainID
	}
	if strings.TrimSpace(config.NodeName) == "" {
		return nodeConfig{}, fmt.Errorf("node name is empty")
	}
	if strings.TrimSpace(config.DataPath) == "" {
		config.DataPath = "data/posnode-" + strings.TrimSpace(config.NodeName)
	}
	if config.ListenPort < 1 || config.ListenPort > 65535 {
		return nodeConfig{}, fmt.Errorf("invalid listen port")
	}
	if strings.TrimSpace(config.ListenIP) == "" {
		return nodeConfig{}, fmt.Errorf("listen ip is empty")
	}
	if strings.TrimSpace(config.AdvertisedIP) != "" && config.AdvertisedPort == 0 {
		config.AdvertisedPort = config.ListenPort
	}
	if config.AdvertisedPort < 0 || config.AdvertisedPort > 65535 {
		return nodeConfig{}, fmt.Errorf("invalid advertised port")
	}
	if config.RPCPort != 0 || config.RPCEnabled {
		config.RPCEnabled = true
		if config.RPCPort < 1 || config.RPCPort > 65535 {
			return nodeConfig{}, fmt.Errorf("invalid rpc port")
		}
		if strings.TrimSpace(config.RPCListenIP) == "" {
			config.RPCListenIP = config.ListenIP
		}
	}
	if config.SlotMillis == 0 {
		config.SlotMillis = defaultSlotMillis
	}
	if config.SlotMillis < 200 {
		return nodeConfig{}, fmt.Errorf("slot millis must be >= 200")
	}
	if config.GenesisStartMs == 0 {
		config.GenesisStartMs = time.Now().UnixMilli()
	}
	if config.EpochSlots == 0 {
		config.EpochSlots = defaultEpochSlots
	}
	if config.FinalityDepth == 0 {
		config.FinalityDepth = blockchain.DefaultFinalityDepth
	}
	if config.FinalityDepth < 1 || config.FinalityDepth > 1_000_000 {
		return nodeConfig{}, fmt.Errorf("finality depth must be 1..1000000")
	}
	if config.TurbineFanout == 0 {
		config.TurbineFanout = consensus.DefaultTurbineFanout
	}
	if config.TurbineFanout < 1 || config.TurbineFanout > consensus.MaxTurbineFanout {
		return nodeConfig{}, fmt.Errorf("turbine fanout must be 1..1024")
	}
	if config.StakeLamports == 0 {
		config.StakeLamports = stake.MinimumStakeLamports
	}
	if config.StakeLamports < stake.MinimumStakeLamports {
		return nodeConfig{}, fmt.Errorf("stake lamports below minimum")
	}
	if config.MempoolMaxTransactions == 0 {
		config.MempoolMaxTransactions = 5000
	}
	if config.MempoolMaxTransactions < 1 {
		return nodeConfig{}, fmt.Errorf("mempool max transactions must be positive")
	}
	if config.MempoolTransactionTTLMillis == 0 {
		config.MempoolTransactionTTLMillis = 60000
	}
	if config.MempoolTransactionTTLMillis < int64(config.SlotMillis) {
		return nodeConfig{}, fmt.Errorf("mempool ttl must be >= slot millis")
	}
	if config.TransactionLeaderForwardSlots == 0 {
		config.TransactionLeaderForwardSlots = 4
	}
	if config.TransactionLeaderForwardSlots < 0 || config.TransactionLeaderForwardSlots > 64 {
		return nodeConfig{}, fmt.Errorf("transaction leader forward slots must be 0..64")
	}
	if config.Genesis.InitialSupplyLamports == 0 {
		config.Genesis.InitialSupplyLamports = defaultInitialSupply
	}
	if len(config.Genesis.FundedAccounts) == 0 {
		return nodeConfig{}, fmt.Errorf("genesis funded accounts are empty")
	}
	if err := normalizePrivacyExecutionModeConfig(&config); err != nil {
		return nodeConfig{}, err
	}
	if err := normalizeContractDeploymentPolicyConfig(&config); err != nil {
		return nodeConfig{}, err
	}
	return enrichNodeChainIdentity(config)
}

func enrichNodeChainIdentity(config nodeConfig) (nodeConfig, error) {
	genesisConfig, err := buildBlockchainGenesisConfig(config)
	if err != nil {
		return nodeConfig{}, err
	}
	_, head, err := blockchain.BuildGenesisState(genesisConfig)
	if err != nil {
		return nodeConfig{}, fmt.Errorf("build chain identity genesis: %w", err)
	}
	programExecutionPolicy, err := defaultProgramExecutionPolicyFingerprint(config.Genesis.PrivacyExecutionMode)
	if err != nil {
		return nodeConfig{}, err
	}
	payload := chainIdentityPayload{
		ChainID:                       config.ChainID,
		GenesisHash:                   head.BlockHash.String(),
		GenesisStartMs:                config.GenesisStartMs,
		SlotMillis:                    config.SlotMillis,
		EpochSlots:                    config.EpochSlots,
		FinalityDepth:                 config.FinalityDepth,
		TurbineFanout:                 config.TurbineFanout,
		TransactionLeaderForwardSlots: config.TransactionLeaderForwardSlots,
		TransactionForwardValidators:  forwardTransactionsToValidators(config),
		PrivacyExecutionMode:          string(config.Genesis.PrivacyExecutionMode),
		ProgramExecutionPolicy:        programExecutionPolicy,
		ContractDeploymentPolicy:      contractDeploymentPolicyFingerprint(config.ContractDeploymentPolicy),
	}
	identityBytes, err := json.Marshal(payload)
	if err != nil {
		return nodeConfig{}, fmt.Errorf("marshal chain identity: %w", err)
	}
	identityHash, err := structure.NewHash(utils.SHA256(identityBytes))
	if err != nil {
		return nodeConfig{}, fmt.Errorf("hash chain identity: %w", err)
	}
	dataRootPath := strings.TrimSpace(config.DataRootPath)
	if dataRootPath == "" {
		dataRootPath = strings.TrimSpace(config.DataPath)
	}
	config.DataRootPath = filepath.Clean(dataRootPath)
	config.DataPath = filepath.Join(filepath.Clean(dataRootPath), "chains", identityHash.String())
	config.GenesisHash = payload.GenesisHash
	config.ChainIdentityHash = identityHash.String()
	config.P2PNetworkID = identityHash.String()
	return config, nil
}

func buildBlockchainGenesisConfig(config nodeConfig) (blockchain.GenesisConfig, error) {
	programExecutionPolicy, err := defaultProgramExecutionPolicyFingerprint(config.Genesis.PrivacyExecutionMode)
	if err != nil {
		return blockchain.GenesisConfig{}, err
	}
	genesis := blockchain.GenesisConfig{
		ChainID:                config.ChainID,
		InitialSupplyLamports:  config.Genesis.InitialSupplyLamports,
		PrivacyExecutionMode:   string(config.Genesis.PrivacyExecutionMode),
		ProgramExecutionPolicy: programExecutionPolicy,
		FundedAccounts:         make([]blockchain.GenesisAccount, 0, len(config.Genesis.FundedAccounts)),
		InitialValidators:      make([]blockchain.GenesisValidator, 0, len(config.Genesis.InitialValidators)),
	}
	if config.Genesis.TreasuryAddress != "" {
		treasuryAddress, err := structure.PublicKeyFromBase58(config.Genesis.TreasuryAddress)
		if err != nil {
			return blockchain.GenesisConfig{}, fmt.Errorf("decode genesis treasury address: %w", err)
		}
		genesis.TreasuryAddress = treasuryAddress
	}
	for _, account := range config.Genesis.FundedAccounts {
		address, err := publicKeyFromAddressOrSeed(account.Address, account.Seed, "funded account")
		if err != nil {
			return blockchain.GenesisConfig{}, err
		}
		genesis.FundedAccounts = append(genesis.FundedAccounts, blockchain.GenesisAccount{
			Address:  address,
			Lamports: account.Lamports,
		})
	}
	for _, validator := range config.Genesis.InitialValidators {
		stakerAddress, err := publicKeyFromAddressOrSeed(validator.StakerAddress, validator.StakerSeed, "validator staker")
		if err != nil {
			return blockchain.GenesisConfig{}, err
		}
		validatorAddress, err := publicKeyFromAddressOrSeed(validator.ValidatorAddress, validator.ValidatorSeed, "validator account")
		if err != nil {
			return blockchain.GenesisConfig{}, err
		}
		consensusPublicKey, err := publicKeyFromAddressOrSeed(validator.ConsensusPublicKey, validator.ConsensusSeed, "validator consensus")
		if err != nil {
			return blockchain.GenesisConfig{}, err
		}
		blsPublicKey, err := blsPublicKey(validator.BLSPublicKeyBase64, validator.ConsensusSeed)
		if err != nil {
			return blockchain.GenesisConfig{}, err
		}
		genesis.InitialValidators = append(genesis.InitialValidators, blockchain.GenesisValidator{
			StakerAddress:      stakerAddress,
			ValidatorAddress:   validatorAddress,
			ConsensusPublicKey: consensusPublicKey,
			BLSPublicKey:       blsPublicKey,
			P2PPeerID:          validator.PeerID,
			StakeLamports:      validator.StakeLamports,
			CommissionBps:      validator.CommissionBps,
		})
	}
	return genesis, nil
}

func normalizePrivacyExecutionModeConfig(config *nodeConfig) error {
	rootMode, rootErr := runtimepkg.NormalizePrivacyExecutionMode(config.PrivacyExecutionMode)
	genesisMode, genesisErr := runtimepkg.NormalizePrivacyExecutionMode(config.Genesis.PrivacyExecutionMode)
	if config.PrivacyExecutionMode != "" && rootErr != nil {
		return fmt.Errorf("invalid privacy execution mode: %w", rootErr)
	}
	if config.Genesis.PrivacyExecutionMode != "" && genesisErr != nil {
		return fmt.Errorf("invalid genesis privacy execution mode: %w", genesisErr)
	}
	if config.PrivacyExecutionMode != "" && config.Genesis.PrivacyExecutionMode != "" && rootMode != genesisMode {
		return fmt.Errorf("privacy execution mode mismatch root=%s genesis=%s", rootMode, genesisMode)
	}
	if config.Genesis.PrivacyExecutionMode != "" {
		config.PrivacyExecutionMode = genesisMode
		config.Genesis.PrivacyExecutionMode = genesisMode
		return nil
	}
	config.PrivacyExecutionMode = rootMode
	config.Genesis.PrivacyExecutionMode = rootMode
	return nil
}

func normalizeContractDeploymentPolicyConfig(config *nodeConfig) error {
	policy := config.ContractDeploymentPolicy
	policy.ResolvedAllowedDeployers = nil
	for index, value := range policy.AllowedDeployers {
		addressText := strings.TrimSpace(value)
		if addressText == "" {
			return fmt.Errorf("contract deployment allowed_deployers[%d] is empty", index)
		}
		address, err := structure.PublicKeyFromBase58(addressText)
		if err != nil {
			return fmt.Errorf("contract deployment allowed_deployers[%d]: %w", index, err)
		}
		policy.ResolvedAllowedDeployers = append(policy.ResolvedAllowedDeployers, address)
	}
	if policy.RequireManifest == nil && isProductionNodeConfig(*config) {
		requireManifest := true
		policy.RequireManifest = &requireManifest
	}
	if policy.MinDeploymentDepositLamports == 0 && isProductionNodeConfig(*config) && len(policy.ResolvedAllowedDeployers) == 0 {
		policy.MinDeploymentDepositLamports = defaultProductionContractDeploymentStake
	}
	config.ContractDeploymentPolicy = policy
	return nil
}

func contractDeploymentPolicyFingerprint(config contractDeploymentPolicyConfig) string {
	requireManifest := false
	if config.RequireManifest != nil {
		requireManifest = *config.RequireManifest
	}
	allowUpgradeable := false
	if config.AllowUpgradeableContracts != nil {
		allowUpgradeable = *config.AllowUpgradeableContracts
	}
	deployers := make([]string, 0, len(config.ResolvedAllowedDeployers))
	for _, deployer := range config.ResolvedAllowedDeployers {
		deployers = append(deployers, deployer.String())
	}
	sort.Strings(deployers)
	return fmt.Sprintf(
		"contract_deployment_policy_v1|min_deposit=%d|require_manifest=%t|allow_upgradeable=%t|deployers=%s",
		config.MinDeploymentDepositLamports,
		requireManifest,
		allowUpgradeable,
		strings.Join(deployers, ","),
	)
}

func defaultProgramExecutionPolicyFingerprint(privacyMode runtimepkg.PrivacyExecutionMode) (string, error) {
	policy, err := runtimepkg.NewDefaultProgramExecutionPolicy(structure.DefaultBuiltinProgramIDs, privacyMode)
	if err != nil {
		return "", fmt.Errorf("program execution policy: %w", err)
	}
	return policy.Fingerprint(), nil
}

func isProductionNodeConfig(config nodeConfig) bool {
	if config.Production {
		return true
	}
	environment := strings.TrimSpace(strings.ToLower(config.Environment))
	return environment == "production" || environment == "prod"
}

func publicKeyFromAddressOrSeed(addressText string, seedText string, fieldName string) (structure.PublicKey, error) {
	addressText = strings.TrimSpace(addressText)
	if addressText != "" {
		publicKey, err := structure.PublicKeyFromBase58(addressText)
		if err != nil {
			return structure.PublicKey{}, fmt.Errorf("decode genesis %s address: %w", fieldName, err)
		}
		return publicKey, nil
	}
	seedText = strings.TrimSpace(seedText)
	if seedText == "" {
		return structure.PublicKey{}, fmt.Errorf("genesis %s key is empty", fieldName)
	}
	keyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(seedText)))
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("derive genesis %s key: %w", fieldName, err)
	}
	return keyPair.PublicKey, nil
}

func blsPublicKey(encodedPublicKey string, consensusSeed string) ([]byte, error) {
	encodedPublicKey = strings.TrimSpace(encodedPublicKey)
	if encodedPublicKey != "" {
		publicKey, err := utils.Base64Decode(encodedPublicKey)
		if err != nil {
			return nil, fmt.Errorf("decode genesis bls public key: %w", err)
		}
		if err := consensus.ValidateBLSPublicKey(publicKey); err != nil {
			return nil, err
		}
		return publicKey, nil
	}
	consensusSeed = strings.TrimSpace(consensusSeed)
	if consensusSeed == "" {
		return nil, nil
	}
	keyPair, err := consensus.BLSKeyPairFromSeed(utils.SHA256([]byte(consensusSeed)))
	if err != nil {
		return nil, fmt.Errorf("derive genesis bls public key: %w", err)
	}
	return keyPair.PublicKey, nil
}

func forwardTransactionsToValidators(config nodeConfig) bool {
	if config.TransactionForwardValidators == nil {
		return true
	}
	return *config.TransactionForwardValidators
}

func exitError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
