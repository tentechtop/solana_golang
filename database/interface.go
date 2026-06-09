package database

// DatabaseConfig 定义数据库配置 + 避免数据层依赖上层应用配置。
type DatabaseConfig struct {
	Path      string
	Engine    EngineType
	Username  string
	Password  string
	MaxSizeMB int
	WAL       bool
}

// EngineType 标识数据库引擎 + 便于运行期选择不同存储实现。
type EngineType string

const (
	// EnginePebble 默认引擎 + 兼容当前生产实现。
	EnginePebble EngineType = "pebble"
	// EngineLevelDB LevelDB 引擎 + 支持轻量级本地 KV 存储。
	EngineLevelDB EngineType = "leveldb"
)

// Table 标识逻辑 KV 表 + 隔离不同业务数据命名空间。
type Table uint16

const (
	// TableAll 标识全部表 + 仅用于缓存等全局操作。
	TableAll Table = 0

	TableAccount         Table = 1
	TableChain           Table = 2
	TableBlock           Table = 3
	TablePeer            Table = 4
	TableHeightToHash    Table = 5
	TableUTXO            Table = 6
	TableTxToBlock       Table = 7
	TableAddrToUTXO      Table = 8
	TableAddrToTx        Table = 9
	TableOrphan          Table = 10
	TableBlockHeader     Table = 11
	TableBlockBody       Table = 12
	TableHashToHeight    Table = 13
	TableHashToChainWork Table = 14
	TableCheckpoint      Table = 15

	TableTest1 Table = 100
	TableTest2 Table = 110
)

// TableMetadata 描述表元数据 + 供日志、兼容和物理存储映射使用。
type TableMetadata struct {
	Table            Table
	Code             uint16
	ColumnFamilyName string
	CacheSizeMB      int64
	CacheTTLSeconds  int64
}

var tableMetadata = []TableMetadata{
	{Table: TableAccount, Code: uint16(TableAccount), ColumnFamilyName: "account"},
	{Table: TableChain, Code: uint16(TableChain), ColumnFamilyName: "chain"},
	{Table: TableBlock, Code: uint16(TableBlock), ColumnFamilyName: "TBLOCK"},
	{Table: TablePeer, Code: uint16(TablePeer), ColumnFamilyName: "peer"},
	{Table: TableHeightToHash, Code: uint16(TableHeightToHash), ColumnFamilyName: "height_to_hash"},
	{Table: TableUTXO, Code: uint16(TableUTXO), ColumnFamilyName: "utxo"},
	{Table: TableTxToBlock, Code: uint16(TableTxToBlock), ColumnFamilyName: "tx_to_block"},
	{Table: TableAddrToUTXO, Code: uint16(TableAddrToUTXO), ColumnFamilyName: "addr_to_utxo"},
	{Table: TableAddrToTx, Code: uint16(TableAddrToTx), ColumnFamilyName: "addr_to_tx"},
	{Table: TableOrphan, Code: uint16(TableOrphan), ColumnFamilyName: "orphan"},
	{Table: TableBlockHeader, Code: uint16(TableBlockHeader), ColumnFamilyName: "TBLOCK_HEADER"},
	{Table: TableBlockBody, Code: uint16(TableBlockBody), ColumnFamilyName: "TBLOCK_BODY"},
	{Table: TableHashToHeight, Code: uint16(TableHashToHeight), ColumnFamilyName: "HASH_TO_HEIGHT"},
	{Table: TableHashToChainWork, Code: uint16(TableHashToChainWork), ColumnFamilyName: "HASH_TO_CHAIN_WORK"},
	{Table: TableCheckpoint, Code: uint16(TableCheckpoint), ColumnFamilyName: "CHECKPOINT"},
	{Table: TableTest1, Code: uint16(TableTest1), ColumnFamilyName: "height_to_hash"},
	{Table: TableTest2, Code: uint16(TableTest2), ColumnFamilyName: "height_to_hash"},
}

// Code 返回稳定表编码 + 用作底层 key 前缀。
func (t Table) Code() uint16 {
	return uint16(t)
}

// Metadata 返回表元数据 + 供调用方获取物理表信息。
func (t Table) Metadata() (TableMetadata, bool) {
	for _, metadata := range tableMetadata {
		if metadata.Table == t {
			return metadata, true
		}
	}
	return TableMetadata{}, false
}

// ColumnFamilyName 返回物理存储名 + 兼容列族风格命名。
func (t Table) ColumnFamilyName() string {
	metadata, ok := t.Metadata()
	if !ok {
		return ""
	}
	return metadata.ColumnFamilyName
}

// String 返回物理表名 + 便于日志和调试输出。
func (t Table) String() string {
	return t.ColumnFamilyName()
}

// TableByCode 根据稳定编码查表 + 支持持久化 key 反查。
func TableByCode(code uint16) (Table, bool) {
	for _, metadata := range tableMetadata {
		if metadata.Code == code {
			return metadata.Table, true
		}
	}
	return 0, false
}

// AllTableMetadata 返回所有表元数据 + 保持声明顺序稳定。
func AllTableMetadata() []TableMetadata {
	tables := make([]TableMetadata, len(tableMetadata))
	copy(tables, tableMetadata)
	return tables
}

