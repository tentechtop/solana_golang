package database

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/pebble"
)

const tableKeyPrefixSize = 2

var (
	ErrDatabaseNotOpen = errors.New("database: pebble database is not open")
	pebbleNoSync       = &pebble.WriteOptions{Sync: false}
)

type cacheEntry struct {
	value     []byte
	expiresAt time.Time
	accessed  time.Time
}

type tableCache struct {
	ttl     time.Duration
	maxSize int
	items   map[string]cacheEntry
}

type pebbleTransaction struct {
	batch *pebble.Batch
	ops   []DBOperation
}

// PebbleDatabase is a Pebble-backed implementation of Database.
type PebbleDatabase struct {
	mu           sync.RWMutex
	db           *pebble.DB
	path         string
	walEnabled   bool
	initialized  bool
	transactions map[string]*pebbleTransaction
	txSeq        uint64

	cacheMu sync.RWMutex
	caches  map[Table]*tableCache
}

var _ Database = (*PebbleDatabase)(nil)

// DatabaseImpl is the default Pebble-backed implementation of Database.
type DatabaseImpl = PebbleDatabase

func NewPebbleDatabase() *PebbleDatabase {
	p := &PebbleDatabase{}
	p.mu.Lock()
	p.ensureDefaultsLocked()
	p.mu.Unlock()
	return p
}

func NewDatabaseImpl() *DatabaseImpl {
	return NewPebbleDatabase()
}

func (p *PebbleDatabase) ensureDefaultsLocked() {
	if !p.initialized {
		p.walEnabled = true
		p.initialized = true
	}
	if p.transactions == nil {
		p.transactions = make(map[string]*pebbleTransaction)
	}
}

func (p *PebbleDatabase) ensureCacheStateLocked() {
	if p.caches == nil {
		p.caches = make(map[Table]*tableCache)
	}
}

func (p *PebbleDatabase) CreateDatabase(config DatabaseConfig) error {
	if config.Path == "" {
		return errors.New("database: config path is empty")
	}
	if err := os.MkdirAll(config.Path, 0755); err != nil {
		return fmt.Errorf("database: create pebble directory: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureDefaultsLocked()
	if p.db != nil {
		return nil
	}

	db, err := pebble.Open(config.Path, &pebble.Options{
		DisableWAL: !p.walEnabled,
	})
	if err != nil {
		return fmt.Errorf("database: open pebble: %w", err)
	}
	p.db = db
	p.path = config.Path
	return nil
}

func (p *PebbleDatabase) CloseDatabase() error {
	return p.Close()
}

func (p *PebbleDatabase) Exists(table Table, key []byte) (bool, error) {
	value, err := p.Get(table, key)
	if err != nil {
		return false, err
	}
	return value != nil, nil
}

func (p *PebbleDatabase) Insert(table Table, key []byte, value []byte) error {
	return p.put(table, key, value)
}

func (p *PebbleDatabase) Delete(table Table, key []byte) error {
	if err := validateDataTable(table); err != nil {
		return err
	}
	db, err := p.getDB()
	if err != nil {
		return err
	}
	if err := db.Delete(encodeKey(table, key), pebbleNoSync); err != nil && !errors.Is(err, pebble.ErrNotFound) {
		return fmt.Errorf("database: delete key: %w", err)
	}
	p.cacheDelete(table, key)
	return nil
}

func (p *PebbleDatabase) Update(table Table, key []byte, value []byte) error {
	return p.put(table, key, value)
}

func (p *PebbleDatabase) Get(table Table, key []byte) ([]byte, error) {
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	if value, ok := p.cacheGet(table, key); ok {
		return value, nil
	}
	value, err := p.getRaw(table, key)
	if err != nil || value == nil {
		return value, err
	}
	p.cacheSet(table, key, value)
	return value, nil
}

func (p *PebbleDatabase) GetInt(table Table, key int) ([]byte, error) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], uint32(key))
	return p.Get(table, encoded[:])
}

func (p *PebbleDatabase) GetInt64(table Table, key int64) ([]byte, error) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(key))
	return p.Get(table, encoded[:])
}

