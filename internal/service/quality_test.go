package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
//
// writeFiles (SPEC-116 P7) lets a test simulate what a REAL coverage
// command does: write a profile file as a side effect of "running". Keyed
// by gate name, then by repo-relative path -> content. Without this, no
// test could honestly exercise the coverage/profile row's success path —
// the Runner interface is the ONLY thing that touches the filesystem as a
// side effect of "running a command" (R3: never a real command from
// inside go test).
type fakeGateRunner struct {
	results    map[string]quality.GateResult
	calls      []string
	writeFiles map[string]map[string]string
}

func (f *fakeGateRunner) Run(_ context.Context, gate quality.Gate, dir string) quality.GateResult {
	f.calls = append(f.calls, gate.Name)
	if files, ok := f.writeFiles[gate.Name]; ok {
		for relPath, content := range files {
			full := filepath.Join(dir, relPath)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				panic(err) // test fixture setup failure — fail loudly, this is not production code
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				panic(err)
			}
		}
	}
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

// writeConstitutionV2Coverage writes a schema_version=2 constitution with
// one always-passing gate and a fully-specified [coverage]/[ratchet] pair —
// [ratchet] always disabled here; SPEC-116's P8 tests configure it
// separately.
func writeConstitutionV2Coverage(t *testing.T, repoDir string, coverageEnabled bool, minDiffPct float64, minChangedLines int, exclude []string, profilePath, format string) {
	t.Helper()
	dir := filepath.Join(repoDir, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	quotedExclude := make([]string, len(exclude))
	for i, e := range exclude {
		quotedExclude[i] = fmt.Sprintf("%q", e)
	}
	doc := fmt.Sprintf(`
schema_version = 2
enabled = true
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
command = ["true"]
timeout = "5m"
required = true
[coverage]
enabled = %v
format = %q
command = ["true"]
profile_path = %q
timeout = "5m"
min_diff_line_pct = %v
min_changed_lines = %d
exclude = [%s]
[ratchet]
enabled = false
max_global_line_pct_drop = 0.0
max_baseline_staleness_pct = 1.0
`, coverageEnabled, format, profilePath, minDiffPct, minChangedLines, strings.Join(quotedExclude, ", "))
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
	// 1 tree + 3 constitution + 2 gates + 3 coverage + 4 ratchet
	// (schema_version=1, SPEC-116: all 7 skipped — [coverage]/[ratchet]
	// are not even declared) + 3 criteria (SPEC-117: skipped — this
	// service was never given WithWorkflowDir) = 16.
	if len(checks) != 16 {
		t.Fatalf("len(checks) = %d, want 16: %+v", len(checks), checks)
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
	if len(resp.Checks) != 16 {
		t.Errorf("len(Checks) = %d, want 16 (SPEC-116 adds 3 skipped coverage + 4 skipped ratchet rows; SPEC-117 adds 3 skipped criteria rows)", len(resp.Checks))
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

// coverageCheckStatus returns the status of the coverage row named name.
func coverageCheckStatus(checks []*model.QualityCheck, name string) string {
	for _, c := range checks {
		if c.Kind == "coverage" && c.Name == name {
			return c.Status
		}
	}
	return ""
}

// TestQualityService_Verify_Coverage_ProfileChecks covers AC14: five ways
// coverage/profile fails, and the one way it passes, all on the same
// fixture (one changed file, foo.go, with a valid LCOV profile covering
// it).
func TestQualityService_Verify_Coverage_ProfileChecks(t *testing.T) {
	validLCOV := "SF:foo.go\nDA:1,1\nDA:2,1\nDA:3,1\nend_of_record\n"
	malformedLCOV := "SF:foo.go\nDA:1,not-a-number\nend_of_record\n"
	emptyLCOV := "TN:\n"

	tests := []struct {
		name         string
		runnerRes    quality.GateResult
		writeContent string // empty means "the fake writes nothing"
		trackProfile bool   // pre-commit the profile file, tracked by git
		wantStatus   string
	}{
		{"command exits non-zero", quality.GateResult{Status: quality.GateStatusFail, ExitCode: 1}, "", false, "fail"},
		{"command exits 0 but writes no file", quality.GateResult{Status: quality.GateStatusPass}, "", false, "fail"},
		{"file exists but is not parseable in the declared format", quality.GateResult{Status: quality.GateStatusPass}, malformedLCOV, false, "fail"},
		{"file parses with zero files", quality.GateResult{Status: quality.GateStatusPass}, emptyLCOV, false, "fail"},
		{"profile_path is tracked by git", quality.GateResult{Status: quality.GateStatusPass}, validLCOV, true, "fail"},
		{"command exits 0 with a valid, untracked profile", quality.GateResult{Status: quality.GateStatusPass}, validLCOV, false, "pass"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := newTestGitRepo(t)
			baseSHA := headSHAFor(t, repoDir)

			writeConstitutionV2Coverage(t, repoDir, true, 1.0, 1, nil, "tmp/coverage.out", "lcov")
			if err := os.WriteFile(filepath.Join(repoDir, "foo.go"), []byte("package main\n\nfunc Foo() {}\n"), 0o644); err != nil {
				t.Fatalf("write foo.go: %v", err)
			}
			if tt.trackProfile {
				if err := os.MkdirAll(filepath.Join(repoDir, "tmp"), 0o755); err != nil {
					t.Fatalf("mkdir tmp: %v", err)
				}
				if err := os.WriteFile(filepath.Join(repoDir, "tmp", "coverage.out"), []byte(tt.writeContent), 0o644); err != nil {
					t.Fatalf("write tracked profile: %v", err)
				}
			}
			commitAll(t, repoDir, "add constitution and foo.go")

			s := newTestQualityStore(t)
			insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, baseSHA)

			runner := &fakeGateRunner{results: map[string]quality.GateResult{"coverage-profile": tt.runnerRes}}
			if !tt.trackProfile && tt.writeContent != "" {
				runner.writeFiles = map[string]map[string]string{"coverage-profile": {"tmp/coverage.out": tt.writeContent}}
			}
			svc := NewQualityService(s, "proj", repoDir, runner)

			cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			checks, err := s.ListChecks(context.Background(), cert.ID)
			if err != nil {
				t.Fatalf("ListChecks: %v", err)
			}
			if got := coverageCheckStatus(checks, "profile"); got != tt.wantStatus {
				t.Errorf("coverage/profile status = %q, want %q", got, tt.wantStatus)
			}

			if tt.trackProfile {
				// D12/AC14: mneme must NEVER delete a tracked file — it
				// must still exist on disk after Verify.
				if _, statErr := os.Stat(filepath.Join(repoDir, "tmp", "coverage.out")); statErr != nil {
					t.Errorf("tracked profile file disappeared after Verify: %v", statErr)
				}
			}
		})
	}
}

// TestQualityService_Verify_Coverage_StaleProfileMutation covers AC15: a
// STALE profile (declaring 100% for a file the diff touches) seeded on
// disk BEFORE Verify runs must NOT produce a false pass when the Runner
// injected for this test writes NOTHING — proving D12's delete-before-run
// actually executes. The paired row: the SAME stale seed, but the Runner
// DOES write a fresh (accurate) profile — must pass.
func TestQualityService_Verify_Coverage_StaleProfileMutation(t *testing.T) {
	staleLyingLCOV := "SF:foo.go\nDA:1,1\nDA:2,1\nDA:3,1\nend_of_record\n" // claims 100%, but is about to be stale
	freshHonestLCOV := "SF:foo.go\nDA:1,0\nDA:2,0\nDA:3,0\nend_of_record\n"

	setup := func(t *testing.T) (repoDir, baseSHA string, s *store.SDDStore) {
		t.Helper()
		repoDir = newTestGitRepo(t)
		baseSHA = headSHAFor(t, repoDir)
		writeConstitutionV2Coverage(t, repoDir, true, 1.0, 1, nil, "tmp/coverage.out", "lcov")
		if err := os.WriteFile(filepath.Join(repoDir, "foo.go"), []byte("package main\n\nfunc Foo() {}\n"), 0o644); err != nil {
			t.Fatalf("write foo.go: %v", err)
		}
		commitAll(t, repoDir, "add constitution and foo.go")

		// Seed a STALE profile on disk BEFORE Verify — this must be
		// deleted by D12 before the fake "runs".
		if err := os.MkdirAll(filepath.Join(repoDir, "tmp"), 0o755); err != nil {
			t.Fatalf("mkdir tmp: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repoDir, "tmp", "coverage.out"), []byte(staleLyingLCOV), 0o644); err != nil {
			t.Fatalf("seed stale profile: %v", err)
		}

		s = newTestQualityStore(t)
		insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, baseSHA)
		return repoDir, baseSHA, s
	}

	t.Run("runner writes nothing: stale profile must not produce a false pass", func(t *testing.T) {
		repoDir, _, s := setup(t)
		runner := &fakeGateRunner{} // writes nothing at all
		svc := NewQualityService(s, "proj", repoDir, runner)

		cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		checks, err := s.ListChecks(context.Background(), cert.ID)
		if err != nil {
			t.Fatalf("ListChecks: %v", err)
		}
		if got := coverageCheckStatus(checks, "profile"); got != "fail" {
			t.Errorf("coverage/profile status = %q, want fail (D12 must have deleted the stale profile)", got)
		}
		if got := coverageCheckStatus(checks, "diff-lines"); got == "pass" {
			t.Error("coverage/diff-lines = pass, want anything but pass (the stale profile must not produce a false green)")
		}
	})

	t.Run("runner writes a fresh profile: passes", func(t *testing.T) {
		repoDir, _, s := setup(t)
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
			"coverage-profile": {"tmp/coverage.out": freshHonestLCOV},
		}}
		svc := NewQualityService(s, "proj", repoDir, runner)

		cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		checks, err := s.ListChecks(context.Background(), cert.ID)
		if err != nil {
			t.Fatalf("ListChecks: %v", err)
		}
		if got := coverageCheckStatus(checks, "profile"); got != "pass" {
			t.Errorf("coverage/profile status = %q, want pass (fresh profile written by the runner)", got)
		}
	})
}

