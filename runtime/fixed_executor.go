package runtime

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"solana_golang/structure"
	"solana_golang/utils"
)

// FixedExecutor 执行固定指令交易 + 作为 VM 接入前的生产闭环执行入口。
type FixedExecutor struct {
	Simulator TransactionSimulator
	Programs  ProgramRegistry
	Logger    *slog.Logger
}

// NewFixedExecutor 创建固定指令执行器 + 组合层显式注册程序防止 runtime import programs。
func NewFixedExecutor(programs ...Program) (FixedExecutor, error) {
	registry, err := NewProgramRegistry(programs...)
	if err != nil {
		return FixedExecutor{}, err
	}
	return FixedExecutor{Programs: registry}, nil
}

// NewFixedExecutorWithRegistry 创建处理器执行器 + 支持组合层按 ProgramSpec 注册程序。
func NewFixedExecutorWithRegistry(registry ProgramRegistry) FixedExecutor {
	return FixedExecutor{Programs: registry.Clone()}
}

// RegisterProgramHandler 注册程序处理器 + 让执行器接入方式对齐 p2p 协议注册。
func (executor *FixedExecutor) RegisterProgramHandler(spec ProgramSpec, handler ProgramHandler) error {
	return executor.Programs.RegisterHandler(spec, handler)
}

// RegisterProgram 注册旧接口程序 + 保留现有业务程序的兼容接入方式。
func (executor *FixedExecutor) RegisterProgram(program Program) error {
	return executor.Programs.RegisterProgram(program)
}

// SetFallbackProgramHandler 设置兜底处理器 + 支持未知程序交给 VM 或扩展执行器。
func (executor *FixedExecutor) SetFallbackProgramHandler(handler ProgramHandler) error {
	return executor.Programs.SetFallbackHandler(handler)
}

// ExecuteTransaction 执行交易 + 在调用 structure legacy 模拟器前完成 runtime 边界规范化。
func (executor FixedExecutor) ExecuteTransaction(contextValue context.Context, request TransactionRequest) (result TransactionResult, err error) {
	startedAt := time.Now()
	transactionID := transactionIDForLog(request.Simulation.Transaction)
	defer func() {
		executor.logTransactionExecution(contextValue, request, transactionID, result, startedAt, err)
	}()

	contextValue = normalizeExecutionContext(contextValue)
	if err := contextValue.Err(); err != nil {
		return TransactionResult{}, err
	}
	if err := validateTransactionRequest(request); err != nil {
		return TransactionResult{}, err
	}

	input := request.Simulation
	if input.Logger == nil {
		input.Logger = executor.Logger
	}
	if request.Slot != 0 {
		input.CurrentSlot = request.Slot
	}
	input.CurrentEpoch = request.Epoch
	if input.ProgramRegistry.IsEmpty() && len(input.Programs) == 0 && input.FallbackProgram == nil && !executor.Programs.IsEmpty() {
		input.ProgramRegistry = executor.Programs.Clone()
	}

	simulator := executor.Simulator
	executionResult, err := simulator.Simulate(input)
	if err != nil {
		return TransactionResult{}, err
	}
	return TransactionResult{Mode: normalizeExecutionMode(request.Mode), Execution: executionResult}, nil
}

type balanceChangeLog struct {
	Address  string `json:"address"`
	Before   uint64 `json:"before"`
	After    uint64 `json:"after"`
	Delta    string `json:"delta"`
	Writable bool   `json:"writable"`
}

func (executor FixedExecutor) logTransactionExecution(
	contextValue context.Context,
	request TransactionRequest,
	transactionID string,
	result TransactionResult,
	startedAt time.Time,
	err error,
) {
	logger := utils.EnsureLogger(executor.Logger)
	attrs := []slog.Attr{
		slog.String("chain_id", request.ChainID),
		slog.Uint64("slot", request.Slot),
		slog.String("tx_id", transactionID),
		slog.String("execution_mode", string(normalizeExecutionMode(request.Mode))),
		slog.Int("status", int(result.Execution.Status)),
		slog.Uint64("fee_total", result.Execution.FeeDetails.TotalFee),
		slog.Uint64("fee_burned", result.Execution.FeeDetails.BurnedFee),
		slog.Uint64("fee_validator", result.Execution.FeeDetails.ValidatorFee),
		slog.Any("balance_changes", balanceChangesForLog(request.Simulation.Transaction, result.Execution)),
		slog.Int("written_accounts", len(result.Execution.WrittenAccounts)),
		slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	}
	if result.Execution.Error != nil {
		attrs = append(attrs,
			slog.Int("error_code", int(result.Execution.Error.Code)),
			slog.String("error_message", result.Execution.Error.Error()),
		)
	}
	if err != nil {
		attrs = append(attrs, slog.Any("error", err))
		logger.LogAttrs(normalizeExecutionContext(contextValue), slog.LevelError, "runtime transaction execution failed", attrs...)
		return
	}
	if result.Execution.Status != structure.TransactionStatusConfirmed {
		logger.LogAttrs(normalizeExecutionContext(contextValue), slog.LevelWarn, "runtime transaction rejected", attrs...)
		return
	}
	logger.LogAttrs(normalizeExecutionContext(contextValue), slog.LevelInfo, "runtime transaction executed", attrs...)
}

func transactionIDForLog(transaction structure.Transaction) string {
	transactionID, err := transaction.TxIDString()
	if err != nil {
		return ""
	}
	return transactionID
}

func balanceChangesForLog(transaction structure.Transaction, result structure.TransactionExecutionResult) []balanceChangeLog {
	limit := len(result.PreBalances)
	if len(result.PostBalances) < limit {
		limit = len(result.PostBalances)
	}
	if len(transaction.Accounts) < limit {
		limit = len(transaction.Accounts)
	}
	changes := make([]balanceChangeLog, 0, limit)
	for index := 0; index < limit; index++ {
		before := result.PreBalances[index]
		after := result.PostBalances[index]
		if before == after {
			continue
		}
		account := transaction.Accounts[index]
		changes = append(changes, balanceChangeLog{
			Address:  account.PublicKey.String(),
			Before:   before,
			After:    after,
			Delta:    lamportDeltaText(before, after),
			Writable: account.IsWritable,
		})
	}
	return changes
}

func lamportDeltaText(before uint64, after uint64) string {
	if after >= before {
		return "+" + strconv.FormatUint(after-before, 10)
	}
	return "-" + strconv.FormatUint(before-after, 10)
}
