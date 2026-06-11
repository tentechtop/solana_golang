package borsh

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"

	"solana_golang/schema"
)

const (
	// DefaultMaxContainerLength 定义默认容器长度上限 + 防止链上 bytes/string 反序列化耗尽内存。
	DefaultMaxContainerLength = 64 * 1024
)

var (
	ErrInvalidData   = errors.New("borsh: invalid data")
	ErrInvalidLength = errors.New("borsh: invalid length")
	ErrInvalidBool   = errors.New("borsh: invalid bool")
)

// Writer 写入 Borsh 字节流 + 使用小端和显式长度保证确定性。
type Writer struct {
	buffer             bytes.Buffer
	maxContainerLength int
}

// NewWriter 创建 Borsh 写入器 + 为动态容器统一设置长度上限。
func NewWriter(maxContainerLength int) *Writer {
	return &Writer{maxContainerLength: normalizeMaxContainerLength(maxContainerLength)}
}

// Bytes 返回已编码字节 + 复制结果防止调用方修改内部缓冲区。
func (writer *Writer) Bytes() []byte {
	return cloneBytes(writer.buffer.Bytes())
}

// BytesView 返回内部编码视图 + 热路径调用方立即接管字节时避免一次复制。
func (writer *Writer) BytesView() []byte {
	return writer.buffer.Bytes()
}

// WriteUint8 写入 u8 + 匹配 Borsh 整数格式。
func (writer *Writer) WriteUint8(value uint8) {
	writer.buffer.WriteByte(value)
}

// WriteUint16 写入 u16 + 使用小端保持 Borsh 兼容。
func (writer *Writer) WriteUint16(value uint16) {
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	writer.buffer.Write(encoded[:])
}

// WriteUint32 写入 u32 + 使用小端保持 Borsh 兼容。
func (writer *Writer) WriteUint32(value uint32) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	writer.buffer.Write(encoded[:])
}

// WriteUint64 写入 u64 + 使用小端保持 Borsh 兼容。
func (writer *Writer) WriteUint64(value uint64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	writer.buffer.Write(encoded[:])
}

// WriteInt64 写入 i64 + 使用小端保持 Borsh 兼容。
func (writer *Writer) WriteInt64(value int64) {
	writer.WriteUint64(uint64(value))
}

// WriteBool 写入 bool + Borsh 使用 0/1 表示布尔值。
func (writer *Writer) WriteBool(value bool) {
	if value {
		writer.buffer.WriteByte(1)
		return
	}
	writer.buffer.WriteByte(0)
}

// WriteBytes 写入 bytes + 使用 u32 长度前缀并限制最大长度。
func (writer *Writer) WriteBytes(value []byte) error {
	if len(value) > writer.maxContainerLength {
		return fmt.Errorf("%w: bytes length %d exceeds %d", ErrInvalidLength, len(value), writer.maxContainerLength)
	}
	writer.WriteUint32(uint32(len(value)))
	writer.buffer.Write(value)
	return nil
}

// WriteFixedBytes 写入固定字节数组 + Borsh 固定数组不写长度前缀。
func (writer *Writer) WriteFixedBytes(value []byte) {
	writer.buffer.Write(value)
}

// WriteString 写入 UTF-8 字符串 + Borsh string 必须是有效 UTF-8。
func (writer *Writer) WriteString(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: invalid utf8 string", ErrInvalidData)
	}
	return writer.WriteBytes([]byte(value))
}

// Reader 读取 Borsh 字节流 + 每个动态容器都执行边界校验。
type Reader struct {
	reader             *bytes.Reader
	maxContainerLength int
}

// NewReader 创建 Borsh 读取器 + 复制输入保证读取期间数据一致。
func NewReader(data []byte, maxContainerLength int) *Reader {
	return &Reader{
		reader:             bytes.NewReader(cloneBytes(data)),
		maxContainerLength: normalizeMaxContainerLength(maxContainerLength),
	}
}

// NewBorrowedReader 创建借用式 Borsh 读取器 + 网络热路径只在调用期间读取输入以减少整包复制。
func NewBorrowedReader(data []byte, maxContainerLength int) *Reader {
	return &Reader{
		reader:             bytes.NewReader(data),
		maxContainerLength: normalizeMaxContainerLength(maxContainerLength),
	}
}

// ReadUint8 读取 u8 + 输入不足时返回上下文错误。
func (reader *Reader) ReadUint8() (uint8, error) {
	value, err := reader.reader.ReadByte()
	if err != nil {
		return 0, fmt.Errorf("%w: read u8: %w", ErrInvalidData, err)
	}
	return value, nil
}

