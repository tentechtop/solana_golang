package structure

import (
	"errors"
	"testing"

	"solana_golang/utils"
)

func TestTransactionValidateAndMarshal(t *testing.T) {
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

	hash, err := transaction.Hash()
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if hash == (utils.Hash{}) {
		t.Fatal("Hash() returned zero hash")
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

func TestTransactionRejectsInvalidInstructionIndex(t *testing.T) {
	transaction := newTestTransaction(t)
	transaction.Message.Instructions[0].ProgramIDIndex = 99

	err := transaction.Validate()
	if !errors.Is(err, ErrInvalidInstruction) {
		t.Fatalf("Validate() error = %v, want ErrInvalidInstruction", err)
	}
}

func TestMessageRejectsEmptyRecentBlockhash(t *testing.T) {
	transaction := newTestTransaction(t)
	transaction.Message.RecentBlockhash = utils.Blockhash{}

	err := transaction.Validate()
	if !errors.Is(err, ErrEmptyRecentBlockhash) {
		t.Fatalf("Validate() error = %v, want ErrEmptyRecentBlockhash", err)
	}
}

func newTestTransaction(t *testing.T) Transaction {
	t.Helper()

	return Transaction{
		Signatures: []utils.Signature{newTestSignature(1)},
		Message: Message{
			Header: MessageHeader{
				NumRequiredSignatures:       1,
				NumReadonlySignedAccounts:   0,
				NumReadonlyUnsignedAccounts: 1,
			},
			AccountKeys: []utils.PublicKey{
				newTestPublicKey(1),
				newTestPublicKey(2),
			},
			RecentBlockhash: newTestHash(3),
			Instructions: []CompiledInstruction{
				{
					ProgramIDIndex: 1,
					AccountIndexes: []uint16{0},
					Data:           []byte{1, 2, 3},
				},
			},
		},
	}
}

func newTestPublicKey(seed byte) utils.PublicKey {
	var key utils.PublicKey
	for index := range key {
		key[index] = seed + byte(index)
	}
	return key
}

func newTestHash(seed byte) utils.Hash {
	var hash utils.Hash
	for index := range hash {
		hash[index] = seed + byte(index)
	}
	return hash
}

func newTestSignature(seed byte) utils.Signature {
	var signature utils.Signature
	for index := range signature {
		signature[index] = seed + byte(index)
	}
	return signature
}
