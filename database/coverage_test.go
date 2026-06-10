package database

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// TestDatabaseMetadataAndKeyCodecEdges 验证元数据和 key 编码边界 + 保证表映射和范围计算稳定。
func TestDatabaseMetadataAndKeyCodecEdges(t *testing.T) {
	if Table(9999).ColumnFamilyName() != "" {
		t.Fatal("unknown table column family is not empty")
	}
	if TablePeer.String() != "peer" {
		t.Fatalf("TablePeer.String() = %q, want peer", TablePeer.String())
	}
	if table, ok := TableByCode(uint16(TablePeer)); !ok || table != TablePeer {
		t.Fatalf("TableByCode(TablePeer) = (%v, %v), want TablePeer true", table, ok)
	}
	if _, ok := TableByCode(9999); ok {
		t.Fatal("TableByCode(unknown) ok = true, want false")
	}
	if len(AllTableMetadata()) == 0 {
		t.Fatal("AllTableMetadata() is empty")
	}
	if tableUpperBound(Table(^uint16(0))) != nil {
		t.Fatal("tableUpperBound(max uint16) != nil")
	}
	if keySuccessor([]byte{0xff, 0xff}) != nil {
		t.Fatal("keySuccessor(all ff) != nil")
	}
	if got := stripTablePrefix([]byte{1}); len(got) != 0 {
		t.Fatalf("stripTablePrefix(short) = %x, want empty", got)
	}
}

// TestDatabaseCachePolicyAndRefresh 验证缓存策略和刷新路径 + 保证缓存淘汰、过期和刷新正确。
func TestDatabaseCachePolicyAndRefresh(t *testing.T) {
	db := newOpenTestDatabase(t, EnginePebble)
	defer db.Close()
	service := db.(*databaseService)

	if service.cacheEnabled(TablePeer) {
		t.Fatal("cacheEnabled(default) = true, want false")
	}
	if err := db.SetCachePolicy(TablePeer, 1, 1); err != nil {
		t.Fatalf("SetCachePolicy() error = %v", err)
	}
	if !service.cacheEnabled(TablePeer) {
		t.Fatal("cacheEnabled(enabled) = false, want true")
	}
	if err := db.Put(TablePeer, []byte("a"), []byte("1")); err != nil {
		t.Fatalf("Put(a) error = %v", err)
	}
	if err := db.Put(TablePeer, []byte("b"), []byte("2")); err != nil {
		t.Fatalf("Put(b) error = %v", err)
	}
	if len(service.caches[TablePeer].items) != 1 {
		t.Fatalf("cache size = %d, want 1", len(service.caches[TablePeer].items))
	}

	time.Sleep(2 * time.Millisecond)
	if _, ok := service.cacheGet(TablePeer, []byte("b")); ok {
		t.Fatal("cacheGet(expired) ok = true, want false")
	}
	if err := db.RefreshCache(TablePeer, []byte("a")); err != nil {
		t.Fatalf("RefreshCache(key) error = %v", err)
	}
	if _, ok := service.cacheGet(TablePeer, []byte("a")); !ok {
		t.Fatal("cacheGet(refreshed key) ok = false, want true")
	}
	if err := db.Delete(TablePeer, []byte("a")); err != nil {
		t.Fatalf("Delete(a) error = %v", err)
	}
	if err := db.RefreshCache(TablePeer, []byte("a")); err != nil {
		t.Fatalf("RefreshCache(missing key) error = %v", err)
	}
	if _, ok := service.cacheGet(TablePeer, []byte("a")); ok {
		t.Fatal("cacheGet(deleted key) ok = true, want false")
	}
	if err := db.RefreshCache(TablePeer, nil); err != nil {
		t.Fatalf("RefreshCache(table) error = %v", err)
	}
	if err := db.ClearCache(TableAll); err != nil {
		t.Fatalf("ClearCache(TableAll) error = %v", err)
	}
}

