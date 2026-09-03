package cli

import (
	"bytes"
	"os"
	"testing"

	"github.com/spf13/cobra"
)

// TestSDDEnablePreview_WritesNothingPerGitStatus is SPEC-140 AC10: in a
// clean repository with no .gitattributes and no SDD marker, running
// `mneme sdd enable` (no --apply) followed by `mneme init --check` leaves
// `git status --porcelain` EMPTY. This asks git itself (Forma 7 of the
// dead-criteria catalog: never ask the text of a file what only the tool
// can answer) rather than trusting either command's own claim that it
// wrote nothing — preserving SPEC-130's literal promise that the preview
// "writes NOTHING — not even a probe file".
func TestSDDEnablePreview_WritesNothingPerGitStatus(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	t.Setenv("HOME", fakeHome)

	if _, _, err := runSDDCmd(t, repoDir, "enable"); err != nil {
		t.Fatalf("sdd enable (preview): %v", err)
	}

	if _, _, err := runInitCheckCmd(t, repoDir); err != nil {
		t.Fatalf("init --check: %v", err)
	}

	porcelain := gitPorcelain(t, repoDir)
	if porcelain != "" {
		t.Errorf("git status --porcelain is not empty after two previews:\n%s", porcelain)
	}
}

// runInitCheckCmd builds a minimal Cobra tree exposing "mneme init", chdirs
// into cwd for the duration of the call, and runs it with --check. Mirrors
// runSDDCmd's own construction.
func runInitCheckCmd(t *testing.T, cwd string) (stdout, stderr string, err error) {
	t.Helper()
	resetGlobalCLIFlags(t)

	root := &cobra.Command{Use: "mneme"}
	root.AddCommand(newInitCmd())

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

	root.SetArgs([]string{"init", "--check"})
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}
