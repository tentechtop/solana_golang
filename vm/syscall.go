package vm

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"solana_golang/utils"
	"solana_golang/zk"
)

type SyscallID uint32

const (
	SyscallLog SyscallID = iota + 1
	SyscallSHA256
	SyscallSetReturnData
	SyscallGetReturnData
	SyscallGetClock
	SyscallGetRent
	SyscallCreateProgramAddress
	SyscallInvoke
	SyscallInvokeSigned
	SyscallVerifySchnorr
)

// SyscallFunc 执行系统调用 + 输入输出统一使用确定性字节编码。
type SyscallFunc func(context *Context, input []byte) ([]byte, error)

// Syscall 描述系统调用 + 包含名称和基础 compute 成本。
type Syscall struct {
	ID      SyscallID
	Name    string
	Cost    uint64
	Handler SyscallFunc
}

// SyscallRegistry 保存系统调用表 + 初始化后按值传递保持只读语义。
type SyscallRegistry struct {
	syscalls map[SyscallID]Syscall
}

// NewSyscallRegistry 创建 syscall 注册表 + 拒绝重复和空 handler。
func NewSyscallRegistry(syscalls ...Syscall) (SyscallRegistry, error) {
	registry := SyscallRegistry{syscalls: make(map[SyscallID]Syscall, len(syscalls))}
	for _, syscall := range syscalls {
		if syscall.ID == 0 || syscall.Name == "" || syscall.Handler == nil {
			return SyscallRegistry{}, fmt.Errorf("%w: invalid syscall", ErrExecutionFailed)
		}
		if _, exists := registry.syscalls[syscall.ID]; exists {
			return SyscallRegistry{}, fmt.Errorf("%w: duplicate syscall %d", ErrExecutionFailed, syscall.ID)
		}
		registry.syscalls[syscall.ID] = syscall
	}
	return registry, nil
}

// DefaultSyscallRegistry 返回默认 syscall 集合 + 覆盖日志、返回值、sysvar、PDA、CPI 和 ZK。
func DefaultSyscallRegistry() SyscallRegistry {
	registry, err := NewSyscallRegistry(
		Syscall{ID: SyscallLog, Name: "sol_log", Cost: 50, Handler: syscallLog},
		Syscall{ID: SyscallSHA256, Name: "sol_sha256", Cost: 90, Handler: syscallSHA256},
		Syscall{ID: SyscallSetReturnData, Name: "sol_set_return_data", Cost: 50, Handler: syscallSetReturnData},
		Syscall{ID: SyscallGetReturnData, Name: "sol_get_return_data", Cost: 25, Handler: syscallGetReturnData},
		Syscall{ID: SyscallGetClock, Name: "sol_get_clock", Cost: 20, Handler: syscallGetClock},
		Syscall{ID: SyscallGetRent, Name: "sol_get_rent", Cost: 20, Handler: syscallGetRent},
		Syscall{ID: SyscallCreateProgramAddress, Name: "sol_create_program_address", Cost: 150, Handler: syscallCreateProgramAddress},
		Syscall{ID: SyscallInvoke, Name: "sol_invoke", Cost: 500, Handler: syscallInvoke},
		Syscall{ID: SyscallInvokeSigned, Name: "sol_invoke_signed", Cost: 700, Handler: syscallInvokeSigned},
		Syscall{ID: SyscallVerifySchnorr, Name: "zk_verify_schnorr", Cost: 2_000, Handler: syscallVerifySchnorr},
	)
	if err != nil {
		return SyscallRegistry{}
	}
	return registry
}

// Invoke 执行 syscall + 统一限制输入大小和扣减计算单元。
func (registry SyscallRegistry) Invoke(context *Context, syscallID SyscallID, input []byte) ([]byte, error) {
	if len(input) > MaxSyscallInputSize {
		return nil, fmt.Errorf("%w: syscall input length %d exceeds %d", ErrExecutionFailed, len(input), MaxSyscallInputSize)
	}
	syscall, exists := registry.syscalls[syscallID]
	if !exists {
		return nil, fmt.Errorf("%w: unknown syscall %d", ErrExecutionFailed, syscallID)
	}
	if err := context.Meter.Consume(syscall.Cost); err != nil {
		return nil, err
	}
	output, err := syscall.Handler(context, utils.CloneBytes(input))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrExecutionFailed, syscall.Name, err)
	}
	context.SetLastSyscallOutput(output)
	return output, nil
}

