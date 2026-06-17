package vm

import (
	"encoding/binary"
	"fmt"

	"solana_golang/utils"
)

const (
	RegOpExit byte = iota
	RegOpMovReg
	RegOpMovImm
	RegOpAddReg
	RegOpAddImm
	RegOpSubReg
	RegOpSubImm
	RegOpMulReg
	RegOpDivReg
	RegOpLoad
	RegOpStore
	RegOpJmp
	RegOpJz
	RegOpJnz
	RegOpSyscall
)

const (
	RegisterMemoryBaseReadOnly = uint64(0x10000000)
	RegisterMemoryBaseInput    = uint64(0x20000000)
	RegisterMemoryBaseHeap     = uint64(0x30000000)
	RegisterMemoryBaseStack    = uint64(0x40000000)
)

const (
	registerCostBase    = uint64(1)
	registerCostMulDiv  = uint64(3)
	registerCostLoad    = uint64(2)
	registerCostStore   = uint64(4)
	registerCostSyscall = uint64(20)
)

type registerInstruction struct {
	Opcode byte
	Dst    byte
	Src    byte
	Offset int32
	Imm    uint64
}

type registerExecutor struct{}

type registerMemory struct {
	readOnly []byte
	input    []byte
	heap     []byte
	stack    []byte
}

func executeRegisterProgram(context *Context, program Program) error {
	if err := verifyRegisterProgram(program, context.Syscalls); err != nil {
		return err
	}
	executor := registerExecutor{}
	return executor.Execute(context, program)
}

func (executor registerExecutor) Execute(context *Context, program Program) error {
	instructions, err := decodeRegisterInstructions(program.Code)
	if err != nil {
		return err
	}
	memory := newRegisterMemory(program.ReadOnlyData, context.Invocation.InstructionData)
	registers := [RegisterCount]uint64{}
	registers[10] = RegisterMemoryBaseStack + uint64(len(memory.stack))
	programCounter := uint64(0)
	for programCounter < uint64(len(instructions)) {
		instruction := instructions[programCounter]
		if err := chargeRegisterInstruction(context.Meter, instruction.Opcode); err != nil {
			return err
		}
		nextProgramCounter, shouldExit, err := executeRegisterInstruction(context, memory, &registers, instruction, programCounter)
		if err != nil {
			return err
		}
		if shouldExit {
			return nil
		}
		programCounter = nextProgramCounter
	}
	return fmt.Errorf("%w: register program missing exit", ErrExecutionFailed)
}

func verifyRegisterProgram(program Program, syscalls SyscallRegistry) error {
	if program.Format != programFormatRegister {
		return fmt.Errorf("%w: invalid register program format %d", ErrInvalidProgram, program.Format)
	}
	instructions, err := decodeRegisterInstructions(program.Code)
	if err != nil {
		return err
	}
	for index, instruction := range instructions {
		if err := verifyRegisterInstruction(instruction, uint64(len(instructions)), syscalls); err != nil {
			return fmt.Errorf("%w: instruction %d: %w", ErrInvalidProgram, index, err)
		}
	}
	return nil
}

func decodeRegisterInstructions(code []byte) ([]registerInstruction, error) {
	if len(code) == 0 || len(code)%RegisterInstructionSize != 0 {
		return nil, fmt.Errorf("%w: invalid register code length %d", ErrInvalidProgram, len(code))
	}
	instructions := make([]registerInstruction, len(code)/RegisterInstructionSize)
	for index := range instructions {
		offset := index * RegisterInstructionSize
		instructions[index] = registerInstruction{
			Opcode: code[offset],
			Dst:    code[offset+1],
			Src:    code[offset+2],
			Offset: int32(binary.LittleEndian.Uint32(code[offset+4 : offset+8])),
			Imm:    binary.LittleEndian.Uint64(code[offset+8 : offset+16]),
		}
	}
	return instructions, nil
}

