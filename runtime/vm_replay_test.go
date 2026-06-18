package runtime_test

import (
	"reflect"
	"testing"

	svm "solana_golang/vm"
)

func TestVirtualMachineReplayIsDeterministicAcrossNodes(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	dataKey := newTestPublicKey(26)
	programKey := newTestPublicKey(27)
	blockhash := newTestHash(28)
	instructionData := []byte("replay-data")
	programData := mustGovernedWriteInstructionDataProgram(t)
	payerLamports := mustMinimumBalance(t, 0) + LamportsPerSignature + 100
	dataLamports := mustMinimumBalance(t, len(instructionData))
	programLamports := mustMinimumBalance(t, len(programData))
	transaction := signedSimulationProgramTransaction(t, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: dataKey, IsSigner: false, IsWritable: true},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{dataKey}, instructionData, blockhash, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})
	input := TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, payerKey, payerLamports, DefaultBuiltinProgramIDs.System, false),
			newSimulationDataAccount(t, dataKey, dataLamports, programKey, false, nil),
			newSimulationDataAccount(t, programKey, programLamports, DefaultBuiltinProgramIDs.BPFLoader, true, programData),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 220),
		CurrentSlot:    220,
	}

	firstResult, err := simulateWithVirtualMachine(t, input)
	if err != nil {
		t.Fatalf("first Simulate() error = %v", err)
	}
	secondResult, err := simulateWithVirtualMachine(t, input)
	if err != nil {
		t.Fatalf("second Simulate() error = %v", err)
	}
	if firstResult.Status != TransactionStatusConfirmed || secondResult.Status != TransactionStatusConfirmed {
		t.Fatalf("status first=%d second=%d errors %v/%v", firstResult.Status, secondResult.Status, firstResult.Error, secondResult.Error)
	}
	if firstResult.ComputeUnitsConsumed == 0 {
		t.Fatal("ComputeUnitsConsumed = 0, want VM metering")
	}
	if firstResult.ComputeUnitsConsumed != secondResult.ComputeUnitsConsumed || firstResult.CostUnits != secondResult.CostUnits {
		t.Fatalf("compute mismatch first=%d/%d second=%d/%d", firstResult.ComputeUnitsConsumed, firstResult.CostUnits, secondResult.ComputeUnitsConsumed, secondResult.CostUnits)
	}
	if !reflect.DeepEqual(firstResult.PostBalances, secondResult.PostBalances) {
		t.Fatalf("post balances mismatch first=%v second=%v", firstResult.PostBalances, secondResult.PostBalances)
	}
	if !reflect.DeepEqual(firstResult.WrittenAccounts, secondResult.WrittenAccounts) {
		t.Fatalf("written accounts mismatch first=%+v second=%+v", firstResult.WrittenAccounts, secondResult.WrittenAccounts)
	}
}

func mustGovernedWriteInstructionDataProgram(t *testing.T) []byte {
	t.Helper()
	programData, err := svm.EncodeGovernedBytecode(
		svm.BuildProgramCode(svm.BuildWriteInstructionDataOp(0, 0)),
		svm.ProgramManifest{ComputeUnitLimit: svm.DefaultComputeUnitLimit},
	)
	if err != nil {
		t.Fatalf("EncodeGovernedBytecode() error = %v", err)
	}
	return programData
}
