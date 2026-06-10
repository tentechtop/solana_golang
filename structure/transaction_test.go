package structure

import (
	"errors"
	"testing"

	"solana_golang/utils"
)

// TestTransactionValidateMarshalAndHash 验证目标行为 + 保证核心场景和边界条件稳定。
func TestTransactionValidateMarshalAndHash(t *testing.T) {
	transaction := newTestTransaction(t)

	if err := transaction.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	encoded, err := transaction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("MarshalBinary() returned empty bytes")
	}

	transactionID, err := transaction.TxID()
	if err != nil {
		t.Fatalf("TxID() error = %v", err)
	}
	if transactionID == (Hash{}) {
		t.Fatal("TxID() returned zero hash")
	}
}

// TestTransactionBuildSignDataExcludesSignatures 验证目标行为 + 保证核心场景和边界条件稳定。
func TestTransactionBuildSignDataExcludesSignatures(t *testing.T) {
	transaction := newTestTransaction(t)

	signData, err := transaction.BuildSignData()
	if err != nil {
		t.Fatalf("BuildSignData() error = %v", err)
	}
	if len(signData) == 0 {
		t.Fatal("BuildSignData() returned empty bytes")
	}

	originalSignature := transaction.Signatures[0]
	transaction.Signatures[0] = newTestSignature(9)
	nextSignData, err := transaction.BuildSignData()
	if err != nil {
		t.Fatalf("BuildSignData(changed signature) error = %v", err)
	}
	if string(signData) != string(nextSignData) {
		t.Fatal("BuildSignData() changed after signature mutation")
	}
	transaction.Signatures[0] = originalSignature
}

// TestTransactionBuildsSolanaMessageFromAccountMeta 验证目标行为 + 保证核心场景和边界条件稳定。
func TestTransactionBuildsSolanaMessageFromAccountMeta(t *testing.T) {
	transaction := newTestTransaction(t)

	message, err := transaction.SolanaMessage()
	if err != nil {
		t.Fatalf("SolanaMessage() error = %v", err)
	}

	if message.Header.NumRequiredSignatures != 1 {
		t.Fatalf("NumRequiredSignatures = %d, want 1", message.Header.NumRequiredSignatures)
	}
	if message.Header.NumReadonlyUnsignedAccounts != 1 {
		t.Fatalf("NumReadonlyUnsignedAccounts = %d, want 1", message.Header.NumReadonlyUnsignedAccounts)
	}
	if len(message.AccountKeys) != len(transaction.Accounts) {
		t.Fatalf("AccountKeys length = %d, want %d", len(message.AccountKeys), len(transaction.Accounts))
	}
}

// TestVersionedSolanaMessageAllowsAddressTableIndexes 验证目标行为 + 保证核心场景和边界条件稳定。
func TestVersionedSolanaMessageAllowsAddressTableIndexes(t *testing.T) {
	transaction := newTestTransaction(t)
	transaction.Message = SolanaMessage{
		Header: MessageHeader{
			NumRequiredSignatures:       1,
			NumReadonlySignedAccounts:   0,
			NumReadonlyUnsignedAccounts: 0,
		},
		AccountKeys:     []PublicKey{newTestPublicKey(1)},
		RecentBlockhash: newTestHash(3),
		Instructions: []CompiledInstruction{
			{
				ProgramIDIndex: 1,
				AccountIndexes: []uint8{0, 1},
				Data:           []byte{1},
			},
		},
		AddressTableLookups: []MessageAddressTableLookup{
			{
				AccountKey:      newTestPublicKey(9),
				WritableIndexes: []uint8{2},
			},
		},
		Version:          0,
		UsesAddressTable: true,
	}

	if err := transaction.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// TestTransactionSenderAndSignerAccounts 验证目标行为 + 保证核心场景和边界条件稳定。
func TestTransactionSenderAndSignerAccounts(t *testing.T) {
	transaction := newTestTransaction(t)

	sender, err := transaction.Sender()
	if err != nil {
		t.Fatalf("Sender() error = %v", err)
	}
	if !sender.Equal(transaction.Accounts[0].PublicKey) {
		t.Fatal("Sender() did not return writable signer")
	}
	if transaction.RequiredSignatureCount() != 1 {
		t.Fatalf("RequiredSignatureCount() = %d, want 1", transaction.RequiredSignatureCount())
	}
	if len(transaction.SignerAccounts()) != 1 {
		t.Fatalf("SignerAccounts() length = %d, want 1", len(transaction.SignerAccounts()))
	}
}

// TestTransactionRejectsSignatureMismatch 验证目标行为 + 保证核心场景和边界条件稳定。
func TestTransactionRejectsSignatureMismatch(t *testing.T) {
	transaction := newTestTransaction(t)
	transaction.Signatures = append(transaction.Signatures, newTestSignature(2))

	err := transaction.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want signature mismatch")
	}
}