// TestDatabaseReadTransactionFullSurface 验证读事务完整读接口 + 保证快照读方法语义一致。
func TestDatabaseReadTransactionFullSurface(t *testing.T) {
	db := newOpenTestDatabase(t, EnginePebble)
	defer db.Close()

	if err := db.BatchInsert(TableBlock, [][]byte{[]byte("a"), []byte("b"), []byte("c")}, [][]byte{[]byte("1"), []byte("2"), []byte("3")}); err != nil {
		t.Fatalf("BatchInsert() error = %v", err)
	}
	readTx, err := db.BeginReadTransaction()
	if err != nil {
		t.Fatalf("BeginReadTransaction() error = %v", err)
	}
	defer readTx.Close()

	if count, err := readTx.CountByPrefix(TableBlock, []byte("")); err != nil || count != 3 {
		t.Fatalf("readTx.CountByPrefix() = (%d, %v), want 3 nil", count, err)
	}
	pairs, err := readTx.RangeQuery(TableBlock, []byte("a"), []byte("d"))
	if err != nil {
		t.Fatalf("readTx.RangeQuery() error = %v", err)
	}
	assertKeyValues(t, pairs, [][]byte{[]byte("a"), []byte("b"), []byte("c")}, [][]byte{[]byte("1"), []byte("2"), []byte("3")})

	limited, err := readTx.RangeQueryWithLimit(TableBlock, []byte("a"), []byte("d"), 2)
	if err != nil {
		t.Fatalf("readTx.RangeQueryWithLimit() error = %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("len(limited) = %d, want 2", len(limited))
	}
	first, err := readTx.First(TableBlock)
	if err != nil || first == nil || !bytes.Equal(first.Key, []byte("a")) {
		t.Fatalf("readTx.First() = (%+v, %v), want a nil", first, err)
	}
	last, err := readTx.Last(TableBlock)
	if err != nil || last == nil || !bytes.Equal(last.Key, []byte("c")) {
		t.Fatalf("readTx.Last() = (%+v, %v), want c nil", last, err)
	}
	if values, err := readTx.BatchGet(TableBlock, [][]byte{[]byte("a"), []byte("missing")}); err != nil || len(values) != 2 || values[1] != nil {
		t.Fatalf("readTx.BatchGet() = (%q, %v), want second nil", values, err)
	}
	if _, err := readTx.Get(TableAll, []byte("bad")); err == nil {
		t.Fatal("readTx.Get(TableAll) error = nil, want validation error")
	}
	if err := readTx.Close(); err != nil {
		t.Fatalf("readTx.Close() error = %v", err)
	}
	if err := readTx.Close(); err != nil {
		t.Fatalf("readTx.Close(second) error = %v", err)
	}
	if _, err := readTx.Count(TableBlock); err == nil {
		t.Fatal("readTx.Count(after close) error = nil, want closed error")
	}
}

// TestDatabaseOperationsAdditionalSurface 验证数据库补充操作面 + 覆盖分页、迭代、维护和 WAL 边界。
func TestDatabaseOperationsAdditionalSurface(t *testing.T) {
	db := newOpenTestDatabase(t, EnginePebble)
	defer db.Close()

	if err := db.BatchInsert(TableTxToBlock, [][]byte{[]byte("a"), []byte("b"), []byte("c")}, [][]byte{[]byte("1"), []byte("2"), []byte("3")}); err != nil {
		t.Fatalf("BatchInsert() error = %v", err)
	}
	if err := db.BatchUpdate(TableTxToBlock, [][]byte{[]byte("a"), []byte("b")}, [][]byte{[]byte("10"), []byte("20")}); err != nil {
		t.Fatalf("BatchUpdate() error = %v", err)
	}
	if err := db.BatchDelete(TableTxToBlock, [][]byte{[]byte("c")}); err != nil {
		t.Fatalf("BatchDelete() error = %v", err)
	}
	if exists, err := db.ExistsInt(TableTxToBlock, 1); err != nil || exists {
		t.Fatalf("ExistsInt(nonexistent) = (%v, %v), want false nil", exists, err)
	}
	if err := db.PutInt64(TableHashToHeight, 9, []byte("nine")); err != nil {
		t.Fatalf("PutInt64() error = %v", err)
	}
	if value, err := db.GetInt64(TableHashToHeight, 9); err != nil || !bytes.Equal(value, []byte("nine")) {
		t.Fatalf("GetInt64() = (%q, %v), want nine nil", value, err)
	}
	if err := db.UpdateInt(TableHeightToHash, 3, []byte("three")); err != nil {
		t.Fatalf("UpdateInt() error = %v", err)
	}
	if err := db.DeleteInt(TableHeightToHash, 3); err != nil {
		t.Fatalf("DeleteInt() error = %v", err)
	}

	page, err := db.PageByPrefix(TableTxToBlock, []byte(""), 2, nil)
	if err != nil || len(page.Data) != 2 {
		t.Fatalf("PageByPrefix() = (%+v, %v), want 2 items", page, err)
	}
	keyPage, err := db.PageKeyByPrefix(TableTxToBlock, []byte(""), 2, nil)
	if err != nil || len(keyPage.Data) != 2 {
		t.Fatalf("PageKeyByPrefix() = (%+v, %v), want 2 items", keyPage, err)
	}
	if exists, err := db.ExistsByPrefix(TableTxToBlock, []byte("a")); err != nil || !exists {
		t.Fatalf("ExistsByPrefix(a) = (%v, %v), want true nil", exists, err)
	}
	reverse, err := db.RangeQueryReverse(TableTxToBlock, []byte("a"), []byte("z"))
	if err != nil || len(reverse) != 2 || !bytes.Equal(reverse[0].Key, []byte("b")) {
		t.Fatalf("RangeQueryReverse() = (%+v, %v), want b first", reverse, err)
	}

	visited := 0
	if err := db.IterateByPrefix(TableTxToBlock, []byte(""), func(key []byte, value []byte) bool {
		visited++
		return visited < 1
	}); err != nil {
		t.Fatalf("IterateByPrefix() error = %v", err)
	}
	if visited != 1 {
		t.Fatalf("visited = %d, want 1", visited)
	}
	if tables, err := db.ListAllTables(); err != nil || len(tables) == 0 {
		t.Fatalf("ListAllTables() = (%v, %v), want non-empty nil", tables, err)
	}
	if err := db.Compact(nil, nil); err != nil {
		t.Fatalf("Compact(nil) error = %v", err)
	}
	if err := db.EnableWAL(false); err == nil {
		t.Fatal("EnableWAL(after open) error = nil, want immutable WAL error")
	}
}

// TestDatabaseFactoryAndClosedEdges 验证工厂和关闭边界 + 保证错误路径可观测。
func TestDatabaseFactoryAndClosedEdges(t *testing.T) {
	if _, err := NewDatabase(DatabaseConfig{Path: t.TempDir(), Engine: EngineType("bad")}); err == nil {
		t.Fatal("NewDatabase(bad engine) error = nil, want unsupported engine")
	}

	db := NewDatabaseImpl()
	if _, err := db.BeginReadTransaction(); !errors.Is(err, ErrDatabaseNotOpen) {
		t.Fatalf("BeginReadTransaction(closed) error = %v, want ErrDatabaseNotOpen", err)
	}
	if err := db.CreateDatabase(DatabaseConfig{Path: ""}); err == nil {
		t.Fatal("CreateDatabase(empty path) error = nil, want path error")
	}
}

// newOpenTestDatabase 创建测试数据库 + 保证每个用例使用隔离目录。
func newOpenTestDatabase(t *testing.T, engine EngineType) Database {
	t.Helper()
	db, err := NewDatabase(DatabaseConfig{Path: t.TempDir(), Engine: engine, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase(%s) error = %v", engine, err)
	}
	return db
}

// assertKeyValues 校验键值序列 + 保证范围查询结果顺序和内容符合预期。
func assertKeyValues(t *testing.T, pairs []KeyValue, wantKeys [][]byte, wantValues [][]byte) {
	t.Helper()
	if len(pairs) != len(wantKeys) || len(pairs) != len(wantValues) {
		t.Fatalf("len(pairs) = %d, want %d", len(pairs), len(wantKeys))
	}
	for index := range pairs {
		if !bytes.Equal(pairs[index].Key, wantKeys[index]) || !bytes.Equal(pairs[index].Value, wantValues[index]) {
			t.Fatalf("pairs[%d] = (%q,%q), want (%q,%q)", index, pairs[index].Key, pairs[index].Value, wantKeys[index], wantValues[index])
		}
	}
}