func (p *PebbleDatabase) Count(table Table) (int, error) {
	if err := validateDataTable(table); err != nil {
		return 0, err
	}
	lower, upper := tableBounds(table)
	return p.countInBounds(lower, upper)
}

func (p *PebbleDatabase) CountByPrefix(table Table, prefix []byte) (int, error) {
	if err := validateDataTable(table); err != nil {
		return 0, err
	}
	lower, upper := prefixBounds(table, prefix)
	return p.countInBounds(lower, upper)
}

func (p *PebbleDatabase) IsEmpty(table Table) (bool, error) {
	first, err := p.First(table)
	if err != nil {
		return false, err
	}
	return first == nil, nil
}

func (p *PebbleDatabase) BatchInsert(table Table, keys [][]byte, values [][]byte) error {
	return p.batchPut(table, keys, values, OperationInsert)
}

func (p *PebbleDatabase) BatchDelete(table Table, keys [][]byte) error {
	ops := make([]DBOperation, len(keys))
	for i, key := range keys {
		ops[i] = DBOperation{Table: table, Key: key, Type: OperationDelete}
	}
	return p.DataTransaction(ops)
}

func (p *PebbleDatabase) BatchUpdate(table Table, keys [][]byte, values [][]byte) error {
	return p.batchPut(table, keys, values, OperationUpdate)
}

func (p *PebbleDatabase) BatchGet(table Table, keys [][]byte) ([][]byte, error) {
	values := make([][]byte, len(keys))
	for i, key := range keys {
		value, err := p.Get(table, key)
		if err != nil {
			return nil, err
		}
		values[i] = value
	}
	return values, nil
}

