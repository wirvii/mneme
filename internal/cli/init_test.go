package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// TestInitCheckMode_NoCWDSideEffects is the evidence that "mneme init --check"
// does not modify or create any file in the working directory.
//
// The test is placed at the service layer rather than driving newInitCmd()
// end-to-end because newInitCmd().RunE calls initSDDService() and initService(),
// which bootstrap real SQLite databases from ~/.mneme/.  Those paths are not
// injectable through the Cobra command constructor, so executing RunE in a test
// would either touch the developer's home directory or fail with a DB-open
// error on environments without a configured mneme installation.
//
// The --check code path in init.go translates exactly to:
//
//  1. EnsureGreenfieldScaffold(cwd) — skipped (guarded by !flagCheck)
//  2. EnsureGlobalManual()          — skipped (guarded by !flagCheck)
//  3. UpsertRepoBlock(cwd)          — skipped (guarded by !flagCheck)
//  4. RunDrift(cwd)                 — read-only scan; calls DetectDrift
//  5. Plan(ctx, cwd)                — read-only; calls DetectLegacy + classify
//
// This test exercises steps 4 and 5 directly through the service, with a fully
// injectable InitService, and asserts that the CLAUDE.md in tmpDir is
// byte-identical after the call and no new files are created.
func TestInitCheckMode_NoCWDSideEffects(t *testing.T) {
	// Build a temporary working directory with a CLAUDE.md of known content.
	tmpDir := t.TempDir()

	const claudeContent = `# CLAUDE.md

This project uses Go with CGO and the fts5 build tag.

## Build

Use make build.

## Conventions

Follow the existing code style.
`

	claudeMDPath := filepath.Join(tmpDir, "CLAUDE.md")
	if err := os.WriteFile(claudeMDPath, []byte(claudeContent), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	// Snapshot the directory entries before the check run.
	entriesBefore := dirEntries(t, tmpDir)

	// Boot an injectable InitService backed by in-memory SQLite.
	// opts is the zero value: upsertBlock is nil, so EnsureGlobalManual and
	// UpsertRepoBlock are no-ops even if called (--check skips them anyway).
	svc := newCheckTestInitService(t)

	// Step 4: RunDrift — the read-only drift scan.
	_, driftErr := svc.RunDrift(tmpDir)
	if driftErr != nil {
		t.Fatalf("RunDrift: %v", driftErr)
	}

	// Step 5: Plan — the read-only legacy detection + classification pass.
	ctx := context.Background()
	_, planErr := svc.Plan(ctx, tmpDir)
	if planErr != nil {
		t.Fatalf("Plan: %v", planErr)
	}

	// Assert CLAUDE.md is byte-identical to what we wrote.
	gotBytes, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("read CLAUDE.md after check: %v", err)
	}
	if string(gotBytes) != claudeContent {
		t.Errorf("CLAUDE.md was modified by check mode\ngot:\n%s\nwant:\n%s", gotBytes, claudeContent)
	}

	// Assert no new files were created in the working directory.
	entriesAfter := dirEntries(t, tmpDir)
	if len(entriesAfter) != len(entriesBefore) {
		t.Errorf("check mode created or deleted files in cwd:\nbefore: %v\nafter:  %v",
			entriesBefore, entriesAfter)
	}
	for name := range entriesAfter {
		if _, existed := entriesBefore[name]; !existed {
			t.Errorf("check mode created unexpected file: %s", name)
		}
	}
}

// dirEntries returns the top-level entries in dir as a name→size map.
// Size is used as a lightweight content fingerprint alongside the name.
func dirEntries(t *testing.T, dir string) map[string]int64 {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	m := make(map[string]int64, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("entry info %s: %v", e.Name(), err)
		}
		m[e.Name()] = info.Size()
	}
	return m
}

// newCheckTestInitService builds an InitService with in-memory SQLite databases
// and zero-value InitServiceOptions (no upsertBlock, no manualContent).
// This mirrors the service configuration used by the --check code path in
// newInitCmd: the block-writing callbacks are nil, so none of the write steps
// can fire even if accidentally reached.
func newCheckTestInitService(t *testing.T) *service.InitService {
	t.Helper()

	sddDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open sdd db: %v", err)
	}
	sddDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sddDB.Close() })

	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	cfg := config.Default()
	cfg.Workflow.Dir = t.TempDir() // redirect any spec-dir writes away from ~

	sddStore := store.NewSDDStore(sddDB)
	sddSvc := service.NewSDDService(sddStore, cfg, "test-project", nil)

	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	memSvc := service.NewMemoryService(projectStore, globalStore, cfg, "test-project", embed.NopEmbedder{})

	// Zero-value options: upsertBlock is nil → write steps are no-ops.
	return service.NewInitService(cfg, sddSvc, memSvc, "test-project", service.InitServiceOptions{})
}
