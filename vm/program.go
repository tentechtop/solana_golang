package vm

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

const (
	BytecodeVersion       = uint16(1)
	bytecodeHeader        = "SVM1"
	programFormatLegacy   = uint16(0)
	programFormatRegister = uint16(1)
	programFormatManifest = uint16(2)
	registerHeaderSize    = 16
	registerChecksumSize  = 32
	manifestHeaderSize    = 62
)

// ProgramManifest 描述合约能力声明 + 部署前固定 syscall、升级权限和 compute 上限。
type ProgramManifest struct {
	ComputeUnitLimit uint64
	UpgradeAuthority Address
	RequiredSyscalls []SyscallID
}

// Program 表示已加载程序 + 后端可以是当前字节码或未来 SBF。
type Program struct {
	Version      uint16
	Format       uint16
	Code         []byte
	ReadOnlyData []byte
	Manifest     *ProgramManifest
}

// Loader 加载程序账户 + 隔离字节码格式校验和执行器。
type Loader interface {
	Load(programAccount ProgramAccount, loaderID Address) (Program, error)
}

// BytecodeLoader 加载最小 VM 字节码 + 作为后续 SBF loader 的替换点。
type BytecodeLoader struct{}

// EncodeBytecode 编码程序字节码 + 测试和部署工具共用同一格式。
func EncodeBytecode(code []byte) ([]byte, error) {
	if len(code) == 0 {
		return nil, fmt.Errorf("%w: code cannot be empty", ErrInvalidProgram)
	}
	if len(code) > MaxProgramDataSize {
		return nil, fmt.Errorf("%w: code length %d exceeds %d", ErrInvalidProgram, len(code), MaxProgramDataSize)
	}
	buffer := bytes.Buffer{}
	buffer.WriteString(bytecodeHeader)
	writeUint16(&buffer, BytecodeVersion)
	writeUint32(&buffer, uint32(len(code)))
	buffer.Write(code)
	return buffer.Bytes(), nil
}

// EncodeRegisterBytecode 编码寄存器字节码 + 使用扩展头部区分旧版执行格式。
func EncodeRegisterBytecode(code []byte, readOnlyData []byte) ([]byte, error) {
	if len(code) == 0 {
		return nil, fmt.Errorf("%w: code cannot be empty", ErrInvalidProgram)
	}
	if len(code)%RegisterInstructionSize != 0 {
		return nil, fmt.Errorf("%w: register code length %d is not aligned", ErrInvalidProgram, len(code))
	}
	if len(code)+len(readOnlyData) > MaxProgramDataSize {
		return nil, fmt.Errorf("%w: register program length exceeds %d", ErrInvalidProgram, MaxProgramDataSize)
	}
	if len(readOnlyData) > MaxReadOnlyDataSize {
		return nil, fmt.Errorf("%w: readonly data length %d exceeds %d", ErrInvalidProgram, len(readOnlyData), MaxReadOnlyDataSize)
	}
	buffer := bytes.Buffer{}
	buffer.WriteString(bytecodeHeader)
	writeUint16(&buffer, BytecodeVersion)
	writeUint16(&buffer, programFormatRegister)
	writeUint32(&buffer, uint32(len(code)))
	writeUint32(&buffer, uint32(len(readOnlyData)))
	buffer.Write(code)
	buffer.Write(readOnlyData)
	sum := sha256.Sum256(buffer.Bytes())
	buffer.Write(sum[:])
	return buffer.Bytes(), nil
}

// EncodeGovernedBytecode 编码带 manifest 的字节码 + 让生产部署具备能力边界和升级治理。
func EncodeGovernedBytecode(code []byte, manifest ProgramManifest) ([]byte, error) {
	if len(code) == 0 {
		return nil, fmt.Errorf("%w: code cannot be empty", ErrInvalidProgram)
	}
	if len(code) > MaxProgramDataSize {
		return nil, fmt.Errorf("%w: code length %d exceeds %d", ErrInvalidProgram, len(code), MaxProgramDataSize)
	}
	return encodeManifestBytecode(programFormatLegacy, code, nil, manifest)
}