// PageResult 表示游标分页结果 + 支持大表分批扫描。
type PageResult struct {
	Data       [][]byte
	LastKey    []byte
	IsLastPage bool
}

// KeyValue 表示原始键值对 + 用于范围和前缀查询结果。
type KeyValue struct {
	Key   []byte
	Value []byte
}

// KeyValueHandler 处理迭代项 + 返回 false 可提前停止扫描。
type KeyValueHandler func(key []byte, value []byte) bool

// OperationType 标识写操作类型 + 用于批量事务执行。
type OperationType uint8

const (
	OperationInsert OperationType = iota + 1
	OperationUpdate
	OperationDelete
)

// DBOperation 描述单个写操作 + 支持批量和事务提交。
type DBOperation struct {
	Table Table
	Key   []byte
	Value []byte
	Type  OperationType
}

func NewInsertOperation(table Table, key []byte, value []byte) DBOperation {
	return DBOperation{Table: table, Key: key, Value: value, Type: OperationInsert}
}

func NewUpdateOperation(table Table, key []byte, value []byte) DBOperation {
	return DBOperation{Table: table, Key: key, Value: value, Type: OperationUpdate}
}

func NewDeleteOperation(table Table, key []byte) DBOperation {
	return DBOperation{Table: table, Key: key, Type: OperationDelete}
}

// DbOperation 保留 Java 风格别名 + 兼容迁移期调用方。
type DbOperation = DBOperation

// Database 定义链上本地 KV 契约 + 屏蔽具体存储引擎差异。
type Database interface {
	CreateDatabase(config DatabaseConfig) error
	CloseDatabase() error

	Exists(table Table, key []byte) (bool, error)
	Insert(table Table, key []byte, value []byte) error
	Delete(table Table, key []byte) error
	Update(table Table, key []byte, value []byte) error
	Get(table Table, key []byte) ([]byte, error)
	GetInt(table Table, key int) ([]byte, error)
	GetInt64(table Table, key int64) ([]byte, error)
	Count(table Table) (int, error)
	CountByPrefix(table Table, prefix []byte) (int, error)
	IsEmpty(table Table) (bool, error)

	BatchInsert(table Table, keys [][]byte, values [][]byte) error
	BatchDelete(table Table, keys [][]byte) error
	BatchUpdate(table Table, keys [][]byte, values [][]byte) error
	BatchGet(table Table, keys [][]byte) ([][]byte, error)

	Close() error
	Flush() error
	Compact(start []byte, limit []byte) error
	Checkpoint(destDir string) error

	Page(table Table, pageSize int, lastKey []byte) (PageResult, error)
	PageByPrefix(table Table, prefix []byte, pageSize int, lastKey []byte) (PageResult, error)
	PageKey(table Table, pageSize int, lastKey []byte) (PageResult, error)
	PageKeyByPrefix(table Table, prefix []byte, pageSize int, lastKey []byte) (PageResult, error)
	PageKeyByPrefixReverse(table Table, prefix []byte, pageSize int, lastKey []byte) (PageResult, error)
	ExistsByPrefix(table Table, prefix []byte) (bool, error)

	DataTransaction(operations []DBOperation) error
	PrefixQuery(table Table, prefix []byte) ([]KeyValue, error)
	PrefixQueryWithLimit(table Table, prefix []byte, limit int) ([]KeyValue, error)
	PrefixQueryReverse(table Table, prefix []byte) ([]KeyValue, error)
	PrefixQueryReverseWithLimit(table Table, prefix []byte, limit int) ([]KeyValue, error)
	RangeQuery(table Table, startKey []byte, endKey []byte) ([]KeyValue, error)
	RangeQueryWithLimit(table Table, startKey []byte, endKey []byte, limit int) ([]KeyValue, error)
	RangeQueryReverse(table Table, startKey []byte, endKey []byte) ([]KeyValue, error)
	RangeQueryReverseWithLimit(table Table, startKey []byte, endKey []byte, limit int) ([]KeyValue, error)
	First(table Table) (*KeyValue, error)
	Last(table Table) (*KeyValue, error)
	FirstByPrefix(table Table, prefix []byte) (*KeyValue, error)
	LastByPrefix(table Table, prefix []byte) (*KeyValue, error)

	ClearCache(table Table) error
	SetCachePolicy(table Table, ttlMillis int64, maxSize int) error
	RefreshCache(table Table, key []byte) error

	BeginTransaction() (string, error)
	CommitTransaction(transactionID string) error
	RollbackTransaction(transactionID string) error
	AddToTransaction(transactionID string, operation DBOperation) error

	ListAllTables() ([]string, error)
	CheckHealth() error
	Iterate(table Table, handler KeyValueHandler) error
	IterateByPrefix(table Table, prefix []byte, handler KeyValueHandler) error
	BatchDeleteRange(table Table, startKey []byte, endKey []byte) error
	DeleteByPrefix(table Table, prefix []byte) error
	EnableWAL(enable bool) error
}

// DataBase 保留 Java 风格接口名 + 兼容迁移期调用方。
type DataBase = Database
