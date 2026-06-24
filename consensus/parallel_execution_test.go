package consensus

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	runtimepkg "solana_golang/runtime"
	"solana_golang/structure"
)

func TestExecuteTransactionsParallelRunsNonConflictingAccountsTogether(t *testing.T) {
	firstTarget := parallelTestPublicKey(11)
	secondTarget := parallelTestPublicKey(12)
	executor := newParallelTestExecutor(20 * time.Millisecond)

	output, err := executeTransactionsParallel(context.Background(), parallelExecutionInput{
		ChainID:          "parallel-test",
		Slot:             1,
		Epoch:            1,
		Transactions:     []structure.Transaction{parallelTestTransaction(1, firstTarget), parallelTestTransaction(2, secondTarget)},
		State:            parallelTestState(firstTarget, secondTarget),
		ProcessedTxIDs:   map[string]struct{}{},
		Executor:         executor,
		MaxIncludedCount: MaxProposalTransactions,
		WorkerCount:      2,
		RejectFailed:     true,
		MarkIncluded:     false,
		FailureLabel:     "parallel",
		ExecutionMode:    runtimepkg.ExecutionModeFixedInstruction,
	})
	if err != nil {
		t.Fatalf("executeTransactionsParallel() error = %v", err)
	}
	if executor.MaxActive() < 2 {
		t.Fatalf("max active workers = %d, want at least 2", executor.MaxActive())
	}
	if len(output.Transactions) != 2 {
		t.Fatalf("transactions = %d, want 2", len(output.Transactions))
	}
	assertParallelTestLamports(t, output.State, firstTarget, 1)
	assertParallelTestLamports(t, output.State, secondTarget, 1)
}

func TestExecuteTransactionsParallelSerializesConflictingWritableAccounts(t *testing.T) {
	target := parallelTestPublicKey(21)
	executor := newParallelTestExecutor(10 * time.Millisecond)

	output, err := executeTransactionsParallel(context.Background(), parallelExecutionInput{
		ChainID:          "parallel-test",
		Slot:             1,
		Epoch:            1,
		Transactions:     []structure.Transaction{parallelTestTransaction(3, target), parallelTestTransaction(4, target)},
		State:            parallelTestState(target),
		ProcessedTxIDs:   map[string]struct{}{},
		Executor:         executor,
		MaxIncludedCount: MaxProposalTransactions,
		WorkerCount:      2,
		RejectFailed:     true,
		MarkIncluded:     false,
		FailureLabel:     "parallel",
		ExecutionMode:    runtimepkg.ExecutionModeFixedInstruction,
	})
	if err != nil {
		t.Fatalf("executeTransactionsParallel() error = %v", err)
	}
	if executor.MaxActive() != 1 {
		t.Fatalf("max active workers = %d, want 1", executor.MaxActive())
	}
	assertParallelTestLamports(t, output.State, target, 2)
}

func TestExecuteTransactionsParallelReportsRejectedTransactions(t *testing.T) {
	confirmedTarget := parallelTestPublicKey(31)
	rejectedTarget := parallelTestPublicKey(32)
	executor := parallelRejectingTestExecutor{}

	output, err := executeTransactionsParallel(context.Background(), parallelExecutionInput{
		ChainID:          "parallel-test",
		Slot:             1,
		Epoch:            1,
		Transactions:     []structure.Transaction{parallelTestTransaction(5, rejectedTarget), parallelTestTransaction(6, confirmedTarget)},
		State:            parallelTestState(confirmedTarget, rejectedTarget),
		ProcessedTxIDs:   map[string]struct{}{},
		Executor:         executor,
		MaxIncludedCount: MaxProposalTransactions,
		WorkerCount:      2,
		RejectFailed:     false,
		MarkIncluded:     false,
		FailureLabel:     "parallel",
		ExecutionMode:    runtimepkg.ExecutionModeFixedInstruction,
	})
	if err != nil {
		t.Fatalf("executeTransactionsParallel() error = %v", err)
	}
	if len(output.Transactions) != 1 {
		t.Fatalf("transactions = %d, want 1", len(output.Transactions))
	}
	if len(output.RejectedTransactions) != 1 {
		t.Fatalf("rejected transactions = %d, want 1", len(output.RejectedTransactions))
	}
	if output.RejectedTransactions[0].Error != "parallel rejection" {
		t.Fatalf("rejected error = %q", output.RejectedTransactions[0].Error)
	}
	assertParallelTestLamports(t, output.State, confirmedTarget, 1)
}

type parallelTestExecutor struct {
	delay     time.Duration
	mutex     sync.Mutex
	active    int
	maxActive int
}

func newParallelTestExecutor(delay time.Duration) *parallelTestExecutor {
	return &parallelTestExecutor{delay: delay}
}

