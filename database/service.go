package database

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type databaseTransaction struct {
	batch KVBatch
	ops   []DBOperation
}

type kvReadSource interface {
	Get(key []byte) ([]byte, error)
	NewIterator(lower []byte, upper []byte) (KVIterator, error)
	IsNotFound(err error) bool
}

type readTransaction struct {
	mu       sync.RWMutex
	snapshot KVSnapshot
	closed   bool
}

type databaseService struct {
	mu           sync.RWMutex
	engine       KVEngine
	path         string
	walEnabled   bool
	initialized  bool
	transactions map[string]*databaseTransaction
	txSeq        uint64

	cacheMu sync.RWMutex
	caches  map[Table]*tableCache
}

// PebbleDatabase 是 Pebble 包装类型 + 保持旧构造函数兼容。
type PebbleDatabase struct {
	*databaseService
}

// LevelDBDatabase 是 LevelDB 包装类型 + 便于调用方显式选择引擎。
type LevelDBDatabase struct {
	*databaseService
}

var _ Database = (*PebbleDatabase)(nil)
var _ Database = (*LevelDBDatabase)(nil)
var _ Database = (*databaseService)(nil)
var _ ReadTransaction = (*readTransaction)(nil)

// DatabaseImpl 默认数据库实现 + 兼容旧代码入口。
type DatabaseImpl = databaseService

func newDatabaseService(engine KVEngine) *databaseService {
	database := &databaseService{engine: engine}
	database.mu.Lock()
	database.ensureDefaultsLocked()
	database.mu.Unlock()
	return database
}

// NewPebbleDatabase 创建 Pebble 数据库实例 + 作为默认高性能存储引擎入口。
func NewPebbleDatabase() *PebbleDatabase {
	return &PebbleDatabase{databaseService: newDatabaseService(newPebbleKVEngine())}
}

// NewLevelDBDatabase 创建 LevelDB 数据库实例 + 为兼容场景提供可替换引擎。
func NewLevelDBDatabase() *LevelDBDatabase {
	return &LevelDBDatabase{databaseService: newDatabaseService(newLevelDBKVEngine())}
}

// NewDatabaseImpl 创建默认数据库实现 + 保留旧接口同时指向 Pebble 引擎。
func NewDatabaseImpl() *DatabaseImpl {
	return newDatabaseService(newPebbleKVEngine())
}

// ensureDefaultsLocked 初始化服务默认状态 + 调用方持锁保证并发安全。
func (p *databaseService) ensureDefaultsLocked() {
	if !p.initialized {
		p.walEnabled = true
		p.initialized = true
	}
	if p.transactions == nil {
		p.transactions = make(map[string]*databaseTransaction)
	}
}

