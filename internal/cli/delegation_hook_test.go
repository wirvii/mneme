package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestDelegationHookCmd_Help(t *testing.T) {
	cmd := newDelegationHookCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, sub := range []string{"enable", "disable", "status"} {
		if !strings.Contains(out, sub) {
			t.Errorf("help output missing subcommand %q", sub)
		}
	}
}

// runDelegationHookCmd executes "mneme delegation-hook <argv...>" and returns
// stdout/stderr. Unlike the subagents commands, delegation-hook does not
// touch the memory database, so no --data-dir/--project isolation is needed.
func runDelegationHookCmd(t *testing.T, argv ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newDelegationHookCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	cmd.SetOut(outBuf)
	cmd.SetErr(errBuf)
	cmd.SetArgs(argv)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestDelegationHookCmd_EnableDisableStatusLifecycle(t *testing.T) {
	repoRoot := t.TempDir()

	stdout, _, err := runDelegationHookCmd(t, "status", repoRoot)
	if err != nil {
		t.Fatalf("status (initial): %v", err)
	}
	if !strings.HasPrefix(stdout, "disabled:") {
		t.Errorf("expected initial status disabled, got: %s", stdout)
	}

	stdout, _, err = runDelegationHookCmd(t, "enable", repoRoot)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !strings.HasPrefix(stdout, "enabled:") {
		t.Errorf("expected 'enabled: ...' output, got: %s", stdout)
	}

	stdout, _, err = runDelegationHookCmd(t, "status", repoRoot)
	if err != nil {
		t.Fatalf("status (after enable): %v", err)
	}
	if !strings.HasPrefix(stdout, "enabled:") {
		t.Errorf("expected status enabled after enable, got: %s", stdout)
	}

	stdout, _, err = runDelegationHookCmd(t, "disable", repoRoot)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if !strings.HasPrefix(stdout, "disabled:") {
		t.Errorf("expected 'disabled: ...' output, got: %s", stdout)
	}

	stdout, _, err = runDelegationHookCmd(t, "status", repoRoot)
	if err != nil {
		t.Fatalf("status (after disable): %v", err)
	}
	if !strings.HasPrefix(stdout, "disabled:") {
		t.Errorf("expected status disabled after disable, got: %s", stdout)
	}
}

func TestDelegationHookCmd_DefaultsToCurrentDirectory(t *testing.T) {
	repoRoot := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldWD) }()

	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	stdout, _, err := runDelegationHookCmd(t, "enable")
	if err != nil {
		t.Fatalf("enable (no path arg): %v", err)
	}
	if !strings.Contains(stdout, repoRoot) {
		t.Errorf("expected output to reference cwd %s, got: %s", repoRoot, stdout)
	}
}
