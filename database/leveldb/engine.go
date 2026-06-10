package leveldb

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
)

var ErrNotOpen = errors.New("database: leveldb database is not open")

type Engine struct {
	db           *leveldb.DB
	writeOptions *opt.WriteOptions
}

func NewEngine() *Engine {
	return &Engine{writeOptions: &opt.WriteOptions{Sync: false}}
}

func (e *Engine) Open(path string, walEnabled bool) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("database: create leveldb directory: %w", err)
	}
	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		return fmt.Errorf("database: open leveldb: %w", err)
	}
	e.db = db
	return nil
}

func (e *Engine) Close() error {
	if e.db == nil {
		return nil
	}
	db := e.db
	e.db = nil
	return db.Close()
}

func (e *Engine) Get(key []byte) ([]byte, error) {
	if e.db == nil {
		return nil, ErrNotOpen
	}
	value, err := e.db.Get(key, nil)
	if errors.Is(err, leveldb.ErrNotFound) {
		return nil, leveldb.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return cloneBytes(value), nil
}

func (e *Engine) Set(key []byte, value []byte) error {
	if e.db == nil {
		return ErrNotOpen
	}
	return e.db.Put(key, value, e.writeOptions)
}

func (e *Engine) Delete(key []byte) error {
	if e.db == nil {
		return ErrNotOpen
	}
	return e.db.Delete(key, e.writeOptions)
}

func (e *Engine) NewBatch() (*Batch, error) {
	if e.db == nil {
		return nil, ErrNotOpen
	}
	return &Batch{
		db:           e.db,
		batch:        new(leveldb.Batch),
		writeOptions: e.writeOptions,
	}, nil
}

func (e *Engine) NewIterator(lower []byte, upper []byte) (*Iterator, error) {
	if e.db == nil {
		return nil, ErrNotOpen
	}
	return &Iterator{iter: e.db.NewIterator(&util.Range{Start: lower, Limit: upper}, nil)}, nil
}

func (e *Engine) NewSnapshot() (*Snapshot, error) {
	if e.db == nil {
		return nil, ErrNotOpen
	}
	snapshot, err := e.db.GetSnapshot()
	if err != nil {
		return nil, err
	}
	return &Snapshot{snapshot: snapshot}, nil
}

func (e *Engine) DeleteRange(start []byte, end []byte) error {
	if e.db == nil {
		return ErrNotOpen
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

func (e *Engine) Flush() error {
	if e.db == nil {
		return ErrNotOpen
	}
	return nil
}

func (e *Engine) Compact(start []byte, limit []byte) error {
	if e.db == nil {
		return ErrNotOpen
	}
	return e.db.CompactRange(util.Range{Start: start, Limit: limit})
}

func (e *Engine) Checkpoint(string) error {
	return errors.New("database: leveldb checkpoint is not supported")
}

func (e *Engine) CheckHealth() error {
	key := []byte{0, 0, 'h', 'e', 'a', 'l', 't', 'h'}
	if err := e.Set(key, []byte("ok")); err != nil {
		return fmt.Errorf("database: health set: %w", err)
	}
	if err := e.Delete(key); err != nil {
		return fmt.Errorf("database: health delete: %w", err)
	}
	return nil
}

func (e *Engine) EnableWAL(bool) error {
	return nil
}

func (e *Engine) IsNotFound(err error) bool {
	return errors.Is(err, leveldb.ErrNotFound)
}

func (e *Engine) SupportsCheckpoint() bool {
	return false
}

func (e *Engine) SupportsDisableWAL() bool {
	return false
}

type Batch struct {
	db           *leveldb.DB
	batch        *leveldb.Batch
	writeOptions *opt.WriteOptions
}

func (b *Batch) Set(key []byte, value []byte) error {
	b.batch.Put(key, value)
	return nil
}

func (b *Batch) Delete(key []byte) error {
	b.batch.Delete(key)
	return nil
}

func (b *Batch) Commit() error {
	return b.db.Write(b.batch, b.writeOptions)
}

func (b *Batch) Close() error {
	b.batch.Reset()
	return nil
}

type Iterator struct {
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

func (i *Iterator) First() bool {
	return i.iter.First()
}

func (i *Iterator) Last() bool {
	return i.iter.Last()
}

func (i *Iterator) SeekGE(key []byte) bool {
	return i.iter.Seek(key)
}

func (i *Iterator) SeekLT(key []byte) bool {
	if !i.iter.Seek(key) {
		return i.iter.Last()
	}
	if bytes.Compare(i.iter.Key(), key) < 0 {
		return true
	}
	return i.iter.Prev()
}

func (i *Iterator) Next() bool {
	return i.iter.Next()
}

func (i *Iterator) Prev() bool {
	return i.iter.Prev()
}

func (i *Iterator) Key() []byte {
	return i.iter.Key()
}

func (i *Iterator) Value() []byte {
	return i.iter.Value()
}

func (i *Iterator) Error() error {
	return i.iter.Error()
}

func (i *Iterator) Close() error {
	i.iter.Release()
	return nil
}

type Snapshot struct {
	snapshot *leveldb.Snapshot
}

func (s *Snapshot) Get(key []byte) ([]byte, error) {
	value, err := s.snapshot.Get(key, nil)
	if errors.Is(err, leveldb.ErrNotFound) {
		return nil, leveldb.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return cloneBytes(value), nil
}

func (s *Snapshot) NewIterator(lower []byte, upper []byte) (*Iterator, error) {
	return &Iterator{iter: s.snapshot.NewIterator(&util.Range{Start: lower, Limit: upper}, nil)}, nil
}

func (s *Snapshot) IsNotFound(err error) bool {
	return errors.Is(err, leveldb.ErrNotFound)
}

func (s *Snapshot) Close() error {
	s.snapshot.Release()
	return nil
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
