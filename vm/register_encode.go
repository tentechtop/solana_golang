package vm

import (
	"encoding/binary"
	"fmt"
)

// BuildRegisterInstruction 编码固定宽度指令 + 汇编器和测试共用同一字节布局。
func BuildRegisterInstruction(opcode byte, dst byte, src byte, offset int32, immediate uint64) ([]byte, error) {
	if opcode > RegOpSyscall {
		return nil, fmt.Errorf("%w: unknown register opcode 0x%02x", ErrInvalidProgram, opcode)
	}
	if dst >= RegisterCount {
		return nil, fmt.Errorf("%w: dst register %d out of range", ErrInvalidProgram, dst)
	}
	if src >= RegisterCount {
		return nil, fmt.Errorf("%w: src register %d out of range", ErrInvalidProgram, src)
	}
	instruction := make([]byte, RegisterInstructionSize)
	instruction[0] = opcode
	instruction[1] = dst
	instruction[2] = src
	binary.LittleEndian.PutUint32(instruction[4:8], uint32(offset))
	binary.LittleEndian.PutUint64(instruction[8:16], immediate)
	return instruction, nil
}

// BuildRegisterProgramCode 拼接寄存器指令 + 保证调用方不会遗漏 EXIT。
func BuildRegisterProgramCode(instructions ...[]byte) ([]byte, error) {
	totalLength := RegisterInstructionSize
	for _, instruction := range instructions {
		if len(instruction) != RegisterInstructionSize {
			return nil, fmt.Errorf("%w: invalid instruction length %d", ErrInvalidProgram, len(instruction))
		}
		totalLength += len(instruction)
	}
	code := make([]byte, 0, totalLength)
	for _, instruction := range instructions {
		code = append(code, instruction...)
	}
	exitInstruction, err := BuildRegisterInstruction(RegOpExit, 0, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	return append(code, exitInstruction...), nil
}
