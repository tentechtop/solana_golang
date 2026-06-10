package p2p

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"solana_golang/utils"
)

const (
	// DefaultMaxMessageSize 定义默认消息上限 + 防止单连接占用过多内存。
	DefaultMaxMessageSize = 4 * 1024 * 1024
	// MessageProtocolVersion 定义当前消息版本 + 便于后续协议平滑升级。
	MessageProtocolVersion uint16 = 1

	messageFrameHeaderSize = 4
	messagePeerIDByteSize  = 32
	messageIDByteSize      = 16
	messageWireHeaderSize  = 2 + 32 + 32 + 16 + 16 + 1 + 4 + 8 + 4
)

// MessageType 表示 P2P 消息类型 + 复用协议编号作为网络路由键。
type MessageType = ProtocolID

const (
	// MessageTypePing 表示探活请求 + 用于检测连接可用性。
	MessageTypePing MessageType = ProtocolPingV1
	// MessageTypePong 表示探活响应 + 用于确认远端仍可通信。
	MessageTypePong MessageType = ProtocolPongV1
	// MessageTypeTransaction 表示交易广播 + 用于交易池传播。
	MessageTypeTransaction MessageType = ProtocolReceiveTransactionV1
	// MessageTypeBlock 表示区块传播 + 用于同步新区块。
	MessageTypeBlock MessageType = ProtocolReceiveBlockV1
	// MessageTypeVote 表示共识投票 + 用于 HotStuff 投票传播。
	MessageTypeVote MessageType = ProtocolHotStuffVoteV1
	// MessageTypeQC 表示投票证书 + 用于 HotStuff 视图推进。
	MessageTypeQC MessageType = ProtocolHotStuffQCV1
	// MessageTypePeer 表示节点信息 + 用于节点发现和地址交换。
	MessageTypePeer MessageType = ProtocolPeerHintsV1
)

// MessageFlag 表示请求响应标识 + 使用 1 字节降低网络开销。
type MessageFlag byte

const (
	// MessageFlagRequest 表示请求或普通消息 + request_id 全零时表示普通消息。
	MessageFlagRequest MessageFlag = 0x00
	// MessageFlagResponse 表示响应消息 + request_id 必须指向原请求消息 ID。
	MessageFlagResponse MessageFlag = 0x01
)

// Message 保存 P2P 消息 + 使用固定头部承载协议路由和请求响应关系。
type Message struct {
	ID                 string      `json:"id"`
	Type               MessageType `json:"type"`
	FromPeerID         string      `json:"from_peer_id,omitempty"`
	ToPeerID           string      `json:"to_peer_id,omitempty"`
	RequestID          string      `json:"request_id,omitempty"`
	Flag               MessageFlag `json:"flag"`
	Payload            []byte      `json:"payload,omitempty"`
	CreatedAtUnixMilli int64       `json:"created_at_unix_milli"`
	Version            uint16      `json:"version"`
}

// NewMessage 创建普通消息 + 自动生成 ID 和创建时间避免上层重复处理。
func NewMessage(messageType MessageType, payload []byte) (Message, error) {
	message, err := newBaseMessage(messageType, payload)
	if err != nil {
		return Message{}, err
	}
	message.MarkAsNormal()
	return message, nil
}

// NewRequestMessage 创建请求消息 + 让响应可以通过 request_id 回到原请求。
func NewRequestMessage(senderPeerID string, messageType MessageType, payload []byte) (Message, error) {
	message, err := newBaseMessage(messageType, payload)
	if err != nil {
		return Message{}, err
	}
	message.FromPeerID = senderPeerID
	message.MarkAsRequest()
	return message, message.Validate(DefaultMaxMessageSize)
}

// NewResponseMessage 创建响应消息 + 将 request_id 绑定到原请求消息 ID。
func NewResponseMessage(senderPeerID string, messageType MessageType, requestID string, payload []byte) (Message, error) {
	message, err := newBaseMessage(messageType, payload)
	if err != nil {
		return Message{}, err
	}
	message.FromPeerID = senderPeerID
	if err := message.MarkAsResponse(requestID); err != nil {
		return Message{}, err
	}
	return message, message.Validate(DefaultMaxMessageSize)
}

