package database

import (
	"fmt"

	leveldbengine "solana_golang/database/leveldb"
	pebbleengine "solana_golang/database/pebble"
)

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

func newPebbleKVEngine() KVEngine {
	return &pebbleKVEngine{engine: pebbleengine.NewEngine()}
}

func newLevelDBKVEngine() KVEngine {
	return &levelDBKVEngine{engine: leveldbengine.NewEngine()}
}

type pebbleKVEngine struct {
	engine *pebbleengine.Engine
}

func (e *pebbleKVEngine) Open(path string, walEnabled bool) error {
	return e.engine.Open(path, walEnabled)
}

func (e *pebbleKVEngine) Close() error {
	return e.engine.Close()
}

func (e *pebbleKVEngine) Get(key []byte) ([]byte, error) {
	return e.engine.Get(key)
}

func (e *pebbleKVEngine) Set(key []byte, value []byte) error {
	return e.engine.Set(key, value)
}

func (e *pebbleKVEngine) Delete(key []byte) error {
	return e.engine.Delete(key)
}

func (e *pebbleKVEngine) NewBatch() (KVBatch, error) {
	return e.engine.NewBatch()
}

func (e *pebbleKVEngine) NewIterator(lower []byte, upper []byte) (KVIterator, error) {
	return e.engine.NewIterator(lower, upper)
}

func (e *pebbleKVEngine) NewSnapshot() (KVSnapshot, error) {
	snapshot, err := e.engine.NewSnapshot()
	if err != nil {
		return nil, err
	}
	return &pebbleKVSnapshot{snapshot: snapshot}, nil
}

func (e *pebbleKVEngine) DeleteRange(start []byte, end []byte) error {
	return e.engine.DeleteRange(start, end)
}

func (e *pebbleKVEngine) Flush() error {
	return e.engine.Flush()
}

func (e *pebbleKVEngine) Compact(start []byte, limit []byte) error {
	return e.engine.Compact(start, limit)
}

func (e *pebbleKVEngine) Checkpoint(destDir string) error {
	return e.engine.Checkpoint(destDir)
}

func (e *pebbleKVEngine) CheckHealth() error {
	return e.engine.CheckHealth()
}

func (e *pebbleKVEngine) EnableWAL(enable bool) error {
	return e.engine.EnableWAL(enable)
}

func (e *pebbleKVEngine) IsNotFound(err error) bool {
	return e.engine.IsNotFound(err)
}

func (e *pebbleKVEngine) SupportsCheckpoint() bool {
	return e.engine.SupportsCheckpoint()
}

func (e *pebbleKVEngine) SupportsDisableWAL() bool {
	return e.engine.SupportsDisableWAL()
}

type levelDBKVEngine struct {
	engine *leveldbengine.Engine
}

func (e *levelDBKVEngine) Open(path string, walEnabled bool) error {
	return e.engine.Open(path, walEnabled)
}

func (e *levelDBKVEngine) Close() error {
	return e.engine.Close()
}

func (e *levelDBKVEngine) Get(key []byte) ([]byte, error) {
	return e.engine.Get(key)
}

func (e *levelDBKVEngine) Set(key []byte, value []byte) error {
	return e.engine.Set(key, value)
}

func (e *levelDBKVEngine) Delete(key []byte) error {
	return e.engine.Delete(key)
}

func (e *levelDBKVEngine) NewBatch() (KVBatch, error) {
	return e.engine.NewBatch()
}

func (e *levelDBKVEngine) NewIterator(lower []byte, upper []byte) (KVIterator, error) {
	return e.engine.NewIterator(lower, upper)
}

func (e *levelDBKVEngine) NewSnapshot() (KVSnapshot, error) {
	snapshot, err := e.engine.NewSnapshot()
	if err != nil {
		return nil, err
	}
	return &levelDBKVSnapshot{snapshot: snapshot}, nil
}

func (e *levelDBKVEngine) DeleteRange(start []byte, end []byte) error {
	return e.engine.DeleteRange(start, end)
}

func (e *levelDBKVEngine) Flush() error {
	return e.engine.Flush()
}

func (e *levelDBKVEngine) Compact(start []byte, limit []byte) error {
	return e.engine.Compact(start, limit)
}

func (e *levelDBKVEngine) Checkpoint(destDir string) error {
	return e.engine.Checkpoint(destDir)
}

func (e *levelDBKVEngine) CheckHealth() error {
	return e.engine.CheckHealth()
}

func (e *levelDBKVEngine) EnableWAL(enable bool) error {
	return e.engine.EnableWAL(enable)
}

func (e *levelDBKVEngine) IsNotFound(err error) bool {
	return e.engine.IsNotFound(err)
}

func (e *levelDBKVEngine) SupportsCheckpoint() bool {
	return e.engine.SupportsCheckpoint()
}

func (e *levelDBKVEngine) SupportsDisableWAL() bool {
	return e.engine.SupportsDisableWAL()
}

type pebbleKVSnapshot struct {
	snapshot *pebbleengine.Snapshot
}

func (s *pebbleKVSnapshot) Get(key []byte) ([]byte, error) {
	return s.snapshot.Get(key)
}

func (s *pebbleKVSnapshot) NewIterator(lower []byte, upper []byte) (KVIterator, error) {
	return s.snapshot.NewIterator(lower, upper)
}

func (s *pebbleKVSnapshot) IsNotFound(err error) bool {
	return s.snapshot.IsNotFound(err)
}

func (s *pebbleKVSnapshot) Close() error {
	return s.snapshot.Close()
}

type levelDBKVSnapshot struct {
	snapshot *leveldbengine.Snapshot
}

func (s *levelDBKVSnapshot) Get(key []byte) ([]byte, error) {
	return s.snapshot.Get(key)
}

func (s *levelDBKVSnapshot) NewIterator(lower []byte, upper []byte) (KVIterator, error) {
	return s.snapshot.NewIterator(lower, upper)
}

func (s *levelDBKVSnapshot) IsNotFound(err error) bool {
	return s.snapshot.IsNotFound(err)
}

func (s *levelDBKVSnapshot) Close() error {
	return s.snapshot.Close()
}
