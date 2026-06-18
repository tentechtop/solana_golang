package runtime_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	systemprogram "solana_golang/programs/system"
	runtimepkg "solana_golang/runtime"
)

func TestFixedExecutorWritesAuditLog(t *testing.T) {
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, nil))
	executor, err := runtimepkg.NewFixedExecutor(systemprogram.NewProgram(DefaultBuiltinProgramIDs.System))
	if err != nil {
		t.Fatalf("NewFixedExecutor() error = %v", err)
	}
	executor.Logger = logger

	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	destinationKey := newTestPublicKey(201)
	blockhash := newTestHash(202)
	amount := uint64(100)
	sourceLamports := mustMinimumBalance(t, 0) + LamportsPerSignature + amount + 100
	destinationLamports := mustMinimumBalance(t, 0)
	transferInstruction, err := NewTransferInstruction(TransferParams{Lamports: amount})
	instructionData := mustSystemInstructionBytes(t, transferInstruction, err)
	transaction := signedSimulationTransaction(t, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, destinationKey}, instructionData, blockhash, map[PublicKey][]byte{
		sourceKey: sourcePrivateKey,
	})

	result, err := executor.ExecuteTransaction(context.Background(), runtimepkg.TransactionRequest{
		ChainID: "audit-chain",
		Slot:    10,
		Mode:    runtimepkg.ExecutionModeFixedInstruction,
		Simulation: TransactionSimulationInput{
			Transaction:    transaction,
			Accounts:       simulationAccounts(t, sourceKey, sourceLamports, destinationKey, destinationLamports),
			BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 10),
			CurrentSlot:    10,
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTransaction() error = %v", err)
	}
	if result.Execution.Status != TransactionStatusConfirmed {
		t.Fatalf("status = %d, want confirmed", result.Execution.Status)
	}

	logLine := logOutput.String()
	for _, expected := range []string{
		`"msg":"runtime transaction executed"`,
		`"chain_id":"audit-chain"`,
		`"tx_id"`,
		`"privacy_execution_mode":"fixed"`,
		`"program_execution_policy"`,
		`"fee_total"`,
		`"balance_changes"`,
	} {
		if !strings.Contains(logLine, expected) {
			t.Fatalf("log output = %q, want %s", logLine, expected)
		}
	}
}

func TestFixedExecutorRunsRegisteredProgramHandler(t *testing.T) {
	executor := runtimepkg.NewFixedExecutorWithRegistry(runtimepkg.NewProgramHandlerRegistry())
	systemProgram := systemprogram.NewProgram(DefaultBuiltinProgramIDs.System)
	if err := executor.RegisterProgramHandler(runtimepkg.ProgramSpec{
		ID:   DefaultBuiltinProgramIDs.System,
		Name: "system",
	}, systemProgram.Execute); err != nil {
		t.Fatalf("RegisterProgramHandler() error = %v", err)
	}

	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	destinationKey := newTestPublicKey(211)
	blockhash := newTestHash(212)
	amount := uint64(120)
	sourceLamports := mustMinimumBalance(t, 0) + LamportsPerSignature + amount + 100
	destinationLamports := mustMinimumBalance(t, 0)
	transferInstruction, err := NewTransferInstruction(TransferParams{Lamports: amount})
	instructionData := mustSystemInstructionBytes(t, transferInstruction, err)
	transaction := signedSimulationTransaction(t, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, destinationKey}, instructionData, blockhash, map[PublicKey][]byte{
		sourceKey: sourcePrivateKey,
	})

	result, err := executor.ExecuteTransaction(context.Background(), runtimepkg.TransactionRequest{
		ChainID: "handler-chain",
		Slot:    20,
		Mode:    runtimepkg.ExecutionModeFixedInstruction,
		Simulation: TransactionSimulationInput{
			Transaction:    transaction,
			Accounts:       simulationAccounts(t, sourceKey, sourceLamports, destinationKey, destinationLamports),
			BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 20),
			CurrentSlot:    20,
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTransaction() error = %v", err)
	}
	if result.Execution.Status != TransactionStatusConfirmed {
		t.Fatalf("status = %d, want confirmed", result.Execution.Status)
	}
	if got, want := result.Execution.PostBalances[0], sourceLamports-LamportsPerSignature-amount; got != want {
		t.Fatalf("source balance = %d, want %d", got, want)
	}
	if got, want := result.Execution.PostBalances[1], destinationLamports+amount; got != want {
		t.Fatalf("destination balance = %d, want %d", got, want)
	}
}
