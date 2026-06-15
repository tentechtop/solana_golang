package structure

import (
	"testing"

	svm "solana_golang/vm"
)

func TestTransactionSimulatorExecutesVirtualMachineProgram(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	dataKey := newTestPublicKey(151)
	programKey := newTestPublicKey(152)
	blockhash := newTestHash(153)
	instructionData := []byte("hello-vm")
	programData := mustVMProgramData(t, svm.BuildProgramCode(svm.BuildWriteInstructionDataOp(0, 0)))
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

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, payerKey, payerLamports, DefaultBuiltinProgramIDs.System, false),
			newSimulationDataAccount(t, dataKey, dataLamports, programKey, false, nil),
			newSimulationDataAccount(t, programKey, programLamports, DefaultBuiltinProgramIDs.BPFLoader, true, programData),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 80),
		CurrentSlot:    80,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("Status = %d, want confirmed: %v", result.Status, result.Error)
	}
	writtenDataAccount := findWrittenAccount(t, result.WrittenAccounts, dataKey)
	if string(writtenDataAccount.Data) != string(instructionData) {
		t.Fatalf("data account = %q, want %q", string(writtenDataAccount.Data), string(instructionData))
	}
}

func TestTransactionSimulatorRejectsVMReadonlyWrite(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	dataKey := newTestPublicKey(154)
	programKey := newTestPublicKey(155)
	blockhash := newTestHash(156)
	programData := mustVMProgramData(t, svm.BuildProgramCode(svm.BuildWriteInstructionDataOp(0, 0)))
	transaction := signedSimulationProgramTransaction(t, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: dataKey, IsSigner: false, IsWritable: false},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{dataKey}, []byte("blocked"), blockhash, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, payerKey, mustMinimumBalance(t, 0)+LamportsPerSignature+100, DefaultBuiltinProgramIDs.System, false),
			newSimulationDataAccount(t, dataKey, mustMinimumBalance(t, 16), programKey, false, nil),
			newSimulationDataAccount(t, programKey, mustMinimumBalance(t, len(programData)), DefaultBuiltinProgramIDs.BPFLoader, true, programData),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 81),
		CurrentSlot:    81,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusFailed {
		t.Fatalf("Status = %d, want failed", result.Status)
	}
	writtenDataAccount := findWrittenAccount(t, result.WrittenAccounts, dataKey)
	if len(writtenDataAccount.Data) != 0 {
		t.Fatalf("readonly data account mutated to %q", string(writtenDataAccount.Data))
	}
}

func mustVMProgramData(t *testing.T, code []byte) []byte {
	t.Helper()

	encoded, err := svm.EncodeBytecode(code)
	if err != nil {
		t.Fatalf("EncodeBytecode() error = %v", err)
	}
	return encoded
}

func newSimulationDataAccount(t *testing.T, address PublicKey, lamports uint64, owner PublicKey, executable bool, data []byte) AddressedAccount {
	t.Helper()

	account, err := NewAccount(lamports, data, owner, executable, 0)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	return AddressedAccount{Address: address, Account: account}
}
