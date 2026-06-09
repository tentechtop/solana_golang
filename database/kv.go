package database

type KVEngine interface {
	Open(path string, walEnabled bool) error
	Close() error
	Get(key []byte) ([]byte, error)
	Set(key []byte, value []byte) error
	Delete(key []byte) error
	NewBatch() (KVBatch, error)
	NewIterator(lower []byte, upper []byte) (KVIterator, error)
	DeleteRange(start []byte, end []byte) error
	Flush() error
	Compact(start []byte, limit []byte) error
	Checkpoint(destDir string) error
	CheckHealth() error
	EnableWAL(enable bool) error
	IsNotFound(err error) bool
	SupportsCheckpoint() bool
	SupportsDisableWAL() bool
}

type KVBatch interface {
	Set(key []byte, value []byte) error
	Delete(key []byte) error
	Commit() error
	Close() error
}

type KVIterator interface {
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
