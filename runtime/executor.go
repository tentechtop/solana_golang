package runtime

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrInvalidExecutionRequest = errors.New("runtime: invalid execution request")
)

// ExecutionMode 标识执行后端 + 当前固定指令和未来 VM 使用同一入口。
type ExecutionMode string

const (
	ExecutionModeFixedInstruction ExecutionMode = "fixed_instruction_v1"
	ExecutionModeGoSVM            ExecutionMode = "gosvm_v1"
	ExecutionModeSVMCompat        ExecutionMode = "svm_compat_v1"
)

// TransactionRequest 描述交易执行请求 + 共识层只传结构化输入不依赖具体程序包。
type TransactionRequest struct {
	ChainID    string
	Slot       uint64
	Epoch      uint64
	Mode       ExecutionMode
	Simulation TransactionSimulationInput
}

// TransactionResult 描述交易执行响应 + 保留 structure 结果作为账本提交依据。
type TransactionResult struct {
	Mode      ExecutionMode
	Execution TransactionExecutionResult
}

// TransactionExecutor 定义交易执行器接口 + 共识层依赖接口避免和业务程序循环引用。
type TransactionExecutor interface {
	ExecuteTransaction(context.Context, TransactionRequest) (TransactionResult, error)
}

func normalizeExecutionContext(contextValue context.Context) context.Context {
	if contextValue == nil {
		return context.Background()
	}
	return contextValue
}

func normalizeExecutionMode(mode ExecutionMode) ExecutionMode {
	if mode == "" {
		return ExecutionModeFixedInstruction
	}
	return mode
}

func validateTransactionRequest(request TransactionRequest) error {
	if request.Mode != "" &&
		request.Mode != ExecutionModeFixedInstruction &&
		request.Mode != ExecutionModeGoSVM &&
		request.Mode != ExecutionModeSVMCompat {
		return fmt.Errorf("%w: unsupported execution mode %s", ErrInvalidExecutionRequest, request.Mode)
	}
	return nil
}
