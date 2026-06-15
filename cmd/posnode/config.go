package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

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
	ChainID        string              `json:"chain_id"`
	NodeName       string              `json:"node_name"`
	ListenIP       string              `json:"listen_ip"`
	ListenPort     int                 `json:"listen_port"`
	PeerSeed       string              `json:"peer_seed"`
	StakerSeed     string              `json:"staker_seed"`
	ValidatorSeed  string              `json:"validator_seed"`
	ConsensusSeed  string              `json:"consensus_seed"`
	StakeLamports  uint64              `json:"stake_lamports"`
	BootstrapPeers []peerConfig        `json:"bootstrap_peers"`
	Genesis        genesisConfig       `json:"genesis"`
	SlotMillis     int                 `json:"slot_millis"`
	EpochSlots     uint64              `json:"epoch_slots"`
	AutoRegister   bool                `json:"auto_register"`
	AutoTransfer   *autoTransferConfig `json:"auto_transfer,omitempty"`
}

type peerConfig struct {
	PeerID  string `json:"peer_id"`
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Network string `json:"network"`
}

type genesisConfig struct {
	InitialSupplyLamports uint64                   `json:"initial_supply_lamports"`
	FundedAccounts        []genesisAccountConfig   `json:"funded_accounts"`
	InitialValidators     []genesisValidatorConfig `json:"initial_validators"`
}

type genesisAccountConfig struct {
	Seed     string `json:"seed"`
	Lamports uint64 `json:"lamports"`
}

type genesisValidatorConfig struct {
	StakerSeed    string `json:"staker_seed"`
	ValidatorSeed string `json:"validator_seed"`
	ConsensusSeed string `json:"consensus_seed"`
	PeerID        string `json:"peer_id"`
	StakeLamports uint64 `json:"stake_lamports"`
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
	if config.ListenPort < 1 || config.ListenPort > 65535 {
		return nodeConfig{}, fmt.Errorf("posnode: invalid listen port")
	}
	if strings.TrimSpace(config.ListenIP) == "" {
		return nodeConfig{}, fmt.Errorf("posnode: listen ip is empty")
	}
	if config.SlotMillis == 0 {
		config.SlotMillis = defaultSlotMillis
	}
	if config.SlotMillis < 200 {
		return nodeConfig{}, fmt.Errorf("posnode: slot millis must be >= 200")
	}
	if config.EpochSlots == 0 {
		config.EpochSlots = defaultEpochSlots
	}
	if config.StakeLamports == 0 {
		config.StakeLamports = stake.MinimumStakeLamports
	}
	if config.StakeLamports < stake.MinimumStakeLamports {
		return nodeConfig{}, fmt.Errorf("posnode: stake lamports below minimum")
	}
	if config.Genesis.InitialSupplyLamports == 0 {
		config.Genesis.InitialSupplyLamports = defaultInitialSupply
	}
	if len(config.Genesis.FundedAccounts) == 0 {
		return nodeConfig{}, fmt.Errorf("posnode: genesis funded accounts are empty")
	}
	if config.PeerSeed == "" || config.StakerSeed == "" || config.ValidatorSeed == "" || config.ConsensusSeed == "" {
		return nodeConfig{}, fmt.Errorf("posnode: node seeds are required")
	}
	return config, nil
}

func (config nodeConfig) slotDuration() time.Duration {
	return time.Duration(config.SlotMillis) * time.Millisecond
}
