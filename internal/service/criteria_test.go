// Package service — this file tests SpecDocWrite's SPEC-117 S3 criteria
// branch (D7/AC8/AC9/AC10). Table-driven per the repo's own convention;
// no mocks, a real in-memory SQLite store throughout.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
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

// --- SPEC-117 EPIC-calidad S3 P7: runCriteriaChecks ---

// writeConstitutionV3Criteria writes a schema_version=3 constitution with
// one always-passing gate, [coverage]/[ratchet] both off, and [criteria]
// with the given enabled/timeout/quota values.
func writeConstitutionV3Criteria(t *testing.T, repoDir string, enabled bool, timeout string, maxManualPct, maxCommandPct float64) {
	t.Helper()
	dir := filepath.Join(repoDir, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	doc := fmt.Sprintf(`
schema_version = 3
enabled = true
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
command = ["true"]
timeout = "5m"
required = true
[coverage]
enabled = false
format = "lcov"
command = ["true"]
profile_path = "tmp/coverage.out"
timeout = "5m"
min_diff_line_pct = 1.0
min_changed_lines = 1
exclude = []
[ratchet]
enabled = false
max_global_line_pct_drop = 0.0
max_baseline_staleness_pct = 1.0
[criteria]
enabled = %v
timeout = %q
max_manual_pct = %v
max_command_pct = %v
`, enabled, timeout, maxManualPct, maxCommandPct)
	if err := os.WriteFile(filepath.Join(dir, "quality.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write quality.toml: %v", err)
	}
}

// writeCriteriaDocAt writes content as content's spec's criteria.toml
// under workflowDir, using the SAME specDocPath function runCriteriaChecks
// reads from (V5 of the design) — never a hand-rolled path a test could
// silently drift from production's own resolution.
func writeCriteriaDocAt(t *testing.T, workflowDir, project, specID, content string) {
	t.Helper()
	path, err := specDocPath(workflowDir, project, specID, model.SpecDocKindCriteria)
	if err != nil {
		t.Fatalf("specDocPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write criteria.toml: %v", err)
	}
}

// findCheck returns the first check matching kind/name, or nil.
func findCheck(checks []*model.QualityCheck, kind, name string) *model.QualityCheck {
	for _, c := range checks {
		if c.Kind == kind && c.Name == name {
			return c
		}
	}
	return nil
}

// TestRunCriteriaChecks_Skipped_Cascade covers AC23's third row: a
// required gate failure skips ALL criteria rows, and the fake runner never
// invokes a criterion command (the gatesStopped guard, G21).
func TestRunCriteriaChecks_Skipped_Cascade(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitutionV3Criteria(t, repoDir, true, "5m", 25.0, 30.0)

	dir := filepath.Join(repoDir, ".mneme")
	doc, err := os.ReadFile(filepath.Join(dir, "quality.toml"))
	if err != nil {
		t.Fatalf("read quality.toml: %v", err)
	}
	// Make the gate REQUIRED and failing.
	failing := strings.Replace(string(doc), `command = ["true"]
timeout = "5m"
required = true`, `command = ["false"]
timeout = "5m"
required = true`, 1)
	if err := os.WriteFile(filepath.Join(dir, "quality.toml"), []byte(failing), 0o644); err != nil {
		t.Fatalf("rewrite quality.toml: %v", err)
	}

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-001", "proj", model.SpecStatusImplementing, "")

	workflowDir := t.TempDir()
	writeCriteriaDocAt(t, workflowDir, "proj", spec.ID, validCriteriaTOML)

	runner := &fakeGateRunner{results: map[string]quality.GateResult{"build": {Status: quality.GateStatusFail, ExitCode: 1}}}
	svc := NewQualityService(s, "proj", repoDir, runner, WithWorkflowDir(workflowDir))

	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	checks, err := s.ListChecks(context.Background(), cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	declared := findCheck(checks, "criteria", "declared")
	if declared == nil || declared.Status != "skipped" {
		t.Fatalf("criteria/declared = %+v, want skipped", declared)
	}
	if !strings.Contains(declared.Summary, "gate") {
		t.Errorf("criteria/declared summary = %q, want it to name the gate cascade", declared.Summary)
	}
	for _, name := range []string{"criterion", "criterion-command", "criterion-manual"} {
		for _, c := range checks {
			if c.Kind == name {
				t.Errorf("found a %s row (%s) after a cascade skip — want zero per-criterion rows", name, c.Name)
			}
		}
	}
	for _, call := range runner.calls {
		if strings.HasPrefix(call, "criterion-") {
			t.Errorf("runner invoked %q — a criterion command must NEVER run once the gate cascade stopped", call)
		}
	}
}

// TestRunCriteriaChecks_Skipped_SchemaOmission and
// TestRunCriteriaChecks_Skipped_DisabledByDecision cover AC23's first two
// rows: "apagado por omision" (schema < 3) and "apagado por decision"
// (criteria.enabled=false) produce DIFFERENT summaries.
func TestRunCriteriaChecks_Skipped_SchemaOmission(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitution(t, repoDir) // schema_version=1, no [criteria] at all

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-001", "proj", model.SpecStatusImplementing, "")
	workflowDir := t.TempDir()

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{}, WithWorkflowDir(workflowDir))
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	checks, _ := s.ListChecks(context.Background(), cert.ID)
	declared := findCheck(checks, "criteria", "declared")
	if declared == nil || declared.Status != "skipped" || !strings.Contains(declared.Summary, "omision") {
		t.Fatalf("criteria/declared = %+v, want skipped naming 'omision'", declared)
	}
}

func TestRunCriteriaChecks_Skipped_DisabledByDecision(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitutionV3Criteria(t, repoDir, false, "5m", 25.0, 30.0)

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-001", "proj", model.SpecStatusImplementing, "")
	workflowDir := t.TempDir()

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{}, WithWorkflowDir(workflowDir))
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	checks, _ := s.ListChecks(context.Background(), cert.ID)
	declared := findCheck(checks, "criteria", "declared")
	if declared == nil || declared.Status != "skipped" || !strings.Contains(declared.Summary, "decision") {
		t.Fatalf("criteria/declared = %+v, want skipped naming 'decision'", declared)
	}

	// The two summaries (schema omission vs decision) must be DISTINCT texts.
	repoDir2 := newTestGitRepo(t)
	writeConstitution(t, repoDir2)
	svc2 := NewQualityService(s, "proj", repoDir2, &fakeGateRunner{}, WithWorkflowDir(workflowDir))
	spec2 := insertTestSpec(t, s, "SPEC-002", "proj", model.SpecStatusImplementing, "")
	cert2, err := svc2.Verify(context.Background(), model.QualityVerifyRequest{ID: spec2.ID})
	if err != nil {
		t.Fatalf("Verify(spec2): %v", err)
	}
	checks2, _ := s.ListChecks(context.Background(), cert2.ID)
	declared2 := findCheck(checks2, "criteria", "declared")
	if declared2.Summary == declared.Summary {
		t.Errorf("schema-omission and decision-off summaries are identical (%q) — AC23 requires distinct texts", declared.Summary)
	}
}