func verifyRegisterInstruction(instruction registerInstruction, instructionCount uint64, syscalls SyscallRegistry) error {
	if instruction.Opcode > RegOpSyscall {
		return fmt.Errorf("%w: unknown opcode 0x%02x", ErrExecutionFailed, instruction.Opcode)
	}
	if err := verifyRegisterIndexes(instruction); err != nil {
		return err
	}
	if isJumpOpcode(instruction.Opcode) && instruction.Imm >= instructionCount {
		return fmt.Errorf("%w: jump target %d out of range", ErrExecutionFailed, instruction.Imm)
	}
	if instruction.Opcode == RegOpSyscall && !syscalls.Exists(SyscallID(instruction.Imm)) {
		return fmt.Errorf("%w: syscall %d is not registered", ErrExecutionFailed, instruction.Imm)
	}
	return nil
}

func verifyRegisterIndexes(instruction registerInstruction) error {
	if instruction.Dst >= RegisterCount {
		return fmt.Errorf("%w: dst register %d out of range", ErrExecutionFailed, instruction.Dst)
	}
	if instruction.Src >= RegisterCount {
		return fmt.Errorf("%w: src register %d out of range", ErrExecutionFailed, instruction.Src)
	}
	if instruction.Dst == 10 && instruction.Opcode != RegOpLoad && instruction.Opcode != RegOpStore && instruction.Opcode != RegOpSyscall {
		return fmt.Errorf("%w: r10 is readonly", ErrExecutionFailed)
	}
	return nil
}

func executeRegisterInstruction(context *Context, memory registerMemory, registers *[RegisterCount]uint64, instruction registerInstruction, programCounter uint64) (uint64, bool, error) {
	nextProgramCounter := programCounter + 1
	switch instruction.Opcode {
	case RegOpExit:
		return nextProgramCounter, true, nil
	case RegOpMovReg:
		registers[instruction.Dst] = registers[instruction.Src]
	case RegOpMovImm:
		registers[instruction.Dst] = instruction.Imm
	case RegOpAddReg:
		registers[instruction.Dst] += registers[instruction.Src]
	case RegOpAddImm:
		registers[instruction.Dst] += instruction.Imm
	case RegOpSubReg:
		registers[instruction.Dst] -= registers[instruction.Src]
	case RegOpSubImm:
		registers[instruction.Dst] -= instruction.Imm
	case RegOpMulReg:
		registers[instruction.Dst] *= registers[instruction.Src]
	case RegOpDivReg:
		if registers[instruction.Src] == 0 {
			return 0, false, fmt.Errorf("%w: divide by zero", ErrExecutionFailed)
		}
		registers[instruction.Dst] /= registers[instruction.Src]
	case RegOpLoad:
		value, err := memory.ReadUint64(registers[instruction.Src], instruction.Offset)
		if err != nil {
			return 0, false, err
		}
		registers[instruction.Dst] = value
	case RegOpStore:
		if err := memory.WriteUint64(registers[instruction.Dst], instruction.Offset, registers[instruction.Src]); err != nil {
			return 0, false, err
		}
	case RegOpJmp:
		nextProgramCounter = instruction.Imm
	case RegOpJz:
		if registers[instruction.Dst] == 0 {
			nextProgramCounter = instruction.Imm
		}
	case RegOpJnz:
		if registers[instruction.Dst] != 0 {
			nextProgramCounter = instruction.Imm
		}
	case RegOpSyscall:
		if err := executeRegisterSyscall(context, memory, registers, SyscallID(instruction.Imm)); err != nil {
			return 0, false, err
		}
	default:
		return 0, false, fmt.Errorf("%w: unsupported register opcode 0x%02x", ErrExecutionFailed, instruction.Opcode)
	}
	return nextProgramCounter, false, nil
}

func chargeRegisterInstruction(meter *ComputeMeter, opcode byte) error {
	switch opcode {
	case RegOpMulReg, RegOpDivReg:
		return meter.Consume(registerCostMulDiv)
	case RegOpLoad:
		return meter.Consume(registerCostLoad)
	case RegOpStore:
		return meter.Consume(registerCostStore)
	case RegOpSyscall:
		return meter.Consume(registerCostSyscall)
	default:
		return meter.Consume(registerCostBase)
	}
}

