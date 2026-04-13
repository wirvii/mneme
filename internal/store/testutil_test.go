package store

import (
	"testing"

	"github.com/juanftp/mneme/internal/db"
)

// newTestStore opens a fresh in-memory SQLite database, applies all migrations,
// and returns a MemoryStore backed by it. The database is closed automatically
// when the test finishes.
//
// SetMaxOpenConns(1) is required for SQLite in-memory databases: the DSN
// "file::memory:" creates a distinct database per connection, so the pool must
// be forced to reuse a single connection to keep all queries on the same schema.
func newTestStore(t *testing.T) *MemoryStore {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	// Enforce a single connection so that all queries share the same in-memory DB.
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	return NewMemoryStore(database)
}
