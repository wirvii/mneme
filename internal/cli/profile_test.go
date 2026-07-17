package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mustRunGitForProfileCmd runs git with args in dir, failing the test on error.
func mustRunGitForProfileCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newProfileCmdFixtureRepo creates a local git repository (entirely inside
// t.TempDir(), no network) with a valid mneme-profile.toml, tagged "v1".
func newProfileCmdFixtureRepo(t *testing.T, name, version string) string {
	t.Helper()
	dir := t.TempDir()

	mustRunGitForProfileCmd(t, dir, "init", "-q")
	mustRunGitForProfileCmd(t, dir, "config", "user.name", "mneme-test")
	mustRunGitForProfileCmd(t, dir, "config", "user.email", "mneme-test@example.com")

	manifest := "name = \"" + name + "\"\nversion = \"" + version + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "mneme-profile.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	mustRunGitForProfileCmd(t, dir, "add", ".")
	mustRunGitForProfileCmd(t, dir, "commit", "-q", "-m", "initial commit")
	mustRunGitForProfileCmd(t, dir, "tag", "v1")

	return dir
}

// isolateProfileCmdCwd chdirs the test process into a fresh, non-git temp
// directory and restores the original cwd on cleanup (SPEC-085 §5.3 note 3:
// a CLI-level test driving a real cobra command must isolate cwd, not just
// --data-dir, because ResolvePin resolves the pin relative to the real
// process cwd).
func isolateProfileCmdCwd(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	return dir
}

// execProfileCmd executes "mneme <argv...>" against an isolated --data-dir
// so tests never touch the real ~/.mneme instance, and returns stdout/stderr
// separately.
func execProfileCmd(t *testing.T, dataDir string, argv ...string) (stdout, stderr string, err error) {
	t.Helper()

	root := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	root.SetOut(outBuf)
	root.SetErr(errBuf)

	args := append([]string{"--data-dir", dataDir}, argv...)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestProfileCmd_AddListStatus_RoundTrip(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()
	source := newProfileCmdFixtureRepo(t, "chatea-pro", "1.0.0")

	out, _, err := execProfileCmd(t, dataDir, "profile", "add", source)
	if err != nil {
		t.Fatalf("profile add: unexpected error: %v", err)
	}
	if !strings.Contains(out, "chatea-pro") || !strings.Contains(out, "1.0.0") {
		t.Errorf("profile add output = %q, want mention of chatea-pro/1.0.0", out)
	}

	out, _, err = execProfileCmd(t, dataDir, "profile", "list")
	if err != nil {
		t.Fatalf("profile list: unexpected error: %v", err)
	}
	if !strings.Contains(out, "chatea-pro") {
		t.Errorf("profile list output = %q, want mention of chatea-pro", out)
	}

	// Write a pin in the isolated cwd pointing at the installed profile, then
	// check `profile status` resolves it as installed.
	projectRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	pin := "name   = \"chatea-pro\"\nsource = \"" + source + "\"\n"
	if err := os.WriteFile(filepath.Join(projectRoot, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}

	out, _, err = execProfileCmd(t, dataDir, "profile", "status")
	if err != nil {
		t.Fatalf("profile status: unexpected error: %v", err)
	}
	if !strings.Contains(out, "Installed") || !strings.Contains(out, "chatea-pro") {
		t.Errorf("profile status output = %q, want mention of Installed/chatea-pro", out)
	}
}

func TestProfileCmd_Status_Absent(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()

	out, _, err := execProfileCmd(t, dataDir, "profile", "status")
	if err != nil {
		t.Fatalf("profile status: unexpected error: %v", err)
	}
	if !strings.Contains(out, "No profile pin") {
		t.Errorf("profile status output = %q, want mention of absence", out)
	}
}

func TestProfileCmd_Add_AlreadyExists(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()
	source := newProfileCmdFixtureRepo(t, "chatea-pro", "1.0.0")

	if _, _, err := execProfileCmd(t, dataDir, "profile", "add", source); err != nil {
		t.Fatalf("first profile add: unexpected error: %v", err)
	}

	_, _, err := execProfileCmd(t, dataDir, "profile", "add", source)
	if err == nil {
		t.Fatal("second profile add: expected error")
	}
	if !strings.Contains(err.Error(), "already installed") {
		t.Errorf("err = %v, want mention of already installed", err)
	}
}

func TestProfileCmd_Update_UsesPinWhenNameOmitted(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()
	source := newProfileCmdFixtureRepo(t, "chatea-pro", "1.0.0")

	if _, _, err := execProfileCmd(t, dataDir, "profile", "add", source); err != nil {
		t.Fatalf("profile add: unexpected error: %v", err)
	}

	projectRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	pin := "name   = \"chatea-pro\"\nsource = \"" + source + "\"\n"
	if err := os.WriteFile(filepath.Join(projectRoot, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}

	out, _, err := execProfileCmd(t, dataDir, "profile", "update")
	if err != nil {
		t.Fatalf("profile update: unexpected error: %v", err)
	}
	if !strings.Contains(out, "chatea-pro") {
		t.Errorf("profile update output = %q, want mention of chatea-pro", out)
	}
}

func TestProfileCmd_Update_NoPinNoName(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()

	_, _, err := execProfileCmd(t, dataDir, "profile", "update")
	if err == nil {
		t.Fatal("profile update: expected error when there is no pin and no name")
	}
	if !strings.Contains(err.Error(), "profile update") {
		t.Errorf("err = %v, want mention of the failing command", err)
	}
}

func TestProfileCmd_List_Empty(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()

	out, _, err := execProfileCmd(t, dataDir, "profile", "list")
	if err != nil {
		t.Fatalf("profile list: unexpected error: %v", err)
	}
	if !strings.Contains(out, "No profiles installed") {
		t.Errorf("profile list output = %q, want mention of empty store", out)
	}
}

func TestProfileCmd_Help(t *testing.T) {
	cmd := newProfileCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, sub := range []string{"add", "update", "list", "status"} {
		if !strings.Contains(out, sub) {
			t.Errorf("help output missing subcommand %q", sub)
		}
	}
}
