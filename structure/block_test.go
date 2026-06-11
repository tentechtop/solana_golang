package structure

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
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
	if root == (Hash{}) {
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
	block.Header.Blockhash = Hash{}

	err := block.Validate()
	if !errors.Is(err, ErrEmptyBlockhash) {
		t.Fatalf("Validate() error = %v, want ErrEmptyBlockhash", err)
	}
}
func TestBlockRejectsTransactionsRootMismatch(t *testing.T) {
	block := newTestBlock(t)
	block.Header.TransactionsRoot = newTestHash(99)

	err := block.Validate()
	if !errors.Is(err, ErrInvalidBlockHeader) {
		t.Fatalf("Validate() error = %v, want ErrInvalidBlockHeader", err)
	}
}
func TestBlockMarshalIsDeterministic(t *testing.T) {
	block := newTestBlock(t)

	firstEncoded, err := block.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(first) error = %v", err)
	}
	secondEncoded, err := block.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(second) error = %v", err)
	}
	if !bytes.Equal(firstEncoded, secondEncoded) {
		t.Fatal("MarshalBinary() returned different bytes for same block")
	}
}
func TestBlockMarshalExcludesRuntimeMeta(t *testing.T) {
	block := newTestBlock(t)
	encoded, err := block.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	blockHash, err := block.Hash()
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	block.Meta = BlockMeta{
		Status:            BlockStatusFinalized,
		TotalFees:         9999,
		ComputeUnitsUsed:  8888,
		ReceivedTimeUnix:  1710000099,
		FinalizedTimeUnix: 1710000100,
	}
	nextEncoded, err := block.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(changed meta) error = %v", err)
	}
	nextBlockHash, err := block.Hash()
	if err != nil {
		t.Fatalf("Hash(changed meta) error = %v", err)
	}

	if !bytes.Equal(encoded, nextEncoded) {
		t.Fatal("MarshalBinary() changed after runtime meta mutation")
	}
	if blockHash != nextBlockHash {
		t.Fatal("Hash() changed after runtime meta mutation")
	}
}

func TestBlockToConfirmedBlockView(t *testing.T) {
	block := newTestBlock(t)

	confirmedBlock, err := block.ToConfirmedBlockView()
	if err != nil {
		t.Fatalf("ToConfirmedBlockView() error = %v", err)
	}
	if confirmedBlock.Blockhash != block.Header.Blockhash.String() {
		t.Fatalf("Blockhash = %q, want %q", confirmedBlock.Blockhash, block.Header.Blockhash.String())
	}
	if confirmedBlock.PreviousBlockhash != block.Header.PreviousBlockhash.String() {
		t.Fatalf("PreviousBlockhash = %q, want %q", confirmedBlock.PreviousBlockhash, block.Header.PreviousBlockhash.String())
	}
	if confirmedBlock.ParentSlot != block.Header.ParentSlot {
		t.Fatalf("ParentSlot = %d, want %d", confirmedBlock.ParentSlot, block.Header.ParentSlot)
	}
	if confirmedBlock.BlockHeight == nil || *confirmedBlock.BlockHeight != block.Header.BlockHeight {
		t.Fatalf("BlockHeight = %v, want %d", confirmedBlock.BlockHeight, block.Header.BlockHeight)
	}
	if len(confirmedBlock.Transactions) != len(block.Transactions) {
		t.Fatalf("Transactions length = %d, want %d", len(confirmedBlock.Transactions), len(block.Transactions))
	}
	if len(confirmedBlock.Signatures) != len(block.Transactions) {
		t.Fatalf("Signatures length = %d, want %d", len(confirmedBlock.Signatures), len(block.Transactions))
	}
}

func TestBlockHeaderMarshalGoldenLayout(t *testing.T) {
	block := newTestBlock(t)
	encoded, err := block.Header.MarshalBinary()
	if err != nil {
		t.Fatalf("Header.MarshalBinary() error = %v", err)
	}

	const headerSize = 326
	if len(encoded) != headerSize {
		t.Fatalf("Header encoded length = %d, want %d", len(encoded), headerSize)
	}
	assertLittleEndianUint16(t, encoded[0:2], block.Header.Version)
	assertLittleEndianUint64(t, encoded[2:10], block.Header.Slot)
	assertLittleEndianUint64(t, encoded[10:18], block.Header.ParentSlot)
	assertLittleEndianUint64(t, encoded[18:26], block.Header.BlockHeight)
	assertBytes(t, encoded[26:58], block.Header.ParentHash[:])
	assertBytes(t, encoded[58:90], block.Header.PreviousBlockhash[:])
	assertBytes(t, encoded[90:122], block.Header.Blockhash[:])
	assertBytes(t, encoded[122:154], block.Header.TransactionsRoot[:])
	assertBytes(t, encoded[154:186], block.Header.AccountsHash[:])
	assertBytes(t, encoded[186:218], block.Header.StateRoot[:])
	assertBytes(t, encoded[218:250], block.Header.RewardsHash[:])
	assertBytes(t, encoded[250:282], block.Header.EntriesHash[:])
	assertLittleEndianUint64(t, encoded[282:290], uint64(block.Header.TimestampUnix))
	assertBytes(t, encoded[290:322], block.Header.Leader[:])
	assertLittleEndianUint32(t, encoded[322:326], block.Header.TransactionCount)
}
func TestBlockMarshalPrefixesHeaderAndTransactionCount(t *testing.T) {
	block := newTestBlock(t)
	headerEncoded, err := block.Header.MarshalBinary()
	if err != nil {
		t.Fatalf("Header.MarshalBinary() error = %v", err)
	}
	blockEncoded, err := block.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}

	if !bytes.Equal(blockEncoded[:len(headerEncoded)], headerEncoded) {
		t.Fatal("MarshalBinary() does not start with header bytes")
	}
	if blockEncoded[len(headerEncoded)] != byte(len(block.Transactions)) {
		t.Fatalf("encoded transaction count = %d, want %d", blockEncoded[len(headerEncoded)], len(block.Transactions))
	}
	if len(blockEncoded) <= len(headerEncoded)+1 {
		t.Fatal("MarshalBinary() did not append transaction bytes")
	}
}
func TestBlockRejectsTransactionCountMismatch(t *testing.T) {
	block := newTestBlock(t)
	block.Header.TransactionCount = uint32(len(block.Transactions) + 1)

	err := block.Validate()
	if !errors.Is(err, ErrInvalidBlockHeader) {
		t.Fatalf("Validate() error = %v, want ErrInvalidBlockHeader", err)
	}
}
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

func assertLittleEndianUint16(t *testing.T, encoded []byte, expected uint16) {
	t.Helper()
	if binary.LittleEndian.Uint16(encoded) != expected {
		t.Fatalf("uint16 = %d, want %d", binary.LittleEndian.Uint16(encoded), expected)
	}
}

func assertLittleEndianUint32(t *testing.T, encoded []byte, expected uint32) {
	t.Helper()
	if binary.LittleEndian.Uint32(encoded) != expected {
		t.Fatalf("uint32 = %d, want %d", binary.LittleEndian.Uint32(encoded), expected)
	}
}

func assertLittleEndianUint64(t *testing.T, encoded []byte, expected uint64) {
	t.Helper()
	if binary.LittleEndian.Uint64(encoded) != expected {
		t.Fatalf("uint64 = %d, want %d", binary.LittleEndian.Uint64(encoded), expected)
	}
}

func assertBytes(t *testing.T, encoded []byte, expected []byte) {
	t.Helper()
	if !bytes.Equal(encoded, expected) {
		t.Fatalf("bytes = %v, want %v", encoded, expected)
	}
}
