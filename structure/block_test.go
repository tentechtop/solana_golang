package structure

import (
	"errors"
	"testing"
)

// TestBlockValidateMarshalAndRoot 验证目标行为 + 保证核心场景和边界条件稳定。
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
	if root == (Hash{}) {
		t.Fatal("ComputeTransactionsRoot() returned zero hash")
	}
}

// TestBlockRejectsInvalidParentSlot 验证目标行为 + 保证核心场景和边界条件稳定。
func TestBlockRejectsInvalidParentSlot(t *testing.T) {
	block := newTestBlock(t)
	block.Header.ParentSlot = block.Header.Slot

	err := block.Validate()
	if !errors.Is(err, ErrInvalidBlockHeader) {
		t.Fatalf("Validate() error = %v, want ErrInvalidBlockHeader", err)
	}
}

// TestBlockRejectsEmptyBlockhash 验证目标行为 + 保证核心场景和边界条件稳定。
func TestBlockRejectsEmptyBlockhash(t *testing.T) {
	block := newTestBlock(t)
	block.Header.Blockhash = Hash{}

	err := block.Validate()
	if !errors.Is(err, ErrEmptyBlockhash) {
		t.Fatalf("Validate() error = %v, want ErrEmptyBlockhash", err)
	}
}

// TestBlockRejectsTransactionsRootMismatch 验证目标行为 + 保证核心场景和边界条件稳定。
func TestBlockRejectsTransactionsRootMismatch(t *testing.T) {
	block := newTestBlock(t)
	block.Header.TransactionsRoot = newTestHash(99)

	err := block.Validate()
	if !errors.Is(err, ErrInvalidBlockHeader) {
		t.Fatalf("Validate() error = %v, want ErrInvalidBlockHeader", err)
	}
}

// newTestBlock 执行对应逻辑 + 保持函数职责清晰可维护。
func newTestBlock(t *testing.T) Block {
	t.Helper()

	block := Block{
		Header: BlockHeader{
			Version:           1,
			Slot:              2,
			ParentSlot:        1,
			BlockHeight:       2,
			ParentHash:        newTestHash(4),
			PreviousBlockhash: newTestHash(5),
			Blockhash:         newTestHash(5),
			AccountsHash:      newTestHash(6),
			StateRoot:         newTestHash(7),
			RewardsHash:       newTestHash(8),
			EntriesHash:       newTestHash(9),
			TimestampUnix:     1710000000,
			Leader:            newTestPublicKey(10),
			TransactionCount:  1,
		},
		Transactions: []Transaction{newTestTransaction(t)},
		Meta: BlockMeta{
			Status:            BlockStatusConfirmed,
			TotalFees:         5000,
			ComputeUnitsUsed:  100,
			ReceivedTimeUnix:  1710000000,
			FinalizedTimeUnix: 1710000001,
		},
	}
	root, err := block.ComputeTransactionsRoot()
	if err != nil {
		t.Fatalf("ComputeTransactionsRoot() error = %v", err)
	}
	block.Header.TransactionsRoot = root
	return block
}
