package p2p

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"solana_golang/codec/borsh"
)

const (
	// DefaultMaxMessageSize 定义默认消息上限 + 防止单连接通过大包耗尽内存。
	DefaultMaxMessageSize = 4 * 1024 * 1024
	// MessageProtocolVersion 定义当前消息版本 + 用于协议升级时显式拒绝未知版本。
	MessageProtocolVersion uint16 = 1

	// messageFrameMagic 定义 P2P 帧魔数 + 快速识别非本协议流量。
	messageFrameMagic uint32 = 0x53475032

	messageFrameHeaderSize = 4 + 2 + 4 + 4 + sha256.Size
	messageIDByteSize      = 16
)

// MessageFlag 表示消息语义 + UNKNOWN=0 仅保留给协议演进和非法输入。
type MessageFlag byte

const (
	MessageFlagUnknown MessageFlag = 0
	// MessageFlagNotify 表示单向通知 + 用于广播和无需响应的消息。
	MessageFlagNotify MessageFlag = 1
	// MessageFlagRequest 表示请求消息 + request_id 必须等于自身消息 ID。
	MessageFlagRequest MessageFlag = 2
	// MessageFlagResponse 表示响应消息 + request_id 必须指向原请求消息 ID。
	MessageFlagResponse MessageFlag = 3
)

// Message 保存 P2P 消息事实模型 + 使用 Borsh 固定布局进行节点通信编码。
type Message struct {
	ID                 string      `json:"id"`
	Type               ProtocolID  `json:"type"`
	FromPeerID         string      `json:"from_peer_id,omitempty"`
	ToPeerID           string      `json:"to_peer_id,omitempty"`
	RequestID          string      `json:"request_id,omitempty"`
	Flag               MessageFlag `json:"flag"`
	Payload            []byte      `json:"payload,omitempty"`
	CreatedAtUnixMilli int64       `json:"created_at_unix_milli"`
	Version            uint16      `json:"version"`
}

// NewMessage 创建单向消息 + 自动生成 ID 和创建时间避免上层重复处理。
func NewMessage(protocolID ProtocolID, payload []byte) (Message, error) {
	message, err := newBaseMessage(protocolID, payload)
	if err != nil {
		return Message{}, err
	}
	message.MarkAsNormal()
	return message, message.Validate(DefaultMaxMessageSize)
}

// NewRequestMessage 创建请求消息 + 让响应可以通过 request_id 回到原请求。
func NewRequestMessage(senderPeerID string, protocolID ProtocolID, payload []byte) (Message, error) {
	message, err := newBaseMessage(protocolID, payload)
	if err != nil {
		return Message{}, err
	}
	message.FromPeerID = senderPeerID
	message.MarkAsRequest()
	return message, message.Validate(DefaultMaxMessageSize)
}

// NewResponseMessage 创建响应消息 + 将 request_id 绑定到原请求消息 ID。
func NewResponseMessage(senderPeerID string, protocolID ProtocolID, requestID string, payload []byte) (Message, error) {
	message, err := newBaseMessage(protocolID, payload)
	if err != nil {
		return Message{}, err
	}
	message.FromPeerID = senderPeerID
	if err := message.MarkAsResponse(requestID); err != nil {
		return Message{}, err
	}
	return message, message.Validate(DefaultMaxMessageSize)
}

// Validate 校验消息字段 + 防止畸形网络载荷进入上层业务。
func (message Message) Validate(maxMessageSize int) error {
	if message.effectiveVersion() != MessageProtocolVersion {
		return fmt.Errorf("%w: unsupported version", ErrInvalidMessage)
	}
	if _, err := messageIDBytes(message.ID); err != nil {
		return err
	}
	if err := validateMessagePeerID(message.FromPeerID, true); err != nil {
		return err
	}
	if err := validateMessagePeerID(message.ToPeerID, true); err != nil {
		return err
	}
	if !isValidMessageFlag(message.Flag) {
		return fmt.Errorf("%w: invalid flag", ErrInvalidMessage)
	}
	if len(message.Payload) > maxPayloadSize(maxMessageSize) {
		return fmt.Errorf("%w: payload too large", ErrInvalidMessage)
	}
	if message.CreatedAtUnixMilli <= 0 {
		return fmt.Errorf("%w: invalid created time", ErrInvalidMessage)
	}
	if err := message.validateRequestID(); err != nil {
		return err
	}
	return nil
}

// Clone 复制消息 + 防止调用方修改 Payload 影响连接写入。
func (message Message) Clone() Message {
	message.Payload = cloneBytes(message.Payload)
	return message
}

// IsRequestResponse 判断是否请求响应消息 + request_id 非空时参与匹配。
func (message Message) IsRequestResponse() bool {
	return message.Flag == MessageFlagRequest || message.Flag == MessageFlagResponse
}