func (p *PebbleDatabase) Close() error {
	p.mu.Lock()
	p.ensureDefaultsLocked()
	db := p.db
	transactions := p.transactions
	p.db = nil
	p.transactions = make(map[string]*pebbleTransaction)
	p.mu.Unlock()

	var firstErr error
	for _, tx := range transactions {
		if err := tx.batch.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if db != nil {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	p.clearCacheOnly(TableAll)
	return firstErr
}

func (p *PebbleDatabase) Flush() error {
	db, err := p.getDB()
	if err != nil {
		return err
	}
	if err := db.Flush(); err != nil {
		return fmt.Errorf("database: flush pebble: %w", err)
	}
	return nil
}

func (p *PebbleDatabase) Compact(start []byte, limit []byte) error {
	db, err := p.getDB()
	if err != nil {
		return err
	}
	if start == nil {
		start = []byte{}
	}
	if limit == nil {
		limit = []byte{0xff, 0xff, 0xff, 0xff}
	}
	return db.Compact(start, limit, true)
}

func (p *PebbleDatabase) Checkpoint(destDir string) error {
	if destDir == "" {
		return errors.New("database: checkpoint destination is empty")
	}
	db, err := p.getDB()
	if err != nil {
		return err
	}
	parent := filepath.Dir(destDir)
	if parent != "." && parent != "" {
		if err := os.MkdirAll(parent, 0755); err != nil {
			return fmt.Errorf("database: create checkpoint parent: %w", err)
		}
	}
	if err := db.Checkpoint(destDir); err != nil {
		return fmt.Errorf("database: create checkpoint: %w", err)
	}
	return nil
}

func (p *PebbleDatabase) Page(table Table, pageSize int, lastKey []byte) (PageResult, error) {
	return p.page(table, nil, pageSize, lastKey, false, false)
}

func (p *PebbleDatabase) PageByPrefix(table Table, prefix []byte, pageSize int, lastKey []byte) (PageResult, error) {
	return p.page(table, prefix, pageSize, lastKey, false, false)
}

func (p *PebbleDatabase) PageKey(table Table, pageSize int, lastKey []byte) (PageResult, error) {
	return p.page(table, nil, pageSize, lastKey, true, false)
}

func (p *PebbleDatabase) PageKeyByPrefix(table Table, prefix []byte, pageSize int, lastKey []byte) (PageResult, error) {
	return p.page(table, prefix, pageSize, lastKey, true, false)
}

func (p *PebbleDatabase) PageKeyByPrefixReverse(table Table, prefix []byte, pageSize int, lastKey []byte) (PageResult, error) {
	return p.page(table, prefix, pageSize, lastKey, true, true)
}

func (p *PebbleDatabase) ExistsByPrefix(table Table, prefix []byte) (bool, error) {
	if err := validateDataTable(table); err != nil {
		return false, err
	}
	db, err := p.getDB()
	if err != nil {
		return false, err
	}
	lower, upper := prefixBounds(table, prefix)
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return false, fmt.Errorf("database: create prefix iterator: %w", err)
	}
	defer iter.Close()
	ok := iter.First()
	if err := iter.Error(); err != nil {
		return false, fmt.Errorf("database: prefix iterator: %w", err)
	}
	return ok, nil
}

func (p *PebbleDatabase) DataTransaction(operations []DBOperation) error {
	if len(operations) == 0 {
		return nil
	}
	db, err := p.getDB()
	if err != nil {
		return err
	}
	batch := db.NewBatch()
	defer batch.Close()

	ops := make([]DBOperation, len(operations))
	for i, op := range operations {
		cloned := cloneOperation(op)
		if err := applyOperationToBatch(batch, cloned); err != nil {
			return err
		}
		ops[i] = cloned
	}
	if err := batch.Commit(pebbleNoSync); err != nil {
		return fmt.Errorf("database: commit transaction: %w", err)
	}
	p.applyCacheOperations(ops)
	return nil
}

func (p *PebbleDatabase) PrefixQuery(table Table, prefix []byte) ([]KeyValue, error) {
	return p.prefixQuery(table, prefix, -1, false)
}

func (p *PebbleDatabase) PrefixQueryWithLimit(table Table, prefix []byte, limit int) ([]KeyValue, error) {
	return p.prefixQuery(table, prefix, limit, false)
}

func (p *PebbleDatabase) PrefixQueryReverse(table Table, prefix []byte) ([]KeyValue, error) {
	return p.prefixQuery(table, prefix, -1, true)
}

func (p *PebbleDatabase) PrefixQueryReverseWithLimit(table Table, prefix []byte, limit int) ([]KeyValue, error) {
	return p.prefixQuery(table, prefix, limit, true)
}

func (p *PebbleDatabase) RangeQuery(table Table, startKey []byte, endKey []byte) ([]KeyValue, error) {
	return p.rangeQuery(table, startKey, endKey, -1, false)
}

func (p *PebbleDatabase) RangeQueryWithLimit(table Table, startKey []byte, endKey []byte, limit int) ([]KeyValue, error) {
	return p.rangeQuery(table, startKey, endKey, limit, false)
}

func (p *PebbleDatabase) RangeQueryReverse(table Table, startKey []byte, endKey []byte) ([]KeyValue, error) {
	return p.rangeQuery(table, startKey, endKey, -1, true)
}

func (p *PebbleDatabase) RangeQueryReverseWithLimit(table Table, startKey []byte, endKey []byte, limit int) ([]KeyValue, error) {
	return p.rangeQuery(table, startKey, endKey, limit, true)
}

func (p *PebbleDatabase) First(table Table) (*KeyValue, error) {
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	lower, upper := tableBounds(table)
	return p.firstInBounds(lower, upper, false)
}

func (p *PebbleDatabase) Last(table Table) (*KeyValue, error) {
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	lower, upper := tableBounds(table)
	return p.firstInBounds(lower, upper, true)
}

func (p *PebbleDatabase) FirstByPrefix(table Table, prefix []byte) (*KeyValue, error) {
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	lower, upper := prefixBounds(table, prefix)
	return p.firstInBounds(lower, upper, false)
}

func (p *PebbleDatabase) LastByPrefix(table Table, prefix []byte) (*KeyValue, error) {
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	lower, upper := prefixBounds(table, prefix)
	return p.firstInBounds(lower, upper, true)
}

func (p *PebbleDatabase) ClearCache(table Table) error {
	if table != TableAll {
		if err := validateDataTable(table); err != nil {
			return err
		}
	}
	p.clearCacheOnly(table)
	return nil
}

func (p *PebbleDatabase) SetCachePolicy(table Table, ttlMillis int64, maxSize int) error {
	if ttlMillis < 0 {
		return errors.New("database: cache ttl cannot be negative")
	}
	if table == TableAll {
		for _, metadata := range AllTableMetadata() {
			if err := p.SetCachePolicy(metadata.Table, ttlMillis, maxSize); err != nil {
				return err
			}
		}
		return nil
	}
	if err := validateDataTable(table); err != nil {
		return err
	}

	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.ensureCacheStateLocked()
	if maxSize <= 0 {
		delete(p.caches, table)
		return nil
	}
	cache := p.caches[table]
	if cache == nil {
		cache = &tableCache{items: make(map[string]cacheEntry)}
		p.caches[table] = cache
	}
	cache.ttl = time.Duration(ttlMillis) * time.Millisecond
	cache.maxSize = maxSize
	enforceCacheLimit(cache)
	return nil
}

func (p *PebbleDatabase) RefreshCache(table Table, key []byte) error {
	if table == TableAll {
		for _, metadata := range AllTableMetadata() {
			if err := p.RefreshCache(metadata.Table, key); err != nil {
				return err
			}
		}
		return nil
	}
	if err := validateDataTable(table); err != nil {
		return err
	}
	if !p.cacheEnabled(table) {
		return nil
	}
	if key != nil {
		value, err := p.getRaw(table, key)
		if err != nil {
			return err
		}
		if value == nil {
			p.cacheDelete(table, key)
			return nil
		}
		p.cacheSet(table, key, value)
		return nil
	}

	p.clearCacheOnly(table)
	pairs, err := p.RangeQuery(table, nil, nil)
	if err != nil {
		return err
	}
	for _, pair := range pairs {
		p.cacheSet(table, pair.Key, pair.Value)
	}
	return nil
}

func (p *PebbleDatabase) BeginTransaction() (string, error) {
	db, err := p.getDB()
	if err != nil {
		return "", err
	}
	id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), atomic.AddUint64(&p.txSeq, 1))
	tx := &pebbleTransaction{batch: db.NewBatch()}

	p.mu.Lock()
	p.ensureDefaultsLocked()
	p.transactions[id] = tx
	p.mu.Unlock()
	return id, nil
}

