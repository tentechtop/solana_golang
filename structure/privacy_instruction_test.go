package structure

import "testing"

func TestTransactionSimulatorDepositsTransparentToPrivate(t *testing.T) {
	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	privacyStateKey := newTestPublicKey(81)
	auditorKey := newTestPublicKey(80)
	blockhash := newTestHash(82)
	amount := uint64(1000)
	sourceLamports := mustMinimumBalance(t, 0) + LamportsPerSignature + amount + 100
	stateLamports := mustMinimumBalance(t, 512)
	commitment := newTestHash(83)

	instruction, err := NewPrivacyDepositInstruction(1, nil, PrivacyDepositParams{
		Amount:         amount,
		Commitment:     commitment,
		SpendAuthority: sourceKey,
		EncryptedNote:  []byte("note-a"),
		AuditRecords: []PrivacyAuditRecord{
			newPrivacyAuditRecord(auditorKey, PrivacyAuditScopeRegulatory, 100, []byte("audit-a")),
		},
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: privacyStateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, privacyStateKey}, instructionData, blockhash, map[PublicKey][]byte{
		sourceKey: sourcePrivateKey,
	})

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       privacySimulationAccounts(t, sourceKey, sourceLamports, privacyStateKey, stateLamports),
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 30),
		CurrentSlot:    30,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("Status = %d, want confirmed: %v", result.Status, result.Error)
	}

	sourceWritten := findWrittenAccount(t, result.WrittenAccounts, sourceKey)
	stateWritten := findWrittenAccount(t, result.WrittenAccounts, privacyStateKey)
	if sourceWritten.Lamports != sourceLamports-LamportsPerSignature-amount {
		t.Fatalf("source lamports = %d, want %d", sourceWritten.Lamports, sourceLamports-LamportsPerSignature-amount)
	}
	if stateWritten.Lamports != stateLamports+amount {
		t.Fatalf("state lamports = %d, want %d", stateWritten.Lamports, stateLamports+amount)
	}

	state, err := UnmarshalPrivacyStateBinary(stateWritten.Data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyStateBinary() error = %v", err)
	}
	if len(state.Notes) != 1 || state.Notes[0].Commitment != commitment || state.Notes[0].Amount != amount {
		t.Fatalf("privacy state notes = %+v, want one deposited note", state.Notes)
	}
	if len(state.Notes[0].AuditRecords) != 1 || state.Notes[0].AuditRecords[0].Auditor != auditorKey {
		t.Fatalf("audit records = %+v, want regulator audit record", state.Notes[0].AuditRecords)
	}
}

func TestTransactionSimulatorSameAccountSendsTransparentAndPrivateTransactions(t *testing.T) {
	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	destinationKey := newTestPublicKey(85)
	privacyStateKey := newTestPublicKey(86)
	firstBlockhash := newTestHash(87)
	secondBlockhash := newTestHash(88)
	transparentAmount := uint64(200)
	privateAmount := uint64(300)
	sourceLamports := mustMinimumBalance(t, 0) + LamportsPerSignature*2 + transparentAmount + privateAmount + 100
	destinationLamports := mustMinimumBalance(t, 0)
	stateLamports := mustMinimumBalance(t, 512)

	transferInstruction, err := NewTransferInstruction(TransferParams{Lamports: transparentAmount})
	transferData := mustSystemInstructionBytes(t, transferInstruction, err)
	transparentTransaction := signedSimulationTransaction(t, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, destinationKey}, transferData, firstBlockhash, map[PublicKey][]byte{
		sourceKey: sourcePrivateKey,
	})
	transparentResult, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction:    transparentTransaction,
		Accounts:       simulationAccounts(t, sourceKey, sourceLamports, destinationKey, destinationLamports),
		BlockhashQueue: newSimulationBlockhashQueue(t, firstBlockhash, 35),
		CurrentSlot:    35,
	})
	if err != nil {
		t.Fatalf("transparent Simulate() error = %v", err)
	}
	if transparentResult.Status != TransactionStatusConfirmed {
		t.Fatalf("transparent status = %d, want confirmed: %v", transparentResult.Status, transparentResult.Error)
	}

	sourceAfterTransparent := findWrittenAccount(t, transparentResult.WrittenAccounts, sourceKey).Lamports
	privateInstruction, err := NewPrivacyDepositInstruction(1, nil, PrivacyDepositParams{
		Amount:         privateAmount,
		Commitment:     newTestHash(89),
		SpendAuthority: sourceKey,
		EncryptedNote:  []byte("note-same-account"),
	})
	privateData := mustPrivacyInstructionBytes(t, privateInstruction, err)
	privateTransaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: privacyStateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, privacyStateKey}, privateData, secondBlockhash, map[PublicKey][]byte{
		sourceKey: sourcePrivateKey,
	})
	privateResult, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction:    privateTransaction,
		Accounts:       privacySimulationAccounts(t, sourceKey, sourceAfterTransparent, privacyStateKey, stateLamports),
		BlockhashQueue: newSimulationBlockhashQueue(t, secondBlockhash, 36),
		CurrentSlot:    36,
	})
	if err != nil {
		t.Fatalf("private Simulate() error = %v", err)
	}
	if privateResult.Status != TransactionStatusConfirmed {
		t.Fatalf("private status = %d, want confirmed: %v", privateResult.Status, privateResult.Error)
	}
	sourceAfterPrivate := findWrittenAccount(t, privateResult.WrittenAccounts, sourceKey).Lamports
	if sourceAfterPrivate != sourceAfterTransparent-LamportsPerSignature-privateAmount {
		t.Fatalf("source final lamports = %d, want %d", sourceAfterPrivate, sourceAfterTransparent-LamportsPerSignature-privateAmount)
	}
}