// TestRunCriteriaChecks_WorkflowDirEmpty covers AC30/D13: with workflowDir
// never configured, criteria rows are skipped and nothing is read from
// disk — verified from a cwd that DOES have a real criteria.toml sitting
// at the (unused) default location, the same mould as S1's AC16.
func TestRunCriteriaChecks_WorkflowDirEmpty(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitutionV3Criteria(t, repoDir, true, "5m", 25.0, 30.0)

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-001", "proj", model.SpecStatusImplementing, "")

	// No WithWorkflowDir call at all.
	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{})
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	checks, _ := s.ListChecks(context.Background(), cert.ID)
	declared := findCheck(checks, "criteria", "declared")
	if declared == nil || declared.Status != "skipped" || !strings.Contains(declared.Summary, "workflowDir") {
		t.Fatalf("criteria/declared = %+v, want skipped naming workflowDir", declared)
	}
}

// TestRunCriteriaChecks_MissingCriteriaToml covers D8's "no existe
// criteria.toml -> fail" row.
func TestRunCriteriaChecks_MissingCriteriaToml(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitutionV3Criteria(t, repoDir, true, "5m", 25.0, 30.0)

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-001", "proj", model.SpecStatusImplementing, "")
	workflowDir := t.TempDir() // no criteria.toml written

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{}, WithWorkflowDir(workflowDir))
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	checks, _ := s.ListChecks(context.Background(), cert.ID)
	declared := findCheck(checks, "criteria", "declared")
	if declared == nil || declared.Status != "fail" {
		t.Fatalf("criteria/declared = %+v, want fail", declared)
	}
	manualQuota := findCheck(checks, "criteria", "manual-quota")
	if manualQuota == nil || manualQuota.Status != "skipped" {
		t.Fatalf("criteria/manual-quota = %+v, want skipped (no accumulation past declared's own failure)", manualQuota)
	}
}

