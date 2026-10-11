package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"

	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
)

type MarketAssetResult struct {
	Program        string `json:"program"`
	Mint           string `json:"mint"`
	Name           string `json:"name"`
	Symbol         string `json:"symbol"`
	Decimals       uint8  `json:"decimals"`
	Balance        string `json:"balance"`
	BalanceAddress string `json:"balance_address"`
	BalanceExists  bool   `json:"balance_exists"`
}
type MarketOrderResult struct {
	MarketAssetResult
	OrderProgram  string `json:"order_program"`
	StateAddress  string `json:"state_address"`
	VaultAddress  string `json:"vault_address"`
	Seller        string `json:"seller"`
	Buyer         string `json:"buyer"`
	Amount        string `json:"amount"`
	TotalLamports string `json:"total_lamports"`
	Locked        string `json:"locked"`
	Status        string `json:"status"`
	CreatedSlot   string `json:"created_slot"`
	ClosedSlot    string `json:"closed_slot"`
	Commitment    string `json:"commitment"`
}
type MarketAssetList struct {
	Assets     []MarketAssetResult `json:"assets"`
	NextCursor string              `json:"next_cursor"`
	Commitment string              `json:"commitment"`
}
type MarketOrderList struct {
	Orders     []MarketOrderResult `json:"orders"`
	NextCursor string              `json:"next_cursor"`
	Commitment string              `json:"commitment"`
}
type MarketBackend interface {
	GetMarketAssets(context.Context, string, string, int) (MarketAssetList, error)
	GetMarketOrders(context.Context, string, string, string, int) (MarketOrderList, error)
	GetMarketOrder(context.Context, string, string) (MarketOrderResult, error)
}

func registerMarketHandlers(router *Router, backend LedgerBackend) {
	_ = router.Register("getMarketCapabilities", func(_ context.Context, params json.RawMessage) (any, *Error) {
		v, e := parseParamsArray(params)
		if e != nil {
			return nil, e
		}
		if len(v) != 0 {
			return nil, invalidParamsError("getMarketCapabilities takes no parameters")
		}
		if _, ok := backend.(MarketBackend); !ok {
			return nil, ErrMethodUnavailable
		}
		code, err := vmprogram.MarketProgramData()
		if err != nil {
			return nil, internalError(err.Error())
		}
		hash := sha256.Sum256(code)
		return map[string]any{"version": 1, "commitment": "finalized", "template_hash": hex.EncodeToString(hash[:]), "template_base64": base64.StdEncoding.EncodeToString(code), "settlement": "native_sol", "fill": "whole_order", "asset_types": []string{"ordinary_fungible"}}, nil
	})
	for _, method := range []string{"getMarketAssets", "getMarketOrders", "getMarketOrder"} {
		method := method
		_ = router.Register(method, func(ctx context.Context, params json.RawMessage) (any, *Error) {
			b, ok := backend.(MarketBackend)
			if !ok {
				return nil, ErrMethodUnavailable
			}
			v, e := parseParamsArray(params)
			if e != nil {
				return nil, e
			}
			n := 3
			if method == "getMarketOrders" {
				n = 4
			}
			if method == "getMarketOrder" {
				n = 2
			}
			if len(v) != n {
				return nil, invalidParamsError("incorrect market parameter count")
			}
			strings := make([]string, n)
			stringCount := n - 1
			if method == "getMarketOrder" {
				stringCount = n
			}
			for i := 0; i < stringCount; i++ {
				if json.Unmarshal(v[i], &strings[i]) != nil || len(strings[i]) > 96 {
					return nil, invalidParamsError("invalid market string parameter")
				}
			}
			limit := 50
			if method != "getMarketOrder" {
				if json.Unmarshal(v[n-1], &limit) != nil || limit < 1 || limit > 100 {
					return nil, invalidParamsError("market limit must be 1..100")
				}
			}
			var result any
			var err error
			switch method {
			case "getMarketAssets":
				result, err = b.GetMarketAssets(ctx, strings[0], strings[1], limit)
			case "getMarketOrders":
				result, err = b.GetMarketOrders(ctx, strings[0], strings[1], strings[2], limit)
			default:
				result, err = b.GetMarketOrder(ctx, strings[0], strings[1])
			}
			if err != nil {
				return nil, internalError(err.Error())
			}
			return result, nil
		})
	}
}