func (p *PebbleDatabase) CommitTransaction(transactionID string) error {
	tx, err := p.takeTransaction(transactionID)
	if err != nil {
		return err
	}
	defer tx.batch.Close()
	if err := tx.batch.Commit(pebbleNoSync); err != nil {
		return fmt.Errorf("database: commit transaction %s: %w", transactionID, err)
	}
	p.applyCacheOperations(tx.ops)
	return nil
}

func (p *PebbleDatabase) RollbackTransaction(transactionID string) error {
	tx, err := p.takeTransaction(transactionID)
	if err != nil {
		return err
	}
	return tx.batch.Close()
}

func (p *PebbleDatabase) AddToTransaction(transactionID string, operation DBOperation) error {
	if transactionID == "" {
		return errors.New("database: transaction id is empty")
	}
	cloned := cloneOperation(operation)
	if err := validateOperation(cloned); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	tx := p.transactions[transactionID]
	if tx == nil {
		return fmt.Errorf("database: transaction %s not found", transactionID)
	}
	if err := applyOperationToBatch(tx.batch, cloned); err != nil {
		return err
	}
	tx.ops = append(tx.ops, cloned)
	return nil
}

func (p *PebbleDatabase) ListAllTables() ([]string, error) {
	metadata := AllTableMetadata()
	tables := make([]string, 0, len(metadata))
	for _, item := range metadata {
		tables = append(tables, item.ColumnFamilyName)
	}
	return tables, nil
}

func (p *PebbleDatabase) CheckHealth() error {
	db, err := p.getDB()
	if err != nil {
		return err
	}
	key := []byte{0, 0, 'h', 'e', 'a', 'l', 't', 'h'}
	if err := db.Set(key, []byte("ok"), pebbleNoSync); err != nil {
		return fmt.Errorf("database: health set: %w", err)
	}
	if err := db.Delete(key, pebbleNoSync); err != nil {
		return fmt.Errorf("database: health delete: %w", err)
	}
	return nil
}

