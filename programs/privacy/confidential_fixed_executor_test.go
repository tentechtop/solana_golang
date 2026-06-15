package privacy

import (
	"bytes"
	"testing"

	"solana_golang/zk"
)

type confidentialOutputFixture struct {
	Opening zk.AmountOpening
	Output  ConfidentialOutputNote
}

func TestFixedConfidentialInstructionExecutorCompletesFourTransactionTypes(t *testing.T) {
	executor := FixedConfidentialInstructionExecutor{}
	sourceAddress := newTestPublicKey(171)
	recipientAddress := newTestPublicKey(172)
	auditorAddress := newTestPublicKey(173)
	regulatorKeyPair := mustConfidentialRegulatorKeyPair(t)
	auditPublicKeySet, auditShares := mustConfidentialAuditShares(t, regulatorKeyPair.PrivateScalar)
	spendKeyPair := mustConfidentialSpendKeyPair(t)
	spendAuthority := mustConfidentialSpendAuthority(t, spendKeyPair.PublicKey)
	ledger := NewConfidentialLedger(map[PublicKey]uint64{
		sourceAddress:    5000,
		recipientAddress: 100,
	})

	err := executor.ExecuteTransparentToTransparent(ledger, ConfidentialTransparentTransferInstruction{
		Source:      sourceAddress,
		Destination: recipientAddress,
		Amount:      111,
	})
	if err != nil {
		t.Fatalf("ExecuteTransparentToTransparent() error = %v", err)
	}
	assertConfidentialBalance(t, ledger, recipientAddress, 211)

	deposit := newConfidentialOutputFixture(t, 700, spendAuthority, regulatorKeyPair.PublicKey)
	deposit.Output.AuditRecords = []ConfidentialAuditRecord{
		newConfidentialAuditRecord(t, auditorAddress, regulatorKeyPair.PublicKey, PrivacyInstructionDeposit, deposit.Output.Commitment, Hash{}, nil, deposit.Opening),
	}
	depositAmountProof := mustCommitmentAmountProof(t, deposit.Output.Commitment, 700, deposit.Opening.Blinding)
	err = executor.ExecuteTransparentToPrivate(ledger, ConfidentialDepositInstruction{
		Source:      sourceAddress,
		Amount:      700,
		Output:      deposit.Output,
		AmountProof: depositAmountProof,
	})
	if err != nil {
		t.Fatalf("ExecuteTransparentToPrivate() error = %v", err)
	}
	assertConfidentialBalance(t, ledger, sourceAddress, 4189)
	assertConfidentialPool(t, ledger, 700)
	notes := mustConfidentialNotes(t, ledger)
	assertConfidentialAuditPayload(t, notes[0], auditorAddress, auditPublicKeySet, auditShares, PrivacyInstructionDeposit, 700, deposit.Output.Commitment, Hash{}, nil)

	transferNullifier := newTestHash(174)
	transferOutput := newConfidentialOutputFixture(t, 700, spendAuthority, regulatorKeyPair.PublicKey)
	transferOutput.Output.AuditRecords = []ConfidentialAuditRecord{
		newConfidentialAuditRecord(t, auditorAddress, regulatorKeyPair.PublicKey, PrivacyInstructionTransfer, deposit.Output.Commitment, transferNullifier, transferOutput.Output.Commitment, deposit.Opening),
	}
	transferDelta := mustSubtractScalars(t, deposit.Opening.Blinding, transferOutput.Opening.Blinding)
	transferBalanceProof := mustBalanceProof(t, [][]byte{deposit.Output.Commitment}, [][]byte{transferOutput.Output.Commitment}, 0, transferDelta)
	transferSpendMessage := BuildConfidentialPrivateTransferSpendMessage(deposit.Output.Commitment, transferNullifier, transferOutput.Output.Commitment)
	transferSpendProof := mustConfidentialSpendProof(t, spendKeyPair.PrivateScalar, transferSpendMessage)
	err = executor.ExecutePrivateToPrivate(ledger, ConfidentialPrivateTransferInstruction{
		SourceCommitment: deposit.Output.Commitment,
		Nullifier:        transferNullifier,
		Output:           transferOutput.Output,
		BalanceProof:     transferBalanceProof,
		SpendProof:       transferSpendProof,
	})
	if err != nil {
		t.Fatalf("ExecutePrivateToPrivate() error = %v", err)
	}
	notes = mustConfidentialNotes(t, ledger)
	if !notes[0].Spent || notes[0].SpendNullifier != transferNullifier {
		t.Fatalf("deposit note spend state = %+v, want private transfer spent", notes[0])
	}
	assertConfidentialAuditPayload(t, notes[1], auditorAddress, auditPublicKeySet, auditShares, PrivacyInstructionTransfer, 700, deposit.Output.Commitment, transferNullifier, transferOutput.Output.Commitment)

	withdrawNullifier := newTestHash(175)
	withdrawBalanceProof := mustBalanceProof(t, [][]byte{transferOutput.Output.Commitment}, nil, 700, transferOutput.Opening.Blinding)
	withdrawSpendMessage := BuildConfidentialWithdrawSpendMessage(transferOutput.Output.Commitment, withdrawNullifier, recipientAddress, 700)
	withdrawSpendProof := mustConfidentialSpendProof(t, spendKeyPair.PrivateScalar, withdrawSpendMessage)
	withdrawAuditRecord := newConfidentialAuditRecord(t, auditorAddress, regulatorKeyPair.PublicKey, PrivacyInstructionWithdraw, transferOutput.Output.Commitment, withdrawNullifier, nil, transferOutput.Opening)
	err = executor.ExecutePrivateToTransparent(ledger, ConfidentialWithdrawInstruction{
		SourceCommitment: transferOutput.Output.Commitment,
		Nullifier:        withdrawNullifier,
		Destination:      recipientAddress,
		Amount:           700,
		BalanceProof:     withdrawBalanceProof,
		SpendProof:       withdrawSpendProof,
		AuditRecords:     []ConfidentialAuditRecord{withdrawAuditRecord},
	})
	if err != nil {
		t.Fatalf("ExecutePrivateToTransparent() error = %v", err)
	}
	assertConfidentialBalance(t, ledger, recipientAddress, 911)
	assertConfidentialPool(t, ledger, 0)
	notes = mustConfidentialNotes(t, ledger)
	if !notes[1].Spent || notes[1].SpendNullifier != withdrawNullifier {
		t.Fatalf("transfer note spend state = %+v, want withdraw spent", notes[1])
	}
	assertConfidentialAuditPayload(t, notes[1], auditorAddress, auditPublicKeySet, auditShares, PrivacyInstructionWithdraw, 700, transferOutput.Output.Commitment, withdrawNullifier, nil)
}

