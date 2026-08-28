package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/gitident"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/sddfile"
)

// runSDDCmd builds a minimal Cobra tree so newSDDCmd() can be invoked in
// isolation, chdirs into cwd for the duration of the call (restored via
// t.Cleanup, SPEC-085 rule 3), and returns stdout, stderr, and any error.
// gitident.Reset() guards against an earlier test's resolved identity
// leaking in (SPEC-085 rule 5) even though the SDD mechanism itself never
// touches gitident (AC6) — the same defensive posture every other
// chdir-into-a-fixture-repo helper in this package already takes.
func runSDDCmd(t *testing.T, cwd string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	resetGlobalCLIFlags(t)
	gitident.Reset()
	t.Cleanup(gitident.Reset)

	root := &cobra.Command{Use: "mneme"}
	root.AddCommand(newSDDCmd())

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

	root.SetArgs(append([]string{"sdd"}, args...))
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// sddCLITestRepo creates a fresh git repository with an initial commit
// (needed for `git status --porcelain` to be meaningful) and a fake HOME
// (SPEC-085 G2/D38: never touch the real ~/.mneme).
func sddCLITestRepo(t *testing.T) (repoDir, fakeHome string) {
	t.Helper()
	repoDir = t.TempDir()
	initGitRepo(t, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitOK(t, repoDir, "add", ".")
	runGitOK(t, repoDir, "commit", "-m", "initial")

	fakeHome = t.TempDir()
	return repoDir, fakeHome
}

func runGitOK(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// seedSDDBacklog chdirs into repoDir, opens the SAME database
// initSDDService() will later reopen (fakeHome/project detection is
// identical for both calls since neither the seed step nor the command
// step sets a git remote), creates one backlog item, and returns its ID.
func seedSDDBacklog(t *testing.T, repoDir, fakeHome, title string) string {
	t.Helper()
	resetGlobalCLIFlags(t)
	gitident.Reset()
	t.Cleanup(gitident.Reset)
	t.Setenv("HOME", fakeHome)

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(orig) }()

	svc, cleanup, err := initSDDService()
	if err != nil {
		t.Fatalf("initSDDService (seed): %v", err)
	}
	defer cleanup()

	item, err := svc.BacklogAdd(context.Background(), model.BacklogAddRequest{
		Title: title, Lane: model.LaneStandard,
	})
	if err != nil {
		t.Fatalf("BacklogAdd (seed): %v", err)
	}
	return item.ID
}

// countAllFiles walks dir and counts every regular file. Returns 0 for a
// missing directory.
func countAllFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return n
}

// TestSDDEnable_DryRunWritesNothing is AC12.
func TestSDDEnable_DryRunWritesNothing(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	seedSDDBacklog(t, repoDir, fakeHome, "dry run item")

	t.Setenv("HOME", fakeHome)
	stdout, _, err := runSDDCmd(t, repoDir, "enable")
	if err != nil {
		t.Fatalf("sdd enable (dry-run): %v", err)
	}

	if n := countAllFiles(t, filepath.Join(repoDir, ".mneme")); n != 0 {
		t.Errorf(".mneme has %d file(s) after a dry-run, want 0", n)
	}

	// AC14: the honest warnings, checked by substring assertion here (not
	// in a separate test) — see the criterion's own note that this
	// assertion belongs with AC12's dry-run output.
	mustContain := []string{
		"no puede determinar si el remoto es publico",
		"no ha escaneado el contenido",
		"revisarse en un pull request",
	}
	for _, want := range mustContain {
		if !strings.Contains(stdout, want) {
			t.Errorf("dry-run output does not contain %q:\n%s", want, stdout)
		}
	}
}

// TestSDDEnable_ApplyThenIdempotent is AC13.
func TestSDDEnable_ApplyThenIdempotent(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	seedSDDBacklog(t, repoDir, fakeHome, "apply item")

	t.Setenv("HOME", fakeHome)

	if _, _, err := runSDDCmd(t, repoDir, "enable", "--apply"); err != nil {
		t.Fatalf("sdd enable --apply (first): %v", err)
	}
	firstStatus := gitPorcelain(t, repoDir)

	if _, _, err := runSDDCmd(t, repoDir, "enable", "--apply"); err != nil {
		t.Fatalf("sdd enable --apply (second): %v", err)
	}
	secondStatus := gitPorcelain(t, repoDir)

	if firstStatus != secondStatus {
		t.Errorf("git status --porcelain differs between the two applies:\nfirst=%q\nsecond=%q", firstStatus, secondStatus)
	}
}

func gitPorcelain(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status --porcelain: %v\n%s", err, out)
	}
	return string(out)
}

