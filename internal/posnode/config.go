package posnode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/p2p"
	"solana_golang/programs/stake"
	runtimepkg "solana_golang/runtime"
	"solana_golang/structure"
)

const (
	defaultChainID                           = "pos-localnet"
	defaultSlotMillis                        = 1000
	defaultEpochSlots                        = 8
	defaultInitialSupply                     = uint64(1_000_000_000_000_000_000)
	defaultProductionContractDeploymentStake = uint64(10_000_000)
)

// nodeConfig 描述 posnode 配置 + 用同一 genesis 文件保证多节点状态一致。
type nodeConfig struct {
	ConfigPath                    string                          `json:"-"`
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
	NodeRole                      string                          `json:"node_role,omitempty"`
	NodeCapabilities              []string                        `json:"node_capabilities,omitempty"`
	ValidatorEnabled              *bool                           `json:"validator_enabled,omitempty"`
	ConsensusEnabled              *bool                           `json:"consensus_enabled,omitempty"`
	ResolvedNodeRole              p2p.PeerRole                    `json:"-"`
	ResolvedNodeCapabilities      p2p.PeerCapability              `json:"-"`
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
	TransactionForwardEnabled     *bool                           `json:"transaction_forward_enabled,omitempty"`
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
	PeerID               string             `json:"peer_id"`
	IP                   string             `json:"ip"`
	Port                 int                `json:"port"`
	Network              string             `json:"network"`
	Role                 string             `json:"role,omitempty"`
	Capabilities         []string           `json:"capabilities,omitempty"`
	ResolvedRole         p2p.PeerRole       `json:"-"`
	ResolvedCapabilities p2p.PeerCapability `json:"-"`
}

type genesisConfig struct {
	InitialSupplyLamports uint64                          `json:"initial_supply_lamports"`
	TreasuryAddress       string                          `json:"treasury_address,omitempty"`
	PrivacyExecutionMode  runtimepkg.PrivacyExecutionMode `json:"privacy_execution_mode,omitempty"`
	FundedAccounts        []genesisAccountConfig          `json:"funded_accounts"`
	InitialValidators     []genesisValidatorConfig        `json:"initial_validators"`
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

func loadNodeConfig(path string) (nodeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nodeConfig{}, fmt.Errorf("posnode: read config: %w", err)
	}
	config := nodeConfig{}
	if err := json.Unmarshal(data, &config); err != nil {
		return nodeConfig{}, fmt.Errorf("posnode: decode config: %w", err)
	}
	config.ConfigPath = filepath.Clean(strings.TrimSpace(path))
	return normalizeNodeConfig(config)
}

