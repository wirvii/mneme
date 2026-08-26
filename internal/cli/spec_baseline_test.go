package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSpecList_ReadableOutputByteForByte pins the EXACT output of
// "mneme spec list" as it stood before SPEC-126, so the freeze marker/note
// this spec adds can be proven to appear ONLY when at least one spec is
// congelada — never in the unmarked case (SPEC-126 AC15, plan.md paso 0).
//
// The three specs are created with "spec new" and NO --from-backlog, so
// their BacklogID stays empty and they can never be frozen (spec.md DD2):
// that is what keeps this golden file valid across every later step of the
// SPEC-126 implementation, even after specFreeze/BacklogStatusIndex exist.
//
// Regenerate with:
//
//	MNEME_UPDATE_GOLDEN=1 go test ./internal/cli -run TestSpecList_ReadableOutputByteForByte
func TestSpecList_ReadableOutputByteForByte(t *testing.T) {
	// Captured BEFORE any runBacklogCmd call: each call chdirs into its own
	// isolated temp directory and only restores the CALLER's cwd via
	// t.Cleanup at the very end of the test (LIFO), so by the time this
	// function body reaches the golden-file read/write below, os.Getwd()
	// would otherwise return the LAST call's temp directory, not this
	// package's real source tree.
	testDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	dataDir := t.TempDir()
	project := "test-spec-list-baseline"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "Alpha spec", "--lane", "standard"); err != nil {
		t.Fatalf("spec new Alpha: %v (stderr=%s)", err, stderr)
	}

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "Beta spec", "--lane", "standard"); err != nil {
		t.Fatalf("spec new Beta: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "advance", "SPEC-002", "--by", "orchestrator"); err != nil {
		t.Fatalf("spec advance SPEC-002: %v (stderr=%s)", err, stderr)
	}

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "Gamma spec", "--lane", "trivial", "--scope", "internal/model/*.go"); err != nil {
		t.Fatalf("spec new Gamma: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "quick", "SPEC-003", "One-line fixture rationale for the baseline.", "--by", "orchestrator"); err != nil {
		t.Fatalf("spec quick SPEC-003: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "spec", "list")
	if err != nil {
		t.Fatalf("spec list: %v (stderr=%s)", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("spec list: unexpected stderr: %s", stderr)
	}

	goldenPath := filepath.Join(testDir, "testdata", "spec_list_baseline.golden")

	if os.Getenv("MNEME_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, []byte(stdout), 0o644); err != nil {
			t.Fatalf("write golden file: %v", err)
		}
		t.Logf("wrote golden file %s (%d bytes)", goldenPath, len(stdout))
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file: %v (regenerate with MNEME_UPDATE_GOLDEN=1)", err)
	}
	if stdout != string(want) {
		t.Errorf("spec list output diverged from the pre-SPEC-126 baseline.\n--- got ---\n%q\n--- want ---\n%q",
			stdout, string(want))
	}
}
