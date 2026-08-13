package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestQualityStatusCmd_NoConstitution_ExitsZero covers AC24: `mneme quality
// status` in a repo with no constitution prints that the mechanism is off
// and exits 0 — the normal state of nearly every repo, not an error.
func TestQualityStatusCmd_NoConstitution_ExitsZero(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	root := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	root.SetOut(outBuf)
	root.SetErr(errBuf)

	origStdout := os.Stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("os.Pipe: %v", pipeErr)
	}
	os.Stdout = w

	root.SetArgs([]string{"--data-dir", t.TempDir(), "--project", "quality-status-cmd-test", "quality", "status"})
	execErr := root.Execute()

	os.Stdout = origStdout
	_ = w.Close()
	var stdoutBuf bytes.Buffer
	_, _ = stdoutBuf.ReadFrom(r)

	if execErr != nil {
		t.Fatalf("mneme quality status exited with error: %v (stderr: %s)", execErr, errBuf.String())
	}
	if stdoutBuf.Len() == 0 {
		t.Error("mneme quality status printed nothing to stdout")
	}
}

// TestQualityBaselineShowCmd_NoBaseline_ExitsZero covers the common state:
// no baseline registered yet — `mneme quality baseline show` reports that
// and exits 0, never an error.
func TestQualityBaselineShowCmd_NoBaseline_ExitsZero(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	root := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	root.SetOut(outBuf)
	root.SetErr(errBuf)

	origStdout := os.Stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("os.Pipe: %v", pipeErr)
	}
	os.Stdout = w

	root.SetArgs([]string{"--data-dir", t.TempDir(), "--project", "quality-baseline-show-cmd-test", "quality", "baseline", "show"})
	execErr := root.Execute()

	os.Stdout = origStdout
	_ = w.Close()
	var stdoutBuf bytes.Buffer
	_, _ = stdoutBuf.ReadFrom(r)

	if execErr != nil {
		t.Fatalf("mneme quality baseline show exited with error: %v (stderr: %s)", execErr, errBuf.String())
	}
	if stdoutBuf.Len() == 0 {
		t.Error("mneme quality baseline show printed nothing to stdout")
	}
}

// TestQualityBaselineUpdateCmd_NoCertificate_Errors covers the negative
// half at the CLI level: `mneme quality baseline update <spec-id>` with no
// certificate at all must exit non-zero, never silently write a file.
func TestQualityBaselineUpdateCmd_NoCertificate_Errors(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	root := NewRootCmd()
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"--data-dir", t.TempDir(), "--project", "quality-baseline-update-cmd-test", "quality", "baseline", "update", "SPEC-NOPE"})

	if err := root.Execute(); err == nil {
		t.Fatal("mneme quality baseline update with no certificate: want error, got nil")
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, ".mneme", "quality-baseline.toml")); !os.IsNotExist(statErr) {
		t.Errorf("baseline file exists after a refused update: %v", statErr)
	}
}
