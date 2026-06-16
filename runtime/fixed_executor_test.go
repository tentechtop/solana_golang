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
		`"fee_total"`,
		`"balance_changes"`,
	} {
		if !strings.Contains(logLine, expected) {
			t.Fatalf("log output = %q, want %s", logLine, expected)
		}
	}
}
