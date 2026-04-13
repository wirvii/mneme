package service_test

import (
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

// newTestService constructs a MemoryService backed by a fully-migrated in-memory
// SQLite database. The database is closed automatically when the test ends via
// t.Cleanup. All tests in this package should use this helper.
func newTestService(t *testing.T) *service.MemoryService {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	s := store.NewMemoryStore(database)
	cfg := config.Default()
	return service.NewMemoryService(s, cfg, "test/project")
}