// TestQualityService_Verify_Coverage_ChangedFilesInProfile covers AC16: the
// mapping-is-broken trap wakes only when it should.
func TestQualityService_Verify_Coverage_ChangedFilesInProfile(t *testing.T) {
	tests := []struct {
		name           string
		profileContent string
		wantStatus     string
	}{
		{
			name:           "changed file's path matches the profile: pass",
			profileContent: "SF:foo.go\nDA:1,1\nDA:2,1\nDA:3,1\nend_of_record\n",
			wantStatus:     "pass",
		},
		{
			name:           "changed file's path never appears in the profile: finding",
			profileContent: "SF:completely/unrelated/other.go\nDA:1,1\nend_of_record\n",
			wantStatus:     "finding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := newTestGitRepo(t)
			baseSHA := headSHAFor(t, repoDir)
			writeConstitutionV2Coverage(t, repoDir, true, 1.0, 1, nil, "tmp/coverage.out", "lcov")
			if err := os.WriteFile(filepath.Join(repoDir, "foo.go"), []byte("package main\n\nfunc Foo() {}\n"), 0o644); err != nil {
				t.Fatalf("write foo.go: %v", err)
			}
			commitAll(t, repoDir, "add constitution and foo.go")

			s := newTestQualityStore(t)
			insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, baseSHA)

			runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
				"coverage-profile": {"tmp/coverage.out": tt.profileContent},
			}}
			svc := NewQualityService(s, "proj", repoDir, runner)

			cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			checks, err := s.ListChecks(context.Background(), cert.ID)
			if err != nil {
				t.Fatalf("ListChecks: %v", err)
			}
			if got := coverageCheckStatus(checks, "changed-files-in-profile"); got != tt.wantStatus {
				t.Errorf("coverage/changed-files-in-profile status = %q, want %q", got, tt.wantStatus)
			}
		})
	}
}