// EncodeGovernedRegisterBytecode 编码带 manifest 的寄存器字节码 + 生产合约显式声明 syscall 能力。
func EncodeGovernedRegisterBytecode(code []byte, readOnlyData []byte, manifest ProgramManifest) ([]byte, error) {
	if len(code) == 0 {
		return nil, fmt.Errorf("%w: code cannot be empty", ErrInvalidProgram)
	}
	if len(code)%RegisterInstructionSize != 0 {
		return nil, fmt.Errorf("%w: register code length %d is not aligned", ErrInvalidProgram, len(code))
	}
	if len(code)+len(readOnlyData) > MaxProgramDataSize {
		return nil, fmt.Errorf("%w: register program length exceeds %d", ErrInvalidProgram, MaxProgramDataSize)
	}
	if len(readOnlyData) > MaxReadOnlyDataSize {
		return nil, fmt.Errorf("%w: readonly data length %d exceeds %d", ErrInvalidProgram, len(readOnlyData), MaxReadOnlyDataSize)
	}
	return encodeManifestBytecode(programFormatRegister, code, readOnlyData, manifest)
}

// DecodeProgramManifest 读取合约 manifest + loader 和治理模块可在不执行代码时检查权限。
func DecodeProgramManifest(data []byte) (ProgramManifest, bool, error) {
	if len(data) > MaxProgramDataSize {
		return ProgramManifest{}, false, fmt.Errorf("%w: program data length %d exceeds %d", ErrInvalidProgram, len(data), MaxProgramDataSize)
	}
	if len(data) < len(bytecodeHeader)+2 {
		return ProgramManifest{}, false, fmt.Errorf("%w: bytecode too short", ErrInvalidProgram)
	}
	if string(data[:len(bytecodeHeader)]) != bytecodeHeader {
		return ProgramManifest{}, false, fmt.Errorf("%w: invalid bytecode header", ErrInvalidProgram)
	}
	if version := binary.LittleEndian.Uint16(data[len(bytecodeHeader):]); version != BytecodeVersion {
		return ProgramManifest{}, false, fmt.Errorf("%w: unsupported bytecode version %d", ErrInvalidProgram, version)
	}
	if _, ok, err := loadLegacyProgram(BytecodeVersion, data); ok || err != nil {
		return ProgramManifest{}, false, err
	}
	program, ok, err := loadManifestProgram(BytecodeVersion, data)
	if err != nil || !ok {
		return ProgramManifest{}, ok, err
	}
	if program.Manifest == nil {
		return ProgramManifest{}, false, nil
	}
	return *program.Manifest, true, nil
}

// Load 加载程序账户 + 校验 executable、loader owner、版本和尾部字节。
func (loader BytecodeLoader) Load(programAccount ProgramAccount, loaderID Address) (Program, error) {
	if !programAccount.Executable {
		return Program{}, fmt.Errorf("%w: program account is not executable", ErrInvalidProgram)
	}
	if programAccount.Owner != loaderID {
		return Program{}, fmt.Errorf("%w: program owner is not loader", ErrInvalidProgram)
	}
	if len(programAccount.Data) > MaxProgramDataSize {
		return Program{}, fmt.Errorf("%w: program data length %d exceeds %d", ErrInvalidProgram, len(programAccount.Data), MaxProgramDataSize)
	}
	data := programAccount.Data
	reader := bytes.NewReader(data)
	header := make([]byte, len(bytecodeHeader))
	if _, err := reader.Read(header); err != nil {
		return Program{}, fmt.Errorf("%w: read header: %w", ErrInvalidProgram, err)
	}
	if string(header) != bytecodeHeader {
		return Program{}, fmt.Errorf("%w: invalid bytecode header", ErrInvalidProgram)
	}
	version, err := readUint16(reader)
	if err != nil {
		return Program{}, err
	}
	if version != BytecodeVersion {
		return Program{}, fmt.Errorf("%w: unsupported bytecode version %d", ErrInvalidProgram, version)
	}
	if program, ok, err := loadLegacyProgram(version, data); ok || err != nil {
		return program, err
	}
	if program, ok, err := loadManifestProgram(version, data); ok || err != nil {
		return program, err
	}
	return loadRegisterProgram(version, data)
}