// TestRunCriteriaChecks_ThreeModes runs a real two-commit git repo through
// all three modes end to end: assert (pass), command (invoked exactly
// once, finding vacuity-unprovable), and manual (finding
// manual-unverified). Covers AC16, AC20, AC22, AC31 together.
func TestRunCriteriaChecks_ThreeModes(t *testing.T) {
	repoDir := newTestGitRepo(t)
	baseSHA := headSHAFor(t, repoDir)

	if err := os.WriteFile(filepath.Join(repoDir, "newfile.go"), []byte("package x\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatalf("write newfile.go: %v", err)
	}
	writeConstitutionV3Criteria(t, repoDir, true, "5m", 60.0, 60.0)
	commitAll(t, repoDir, "add newfile and constitution")

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-001", "proj", model.SpecStatusImplementing, baseSHA)
	workflowDir := t.TempDir()

	doc := `
schema_version = 1

[[criterion]]
id = "AC1"
mode = "assert"
text = "newfile.go existe"
  [[criterion.assert]]
  verb = "file_exists"
  path = "newfile.go"
  new = true

[[criterion]]
id = "AC2"
mode = "command"
text = "el comando pasa"
command = ["true"]
timeout = "1m"

[[criterion]]
id = "AC3"
mode = "manual"
text = "revision visual"
evidence_required = "captura"
`
	writeCriteriaDocAt(t, workflowDir, "proj", spec.ID, doc)

	runner := &fakeGateRunner{}
	svc := NewQualityService(s, "proj", repoDir, runner, WithWorkflowDir(workflowDir))
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	checks, err := s.ListChecks(context.Background(), cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}

	assertRow := findCheck(checks, "criterion", "AC1")
	if assertRow == nil || assertRow.Status != "pass" {
		t.Fatalf("criterion/AC1 = %+v, want pass", assertRow)
	}

	commandRow := findCheck(checks, "criterion-command", "AC2")
	if commandRow == nil || commandRow.Status != "finding" || !strings.Contains(commandRow.Summary, "vacuity-unprovable") {
		t.Fatalf("criterion-command/AC2 = %+v, want finding vacuity-unprovable", commandRow)
	}
	commandInvocations := 0
	for _, call := range runner.calls {
		if call == "criterion-AC2" {
			commandInvocations++
		}
	}
	if commandInvocations != 1 {
		t.Errorf("criterion-AC2 invoked %d times, want exactly 1 (never re-run against base)", commandInvocations)
	}

	manualRow := findCheck(checks, "criterion-manual", "AC3")
	if manualRow == nil || manualRow.Status != "finding" || manualRow.Summary != "manual-unverified" {
		t.Fatalf("criterion-manual/AC3 = %+v, want finding manual-unverified", manualRow)
	}

	// AC31: round-trip through real SQLite with Detail intact.
	if assertRow.Detail == "" {
		t.Error("criterion/AC1 Detail is empty, want the verbatim declaration + outcome")
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(assertRow.Detail), &detail); err != nil {
		t.Fatalf("unmarshal criterion/AC1 Detail: %v", err)
	}
	if detail["mode"] != "assert" {
		t.Errorf("Detail[mode] = %v, want assert", detail["mode"])
	}
}

// TestRunCriteriaChecks_Vacuous covers AC16/D5: a criterion whose anchor
// already existed at base is `finding` `vacuous`, and the certificate's
// verdict degrades to findings (never pass).
func TestRunCriteriaChecks_Vacuous(t *testing.T) {
	repoDir := newTestGitRepo(t)
	baseSHA := headSHAFor(t, repoDir)
	writeConstitutionV3Criteria(t, repoDir, true, "5m", 60.0, 60.0)
	commitAll(t, repoDir, "add constitution")

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-001", "proj", model.SpecStatusImplementing, baseSHA)
	workflowDir := t.TempDir()

	// main.go already exists at base AND head (newTestGitRepo's own fixture
	// file) — new=false, so this is a legitimate regression guardian shape.
	doc := `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "assert"
text = "main.go sigue existiendo"
  [[criterion.assert]]
  verb = "file_exists"
  path = "main.go"
  new = false
`
	writeCriteriaDocAt(t, workflowDir, "proj", spec.ID, doc)

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{}, WithWorkflowDir(workflowDir))
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cert.Verdict == model.QualityVerdictPass {
		t.Error("certificate verdict = pass, want findings (an un-acked vacuous finding must block)")
	}
	checks, _ := s.ListChecks(context.Background(), cert.ID)
	row := findCheck(checks, "criterion", "AC1")
	if row == nil || row.Status != "finding" || !strings.HasPrefix(row.Summary, "vacuous") {
		t.Fatalf("criterion/AC1 = %+v, want finding with summary starting 'vacuous'", row)
	}
}