// TestQualityService_Verify_Coverage_DiffLinesThreshold covers AC17: the
// threshold itself, and its min_changed_lines floor.
func TestQualityService_Verify_Coverage_DiffLinesThreshold(t *testing.T) {
	// foo.go has 3 changed lines; the profile always instruments exactly
	// those 3.
	tests := []struct {
		name            string
		minDiffPct      float64
		minChangedLines int
		profileContent  string
		wantStatus      string
	}{
		{
			name:            "below threshold: fail",
			minDiffPct:      80.0,
			minChangedLines: 1,
			profileContent:  "SF:foo.go\nDA:1,1\nDA:2,0\nDA:3,0\nend_of_record\n", // 33%
			wantStatus:      "fail",
		},
		{
			name:            "above threshold: pass",
			minDiffPct:      80.0,
			minChangedLines: 1,
			profileContent:  "SF:foo.go\nDA:1,1\nDA:2,1\nDA:3,1\nend_of_record\n", // 100%
			wantStatus:      "pass",
		},
		{
			name:            "below min_changed_lines floor: skipped",
			minDiffPct:      80.0,
			minChangedLines: 10,
			profileContent:  "SF:foo.go\nDA:1,0\nDA:2,0\nDA:3,0\nend_of_record\n", // 0%, but only 3 eligible lines < floor of 10
			wantStatus:      "skipped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := newTestGitRepo(t)
			baseSHA := headSHAFor(t, repoDir)
			writeConstitutionV2Coverage(t, repoDir, true, tt.minDiffPct, tt.minChangedLines, nil, "tmp/coverage.out", "lcov")
			if err := os.WriteFile(filepath.Join(repoDir, "foo.go"), []byte("package main\n\nfunc Foo() {}\n"), 0o644); err != nil {
				t.Fatalf("write foo.go: %v", err)
			}
			commitAll(t, repoDir, "add constitution and foo.go")

			s := newTestQualityStore(t)
			insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, baseSHA)

			runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
				"coverage-profile": {"tmp/coverage.out": tt.profileContent},
			}}
			svc := NewQualityService(s, "proj", repoDir, runner)

			cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			checks, err := s.ListChecks(context.Background(), cert.ID)
			if err != nil {
				t.Fatalf("ListChecks: %v", err)
			}
			if got := coverageCheckStatus(checks, "diff-lines"); got != tt.wantStatus {
				t.Errorf("coverage/diff-lines status = %q, want %q", got, tt.wantStatus)
			}
		})
	}
}

// TestQualityService_Verify_Coverage_Cascade covers AC25: the gate cascade
// reaches the coverage command, proven by COUNTING invocations — with a
// required gate failing, the coverage command must be invoked ZERO times;
// with every gate passing, exactly ONCE.
func TestQualityService_Verify_Coverage_Cascade(t *testing.T) {
	newFixture := func(t *testing.T) (repoDir string, s *store.SDDStore) {
		t.Helper()
		repoDir = newTestGitRepo(t)
		writeConstitutionV2Coverage(t, repoDir, true, 1.0, 1, nil, "tmp/coverage.out", "lcov")
		commitAll(t, repoDir, "add constitution")
		s = newTestQualityStore(t)
		insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")
		return repoDir, s
	}

	t.Run("a required gate failure stops before the coverage command", func(t *testing.T) {
		repoDir, s := newFixture(t)
		runner := &fakeGateRunner{results: map[string]quality.GateResult{
			"build": {Status: quality.GateStatusFail, ExitCode: 1},
		}}
		svc := NewQualityService(s, "proj", repoDir, runner)
		if _, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"}); err != nil {
			t.Fatalf("Verify: %v", err)
		}
		count := 0
		for _, c := range runner.calls {
			if c == "coverage-profile" {
				count++
			}
		}
		if count != 0 {
			t.Errorf("coverage-profile invoked %d times, want 0 (required gate failed)", count)
		}
	})

	t.Run("all gates pass: the coverage command runs exactly once", func(t *testing.T) {
		repoDir, s := newFixture(t)
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
			"coverage-profile": {"tmp/coverage.out": "TN:\n"},
		}}
		svc := NewQualityService(s, "proj", repoDir, runner)
		if _, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"}); err != nil {
			t.Fatalf("Verify: %v", err)
		}
		count := 0
		for _, c := range runner.calls {
			if c == "coverage-profile" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("coverage-profile invoked %d times, want 1", count)
		}
	})
}

