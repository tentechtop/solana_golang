package database

import (
	"bytes"
	"errors"
	"testing"
)

func TestDatabaseMigrationAppliesAndPersistsVersion(t *testing.T) {
	path := t.TempDir()
	db, err := NewDatabase(DatabaseConfig{Path: path, Engine: EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}

	planCalls := 0
	if err := db.RegisterMigration(Migration{
		Version: 1,
		Name:    "seed peer metadata",
		Plan: func(context MigrationContext) ([]DBOperation, error) {
			planCalls++
			if context.FromVersion != 0 || context.TargetVersion != 1 {
				t.Fatalf("migration context version = %d -> %d, want 0 -> 1", context.FromVersion, context.TargetVersion)
			}
			return []DBOperation{
				NewInsertOperation(TablePeer, []byte("migration:peer"), []byte("applied")),
			}, nil
		},
	}); err != nil {
		t.Fatalf("RegisterMigration() error = %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate(idempotent) error = %v", err)
	}
	if planCalls != 1 {
		t.Fatalf("plan calls = %d, want 1", planCalls)
	}

	version, err := db.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	}
	if version != 1 {
		t.Fatalf("SchemaVersion() = %d, want 1", version)
	}
	value, err := db.Get(TablePeer, []byte("migration:peer"))
	if err != nil {
		t.Fatalf("Get(migrated key) error = %v", err)
	}
	if !bytes.Equal(value, []byte("applied")) {
		t.Fatalf("migrated value = %q, want applied", value)
	}
	history, err := db.MigrationHistory()
	if err != nil {
		t.Fatalf("MigrationHistory() error = %v", err)
	}
	if len(history) != 1 || history[0].Status != migrationStatusApplied {
		t.Fatalf("MigrationHistory() = %+v, want one applied record", history)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := NewDatabase(DatabaseConfig{Path: path, Engine: EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase(reopen) error = %v", err)
	}
	defer reopened.Close()
	version, err = reopened.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion(reopen) error = %v", err)
	}
	if version != 1 {
		t.Fatalf("SchemaVersion(reopen) = %d, want 1", version)
	}
}

func TestDatabaseMigrationFailureDoesNotAdvanceVersion(t *testing.T) {
	db, err := NewDatabase(DatabaseConfig{Path: t.TempDir(), Engine: EngineLevelDB, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	defer db.Close()

	planErr := errors.New("planned failure")
	if err := db.RegisterMigration(Migration{
		Version: 2,
		Name:    "failing migration",
		Plan: func(context MigrationContext) ([]DBOperation, error) {
			return nil, planErr
		},
	}); err != nil {
		t.Fatalf("RegisterMigration() error = %v", err)
	}
	if err := db.Migrate(); err == nil {
		t.Fatal("Migrate() error = nil, want failure")
	}
	version, err := db.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	}
	if version != 0 {
		t.Fatalf("SchemaVersion() = %d, want 0", version)
	}
	history, err := db.MigrationHistory()
	if err != nil {
		t.Fatalf("MigrationHistory() error = %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("len(MigrationHistory()) = %d, want 1", len(history))
	}
	if history[0].Status != migrationStatusFailed || history[0].Error == "" {
		t.Fatalf("failed history = %+v, want failed record with error", history[0])
	}
}

func TestDatabaseMigrationDetectsChecksumMismatch(t *testing.T) {
	path := t.TempDir()
	db, err := NewDatabase(DatabaseConfig{Path: path, Engine: EngineLevelDB, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	if err := db.RegisterMigration(Migration{Version: 1, Name: "original", Plan: func(MigrationContext) ([]DBOperation, error) {
		return nil, nil
	}}); err != nil {
		t.Fatalf("RegisterMigration(original) error = %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate(original) error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := NewDatabase(DatabaseConfig{Path: path, Engine: EngineLevelDB, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase(reopen) error = %v", err)
	}
	defer reopened.Close()
	if err := reopened.RegisterMigration(Migration{Version: 1, Name: "changed", Plan: func(MigrationContext) ([]DBOperation, error) {
		return nil, nil
	}}); err != nil {
		t.Fatalf("RegisterMigration(changed) error = %v", err)
	}
	if err := reopened.Migrate(); err == nil {
		t.Fatal("Migrate(changed checksum) error = nil, want checksum mismatch")
	}
}

func TestDatabaseMigrationValidation(t *testing.T) {
	db, err := NewDatabase(DatabaseConfig{Path: t.TempDir(), Engine: EngineLevelDB, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	defer db.Close()

	if err := db.RegisterMigration(Migration{Name: "missing version", Plan: func(MigrationContext) ([]DBOperation, error) {
		return nil, nil
	}}); err == nil {
		t.Fatal("RegisterMigration(missing version) error = nil, want validation error")
	}
	if err := db.RegisterMigration(Migration{Version: 1, Name: "bad op", Plan: func(MigrationContext) ([]DBOperation, error) {
		return []DBOperation{{Table: TableAll, Type: OperationInsert}}, nil
	}}); err != nil {
		t.Fatalf("RegisterMigration(bad op migration) error = %v", err)
	}
	if err := db.Migrate(); err == nil {
		t.Fatal("Migrate(bad op) error = nil, want validation error")
	}
	version, err := db.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	}
	if version != 0 {
		t.Fatalf("SchemaVersion() = %d, want 0", version)
	}
}
