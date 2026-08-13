// Package service — this file tests SpecDocWrite's SPEC-118 S4 budget
// branch (D11/AC20/AC21). Table-driven per the repo's own convention; no
// mocks, a real in-memory SQLite store throughout.
package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

const validBudgetTOMLForSpecDocWrite = `
schema_version = 1
margin = 2
radius = ["**"]

[[quota]]
dir = "internal/x"
max_new_symbols = 5
`

// TestSpecDocWrite_Budget_ValidDocument covers AC20's positive row: a
// valid budget.toml is written and ParseBudget re-reads it identically.
func TestSpecDocWrite_Budget_ValidDocument(t *testing.T) {
	svc, workflowDir, repoDir := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	if err := os.MkdirAll(filepath.Join(repoDir, "internal/x"), 0o755); err != nil {
		t.Fatalf("mkdir internal/x: %v", err)
	}

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	resp, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindBudget, Content: validBudgetTOMLForSpecDocWrite,
	})
	if err != nil {
		t.Fatalf("SpecDocWrite(budget, valid): %v", err)
	}

	wantPath := filepath.Join(workflowDir, "wirvii-mneme", "specs", spec.ID, "budget.toml")
	if resp.Path != wantPath {
		t.Errorf("Path = %q, want %q", resp.Path, wantPath)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read written budget.toml: %v", err)
	}
	if string(data) != validBudgetTOMLForSpecDocWrite {
		t.Errorf("file content = %q, want the exact document written", string(data))
	}
}

// TestSpecDocWrite_Budget_InvalidDocument_DoesNotWrite covers AC20's
// negative row (G24): an invalid document is rejected and the file never
// appears on disk.
func TestSpecDocWrite_Budget_InvalidDocument_DoesNotWrite(t *testing.T) {
	svc, workflowDir, _ := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	invalid := `
schema_version = 1
margin = 2
`
	_, err = svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindBudget, Content: invalid,
	})
	if err == nil {
		t.Fatal("SpecDocWrite(budget, invalid): want error, got nil")
	}

	wantPath := filepath.Join(workflowDir, "wirvii-mneme", "specs", spec.ID, "budget.toml")
	if _, statErr := os.Stat(wantPath); !os.IsNotExist(statErr) {
		t.Errorf("budget.toml exists at %q after a rejected write, want it absent", wantPath)
	}
}

// TestSpecDocWrite_Budget_OverwriteRejectionPreservesPriorContent covers
// AC20's "conserve prior content byte for byte" half.
func TestSpecDocWrite_Budget_OverwriteRejectionPreservesPriorContent(t *testing.T) {
	svc, workflowDir, repoDir := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	if err := os.MkdirAll(filepath.Join(repoDir, "internal/x"), 0o755); err != nil {
		t.Fatalf("mkdir internal/x: %v", err)
	}

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	if _, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindBudget, Content: validBudgetTOMLForSpecDocWrite,
	}); err != nil {
		t.Fatalf("SpecDocWrite(budget, first valid write): %v", err)
	}

	_, err = svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindBudget, Content: "not valid toml at all {{{",
	})
	if err == nil {
		t.Fatal("SpecDocWrite(budget, second invalid write): want error, got nil")
	}

	wantPath := filepath.Join(workflowDir, "wirvii-mneme", "specs", spec.ID, "budget.toml")
	data, readErr := os.ReadFile(wantPath)
	if readErr != nil {
		t.Fatalf("read budget.toml: %v", readErr)
	}
	if string(data) != validBudgetTOMLForSpecDocWrite {
		t.Errorf("file content = %q after a rejected overwrite, want the ORIGINAL valid document preserved byte for byte", string(data))
	}
}

