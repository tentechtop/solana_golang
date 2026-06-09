package structure

import (
	"fmt"

	"solana_golang/utils"
)

// NewCompiledInstruction 创建已编译指令 + 复制输入切片防止外部修改。
func NewCompiledInstruction(programIDIndex uint8, accountIndexes []uint8, data []byte) (CompiledInstruction, error) {
	instruction := CompiledInstruction{
		ProgramIDIndex: programIDIndex,
		AccountIndexes: cloneUint8Slice(accountIndexes),
		Data:           utils.CloneBytes(data),
	}
	if err := instruction.Validate(MaxAccountsPerTransaction); err != nil {
		return CompiledInstruction{}, err
	}
	return instruction, nil
}

// Validate 校验已编译指令 + 防止程序索引和账户索引越界。
func (instruction CompiledInstruction) Validate(accountCount int) error {
	return validateInstruction(instruction, accountCount)
}

// DataHash 计算指令数据哈希 + 为去重、审计和执行缓存提供稳定摘要。
func (instruction CompiledInstruction) DataHash() (Hash, error) {
	return NewHash(utils.SHA256(instruction.Data))
}

// AccountCount 返回指令账户数量 + 避免调用方直接读取可变切片长度。
func (instruction CompiledInstruction) AccountCount() int {
	return len(instruction.AccountIndexes)
}

// HasAccountIndex 判断指令是否引用账户 + 用于账户锁和权限检查。
func (instruction CompiledInstruction) HasAccountIndex(accountIndex uint8) bool {
	for _, currentIndex := range instruction.AccountIndexes {
		if currentIndex == accountIndex {
			return true
		}
	}
	return false
}

// MarshalBinary 序列化已编译指令 + 保持和 Solana message 指令格式一致。
func (instruction CompiledInstruction) MarshalBinary() ([]byte, error) {
	if err := instruction.Validate(MaxAccountsPerTransaction); err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, 1+3+len(instruction.AccountIndexes)+3+len(instruction.Data))
	if err := appendInstruction(&encoded, instruction); err != nil {
		return nil, fmt.Errorf("structure: marshal instruction: %w", err)
	}
	return encoded, nil
}

// CompileInstruction 编译业务指令账户 + 使用公钥索引表生成 Solana 指令。
func CompileInstruction(programID PublicKey, accountKeys []PublicKey, data []byte, accountIndexByKey map[PublicKey]uint8) (CompiledInstruction, error) {
	programIDIndex, exists := accountIndexByKey[programID]
	if !exists {
		return CompiledInstruction{}, fmt.Errorf("%w: program id account is missing", ErrInvalidInstruction)
	}
	if len(accountKeys) > MaxInstructionAccounts {
		return CompiledInstruction{}, fmt.Errorf("%w: account count %d exceeds %d", ErrInvalidInstruction, len(accountKeys), MaxInstructionAccounts)
	}

	accountIndexes := make([]uint8, len(accountKeys))
	for accountPosition, accountKey := range accountKeys {
		accountIndex, exists := accountIndexByKey[accountKey]
		if !exists {
			return CompiledInstruction{}, fmt.Errorf("%w: account %d is missing", ErrInvalidInstruction, accountPosition)
		}
		accountIndexes[accountPosition] = accountIndex
	}
	return NewCompiledInstruction(programIDIndex, accountIndexes, data)
}
