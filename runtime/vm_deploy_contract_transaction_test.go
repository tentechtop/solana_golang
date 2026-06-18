package runtime_test

import (
	"bytes"
	"testing"

	"solana_golang/blockchain"
	"solana_golang/structure"
)

func TestWalletDeployContractTransactionCreatesExecutableProgramAccount(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	programKey, programPrivateKey := newSimulationSigner(t)
	blockhash := newTestHash(230)
	slot := uint64(230)
	depositLamports := uint64(777)
	programData := mustERC20LikeContractProgramData(t)
	programLamports := mustMinimumBalance(t, len(programData)) + depositLamports

	transaction, err := blockchain.NewDeployContractTransaction(
		structure.SolanaKeyPair{PublicKey: payerKey, PrivateKey: payerPrivateKey},
		structure.SolanaKeyPair{PublicKey: programKey, PrivateKey: programPrivateKey},
		programData,
		depositLamports,
		blockhash,
	)
	if err != nil {
		t.Fatalf("NewDeployContractTransaction() error = %v", err)
	}
	transactionFee := mustTransactionFeeDetails(t, transaction).TotalFee
	payerLamports := mustMinimumBalance(t, 0) + transactionFee + programLamports + 100

	result, err := simulateWithVirtualMachine(t, TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, payerKey, payerLamports, DefaultBuiltinProgramIDs.System, false),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, slot),
		CurrentSlot:    slot,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("Status = %d, error = %v", result.Status, result.Error)
	}

	payerAccount := findWrittenAccount(t, result.WrittenAccounts, payerKey)
	programAccount := findWrittenAccount(t, result.WrittenAccounts, programKey)
	if payerAccount.Lamports != payerLamports-transactionFee-programLamports {
		t.Fatalf("payer lamports = %d, want %d", payerAccount.Lamports, payerLamports-transactionFee-programLamports)
	}
	if programAccount.Lamports != programLamports {
		t.Fatalf("program lamports = %d, want %d", programAccount.Lamports, programLamports)
	}
	if programAccount.Owner != DefaultBuiltinProgramIDs.BPFLoader {
		t.Fatalf("program owner = %s, want bpfloader", programAccount.Owner.String())
	}
	if !programAccount.Executable {
		t.Fatal("program account is not executable")
	}
	if !bytes.Equal(programAccount.Data, programData) {
		t.Fatal("program account data does not match bytecode")
	}
	if result.ComputeUnitsConsumed != structure.DefaultBuiltinInstructionCU*2 {
		t.Fatalf("compute units = %d, want %d", result.ComputeUnitsConsumed, structure.DefaultBuiltinInstructionCU*2)
	}
}