func normalizeNodeConfig(config nodeConfig) (nodeConfig, error) {
	if strings.TrimSpace(config.ChainID) == "" {
		config.ChainID = defaultChainID
	}
	if strings.TrimSpace(config.NodeName) == "" {
		return nodeConfig{}, fmt.Errorf("posnode: node name is empty")
	}
	if strings.TrimSpace(config.DataPath) == "" {
		config.DataPath = "data/posnode-" + strings.TrimSpace(config.NodeName)
	}
	if config.ListenPort < 1 || config.ListenPort > 65535 {
		return nodeConfig{}, fmt.Errorf("posnode: invalid listen port")
	}
	if strings.TrimSpace(config.ListenIP) == "" {
		return nodeConfig{}, fmt.Errorf("posnode: listen ip is empty")
	}
	if strings.TrimSpace(config.AdvertisedIP) != "" && config.AdvertisedPort == 0 {
		config.AdvertisedPort = config.ListenPort
	}
	if config.AdvertisedPort < 0 || config.AdvertisedPort > 65535 {
		return nodeConfig{}, fmt.Errorf("posnode: invalid advertised port")
	}
	if config.RPCPort != 0 || config.RPCEnabled {
		config.RPCEnabled = true
		if config.RPCPort < 1 || config.RPCPort > 65535 {
			return nodeConfig{}, fmt.Errorf("posnode: invalid rpc port")
		}
		if strings.TrimSpace(config.RPCListenIP) == "" {
			config.RPCListenIP = config.ListenIP
		}
	}
	if config.SlotMillis == 0 {
		config.SlotMillis = defaultSlotMillis
	}
	if config.SlotMillis < 200 {
		return nodeConfig{}, fmt.Errorf("posnode: slot millis must be >= 200")
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
		return nodeConfig{}, fmt.Errorf("posnode: finality depth must be 1..1000000")
	}
	if config.TurbineFanout == 0 {
		config.TurbineFanout = consensus.DefaultTurbineFanout
	}
	if config.TurbineFanout < 1 || config.TurbineFanout > consensus.MaxTurbineFanout {
		return nodeConfig{}, fmt.Errorf("posnode: turbine fanout must be 1..1024")
	}
	if config.StakeLamports == 0 {
		config.StakeLamports = stake.MinimumStakeLamports
	}
	if config.StakeLamports < stake.MinimumStakeLamports {
		return nodeConfig{}, fmt.Errorf("posnode: stake lamports below minimum")
	}
	if config.MempoolMaxTransactions == 0 {
		config.MempoolMaxTransactions = 5000
	}
	if config.MempoolMaxTransactions < 1 {
		return nodeConfig{}, fmt.Errorf("posnode: mempool max transactions must be positive")
	}
	if config.MempoolTransactionTTLMillis == 0 {
		config.MempoolTransactionTTLMillis = 60000
	}
	if config.MempoolTransactionTTLMillis < int64(config.SlotMillis) {
		return nodeConfig{}, fmt.Errorf("posnode: mempool ttl must be >= slot millis")
	}
	if config.TransactionLeaderForwardSlots == 0 {
		config.TransactionLeaderForwardSlots = 4
	}
	if config.TransactionLeaderForwardSlots < 0 || config.TransactionLeaderForwardSlots > 64 {
		return nodeConfig{}, fmt.Errorf("posnode: transaction leader forward slots must be 0..64")
	}
	if err := normalizeNodeAttributes(&config); err != nil {
		return nodeConfig{}, err
	}
	if err := validateNodeRoleControls(config); err != nil {
		return nodeConfig{}, err
	}
	if isProductionNodeConfig(config) && config.requiresTreasuryKeyPath() && strings.TrimSpace(config.TreasuryKeyPath) == "" {
		return nodeConfig{}, fmt.Errorf("posnode: production treasury key path is required")
	}
	if isProductionNodeConfig(config) && !config.hasProductionKeyPaths() {
		return nodeConfig{}, fmt.Errorf("posnode: production key paths are required")
	}
	if isProductionNodeConfig(config) && config.allowInsecureP2P() {
		return nodeConfig{}, fmt.Errorf("posnode: insecure p2p disabled in production")
	}
	if err := normalizePrivacyExecutionModeConfig(&config); err != nil {
		return nodeConfig{}, err
	}
	if err := normalizeContractDeploymentPolicyConfig(&config); err != nil {
		return nodeConfig{}, err
	}
	if config.Genesis.InitialSupplyLamports == 0 {
		config.Genesis.InitialSupplyLamports = defaultInitialSupply
	}
	if len(config.Genesis.FundedAccounts) == 0 {
		return nodeConfig{}, fmt.Errorf("posnode: genesis funded accounts are empty")
	}
	if isProductionNodeConfig(config) && !config.hasProductionGenesisPublicKeys() {
		return nodeConfig{}, fmt.Errorf("posnode: production genesis public keys are required")
	}
	if !config.hasNodeKeyMaterial() {
		return nodeConfig{}, fmt.Errorf("posnode: node key material is required")
	}
	if err := normalizeBootstrapPeerAttributes(config.BootstrapPeers); err != nil {
		return nodeConfig{}, err
	}
	return enrichNodeChainIdentity(config)
}

