package database

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestPebbleDatabaseCRUDPageAndRange(t *testing.T) {
	db := NewPebbleDatabase()
	if err := db.CreateDatabase(DatabaseConfig{Path: t.TempDir()}); err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
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

	exists, err := db.Exists(TablePeer, []byte("a"))
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if !exists {
		t.Fatal("Exists() = false, want true")
	}

	if err := db.Update(TablePeer, []byte("a"), []byte("updated")); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	value, err = db.Get(TablePeer, []byte("a"))
	if err != nil {
		t.Fatalf("Get() after Update error = %v", err)
	}
	if !bytes.Equal(value, []byte("updated")) {
		t.Fatalf("updated Get() = %q, want %q", value, []byte("updated"))
	}

	count, err := db.Count(TablePeer)
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if count != 5 {
		t.Fatalf("Count() = %d, want 5", count)
	}

	page, err := db.PageKey(TablePeer, 2, nil)
	if err != nil {
		t.Fatalf("PageKey() error = %v", err)
	}
	if len(page.Data) != 2 || !bytes.Equal(page.Data[0], []byte("a")) || !bytes.Equal(page.Data[1], []byte("b")) {
		t.Fatalf("PageKey() data = %q, want [a b]", page.Data)
	}
	if page.IsLastPage {
		t.Fatal("first page marked as last page")
	}

	nextPage, err := db.PageKey(TablePeer, 10, page.LastKey)
	if err != nil {
		t.Fatalf("PageKey() next error = %v", err)
	}
	if len(nextPage.Data) != 3 || !nextPage.IsLastPage {
		t.Fatalf("next page = %+v, want 3 items and last page", nextPage)
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
func TestPebbleDatabaseTransactionsAndDeleteRange(t *testing.T) {
	db := NewPebbleDatabase()
	if err := db.CreateDatabase(DatabaseConfig{Path: t.TempDir()}); err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	defer db.Close()

	txID, err := db.BeginTransaction()
	if err != nil {
		t.Fatalf("BeginTransaction() error = %v", err)
	}
	if err := db.AddToTransaction(txID, DBOperation{Table: TableChain, Key: []byte("k1"), Value: []byte("v1"), Type: OperationInsert}); err != nil {
		t.Fatalf("AddToTransaction(insert) error = %v", err)
	}
	if err := db.AddToTransaction(txID, DBOperation{Table: TableChain, Key: []byte("k2"), Value: []byte("v2"), Type: OperationInsert}); err != nil {
		t.Fatalf("AddToTransaction(insert) error = %v", err)
	}
	if err := db.CommitTransaction(txID); err != nil {
		t.Fatalf("CommitTransaction() error = %v", err)
	}

	values, err := db.BatchGet(TableChain, [][]byte{[]byte("k1"), []byte("k2")})
	if err != nil {
		t.Fatalf("BatchGet() error = %v", err)
	}
	if !bytes.Equal(values[0], []byte("v1")) || !bytes.Equal(values[1], []byte("v2")) {
		t.Fatalf("BatchGet() = %q, want [v1 v2]", values)
	}

	if err := db.DataTransaction([]DBOperation{
		{Table: TableChain, Key: []byte("k2"), Value: []byte("updated"), Type: OperationUpdate},
		{Table: TableChain, Key: []byte("k3"), Value: []byte("v3"), Type: OperationInsert},
	}); err != nil {
		t.Fatalf("DataTransaction() error = %v", err)
	}

	if err := db.BatchDeleteRange(TableChain, []byte("k1"), []byte("k3")); err != nil {
		t.Fatalf("BatchDeleteRange() error = %v", err)
	}

	exists, err := db.Exists(TableChain, []byte("k2"))
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if exists {
		t.Fatal("k2 exists after delete range, want deleted")
	}

	exists, err = db.Exists(TableChain, []byte("k3"))
	if err != nil {
		t.Fatalf("Exists(k3) error = %v", err)
	}
	if !exists {
		t.Fatal("k3 deleted by half-open range, want existing")
	}
}
func TestPebbleDatabaseDataTransactionOperations(t *testing.T) {
	db := NewPebbleDatabase()
	if err := db.CreateDatabase(DatabaseConfig{Path: t.TempDir()}); err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	defer db.Close()

	operations := []DBOperation{
		NewInsertOperation(TableAccount, []byte("alice"), []byte("10")),
		NewInsertOperation(TableAccount, []byte("bob"), []byte("20")),
		NewUpdateOperation(TableAccount, []byte("alice"), []byte("15")),
		NewDeleteOperation(TableAccount, []byte("bob")),
	}
	if err := db.DataTransaction(operations); err != nil {
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
}
func TestPebbleDatabasePrefixQueryAndReverse(t *testing.T) {
	db := NewPebbleDatabase()
	if err := db.CreateDatabase(DatabaseConfig{Path: t.TempDir()}); err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	defer db.Close()

	if err := db.BatchInsert(
		TableAddrToTx,
		[][]byte{
			[]byte("addr:001"),
			[]byte("addr:002"),
			[]byte("addr:010"),
			[]byte("other:001"),
		},
		[][]byte{
			[]byte("tx-001"),
			[]byte("tx-002"),
			[]byte("tx-010"),
			[]byte("skip"),
		},
	); err != nil {
		t.Fatalf("BatchInsert() error = %v", err)
	}

	forward, err := db.PrefixQuery(TableAddrToTx, []byte("addr:"))
	if err != nil {
		t.Fatalf("PrefixQuery() error = %v", err)
	}
	assertKeys(t, forward, [][]byte{[]byte("addr:001"), []byte("addr:002"), []byte("addr:010")})
	assertValues(t, forward, [][]byte{[]byte("tx-001"), []byte("tx-002"), []byte("tx-010")})

	reverse, err := db.PrefixQueryReverse(TableAddrToTx, []byte("addr:"))
	if err != nil {
		t.Fatalf("PrefixQueryReverse() error = %v", err)
	}
	assertKeys(t, reverse, [][]byte{[]byte("addr:010"), []byte("addr:002"), []byte("addr:001")})
	assertValues(t, reverse, [][]byte{[]byte("tx-010"), []byte("tx-002"), []byte("tx-001")})

	limited, err := db.PrefixQueryReverseWithLimit(TableAddrToTx, []byte("addr:"), 2)
	if err != nil {
		t.Fatalf("PrefixQueryReverseWithLimit() error = %v", err)
	}
	assertKeys(t, limited, [][]byte{[]byte("addr:010"), []byte("addr:002")})
}
func TestPebbleDatabaseBlockchainCommonHelpers(t *testing.T) {
	db := NewPebbleDatabase()
	if err := db.CreateDatabase(DatabaseConfig{Path: t.TempDir()}); err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	defer db.Close()

	empty, err := db.IsEmpty(TableHeightToHash)
	if err != nil {
		t.Fatalf("IsEmpty() error = %v", err)
	}
	if !empty {
		t.Fatal("new table IsEmpty() = false, want true")
	}

	if err := db.BatchInsert(
		TableHeightToHash,
		[][]byte{heightKey(1), heightKey(2), heightKey(3)},
		[][]byte{[]byte("hash-1"), []byte("hash-2"), []byte("hash-3")},
	); err != nil {
		t.Fatalf("BatchInsert(height) error = %v", err)
	}

	first, err := db.First(TableHeightToHash)
	if err != nil {
		t.Fatalf("First() error = %v", err)
	}
	if first == nil || !bytes.Equal(first.Key, heightKey(1)) || !bytes.Equal(first.Value, []byte("hash-1")) {
		t.Fatalf("First() = %+v, want height 1", first)
	}

	last, err := db.Last(TableHeightToHash)
	if err != nil {
		t.Fatalf("Last() error = %v", err)
	}
	if last == nil || !bytes.Equal(last.Key, heightKey(3)) || !bytes.Equal(last.Value, []byte("hash-3")) {
		t.Fatalf("Last() = %+v, want height 3", last)
	}

	reverseRange, err := db.RangeQueryReverseWithLimit(TableHeightToHash, heightKey(1), heightKey(4), 2)
	if err != nil {
		t.Fatalf("RangeQueryReverseWithLimit() error = %v", err)
	}
	assertKeys(t, reverseRange, [][]byte{heightKey(3), heightKey(2)})

	if err := db.BatchInsert(
		TableAddrToUTXO,
		[][]byte{[]byte("addr:a:1"), []byte("addr:a:2"), []byte("addr:b:1")},
		[][]byte{[]byte("utxo-a1"), []byte("utxo-a2"), []byte("utxo-b1")},
	); err != nil {
		t.Fatalf("BatchInsert(addr) error = %v", err)
	}

	count, err := db.CountByPrefix(TableAddrToUTXO, []byte("addr:a:"))
	if err != nil {
		t.Fatalf("CountByPrefix() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("CountByPrefix() = %d, want 2", count)
	}

	firstByPrefix, err := db.FirstByPrefix(TableAddrToUTXO, []byte("addr:a:"))
	if err != nil {
		t.Fatalf("FirstByPrefix() error = %v", err)
	}
	if firstByPrefix == nil || !bytes.Equal(firstByPrefix.Key, []byte("addr:a:1")) {
		t.Fatalf("FirstByPrefix() = %+v, want addr:a:1", firstByPrefix)
	}

	lastByPrefix, err := db.LastByPrefix(TableAddrToUTXO, []byte("addr:a:"))
	if err != nil {
		t.Fatalf("LastByPrefix() error = %v", err)
	}
	if lastByPrefix == nil || !bytes.Equal(lastByPrefix.Key, []byte("addr:a:2")) {
		t.Fatalf("LastByPrefix() = %+v, want addr:a:2", lastByPrefix)
	}

	if err := db.DeleteByPrefix(TableAddrToUTXO, []byte("addr:a:")); err != nil {
		t.Fatalf("DeleteByPrefix() error = %v", err)
	}
	count, err = db.CountByPrefix(TableAddrToUTXO, []byte("addr:a:"))
	if err != nil {
		t.Fatalf("CountByPrefix(after delete) error = %v", err)
	}
	if count != 0 {
		t.Fatalf("CountByPrefix(after delete) = %d, want 0", count)
	}

	if err := db.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	checkpointDir := filepath.Join(t.TempDir(), "checkpoint")
	if err := db.Checkpoint(checkpointDir); err != nil {
		t.Fatalf("Checkpoint() error = %v", err)
	}
	if info, err := os.Stat(checkpointDir); err != nil || !info.IsDir() {
		t.Fatalf("checkpoint dir stat = (%v, %v), want directory", info, err)
	}
}
func assertKeys(t *testing.T, pairs []KeyValue, want [][]byte) {
	t.Helper()
	if len(pairs) != len(want) {
		t.Fatalf("len(pairs) = %d, want %d", len(pairs), len(want))
	}
	for i := range want {
		if !bytes.Equal(pairs[i].Key, want[i]) {
			t.Fatalf("pairs[%d].Key = %q, want %q", i, pairs[i].Key, want[i])
		}
	}
}
func heightKey(height uint64) []byte {
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, height)
	return key
}
func assertValues(t *testing.T, pairs []KeyValue, want [][]byte) {
	t.Helper()
	if len(pairs) != len(want) {
		t.Fatalf("len(pairs) = %d, want %d", len(pairs), len(want))
	}
	for i := range want {
		if !bytes.Equal(pairs[i].Value, want[i]) {
			t.Fatalf("pairs[%d].Value = %q, want %q", i, pairs[i].Value, want[i])
		}
	}
}
