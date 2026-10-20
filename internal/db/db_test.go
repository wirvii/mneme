package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

// codedError mimics the Code() accessor of modernc's *sqlite.Error. That type
// has only unexported fields, so extended result codes (which a real database
// rarely yields on demand) can only be exercised through this stand-in.
type codedError struct{ code int }

func (e *codedError) Error() string { return fmt.Sprintf("sqlite error %d", e.code) }

// Code returns the SQLite result code.
func (e *codedError) Code() int { return e.code }

// TestIsBusy covers the classifier that decides whether Open retries Ping.
func TestIsBusy(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"busy", &codedError{code: sqlite3.SQLITE_BUSY}, true},
		{"busy recovery (extended)", &codedError{code: sqlite3.SQLITE_BUSY_RECOVERY}, true},
		{"busy snapshot (extended)", &codedError{code: sqlite3.SQLITE_BUSY_SNAPSHOT}, true},
		{"wrapped busy", fmt.Errorf("db: open: ping: %w", &codedError{code: sqlite3.SQLITE_BUSY}), true},
		{"locked is not busy", &codedError{code: sqlite3.SQLITE_LOCKED}, false},
		{"constraint", &codedError{code: sqlite3.SQLITE_CONSTRAINT}, false},
		{"not a sqlite error", errors.New("database is locked (5) (SQLITE_BUSY)"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBusy(tc.err); got != tc.want {
				t.Errorf("isBusy(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsBusy_RealDriverError checks the classifier against the error the real
// driver returns when a second connection cannot get the write lock, so the
// stand-in above cannot drift from the driver's actual error type.
func TestIsBusy_RealDriverError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.db")
	holder, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	defer holder.Close()
	other, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatalf("open other: %v", err)
	}
	defer other.Close()

	ctx := context.Background()
	c1, err := holder.Conn(ctx)
	if err != nil {
		t.Fatalf("holder conn: %v", err)
	}
	defer c1.Close()
	if _, err := c1.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("holder begin: %v", err)
	}
	defer func() { _, _ = c1.ExecContext(ctx, "ROLLBACK") }()

	c2, err := other.Conn(ctx)
	if err != nil {
		t.Fatalf("other conn: %v", err)
	}
	defer c2.Close()
	_, err = c2.ExecContext(ctx, "BEGIN IMMEDIATE")
	if err == nil {
		t.Fatal("second BEGIN IMMEDIATE succeeded, want SQLITE_BUSY")
	}
	if !isBusy(err) {
		t.Errorf("isBusy(%v) = false for a real busy error", err)
	}
}
