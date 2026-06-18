package poswire

import (
	"encoding/json"
	"testing"

	"solana_golang/rpc"
)

func TestRPCForwardRequestRoundTrip(t *testing.T) {
	payload, err := MarshalRPCForwardRequest(RPCForwardRequest{
		Method: rpc.MethodGetBalance,
		Params: []byte(`["11111111111111111111111111111111"]`),
	}, 4096)
	if err != nil {
		t.Fatalf("MarshalRPCForwardRequest() error = %v", err)
	}
	if len(payload) == 0 || payload[0] == '{' {
		t.Fatalf("payload is not binary: %q", payload)
	}
	decoded, err := UnmarshalRPCForwardRequest(payload, 4096)
	if err != nil {
		t.Fatalf("UnmarshalRPCForwardRequest() error = %v", err)
	}
	if decoded.Method != rpc.MethodGetBalance || string(decoded.Params) != `["11111111111111111111111111111111"]` {
		t.Fatalf("decoded request = %+v", decoded)
	}
}

func TestRPCForwardResponsePreservesErrorData(t *testing.T) {
	forwardError, err := RPCForwardErrorFromRPCError(&rpc.Error{
		Code:    rpc.CodeMethodUnavailable,
		Message: "method unavailable",
		Data:    json.RawMessage(`{"retry":false}`),
	})
	if err != nil {
		t.Fatalf("RPCForwardErrorFromRPCError() error = %v", err)
	}
	payload, err := MarshalRPCForwardResponse(RPCForwardResponse{Error: forwardError}, 4096)
	if err != nil {
		t.Fatalf("MarshalRPCForwardResponse() error = %v", err)
	}
	decoded, err := UnmarshalRPCForwardResponse(payload, 4096)
	if err != nil {
		t.Fatalf("UnmarshalRPCForwardResponse() error = %v", err)
	}
	rpcError := decoded.RPCError()
	if rpcError == nil || rpcError.Code != rpc.CodeMethodUnavailable || rpcError.Message != "method unavailable" {
		t.Fatalf("decoded rpc error = %+v", rpcError)
	}
	encoded, err := json.Marshal(rpcError)
	if err != nil {
		t.Fatalf("Marshal(rpcError) error = %v", err)
	}
	if string(encoded) != `{"code":-32001,"message":"method unavailable","data":{"retry":false}}` {
		t.Fatalf("encoded error = %s", string(encoded))
	}
}

func TestRPCForwardRejectsLegacyJSONPayload(t *testing.T) {
	if _, err := UnmarshalRPCForwardRequest([]byte(`{"method":"getBalance"}`), 4096); err == nil {
		t.Fatal("UnmarshalRPCForwardRequest(JSON) error = nil, want error")
	}
	if _, err := UnmarshalRPCForwardResponse([]byte(`{"result":null}`), 4096); err == nil {
		t.Fatal("UnmarshalRPCForwardResponse(JSON) error = nil, want error")
	}
}
