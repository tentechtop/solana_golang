package structure

import (
	"errors"
	"testing"

	"solana_golang/utils"
)

func TestBlockValidateMarshalAndRoot(t *testing.T) {
	block := newTestBlock(t)

	if err := block.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	encoded, err := block.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("MarshalBinary() returned empty bytes")
	}

	root, err := block.ComputeTransactionsRoot()
	if err != nil {
		t.Fatalf("ComputeTransactionsRoot() error = %v", err)
	}
	if root == (utils.Hash{}) {
		t.Fatal("ComputeTransactionsRoot() returned zero hash")
	}
}

func TestBlockRejectsInvalidParentSlot(t *testing.T) {
	block := newTestBlock(t)
	block.Header.ParentSlot = block.Header.Slot

	err := block.Validate()
	if !errors.Is(err, ErrInvalidBlockHeader) {
		t.Fatalf("Validate() error = %v, want ErrInvalidBlockHeader", err)
	}
}

func TestBlockRejectsEmptyBlockhash(t *testing.T) {
	block := newTestBlock(t)
	block.Header.Blockhash = utils.Hash{}

	err := block.Validate()
	if !errors.Is(err, ErrEmptyBlockhash) {
		t.Fatalf("Validate() error = %v, want ErrEmptyBlockhash", err)
	}
}

func newTestBlock(t *testing.T) Block {
	t.Helper()

	return Block{
		Header: BlockHeader{
			Slot:             2,
			ParentSlot:       1,
			ParentHash:       newTestHash(4),
			Blockhash:        newTestHash(5),
			TransactionsRoot: newTestHash(6),
			StateRoot:        newTestHash(7),
			TimestampUnix:    1710000000,
			Leader:           newTestPublicKey(8),
		},
		Transactions: []Transaction{newTestTransaction(t)},
	}
}