// ReadUint16 读取 u16 + 使用小端保持 Borsh 兼容。
func (reader *Reader) ReadUint16() (uint16, error) {
	var encoded [2]byte
	if _, err := io.ReadFull(reader.reader, encoded[:]); err != nil {
		return 0, fmt.Errorf("%w: read u16: %w", ErrInvalidData, err)
	}
	return binary.LittleEndian.Uint16(encoded[:]), nil
}

// ReadUint32 读取 u32 + 使用小端保持 Borsh 兼容。
func (reader *Reader) ReadUint32() (uint32, error) {
	var encoded [4]byte
	if _, err := io.ReadFull(reader.reader, encoded[:]); err != nil {
		return 0, fmt.Errorf("%w: read u32: %w", ErrInvalidData, err)
	}
	return binary.LittleEndian.Uint32(encoded[:]), nil
}

// ReadUint64 读取 u64 + 使用小端保持 Borsh 兼容。
func (reader *Reader) ReadUint64() (uint64, error) {
	var encoded [8]byte
	if _, err := io.ReadFull(reader.reader, encoded[:]); err != nil {
		return 0, fmt.Errorf("%w: read u64: %w", ErrInvalidData, err)
	}
	return binary.LittleEndian.Uint64(encoded[:]), nil
}

// ReadInt64 读取 i64 + 使用小端保持 Borsh 兼容。
func (reader *Reader) ReadInt64() (int64, error) {
	value, err := reader.ReadUint64()
	if err != nil {
		return 0, err
	}
	return int64(value), nil
}

// ReadBool 读取 bool + 拒绝非 0/1 的畸形布尔值。
func (reader *Reader) ReadBool() (bool, error) {
	value, err := reader.ReadUint8()
	if err != nil {
		return false, err
	}
	if value == 0 {
		return false, nil
	}
	if value == 1 {
		return true, nil
	}
	return false, fmt.Errorf("%w: value %d", ErrInvalidBool, value)
}

// ReadBytes 读取 bytes + 长度前缀必须满足配置上限和剩余字节数。
func (reader *Reader) ReadBytes() ([]byte, error) {
	length, err := reader.ReadUint32()
	if err != nil {
		return nil, err
	}
	if length > uint32(reader.maxContainerLength) {
		return nil, fmt.Errorf("%w: bytes length %d exceeds %d", ErrInvalidLength, length, reader.maxContainerLength)
	}
	if length > uint32(reader.reader.Len()) {
		return nil, fmt.Errorf("%w: bytes length %d exceeds remaining %d", ErrInvalidLength, length, reader.reader.Len())
	}

	value := make([]byte, int(length))
	if _, err := io.ReadFull(reader.reader, value); err != nil {
		return nil, fmt.Errorf("%w: read bytes: %w", ErrInvalidData, err)
	}
	return value, nil
}

// ReadFixedBytes 读取固定字节数组 + Borsh 固定数组不带长度前缀。
func (reader *Reader) ReadFixedBytes(length int) ([]byte, error) {
	if length < 0 || length > reader.maxContainerLength {
		return nil, fmt.Errorf("%w: fixed bytes length %d exceeds %d", ErrInvalidLength, length, reader.maxContainerLength)
	}
	if length > reader.reader.Len() {
		return nil, fmt.Errorf("%w: fixed bytes length %d exceeds remaining %d", ErrInvalidLength, length, reader.reader.Len())
	}

	value := make([]byte, length)
	if _, err := io.ReadFull(reader.reader, value); err != nil {
		return nil, fmt.Errorf("%w: read fixed bytes: %w", ErrInvalidData, err)
	}
	return value, nil
}

// ReadString 读取 UTF-8 字符串 + 拒绝非法 UTF-8 避免跨语言歧义。
func (reader *Reader) ReadString() (string, error) {
	value, err := reader.ReadBytes()
	if err != nil {
		return "", err
	}
	if !utf8.Valid(value) {
		return "", fmt.Errorf("%w: invalid utf8 string", ErrInvalidData)
	}
	return string(value), nil
}

// EnsureEOF 确认没有多余字节 + 防止反序列化吞掉前缀后忽略尾部污染。
func (reader *Reader) EnsureEOF() error {
	if reader.reader.Len() != 0 {
		return fmt.Errorf("%w: %d trailing bytes", ErrInvalidData, reader.reader.Len())
	}
	return nil
}

// NewEnvelope 创建 Borsh raw envelope + 数据库存储链上 bytes 时补齐 schema 元信息。
func NewEnvelope(schemaType string, version uint16, schemaID string, payload []byte) (schema.Envelope, error) {
	return schema.NewEnvelope(schemaType, version, schema.CodecBorsh, schemaID, payload)
}

func normalizeMaxContainerLength(maxContainerLength int) int {
	if maxContainerLength <= 0 {
		return DefaultMaxContainerLength
	}
	if maxContainerLength > math.MaxUint32 {
		return math.MaxUint32
	}
	return maxContainerLength
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
