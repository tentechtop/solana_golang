package database

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	migrationStatusRunning = "running"
	migrationStatusApplied = "applied"
	migrationStatusFailed  = "failed"
	migrationLockTTL       = 30 * time.Minute
)

var (
	migrationSystemPrefix      = []byte{0, 0, 'm', 'i', 'g', ':'}
	migrationCurrentVersionKey = append(cloneBytes(migrationSystemPrefix), []byte("current_version")...)
	migrationLockKey           = append(cloneBytes(migrationSystemPrefix), []byte("lock")...)
	migrationHistoryPrefix     = append(cloneBytes(migrationSystemPrefix), []byte("history:")...)

	defaultMigrationMu sync.RWMutex
	defaultMigrations  []Migration
)

// MigrationReader 定义迁移只读视图 + 限制迁移规划阶段直接写库造成半提交。
type MigrationReader interface {
	Get(table Table, key []byte) ([]byte, error)
	Exists(table Table, key []byte) (bool, error)
	Count(table Table) (int, error)
	PrefixQuery(table Table, prefix []byte) ([]KeyValue, error)
	RangeQuery(table Table, startKey []byte, endKey []byte) ([]KeyValue, error)
}

// MigrationContext 描述迁移执行上下文 + 传递版本和只读数据库能力。
type MigrationContext struct {
	Reader        MigrationReader
	FromVersion   uint64
	TargetVersion uint64
	StartedAt     time.Time
}

// MigrationPlanFunc 生成迁移写入计划 + 由框架统一原子提交业务写入和元数据。
type MigrationPlanFunc func(context MigrationContext) ([]DBOperation, error)

// Migration 定义单个 schema 迁移 + 版本单调递增保证可审计执行顺序。
type Migration struct {
	Version uint64
	Name    string
	Plan    MigrationPlanFunc
}