// Validate 校验消息字段 + 防止畸形头部和超大负载进入网络层。
func (message Message) Validate(maxMessageSize int) error {
	if message.effectiveVersion() < MessageProtocolVersion {
		return fmt.Errorf("%w: invalid version", ErrInvalidMessage)
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
	if message.Flag != MessageFlagRequest && message.Flag != MessageFlagResponse {
		return fmt.Errorf("%w: invalid flag", ErrInvalidMessage)
	}
	if len(message.Payload) > maxPayloadSize(maxMessageSize) {
		return fmt.Errorf("%w: payload too large", ErrInvalidMessage)
	}
	if message.CreatedAtUnixMilli < 0 {
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

// IsRequestResponse 判断是否请求响应消息 + request_id 非全零时参与匹配。
func (message Message) IsRequestResponse() bool {
	return isValidMessageID(message.RequestID) && !isZeroMessageID(message.RequestID)
}

// IsRequest 判断是否请求消息 + 请求消息要求 request_id 等于自身 ID。
func (message Message) IsRequest() bool {
	return message.IsRequestResponse() && message.Flag == MessageFlagRequest
}

// IsResponse 判断是否响应消息 + 响应消息要求 request_id 指向原请求。
func (message Message) IsResponse() bool {
	return message.IsRequestResponse() && message.Flag == MessageFlagResponse
}

// MarkAsNormal 标记普通消息 + 清空 request_id 避免被当作请求响应。
func (message *Message) MarkAsNormal() {
	message.Flag = MessageFlagRequest
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

// MarshalBinary 序列化消息 + 使用固定头部和大端序降低解析成本。
func (message Message) MarshalBinary(maxMessageSize int) ([]byte, error) {
	if err := message.Validate(maxMessageSize); err != nil {
		return nil, err
	}

	senderID, err := messagePeerIDBytes(message.FromPeerID)
	if err != nil {
		return nil, err
	}
	targetID, err := messagePeerIDBytes(message.ToPeerID)
	if err != nil {
		return nil, err
	}
	messageID, err := messageIDBytes(message.ID)
	if err != nil {
		return nil, err
	}
	requestID, err := messageRequestIDBytes(message.RequestID)
	if err != nil {
		return nil, err
	}

	payloadLength := len(message.Payload)
	encoded := make([]byte, messageWireHeaderSize+payloadLength)
	binary.BigEndian.PutUint16(encoded[0:2], message.effectiveVersion())
	copy(encoded[2:34], senderID)
	copy(encoded[34:66], targetID)
	copy(encoded[66:82], messageID)
	copy(encoded[82:98], requestID)
	encoded[98] = byte(message.Flag)
	binary.BigEndian.PutUint32(encoded[99:103], uint32(message.Type))
	binary.BigEndian.PutUint64(encoded[103:111], uint64(message.CreatedAtUnixMilli))
	binary.BigEndian.PutUint32(encoded[111:115], uint32(payloadLength))
	copy(encoded[messageWireHeaderSize:], message.Payload)
	return encoded, nil
}

// UnmarshalBinary 反序列化消息 + 严格校验长度和请求响应约束。
func UnmarshalBinary(data []byte, maxMessageSize int) (Message, error) {
	if len(data) < messageWireHeaderSize {
		return Message{}, fmt.Errorf("%w: frame too short", ErrInvalidMessage)
	}

	payloadLength := int(binary.BigEndian.Uint32(data[111:115]))
	if payloadLength < 0 || payloadLength > maxPayloadSize(maxMessageSize) {
		return Message{}, fmt.Errorf("%w: invalid payload length", ErrInvalidMessage)
	}
	if len(data) != messageWireHeaderSize+payloadLength {
		return Message{}, fmt.Errorf("%w: frame length mismatch", ErrInvalidMessage)
	}

	message := Message{
		Version:            binary.BigEndian.Uint16(data[0:2]),
		FromPeerID:         messagePeerIDString(data[2:34]),
		ToPeerID:           messagePeerIDString(data[34:66]),
		ID:                 hex.EncodeToString(data[66:82]),
		RequestID:          messageRequestIDString(data[82:98]),
		Flag:               MessageFlag(data[98]),
		Type:               MessageType(binary.BigEndian.Uint32(data[99:103])),
		CreatedAtUnixMilli: int64(binary.BigEndian.Uint64(data[103:111])),
		Payload:            cloneBytes(data[messageWireHeaderSize:]),
	}
	if err := message.Validate(maxMessageSize); err != nil {
		return Message{}, err
	}
	return message, nil
}
func writeMessageFrame(writer io.Writer, message Message, maxMessageSize int) error {
	encoded, err := message.MarshalBinary(maxMessageSize)
	if err != nil {
		return err
	}
	if len(encoded) > normalizeMaxMessageSize(maxMessageSize) {
		return fmt.Errorf("%w: encoded frame too large", ErrInvalidMessage)
	}

	header := make([]byte, messageFrameHeaderSize)
	binary.BigEndian.PutUint32(header, uint32(len(encoded)))
	if _, err := writer.Write(header); err != nil {
		return fmt.Errorf("p2p: write message header: %w", err)
	}
	if _, err := writer.Write(encoded); err != nil {
		return fmt.Errorf("p2p: write message body: %w", err)
	}
	return nil
}
func readMessageFrame(reader io.Reader, maxMessageSize int) (Message, error) {
	header := make([]byte, messageFrameHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return Message{}, fmt.Errorf("p2p: read message header: %w", err)
	}

	frameSize := int(binary.BigEndian.Uint32(header))
	if frameSize < messageWireHeaderSize || frameSize > normalizeMaxMessageSize(maxMessageSize) {
		return Message{}, fmt.Errorf("%w: invalid frame size %d", ErrInvalidMessage, frameSize)
	}

	encoded := make([]byte, frameSize)
	if _, err := io.ReadFull(reader, encoded); err != nil {
		return Message{}, fmt.Errorf("p2p: read message body: %w", err)
	}
	return UnmarshalBinary(encoded, maxMessageSize)
}
func newBaseMessage(messageType MessageType, payload []byte) (Message, error) {
	messageID, err := newMessageID()
	if err != nil {
		return Message{}, err
	}
	message := Message{
		ID:                 messageID,
		Type:               messageType,
		Payload:            cloneBytes(payload),
		CreatedAtUnixMilli: time.Now().UnixMilli(),
		Version:            MessageProtocolVersion,
	}
	if err := message.Validate(DefaultMaxMessageSize); err != nil {
		return Message{}, err
	}
	return message, nil
}
func (message Message) effectiveVersion() uint16 {
	if message.Version == 0 {
		return MessageProtocolVersion
	}
	return message.Version
}
func (message Message) validateRequestID() error {
	if message.RequestID == "" {
		if message.Flag == MessageFlagResponse {
			return fmt.Errorf("%w: empty response request id", ErrInvalidMessage)
		}
		return nil
	}
	if !isValidMessageID(message.RequestID) {
		return fmt.Errorf("%w: invalid request id", ErrInvalidMessage)
	}
	if message.Flag == MessageFlagRequest && !strings.EqualFold(message.RequestID, message.ID) {
		return fmt.Errorf("%w: request id must equal message id", ErrInvalidMessage)
	}
	if message.Flag == MessageFlagResponse && isZeroMessageID(message.RequestID) {
		return fmt.Errorf("%w: zero response request id", ErrInvalidMessage)
	}
	return nil
}
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
func messageRequestIDBytes(value string) ([]byte, error) {
	if value == "" {
		return make([]byte, messageIDByteSize), nil
	}
	return messageIDBytes(value)
}
func messageRequestIDString(value []byte) string {
	if isZeroBytes(value) {
		return ""
	}
	return hex.EncodeToString(value)
}
func isValidMessageID(value string) bool {
	if len(value) != messageIDByteSize*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func isZeroMessageID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && isZeroBytes(decoded)
}
func messagePeerIDBytes(peerID string) ([]byte, error) {
	if peerID == "" {
		return make([]byte, messagePeerIDByteSize), nil
	}
	decoded, err := utils.Base58Decode(peerID)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid peer id", ErrInvalidMessage)
	}
	if len(decoded) != messagePeerIDByteSize {
		return nil, fmt.Errorf("%w: invalid peer id length", ErrInvalidMessage)
	}
	return decoded, nil
}
func messagePeerIDString(value []byte) string {
	if isZeroBytes(value) {
		return ""
	}
	return utils.Base58Encode(value)
}
func validateMessagePeerID(peerID string, optional bool) error {
	if peerID == "" && optional {
		return nil
	}
	if err := validatePeerID(peerID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	return nil
}
func maxPayloadSize(maxMessageSize int) int {
	normalized := normalizeMaxMessageSize(maxMessageSize)
	if normalized <= messageWireHeaderSize {
		return 0
	}
	return normalized - messageWireHeaderSize
}
func normalizeMaxMessageSize(maxMessageSize int) int {
	if maxMessageSize <= 0 {
		return DefaultMaxMessageSize
	}
	return maxMessageSize
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
