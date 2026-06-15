package runtime

import (
	"context"
)

// FixedExecutor 执行固定指令交易 + 作为 VM 接入前的生产闭环执行入口。
type FixedExecutor struct {
	Simulator TransactionSimulator
	Programs  ProgramRegistry
}

// NewFixedExecutor 创建固定指令执行器 + 组合层显式注册程序防止 runtime import programs。
func NewFixedExecutor(programs ...Program) (FixedExecutor, error) {
	registry, err := NewProgramRegistry(programs...)
	if err != nil {
		return FixedExecutor{}, err
	}
	return FixedExecutor{Programs: registry}, nil
}

// ExecuteTransaction 执行交易 + 在调用 structure legacy 模拟器前完成 runtime 边界规范化。
func (executor FixedExecutor) ExecuteTransaction(contextValue context.Context, request TransactionRequest) (TransactionResult, error) {
	contextValue = normalizeExecutionContext(contextValue)
	if err := contextValue.Err(); err != nil {
		return TransactionResult{}, err
	}
	if err := validateTransactionRequest(request); err != nil {
		return TransactionResult{}, err
	}

	input := request.Simulation
	if request.Slot != 0 {
		input.CurrentSlot = request.Slot
	}
	if len(input.Programs) == 0 && input.FallbackProgram == nil && !executor.Programs.IsEmpty() {
		input.Programs = make([]Program, 0, len(executor.Programs.programs))
		for _, program := range executor.Programs.programs {
			input.Programs = append(input.Programs, program)
		}
		input.FallbackProgram = executor.Programs.fallback
	}

	simulator := executor.Simulator
	executionResult, err := simulator.Simulate(input)
	if err != nil {
		return TransactionResult{}, err
	}
	return TransactionResult{Mode: normalizeExecutionMode(request.Mode), Execution: executionResult}, nil
}
