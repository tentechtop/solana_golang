package consensus

import (
	"context"
	"fmt"
	goruntime "runtime"
	"sync"

	runtimepkg "solana_golang/runtime"
	"solana_golang/structure"
)

const defaultParallelExecutionWorkers = 0

type parallelExecutionInput struct {
	ChainID          string
	Slot             uint64
	Epoch            uint64
	Transactions     []structure.Transaction
	State            ChainState
	BlockhashQueue   structure.BlockhashQueue
	ProcessedTxIDs   map[string]struct{}
	Executor         runtimepkg.TransactionExecutor
	MaxIncludedCount int
	WorkerCount      int
	RejectFailed     bool
	MarkIncluded     bool
	FailureLabel     string
	ExecutionMode    runtimepkg.ExecutionMode
}

type parallelExecutionOutput struct {
	State                ChainState
	Transactions         []structure.Transaction
	Receipts             []structure.Hash
	FeeDetails           []structure.FeeDetails
	RejectedTransactions []RejectedTransaction
}

// RejectedTransaction 描述未入块交易 + 让节点能清理确定失败的 mempool 项。
type RejectedTransaction struct {
	TransactionID string
	Transaction   structure.Transaction
	Status        structure.TransactionStatus
	Error         string
}

type scheduledTransaction struct {
	OriginalIndex int
	Transaction   structure.Transaction
}

type parallelExecutionResult struct {
	OriginalIndex int
	Transaction   structure.Transaction
	Result        runtimepkg.TransactionResult
	Err           error
}

// executeTransactionsParallel 按账户冲突并行执行交易 + 共识结果必须按原始顺序提交保持确定性。
func executeTransactionsParallel(
	contextValue context.Context,
	input parallelExecutionInput,
) (parallelExecutionOutput, error) {
	contextValue = normalizeParallelExecutionContext(contextValue)
	if err := validateParallelExecutionInput(input); err != nil {
		return parallelExecutionOutput{}, err
	}

	nextState := input.State.clone()
	output := parallelExecutionOutput{State: nextState}
	pendingTransactions := buildScheduledTransactions(input.Transactions)
	processedTransactionIDs := cloneProcessedTransactionIDs(input.ProcessedTxIDs)
	workerCount := normalizeParallelWorkerCount(input.WorkerCount, len(input.Transactions))

	for len(pendingTransactions) > 0 && len(output.Transactions) < input.MaxIncludedCount {
		if err := contextValue.Err(); err != nil {
			return parallelExecutionOutput{}, err
		}
		batch, remaining, err := selectParallelBatch(pendingTransactions, input.MaxIncludedCount-len(output.Transactions))
		if err != nil {
			return parallelExecutionOutput{}, err
		}
		if len(batch) == 0 {
			return parallelExecutionOutput{}, fmt.Errorf("consensus: empty parallel execution batch")
		}

		results, err := executeParallelBatch(contextValue, input, nextState, processedTransactionIDs, batch, workerCount)
		if err != nil {
			return parallelExecutionOutput{}, err
		}
		nextState, output, err = applyParallelBatchResults(nextState, output, input, processedTransactionIDs, results)
		if err != nil {
			return parallelExecutionOutput{}, err
		}
		pendingTransactions = remaining
		output.State = nextState
	}
	return output, nil
}

func normalizeParallelExecutionContext(contextValue context.Context) context.Context {
	if contextValue == nil {
		return context.Background()
	}
	return contextValue
}

func validateParallelExecutionInput(input parallelExecutionInput) error {
	if input.Executor == nil {
		return fmt.Errorf("consensus: nil transaction executor")
	}
	if input.MaxIncludedCount < 0 {
		return fmt.Errorf("consensus: invalid max included count %d", input.MaxIncludedCount)
	}
	return nil
}

func buildScheduledTransactions(transactions []structure.Transaction) []scheduledTransaction {
	scheduledTransactions := make([]scheduledTransaction, len(transactions))
	for index, transaction := range transactions {
		scheduledTransactions[index] = scheduledTransaction{
			OriginalIndex: index,
			Transaction:   transaction,
		}
	}
	return scheduledTransactions
}

func cloneProcessedTransactionIDs(source map[string]struct{}) map[string]struct{} {
	cloned := make(map[string]struct{}, len(source))
	for transactionID := range source {
		cloned[transactionID] = struct{}{}
	}
	return cloned
}

func normalizeParallelWorkerCount(workerCount int, transactionCount int) int {
	if transactionCount <= 1 {
		return 1
	}
	if workerCount == defaultParallelExecutionWorkers {
		workerCount = goruntime.NumCPU()
	}
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > transactionCount {
		return transactionCount
	}
	return workerCount
}

func selectParallelBatch(
	pendingTransactions []scheduledTransaction,
	remainingCapacity int,
) ([]scheduledTransaction, []scheduledTransaction, error) {
	locks := runtimepkg.NewAccountLockSet()
	batch := make([]scheduledTransaction, 0, minInt(remainingCapacity, len(pendingTransactions)))
	remaining := make([]scheduledTransaction, 0, len(pendingTransactions))
	for _, scheduled := range pendingTransactions {
		if len(batch) >= remainingCapacity {
			remaining = append(remaining, scheduled)
			continue
		}
		locked, err := locks.TryLockTransaction(scheduled.Transaction)
		if err != nil {
			return nil, nil, fmt.Errorf("consensus: lock transaction %d: %w", scheduled.OriginalIndex, err)
		}
		if !locked {
			remaining = append(remaining, scheduled)
			continue
		}
		batch = append(batch, scheduled)
	}
	return batch, remaining, nil
}

