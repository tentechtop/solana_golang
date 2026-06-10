package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	MethodGetBalance      = "getBalance"
	MethodSendTransaction = "sendTransaction"
	MethodGetBlock        = "getBlock"
)

// LedgerBackend 定义链业务后端 + 让 RPC 层只负责协议转换和参数校验。
type LedgerBackend interface {
	GetBalance(ctx context.Context, address string) (BalanceResult, error)
	SendTransaction(ctx context.Context, encodedTransaction string) (string, error)
	GetBlock(ctx context.Context, slot uint64) (BlockResult, error)
}

type BalanceResult struct {
	Value uint64 `json:"value"`
}

type BlockResult struct {
	Slot         uint64 `json:"slot"`
	Blockhash    string `json:"blockhash,omitempty"`
	ParentSlot   uint64 `json:"parentSlot,omitempty"`
	Transactions []any  `json:"transactions,omitempty"`
}

// RegisterDefaultHandlers 执行对应逻辑 + 保持函数职责清晰可维护。
func RegisterDefaultHandlers(router *Router, backend LedgerBackend) {
	_ = router.Register(MethodGetBalance, getBalanceHandler(backend))
	_ = router.Register(MethodSendTransaction, sendTransactionHandler(backend))
	_ = router.Register(MethodGetBlock, getBlockHandler(backend))
}

// getBalanceHandler 执行对应逻辑 + 保持函数职责清晰可维护。
func getBalanceHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		if backend == nil {
			return nil, ErrMethodUnavailable
		}
		requestParams, rpcError := parseGetBalanceParams(params)
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := backend.GetBalance(ctx, requestParams.Address)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get balance: %v", err))
		}
		return result, nil
	}
}

// sendTransactionHandler 执行对应逻辑 + 保持函数职责清晰可维护。
func sendTransactionHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		if backend == nil {
			return nil, ErrMethodUnavailable
		}
		requestParams, rpcError := parseSendTransactionParams(params)
		if rpcError != nil {
			return nil, rpcError
		}
		signature, err := backend.SendTransaction(ctx, requestParams.EncodedTransaction)
		if err != nil {
			return nil, internalError(fmt.Sprintf("send transaction: %v", err))
		}
		return signature, nil
	}
}

// getBlockHandler 执行对应逻辑 + 保持函数职责清晰可维护。
func getBlockHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		if backend == nil {
			return nil, ErrMethodUnavailable
		}
		requestParams, rpcError := parseGetBlockParams(params)
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := backend.GetBlock(ctx, requestParams.Slot)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get block: %v", err))
		}
		return result, nil
	}
}

type getBalanceParams struct {
	Address string `json:"address"`
}

type sendTransactionParams struct {
	EncodedTransaction string `json:"encodedTransaction"`
}

type getBlockParams struct {
	Slot uint64 `json:"slot"`
}

// parseGetBalanceParams 执行对应逻辑 + 保持函数职责清晰可维护。
func parseGetBalanceParams(params json.RawMessage) (getBalanceParams, *Error) {
	values, rpcError := parseParamsArray(params)
	if rpcError != nil {
		return getBalanceParams{}, rpcError
	}
	if len(values) < 1 {
		return getBalanceParams{}, invalidParamsError("getBalance requires address")
	}
	var address string
	if err := json.Unmarshal(values[0], &address); err != nil || address == "" {
		return getBalanceParams{}, invalidParamsError("getBalance address must be a non-empty string")
	}
	return getBalanceParams{Address: address}, nil
}

// parseSendTransactionParams 执行对应逻辑 + 保持函数职责清晰可维护。
func parseSendTransactionParams(params json.RawMessage) (sendTransactionParams, *Error) {
	values, rpcError := parseParamsArray(params)
	if rpcError != nil {
		return sendTransactionParams{}, rpcError
	}
	if len(values) < 1 {
		return sendTransactionParams{}, invalidParamsError("sendTransaction requires encoded transaction")
	}
	var encodedTransaction string
	if err := json.Unmarshal(values[0], &encodedTransaction); err != nil || encodedTransaction == "" {
		return sendTransactionParams{}, invalidParamsError("sendTransaction transaction must be a non-empty string")
	}
	return sendTransactionParams{EncodedTransaction: encodedTransaction}, nil
}

// parseGetBlockParams 执行对应逻辑 + 保持函数职责清晰可维护。
func parseGetBlockParams(params json.RawMessage) (getBlockParams, *Error) {
	values, rpcError := parseParamsArray(params)
	if rpcError != nil {
		return getBlockParams{}, rpcError
	}
	if len(values) < 1 {
		return getBlockParams{}, invalidParamsError("getBlock requires slot")
	}
	var slot uint64
	if err := json.Unmarshal(values[0], &slot); err != nil {
		return getBlockParams{}, invalidParamsError("getBlock slot must be an unsigned integer")
	}
	return getBlockParams{Slot: slot}, nil
}

// parseParamsArray 执行对应逻辑 + 保持函数职责清晰可维护。
func parseParamsArray(params json.RawMessage) ([]json.RawMessage, *Error) {
	if len(params) == 0 || string(params) == "null" {
		return nil, invalidParamsError("params must be an array")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(params, &values); err != nil {
		return nil, invalidParamsError("params must be an array")
	}
	return values, nil
}