// TestQualityService_Verify_Coverage_OffStates covers AC26's first two
// rows: "apagado por omision" (schema_version=1) and "apagado por
// decision" ([coverage].enabled=false) both skip all three rows, with
// DIFFERENT summaries.
func TestQualityService_Verify_Coverage_OffStates(t *testing.T) {
	t.Run("schema_version=1: skipped, summary names the schema", func(t *testing.T) {
		repoDir := newTestGitRepo(t)
		writeConstitution(t, repoDir) // schema_version=1, no [coverage] at all
		commitAll(t, repoDir, "add constitution")

		s := newTestQualityStore(t)
		insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")
		svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{})

		cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		checks, err := s.ListChecks(context.Background(), cert.ID)
		if err != nil {
			t.Fatalf("ListChecks: %v", err)
		}
		if got := coverageCheckStatus(checks, "profile"); got != "skipped" {
			t.Fatalf("coverage/profile status = %q, want skipped", got)
		}
		summary := ""
		for _, c := range checks {
			if c.Kind == "coverage" && c.Name == "profile" {
				summary = c.Summary
			}
		}
		if !strings.Contains(summary, "schema_version") {
			t.Errorf("summary %q does not name schema_version", summary)
		}
	})

	t.Run("coverage.enabled=false: skipped, DIFFERENT summary", func(t *testing.T) {
		repoDir := newTestGitRepo(t)
		writeConstitutionV2Coverage(t, repoDir, false, 80.0, 5, nil, "tmp/coverage.out", "lcov")
		commitAll(t, repoDir, "add constitution")

		s := newTestQualityStore(t)
		insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")
		svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{})

		cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		checks, err := s.ListChecks(context.Background(), cert.ID)
		if err != nil {
			t.Fatalf("ListChecks: %v", err)
		}
		if got := coverageCheckStatus(checks, "profile"); got != "skipped" {
			t.Fatalf("coverage/profile status = %q, want skipped", got)
		}
		summary := ""
		for _, c := range checks {
			if c.Kind == "coverage" && c.Name == "profile" {
				summary = c.Summary
			}
		}
		if !strings.Contains(summary, "enabled") {
			t.Errorf("summary %q does not name enabled=false", summary)
		}
		if strings.Contains(summary, "schema_version") {
			t.Error("the two skip summaries (schema-1 vs enabled=false) must differ — this one must NOT mention schema_version")
		}
	})
}

// writeConstitutionV2Full writes a schema_version=2 constitution with both
// [coverage] and [ratchet] fully configured — SPEC-116's P8 tests need
// [ratchet] independently tunable from [coverage], unlike
// writeConstitutionV2Coverage (P7), which always disables it.
func writeConstitutionV2Full(t *testing.T, repoDir string, ratchetEnabled bool, maxDrop, maxStaleness float64, exclude []string) {
	t.Helper()
	dir := filepath.Join(repoDir, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	quotedExclude := make([]string, len(exclude))
	for i, e := range exclude {
		quotedExclude[i] = fmt.Sprintf("%q", e)
	}
	doc := fmt.Sprintf(`
schema_version = 2
enabled = true
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
command = ["true"]
timeout = "5m"
required = true
[coverage]
enabled = true
format = "lcov"
command = ["true"]
profile_path = "tmp/coverage.out"
timeout = "5m"
min_diff_line_pct = 1.0
min_changed_lines = 1
exclude = [%s]
[ratchet]
enabled = %v
max_global_line_pct_drop = %v
max_baseline_staleness_pct = %v
`, strings.Join(quotedExclude, ", "), ratchetEnabled, maxDrop, maxStaleness)
	if err := os.WriteFile(filepath.Join(dir, "quality.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write quality.toml: %v", err)
	}
}

// newTestBaseline builds a *quality.Baseline with sensible defaults, letting
// a test override just the fields it cares about.
func newTestBaseline(measuredAtSHA string, globalPct float64, scopeHash string) *quality.Baseline {
	return &quality.Baseline{
		SchemaVersion: quality.BaselineSchemaVersion,
		MeasuredAtSHA: measuredAtSHA,
		MeasuredAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CertificateID: "test-cert",
		LinesTotal:    100,
		LinesCovered:  int(globalPct),
		GlobalLinePct: globalPct,
		ScopeHash:     scopeHash,
	}
}

// writeBaseline writes b to repoDir/.mneme/quality-baseline.toml.
func writeBaseline(t *testing.T, repoDir string, b *quality.Baseline) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repoDir, quality.BaselineRelPath), []byte(quality.RenderBaseline(b)), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
}

// ratchetCheckStatus returns the status of the ratchet row named name.
func ratchetCheckStatus(checks []*model.QualityCheck, name string) string {
	for _, c := range checks {
		if c.Kind == "ratchet" && c.Name == name {
			return c.Status
		}
	}
	return ""
}