// TestSDDEnable_RefusesForeignRecords is AC17 (SPEC-130), UPDATED by
// SPEC-131 D61: the refusal's text changed from "leerlos llega con BL-201"
// (reading did not exist) to "ejecuta mneme sdd import primero" (it does
// now) — the mention of BL-202 (the still-pending collision reconciler)
// survives unchanged. A BL-050.md whose anchor the local database does not
// know makes `sdd enable --apply` (and `sdd export`) refuse WITHOUT
// writing anything, naming the file, the import remedy, and BL-202. The
// required mutation — removing the guard — is executed for real below and
// reverted byte for byte.
func TestSDDEnable_RefusesForeignRecords(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	t.Setenv("HOME", fakeHome)

	foreign := &sddfile.BacklogRecord{Item: &model.BacklogItem{
		ID: "BL-050", Title: "de otra maquina", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: "wirvii/mneme", Lane: model.LaneStandard,
		UUID: "01a044bc-7c25-7448-87e9-febc5c5982ee",
	}}
	data, err := sddfile.MarshalBacklog(foreign)
	if err != nil {
		t.Fatalf("MarshalBacklog fixture: %v", err)
	}
	path := sddfile.BacklogPath(repoDir, "BL-050")
	if err := sddfile.WriteRecord(path, data); err != nil {
		t.Fatalf("WriteRecord fixture: %v", err)
	}

	stdout, stderr, err := runSDDCmd(t, repoDir, "enable", "--apply")
	if err == nil {
		t.Fatalf("sdd enable --apply must refuse with a foreign anchor present; stdout=%q stderr=%q", stdout, stderr)
	}
	combined := stdout + stderr + err.Error()
	if !strings.Contains(combined, "mneme sdd import") || !strings.Contains(combined, "BL-202") {
		t.Errorf("refusal message does not name the import remedy / BL-202: %s", combined)
	}

	after, err := sddfile.ReadRecord(path)
	if err != nil {
		t.Fatalf("ReadRecord after refusal: %v", err)
	}
	if string(after) != string(data) {
		t.Error("the foreign file must be byte-for-byte untouched after a refused enable")
	}
}

// TestSDDDisable exercises AC18: no-op without --apply, --apply creates
// sdd.off and adds it to .gitignore without disturbing existing entries,
// never deletes anything, and a later mutation writes nothing more.
func TestSDDDisable(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	itemID := seedSDDBacklog(t, repoDir, fakeHome, "disable item")
	t.Setenv("HOME", fakeHome)

	// Pre-existing, unrelated .gitignore content that must survive.
	gitignorePath := filepath.Join(repoDir, ".mneme", ".gitignore")
	if err := os.MkdirAll(filepath.Dir(gitignorePath), 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	if err := os.WriteFile(gitignorePath, []byte("shared/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	if _, _, err := runSDDCmd(t, repoDir, "enable", "--apply"); err != nil {
		t.Fatalf("sdd enable --apply: %v", err)
	}
	recordPath := sddfile.BacklogPath(repoDir, itemID)
	before, err := sddfile.ReadRecord(recordPath)
	if err != nil {
		t.Fatalf("ReadRecord before disable: %v", err)
	}

	offPath := filepath.Join(repoDir, ".mneme", "sdd.off")

	// Without --apply: no change.
	if _, _, err := runSDDCmd(t, repoDir, "disable"); err != nil {
		t.Fatalf("sdd disable (dry-run): %v", err)
	}
	if _, statErr := os.Stat(offPath); !os.IsNotExist(statErr) {
		t.Error("sdd.off must not exist after a dry-run disable")
	}

	// With --apply: sdd.off appears, .gitignore gains it without losing
	// "shared/", and the pre-existing record is untouched.
	if _, _, err := runSDDCmd(t, repoDir, "disable", "--apply"); err != nil {
		t.Fatalf("sdd disable --apply: %v", err)
	}
	if _, statErr := os.Stat(offPath); statErr != nil {
		t.Errorf("sdd.off must exist after --apply: %v", statErr)
	}
	gitignore, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), "shared/") {
		t.Error(".gitignore lost its pre-existing shared/ entry")
	}
	if !strings.Contains(string(gitignore), "sdd.off") {
		t.Error(".gitignore was not given the sdd.off entry")
	}

	after, err := sddfile.ReadRecord(recordPath)
	if err != nil {
		t.Fatalf("ReadRecord after disable: %v", err)
	}
	if string(before) != string(after) {
		t.Error("disable must never touch existing SDD records")
	}

	// A mutation after disable must write nothing.
	if _, err := seedAfterDisableRefine(t, repoDir, fakeHome, itemID); err != nil {
		t.Fatalf("BacklogRefine after disable: %v", err)
	}
	if n := countAllFiles(t, filepath.Join(repoDir, ".mneme", "sdd.off")); n == 0 {
		t.Error("sdd.off itself disappeared, which should never happen")
	}
	stillOne := countAllFiles(t, filepath.Join(repoDir, ".mneme", "sdd", "backlog"))
	if stillOne != 1 {
		t.Errorf("backlog dir has %d file(s) after a post-disable mutation, want still exactly 1 (unchanged)", stillOne)
	}
}