// TestSpecDocWrite_Budget_AnchorValidation covers AC21's four rows: a
// [[modify]] file that does not exist -> rejected naming the path; an
// existing file with a symbol the extractor cannot find -> rejected naming
// the symbol; the same pair with a symbol that DOES exist -> accepted; a
// [[quota]].dir that does not exist -> rejected naming the dir, and one
// that does -> accepted.
func TestSpecDocWrite_Budget_AnchorValidation(t *testing.T) {
	svc, _, repoDir := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	if err := os.MkdirAll(filepath.Join(repoDir, "internal/x"), 0o755); err != nil {
		t.Fatalf("mkdir internal/x: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "internal/x/existing.go"), []byte("package x\n\nfunc RealSymbol() {}\n"), 0o644); err != nil {
		t.Fatalf("write existing.go: %v", err)
	}

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	docMissingFile := `
schema_version = 1
margin = 0
radius = ["**"]
[[modify]]
file = "internal/x/missing.go"
symbol = "RealSymbol"
`
	_, err = svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindBudget, Content: docMissingFile})
	if err == nil {
		t.Fatal("SpecDocWrite(budget, missing file): want error, got nil")
	}
	if !strings.Contains(err.Error(), "internal/x/missing.go") {
		t.Errorf("error = %q, want it to name the missing file", err.Error())
	}

	docMissingSymbol := `
schema_version = 1
margin = 0
radius = ["**"]
[[modify]]
file = "internal/x/existing.go"
symbol = "NoSuchSymbol"
`
	_, err = svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindBudget, Content: docMissingSymbol})
	if err == nil {
		t.Fatal("SpecDocWrite(budget, missing symbol): want error, got nil")
	}
	if !strings.Contains(err.Error(), "NoSuchSymbol") {
		t.Errorf("error = %q, want it to name the missing symbol", err.Error())
	}

	docResolvingModify := `
schema_version = 1
margin = 0
radius = ["**"]
[[modify]]
file = "internal/x/existing.go"
symbol = "RealSymbol"
`
	if _, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindBudget, Content: docResolvingModify}); err != nil {
		t.Fatalf("SpecDocWrite(budget, resolving modify): unexpected error: %v", err)
	}

	docMissingQuotaDir := `
schema_version = 1
margin = 0
radius = ["**"]
[[quota]]
dir = "internal/nope"
max_new_symbols = 1
`
	_, err = svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindBudget, Content: docMissingQuotaDir})
	if err == nil {
		t.Fatal("SpecDocWrite(budget, missing quota dir): want error, got nil")
	}
	if !strings.Contains(err.Error(), "internal/nope") {
		t.Errorf("error = %q, want it to name the missing dir", err.Error())
	}

	docResolvingQuotaDir := `
schema_version = 1
margin = 0
radius = ["**"]
[[quota]]
dir = "internal/x"
max_new_symbols = 1
`
	if _, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindBudget, Content: docResolvingQuotaDir}); err != nil {
		t.Fatalf("SpecDocWrite(budget, resolving quota dir): unexpected error: %v", err)
	}
}

// TestSpecDocWrite_Budget_EmptyRepoDirSkipsAnchorResolution covers D15:
// with repoDir never configured, an unresolvable [[modify]]/[[quota]]
// anchor is NOT rejected — structural validation still runs, but there is
// no working tree to resolve against, and mneme never falls back to
// os.Getwd().
func TestSpecDocWrite_Budget_EmptyRepoDirSkipsAnchorResolution(t *testing.T) {
	svc, _ := newTestSDDServiceWithWorkflowDir(t, "wirvii/mneme")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	docUnresolvable := `
schema_version = 1
margin = 0
radius = ["**"]
[[modify]]
file = "definitely/does/not/exist.go"
symbol = "Nope"
[[quota]]
dir = "definitely/not/a/dir"
max_new_symbols = 1
`
	if _, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindBudget, Content: docUnresolvable,
	}); err != nil {
		t.Fatalf("SpecDocWrite(budget, repoDir empty): unexpected error: %v (D15 — no os.Getwd() fallback, anchor resolution must be skipped)", err)
	}
}
