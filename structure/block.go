package structure

import (
	"fmt"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
)

const (
	MaxTransactionsPerBlock = 128 * 1024
)

type BlockStatus int16

const (
	BlockStatusProposed BlockStatus = iota
	BlockStatusConfirmed
	BlockStatusFinalized
	BlockStatusSkipped
)

// BlockHeader 描述区块头 + 为共识验证、账本索引和状态追踪提供稳定字段。
type BlockHeader struct {
	Version           uint16
	Slot              uint64
	ParentSlot        uint64
	BlockHeight       uint64
	ParentHash        Hash
	PreviousBlockhash Hash
	Blockhash         Hash
	TransactionsRoot  Hash
	AccountsHash      Hash
	StateRoot         Hash
	RewardsHash       Hash
	EntriesHash       Hash
	TimestampUnix     int64
	Leader            PublicKey
	TransactionCount  uint32
}

// BlockMeta 描述区块运行时元数据 + 避免把索引和观测字段混入共识哈希。
type BlockMeta struct {
	Status            BlockStatus
	TotalFees         uint64
	ComputeUnitsUsed  uint64
	ReceivedTimeUnix  int64
	FinalizedTimeUnix int64
}

// TransactionStatusMeta 描述交易执行元数据 + 保持区块交易和执行结果分层。
type TransactionStatusMeta struct {
	Status               TransactionStatus `json:"status"`
	Err                  string            `json:"err,omitempty"`
	Fee                  uint64            `json:"fee"`
	ComputeUnitsConsumed uint64            `json:"computeUnitsConsumed,omitempty"`
	PreBalances          []uint64          `json:"preBalances,omitempty"`
	PostBalances         []uint64          `json:"postBalances,omitempty"`
	LogMessages          []string          `json:"logMessages,omitempty"`
}

// TransactionWithStatusMeta 描述带执行结果的交易 + 用于区块查询视图返回完整交易状态。
type TransactionWithStatusMeta struct {
	Transaction Transaction           `json:"transaction"`
	Meta        TransactionStatusMeta `json:"meta"`
}

// BlockReward 描述区块奖励 + 用于区块查询视图展示奖励明细。
type BlockReward struct {
	Pubkey      string `json:"pubkey"`
	Lamports    int64  `json:"lamports"`
	PostBalance uint64 `json:"postBalance"`
	RewardType  string `json:"rewardType,omitempty"`
	Commission  *uint8 `json:"commission,omitempty"`
}

// ConfirmedBlockView 描述已确认区块查询视图 + 用于 RPC 和跨节点数据展示。
type ConfirmedBlockView struct {
	PreviousBlockhash   string                      `json:"previousBlockhash"`
	Blockhash           string                      `json:"blockhash"`
	ParentSlot          uint64                      `json:"parentSlot"`
	Transactions        []TransactionWithStatusMeta `json:"transactions,omitempty"`
	Signatures          []string                    `json:"signatures,omitempty"`
	Rewards             []BlockReward               `json:"rewards,omitempty"`
	NumRewardPartitions *uint64                     `json:"numRewardPartitions,omitempty"`
	BlockTime           *int64                      `json:"blockTime,omitempty"`
	BlockHeight         *uint64                     `json:"blockHeight,omitempty"`
}

// Block 描述完整区块 + 分离共识头、交易体和运行时元数据。
type Block struct {
	Header       BlockHeader
	Transactions []Transaction
	Meta         BlockMeta
}

// Validate 校验区块结构 + 在共识处理前拦截非法区块输入。
func (block Block) Validate() error {
	if err := block.Header.Validate(); err != nil {
		return fmt.Errorf("structure: validate block header: %w", err)
	}
	if len(block.Transactions) > MaxTransactionsPerBlock {
		return fmt.Errorf("%w: got %d, max %d", ErrTooManyTransactions, len(block.Transactions), MaxTransactionsPerBlock)
	}
	if err := block.validateTransactionCount(); err != nil {
		return err
	}
	for transactionIndex, transaction := range block.Transactions {
		if err := transaction.Validate(); err != nil {
			return fmt.Errorf("structure: transaction %d: %w", transactionIndex, err)
		}
	}
	return block.validateTransactionsRoot()
}

