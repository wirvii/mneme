package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/rules"
)

// mnemeRepoRoot returns the absolute path of the mneme repository root.
// The test binary cwd is always the package directory (internal/cli), so
// the repo root is two levels up.
func mnemeRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	// internal/cli → repo root = ../../
	return filepath.Join(cwd, "..", "..")
}

// repoSlug is the project slug produced by project.NewDetector for the mneme
// repository. It is derived from the remote URL git@github.com:wirvii/mneme.git.
const repoSlug = "wirvii/mneme"

// setupNudgeDB creates a temporary DataDir, builds a codegraph DB for the
// mneme project slug at the expected path, and returns (dataDir, dbPath).
// It seeds the DB with one node having the given updatedAtMs timestamp.
// Call t.Setenv("MNEME_DATA_DIR", dataDir) after this to redirect config.
func setupNudgeDB(t *testing.T, updatedAtMs int64) (dataDir, dbPath string) {
	t.Helper()
	dataDir = t.TempDir()
	projectsDir := filepath.Join(dataDir, "projects")

	// DBPath joins projectsDir with slug+"-codegraph.db". Because the slug is
	// "wirvii/mneme", the resulting path contains a sub-directory "wirvii/" that
	// must be created before the file is opened.
	dbPath = codegraph.DBPath(projectsDir, repoSlug)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir for dbPath: %v", err)
	}
	cdb, err := codegraph.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}

	if updatedAtMs > 0 {
		// Insert one node so ProbeGraph returns hasNodes=true.
		st := codegraph.NewStore(cdb)
		n := codegraph.Node{
			ID:            "test-node-001",
			Kind:          codegraph.NodeKindFunction,
			Name:          "TestFn",
			QualifiedName: "pkg.TestFn",
			FilePath:      "pkg/fn.go",
			Language:      "go",
			StartLine:     1,
			EndLine:       5,
			UpdatedAt:     updatedAtMs,
		}
		if err := st.UpsertNode(n); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}
	cdb.Close()
	return dataDir, dbPath
}

// buildNudgeInput builds a hookPreToolInput for nudge tests.
func buildNudgeInput(toolName, filePath, sessionID, grepPath string) hookPreToolInput {
	var inp hookPreToolInput
	inp.ToolName = toolName
	inp.ToolInput.FilePath = filePath
	inp.ToolInput.Path = grepPath
	inp.SessionID = sessionID
	return inp
}

// cleanStatefile removes the nudge statefile from dataDir if it exists, so
// tests start with a clean slate.
func cleanStatefile(dataDir string) {
	_ = os.Remove(filepath.Join(dataDir, nudgeStateFilename))
}

// TestNudge_FiresOnRead_FreshGraph verifies that a Read payload for a project
// with a fresh (non-stale) indexed code graph emits the nudge block and does
// NOT include the stale-refresh line.
func TestNudge_FiresOnRead_FreshGraph(t *testing.T) {
	freshMs := time.Now().UnixMilli()
	dataDir, _ := setupNudgeDB(t, freshMs)
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	inp := buildNudgeInput("Read", "internal/x.go", "s1", "")

	var stdout, stderr bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &stdout, &stderr)

	out := stdout.String()
	if !strings.Contains(out, "<!-- mneme:codegraph-nudge:start -->") {
		t.Errorf("nudge block missing; stdout: %s", out)
	}
	if !strings.Contains(out, "<!-- mneme:codegraph-nudge:end -->") {
		t.Errorf("nudge end-tag missing; stdout: %s", out)
	}
	if strings.Contains(out, "mneme codegraph index") {
		t.Errorf("fresh graph should NOT include stale-refresh line; stdout: %s", out)
	}
}

// TestNudge_FiresOnGrep_NoPath verifies that a Grep payload without a path
// (no file_path or path field) still emits the nudge. The anti-loop filter must
// not block the nudge when there is no candidate path to inspect.
func TestNudge_FiresOnGrep_NoPath(t *testing.T) {
	freshMs := time.Now().UnixMilli()
	dataDir, _ := setupNudgeDB(t, freshMs)
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	inp := buildNudgeInput("Grep", "", "s2", "")

	var stdout, stderr bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &stdout, &stderr)

	if !strings.Contains(stdout.String(), "<!-- mneme:codegraph-nudge:start -->") {
		t.Errorf("nudge should fire for Grep without path; stdout: %s", stdout.String())
	}
}

