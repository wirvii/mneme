// Package service — this file tests SpecDocWrite's SPEC-117 S3 criteria
// branch (D7/AC8/AC9/AC10). Table-driven per the repo's own convention;
// no mocks, a real in-memory SQLite store throughout.
package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// newTestSDDServiceWithRepoDir mirrors newTestSDDServiceWithWorkflowDir but
// ALSO fixes repoDir to a fresh t.TempDir() — the "working tree" anchor
// resolution (D7) needs to resolve against.
func newTestSDDServiceWithRepoDir(t *testing.T, project string) (svc *SDDService, workflowDir, repoDir string) {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	workflowDir = t.TempDir()
	cfg.Workflow.Dir = workflowDir
	repoDir = t.TempDir()

	svc = NewSDDService(sddStore, cfg, project, nil)
	svc.WithRepoDir(repoDir)
	return svc, workflowDir, repoDir
}

const validCriteriaTOML = `
schema_version = 1

[[criterion]]
id = "AC1"
mode = "assert"
text = "internal/quality gana el parser de criterios."
  [[criterion.assert]]
  verb = "file_exists"
  path = "internal/quality/criteria.go"
  new = true
`

// TestSpecDocWrite_Criteria_ValidDocument covers AC9's positive row: a
// valid criteria.toml is written and ParseCriteria re-reads it identically.
func TestSpecDocWrite_Criteria_ValidDocument(t *testing.T) {
	svc, workflowDir, _ := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	resp, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindCriteria, Content: validCriteriaTOML,
	})
	if err != nil {
		t.Fatalf("SpecDocWrite(criteria, valid): %v", err)
	}

	wantPath := filepath.Join(workflowDir, "wirvii-mneme", "specs", spec.ID, "criteria.toml")
	if resp.Path != wantPath {
		t.Errorf("Path = %q, want %q", resp.Path, wantPath)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read written criteria.toml: %v", err)
	}
	if string(data) != validCriteriaTOML {
		t.Errorf("file content = %q, want the exact document written", string(data))
	}
}

// TestSpecDocWrite_Criteria_InvalidDocument_DoesNotWrite covers AC9's
// negative row (and the guardian for G6): an invalid document is rejected
// and the file never appears on disk.
func TestSpecDocWrite_Criteria_InvalidDocument_DoesNotWrite(t *testing.T) {
	svc, workflowDir, _ := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	invalid := `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "assert"
text = "x"
`
	_, err = svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindCriteria, Content: invalid,
	})
	if err == nil {
		t.Fatal("SpecDocWrite(criteria, invalid): want error, got nil")
	}

	wantPath := filepath.Join(workflowDir, "wirvii-mneme", "specs", spec.ID, "criteria.toml")
	if _, statErr := os.Stat(wantPath); !os.IsNotExist(statErr) {
		t.Errorf("criteria.toml exists at %q after a rejected write, want it absent", wantPath)
	}
}

// TestSpecDocWrite_Criteria_OverwriteRejectionPreservesPriorContent covers
// AC9's "or, if it already existed, conserve its content byte for byte"
// half: a second, invalid write must never clobber a prior valid one.
func TestSpecDocWrite_Criteria_OverwriteRejectionPreservesPriorContent(t *testing.T) {
	svc, workflowDir, _ := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	if _, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindCriteria, Content: validCriteriaTOML,
	}); err != nil {
		t.Fatalf("SpecDocWrite(criteria, first valid write): %v", err)
	}

	_, err = svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindCriteria, Content: "not valid toml at all {{{",
	})
	if err == nil {
		t.Fatal("SpecDocWrite(criteria, second invalid write): want error, got nil")
	}

	wantPath := filepath.Join(workflowDir, "wirvii-mneme", "specs", spec.ID, "criteria.toml")
	data, readErr := os.ReadFile(wantPath)
	if readErr != nil {
		t.Fatalf("read criteria.toml: %v", readErr)
	}
	if string(data) != validCriteriaTOML {
		t.Errorf("file content = %q after a rejected overwrite, want the ORIGINAL valid document preserved byte for byte", string(data))
	}
}

// TestSpecDocWrite_Criteria_AnchorValidation covers AC8's declare-time
// anchor resolution against the REAL working tree: a new=false path that
// does not exist is rejected naming the path; one that exists is accepted.
func TestSpecDocWrite_Criteria_AnchorValidation(t *testing.T) {
	svc, _, repoDir := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(repoDir, "existing.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("write existing.go: %v", err)
	}

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	docWithMissingAnchor := `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "assert"
text = "x"
  [[criterion.assert]]
  verb = "file_exists"
  path = "docs/api/mcp.md"
  new = false
`
	_, err = svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindCriteria, Content: docWithMissingAnchor,
	})
	if err == nil {
		t.Fatal("SpecDocWrite(criteria, new=false missing anchor): want error, got nil")
	}
	if !strings.Contains(err.Error(), "docs/api/mcp.md") {
		t.Errorf("error = %q, want it to name the missing anchor path", err.Error())
	}

	docWithExistingAnchor := `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "assert"
text = "x"
  [[criterion.assert]]
  verb = "file_exists"
  path = "existing.go"
  new = false
`
	if _, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindCriteria, Content: docWithExistingAnchor,
	}); err != nil {
		t.Fatalf("SpecDocWrite(criteria, new=false existing anchor): unexpected error: %v", err)
	}
}

// TestSpecDocWrite_Criteria_EmptyRepoDirSkipsAnchorResolution covers D13:
// with repoDir never configured, a new=false anchor that would fail
// resolution against a real tree is NOT rejected — structural validation
// still runs, but there is no working tree to resolve against, and mneme
// never falls back to os.Getwd().
func TestSpecDocWrite_Criteria_EmptyRepoDirSkipsAnchorResolution(t *testing.T) {
	svc, _ := newTestSDDServiceWithWorkflowDir(t, "wirvii/mneme")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	docWithUnresolvableAnchor := `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "assert"
text = "x"
  [[criterion.assert]]
  verb = "file_exists"
  path = "definitely/does/not/exist.go"
  new = false
`
	if _, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindCriteria, Content: docWithUnresolvableAnchor,
	}); err != nil {
		t.Fatalf("SpecDocWrite(criteria, repoDir empty): unexpected error: %v (D13 — no os.Getwd() fallback, anchor resolution must be skipped)", err)
	}
}