// IsRequest 判断是否请求消息 + 请求消息要求 request_id 等于自身 ID。
func (message Message) IsRequest() bool {
	return message.Flag == MessageFlagRequest && strings.EqualFold(message.RequestID, message.ID)
}

// IsResponse 判断是否响应消息 + 响应消息要求 request_id 指向原请求。
func (message Message) IsResponse() bool {
	return message.Flag == MessageFlagResponse && isValidMessageID(message.RequestID)
}

// MarkAsNormal 标记单向通知 + 清空 request_id 避免被当作请求响应。
func (message *Message) MarkAsNormal() {
	message.Flag = MessageFlagNotify
	message.RequestID = ""
}

// MarkAsRequest 标记请求消息 + 使用自身 ID 作为 request_id。
func (message *Message) MarkAsRequest() {
	message.Flag = MessageFlagRequest
	message.RequestID = strings.ToLower(message.ID)
}

// MarkAsResponse 标记响应消息 + 绑定原请求 ID 便于上层匹配。
func (message *Message) MarkAsResponse(requestID string) error {
	if !isValidMessageID(requestID) || isZeroMessageID(requestID) {
		return fmt.Errorf("%w: invalid request id", ErrInvalidMessage)
	}
	message.Flag = MessageFlagResponse
	message.RequestID = strings.ToLower(requestID)
	return nil
}

// MarshalBinary 序列化消息 + P2P 通信使用 Borsh 固定字段布局。
func (message Message) MarshalBinary(maxMessageSize int) ([]byte, error) {
	if err := message.Validate(maxMessageSize); err != nil {
		return nil, err
	}

	writer := borsh.NewWriter(maxPayloadSize(maxMessageSize))
	writer.WriteUint16(message.effectiveVersion())
	if err := writer.WriteString(strings.ToLower(message.ID)); err != nil {
		return nil, fmt.Errorf("p2p: marshal message id: %w", err)
	}
	writer.WriteUint32(uint32(message.Type))
	if err := writer.WriteString(message.FromPeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal from peer id: %w", err)
	}
	if err := writer.WriteString(message.ToPeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal to peer id: %w", err)
	}
	if err := writer.WriteString(strings.ToLower(message.RequestID)); err != nil {
		return nil, fmt.Errorf("p2p: marshal request id: %w", err)
	}
	writer.WriteUint8(uint8(message.Flag))
	writer.WriteInt64(message.CreatedAtUnixMilli)
	if err := writer.WriteBytes(message.Payload); err != nil {
		return nil, fmt.Errorf("p2p: marshal payload: %w", err)
	}

	encoded := writer.BytesView()
	if len(encoded) > maxPayloadSize(maxMessageSize) {
		return nil, fmt.Errorf("%w: message payload too large", ErrInvalidMessage)
	}
	return encoded, nil
}

// UnmarshalBinary 反序列化消息 + Borsh 解码后继续执行业务边界校验。
func UnmarshalBinary(data []byte, maxMessageSize int) (Message, error) {
	if len(data) == 0 {
		return Message{}, fmt.Errorf("%w: empty message payload", ErrInvalidMessage)
	}
	if len(data) > maxPayloadSize(maxMessageSize) {
		return Message{}, fmt.Errorf("%w: message payload too large", ErrInvalidMessage)
	}

	reader := borsh.NewBorrowedReader(data, maxPayloadSize(maxMessageSize))
	version, err := reader.ReadUint16()
	if err != nil {
		return Message{}, fmt.Errorf("p2p: unmarshal version: %w", err)
	}
	messageID, err := reader.ReadString()
	if err != nil {
		return Message{}, fmt.Errorf("p2p: unmarshal message id: %w", err)
	}
	protocolID, err := reader.ReadUint32()
	if err != nil {
		return Message{}, fmt.Errorf("p2p: unmarshal message type: %w", err)
	}
	fromPeerID, err := reader.ReadString()
	if err != nil {
		return Message{}, fmt.Errorf("p2p: unmarshal from peer id: %w", err)
	}
	toPeerID, err := reader.ReadString()
	if err != nil {
		return Message{}, fmt.Errorf("p2p: unmarshal to peer id: %w", err)
	}
	requestID, err := reader.ReadString()
	if err != nil {
		return Message{}, fmt.Errorf("p2p: unmarshal request id: %w", err)
	}
	flag, err := reader.ReadUint8()
	if err != nil {
		return Message{}, fmt.Errorf("p2p: unmarshal flag: %w", err)
	}
	createdAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return Message{}, fmt.Errorf("p2p: unmarshal created time: %w", err)
	}
	payload, err := reader.ReadBytes()
	if err != nil {
		return Message{}, fmt.Errorf("p2p: unmarshal payload: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return Message{}, fmt.Errorf("p2p: unmarshal message: %w", err)
	}

	message := Message{
		ID:                 strings.ToLower(messageID),
		Type:               ProtocolID(protocolID),
		FromPeerID:         fromPeerID,
		ToPeerID:           toPeerID,
		RequestID:          strings.ToLower(requestID),
		Flag:               MessageFlag(flag),
		Payload:            payload,
		CreatedAtUnixMilli: createdAtUnixMilli,
		Version:            version,
	}
	if err := message.Validate(maxMessageSize); err != nil {
		return Message{}, err
	}
	return message, nil
}