// TestQualityService_Verify_Ratchet_GlobalLinePct covers AC19: a drop
// beyond tolerance is a finding; within tolerance, or an improvement, is
// not; and with no baseline registered at all, the row is skipped naming
// the remedy command.
func TestQualityService_Verify_Ratchet_GlobalLinePct(t *testing.T) {
	tests := []struct {
		name            string
		baselinePct     float64
		maxDrop         float64
		measuredContent string // LCOV content for foo.go (3 lines)
		wantStatus      string
	}{
		{
			name: "drop beyond tolerance: finding", baselinePct: 90.0, maxDrop: 1.0,
			measuredContent: "SF:foo.go\nDA:1,0\nDA:2,0\nDA:3,1\nend_of_record\n", // 33%
			wantStatus:      "finding",
		},
		{
			name: "drop within tolerance: pass", baselinePct: 34.0, maxDrop: 5.0,
			measuredContent: "SF:foo.go\nDA:1,0\nDA:2,0\nDA:3,1\nend_of_record\n", // 33%
			wantStatus:      "pass",
		},
		{
			name: "improvement: pass", baselinePct: 10.0, maxDrop: 0.0,
			measuredContent: "SF:foo.go\nDA:1,1\nDA:2,1\nDA:3,1\nend_of_record\n", // 100%
			wantStatus:      "pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := newTestGitRepo(t)
			baseSHA := headSHAFor(t, repoDir)
			// A huge staleness margin isolates row 6 from row 7 in this table.
			writeConstitutionV2Full(t, repoDir, true, tt.maxDrop, 1000.0, nil)
			if err := os.WriteFile(filepath.Join(repoDir, "foo.go"), []byte("package main\n\nfunc Foo() {}\n"), 0o644); err != nil {
				t.Fatalf("write foo.go: %v", err)
			}
			writeBaseline(t, repoDir, newTestBaseline(baseSHA, tt.baselinePct, quality.ScopeHash("lcov", nil)))
			commitAll(t, repoDir, "add constitution, foo.go, baseline")

			s := newTestQualityStore(t)
			insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, baseSHA)
			runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
				"coverage-profile": {"tmp/coverage.out": tt.measuredContent},
			}}
			svc := NewQualityService(s, "proj", repoDir, runner)

			cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			checks, err := s.ListChecks(context.Background(), cert.ID)
			if err != nil {
				t.Fatalf("ListChecks: %v", err)
			}
			if got := ratchetCheckStatus(checks, "global-line-pct"); got != tt.wantStatus {
				t.Errorf("ratchet/global-line-pct status = %q, want %q", got, tt.wantStatus)
			}
		})
	}

	t.Run("no baseline registered: skipped, names the remedy", func(t *testing.T) {
		repoDir := newTestGitRepo(t)
		writeConstitutionV2Full(t, repoDir, true, 0.0, 1.0, nil)
		commitAll(t, repoDir, "add constitution")

		s := newTestQualityStore(t)
		insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
			"coverage-profile": {"tmp/coverage.out": "SF:x.go\nDA:1,1\nend_of_record\n"},
		}}
		svc := NewQualityService(s, "proj", repoDir, runner)

		cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		checks, err := s.ListChecks(context.Background(), cert.ID)
		if err != nil {
			t.Fatalf("ListChecks: %v", err)
		}
		if got := ratchetCheckStatus(checks, "global-line-pct"); got != "skipped" {
			t.Fatalf("ratchet/global-line-pct status = %q, want skipped", got)
		}
		var summary string
		for _, c := range checks {
			if c.Kind == "ratchet" && c.Name == "global-line-pct" {
				summary = c.Summary
			}
		}
		if !strings.Contains(summary, "baseline update") {
			t.Errorf("summary %q does not name `mneme quality baseline update`", summary)
		}
	})
}

// baselineIntegrityFixture builds the git history AC20's table needs:
// commit c1 (constitution + foo.go, with `before`'s baseline content if
// non-nil) becomes spec.BaseSHA; if `after` differs from `before` (or
// `before` is nil and `after` is not, or vice versa), a second commit c2
// applies that change and becomes HEAD.
func baselineIntegrityFixture(t *testing.T, before, after *quality.Baseline) (repoDir, baseSHA string) {
	t.Helper()
	repoDir = newTestGitRepo(t)
	writeConstitutionV2Full(t, repoDir, true, 0.0, 1000.0, nil) // staleness margin huge: isolate row 4 from row 7
	if err := os.WriteFile(filepath.Join(repoDir, "foo.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	beforeContent := ""
	if before != nil {
		beforeContent = quality.RenderBaseline(before)
		writeBaseline(t, repoDir, before)
	}
	commitAll(t, repoDir, "base commit")
	baseSHA = headSHAFor(t, repoDir)

	afterContent := ""
	if after != nil {
		afterContent = quality.RenderBaseline(after)
	}

	switch {
	case beforeContent == afterContent:
		// Nothing to change — HEAD (== baseSHA, no second commit) already
		// reflects "after" because it never differed from "before".
	case after == nil:
		if err := os.Remove(filepath.Join(repoDir, quality.BaselineRelPath)); err != nil {
			t.Fatalf("remove baseline: %v", err)
		}
		commitAll(t, repoDir, "delete baseline")
	default:
		writeBaseline(t, repoDir, after)
		commitAll(t, repoDir, "update baseline")
	}
	return repoDir, baseSHA
}

// TestQualityService_Verify_Ratchet_BaselineIntegrity covers AC20's five-
// row table end-to-end, over a REAL git repo (unlike TestBaselineDirection
// in internal/quality, which tests the pure function directly) — this is
// where P8's I/O wiring (MergeBase + FileAtRef) is proven correct.
func TestQualityService_Verify_Ratchet_BaselineIntegrity(t *testing.T) {
	scopeHash := quality.ScopeHash("lcov", nil)

	tests := []struct {
		name       string
		before     *quality.Baseline
		after      *quality.Baseline
		wantStatus string
	}{
		{"absent -> absent: pass", nil, nil, "pass"},
		{"absent -> present: pass (honest bootstrap)", nil, newTestBaseline("x", 70.0, scopeHash), "pass"},
		{"present -> absent: FINDING (the obvious leak)", newTestBaseline("x", 70.0, scopeHash), nil, "finding"},
		{"present -> present, pct higher: pass", newTestBaseline("x", 60.0, scopeHash), newTestBaseline("x", 75.0, scopeHash), "pass"},
		{"present -> present, pct lower: FINDING", newTestBaseline("x", 75.0, scopeHash), newTestBaseline("x", 60.0, scopeHash), "finding"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir, baseSHA := baselineIntegrityFixture(t, tt.before, tt.after)

			s := newTestQualityStore(t)
			insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, baseSHA)
			runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
				"coverage-profile": {"tmp/coverage.out": "SF:x.go\nDA:1,1\nend_of_record\n"},
			}}
			svc := NewQualityService(s, "proj", repoDir, runner)

			cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			checks, err := s.ListChecks(context.Background(), cert.ID)
			if err != nil {
				t.Fatalf("ListChecks: %v", err)
			}
			if got := ratchetCheckStatus(checks, "baseline-integrity"); got != tt.wantStatus {
				t.Errorf("ratchet/baseline-integrity status = %q, want %q", got, tt.wantStatus)
			}
		})
	}
}

