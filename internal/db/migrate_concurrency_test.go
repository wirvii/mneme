package db

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file deliberately uses only Open and SQL (no applyMigration, no
// migrationsFS, no helpers from other test files) so that SPEC-178 C2 can
// copy it unchanged into a worktree of the pre-fix commit and watch it fail.

const (
	// concurrentRounds is how many brand-new database files each race test
	// opens; a single lucky interleaving must not be enough to pass.
	concurrentRounds = 20
	// concurrentOpeners is how many goroutines race on each new file.
	concurrentOpeners = 8
	// concurrentProcesses is how many real test-binary processes race on one file.
	concurrentProcesses = 4
	// specsColumnCount is the number of columns specs has once every
	// migration (004, 005, 011, 012, 019, 020) has been applied.
	specsColumnCount = 14

	childPathEnv     = "MNEME_DB_TEST_CHILD_PATH"
	childDeadlineEnv = "MNEME_DB_TEST_CHILD_DEADLINE_NS"
)

// embeddedVersionCount counts the migration files on disk next to this test.
// Every file registers exactly one version, so it is the number of
// schema_version rows a fully migrated database must hold.
func embeddedVersionCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			n++
		}
	}
	return n
}

// assertFullyMigratedShape reopens path with Open and checks the invariants
// the concurrent-open race used to break.
func assertFullyMigratedShape(t *testing.T, path string) {
	t.Helper()
	d, err := Open(path)
	if err != nil {
		t.Fatalf("reopen %s: %v", path, err)
	}
	defer d.Close()

	var cols int
	if err := d.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('specs')`).Scan(&cols); err != nil {
		t.Fatalf("count specs columns: %v", err)
	}
	if cols != specsColumnCount {
		t.Errorf("specs has %d columns, want %d", cols, specsColumnCount)
	}

	var idx int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_specs_uuid'`).Scan(&idx); err != nil {
		t.Fatalf("check idx_specs_uuid: %v", err)
	}
	if idx != 1 {
		t.Errorf("idx_specs_uuid count = %d, want 1", idx)
	}

	var sha int
	if err := d.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('spec_history') WHERE name='reviewed_sha'`).Scan(&sha); err != nil {
		t.Fatalf("check reviewed_sha: %v", err)
	}
	if sha != 1 {
		t.Errorf("spec_history.reviewed_sha count = %d, want 1", sha)
	}

	var rows, distinct int
	if err := d.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT version) FROM schema_version`).Scan(&rows, &distinct); err != nil {
		t.Fatalf("count schema_version: %v", err)
	}
	want := embeddedVersionCount(t)
	if rows != want || distinct != want {
		t.Errorf("schema_version rows=%d distinct=%d, want %d", rows, distinct, want)
	}
}

// TestOpen_ConcurrentNewDatabase races several Open calls on a database file
// that does not exist yet. Before SPEC-178 the losers re-ran migration 005
// from a stale version and left specs without its later columns.
func TestOpen_ConcurrentNewDatabase(t *testing.T) {
	for round := 0; round < concurrentRounds; round++ {
		path := filepath.Join(t.TempDir(), "race.db")

		start := make(chan struct{})
		errs := make(chan error, concurrentOpeners)
		var wg sync.WaitGroup
		for i := 0; i < concurrentOpeners; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				d, err := Open(path)
				if err == nil {
					err = d.Close()
				}
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(errs)

		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: concurrent Open: %v", round, err)
			}
		}
		assertFullyMigratedShape(t, path)
	}
}

// TestOpen_ConcurrentNewDatabase_Processes races real operating-system
// processes (the test binary relaunched) on one new file: the in-process test
// cannot prove the lock works across processes, which is the production case
// (MCP server plus a hook plus the CLI).
func TestOpen_ConcurrentNewDatabase_Processes(t *testing.T) {
	if childPath := os.Getenv(childPathEnv); childPath != "" {
		runOpenChild(t, childPath)
		return
	}

	for round := 0; round < concurrentRounds; round++ {
		path := filepath.Join(t.TempDir(), "race.db")
		// Children spin until a common instant so their Open calls overlap
		// despite uneven process start-up times.
		deadline := time.Now().Add(250 * time.Millisecond).UnixNano()

		cmds := make([]*exec.Cmd, concurrentProcesses)
		outs := make([]*strings.Builder, concurrentProcesses)
		for i := range cmds {
			cmd := exec.Command(os.Args[0], "-test.run=^TestOpen_ConcurrentNewDatabase_Processes$")
			cmd.Env = append(os.Environ(),
				childPathEnv+"="+path,
				childDeadlineEnv+"="+strconv.FormatInt(deadline, 10),
			)
			outs[i] = &strings.Builder{}
			cmd.Stdout = outs[i]
			cmd.Stderr = outs[i]
			cmds[i] = cmd
		}
		for i, cmd := range cmds {
			if err := cmd.Start(); err != nil {
				t.Fatalf("start child %d: %v", i, err)
			}
		}
		for i, cmd := range cmds {
			if err := cmd.Wait(); err != nil {
				t.Errorf("round %d: child %d failed: %v\n%s", round, i, err, outs[i].String())
			}
		}
		if t.Failed() {
			return
		}
		assertFullyMigratedShape(t, path)
	}
}

// runOpenChild is the body of a relaunched test process: wait for the shared
// start instant, Open the database and exit.
func runOpenChild(t *testing.T, path string) {
	t.Helper()
	if raw := os.Getenv(childDeadlineEnv); raw != "" {
		ns, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			t.Fatalf("parse %s: %v", childDeadlineEnv, err)
		}
		for time.Now().UnixNano() < ns {
			time.Sleep(time.Millisecond)
		}
	}
	d, err := Open(path)
	if err != nil {
		t.Fatalf("child Open: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("child Close: %v", err)
	}
}
