package database

import (
	"errors"
	"fmt"
)

var (
	ErrDatabaseNotOpen     = errors.New("database: database is not open")
	ErrKeyNotFound         = errors.New("database: key not found")
	ErrFeatureNotSupported = errors.New("database: feature not supported")
)

type databaseEngine interface {
	Open(config DatabaseConfig) error
	Close() error
	Get(key []byte) ([]byte, error)
	Set(key []byte, value []byte) error
	Delete(key []byte) error
	NewBatch() (databaseBatch, error)
	NewIterator(lower []byte, upper []byte) (databaseIterator, error)
	DeleteRange(start []byte, end []byte) error
	Flush() error
	Compact(start []byte, limit []byte) error
	Checkpoint(destDir string) error
	CheckHealth() error
	EnableWAL(enable bool) error
}

type databaseBatch interface {
	Set(key []byte, value []byte) error
	Delete(key []byte) error
	Commit() error
	Close() error
}

type databaseIterator interface {
	First() bool
	Last() bool
	SeekGE(key []byte) bool
	SeekLT(key []byte) bool
	Next() bool
	Prev() bool
	Key() []byte
	Value() []byte
	Error() error
	Close() error
}

func NewDatabase(config DatabaseConfig) (Database, error) {
	engineType := config.Engine
	if engineType == "" {
		engineType = EnginePebble
	}

	var engine databaseEngine
	switch engineType {
	case EnginePebble:
		engine = newPebbleEngine()
	case EngineLevelDB:
		engine = newLevelDBEngine()
	default:
		return nil, fmt.Errorf("database: unsupported engine %q", engineType)
	}

	database := newDatabaseCore(engine)
	if err := database.CreateDatabase(config); err != nil {
		return nil, err
	}
	return database, nil
}