func encodeManifestBytecode(executableFormat uint16, code []byte, readOnlyData []byte, manifest ProgramManifest) ([]byte, error) {
	normalizedManifest, err := normalizeProgramManifest(manifest)
	if err != nil {
		return nil, err
	}
	if executableFormat != programFormatLegacy && executableFormat != programFormatRegister {
		return nil, fmt.Errorf("%w: unsupported manifest executable format %d", ErrInvalidProgram, executableFormat)
	}
	if len(code)+len(readOnlyData) > MaxProgramDataSize {
		return nil, fmt.Errorf("%w: manifest program length exceeds %d", ErrInvalidProgram, MaxProgramDataSize)
	}
	if len(readOnlyData) > MaxReadOnlyDataSize {
		return nil, fmt.Errorf("%w: readonly data length %d exceeds %d", ErrInvalidProgram, len(readOnlyData), MaxReadOnlyDataSize)
	}
	buffer := bytes.Buffer{}
	buffer.WriteString(bytecodeHeader)
	writeUint16(&buffer, BytecodeVersion)
	writeUint16(&buffer, programFormatManifest)
	writeUint16(&buffer, executableFormat)
	writeUint16(&buffer, 0)
	writeUint64(&buffer, normalizedManifest.ComputeUnitLimit)
	buffer.Write(normalizedManifest.UpgradeAuthority[:])
	writeUint16(&buffer, uint16(len(normalizedManifest.RequiredSyscalls)))
	writeUint32(&buffer, uint32(len(code)))
	writeUint32(&buffer, uint32(len(readOnlyData)))
	for _, syscallID := range normalizedManifest.RequiredSyscalls {
		writeUint32(&buffer, uint32(syscallID))
	}
	buffer.Write(code)
	buffer.Write(readOnlyData)
	sum := sha256.Sum256(buffer.Bytes())
	buffer.Write(sum[:])
	return buffer.Bytes(), nil
}

func loadLegacyProgram(version uint16, data []byte) (Program, bool, error) {
	if len(data) < len(bytecodeHeader)+2+4 {
		return Program{}, false, nil
	}
	codeLength := binary.LittleEndian.Uint32(data[len(bytecodeHeader)+2:])
	if codeLength == 0 || int(codeLength) != len(data)-len(bytecodeHeader)-2-4 {
		return Program{}, false, nil
	}
	code := make([]byte, int(codeLength))
	copy(code, data[len(bytecodeHeader)+2+4:])
	return Program{Version: version, Format: programFormatLegacy, Code: code}, true, nil
}