func executeRegisterSyscall(context *Context, memory registerMemory, registers *[RegisterCount]uint64, syscallID SyscallID) error {
	inputLength := registers[2]
	if inputLength > MaxSyscallInputSize {
		return fmt.Errorf("%w: syscall input length %d exceeds %d", ErrExecutionFailed, inputLength, MaxSyscallInputSize)
	}
	input, err := memory.Read(registers[1], int(inputLength))
	if err != nil {
		return err
	}
	output, err := context.Syscalls.Invoke(context, syscallID, input)
	if err != nil {
		return err
	}
	registers[0] = uint64(len(output))
	return nil
}

func newRegisterMemory(readOnlyData []byte, input []byte) registerMemory {
	return registerMemory{
		readOnly: utils.CloneBytes(readOnlyData),
		input:    utils.CloneBytes(input),
		heap:     make([]byte, RegisterHeapSize),
		stack:    make([]byte, RegisterStackSize),
	}
}

func (memory registerMemory) ReadUint64(base uint64, offset int32) (uint64, error) {
	data, err := memory.Read(applySignedOffset(base, offset), 8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(data), nil
}

func (memory registerMemory) WriteUint64(base uint64, offset int32, value uint64) error {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	return memory.Write(applySignedOffset(base, offset), encoded[:])
}

func (memory registerMemory) Read(address uint64, length int) ([]byte, error) {
	if length < 0 || length > RegisterMaxMemoryAccessSize {
		return nil, fmt.Errorf("%w: memory read length %d invalid", ErrExecutionFailed, length)
	}
	region, offset, err := memory.region(address, length)
	if err != nil {
		return nil, err
	}
	value := make([]byte, length)
	copy(value, region[offset:offset+length])
	return value, nil
}

func (memory registerMemory) Write(address uint64, data []byte) error {
	if len(data) > RegisterMaxMemoryAccessSize {
		return fmt.Errorf("%w: memory write length %d invalid", ErrExecutionFailed, len(data))
	}
	region, offset, err := memory.mutableRegion(address, len(data))
	if err != nil {
		return err
	}
	copy(region[offset:offset+len(data)], data)
	return nil
}

func (memory registerMemory) region(address uint64, length int) ([]byte, int, error) {
	if region, offset, ok := resolveMemoryRegion(address, length, RegisterMemoryBaseReadOnly, memory.readOnly); ok {
		return region, offset, nil
	}
	if region, offset, ok := resolveMemoryRegion(address, length, RegisterMemoryBaseInput, memory.input); ok {
		return region, offset, nil
	}
	if region, offset, ok := resolveMemoryRegion(address, length, RegisterMemoryBaseHeap, memory.heap); ok {
		return region, offset, nil
	}
	if region, offset, ok := resolveMemoryRegion(address, length, RegisterMemoryBaseStack, memory.stack); ok {
		return region, offset, nil
	}
	return nil, 0, fmt.Errorf("%w: memory address 0x%x length %d out of range", ErrExecutionFailed, address, length)
}

func (memory registerMemory) mutableRegion(address uint64, length int) ([]byte, int, error) {
	if region, offset, ok := resolveMemoryRegion(address, length, RegisterMemoryBaseHeap, memory.heap); ok {
		return region, offset, nil
	}
	if region, offset, ok := resolveMemoryRegion(address, length, RegisterMemoryBaseStack, memory.stack); ok {
		return region, offset, nil
	}
	return nil, 0, fmt.Errorf("%w: memory address 0x%x length %d is readonly or out of range", ErrExecutionFailed, address, length)
}

func resolveMemoryRegion(address uint64, length int, base uint64, region []byte) ([]byte, int, bool) {
	if address < base || length < 0 {
		return nil, 0, false
	}
	offset := address - base
	if offset > uint64(len(region)) {
		return nil, 0, false
	}
	if uint64(length) > uint64(len(region))-offset {
		return nil, 0, false
	}
	return region, int(offset), true
}

func applySignedOffset(base uint64, offset int32) uint64 {
	if offset >= 0 {
		return base + uint64(offset)
	}
	return base - uint64(-offset)
}

func isJumpOpcode(opcode byte) bool {
	return opcode == RegOpJmp || opcode == RegOpJz || opcode == RegOpJnz
}
