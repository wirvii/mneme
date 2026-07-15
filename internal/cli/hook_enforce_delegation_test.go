package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/enforcement"
	"github.com/wirvii/mneme/internal/model"
)

// --- evaluateDelegation (config/project/manifest wiring, DB-backed) --------
//
// These tests mirror hook_pathowned_test.go's isolation pattern (chdirTemp +
// t.Setenv("HOME", ...) + a real, non-origin git repo so project detection
// is deterministic) but exercise the full evaluateDelegation wiring — config
// load, project detection, manifest query, and the resolvePathOwnership
// closure — rather than resolvePathOwnership in isolation.

// setupDelegationRepo creates an isolated $HOME + a fresh git repo (no
// origin remote, so project.NewDetector falls back to the lowercased repo
// basename), chdirs into it, and returns the detected slug plus the path
// where a manifest would be seeded for that slug.
func setupDelegationRepo(t *testing.T) (slug, dbPath string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	chdirTemp(t, repoDir)

	slug = strings.ToLower(filepath.Base(repoDir))
	dbPath = filepath.Join(home, ".mneme", "projects", slug+".db")
	return slug, dbPath
}

func seedManifest(t *testing.T, dbPath, project, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir projects dir: %v", err)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	if err := insertTestManifest(database, "m1", project, content); err != nil {
		t.Fatalf("insertTestManifest: %v", err)
	}
}

