package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

type AccountInfoBackend interface {
	GetAccountInfo(ctx context.Context, address string) (AccountInfoResult, error)
}

type TransactionSimulationBackend interface {
	SimulateTransaction(ctx context.Context, encodedTransaction string) (TransactionSimulationResult, error)
}

// AccountInfoResult 返回合约开发需要的完整账户元数据；受保护账户可以隐藏原始 data。
type AccountInfoResult struct {
	Address      string `json:"address"`
	Exists       bool   `json:"exists"`
	Lamports     string `json:"lamports,omitempty"`
	Owner        string `json:"owner,omitempty"`
	Executable   bool   `json:"executable"`
	RentEpoch    uint64 `json:"rent_epoch,omitempty"`
	DataLength   int    `json:"data_length,omitempty"`
	DataBase64   string `json:"data_base64,omitempty"`
	DataHash     string `json:"data_hash,omitempty"`
	DataRedacted bool   `json:"data_redacted,omitempty"`
}

type TransactionSimulationErrorResult struct {
	Code             uint16  `json:"code"`
	Message          string  `json:"message"`
	InstructionIndex *uint16 `json:"instruction_index,omitempty"`
	InstructionCode  *uint16 `json:"instruction_code,omitempty"`
}

type TransactionSimulationReturnDataResult struct {
	Program    string `json:"program"`
	DataBase64 string `json:"data_base64"`
}

type TransactionSimulationCompiledInstructionResult struct {
	ProgramIDIndex uint8   `json:"program_id_index"`
	AccountIndexes []uint8 `json:"account_indexes"`
	DataBase64     string  `json:"data_base64"`
}

type TransactionSimulationInnerInstructionResult struct {
	Index        uint16                                           `json:"index"`
	Instructions []TransactionSimulationCompiledInstructionResult `json:"instructions"`
}

type TransactionSimulationResult struct {
	Status               string                                        `json:"status"`
	Error                *TransactionSimulationErrorResult             `json:"error,omitempty"`
	FeeLamports          string                                        `json:"fee_lamports"`
	PreBalances          []string                                      `json:"pre_balances"`
	PostBalances         []string                                      `json:"post_balances"`
	LogMessages          []string                                      `json:"log_messages"`
	InnerInstructions    []TransactionSimulationInnerInstructionResult `json:"inner_instructions"`
	ReturnData           *TransactionSimulationReturnDataResult        `json:"return_data,omitempty"`
	ComputeUnitsConsumed uint64                                        `json:"compute_units_consumed"`
	CostUnits            uint64                                        `json:"cost_units"`
	WrittenAccounts      []AccountInfoResult                           `json:"written_accounts"`
}

func getAccountInfoHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		accountBackend, ok := backend.(AccountInfoBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		if len(values) != 1 {
			return nil, invalidParamsError("getAccountInfo requires exactly one address")
		}
		address, rpcError := parseStringParam(values[0], "getAccountInfo address")
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := accountBackend.GetAccountInfo(ctx, address)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get account info: %v", err))
		}
		return result, nil
	}
}

func simulateTransactionHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		simulationBackend, ok := backend.(TransactionSimulationBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		if len(values) != 1 {
			return nil, invalidParamsError("simulateTransaction requires exactly one encoded transaction")
		}
		encodedTransaction, rpcError := parseStringParam(values[0], "simulateTransaction transaction")
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := simulationBackend.SimulateTransaction(ctx, encodedTransaction)
		if err != nil {
			return nil, internalError(fmt.Sprintf("simulate transaction: %v", err))
		}
		return result, nil
	}
}
