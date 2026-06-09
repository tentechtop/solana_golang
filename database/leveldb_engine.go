package database

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
)

type levelDBEngine struct {
	db           *leveldb.DB
	writeOptions *opt.WriteOptions
}

func newLevelDBEngine() *levelDBEngine {
	return &levelDBEngine{writeOptions: &opt.WriteOptions{Sync: false}}
}

func (e *levelDBEngine) Open(config DatabaseConfig) error {
	if err := os.MkdirAll(config.Path, 0755); err != nil {
		return fmt.Errorf("database: create leveldb directory: %w", err)
	}
	db, err := leveldb.OpenFile(config.Path, nil)
	if err != nil {
		return fmt.Errorf("database: open leveldb: %w", err)
	}
	e.db = db
	return nil
}

func (e *levelDBEngine) Close() error {
	if e.db == nil {
		return nil
	}
	db := e.db
	e.db = nil
	return db.Close()
}

func (e *levelDBEngine) Get(key []byte) ([]byte, error) {
	if e.db == nil {
		return nil, ErrDatabaseNotOpen
	}
	value, err := e.db.Get(key, nil)
	if errors.Is(err, leveldb.ErrNotFound) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	return cloneBytes(value), nil
}

func (e *levelDBEngine) Set(key []byte, value []byte) error {
	if e.db == nil {
		return ErrDatabaseNotOpen
	}
	return e.db.Put(key, value, e.writeOptions)
}

func (e *levelDBEngine) Delete(key []byte) error {
	if e.db == nil {
		return ErrDatabaseNotOpen
	}
	return e.db.Delete(key, e.writeOptions)
}

func (e *levelDBEngine) NewBatch() (databaseBatch, error) {
	if e.db == nil {
		return nil, ErrDatabaseNotOpen
	}
	return &levelDBBatch{
		db:           e.db,
		batch:        new(leveldb.Batch),
		writeOptions: e.writeOptions,
	}, nil
}

func (e *levelDBEngine) NewIterator(lower []byte, upper []byte) (databaseIterator, error) {
	if e.db == nil {
		return nil, ErrDatabaseNotOpen
	}
	return &levelDBIterator{iter: e.db.NewIterator(&util.Range{Start: lower, Limit: upper}, nil)}, nil
}

func (e *levelDBEngine) DeleteRange(start []byte, end []byte) error {
	if e.db == nil {
		return ErrDatabaseNotOpen
	}
	iter := e.db.NewIterator(&util.Range{Start: start, Limit: end}, nil)
	defer iter.Release()

	batch := new(leveldb.Batch)
	for ok := iter.First(); ok; ok = iter.Next() {
		batch.Delete(cloneBytes(iter.Key()))
	}
	if err := iter.Error(); err != nil {
		return err
	}
	return e.db.Write(batch, e.writeOptions)
}

func (e *levelDBEngine) Flush() error {
	if e.db == nil {
		return ErrDatabaseNotOpen
	}
	return nil
}

func (e *levelDBEngine) Compact(start []byte, limit []byte) error {
	if e.db == nil {
		return ErrDatabaseNotOpen
	}
	return e.db.CompactRange(util.Range{Start: start, Limit: limit})
}

func (e *levelDBEngine) Checkpoint(string) error {
	return ErrFeatureNotSupported
}

func (e *levelDBEngine) CheckHealth() error {
	key := []byte{0, 0, 'h', 'e', 'a', 'l', 't', 'h'}
	if err := e.Set(key, []byte("ok")); err != nil {
		return fmt.Errorf("database: health set: %w", err)
	}
	if err := e.Delete(key); err != nil {
		return fmt.Errorf("database: health delete: %w", err)
	}
	return nil
}

func (e *levelDBEngine) EnableWAL(enable bool) error {
	if enable {
		return nil
	}
	return ErrFeatureNotSupported
}

type levelDBBatch struct {
	db           *leveldb.DB
	batch        *leveldb.Batch
	writeOptions *opt.WriteOptions
}

func (b *levelDBBatch) Set(key []byte, value []byte) error {
	b.batch.Put(key, value)
	return nil
}

func (b *levelDBBatch) Delete(key []byte) error {
	b.batch.Delete(key)
	return nil
}

func (b *levelDBBatch) Commit() error {
	return b.db.Write(b.batch, b.writeOptions)
}

func (b *levelDBBatch) Close() error {
	b.batch.Reset()
	return nil
}

type levelDBIterator struct {
	iter iterator
}

type iterator interface {
	First() bool
	Last() bool
	Seek(key []byte) bool
	Next() bool
	Prev() bool
	Key() []byte
	Value() []byte
	Error() error
	Release()
}

func (i *levelDBIterator) First() bool {
	return i.iter.First()
}

func (i *levelDBIterator) Last() bool {
	return i.iter.Last()
}

func (i *levelDBIterator) SeekGE(key []byte) bool {
	return i.iter.Seek(key)
}

func (i *levelDBIterator) SeekLT(key []byte) bool {
	if !i.iter.Seek(key) {
		return i.iter.Last()
	}
	if bytes.Compare(i.iter.Key(), key) < 0 {
		return true
	}
	return i.iter.Prev()
}

func (i *levelDBIterator) Next() bool {
	return i.iter.Next()
}

func (i *levelDBIterator) Prev() bool {
	return i.iter.Prev()
}

func (i *levelDBIterator) Key() []byte {
	return i.iter.Key()
}

func (i *levelDBIterator) Value() []byte {
	return i.iter.Value()
}

func (i *levelDBIterator) Error() error {
	return i.iter.Error()
}

func (i *levelDBIterator) Close() error {
	i.iter.Release()
	return nil
}
