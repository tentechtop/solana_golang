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

func TestProtocolRegistryDefaultsProtocolClass(t *testing.T) {
	registry := NewProtocolRegistry()
	pingSpec := ProtocolSpec{
		ID:          ProtocolPingV1,
		Name:        "/p2p/ping/1.0.0",
		HasResponse: false,
		Priority:    MessagePriorityHigh,
	}
	if err := registry.RegisterVoidHandler(pingSpec, func(ctx context.Context, message Message) error { return nil }); err != nil {
		t.Fatalf("RegisterVoidHandler(ping) error = %v", err)
	}
	storedPingSpec, ok := registry.Spec(ProtocolPingV1)
	if !ok || storedPingSpec.Class != ProtocolClassControl {
		t.Fatalf("ping class = %d ok=%v, want control", storedPingSpec.Class, ok)
	}

	customSpec := ProtocolSpec{
		ID:          ProtocolID(9000),
		Name:        "/custom/business/1.0.0",
		HasResponse: false,
		Priority:    MessagePriorityNormal,
	}
	if err := registry.RegisterVoidHandler(customSpec, func(ctx context.Context, message Message) error { return nil }); err != nil {
		t.Fatalf("RegisterVoidHandler(custom) error = %v", err)
	}
	storedCustomSpec, ok := registry.Spec(customSpec.ID)
	if !ok || storedCustomSpec.Class != ProtocolClassData {
		t.Fatalf("custom class = %d ok=%v, want data", storedCustomSpec.Class, ok)
	}
}

func TestDefaultProtocolSpecsDeclareSafeConcurrency(t *testing.T) {
	specs := DefaultProtocolSpecs()
	byID := make(map[ProtocolID]ProtocolSpec, len(specs))
	for _, spec := range specs {
		if err := spec.Validate(); err != nil {
			t.Fatalf("default spec %d Validate() error = %v", spec.ID, err)
		}
		byID[spec.ID] = spec
	}

	statelessProtocols := []ProtocolID{
		ProtocolPingV1,
		ProtocolPongV1,
		ProtocolFindNodeRequestV1,
		ProtocolFindNodeResponseV1,
		ProtocolQueryBlockByHashV1,
		ProtocolQueryBlockByHeightV1,
		ProtocolQueryCommonAncestorV1,
		ProtocolQueryBlockHeadersV1,
		ProtocolIdentifyRequestV1,
		ProtocolIdentifyResponseV1,
	}
	for _, protocolID := range statelessProtocols {
		if byID[protocolID].Concurrency != ProtocolConcurrencyStateless {
			t.Fatalf("protocol %d concurrency = %d, want stateless", protocolID, byID[protocolID].Concurrency)
		}
	}

	orderedProtocols := []ProtocolID{
		ProtocolHandshakeV1,
		ProtocolBlockV1,
		ProtocolBroadcastResourceV1,
		ProtocolGetResourceRequestV1,
		ProtocolReceiveBlockV1,
		ProtocolReceiveTransactionV1,
		ProtocolHandshakeSuccessV1,
		ProtocolPeerHintsV1,
		ProtocolNodeStatusV1,
		ProtocolHotStuffVoteV1,
		ProtocolHotStuffQCV1,
		ProtocolSecureSessionV1,
	}
	for _, protocolID := range orderedProtocols {
		if byID[protocolID].Concurrency != ProtocolConcurrencyOrdered {
			t.Fatalf("protocol %d concurrency = %d, want ordered", protocolID, byID[protocolID].Concurrency)
		}
	}
}

func TestProtocolRegistryRejectsStateKeyConcurrencyWithoutPartitionKey(t *testing.T) {
	registry := NewProtocolRegistry()
	spec := ProtocolSpec{
		ID:          ProtocolID(9300),
		Name:        "/p2p/test/state-key/1.0.0",
		HasResponse: false,
		Priority:    MessagePriorityNormal,
		Concurrency: ProtocolConcurrencyStateKey,
	}
	err := registry.RegisterVoidHandler(spec, func(ctx context.Context, message Message) error {
		return nil
	})
	if !errors.Is(err, ErrInvalidProtocol) {
		t.Fatalf("RegisterVoidHandler() error = %v, want ErrInvalidProtocol", err)
	}
}
