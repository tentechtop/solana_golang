package database

import (
	"fmt"

	leveldbengine "solana_golang/database/leveldb"
	pebbleengine "solana_golang/database/pebble"
)

// NewDatabase 执行对应逻辑 + 保持函数职责清晰可维护。
func NewDatabase(config DatabaseConfig) (Database, error) {
	engineType := config.Engine
	if engineType == "" {
		engineType = EnginePebble
	}

	var engine KVEngine
	switch engineType {
	case EnginePebble:
		engine = newPebbleKVEngine()
	case EngineLevelDB:
		engine = newLevelDBKVEngine()
	default:
		return nil, fmt.Errorf("database: unsupported engine %q", engineType)
	}

	database := newDatabaseService(engine)
	if err := database.CreateDatabase(config); err != nil {
		return nil, err
	}
	return database, nil
}

// newPebbleKVEngine 执行对应逻辑 + 保持函数职责清晰可维护。
func newPebbleKVEngine() KVEngine {
	return &pebbleKVEngine{engine: pebbleengine.NewEngine()}
}

// newLevelDBKVEngine 执行对应逻辑 + 保持函数职责清晰可维护。
func newLevelDBKVEngine() KVEngine {
	return &levelDBKVEngine{engine: leveldbengine.NewEngine()}
}

type pebbleKVEngine struct {
	engine *pebbleengine.Engine
}

// Open 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) Open(path string, walEnabled bool) error {
	return e.engine.Open(path, walEnabled)
}

// Close 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) Close() error {
	return e.engine.Close()
}

// Get 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) Get(key []byte) ([]byte, error) {
	return e.engine.Get(key)
}

// Set 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) Set(key []byte, value []byte) error {
	return e.engine.Set(key, value)
}

// Delete 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) Delete(key []byte) error {
	return e.engine.Delete(key)
}

// NewBatch 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) NewBatch() (KVBatch, error) {
	return e.engine.NewBatch()
}

// NewIterator 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) NewIterator(lower []byte, upper []byte) (KVIterator, error) {
	return e.engine.NewIterator(lower, upper)
}

// NewSnapshot 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) NewSnapshot() (KVSnapshot, error) {
	snapshot, err := e.engine.NewSnapshot()
	if err != nil {
		return nil, err
	}
	return &pebbleKVSnapshot{snapshot: snapshot}, nil
}

// DeleteRange 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) DeleteRange(start []byte, end []byte) error {
	return e.engine.DeleteRange(start, end)
}

// Flush 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) Flush() error {
	return e.engine.Flush()
}

// Compact 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) Compact(start []byte, limit []byte) error {
	return e.engine.Compact(start, limit)
}

// Checkpoint 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) Checkpoint(destDir string) error {
	return e.engine.Checkpoint(destDir)
}

// CheckHealth 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) CheckHealth() error {
	return e.engine.CheckHealth()
}

// EnableWAL 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) EnableWAL(enable bool) error {
	return e.engine.EnableWAL(enable)
}

// IsNotFound 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) IsNotFound(err error) bool {
	return e.engine.IsNotFound(err)
}

// SupportsCheckpoint 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) SupportsCheckpoint() bool {
	return e.engine.SupportsCheckpoint()
}

// SupportsDisableWAL 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *pebbleKVEngine) SupportsDisableWAL() bool {
	return e.engine.SupportsDisableWAL()
}

type levelDBKVEngine struct {
	engine *leveldbengine.Engine
}

// Open 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) Open(path string, walEnabled bool) error {
	return e.engine.Open(path, walEnabled)
}

// Close 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) Close() error {
	return e.engine.Close()
}

// Get 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) Get(key []byte) ([]byte, error) {
	return e.engine.Get(key)
}

// Set 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) Set(key []byte, value []byte) error {
	return e.engine.Set(key, value)
}

// Delete 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) Delete(key []byte) error {
	return e.engine.Delete(key)
}

// NewBatch 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) NewBatch() (KVBatch, error) {
	return e.engine.NewBatch()
}

// NewIterator 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) NewIterator(lower []byte, upper []byte) (KVIterator, error) {
	return e.engine.NewIterator(lower, upper)
}

// NewSnapshot 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) NewSnapshot() (KVSnapshot, error) {
	snapshot, err := e.engine.NewSnapshot()
	if err != nil {
		return nil, err
	}
	return &levelDBKVSnapshot{snapshot: snapshot}, nil
}

// DeleteRange 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) DeleteRange(start []byte, end []byte) error {
	return e.engine.DeleteRange(start, end)
}

// Flush 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) Flush() error {
	return e.engine.Flush()
}

// Compact 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) Compact(start []byte, limit []byte) error {
	return e.engine.Compact(start, limit)
}

// Checkpoint 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) Checkpoint(destDir string) error {
	return e.engine.Checkpoint(destDir)
}

// CheckHealth 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) CheckHealth() error {
	return e.engine.CheckHealth()
}

// EnableWAL 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) EnableWAL(enable bool) error {
	return e.engine.EnableWAL(enable)
}

// IsNotFound 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) IsNotFound(err error) bool {
	return e.engine.IsNotFound(err)
}

// SupportsCheckpoint 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) SupportsCheckpoint() bool {
	return e.engine.SupportsCheckpoint()
}

// SupportsDisableWAL 执行对应逻辑 + 保持函数职责清晰可维护。
func (e *levelDBKVEngine) SupportsDisableWAL() bool {
	return e.engine.SupportsDisableWAL()
}

type pebbleKVSnapshot struct {
	snapshot *pebbleengine.Snapshot
}

// Get 执行对应逻辑 + 保持函数职责清晰可维护。
func (s *pebbleKVSnapshot) Get(key []byte) ([]byte, error) {
	return s.snapshot.Get(key)
}

// NewIterator 执行对应逻辑 + 保持函数职责清晰可维护。
func (s *pebbleKVSnapshot) NewIterator(lower []byte, upper []byte) (KVIterator, error) {
	return s.snapshot.NewIterator(lower, upper)
}

// IsNotFound 执行对应逻辑 + 保持函数职责清晰可维护。
func (s *pebbleKVSnapshot) IsNotFound(err error) bool {
	return s.snapshot.IsNotFound(err)
}

// Close 执行对应逻辑 + 保持函数职责清晰可维护。
func (s *pebbleKVSnapshot) Close() error {
	return s.snapshot.Close()
}

type levelDBKVSnapshot struct {
	snapshot *leveldbengine.Snapshot
}

// Get 执行对应逻辑 + 保持函数职责清晰可维护。
func (s *levelDBKVSnapshot) Get(key []byte) ([]byte, error) {
	return s.snapshot.Get(key)
}

// NewIterator 执行对应逻辑 + 保持函数职责清晰可维护。
func (s *levelDBKVSnapshot) NewIterator(lower []byte, upper []byte) (KVIterator, error) {
	return s.snapshot.NewIterator(lower, upper)
}

// IsNotFound 执行对应逻辑 + 保持函数职责清晰可维护。
func (s *levelDBKVSnapshot) IsNotFound(err error) bool {
	return s.snapshot.IsNotFound(err)
}

// Close 执行对应逻辑 + 保持函数职责清晰可维护。
func (s *levelDBKVSnapshot) Close() error {
	return s.snapshot.Close()
}
