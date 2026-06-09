package structure

import (
	"fmt"

	"solana_golang/utils"
)

const (
	MaxAccountKeysPerTransaction  = 256
	MaxInstructionsPerTransaction = 1024
	MaxInstructionAccounts        = 256
	MaxInstructionDataSize        = 64 * 1024
)

// MessageHeader 描述交易账户权限布局 + 避免每条指令重复存储账户权限。
type MessageHeader struct {
	NumRequiredSignatures       uint8
	NumReadonlySignedAccounts   uint8
	NumReadonlyUnsignedAccounts uint8
}

// CompiledInstruction 描述已编译指令 + 使用账户索引减少链上交易体积。
type CompiledInstruction struct {
	ProgramIDIndex uint16
	AccountIndexes []uint16
	Data           []byte
}

// Message 描述可签名交易消息 + 保持签名数据与签名本身解耦。
type Message struct {
	Header          MessageHeader
	AccountKeys     []utils.PublicKey
	RecentBlockhash utils.Blockhash
	Instructions    []CompiledInstruction
}

// Transaction 描述链上交易 + 使用固定长度签名保证校验成本稳定。
type Transaction struct {
	Signatures []utils.Signature
	Message    Message
}

// Validate 校验交易结构 + 在进入执行层前拦截非法输入。
func (transaction Transaction) Validate() error {
	if len(transaction.Signatures) == 0 {
		return ErrEmptyTransactionSignatures
	}
	if err := transaction.Message.Validate(); err != nil {
		return fmt.Errorf("structure: validate transaction message: %w", err)
	}
	requiredSignatures := int(transaction.Message.Header.NumRequiredSignatures)
	if len(transaction.Signatures) != requiredSignatures {
		return fmt.Errorf("structure: signatures count %d does not match required %d", len(transaction.Signatures), requiredSignatures)
	}
	return nil
}

// MessageBytes 序列化签名消息 + 为签名验证提供确定性字节序。
func (transaction Transaction) MessageBytes() ([]byte, error) {
	return transaction.Message.MarshalBinary()
}

// Hash 计算交易哈希 + 使用确定性序列化避免字段顺序歧义。
func (transaction Transaction) Hash() (utils.Hash, error) {
	encoded, err := transaction.MarshalBinary()
	if err != nil {
		return utils.Hash{}, fmt.Errorf("structure: marshal transaction before hash: %w", err)
	}
	return utils.NewHash(utils.SHA256(encoded))
}

// MarshalBinary 序列化完整交易 + 使用 Solana short_vec 兼容紧凑长度编码。
func (transaction Transaction) MarshalBinary() ([]byte, error) {
	if err := transaction.Validate(); err != nil {
		return nil, err
	}

	encoded := make([]byte, 0, estimateTransactionSize(transaction))
	if err := appendShortVecLength(&encoded, len(transaction.Signatures)); err != nil {
		return nil, fmt.Errorf("structure: encode signatures length: %w", err)
	}
	for _, signature := range transaction.Signatures {
		encoded = append(encoded, signature[:]...)
	}

	messageBytes, err := transaction.Message.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("structure: marshal message: %w", err)
	}
	encoded = append(encoded, messageBytes...)
	return encoded, nil
}

// Validate 校验消息结构 + 保证账户索引和权限计数不会越界。
func (message Message) Validate() error {
	if len(message.AccountKeys) == 0 {
		return ErrEmptyAccountKeys
	}
	if len(message.AccountKeys) > MaxAccountKeysPerTransaction {
		return fmt.Errorf("structure: account keys count %d exceeds %d", len(message.AccountKeys), MaxAccountKeysPerTransaction)
	}
	if message.RecentBlockhash == (utils.Blockhash{}) {
		return ErrEmptyRecentBlockhash
	}
	if err := validateMessageHeader(message.Header, len(message.AccountKeys)); err != nil {
		return err
	}
	return validateInstructions(message.Instructions, len(message.AccountKeys))
}

// MarshalBinary 序列化交易消息 + 保持签名验证输入稳定。
func (message Message) MarshalBinary() ([]byte, error) {
	if err := message.Validate(); err != nil {
		return nil, err
	}

	encoded := make([]byte, 0, estimateMessageSize(message))
	encoded = append(encoded, message.Header.NumRequiredSignatures)
	encoded = append(encoded, message.Header.NumReadonlySignedAccounts)
	encoded = append(encoded, message.Header.NumReadonlyUnsignedAccounts)

	if err := appendShortVecLength(&encoded, len(message.AccountKeys)); err != nil {
		return nil, fmt.Errorf("structure: encode account keys length: %w", err)
	}
	for _, accountKey := range message.AccountKeys {
		encoded = append(encoded, accountKey[:]...)
	}

	encoded = append(encoded, message.RecentBlockhash[:]...)
	if err := appendInstructions(&encoded, message.Instructions); err != nil {
		return nil, err
	}
	return encoded, nil
}

