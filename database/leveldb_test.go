package database

import (
	"bytes"
	"errors"
	"testing"
)

func TestLevelDBDatabaseCRUDPageAndRange(t *testing.T) {
	db := NewLevelDBDatabase()
	if err := db.CreateDatabase(DatabaseConfig{Path: t.TempDir()}); err != nil {
		t.Fatalf("CreateDatabase(leveldb) error = %v", err)
	}
	defer db.Close()

	if err := db.Insert(TablePeer, []byte("a"), []byte("1")); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if err := db.BatchInsert(
		TablePeer,
		[][]byte{[]byte("b"), []byte("c"), []byte("p:1"), []byte("p:2")},
		[][]byte{[]byte("2"), []byte("3"), []byte("prefix-1"), []byte("prefix-2")},
	); err != nil {
		t.Fatalf("BatchInsert() error = %v", err)
	}

	value, err := db.Get(TablePeer, []byte("a"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !bytes.Equal(value, []byte("1")) {
		t.Fatalf("Get() = %q, want %q", value, []byte("1"))
	}

	page, err := db.PageKey(TablePeer, 2, nil)
	if err != nil {
		t.Fatalf("PageKey() error = %v", err)
	}
	if len(page.Data) != 2 || !bytes.Equal(page.Data[0], []byte("a")) || !bytes.Equal(page.Data[1], []byte("b")) {
		t.Fatalf("PageKey() data = %q, want [a b]", page.Data)
	}

	reverse, err := db.PageKeyByPrefixReverse(TablePeer, []byte("p:"), 2, nil)
	if err != nil {
		t.Fatalf("PageKeyByPrefixReverse() error = %v", err)
	}
	if len(reverse.Data) != 2 || !bytes.Equal(reverse.Data[0], []byte("p:2")) || !bytes.Equal(reverse.Data[1], []byte("p:1")) {
		t.Fatalf("reverse prefix page = %q, want [p:2 p:1]", reverse.Data)
	}

	pairs, err := db.RangeQueryWithLimit(TablePeer, []byte("a"), []byte("p:2"), 3)
	if err != nil {
		t.Fatalf("RangeQueryWithLimit() error = %v", err)
	}
	if len(pairs) != 3 || !bytes.Equal(pairs[0].Key, []byte("a")) || !bytes.Equal(pairs[2].Key, []byte("c")) {
		t.Fatalf("range pairs = %+v, want keys [a b c]", pairs)
	}
}
func TestLevelDBDatabaseTransactionsAndUnsupportedCheckpoint(t *testing.T) {
	db, err := NewDatabase(DatabaseConfig{Path: t.TempDir(), Engine: EngineLevelDB})
	if err != nil {
		t.Fatalf("NewDatabase(leveldb) error = %v", err)
	}
	defer db.Close()

	if err := db.DataTransaction([]DBOperation{
		NewInsertOperation(TableAccount, []byte("alice"), []byte("10")),
		NewInsertOperation(TableAccount, []byte("bob"), []byte("20")),
		NewUpdateOperation(TableAccount, []byte("alice"), []byte("15")),
		NewDeleteOperation(TableAccount, []byte("bob")),
	}); err != nil {
		t.Fatalf("DataTransaction() error = %v", err)
	}

	alice, err := db.Get(TableAccount, []byte("alice"))
	if err != nil {
		t.Fatalf("Get(alice) error = %v", err)
	}
	if !bytes.Equal(alice, []byte("15")) {
		t.Fatalf("alice = %q, want 15", alice)
	}

	exists, err := db.Exists(TableAccount, []byte("bob"))
	if err != nil {
		t.Fatalf("Exists(bob) error = %v", err)
	}
	if exists {
		t.Fatal("bob exists after delete operation")
	}

	if err := db.Checkpoint(t.TempDir()); !errors.Is(err, ErrFeatureNotSupported) {
		t.Fatalf("Checkpoint() error = %v, want ErrFeatureNotSupported", err)
	}
}
func TestNewDatabaseUsesPebbleByDefault(t *testing.T) {
	db, err := NewDatabase(DatabaseConfig{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("NewDatabase(default) error = %v", err)
	}
	defer db.Close()

	if err := db.CheckHealth(); err != nil {
		t.Fatalf("CheckHealth() error = %v", err)
	}
}