func TestTransactionSimulatorWithdrawsPrivateToTransparent(t *testing.T) {
	destinationKey, destinationPrivateKey := newSimulationSigner(t)
	privacyStateKey := newTestPublicKey(91)
	blockhash := newTestHash(92)
	amount := uint64(700)
	commitment := newTestHash(93)
	nullifier := newTestHash(94)
	stateAccount := privacyStateAccountWithNote(t, privacyStateKey, destinationKey, commitment, amount)
	destinationLamports := mustMinimumBalance(t, 0) + LamportsPerSignature + 100

	instruction, err := NewPrivacyWithdrawInstruction(1, nil, PrivacyWithdrawParams{
		Amount:           amount,
		SourceCommitment: commitment,
		Nullifier:        nullifier,
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: destinationKey, IsSigner: true, IsWritable: true},
		{PublicKey: privacyStateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{privacyStateKey, destinationKey}, instructionData, blockhash, map[PublicKey][]byte{
		destinationKey: destinationPrivateKey,
	})

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, destinationKey, destinationLamports, DefaultBuiltinProgramIDs.System, false),
			stateAccount,
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 40),
		CurrentSlot:    40,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("Status = %d, want confirmed: %v", result.Status, result.Error)
	}

	destinationWritten := findWrittenAccount(t, result.WrittenAccounts, destinationKey)
	stateWritten := findWrittenAccount(t, result.WrittenAccounts, privacyStateKey)
	if destinationWritten.Lamports != destinationLamports-LamportsPerSignature+amount {
		t.Fatalf("destination lamports = %d, want %d", destinationWritten.Lamports, destinationLamports-LamportsPerSignature+amount)
	}
	state, err := UnmarshalPrivacyStateBinary(stateWritten.Data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyStateBinary() error = %v", err)
	}
	if len(state.SpentNullifiers) != 1 || state.SpentNullifiers[0] != nullifier || !state.Notes[0].Spent {
		t.Fatalf("privacy state = %+v, want spent note and nullifier", state)
	}
}