// Hash 计算区块哈希 + 使用区块头确定性序列化作为哈希输入。
func (block Block) Hash() (Hash, error) {
	return block.Header.Hash()
}

// ToConfirmedBlockView 转换已确认区块查询视图 + 保持内部共识字段和外部 RPC 结构分离。
func (block Block) ToConfirmedBlockView() (ConfirmedBlockView, error) {
	if err := block.Validate(); err != nil {
		return ConfirmedBlockView{}, err
	}

	blockTime := block.Header.TimestampUnix
	blockHeight := block.Header.BlockHeight
	transactions := make([]TransactionWithStatusMeta, len(block.Transactions))
	signatures := make([]string, len(block.Transactions))
	for transactionIndex, transaction := range block.Transactions {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			return ConfirmedBlockView{}, fmt.Errorf("structure: transaction %d id: %w", transactionIndex, err)
		}
		signatures[transactionIndex] = transactionID
		transactions[transactionIndex] = TransactionWithStatusMeta{
			Transaction: transaction.Clone(),
			Meta: TransactionStatusMeta{
				Status: transaction.Status,
				Fee:    transaction.Fee,
			},
		}
	}

	return ConfirmedBlockView{
		PreviousBlockhash: block.Header.PreviousBlockhash.String(),
		Blockhash:         block.Header.Blockhash.String(),
		ParentSlot:        block.Header.ParentSlot,
		Transactions:      transactions,
		Signatures:        signatures,
		Rewards:           []BlockReward{},
		BlockTime:         &blockTime,
		BlockHeight:       &blockHeight,
	}, nil
}

// MarshalBinary 序列化完整区块 + 为网络传输和持久化提供确定性格式。
func (block Block) MarshalBinary() ([]byte, error) {
	if err := block.Validate(); err != nil {
		return nil, err
	}

	encoded, err := block.Header.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("structure: marshal block header: %w", err)
	}
	if err := appendShortVecLength(&encoded, len(block.Transactions)); err != nil {
		return nil, fmt.Errorf("structure: encode transactions length: %w", err)
	}
	for transactionIndex, transaction := range block.Transactions {
		transactionBytes, err := transaction.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("structure: marshal transaction %d: %w", transactionIndex, err)
		}
		encoded = append(encoded, transactionBytes...)
	}
	return encoded, nil
}

// Validate 校验区块头 + 保证高度、哈希、时间和出块者字段合法。
func (header BlockHeader) Validate() error {
	if header.Blockhash == (Hash{}) {
		return ErrEmptyBlockhash
	}
	if header.Leader.IsZero() {
		return fmt.Errorf("%w: leader cannot be empty", ErrInvalidBlockHeader)
	}
	if header.Slot > 0 && header.ParentSlot >= header.Slot {
		return fmt.Errorf("%w: parent slot must be less than slot", ErrInvalidBlockHeader)
	}
	if header.TimestampUnix <= 0 {
		return fmt.Errorf("%w: timestamp must be positive", ErrInvalidBlockHeader)
	}
	if header.TransactionCount > MaxTransactionsPerBlock {
		return fmt.Errorf("%w: transaction count exceeds max block limit", ErrInvalidBlockHeader)
	}
	return nil
}

// Hash 计算区块头哈希 + 使用 SHA-256 保持依赖简单且性能稳定。
func (header BlockHeader) Hash() (Hash, error) {
	encoded, err := header.MarshalBinary()
	if err != nil {
		return Hash{}, fmt.Errorf("structure: marshal block header before hash: %w", err)
	}
	return NewHash(utils.SHA256(encoded))
}

// MarshalBinary 序列化区块头 + 使用 Borsh 固定字段顺序保证确定性字节。
func (header BlockHeader) MarshalBinary() ([]byte, error) {
	if err := header.Validate(); err != nil {
		return nil, err
	}

	writer := borsh.NewWriter(PublicKeySize)
	writer.WriteUint16(header.Version)
	writer.WriteUint64(header.Slot)
	writer.WriteUint64(header.ParentSlot)
	writer.WriteUint64(header.BlockHeight)
	writer.WriteFixedBytes(header.ParentHash[:])
	writer.WriteFixedBytes(header.PreviousBlockhash[:])
	writer.WriteFixedBytes(header.Blockhash[:])
	writer.WriteFixedBytes(header.TransactionsRoot[:])
	writer.WriteFixedBytes(header.AccountsHash[:])
	writer.WriteFixedBytes(header.StateRoot[:])
	writer.WriteFixedBytes(header.RewardsHash[:])
	writer.WriteFixedBytes(header.EntriesHash[:])
	writer.WriteInt64(header.TimestampUnix)
	writer.WriteFixedBytes(header.Leader[:])
	writer.WriteUint32(header.TransactionCount)
	return writer.Bytes(), nil
}