func normalizeContractDeploymentPolicyConfig(config *nodeConfig) error {
	policy := config.ContractDeploymentPolicy
	policy.ResolvedAllowedDeployers = nil
	for index, value := range policy.AllowedDeployers {
		addressText := strings.TrimSpace(value)
		if addressText == "" {
			return fmt.Errorf("posnode: contract deployment allowed_deployers[%d] is empty", index)
		}
		address, err := structure.PublicKeyFromBase58(addressText)
		if err != nil {
			return fmt.Errorf("posnode: contract deployment allowed_deployers[%d]: %w", index, err)
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

func normalizePrivacyExecutionModeConfig(config *nodeConfig) error {
	rootMode, rootErr := runtimepkg.NormalizePrivacyExecutionMode(config.PrivacyExecutionMode)
	genesisMode, genesisErr := runtimepkg.NormalizePrivacyExecutionMode(config.Genesis.PrivacyExecutionMode)
	if config.PrivacyExecutionMode != "" && rootErr != nil {
		return fmt.Errorf("posnode: invalid privacy execution mode: %w", rootErr)
	}
	if config.Genesis.PrivacyExecutionMode != "" && genesisErr != nil {
		return fmt.Errorf("posnode: invalid genesis privacy execution mode: %w", genesisErr)
	}
	if config.PrivacyExecutionMode != "" && config.Genesis.PrivacyExecutionMode != "" && rootMode != genesisMode {
		return fmt.Errorf("posnode: privacy execution mode mismatch root=%s genesis=%s", rootMode, genesisMode)
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

func normalizeNodeAttributes(config *nodeConfig) error {
	role, err := parsePeerRoleConfig(config.NodeRole, p2p.PeerRoleFull)
	if err != nil {
		return fmt.Errorf("posnode: invalid node role: %w", err)
	}
	capabilities, err := parsePeerCapabilitiesConfig(config.NodeCapabilities, role, defaultNodeCapabilities(role))
	if err != nil {
		return fmt.Errorf("posnode: invalid node capabilities: %w", err)
	}
	config.ResolvedNodeRole = role
	config.ResolvedNodeCapabilities = capabilities
	return nil
}

func normalizeBootstrapPeerAttributes(peers []peerConfig) error {
	for index := range peers {
		role, err := parsePeerRoleConfig(peers[index].Role, p2p.PeerRoleValidator)
		if err != nil {
			return fmt.Errorf("posnode: invalid bootstrap peer %s role: %w", peers[index].PeerID, err)
		}
		capabilities, err := parsePeerCapabilitiesConfig(peers[index].Capabilities, role, defaultPeerCapabilities(role))
		if err != nil {
			return fmt.Errorf("posnode: invalid bootstrap peer %s capabilities: %w", peers[index].PeerID, err)
		}
		peers[index].ResolvedRole = role
		peers[index].ResolvedCapabilities = capabilities
	}
	return nil
}

func parsePeerRoleConfig(value string, defaultRole p2p.PeerRole) (p2p.PeerRole, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "":
		return defaultRole, nil
	case "full":
		return p2p.PeerRoleFull, nil
	case "public_rpc", "public-rpc", "rpc", "rpc_gateway", "rpc-gateway":
		return p2p.PeerRolePublicRPC, nil
	case "validator":
		return p2p.PeerRoleValidator, nil
	case "bootnode", "bootstrap":
		return p2p.PeerRoleBootnode, nil
	case "archive":
		return p2p.PeerRoleArchive, nil
	default:
		return p2p.PeerRoleUnknown, fmt.Errorf("unsupported role %q", value)
	}
}

func parsePeerCapabilitiesConfig(
	values []string,
	role p2p.PeerRole,
	defaultCapabilities p2p.PeerCapability,
) (p2p.PeerCapability, error) {
	if len(values) == 0 {
		return defaultCapabilities, nil
	}
	capabilities := p2p.PeerCapability(0)
	for _, value := range values {
		capability, err := parsePeerCapabilityConfig(value)
		if err != nil {
			return 0, err
		}
		capabilities |= capability
	}
	if role == p2p.PeerRoleValidator {
		capabilities |= p2p.PeerCapabilityValidator
	}
	if role == p2p.PeerRoleArchive {
		capabilities |= p2p.PeerCapabilityArchive
	}
	return capabilities, nil
}

func parsePeerCapabilityConfig(value string) (p2p.PeerCapability, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "relay":
		return p2p.PeerCapabilityRelay, nil
	case "archive":
		return p2p.PeerCapabilityArchive, nil
	case "validator":
		return p2p.PeerCapabilityValidator, nil
	case "state_sync", "statesync", "state-sync":
		return p2p.PeerCapabilityStateSync, nil
	case "dht":
		return p2p.PeerCapabilityDHT, nil
	default:
		return 0, fmt.Errorf("unsupported capability %q", value)
	}
}

func defaultNodeCapabilities(role p2p.PeerRole) p2p.PeerCapability {
	switch role {
	case p2p.PeerRoleValidator:
		return p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay | p2p.PeerCapabilityValidator
	case p2p.PeerRolePublicRPC:
		return p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay
	case p2p.PeerRoleBootnode:
		return p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay
	case p2p.PeerRoleArchive:
		return p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay | p2p.PeerCapabilityArchive
	default:
		return p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay
	}
}

func defaultPeerCapabilities(role p2p.PeerRole) p2p.PeerCapability {
	switch role {
	case p2p.PeerRoleBootnode:
		return p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay
	case p2p.PeerRolePublicRPC:
		return p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay
	case p2p.PeerRoleArchive:
		return p2p.PeerCapabilityArchive | p2p.PeerCapabilityRelay
	default:
		return p2p.PeerCapabilityValidator | p2p.PeerCapabilityRelay
	}
}

func (config nodeConfig) slotDuration() time.Duration {
	return time.Duration(config.SlotMillis) * time.Millisecond
}

func (config nodeConfig) genesisStartTime() time.Time {
	return time.UnixMilli(config.GenesisStartMs)
}

func (config nodeConfig) allowInsecureP2P() bool {
	if config.AllowInsecureP2P == nil {
		if isProductionNodeConfig(config) {
			return false
		}
		return true
	}
	return *config.AllowInsecureP2P
}

func (config nodeConfig) publicRPCMode() bool {
	return config.ResolvedNodeRole == p2p.PeerRolePublicRPC
}

func (config nodeConfig) validatorEnabled() bool {
	if config.ValidatorEnabled != nil {
		return *config.ValidatorEnabled
	}
	return !config.publicRPCMode()
}

func (config nodeConfig) consensusEnabled() bool {
	if config.ConsensusEnabled != nil {
		return *config.ConsensusEnabled
	}
	return config.validatorEnabled()
}

func (config nodeConfig) transactionForwardEnabled() bool {
	if config.TransactionForwardEnabled == nil {
		return true
	}
	return *config.TransactionForwardEnabled
}

func (config nodeConfig) forwardTransactionsToValidators() bool {
	if config.TransactionForwardValidators == nil {
		return true
	}
	return *config.TransactionForwardValidators
}

func validateNodeRoleControls(config nodeConfig) error {
	if config.publicRPCMode() && config.validatorEnabled() {
		return fmt.Errorf("posnode: public_rpc role cannot enable validator")
	}
	if config.publicRPCMode() && config.consensusEnabled() {
		return fmt.Errorf("posnode: public_rpc role cannot enable consensus")
	}
	if !config.validatorEnabled() && config.consensusEnabled() {
		return fmt.Errorf("posnode: consensus requires validator_enabled")
	}
	if config.AutoRegister && !config.validatorEnabled() {
		return fmt.Errorf("posnode: auto_register requires validator_enabled")
	}
	if !config.validatorEnabled() && config.ResolvedNodeCapabilities&p2p.PeerCapabilityValidator != 0 {
		return fmt.Errorf("posnode: validator capability requires validator_enabled")
	}
	return nil
}

func (config nodeConfig) allowHardcodedTreasury() bool {
	if isProductionNodeConfig(config) {
		return false
	}
	if config.AllowHardcodedTreasury == nil {
		return true
	}
	return *config.AllowHardcodedTreasury
}

func isProductionNodeConfig(config nodeConfig) bool {
	if config.Production {
		return true
	}
	environment := strings.TrimSpace(strings.ToLower(config.Environment))
	return environment == "production" || environment == "prod"
}

func (config nodeConfig) requiresTreasuryKeyPath() bool {
	return !config.publicRPCMode()
}

func (config nodeConfig) hasProductionKeyPaths() bool {
	if strings.TrimSpace(config.PeerKeyPath) == "" {
		return false
	}
	if !config.validatorEnabled() {
		return true
	}
	return strings.TrimSpace(config.StakerKeyPath) != "" &&
		strings.TrimSpace(config.ValidatorKeyPath) != "" &&
		strings.TrimSpace(config.ConsensusKeyPath) != "" &&
		strings.TrimSpace(config.BLSKeyPath) != ""
}

func (config nodeConfig) hasNodeKeyMaterial() bool {
	if strings.TrimSpace(config.PeerSeed) == "" && strings.TrimSpace(config.PeerKeyPath) == "" {
		return false
	}
	if !config.validatorEnabled() {
		return true
	}
	if strings.TrimSpace(config.StakerSeed) == "" && strings.TrimSpace(config.StakerKeyPath) == "" {
		return false
	}
	if strings.TrimSpace(config.ValidatorSeed) == "" && strings.TrimSpace(config.ValidatorKeyPath) == "" {
		return false
	}
	if strings.TrimSpace(config.ConsensusSeed) == "" && strings.TrimSpace(config.ConsensusKeyPath) == "" {
		return false
	}
	if strings.TrimSpace(config.ConsensusSeed) == "" && strings.TrimSpace(config.BLSKeyPath) == "" {
		return false
	}
	return true
}

func (config nodeConfig) hasProductionGenesisPublicKeys() bool {
	if strings.TrimSpace(config.Genesis.TreasuryAddress) == "" {
		return false
	}
	for _, account := range config.Genesis.FundedAccounts {
		if strings.TrimSpace(account.Address) == "" || strings.TrimSpace(account.Seed) != "" {
			return false
		}
	}
	for _, validator := range config.Genesis.InitialValidators {
		if strings.TrimSpace(validator.StakerAddress) == "" ||
			strings.TrimSpace(validator.ValidatorAddress) == "" ||
			strings.TrimSpace(validator.ConsensusPublicKey) == "" ||
			strings.TrimSpace(validator.BLSPublicKeyBase64) == "" {
			return false
		}
		if strings.TrimSpace(validator.StakerSeed) != "" ||
			strings.TrimSpace(validator.ValidatorSeed) != "" ||
			strings.TrimSpace(validator.ConsensusSeed) != "" {
			return false
		}
	}
	return true
}