func TestTransactionSimulatorTransfersPrivateToPrivate(t *testing.T) {
	authorityKey, authorityPrivateKey := newSimulationSigner(t)
	nextAuthorityKey := newTestPublicKey(96)
	auditorKey := newTestPublicKey(95)
	privacyStateKey := newTestPublicKey(97)
	blockhash := newTestHash(98)
	amount := uint64(600)
	sourceCommitment := newTestHash(99)
	nullifier := newTestHash(100)
	outputCommitment := newTestHash(101)
	stateAccount := privacyStateAccountWithNote(t, privacyStateKey, authorityKey, sourceCommitment, amount)

	instruction, err := NewPrivacyTransferInstruction(1, nil, PrivacyTransferParams{
		Amount:               amount,
		SourceCommitment:     sourceCommitment,
		Nullifier:            nullifier,
		OutputCommitment:     outputCommitment,
		OutputSpendAuthority: nextAuthorityKey,
		OutputEncryptedNote:  []byte("note-b"),
		OutputAuditRecords: []PrivacyAuditRecord{
			newPrivacyAuditRecord(auditorKey, PrivacyAuditScopeBusiness, 0, []byte("audit-b")),
		},
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: authorityKey, IsSigner: true, IsWritable: true},
		{PublicKey: privacyStateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{privacyStateKey}, instructionData, blockhash, map[PublicKey][]byte{
		authorityKey: authorityPrivateKey,
	})

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, authorityKey, mustMinimumBalance(t, 0)+LamportsPerSignature+100, DefaultBuiltinProgramIDs.System, false),
			stateAccount,
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 45),
		CurrentSlot:    45,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("Status = %d, want confirmed: %v", result.Status, result.Error)
	}

	stateWritten := findWrittenAccount(t, result.WrittenAccounts, privacyStateKey)
	state, err := UnmarshalPrivacyStateBinary(stateWritten.Data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyStateBinary() error = %v", err)
	}
	if len(state.Notes) != 2 || !state.Notes[0].Spent || state.Notes[1].Commitment != outputCommitment {
		t.Fatalf("privacy notes = %+v, want spent source and new output", state.Notes)
	}
	if state.Notes[1].SpendAuthority != nextAuthorityKey {
		t.Fatal("output spend authority mismatch")
	}
	if len(state.Notes[1].AuditRecords) != 1 || state.Notes[1].AuditRecords[0].Scope != PrivacyAuditScopeBusiness {
		t.Fatalf("output audit records = %+v, want business audit record", state.Notes[1].AuditRecords)
	}
	if len(state.SpentNullifiers) != 1 || state.SpentNullifiers[0] != nullifier {
		t.Fatalf("spent nullifiers = %+v, want one nullifier", state.SpentNullifiers)
	}
}

func TestTransactionSimulatorAuthorizesPrivacyAudit(t *testing.T) {
	authorityKey, authorityPrivateKey := newSimulationSigner(t)
	privacyStateKey := newTestPublicKey(105)
	auditorKey := newTestPublicKey(106)
	blockhash := newTestHash(107)
	amount := uint64(400)
	commitment := newTestHash(108)
	stateAccount := privacyStateAccountWithNote(t, privacyStateKey, authorityKey, commitment, amount)

	instruction, err := NewPrivacyAuthorizeAuditInstruction(1, nil, PrivacyAuthorizeAuditParams{
		Commitment:      commitment,
		Auditor:         auditorKey,
		Scope:           PrivacyAuditScopeRegulatory,
		ExpiresAtSlot:   120,
		AuditCiphertext: []byte("audit-c"),
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: authorityKey, IsSigner: true, IsWritable: true},
		{PublicKey: privacyStateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{privacyStateKey}, instructionData, blockhash, map[PublicKey][]byte{
		authorityKey: authorityPrivateKey,
	})

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, authorityKey, mustMinimumBalance(t, 0)+LamportsPerSignature+100, DefaultBuiltinProgramIDs.System, false),
			stateAccount,
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 60),
		CurrentSlot:    60,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("Status = %d, want confirmed: %v", result.Status, result.Error)
	}

	stateWritten := findWrittenAccount(t, result.WrittenAccounts, privacyStateKey)
	state, err := UnmarshalPrivacyStateBinary(stateWritten.Data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyStateBinary() error = %v", err)
	}
	if len(state.Notes[0].AuditRecords) != 1 || state.Notes[0].AuditRecords[0].Auditor != auditorKey {
		t.Fatalf("audit records = %+v, want authorized auditor", state.Notes[0].AuditRecords)
	}
}

func TestTransactionSimulatorRejectsExpiredPrivacyAuditAuthorization(t *testing.T) {
	authorityKey, authorityPrivateKey := newSimulationSigner(t)
	privacyStateKey := newTestPublicKey(109)
	auditorKey := newTestPublicKey(110)
	blockhash := newTestHash(111)
	commitment := newTestHash(112)
	stateAccount := privacyStateAccountWithNote(t, privacyStateKey, authorityKey, commitment, 400)

	instruction, err := NewPrivacyAuthorizeAuditInstruction(1, nil, PrivacyAuthorizeAuditParams{
		Commitment:      commitment,
		Auditor:         auditorKey,
		Scope:           PrivacyAuditScopeRegulatory,
		ExpiresAtSlot:   60,
		AuditCiphertext: []byte("expired-audit"),
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: authorityKey, IsSigner: true, IsWritable: true},
		{PublicKey: privacyStateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{privacyStateKey}, instructionData, blockhash, map[PublicKey][]byte{
		authorityKey: authorityPrivateKey,
	})

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, authorityKey, mustMinimumBalance(t, 0)+LamportsPerSignature+100, DefaultBuiltinProgramIDs.System, false),
			stateAccount,
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 60),
		CurrentSlot:    60,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusFailed {
		t.Fatalf("Status = %d, want failed", result.Status)
	}
}

