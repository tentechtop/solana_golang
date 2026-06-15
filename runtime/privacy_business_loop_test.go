package runtime_test

import (
	"testing"

	"solana_golang/zk"
)

func TestAccountTransparentAndPrivateAuditBusinessLoop(t *testing.T) {
	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	spendKeyPair := mustPrivacySpendKeyPair(t)
	spendAuthority := mustSchnorrSpendAuthority(t, spendKeyPair.PublicKey)
	destinationKey := newTestPublicKey(121)
	privacyStateKey := newTestPublicKey(122)
	auditorKey := newTestPublicKey(123)
	auditKey := newTestAuditKey(124)
	transparentAmount := uint64(100)
	privateAmount := uint64(700)
	sourceLamports := mustMinimumBalance(t, 0) + LamportsPerSignature*4 + transparentAmount + privateAmount + 1000
	destinationLamports := mustMinimumBalance(t, 0)
	privacyStateLamports := mustMinimumBalance(t, 4096)

	sourceLamports, destinationLamports = runBusinessLoopTransparentTransfer(t, sourceKey, sourcePrivateKey, destinationKey, sourceLamports, destinationLamports, transparentAmount)

	depositCommitment := newTestHash(125)
	depositResult := runBusinessLoopDeposit(t, sourceKey, sourcePrivateKey, privacyStateKey, auditorKey, auditKey, sourceLamports, privacyStateLamports, privateAmount, depositCommitment, spendAuthority)
	sourceLamports = findWrittenAccount(t, depositResult.WrittenAccounts, sourceKey).Lamports
	privacyStateLamports = findWrittenAccount(t, depositResult.WrittenAccounts, privacyStateKey).Lamports
	privacyState := mustPrivacyStateFromWrittenAccount(t, depositResult.WrittenAccounts, privacyStateKey)
	assertAuditPayloads(t, privacyState.Notes[0], auditorKey, auditKey, 202, []PrivacyAuditPayload{
		{Version: PrivacyAuditPayloadVersion, TransactionType: PrivacyInstructionDeposit, Commitment: depositCommitment, Amount: privateAmount, Slot: 202},
	})

	transferNullifier := newTestHash(126)
	outputCommitment := newTestHash(127)
	transferResult := runBusinessLoopPrivateTransfer(t, sourceKey, sourcePrivateKey, spendKeyPair.PrivateScalar, spendAuthority, privacyStateKey, auditorKey, auditKey, sourceLamports, privacyStateLamports, privacyState, privateAmount, depositCommitment, transferNullifier, outputCommitment)
	sourceLamports = findWrittenAccount(t, transferResult.WrittenAccounts, sourceKey).Lamports
	privacyStateLamports = findWrittenAccount(t, transferResult.WrittenAccounts, privacyStateKey).Lamports
	privacyState = mustPrivacyStateFromWrittenAccount(t, transferResult.WrittenAccounts, privacyStateKey)
	if !privacyState.Notes[0].Spent || privacyState.Notes[0].SpentSlot != 203 || privacyState.Notes[0].SpendNullifier != transferNullifier {
		t.Fatalf("source note spend metadata = %+v, want transfer spend", privacyState.Notes[0])
	}
	assertAuditPayloads(t, privacyState.Notes[1], auditorKey, auditKey, 203, []PrivacyAuditPayload{
		{Version: PrivacyAuditPayloadVersion, TransactionType: PrivacyInstructionTransfer, Commitment: depositCommitment, Nullifier: transferNullifier, OutputCommitment: outputCommitment, Amount: privateAmount, Slot: 203},
	})

	withdrawNullifier := newTestHash(128)
	withdrawResult := runBusinessLoopWithdraw(t, sourceKey, sourcePrivateKey, spendKeyPair.PrivateScalar, destinationKey, privacyStateKey, auditorKey, auditKey, sourceLamports, destinationLamports, privacyStateLamports, privacyState, privateAmount, outputCommitment, withdrawNullifier)
	privacyState = mustPrivacyStateFromWrittenAccount(t, withdrawResult.WrittenAccounts, privacyStateKey)
	destinationLamports = findWrittenAccount(t, withdrawResult.WrittenAccounts, destinationKey).Lamports
	if !privacyState.Notes[1].Spent || privacyState.Notes[1].SpentSlot != 204 || privacyState.Notes[1].SpendNullifier != withdrawNullifier {
		t.Fatalf("output note spend metadata = %+v, want withdraw spend", privacyState.Notes[1])
	}
	if destinationLamports != mustMinimumBalance(t, 0)+transparentAmount+privateAmount {
		t.Fatalf("destination lamports = %d, want transparent + private receipts", destinationLamports)
	}
	assertAuditPayloads(t, privacyState.Notes[1], auditorKey, auditKey, 204, []PrivacyAuditPayload{
		{Version: PrivacyAuditPayloadVersion, TransactionType: PrivacyInstructionTransfer, Commitment: depositCommitment, Nullifier: transferNullifier, OutputCommitment: outputCommitment, Amount: privateAmount, Slot: 203},
		{Version: PrivacyAuditPayloadVersion, TransactionType: PrivacyInstructionWithdraw, Commitment: outputCommitment, Nullifier: withdrawNullifier, Amount: privateAmount, Slot: 204},
	})
}