// TestTransactionRejectsDuplicateAccounts 验证目标行为 + 保证核心场景和边界条件稳定。
func TestTransactionRejectsDuplicateAccounts(t *testing.T) {
	transaction := newTestTransaction(t)
	transaction.Accounts[1].PublicKey = transaction.Accounts[0].PublicKey

	err := transaction.Validate()
	if !errors.Is(err, ErrInvalidAccountMeta) {
		t.Fatalf("Validate() error = %v, want ErrInvalidAccountMeta", err)
	}
}

// TestTransactionRejectsInvalidInstructionIndex 验证目标行为 + 保证核心场景和边界条件稳定。
func TestTransactionRejectsInvalidInstructionIndex(t *testing.T) {
	transaction := newTestTransaction(t)
	transaction.Instructions[0].ProgramIDIndex = 99

	err := transaction.Validate()
	if !errors.Is(err, ErrInvalidInstruction) {
		t.Fatalf("Validate() error = %v, want ErrInvalidInstruction", err)
	}
}

// TestTransactionRejectsEmptyRecentBlockhash 验证目标行为 + 保证核心场景和边界条件稳定。
func TestTransactionRejectsEmptyRecentBlockhash(t *testing.T) {
	transaction := newTestTransaction(t)
	transaction.RecentBlockhash = Blockhash{}

	err := transaction.Validate()
	if !errors.Is(err, ErrEmptyRecentBlockhash) {
		t.Fatalf("Validate() error = %v, want ErrEmptyRecentBlockhash", err)
	}
}

// TestTransactionExpiration 验证目标行为 + 保证核心场景和边界条件稳定。
func TestTransactionExpiration(t *testing.T) {
	transaction := newTestTransaction(t)
	transaction.SubmitTime = 1000

	if transaction.IsExpired(1200) {
		t.Fatal("IsExpired() = true, want false")
	}
	if !transaction.IsExpired(1501) {
		t.Fatal("IsExpired() = false, want true")
	}
}

// TestTransactionCloneIsolation 验证目标行为 + 保证核心场景和边界条件稳定。
func TestTransactionCloneIsolation(t *testing.T) {
	transaction := newTestTransaction(t)
	cloned := transaction.Clone()
	cloned.Instructions[0].Data[0] = 99

	if transaction.Instructions[0].Data[0] == cloned.Instructions[0].Data[0] {
		t.Fatal("Clone() shares instruction data")
	}
}

// TestTransactionSignAndVerifySignatures 验证目标行为 + 保证核心场景和边界条件稳定。
func TestTransactionSignAndVerifySignatures(t *testing.T) {
	publicKeyBytes, privateKey, err := utils.GenerateEd25519KeyPairBytes()
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPairBytes() error = %v", err)
	}
	publicKey, err := NewPublicKey(publicKeyBytes)
	if err != nil {
		t.Fatalf("NewPublicKey() error = %v", err)
	}

	transaction := newTestTransaction(t)
	transaction.Signatures = nil
	transaction.Accounts[0].PublicKey = publicKey
	signedTransaction, err := transaction.Sign(map[PublicKey][]byte{
		publicKey: privateKey,
	})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	valid, err := signedTransaction.HasValidSignatures()
	if err != nil {
		t.Fatalf("HasValidSignatures() error = %v", err)
	}
	if !valid {
		t.Fatal("HasValidSignatures() = false, want true")
	}

	signedTransaction.Instructions[0].Data[0] = 99
	valid, err = signedTransaction.HasValidSignatures()
	if err != nil {
		t.Fatalf("HasValidSignatures(mutated) error = %v", err)
	}
	if valid {
		t.Fatal("HasValidSignatures(mutated) = true, want false")
	}
}