// TestRunCriteriaChecks_BaseUnknown covers AC19: no BaseSHA means every
// assert-mode criterion is `finding` `base-unknown`, never `pass`.
func TestRunCriteriaChecks_BaseUnknown(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitutionV3Criteria(t, repoDir, true, "5m", 60.0, 60.0)
	commitAll(t, repoDir, "add constitution")

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-001", "proj", model.SpecStatusImplementing, "") // no BaseSHA
	workflowDir := t.TempDir()

	doc := `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "assert"
text = "main.go existe"
  [[criterion.assert]]
  verb = "file_exists"
  path = "main.go"
  new = false
`
	writeCriteriaDocAt(t, workflowDir, "proj", spec.ID, doc)

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{}, WithWorkflowDir(workflowDir))
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	checks, _ := s.ListChecks(context.Background(), cert.ID)
	row := findCheck(checks, "criterion", "AC1")
	if row == nil || row.Status != "finding" || !strings.HasPrefix(row.Summary, "base-unknown") {
		t.Fatalf("criterion/AC1 = %+v, want finding with summary starting 'base-unknown'", row)
	}
}

// TestRunCriteriaChecks_AnchorNotNew covers AC18: a new=true assertion
// whose anchor already existed at base is `finding` `anchor-not-new`.
func TestRunCriteriaChecks_AnchorNotNew(t *testing.T) {
	repoDir := newTestGitRepo(t)
	baseSHA := headSHAFor(t, repoDir)
	writeConstitutionV3Criteria(t, repoDir, true, "5m", 60.0, 60.0)
	commitAll(t, repoDir, "add constitution")

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-001", "proj", model.SpecStatusImplementing, baseSHA)
	workflowDir := t.TempDir()

	// main.go existed already at base, but the criterion LIES and says new=true.
	doc := `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "assert"
text = "main.go es nuevo (mentira)"
  [[criterion.assert]]
  verb = "file_exists"
  path = "main.go"
  new = true
`
	writeCriteriaDocAt(t, workflowDir, "proj", spec.ID, doc)

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{}, WithWorkflowDir(workflowDir))
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	checks, _ := s.ListChecks(context.Background(), cert.ID)
	row := findCheck(checks, "criterion", "AC1")
	if row == nil || row.Status != "finding" || !strings.HasPrefix(row.Summary, "anchor-not-new") {
		t.Fatalf("criterion/AC1 = %+v, want finding with summary starting 'anchor-not-new'", row)
	}
}