// IsZero 判断 syscall 表是否为空 + 便于 runtime 使用默认表。
func (registry SyscallRegistry) IsZero() bool {
	return len(registry.syscalls) == 0
}

func syscallLog(context *Context, input []byte) ([]byte, error) {
	return nil, context.Log(string(input))
}

func syscallSHA256(_ *Context, input []byte) ([]byte, error) {
	sum := sha256.Sum256(input)
	return utils.CloneBytes(sum[:]), nil
}

func syscallSetReturnData(context *Context, input []byte) ([]byte, error) {
	return nil, context.SetReturnData(input)
}

func syscallGetReturnData(context *Context, _ []byte) ([]byte, error) {
	if context.ReturnData == nil {
		return nil, nil
	}
	buffer := bytes.Buffer{}
	buffer.Write(context.ReturnData.ProgramID[:])
	writeSyscallUint32(&buffer, uint32(len(context.ReturnData.Data)))
	buffer.Write(context.ReturnData.Data)
	return buffer.Bytes(), nil
}

func syscallGetClock(context *Context, _ []byte) ([]byte, error) {
	buffer := bytes.Buffer{}
	writeSyscallUint64(&buffer, context.Invocation.Sysvars.Clock.Slot)
	writeSyscallUint64(&buffer, uint64(context.Invocation.Sysvars.Clock.UnixTimestamp))
	return buffer.Bytes(), nil
}

func syscallGetRent(context *Context, _ []byte) ([]byte, error) {
	buffer := bytes.Buffer{}
	writeSyscallUint64(&buffer, context.Invocation.Sysvars.Rent.LamportsPerByteYear)
	writeSyscallUint64(&buffer, context.Invocation.Sysvars.Rent.ExemptionThresholdYears)
	writeSyscallUint64(&buffer, context.Invocation.Sysvars.Rent.AccountStorageOverheadSize)
	return buffer.Bytes(), nil
}

func syscallCreateProgramAddress(_ *Context, input []byte) ([]byte, error) {
	programID, seeds, err := decodeProgramAddressInput(input)
	if err != nil {
		return nil, err
	}
	address, err := CreateProgramAddress(programID, seeds)
	if err != nil {
		return nil, err
	}
	return address[:], nil
}

func syscallInvoke(context *Context, input []byte) ([]byte, error) {
	instruction, err := decodeCPIInstruction(input)
	if err != nil {
		return nil, err
	}
	if context.CPI == nil {
		return nil, fmt.Errorf("%w: cpi runtime unavailable", ErrExecutionFailed)
	}
	return nil, context.CPI.Invoke(context, instruction)
}

func syscallInvokeSigned(context *Context, input []byte) ([]byte, error) {
	instruction, err := decodeSignedCPIInstruction(context.Invocation.ProgramID, input)
	if err != nil {
		return nil, err
	}
	if context.CPI == nil {
		return nil, fmt.Errorf("%w: cpi runtime unavailable", ErrExecutionFailed)
	}
	return nil, context.CPI.Invoke(context, instruction)
}

func syscallVerifySchnorr(_ *Context, input []byte) ([]byte, error) {
	reader := bytes.NewReader(input)
	digestBytes, err := readSyscallFixedBytes(reader, zk.HashSize)
	if err != nil {
		return nil, err
	}
	digest, err := zk.NewDigest(digestBytes)
	if err != nil {
		return nil, err
	}
	message, err := readSyscallBytes(reader)
	if err != nil {
		return nil, err
	}
	proof, err := readSyscallBytes(reader)
	if err != nil {
		return nil, err
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("trailing schnorr input bytes %d", reader.Len())
	}
	if err := zk.VerifySchnorrProofBytes(proof, message, digest); err != nil {
		return nil, err
	}
	return []byte{1}, nil
}

