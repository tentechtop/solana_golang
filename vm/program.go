package vm

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

const (
	BytecodeVersion       = uint16(1)
	bytecodeHeader        = "SVM1"
	programFormatLegacy   = uint16(0)
	programFormatRegister = uint16(1)
	registerHeaderSize    = 16
	registerChecksumSize  = 32
)

// Program 表示已加载程序 + 后端可以是当前字节码或未来 SBF。
type Program struct {
	Version      uint16
	Format       uint16
	Code         []byte
	ReadOnlyData []byte
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
	return loadRegisterProgram(version, data)
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
