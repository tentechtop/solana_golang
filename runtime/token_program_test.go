package runtime_test

import (
	"testing"

	tokenprogram "solana_golang/programs/token"
	runtimepkg "solana_golang/runtime"
	"solana_golang/structure"
)

func TestTokenProgramMintsAndTransfers(t *testing.T) {
	authorityKey, authorityPrivateKey := newSimulationSigner(t)
	recipientOwnerKey, recipientOwnerPrivateKey := newSimulationSigner(t)
	mintKey := newTestPublicKey(141)
	sourceTokenKey := newTestPublicKey(142)
	destinationTokenKey := newTestPublicKey(143)
	blockhash := newTestHash(144)
	tokenRent := mustMinimumBalance(t, tokenprogram.MaxTokenStateBytes)
	authorityLamports := mustMinimumBalance(t, 0) + LamportsPerSignature*4 + 100

	accounts := []AddressedAccount{
		newSimulationAccount(t, authorityKey, authorityLamports, DefaultBuiltinProgramIDs.System, false),
		newSimulationAccount(t, recipientOwnerKey, mustMinimumBalance(t, 0)+LamportsPerSignature+100, DefaultBuiltinProgramIDs.System, false),
		newSimulationAccount(t, mintKey, tokenRent, DefaultBuiltinProgramIDs.Token, false),
		newSimulationAccount(t, sourceTokenKey, tokenRent, DefaultBuiltinProgramIDs.Token, false),
		newSimulationAccount(t, destinationTokenKey, tokenRent, DefaultBuiltinProgramIDs.Token, false),
		newSimulationAccount(t, DefaultBuiltinProgramIDs.System, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		newSimulationAccount(t, DefaultBuiltinProgramIDs.Token, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
	}

	accounts = executeTokenInstruction(t, accounts, tokenprogram.NewInitializeMintInstruction(6), []AccountMeta{
		{PublicKey: authorityKey, IsSigner: true, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Token, IsSigner: false, IsWritable: false},
	}, []PublicKey{mintKey, authorityKey}, blockhash, map[PublicKey][]byte{authorityKey: authorityPrivateKey}, 11)

	accounts = executeTokenInstruction(t, accounts, tokenprogram.NewInitializeAccountInstruction(), []AccountMeta{
		{PublicKey: authorityKey, IsSigner: true, IsWritable: true},
		{PublicKey: sourceTokenKey, IsSigner: false, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: false},
		{PublicKey: DefaultBuiltinProgramIDs.Token, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceTokenKey, mintKey, authorityKey}, blockhash, map[PublicKey][]byte{authorityKey: authorityPrivateKey}, 11)

	accounts = executeTokenInstruction(t, accounts, tokenprogram.NewInitializeAccountInstruction(), []AccountMeta{
		{PublicKey: recipientOwnerKey, IsSigner: true, IsWritable: true},
		{PublicKey: destinationTokenKey, IsSigner: false, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: false},
		{PublicKey: DefaultBuiltinProgramIDs.Token, IsSigner: false, IsWritable: false},
	}, []PublicKey{destinationTokenKey, mintKey, recipientOwnerKey}, blockhash, map[PublicKey][]byte{recipientOwnerKey: recipientOwnerPrivateKey}, 11)

	mintToInstruction, err := tokenprogram.NewMintToInstruction(500)
	mintToInstruction = mustTokenInstruction(t, mintToInstruction, err)
	accounts = executeTokenInstruction(t, accounts, mintToInstruction, []AccountMeta{
		{PublicKey: authorityKey, IsSigner: true, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: true},
		{PublicKey: sourceTokenKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Token, IsSigner: false, IsWritable: false},
	}, []PublicKey{mintKey, sourceTokenKey, authorityKey}, blockhash, map[PublicKey][]byte{authorityKey: authorityPrivateKey}, 11)

	transferInstruction, err := tokenprogram.NewTransferInstruction(125)
	transferInstruction = mustTokenInstruction(t, transferInstruction, err)
	accounts = executeTokenInstruction(t, accounts, transferInstruction, []AccountMeta{
		{PublicKey: authorityKey, IsSigner: true, IsWritable: true},
		{PublicKey: sourceTokenKey, IsSigner: false, IsWritable: true},
		{PublicKey: destinationTokenKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Token, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceTokenKey, destinationTokenKey, authorityKey}, blockhash, map[PublicKey][]byte{authorityKey: authorityPrivateKey}, 11)

	sourceState := mustTokenAccountState(t, findAccount(t, accounts, sourceTokenKey).Data)
	destinationState := mustTokenAccountState(t, findAccount(t, accounts, destinationTokenKey).Data)
	mintState := mustMintState(t, findAccount(t, accounts, mintKey).Data)
	if sourceState.Amount != 375 || destinationState.Amount != 125 || mintState.Supply != 500 {
		t.Fatalf("token balances source=%d destination=%d supply=%d", sourceState.Amount, destinationState.Amount, mintState.Supply)
	}
}

func TestTokenProgramRejectsReadonlyWrite(t *testing.T) {
	authorityKey, authorityPrivateKey := newSimulationSigner(t)
	mintKey := newTestPublicKey(151)
	blockhash := newTestHash(152)
	instruction := tokenprogram.NewInitializeMintInstruction(2)
	transaction := signedTokenTransaction(t, []AccountMeta{
		{PublicKey: authorityKey, IsSigner: true, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: false},
		{PublicKey: DefaultBuiltinProgramIDs.Token, IsSigner: false, IsWritable: false},
	}, []PublicKey{mintKey, authorityKey}, instruction, blockhash, map[PublicKey][]byte{authorityKey: authorityPrivateKey})

	result, err := simulateWithDefaultPrograms(t, TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, authorityKey, mustMinimumBalance(t, 0)+LamportsPerSignature+100, DefaultBuiltinProgramIDs.System, false),
			newSimulationAccount(t, mintKey, mustMinimumBalance(t, tokenprogram.MaxTokenStateBytes), DefaultBuiltinProgramIDs.Token, false),
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Token, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 12),
		CurrentSlot:    12,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusFailed {
		t.Fatalf("status = %d, want failed", result.Status)
	}
}

func TestTransactionSimulatorRejectsProcessedTransactionID(t *testing.T) {
	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	destinationKey := newTestPublicKey(161)
	blockhash := newTestHash(162)
	transferInstruction, err := NewTransferInstruction(TransferParams{Lamports: 1})
	systemInstruction := mustSystemInstructionBytes(t, transferInstruction, err)
	transaction := signedSimulationTransaction(t, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, destinationKey}, systemInstruction, blockhash, map[PublicKey][]byte{sourceKey: sourcePrivateKey})
	transactionID, err := transaction.TxIDString()
	if err != nil {
		t.Fatalf("TxIDString() error = %v", err)
	}

	result, err := simulateWithDefaultPrograms(t, TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       simulationAccounts(t, sourceKey, mustMinimumBalance(t, 0)+LamportsPerSignature+100, destinationKey, mustMinimumBalance(t, 0)),
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 13),
		CurrentSlot:    13,
		ProcessedTxIDs: map[string]struct{}{transactionID: {}},
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusFailed || result.Error == nil || result.Error.Code != structure.TransactionErrorCodeAlreadyProcessed {
		t.Fatalf("result = %+v, want already processed", result)
	}
}

func TestAccountLockSetAllowsReadSharingAndRejectsWriteConflict(t *testing.T) {
	readOnlyKey := newTestPublicKey(171)
	writerOne := signedLockTransaction(t, newTestPublicKey(172), readOnlyKey, true)
	readerTwo := signedLockTransaction(t, newTestPublicKey(173), readOnlyKey, false)
	writerThree := signedLockTransaction(t, newTestPublicKey(174), readOnlyKey, true)
	locks := runtimepkg.NewAccountLockSet()
	locked, err := locks.TryLockTransaction(readerTwo)
	if err != nil || !locked {
		t.Fatalf("reader lock = %v, %v", locked, err)
	}
	locked, err = locks.TryLockTransaction(writerOne)
	if err != nil {
		t.Fatalf("writer lock error = %v", err)
	}
	if locked {
		t.Fatal("writer locked while reader holds account")
	}
	if err := locks.UnlockTransaction(readerTwo); err != nil {
		t.Fatalf("UnlockTransaction() error = %v", err)
	}
	locked, err = locks.TryLockTransaction(writerThree)
	if err != nil || !locked {
		t.Fatalf("writer lock after unlock = %v, %v", locked, err)
	}
}

func executeTokenInstruction(
	t *testing.T,
	accounts []AddressedAccount,
	instruction tokenprogram.Instruction,
	metas []AccountMeta,
	instructionAccounts []PublicKey,
	blockhash Blockhash,
	privateKeys map[PublicKey][]byte,
	slot uint64,
) []AddressedAccount {
	t.Helper()
	transaction := signedTokenTransaction(t, metas, instructionAccounts, instruction, blockhash, privateKeys)
	result, err := simulateWithDefaultPrograms(t, TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       accounts,
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, slot),
		CurrentSlot:    slot,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("status = %d, want confirmed: %v", result.Status, result.Error)
	}
	return mergeWrittenAccounts(accounts, result.WrittenAccounts)
}

func signedTokenTransaction(
	t *testing.T,
	accounts []AccountMeta,
	instructionAccounts []PublicKey,
	instruction tokenprogram.Instruction,
	blockhash Blockhash,
	privateKeys map[PublicKey][]byte,
) Transaction {
	t.Helper()
	instructionData, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	return signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Token, accounts, instructionAccounts, instructionData, blockhash, privateKeys)
}