func (p *PebbleDatabase) Iterate(table Table, handler KeyValueHandler) error {
	return p.iterate(table, nil, handler)
}

func (p *PebbleDatabase) IterateByPrefix(table Table, prefix []byte, handler KeyValueHandler) error {
	return p.iterate(table, prefix, handler)
}

func (p *PebbleDatabase) BatchDeleteRange(table Table, startKey []byte, endKey []byte) error {
	if err := validateDataTable(table); err != nil {
		return err
	}
	db, err := p.getDB()
	if err != nil {
		return err
	}
	start, end := rangeBounds(table, startKey, endKey)
	if end == nil {
		return p.deleteRangeByIteration(table, start, end)
	}
	if err := db.DeleteRange(start, end, pebbleNoSync); err != nil {
		return fmt.Errorf("database: delete range: %w", err)
	}
	p.clearCacheOnly(table)
	return nil
}

func (p *PebbleDatabase) DeleteByPrefix(table Table, prefix []byte) error {
	if err := validateDataTable(table); err != nil {
		return err
	}
	db, err := p.getDB()
	if err != nil {
		return err
	}
	start, end := prefixBounds(table, prefix)
	if end == nil {
		return p.deleteRangeByIteration(table, start, end)
	}
	if err := db.DeleteRange(start, end, pebbleNoSync); err != nil {
		return fmt.Errorf("database: delete by prefix: %w", err)
	}
	p.clearCacheOnly(table)
	return nil
}

func (p *PebbleDatabase) EnableWAL(enable bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureDefaultsLocked()
	if p.db != nil && p.walEnabled != enable {
		return errors.New("database: pebble WAL setting can only be changed before CreateDatabase")
	}
	p.walEnabled = enable
	return nil
}

func (p *PebbleDatabase) put(table Table, key []byte, value []byte) error {
	if err := validateDataTable(table); err != nil {
		return err
	}
	db, err := p.getDB()
	if err != nil {
		return err
	}
	if err := db.Set(encodeKey(table, key), value, pebbleNoSync); err != nil {
		return fmt.Errorf("database: put key: %w", err)
	}
	p.cacheSet(table, key, value)
	return nil
}

func (p *PebbleDatabase) getRaw(table Table, key []byte) ([]byte, error) {
	db, err := p.getDB()
	if err != nil {
		return nil, err
	}
	value, closer, err := db.Get(encodeKey(table, key))
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("database: get key: %w", err)
	}
	defer closer.Close()
	return cloneBytes(value), nil
}

func (p *PebbleDatabase) batchPut(table Table, keys [][]byte, values [][]byte, opType OperationType) error {
	if len(keys) != len(values) {
		return fmt.Errorf("database: keys and values length mismatch: %d != %d", len(keys), len(values))
	}
	ops := make([]DBOperation, len(keys))
	for i := range keys {
		ops[i] = DBOperation{Table: table, Key: keys[i], Value: values[i], Type: opType}
	}
	return p.DataTransaction(ops)
}

func (p *PebbleDatabase) page(table Table, prefix []byte, pageSize int, lastKey []byte, keysOnly bool, reverse bool) (PageResult, error) {
	if pageSize <= 0 {
		return PageResult{IsLastPage: true}, nil
	}
	if err := validateDataTable(table); err != nil {
		return PageResult{}, err
	}
	db, err := p.getDB()
	if err != nil {
		return PageResult{}, err
	}

	lower, upper := prefixBounds(table, prefix)
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return PageResult{}, fmt.Errorf("database: create page iterator: %w", err)
	}
	defer iter.Close()

	ok := positionPageIterator(iter, table, upper, lastKey, reverse)
	result := PageResult{Data: make([][]byte, 0, pageSize)}
	for ok && len(result.Data) < pageSize {
		rawKey := stripTablePrefix(iter.Key())
		if keysOnly {
			result.Data = append(result.Data, rawKey)
		} else {
			result.Data = append(result.Data, cloneBytes(iter.Value()))
		}
		result.LastKey = cloneBytes(rawKey)
		if reverse {
			ok = iter.Prev()
		} else {
			ok = iter.Next()
		}
	}
	if err := iter.Error(); err != nil {
		return PageResult{}, fmt.Errorf("database: page iterator: %w", err)
	}
	result.IsLastPage = !ok
	return result, nil
}

