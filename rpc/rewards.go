package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
)

const MethodGetRewardCapabilities = "getRewardCapabilities"
const MethodGetRewardState = "getRewardState"

type RewardBackend interface {
	GetRewardState(context.Context, string, string) (map[string]any, error)
}

func registerRewardHandlers(router *Router, backend LedgerBackend) {
	_ = router.Register(MethodGetRewardCapabilities, func(_ context.Context, params json.RawMessage) (any, *Error) {
		values, e := parseParamsArray(params)
		if e != nil {
			return nil, e
		}
		if len(values) != 0 {
			return nil, invalidParamsError("getRewardCapabilities takes no parameters")
		}
		if _, ok := backend.(RewardBackend); !ok {
			return nil, ErrMethodUnavailable
		}
		code, err := vmprogram.RewardProgramData()
		if err != nil {
			return nil, internalError(err.Error())
		}
		hash := sha256.Sum256(code)
		return map[string]any{"version": 1, "commitment": "finalized", "template_hash": hex.EncodeToString(hash[:]), "template_base64": base64.StdEncoding.EncodeToString(code), "calendar": "oracle_utc_day", "actions": []string{"signin", "video"}}, nil
	})
	_ = router.Register(MethodGetRewardState, func(ctx context.Context, params json.RawMessage) (any, *Error) {
		b, ok := backend.(RewardBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, e := parseParamsArray(params)
		if e != nil {
			return nil, e
		}
		if len(values) != 2 {
			return nil, invalidParamsError("getRewardState requires program and owner")
		}
		var program, owner string
		if json.Unmarshal(values[0], &program) != nil || json.Unmarshal(values[1], &owner) != nil || len(program) > 96 || len(owner) > 96 {
			return nil, invalidParamsError("invalid reward state parameters")
		}
		result, err := b.GetRewardState(ctx, program, owner)
		if err != nil {
			return nil, internalError(err.Error())
		}
		return result, nil
	})
}