func loadManifestProgram(version uint16, data []byte) (Program, bool, error) {
	if len(data) < manifestHeaderSize+registerChecksumSize {
		return Program{}, false, nil
	}
	if binary.LittleEndian.Uint16(data[6:8]) != programFormatManifest {
		return Program{}, false, nil
	}
	checksumOffset := len(data) - registerChecksumSize
	wantSum := sha256.Sum256(data[:checksumOffset])
	if !bytes.Equal(wantSum[:], data[checksumOffset:]) {
		return Program{}, true, fmt.Errorf("%w: manifest checksum mismatch", ErrInvalidProgram)
	}
	executableFormat := binary.LittleEndian.Uint16(data[8:10])
	if executableFormat != programFormatLegacy && executableFormat != programFormatRegister {
		return Program{}, true, fmt.Errorf("%w: unsupported manifest executable format %d", ErrInvalidProgram, executableFormat)
	}
	flags := binary.LittleEndian.Uint16(data[10:12])
	if flags != 0 {
		return Program{}, true, fmt.Errorf("%w: unsupported manifest flags %d", ErrInvalidProgram, flags)
	}
	manifest := ProgramManifest{ComputeUnitLimit: binary.LittleEndian.Uint64(data[12:20])}
	copy(manifest.UpgradeAuthority[:], data[20:52])
	syscallCount := binary.LittleEndian.Uint16(data[52:54])
	codeLength := binary.LittleEndian.Uint32(data[54:58])
	readOnlyLength := binary.LittleEndian.Uint32(data[58:62])
	if syscallCount > MaxProgramSyscallCount {
		return Program{}, true, fmt.Errorf("%w: syscall count %d exceeds %d", ErrInvalidProgram, syscallCount, MaxProgramSyscallCount)
	}
	if codeLength == 0 {
		return Program{}, true, fmt.Errorf("%w: manifest code cannot be empty", ErrInvalidProgram)
	}
	if executableFormat == programFormatRegister && codeLength%RegisterInstructionSize != 0 {
		return Program{}, true, fmt.Errorf("%w: register code length %d is not aligned", ErrInvalidProgram, codeLength)
	}
	if readOnlyLength > MaxReadOnlyDataSize {
		return Program{}, true, fmt.Errorf("%w: readonly data length %d exceeds %d", ErrInvalidProgram, readOnlyLength, MaxReadOnlyDataSize)
	}
	syscallBytes := int(syscallCount) * 4
	sectionStart := manifestHeaderSize + syscallBytes
	if int(codeLength)+int(readOnlyLength) != checksumOffset-sectionStart {
		return Program{}, true, fmt.Errorf("%w: manifest section length mismatch", ErrInvalidProgram)
	}
	manifest.RequiredSyscalls = make([]SyscallID, int(syscallCount))
	for index := range manifest.RequiredSyscalls {
		offset := manifestHeaderSize + index*4
		manifest.RequiredSyscalls[index] = SyscallID(binary.LittleEndian.Uint32(data[offset : offset+4]))
	}
	normalizedManifest, err := normalizeProgramManifest(manifest)
	if err != nil {
		return Program{}, true, err
	}
	if !programManifestSyscallsEqual(manifest.RequiredSyscalls, normalizedManifest.RequiredSyscalls) {
		return Program{}, true, fmt.Errorf("%w: manifest syscalls must be sorted and unique", ErrInvalidProgram)
	}
	codeStart := sectionStart
	codeEnd := codeStart + int(codeLength)
	readOnlyEnd := codeEnd + int(readOnlyLength)
	code := make([]byte, int(codeLength))
	readOnlyData := make([]byte, int(readOnlyLength))
	copy(code, data[codeStart:codeEnd])
	copy(readOnlyData, data[codeEnd:readOnlyEnd])
	return Program{Version: version, Format: executableFormat, Code: code, ReadOnlyData: readOnlyData, Manifest: &normalizedManifest}, true, nil
}

