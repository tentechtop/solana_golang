package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync"
)

const (
	// EnvelopeMagic 定义 raw payload 魔数 + 用于快速拒绝错误数据格式。
	EnvelopeMagic uint32 = 0x53475352
	// EnvelopeVersion 定义当前 envelope 版本 + 用于后续兼容升级。
	EnvelopeVersion uint16 = 1
	// DefaultMaxPayloadSize 定义默认 payload 上限 + 防止反序列化入口分配过大内存。
	DefaultMaxPayloadSize = 4 * 1024 * 1024

	envelopeFixedHeaderSize = 4 + 2 + 1 + 2 + 2 + 2 + 4 + sha256.Size
	maxTypeLength           = 128
	maxSchemaIDLength       = 128
)

var (
	ErrInvalidSchema    = errors.New("schema: invalid schema")
	ErrSchemaRegistered = errors.New("schema: already registered")
	ErrSchemaNotFound   = errors.New("schema: not registered")
	ErrInvalidEnvelope  = errors.New("schema: invalid envelope")
	ErrHashMismatch     = errors.New("schema: payload hash mismatch")
	ErrInvalidCanonical = errors.New("schema: invalid canonical payload")
	DefaultRegistry     = NewRegistry()
)

// Codec 标识序列化格式 + 避免 borsh、json、canonical raw bytes 混用。
type Codec uint8

const (
	CodecUnknown Codec = iota
	CodecBorsh
	CodecJSON
	CodecCanonical
)

// String 返回编码名称 + 用于日志和数据库可读字段。
func (codec Codec) String() string {
	switch codec {
	case CodecBorsh:
		return "borsh"
	case CodecJSON:
		return "json"
	case CodecCanonical:
		return "canonical"
	default:
		return "unknown"
	}
}

// SchemaKey 定位唯一 schema + 保障历史 raw bytes 能找到正确解码器。
type SchemaKey struct {
	Type     string
	Version  uint16
	Codec    Codec
	SchemaID string
}

// Schema 描述版本化解码规则 + 将 decode、validate、migrate 固化在注册中心。
type Schema struct {
	Key      SchemaKey
	Decode   func([]byte) (any, error)
	Validate func(any) error
	Migrate  func(any) (any, error)
}

// Envelope 保存 raw payload 元信息 + 数据库存储 blob 时必须可追踪版本和校验哈希。
type Envelope struct {
	Type         string
	Version      uint16
	Codec        Codec
	SchemaID     string
	PayloadBytes []byte
	PayloadHash  [sha256.Size]byte
}

// Registry 保存 schema 白名单 + 禁止动态解析未注册消息类型。
type Registry struct {
	mutex     sync.RWMutex
	records   map[SchemaKey]Schema
	schemaIDs map[string]SchemaKey
}

// NewRegistry 创建 schema 注册中心 + 便于测试和模块启动时隔离校验。
func NewRegistry() *Registry {
	return &Registry{
		records:   make(map[SchemaKey]Schema),
		schemaIDs: make(map[string]SchemaKey),
	}
}

// Register 注册 schema + 防止同一 schema_id 被覆盖为不同结构。
func (registry *Registry) Register(record Schema) error {
	if registry == nil {
		return fmt.Errorf("%w: nil registry", ErrInvalidSchema)
	}
	if err := validateSchema(record); err != nil {
		return err
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	if _, exists := registry.records[record.Key]; exists {
		return fmt.Errorf("%w: %s", ErrSchemaRegistered, record.Key.SchemaID)
	}
	if existingKey, exists := registry.schemaIDs[record.Key.SchemaID]; exists && existingKey != record.Key {
		return fmt.Errorf("%w: schema id reused by different key", ErrSchemaRegistered)
	}
	registry.records[record.Key] = record
	registry.schemaIDs[record.Key.SchemaID] = record.Key
	return nil
}

// Lookup 查找 schema + 反序列化前必须先确认白名单存在。
func (registry *Registry) Lookup(key SchemaKey) (Schema, bool) {
	if registry == nil {
		return Schema{}, false
	}

	registry.mutex.RLock()
	defer registry.mutex.RUnlock()

	record, exists := registry.records[key]
	return record, exists
}

// DecodeEnvelope 解码 envelope + 未注册 schema 一律拒绝解析。
func (registry *Registry) DecodeEnvelope(envelope Envelope) (any, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: nil registry", ErrSchemaNotFound)
	}
	if err := envelope.Validate(DefaultMaxPayloadSize); err != nil {
		return nil, err
	}

	key := SchemaKey{
		Type:     envelope.Type,
		Version:  envelope.Version,
		Codec:    envelope.Codec,
		SchemaID: envelope.SchemaID,
	}
	record, exists := registry.Lookup(key)
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrSchemaNotFound, envelope.SchemaID)
	}

	decoded, err := record.Decode(envelope.PayloadBytes)
	if err != nil {
		return nil, fmt.Errorf("schema: decode %s: %w", envelope.SchemaID, err)
	}
	if record.Validate != nil {
		if err := record.Validate(decoded); err != nil {
			return nil, fmt.Errorf("schema: validate %s: %w", envelope.SchemaID, err)
		}
	}
	if record.Migrate == nil {
		return decoded, nil
	}

	migrated, err := record.Migrate(decoded)
	if err != nil {
		return nil, fmt.Errorf("schema: migrate %s: %w", envelope.SchemaID, err)
	}
	return migrated, nil
}