func TestTransactionSimulatorRejectsDuplicatePrivacyNullifier(t *testing.T) {
	destinationKey, destinationPrivateKey := newSimulationSigner(t)
	privacyStateKey := newTestPublicKey(101)
	blockhash := newTestHash(102)
	amount := uint64(500)
	commitment := newTestHash(103)
	nullifier := newTestHash(104)
	stateAccount := privacyStateAccountWithNote(t, privacyStateKey, destinationKey, commitment, amount)
	state := mustPrivacyStateFromAccount(t, stateAccount)
	state.Notes[0].Spent = true
	state.Notes[0].SpentSlot = 49
	state.Notes[0].SpendNullifier = nullifier
	state.SpentNullifiers = append(state.SpentNullifiers, nullifier)
	stateAccount.Account = mustPrivacyAccountFromState(t, stateAccount.Account.Lamports, state)

	instruction, err := NewPrivacyWithdrawInstruction(1, nil, PrivacyWithdrawParams{
		Amount:           amount,
		SourceCommitment: commitment,
		Nullifier:        nullifier,
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: destinationKey, IsSigner: true, IsWritable: true},
		{PublicKey: privacyStateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{privacyStateKey, destinationKey}, instructionData, blockhash, map[PublicKey][]byte{
		destinationKey: destinationPrivateKey,
	})

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, destinationKey, mustMinimumBalance(t, 0)+LamportsPerSignature+100, DefaultBuiltinProgramIDs.System, false),
			stateAccount,
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 50),
		CurrentSlot:    50,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusFailed {
		t.Fatalf("Status = %d, want failed", result.Status)
	}
}

func TestTransactionSimulatorRejectsPrivacySpendWithoutAuthority(t *testing.T) {
	authorityKey, _ := newSimulationSigner(t)
	attackerKey, attackerPrivateKey := newSimulationSigner(t)
	privacyStateKey := newTestPublicKey(111)
	blockhash := newTestHash(112)
	amount := uint64(500)
	commitment := newTestHash(113)
	nullifier := newTestHash(114)
	stateAccount := privacyStateAccountWithNote(t, privacyStateKey, authorityKey, commitment, amount)

	instruction, err := NewPrivacyWithdrawInstruction(1, nil, PrivacyWithdrawParams{
		Amount:           amount,
		SourceCommitment: commitment,
		Nullifier:        nullifier,
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: attackerKey, IsSigner: true, IsWritable: true},
		{PublicKey: privacyStateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{privacyStateKey, attackerKey}, instructionData, blockhash, map[PublicKey][]byte{
		attackerKey: attackerPrivateKey,
	})

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, attackerKey, mustMinimumBalance(t, 0)+LamportsPerSignature+100, DefaultBuiltinProgramIDs.System, false),
			stateAccount,
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 70),
		CurrentSlot:    70,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusFailed {
		t.Fatalf("Status = %d, want failed", result.Status)
	}
}