func TestFixedConfidentialInstructionExecutorRejectsBadSpendProof(t *testing.T) {
	executor := FixedConfidentialInstructionExecutor{}
	sourceAddress := newTestPublicKey(181)
	auditorAddress := newTestPublicKey(182)
	regulatorKeyPair := mustConfidentialRegulatorKeyPair(t)
	spendKeyPair := mustConfidentialSpendKeyPair(t)
	wrongSpendKeyPair := mustConfidentialSpendKeyPair(t)
	spendAuthority := mustConfidentialSpendAuthority(t, spendKeyPair.PublicKey)
	ledger := NewConfidentialLedger(map[PublicKey]uint64{sourceAddress: 2000})

	deposit := newConfidentialOutputFixture(t, 300, spendAuthority, regulatorKeyPair.PublicKey)
	deposit.Output.AuditRecords = []ConfidentialAuditRecord{
		newConfidentialAuditRecord(t, auditorAddress, regulatorKeyPair.PublicKey, PrivacyInstructionDeposit, deposit.Output.Commitment, Hash{}, nil, deposit.Opening),
	}
	err := executor.ExecuteTransparentToPrivate(ledger, ConfidentialDepositInstruction{
		Source:      sourceAddress,
		Amount:      300,
		Output:      deposit.Output,
		AmountProof: mustCommitmentAmountProof(t, deposit.Output.Commitment, 300, deposit.Opening.Blinding),
	})
	if err != nil {
		t.Fatalf("ExecuteTransparentToPrivate() error = %v", err)
	}

	nullifier := newTestHash(183)
	output := newConfidentialOutputFixture(t, 300, spendAuthority, regulatorKeyPair.PublicKey)
	output.Output.AuditRecords = []ConfidentialAuditRecord{
		newConfidentialAuditRecord(t, auditorAddress, regulatorKeyPair.PublicKey, PrivacyInstructionTransfer, deposit.Output.Commitment, nullifier, output.Output.Commitment, deposit.Opening),
	}
	delta := mustSubtractScalars(t, deposit.Opening.Blinding, output.Opening.Blinding)
	spendMessage := BuildConfidentialPrivateTransferSpendMessage(deposit.Output.Commitment, nullifier, output.Output.Commitment)
	err = executor.ExecutePrivateToPrivate(ledger, ConfidentialPrivateTransferInstruction{
		SourceCommitment: deposit.Output.Commitment,
		Nullifier:        nullifier,
		Output:           output.Output,
		BalanceProof:     mustBalanceProof(t, [][]byte{deposit.Output.Commitment}, [][]byte{output.Output.Commitment}, 0, delta),
		SpendProof:       mustConfidentialSpendProof(t, wrongSpendKeyPair.PrivateScalar, spendMessage),
	})
	if err == nil {
		t.Fatal("ExecutePrivateToPrivate() accepted wrong spend proof")
	}
	notes := mustConfidentialNotes(t, ledger)
	if notes[0].Spent {
		t.Fatal("note was spent after rejected spend proof")
	}
}