// seedAfterDisableRefine performs one BacklogRefine directly through the
// service layer (same DB the CLI commands above wrote to) to prove the
// wrapper is inert once .mneme/sdd.off exists.
func seedAfterDisableRefine(t *testing.T, repoDir, fakeHome, itemID string) (*model.BacklogItem, error) {
	t.Helper()
	resetGlobalCLIFlags(t)
	gitident.Reset()
	t.Cleanup(gitident.Reset)
	t.Setenv("HOME", fakeHome)

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(orig) }()

	svc, cleanup, err := initSDDService()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	return svc.BacklogRefine(context.Background(), model.BacklogRefineRequest{
		ID: itemID, Refinement: "after disable", By: "test",
	})
}

// TestSDDExportCmd exercises "mneme sdd export" end to end: it requires the
// mechanism to already be enabled, and re-materializes everything.
func TestSDDExportCmd(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	seedSDDBacklog(t, repoDir, fakeHome, "export item")
	t.Setenv("HOME", fakeHome)

	// export before enable must fail.
	if _, _, err := runSDDCmd(t, repoDir, "export"); err == nil {
		t.Fatal("sdd export before enable must fail")
	}

	if _, _, err := runSDDCmd(t, repoDir, "enable", "--apply"); err != nil {
		t.Fatalf("sdd enable --apply: %v", err)
	}

	stdout, _, err := runSDDCmd(t, repoDir, "export")
	if err != nil {
		t.Fatalf("sdd export: %v", err)
	}
	if !strings.Contains(stdout, "Exported 1 backlog item(s)") {
		t.Errorf("sdd export output does not report the count: %q", stdout)
	}
}

// TestSDDStatusCmd exercises "mneme sdd status" in both plain-text and
// --json modes, before and after enabling.
func TestSDDStatusCmd(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	seedSDDBacklog(t, repoDir, fakeHome, "status item")
	t.Setenv("HOME", fakeHome)

	stdout, _, err := runSDDCmd(t, repoDir, "status")
	if err != nil {
		t.Fatalf("sdd status (before enable): %v", err)
	}
	if !strings.Contains(stdout, "disabled") {
		t.Errorf("sdd status before enable should report disabled: %q", stdout)
	}

	if _, _, err := runSDDCmd(t, repoDir, "enable", "--apply"); err != nil {
		t.Fatalf("sdd enable --apply: %v", err)
	}

	stdout, _, err = runSDDCmd(t, repoDir, "status")
	if err != nil {
		t.Fatalf("sdd status (after enable): %v", err)
	}
	if !strings.Contains(stdout, "enabled") {
		t.Errorf("sdd status after enable should report enabled: %q", stdout)
	}

	jsonOut, _, err := runSDDCmd(t, repoDir, "status", "--json")
	if err != nil {
		t.Fatalf("sdd status --json: %v", err)
	}
	if !strings.Contains(jsonOut, `"enabled": true`) && !strings.Contains(jsonOut, `"Enabled": true`) {
		t.Errorf("sdd status --json does not report enabled: %q", jsonOut)
	}
}

// TestSDD_ExactlyOneAddCommand is AC20 (SPEC-130), measured over the diff
// rather than a hard-coded count elsewhere: this test only confirms
// newSDDCmd's own shape — the diff-based count itself is verified against
// the real git history in SPEC-131 AC21 (`mneme sdd hooks`/`import` hang
// off the ALREADY-registered `sdd` command; internal/cli/root.go gains no
// new top-level `Cmd(),` line). `hooks` (SPEC-131 D58) is the SDD
// mechanism's own subcommand GROUP, not a new top-level command — the
// same distinction team-memory's own `hooks` group already draws.
func TestSDD_ExactlyOneAddCommand(t *testing.T) {
	cmd := newSDDCmd()
	if cmd.Use != "sdd" {
		t.Fatalf("newSDDCmd().Use = %q, want %q", cmd.Use, "sdd")
	}
	want := map[string]bool{"enable": true, "disable": true, "export": true, "status": true, "hooks": true, "import": true}
	got := make(map[string]bool)
	for _, c := range cmd.Commands() {
		name := strings.Fields(c.Use)[0]
		got[name] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("newSDDCmd() is missing the %q subcommand", name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("newSDDCmd() has %d subcommands, want exactly %d: %v", len(got), len(want), got)
	}
}