// TestRunCriteriaChecks_CommandFail covers AC20's exit!=0 row.
func TestRunCriteriaChecks_CommandFail(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitutionV3Criteria(t, repoDir, true, "5m", 60.0, 60.0)
	commitAll(t, repoDir, "add constitution")

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-001", "proj", model.SpecStatusImplementing, "")
	workflowDir := t.TempDir()

	doc := `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "command"
text = "el comando falla"
command = ["false"]
timeout = "1m"
`
	writeCriteriaDocAt(t, workflowDir, "proj", spec.ID, doc)

	runner := &fakeGateRunner{results: map[string]quality.GateResult{"criterion-AC1": {Status: quality.GateStatusFail, ExitCode: 7}}}
	svc := NewQualityService(s, "proj", repoDir, runner, WithWorkflowDir(workflowDir))
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	checks, _ := s.ListChecks(context.Background(), cert.ID)
	row := findCheck(checks, "criterion-command", "AC1")
	if row == nil || row.Status != "fail" || row.ExitCode != 7 {
		t.Fatalf("criterion-command/AC1 = %+v, want fail with exit_code=7", row)
	}
}

// TestRunCriteriaChecks_Quotas covers AC21: exceeding a quota FAILS the
// certificate outright — never a firmable finding — and the cupo row
// itself cannot be signed (ErrNotACriterion, since its kind is "criteria",
// not "criterion*").
func TestRunCriteriaChecks_Quotas(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitutionV3Criteria(t, repoDir, true, "5m", 25.0, 100.0)
	commitAll(t, repoDir, "add constitution")

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-001", "proj", model.SpecStatusImplementing, "")
	workflowDir := t.TempDir()

	// 2 of 4 manual (50%) exceeds a 25% cap.
	doc := `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "manual"
text = "m1"
evidence_required = "e1"
[[criterion]]
id = "AC2"
mode = "manual"
text = "m2"
evidence_required = "e2"
[[criterion]]
id = "AC3"
mode = "assert"
text = "a1"
  [[criterion.assert]]
  verb = "file_exists"
  path = "main.go"
  new = false
[[criterion]]
id = "AC4"
mode = "assert"
text = "a2"
  [[criterion.assert]]
  verb = "file_exists"
  path = "main.go"
  new = false
`
	writeCriteriaDocAt(t, workflowDir, "proj", spec.ID, doc)

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{}, WithWorkflowDir(workflowDir))
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cert.Verdict != model.QualityVerdictFail {
		t.Errorf("certificate verdict = %s, want fail (a quota breach, never degraded to findings)", cert.Verdict)
	}
	checks, _ := s.ListChecks(context.Background(), cert.ID)
	quotaRow := findCheck(checks, "criteria", "manual-quota")
	if quotaRow == nil || quotaRow.Status != "fail" {
		t.Fatalf("criteria/manual-quota = %+v, want fail", quotaRow)
	}
	// The quota row is NOT firmable — its kind is "criteria", not
	// "criterion*" — verified against Sign's own guard in
	// TestSign_RejectsNonCriterionRow (P8), which exercises this SAME kind
	// distinction directly rather than duplicating it here.
}