// Clone 深拷贝指令 + 避免调用方共享底层切片造成数据串改。
func (instruction CompiledInstruction) Clone() CompiledInstruction {
	return CompiledInstruction{
		ProgramIDIndex: instruction.ProgramIDIndex,
		AccountIndexes: cloneUint16Slice(instruction.AccountIndexes),
		Data:           utils.CloneBytes(instruction.Data),
	}
}

func validateMessageHeader(header MessageHeader, accountKeyCount int) error {
	if header.NumRequiredSignatures == 0 {
		return fmt.Errorf("%w: required signatures cannot be zero", ErrInvalidMessageHeader)
	}
	if int(header.NumRequiredSignatures) > accountKeyCount {
		return fmt.Errorf("%w: required signatures exceed account keys", ErrInvalidMessageHeader)
	}
	if header.NumReadonlySignedAccounts > header.NumRequiredSignatures {
		return fmt.Errorf("%w: readonly signed accounts exceed signed accounts", ErrInvalidMessageHeader)
	}

	unsignedAccounts := accountKeyCount - int(header.NumRequiredSignatures)
	if int(header.NumReadonlyUnsignedAccounts) > unsignedAccounts {
		return fmt.Errorf("%w: readonly unsigned accounts exceed unsigned accounts", ErrInvalidMessageHeader)
	}
	return nil
}

func validateInstructions(instructions []CompiledInstruction, accountKeyCount int) error {
	if len(instructions) > MaxInstructionsPerTransaction {
		return fmt.Errorf("structure: instructions count %d exceeds %d", len(instructions), MaxInstructionsPerTransaction)
	}
	for instructionIndex, instruction := range instructions {
		if err := validateInstruction(instruction, accountKeyCount); err != nil {
			return fmt.Errorf("structure: instruction %d: %w", instructionIndex, err)
		}
	}
	return nil
}

func validateInstruction(instruction CompiledInstruction, accountKeyCount int) error {
	if int(instruction.ProgramIDIndex) >= accountKeyCount {
		return fmt.Errorf("%w: program id index %d out of range", ErrInvalidInstruction, instruction.ProgramIDIndex)
	}
	if len(instruction.AccountIndexes) > MaxInstructionAccounts {
		return fmt.Errorf("%w: account index count %d exceeds %d", ErrInvalidInstruction, len(instruction.AccountIndexes), MaxInstructionAccounts)
	}
	if len(instruction.Data) > MaxInstructionDataSize {
		return fmt.Errorf("%w: data size %d exceeds %d", ErrInvalidInstruction, len(instruction.Data), MaxInstructionDataSize)
	}
	for _, accountIndex := range instruction.AccountIndexes {
		if int(accountIndex) >= accountKeyCount {
			return fmt.Errorf("%w: account index %d out of range", ErrInvalidInstruction, accountIndex)
		}
	}
	return nil
}

func appendInstructions(encoded *[]byte, instructions []CompiledInstruction) error {
	if err := appendShortVecLength(encoded, len(instructions)); err != nil {
		return fmt.Errorf("structure: encode instructions length: %w", err)
	}
	for instructionIndex, instruction := range instructions {
		if err := appendInstruction(encoded, instruction); err != nil {
			return fmt.Errorf("structure: encode instruction %d: %w", instructionIndex, err)
		}
	}
	return nil
}

func appendInstruction(encoded *[]byte, instruction CompiledInstruction) error {
	*encoded = append(*encoded, utils.Uint16ToBytesLE(instruction.ProgramIDIndex)...)
	if err := appendUint16Indexes(encoded, instruction.AccountIndexes); err != nil {
		return fmt.Errorf("structure: encode instruction accounts: %w", err)
	}
	if err := appendShortVecLength(encoded, len(instruction.Data)); err != nil {
		return fmt.Errorf("structure: encode instruction data length: %w", err)
	}
	*encoded = append(*encoded, instruction.Data...)
	return nil
}

func appendUint16Indexes(encoded *[]byte, indexes []uint16) error {
	if err := appendShortVecLength(encoded, len(indexes)); err != nil {
		return err
	}
	for _, index := range indexes {
		*encoded = append(*encoded, utils.Uint16ToBytesLE(index)...)
	}
	return nil
}

func appendShortVecLength(encoded *[]byte, length int) error {
	lengthBytes, err := utils.EncodeShortVecLength(length)
	if err != nil {
		return err
	}
	*encoded = append(*encoded, lengthBytes...)
	return nil
}

func estimateTransactionSize(transaction Transaction) int {
	return 3 + len(transaction.Signatures)*utils.SignatureSize + estimateMessageSize(transaction.Message)
}

func estimateMessageSize(message Message) int {
	size := 3 + 3 + len(message.AccountKeys)*utils.PublicKeySize + utils.PublicKeySize
	for _, instruction := range message.Instructions {
		size += 2 + 3 + len(instruction.AccountIndexes)*2 + 3 + len(instruction.Data)
	}
	return size
}

func cloneUint16Slice(value []uint16) []uint16 {
	if value == nil {
		return nil
	}
	cloned := make([]uint16, len(value))
	copy(cloned, value)
	return cloned
}
