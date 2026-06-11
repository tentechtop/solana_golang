package structure

import (
	"fmt"

	"solana_golang/codec/borsh"
)

type SystemInstructionType uint32

const (
	SystemInstructionCreateAccount SystemInstructionType = iota
	SystemInstructionAssign
	SystemInstructionTransfer
	SystemInstructionAllocate
)

// CreateAccountParams 描述创建账户参数 + 由系统指令分配余额、空间和 owner。
type CreateAccountParams struct {
	Lamports uint64
	Space    uint64
	Owner    PublicKey
}

// TransferParams 描述转账参数 + 只保存金额并由账户列表表达来源和目标。
type TransferParams struct {
	Lamports uint64
}

// AssignParams 描述 owner 变更参数 + 账户本身由指令账户列表提供。
type AssignParams struct {
	Owner PublicKey
}

// AllocateParams 描述空间分配参数 + 只表达目标账户新的 data 长度。
type AllocateParams struct {
	Space uint64
}

// SystemInstruction 描述系统程序指令 + 使用 type 和参数体表达账户基础操作。
type SystemInstruction struct {
	Type          SystemInstructionType
	CreateAccount *CreateAccountParams
	Transfer      *TransferParams
	Assign        *AssignParams
	Allocate      *AllocateParams
}

// NewCreateAccountInstruction 创建账户指令数据 + 集中校验空间上限。
func NewCreateAccountInstruction(params CreateAccountParams) (SystemInstruction, error) {
	instruction := SystemInstruction{Type: SystemInstructionCreateAccount, CreateAccount: &params}
	return instruction, instruction.Validate()
}

// NewTransferInstruction 创建转账指令数据 + 保持系统转账参数最小化。
func NewTransferInstruction(params TransferParams) (SystemInstruction, error) {
	instruction := SystemInstruction{Type: SystemInstructionTransfer, Transfer: &params}
	return instruction, instruction.Validate()
}

// NewAssignInstruction 创建 owner 变更指令数据 + 将 owner 写入参数体。
func NewAssignInstruction(params AssignParams) (SystemInstruction, error) {
	instruction := SystemInstruction{Type: SystemInstructionAssign, Assign: &params}
	return instruction, instruction.Validate()
}

// NewAllocateInstruction 创建空间分配指令数据 + 统一限制账户 data 长度。
func NewAllocateInstruction(params AllocateParams) (SystemInstruction, error) {
	instruction := SystemInstruction{Type: SystemInstructionAllocate, Allocate: &params}
	return instruction, instruction.Validate()
}

// Validate 校验系统指令 + 防止 oneof 参数缺失或空间越界。
func (instruction SystemInstruction) Validate() error {
	switch instruction.Type {
	case SystemInstructionCreateAccount:
		return validateCreateAccountParams(instruction.CreateAccount)
	case SystemInstructionAssign:
		return validateAssignParams(instruction.Assign)
	case SystemInstructionTransfer:
		return validateTransferParams(instruction.Transfer)
	case SystemInstructionAllocate:
		return validateAllocateParams(instruction.Allocate)
	default:
		return fmt.Errorf("%w: unsupported type %d", ErrInvalidSystemInstruction, instruction.Type)
	}
}

// MarshalBinary 序列化系统指令 + 作为 instruction data 的确定性编码。
func (instruction SystemInstruction) MarshalBinary() ([]byte, error) {
	if err := instruction.Validate(); err != nil {
		return nil, err
	}

	writer := borsh.NewWriter(128)
	writer.WriteUint32(uint32(instruction.Type))
	switch instruction.Type {
	case SystemInstructionCreateAccount:
		writer.WriteUint64(instruction.CreateAccount.Lamports)
		writer.WriteUint64(instruction.CreateAccount.Space)
		writer.WriteFixedBytes(instruction.CreateAccount.Owner[:])
	case SystemInstructionAssign:
		writer.WriteFixedBytes(instruction.Assign.Owner[:])
	case SystemInstructionTransfer:
		writer.WriteUint64(instruction.Transfer.Lamports)
	case SystemInstructionAllocate:
		writer.WriteUint64(instruction.Allocate.Space)
	}
	return writer.Bytes(), nil
}

