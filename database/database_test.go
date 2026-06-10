package database

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

// TestDatabaseContractAcrossEngines 验证目标行为 + 保证核心场景和边界条件稳定。
func TestDatabaseContractAcrossEngines(t *testing.T) {
	cases := []struct {
		name   string
		engine EngineType
	}{
		{name: "pebble", engine: EnginePebble},
		{name: "leveldb", engine: EngineLevelDB},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			db, err := NewDatabase(DatabaseConfig{Path: t.TempDir(), Engine: testCase.engine})
			if err != nil {
				t.Fatalf("NewDatabase() error = %v", err)
			}
			defer db.Close()

			runDatabaseContract(t, db)
			runDatabaseExtendedContract(t, db)
		})
	}
}
func runDatabaseContract(t *testing.T, db Database) {
	t.Helper()

	if err := db.Insert(TableTest1, []byte("k1"), []byte("v1")); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if err := db.Insert(TableTest1, []byte("prefix:k2"), []byte("v2")); err != nil {
		t.Fatalf("Insert(prefix) error = %v", err)
	}

	value, err := db.Get(TableTest1, []byte("k1"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !bytes.Equal(value, []byte("v1")) {
		t.Fatalf("Get() = %q, want v1", value)
	}

	pairs, err := db.PrefixQuery(TableTest1, []byte("prefix:"))
	if err != nil {
		t.Fatalf("PrefixQuery() error = %v", err)
	}
	if len(pairs) != 1 || !bytes.Equal(pairs[0].Key, []byte("prefix:k2")) {
		t.Fatalf("PrefixQuery() = %+v, want prefix:k2", pairs)
	}

	if err := db.DataTransaction([]DBOperation{
		NewUpdateOperation(TableTest1, []byte("k1"), []byte("v1-updated")),
		NewDeleteOperation(TableTest1, []byte("prefix:k2")),
	}); err != nil {
		t.Fatalf("DataTransaction() error = %v", err)
	}

	value, err = db.Get(TableTest1, []byte("k1"))
	if err != nil {
		t.Fatalf("Get(updated) error = %v", err)
	}
	if !bytes.Equal(value, []byte("v1-updated")) {
		t.Fatalf("Get(updated) = %q, want v1-updated", value)
	}

	exists, err := db.Exists(TableTest1, []byte("prefix:k2"))
	if err != nil {
		t.Fatalf("Exists(deleted) error = %v", err)
	}
	if exists {
		t.Fatal("Exists(deleted) = true, want false")
	}
}
func runDatabaseExtendedContract(t *testing.T, db Database) {
	t.Helper()

	testNumericKeyMethods(t, db)
	testKeyValueCollectionMethods(t, db)
	testCacheAndCloneSafety(t, db)
	testManualTransactionRollback(t, db)
	testReadTransactionSnapshotConsistency(t, db)
	testValidationAndBoundaryCases(t, db)
	testConcurrentAccess(t, db)
}

// testNumericKeyMethods 验证目标行为 + 保证核心场景和边界条件稳定。
func testNumericKeyMethods(t *testing.T, db Database) {
	t.Helper()

	if err := db.PutInt(TableHeightToHash, 7, []byte("hash-7")); err != nil {
		t.Fatalf("PutInt() error = %v", err)
	}
	value, err := db.GetInt(TableHeightToHash, 7)
	if err != nil {
		t.Fatalf("GetInt() error = %v", err)
	}
	if !bytes.Equal(value, []byte("hash-7")) {
		t.Fatalf("GetInt() = %q, want hash-7", value)
	}

	if err := db.UpdateInt64(TableHashToHeight, 1001, []byte("height-1001")); err != nil {
		t.Fatalf("UpdateInt64() error = %v", err)
	}
	exists, err := db.ExistsInt64(TableHashToHeight, 1001)
	if err != nil {
		t.Fatalf("ExistsInt64() error = %v", err)
	}
	if !exists {
		t.Fatal("ExistsInt64() = false, want true")
	}
	if err := db.DeleteInt64(TableHashToHeight, 1001); err != nil {
		t.Fatalf("DeleteInt64() error = %v", err)
	}
	exists, err = db.ExistsInt64(TableHashToHeight, 1001)
	if err != nil {
		t.Fatalf("ExistsInt64(after delete) error = %v", err)
	}
	if exists {
		t.Fatal("ExistsInt64(after delete) = true, want false")
	}
}

// testKeyValueCollectionMethods 验证目标行为 + 保证核心场景和边界条件稳定。
func testKeyValueCollectionMethods(t *testing.T, db Database) {
	t.Helper()

	if err := db.ClearTable(TableAddrToTx); err != nil {
		t.Fatalf("ClearTable() setup error = %v", err)
	}
	if err := db.BatchInsert(
		TableAddrToTx,
		[][]byte{[]byte("addr:a:1"), []byte("addr:a:2"), []byte("addr:b:1")},
		[][]byte{[]byte("tx-a1"), []byte("tx-a2"), []byte("tx-b1")},
	); err != nil {
		t.Fatalf("BatchInsert(collection) error = %v", err)
	}

	keys, err := db.KeysByPrefix(TableAddrToTx, []byte("addr:a:"))
	if err != nil {
		t.Fatalf("KeysByPrefix() error = %v", err)
	}
	assertByteSlices(t, keys, [][]byte{[]byte("addr:a:1"), []byte("addr:a:2")})

	values, err := db.ValuesByPrefix(TableAddrToTx, []byte("addr:a:"))
	if err != nil {
		t.Fatalf("ValuesByPrefix() error = %v", err)
	}
	assertByteSlices(t, values, [][]byte{[]byte("tx-a1"), []byte("tx-a2")})

	allKeys, err := db.Keys(TableAddrToTx)
	if err != nil {
		t.Fatalf("Keys() error = %v", err)
	}
	if len(allKeys) != 3 {
		t.Fatalf("len(Keys()) = %d, want 3", len(allKeys))
	}
	allValues, err := db.Values(TableAddrToTx)
	if err != nil {
		t.Fatalf("Values() error = %v", err)
	}
	if len(allValues) != 3 {
		t.Fatalf("len(Values()) = %d, want 3", len(allValues))
	}
}

// testCacheAndCloneSafety 验证目标行为 + 保证核心场景和边界条件稳定。
func testCacheAndCloneSafety(t *testing.T, db Database) {
	t.Helper()

	if err := db.SetCachePolicy(TablePeer, 60_000, 2); err != nil {
		t.Fatalf("SetCachePolicy() error = %v", err)
	}
	if err := db.Put(TablePeer, []byte("clone"), []byte("safe")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	value, err := db.Get(TablePeer, []byte("clone"))
	if err != nil {
		t.Fatalf("Get(cache) error = %v", err)
	}
	value[0] = 'x'

	value, err = db.Get(TablePeer, []byte("clone"))
	if err != nil {
		t.Fatalf("Get(cache clone) error = %v", err)
	}
	if !bytes.Equal(value, []byte("safe")) {
		t.Fatalf("cached Get() = %q, want safe", value)
	}
	if err := db.ClearCache(TablePeer); err != nil {
		t.Fatalf("ClearCache() error = %v", err)
	}
	if err := db.SetCachePolicy(TablePeer, 0, 0); err != nil {
		t.Fatalf("SetCachePolicy(disable) error = %v", err)
	}
}

// testManualTransactionRollback 验证目标行为 + 保证核心场景和边界条件稳定。
func testManualTransactionRollback(t *testing.T, db Database) {
	t.Helper()

	transactionID, err := db.BeginTransaction()
	if err != nil {
		t.Fatalf("BeginTransaction() error = %v", err)
	}
	if err := db.AddToTransaction(transactionID, NewInsertOperation(TableOrphan, []byte("rollback"), []byte("value"))); err != nil {
		t.Fatalf("AddToTransaction() error = %v", err)
	}
	if err := db.RollbackTransaction(transactionID); err != nil {
		t.Fatalf("RollbackTransaction() error = %v", err)
	}
	exists, err := db.Exists(TableOrphan, []byte("rollback"))
	if err != nil {
		t.Fatalf("Exists(rollback) error = %v", err)
	}
	if exists {
		t.Fatal("rolled back key exists, want absent")
	}
}

// testReadTransactionSnapshotConsistency 验证目标行为 + 保证核心场景和边界条件稳定。
func testReadTransactionSnapshotConsistency(t *testing.T, db Database) {
	t.Helper()

	if err := db.ClearTable(TableAccount); err != nil {
		t.Fatalf("ClearTable(read tx setup) error = %v", err)
	}
	if err := db.BatchInsert(
		TableAccount,
		[][]byte{[]byte("acct:1"), []byte("acct:2")},
		[][]byte{[]byte("old-1"), []byte("old-2")},
	); err != nil {
		t.Fatalf("BatchInsert(read tx setup) error = %v", err)
	}

	readTx, err := db.BeginReadTransaction()
	if err != nil {
		t.Fatalf("BeginReadTransaction() error = %v", err)
	}
	defer readTx.Close()

	if err := db.DataTransaction([]DBOperation{
		NewUpdateOperation(TableAccount, []byte("acct:1"), []byte("new-1")),
		NewInsertOperation(TableAccount, []byte("acct:3"), []byte("new-3")),
	}); err != nil {
		t.Fatalf("DataTransaction(read tx update) error = %v", err)
	}

	value, err := readTx.Get(TableAccount, []byte("acct:1"))
	if err != nil {
		t.Fatalf("readTx.Get(old) error = %v", err)
	}
	if !bytes.Equal(value, []byte("old-1")) {
		t.Fatalf("readTx.Get(old) = %q, want old-1", value)
	}
	value[0] = 'x'

	value, err = readTx.Get(TableAccount, []byte("acct:1"))
	if err != nil {
		t.Fatalf("readTx.Get(clone) error = %v", err)
	}
	if !bytes.Equal(value, []byte("old-1")) {
		t.Fatalf("readTx.Get(clone) = %q, want old-1", value)
	}

	exists, err := readTx.Exists(TableAccount, []byte("acct:3"))
	if err != nil {
		t.Fatalf("readTx.Exists(new) error = %v", err)
	}
	if exists {
		t.Fatal("readTx.Exists(new) = true, want false")
	}

	pairs, err := readTx.PrefixQuery(TableAccount, []byte("acct:"))
	if err != nil {
		t.Fatalf("readTx.PrefixQuery() error = %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("len(readTx.PrefixQuery()) = %d, want 2", len(pairs))
	}

	count, err := readTx.Count(TableAccount)
	if err != nil {
		t.Fatalf("readTx.Count() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("readTx.Count() = %d, want 2", count)
	}

	values, err := readTx.BatchGet(TableAccount, [][]byte{[]byte("acct:1"), []byte("acct:2")})
	if err != nil {
		t.Fatalf("readTx.BatchGet() error = %v", err)
	}
	assertByteSlices(t, values, [][]byte{[]byte("old-1"), []byte("old-2")})

	if err := readTx.Close(); err != nil {
		t.Fatalf("readTx.Close() error = %v", err)
	}
	if _, err := readTx.Get(TableAccount, []byte("acct:1")); err == nil {
		t.Fatal("readTx.Get(after close) error = nil, want closed error")
	}

	value, err = db.Get(TableAccount, []byte("acct:1"))
	if err != nil {
		t.Fatalf("db.Get(new) error = %v", err)
	}
	if !bytes.Equal(value, []byte("new-1")) {
		t.Fatalf("db.Get(new) = %q, want new-1", value)
	}
}

// testValidationAndBoundaryCases 验证目标行为 + 保证核心场景和边界条件稳定。
func testValidationAndBoundaryCases(t *testing.T, db Database) {
	t.Helper()

	if err := db.Insert(TableAll, []byte("bad"), []byte("bad")); err == nil {
		t.Fatal("Insert(TableAll) error = nil, want validation error")
	}
	if _, err := db.Get(Table(65535), []byte("bad")); err == nil {
		t.Fatal("Get(unknown table) error = nil, want validation error")
	}
	if err := db.BatchInsert(TablePeer, [][]byte{[]byte("k")}, nil); err == nil {
		t.Fatal("BatchInsert(length mismatch) error = nil, want validation error")
	}
	if err := db.Iterate(TablePeer, nil); err == nil {
		t.Fatal("Iterate(nil handler) error = nil, want validation error")
	}
	if err := db.SetCachePolicy(TablePeer, -1, 1); err == nil {
		t.Fatal("SetCachePolicy(negative ttl) error = nil, want validation error")
	}
	if _, err := db.PrefixQueryWithLimit(TablePeer, []byte("none"), 0); err != nil {
		t.Fatalf("PrefixQueryWithLimit(limit 0) error = %v", err)
	}
	if _, err := db.Page(TablePeer, 0, nil); err != nil {
		t.Fatalf("Page(size 0) error = %v", err)
	}

	transactionID, err := db.BeginTransaction()
	if err != nil {
		t.Fatalf("BeginTransaction(validation) error = %v", err)
	}
	if err := db.AddToTransaction(transactionID, DBOperation{Table: TablePeer, Key: []byte("bad"), Type: OperationType(99)}); err == nil {
		t.Fatal("AddToTransaction(bad operation) error = nil, want validation error")
	}
	if err := db.RollbackTransaction(transactionID); err != nil {
		t.Fatalf("RollbackTransaction(validation) error = %v", err)
	}
	if err := db.CommitTransaction(transactionID); err == nil {
		t.Fatal("CommitTransaction(rolled back id) error = nil, want not found")
	}
}

// testConcurrentAccess 验证目标行为 + 保证核心场景和边界条件稳定。
func testConcurrentAccess(t *testing.T, db Database) {
	t.Helper()

	const workers = 8
	const writesPerWorker = 16

	var waitGroup sync.WaitGroup
	errCh := make(chan error, workers*writesPerWorker)
	for workerID := 0; workerID < workers; workerID++ {
		waitGroup.Add(1)
		go func(workerID int) {
			defer waitGroup.Done()
			for index := 0; index < writesPerWorker; index++ {
				key := []byte{byte(workerID), byte(index)}
				if err := db.Put(TableCheckpoint, key, []byte("ok")); err != nil {
					errCh <- err
					return
				}
				if _, err := db.Get(TableCheckpoint, key); err != nil {
					errCh <- err
					return
				}
			}
		}(workerID)
	}
	waitGroup.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent access error = %v", err)
		}
	}
	count, err := db.Count(TableCheckpoint)
	if err != nil {
		t.Fatalf("Count(concurrent) error = %v", err)
	}
	if count < workers*writesPerWorker {
		t.Fatalf("Count(concurrent) = %d, want at least %d", count, workers*writesPerWorker)
	}
}
func assertByteSlices(t *testing.T, got [][]byte, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if !bytes.Equal(got[index], want[index]) {
			t.Fatalf("got[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}

// TestDatabaseOperationsRequireOpenDatabase 验证目标行为 + 保证核心场景和边界条件稳定。
func TestDatabaseOperationsRequireOpenDatabase(t *testing.T) {
	db := NewDatabaseImpl()
	if _, err := db.Get(TablePeer, []byte("closed")); !errors.Is(err, ErrDatabaseNotOpen) {
		t.Fatalf("Get() error = %v, want ErrDatabaseNotOpen", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