func (p *PebbleDatabase) prefixQuery(table Table, prefix []byte, limit int, reverse bool) ([]KeyValue, error) {
	if limit == 0 {
		return []KeyValue{}, nil
	}
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	db, err := p.getDB()
	if err != nil {
		return nil, err
	}
	lower, upper := prefixBounds(table, prefix)
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, fmt.Errorf("database: create prefix query iterator: %w", err)
	}
	defer iter.Close()

	pairs := make([]KeyValue, 0)
	for ok := positionPrefixQueryIterator(iter, reverse); ok; ok = advancePrefixQueryIterator(iter, reverse) {
		pairs = append(pairs, KeyValue{
			Key:   stripTablePrefix(iter.Key()),
			Value: cloneBytes(iter.Value()),
		})
		if limit > 0 && len(pairs) >= limit {
			break
		}
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("database: prefix query iterator: %w", err)
	}
	return pairs, nil
}

func (p *PebbleDatabase) rangeQuery(table Table, startKey []byte, endKey []byte, limit int, reverse bool) ([]KeyValue, error) {
	if limit == 0 {
		return []KeyValue{}, nil
	}
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	db, err := p.getDB()
	if err != nil {
		return nil, err
	}
	lower, upper := rangeBounds(table, startKey, endKey)
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, fmt.Errorf("database: create range iterator: %w", err)
	}
	defer iter.Close()

	pairs := make([]KeyValue, 0)
	for ok := positionPrefixQueryIterator(iter, reverse); ok; ok = advancePrefixQueryIterator(iter, reverse) {
		pairs = append(pairs, KeyValue{
			Key:   stripTablePrefix(iter.Key()),
			Value: cloneBytes(iter.Value()),
		})
		if limit > 0 && len(pairs) >= limit {
			break
		}
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("database: range iterator: %w", err)
	}
	return pairs, nil
}

func (p *PebbleDatabase) countInBounds(lower []byte, upper []byte) (int, error) {
	db, err := p.getDB()
	if err != nil {
		return 0, err
	}
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return 0, fmt.Errorf("database: create count iterator: %w", err)
	}
	defer iter.Close()

	count := 0
	for ok := iter.First(); ok; ok = iter.Next() {
		count++
	}
	if err := iter.Error(); err != nil {
		return 0, fmt.Errorf("database: count iterator: %w", err)
	}
	return count, nil
}

func (p *PebbleDatabase) firstInBounds(lower []byte, upper []byte, reverse bool) (*KeyValue, error) {
	db, err := p.getDB()
	if err != nil {
		return nil, err
	}
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, fmt.Errorf("database: create boundary iterator: %w", err)
	}
	defer iter.Close()

	if !positionPrefixQueryIterator(iter, reverse) {
		if err := iter.Error(); err != nil {
			return nil, fmt.Errorf("database: boundary iterator: %w", err)
		}
		return nil, nil
	}
	pair := &KeyValue{
		Key:   stripTablePrefix(iter.Key()),
		Value: cloneBytes(iter.Value()),
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("database: boundary iterator: %w", err)
	}
	return pair, nil
}

func (p *PebbleDatabase) iterate(table Table, prefix []byte, handler KeyValueHandler) error {
	if handler == nil {
		return errors.New("database: iterator handler is nil")
	}
	if err := validateDataTable(table); err != nil {
		return err
	}
	db, err := p.getDB()
	if err != nil {
		return err
	}
	lower, upper := prefixBounds(table, prefix)
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return fmt.Errorf("database: create iterator: %w", err)
	}
	defer iter.Close()

	for ok := iter.First(); ok; ok = iter.Next() {
		if !handler(stripTablePrefix(iter.Key()), cloneBytes(iter.Value())) {
			break
		}
	}
	if err := iter.Error(); err != nil {
		return fmt.Errorf("database: iterator: %w", err)
	}
	return nil
}