// TestNudge_FiresOnGlob verifies that a Glob payload emits the nudge.
func TestNudge_FiresOnGlob(t *testing.T) {
	freshMs := time.Now().UnixMilli()
	dataDir, _ := setupNudgeDB(t, freshMs)
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	inp := buildNudgeInput("Glob", "", "s3", "**/*.go")

	var stdout, stderr bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &stdout, &stderr)

	if !strings.Contains(stdout.String(), "<!-- mneme:codegraph-nudge:start -->") {
		t.Errorf("nudge should fire for Glob; stdout: %s", stdout.String())
	}
}

// TestNudge_NotFiredOnEdit verifies that mutating tools (e.g. Write) do NOT
// trigger the nudge — they proceed to the rules engine branch unchanged.
func TestNudge_NotFiredOnEdit(t *testing.T) {
	freshMs := time.Now().UnixMilli()
	dataDir, _ := setupNudgeDB(t, freshMs)
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	inp := buildNudgeInput("Write", "internal/x.go", "s4", "")

	var stdout, stderr bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &stdout, &stderr)

	if strings.Contains(stdout.String(), "codegraph-nudge") {
		t.Errorf("nudge must NOT fire for Write; stdout: %s", stdout.String())
	}
}

// TestNudge_SuppressedSecondCall_SameSession verifies that two invocations with
// the same session_id only produce the nudge on the first call.
func TestNudge_SuppressedSecondCall_SameSession(t *testing.T) {
	freshMs := time.Now().UnixMilli()
	dataDir, _ := setupNudgeDB(t, freshMs)
	t.Setenv("MNEME_DATA_DIR", dataDir)
	cleanStatefile(dataDir)

	cwd := mnemeRepoRoot(t)
	inp := buildNudgeInput("Read", "internal/x.go", "sess-abc", "")

	var out1, out2 bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &out1, io.Discard)
	maybeEmitCodegraphNudge(inp, cwd, &out2, io.Discard)

	if !strings.Contains(out1.String(), "codegraph-nudge") {
		t.Errorf("first call should emit nudge; stdout: %s", out1.String())
	}
	if strings.Contains(out2.String(), "codegraph-nudge") {
		t.Errorf("second call with same session_id should be suppressed; stdout: %s", out2.String())
	}
}

// TestNudge_TTLExpiry_NoSession verifies the project-keyed TTL behaviour when
// no session_id is present. The nudge re-fires after 4h (simulated by writing
// an expired entry directly into the statefile).
func TestNudge_TTLExpiry_NoSession(t *testing.T) {
	freshMs := time.Now().UnixMilli()
	dataDir, _ := setupNudgeDB(t, freshMs)
	t.Setenv("MNEME_DATA_DIR", dataDir)

	stateFilePath := filepath.Join(dataDir, nudgeStateFilename)
	key := "proj:" + repoSlug

	cwd := mnemeRepoRoot(t)
	inp := buildNudgeInput("Read", "internal/x.go", "", "") // no session_id → proj: key

	// Case 1: TTL not expired (stored ~1h ago).
	storeState(t, stateFilePath, key, time.Now().Add(-1*time.Hour).UnixMilli())
	var out1 bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &out1, io.Discard)
	if strings.Contains(out1.String(), "codegraph-nudge") {
		t.Errorf("nudge should be suppressed when TTL not expired; stdout: %s", out1.String())
	}

	// Case 2: TTL expired (stored > 4h ago).
	storeState(t, stateFilePath, key, time.Now().Add(-5*time.Hour).UnixMilli())
	var out2 bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &out2, io.Discard)
	if !strings.Contains(out2.String(), "codegraph-nudge") {
		t.Errorf("nudge should re-fire after TTL expiry; stdout: %s", out2.String())
	}
}

// storeState writes a single-key nudge statefile entry directly for TTL testing.
func storeState(t *testing.T, path, key string, unixMs int64) {
	t.Helper()
	data, _ := json.Marshal(nudgeState{key: unixMs})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("storeState: %v", err)
	}
}

