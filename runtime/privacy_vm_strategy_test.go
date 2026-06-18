package runtime_test

import (
	"reflect"
	"testing"
)

func TestPrivacyVMSyscallSupportsCurrentFourTransferModes(t *testing.T) {
	t.Run("transparent_to_transparent", assertVMSyscallTransparentToTransparent)
	t.Run("transparent_to_private", assertVMSyscallTransparentToPrivate)
	t.Run("private_to_transparent", assertVMSyscallPrivateToTransparent)
	t.Run("private_to_private", assertVMSyscallPrivateToPrivate)
}

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

func assertVMSyscallTransparentToTransparent(t *testing.T) {
	t.Helper()

	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	destinationKey := newTestPublicKey(251)
	blockhash := newTestHash(252)
	amount := uint64(600)
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

	result := simulatePrivacyVMSyscallConfirmed(t, TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       simulationAccounts(t, sourceKey, sourceLamports, destinationKey, destinationLamports),
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 93),
		CurrentSlot:    93,
	})
	sourceWritten := findWrittenAccount(t, result.WrittenAccounts, sourceKey)
	destinationWritten := findWrittenAccount(t, result.WrittenAccounts, destinationKey)
	if sourceWritten.Lamports != sourceLamports-LamportsPerSignature-amount {
		t.Fatalf("source lamports = %d, want %d", sourceWritten.Lamports, sourceLamports-LamportsPerSignature-amount)
	}
	if destinationWritten.Lamports != destinationLamports+amount {
		t.Fatalf("destination lamports = %d, want %d", destinationWritten.Lamports, destinationLamports+amount)
	}
}

func assertVMSyscallTransparentToPrivate(t *testing.T) {
	t.Helper()

	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	stateKey := newTestPublicKey(253)
	blockhash := newTestHash(254)
	amount := uint64(700)
	commitment := newTestHash(255)
	sourceLamports := mustMinimumBalance(t, 0) + LamportsPerSignature + amount + 100
	stateLamports := mustMinimumBalance(t, 512)
	instruction, err := NewPrivacyDepositInstruction(PrivacyStateVersion, nil, PrivacyDepositParams{
		Amount:         amount,
		Commitment:     commitment,
		SpendAuthority: sourceKey,
		EncryptedNote:  []byte("vm-mode-deposit"),
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: stateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, stateKey}, instructionData, blockhash, map[PublicKey][]byte{
		sourceKey: sourcePrivateKey,
	})

	result := simulatePrivacyVMSyscallConfirmed(t, TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       privacySimulationAccounts(t, sourceKey, sourceLamports, stateKey, stateLamports),
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 94),
		CurrentSlot:    94,
	})
	sourceWritten := findWrittenAccount(t, result.WrittenAccounts, sourceKey)
	stateWritten := findWrittenAccount(t, result.WrittenAccounts, stateKey)
	state := mustPrivacyStateFromData(t, stateWritten.Data)
	if sourceWritten.Lamports != sourceLamports-LamportsPerSignature-amount {
		t.Fatalf("source lamports = %d, want %d", sourceWritten.Lamports, sourceLamports-LamportsPerSignature-amount)
	}
	if stateWritten.Lamports != stateLamports+amount {
		t.Fatalf("state lamports = %d, want %d", stateWritten.Lamports, stateLamports+amount)
	}
	if len(state.Notes) != 1 || state.Notes[0].Commitment != commitment || state.Notes[0].Amount != amount {
		t.Fatalf("privacy notes = %+v, want deposited note", state.Notes)
	}
}

func assertVMSyscallPrivateToTransparent(t *testing.T) {
	t.Helper()

	destinationKey, destinationPrivateKey := newSimulationSigner(t)
	stateKey := newTestPublicKey(1)
	blockhash := newTestHash(2)
	amount := uint64(800)
	commitment := newTestHash(3)
	nullifier := newTestHash(4)
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

	result := simulatePrivacyVMSyscallConfirmed(t, TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, destinationKey, destinationLamports, DefaultBuiltinProgramIDs.System, false),
			stateAccount,
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 95),
		CurrentSlot:    95,
	})
	destinationWritten := findWrittenAccount(t, result.WrittenAccounts, destinationKey)
	stateWritten := findWrittenAccount(t, result.WrittenAccounts, stateKey)
	state := mustPrivacyStateFromData(t, stateWritten.Data)
	if destinationWritten.Lamports != destinationLamports-LamportsPerSignature+amount {
		t.Fatalf("destination lamports = %d, want %d", destinationWritten.Lamports, destinationLamports-LamportsPerSignature+amount)
	}
	if len(state.SpentNullifiers) != 1 || state.SpentNullifiers[0] != nullifier || !state.Notes[0].Spent {
		t.Fatalf("privacy state = %+v, want spent note and nullifier", state)
	}
}

func assertVMSyscallPrivateToPrivate(t *testing.T) {
	t.Helper()

	authorityKey, authorityPrivateKey := newSimulationSigner(t)
	receiverKey := newTestPublicKey(5)
	stateKey := newTestPublicKey(6)
	blockhash := newTestHash(7)
	amount := uint64(900)
	sourceCommitment := newTestHash(8)
	nullifier := newTestHash(9)
	outputCommitment := newTestHash(10)
	stateAccount := privacyStateAccountWithNote(t, stateKey, authorityKey, sourceCommitment, amount)
	instruction, err := NewPrivacyTransferInstruction(PrivacyStateVersion, nil, PrivacyTransferParams{
		Amount:               amount,
		SourceCommitment:     sourceCommitment,
		Nullifier:            nullifier,
		OutputCommitment:     outputCommitment,
		OutputSpendAuthority: receiverKey,
		OutputEncryptedNote:  []byte("vm-mode-transfer"),
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: authorityKey, IsSigner: true, IsWritable: true},
		{PublicKey: stateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{stateKey}, instructionData, blockhash, map[PublicKey][]byte{
		authorityKey: authorityPrivateKey,
	})

	result := simulatePrivacyVMSyscallConfirmed(t, TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, authorityKey, mustMinimumBalance(t, 0)+LamportsPerSignature+100, DefaultBuiltinProgramIDs.System, false),
			stateAccount,
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 96),
		CurrentSlot:    96,
	})
	stateWritten := findWrittenAccount(t, result.WrittenAccounts, stateKey)
	state := mustPrivacyStateFromData(t, stateWritten.Data)
	if len(state.Notes) != 2 || !state.Notes[0].Spent || state.Notes[1].Commitment != outputCommitment {
		t.Fatalf("privacy notes = %+v, want spent source and output note", state.Notes)
	}
	if state.Notes[1].Amount != amount || state.Notes[1].SpendAuthority != receiverKey {
		t.Fatalf("output note = %+v, want amount %d receiver", state.Notes[1], amount)
	}
	if len(state.SpentNullifiers) != 1 || state.SpentNullifiers[0] != nullifier {
		t.Fatalf("spent nullifiers = %+v, want one nullifier", state.SpentNullifiers)
	}
}

func simulatePrivacyVMSyscallConfirmed(t *testing.T, input TransactionSimulationInput) TransactionExecutionResult {
	t.Helper()

	result, err := simulateWithPrivacyVMSyscall(t, input)
	if err != nil {
		t.Fatalf("vm syscall Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("vm syscall status = %d, want confirmed: %v", result.Status, result.Error)
	}
	if result.Error != nil {
		t.Fatalf("vm syscall error = %v, want nil", result.Error)
	}
	return result
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