// UnmarshalSystemInstructionBinary 反序列化系统指令 + 解码后校验参数和尾部字节。
func UnmarshalSystemInstructionBinary(data []byte) (SystemInstruction, error) {
	reader := borsh.NewReader(data, 128)
	instructionType, err := reader.ReadUint32()
	if err != nil {
		return SystemInstruction{}, fmt.Errorf("structure: decode system instruction type: %w", err)
	}

	instruction, err := readSystemInstructionBody(reader, SystemInstructionType(instructionType))
	if err != nil {
		return SystemInstruction{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return SystemInstruction{}, fmt.Errorf("structure: decode system instruction eof: %w", err)
	}
	return instruction, instruction.Validate()
}

func readSystemInstructionBody(reader *borsh.Reader, instructionType SystemInstructionType) (SystemInstruction, error) {
	switch instructionType {
	case SystemInstructionCreateAccount:
		return readCreateAccountInstruction(reader)
	case SystemInstructionAssign:
		return readAssignInstruction(reader)
	case SystemInstructionTransfer:
		return readTransferInstruction(reader)
	case SystemInstructionAllocate:
		return readAllocateInstruction(reader)
	default:
		return SystemInstruction{}, fmt.Errorf("%w: unsupported type %d", ErrInvalidSystemInstruction, instructionType)
	}
}

func readCreateAccountInstruction(reader *borsh.Reader) (SystemInstruction, error) {
	lamports, err := reader.ReadUint64()
	if err != nil {
		return SystemInstruction{}, fmt.Errorf("structure: decode create account lamports: %w", err)
	}
	space, err := reader.ReadUint64()
	if err != nil {
		return SystemInstruction{}, fmt.Errorf("structure: decode create account space: %w", err)
	}
	owner, err := readSystemInstructionPublicKey(reader, "create account owner")
	if err != nil {
		return SystemInstruction{}, err
	}
	return NewCreateAccountInstruction(CreateAccountParams{Lamports: lamports, Space: space, Owner: owner})
}

func readAssignInstruction(reader *borsh.Reader) (SystemInstruction, error) {
	owner, err := readSystemInstructionPublicKey(reader, "assign owner")
	if err != nil {
		return SystemInstruction{}, err
	}
	return NewAssignInstruction(AssignParams{Owner: owner})
}

func readTransferInstruction(reader *borsh.Reader) (SystemInstruction, error) {
	lamports, err := reader.ReadUint64()
	if err != nil {
		return SystemInstruction{}, fmt.Errorf("structure: decode transfer lamports: %w", err)
	}
	return NewTransferInstruction(TransferParams{Lamports: lamports})
}

func readAllocateInstruction(reader *borsh.Reader) (SystemInstruction, error) {
	space, err := reader.ReadUint64()
	if err != nil {
		return SystemInstruction{}, fmt.Errorf("structure: decode allocate space: %w", err)
	}
	return NewAllocateInstruction(AllocateParams{Space: space})
}

func readSystemInstructionPublicKey(reader *borsh.Reader, field string) (PublicKey, error) {
	value, err := reader.ReadFixedBytes(PublicKeySize)
	if err != nil {
		return PublicKey{}, fmt.Errorf("structure: decode %s: %w", field, err)
	}
	publicKey, err := NewPublicKey(value)
	if err != nil {
		return PublicKey{}, fmt.Errorf("structure: decode %s: %w", field, err)
	}
	return publicKey, nil
}

func validateCreateAccountParams(params *CreateAccountParams) error {
	if params == nil {
		return fmt.Errorf("%w: create account params are nil", ErrInvalidSystemInstruction)
	}
	return validateAccountSpace(params.Space)
}

func validateAssignParams(params *AssignParams) error {
	if params == nil {
		return fmt.Errorf("%w: assign params are nil", ErrInvalidSystemInstruction)
	}
	return nil
}

func validateTransferParams(params *TransferParams) error {
	if params == nil {
		return fmt.Errorf("%w: transfer params are nil", ErrInvalidSystemInstruction)
	}
	return nil
}

func validateAllocateParams(params *AllocateParams) error {
	if params == nil {
		return fmt.Errorf("%w: allocate params are nil", ErrInvalidSystemInstruction)
	}
	return validateAccountSpace(params.Space)
}

func validateAccountSpace(space uint64) error {
	if space > uint64(MaxAccountDataSize) {
		return fmt.Errorf("%w: space %d exceeds %d", ErrAccountDataTooLarge, space, MaxAccountDataSize)
	}
	return nil
}
