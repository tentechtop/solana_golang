package structure

import "testing"

type testAuditablePrivacyAccount struct {
	transparentAddress      PublicKey
	transparentPrivateKey   []byte
	spendPrivateScalar      []byte
	spendAuthority          PublicKey
	privacyStateAddress     PublicKey
	transparentLamports     uint64
	privacyStateLamports    uint64
	privacyState            PrivacyState
	regulatoryAuditor       PublicKey
	regulatoryAuditKey      []byte
	lastTransparentSlot     uint64
	lastPrivacyDepositSlot  uint64
	lastPrivacyTransferSlot uint64
	lastPrivacyWithdrawSlot uint64
}

func TestAuditablePrivacyAccountSendsFourTransactionTypes(t *testing.T) {
	account := newTestAuditablePrivacyAccount(t)
	recipientAddress := newTestPublicKey(142)
	recipientLamports := mustMinimumBalance(t, 0)
	transparentAmount := uint64(111)
	privateAmount := uint64(777)

	recipientLamports = account.SendTransparentToTransparent(t, recipientAddress, recipientLamports, transparentAmount)
	if recipientLamports != mustMinimumBalance(t, 0)+transparentAmount {
		t.Fatalf("transparent recipient lamports = %d, want %d", recipientLamports, mustMinimumBalance(t, 0)+transparentAmount)
	}

	depositCommitment := newTestHash(143)
	depositNote := account.SendTransparentToPrivate(t, privateAmount, depositCommitment)
	assertAuditPayloads(t, depositNote, account.regulatoryAuditor, account.regulatoryAuditKey, account.lastPrivacyDepositSlot, []PrivacyAuditPayload{
		{Version: PrivacyAuditPayloadVersion, TransactionType: PrivacyInstructionDeposit, Commitment: depositCommitment, Amount: privateAmount, Slot: account.lastPrivacyDepositSlot},
	})

	transferNullifier := newTestHash(144)
	outputCommitment := newTestHash(145)
	outputNote := account.SendPrivateToPrivate(t, privateAmount, depositCommitment, transferNullifier, outputCommitment)
	if !account.privacyState.Notes[0].Spent || account.privacyState.Notes[0].SpendNullifier != transferNullifier {
		t.Fatalf("deposit note spend metadata = %+v, want spent by private transfer", account.privacyState.Notes[0])
	}
	assertAuditPayloads(t, outputNote, account.regulatoryAuditor, account.regulatoryAuditKey, account.lastPrivacyTransferSlot, []PrivacyAuditPayload{
		{Version: PrivacyAuditPayloadVersion, TransactionType: PrivacyInstructionTransfer, Commitment: depositCommitment, Nullifier: transferNullifier, OutputCommitment: outputCommitment, Amount: privateAmount, Slot: account.lastPrivacyTransferSlot},
	})

	withdrawNullifier := newTestHash(146)
	recipientLamports = account.SendPrivateToTransparent(t, recipientAddress, recipientLamports, privateAmount, outputCommitment, withdrawNullifier)
	if recipientLamports != mustMinimumBalance(t, 0)+transparentAmount+privateAmount {
		t.Fatalf("recipient lamports = %d, want transparent and private receipts", recipientLamports)
	}
	if !account.privacyState.Notes[1].Spent || account.privacyState.Notes[1].SpendNullifier != withdrawNullifier {
		t.Fatalf("output note spend metadata = %+v, want spent by private withdraw", account.privacyState.Notes[1])
	}
	assertAuditPayloads(t, account.privacyState.Notes[1], account.regulatoryAuditor, account.regulatoryAuditKey, account.lastPrivacyWithdrawSlot, []PrivacyAuditPayload{
		{Version: PrivacyAuditPayloadVersion, TransactionType: PrivacyInstructionTransfer, Commitment: depositCommitment, Nullifier: transferNullifier, OutputCommitment: outputCommitment, Amount: privateAmount, Slot: account.lastPrivacyTransferSlot},
		{Version: PrivacyAuditPayloadVersion, TransactionType: PrivacyInstructionWithdraw, Commitment: outputCommitment, Nullifier: withdrawNullifier, Amount: privateAmount, Slot: account.lastPrivacyWithdrawSlot},
	})
}