func executeParallelBatch(
	contextValue context.Context,
	input parallelExecutionInput,
	state ChainState,
	processedTransactionIDs map[string]struct{},
	batch []scheduledTransaction,
	workerCount int,
) ([]parallelExecutionResult, error) {
	jobs := make(chan int)
	results := make([]parallelExecutionResult, len(batch))
	firstError := parallelExecutionError{}
	var workers sync.WaitGroup

	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for batchIndex := range jobs {
				results[batchIndex] = executeScheduledTransaction(contextValue, input, state, processedTransactionIDs, batch[batchIndex])
				if results[batchIndex].Err != nil {
					firstError.Store(results[batchIndex].Err)
				}
			}
		}()
	}

	for batchIndex := range batch {
		if err := contextValue.Err(); err != nil {
			close(jobs)
			workers.Wait()
			return nil, err
		}
		if firstError.Load() != nil {
			close(jobs)
			workers.Wait()
			return nil, firstError.Load()
		}
		jobs <- batchIndex
	}
	close(jobs)
	workers.Wait()
	if err := firstError.Load(); err != nil {
		return nil, err
	}
	return results, nil
}

func executeScheduledTransaction(
	contextValue context.Context,
	input parallelExecutionInput,
	state ChainState,
	processedTransactionIDs map[string]struct{},
	scheduled scheduledTransaction,
) parallelExecutionResult {
	result, err := input.Executor.ExecuteTransaction(contextValue, runtimepkg.TransactionRequest{
		ChainID: input.ChainID,
		Slot:    input.Slot,
		Epoch:   input.Epoch,
		Mode:    input.ExecutionMode,
		Simulation: runtimepkg.TransactionSimulationInput{
			Transaction:    scheduled.Transaction,
			Accounts:       state.Accounts,
			BlockhashQueue: input.BlockhashQueue,
			CurrentSlot:    input.Slot,
			CurrentEpoch:   input.Epoch,
			ProcessedTxIDs: processedTransactionIDs,
		},
	})
	if err != nil {
		err = fmt.Errorf("consensus: execute transaction %d: %w", scheduled.OriginalIndex, err)
	}
	return parallelExecutionResult{
		OriginalIndex: scheduled.OriginalIndex,
		Transaction:   scheduled.Transaction,
		Result:        result,
		Err:           err,
	}
}

func applyParallelBatchResults(
	state ChainState,
	output parallelExecutionOutput,
	input parallelExecutionInput,
	processedTransactionIDs map[string]struct{},
	results []parallelExecutionResult,
) (ChainState, parallelExecutionOutput, error) {
	nextState := state
	for _, executionResult := range results {
		if executionResult.Result.Execution.Status != structure.TransactionStatusConfirmed {
			if input.RejectFailed {
				return ChainState{}, parallelExecutionOutput{}, fmt.Errorf(
					"consensus: %s transaction %d failed: %s",
					input.FailureLabel,
					executionResult.OriginalIndex,
					transactionExecutionFailureMessage(executionResult.Result.Execution),
				)
			}
			rejectedTransaction, err := buildRejectedTransaction(executionResult)
			if err != nil {
				return ChainState{}, parallelExecutionOutput{}, err
			}
			output.RejectedTransactions = append(output.RejectedTransactions, rejectedTransaction)
			continue
		}
		transactionID, err := executionResult.Transaction.TxIDString()
		if err != nil {
			return ChainState{}, parallelExecutionOutput{}, fmt.Errorf("consensus: transaction id %d: %w", executionResult.OriginalIndex, err)
		}
		processedTransactionIDs[transactionID] = struct{}{}
		nextState = nextState.applyWrites(executionResult.Result.Execution.WrittenAccounts)

		transaction := executionResult.Transaction
		if input.MarkIncluded {
			transaction = markTransactionConfirmed(transaction, executionResult.Result.Execution.FeeDetails.TotalFee)
		}
		output.Transactions = append(output.Transactions, transaction)
		output.FeeDetails = append(output.FeeDetails, executionResult.Result.Execution.FeeDetails)
		receiptHash, err := hashReceipt(executionResult.Result.Execution)
		if err != nil {
			return ChainState{}, parallelExecutionOutput{}, fmt.Errorf("consensus: hash receipt %d: %w", executionResult.OriginalIndex, err)
		}
		output.Receipts = append(output.Receipts, receiptHash)
	}
	return nextState, output, nil
}

func buildRejectedTransaction(executionResult parallelExecutionResult) (RejectedTransaction, error) {
	transactionID, err := executionResult.Transaction.TxIDString()
	if err != nil {
		return RejectedTransaction{}, fmt.Errorf("consensus: rejected transaction id %d: %w", executionResult.OriginalIndex, err)
	}
	return RejectedTransaction{
		TransactionID: transactionID,
		Transaction:   executionResult.Transaction.Clone(),
		Status:        executionResult.Result.Execution.Status,
		Error:         transactionExecutionFailureMessage(executionResult.Result.Execution),
	}, nil
}

func transactionExecutionFailureMessage(result structure.TransactionExecutionResult) string {
	if result.Error == nil {
		return "transaction rejected"
	}
	return result.Error.Error()
}

type parallelExecutionError struct {
	mutex sync.Mutex
	err   error
}

func (executionError *parallelExecutionError) Store(err error) {
	if err == nil {
		return
	}
	executionError.mutex.Lock()
	defer executionError.mutex.Unlock()
	if executionError.err == nil {
		executionError.err = err
	}
}

func (executionError *parallelExecutionError) Load() error {
	executionError.mutex.Lock()
	defer executionError.mutex.Unlock()
	return executionError.err
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
