package p2p

import (
	"bytes"
	"errors"
	"testing"

	"solana_golang/codec/borsh"
	"solana_golang/schema"
)

func TestMessageFrameRoundTrip(t *testing.T) {
	message, err := NewMessage(MessageTypePing, []byte("hello"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}

	var buffer bytes.Buffer
	if err := writeMessageFrame(&buffer, message, DefaultMaxMessageSize); err != nil {
		t.Fatalf("writeMessageFrame() error = %v", err)
	}

	decoded, err := readMessageFrame(&buffer, DefaultMaxMessageSize)
	if err != nil {
		t.Fatalf("readMessageFrame() error = %v", err)
	}
	if decoded.ID != message.ID {
		t.Fatalf("ID = %q, want %q", decoded.ID, message.ID)
	}
	if decoded.Type != MessageTypePing {
		t.Fatalf("Type = %d, want %d", decoded.Type, MessageTypePing)
	}
	if decoded.Version != MessageProtocolVersion {
		t.Fatalf("Version = %d, want %d", decoded.Version, MessageProtocolVersion)
	}
	if !bytes.Equal(decoded.Payload, []byte("hello")) {
		t.Fatalf("Payload = %q, want hello", decoded.Payload)
	}
}
func TestRequestResponseMessageRules(t *testing.T) {
	peerID := testPeerID(10)
	request, err := NewRequestMessage(peerID, MessageTypePing, []byte("ping"))
	if err != nil {
		t.Fatalf("NewRequestMessage() error = %v", err)
	}
	if !request.IsRequest() {
		t.Fatal("IsRequest() = false, want true")
	}
	if request.RequestID != request.ID {
		t.Fatalf("RequestID = %q, want %q", request.RequestID, request.ID)
	}

	response, err := NewResponseMessage(peerID, MessageTypePong, request.ID, []byte("pong"))
	if err != nil {
		t.Fatalf("NewResponseMessage() error = %v", err)
	}
	if !response.IsResponse() {
		t.Fatal("IsResponse() = false, want true")
	}
	if response.RequestID != request.ID {
		t.Fatalf("Response RequestID = %q, want %q", response.RequestID, request.ID)
	}
}
func TestMessageBinaryCarriesPeerRoute(t *testing.T) {
	fromPeerID := testPeerID(11)
	toPeerID := testPeerID(12)
	message, err := NewMessage(MessageTypeBlock, []byte("block"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	message.FromPeerID = fromPeerID
	message.ToPeerID = toPeerID

	encoded, err := message.MarshalBinary(DefaultMaxMessageSize)
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalBinary(encoded, DefaultMaxMessageSize)
	if err != nil {
		t.Fatalf("UnmarshalBinary() error = %v", err)
	}
	if decoded.FromPeerID != fromPeerID {
		t.Fatalf("FromPeerID = %q, want %q", decoded.FromPeerID, fromPeerID)
	}
	if decoded.ToPeerID != toPeerID {
		t.Fatalf("ToPeerID = %q, want %q", decoded.ToPeerID, toPeerID)
	}
}
func TestMessageMarshalUsesBorshLayout(t *testing.T) {
	message, err := NewMessage(MessageTypePing, []byte("borsh"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}

	encoded, err := message.MarshalBinary(DefaultMaxMessageSize)
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}

	reader := borsh.NewReader(encoded, DefaultMaxMessageSize)
	version, err := reader.ReadUint16()
	if err != nil {
		t.Fatalf("ReadUint16(version) error = %v", err)
	}
	if version != MessageProtocolVersion {
		t.Fatalf("version = %d, want %d", version, MessageProtocolVersion)
	}
	messageID, err := reader.ReadString()
	if err != nil {
		t.Fatalf("ReadString(id) error = %v", err)
	}
	if messageID != message.ID {
		t.Fatalf("id = %q, want %q", messageID, message.ID)
	}
	messageType, err := reader.ReadUint32()
	if err != nil {
		t.Fatalf("ReadUint32(type) error = %v", err)
	}
	if MessageType(messageType) != MessageTypePing {
		t.Fatalf("type = %d, want %d", messageType, MessageTypePing)
	}
	if _, err := reader.ReadString(); err != nil {
		t.Fatalf("ReadString(from peer) error = %v", err)
	}
	if _, err := reader.ReadString(); err != nil {
		t.Fatalf("ReadString(to peer) error = %v", err)
	}
	if _, err := reader.ReadString(); err != nil {
		t.Fatalf("ReadString(request id) error = %v", err)
	}
	flag, err := reader.ReadUint8()
	if err != nil {
		t.Fatalf("ReadUint8(flag) error = %v", err)
	}
	if MessageFlag(flag) != MessageFlagNotify {
		t.Fatalf("flag = %d, want notify", flag)
	}
	if _, err := reader.ReadInt64(); err != nil {
		t.Fatalf("ReadInt64(created at) error = %v", err)
	}
	payload, err := reader.ReadBytes()
	if err != nil {
		t.Fatalf("ReadBytes(payload) error = %v", err)
	}
	if !bytes.Equal(payload, []byte("borsh")) {
		t.Fatalf("payload = %q, want borsh", payload)
	}
	if err := reader.EnsureEOF(); err != nil {
		t.Fatalf("EnsureEOF() error = %v", err)
	}
}
func TestMessageFrameRejectsChecksumMismatch(t *testing.T) {
	message, err := NewMessage(MessageTypePing, []byte("hello"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}

	var buffer bytes.Buffer
	if err := writeMessageFrame(&buffer, message, DefaultMaxMessageSize); err != nil {
		t.Fatalf("writeMessageFrame() error = %v", err)
	}

	encodedFrame := buffer.Bytes()
	encodedFrame[len(encodedFrame)-1] ^= 0xff
	if _, err := readMessageFrame(bytes.NewReader(encodedFrame), DefaultMaxMessageSize); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("readMessageFrame(corrupt) error = %v, want ErrInvalidMessage", err)
	}
}
func TestMessageRejectsUnknownBorshFlag(t *testing.T) {
	message, err := NewMessage(MessageTypePing, []byte("hello"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	writer := borsh.NewWriter(DefaultMaxMessageSize)
	writer.WriteUint16(MessageProtocolVersion)
	if err := writer.WriteString(message.ID); err != nil {
		t.Fatalf("WriteString(id) error = %v", err)
	}
	writer.WriteUint32(uint32(message.Type))
	if err := writer.WriteString(""); err != nil {
		t.Fatalf("WriteString(from peer) error = %v", err)
	}
	if err := writer.WriteString(""); err != nil {
		t.Fatalf("WriteString(to peer) error = %v", err)
	}
	if err := writer.WriteString(""); err != nil {
		t.Fatalf("WriteString(request id) error = %v", err)
	}
	writer.WriteUint8(uint8(MessageFlagUnknown))
	writer.WriteInt64(message.CreatedAtUnixMilli)
	if err := writer.WriteBytes([]byte("hello")); err != nil {
		t.Fatalf("WriteBytes(payload) error = %v", err)
	}

	if _, err := UnmarshalBinary(writer.Bytes(), DefaultMaxMessageSize); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("UnmarshalBinary(unknown borsh flag) error = %v, want ErrInvalidMessage", err)
	}
}
func TestP2PMessageSchemaEnvelope(t *testing.T) {
	registry := schema.NewRegistry()
	if err := RegisterP2PMessageSchema(registry); err != nil {
		t.Fatalf("RegisterP2PMessageSchema() error = %v", err)
	}
	message, err := NewMessage(MessageTypePing, []byte("hello"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	envelope, err := NewP2PMessageEnvelope(message)
	if err != nil {
		t.Fatalf("NewP2PMessageEnvelope() error = %v", err)
	}
	if envelope.Codec != schema.CodecBorsh {
		t.Fatalf("envelope codec = %s, want borsh", envelope.Codec)
	}

	decoded, err := registry.DecodeEnvelope(envelope)
	if err != nil {
		t.Fatalf("DecodeEnvelope() error = %v", err)
	}
	decodedMessage, ok := decoded.(Message)
	if !ok {
		t.Fatalf("DecodeEnvelope() type = %T, want Message", decoded)
	}
	if decodedMessage.ID != message.ID {
		t.Fatalf("decoded ID = %q, want %q", decodedMessage.ID, message.ID)
	}
}
func TestMessageRejectsInvalidFields(t *testing.T) {
	if err := (Message{}).Validate(DefaultMaxMessageSize); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("Validate(empty) error = %v, want ErrInvalidMessage", err)
	}

	message, err := NewMessage(MessageTypePing, bytes.Repeat([]byte{1}, 4))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	if err := message.Validate(2); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("Validate(oversized) error = %v, want ErrInvalidMessage", err)
	}
}
func TestMessageCloneIsolatesPayload(t *testing.T) {
	message, err := NewMessage(MessageTypePing, []byte{1, 2, 3})
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}

	cloned := message.Clone()
	cloned.Payload[0] = 9
	if message.Payload[0] == 9 {
		t.Fatal("Clone() shared payload, want isolated copy")
	}
}