// TestQualityService_Verify_Ratchet_BaselineComparable covers AC21's two
// independent causes, plus their joint pass.
func TestQualityService_Verify_Ratchet_BaselineComparable(t *testing.T) {
	t.Run("measured_at_sha not an ancestor of HEAD: finding", func(t *testing.T) {
		// A sibling branch's tip is registered as the baseline's SHA —
		// never an ancestor of the eventual HEAD.
		repoDir := newTestGitRepo(t)
		writeConstitutionV2Full(t, repoDir, true, 0.0, 1000.0, nil)
		commitAll(t, repoDir, "add constitution") // c0

		env := append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
		gitRun := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = repoDir
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		c0 := headSHAFor(t, repoDir)
		gitRun("checkout", "-b", "sibling", c0)
		if err := os.WriteFile(filepath.Join(repoDir, "sibling.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write sibling.go: %v", err)
		}
		commitAll(t, repoDir, "sibling commit")
		siblingSHA := headSHAFor(t, repoDir)

		gitRun("checkout", "main")
		if err := os.WriteFile(filepath.Join(repoDir, "foo.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write foo.go: %v", err)
		}
		writeBaseline(t, repoDir, newTestBaseline(siblingSHA, 70.0, quality.ScopeHash("lcov", nil)))
		commitAll(t, repoDir, "add foo.go and a baseline pointing at the sibling")

		s := newTestQualityStore(t)
		insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{"coverage-profile": {"tmp/coverage.out": "SF:x.go\nDA:1,1\nend_of_record\n"}}}
		svc := NewQualityService(s, "proj", repoDir, runner)

		cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		checks, err := s.ListChecks(context.Background(), cert.ID)
		if err != nil {
			t.Fatalf("ListChecks: %v", err)
		}
		if got := ratchetCheckStatus(checks, "baseline-comparable"); got != "finding" {
			t.Errorf("ratchet/baseline-comparable status = %q, want finding", got)
		}
	})

	t.Run("scope_hash mismatch (exclude widened): finding", func(t *testing.T) {
		repoDir := newTestGitRepo(t)
		baseSHA := headSHAFor(t, repoDir)
		writeConstitutionV2Full(t, repoDir, true, 0.0, 1000.0, []string{"**/*_gen.go"})
		// The registered baseline's scope_hash reflects an EMPTY exclude
		// list — stale relative to the constitution's current scope.
		writeBaseline(t, repoDir, newTestBaseline(baseSHA, 70.0, quality.ScopeHash("lcov", nil)))
		commitAll(t, repoDir, "add constitution with widened exclude and a stale-scope baseline")

		s := newTestQualityStore(t)
		insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{"coverage-profile": {"tmp/coverage.out": "SF:x.go\nDA:1,1\nend_of_record\n"}}}
		svc := NewQualityService(s, "proj", repoDir, runner)

		cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		checks, err := s.ListChecks(context.Background(), cert.ID)
		if err != nil {
			t.Fatalf("ListChecks: %v", err)
		}
		if got := ratchetCheckStatus(checks, "baseline-comparable"); got != "finding" {
			t.Errorf("ratchet/baseline-comparable status = %q, want finding", got)
		}
	})

	t.Run("ancestor and scope both satisfied: pass", func(t *testing.T) {
		repoDir := newTestGitRepo(t)
		baseSHA := headSHAFor(t, repoDir)
		writeConstitutionV2Full(t, repoDir, true, 0.0, 1000.0, nil)
		writeBaseline(t, repoDir, newTestBaseline(baseSHA, 70.0, quality.ScopeHash("lcov", nil)))
		commitAll(t, repoDir, "add constitution and a comparable baseline")

		s := newTestQualityStore(t)
		insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{"coverage-profile": {"tmp/coverage.out": "SF:x.go\nDA:1,1\nend_of_record\n"}}}
		svc := NewQualityService(s, "proj", repoDir, runner)

		cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		checks, err := s.ListChecks(context.Background(), cert.ID)
		if err != nil {
			t.Fatalf("ListChecks: %v", err)
		}
		if got := ratchetCheckStatus(checks, "baseline-comparable"); got != "pass" {
			t.Errorf("ratchet/baseline-comparable status = %q, want pass", got)
		}
	})
}