// NewEnvelope 创建 raw payload envelope + 写入数据库前统一补齐长度和哈希元信息。
func NewEnvelope(schemaType string, version uint16, codec Codec, schemaID string, payload []byte) (Envelope, error) {
	envelope := Envelope{
		Type:         schemaType,
		Version:      version,
		Codec:        codec,
		SchemaID:     schemaID,
		PayloadBytes: cloneBytes(payload),
		PayloadHash:  sha256.Sum256(payload),
	}
	if err := envelope.Validate(DefaultMaxPayloadSize); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

// Validate 校验 envelope 元信息 + 防止类型、版本、哈希或 payload 边界异常。
func (envelope Envelope) Validate(maxPayloadSize int) error {
	if envelope.Type == "" || len(envelope.Type) > maxTypeLength {
		return fmt.Errorf("%w: invalid type", ErrInvalidEnvelope)
	}
	if envelope.Version == 0 {
		return fmt.Errorf("%w: invalid version", ErrInvalidEnvelope)
	}
	if envelope.Codec == CodecUnknown {
		return fmt.Errorf("%w: invalid codec", ErrInvalidEnvelope)
	}
	if envelope.SchemaID == "" || len(envelope.SchemaID) > maxSchemaIDLength {
		return fmt.Errorf("%w: invalid schema id", ErrInvalidEnvelope)
	}
	normalizedMaxPayloadSize := normalizeMaxPayloadSize(maxPayloadSize)
	if len(envelope.PayloadBytes) > normalizedMaxPayloadSize {
		return fmt.Errorf("%w: payload too large", ErrInvalidEnvelope)
	}
	if sha256.Sum256(envelope.PayloadBytes) != envelope.PayloadHash {
		return ErrHashMismatch
	}
	return nil
}

// MarshalBinary 序列化 envelope + 使用固定字段顺序确保 raw bytes 存储可重放。
func (envelope Envelope) MarshalBinary(maxPayloadSize int) ([]byte, error) {
	if err := envelope.Validate(maxPayloadSize); err != nil {
		return nil, err
	}

	typeBytes := []byte(envelope.Type)
	schemaIDBytes := []byte(envelope.SchemaID)
	payloadLength := len(envelope.PayloadBytes)
	totalLength := envelopeFixedHeaderSize + len(typeBytes) + len(schemaIDBytes) + payloadLength
	encoded := make([]byte, totalLength)

	offset := 0
	binary.BigEndian.PutUint32(encoded[offset:offset+4], EnvelopeMagic)
	offset += 4
	binary.BigEndian.PutUint16(encoded[offset:offset+2], EnvelopeVersion)
	offset += 2
	encoded[offset] = byte(envelope.Codec)
	offset++
	binary.BigEndian.PutUint16(encoded[offset:offset+2], uint16(len(typeBytes)))
	offset += 2
	binary.BigEndian.PutUint16(encoded[offset:offset+2], envelope.Version)
	offset += 2
	binary.BigEndian.PutUint16(encoded[offset:offset+2], uint16(len(schemaIDBytes)))
	offset += 2
	binary.BigEndian.PutUint32(encoded[offset:offset+4], uint32(payloadLength))
	offset += 4
	copy(encoded[offset:offset+sha256.Size], envelope.PayloadHash[:])
	offset += sha256.Size
	copy(encoded[offset:offset+len(typeBytes)], typeBytes)
	offset += len(typeBytes)
	copy(encoded[offset:offset+len(schemaIDBytes)], schemaIDBytes)
	offset += len(schemaIDBytes)
	copy(encoded[offset:], envelope.PayloadBytes)
	return encoded, nil
}

// UnmarshalEnvelope 反序列化 envelope + 只解析元信息和 payload 不执行业务 decode。
func UnmarshalEnvelope(data []byte, maxPayloadSize int) (Envelope, error) {
	if len(data) < envelopeFixedHeaderSize {
		return Envelope{}, fmt.Errorf("%w: envelope too short", ErrInvalidEnvelope)
	}

	offset := 0
	if binary.BigEndian.Uint32(data[offset:offset+4]) != EnvelopeMagic {
		return Envelope{}, fmt.Errorf("%w: invalid magic", ErrInvalidEnvelope)
	}
	offset += 4
	if binary.BigEndian.Uint16(data[offset:offset+2]) != EnvelopeVersion {
		return Envelope{}, fmt.Errorf("%w: unsupported envelope version", ErrInvalidEnvelope)
	}
	offset += 2

	codec := Codec(data[offset])
	offset++
	typeLength := int(binary.BigEndian.Uint16(data[offset : offset+2]))
	offset += 2
	version := binary.BigEndian.Uint16(data[offset : offset+2])
	offset += 2
	schemaIDLength := int(binary.BigEndian.Uint16(data[offset : offset+2]))
	offset += 2
	payloadLengthValue := binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4

	var payloadHash [sha256.Size]byte
	copy(payloadHash[:], data[offset:offset+sha256.Size])
	offset += sha256.Size

	if typeLength <= 0 || typeLength > maxTypeLength {
		return Envelope{}, fmt.Errorf("%w: invalid type length", ErrInvalidEnvelope)
	}
	if schemaIDLength <= 0 || schemaIDLength > maxSchemaIDLength {
		return Envelope{}, fmt.Errorf("%w: invalid schema id length", ErrInvalidEnvelope)
	}
	normalizedMaxPayloadSize := normalizeMaxPayloadSize(maxPayloadSize)
	if normalizedMaxPayloadSize > math.MaxUint32 {
		normalizedMaxPayloadSize = math.MaxUint32
	}
	if payloadLengthValue > uint32(normalizedMaxPayloadSize) {
		return Envelope{}, fmt.Errorf("%w: invalid payload length", ErrInvalidEnvelope)
	}
	payloadLength := int(payloadLengthValue)
	expectedLength := envelopeFixedHeaderSize + typeLength + schemaIDLength + payloadLength
	if len(data) != expectedLength {
		return Envelope{}, fmt.Errorf("%w: envelope length mismatch", ErrInvalidEnvelope)
	}

	envelope := Envelope{
		Type:         string(data[offset : offset+typeLength]),
		Version:      version,
		Codec:        codec,
		SchemaID:     string(data[offset+typeLength : offset+typeLength+schemaIDLength]),
		PayloadBytes: cloneBytes(data[offset+typeLength+schemaIDLength:]),
		PayloadHash:  payloadHash,
	}
	if err := envelope.Validate(maxPayloadSize); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

// CanonicalHash 计算签名哈希 + 强制加入 domain、message type、version 和规范 payload。
func CanonicalHash(domainSeparator string, messageType string, version uint16, canonicalPayload []byte) ([sha256.Size]byte, error) {
	if domainSeparator == "" || messageType == "" || version == 0 {
		return [sha256.Size]byte{}, fmt.Errorf("%w: missing metadata", ErrInvalidCanonical)
	}

	var buffer bytes.Buffer
	writeCanonicalField(&buffer, []byte(domainSeparator))
	writeCanonicalField(&buffer, []byte(messageType))
	versionBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(versionBytes, version)
	writeCanonicalField(&buffer, versionBytes)
	writeCanonicalField(&buffer, canonicalPayload)
	return sha256.Sum256(buffer.Bytes()), nil
}

// CanonicalHashHex 返回签名哈希文本 + 便于数据库和日志保存。
func CanonicalHashHex(domainSeparator string, messageType string, version uint16, canonicalPayload []byte) (string, error) {
	hash, err := CanonicalHash(domainSeparator, messageType, version, canonicalPayload)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash[:]), nil
}

func validateSchema(record Schema) error {
	if record.Key.Type == "" || len(record.Key.Type) > maxTypeLength {
		return fmt.Errorf("%w: invalid type", ErrInvalidSchema)
	}
	if record.Key.Version == 0 {
		return fmt.Errorf("%w: invalid version", ErrInvalidSchema)
	}
	if record.Key.Codec == CodecUnknown {
		return fmt.Errorf("%w: invalid codec", ErrInvalidSchema)
	}
	if record.Key.SchemaID == "" || len(record.Key.SchemaID) > maxSchemaIDLength {
		return fmt.Errorf("%w: invalid schema id", ErrInvalidSchema)
	}
	if record.Decode == nil {
		return fmt.Errorf("%w: missing decoder", ErrInvalidSchema)
	}
	return nil
}

func writeCanonicalField(buffer *bytes.Buffer, value []byte) {
	lengthBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBytes, uint32(len(value)))
	buffer.Write(lengthBytes)
	buffer.Write(value)
}

func normalizeMaxPayloadSize(maxPayloadSize int) int {
	if maxPayloadSize <= 0 {
		return DefaultMaxPayloadSize
	}
	return maxPayloadSize
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
