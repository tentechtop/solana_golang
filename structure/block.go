package structure

import (
	"fmt"

	"solana_golang/utils"
)

const (
	MaxTransactionsPerBlock = 128 * 1024
)

// BlockHeader 描述区块头核心元数据 + 为共识验证和索引查询提供稳定字段。
type BlockHeader struct {
	Slot             uint64
	ParentSlot       uint64
	ParentHash       utils.Hash
	Blockhash        utils.Hash
	TransactionsRoot utils.Hash
	StateRoot        utils.Hash
	TimestampUnix    int64
	Leader           utils.PublicKey
}

// Block 描述完整区块 + 将区块头和交易列表分离便于轻量验证。
type Block struct {
	Header       BlockHeader
	Transactions []Transaction
}

// Validate 校验区块结构 + 在共识处理前拦截非法区块输入。
func (block Block) Validate() error {
	if err := block.Header.Validate(); err != nil {
		return fmt.Errorf("structure: validate block header: %w", err)
	}
	if len(block.Transactions) > MaxTransactionsPerBlock {
		return fmt.Errorf("%w: got %d, max %d", ErrTooManyTransactions, len(block.Transactions), MaxTransactionsPerBlock)
	}
	for transactionIndex, transaction := range block.Transactions {
		if err := transaction.Validate(); err != nil {
			return fmt.Errorf("structure: transaction %d: %w", transactionIndex, err)
		}
	}
	return nil
}

// Hash 计算区块哈希 + 使用区块头确定性序列化作为哈希输入。
func (block Block) Hash() (utils.Hash, error) {
	return block.Header.Hash()
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

// Validate 校验区块头 + 保证高度、哈希和出块者字段合法。
func (header BlockHeader) Validate() error {
	if header.Blockhash == (utils.Hash{}) {
		return ErrEmptyBlockhash
	}
	if header.Leader.IsZero() {
		return fmt.Errorf("%w: leader cannot be empty", ErrInvalidBlockHeader)
	}
	if header.Slot > 0 && header.ParentSlot >= header.Slot {
		return fmt.Errorf("%w: parent slot must be less than slot", ErrInvalidBlockHeader)
	}
	return nil
}

// Hash 计算区块头哈希 + 使用 SHA-256 保持依赖简单且性能稳定。
func (header BlockHeader) Hash() (utils.Hash, error) {
	encoded, err := header.MarshalBinary()
	if err != nil {
		return utils.Hash{}, fmt.Errorf("structure: marshal block header before hash: %w", err)
	}
	return utils.NewHash(utils.SHA256(encoded))
}

// MarshalBinary 序列化区块头 + 使用小端整数兼容 Solana 数据习惯。
func (header BlockHeader) MarshalBinary() ([]byte, error) {
	if err := header.Validate(); err != nil {
		return nil, err
	}

	encoded := make([]byte, 0, 8+8+utils.PublicKeySize*5+8)
	encoded = append(encoded, utils.Uint64ToBytesLE(header.Slot)...)
	encoded = append(encoded, utils.Uint64ToBytesLE(header.ParentSlot)...)
	encoded = append(encoded, header.ParentHash[:]...)
	encoded = append(encoded, header.Blockhash[:]...)
	encoded = append(encoded, header.TransactionsRoot[:]...)
	encoded = append(encoded, header.StateRoot[:]...)
	encoded = append(encoded, utils.Int64ToBytesLE(header.TimestampUnix)...)
	encoded = append(encoded, header.Leader[:]...)
	return encoded, nil
}

// ComputeTransactionsRoot 计算交易根 + 使用二叉 Merkle 树压缩交易哈希。
func (block Block) ComputeTransactionsRoot() (utils.Hash, error) {
	transactionHashes := make([]utils.Hash, len(block.Transactions))
	for transactionIndex, transaction := range block.Transactions {
		transactionHash, err := transaction.Hash()
		if err != nil {
			return utils.Hash{}, fmt.Errorf("structure: hash transaction %d: %w", transactionIndex, err)
		}
		transactionHashes[transactionIndex] = transactionHash
	}
	return merkleRoot(transactionHashes)
}

func merkleRoot(hashes []utils.Hash) (utils.Hash, error) {
	if len(hashes) == 0 {
		return utils.NewHash(make([]byte, utils.PublicKeySize))
	}
	currentLevel := cloneHashes(hashes)
	for len(currentLevel) > 1 {
		nextLevel, err := merkleParentLevel(currentLevel)
		if err != nil {
			return utils.Hash{}, fmt.Errorf("structure: build merkle parent level: %w", err)
		}
		currentLevel = nextLevel
	}
	return currentLevel[0], nil
}

func merkleParentLevel(currentLevel []utils.Hash) ([]utils.Hash, error) {
	nextLevel := make([]utils.Hash, 0, (len(currentLevel)+1)/2)
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

func hashPair(left utils.Hash, right utils.Hash) (utils.Hash, error) {
	hash, err := utils.NewHash(utils.SHA256(utils.ConcatBytes(left[:], right[:])))
	if err != nil {
		return utils.Hash{}, err
	}
	return hash, nil
}

func cloneHashes(value []utils.Hash) []utils.Hash {
	cloned := make([]utils.Hash, len(value))
	copy(cloned, value)
	return cloned
}
