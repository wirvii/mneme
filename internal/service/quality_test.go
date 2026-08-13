package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
	"github.com/wirvii/mneme/internal/store"
)

// fakeGateRunner is the D14 seam every test in this file injects instead of
// quality.ExecRunner — nothing here ever executes make, sh, or the real
// project suite (AC18/R3). It returns a fixed GateResult per gate name
// (defaulting to pass) and records every gate name it was asked to run, so a
// test can assert which gates were actually invoked (e.g. to prove a
// "skipped" gate was never executed for real).
type fakeGateRunner struct {
	results map[string]quality.GateResult
	calls   []string
}

func (f *fakeGateRunner) Run(_ context.Context, gate quality.Gate, _ string) quality.GateResult {
	f.calls = append(f.calls, gate.Name)
	if res, ok := f.results[gate.Name]; ok {
		res.Name = gate.Name
		return res
	}
	return quality.GateResult{Name: gate.Name, Status: quality.GateStatusPass}
}

// newTestQualityStore opens a fresh in-memory SQLite database (migrated) and
// returns a bare *store.SDDStore for QualityService tests.
func newTestQualityStore(t *testing.T) *store.SDDStore {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	return store.NewSDDStore(database)
}

// writeConstitution writes a valid two-gate constitution (build, test) under
// repoDir/.mneme/quality.toml.
func writeConstitution(t *testing.T, repoDir string, extraGates ...string) {
	t.Helper()
	dir := filepath.Join(repoDir, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	doc := `
schema_version = 1
enabled = true
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
command = ["true"]
timeout = "5m"
required = true
[[gate]]
name = "test"
command = ["true"]
timeout = "5m"
required = true
`
	for _, g := range extraGates {
		doc += g
	}
	if err := os.WriteFile(filepath.Join(dir, "quality.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write quality.toml: %v", err)
	}
}

// commitAll stages and commits every change in repoDir.
func commitAll(t *testing.T, repoDir, message string) {
	t.Helper()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", message}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// insertTestSpec inserts a spec directly via the store (bypassing the full
// SpecAdvance lifecycle, which this file's tests do not need).
func insertTestSpec(t *testing.T, s *store.SDDStore, id, project string, status model.SpecStatus, baseSHA string) *model.Spec {
	t.Helper()
	spec := &model.Spec{
		ID: id, Title: "Test spec", Status: status, Project: project,
		Lane: model.LaneStandard, BaseSHA: baseSHA,
	}
	// CreateSpec persists Status verbatim (no state-machine check) — the
	// SDDStore layer trusts the caller. That is exactly what this test
	// helper needs: it seeds a spec directly at the status under test
	// without walking SpecAdvance's full transition chain.
	if err := s.CreateSpec(context.Background(), spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	return spec
}

// TestQualityService_Verify_NilRunner_Errors covers AC18/D14/R3: a
// QualityService with no injected Runner refuses to run rather than
// silently constructing a real quality.ExecRunner.
func TestQualityService_Verify_NilRunner_Errors(t *testing.T) {
	s := newTestQualityStore(t)
	insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")

	svc := NewQualityService(s, "proj", t.TempDir(), nil)
	_, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
	if err == nil {
		t.Fatal("Verify with nil runner succeeded, want an error")
	}
}

// TestQualityService_Verify_WrongStatus covers the D5 precondition: Verify
// only admits implementing or qa.
func TestQualityService_Verify_WrongStatus(t *testing.T) {
	s := newTestQualityStore(t)
	insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusDraft, "")

	svc := NewQualityService(s, "proj", t.TempDir(), &fakeGateRunner{})
	_, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
	if !errors.Is(err, model.ErrInvalidTransition) {
		t.Errorf("Verify(draft spec) error = %v, want ErrInvalidTransition", err)
	}
}

// TestQualityService_Verify_MissingConstitution covers the absent-file path.
func TestQualityService_Verify_MissingConstitution(t *testing.T) {
	s := newTestQualityStore(t)
	insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")

	svc := NewQualityService(s, "proj", t.TempDir(), &fakeGateRunner{})
	_, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
	if !errors.Is(err, model.ErrInvalidConstitution) {
		t.Errorf("Verify(no constitution) error = %v, want ErrInvalidConstitution", err)
	}
}

// TestQualityService_Verify_InvalidConstitution covers an unparseable file.
func TestQualityService_Verify_InvalidConstitution(t *testing.T) {
	repoDir := newTestGitRepo(t)
	if err := os.MkdirAll(filepath.Join(repoDir, ".mneme"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".mneme", "quality.toml"), []byte("not valid toml at all ]["), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := newTestQualityStore(t)
	insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{})
	_, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
	if !errors.Is(err, model.ErrInvalidConstitution) {
		t.Errorf("Verify(invalid constitution) error = %v, want ErrInvalidConstitution", err)
	}
}

// TestQualityService_Verify_HappyPath_Pass covers the end-to-end pass case:
// clean tree, tracked/unchanged constitution, both gates pass.
func TestQualityService_Verify_HappyPath_Pass(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitution(t, repoDir)
	commitAll(t, repoDir, "add constitution")

	s := newTestQualityStore(t)
	insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")

	runner := &fakeGateRunner{}
	svc := NewQualityService(s, "proj", repoDir, runner, WithMnemeVersion("v-test"))

	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cert.Verdict != model.QualityVerdictPass {
		t.Errorf("Verdict = %q, want pass", cert.Verdict)
	}
	if cert.Dirty {
		t.Error("Dirty = true, want false (clean tree)")
	}
	if cert.MnemeVersion != "v-test" {
		t.Errorf("MnemeVersion = %q, want v-test", cert.MnemeVersion)
	}
	if len(runner.calls) != 2 {
		t.Errorf("runner was called %d times, want 2 (build, test)", len(runner.calls))
	}

	checks, err := s.ListChecks(context.Background(), cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	// 1 tree + 3 constitution + 2 gates = 6.
	if len(checks) != 6 {
		t.Fatalf("len(checks) = %d, want 6: %+v", len(checks), checks)
	}
}

// TestQualityService_Verify_RequiredGateFailure_SkipsRest covers D6: a
// required gate that fails stops execution — later gates never run for
// real and are recorded "skipped".
func TestQualityService_Verify_RequiredGateFailure_SkipsRest(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitution(t, repoDir, `
[[gate]]
name = "lint"
command = ["true"]
timeout = "5m"
required = true
`)
	commitAll(t, repoDir, "add constitution")

	s := newTestQualityStore(t)
	insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")

	runner := &fakeGateRunner{results: map[string]quality.GateResult{
		"build": {Status: quality.GateStatusFail, ExitCode: 1},
	}}
	svc := NewQualityService(s, "proj", repoDir, runner)

	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cert.Verdict != model.QualityVerdictFail {
		t.Errorf("Verdict = %q, want fail", cert.Verdict)
	}
	// Only "build" (the failing gate) should have been RUN; test/lint must
	// never actually execute after it.
	if len(runner.calls) != 1 || runner.calls[0] != "build" {
		t.Errorf("runner.calls = %v, want exactly [build]", runner.calls)
	}

	checks, err := s.ListChecks(context.Background(), cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	var testStatus, lintStatus string
	for _, c := range checks {
		switch c.Name {
		case "test":
			testStatus = c.Status
		case "lint":
			lintStatus = c.Status
		}
	}
	if testStatus != "skipped" || lintStatus != "skipped" {
		t.Errorf("test/lint status = %q/%q, want skipped/skipped", testStatus, lintStatus)
	}
}

// TestQualityService_Verify_DirtyTree_FailsVerdict covers D8: an untracked
// file fails the certificate even though every gate passes.
func TestQualityService_Verify_DirtyTree_FailsVerdict(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitution(t, repoDir)
	commitAll(t, repoDir, "add constitution")

	if err := os.WriteFile(filepath.Join(repoDir, "untracked.txt"), []byte("oops"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	s := newTestQualityStore(t)
	insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{})
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cert.Verdict != model.QualityVerdictFail {
		t.Errorf("Verdict = %q, want fail (dirty worktree)", cert.Verdict)
	}
	if !cert.Dirty {
		t.Error("Dirty = false, want true")
	}
}

// TestQualityService_Verify_ConstitutionChangedInRange covers AC12 at the
// finding level: modifying the constitution within the spec's base_sha..HEAD
// range produces a "finding", not a hard failure by itself — the overall
// verdict still degrades to "findings" (not "pass") when nothing else fails.
func TestQualityService_Verify_ConstitutionChangedInRange(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitution(t, repoDir)
	commitAll(t, repoDir, "add constitution")

	s := newTestQualityStore(t)

	baseSHA := headSHAFor(t, repoDir)

	// Modify the constitution AFTER base_sha, then commit again — still a
	// valid constitution (Parse must succeed), just a different one.
	writeConstitution(t, repoDir, `
[[gate]]
name = "lint"
command = ["true"]
timeout = "5m"
required = false
`)
	commitAll(t, repoDir, "change constitution")

	insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, baseSHA)

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{})
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cert.Verdict != model.QualityVerdictFindings {
		t.Errorf("Verdict = %q, want findings (constitution changed in range, nothing else fails)", cert.Verdict)
	}

	checks, err := s.ListChecks(context.Background(), cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	found := false
	for _, c := range checks {
		if c.Name == "unchanged-in-range" {
			found = true
			if c.Status != "finding" {
				t.Errorf("unchanged-in-range status = %q, want finding", c.Status)
			}
			if c.Detail == "" {
				t.Error("unchanged-in-range detail is empty, want before/after hashes")
			}
		}
	}
	if !found {
		t.Fatal("no unchanged-in-range check found")
	}
}

// headSHAFor returns the current HEAD SHA of repoDir via git rev-parse.
func headSHAFor(t *testing.T, repoDir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestQualityService_Status_Absent covers AC24: no constitution -> Note says
// so, no error.
func TestQualityService_Status_Absent(t *testing.T) {
	s := newTestQualityStore(t)
	svc := NewQualityService(s, "proj", t.TempDir(), &fakeGateRunner{})

	resp, err := svc.Status(context.Background(), model.QualityStatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp.Exists {
		t.Error("Exists = true, want false")
	}
	if resp.Enabled {
		t.Error("Enabled = true, want false")
	}
	if resp.Note == "" {
		t.Error("Note is empty, want a human-readable message")
	}
}

// TestQualityService_Status_WithCertificate covers the populated path: an
// enabled constitution plus a spec's latest certificate and checks.
func TestQualityService_Status_WithCertificate(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitution(t, repoDir)
	commitAll(t, repoDir, "add constitution")

	s := newTestQualityStore(t)
	insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{})
	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	resp, err := svc.Status(context.Background(), model.QualityStatusRequest{ID: "SPEC-1"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !resp.Exists || !resp.Enabled {
		t.Fatalf("resp = %+v, want exists+enabled", resp)
	}
	if len(resp.GateNames) != 2 {
		t.Errorf("GateNames = %v, want 2 entries", resp.GateNames)
	}
	if resp.LatestCertificate == nil || resp.LatestCertificate.ID != cert.ID {
		t.Fatalf("LatestCertificate = %+v, want id=%s", resp.LatestCertificate, cert.ID)
	}
	if len(resp.Checks) != 6 {
		t.Errorf("len(Checks) = %d, want 6", len(resp.Checks))
	}
}

// TestQualityService_Ack_RequiresByAndJustification covers Ack's own
// precondition.
func TestQualityService_Ack_RequiresByAndJustification(t *testing.T) {
	s := newTestQualityStore(t)
	svc := NewQualityService(s, "proj", t.TempDir(), &fakeGateRunner{})

	tests := []model.QualityAckRequest{
		{CertificateID: "x", Seq: 1, By: "", Justification: "ok"},
		{CertificateID: "x", Seq: 1, By: "orch", Justification: ""},
	}
	for _, req := range tests {
		if err := svc.Ack(context.Background(), req); !errors.Is(err, model.ErrReasonRequired) {
			t.Errorf("Ack(%+v) error = %v, want ErrReasonRequired", req, err)
		}
	}
}
