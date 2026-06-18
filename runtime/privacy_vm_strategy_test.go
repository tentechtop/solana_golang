package runtime_test

import (
	"reflect"
	"testing"
)

func TestPrivacyVMSyscallMatchesFixedDeposit(t *testing.T) {
	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	stateKey := newTestPublicKey(221)
	blockhash := newTestHash(222)
	amount := uint64(900)
	commitment := newTestHash(223)
	sourceLamports := mustMinimumBalance(t, 0) + LamportsPerSignature + amount + 100
	stateLamports := mustMinimumBalance(t, 512)
	instruction, err := NewPrivacyDepositInstruction(PrivacyStateVersion, nil, PrivacyDepositParams{
		Amount:         amount,
		Commitment:     commitment,
		SpendAuthority: sourceKey,
		EncryptedNote:  []byte("mode-deposit"),
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: stateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, stateKey}, instructionData, blockhash, map[PublicKey][]byte{
		sourceKey: sourcePrivateKey,
	})
	input := TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       privacySimulationAccounts(t, sourceKey, sourceLamports, stateKey, stateLamports),
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 90),
		CurrentSlot:    90,
	}

	assertPrivacyModeResultsEqual(t, input)
}

func TestPrivacyVMSyscallMatchesFixedWithdraw(t *testing.T) {
	destinationKey, destinationPrivateKey := newSimulationSigner(t)
	stateKey := newTestPublicKey(231)
	blockhash := newTestHash(232)
	amount := uint64(700)
	commitment := newTestHash(233)
	nullifier := newTestHash(234)
	stateAccount := privacyStateAccountWithNote(t, stateKey, destinationKey, commitment, amount)
	destinationLamports := mustMinimumBalance(t, 0) + LamportsPerSignature + 100
	instruction, err := NewPrivacyWithdrawInstruction(PrivacyStateVersion, nil, PrivacyWithdrawParams{
		Amount:           amount,
		SourceCommitment: commitment,
		Nullifier:        nullifier,
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: destinationKey, IsSigner: true, IsWritable: true},
		{PublicKey: stateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{stateKey, destinationKey}, instructionData, blockhash, map[PublicKey][]byte{
		destinationKey: destinationPrivateKey,
	})
	input := TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, destinationKey, destinationLamports, DefaultBuiltinProgramIDs.System, false),
			stateAccount,
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 91),
		CurrentSlot:    91,
	}

	assertPrivacyModeResultsEqual(t, input)
}

func TestPrivacyVMSyscallMatchesFixedTransfer(t *testing.T) {
	authorityKey, authorityPrivateKey := newSimulationSigner(t)
	receiverKey := newTestPublicKey(241)
	stateKey := newTestPublicKey(242)
	blockhash := newTestHash(243)
	amount := uint64(800)
	sourceCommitment := newTestHash(244)
	nullifier := newTestHash(245)
	outputCommitment := newTestHash(246)
	stateAccount := privacyStateAccountWithNote(t, stateKey, authorityKey, sourceCommitment, amount)
	instruction, err := NewPrivacyTransferInstruction(PrivacyStateVersion, nil, PrivacyTransferParams{
		Amount:               amount,
		SourceCommitment:     sourceCommitment,
		Nullifier:            nullifier,
		OutputCommitment:     outputCommitment,
		OutputSpendAuthority: receiverKey,
		OutputEncryptedNote:  []byte("mode-transfer"),
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: authorityKey, IsSigner: true, IsWritable: true},
		{PublicKey: stateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{stateKey}, instructionData, blockhash, map[PublicKey][]byte{
		authorityKey: authorityPrivateKey,
	})
	input := TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, authorityKey, mustMinimumBalance(t, 0)+LamportsPerSignature+100, DefaultBuiltinProgramIDs.System, false),
			stateAccount,
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 92),
		CurrentSlot:    92,
	}

	assertPrivacyModeResultsEqual(t, input)
}

func assertPrivacyModeResultsEqual(t *testing.T, input TransactionSimulationInput) {
	t.Helper()

	fixedResult, err := simulateWithDefaultPrograms(t, input)
	if err != nil {
		t.Fatalf("fixed Simulate() error = %v", err)
	}
	vmResult, err := simulateWithPrivacyVMSyscall(t, input)
	if err != nil {
		t.Fatalf("vm syscall Simulate() error = %v", err)
	}
	if fixedResult.Status != TransactionStatusConfirmed || vmResult.Status != TransactionStatusConfirmed {
		t.Fatalf("status fixed=%d vm=%d fixedErr=%v vmErr=%v", fixedResult.Status, vmResult.Status, fixedResult.Error, vmResult.Error)
	}
	if !reflect.DeepEqual(fixedResult.PostBalances, vmResult.PostBalances) {
		t.Fatalf("post balances fixed=%v vm=%v", fixedResult.PostBalances, vmResult.PostBalances)
	}
	if !reflect.DeepEqual(fixedResult.WrittenAccounts, vmResult.WrittenAccounts) {
		t.Fatalf("written accounts fixed=%+v vm=%+v", fixedResult.WrittenAccounts, vmResult.WrittenAccounts)
	}
}
