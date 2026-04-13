package service_test

import (
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

// newTestService constructs a MemoryService backed by two fully-migrated
// in-memory SQLite databases: one for project-scoped memories and one for
// global/org-scoped memories. Both databases are closed automatically when the
// test ends via t.Cleanup. All tests in this package should use this helper.
func newTestService(t *testing.T) *service.MemoryService {
	t.Helper()
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })
	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	return service.NewMemoryService(projectStore, globalStore, cfg, "test/project")
}