func TestFixedConfidentialInstructionExecutorRejectsMismatchedAuditCiphertext(t *testing.T) {
	executor := FixedConfidentialInstructionExecutor{}
	sourceAddress := newTestPublicKey(184)
	auditorAddress := newTestPublicKey(185)
	regulatorKeyPair := mustConfidentialRegulatorKeyPair(t)
	spendKeyPair := mustConfidentialSpendKeyPair(t)
	spendAuthority := mustConfidentialSpendAuthority(t, spendKeyPair.PublicKey)
	ledger := NewConfidentialLedger(map[PublicKey]uint64{sourceAddress: 1000})

	deposit := newConfidentialOutputFixture(t, 300, spendAuthority, regulatorKeyPair.PublicKey)
	deposit.Output.AuditRecords = []ConfidentialAuditRecord{
		newConfidentialAuditRecord(t, auditorAddress, regulatorKeyPair.PublicKey, PrivacyInstructionDeposit, deposit.Output.Commitment, Hash{}, nil, deposit.Opening),
	}
	deposit.Output.AuditRecords[0].AmountCiphertext = mustConfidentialAmountCiphertextOnly(t, regulatorKeyPair.PublicKey, 301)
	err := executor.ExecuteTransparentToPrivate(ledger, ConfidentialDepositInstruction{
		Source:      sourceAddress,
		Amount:      300,
		Output:      deposit.Output,
		AmountProof: mustCommitmentAmountProof(t, deposit.Output.Commitment, 300, deposit.Opening.Blinding),
	})
	if err == nil {
		t.Fatal("ExecuteTransparentToPrivate() accepted mismatched audit ciphertext")
	}
	assertConfidentialBalance(t, ledger, sourceAddress, 1000)
	assertConfidentialPool(t, ledger, 0)
}

func newConfidentialOutputFixture(t *testing.T, amount uint64, spendAuthority PublicKey, regulatorPublicKey []byte) confidentialOutputFixture {
	t.Helper()

	opening, err := zk.NewAmountOpening(amount)
	if err != nil {
		t.Fatalf("NewAmountOpening() error = %v", err)
	}
	commitment, err := zk.CommitAmount(opening)
	if err != nil {
		t.Fatalf("CommitAmount() error = %v", err)
	}
	rangeProof, err := zk.NewRangeProof(opening, zk.ConfidentialRangeBits)
	if err != nil {
		t.Fatalf("NewRangeProof() error = %v", err)
	}
	ciphertext, randomness := mustConfidentialAmountCiphertext(t, regulatorPublicKey, amount)
	amountProof := mustConfidentialAmountCiphertextProof(t, regulatorPublicKey, commitment, ciphertext, opening, randomness)
	return confidentialOutputFixture{
		Opening: opening,
		Output: ConfidentialOutputNote{
			Commitment:       commitment,
			SpendAuthority:   spendAuthority,
			AmountPublicKey:  append([]byte(nil), regulatorPublicKey...),
			AmountCiphertext: ciphertext,
			AmountProof:      amountProof,
			RangeProof:       rangeProof,
		},
	}
}

