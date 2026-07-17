package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newMonorepoProfileFixtureRepo builds a local git profile repo (no network)
// carrying one monorepo/turborepo scaffold plus a composable blueprint, tagged
// v1 — the source an `app add` CLI test installs.
func newMonorepoProfileFixtureRepo(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()

	mustRunGitForProfileCmd(t, dir, "init", "-q")
	mustRunGitForProfileCmd(t, dir, "config", "user.name", "mneme-test")
	mustRunGitForProfileCmd(t, dir, "config", "user.email", "mneme-test@example.com")

	writeFixtureFile(t, dir, "mneme-profile.toml", "name = \""+name+"\"\nversion = \"1.0.0\"\n")
	writeFixtureFile(t, dir, "scaffolds/saas/scaffold.toml",
		"layout = \"monorepo\"\ntoolchain = \"turborepo\"\nbootstrap = \"create-turbo@2.3.1\"\nblueprints = [\"go-core-srv\"]\n")
	writeFixtureFile(t, dir, "_blueprints/go-core-srv/main.go", "package main\n")

	mustRunGitForProfileCmd(t, dir, "add", ".")
	mustRunGitForProfileCmd(t, dir, "commit", "-q", "-m", "initial")
	mustRunGitForProfileCmd(t, dir, "tag", "v1")
	return dir
}

func TestAppCmd_Add_RoundTrip(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()
	source := newMonorepoProfileFixtureRepo(t, "saas-profile")

	if _, _, err := execProfileCmd(t, dataDir, "profile", "add", source); err != nil {
		t.Fatalf("profile add: %v", err)
	}

	// A monorepo root pinned to the installed profile, recording scaffold=saas.
	monorepo := t.TempDir()
	pin := "name = \"saas-profile\"\nsource = \"" + source + "\"\nscaffold = \"saas\"\n"
	if err := os.WriteFile(filepath.Join(monorepo, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(monorepo, "pnpm-workspace.yaml"), []byte("packages:\n  - \"apps/*\"\n"), 0o644); err != nil {
		t.Fatalf("write workspace: %v", err)
	}

	out, _, err := execProfileCmd(t, dataDir, "app", "add", "go-core-srv", "--name", "billing", "--dir", monorepo)
	if err != nil {
		t.Fatalf("app add: %v", err)
	}
	if !strings.Contains(out, "Added app billing") {
		t.Errorf("output = %q, want mention of Added app billing", out)
	}
	if _, err := os.Stat(filepath.Join(monorepo, "apps", "billing", "main.go")); err != nil {
		t.Errorf("app not created: %v", err)
	}
	// No git init on an existing monorepo (the temp dir was not a git repo).
	if _, err := os.Stat(filepath.Join(monorepo, ".git")); !os.IsNotExist(err) {
		t.Error("app add must not create .git")
	}
}

func TestAppCmd_Add_NameRequired(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()
	_, _, err := execProfileCmd(t, dataDir, "app", "add", "go-core-srv")
	if err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Fatalf("want --name is required, got %v", err)
	}
}

func TestAppCmd_Add_BadVar(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()
	_, _, err := execProfileCmd(t, dataDir, "app", "add", "go-core-srv", "--name", "x", "--var", "broken")
	if err == nil || !strings.Contains(err.Error(), "expected key=value") {
		t.Fatalf("want key=value error, got %v", err)
	}
}