// TestQualityService_Verify_Ratchet_BaselineStale covers AC22: exceeding
// the margin is a finding (with the gap named); within the margin, or
// below the mark entirely, passes; and no baseline skips.
func TestQualityService_Verify_Ratchet_BaselineStale(t *testing.T) {
	tests := []struct {
		name         string
		baselinePct  float64
		maxStaleness float64
		measuredLCOV string
		wantStatus   string
	}{
		{
			name: "exceeds margin: finding", baselinePct: 70.0, maxStaleness: 1.0,
			measuredLCOV: "SF:foo.go\nDA:1,1\nDA:2,1\nDA:3,1\nend_of_record\n", wantStatus: "finding", // 100%
		},
		{
			name: "within margin: pass", baselinePct: 99.9, maxStaleness: 1.0,
			measuredLCOV: "SF:foo.go\nDA:1,1\nDA:2,1\nDA:3,1\nend_of_record\n", wantStatus: "pass", // 100%, 0.1pt over
		},
		{
			name: "below the mark: pass (that direction is row 6's problem)", baselinePct: 100.0, maxStaleness: 1.0,
			measuredLCOV: "SF:foo.go\nDA:1,0\nDA:2,0\nDA:3,0\nend_of_record\n", wantStatus: "pass", // 0%
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := newTestGitRepo(t)
			baseSHA := headSHAFor(t, repoDir)
			// max_global_line_pct_drop=0.0: every fixture here measures AT
			// or ABOVE the baseline (never a drop), so row 6 always passes
			// regardless — isolating row 7 without violating Parse's own
			// cross-bound rule (staleness must be >= drop, D6).
			writeConstitutionV2Full(t, repoDir, true, 0.0, tt.maxStaleness, nil)
			if err := os.WriteFile(filepath.Join(repoDir, "foo.go"), []byte("package main\n\nfunc Foo() {}\n"), 0o644); err != nil {
				t.Fatalf("write foo.go: %v", err)
			}
			writeBaseline(t, repoDir, newTestBaseline(baseSHA, tt.baselinePct, quality.ScopeHash("lcov", nil)))
			commitAll(t, repoDir, "add constitution, foo.go, baseline")

			s := newTestQualityStore(t)
			insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, baseSHA)
			runner := &fakeGateRunner{writeFiles: map[string]map[string]string{"coverage-profile": {"tmp/coverage.out": tt.measuredLCOV}}}
			svc := NewQualityService(s, "proj", repoDir, runner)

			cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			checks, err := s.ListChecks(context.Background(), cert.ID)
			if err != nil {
				t.Fatalf("ListChecks: %v", err)
			}
			if got := ratchetCheckStatus(checks, "baseline-stale"); got != tt.wantStatus {
				t.Errorf("ratchet/baseline-stale status = %q, want %q", got, tt.wantStatus)
			}
		})
	}

	t.Run("no baseline: skipped", func(t *testing.T) {
		repoDir := newTestGitRepo(t)
		writeConstitutionV2Full(t, repoDir, true, 0.0, 1.0, nil)
		commitAll(t, repoDir, "add constitution")

		s := newTestQualityStore(t)
		insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{"coverage-profile": {"tmp/coverage.out": "SF:x.go\nDA:1,1\nend_of_record\n"}}}
		svc := NewQualityService(s, "proj", repoDir, runner)

		cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		checks, err := s.ListChecks(context.Background(), cert.ID)
		if err != nil {
			t.Fatalf("ListChecks: %v", err)
		}
		if got := ratchetCheckStatus(checks, "baseline-stale"); got != "skipped" {
			t.Errorf("ratchet/baseline-stale status = %q, want skipped", got)
		}
	})
}

// TestQualityService_Verify_Ratchet_OffStates covers the rest of AC26:
// [ratchet].enabled=false skips rows 4-7 while rows 1-3 still evaluate
// normally — the ratchet being off does not turn off diff coverage.
func TestQualityService_Verify_Ratchet_OffStates(t *testing.T) {
	repoDir := newTestGitRepo(t)
	baseSHA := headSHAFor(t, repoDir)
	writeConstitutionV2Full(t, repoDir, false, 0.0, 1.0, nil)
	if err := os.WriteFile(filepath.Join(repoDir, "foo.go"), []byte("package main\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	commitAll(t, repoDir, "add constitution and foo.go")

	s := newTestQualityStore(t)
	insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, baseSHA)
	runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
		"coverage-profile": {"tmp/coverage.out": "SF:foo.go\nDA:1,1\nDA:2,1\nDA:3,1\nend_of_record\n"},
	}}
	svc := NewQualityService(s, "proj", repoDir, runner)

	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: "SPEC-1"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	checks, err := s.ListChecks(context.Background(), cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}

	if got := coverageCheckStatus(checks, "diff-lines"); got != "pass" {
		t.Errorf("coverage/diff-lines status = %q, want pass (ratchet being off must not affect coverage rows)", got)
	}
	for _, name := range []string{"baseline-integrity", "baseline-comparable", "global-line-pct", "baseline-stale"} {
		if got := ratchetCheckStatus(checks, name); got != "skipped" {
			t.Errorf("ratchet/%s status = %q, want skipped", name, got)
		}
	}
}

// TestQualityService_BaselineUpdate_NoCertificate_Refuses covers AC23's
// third row: no certificate at all for the spec — BaselineUpdate refuses,
// and nothing is written to disk.
func TestQualityService_BaselineUpdate_NoCertificate_Refuses(t *testing.T) {
	repoDir := newTestGitRepo(t)
	s := newTestQualityStore(t)
	insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")
	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{})

	if _, err := svc.BaselineUpdate(context.Background(), model.QualityBaselineUpdateRequest{ID: "SPEC-1"}); err == nil {
		t.Fatal("BaselineUpdate with no certificate: want error, got nil")
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, quality.BaselineRelPath)); !os.IsNotExist(statErr) {
		t.Errorf("baseline file exists after a refused BaselineUpdate: %v", statErr)
	}
}

// TestQualityService_BaselineUpdate_NonPassCertificate_Refuses covers
// AC23's second row: the latest certificate exists but is NOT pass —
// BaselineUpdate refuses, and the file is not modified/created. This is
// the guardian against the "blanqueo perfecto" U-E warns about: writing a
// mark from a red certificate.
func TestQualityService_BaselineUpdate_NonPassCertificate_Refuses(t *testing.T) {
	repoDir := newTestGitRepo(t)
	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")

	cert := &model.QualityCertificate{
		Project: "proj", SpecID: spec.ID, HeadSHA: headSHAFor(t, repoDir),
		ConstitutionHash: "h", SchemaVersion: 2, Verdict: model.QualityVerdictFindings,
	}
	detail := coverageProfileDetail{LinesTotal: 100, LinesCovered: 90, GlobalLinePct: 90.0, ScopeHash: "sh"}
	detailJSON, _ := json.Marshal(detail)
	checks := []*model.QualityCheck{{Kind: "coverage", Name: "profile", Status: "pass", Detail: string(detailJSON)}}
	if err := s.InsertCertificate(context.Background(), cert, checks); err != nil {
		t.Fatalf("InsertCertificate: %v", err)
	}

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{})
	if _, err := svc.BaselineUpdate(context.Background(), model.QualityBaselineUpdateRequest{ID: "SPEC-1"}); !errors.Is(err, model.ErrCertificateNotGreen) {
		t.Fatalf("BaselineUpdate with a non-pass certificate error = %v, want ErrCertificateNotGreen", err)
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, quality.BaselineRelPath)); !os.IsNotExist(statErr) {
		t.Errorf("baseline file exists after a refused BaselineUpdate: %v", statErr)
	}
}