func newConfidentialAuditRecord(t *testing.T, auditor PublicKey, regulatorPublicKey []byte, transactionType PrivacyInstructionType, commitment []byte, nullifier Hash, outputCommitment []byte, opening zk.AmountOpening) ConfidentialAuditRecord {
	t.Helper()

	ciphertext, randomness := mustConfidentialAmountCiphertext(t, regulatorPublicKey, opening.Amount)
	return ConfidentialAuditRecord{
		Auditor:          auditor,
		Scope:            PrivacyAuditScopeRegulatory,
		ExpiresAtSlot:    1000,
		TransactionType:  transactionType,
		Commitment:       append([]byte(nil), commitment...),
		Nullifier:        nullifier,
		OutputCommitment: append([]byte(nil), outputCommitment...),
		AuditPublicKey:   append([]byte(nil), regulatorPublicKey...),
		AmountCiphertext: ciphertext,
		AmountProof:      mustConfidentialAmountCiphertextProof(t, regulatorPublicKey, commitment, ciphertext, opening, randomness),
	}
}

func mustConfidentialRegulatorKeyPair(t *testing.T) zk.ElGamalKeyPair {
	t.Helper()

	keyPair, err := zk.GenerateElGamalKeyPair()
	if err != nil {
		t.Fatalf("GenerateElGamalKeyPair() error = %v", err)
	}
	return keyPair
}

func mustConfidentialAuditShares(t *testing.T, privateScalar []byte) (zk.ThresholdPublicKeySet, []zk.ThresholdShare) {
	t.Helper()

	publicKeySet, shares, err := zk.SplitScalarWithPublicKeySet(privateScalar, 3, 5)
	if err != nil {
		t.Fatalf("SplitScalarWithPublicKeySet() error = %v", err)
	}
	return publicKeySet, []zk.ThresholdShare{shares[0], shares[2], shares[4]}
}

func mustConfidentialSpendKeyPair(t *testing.T) zk.SchnorrKeyPair {
	t.Helper()

	keyPair, err := zk.GenerateSchnorrKeyPair()
	if err != nil {
		t.Fatalf("GenerateSchnorrKeyPair() error = %v", err)
	}
	return keyPair
}

func mustConfidentialSpendAuthority(t *testing.T, publicKey []byte) PublicKey {
	t.Helper()

	digest, err := zk.SchnorrPublicKeyDigest(publicKey)
	if err != nil {
		t.Fatalf("SchnorrPublicKeyDigest() error = %v", err)
	}
	authority, err := NewPublicKey(digest[:])
	if err != nil {
		t.Fatalf("NewPublicKey() error = %v", err)
	}
	return authority
}

func mustConfidentialAmountCiphertext(t *testing.T, publicKey []byte, amount uint64) (zk.ElGamalCiphertext, []byte) {
	t.Helper()

	ciphertext, randomness, err := zk.EncryptAmount(publicKey, amount)
	if err != nil {
		t.Fatalf("EncryptAmount() error = %v", err)
	}
	return ciphertext, randomness
}

func mustConfidentialAmountCiphertextOnly(t *testing.T, publicKey []byte, amount uint64) zk.ElGamalCiphertext {
	t.Helper()

	ciphertext, _ := mustConfidentialAmountCiphertext(t, publicKey, amount)
	return ciphertext
}

func mustConfidentialAmountCiphertextProof(t *testing.T, publicKey []byte, commitment []byte, ciphertext zk.ElGamalCiphertext, opening zk.AmountOpening, randomness []byte) zk.AmountCiphertextProof {
	t.Helper()

	proof, err := zk.NewAmountCiphertextProof(publicKey, commitment, ciphertext, opening, randomness)
	if err != nil {
		t.Fatalf("NewAmountCiphertextProof() error = %v", err)
	}
	return proof
}