// TestAccountMetaMergeSortAndIndexMap 验证目标行为 + 保证核心场景和边界条件稳定。
func TestAccountMetaMergeSortAndIndexMap(t *testing.T) {
	firstKey := newTestPublicKey(1)
	secondKey := newTestPublicKey(2)

	mergedAccounts, err := MergeAccountMetas([]AccountMeta{
		{PublicKey: secondKey, IsSigner: false, IsWritable: false},
		{PublicKey: firstKey, IsSigner: true, IsWritable: false},
		{PublicKey: firstKey, IsSigner: false, IsWritable: true},
	})
	if err != nil {
		t.Fatalf("MergeAccountMetas() error = %v", err)
	}
	if len(mergedAccounts) != 2 {
		t.Fatalf("MergeAccountMetas() length = %d, want 2", len(mergedAccounts))
	}
	if !mergedAccounts[1].IsFeePayer() {
		t.Fatal("MergeAccountMetas() did not preserve signer and writable permissions")
	}

	sortedAccounts := SortAccountMetasForMessage(mergedAccounts)
	if !sortedAccounts[0].IsFeePayer() {
		t.Fatal("SortAccountMetasForMessage() did not move fee payer first")
	}

	indexMap, err := AccountIndexMap(sortedAccounts)
	if err != nil {
		t.Fatalf("AccountIndexMap() error = %v", err)
	}
	if indexMap[firstKey] != 0 {
		t.Fatalf("AccountIndexMap(firstKey) = %d, want 0", indexMap[firstKey])
	}
}

// TestCompileInstructionAndMarshal 验证目标行为 + 保证核心场景和边界条件稳定。
func TestCompileInstructionAndMarshal(t *testing.T) {
	programKey := newTestPublicKey(1)
	accountKey := newTestPublicKey(2)
	indexMap := map[PublicKey]uint8{
		programKey: 0,
		accountKey: 1,
	}

	instruction, err := CompileInstruction(programKey, []PublicKey{accountKey}, []byte{7, 8}, indexMap)
	if err != nil {
		t.Fatalf("CompileInstruction() error = %v", err)
	}
	if !instruction.HasAccountIndex(1) {
		t.Fatal("HasAccountIndex() = false, want true")
	}

	encoded, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("MarshalBinary() returned empty bytes")
	}
}
func newTestTransaction(t *testing.T) Transaction {
	t.Helper()

	return Transaction{
		Signatures: []Signature{newTestSignature(1)},
		Accounts: []AccountMeta{
			{
				PublicKey:  newTestPublicKey(1),
				IsSigner:   true,
				IsWritable: true,
			},
			{
				PublicKey:  newTestPublicKey(2),
				IsSigner:   false,
				IsWritable: false,
			},
		},
		Instructions: []CompiledInstruction{
			{
				ProgramIDIndex: 1,
				AccountIndexes: []uint8{0},
				Data:           []byte{1, 2, 3},
			},
		},
		RecentBlockhash: newTestHash(3),
		PohRecord: &PohRecord{
			Slot:      2,
			Hash:      newTestHash(4),
			Sequence:  10,
			Timestamp: 1000,
		},
		Fee:        5000,
		Size:       0,
		SubmitTime: 1000,
		Status:     TransactionStatusPending,
	}
}
func newTestPublicKey(seed byte) PublicKey {
	var key PublicKey
	for index := range key {
		key[index] = seed + byte(index)
	}
	return key
}
func newTestHash(seed byte) Hash {
	var hash Hash
	for index := range hash {
		hash[index] = seed + byte(index)
	}
	return hash
}
func newTestSignature(seed byte) Signature {
	var signature Signature
	for index := range signature {
		signature[index] = seed + byte(index)
	}
	return signature
}
