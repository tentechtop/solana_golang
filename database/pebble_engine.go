package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cockroachdb/pebble"
)

var pebbleNoSync = &pebble.WriteOptions{Sync: false}

type pebbleEngine struct {
	db        *pebble.DB
	walEnable bool
}

func newPebbleEngine() *pebbleEngine {
	return &pebbleEngine{walEnable: true}
}

func (e *pebbleEngine) Open(config DatabaseConfig) error {
	if err := os.MkdirAll(config.Path, 0755); err != nil {
		return fmt.Errorf("database: create pebble directory: %w", err)
	}
	db, err := pebble.Open(config.Path, &pebble.Options{DisableWAL: !config.WAL})
	if err != nil {
		return fmt.Errorf("database: open pebble: %w", err)
	}
	e.db = db
	e.walEnable = config.WAL
	return nil
}

func (e *pebbleEngine) Close() error {
	if e.db == nil {
		return nil
	}
	db := e.db
	e.db = nil
	return db.Close()
}

func (e *pebbleEngine) Get(key []byte) ([]byte, error) {
	if e.db == nil {
		return nil, ErrDatabaseNotOpen
	}
	value, closer, err := e.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	return cloneBytes(value), nil
}

func (e *pebbleEngine) Set(key []byte, value []byte) error {
	if e.db == nil {
		return ErrDatabaseNotOpen
	}
	return e.db.Set(key, value, pebbleNoSync)
}

func (e *pebbleEngine) Delete(key []byte) error {
	if e.db == nil {
		return ErrDatabaseNotOpen
	}
	return e.db.Delete(key, pebbleNoSync)
}

func (e *pebbleEngine) NewBatch() (databaseBatch, error) {
	if e.db == nil {
		return nil, ErrDatabaseNotOpen
	}
	return &pebbleBatch{batch: e.db.NewBatch()}, nil
}

func (e *pebbleEngine) NewIterator(lower []byte, upper []byte) (databaseIterator, error) {
	if e.db == nil {
		return nil, ErrDatabaseNotOpen
	}
	iter, err := e.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, err
	}
	return &pebbleIterator{iter: iter}, nil
}

func (e *pebbleEngine) DeleteRange(start []byte, end []byte) error {
	if e.db == nil {
		return ErrDatabaseNotOpen
	}
	return e.db.DeleteRange(start, end, pebbleNoSync)
}

func (e *pebbleEngine) Flush() error {
	if e.db == nil {
		return ErrDatabaseNotOpen
	}
	return e.db.Flush()
}

func (e *pebbleEngine) Compact(start []byte, limit []byte) error {
	if e.db == nil {
		return ErrDatabaseNotOpen
	}
	return e.db.Compact(start, limit, true)
}

func (e *pebbleEngine) Checkpoint(destDir string) error {
	if e.db == nil {
		return ErrDatabaseNotOpen
	}
	parent := filepath.Dir(destDir)
	if parent != "." && parent != "" {
		if err := os.MkdirAll(parent, 0755); err != nil {
			return fmt.Errorf("database: create checkpoint parent: %w", err)
		}
	}
	return e.db.Checkpoint(destDir)
}

func (e *pebbleEngine) CheckHealth() error {
	key := []byte{0, 0, 'h', 'e', 'a', 'l', 't', 'h'}
	if err := e.Set(key, []byte("ok")); err != nil {
		return fmt.Errorf("database: health set: %w", err)
	}
	if err := e.Delete(key); err != nil {
		return fmt.Errorf("database: health delete: %w", err)
	}
	return nil
}

func (e *pebbleEngine) EnableWAL(enable bool) error {
	if e.db != nil && e.walEnable != enable {
		return errors.New("database: WAL setting can only be changed before open")
	}
	e.walEnable = enable
	return nil
}

type pebbleBatch struct {
	batch *pebble.Batch
}

func (b *pebbleBatch) Set(key []byte, value []byte) error {
	return b.batch.Set(key, value, nil)
}

func (b *pebbleBatch) Delete(key []byte) error {
	return b.batch.Delete(key, nil)
}

func (b *pebbleBatch) Commit() error {
	return b.batch.Commit(pebbleNoSync)
}

func (b *pebbleBatch) Close() error {
	return b.batch.Close()
}

type pebbleIterator struct {
	iter *pebble.Iterator
}

func (i *pebbleIterator) First() bool {
	return i.iter.First()
}

func (i *pebbleIterator) Last() bool {
	return i.iter.Last()
}

func (i *pebbleIterator) SeekGE(key []byte) bool {
	return i.iter.SeekGE(key)
}

func (i *pebbleIterator) SeekLT(key []byte) bool {
	return i.iter.SeekLT(key)
}

func (i *pebbleIterator) Next() bool {
	return i.iter.Next()
}

func (i *pebbleIterator) Prev() bool {
	return i.iter.Prev()
}

func (i *pebbleIterator) Key() []byte {
	return i.iter.Key()
}

func (i *pebbleIterator) Value() []byte {
	return i.iter.Value()
}

func (i *pebbleIterator) Error() error {
	return i.iter.Error()
}

func (i *pebbleIterator) Close() error {
	return i.iter.Close()
}
