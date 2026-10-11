package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
)

const (
	MethodGetStablecoinCapabilities = "getStablecoinCapabilities"
	MethodGetStablecoins            = "getStablecoins"
	MethodGetStablecoinState        = "getStablecoinState"
	MethodGetStablecoinOrders       = "getStablecoinOrders"
)

type StablecoinStateResult struct {
	Address           string `json:"address"`
	Program           string `json:"program"`
	Creator           string `json:"creator"`
	Authority         string `json:"authority"`
	PendingAuthority  string `json:"pending_authority"`
	Name              string `json:"name"`
	Symbol            string `json:"symbol"`
	Currency          string `json:"currency"`
	Decimals          uint8  `json:"decimals"`
	Supply            string `json:"supply"`
	DeclaredReserve   string `json:"declared_reserve"`
	PendingRedemption string `json:"pending_redemption"`
	Limit             string `json:"limit"`
	Paused            bool   `json:"paused"`
	BalanceAddress    string `json:"balance_address"`
	AccountExists     bool   `json:"account_exists"`
	Balance           string `json:"balance"`
}

type StablecoinListResult struct {
	Assets     []StablecoinStateResult `json:"assets"`
	NextCursor string                  `json:"next_cursor"`
}

type StablecoinOrderResult struct {
	Address   string `json:"address"`
	Mint      string `json:"mint"`
	Owner     string `json:"owner"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Amount    string `json:"amount"`
	Reference string `json:"reference"`
	Receipt   string `json:"receipt"`
}

type StablecoinOrderListResult struct {
	Orders     []StablecoinOrderResult `json:"orders"`
	NextCursor string                  `json:"next_cursor"`
}

type StablecoinBackend interface {
	GetStablecoins(context.Context, string, string, int) (StablecoinListResult, error)
	GetStablecoinState(context.Context, string, string) (StablecoinStateResult, error)
	GetStablecoinOrders(context.Context, string, string, string, int) (StablecoinOrderListResult, error)
}

func registerStablecoinHandlers(router *Router, backend LedgerBackend) {
	_ = router.Register(MethodGetStablecoinCapabilities, func(_ context.Context, params json.RawMessage) (any, *Error) {
		values, e := parseParamsArray(params)
		if e != nil {
			return nil, e
		}
		if len(values) != 0 {
			return nil, invalidParamsError("getStablecoinCapabilities takes no parameters")
		}
		if _, ok := backend.(StablecoinBackend); !ok {
			return nil, ErrMethodUnavailable
		}
		data, err := vmprogram.StablecoinProgramData()
		if err != nil {
			return nil, internalError(err.Error())
		}
		hash := sha256.Sum256(data)
		return map[string]any{"version": 1, "template_base64": base64.StdEncoding.EncodeToString(data), "template_hash": hex.EncodeToString(hash[:]), "backing": "issuer_declared", "max_decimals": 9, "commitment": "finalized"}, nil
	})
	for _, method := range []string{MethodGetStablecoins, MethodGetStablecoinState, MethodGetStablecoinOrders} {
		method := method
		_ = router.Register(method, func(ctx context.Context, params json.RawMessage) (any, *Error) {
			b, ok := backend.(StablecoinBackend)
			if !ok {
				return nil, ErrMethodUnavailable
			}
			values, e := parseParamsArray(params)
			if e != nil {
				return nil, e
			}
			count := 3
			if method == MethodGetStablecoinState {
				count = 2
			}
			if method == MethodGetStablecoinOrders {
				count = 4
			}
			if len(values) != count {
				return nil, invalidParamsError(fmt.Sprintf("%s requires %d parameters", method, count))
			}
			texts := make([]string, count)
			stringCount := count
			if method != MethodGetStablecoinState {
				stringCount--
			}
			for n := 0; n < stringCount; n++ {
				if err := json.Unmarshal(values[n], &texts[n]); err != nil || len(texts[n]) > 96 {
					return nil, invalidParamsError("invalid address or cursor")
				}
			}
			limit := 50
			if method != MethodGetStablecoinState {
				if err := json.Unmarshal(values[count-1], &limit); err != nil || limit < 1 || limit > 100 {
					return nil, invalidParamsError("limit must be 1..100")
				}
			}
			var result any
			var err error
			switch method {
			case MethodGetStablecoins:
				result, err = b.GetStablecoins(ctx, texts[0], texts[1], limit)
			case MethodGetStablecoinState:
				result, err = b.GetStablecoinState(ctx, texts[0], texts[1])
			case MethodGetStablecoinOrders:
				result, err = b.GetStablecoinOrders(ctx, texts[0], texts[1], texts[2], limit)
			}
			if err != nil {
				return nil, internalError(err.Error())
			}
			return result, nil
		})
	}
}
