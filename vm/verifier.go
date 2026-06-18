package vm

import "fmt"

// VerifyProgram 静态验证程序能力边界 + 执行前拦截非法 opcode、跳转和 syscall。
func VerifyProgram(program Program, syscalls SyscallRegistry) error {
	switch program.Format {
	case programFormatLegacy:
		return verifyLegacyProgram(program, syscalls)
	case programFormatRegister:
		return verifyRegisterProgramWithManifest(program, syscalls)
	default:
		return fmt.Errorf("%w: unsupported program format %d", ErrInvalidProgram, program.Format)
	}
}

func verifyLegacyProgram(program Program, syscalls SyscallRegistry) error {
	if len(program.Code) == 0 {
		return fmt.Errorf("%w: empty legacy code", ErrInvalidProgram)
	}
	programCounter := 0
	for programCounter < len(program.Code) {
		opcode := program.Code[programCounter]
		programCounter++
		switch opcode {
		case OpReturn:
			if programCounter != len(program.Code) {
				return fmt.Errorf("%w: trailing bytes after return", ErrInvalidProgram)
			}
			return nil
		case OpRequireSigner:
			if err := verifyLegacyBytesAvailable(program.Code, programCounter, 1); err != nil {
				return err
			}
			programCounter++
		case OpWriteInstructionData:
			if err := verifyLegacyBytesAvailable(program.Code, programCounter, 5); err != nil {
				return err
			}
			programCounter += 5
		case OpTransferLamports:
			if err := verifyLegacyBytesAvailable(program.Code, programCounter, 10); err != nil {
				return err
			}
			programCounter += 10
		case OpSyscall:
			syscallID, nextProgramCounter, err := verifyLegacySyscall(program, syscalls, programCounter)
			if err != nil {
				return err
			}
			if err := verifyProgramAllowsSyscall(program, syscallID); err != nil {
				return err
			}
			programCounter = nextProgramCounter
		default:
			return fmt.Errorf("%w: unsupported opcode 0x%02x", ErrInvalidProgram, opcode)
		}
	}
	return fmt.Errorf("%w: missing return", ErrInvalidProgram)
}

func verifyRegisterProgramWithManifest(program Program, syscalls SyscallRegistry) error {
	if program.Format != programFormatRegister {
		return fmt.Errorf("%w: invalid register program format %d", ErrInvalidProgram, program.Format)
	}
	instructions, err := decodeRegisterInstructions(program.Code)
	if err != nil {
		return err
	}
	for index, instruction := range instructions {
		if err := verifyRegisterInstructionWithManifest(program, instruction, uint64(len(instructions)), syscalls); err != nil {
			return fmt.Errorf("%w: instruction %d: %w", ErrInvalidProgram, index, err)
		}
	}
	return nil
}

func verifyRegisterInstructionWithManifest(program Program, instruction registerInstruction, instructionCount uint64, syscalls SyscallRegistry) error {
	if err := verifyRegisterInstruction(instruction, instructionCount, syscalls); err != nil {
		return err
	}
	if instruction.Opcode != RegOpSyscall {
		return nil
	}
	return verifyProgramAllowsSyscall(program, SyscallID(instruction.Imm))
}

func verifyLegacySyscall(program Program, syscalls SyscallRegistry, programCounter int) (SyscallID, int, error) {
	if err := verifyLegacyBytesAvailable(program.Code, programCounter, 8); err != nil {
		return 0, 0, err
	}
	syscallID, err := readCodeUint32(program.Code, &programCounter)
	if err != nil {
		return 0, 0, err
	}
	inputLength, err := readCodeUint32(program.Code, &programCounter)
	if err != nil {
		return 0, 0, err
	}
	if inputLength > MaxSyscallInputSize {
		return 0, 0, fmt.Errorf("%w: syscall input length %d exceeds %d", ErrInvalidProgram, inputLength, MaxSyscallInputSize)
	}
	if err := verifyLegacyBytesAvailable(program.Code, programCounter, int(inputLength)); err != nil {
		return 0, 0, err
	}
	if !syscalls.Exists(SyscallID(syscallID)) {
		return 0, 0, fmt.Errorf("%w: syscall %d is not registered", ErrInvalidProgram, syscallID)
	}
	return SyscallID(syscallID), programCounter + int(inputLength), nil
}

func verifyLegacyBytesAvailable(code []byte, programCounter int, length int) error {
	if length < 0 || programCounter < 0 || len(code)-programCounter < length {
		return fmt.Errorf("%w: truncated legacy bytecode", ErrInvalidProgram)
	}
	return nil
}

func verifyProgramAllowsSyscall(program Program, syscallID SyscallID) error {
	if program.Manifest == nil {
		return nil
	}
	for _, allowedSyscall := range program.Manifest.RequiredSyscalls {
		if allowedSyscall == syscallID {
			return nil
		}
	}
	return fmt.Errorf("%w: syscall %d is not declared in manifest", ErrInvalidProgram, syscallID)
}
