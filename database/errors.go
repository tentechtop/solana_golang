package database

import "errors"

var (
	ErrDatabaseNotOpen     = errors.New("database: database is not open")
	ErrKeyNotFound         = errors.New("database: key not found")
	ErrFeatureNotSupported = errors.New("database: feature not supported")
	ErrMigrationLocked     = errors.New("database: migration lock is held")
)