// MigrationRecord 记录迁移状态 + 用于启动审计和失败排查。
type MigrationRecord struct {
	Version        uint64 `json:"version"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	Checksum       string `json:"checksum"`
	StartedAtUnix  int64  `json:"started_at_unix"`
	FinishedAtUnix int64  `json:"finished_at_unix"`
	Error          string `json:"error,omitempty"`
	CheckpointPath string `json:"checkpoint_path,omitempty"`
}

type migrationLockRecord struct {
	Owner       string `json:"owner"`
	StartedAt   int64  `json:"started_at"`
	ExpiresAt   int64  `json:"expires_at"`
	ProcessID   int    `json:"process_id"`
	Description string `json:"description"`
}

// RegisterDatabaseMigration 注册全局迁移 + 用于 NewDatabase 启动时自动执行。
func RegisterDatabaseMigration(migration Migration) error {
	if err := validateMigration(migration); err != nil {
		return err
	}
	defaultMigrationMu.Lock()
	defer defaultMigrationMu.Unlock()
	return appendUniqueMigrationLocked(&defaultMigrations, migration)
}

// RegisterMigration 注册实例迁移 + 用于测试和按实例定制升级链。
func (p *databaseService) RegisterMigration(migration Migration) error {
	if err := validateMigration(migration); err != nil {
		return err
	}
	p.migrationMu.Lock()
	defer p.migrationMu.Unlock()
	return appendUniqueMigrationLocked(&p.migrations, migration)
}

// Migrate 执行所有待迁移版本 + 按版本顺序保证幂等升级。
func (p *databaseService) Migrate() error {
	engine, err := p.getDB()
	if err != nil {
		return err
	}
	p.migrationMu.Lock()
	defer p.migrationMu.Unlock()

	migrations, err := p.collectMigrations()
	if err != nil {
		return err
	}
	if len(migrations) == 0 {
		return nil
	}

	if err := p.acquireMigrationLock(engine); err != nil {
		return err
	}
	lockReleased := false
	defer func() {
		if !lockReleased {
			_ = deleteRawKey(engine, migrationLockKey)
		}
	}()

	currentVersion, err := readSchemaVersion(engine)
	if err != nil {
		return err
	}
	if err := verifyAppliedMigrations(engine, migrations, currentVersion); err != nil {
		return err
	}
	for _, migration := range migrations {
		if migration.Version <= currentVersion {
			continue
		}
		if err := p.applyMigration(engine, currentVersion, migration); err != nil {
			return err
		}
		currentVersion = migration.Version
	}
	if err := deleteRawKey(engine, migrationLockKey); err != nil {
		return err
	}
	lockReleased = true
	return nil
}

// SchemaVersion 读取当前 schema 版本 + 缺省版本返回 0 便于首次初始化。
func (p *databaseService) SchemaVersion() (uint64, error) {
	engine, err := p.getDB()
	if err != nil {
		return 0, err
	}
	return readSchemaVersion(engine)
}

// MigrationHistory 读取迁移历史 + 按版本升序返回审计记录。
func (p *databaseService) MigrationHistory() ([]MigrationRecord, error) {
	engine, err := p.getDB()
	if err != nil {
		return nil, err
	}
	upper := keySuccessor(migrationHistoryPrefix)
	iter, err := engine.NewIterator(migrationHistoryPrefix, upper)
	if err != nil {
		return nil, fmt.Errorf("database: create migration history iterator: %w", err)
	}
	defer iter.Close()

	records := make([]MigrationRecord, 0)
	for ok := iter.First(); ok; ok = iter.Next() {
		record, err := decodeMigrationRecord(iter.Value())
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("database: migration history iterator: %w", err)
	}
	sort.Slice(records, func(i int, j int) bool {
		return records[i].Version < records[j].Version
	})
	return records, nil
}

func (p *databaseService) collectMigrations() ([]Migration, error) {
	defaultMigrationMu.RLock()
	migrations := make([]Migration, 0, len(defaultMigrations)+len(p.migrations))
	migrations = append(migrations, defaultMigrations...)
	defaultMigrationMu.RUnlock()
	migrations = append(migrations, p.migrations...)
	return normalizeMigrations(migrations)
}

func (p *databaseService) applyMigration(engine KVEngine, fromVersion uint64, migration Migration) error {
	startedAt := time.Now().UTC()
	checkpointPath, err := p.createMigrationCheckpoint(engine, migration.Version, startedAt)
	if err != nil {
		return err
	}
	runningRecord := newMigrationRecord(migration, migrationStatusRunning, startedAt, 0, "", checkpointPath)
	if err := writeMigrationRecord(engine, runningRecord); err != nil {
		return err
	}

	readTransaction, err := p.BeginReadTransaction()
	if err != nil {
		return p.markMigrationFailed(engine, migration, startedAt, checkpointPath, err)
	}
	defer readTransaction.Close()

	context := MigrationContext{
		Reader:        readTransaction,
		FromVersion:   fromVersion,
		TargetVersion: migration.Version,
		StartedAt:     startedAt,
	}
	operations, err := migration.Plan(context)
	if err != nil {
		return p.markMigrationFailed(engine, migration, startedAt, checkpointPath, err)
	}
	if err := validateMigrationOperations(operations); err != nil {
		return p.markMigrationFailed(engine, migration, startedAt, checkpointPath, err)
	}
	if err := commitMigration(engine, migration, operations, startedAt, checkpointPath); err != nil {
		return p.markMigrationFailed(engine, migration, startedAt, checkpointPath, err)
	}
	p.applyCacheOperations(operations)
	return nil
}

func (p *databaseService) createMigrationCheckpoint(engine KVEngine, version uint64, startedAt time.Time) (string, error) {
	if !engine.SupportsCheckpoint() {
		return "", nil
	}
	p.mu.RLock()
	root := p.path
	p.mu.RUnlock()
	if root == "" {
		return "", ErrDatabaseNotOpen
	}
	name := fmt.Sprintf("%020d_%d", version, startedAt.UnixNano())
	checkpointRoot := filepath.Join(filepath.Dir(root), filepath.Base(root)+"_migration_checkpoints")
	checkpointPath := filepath.Join(checkpointRoot, name)
	if err := engine.Checkpoint(checkpointPath); err != nil {
		return "", fmt.Errorf("database: create migration checkpoint: %w", err)
	}
	return checkpointPath, nil
}

func (p *databaseService) markMigrationFailed(engine KVEngine, migration Migration, startedAt time.Time, checkpointPath string, cause error) error {
	record := newMigrationRecord(migration, migrationStatusFailed, startedAt, time.Now().UTC().Unix(), cause.Error(), checkpointPath)
	if err := writeMigrationRecord(engine, record); err != nil {
		return fmt.Errorf("database: migration %d failed: %w; mark failed: %v", migration.Version, cause, err)
	}
	return fmt.Errorf("database: migration %d failed: %w", migration.Version, cause)
}

func (p *databaseService) acquireMigrationLock(engine KVEngine) error {
	value, err := readRawSystemKey(engine, migrationLockKey)
	if err != nil {
		return err
	}
	if len(value) > 0 {
		if !isMigrationLockStale(value, time.Now().UTC()) {
			return ErrMigrationLocked
		}
	}
	record, err := newMigrationLockRecord()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("database: encode migration lock: %w", err)
	}
	return writeRawKey(engine, migrationLockKey, encoded)
}

func commitMigration(engine KVEngine, migration Migration, operations []DBOperation, startedAt time.Time, checkpointPath string) error {
	batch, err := engine.NewBatch()
	if err != nil {
		return fmt.Errorf("database: create migration batch: %w", err)
	}
	defer batch.Close()
	for _, operation := range operations {
		if err := applyOperationToBatch(batch, cloneOperation(operation)); err != nil {
			return err
		}
	}
	versionValue := make([]byte, 8)
	binary.BigEndian.PutUint64(versionValue, migration.Version)
	if err := batch.Set(migrationCurrentVersionKey, versionValue); err != nil {
		return fmt.Errorf("database: set schema version: %w", err)
	}
	record := newMigrationRecord(migration, migrationStatusApplied, startedAt, time.Now().UTC().Unix(), "", checkpointPath)
	encodedRecord, err := encodeMigrationRecord(record)
	if err != nil {
		return err
	}
	if err := batch.Set(migrationHistoryKey(migration.Version), encodedRecord); err != nil {
		return fmt.Errorf("database: set migration history: %w", err)
	}
	if err := batch.Commit(); err != nil {
		return fmt.Errorf("database: commit migration batch: %w", err)
	}
	return nil
}

func writeMigrationRecord(engine KVEngine, record MigrationRecord) error {
	encoded, err := encodeMigrationRecord(record)
	if err != nil {
		return err
	}
	return writeRawKey(engine, migrationHistoryKey(record.Version), encoded)
}

func readMigrationRecord(engine KVEngine, version uint64) (MigrationRecord, bool, error) {
	value, err := readRawSystemKey(engine, migrationHistoryKey(version))
	if err != nil {
		return MigrationRecord{}, false, err
	}
	if len(value) == 0 {
		return MigrationRecord{}, false, nil
	}
	record, err := decodeMigrationRecord(value)
	if err != nil {
		return MigrationRecord{}, false, err
	}
	return record, true, nil
}

func readSchemaVersion(engine KVEngine) (uint64, error) {
	value, err := readRawSystemKey(engine, migrationCurrentVersionKey)
	if err != nil {
		return 0, err
	}
	if len(value) == 0 {
		return 0, nil
	}
	if len(value) != 8 {
		return 0, fmt.Errorf("database: invalid schema version length %d", len(value))
	}
	return binary.BigEndian.Uint64(value), nil
}

func writeRawKey(engine KVEngine, key []byte, value []byte) error {
	if err := engine.Set(key, value); err != nil {
		return fmt.Errorf("database: write migration metadata: %w", err)
	}
	return nil
}

func deleteRawKey(engine KVEngine, key []byte) error {
	if err := engine.Delete(key); err != nil && !engine.IsNotFound(err) {
		return fmt.Errorf("database: delete migration metadata: %w", err)
	}
	return nil
}

func readRawSystemKey(engine KVEngine, key []byte) ([]byte, error) {
	value, err := engine.Get(key)
	if engine.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("database: read migration metadata: %w", err)
	}
	return cloneBytes(value), nil
}

func validateMigration(migration Migration) error {
	if migration.Version == 0 {
		return errors.New("database: migration version must be greater than zero")
	}
	if migration.Name == "" {
		return errors.New("database: migration name is empty")
	}
	if migration.Plan == nil {
		return errors.New("database: migration plan is nil")
	}
	return nil
}

func validateMigrationOperations(operations []DBOperation) error {
	for _, operation := range operations {
		if err := validateOperation(operation); err != nil {
			return err
		}
	}
	return nil
}

func normalizeMigrations(migrations []Migration) ([]Migration, error) {
	copied := append([]Migration(nil), migrations...)
	sort.Slice(copied, func(i int, j int) bool {
		return copied[i].Version < copied[j].Version
	})
	for index, migration := range copied {
		if err := validateMigration(migration); err != nil {
			return nil, err
		}
		if index > 0 && copied[index-1].Version == migration.Version {
			return nil, fmt.Errorf("database: duplicate migration version %d", migration.Version)
		}
	}
	return copied, nil
}

func verifyAppliedMigrations(engine KVEngine, migrations []Migration, currentVersion uint64) error {
	for _, migration := range migrations {
		if migration.Version > currentVersion {
			continue
		}
		record, ok, err := readMigrationRecord(engine, migration.Version)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("database: applied migration %d history is missing", migration.Version)
		}
		if record.Status != migrationStatusApplied {
			return fmt.Errorf("database: applied migration %d status is %s", migration.Version, record.Status)
		}
		if record.Checksum != migrationChecksum(migration) {
			return fmt.Errorf("database: applied migration %d checksum mismatch", migration.Version)
		}
	}
	return nil
}

func appendUniqueMigrationLocked(migrations *[]Migration, migration Migration) error {
	for _, existing := range *migrations {
		if existing.Version == migration.Version {
			return fmt.Errorf("database: duplicate migration version %d", migration.Version)
		}
	}
	*migrations = append(*migrations, migration)
	return nil
}

func newMigrationRecord(migration Migration, status string, startedAt time.Time, finishedAtUnix int64, errorMessage string, checkpointPath string) MigrationRecord {
	return MigrationRecord{
		Version:        migration.Version,
		Name:           migration.Name,
		Status:         status,
		Checksum:       migrationChecksum(migration),
		StartedAtUnix:  startedAt.Unix(),
		FinishedAtUnix: finishedAtUnix,
		Error:          errorMessage,
		CheckpointPath: checkpointPath,
	}
}

func encodeMigrationRecord(record MigrationRecord) ([]byte, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("database: encode migration record: %w", err)
	}
	return encoded, nil
}

func decodeMigrationRecord(value []byte) (MigrationRecord, error) {
	var record MigrationRecord
	if err := json.Unmarshal(value, &record); err != nil {
		return MigrationRecord{}, fmt.Errorf("database: decode migration record: %w", err)
	}
	return record, nil
}

func migrationHistoryKey(version uint64) []byte {
	key := make([]byte, len(migrationHistoryPrefix)+8)
	copy(key, migrationHistoryPrefix)
	binary.BigEndian.PutUint64(key[len(migrationHistoryPrefix):], version)
	return key
}

func migrationChecksum(migration Migration) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%020d:%s", migration.Version, migration.Name)))
	return hex.EncodeToString(hash[:])
}

func newMigrationLockRecord() (migrationLockRecord, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return migrationLockRecord{}, fmt.Errorf("database: read hostname for migration lock: %w", err)
	}
	now := time.Now().UTC()
	return migrationLockRecord{
		Owner:       hostname,
		StartedAt:   now.Unix(),
		ExpiresAt:   now.Add(migrationLockTTL).Unix(),
		ProcessID:   os.Getpid(),
		Description: "schema migration",
	}, nil
}

func isMigrationLockStale(value []byte, now time.Time) bool {
	var record migrationLockRecord
	if err := json.Unmarshal(value, &record); err != nil {
		return false
	}
	return record.ExpiresAt > 0 && record.ExpiresAt < now.Unix()
}
