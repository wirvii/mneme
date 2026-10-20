package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// errInjected is the failure the faultyExecutor injects.
var errInjected = errors.New("injected failure")

// faultyExecutor delegates to a real database but fails its failAt-th call
// (counting Exec, Query and QueryRow together). Walking failAt from 1 upward
// makes every error branch of a migration helper run once against real SQLite
// state, without a mock of the schema.
type faultyExecutor struct {
	inner  *sql.DB
	dead   *sql.DB // a closed handle: its QueryRowContext yields a Row that errors on Scan
	failAt int
	calls  int
}

func (f *faultyExecutor) hit() bool {
	f.calls++
	return f.calls == f.failAt
}

func (f *faultyExecutor) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if f.hit() {
		return nil, errInjected
	}
	return f.inner.ExecContext(ctx, q, args...)
}

func (f *faultyExecutor) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if f.hit() {
		return nil, errInjected
	}
	return f.inner.QueryContext(ctx, q, args...)
}

func (f *faultyExecutor) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if f.hit() {
		return f.dead.QueryRowContext(ctx, q, args...)
	}
	return f.inner.QueryRowContext(ctx, q, args...)
}

// buildDamagedDatabase returns the path of a closed database file that makes
// every repair and fill path of a migration pass do real work: specs and
// spec_history rebuilt by a re-run of 005, a backlog row and a spec row with
// empty uuids, memories without reviewed_at, and version 24 unrecorded.
func buildDamagedDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "damaged.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	statements := []string{
		`INSERT INTO backlog_items (id, title, project, created_at, updated_at) VALUES ('BL-001', 'T', 'p', 'x', 'x')`,
		`INSERT INTO specs (id, title, project, created_at, updated_at) VALUES ('SPEC-001', 'T', 'p', 'x', 'x')`,
		loadMigration005(t),
		`ALTER TABLE memories DROP COLUMN reviewed_at`,
		`DELETE FROM schema_version WHERE version = 24`,
	}
	for _, s := range statements {
		if _, err := d.Exec(s); err != nil {
			t.Fatalf("prepare damaged database (%.40s): %v", s, err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}

// copyDatabase copies a closed database file into a fresh directory and opens
// it raw (no migrations).
func copyDatabase(t *testing.T, src string) *sql.DB {
	t.Helper()
	content, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	dst := filepath.Join(t.TempDir(), "copy.db")
	if err := os.WriteFile(dst, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
	return openRawFile(t, dst)
}

// TestMigrationHelpers_PropagateEveryFailure injects a failure into each
// successive database call of every migration helper and requires that the
// helper reports it: nothing in the pass may swallow an error and carry on
// with a half-repaired database.
func TestMigrationHelpers_PropagateEveryFailure(t *testing.T) {
	damaged := buildDamagedDatabase(t)

	ctx := context.Background()
	targets := map[string]func(sqlExecutor) error{
		"ensureSpecColumns":            ensureSpecColumns,
		"ensureSpecHistoryReviewedSHA": ensureSpecHistoryReviewedSHA,
		"ensureSDDUUIDs":               ensureSDDUUIDs,
		"ensureMemoryReviewedAt":       ensureMemoryReviewedAt,
		"migration005PreFlight":        migration005PreFlight,
		"workPending": func(ex sqlExecutor) error {
			_, err := workPending(ctx, ex, migrationsFS)
			return err
		},
		"migration022PreFlight": func(ex sqlExecutor) error {
			_, err := migration022PreFlight(ctx, ex)
			return err
		},
		"applyMigration records the version itself": func(ex sqlExecutor) error {
			return applyMigration(ex, 90, `CREATE TABLE fault_probe (a INTEGER)`)
		},
		"applyMigration with a file that records its own version": func(ex sqlExecutor) error {
			return applyMigration(ex, 91, `INSERT INTO schema_version (version, applied_at) VALUES (91, 'x')`)
		},
		"migrateLocked": func(ex sqlExecutor) error {
			return migrateLocked(ctx, ex, migrationsFS)
		},
		"migrateLocked with an unreadable migrations dir": func(ex sqlExecutor) error {
			return migrateLocked(ctx, ex, fstest.MapFS{})
		},
	}

	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			for failAt := 1; ; failAt++ {
				raw := copyDatabase(t, damaged)
				dead, err := sql.Open("sqlite", "file::memory:")
				if err != nil {
					t.Fatalf("open dead handle: %v", err)
				}
				_ = dead.Close()

				ex := &faultyExecutor{inner: raw, dead: dead, failAt: failAt}
				err = target(ex)
				if ex.calls < failAt {
					return // every call succeeded: all failure points were walked
				}
				if err == nil {
					t.Fatalf("failure injected at database call %d was swallowed", failAt)
				}
			}
		})
	}
}

// TestOpen_FailureModes covers the errors Open reports before and during the
// migration pass.
func TestOpen_FailureModes(t *testing.T) {
	t.Run("parent is a file", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := Open(filepath.Join(parent, "sub", "x.db")); err == nil {
			t.Error("Open succeeded although its parent directory cannot be created")
		}
	})

	t.Run("path is a directory", func(t *testing.T) {
		if _, err := Open(t.TempDir()); err == nil {
			t.Error("Open succeeded on a directory")
		}
	})

	t.Run("file is not a database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "junk.db")
		if err := os.WriteFile(path, []byte("this is definitely not a sqlite database file"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := Open(path); err == nil {
			t.Error("Open succeeded on a file that is not a database")
		}
	})
}

// TestMigrate_ClosedDatabase checks that migrate reports an unusable handle
// instead of panicking or hanging.
func TestMigrate_ClosedDatabase(t *testing.T) {
	raw, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := migrate(raw); err == nil {
		t.Error("migrate on a closed database returned nil")
	}
}