// TestNudge_EmptyGraph_NoNudge verifies that no nudge is emitted when the
// codegraph DB exists but has 0 nodes (A-D6: empty graph → not helpful).
func TestNudge_EmptyGraph_NoNudge(t *testing.T) {
	// setupNudgeDB with updatedAtMs=0 creates DB with schema but no nodes.
	dataDir, _ := setupNudgeDB(t, 0)
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	inp := buildNudgeInput("Read", "internal/x.go", "s-empty", "")

	var stdout bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &stdout, io.Discard)

	if strings.Contains(stdout.String(), "codegraph-nudge") {
		t.Errorf("nudge must NOT fire for empty graph; stdout: %s", stdout.String())
	}
}

// TestNudge_StaleGraph_AddsRefreshLine verifies that when MAX(updated_at) is
// older than 24h, the nudge includes the stale-refresh recommendation line.
func TestNudge_StaleGraph_AddsRefreshLine(t *testing.T) {
	// 25h ago — exceeds the 24h staleness threshold.
	staleMs := time.Now().Add(-25 * time.Hour).UnixMilli()
	dataDir, _ := setupNudgeDB(t, staleMs)
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	inp := buildNudgeInput("Read", "internal/x.go", "s-stale", "")

	var stdout bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &stdout, io.Discard)

	out := stdout.String()
	if !strings.Contains(out, "codegraph-nudge") {
		t.Errorf("nudge should fire for stale graph; stdout: %s", out)
	}
	if !strings.Contains(out, "mneme codegraph index") {
		t.Errorf("stale nudge should recommend 'mneme codegraph index'; stdout: %s", out)
	}
}

// TestNudge_AntiLoop_MnemeInternalPath verifies that a Read targeting a file
// under ~/.mneme (DataDir) does NOT trigger the nudge (anti-loop guard).
func TestNudge_AntiLoop_MnemeInternalPath(t *testing.T) {
	freshMs := time.Now().UnixMilli()
	dataDir, _ := setupNudgeDB(t, freshMs)
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	// file_path is inside the DataDir — should trigger anti-loop.
	internalPath := filepath.Join(dataDir, "global.db")
	inp := buildNudgeInput("Read", internalPath, "s-loop", "")

	var stdout bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &stdout, io.Discard)

	if strings.Contains(stdout.String(), "codegraph-nudge") {
		t.Errorf("nudge must NOT fire for mneme-internal path; stdout: %s", stdout.String())
	}
}

// TestNudge_OptOut_Config verifies that setting HookNudgeEnabled=false in
// config suppresses the nudge.
func TestNudge_OptOut_Config(t *testing.T) {
	freshMs := time.Now().UnixMilli()
	dataDir, _ := setupNudgeDB(t, freshMs)
	t.Setenv("MNEME_DATA_DIR", dataDir)
	// Opt-out via env (which drives config in the test environment).
	t.Setenv("MNEME_CODEGRAPH_HOOK_NUDGE", "false")

	cwd := mnemeRepoRoot(t)
	inp := buildNudgeInput("Read", "internal/x.go", "s-optout", "")

	var stdout bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &stdout, io.Discard)

	if strings.Contains(stdout.String(), "codegraph-nudge") {
		t.Errorf("nudge must be suppressed when HookNudgeEnabled=false; stdout: %s", stdout.String())
	}
}

// TestNudge_OptOut_Env verifies that MNEME_CODEGRAPH_HOOK_NUDGE=false suppresses
// the nudge (env override wins over any config default).
func TestNudge_OptOut_Env(t *testing.T) {
	freshMs := time.Now().UnixMilli()
	dataDir, _ := setupNudgeDB(t, freshMs)
	t.Setenv("MNEME_DATA_DIR", dataDir)
	t.Setenv("MNEME_CODEGRAPH_HOOK_NUDGE", "0")

	cwd := mnemeRepoRoot(t)
	inp := buildNudgeInput("Read", "internal/x.go", "s-optout-env", "")

	var stdout bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &stdout, io.Discard)

	if strings.Contains(stdout.String(), "codegraph-nudge") {
		t.Errorf("nudge must be suppressed when MNEME_CODEGRAPH_HOOK_NUDGE=0; stdout: %s", stdout.String())
	}
}