// TestQualityService_BaselineUpdate_PassCertificate_WritesExactFigures
// covers AC23's first row: BaselineUpdate NEVER invents a number — the
// written lines_total/lines_covered are byte-for-byte the ones in the
// certificate's coverage/profile Detail.
func TestQualityService_BaselineUpdate_PassCertificate_WritesExactFigures(t *testing.T) {
	repoDir := newTestGitRepo(t)
	headSHA := headSHAFor(t, repoDir)
	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")

	cert := &model.QualityCertificate{
		Project: "proj", SpecID: spec.ID, HeadSHA: headSHA,
		ConstitutionHash: "h", SchemaVersion: 2, Verdict: model.QualityVerdictPass,
	}
	detail := coverageProfileDetail{LinesTotal: 41230, LinesCovered: 29066, GlobalLinePct: 70.50, ScopeHash: "b21f0000"}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	checks := []*model.QualityCheck{{Kind: "coverage", Name: "profile", Status: "pass", Detail: string(detailJSON)}}
	if err := s.InsertCertificate(context.Background(), cert, checks); err != nil {
		t.Fatalf("InsertCertificate: %v", err)
	}

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{})
	baseline, err := svc.BaselineUpdate(context.Background(), model.QualityBaselineUpdateRequest{ID: "SPEC-1"})
	if err != nil {
		t.Fatalf("BaselineUpdate: %v", err)
	}

	if baseline.LinesTotal != detail.LinesTotal || baseline.LinesCovered != detail.LinesCovered ||
		baseline.GlobalLinePct != detail.GlobalLinePct || baseline.ScopeHash != detail.ScopeHash {
		t.Fatalf("BaselineUpdate figures = %+v, want exactly %+v", baseline, detail)
	}
	if baseline.MeasuredAtSHA != headSHA {
		t.Errorf("MeasuredAtSHA = %q, want %q (the certificate's HeadSHA)", baseline.MeasuredAtSHA, headSHA)
	}
	if baseline.CertificateID != cert.ID {
		t.Errorf("CertificateID = %q, want %q", baseline.CertificateID, cert.ID)
	}

	raw, err := os.ReadFile(filepath.Join(repoDir, quality.BaselineRelPath))
	if err != nil {
		t.Fatalf("read written baseline: %v", err)
	}
	roundTripped, err := quality.ParseBaseline(raw)
	if err != nil {
		t.Fatalf("ParseBaseline(written): %v", err)
	}
	if roundTripped.LinesTotal != detail.LinesTotal || roundTripped.GlobalLinePct != detail.GlobalLinePct {
		t.Errorf("written baseline on disk = %+v, want figures matching %+v", roundTripped, detail)
	}
}

// TestQualityService_Status_ReportsBaseline covers AC29's positive half: a
// registered baseline appears in the response with its recorded figures,
// plus obsolescence computed from the spec's own latest certificate.
func TestQualityService_Status_ReportsBaseline(t *testing.T) {
	repoDir := newTestGitRepo(t)
	headSHA := headSHAFor(t, repoDir)
	writeConstitutionV2Full(t, repoDir, true, 0.0, 1.0, nil)
	commitAll(t, repoDir, "add constitution")

	writeBaseline(t, repoDir, newTestBaseline(headSHA, 70.0, quality.ScopeHash("lcov", nil)))

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")

	cert := &model.QualityCertificate{
		Project: "proj", SpecID: spec.ID, HeadSHA: headSHA,
		ConstitutionHash: "h", SchemaVersion: 2, Verdict: model.QualityVerdictPass,
	}
	detail := coverageProfileDetail{LinesTotal: 100, LinesCovered: 75, GlobalLinePct: 75.0, ScopeHash: quality.ScopeHash("lcov", nil)}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	checks := []*model.QualityCheck{{Kind: "coverage", Name: "profile", Status: "pass", Detail: string(detailJSON)}}
	if err := s.InsertCertificate(context.Background(), cert, checks); err != nil {
		t.Fatalf("InsertCertificate: %v", err)
	}

	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{})
	resp, err := svc.Status(context.Background(), model.QualityStatusRequest{ID: "SPEC-1"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if resp.Baseline == nil {
		t.Fatal("resp.Baseline is nil, want it populated")
	}
	if resp.Baseline.GlobalLinePct != 70.0 {
		t.Errorf("Baseline.GlobalLinePct = %v, want 70.0", resp.Baseline.GlobalLinePct)
	}
	if resp.Baseline.MeasuredAtSHA != headSHA {
		t.Errorf("Baseline.MeasuredAtSHA = %q, want %q", resp.Baseline.MeasuredAtSHA, headSHA)
	}
	if !resp.Baseline.StalenessKnown {
		t.Fatal("Baseline.StalenessKnown = false, want true (a usable certificate was supplied)")
	}
	if !resp.Baseline.Stale {
		t.Error("Baseline.Stale = false, want true (measured 75% vs baseline 70%, margin 1.0)")
	}
}

// TestQualityService_Status_NoBaseline_OmitsField is AC29's paired
// negative row: no baseline file at all -> resp.Baseline stays nil.
func TestQualityService_Status_NoBaseline_OmitsField(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitutionV2Full(t, repoDir, true, 0.0, 1.0, nil)
	commitAll(t, repoDir, "add constitution")

	s := newTestQualityStore(t)
	insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, "")
	svc := NewQualityService(s, "proj", repoDir, &fakeGateRunner{})

	resp, err := svc.Status(context.Background(), model.QualityStatusRequest{ID: "SPEC-1"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp.Baseline != nil {
		t.Errorf("resp.Baseline = %+v, want nil (no baseline file exists)", resp.Baseline)
	}
}
