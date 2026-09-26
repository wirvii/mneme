// Package service — this file tests SpecDocWrite's SPEC-117 S3 criteria
// branch (D7/AC8/AC9/AC10). Table-driven per the repo's own convention;
// no mocks, a real in-memory SQLite store throughout.
package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

	// SPEC-158 D1.c: the lenient reader no longer validates mode
	// cross-rules (an "assert" criterion with zero assertions is now a
	// harmless, discarded key) — a genuinely invalid document is one with
	// an unknown key at the criterion level, which DisallowUnknownFields
	// still rejects.
	invalid := `
schema_version = 1
[[criterion]]
id = "AC1"
text = "x"
verbo = "no existe"
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

// TestSpecDocWrite_Criteria_MinimalDocument covers AC7's first half: a
// document with nothing but schema_version + one id/text criterion — no
// mode, no assert, no anchors to resolve — is written byte for byte
// (SPEC-158 D1.c: the evaluator is gone, so a criteria.toml no longer needs
// anything beyond id+text).
func TestSpecDocWrite_Criteria_MinimalDocument(t *testing.T) {
	svc, workflowDir, _ := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	minimal := `
schema_version = 1
[[criterion]]
id = "AC1"
text = "una afirmacion que el qa-tester puede comprobar ejecutando algo"
`
	resp, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindCriteria, Content: minimal,
	})
	if err != nil {
		t.Fatalf("SpecDocWrite(criteria, minimal): %v", err)
	}

	wantPath := filepath.Join(workflowDir, "wirvii-mneme", "specs", spec.ID, "criteria.toml")
	if resp.Path != wantPath {
		t.Errorf("Path = %q, want %q", resp.Path, wantPath)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read written criteria.toml: %v", err)
	}
	if string(data) != minimal {
		t.Errorf("file content = %q, want the exact document written", string(data))
	}
}

// TestSpecDocWrite_BudgetKindRejected covers SPEC-158 AC7's second half:
// spec_doc_write kind "budget" — retired along with the certification
// mechanism's budget-against-the-graph tramo — is rejected with
// model.ErrUnknownSpecDocKind, and no budget.toml appears anywhere under
// the spec's workflow directory.
func TestSpecDocWrite_BudgetKindRejected(t *testing.T) {
	svc, workflowDir, _ := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	_, err = svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKind("budget"), Content: "schema_version = 1\n",
	})
	if !errors.Is(err, model.ErrUnknownSpecDocKind) {
		t.Fatalf("SpecDocWrite(budget) error = %v, want ErrUnknownSpecDocKind", err)
	}

	specDir := filepath.Join(workflowDir, "wirvii-mneme", "specs", spec.ID)
	if _, statErr := os.Stat(filepath.Join(specDir, "budget.toml")); !os.IsNotExist(statErr) {
		t.Errorf("budget.toml exists at %q after a rejected write, want it absent", specDir)
	}
}
