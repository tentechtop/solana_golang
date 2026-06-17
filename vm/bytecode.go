package vm

import (
	"encoding/binary"
	"fmt"
)

const (
	OpReturn               = byte(0x00)
	OpRequireSigner        = byte(0x01)
	OpWriteInstructionData = byte(0x02)
	OpTransferLamports     = byte(0x03)
	OpSyscall              = byte(0x04)
)

const (
	baseInstructionCost = uint64(10)
	writeByteCost       = uint64(1)
	transferCost        = uint64(25)
)

// Executor 执行已加载程序 + 支持未来替换为 SBF 解释器或 JIT。
type Executor interface {
	Execute(context *Context, program Program) error
}

// BytecodeExecutor 执行最小确定性字节码 + 用于生产运行时骨架验证。
type BytecodeExecutor struct{}

// Execute 执行字节码 + 所有账户写入都通过 AccountSet 受控接口。
func (executor BytecodeExecutor) Execute(context *Context, program Program) error {
	if program.Format == programFormatRegister {
		return executeRegisterProgram(context, program)
	}
	if len(program.Code) == 0 {
		return fmt.Errorf("%w: empty code", ErrInvalidProgram)
	}
	programCounter := 0
	for programCounter < len(program.Code) {
		opcode := program.Code[programCounter]
		programCounter++
		if err := context.Meter.Consume(baseInstructionCost); err != nil {
			return err
		}
		switch opcode {
		case OpReturn:
			return nil
		case OpRequireSigner:
			if err := executeRequireSigner(program.Code, &programCounter, context.Accounts); err != nil {
				return err
			}
		case OpWriteInstructionData:
			if err := executeWriteInstructionData(program.Code, &programCounter, context); err != nil {
				return err
			}
		case OpTransferLamports:
			if err := executeTransferLamports(program.Code, &programCounter, context.Accounts, context.Meter); err != nil {
				return err
			}
		case OpSyscall:
			if err := executeSyscall(program.Code, &programCounter, context); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: unsupported opcode 0x%02x", ErrExecutionFailed, opcode)
		}
	}
	return fmt.Errorf("%w: missing return", ErrExecutionFailed)
}

// BuildProgramCode 拼接操作码 + 部署工具和测试共用确定性构造。
func BuildProgramCode(operations ...[]byte) []byte {
	totalLength := 0
	for _, operation := range operations {
		totalLength += len(operation)
	}
	code := make([]byte, 0, totalLength+1)
	for _, operation := range operations {
		code = append(code, operation...)
	}
	code = append(code, OpReturn)
	return code
}

// BuildRequireSignerOp 构造签名校验操作 + 让合约显式声明授权账户。
func BuildRequireSignerOp(accountIndex uint8) []byte {
	return []byte{OpRequireSigner, accountIndex}
}

// BuildWriteInstructionDataOp 构造数据写入操作 + 将 instruction data 写入目标账户。
func BuildWriteInstructionDataOp(accountIndex uint8, offset uint32) []byte {
	operation := []byte{OpWriteInstructionData, accountIndex}
	operation = binary.LittleEndian.AppendUint32(operation, offset)
	return operation
}

// BuildTransferLamportsOp 构造余额转移操作 + 金额固定在程序字节码中便于测试。
func BuildTransferLamportsOp(sourceIndex uint8, destinationIndex uint8, lamports uint64) []byte {
	operation := []byte{OpTransferLamports, sourceIndex, destinationIndex}
	operation = binary.LittleEndian.AppendUint64(operation, lamports)
	return operation
}

// BuildSyscallOp 构造 syscall 操作 + 合约通过统一入口访问运行时能力。
func BuildSyscallOp(syscallID SyscallID, input []byte) []byte {
	operation := []byte{OpSyscall}
	operation = binary.LittleEndian.AppendUint32(operation, uint32(syscallID))
	operation = binary.LittleEndian.AppendUint32(operation, uint32(len(input)))
	operation = append(operation, input...)
	return operation
}

func executeRequireSigner(code []byte, programCounter *int, accounts *AccountSet) error {
	accountIndex, err := readCodeUint8(code, programCounter)
	if err != nil {
		return err
	}
	return accounts.RequireSigner(int(accountIndex))
}

func executeWriteInstructionData(code []byte, programCounter *int, context *Context) error {
	accountIndex, err := readCodeUint8(code, programCounter)
	if err != nil {
		return err
	}
	offset, err := readCodeUint32(code, programCounter)
	if err != nil {
		return err
	}
	if err := context.Meter.Consume(uint64(len(context.Invocation.InstructionData)) * writeByteCost); err != nil {
		return err
	}
	return context.Accounts.WriteInstructionData(int(accountIndex), offset, context.Invocation.InstructionData)
}

func executeTransferLamports(code []byte, programCounter *int, accounts *AccountSet, meter *ComputeMeter) error {
	sourceIndex, err := readCodeUint8(code, programCounter)
	if err != nil {
		return err
	}
	destinationIndex, err := readCodeUint8(code, programCounter)
	if err != nil {
		return err
	}
	lamports, err := readCodeUint64(code, programCounter)
	if err != nil {
		return err
	}
	if err := meter.Consume(transferCost); err != nil {
		return err
	}
	return accounts.TransferLamports(int(sourceIndex), int(destinationIndex), lamports)
}

func executeSyscall(code []byte, programCounter *int, context *Context) error {
	syscallID, err := readCodeUint32(code, programCounter)
	if err != nil {
		return err
	}
	inputLength, err := readCodeUint32(code, programCounter)
	if err != nil {
		return err
	}
	if inputLength > MaxSyscallInputSize {
		return fmt.Errorf("%w: syscall input length %d exceeds %d", ErrExecutionFailed, inputLength, MaxSyscallInputSize)
	}
	input, err := readCodeFixedBytes(code, programCounter, int(inputLength))
	if err != nil {
		return err
	}
	_, err = context.Syscalls.Invoke(context, SyscallID(syscallID), input)
	return err
}

func readCodeUint8(code []byte, programCounter *int) (uint8, error) {
	if *programCounter >= len(code) {
		return 0, fmt.Errorf("%w: unexpected end reading u8", ErrExecutionFailed)
	}
	value := code[*programCounter]
	*programCounter = *programCounter + 1
	return value, nil
}

func readCodeUint32(code []byte, programCounter *int) (uint32, error) {
	if len(code)-*programCounter < 4 {
		return 0, fmt.Errorf("%w: unexpected end reading u32", ErrExecutionFailed)
	}
	value := binary.LittleEndian.Uint32(code[*programCounter : *programCounter+4])
	*programCounter += 4
	return value, nil
}

func readCodeUint64(code []byte, programCounter *int) (uint64, error) {
	if len(code)-*programCounter < 8 {
		return 0, fmt.Errorf("%w: unexpected end reading u64", ErrExecutionFailed)
	}
	value := binary.LittleEndian.Uint64(code[*programCounter : *programCounter+8])
	*programCounter += 8
	return value, nil
}

func readCodeFixedBytes(code []byte, programCounter *int, length int) ([]byte, error) {
	if length < 0 || len(code)-*programCounter < length {
		return nil, fmt.Errorf("%w: unexpected end reading bytes", ErrExecutionFailed)
	}
	value := make([]byte, length)
	copy(value, code[*programCounter:*programCounter+length])
	*programCounter += length
	return value, nil
}