// writeMessageFrame 写入 P2P 帧 + 固定头部承载 magic、版本、类型、长度和 checksum。
func writeMessageFrame(writer io.Writer, message Message, maxMessageSize int) error {
	payload, err := message.MarshalBinary(maxMessageSize)
	if err != nil {
		return err
	}
	if len(payload) > maxPayloadSize(maxMessageSize) {
		return fmt.Errorf("%w: frame payload too large", ErrInvalidMessage)
	}

	header := acquireMessageFrameHeader()
	defer releaseMessageFrameHeader(header)
	checksum := sha256.Sum256(payload)
	binary.BigEndian.PutUint32(header[0:4], messageFrameMagic)
	binary.BigEndian.PutUint16(header[4:6], message.effectiveVersion())
	binary.BigEndian.PutUint32(header[6:10], uint32(message.Type))
	binary.BigEndian.PutUint32(header[10:14], uint32(len(payload)))
	copy(header[14:messageFrameHeaderSize], checksum[:])

	if err := writeFull(writer, header); err != nil {
		return fmt.Errorf("p2p: write message header: %w", err)
	}
	if err := writeFull(writer, payload); err != nil {
		return fmt.Errorf("p2p: write message body: %w", err)
	}
	return nil
}

// readMessageFrame 读取 P2P 帧 + 先校验外层边界和 checksum 再解码消息体。
func readMessageFrame(reader io.Reader, maxMessageSize int) (Message, error) {
	header := acquireMessageFrameHeader()
	defer releaseMessageFrameHeader(header)
	if _, err := io.ReadFull(reader, header); err != nil {
		return Message{}, fmt.Errorf("p2p: read message header: %w", err)
	}

	frameHeader, err := parseMessageFrameHeader(header, maxMessageSize)
	if err != nil {
		return Message{}, err
	}
	payload := acquireMessagePayloadBuffer(frameHeader.payloadLength)
	defer releaseMessagePayloadBuffer(payload)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return Message{}, fmt.Errorf("p2p: read message body: %w", err)
	}
	if sha256.Sum256(payload) != frameHeader.checksum {
		return Message{}, fmt.Errorf("%w: checksum mismatch", ErrInvalidMessage)
	}

	message, err := UnmarshalBinary(payload, maxMessageSize)
	if err != nil {
		return Message{}, err
	}
	if message.Type != frameHeader.protocolID {
		return Message{}, fmt.Errorf("%w: frame type mismatch", ErrInvalidMessage)
	}
	if message.effectiveVersion() != frameHeader.version {
		return Message{}, fmt.Errorf("%w: frame version mismatch", ErrInvalidMessage)
	}
	return message, nil
}

// newBaseMessage 创建基础消息 + 生成唯一 ID、复制载荷并设置协议版本。
func newBaseMessage(protocolID ProtocolID, payload []byte) (Message, error) {
	messageID, err := newMessageID()
	if err != nil {
		return Message{}, err
	}
	message := Message{
		ID:                 messageID,
		Type:               protocolID,
		Flag:               MessageFlagNotify,
		Payload:            cloneBytes(payload),
		CreatedAtUnixMilli: time.Now().UnixMilli(),
		Version:            MessageProtocolVersion,
	}
	if err := message.Validate(DefaultMaxMessageSize); err != nil {
		return Message{}, err
	}
	return message, nil
}

// effectiveVersion 返回有效协议版本 + 兼容零值消息结构。
func (message Message) effectiveVersion() uint16 {
	if message.Version == 0 {
		return MessageProtocolVersion
	}
	return message.Version
}

// validateRequestID 校验请求响应关系 + 防止响应错绑或通知伪装请求。
func (message Message) validateRequestID() error {
	switch message.Flag {
	case MessageFlagNotify:
		if message.RequestID != "" {
			return fmt.Errorf("%w: notify request id must be empty", ErrInvalidMessage)
		}
		return nil
	case MessageFlagRequest:
		if !isValidMessageID(message.RequestID) || isZeroMessageID(message.RequestID) {
			return fmt.Errorf("%w: invalid request id", ErrInvalidMessage)
		}
		if !strings.EqualFold(message.RequestID, message.ID) {
			return fmt.Errorf("%w: request id must equal message id", ErrInvalidMessage)
		}
		return nil
	case MessageFlagResponse:
		if !isValidMessageID(message.RequestID) || isZeroMessageID(message.RequestID) {
			return fmt.Errorf("%w: invalid response request id", ErrInvalidMessage)
		}
		return nil
	default:
		return fmt.Errorf("%w: invalid flag", ErrInvalidMessage)
	}
}