func mustTokenInstruction(t *testing.T, instruction tokenprogram.Instruction, err error) tokenprogram.Instruction {
	t.Helper()
	if err != nil {
		t.Fatalf("token instruction error = %v", err)
	}
	return instruction
}

func mergeWrittenAccounts(accounts []AddressedAccount, writtenAccounts []AddressedAccount) []AddressedAccount {
	merged := make([]AddressedAccount, len(accounts))
	copy(merged, accounts)
	indexByAddress := make(map[PublicKey]int, len(merged))
	for index, account := range merged {
		indexByAddress[account.Address] = index
	}
	for _, account := range writtenAccounts {
		if index, exists := indexByAddress[account.Address]; exists {
			merged[index] = account
			continue
		}
		indexByAddress[account.Address] = len(merged)
		merged = append(merged, account)
	}
	return merged
}

func findAccount(t *testing.T, accounts []AddressedAccount, address PublicKey) Account {
	t.Helper()
	for _, account := range accounts {
		if account.Address == address {
			return account.Account
		}
	}
	t.Fatalf("account %s not found", address.String())
	return Account{}
}

func mustTokenAccountState(t *testing.T, data []byte) tokenprogram.AccountState {
	t.Helper()
	state, err := tokenprogram.UnmarshalAccountStateBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalAccountStateBinary() error = %v", err)
	}
	return state
}

func mustMintState(t *testing.T, data []byte) tokenprogram.MintState {
	t.Helper()
	state, err := tokenprogram.UnmarshalMintStateBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalMintStateBinary() error = %v", err)
	}
	return state
}

func signedLockTransaction(t *testing.T, signer PublicKey, shared PublicKey, sharedWritable bool) Transaction {
	t.Helper()
	return Transaction{
		Signatures: []structure.Signature{structure.Signature{1}},
		Accounts: []AccountMeta{
			{PublicKey: signer, IsSigner: true, IsWritable: true},
			{PublicKey: shared, IsSigner: false, IsWritable: sharedWritable},
			{PublicKey: DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
		},
		Instructions:    []CompiledInstruction{{ProgramIDIndex: 2, AccountIndexes: []uint8{0, 1}}},
		RecentBlockhash: newTestHash(175),
	}
}