func loadRegisterProgram(version uint16, data []byte) (Program, error) {
	if len(data) < registerHeaderSize+registerChecksumSize {
		return Program{}, fmt.Errorf("%w: register bytecode too short", ErrInvalidProgram)
	}
	checksumOffset := len(data) - registerChecksumSize
	wantSum := sha256.Sum256(data[:checksumOffset])
	if !bytes.Equal(wantSum[:], data[checksumOffset:]) {
		return Program{}, fmt.Errorf("%w: register checksum mismatch", ErrInvalidProgram)
	}
	format := binary.LittleEndian.Uint16(data[6:8])
	if format != programFormatRegister {
		return Program{}, fmt.Errorf("%w: unsupported program format %d", ErrInvalidProgram, format)
	}
	codeLength := binary.LittleEndian.Uint32(data[8:12])
	readOnlyLength := binary.LittleEndian.Uint32(data[12:16])
	if codeLength == 0 || codeLength%RegisterInstructionSize != 0 {
		return Program{}, fmt.Errorf("%w: invalid register code length %d", ErrInvalidProgram, codeLength)
	}
	if readOnlyLength > MaxReadOnlyDataSize {
		return Program{}, fmt.Errorf("%w: readonly data length %d exceeds %d", ErrInvalidProgram, readOnlyLength, MaxReadOnlyDataSize)
	}
	if int(codeLength)+int(readOnlyLength) != checksumOffset-registerHeaderSize {
		return Program{}, fmt.Errorf("%w: register section length mismatch", ErrInvalidProgram)
	}
	codeStart := registerHeaderSize
	codeEnd := codeStart + int(codeLength)
	readOnlyEnd := codeEnd + int(readOnlyLength)
	code := make([]byte, int(codeLength))
	readOnlyData := make([]byte, int(readOnlyLength))
	copy(code, data[codeStart:codeEnd])
	copy(readOnlyData, data[codeEnd:readOnlyEnd])
	return Program{Version: version, Format: format, Code: code, ReadOnlyData: readOnlyData}, nil
}

func writeUint16(buffer *bytes.Buffer, value uint16) {
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	buffer.Write(encoded[:])
}

func writeUint32(buffer *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	buffer.Write(encoded[:])
}

func writeUint64(buffer *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	buffer.Write(encoded[:])
}

func readUint16(reader *bytes.Reader) (uint16, error) {
	var encoded [2]byte
	if _, err := reader.Read(encoded[:]); err != nil {
		return 0, fmt.Errorf("%w: read u16: %w", ErrInvalidProgram, err)
	}
	return binary.LittleEndian.Uint16(encoded[:]), nil
}

func readUint32(reader *bytes.Reader) (uint32, error) {
	var encoded [4]byte
	if _, err := reader.Read(encoded[:]); err != nil {
		return 0, fmt.Errorf("%w: read u32: %w", ErrInvalidProgram, err)
	}
	return binary.LittleEndian.Uint32(encoded[:]), nil
}

func normalizeProgramManifest(manifest ProgramManifest) (ProgramManifest, error) {
	if manifest.ComputeUnitLimit > MaxComputeUnitLimit {
		return ProgramManifest{}, fmt.Errorf("%w: compute limit %d exceeds %d", ErrInvalidProgram, manifest.ComputeUnitLimit, MaxComputeUnitLimit)
	}
	if len(manifest.RequiredSyscalls) > MaxProgramSyscallCount {
		return ProgramManifest{}, fmt.Errorf("%w: syscall count %d exceeds %d", ErrInvalidProgram, len(manifest.RequiredSyscalls), MaxProgramSyscallCount)
	}
	requiredSyscalls := make([]SyscallID, 0, len(manifest.RequiredSyscalls))
	seen := make(map[SyscallID]struct{}, len(manifest.RequiredSyscalls))
	for _, syscallID := range manifest.RequiredSyscalls {
		if syscallID == 0 {
			return ProgramManifest{}, fmt.Errorf("%w: syscall id cannot be zero", ErrInvalidProgram)
		}
		if _, exists := seen[syscallID]; exists {
			continue
		}
		seen[syscallID] = struct{}{}
		requiredSyscalls = append(requiredSyscalls, syscallID)
	}
	sort.Slice(requiredSyscalls, func(leftIndex int, rightIndex int) bool {
		return requiredSyscalls[leftIndex] < requiredSyscalls[rightIndex]
	})
	manifest.RequiredSyscalls = requiredSyscalls
	return manifest, nil
}

func programManifestSyscallsEqual(left []SyscallID, right []SyscallID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
