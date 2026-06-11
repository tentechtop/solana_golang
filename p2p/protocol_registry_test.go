package p2p

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestProtocolRegistryHandlesResponseProtocol(t *testing.T) {
	registry := NewProtocolRegistry()
	localPeerID := testPeerID(8)
	remotePeerID := testPeerID(9)
	spec := ProtocolSpec{
		ID:          ProtocolPingV1,
		Name:        "/p2p/ping/1.0.0",
		HasResponse: true,
		Priority:    MessagePriorityHigh,
	}

	err := registry.RegisterResultHandler(spec, func(ctx context.Context, message Message) (Message, error) {
		return responseFor(message, localPeerID, ProtocolPongV1, []byte("pong"))
	})
	if err != nil {
		t.Fatalf("RegisterResultHandler() error = %v", err)
	}

	request, err := NewRequestMessage(remotePeerID, ProtocolPingV1, []byte("ping"))
	if err != nil {
		t.Fatalf("NewRequestMessage() error = %v", err)
	}
	request.ToPeerID = localPeerID

	result, err := registry.Handle(context.Background(), request)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !result.HasResponse {
		t.Fatal("HasResponse = false, want true")
	}
	if result.Message.RequestID != request.ID {
		t.Fatalf("RequestID = %q, want %q", result.Message.RequestID, request.ID)
	}
	if !bytes.Equal(result.Message.Payload, []byte("pong")) {
		t.Fatalf("Payload = %q, want pong", result.Message.Payload)
	}
}
func TestProtocolRegistryRejectsMismatchedRegistration(t *testing.T) {
	registry := NewProtocolRegistry()
	spec := ProtocolSpec{
		ID:          ProtocolReceiveTransactionV1,
		Name:        "/p2p/transaction/receive/1.0.0",
		HasResponse: false,
		Priority:    MessagePriorityNormal,
	}

	err := registry.RegisterResultHandler(spec, func(ctx context.Context, message Message) (Message, error) {
		return Message{}, nil
	})
	if !errors.Is(err, ErrProtocolResponseMismatch) {
		t.Fatalf("RegisterResultHandler() error = %v, want ErrProtocolResponseMismatch", err)
	}
}
func TestProtocolRegistryRejectsDuplicateProtocol(t *testing.T) {
	registry := NewProtocolRegistry()
	spec := ProtocolSpec{
		ID:          ProtocolReceiveBlockV1,
		Name:        "/p2p/block/receive/1.0.0",
		HasResponse: false,
		Priority:    MessagePriorityNormal,
	}
	handler := func(ctx context.Context, message Message) error { return nil }
	if err := registry.RegisterVoidHandler(spec, handler); err != nil {
		t.Fatalf("RegisterVoidHandler() first error = %v", err)
	}
	err := registry.RegisterVoidHandler(spec, handler)
	if !errors.Is(err, ErrProtocolConflict) {
		t.Fatalf("RegisterVoidHandler() duplicate error = %v, want ErrProtocolConflict", err)
	}
}