func EncodeProgramAddressInput(programID Address, seeds [][]byte) ([]byte, error) {
	if len(seeds) == 0 || len(seeds) > MaxPDASeedCount {
		return nil, fmt.Errorf("%w: invalid seed count", ErrInvalidAccount)
	}
	buffer := bytes.Buffer{}
	buffer.Write(programID[:])
	buffer.WriteByte(byte(len(seeds)))
	for _, seed := range seeds {
		if len(seed) == 0 || len(seed) > MaxPDASeedLength {
			return nil, fmt.Errorf("%w: invalid seed length", ErrInvalidAccount)
		}
		buffer.WriteByte(byte(len(seed)))
		buffer.Write(seed)
	}
	return buffer.Bytes(), nil
}

func EncodeCPIInstruction(instruction CPIInstruction) ([]byte, error) {
	if len(instruction.AccountIndexes) > MaxInstructionDataSize {
		return nil, fmt.Errorf("%w: too many cpi accounts", ErrInvalidAccount)
	}
	if len(instruction.InstructionData) > MaxInstructionDataSize {
		return nil, fmt.Errorf("%w: cpi data too large", ErrExecutionFailed)
	}
	buffer := bytes.Buffer{}
	buffer.Write(instruction.ProgramID[:])
	buffer.WriteByte(byte(len(instruction.AccountIndexes)))
	buffer.Write(instruction.AccountIndexes)
	writeSyscallUint32(&buffer, uint32(len(instruction.InstructionData)))
	buffer.Write(instruction.InstructionData)
	return buffer.Bytes(), nil
}

func EncodeSignedCPIInstruction(instruction CPIInstruction, signerProgramID Address, signerSeeds [][][]byte) ([]byte, error) {
	encoded, err := EncodeCPIInstruction(instruction)
	if err != nil {
		return nil, err
	}
	buffer := bytes.NewBuffer(encoded)
	if len(signerSeeds) > MaxPDASeedCount {
		return nil, fmt.Errorf("%w: too many signer seed groups", ErrInvalidAccount)
	}
	buffer.WriteByte(byte(len(signerSeeds)))
	for _, seedGroup := range signerSeeds {
		seedInput, err := EncodeProgramAddressInput(signerProgramID, seedGroup)
		if err != nil {
			return nil, err
		}
		buffer.Write(seedInput[AddressSize:])
	}
	return buffer.Bytes(), nil
}

func EncodeSchnorrVerifyInput(publicKeyDigest zk.Digest, message []byte, proof []byte) []byte {
	buffer := bytes.Buffer{}
	buffer.Write(publicKeyDigest[:])
	writeSyscallUint32(&buffer, uint32(len(message)))
	buffer.Write(message)
	writeSyscallUint32(&buffer, uint32(len(proof)))
	buffer.Write(proof)
	return buffer.Bytes()
}

func decodeProgramAddressInput(input []byte) (Address, [][]byte, error) {
	reader := bytes.NewReader(input)
	programIDBytes, err := readSyscallFixedBytes(reader, AddressSize)
	if err != nil {
		return Address{}, nil, err
	}
	var programID Address
	copy(programID[:], programIDBytes)
	seedCountByte, err := reader.ReadByte()
	if err != nil {
		return Address{}, nil, err
	}
	seedCount := int(seedCountByte)
	if seedCount == 0 || seedCount > MaxPDASeedCount {
		return Address{}, nil, fmt.Errorf("invalid seed count %d", seedCount)
	}
	seeds := make([][]byte, seedCount)
	for seedIndex := range seeds {
		seedLengthByte, err := reader.ReadByte()
		if err != nil {
			return Address{}, nil, err
		}
		seedLength := int(seedLengthByte)
		if seedLength == 0 || seedLength > MaxPDASeedLength {
			return Address{}, nil, fmt.Errorf("invalid seed %d length %d", seedIndex, seedLength)
		}
		seeds[seedIndex], err = readSyscallFixedBytes(reader, seedLength)
		if err != nil {
			return Address{}, nil, err
		}
	}
	if reader.Len() != 0 {
		return Address{}, nil, fmt.Errorf("trailing pda input bytes %d", reader.Len())
	}
	return programID, seeds, nil
}