func (p *PebbleDatabase) deleteRangeByIteration(table Table, start []byte, end []byte) error {
	db, err := p.getDB()
	if err != nil {
		return err
	}
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: start, UpperBound: end})
	if err != nil {
		return fmt.Errorf("database: create delete range iterator: %w", err)
	}
	defer iter.Close()

	batch := db.NewBatch()
	defer batch.Close()
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := batch.Delete(cloneBytes(iter.Key()), nil); err != nil {
			return fmt.Errorf("database: delete range batch: %w", err)
		}
	}
	if err := iter.Error(); err != nil {
		return fmt.Errorf("database: delete range iterator: %w", err)
	}
	if err := batch.Commit(pebbleNoSync); err != nil {
		return fmt.Errorf("database: commit delete range: %w", err)
	}
	p.clearCacheOnly(table)
	return nil
}

func (p *PebbleDatabase) getDB() (*pebble.DB, error) {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()
	if db == nil {
		return nil, ErrDatabaseNotOpen
	}
	return db, nil
}

func (p *PebbleDatabase) takeTransaction(transactionID string) (*pebbleTransaction, error) {
	if transactionID == "" {
		return nil, errors.New("database: transaction id is empty")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	tx := p.transactions[transactionID]
	if tx == nil {
		return nil, fmt.Errorf("database: transaction %s not found", transactionID)
	}
	delete(p.transactions, transactionID)
	return tx, nil
}

func (p *PebbleDatabase) cacheEnabled(table Table) bool {
	p.cacheMu.RLock()
	defer p.cacheMu.RUnlock()
	cache := p.caches[table]
	return cache != nil && cache.maxSize > 0
}

func (p *PebbleDatabase) cacheGet(table Table, key []byte) ([]byte, bool) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	cache := p.caches[table]
	if cache == nil || cache.maxSize <= 0 {
		return nil, false
	}
	entry, ok := cache.items[string(key)]
	if !ok {
		return nil, false
	}
	now := time.Now()
	if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
		delete(cache.items, string(key))
		return nil, false
	}
	entry.accessed = now
	cache.items[string(key)] = entry
	return cloneBytes(entry.value), true
}

func (p *PebbleDatabase) cacheSet(table Table, key []byte, value []byte) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.ensureCacheStateLocked()
	cache := p.caches[table]
	if cache == nil || cache.maxSize <= 0 {
		return
	}
	now := time.Now()
	entry := cacheEntry{value: cloneBytes(value), accessed: now}
	if cache.ttl > 0 {
		entry.expiresAt = now.Add(cache.ttl)
	}
	cache.items[string(key)] = entry
	enforceCacheLimit(cache)
}

func (p *PebbleDatabase) cacheDelete(table Table, key []byte) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	cache := p.caches[table]
	if cache == nil {
		return
	}
	delete(cache.items, string(key))
}

func (p *PebbleDatabase) clearCacheOnly(table Table) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if p.caches == nil {
		return
	}
	if table == TableAll {
		for _, cache := range p.caches {
			cache.items = make(map[string]cacheEntry)
		}
		return
	}
	cache := p.caches[table]
	if cache != nil {
		cache.items = make(map[string]cacheEntry)
	}
}

func (p *PebbleDatabase) applyCacheOperations(operations []DBOperation) {
	for _, op := range operations {
		switch op.Type {
		case OperationInsert, OperationUpdate:
			p.cacheSet(op.Table, op.Key, op.Value)
		case OperationDelete:
			p.cacheDelete(op.Table, op.Key)
		}
	}
}

func enforceCacheLimit(cache *tableCache) {
	for cache.maxSize > 0 && len(cache.items) > cache.maxSize {
		var oldestKey string
		var oldestTime time.Time
		for key, entry := range cache.items {
			if oldestKey == "" || entry.accessed.Before(oldestTime) {
				oldestKey = key
				oldestTime = entry.accessed
			}
		}
		delete(cache.items, oldestKey)
	}
}