// newMessageID 生成消息 ID + 前缀写入毫秒时间便于排序并保留随机熵。
func newMessageID() (string, error) {
	buffer := make([]byte, messageIDByteSize)
	unixMillis := uint64(time.Now().UnixMilli())
	buffer[0] = byte(unixMillis >> 40)
	buffer[1] = byte(unixMillis >> 32)
	buffer[2] = byte(unixMillis >> 24)
	buffer[3] = byte(unixMillis >> 16)
	buffer[4] = byte(unixMillis >> 8)
	buffer[5] = byte(unixMillis)
	if _, err := rand.Read(buffer[6:]); err != nil {
		return "", fmt.Errorf("p2p: generate message id: %w", err)
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x70
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return hex.EncodeToString(buffer), nil
}

// messageIDBytes 解码消息 ID + 统一校验 16 字节十六进制格式。
func messageIDBytes(value string) ([]byte, error) {
	if !isValidMessageID(value) {
		return nil, fmt.Errorf("%w: invalid message id", ErrInvalidMessage)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid message id", ErrInvalidMessage)
	}
	return decoded, nil
}

// isValidMessageID 判断消息 ID 格式 + 仅接受固定长度十六进制字符串。
func isValidMessageID(value string) bool {
	if len(value) != messageIDByteSize*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// isZeroMessageID 判断是否全零消息 ID + 用于拒绝无效响应关联。
func isZeroMessageID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && isZeroBytes(decoded)
}

// validateMessagePeerID 校验消息路由节点 + 支持可选字段但拒绝畸形 ID。
func validateMessagePeerID(peerID string, optional bool) error {
	if peerID == "" && optional {
		return nil
	}
	if err := validatePeerID(peerID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	return nil
}

// maxPayloadSize 计算消息体载荷上限 + 从总帧大小中扣除固定帧头。
func maxPayloadSize(maxMessageSize int) int {
	normalized := normalizeMaxMessageSize(maxMessageSize)
	if normalized <= messageFrameHeaderSize {
		return 0
	}
	return normalized - messageFrameHeaderSize
}

// normalizeMaxMessageSize 归一化消息大小上限 + 防止零值关闭防护。
func normalizeMaxMessageSize(maxMessageSize int) int {
	if maxMessageSize <= 0 {
		return DefaultMaxMessageSize
	}
	return maxMessageSize
}

// isValidMessageFlag 校验消息标识 + UNKNOWN 不能作为业务消息发送。
func isValidMessageFlag(flag MessageFlag) bool {
	return flag == MessageFlagNotify || flag == MessageFlagRequest || flag == MessageFlagResponse
}

type messageFrameHeader struct {
	version       uint16
	protocolID    ProtocolID
	payloadLength int
	checksum      [sha256.Size]byte
}

func parseMessageFrameHeader(header []byte, maxMessageSize int) (messageFrameHeader, error) {
	if len(header) != messageFrameHeaderSize {
		return messageFrameHeader{}, fmt.Errorf("%w: invalid header length", ErrInvalidMessage)
	}
	if binary.BigEndian.Uint32(header[0:4]) != messageFrameMagic {
		return messageFrameHeader{}, fmt.Errorf("%w: invalid magic", ErrInvalidMessage)
	}

	version := binary.BigEndian.Uint16(header[4:6])
	if version != MessageProtocolVersion {
		return messageFrameHeader{}, fmt.Errorf("%w: unsupported frame version", ErrInvalidMessage)
	}
	frameMaxPayloadSize := maxPayloadSize(maxMessageSize)
	if frameMaxPayloadSize > math.MaxUint32 {
		frameMaxPayloadSize = math.MaxUint32
	}
	payloadLengthValue := binary.BigEndian.Uint32(header[10:14])
	if payloadLengthValue == 0 || payloadLengthValue > uint32(frameMaxPayloadSize) {
		return messageFrameHeader{}, fmt.Errorf("%w: invalid payload length", ErrInvalidMessage)
	}
	payloadLength := int(payloadLengthValue)

	var checksum [sha256.Size]byte
	copy(checksum[:], header[14:messageFrameHeaderSize])
	return messageFrameHeader{
		version:       version,
		protocolID:    ProtocolID(binary.BigEndian.Uint32(header[6:10])),
		payloadLength: payloadLength,
		checksum:      checksum,
	}, nil
}

// writeFull 完整写入字节流 + 避免短写导致网络帧被静默截断。
func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

func isZeroBytes(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}
