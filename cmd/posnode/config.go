package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/programs/stake"
)

const (
	defaultChainID       = "pos-localnet"
	defaultSlotMillis    = 1000
	defaultEpochSlots    = 8
	defaultInitialSupply = uint64(1_000_000_000_000_000_000)
)

// nodeConfig 描述 posnode 配置 + 用同一 genesis 文件保证多节点状态一致。
type nodeConfig struct {
	ChainID                       string              `json:"chain_id"`
	Environment                   string              `json:"environment"`
	Production                    bool                `json:"production"`
	NodeName                      string              `json:"node_name"`
	DataPath                      string              `json:"data_path"`
	ListenIP                      string              `json:"listen_ip"`
	ListenPort                    int                 `json:"listen_port"`
	RPCEnabled                    bool                `json:"rpc_enabled"`
	RPCListenIP                   string              `json:"rpc_listen_ip"`
	RPCPort                       int                 `json:"rpc_port"`
	AllowInsecureP2P              *bool               `json:"allow_insecure_p2p,omitempty"`
	PeerSeed                      string              `json:"peer_seed"`
	StakerSeed                    string              `json:"staker_seed"`
	ValidatorSeed                 string              `json:"validator_seed"`
	ConsensusSeed                 string              `json:"consensus_seed"`
	PeerKeyPath                   string              `json:"peer_key_path,omitempty"`
	StakerKeyPath                 string              `json:"staker_key_path,omitempty"`
	ValidatorKeyPath              string              `json:"validator_key_path,omitempty"`
	ConsensusKeyPath              string              `json:"consensus_key_path,omitempty"`
	BLSKeyPath                    string              `json:"bls_key_path,omitempty"`
	StakeLamports                 uint64              `json:"stake_lamports"`
	BootstrapPeers                []peerConfig        `json:"bootstrap_peers"`
	Genesis                       genesisConfig       `json:"genesis"`
	SlotMillis                    int                 `json:"slot_millis"`
	GenesisStartMs                int64               `json:"genesis_start_unix_millis"`
	EpochSlots                    uint64              `json:"epoch_slots"`
	FinalityDepth                 uint64              `json:"finality_depth"`
	DisableStateRecovery          bool                `json:"disable_state_recovery,omitempty"`
	TurbineFanout                 int                 `json:"turbine_fanout"`
	AutoRegister                  bool                `json:"auto_register"`
	MempoolMaxTransactions        int                 `json:"mempool_max_transactions"`
	MempoolTransactionTTLMillis   int64               `json:"mempool_transaction_ttl_millis"`
	TransactionLeaderForwardSlots int                 `json:"transaction_leader_forward_slots"`
	TransactionForwardValidators  *bool               `json:"transaction_forward_validators,omitempty"`
	TreasuryKeyPath               string              `json:"treasury_key_path,omitempty"`
	AllowHardcodedTreasury        *bool               `json:"allow_hardcoded_treasury,omitempty"`
	AutoTransfer                  *autoTransferConfig `json:"auto_transfer,omitempty"`
}

type peerConfig struct {
	PeerID  string `json:"peer_id"`
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Network string `json:"network"`
}

type genesisConfig struct {
	InitialSupplyLamports uint64                   `json:"initial_supply_lamports"`
	TreasuryAddress       string                   `json:"treasury_address,omitempty"`
	FundedAccounts        []genesisAccountConfig   `json:"funded_accounts"`
	InitialValidators     []genesisValidatorConfig `json:"initial_validators"`
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

func loadNodeConfig(path string) (nodeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nodeConfig{}, fmt.Errorf("posnode: read config: %w", err)
	}
	config := nodeConfig{}
	if err := json.Unmarshal(data, &config); err != nil {
		return nodeConfig{}, fmt.Errorf("posnode: decode config: %w", err)
	}
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
	if isProductionNodeConfig(config) && strings.TrimSpace(config.TreasuryKeyPath) == "" {
		return nodeConfig{}, fmt.Errorf("posnode: production treasury key path is required")
	}
	if isProductionNodeConfig(config) && !config.hasProductionKeyPaths() {
		return nodeConfig{}, fmt.Errorf("posnode: production key paths are required")
	}
	if isProductionNodeConfig(config) && config.allowInsecureP2P() {
		return nodeConfig{}, fmt.Errorf("posnode: insecure p2p disabled in production")
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
	return config, nil
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

func (config nodeConfig) forwardTransactionsToValidators() bool {
	if config.TransactionForwardValidators == nil {
		return true
	}
	return *config.TransactionForwardValidators
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

func (config nodeConfig) hasProductionKeyPaths() bool {
	return strings.TrimSpace(config.PeerKeyPath) != "" &&
		strings.TrimSpace(config.StakerKeyPath) != "" &&
		strings.TrimSpace(config.ValidatorKeyPath) != "" &&
		strings.TrimSpace(config.ConsensusKeyPath) != "" &&
		strings.TrimSpace(config.BLSKeyPath) != ""
}

func (config nodeConfig) hasNodeKeyMaterial() bool {
	if strings.TrimSpace(config.PeerSeed) == "" && strings.TrimSpace(config.PeerKeyPath) == "" {
		return false
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
