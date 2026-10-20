package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/codegraph"
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

// setupOpportunityDB creates a temporary DataDir, builds a codegraph DB for the
// mneme project slug at the expected path, and returns (dataDir, dbPath).
// It seeds the DB with one node having the given updatedAtMs timestamp.
// Call t.Setenv("MNEME_DATA_DIR", dataDir) after this to redirect config.
func setupOpportunityDB(t *testing.T, updatedAtMs int64) (dataDir, dbPath string) {
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

// buildToolInput builds a hookPreToolInput for hook telemetry tests.
func buildToolInput(toolName, filePath, sessionID, grepPath string) hookPreToolInput {
	var inp hookPreToolInput
	inp.ToolName = toolName
	inp.ToolInput.FilePath = filePath
	inp.ToolInput.Path = grepPath
	inp.SessionID = sessionID
	return inp
}

// codegraphReminderMarkers are the strings the retired SPEC-044 reminder used
// to print; SPEC-165 requires none of them to reach the hook's stdout again.
var codegraphReminderMarkers = []string{"codegraph-nudge", "consult the code graph FIRST", "MANDATORY"}

// TestPreToolUse_NoCodegraphReminder is SPEC-165 C4: a Read on a project with
// an indexed graph exits cleanly, prints no reminder, creates no statefile and
// still records the opportunity.
func TestPreToolUse_NoCodegraphReminder(t *testing.T) {
	dataDir, _ := setupOpportunityDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)

	stdin := buildPreToolStdin("Read", "internal/x.go")
	var stdout, stderr bytes.Buffer
	if err := runHookPreToolUse(stdin, &stdout, &stderr); err != nil {
		t.Fatalf("runHookPreToolUse: %v", err)
	}
	for _, marker := range codegraphReminderMarkers {
		if strings.Contains(stdout.String(), marker) {
			t.Errorf("stdout must not contain %q; got: %s", marker, stdout.String())
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "codegraph-nudge-state.json")); !os.IsNotExist(err) {
		t.Errorf("the retired nudge statefile must not be created (stat err = %v)", err)
	}
	if events := readOpportunities(t, dataDir); len(events) != 1 {
		t.Errorf("expected 1 opportunity event, got %d", len(events))
	}
}

// TestOpportunity_NotRecordedForOtherTools verifies mutating and non-search
// tools never count as an opportunity.
func TestOpportunity_NotRecordedForOtherTools(t *testing.T) {
	dataDir, _ := setupOpportunityDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	recordCodegraphOpportunity(buildToolInput("Write", "internal/x.go", "s4", ""), cwd)
	recordCodegraphOpportunity(bashInput("go test ./...", "b3"), cwd)

	if events := readOpportunities(t, dataDir); len(events) != 0 {
		t.Fatalf("expected no opportunity events, got %d", len(events))
	}
}

// TestOpportunity_AntiLoop_MnemeInternalPath verifies that reading a file under
// the mneme data dir does not count as an opportunity.
func TestOpportunity_AntiLoop_MnemeInternalPath(t *testing.T) {
	dataDir, _ := setupOpportunityDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	recordCodegraphOpportunity(buildToolInput("Read", filepath.Join(dataDir, "global.db"), "s-loop", ""), cwd)

	if events := readOpportunities(t, dataDir); len(events) != 0 {
		t.Fatalf("expected no opportunity events for an internal path, got %d", len(events))
	}
}

// TestOpportunity_FailOpen_NoProject verifies a cwd that is not a git repo
// records nothing and does not panic.
func TestOpportunity_FailOpen_NoProject(t *testing.T) {
	dataDir, _ := setupOpportunityDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)

	recordCodegraphOpportunity(buildToolInput("Read", "internal/x.go", "s-noproj", ""), t.TempDir())

	if events := readOpportunities(t, dataDir); len(events) != 0 {
		t.Fatalf("expected no opportunity events without a project, got %d", len(events))
	}
}
