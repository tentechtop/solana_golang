package database

// DatabaseConfig contains the database-related part of the system config.
// It intentionally stays small so upper layers can adapt their own config
// structs without forcing the database package to import application config.
type DatabaseConfig struct {
	Path      string
	Username  string
	Password  string
	MaxSizeMB int
}

// Table identifies one logical KV table/column family.
type Table uint16

const (
	// TableAll is a sentinel used by cache operations that target every table.
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

// TableMetadata mirrors the Java TableEnum metadata that is useful outside
// the concrete database implementation.
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

// Code returns the stable numeric table code.
func (t Table) Code() uint16 {
	return uint16(t)
}

// Metadata returns the metadata bound to this table.
func (t Table) Metadata() (TableMetadata, bool) {
	for _, metadata := range tableMetadata {
		if metadata.Table == t {
			return metadata, true
		}
	}
	return TableMetadata{}, false
}

// ColumnFamilyName returns the physical storage name for the table.
func (t Table) ColumnFamilyName() string {
	metadata, ok := t.Metadata()
	if !ok {
		return ""
	}
	return metadata.ColumnFamilyName
}

// String returns the physical table/column-family name.
func (t Table) String() string {
	return t.ColumnFamilyName()
}

// TableByCode returns the table for a stable numeric code.
func TableByCode(code uint16) (Table, bool) {
	for _, metadata := range tableMetadata {
		if metadata.Code == code {
			return metadata.Table, true
		}
	}
	return 0, false
}

// AllTableMetadata returns all known table metadata in declaration order.
func AllTableMetadata() []TableMetadata {
	tables := make([]TableMetadata, len(tableMetadata))
	copy(tables, tableMetadata)
	return tables
}

// PageResult is the result of cursor-style pagination over raw values.
type PageResult struct {
	Data       [][]byte
	LastKey    []byte
	IsLastPage bool
}

// KeyValue is a raw key/value pair returned by range scans.
type KeyValue struct {
	Key   []byte
	Value []byte
}

// KeyValueHandler handles one iterator item. Returning false stops iteration.
type KeyValueHandler func(key []byte, value []byte) bool

// OperationType identifies one write operation inside a transaction.
type OperationType uint8

const (
	OperationInsert OperationType = iota + 1
	OperationUpdate
	OperationDelete
)

// DBOperation describes one operation inside a batch or transaction.
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

// DbOperation keeps the Java-style name available for callers that prefer it.
type DbOperation = DBOperation

// Database defines the KV database contract used by the chain.
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

// DataBase keeps the Java interface spelling available while Database remains
// the Go-preferred name.
type DataBase = Database