// ComputeTransactionsRoot 计算交易根 + 使用二叉 Merkle 树压缩交易哈希。
func (block Block) ComputeTransactionsRoot() (Hash, error) {
	transactionHashes := make([]Hash, len(block.Transactions))
	for transactionIndex, transaction := range block.Transactions {
		transactionHash, err := transaction.Hash()
		if err != nil {
			return Hash{}, fmt.Errorf("structure: hash transaction %d: %w", transactionIndex, err)
		}
		transactionHashes[transactionIndex] = transactionHash
	}
	return merkleRoot(transactionHashes)
}

// VerifyTransactionsRoot 校验交易根 + 防止区块头与交易列表不一致。
func (block Block) VerifyTransactionsRoot() error {
	return block.validateTransactionsRoot()
}

// validateTransactionCount 校验交易数量一致性 + 允许空区块使用零计数。
func (block Block) validateTransactionCount() error {
	if block.Header.TransactionCount == 0 && len(block.Transactions) == 0 {
		return nil
	}
	if int(block.Header.TransactionCount) != len(block.Transactions) {
		return fmt.Errorf("%w: header transaction count %d does not match body %d", ErrInvalidBlockHeader, block.Header.TransactionCount, len(block.Transactions))
	}
	return nil
}

// validateTransactionsRoot 校验交易 Merkle 根 + 防止区块头与交易体不一致。
func (block Block) validateTransactionsRoot() error {
	computedRoot, err := block.ComputeTransactionsRoot()
	if err != nil {
		return fmt.Errorf("structure: compute transactions root: %w", err)
	}
	if block.Header.TransactionsRoot != computedRoot {
		return fmt.Errorf("%w: transactions root mismatch", ErrInvalidBlockHeader)
	}
	return nil
}

// merkleRoot 计算 Merkle 根 + 空交易列表返回确定性零哈希。
func merkleRoot(hashes []Hash) (Hash, error) {
	if len(hashes) == 0 {
		return NewHash(make([]byte, PublicKeySize))
	}
	currentLevel := cloneHashes(hashes)
	for len(currentLevel) > 1 {
		nextLevel, err := merkleParentLevel(currentLevel)
		if err != nil {
			return Hash{}, fmt.Errorf("structure: build merkle parent level: %w", err)
		}
		currentLevel = nextLevel
	}
	return currentLevel[0], nil
}

// merkleParentLevel 构造上一层 Merkle 节点 + 奇数节点复制自身配对。
func merkleParentLevel(currentLevel []Hash) ([]Hash, error) {
	nextLevel := make([]Hash, 0, (len(currentLevel)+1)/2)
	for index := 0; index < len(currentLevel); index += 2 {
		left := currentLevel[index]
		right := left
		if index+1 < len(currentLevel) {
			right = currentLevel[index+1]
		}
		pairHash, err := hashPair(left, right)
		if err != nil {
			return nil, fmt.Errorf("structure: hash merkle pair %d: %w", index/2, err)
		}
		nextLevel = append(nextLevel, pairHash)
	}
	return nextLevel, nil
}

// hashPair 计算两个子节点父哈希 + 按左右顺序拼接保持确定性。
func hashPair(left Hash, right Hash) (Hash, error) {
	hash, err := NewHash(utils.SHA256(utils.ConcatBytes(left[:], right[:])))
	if err != nil {
		return Hash{}, err
	}
	return hash, nil
}

// cloneHashes 复制哈希切片 + 避免 Merkle 计算修改调用方输入。
func cloneHashes(value []Hash) []Hash {
	cloned := make([]Hash, len(value))
	copy(cloned, value)
	return cloned
}