func TestTransactionSimulatorRejectsInvalidPrivacySpendProof(t *testing.T) {
	feePayerKey, feePayerPrivateKey := newSimulationSigner(t)
	spendKeyPair := mustPrivacySpendKeyPair(t)
	spendAuthority := mustSchnorrSpendAuthority(t, spendKeyPair.PublicKey)
	destinationKey := newTestPublicKey(115)
	privacyStateKey := newTestPublicKey(116)
	blockhash := newTestHash(117)
	amount := uint64(500)
	commitment := newTestHash(118)
	nullifier := newTestHash(119)
	stateAccount := privacyStateAccountWithNote(t, privacyStateKey, spendAuthority, commitment, amount)
	params := PrivacyWithdrawParams{
		Amount:           amount,
		SourceCommitment: commitment,
		Nullifier:        nullifier,
	}
	wrongProofMessage, err := BuildPrivacyWithdrawProofMessage(1, privacyStateKey, destinationKey, params, 72)
	if err != nil {
		t.Fatalf("BuildPrivacyWithdrawProofMessage() error = %v", err)
	}
	proofBytes := mustSchnorrProofBytes(t, spendKeyPair.PrivateScalar, wrongProofMessage)
	instruction, err := NewPrivacyWithdrawInstruction(1, proofBytes, params)
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: feePayerKey, IsSigner: true, IsWritable: true},
		{PublicKey: privacyStateKey, IsSigner: false, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{privacyStateKey, destinationKey}, instructionData, blockhash, map[PublicKey][]byte{
		feePayerKey: feePayerPrivateKey,
	})

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, feePayerKey, mustMinimumBalance(t, 0)+LamportsPerSignature+100, DefaultBuiltinProgramIDs.System, false),
			stateAccount,
			newSimulationAccount(t, destinationKey, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.System, false),
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 71),
		CurrentSlot:    71,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusFailed {
		t.Fatalf("Status = %d, want failed", result.Status)
	}
}

func signedSimulationProgramTransaction(t *testing.T, programID PublicKey, accounts []AccountMeta, instructionAccounts []PublicKey, instructionData []byte, blockhash Blockhash, privateKeys map[PublicKey][]byte) Transaction {
	t.Helper()

	accountIndexByKey, err := AccountIndexMap(accounts)
	if err != nil {
		t.Fatalf("AccountIndexMap() error = %v", err)
	}
	compiledInstruction, err := CompileInstruction(programID, instructionAccounts, instructionData, accountIndexByKey)
	if err != nil {
		t.Fatalf("CompileInstruction() error = %v", err)
	}
	transaction := Transaction{
		Accounts:        accounts,
		Instructions:    []CompiledInstruction{compiledInstruction},
		RecentBlockhash: blockhash,
	}
	signedTransaction, err := transaction.Sign(privateKeys)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return signedTransaction
}

func privacySimulationAccounts(t *testing.T, sourceKey PublicKey, sourceLamports uint64, privacyStateKey PublicKey, stateLamports uint64) []AddressedAccount {
	t.Helper()

	return []AddressedAccount{
		newSimulationAccount(t, sourceKey, sourceLamports, DefaultBuiltinProgramIDs.System, false),
		newSimulationAccount(t, privacyStateKey, stateLamports, DefaultBuiltinProgramIDs.Privacy, false),
		newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
	}
}

func privacyStateAccountWithNote(t *testing.T, address PublicKey, spendAuthority PublicKey, commitment Hash, amount uint64) AddressedAccount {
	t.Helper()

	state := PrivacyState{
		Version: PrivacyStateVersion,
		Notes: []PrivacyNoteRecord{
			{Commitment: commitment, SpendAuthority: spendAuthority, Amount: amount, VMVersion: 1, EncryptedNote: []byte("note-a")},
		},
	}
	return AddressedAccount{Address: address, Account: mustPrivacyAccountFromState(t, mustMinimumBalance(t, 512)+amount, state)}
}

func mustPrivacyAccountFromState(t *testing.T, lamports uint64, state PrivacyState) Account {
	t.Helper()

	encoded, err := state.MarshalBinary()
	if err != nil {
		t.Fatalf("PrivacyState.MarshalBinary() error = %v", err)
	}
	account, err := NewAccount(lamports, encoded, DefaultBuiltinProgramIDs.Privacy, false, 0)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	return account
}

func mustPrivacyInstructionBytes(t *testing.T, instruction PrivacyInstruction, err error) []byte {
	t.Helper()

	if err != nil {
		t.Fatalf("build privacy instruction error = %v", err)
	}
	encoded, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	return encoded
}

func mustPrivacyStateFromAccount(t *testing.T, account AddressedAccount) PrivacyState {
	t.Helper()

	state, err := UnmarshalPrivacyStateBinary(account.Account.Data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyStateBinary() error = %v", err)
	}
	return state
}

func newPrivacyAuditRecord(auditor PublicKey, scope PrivacyAuditScope, expiresAtSlot uint64, ciphertext []byte) PrivacyAuditRecord {
	return PrivacyAuditRecord{
		Auditor:         auditor,
		Scope:           scope,
		ExpiresAtSlot:   expiresAtSlot,
		AuditCiphertext: ciphertext,
	}
}