// TestNudge_FailOpen_NoProject verifies that when cwd is not a git repo (no
// project slug can be derived), the nudge silently returns without emitting
// anything and without panicking.
func TestNudge_FailOpen_NoProject(t *testing.T) {
	freshMs := time.Now().UnixMilli()
	dataDir, _ := setupNudgeDB(t, freshMs)
	t.Setenv("MNEME_DATA_DIR", dataDir)

	// Use a temp dir that is NOT a git repo — DetectProject returns "".
	nonGitDir := t.TempDir()
	inp := buildNudgeInput("Read", "internal/x.go", "s-noproj", "")

	var stdout, stderr bytes.Buffer
	// Must not panic.
	maybeEmitCodegraphNudge(inp, nonGitDir, &stdout, &stderr)

	if strings.Contains(stdout.String(), "codegraph-nudge") {
		t.Errorf("nudge must not fire for no-project cwd; stdout: %s", stdout.String())
	}
}

// TestNudge_FailOpen_CorruptStatefile verifies that a corrupted statefile is
// treated as "never injected" — the nudge fires normally without returning any
// error.
func TestNudge_FailOpen_CorruptStatefile(t *testing.T) {
	freshMs := time.Now().UnixMilli()
	dataDir, _ := setupNudgeDB(t, freshMs)
	t.Setenv("MNEME_DATA_DIR", dataDir)

	// Write invalid JSON to the statefile.
	stateFilePath := filepath.Join(dataDir, nudgeStateFilename)
	if err := os.WriteFile(stateFilePath, []byte("{not valid json!!!"), 0o600); err != nil {
		t.Fatalf("write corrupt statefile: %v", err)
	}

	cwd := mnemeRepoRoot(t)
	inp := buildNudgeInput("Read", "internal/x.go", "s-corrupt", "")

	var stdout bytes.Buffer
	// Must not panic and must emit nudge (corrupt = "never injected").
	maybeEmitCodegraphNudge(inp, cwd, &stdout, io.Discard)

	if !strings.Contains(stdout.String(), "codegraph-nudge") {
		t.Errorf("corrupt statefile should be treated as never-injected; stdout: %s", stdout.String())
	}
}

// TestNudge_DoesNotAffectBlockRules verifies that the codegraph nudge does not
// interfere with the blocking rules engine. When a block-severity rule matches
// an Edit invocation, the existing exit-2 behaviour is preserved.
// We test this by checking that renderPreToolUseOutput (called by the rules
// branch) still produces the BLOCKED action line, independent of the nudge.
func TestNudge_DoesNotAffectBlockRules(t *testing.T) {
	// Set up a temp DataDir; no codegraph DB needed (Edit won't nudge).
	dataDir := t.TempDir()
	t.Setenv("MNEME_DATA_DIR", dataDir)

	// Build a block rule and verify the rules-engine output is intact.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if insertErr := insertTestRule(database, "r-block", "Protect source", "Do not edit source.",
		model.SeverityBlock, []string{"tool:Edit+internal/**"}); insertErr != nil {
		t.Fatalf("insertTestRule: %v", insertErr)
	}
	database.Close()

	rulesList, err := queryRulesFromDB(dbPath)
	if err != nil {
		t.Fatalf("queryRulesFromDB: %v", err)
	}

	cwd := dir
	filePath := filepath.Join(dir, "internal", "store", "memory.go")
	result := rules.Match(rulesList, rules.Invocation{Tool: "Edit", FilePath: filePath, CWD: cwd, Caller: rules.CallerOrchestrator})

	var stdout bytes.Buffer
	renderPreToolUseOutput(&stdout, "Edit", filePath, cwd, result)

	out := stdout.String()
	if !strings.Contains(out, "Action: BLOCKED") {
		t.Errorf("block rule should produce BLOCKED action line; stdout: %s", out)
	}

	// Verify nudge was NOT emitted for Edit (nudgeTools check).
	inp := buildNudgeInput("Edit", filePath, "s-block", "")
	var nudgeOut bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &nudgeOut, io.Discard)
	if strings.Contains(nudgeOut.String(), "codegraph-nudge") {
		t.Errorf("nudge must NOT fire for Edit tool; stdout: %s", nudgeOut.String())
	}
}