// CreateDatabase 打开底层 KV 引擎 + 初始化路径和 WAL 配置保证写入一致性。
func (p *databaseService) CreateDatabase(config DatabaseConfig) error {
	if config.Path == "" {
		return errors.New("database: config path is empty")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureDefaultsLocked()
	if p.path != "" {
		return nil
	}
	p.walEnabled = config.WAL
	if err := p.engine.Open(config.Path, config.WAL); err != nil {
		return fmt.Errorf("database: open engine: %w", err)
	}
	p.path = config.Path
	return nil
}

// CloseDatabase 关闭数据库服务 + 兼容历史接口命名。
func (p *databaseService) CloseDatabase() error {
	return p.Close()
}

// Exists 判断键是否存在 + 复用 Get 保持缓存和底层读取语义一致。
func (p *databaseService) Exists(table Table, key []byte) (bool, error) {
	value, err := p.Get(table, key)
	if err != nil {
		return false, err
	}
	return value != nil, nil
}

// Put 写入键值 + 统一走 put 保证缓存同步更新。
func (p *databaseService) Put(table Table, key []byte, value []byte) error {
	return p.put(table, key, value)
}
func (p *databaseService) Insert(table Table, key []byte, value []byte) error {
	return p.put(table, key, value)
}

// Delete 删除键值 + 底层 NotFound 视为幂等成功并同步清理缓存。
func (p *databaseService) Delete(table Table, key []byte) error {
	if err := validateDataTable(table); err != nil {
		return err
	}
	engine, err := p.getDB()
	if err != nil {
		return err
	}
	if err := engine.Delete(encodeKey(table, key)); err != nil && !engine.IsNotFound(err) {
		return fmt.Errorf("database: delete key: %w", err)
	}
	p.cacheDelete(table, key)
	return nil
}

// Update 更新键值 + 与 Put 共用写路径保持缓存一致。
func (p *databaseService) Update(table Table, key []byte, value []byte) error {
	return p.put(table, key, value)
}

// Get 读取键值 + 优先命中缓存并返回克隆数据隔离调用方修改。
func (p *databaseService) Get(table Table, key []byte) ([]byte, error) {
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
func (p *databaseService) ExistsInt(table Table, key int) (bool, error) {
	return p.Exists(table, encodeIntKey(key))
}
func (p *databaseService) ExistsInt64(table Table, key int64) (bool, error) {
	return p.Exists(table, encodeInt64Key(key))
}
func (p *databaseService) PutInt(table Table, key int, value []byte) error {
	return p.Put(table, encodeIntKey(key), value)
}
func (p *databaseService) PutInt64(table Table, key int64, value []byte) error {
	return p.Put(table, encodeInt64Key(key), value)
}
func (p *databaseService) UpdateInt(table Table, key int, value []byte) error {
	return p.Update(table, encodeIntKey(key), value)
}
func (p *databaseService) UpdateInt64(table Table, key int64, value []byte) error {
	return p.Update(table, encodeInt64Key(key), value)
}
func (p *databaseService) DeleteInt(table Table, key int) error {
	return p.Delete(table, encodeIntKey(key))
}
func (p *databaseService) DeleteInt64(table Table, key int64) error {
	return p.Delete(table, encodeInt64Key(key))
}
func (p *databaseService) GetInt(table Table, key int) ([]byte, error) {
	return p.Get(table, encodeIntKey(key))
}
func (p *databaseService) GetInt64(table Table, key int64) ([]byte, error) {
	return p.Get(table, encodeInt64Key(key))
}
func (p *databaseService) Count(table Table) (int, error) {
	if err := validateDataTable(table); err != nil {
		return 0, err
	}
	lower, upper := tableBounds(table)
	return p.countInBounds(lower, upper)
}
func (p *databaseService) CountByPrefix(table Table, prefix []byte) (int, error) {
	if err := validateDataTable(table); err != nil {
		return 0, err
	}
	lower, upper := prefixBounds(table, prefix)
	return p.countInBounds(lower, upper)
}
func (p *databaseService) IsEmpty(table Table) (bool, error) {
	first, err := p.First(table)
	if err != nil {
		return false, err
	}
	return first == nil, nil
}
func (p *databaseService) BatchInsert(table Table, keys [][]byte, values [][]byte) error {
	return p.batchPut(table, keys, values, OperationInsert)
}
func (p *databaseService) BatchDelete(table Table, keys [][]byte) error {
	ops := make([]DBOperation, len(keys))
	for i, key := range keys {
		ops[i] = DBOperation{Table: table, Key: key, Type: OperationDelete}
	}
	return p.DataTransaction(ops)
}
func (p *databaseService) BatchUpdate(table Table, keys [][]byte, values [][]byte) error {
	return p.batchPut(table, keys, values, OperationUpdate)
}
func (p *databaseService) BatchGet(table Table, keys [][]byte) ([][]byte, error) {
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

// Close 关闭数据库和未提交事务 + 先摘除服务状态再释放底层资源。
func (p *databaseService) Close() error {
	p.mu.Lock()
	p.ensureDefaultsLocked()
	engine := p.engine
	transactions := p.transactions
	p.path = ""
	p.transactions = make(map[string]*databaseTransaction)
	p.mu.Unlock()

	var firstErr error
	for _, tx := range transactions {
		if err := tx.batch.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if engine != nil {
		if err := engine.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	p.clearCacheOnly(TableAll)
	return firstErr
}

// Flush 刷盘当前写入 + 将引擎错误补充上下文后向上传递。
func (p *databaseService) Flush() error {
	engine, err := p.getDB()
	if err != nil {
		return err
	}
	if err := engine.Flush(); err != nil {
		return fmt.Errorf("database: flush engine: %w", err)
	}
	return nil
}

// Compact 压缩指定键范围 + 空边界映射为全库范围以便维护任务调用。
func (p *databaseService) Compact(start []byte, limit []byte) error {
	engine, err := p.getDB()
	if err != nil {
		return err
	}
	if start == nil {
		start = []byte{}
	}
	if limit == nil {
		limit = []byte{0xff, 0xff, 0xff, 0xff}
	}
	return engine.Compact(start, limit)
}

// Checkpoint 创建数据库检查点 + 仅在引擎支持时执行防止误报成功。
func (p *databaseService) Checkpoint(destDir string) error {
	if destDir == "" {
		return errors.New("database: checkpoint destination is empty")
	}
	engine, err := p.getDB()
	if err != nil {
		return err
	}
	if !engine.SupportsCheckpoint() {
		return ErrFeatureNotSupported
	}
	if err := engine.Checkpoint(destDir); err != nil {
		return fmt.Errorf("database: create checkpoint: %w", err)
	}
	return nil
}
func (p *databaseService) Page(table Table, pageSize int, lastKey []byte) (PageResult, error) {
	return p.page(table, nil, pageSize, lastKey, false, false)
}
func (p *databaseService) PageByPrefix(table Table, prefix []byte, pageSize int, lastKey []byte) (PageResult, error) {
	return p.page(table, prefix, pageSize, lastKey, false, false)
}
func (p *databaseService) PageKey(table Table, pageSize int, lastKey []byte) (PageResult, error) {
	return p.page(table, nil, pageSize, lastKey, true, false)
}
func (p *databaseService) PageKeyByPrefix(table Table, prefix []byte, pageSize int, lastKey []byte) (PageResult, error) {
	return p.page(table, prefix, pageSize, lastKey, true, false)
}
func (p *databaseService) PageKeyByPrefixReverse(table Table, prefix []byte, pageSize int, lastKey []byte) (PageResult, error) {
	return p.page(table, prefix, pageSize, lastKey, true, true)
}
func (p *databaseService) ExistsByPrefix(table Table, prefix []byte) (bool, error) {
	if err := validateDataTable(table); err != nil {
		return false, err
	}
	engine, err := p.getDB()
	if err != nil {
		return false, err
	}
	lower, upper := prefixBounds(table, prefix)
	iter, err := engine.NewIterator(lower, upper)
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

// BeginReadTransaction 创建只读快照 + 为多次读取提供一致视图。
func (p *databaseService) BeginReadTransaction() (ReadTransaction, error) {
	engine, err := p.getDB()
	if err != nil {
		return nil, err
	}
	snapshot, err := engine.NewSnapshot()
	if err != nil {
		return nil, fmt.Errorf("database: create read transaction snapshot: %w", err)
	}
	return &readTransaction{snapshot: snapshot}, nil
}

// DataTransaction 原子提交批量操作 + 提交成功后再同步缓存保证读写一致。
func (p *databaseService) DataTransaction(operations []DBOperation) error {
	if len(operations) == 0 {
		return nil
	}
	engine, err := p.getDB()
	if err != nil {
		return err
	}
	batch, err := engine.NewBatch()
	if err != nil {
		return fmt.Errorf("database: create transaction batch: %w", err)
	}
	defer batch.Close()

	ops := make([]DBOperation, len(operations))
	for i, op := range operations {
		cloned := cloneOperation(op)
		if err := applyOperationToBatch(batch, cloned); err != nil {
			return err
		}
		ops[i] = cloned
	}
	if err := batch.Commit(); err != nil {
		return fmt.Errorf("database: commit transaction: %w", err)
	}
	p.applyCacheOperations(ops)
	return nil
}
func (p *databaseService) PrefixQuery(table Table, prefix []byte) ([]KeyValue, error) {
	return p.prefixQuery(table, prefix, -1, false)
}
func (p *databaseService) PrefixQueryWithLimit(table Table, prefix []byte, limit int) ([]KeyValue, error) {
	return p.prefixQuery(table, prefix, limit, false)
}
func (p *databaseService) PrefixQueryReverse(table Table, prefix []byte) ([]KeyValue, error) {
	return p.prefixQuery(table, prefix, -1, true)
}
func (p *databaseService) PrefixQueryReverseWithLimit(table Table, prefix []byte, limit int) ([]KeyValue, error) {
	return p.prefixQuery(table, prefix, limit, true)
}
func (p *databaseService) RangeQuery(table Table, startKey []byte, endKey []byte) ([]KeyValue, error) {
	return p.rangeQuery(table, startKey, endKey, -1, false)
}
func (p *databaseService) RangeQueryWithLimit(table Table, startKey []byte, endKey []byte, limit int) ([]KeyValue, error) {
	return p.rangeQuery(table, startKey, endKey, limit, false)
}
func (p *databaseService) RangeQueryReverse(table Table, startKey []byte, endKey []byte) ([]KeyValue, error) {
	return p.rangeQuery(table, startKey, endKey, -1, true)
}
func (p *databaseService) RangeQueryReverseWithLimit(table Table, startKey []byte, endKey []byte, limit int) ([]KeyValue, error) {
	return p.rangeQuery(table, startKey, endKey, limit, true)
}
func (p *databaseService) First(table Table) (*KeyValue, error) {
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	lower, upper := tableBounds(table)
	return p.firstInBounds(lower, upper, false)
}
func (p *databaseService) Last(table Table) (*KeyValue, error) {
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	lower, upper := tableBounds(table)
	return p.firstInBounds(lower, upper, true)
}
func (p *databaseService) FirstByPrefix(table Table, prefix []byte) (*KeyValue, error) {
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	lower, upper := prefixBounds(table, prefix)
	return p.firstInBounds(lower, upper, false)
}
func (p *databaseService) LastByPrefix(table Table, prefix []byte) (*KeyValue, error) {
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	lower, upper := prefixBounds(table, prefix)
	return p.firstInBounds(lower, upper, true)
}
func (p *databaseService) Keys(table Table) ([][]byte, error) {
	pairs, err := p.RangeQuery(table, nil, nil)
	if err != nil {
		return nil, err
	}
	return collectKeys(pairs), nil
}
func (p *databaseService) KeysByPrefix(table Table, prefix []byte) ([][]byte, error) {
	pairs, err := p.PrefixQuery(table, prefix)
	if err != nil {
		return nil, err
	}
	return collectKeys(pairs), nil
}
func (p *databaseService) Values(table Table) ([][]byte, error) {
	pairs, err := p.RangeQuery(table, nil, nil)
	if err != nil {
		return nil, err
	}
	return collectValues(pairs), nil
}
func (p *databaseService) ValuesByPrefix(table Table, prefix []byte) ([][]byte, error) {
	pairs, err := p.PrefixQuery(table, prefix)
	if err != nil {
		return nil, err
	}
	return collectValues(pairs), nil
}
func (p *databaseService) ClearCache(table Table) error {
	if table != TableAll {
		if err := validateDataTable(table); err != nil {
			return err
		}
	}
	p.clearCacheOnly(table)
	return nil
}
func (p *databaseService) SetCachePolicy(table Table, ttlMillis int64, maxSize int) error {
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

// RefreshCache 刷新缓存内容 + 支持单键回源或整表重建。
func (p *databaseService) RefreshCache(table Table, key []byte) error {
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

// BeginTransaction 创建手动事务 + 返回事务 ID 供多步写入后统一提交。
func (p *databaseService) BeginTransaction() (string, error) {
	engine, err := p.getDB()
	if err != nil {
		return "", err
	}
	batch, err := engine.NewBatch()
	if err != nil {
		return "", fmt.Errorf("database: create manual transaction batch: %w", err)
	}
	id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), atomic.AddUint64(&p.txSeq, 1))
	tx := &databaseTransaction{batch: batch}

	p.mu.Lock()
	p.ensureDefaultsLocked()
	p.transactions[id] = tx
	p.mu.Unlock()
	return id, nil
}

// CommitTransaction 提交手动事务 + 成功写入后应用事务内缓存变更。
func (p *databaseService) CommitTransaction(transactionID string) error {
	tx, err := p.takeTransaction(transactionID)
	if err != nil {
		return err
	}
	defer tx.batch.Close()
	if err := tx.batch.Commit(); err != nil {
		return fmt.Errorf("database: commit transaction %s: %w", transactionID, err)
	}
	p.applyCacheOperations(tx.ops)
	return nil
}

// RollbackTransaction 回滚手动事务 + 关闭未提交批次释放底层资源。
func (p *databaseService) RollbackTransaction(transactionID string) error {
	tx, err := p.takeTransaction(transactionID)
	if err != nil {
		return err
	}
	return tx.batch.Close()
}

// AddToTransaction 追加事务操作 + 克隆输入避免外部修改影响提交内容。
func (p *databaseService) AddToTransaction(transactionID string, operation DBOperation) error {
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
func (p *databaseService) ListAllTables() ([]string, error) {
	metadata := AllTableMetadata()
	tables := make([]string, 0, len(metadata))
	for _, item := range metadata {
		tables = append(tables, item.ColumnFamilyName)
	}
	return tables, nil
}
func (p *databaseService) CheckHealth() error {
	engine, err := p.getDB()
	if err != nil {
		return err
	}
	return engine.CheckHealth()
}
func (p *databaseService) Iterate(table Table, handler KeyValueHandler) error {
	return p.iterate(table, nil, handler)
}
func (p *databaseService) IterateByPrefix(table Table, prefix []byte, handler KeyValueHandler) error {
	return p.iterate(table, prefix, handler)
}
func (p *databaseService) BatchDeleteRange(table Table, startKey []byte, endKey []byte) error {
	if err := validateDataTable(table); err != nil {
		return err
	}
	engine, err := p.getDB()
	if err != nil {
		return err
	}
	start, end := rangeBounds(table, startKey, endKey)
	if end == nil {
		return p.deleteRangeByIteration(table, start, end)
	}
	if err := engine.DeleteRange(start, end); err != nil {
		return fmt.Errorf("database: delete range: %w", err)
	}
	p.clearCacheOnly(table)
	return nil
}

// DeleteByPrefix 删除指定前缀数据 + 使用引擎范围删除并清空表缓存。
func (p *databaseService) DeleteByPrefix(table Table, prefix []byte) error {
	if err := validateDataTable(table); err != nil {
		return err
	}
	engine, err := p.getDB()
	if err != nil {
		return err
	}
	start, end := prefixBounds(table, prefix)
	if end == nil {
		return p.deleteRangeByIteration(table, start, end)
	}
	if err := engine.DeleteRange(start, end); err != nil {
		return fmt.Errorf("database: delete by prefix: %w", err)
	}
	p.clearCacheOnly(table)
	return nil
}

// ClearTable 清空单表数据 + 复用范围删除确保缓存同步失效。
func (p *databaseService) ClearTable(table Table) error {
	return p.BatchDeleteRange(table, nil, nil)
}

// EnableWAL 设置写前日志开关 + 只允许建库前变更避免运行期一致性风险。
func (p *databaseService) EnableWAL(enable bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureDefaultsLocked()
	if p.path != "" && p.walEnabled != enable {
		return errors.New("database: WAL setting can only be changed before CreateDatabase")
	}
	if !enable && !p.engine.SupportsDisableWAL() {
		return ErrFeatureNotSupported
	}
	p.walEnabled = enable
	return p.engine.EnableWAL(enable)
}

// put 写入原始键值 + 统一编码表前缀并维护写后缓存。
func (p *databaseService) put(table Table, key []byte, value []byte) error {
	if err := validateDataTable(table); err != nil {
		return err
	}
	engine, err := p.getDB()
	if err != nil {
		return err
	}
	if err := engine.Set(encodeKey(table, key), value); err != nil {
		return fmt.Errorf("database: put key: %w", err)
	}
	p.cacheSet(table, key, value)
	return nil
}

// getRaw 直接读取底层引擎 + 跳过缓存用于缓存刷新和快照外读。
func (p *databaseService) getRaw(table Table, key []byte) ([]byte, error) {
	engine, err := p.getDB()
	if err != nil {
		return nil, err
	}
	return readRaw(engine, table, key)
}

// batchPut 构造批量写操作 + 先校验键值数量避免部分写入。
func (p *databaseService) batchPut(table Table, keys [][]byte, values [][]byte, opType OperationType) error {
	if len(keys) != len(values) {
		return fmt.Errorf("database: keys and values length mismatch: %d != %d", len(keys), len(values))
	}
	ops := make([]DBOperation, len(keys))
	for i := range keys {
		ops[i] = DBOperation{Table: table, Key: keys[i], Value: values[i], Type: opType}
	}
	return p.DataTransaction(ops)
}

// page 执行游标分页查询 + 使用 lastKey 避免重复返回上一页末尾数据。
func (p *databaseService) page(table Table, prefix []byte, pageSize int, lastKey []byte, keysOnly bool, reverse bool) (PageResult, error) {
	if pageSize <= 0 {
		return PageResult{IsLastPage: true}, nil
	}
	if err := validateDataTable(table); err != nil {
		return PageResult{}, err
	}
	engine, err := p.getDB()
	if err != nil {
		return PageResult{}, err
	}

	lower, upper := prefixBounds(table, prefix)
	iter, err := engine.NewIterator(lower, upper)
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

// prefixQuery 查询前缀范围 + 统一处理限制条数和正反向遍历。
func (p *databaseService) prefixQuery(table Table, prefix []byte, limit int, reverse bool) ([]KeyValue, error) {
	if limit == 0 {
		return []KeyValue{}, nil
	}
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	engine, err := p.getDB()
	if err != nil {
		return nil, err
	}
	return readPrefixQuery(engine, table, prefix, limit, reverse)
}

// rangeQuery 查询闭开键范围 + 统一处理限制条数和正反向遍历。
func (p *databaseService) rangeQuery(table Table, startKey []byte, endKey []byte, limit int, reverse bool) ([]KeyValue, error) {
	if limit == 0 {
		return []KeyValue{}, nil
	}
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	engine, err := p.getDB()
	if err != nil {
		return nil, err
	}
	return readRangeQuery(engine, table, startKey, endKey, limit, reverse)
}
func (p *databaseService) countInBounds(lower []byte, upper []byte) (int, error) {
	engine, err := p.getDB()
	if err != nil {
		return 0, err
	}
	return readCountInBounds(engine, lower, upper)
}
func (p *databaseService) firstInBounds(lower []byte, upper []byte, reverse bool) (*KeyValue, error) {
	engine, err := p.getDB()
	if err != nil {
		return nil, err
	}
	return readFirstInBounds(engine, lower, upper, reverse)
}
func (tx *readTransaction) Get(table Table, key []byte) ([]byte, error) {
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	snapshot, err := tx.readSource()
	if err != nil {
		return nil, err
	}
	defer snapshot.unlock()
	return readRaw(snapshot.source, table, key)
}
func (tx *readTransaction) Exists(table Table, key []byte) (bool, error) {
	value, err := tx.Get(table, key)
	if err != nil {
		return false, err
	}
	return value != nil, nil
}
func (tx *readTransaction) BatchGet(table Table, keys [][]byte) ([][]byte, error) {
	values := make([][]byte, len(keys))
	for index, key := range keys {
		value, err := tx.Get(table, key)
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	return values, nil
}
func (tx *readTransaction) Count(table Table) (int, error) {
	if err := validateDataTable(table); err != nil {
		return 0, err
	}
	snapshot, err := tx.readSource()
	if err != nil {
		return 0, err
	}
	defer snapshot.unlock()
	lower, upper := tableBounds(table)
	return readCountInBounds(snapshot.source, lower, upper)
}
func (tx *readTransaction) CountByPrefix(table Table, prefix []byte) (int, error) {
	if err := validateDataTable(table); err != nil {
		return 0, err
	}
	snapshot, err := tx.readSource()
	if err != nil {
		return 0, err
	}
	defer snapshot.unlock()
	lower, upper := prefixBounds(table, prefix)
	return readCountInBounds(snapshot.source, lower, upper)
}
func (tx *readTransaction) PrefixQuery(table Table, prefix []byte) ([]KeyValue, error) {
	return tx.PrefixQueryWithLimit(table, prefix, -1)
}
func (tx *readTransaction) PrefixQueryWithLimit(table Table, prefix []byte, limit int) ([]KeyValue, error) {
	if limit == 0 {
		return []KeyValue{}, nil
	}
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	snapshot, err := tx.readSource()
	if err != nil {
		return nil, err
	}
	defer snapshot.unlock()
	return readPrefixQuery(snapshot.source, table, prefix, limit, false)
}
func (tx *readTransaction) RangeQuery(table Table, startKey []byte, endKey []byte) ([]KeyValue, error) {
	return tx.RangeQueryWithLimit(table, startKey, endKey, -1)
}
func (tx *readTransaction) RangeQueryWithLimit(table Table, startKey []byte, endKey []byte, limit int) ([]KeyValue, error) {
	if limit == 0 {
		return []KeyValue{}, nil
	}
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	snapshot, err := tx.readSource()
	if err != nil {
		return nil, err
	}
	defer snapshot.unlock()
	return readRangeQuery(snapshot.source, table, startKey, endKey, limit, false)
}
func (tx *readTransaction) First(table Table) (*KeyValue, error) {
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	snapshot, err := tx.readSource()
	if err != nil {
		return nil, err
	}
	defer snapshot.unlock()
	lower, upper := tableBounds(table)
	return readFirstInBounds(snapshot.source, lower, upper, false)
}
func (tx *readTransaction) Last(table Table) (*KeyValue, error) {
	if err := validateDataTable(table); err != nil {
		return nil, err
	}
	snapshot, err := tx.readSource()
	if err != nil {
		return nil, err
	}
	defer snapshot.unlock()
	lower, upper := tableBounds(table)
	return readFirstInBounds(snapshot.source, lower, upper, true)
}
func (tx *readTransaction) Close() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return nil
	}
	tx.closed = true
	return tx.snapshot.Close()
}

type lockedReadSource struct {
	source KVSnapshot
	unlock func()
}

// readSource 获取快照读源 + 读锁覆盖使用期防止 Close 并发释放。
func (tx *readTransaction) readSource() (lockedReadSource, error) {
	tx.mu.RLock()
	if tx.closed {
		tx.mu.RUnlock()
		return lockedReadSource{}, errors.New("database: read transaction closed")
	}
	return lockedReadSource{source: tx.snapshot, unlock: tx.mu.RUnlock}, nil
}

// readRaw 读取单个编码键 + 将引擎 NotFound 归一为 nil 结果。
func readRaw(reader kvReadSource, table Table, key []byte) ([]byte, error) {
	value, err := reader.Get(encodeKey(table, key))
	if reader.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("database: get key: %w", err)
	}
	return cloneBytes(value), nil
}
func readPrefixQuery(reader kvReadSource, table Table, prefix []byte, limit int, reverse bool) ([]KeyValue, error) {
	lower, upper := prefixBounds(table, prefix)
	return readQueryInBounds(reader, lower, upper, limit, reverse, "prefix query")
}
func readRangeQuery(reader kvReadSource, table Table, startKey []byte, endKey []byte, limit int, reverse bool) ([]KeyValue, error) {
	lower, upper := rangeBounds(table, startKey, endKey)
	return readQueryInBounds(reader, lower, upper, limit, reverse, "range")
}

// readQueryInBounds 遍历已编码边界 + 统一复制键值防止调用方修改底层缓冲。
func readQueryInBounds(reader kvReadSource, lower []byte, upper []byte, limit int, reverse bool, operation string) ([]KeyValue, error) {
	iter, err := reader.NewIterator(lower, upper)
	if err != nil {
		return nil, fmt.Errorf("database: create %s iterator: %w", operation, err)
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
		return nil, fmt.Errorf("database: %s iterator: %w", operation, err)
	}
	return pairs, nil
}

// readCountInBounds 统计已编码范围内记录数 + 复用迭代器避免加载值集合。
func readCountInBounds(reader kvReadSource, lower []byte, upper []byte) (int, error) {
	iter, err := reader.NewIterator(lower, upper)
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

// readFirstInBounds 读取范围边界记录 + reverse 控制首条或末条选择。
func readFirstInBounds(reader kvReadSource, lower []byte, upper []byte, reverse bool) (*KeyValue, error) {
	iter, err := reader.NewIterator(lower, upper)
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

// iterate 流式遍历键值 + 由 handler 返回值控制提前停止。
func (p *databaseService) iterate(table Table, prefix []byte, handler KeyValueHandler) error {
	if handler == nil {
		return errors.New("database: iterator handler is nil")
	}
	if err := validateDataTable(table); err != nil {
		return err
	}
	engine, err := p.getDB()
	if err != nil {
		return err
	}
	lower, upper := prefixBounds(table, prefix)
	iter, err := engine.NewIterator(lower, upper)
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

// deleteRangeByIteration 迭代删除无上界范围 + 兜底处理引擎无法表达的删除边界。
func (p *databaseService) deleteRangeByIteration(table Table, start []byte, end []byte) error {
	engine, err := p.getDB()
	if err != nil {
		return err
	}
	iter, err := engine.NewIterator(start, end)
	if err != nil {
		return fmt.Errorf("database: create delete range iterator: %w", err)
	}
	defer iter.Close()

	batch, err := engine.NewBatch()
	if err != nil {
		return fmt.Errorf("database: create delete range batch: %w", err)
	}
	defer batch.Close()
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := batch.Delete(cloneBytes(iter.Key())); err != nil {
			return fmt.Errorf("database: delete range batch: %w", err)
		}
	}
	if err := iter.Error(); err != nil {
		return fmt.Errorf("database: delete range iterator: %w", err)
	}
	if err := batch.Commit(); err != nil {
		return fmt.Errorf("database: commit delete range: %w", err)
	}
	p.clearCacheOnly(table)
	return nil
}

// getDB 获取已打开引擎 + 在锁内检查打开状态避免并发关闭竞态。
func (p *databaseService) getDB() (KVEngine, error) {
	p.mu.RLock()
	engine := p.engine
	opened := p.path != ""
	p.mu.RUnlock()
	if engine == nil || !opened {
		return nil, ErrDatabaseNotOpen
	}
	return engine, nil
}

// takeTransaction 取出并移除事务 + 防止提交和回滚重复执行。
func (p *databaseService) takeTransaction(transactionID string) (*databaseTransaction, error) {
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

// applyOperationToBatch 写入批处理操作 + 在加入 batch 前统一校验表和类型。
func applyOperationToBatch(batch KVBatch, op DBOperation) error {
	if err := validateOperation(op); err != nil {
		return err
	}
	key := encodeKey(op.Table, op.Key)
	switch op.Type {
	case OperationInsert, OperationUpdate:
		if err := batch.Set(key, op.Value); err != nil {
			return fmt.Errorf("database: add set operation: %w", err)
		}
	case OperationDelete:
		if err := batch.Delete(key); err != nil {
			return fmt.Errorf("database: add delete operation: %w", err)
		}
	default:
		return fmt.Errorf("database: unsupported operation type %d", op.Type)
	}
	return nil
}

// validateOperation 校验数据库操作 + 阻止非法表和未知操作类型进入批处理。
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

// validateDataTable 校验数据表枚举 + 禁止 TableAll 参与单表读写。
func validateDataTable(table Table) error {
	if table == TableAll {
		return errors.New("database: TableAll cannot be used for data operations")
	}
	if _, ok := table.Metadata(); !ok {
		return fmt.Errorf("database: unknown table code %d", table)
	}
	return nil
}
func encodeIntKey(key int) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], uint32(key))
	return encoded[:]
}
func encodeInt64Key(key int64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(key))
	return encoded[:]
}
func collectKeys(pairs []KeyValue) [][]byte {
	keys := make([][]byte, 0, len(pairs))
	for _, pair := range pairs {
		keys = append(keys, cloneBytes(pair.Key))
	}
	return keys
}
func collectValues(pairs []KeyValue) [][]byte {
	values := make([][]byte, 0, len(pairs))
	for _, pair := range pairs {
		values = append(values, cloneBytes(pair.Value))
	}
	return values
}

// positionPageIterator 定位分页起点 + 正向跳过 lastKey 反向读取 lastKey 之前的数据。
func positionPageIterator(iter KVIterator, table Table, upper []byte, lastKey []byte, reverse bool) bool {
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

// positionPrefixQueryIterator 定位范围查询首项 + 根据方向选择 First 或 Last。
func positionPrefixQueryIterator(iter KVIterator, reverse bool) bool {
	if reverse {
		return iter.Last()
	}
	return iter.First()
}

// advancePrefixQueryIterator 推进范围查询游标 + 根据方向选择 Next 或 Prev。
func advancePrefixQueryIterator(iter KVIterator, reverse bool) bool {
	if reverse {
		return iter.Prev()
	}
	return iter.Next()
}
