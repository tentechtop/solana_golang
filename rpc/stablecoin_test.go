package rpc

import (
	"context"
	"encoding/json"
	"testing"
)

type stablecoinTestBackend struct{ testLedgerBackend }

func (stablecoinTestBackend) GetStablecoins(context.Context, string, string, int) (StablecoinListResult, error) {
	return StablecoinListResult{Assets: []StablecoinStateResult{}}, nil
}
func (stablecoinTestBackend) GetStablecoinState(context.Context, string, string) (StablecoinStateResult, error) {
	return StablecoinStateResult{Supply: "18446744073709551615", DeclaredReserve: "18446744073709551615", Balance: "9007199254740993"}, nil
}
func (stablecoinTestBackend) GetStablecoinOrders(context.Context, string, string, string, int) (StablecoinOrderListResult, error) {
	return StablecoinOrderListResult{Orders: []StablecoinOrderResult{}}, nil
}

func TestStablecoinRPCPublicAndDefault(t *testing.T) {
	for _, router := range []*Router{NewDefaultRouter(stablecoinTestBackend{}), NewPublicRouter(stablecoinTestBackend{})} {
		for _, tc := range []struct {
			method, params string
			ok             bool
		}{
			{MethodGetStablecoinCapabilities, `[]`, true}, {MethodGetStablecoinCapabilities, `[1]`, false},
			{MethodGetStablecoinState, `["mint","owner"]`, true}, {MethodGetStablecoinState, `["mint"]`, false},
			{MethodGetStablecoins, `["owner","",50]`, true}, {MethodGetStablecoins, `["owner","",0]`, false}, {MethodGetStablecoins, `["owner","",101]`, false},
			{MethodGetStablecoinOrders, `["mint","owner","",1]`, true}, {MethodGetStablecoinOrders, `["mint","owner","","1"]`, false},
		} {
			response := router.Handle(context.Background(), Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: tc.method, Params: json.RawMessage(tc.params)})
			if (response.Error == nil) != tc.ok {
				t.Fatalf("%s %s: %+v", tc.method, tc.params, response.Error)
			}
			if tc.ok && tc.method == MethodGetStablecoinCapabilities && response.Result.(map[string]any)["commitment"] != "finalized" {
				t.Fatal("nonfinalized capability")
			}
			if tc.ok && tc.method == MethodGetStablecoinState {
				wire, _ := json.Marshal(response.Result)
				var v map[string]any
				_ = json.Unmarshal(wire, &v)
				if v["supply"] != "18446744073709551615" || v["balance"] != "9007199254740993" {
					t.Fatalf("lost precision %s", wire)
				}
			}
		}
	}
	response := NewDefaultRouter(testLedgerBackend{}).Handle(context.Background(), Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: MethodGetStablecoinCapabilities, Params: json.RawMessage(`[]`)})
	if response.Error == nil {
		t.Fatal("unsupported backend advertised capability")
	}
}
