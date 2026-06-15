package blockchain

import (
	"encoding/base64"

	"solana_golang/consensus"
	"solana_golang/structure"
)

// GenesisConfig 描述创世账本 + 初始化资金账户和启动验证者。
type GenesisConfig struct {
	ChainID               string
	InitialSupplyLamports uint64
	FundedAccounts        []GenesisAccount
	InitialValidators     []GenesisValidator
}

// GenesisAccount 描述创世普通账户 + 用于冷启动分配初始余额。
type GenesisAccount struct {
	Address  structure.PublicKey
	Lamports uint64
}

// GenesisValidator 描述创世验证者 + 用于解决空链无法选第一个 leader 的冷启动问题。
type GenesisValidator struct {
	StakerAddress      structure.PublicKey
	ValidatorAddress   structure.PublicKey
	ConsensusPublicKey structure.PublicKey
	P2PPeerID          string
	StakeLamports      uint64
	CommissionBps      uint16
}

// Head 描述主链头 + 节点重启和出块路径从这里恢复高度和状态根。
type Head struct {
	ChainID         string
	Height          uint64
	Slot            uint64
	BlockHash       structure.Hash
	QCHash          structure.Hash
	StateRoot       structure.Hash
	EpochID         uint64
	FinalizedHeight uint64
	FinalizedHash   structure.Hash
	UpdatedAtMs     int64
}

// CommitBlockRequest 描述区块提交输入 + 共识验块后由 blockchain 原子落账。
type CommitBlockRequest struct {
	Proposal  consensus.BlockProposal
	NextState consensus.ChainState
	QC        *consensus.QuorumCertificate
}

// ForkDecision 描述分叉裁决结果 + 用于审计重组是否发生。
type ForkDecision struct {
	Accepted       bool
	Reorganized    bool
	CommonAncestor Head
	OldBlocks      []structure.Hash
	NewBlocks      []structure.Hash
	Reason         string
}

type storedProposal struct {
	Header          consensus.BlockHeader `json:"header"`
	Transactions    []string              `json:"transactions"`
	LeaderSignature string                `json:"leader_signature"`
}

type storedHead struct {
	ChainID         string `json:"chain_id"`
	Height          uint64 `json:"height"`
	Slot            uint64 `json:"slot"`
	BlockHash       string `json:"block_hash"`
	QCHash          string `json:"qc_hash"`
	StateRoot       string `json:"state_root"`
	EpochID         uint64 `json:"epoch_id"`
	FinalizedHeight uint64 `json:"finalized_height"`
	FinalizedHash   string `json:"finalized_hash"`
	UpdatedAtMs     int64  `json:"updated_at_ms"`
}

func encodeBytes(value []byte) string {
	return base64.StdEncoding.EncodeToString(value)
}

func decodeBytes(value string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(value)
}
