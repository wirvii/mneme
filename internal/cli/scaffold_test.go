package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldCmd_Capture_MonorepoRoundTrip(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()

	// Exemplar turborepo-shaped repo.
	repo := t.TempDir()
	writeFixtureFile(t, repo, "turbo.json", `{"pipeline":{}}`)
	writeFixtureFile(t, repo, "package.json", `{"name":"acme"}`)
	writeFixtureFile(t, repo, "apps/web/index.ts", "// acme web")
	writeFixtureFile(t, repo, "packages/ui/b.ts", "// shared")

	profileDir := t.TempDir()
	out, _, err := execProfileCmd(t, dataDir, "scaffold", "capture", repo,
		"--name", "saas", "--into", profileDir)
	if err != nil {
		t.Fatalf("scaffold capture: %v", err)
	}
	if !strings.Contains(out, "Captured scaffold saas") || !strings.Contains(out, "monorepo/turborepo") {
		t.Errorf("output = %q, want mention of Captured scaffold saas (monorepo/turborepo)", out)
	}

	if _, err := os.Stat(filepath.Join(profileDir, "scaffolds", "saas", "scaffold.toml")); err != nil {
		t.Errorf("scaffold.toml not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(profileDir, "_blueprints", "web")); err != nil {
		t.Errorf("web blueprint not captured: %v", err)
	}
}

func TestScaffoldCmd_Capture_SingleLayout(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()

	repo := t.TempDir()
	writeFixtureFile(t, repo, "go.mod", "module github.com/acme/lib\n")
	writeFixtureFile(t, repo, "lib.go", "package lib")

	profileDir := t.TempDir()
	out, _, err := execProfileCmd(t, dataDir, "scaffold", "capture", repo,
		"--name", "library-go", "--into", profileDir)
	if err != nil {
		t.Fatalf("scaffold capture: %v", err)
	}
	// layoutToolchain renders a single layout with no toolchain suffix.
	if !strings.Contains(out, "Captured scaffold library-go (single)") {
		t.Errorf("output = %q, want 'Captured scaffold library-go (single)'", out)
	}
	if _, err := os.Stat(filepath.Join(profileDir, "scaffolds", "library-go", "skeleton", "lib.go")); err != nil {
		t.Errorf("skeleton not captured: %v", err)
	}
}

func TestScaffoldCmd_Capture_NothingToCapture(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()
	empty := t.TempDir()
	_, _, err := execProfileCmd(t, dataDir, "scaffold", "capture", empty, "--name", "x", "--into", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "nothing to capture") {
		t.Fatalf("err = %v, want 'nothing to capture'", err)
	}
}