func mustCommitmentAmountProof(t *testing.T, commitment []byte, amount uint64, blinding []byte) zk.BalanceProof {
	t.Helper()

	proof, err := zk.NewCommitmentAmountProof(commitment, amount, blinding)
	if err != nil {
		t.Fatalf("NewCommitmentAmountProof() error = %v", err)
	}
	return proof
}

func mustSubtractScalars(t *testing.T, left []byte, right []byte) []byte {
	t.Helper()

	delta, err := zk.SubtractScalars(left, right)
	if err != nil {
		t.Fatalf("SubtractScalars() error = %v", err)
	}
	return delta
}

func mustBalanceProof(t *testing.T, inputs [][]byte, outputs [][]byte, publicAmount uint64, blindingDelta []byte) zk.BalanceProof {
	t.Helper()

	proof, err := zk.NewBalanceProof(inputs, outputs, publicAmount, blindingDelta)
	if err != nil {
		t.Fatalf("NewBalanceProof() error = %v", err)
	}
	return proof
}

func mustConfidentialSpendProof(t *testing.T, privateScalar []byte, message []byte) []byte {
	t.Helper()

	proof, err := zk.NewSchnorrProofBytes(privateScalar, message)
	if err != nil {
		t.Fatalf("NewSchnorrProofBytes() error = %v", err)
	}
	return proof
}

func assertConfidentialBalance(t *testing.T, ledger *ConfidentialLedger, address PublicKey, want uint64) {
	t.Helper()

	got, err := ledger.TransparentBalance(address)
	if err != nil {
		t.Fatalf("TransparentBalance() error = %v", err)
	}
	if got != want {
		t.Fatalf("TransparentBalance() = %d, want %d", got, want)
	}
}

func assertConfidentialPool(t *testing.T, ledger *ConfidentialLedger, want uint64) {
	t.Helper()

	got, err := ledger.PrivacyPoolBalance()
	if err != nil {
		t.Fatalf("PrivacyPoolBalance() error = %v", err)
	}
	if got != want {
		t.Fatalf("PrivacyPoolBalance() = %d, want %d", got, want)
	}
}

func mustConfidentialNotes(t *testing.T, ledger *ConfidentialLedger) []ConfidentialNote {
	t.Helper()

	notes, err := ledger.NotesSnapshot()
	if err != nil {
		t.Fatalf("NotesSnapshot() error = %v", err)
	}
	return notes
}

func assertConfidentialAuditPayload(t *testing.T, note ConfidentialNote, auditor PublicKey, publicKeySet zk.ThresholdPublicKeySet, shares []zk.ThresholdShare, transactionType PrivacyInstructionType, amount uint64, commitment []byte, nullifier Hash, outputCommitment []byte) {
	t.Helper()

	payloads, err := AuditConfidentialNote(note, auditor, PrivacyAuditScopeRegulatory, 900, publicKeySet, shares, 2048)
	if err != nil {
		t.Fatalf("AuditConfidentialNote() error = %v", err)
	}
	for _, payload := range payloads {
		if payload.TransactionType != transactionType {
			continue
		}
		assertConfidentialPayloadFields(t, payload, amount, commitment, nullifier, outputCommitment)
		return
	}
	t.Fatalf("audit payload for type %d not found: %+v", transactionType, payloads)
}

func assertConfidentialPayloadFields(t *testing.T, payload ConfidentialAuditPayload, amount uint64, commitment []byte, nullifier Hash, outputCommitment []byte) {
	t.Helper()

	if payload.Amount != amount {
		t.Fatalf("audit amount = %d, want %d", payload.Amount, amount)
	}
	if !bytes.Equal(payload.Commitment, commitment) {
		t.Fatal("audit commitment mismatch")
	}
	if payload.Nullifier != nullifier {
		t.Fatal("audit nullifier mismatch")
	}
	if !bytes.Equal(payload.OutputCommitment, outputCommitment) {
		t.Fatal("audit output commitment mismatch")
	}
}