func newTestAuditablePrivacyAccount(t *testing.T) *testAuditablePrivacyAccount {
	t.Helper()

	transparentAddress, transparentPrivateKey := newSimulationSigner(t)
	spendKeyPair := mustPrivacySpendKeyPair(t)
	return &testAuditablePrivacyAccount{
		transparentAddress:      transparentAddress,
		transparentPrivateKey:   transparentPrivateKey,
		spendPrivateScalar:      spendKeyPair.PrivateScalar,
		spendAuthority:          mustSchnorrSpendAuthority(t, spendKeyPair.PublicKey),
		privacyStateAddress:     newTestPublicKey(141),
		transparentLamports:     mustMinimumBalance(t, 0) + LamportsPerSignature*4 + 111 + 777 + 1000,
		privacyStateLamports:    mustMinimumBalance(t, 4096),
		regulatoryAuditor:       newTestPublicKey(147),
		regulatoryAuditKey:      newTestAuditKey(148),
		lastTransparentSlot:     201,
		lastPrivacyDepositSlot:  202,
		lastPrivacyTransferSlot: 203,
		lastPrivacyWithdrawSlot: 204,
	}
}

func (account *testAuditablePrivacyAccount) SendTransparentToTransparent(t *testing.T, recipientAddress PublicKey, recipientLamports uint64, amount uint64) uint64 {
	t.Helper()

	sourceLamports, updatedRecipientLamports := runBusinessLoopTransparentTransfer(t, account.transparentAddress, account.transparentPrivateKey, recipientAddress, account.transparentLamports, recipientLamports, amount)
	account.transparentLamports = sourceLamports
	return updatedRecipientLamports
}

func (account *testAuditablePrivacyAccount) SendTransparentToPrivate(t *testing.T, amount uint64, commitment Hash) PrivacyNoteRecord {
	t.Helper()

	result := runBusinessLoopDeposit(t, account.transparentAddress, account.transparentPrivateKey, account.privacyStateAddress, account.regulatoryAuditor, account.regulatoryAuditKey, account.transparentLamports, account.privacyStateLamports, amount, commitment, account.spendAuthority)
	account.transparentLamports = findWrittenAccount(t, result.WrittenAccounts, account.transparentAddress).Lamports
	account.privacyStateLamports = findWrittenAccount(t, result.WrittenAccounts, account.privacyStateAddress).Lamports
	account.privacyState = mustPrivacyStateFromWrittenAccount(t, result.WrittenAccounts, account.privacyStateAddress)
	return account.privacyState.Notes[0]
}

func (account *testAuditablePrivacyAccount) SendPrivateToPrivate(t *testing.T, amount uint64, sourceCommitment Hash, nullifier Hash, outputCommitment Hash) PrivacyNoteRecord {
	t.Helper()

	result := runBusinessLoopPrivateTransfer(t, account.transparentAddress, account.transparentPrivateKey, account.spendPrivateScalar, account.spendAuthority, account.privacyStateAddress, account.regulatoryAuditor, account.regulatoryAuditKey, account.transparentLamports, account.privacyStateLamports, account.privacyState, amount, sourceCommitment, nullifier, outputCommitment)
	account.transparentLamports = findWrittenAccount(t, result.WrittenAccounts, account.transparentAddress).Lamports
	account.privacyStateLamports = findWrittenAccount(t, result.WrittenAccounts, account.privacyStateAddress).Lamports
	account.privacyState = mustPrivacyStateFromWrittenAccount(t, result.WrittenAccounts, account.privacyStateAddress)
	return account.privacyState.Notes[1]
}

func (account *testAuditablePrivacyAccount) SendPrivateToTransparent(t *testing.T, recipientAddress PublicKey, recipientLamports uint64, amount uint64, sourceCommitment Hash, nullifier Hash) uint64 {
	t.Helper()

	result := runBusinessLoopWithdraw(t, account.transparentAddress, account.transparentPrivateKey, account.spendPrivateScalar, recipientAddress, account.privacyStateAddress, account.regulatoryAuditor, account.regulatoryAuditKey, account.transparentLamports, recipientLamports, account.privacyStateLamports, account.privacyState, amount, sourceCommitment, nullifier)
	account.privacyState = mustPrivacyStateFromWrittenAccount(t, result.WrittenAccounts, account.privacyStateAddress)
	account.privacyStateLamports = findWrittenAccount(t, result.WrittenAccounts, account.privacyStateAddress).Lamports
	account.transparentLamports = findWrittenAccount(t, result.WrittenAccounts, account.transparentAddress).Lamports
	return findWrittenAccount(t, result.WrittenAccounts, recipientAddress).Lamports
}