func runBusinessLoopTransparentTransfer(t *testing.T, sourceKey PublicKey, sourcePrivateKey []byte, destinationKey PublicKey, sourceLamports uint64, destinationLamports uint64, amount uint64) (uint64, uint64) {
	t.Helper()

	instruction, err := NewTransferInstruction(TransferParams{Lamports: amount})
	instructionData := mustSystemInstructionBytes(t, instruction, err)
	blockhash := newTestHash(130)
	transaction := signedSimulationTransaction(t, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, destinationKey}, instructionData, blockhash, map[PublicKey][]byte{sourceKey: sourcePrivateKey})

	result, err := simulateWithDefaultPrograms(t, TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       simulationAccounts(t, sourceKey, sourceLamports, destinationKey, destinationLamports),
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 201),
		CurrentSlot:    201,
	})
	assertConfirmedSimulation(t, result, err)
	return findWrittenAccount(t, result.WrittenAccounts, sourceKey).Lamports, findWrittenAccount(t, result.WrittenAccounts, destinationKey).Lamports
}

func runBusinessLoopDeposit(t *testing.T, sourceKey PublicKey, sourcePrivateKey []byte, privacyStateKey PublicKey, auditorKey PublicKey, auditKey []byte, sourceLamports uint64, stateLamports uint64, amount uint64, commitment Hash, spendAuthority PublicKey) TransactionExecutionResult {
	t.Helper()

	auditRecord := mustEncryptedPrivacyAuditRecord(t, auditorKey, PrivacyAuditScopeRegulatory, 1000, auditKey, PrivacyAuditPayload{
		Version:         PrivacyAuditPayloadVersion,
		TransactionType: PrivacyInstructionDeposit,
		Commitment:      commitment,
		Amount:          amount,
		Slot:            202,
	})
	instruction, err := NewPrivacyDepositInstruction(1, nil, PrivacyDepositParams{
		Amount:         amount,
		Commitment:     commitment,
		SpendAuthority: spendAuthority,
		EncryptedNote:  []byte("deposit-note"),
		AuditRecords:   []PrivacyAuditRecord{auditRecord},
	})
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	blockhash := newTestHash(131)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: privacyStateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, privacyStateKey}, instructionData, blockhash, map[PublicKey][]byte{sourceKey: sourcePrivateKey})

	result, err := simulateWithDefaultPrograms(t, TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       privacySimulationAccounts(t, sourceKey, sourceLamports, privacyStateKey, stateLamports),
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 202),
		CurrentSlot:    202,
	})
	assertConfirmedSimulation(t, result, err)
	return result
}

