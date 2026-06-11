package structure

import (
	"fmt"

	"solana_golang/utils"
)

type InstructionErrorCode uint16

const (
	InstructionErrorCodeNone InstructionErrorCode = iota
	InstructionErrorCodeGeneric
	InstructionErrorCodeInvalidArgument
	InstructionErrorCodeInvalidInstructionData
	InstructionErrorCodeInvalidAccountData
	InstructionErrorCodeAccountNotFound
	InstructionErrorCodeMissingRequiredSignature
	InstructionErrorCodeInsufficientFunds
	InstructionErrorCodeAccountBorrowFailed
	InstructionErrorCodeComputationalBudgetExceeded
)

type InstructionAccountMeta = AccountMeta

// Instruction 描述未编译业务指令 + 保留程序 ID、账户权限和原始数据。
type Instruction struct {
	ProgramID PublicKey
	Accounts  []InstructionAccountMeta
	Data      []byte
}

// InstructionError 描述指令执行错误 + 保留指令序号和稳定错误码。
type InstructionError struct {
	InstructionIndex uint16
	Code             InstructionErrorCode
	Message          string
}

// NewInstruction 创建业务指令 + 复制账户和数据避免调用方修改。
func NewInstruction(programID PublicKey, accounts []InstructionAccountMeta, data []byte) (Instruction, error) {
	instruction := Instruction{
		ProgramID: programID,
		Accounts:  cloneAccounts(accounts),
		Data:      utils.CloneBytes(data),
	}
	if err := instruction.Validate(); err != nil {
		return Instruction{}, err
	}
	return instruction, nil
}

// Validate 校验业务指令 + 防止超限账户和数据进入消息编译。
func (instruction Instruction) Validate() error {
	if len(instruction.Accounts) > MaxInstructionAccounts {
		return fmt.Errorf("%w: account count %d exceeds %d", ErrInvalidInstruction, len(instruction.Accounts), MaxInstructionAccounts)
	}
	if len(instruction.Data) > MaxInstructionDataSize {
		return fmt.Errorf("%w: data size %d exceeds %d", ErrInvalidInstruction, len(instruction.Data), MaxInstructionDataSize)
	}
	for accountIndex, account := range instruction.Accounts {
		if err := account.Validate(); err != nil {
			return fmt.Errorf("structure: instruction account %d: %w", accountIndex, err)
		}
	}
	return nil
}

// AccountKeys 返回指令账户公钥 + 为编译阶段构建账户索引提供输入。
func (instruction Instruction) AccountKeys() []PublicKey {
	accountKeys := make([]PublicKey, len(instruction.Accounts))
	for accountIndex, account := range instruction.Accounts {
		accountKeys[accountIndex] = account.PublicKey
	}
	return accountKeys
}

// Compile 编译业务指令 + 使用消息账户索引表生成紧凑指令。
func (instruction Instruction) Compile(accountIndexByKey map[PublicKey]uint8) (CompiledInstruction, error) {
	if err := instruction.Validate(); err != nil {
		return CompiledInstruction{}, err
	}
	return CompileInstruction(instruction.ProgramID, instruction.AccountKeys(), instruction.Data, accountIndexByKey)
}

// Clone 深拷贝业务指令 + 避免执行层共享账户和数据切片。
func (instruction Instruction) Clone() Instruction {
	return Instruction{
		ProgramID: instruction.ProgramID,
		Accounts:  cloneAccounts(instruction.Accounts),
		Data:      utils.CloneBytes(instruction.Data),
	}
}

// Error 返回指令错误文本 + 便于日志和测试统一输出。
func (instructionError InstructionError) Error() string {
	if instructionError.Message != "" {
		return fmt.Sprintf("instruction %d: %s", instructionError.InstructionIndex, instructionError.Message)
	}
	return fmt.Sprintf("instruction %d: error code %d", instructionError.InstructionIndex, instructionError.Code)
}

// Validate 校验指令错误 + 防止成功码被误写为失败结果。
func (instructionError InstructionError) Validate() error {
	if instructionError.Code == InstructionErrorCodeNone {
		return fmt.Errorf("%w: instruction error code cannot be none", ErrInvalidExecutionResult)
	}
	return nil
}

// Clone 深拷贝指令错误 + 保持错误值传递时不可变。
func (instructionError InstructionError) Clone() *InstructionError {
	cloned := instructionError
	return &cloned
}
