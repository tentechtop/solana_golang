package structure

import (
	"errors"
	"testing"

	"solana_golang/utils"
)

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
	if transactionID == (Signature{}) {
		t.Fatal("TxID() returned zero hash")
	}
	if !transactionID.Equal(transaction.Signatures[0]) {
		t.Fatal("TxID() did not return first signature")
	}
	transactionHash, err := transaction.Hash()
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if transactionHash == (Hash{}) {
		t.Fatal("Hash() returned zero hash")
	}
}
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

func TestTransactionUnmarshalLegacyRoundTrip(t *testing.T) {
	transaction := newTestTransaction(t)
	encoded, err := transaction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}

	decoded, err := UnmarshalTransactionBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalTransactionBinary() error = %v", err)
	}
	reencoded, err := decoded.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(decoded) error = %v", err)
	}
	if string(reencoded) != string(encoded) {
		t.Fatal("legacy transaction did not round trip")
	}
	if decoded.Message.UsesAddressTable {
		t.Fatal("legacy message UsesAddressTable = true")
	}
}

func TestVersionedMessageUsesPrefixAndRoundTrip(t *testing.T) {
	transaction := newTestVersionedTransaction()
	encoded, err := transaction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}

	messageOffset := 1 + SignatureSize
	if encoded[messageOffset] != 0x80 {
		t.Fatalf("version prefix = %#x, want 0x80", encoded[messageOffset])
	}

	decoded, err := UnmarshalTransactionBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalTransactionBinary() error = %v", err)
	}
	if !decoded.Message.UsesAddressTable {
		t.Fatal("decoded message UsesAddressTable = false")
	}
	if decoded.Message.Version != 0 {
		t.Fatalf("decoded version = %d, want 0", decoded.Message.Version)
	}
	if len(decoded.Message.AddressTableLookups) != 1 {
		t.Fatalf("decoded lookups = %d, want 1", len(decoded.Message.AddressTableLookups))
	}
}

func TestTransactionRejectsOversizedWireTransaction(t *testing.T) {
	transaction := newOversizedWireTransaction()

	if _, err := transaction.MarshalBinary(); !errors.Is(err, ErrTransactionTooLarge) {
		t.Fatalf("MarshalBinary(oversized) error = %v, want ErrTransactionTooLarge", err)
	}
}
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
func TestTransactionRejectsSignatureMismatch(t *testing.T) {
	transaction := newTestTransaction(t)
	transaction.Signatures = append(transaction.Signatures, newTestSignature(2))

	err := transaction.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want signature mismatch")
	}
}
func TestTransactionRejectsDuplicateAccounts(t *testing.T) {
	transaction := newTestTransaction(t)
	transaction.Accounts[1].PublicKey = transaction.Accounts[0].PublicKey

	err := transaction.Validate()
	if !errors.Is(err, ErrInvalidAccountMeta) {
		t.Fatalf("Validate() error = %v, want ErrInvalidAccountMeta", err)
	}
}
func TestTransactionRejectsInvalidInstructionIndex(t *testing.T) {
	transaction := newTestTransaction(t)
	transaction.Instructions[0].ProgramIDIndex = 99

	err := transaction.Validate()
	if !errors.Is(err, ErrInvalidInstruction) {
		t.Fatalf("Validate() error = %v, want ErrInvalidInstruction", err)
	}
}
func TestTransactionRejectsEmptyRecentBlockhash(t *testing.T) {
	transaction := newTestTransaction(t)
	transaction.RecentBlockhash = Blockhash{}

	err := transaction.Validate()
	if !errors.Is(err, ErrEmptyRecentBlockhash) {
		t.Fatalf("Validate() error = %v, want ErrEmptyRecentBlockhash", err)
	}
}
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
func TestTransactionCloneIsolation(t *testing.T) {
	transaction := newTestTransaction(t)
	cloned := transaction.Clone()
	cloned.Instructions[0].Data[0] = 99

	if transaction.Instructions[0].Data[0] == cloned.Instructions[0].Data[0] {
		t.Fatal("Clone() shares instruction data")
	}
}
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
		Fee:             5000,
		Size:            0,
		SubmitTime:      1000,
		Status:          TransactionStatusPending,
	}
}

func newTestVersionedTransaction() Transaction {
	return Transaction{
		Signatures: []Signature{newTestSignature(1)},
		Message: SolanaMessage{
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
		},
		Status: TransactionStatusPending,
	}
}

func newOversizedWireTransaction() Transaction {
	signatures := make([]Signature, MaxSignaturesPerTransaction)
	accounts := make([]AccountMeta, 0, MaxSignaturesPerTransaction+1)
	for index := range signatures {
		signatures[index] = newTestSignature(byte(index + 1))
		accounts = append(accounts, AccountMeta{
			PublicKey:  newTestPublicKey(byte(index + 1)),
			IsSigner:   true,
			IsWritable: true,
		})
	}
	accounts = append(accounts, AccountMeta{
		PublicKey:  newTestPublicKey(30),
		IsSigner:   false,
		IsWritable: false,
	})
	return Transaction{
		Signatures: signatures,
		Accounts:   accounts,
		Instructions: []CompiledInstruction{
			{
				ProgramIDIndex: uint8(len(accounts) - 1),
				AccountIndexes: []uint8{0},
				Data:           make([]byte, 20),
			},
		},
		RecentBlockhash: newTestHash(3),
		Status:          TransactionStatusPending,
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
