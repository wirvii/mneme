package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// runBacklogCmd executes "mneme backlog <argv...>" against an isolated
// --data-dir/--project so tests never touch the real ~/.mneme instance, and
// returns stdout/stderr separately.
//
// It also chdirs into an isolated, non-git temp directory for the duration
// of the call — same pattern as runSubagentsCmd (internal/cli/subagents_test.go)
// — because --data-dir alone does nothing to isolate service.DetectTeamMemory,
// which resolves relative to the REAL process cwd via `git rev-parse
// --show-toplevel` (SPEC-085).
//
// newBacklogAddCmd writes its "Created ..." line and refinement advisory via
// fmt.Fprintf(os.Stdout, ...) directly (matching every other backlog
// subcommand in this file), not cmd.OutOrStdout() — so capturing output
// requires redirecting the process-wide os.Stdout for the call's duration,
// rather than cobra's SetOut.
func runBacklogCmd(t *testing.T, dataDir, project string, argv ...string) (stdout, stderr string, err error) {
	t.Helper()

	isolatedCwd := t.TempDir()
	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("getwd: %v", wdErr)
	}
	if chErr := os.Chdir(isolatedCwd); chErr != nil {
		t.Fatalf("chdir into isolated cwd: %v", chErr)
	}
	t.Cleanup(func() {
		if restoreErr := os.Chdir(orig); restoreErr != nil {
			t.Fatalf("restore cwd: %v", restoreErr)
		}
	})

	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("os.Pipe: %v", pipeErr)
	}
	origStdout := os.Stdout
	os.Stdout = w

	root := NewRootCmd()
	errBuf := new(bytes.Buffer)
	root.SetErr(errBuf)

	args := append([]string{"--data-dir", dataDir, "--project", project}, argv...)
	root.SetArgs(args)
	err = root.Execute()

	os.Stdout = origStdout
	if closeErr := w.Close(); closeErr != nil {
		t.Fatalf("close stdout pipe writer: %v", closeErr)
	}
	outBytes, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("read stdout pipe: %v", readErr)
	}

	return string(outBytes), errBuf.String(), err
}

// TestBacklogAdd_PrintsAdvisoryOnStandardLane verifies CLI parity with the
// MCP envelope (SPEC-103 AC5): a standard-lane "mneme backlog add" prints the
// grill-me refinement advisory to stdout.
func TestBacklogAdd_PrintsAdvisoryOnStandardLane(t *testing.T) {
	dataDir := t.TempDir()

	stdout, stderr, err := runBacklogCmd(t, dataDir, "test-backlog-add-standard",
		"backlog", "add", "Standard-lane item", "--lane", "standard")
	if err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stdout, "Created BL-") {
		t.Errorf("stdout missing creation line: %s", stdout)
	}
	if !strings.Contains(stdout, "grill-me") {
		t.Errorf("stdout missing refinement advisory (grill-me): %s", stdout)
	}
}

// TestBacklogAdd_NoAdvisoryOnTrivialLane verifies that a trivial-lane
// "mneme backlog add" prints no refinement advisory (SPEC-103 AC6).
func TestBacklogAdd_NoAdvisoryOnTrivialLane(t *testing.T) {
	dataDir := t.TempDir()

	stdout, stderr, err := runBacklogCmd(t, dataDir, "test-backlog-add-trivial",
		"backlog", "add", "Trivial-lane item", "--lane", "trivial", "--scope", "internal/model/*.go")
	if err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stdout, "Created BL-") {
		t.Errorf("stdout missing creation line: %s", stdout)
	}
	if strings.Contains(stdout, "grill-me") {
		t.Errorf("stdout unexpectedly contains refinement advisory for a trivial-lane item: %s", stdout)
	}
}
