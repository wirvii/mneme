package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
)

// runTeamMemoryCmd builds a minimal Cobra tree exposing the full
// "team-memory" command group (all subcommands, not just one), chdirs into
// cwd for the duration of the call, and returns stdout/stderr/error.
func runTeamMemoryCmd(t *testing.T, cwd string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	resetGlobalCLIFlags(t)
	root := &cobra.Command{Use: "mneme"}
	root.AddCommand(newTeamMemoryCmd())

	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)

	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("Getwd: %v", wdErr)
	}
	if chErr := os.Chdir(cwd); chErr != nil {
		t.Fatalf("Chdir %s: %v", cwd, chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	root.SetArgs(append([]string{"team-memory"}, args...))
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// TestTeamMemoryImportAndStatus_CLI is SPEC-140 AC15: `import --dry-run`
// and `status` both exit 0; a real `import` over a vault with N notes this
// base does not have leaves those N memories COUNTED AGAINST THE DATABASE
// (never against the command's own printed numbers — Forma 3 of the dead-
// criteria catalog); and `status` without installed hooks names the exact
// literal `mneme team-memory hooks install`.
func TestTeamMemoryImportAndStatus_CLI(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	sharedRoot := writeSharedVaultMarker(t, dir)
	writeSharedNoteForHookTest(t, sharedRoot,
		"01938f1b-abcd-7abc-8def-0000000000a1", "team/import-cmd-fixture-a",
		"Imported via the visible command A", "First fixture memory.")
	writeSharedNoteForHookTest(t, sharedRoot,
		"01938f1b-abcd-7abc-8def-0000000000a2", "team/import-cmd-fixture-b",
		"Imported via the visible command B", "Second fixture memory.")

	// status, before hooks are installed: names the literal fix command,
	// and reports the vault as present.
	statusOut, _, statusErr := runTeamMemoryCmd(t, dir, "status")
	if statusErr != nil {
		t.Fatalf("team-memory status: %v", statusErr)
	}
	if !strings.Contains(statusOut, "mneme team-memory hooks install") {
		t.Errorf("status must name the exact fix command when hooks are missing: %q", statusOut)
	}
	if !strings.Contains(statusOut, "present") {
		t.Errorf("status must report the vault as present: %q", statusOut)
	}

	// import --dry-run: exits 0, writes nothing to the database.
	dryOut, _, dryErr := runTeamMemoryCmd(t, dir, "import", "--dry-run")
	if dryErr != nil {
		t.Fatalf("team-memory import --dry-run: %v", dryErr)
	}
	if !strings.Contains(dryOut, "Would import") {
		t.Errorf("dry-run output must say 'Would import', got: %q", dryOut)
	}
	assertMemoryAbsent(t, dir, fakeHome, "01938f1b-abcd-7abc-8def-0000000000a1")

	// import (real): exits 0, and the database — not the command's own
	// stdout — now has both memories.
	realOut, _, realErr := runTeamMemoryCmd(t, dir, "import")
	if realErr != nil {
		t.Fatalf("team-memory import: %v", realErr)
	}
	if strings.Contains(realOut, "Would import") {
		t.Errorf("real import output must not say 'Would import': %q", realOut)
	}
	assertMemoryPresent(t, dir, fakeHome, "01938f1b-abcd-7abc-8def-0000000000a1")
	assertMemoryPresent(t, dir, fakeHome, "01938f1b-abcd-7abc-8def-0000000000a2")
}

// assertMemoryPresent/assertMemoryAbsent query the LOCAL DATABASE directly
// (a fresh service instance, same fakeHome/cwd) rather than trusting any
// command's own printed count — Forma 3 of the dead-criteria catalog is
// exactly a criterion that measures where an error or a miscount can be
// silently absorbed before reaching the assertion.
func assertMemoryPresent(t *testing.T, cwd, fakeHome, id string) {
	t.Helper()
	if !memoryExistsInDB(t, cwd, fakeHome, id) {
		t.Fatalf("expected memory %s to exist in the local database", id)
	}
}

func assertMemoryAbsent(t *testing.T, cwd, fakeHome, id string) {
	t.Helper()
	if memoryExistsInDB(t, cwd, fakeHome, id) {
		t.Fatalf("expected memory %s to be ABSENT from the local database (dry-run must not write)", id)
	}
}

func memoryExistsInDB(t *testing.T, cwd, fakeHome, id string) bool {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(orig) }()
	t.Setenv("HOME", fakeHome)

	svc, cleanup, err := initService()
	if err != nil {
		t.Fatalf("initService: %v", err)
	}
	defer cleanup()

	m, err := svc.Get(context.Background(), id)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return false
		}
		t.Fatalf("Get(%s): %v", id, err)
	}
	return m != nil
}
