package rpc

import (
	"context"
	"encoding/json"
	"testing"
)

type rwaTestBackend struct{ testLedgerBackend }

func (rwaTestBackend) GetRWAAssets(context.Context, string, string, int) (RWAListResult, error) {
	return RWAListResult{Assets: []RWAStateResult{}}, nil
}
func (rwaTestBackend) GetRWAState(context.Context, string, string) (RWAStateResult, error) {
	return RWAStateResult{Supply: "18446744073709551615", Balance: "9007199254740993"}, nil
}
func (rwaTestBackend) GetRWAProposal(context.Context, string) (RWAProposalResult, error) {
	return RWAProposalResult{Yes: "9007199254740993"}, nil
}
func (rwaTestBackend) GetRWADividend(context.Context, string, string) (RWADividendResult, error) {
	return RWADividendResult{Total: "18446744073709551615"}, nil
}

func TestRWARPCPublicAndDefault(t *testing.T) {
	for _, router := range []*Router{NewDefaultRouter(rwaTestBackend{}), NewPublicRouter(rwaTestBackend{})} {
		for _, test := range []struct {
			method, params string
			ok             bool
		}{
			{MethodGetRWACapabilities, `[]`, true}, {MethodGetRWACapabilities, `[1]`, false},
			{MethodGetRWAAssets, `["owner","",50]`, true}, {MethodGetRWAAssets, `["owner","",0]`, false},
			{MethodGetRWAState, `["mint","owner"]`, true}, {MethodGetRWAState, `["mint"]`, false},
			{MethodGetRWAProposal, `["proposal"]`, true}, {MethodGetRWAProposal, `[]`, false},
			{MethodGetRWADividend, `["dividend","owner"]`, true}, {MethodGetRWADividend, `["dividend"]`, false},
		} {
			response := router.Handle(context.Background(), Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: test.method, Params: json.RawMessage(test.params)})
			if (response.Error == nil) != test.ok {
				t.Fatalf("%s %s: %+v", test.method, test.params, response.Error)
			}
			if test.ok && test.method == MethodGetRWACapabilities && response.Result.(map[string]any)["commitment"] != "finalized" {
				t.Fatal("nonfinalized RWA capability")
			}
			if test.ok {
				wire, _ := json.Marshal(response.Result)
				var decoded any
				if json.Unmarshal(wire, &decoded) != nil {
					t.Fatalf("invalid JSON result: %s", wire)
				}
			}
		}
	}
	response := NewDefaultRouter(testLedgerBackend{}).Handle(context.Background(), Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: MethodGetRWACapabilities, Params: json.RawMessage(`[]`)})
	if response.Error == nil {
		t.Fatal("unsupported backend advertised RWA capability")
	}
}
