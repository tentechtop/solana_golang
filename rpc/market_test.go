package rpc

import (
	"context"
	"encoding/json"
	"testing"
)

type marketTestBackend struct{ testLedgerBackend }

func (marketTestBackend) GetMarketAssets(context.Context, string, string, int) (MarketAssetList, error) {
	return MarketAssetList{Assets: []MarketAssetResult{}, Commitment: "finalized"}, nil
}
func (marketTestBackend) GetMarketOrders(context.Context, string, string, string, int) (MarketOrderList, error) {
	return MarketOrderList{Orders: []MarketOrderResult{}, Commitment: "finalized"}, nil
}
func (marketTestBackend) GetMarketOrder(context.Context, string, string) (MarketOrderResult, error) {
	return MarketOrderResult{Amount: "18446744073709551615", TotalLamports: "9007199254740993", Commitment: "finalized"}, nil
}
func TestMarketRPCValidationAndExactAmounts(t *testing.T) {
	for _, router := range []*Router{NewDefaultRouter(marketTestBackend{}), NewPublicRouter(marketTestBackend{})} {
		for _, tc := range []struct {
			method, params string
			ok             bool
		}{
			{"getMarketCapabilities", "[]", true}, {"getMarketCapabilities", "[1]", false}, {"getMarketAssets", `["owner","",50]`, true}, {"getMarketAssets", `["owner","",0]`, false}, {"getMarketAssets", `["owner","",101]`, false}, {"getMarketOrders", `["owner","open","",50]`, true}, {"getMarketOrders", `["owner","open","","50"]`, false}, {"getMarketOrder", `["program","owner"]`, true}, {"getMarketOrder", `["program"]`, false},
		} {
			r := router.Handle(context.Background(), Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: tc.method, Params: json.RawMessage(tc.params)})
			if (r.Error == nil) != tc.ok {
				t.Fatalf("%s %s: %+v", tc.method, tc.params, r.Error)
			}
			if tc.ok && tc.method == "getMarketOrder" {
				wire, _ := json.Marshal(r.Result)
				var result map[string]any
				json.Unmarshal(wire, &result)
				if result["amount"] != "18446744073709551615" || result["total_lamports"] != "9007199254740993" {
					t.Fatal("integer precision lost")
				}
			}
		}
	}
	r := NewPublicRouter(testLedgerBackend{}).Handle(context.Background(), Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "getMarketCapabilities", Params: json.RawMessage(`[]`)})
	if r.Error == nil {
		t.Fatal("unsupported backend advertised market")
	}
}
