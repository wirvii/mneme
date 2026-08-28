package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRootForTestguard resolves this repository's own root relative to
// this test file, so the test works regardless of the working directory
// `go test` was invoked from.
func repoRootForTestguard(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("testguard check: runtime.Caller(0) failed")
	}
	// internal/cli/testguard_check_test.go -> repo root
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// TestTestguardScript_GitDirBothDirections is SPEC-131 AC28c: the FIFTH
// net scripts/testguard.sh gained (a `.git` directory inside the
// sandboxed TEST_HOME, SPEC-131 D60) is verified in BOTH directions here —
// creating it trips the guard, removing it restores green — the same
// discipline the plan requires: "a net that has never been seen red is
// not a net". This test never touches the REAL repository's own .git; it
// operates entirely inside its own t.TempDir().
func TestTestguardScript_GitDirBothDirections(t *testing.T) {
	scriptPath := filepath.Join(repoRootForTestguard(t), "scripts", "testguard.sh")
	testHome := t.TempDir()

	runScript := func() ([]byte, error) {
		cmd := exec.Command("bash", scriptPath)
		cmd.Env = append(os.Environ(), "TEST_HOME="+testHome)
		return cmd.CombinedOutput()
	}

	if out, err := runScript(); err != nil {
		t.Fatalf("testguard.sh must be green with an empty TEST_HOME: %v\n%s", err, out)
	}

	if err := os.Mkdir(filepath.Join(testHome, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir TEST_HOME/.git: %v", err)
	}
	out, err := runScript()
	if err == nil {
		t.Fatalf("testguard.sh must fail when TEST_HOME/.git exists, got exit 0:\n%s", out)
	}
	if !strings.Contains(string(out), ".git") {
		t.Errorf("testguard.sh failure output does not name .git:\n%s", out)
	}

	if err := os.RemoveAll(filepath.Join(testHome, ".git")); err != nil {
		t.Fatalf("remove TEST_HOME/.git: %v", err)
	}
	if out, err := runScript(); err != nil {
		t.Fatalf("testguard.sh must be green again after removing TEST_HOME/.git: %v\n%s", err, out)
	}
}