func (executor *parallelTestExecutor) ExecuteTransaction(
	contextValue context.Context,
	request runtimepkg.TransactionRequest,
) (runtimepkg.TransactionResult, error) {
	if err := contextValue.Err(); err != nil {
		return runtimepkg.TransactionResult{}, err
	}
	executor.start()
	defer executor.finish()
	time.Sleep(executor.delay)

	targetAddress, err := parallelTestTargetAddress(request.Simulation.Transaction)
	if err != nil {
		return runtimepkg.TransactionResult{}, err
	}
	account, err := parallelTestAccount(request.Simulation.Accounts, targetAddress)
	if err != nil {
		return runtimepkg.TransactionResult{}, err
	}
	account.Lamports++
	return runtimepkg.TransactionResult{
		Mode: request.Mode,
		Execution: structure.TransactionExecutionResult{
			Status:       structure.TransactionStatusConfirmed,
			PostBalances: []uint64{account.Lamports},
			WrittenAccounts: []structure.AddressedAccount{
				{Address: targetAddress, Account: account},
			},
		},
	}, nil
}

func (executor *parallelTestExecutor) start() {
	executor.mutex.Lock()
	defer executor.mutex.Unlock()
	executor.active++
	if executor.active > executor.maxActive {
		executor.maxActive = executor.active
	}
}

func (executor *parallelTestExecutor) finish() {
	executor.mutex.Lock()
	defer executor.mutex.Unlock()
	executor.active--
}

func (executor *parallelTestExecutor) MaxActive() int {
	executor.mutex.Lock()
	defer executor.mutex.Unlock()
	return executor.maxActive
}

type parallelRejectingTestExecutor struct{}

func (executor parallelRejectingTestExecutor) ExecuteTransaction(
	contextValue context.Context,
	request runtimepkg.TransactionRequest,
) (runtimepkg.TransactionResult, error) {
	targetAddress, err := parallelTestTargetAddress(request.Simulation.Transaction)
	if err != nil {
		return runtimepkg.TransactionResult{}, err
	}
	if request.Simulation.Transaction.Instructions[0].Data[0] == 5 {
		return runtimepkg.TransactionResult{
			Mode: request.Mode,
			Execution: structure.TransactionExecutionResult{
				Status: structure.TransactionStatusFailed,
				Error: &structure.TransactionError{
					Code:    structure.TransactionErrorCodeInstructionError,
					Message: "parallel rejection",
				},
			},
		}, nil
	}
	account, err := parallelTestAccount(request.Simulation.Accounts, targetAddress)
	if err != nil {
		return runtimepkg.TransactionResult{}, err
	}
	account.Lamports++
	return runtimepkg.TransactionResult{
		Mode: request.Mode,
		Execution: structure.TransactionExecutionResult{
			Status:       structure.TransactionStatusConfirmed,
			PostBalances: []uint64{account.Lamports},
			WrittenAccounts: []structure.AddressedAccount{
				{Address: targetAddress, Account: account},
			},
		},
	}, nil
}

func parallelTestTransaction(seed byte, targetAddress structure.PublicKey) structure.Transaction {
	payerAddress := parallelTestPublicKey(seed)
	programAddress := parallelTestPublicKey(250)
	return structure.Transaction{
		Signatures:      []structure.Signature{parallelTestSignature(seed)},
		RecentBlockhash: parallelTestHash(1),
		Accounts: []structure.AccountMeta{
			{PublicKey: payerAddress, IsSigner: true, IsWritable: true},
			{PublicKey: targetAddress, IsSigner: false, IsWritable: true},
			{PublicKey: programAddress, IsSigner: false, IsWritable: false},
		},
		Instructions: []structure.CompiledInstruction{
			{ProgramIDIndex: 2, AccountIndexes: []uint8{0, 1}, Data: []byte{seed}},
		},
	}
}

func parallelTestState(addresses ...structure.PublicKey) ChainState {
	accounts := make([]structure.AddressedAccount, 0, len(addresses))
	for _, address := range addresses {
		accounts = append(accounts, structure.AddressedAccount{
			Address: address,
			Account: structure.Account{Owner: structure.DefaultBuiltinProgramIDs.System},
		})
	}
	return ChainState{Accounts: accounts}
}

func parallelTestTargetAddress(transaction structure.Transaction) (structure.PublicKey, error) {
	if len(transaction.Accounts) < 2 {
		return structure.PublicKey{}, fmt.Errorf("missing target account")
	}
	return transaction.Accounts[1].PublicKey, nil
}

func parallelTestAccount(accounts []structure.AddressedAccount, address structure.PublicKey) (structure.Account, error) {
	for _, account := range accounts {
		if account.Address == address {
			return account.Account.Clone(), nil
		}
	}
	return structure.Account{}, fmt.Errorf("account not found")
}

func assertParallelTestLamports(t *testing.T, state ChainState, address structure.PublicKey, want uint64) {
	t.Helper()
	account, err := parallelTestAccount(state.Accounts, address)
	if err != nil {
		t.Fatal(err)
	}
	if account.Lamports != want {
		t.Fatalf("lamports = %d, want %d", account.Lamports, want)
	}
}

func parallelTestPublicKey(seed byte) structure.PublicKey {
	var publicKey structure.PublicKey
	publicKey[31] = seed
	return publicKey
}

func parallelTestSignature(seed byte) structure.Signature {
	var signature structure.Signature
	signature[63] = seed
	return signature
}

func parallelTestHash(seed byte) structure.Hash {
	var hash structure.Hash
	hash[31] = seed
	return hash
}
