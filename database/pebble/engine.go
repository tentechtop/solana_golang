package pebble

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cockroachdb/pebble"
)

var pebbleNoSync = &pebble.WriteOptions{Sync: false}

var ErrNotOpen = errors.New("database: pebble database is not open")

type Engine struct {
	db        *pebble.DB
	walEnable bool
}

func NewEngine() *Engine {
	return &Engine{walEnable: true}
}

func (e *Engine) Open(path string, walEnabled bool) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("database: create pebble directory: %w", err)
	}
	db, err := pebble.Open(path, &pebble.Options{DisableWAL: !walEnabled})
	if err != nil {
		return fmt.Errorf("database: open pebble: %w", err)
	}
	e.db = db
	e.walEnable = walEnabled
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
	value, closer, err := e.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, pebble.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	return cloneBytes(value), nil
}

func (e *Engine) Set(key []byte, value []byte) error {
	if e.db == nil {
		return ErrNotOpen
	}
	return e.db.Set(key, value, pebbleNoSync)
}

func (e *Engine) Delete(key []byte) error {
	if e.db == nil {
		return ErrNotOpen
	}
	return e.db.Delete(key, pebbleNoSync)
}

func (e *Engine) NewBatch() (*Batch, error) {
	if e.db == nil {
		return nil, ErrNotOpen
	}
	return &Batch{batch: e.db.NewBatch()}, nil
}

func (e *Engine) NewIterator(lower []byte, upper []byte) (*Iterator, error) {
	if e.db == nil {
		return nil, ErrNotOpen
	}
	iter, err := e.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, err
	}
	return &Iterator{iter: iter}, nil
}

func (e *Engine) NewSnapshot() (*Snapshot, error) {
	if e.db == nil {
		return nil, ErrNotOpen
	}
	return &Snapshot{snapshot: e.db.NewSnapshot()}, nil
}

func (e *Engine) DeleteRange(start []byte, end []byte) error {
	if e.db == nil {
		return ErrNotOpen
	}
	return e.db.DeleteRange(start, end, pebbleNoSync)
}

func (e *Engine) Flush() error {
	if e.db == nil {
		return ErrNotOpen
	}
	return e.db.Flush()
}

func (e *Engine) Compact(start []byte, limit []byte) error {
	if e.db == nil {
		return ErrNotOpen
	}
	return e.db.Compact(start, limit, true)
}

func (e *Engine) Checkpoint(destDir string) error {
	if e.db == nil {
		return ErrNotOpen
	}
	parent := filepath.Dir(destDir)
	if parent != "." && parent != "" {
		if err := os.MkdirAll(parent, 0755); err != nil {
			return fmt.Errorf("database: create checkpoint parent: %w", err)
		}
	}
	return e.db.Checkpoint(destDir)
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

func (e *Engine) EnableWAL(enable bool) error {
	if e.db != nil && e.walEnable != enable {
		return errors.New("database: WAL setting can only be changed before open")
	}
	e.walEnable = enable
	return nil
}

func (e *Engine) IsNotFound(err error) bool {
	return errors.Is(err, pebble.ErrNotFound)
}

func (e *Engine) SupportsCheckpoint() bool {
	return true
}

func (e *Engine) SupportsDisableWAL() bool {
	return true
}

type Batch struct {
	batch *pebble.Batch
}

func (b *Batch) Set(key []byte, value []byte) error {
	return b.batch.Set(key, value, nil)
}

func (b *Batch) Delete(key []byte) error {
	return b.batch.Delete(key, nil)
}

func (b *Batch) Commit() error {
	return b.batch.Commit(pebbleNoSync)
}

func (b *Batch) Close() error {
	return b.batch.Close()
}

type Iterator struct {
	iter *pebble.Iterator
}

func (i *Iterator) First() bool {
	return i.iter.First()
}

func (i *Iterator) Last() bool {
	return i.iter.Last()
}

func (i *Iterator) SeekGE(key []byte) bool {
	return i.iter.SeekGE(key)
}

func (i *Iterator) SeekLT(key []byte) bool {
	return i.iter.SeekLT(key)
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
	return i.iter.Close()
}

type Snapshot struct {
	snapshot *pebble.Snapshot
}

func (s *Snapshot) Get(key []byte) ([]byte, error) {
	value, closer, err := s.snapshot.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, pebble.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	return cloneBytes(value), nil
}

func (s *Snapshot) NewIterator(lower []byte, upper []byte) (*Iterator, error) {
	iter, err := s.snapshot.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, err
	}
	return &Iterator{iter: iter}, nil
}

func (s *Snapshot) IsNotFound(err error) bool {
	return errors.Is(err, pebble.ErrNotFound)
}

func (s *Snapshot) Close() error {
	return s.snapshot.Close()
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