func runBusinessLoopPrivateTransfer(t *testing.T, sourceKey PublicKey, sourcePrivateKey []byte, spendPrivateScalar []byte, spendAuthority PublicKey, privacyStateKey PublicKey, auditorKey PublicKey, auditKey []byte, sourceLamports uint64, stateLamports uint64, state PrivacyState, amount uint64, sourceCommitment Hash, nullifier Hash, outputCommitment Hash) TransactionExecutionResult {
	t.Helper()

	auditRecord := mustEncryptedPrivacyAuditRecord(t, auditorKey, PrivacyAuditScopeRegulatory, 1000, auditKey, PrivacyAuditPayload{
		Version:          PrivacyAuditPayloadVersion,
		TransactionType:  PrivacyInstructionTransfer,
		Commitment:       sourceCommitment,
		Nullifier:        nullifier,
		OutputCommitment: outputCommitment,
		Amount:           amount,
		Slot:             203,
	})
	params := PrivacyTransferParams{
		Amount:               amount,
		SourceCommitment:     sourceCommitment,
		Nullifier:            nullifier,
		OutputCommitment:     outputCommitment,
		OutputSpendAuthority: spendAuthority,
		OutputEncryptedNote:  []byte("transfer-note"),
		OutputAuditRecords:   []PrivacyAuditRecord{auditRecord},
	}
	proofMessage, err := BuildPrivacyTransferProofMessage(1, privacyStateKey, params, 203)
	if err != nil {
		t.Fatalf("BuildPrivacyTransferProofMessage() error = %v", err)
	}
	proofBytes := mustSchnorrProofBytes(t, spendPrivateScalar, proofMessage)
	instruction, err := NewPrivacyTransferInstruction(1, proofBytes, params)
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	blockhash := newTestHash(132)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: privacyStateKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{privacyStateKey}, instructionData, blockhash, map[PublicKey][]byte{sourceKey: sourcePrivateKey})

	result, err := simulateWithDefaultPrograms(t, TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, sourceKey, sourceLamports, DefaultBuiltinProgramIDs.System, false),
			{Address: privacyStateKey, Account: mustPrivacyAccountFromState(t, stateLamports, state)},
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 203),
		CurrentSlot:    203,
	})
	assertConfirmedSimulation(t, result, err)
	return result
}

func runBusinessLoopWithdraw(t *testing.T, sourceKey PublicKey, sourcePrivateKey []byte, spendPrivateScalar []byte, destinationKey PublicKey, privacyStateKey PublicKey, auditorKey PublicKey, auditKey []byte, sourceLamports uint64, destinationLamports uint64, stateLamports uint64, state PrivacyState, amount uint64, sourceCommitment Hash, nullifier Hash) TransactionExecutionResult {
	t.Helper()

	auditRecord := mustEncryptedPrivacyAuditRecord(t, auditorKey, PrivacyAuditScopeRegulatory, 1000, auditKey, PrivacyAuditPayload{
		Version:         PrivacyAuditPayloadVersion,
		TransactionType: PrivacyInstructionWithdraw,
		Commitment:      sourceCommitment,
		Nullifier:       nullifier,
		Amount:          amount,
		Slot:            204,
	})
	params := PrivacyWithdrawParams{
		Amount:           amount,
		SourceCommitment: sourceCommitment,
		Nullifier:        nullifier,
		AuditRecords:     []PrivacyAuditRecord{auditRecord},
	}
	proofMessage, err := BuildPrivacyWithdrawProofMessage(1, privacyStateKey, destinationKey, params, 204)
	if err != nil {
		t.Fatalf("BuildPrivacyWithdrawProofMessage() error = %v", err)
	}
	proofBytes := mustSchnorrProofBytes(t, spendPrivateScalar, proofMessage)
	instruction, err := NewPrivacyWithdrawInstruction(1, proofBytes, params)
	instructionData := mustPrivacyInstructionBytes(t, instruction, err)
	blockhash := newTestHash(133)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.Privacy, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: privacyStateKey, IsSigner: false, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}, []PublicKey{privacyStateKey, destinationKey}, instructionData, blockhash, map[PublicKey][]byte{sourceKey: sourcePrivateKey})

	result, err := simulateWithDefaultPrograms(t, TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, sourceKey, sourceLamports, DefaultBuiltinProgramIDs.System, false),
			{Address: privacyStateKey, Account: mustPrivacyAccountFromState(t, stateLamports, state)},
			newSimulationAccount(t, destinationKey, destinationLamports, DefaultBuiltinProgramIDs.System, false),
			newSimulationAccount(t, DefaultBuiltinProgramIDs.Privacy, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 204),
		CurrentSlot:    204,
	})
	assertConfirmedSimulation(t, result, err)
	return result
}

