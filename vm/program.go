package vm

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"solana_golang/utils"
)

const (
	BytecodeVersion = uint16(1)
	bytecodeHeader  = "SVM1"
)

// Program 表示已加载程序 + 后端可以是当前字节码或未来 SBF。
type Program struct {
	Version uint16
	Code    []byte
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
	reader := bytes.NewReader(programAccount.Data)
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
	codeLength, err := readUint32(reader)
	if err != nil {
		return Program{}, err
	}
	if codeLength == 0 || codeLength > uint32(reader.Len()) {
		return Program{}, fmt.Errorf("%w: invalid code length %d", ErrInvalidProgram, codeLength)
	}
	code := make([]byte, int(codeLength))
	if _, err := reader.Read(code); err != nil {
		return Program{}, fmt.Errorf("%w: read code: %w", ErrInvalidProgram, err)
	}
	if reader.Len() != 0 {
		return Program{}, fmt.Errorf("%w: %d trailing bytes", ErrInvalidProgram, reader.Len())
	}
	return Program{Version: version, Code: utils.CloneBytes(code)}, nil
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
