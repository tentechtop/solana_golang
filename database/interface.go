package database

// DatabaseConfig 定义数据库配置 + 用于解耦存储引擎初始化参数。
type DatabaseConfig struct {
	Path      string
	Engine    EngineType
	Username  string
	Password  string
	MaxSizeMB int
	WAL       bool
}

// EngineType 标识数据库引擎 + 用于运行期选择不同存储实现。
type EngineType string

const (
	// EnginePebble 标识 Pebble 引擎 + 兼容当前生产默认存储实现。
	EnginePebble EngineType = "pebble"
	// EngineLevelDB 标识 LevelDB 引擎 + 支持轻量级本地 KV 存储。
	EngineLevelDB EngineType = "leveldb"
)

// Table 标识逻辑 KV 表 + 用于隔离不同业务数据命名空间。
type Table uint16

const (
	// TableAll 标识全部表 + 仅用于缓存等全局操作。
	TableAll Table = 0

	// TableAccount 存储账户数据 + 用于维护账户状态。
	TableAccount Table = 1
	// TableChain 存储链数据 + 用于维护主链元信息。
	TableChain Table = 2
	// TableBlock 存储区块数据 + 用于按哈希读取完整区块。
	TableBlock Table = 3
	// TablePeer 存储节点数据 + 用于维护 P2P 节点信息。
	TablePeer Table = 4
	// TableHeightToHash 存储高度到哈希映射 + 用于按高度定位区块。
	TableHeightToHash Table = 5
	// TableUTXO 存储未花费输出 + 用于交易校验和余额查询。
	TableUTXO Table = 6
	// TableTxToBlock 存储交易到区块映射 + 用于按交易反查区块。
	TableTxToBlock Table = 7
	// TableAddrToUTXO 存储地址到 UTXO 映射 + 用于地址余额扫描。
	TableAddrToUTXO Table = 8
	// TableAddrToTx 存储地址到交易映射 + 用于地址交易历史查询。
	TableAddrToTx Table = 9
	// TableOrphan 存储孤块数据 + 用于链重组前的临时管理。
	TableOrphan Table = 10
	// TableBlockHeader 存储区块头 + 用于轻量级链校验。
	TableBlockHeader Table = 11
	// TableBlockBody 存储区块体 + 用于按需加载交易内容。
	TableBlockBody Table = 12
	// TableHashToHeight 存储哈希到高度映射 + 用于区块索引反查。
	TableHashToHeight Table = 13
	// TableHashToChainWork 存储哈希到链工作量映射 + 用于最佳链选择。
	TableHashToChainWork Table = 14
	// TableCheckpoint 存储检查点 + 用于快速恢复可信链状态。
	TableCheckpoint Table = 15

	// TableTest1 标识测试表一 + 用于数据库测试隔离。
	TableTest1 Table = 100
	// TableTest2 标识测试表二 + 用于数据库测试隔离。
	TableTest2 Table = 110
)

// TableMetadata 描述表元数据 + 用于日志、兼容和物理存储映射。
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

// Metadata 返回表元数据 + 用于调用方获取物理表信息。
func (t Table) Metadata() (TableMetadata, bool) {
	for _, metadata := range tableMetadata {
		if metadata.Table == t {
			return metadata, true
		}
	}
	return TableMetadata{}, false
}

// ColumnFamilyName 返回物理存储名 + 用于兼容列族风格命名。
func (t Table) ColumnFamilyName() string {
	metadata, ok := t.Metadata()
	if !ok {
		return ""
	}
	return metadata.ColumnFamilyName
}

// String 返回物理表名 + 用于日志和调试输出。
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

// AllTableMetadata 返回全部表元数据 + 保持声明顺序稳定。
func AllTableMetadata() []TableMetadata {
	tables := make([]TableMetadata, len(tableMetadata))
	copy(tables, tableMetadata)
	return tables
}

// PageResult 表示游标分页结果 + 用于大表分批扫描。
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
	// OperationInsert 标识插入操作 + 用于批量写入新键值。
	OperationInsert OperationType = iota + 1
	// OperationUpdate 标识更新操作 + 用于批量覆盖已有键值。
	OperationUpdate
	// OperationDelete 标识删除操作 + 用于批量移除已有键值。
	OperationDelete
)

// DBOperation 描述单个写操作 + 用于批量和事务提交。
type DBOperation struct {
	Table Table
	Key   []byte
	Value []byte
	Type  OperationType
}

// NewInsertOperation 创建插入操作 + 用于统一构造事务写入项。
func NewInsertOperation(table Table, key []byte, value []byte) DBOperation {
	return DBOperation{Table: table, Key: key, Value: value, Type: OperationInsert}
}

// NewUpdateOperation 创建更新操作 + 用于统一构造事务覆盖项。
func NewUpdateOperation(table Table, key []byte, value []byte) DBOperation {
	return DBOperation{Table: table, Key: key, Value: value, Type: OperationUpdate}
}