// TestEvaluateDelegation_FileTool_BlockedByImplementer covers AC4: an Edit to
// a path owned by "backend" in a real, seeded manifest blocks with that
// owner.
func TestEvaluateDelegation_FileTool_BlockedByImplementer(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	seedManifest(t, dbPath, slug, `[{"role":"backend","areas":["internal/**"]}]`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	input := hookPreToolInput{ToolName: "Edit"}
	input.ToolInput.FilePath = "internal/foo.go"

	got := evaluateDelegation(input, cwd)
	if !got.Block {
		t.Fatalf("Block = false, want true (decision: %+v)", got)
	}
	if got.Owner != "backend" {
		t.Errorf("Owner = %q, want backend", got.Owner)
	}
}

// TestEvaluateDelegation_Bash_BlockedByImplementer covers AC6 parity through
// the full wiring: a Bash redirect to a manifest-owned path blocks.
func TestEvaluateDelegation_Bash_BlockedByImplementer(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	seedManifest(t, dbPath, slug, `[{"role":"backend","areas":["internal/**"]}]`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	input := hookPreToolInput{ToolName: "Bash"}
	input.ToolInput.Command = "echo x > internal/foo.go"

	got := evaluateDelegation(input, cwd)
	if !got.Block {
		t.Fatalf("Block = false, want true (decision: %+v)", got)
	}
	if got.Owner != "backend" {
		t.Errorf("Owner = %q, want backend", got.Owner)
	}
}

// TestEvaluateDelegation_UnwhitelistedNoManifest_LegacyBlock covers AC3: no
// manifest at all (fresh project, nothing seeded) blocks with owner
// "legacy".
func TestEvaluateDelegation_UnwhitelistedNoManifest_LegacyBlock(t *testing.T) {
	_, _ = setupDelegationRepo(t) // no seedManifest call: manifest absent.

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	input := hookPreToolInput{ToolName: "Edit"}
	input.ToolInput.FilePath = "internal/foo.go"

	got := evaluateDelegation(input, cwd)
	if !got.Block {
		t.Fatalf("Block = false, want true (decision: %+v)", got)
	}
	if got.Owner != "legacy" {
		t.Errorf("Owner = %q, want legacy", got.Owner)
	}
}

// TestEvaluateDelegation_UnownedPath_Allows covers AC5: a manifest exists but
// does not own the target path.
func TestEvaluateDelegation_UnownedPath_Allows(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	seedManifest(t, dbPath, slug, `[{"role":"backend","areas":["internal/**"]}]`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	input := hookPreToolInput{ToolName: "Edit"}
	input.ToolInput.FilePath = "README.md"

	got := evaluateDelegation(input, cwd)
	if got.Block {
		t.Errorf("Block = true, want false (decision: %+v)", got)
	}
}

// TestEvaluateDelegation_WhitelistedPath_Allows covers AC2: a whitelisted
// path is allowed without ever consulting the manifest (no manifest seeded).
func TestEvaluateDelegation_WhitelistedPath_Allows(t *testing.T) {
	_, _ = setupDelegationRepo(t)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	input := hookPreToolInput{ToolName: "Edit"}
	input.ToolInput.FilePath = ".claude/settings.json"

	got := evaluateDelegation(input, cwd)
	if got.Block {
		t.Errorf("Block = true, want false for a whitelisted path (decision: %+v)", got)
	}
}

// --- runHookEnforceDelegation (ALLOW branches only — os.Exit(2) would kill
// the test binary on a block, same constraint documented in
// hook_pathowned_test.go for runHookPathOwned) --------------------------

// TestRunHookEnforceDelegation_Subagent_AllowsWithoutExit covers AC1: a
// payload with a non-empty agent_id short-circuits to allow before the
// tool/path is ever evaluated, even though internal/foo.go would otherwise be
// blocked (no manifest, legacy deny-by-default).
func TestRunHookEnforceDelegation_Subagent_AllowsWithoutExit(t *testing.T) {
	setupDelegationRepo(t)

	payload := `{"agent_id":"abc-123","tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}`
	var errBuf bytes.Buffer
	if err := runHookEnforceDelegation(strings.NewReader(payload), &errBuf); err != nil {
		t.Fatalf("runHookEnforceDelegation returned error: %v", err)
	}
}

// TestRunHookEnforceDelegation_UnrelatedTool_AllowsWithoutExit covers NR3: a
// tool outside delegationTools (e.g. Read) is never evaluated.
func TestRunHookEnforceDelegation_UnrelatedTool_AllowsWithoutExit(t *testing.T) {
	setupDelegationRepo(t)

	payload := `{"tool_name":"Read","tool_input":{"file_path":"internal/foo.go"}}`
	var errBuf bytes.Buffer
	if err := runHookEnforceDelegation(strings.NewReader(payload), &errBuf); err != nil {
		t.Fatalf("runHookEnforceDelegation returned error: %v", err)
	}
}

// TestRunHookEnforceDelegation_WhitelistedPath_AllowsWithoutExit exercises
// the full wrapper (JSON parse, caller resolution, tool filter, config load,
// project detection, manifest query) for an allowed, whitelisted path.
func TestRunHookEnforceDelegation_WhitelistedPath_AllowsWithoutExit(t *testing.T) {
	setupDelegationRepo(t)

	payload := `{"tool_name":"Edit","tool_input":{"file_path":".claude/settings.json"}}`
	var errBuf bytes.Buffer
	if err := runHookEnforceDelegation(strings.NewReader(payload), &errBuf); err != nil {
		t.Fatalf("runHookEnforceDelegation returned error: %v", err)
	}
	if errBuf.Len() != 0 {
		t.Errorf("expected no stderr output for an allowed invocation, got: %q", errBuf.String())
	}
}

// TestRunHookEnforceDelegation_UnownedPath_AllowsWithoutExit covers AC5
// through the full wrapper: a real seeded manifest exists but does not own
// the target path.
func TestRunHookEnforceDelegation_UnownedPath_AllowsWithoutExit(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	seedManifest(t, dbPath, slug, `[{"role":"backend","areas":["internal/**"]}]`)

	payload := `{"tool_name":"Edit","tool_input":{"file_path":"README.md"}}`
	var errBuf bytes.Buffer
	if err := runHookEnforceDelegation(strings.NewReader(payload), &errBuf); err != nil {
		t.Fatalf("runHookEnforceDelegation returned error: %v", err)
	}
}

// TestRunHookEnforceDelegation_InvalidJSON_AllowsWithoutExit covers AC7: an
// empty stdin (io.EOF) fails open.
func TestRunHookEnforceDelegation_InvalidJSON_AllowsWithoutExit(t *testing.T) {
	var errBuf bytes.Buffer
	if err := runHookEnforceDelegation(strings.NewReader(""), &errBuf); err != nil {
		t.Fatalf("runHookEnforceDelegation returned error: %v", err)
	}
}

// --- printDelegationBlock ----------------------------------------------------

// TestPrintDelegationBlock_NamesOwner verifies the rendered message includes
// the "(delega a @<owner>)" annotation for a real implementer owner, but not
// for "legacy" or an empty owner (hard-blocks).
func TestPrintDelegationBlock_NamesOwner(t *testing.T) {
	tests := []struct {
		name       string
		owner      string
		wantSubstr string
		wantAbsent string
	}{
		{"real_owner_named", "backend", "(delega a @backend)", ""},
		{"legacy_owner_not_named", "legacy", "", "(delega a @legacy)"},
		{"empty_owner_not_named", "", "", "(delega a @"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			decision := enforcement.Decision{Block: true, Reason: "Ruta bloqueada: 'x'", Owner: tt.owner}
			printDelegationBlock(&buf, decision)
			got := buf.String()

			if tt.wantSubstr != "" && !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("output missing %q\nfull output:\n%s", tt.wantSubstr, got)
			}
			if tt.wantAbsent != "" && strings.Contains(got, tt.wantAbsent) {
				t.Errorf("output should not contain %q\nfull output:\n%s", tt.wantAbsent, got)
			}
			if !strings.Contains(got, "BLOQUEADO") {
				t.Error("expected BLOQUEADO in output")
			}
		})
	}
}

// --- logBlockedEditDiscovery (AC8: a save failure never changes enforcement) -

// TestLogBlockedEditDiscovery_SaveFailure_WritesWarningOnly verifies that an
// injected failing saveDiscoveryFunc only writes a warning to errW and never
// panics or otherwise escapes — the block/os.Exit(2) it follows is entirely
// unaffected by a logging failure (R3).
func TestLogBlockedEditDiscovery_SaveFailure_WritesWarningOnly(t *testing.T) {
	failingSave := func(_ context.Context, _ model.SaveRequest) (*model.SaveResponse, error) {
		return nil, errors.New("simulated save failure")
	}

	var errBuf bytes.Buffer
	logBlockedEditDiscovery(&errBuf, failingSave, "Edit", "internal/foo.go", "Ruta bloqueada: 'internal/foo.go'")

	if !strings.Contains(errBuf.String(), "save failed") {
		t.Errorf("expected a save-failure warning on errW, got: %q", errBuf.String())
	}
}

// TestLogBlockedEditDiscovery_SuccessfulSave_ReceivesExpectedRequest verifies
// the Title/Content/Type of the discovery memory logBlockedEditDiscovery
// builds, mirroring block()'s bash logging call (AC8).
func TestLogBlockedEditDiscovery_SuccessfulSave_ReceivesExpectedRequest(t *testing.T) {
	var captured model.SaveRequest
	save := func(_ context.Context, req model.SaveRequest) (*model.SaveResponse, error) {
		captured = req
		return &model.SaveResponse{}, nil
	}

	var errBuf bytes.Buffer
	logBlockedEditDiscovery(&errBuf, save, "Edit", "internal/foo.go", "Ruta bloqueada: 'internal/foo.go'")

	if errBuf.Len() != 0 {
		t.Errorf("expected no stderr output on successful save, got: %q", errBuf.String())
	}
	wantTitle := "Blocked edit: principal -> Edit -> foo.go"
	if captured.Title != wantTitle {
		t.Errorf("Title = %q, want %q", captured.Title, wantTitle)
	}
	if captured.Type != model.TypeDiscovery {
		t.Errorf("Type = %q, want %q", captured.Type, model.TypeDiscovery)
	}
	if !strings.Contains(captured.Content, "Ruta bloqueada: 'internal/foo.go'") {
		t.Errorf("Content missing the block reason: %q", captured.Content)
	}
}

// TestLogBlockedEditDiscovery_EmptyTarget_UsesUnknownBasename verifies the
// "unknown" fallback for hard-blocks (sed -i, python/node inline scripts)
// that have no single resolvable target path.
func TestLogBlockedEditDiscovery_EmptyTarget_UsesUnknownBasename(t *testing.T) {
	var captured model.SaveRequest
	save := func(_ context.Context, req model.SaveRequest) (*model.SaveResponse, error) {
		captured = req
		return &model.SaveResponse{}, nil
	}

	var errBuf bytes.Buffer
	logBlockedEditDiscovery(&errBuf, save, "Bash", "", "sed -i fuera de .claude/")

	wantTitle := "Blocked edit: principal -> Bash -> unknown"
	if captured.Title != wantTitle {
		t.Errorf("Title = %q, want %q", captured.Title, wantTitle)
	}
}
