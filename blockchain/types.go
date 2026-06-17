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
	TreasuryAddress       structure.PublicKey
	FundedAccounts        []GenesisAccount
	InitialValidators     []GenesisValidator
}

// LedgerConfig 描述账本运行参数 + 让 finality 和启动恢复由节点配置显式控制。
type LedgerConfig struct {
	FinalityDepth        uint64
	DisableStateRecovery bool
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
	BLSPublicKey       []byte
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

// ImportSnapshotRequest 描述可信 finalized 快照导入 + 新节点 state sync 用它快速建立本地 root。
type ImportSnapshotRequest struct {
	Proposal consensus.BlockProposal
	State    consensus.ChainState
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

// BlockLocatorEntry 描述主链定位点 + 节点间先定位共同祖先再补齐分叉历史。
type BlockLocatorEntry struct {
	Height    uint64
	BlockHash structure.Hash
}

type storedProposal struct {
	Header          consensus.BlockHeader         `json:"header"`
	Transactions    []string                      `json:"transactions"`
	RewardQCs       []consensus.QuorumCertificate `json:"reward_qcs,omitempty"`
	Evidence        []consensus.SlashingEvidence  `json:"evidence,omitempty"`
	Rewards         []consensus.BlockReward       `json:"rewards,omitempty"`
	LeaderSignature string                        `json:"leader_signature"`
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