func decodeCPIInstruction(input []byte) (CPIInstruction, error) {
	reader := bytes.NewReader(input)
	programIDBytes, err := readSyscallFixedBytes(reader, AddressSize)
	if err != nil {
		return CPIInstruction{}, err
	}
	var programID Address
	copy(programID[:], programIDBytes)
	accountCountByte, err := reader.ReadByte()
	if err != nil {
		return CPIInstruction{}, err
	}
	accountIndexes, err := readSyscallFixedBytes(reader, int(accountCountByte))
	if err != nil {
		return CPIInstruction{}, err
	}
	instructionData, err := readSyscallBytes(reader)
	if err != nil {
		return CPIInstruction{}, err
	}
	if reader.Len() != 0 {
		return CPIInstruction{}, fmt.Errorf("trailing cpi input bytes %d", reader.Len())
	}
	return CPIInstruction{ProgramID: programID, AccountIndexes: accountIndexes, InstructionData: instructionData}, nil
}

func decodeSignedCPIInstruction(programID Address, input []byte) (CPIInstruction, error) {
	instruction, err := decodeCPIInstructionPrefix(input)
	if err != nil {
		return CPIInstruction{}, err
	}
	offset := AddressSize + 1 + len(instruction.AccountIndexes) + 4 + len(instruction.InstructionData)
	reader := bytes.NewReader(input[offset:])
	groupCountByte, err := reader.ReadByte()
	if err != nil {
		return CPIInstruction{}, err
	}
	signerPDAs := make([]Address, int(groupCountByte))
	for groupIndex := range signerPDAs {
		seedCountByte, err := reader.ReadByte()
		if err != nil {
			return CPIInstruction{}, err
		}
		seeds := make([][]byte, int(seedCountByte))
		for seedIndex := range seeds {
			seedLengthByte, err := reader.ReadByte()
			if err != nil {
				return CPIInstruction{}, err
			}
			seeds[seedIndex], err = readSyscallFixedBytes(reader, int(seedLengthByte))
			if err != nil {
				return CPIInstruction{}, err
			}
		}
		signerPDAs[groupIndex], err = CreateProgramAddress(programID, seeds)
		if err != nil {
			return CPIInstruction{}, err
		}
	}
	if reader.Len() != 0 {
		return CPIInstruction{}, fmt.Errorf("trailing signed cpi input bytes %d", reader.Len())
	}
	instruction.SignerPDAs = signerPDAs
	return instruction, nil
}

func decodeCPIInstructionPrefix(input []byte) (CPIInstruction, error) {
	reader := bytes.NewReader(input)
	programIDBytes, err := readSyscallFixedBytes(reader, AddressSize)
	if err != nil {
		return CPIInstruction{}, err
	}
	var programID Address
	copy(programID[:], programIDBytes)
	accountCountByte, err := reader.ReadByte()
	if err != nil {
		return CPIInstruction{}, err
	}
	accountIndexes, err := readSyscallFixedBytes(reader, int(accountCountByte))
	if err != nil {
		return CPIInstruction{}, err
	}
	instructionData, err := readSyscallBytes(reader)
	if err != nil {
		return CPIInstruction{}, err
	}
	return CPIInstruction{ProgramID: programID, AccountIndexes: accountIndexes, InstructionData: instructionData}, nil
}

func writeSyscallUint32(buffer *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	buffer.Write(encoded[:])
}

func writeSyscallUint64(buffer *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	buffer.Write(encoded[:])
}

func readSyscallBytes(reader *bytes.Reader) ([]byte, error) {
	var lengthBytes [4]byte
	if _, err := reader.Read(lengthBytes[:]); err != nil {
		return nil, err
	}
	length := binary.LittleEndian.Uint32(lengthBytes[:])
	if length > MaxSyscallInputSize || length > uint32(reader.Len()) {
		return nil, fmt.Errorf("invalid syscall bytes length %d", length)
	}
	return readSyscallFixedBytes(reader, int(length))
}

func readSyscallFixedBytes(reader *bytes.Reader, length int) ([]byte, error) {
	if length < 0 || length > reader.Len() {
		return nil, fmt.Errorf("need %d bytes, remaining %d", length, reader.Len())
	}
	value := make([]byte, length)
	if _, err := reader.Read(value); err != nil {
		return nil, err
	}
	return value, nil
}