func applyOperationToBatch(batch *pebble.Batch, op DBOperation) error {
	if err := validateOperation(op); err != nil {
		return err
	}
	key := encodeKey(op.Table, op.Key)
	switch op.Type {
	case OperationInsert, OperationUpdate:
		if err := batch.Set(key, op.Value, nil); err != nil {
			return fmt.Errorf("database: add set operation: %w", err)
		}
	case OperationDelete:
		if err := batch.Delete(key, nil); err != nil {
			return fmt.Errorf("database: add delete operation: %w", err)
		}
	default:
		return fmt.Errorf("database: unsupported operation type %d", op.Type)
	}
	return nil
}

func validateOperation(op DBOperation) error {
	if err := validateDataTable(op.Table); err != nil {
		return err
	}
	switch op.Type {
	case OperationInsert, OperationUpdate, OperationDelete:
		return nil
	default:
		return fmt.Errorf("database: unsupported operation type %d", op.Type)
	}
}

func validateDataTable(table Table) error {
	if table == TableAll {
		return errors.New("database: TableAll cannot be used for data operations")
	}
	if _, ok := table.Metadata(); !ok {
		return fmt.Errorf("database: unknown table code %d", table)
	}
	return nil
}

func positionPageIterator(iter *pebble.Iterator, table Table, upper []byte, lastKey []byte, reverse bool) bool {
	if reverse {
		if lastKey != nil {
			return iter.SeekLT(encodeKey(table, lastKey))
		}
		if upper == nil {
			return iter.Last()
		}
		return iter.SeekLT(upper)
	}
	if lastKey == nil {
		return iter.First()
	}
	encoded := encodeKey(table, lastKey)
	ok := iter.SeekGE(encoded)
	if ok && bytes.Equal(iter.Key(), encoded) {
		return iter.Next()
	}
	return ok
}

func positionPrefixQueryIterator(iter *pebble.Iterator, reverse bool) bool {
	if reverse {
		return iter.Last()
	}
	return iter.First()
}

func advancePrefixQueryIterator(iter *pebble.Iterator, reverse bool) bool {
	if reverse {
		return iter.Prev()
	}
	return iter.Next()
}

func tableBounds(table Table) ([]byte, []byte) {
	return tablePrefix(table), tableUpperBound(table)
}

func rangeBounds(table Table, startKey []byte, endKey []byte) ([]byte, []byte) {
	lower, upper := tableBounds(table)
	if startKey != nil {
		lower = encodeKey(table, startKey)
	}
	if endKey != nil {
		upper = encodeKey(table, endKey)
	}
	return lower, upper
}

func prefixBounds(table Table, prefix []byte) ([]byte, []byte) {
	lower := encodeKey(table, prefix)
	upper := keySuccessor(lower)
	tableUpper := tableUpperBound(table)
	if upper == nil || (tableUpper != nil && bytes.Compare(upper, tableUpper) > 0) {
		upper = tableUpper
	}
	return lower, upper
}

func tablePrefix(table Table) []byte {
	prefix := make([]byte, tableKeyPrefixSize)
	binary.BigEndian.PutUint16(prefix, table.Code())
	return prefix
}

func tableUpperBound(table Table) []byte {
	code := table.Code()
	if code == ^uint16(0) {
		return nil
	}
	upper := make([]byte, tableKeyPrefixSize)
	binary.BigEndian.PutUint16(upper, code+1)
	return upper
}

func encodeKey(table Table, key []byte) []byte {
	prefix := tablePrefix(table)
	encoded := make([]byte, len(prefix)+len(key))
	copy(encoded, prefix)
	copy(encoded[len(prefix):], key)
	return encoded
}

func stripTablePrefix(key []byte) []byte {
	if len(key) <= tableKeyPrefixSize {
		return []byte{}
	}
	return cloneBytes(key[tableKeyPrefixSize:])
}

func keySuccessor(key []byte) []byte {
	successor := cloneBytes(key)
	for i := len(successor) - 1; i >= 0; i-- {
		if successor[i] != 0xff {
			successor[i]++
			return successor[:i+1]
		}
	}
	return nil
}

func cloneOperation(op DBOperation) DBOperation {
	op.Key = cloneBytes(op.Key)
	op.Value = cloneBytes(op.Value)
	return op
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