func assertAuditPayloads(t *testing.T, note PrivacyNoteRecord, auditor PublicKey, auditKey []byte, currentSlot uint64, want []PrivacyAuditPayload) {
	t.Helper()

	got, err := AuditPrivacyNote(note, auditor, PrivacyAuditScopeRegulatory, currentSlot, auditKey)
	if err != nil {
		t.Fatalf("AuditPrivacyNote() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("audit payload count = %d, want %d: %+v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("audit payload %d = %+v, want %+v", index, got[index], want[index])
		}
	}
}

func assertConfirmedSimulation(t *testing.T, result TransactionExecutionResult, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("Status = %d, want confirmed: %v", result.Status, result.Error)
	}
}

func mustEncryptedPrivacyAuditRecord(t *testing.T, auditor PublicKey, scope PrivacyAuditScope, expiresAtSlot uint64, auditKey []byte, payload PrivacyAuditPayload) PrivacyAuditRecord {
	t.Helper()

	record, err := NewEncryptedPrivacyAuditRecord(auditor, scope, expiresAtSlot, auditKey, payload)
	if err != nil {
		t.Fatalf("NewEncryptedPrivacyAuditRecord() error = %v", err)
	}
	return record
}

func mustPrivacyStateFromWrittenAccount(t *testing.T, accounts []AddressedAccount, address PublicKey) PrivacyState {
	t.Helper()

	account := findWrittenAccount(t, accounts, address)
	state, err := UnmarshalPrivacyStateBinary(account.Data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyStateBinary() error = %v", err)
	}
	return state
}

func newTestAuditKey(seed byte) []byte {
	key := make([]byte, PrivacyAuditKeySize)
	for index := range key {
		key[index] = seed + byte(index)
	}
	return key
}

func mustPrivacySpendKeyPair(t *testing.T) zk.SchnorrKeyPair {
	t.Helper()

	keyPair, err := zk.GenerateSchnorrKeyPair()
	if err != nil {
		t.Fatalf("GenerateSchnorrKeyPair() error = %v", err)
	}
	return keyPair
}

func mustSchnorrSpendAuthority(t *testing.T, publicKey []byte) PublicKey {
	t.Helper()

	digest, err := zk.SchnorrPublicKeyDigest(publicKey)
	if err != nil {
		t.Fatalf("SchnorrPublicKeyDigest() error = %v", err)
	}
	spendAuthority, err := NewPublicKey(digest[:])
	if err != nil {
		t.Fatalf("NewPublicKey(spend authority) error = %v", err)
	}
	return spendAuthority
}

func mustSchnorrProofBytes(t *testing.T, privateScalar []byte, message []byte) []byte {
	t.Helper()

	proofBytes, err := zk.NewSchnorrProofBytes(privateScalar, message)
	if err != nil {
		t.Fatalf("NewSchnorrProofBytes() error = %v", err)
	}
	return proofBytes
}
