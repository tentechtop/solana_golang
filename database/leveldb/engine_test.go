package leveldb

import (
	"bytes"
	"testing"
)

// TestEngineCRUDBatchIteratorAndMaintenance 验证目标行为 + 保证核心场景和边界条件稳定。
func TestEngineCRUDBatchIteratorAndMaintenance(t *testing.T) {
	engine := NewEngine()
	if err := engine.Open(t.TempDir(), true); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()

	if err := engine.Set([]byte("a"), []byte("1")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	value, err := engine.Get([]byte("a"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !bytes.Equal(value, []byte("1")) {
		t.Fatalf("Get() = %q, want 1", value)
	}

	batch, err := engine.NewBatch()
	if err != nil {
		t.Fatalf("NewBatch() error = %v", err)
	}
	if err := batch.Set([]byte("b"), []byte("2")); err != nil {
		t.Fatalf("Batch.Set() error = %v", err)
	}
	if err := batch.Set([]byte("c"), []byte("3")); err != nil {
		t.Fatalf("Batch.Set(c) error = %v", err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("Batch.Commit() error = %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("Batch.Close() error = %v", err)
	}

	iter, err := engine.NewIterator([]byte("a"), []byte("d"))
	if err != nil {
		t.Fatalf("NewIterator() error = %v", err)
	}
	defer iter.Close()
	if !iter.First() || !bytes.Equal(iter.Key(), []byte("a")) {
		t.Fatalf("Iterator.First() key = %q, want a", iter.Key())
	}
	if !iter.Last() || !bytes.Equal(iter.Key(), []byte("c")) {
		t.Fatalf("Iterator.Last() key = %q, want c", iter.Key())
	}
	if !iter.SeekGE([]byte("b")) || !bytes.Equal(iter.Value(), []byte("2")) {
		t.Fatalf("Iterator.SeekGE(b) value = %q, want 2", iter.Value())
	}
	if !iter.SeekLT([]byte("c")) || !bytes.Equal(iter.Key(), []byte("b")) {
		t.Fatalf("Iterator.SeekLT(c) key = %q, want b", iter.Key())
	}
	if err := iter.Error(); err != nil {
		t.Fatalf("Iterator.Error() = %v", err)
	}

	if err := engine.DeleteRange([]byte("a"), []byte("c")); err != nil {
		t.Fatalf("DeleteRange() error = %v", err)
	}
	if _, err := engine.Get([]byte("b")); !engine.IsNotFound(err) {
		t.Fatalf("Get(deleted) error = %v, want not found", err)
	}
	if _, err := engine.Get([]byte("c")); err != nil {
		t.Fatalf("Get(range end) error = %v", err)
	}

	if err := engine.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if err := engine.Compact([]byte("a"), []byte("z")); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if err := engine.Checkpoint(t.TempDir()); err == nil {
		t.Fatal("Checkpoint() error = nil, want unsupported error")
	}
	if err := engine.CheckHealth(); err != nil {
		t.Fatalf("CheckHealth() error = %v", err)
	}
}

// TestEngineClosedOperationsReturnError 验证目标行为 + 保证核心场景和边界条件稳定。
func TestEngineClosedOperationsReturnError(t *testing.T) {
	engine := NewEngine()

	if err := engine.Set([]byte("a"), []byte("1")); err == nil {
		t.Fatal("Set() error = nil, want not open error")
	}
	if _, err := engine.Get([]byte("a")); err == nil {
		t.Fatal("Get() error = nil, want not open error")
	}
	if _, err := engine.NewBatch(); err == nil {
		t.Fatal("NewBatch() error = nil, want not open error")
	}
	if _, err := engine.NewIterator(nil, nil); err == nil {
		t.Fatal("NewIterator() error = nil, want not open error")
	}
	if _, err := engine.NewSnapshot(); err == nil {
		t.Fatal("NewSnapshot() error = nil, want not open error")
	}
}

// TestSnapshotKeepsStableReadView 验证目标行为 + 保证核心场景和边界条件稳定。
func TestSnapshotKeepsStableReadView(t *testing.T) {
	engine := NewEngine()
	if err := engine.Open(t.TempDir(), true); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()

	if err := engine.Set([]byte("a"), []byte("old")); err != nil {
		t.Fatalf("Set(old) error = %v", err)
	}
	snapshot, err := engine.NewSnapshot()
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}
	defer snapshot.Close()

	if err := engine.Set([]byte("a"), []byte("new")); err != nil {
		t.Fatalf("Set(new) error = %v", err)
	}
	value, err := snapshot.Get([]byte("a"))
	if err != nil {
		t.Fatalf("Snapshot.Get() error = %v", err)
	}
	if !bytes.Equal(value, []byte("old")) {
		t.Fatalf("Snapshot.Get() = %q, want old", value)
	}

	iter, err := snapshot.NewIterator([]byte("a"), []byte("b"))
	if err != nil {
		t.Fatalf("Snapshot.NewIterator() error = %v", err)
	}
	defer iter.Close()
	if !iter.First() || !bytes.Equal(iter.Value(), []byte("old")) {
		t.Fatalf("Snapshot iterator value = %q, want old", iter.Value())
	}
}
