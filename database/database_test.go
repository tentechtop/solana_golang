package database

import (
	"bytes"
	"testing"
)

func TestDatabaseContractAcrossEngines(t *testing.T) {
	cases := []struct {
		name   string
		engine EngineType
	}{
		{name: "pebble", engine: EnginePebble},
		{name: "leveldb", engine: EngineLevelDB},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			db, err := NewDatabase(DatabaseConfig{Path: t.TempDir(), Engine: testCase.engine})
			if err != nil {
				t.Fatalf("NewDatabase() error = %v", err)
			}
			defer db.Close()

			runDatabaseContract(t, db)
		})
	}
}

func runDatabaseContract(t *testing.T, db Database) {
	t.Helper()

	if err := db.Insert(TableTest1, []byte("k1"), []byte("v1")); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if err := db.Insert(TableTest1, []byte("prefix:k2"), []byte("v2")); err != nil {
		t.Fatalf("Insert(prefix) error = %v", err)
	}

	value, err := db.Get(TableTest1, []byte("k1"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !bytes.Equal(value, []byte("v1")) {
		t.Fatalf("Get() = %q, want v1", value)
	}

	pairs, err := db.PrefixQuery(TableTest1, []byte("prefix:"))
	if err != nil {
		t.Fatalf("PrefixQuery() error = %v", err)
	}
	if len(pairs) != 1 || !bytes.Equal(pairs[0].Key, []byte("prefix:k2")) {
		t.Fatalf("PrefixQuery() = %+v, want prefix:k2", pairs)
	}

	if err := db.DataTransaction([]DBOperation{
		NewUpdateOperation(TableTest1, []byte("k1"), []byte("v1-updated")),
		NewDeleteOperation(TableTest1, []byte("prefix:k2")),
	}); err != nil {
		t.Fatalf("DataTransaction() error = %v", err)
	}

	value, err = db.Get(TableTest1, []byte("k1"))
	if err != nil {
		t.Fatalf("Get(updated) error = %v", err)
	}
	if !bytes.Equal(value, []byte("v1-updated")) {
		t.Fatalf("Get(updated) = %q, want v1-updated", value)
	}

	exists, err := db.Exists(TableTest1, []byte("prefix:k2"))
	if err != nil {
		t.Fatalf("Exists(deleted) error = %v", err)
	}
	if exists {
		t.Fatal("Exists(deleted) = true, want false")
	}
}
