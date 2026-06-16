package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"solana_golang/blockchain"
	"solana_golang/structure"
	"solana_golang/utils"
)

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
}

func enrichNodeChainIdentity(config nodeConfig) (nodeConfig, error) {
	genesisConfig, err := buildBlockchainGenesisConfig(config)
	if err != nil {
		return nodeConfig{}, err
	}
	_, head, err := blockchain.BuildGenesisState(genesisConfig)
	if err != nil {
		return nodeConfig{}, fmt.Errorf("posnode: build chain identity genesis: %w", err)
	}
	identityPayload := chainIdentityPayload{
		ChainID:                       config.ChainID,
		GenesisHash:                   head.BlockHash.String(),
		GenesisStartMs:                config.GenesisStartMs,
		SlotMillis:                    config.SlotMillis,
		EpochSlots:                    config.EpochSlots,
		FinalityDepth:                 config.FinalityDepth,
		TurbineFanout:                 config.TurbineFanout,
		TransactionLeaderForwardSlots: config.TransactionLeaderForwardSlots,
		TransactionForwardValidators:  config.forwardTransactionsToValidators(),
	}
	identityBytes, err := json.Marshal(identityPayload)
	if err != nil {
		return nodeConfig{}, fmt.Errorf("posnode: marshal chain identity: %w", err)
	}
	identityHash, err := structure.NewHash(utils.SHA256(identityBytes))
	if err != nil {
		return nodeConfig{}, fmt.Errorf("posnode: hash chain identity: %w", err)
	}
	dataRootPath := strings.TrimSpace(config.DataRootPath)
	if dataRootPath == "" {
		dataRootPath = strings.TrimSpace(config.DataPath)
	}
	config.DataRootPath = filepath.Clean(dataRootPath)
	config.DataPath = resolveChainDataPath(config.DataRootPath, identityHash.String())
	config.GenesisHash = identityPayload.GenesisHash
	config.ChainIdentityHash = identityHash.String()
	config.P2PNetworkID = identityHash.String()
	return config, nil
}

func resolveChainDataPath(dataRootPath string, chainIdentityHash string) string {
	return filepath.Join(filepath.Clean(dataRootPath), "chains", chainIdentityHash)
}

func buildBlockchainGenesisConfig(config nodeConfig) (blockchain.GenesisConfig, error) {
	genesis := blockchain.GenesisConfig{
		ChainID:               config.ChainID,
		InitialSupplyLamports: config.Genesis.InitialSupplyLamports,
		FundedAccounts:        make([]blockchain.GenesisAccount, 0, len(config.Genesis.FundedAccounts)),
		InitialValidators:     make([]blockchain.GenesisValidator, 0, len(config.Genesis.InitialValidators)),
	}
	if config.Genesis.TreasuryAddress != "" {
		treasuryAddress, err := structure.PublicKeyFromBase58(config.Genesis.TreasuryAddress)
		if err != nil {
			return blockchain.GenesisConfig{}, fmt.Errorf("posnode: decode genesis treasury address: %w", err)
		}
		genesis.TreasuryAddress = treasuryAddress
	}
	for _, account := range config.Genesis.FundedAccounts {
		address, err := genesisPublicKeyFromAddressOrSeed(account.Address, account.Seed, "funded account")
		if err != nil {
			return blockchain.GenesisConfig{}, err
		}
		genesis.FundedAccounts = append(genesis.FundedAccounts, blockchain.GenesisAccount{
			Address:  address,
			Lamports: account.Lamports,
		})
	}
	for _, validator := range config.Genesis.InitialValidators {
		stakerAddress, err := genesisPublicKeyFromAddressOrSeed(validator.StakerAddress, validator.StakerSeed, "validator staker")
		if err != nil {
			return blockchain.GenesisConfig{}, err
		}
		validatorAddress, err := genesisPublicKeyFromAddressOrSeed(validator.ValidatorAddress, validator.ValidatorSeed, "validator account")
		if err != nil {
			return blockchain.GenesisConfig{}, err
		}
		consensusPublicKey, err := genesisPublicKeyFromAddressOrSeed(validator.ConsensusPublicKey, validator.ConsensusSeed, "validator consensus")
		if err != nil {
			return blockchain.GenesisConfig{}, err
		}
		blsPublicKey, err := genesisBLSPublicKey(validator.BLSPublicKeyBase64, validator.ConsensusSeed)
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
