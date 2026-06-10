package p2p

import (
	"bytes"
	"errors"
	"testing"
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