// NewDeleteOperation 创建删除操作 + 用于统一构造事务删除项。
func NewDeleteOperation(table Table, key []byte) DBOperation {
	return DBOperation{Table: table, Key: key, Type: OperationDelete}
}

// Database 定义链上本地 KV 契约 + 用于屏蔽具体存储引擎差异。
type Database interface {
	// CreateDatabase 创建数据库实例 + 用于按配置初始化存储引擎。
	CreateDatabase(config DatabaseConfig) error
	// CloseDatabase 关闭数据库实例 + 用于释放底层文件和句柄资源。
	CloseDatabase() error

	// Exists 判断字节键是否存在 + 用于避免无效读写。
	Exists(table Table, key []byte) (bool, error)
	// Put 写入字节键值 + 用于新增或覆盖存储项。
	Put(table Table, key []byte, value []byte) error
	// Insert 插入字节键值 + 用于只允许新增的写入场景。
	Insert(table Table, key []byte, value []byte) error
	// Delete 删除字节键值 + 用于移除指定存储项。
	Delete(table Table, key []byte) error
	// Update 更新字节键值 + 用于只允许覆盖已有数据的场景。
	Update(table Table, key []byte, value []byte) error
	// Get 读取字节键值 + 用于按原始 key 查询数据。
	Get(table Table, key []byte) ([]byte, error)
	// ExistsInt 判断整数键是否存在 + 用于兼容数字索引访问。
	ExistsInt(table Table, key int) (bool, error)
	// ExistsInt64 判断 64 位整数键是否存在 + 用于兼容高度等大数字索引。
	ExistsInt64(table Table, key int64) (bool, error)
	// PutInt 写入整数键值 + 用于数字索引新增或覆盖。
	PutInt(table Table, key int, value []byte) error
	// PutInt64 写入 64 位整数键值 + 用于高度等大数字索引写入。
	PutInt64(table Table, key int64, value []byte) error
	// UpdateInt 更新整数键值 + 用于数字索引覆盖已有数据。
	UpdateInt(table Table, key int, value []byte) error
	// UpdateInt64 更新 64 位整数键值 + 用于高度等大数字索引覆盖。
	UpdateInt64(table Table, key int64, value []byte) error
	// DeleteInt 删除整数键值 + 用于移除数字索引数据。
	DeleteInt(table Table, key int) error
	// DeleteInt64 删除 64 位整数键值 + 用于移除高度等大数字索引数据。
	DeleteInt64(table Table, key int64) error
	// GetInt 读取整数键值 + 用于数字索引查询。
	GetInt(table Table, key int) ([]byte, error)
	// GetInt64 读取 64 位整数键值 + 用于高度等大数字索引查询。
	GetInt64(table Table, key int64) ([]byte, error)
	// Count 统计表记录数 + 用于容量评估和健康检查。
	Count(table Table) (int, error)
	// CountByPrefix 统计前缀记录数 + 用于索引分组计数。
	CountByPrefix(table Table, prefix []byte) (int, error)
	// IsEmpty 判断表是否为空 + 用于初始化和清理校验。
	IsEmpty(table Table) (bool, error)

	// BatchInsert 批量插入键值 + 用于降低多次写入开销。
	BatchInsert(table Table, keys [][]byte, values [][]byte) error
	// BatchDelete 批量删除键值 + 用于降低多次删除开销。
	BatchDelete(table Table, keys [][]byte) error
	// BatchUpdate 批量更新键值 + 用于降低多次覆盖开销。
	BatchUpdate(table Table, keys [][]byte, values [][]byte) error
	// BatchGet 批量读取键值 + 用于降低多次查询开销。
	BatchGet(table Table, keys [][]byte) ([][]byte, error)

	// Close 关闭当前连接 + 用于释放调用方持有的数据库资源。
	Close() error
	// Flush 刷盘内存数据 + 用于确保关键写入持久化。
	Flush() error
	// Compact 压缩指定 key 范围 + 用于回收空间并优化读性能。
	Compact(start []byte, limit []byte) error
	// Checkpoint 创建数据库检查点 + 用于备份和快速恢复。
	Checkpoint(destDir string) error

	// Page 分页读取值 + 用于无前缀的大表顺序扫描。
	Page(table Table, pageSize int, lastKey []byte) (PageResult, error)
	// PageByPrefix 按前缀分页读取值 + 用于索引分组顺序扫描。
	PageByPrefix(table Table, prefix []byte, pageSize int, lastKey []byte) (PageResult, error)
	// PageKey 分页读取键 + 用于只需要 key 的顺序扫描。
	PageKey(table Table, pageSize int, lastKey []byte) (PageResult, error)
	// PageKeyByPrefix 按前缀分页读取键 + 用于索引分组 key 扫描。
	PageKeyByPrefix(table Table, prefix []byte, pageSize int, lastKey []byte) (PageResult, error)
	// PageKeyByPrefixReverse 按前缀倒序分页读取键 + 用于最新数据优先扫描。
	PageKeyByPrefixReverse(table Table, prefix []byte, pageSize int, lastKey []byte) (PageResult, error)
	// ExistsByPrefix 判断前缀是否存在 + 用于快速确认索引分组是否有数据。
	ExistsByPrefix(table Table, prefix []byte) (bool, error)

	// DataTransaction 执行批量事务 + 用于保证多项写操作原子提交。
	DataTransaction(operations []DBOperation) error
	// PrefixQuery 查询前缀键值 + 用于读取同一索引分组全部数据。
	PrefixQuery(table Table, prefix []byte) ([]KeyValue, error)
	// PrefixQueryWithLimit 限量查询前缀键值 + 用于控制扫描成本。
	PrefixQueryWithLimit(table Table, prefix []byte, limit int) ([]KeyValue, error)
	// PrefixQueryReverse 倒序查询前缀键值 + 用于最新数据优先读取。
	PrefixQueryReverse(table Table, prefix []byte) ([]KeyValue, error)
	// PrefixQueryReverseWithLimit 限量倒序查询前缀键值 + 用于控制逆序扫描成本。
	PrefixQueryReverseWithLimit(table Table, prefix []byte, limit int) ([]KeyValue, error)
	// RangeQuery 查询范围键值 + 用于读取有序 key 区间数据。
	RangeQuery(table Table, startKey []byte, endKey []byte) ([]KeyValue, error)
	// RangeQueryWithLimit 限量查询范围键值 + 用于控制区间扫描成本。
	RangeQueryWithLimit(table Table, startKey []byte, endKey []byte, limit int) ([]KeyValue, error)
	// RangeQueryReverse 倒序查询范围键值 + 用于从区间末尾读取数据。
	RangeQueryReverse(table Table, startKey []byte, endKey []byte) ([]KeyValue, error)
	// RangeQueryReverseWithLimit 限量倒序查询范围键值 + 用于控制逆序区间扫描成本。
	RangeQueryReverseWithLimit(table Table, startKey []byte, endKey []byte, limit int) ([]KeyValue, error)
	// First 读取表首个键值 + 用于获取最小 key 数据。
	First(table Table) (*KeyValue, error)
	// Last 读取表最后键值 + 用于获取最大 key 数据。
	Last(table Table) (*KeyValue, error)
	// FirstByPrefix 读取前缀首个键值 + 用于获取分组最小 key 数据。
	FirstByPrefix(table Table, prefix []byte) (*KeyValue, error)
	// LastByPrefix 读取前缀最后键值 + 用于获取分组最大 key 数据。
	LastByPrefix(table Table, prefix []byte) (*KeyValue, error)
	// Keys 读取全部键 + 用于只关心索引 key 的场景。
	Keys(table Table) ([][]byte, error)
	// KeysByPrefix 按前缀读取键 + 用于只关心分组索引 key 的场景。
	KeysByPrefix(table Table, prefix []byte) ([][]byte, error)
	// Values 读取全部值 + 用于只关心表数据内容的场景。
	Values(table Table) ([][]byte, error)
	// ValuesByPrefix 按前缀读取值 + 用于只关心分组数据内容的场景。
	ValuesByPrefix(table Table, prefix []byte) ([][]byte, error)

	// ClearCache 清理表缓存 + 用于强制释放或刷新热点数据。
	ClearCache(table Table) error
	// SetCachePolicy 设置表缓存策略 + 用于控制 TTL 和容量。
	SetCachePolicy(table Table, ttlMillis int64, maxSize int) error
	// RefreshCache 刷新指定缓存项 + 用于让热点数据保持最新。
	RefreshCache(table Table, key []byte) error

	// BeginTransaction 开启事务 + 用于聚合多项写操作。
	BeginTransaction() (string, error)
	// CommitTransaction 提交事务 + 用于原子持久化已加入操作。
	CommitTransaction(transactionID string) error
	// RollbackTransaction 回滚事务 + 用于放弃未提交操作。
	RollbackTransaction(transactionID string) error
	// AddToTransaction 添加事务操作 + 用于延迟批量提交。
	AddToTransaction(transactionID string, operation DBOperation) error

	// ListAllTables 列出物理表名 + 用于运维检查和调试。
	ListAllTables() ([]string, error)
	// CheckHealth 检查数据库健康状态 + 用于启动和运行期探活。
	CheckHealth() error
	// Iterate 遍历表键值 + 用于流式处理全表数据。
	Iterate(table Table, handler KeyValueHandler) error
	// IterateByPrefix 按前缀遍历键值 + 用于流式处理分组数据。
	IterateByPrefix(table Table, prefix []byte, handler KeyValueHandler) error
	// BatchDeleteRange 批量删除范围数据 + 用于高效清理连续 key 区间。
	BatchDeleteRange(table Table, startKey []byte, endKey []byte) error
	// DeleteByPrefix 删除前缀数据 + 用于高效清理索引分组。
	DeleteByPrefix(table Table, prefix []byte) error
	// ClearTable 清空表数据 + 用于测试和运维清理。
	ClearTable(table Table) error
	// EnableWAL 切换预写日志 + 用于在性能和持久性之间选择。
	EnableWAL(enable bool) error
}
