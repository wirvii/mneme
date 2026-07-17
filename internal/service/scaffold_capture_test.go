package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
)

// writeFile writes content to path, creating parent directories — a test
// helper for building exemplar-repo and profile-repo fixtures under t.TempDir()
// (SPEC-085 isolation: no real HOME, no git, no network).
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newMonorepoExemplar builds a turborepo-shaped exemplar repo under a temp dir
// and returns its path.
func newMonorepoExemplar(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "turbo.json"), `{"pipeline":{}}`)
	writeFile(t, filepath.Join(repo, "pnpm-workspace.yaml"), "packages:\n  - \"apps/*\"\n")
	writeFile(t, filepath.Join(repo, "package.json"), `{"name":"acme-mono"}`)
	writeFile(t, filepath.Join(repo, ".DS_Store"), "junk")
	writeFile(t, filepath.Join(repo, "apps", "web", "index.ts"), "// acme-mono web")
	writeFile(t, filepath.Join(repo, "apps", "api", "main.go"), "package main // acme-mono")
	writeFile(t, filepath.Join(repo, "packages", "ui", "button.ts"), "// shared")
	// Noise that must never be copied:
	writeFile(t, filepath.Join(repo, ".git", "config"), "[core]")
	writeFile(t, filepath.Join(repo, "node_modules", "dep", "index.js"), "module.exports={}")
	return repo
}

func TestCaptureScaffold_MonorepoTurborepo(t *testing.T) {
	repo := newMonorepoExemplar(t)
	profileDir := t.TempDir()

	svc := NewProfileService(t.TempDir(), false)
	res, err := svc.CaptureScaffold(ScaffoldCaptureInput{Repo: repo, Name: "saas", Into: profileDir})
	if err != nil {
		t.Fatalf("CaptureScaffold: %v", err)
	}

	if res.Layout != "monorepo" || res.Toolchain != "turborepo" {
		t.Errorf("layout/toolchain = %q/%q, want monorepo/turborepo", res.Layout, res.Toolchain)
	}

	// scaffold.toml written and ParseScaffold-valid (AC12).
	tomlData, err := os.ReadFile(res.ScaffoldTOMLPath)
	if err != nil {
		t.Fatalf("read scaffold.toml: %v", err)
	}
	if _, err := profile.ParseScaffold(tomlData); err != nil {
		t.Errorf("captured scaffold.toml invalid: %v\n%s", err, tomlData)
	}

	// Blueprints copied to _blueprints/<app>.
	for _, app := range []string{"web", "api"} {
		if _, err := os.Stat(filepath.Join(profileDir, "_blueprints", app)); err != nil {
			t.Errorf("blueprint %s not captured: %v", app, err)
		}
	}

	// Shell captured (root files), packages -> overlay, but NOT .git / node_modules / .DS_Store.
	if _, err := os.Stat(filepath.Join(profileDir, "scaffolds", "saas", "shell", "turbo.json")); err != nil {
		t.Errorf("shell/turbo.json not captured: %v", err)
	}
	if _, err := os.Stat(filepath.Join(profileDir, "scaffolds", "saas", "overlay", "packages", "ui", "button.ts")); err != nil {
		t.Errorf("overlay/packages not captured: %v", err)
	}
	if _, err := os.Stat(filepath.Join(profileDir, "scaffolds", "saas", "shell", ".DS_Store")); !os.IsNotExist(err) {
		t.Error(".DS_Store must be excluded from the shell")
	}
	// Nothing named node_modules / .git anywhere in the captured tree.
	_ = filepath.Walk(profileDir, func(p string, info os.FileInfo, _ error) error {
		if info != nil && info.IsDir() && (info.Name() == "node_modules" || info.Name() == ".git") {
			t.Errorf("excluded dir leaked into capture: %s", p)
		}
		return nil
	})

	// Parametrization: the exemplar project name is rewritten to the placeholder.
	webData, err := os.ReadFile(filepath.Join(profileDir, "_blueprints", "web", "index.ts"))
	if err != nil {
		t.Fatalf("read captured web blueprint: %v", err)
	}
	if got := string(webData); got != "// {{PROJECT_NAME}} web" {
		t.Errorf("blueprint content not parametrized: %q", got)
	}
}

func TestCaptureScaffold_SingleWithModulePath(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module github.com/acme/acme-lib\n\ngo 1.25\n")
	writeFile(t, filepath.Join(repo, "lib.go"), "package lib // github.com/acme/acme-lib")
	writeFile(t, filepath.Join(repo, "README.md"), "# acme-lib")
	profileDir := t.TempDir()

	svc := NewProfileService(t.TempDir(), false)
	res, err := svc.CaptureScaffold(ScaffoldCaptureInput{Repo: repo, Name: "library-go", Into: profileDir})
	if err != nil {
		t.Fatalf("CaptureScaffold: %v", err)
	}
	if res.Layout != "single" || res.Toolchain != "" {
		t.Errorf("layout/toolchain = %q/%q, want single/(empty)", res.Layout, res.Toolchain)
	}

	// Whole repo captured into skeleton/, with the module path parametrized.
	libData, err := os.ReadFile(filepath.Join(profileDir, "scaffolds", "library-go", "skeleton", "lib.go"))
	if err != nil {
		t.Fatalf("read captured skeleton lib.go: %v", err)
	}
	if got := string(libData); got != "package lib // {{MODULE_PATH}}" {
		t.Errorf("skeleton not parametrized with module path: %q", got)
	}

	wantVars := []string{"MODULE_PATH", "PROJECT_NAME"}
	if len(res.Vars) != len(wantVars) || res.Vars[0] != wantVars[0] || res.Vars[1] != wantVars[1] {
		t.Errorf("vars = %v, want %v", res.Vars, wantVars)
	}
}

func TestCaptureScaffold_NameDerivedFromBasename(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "My-Cool-Repo")
	writeFile(t, filepath.Join(repo, "go.mod"), "module x\n")
	profileDir := t.TempDir()

	svc := NewProfileService(t.TempDir(), false)
	res, err := svc.CaptureScaffold(ScaffoldCaptureInput{Repo: repo, Into: profileDir})
	if err != nil {
		t.Fatalf("CaptureScaffold: %v", err)
	}
	if res.Scaffold != "my-cool-repo" {
		t.Errorf("derived name = %q, want my-cool-repo", res.Scaffold)
	}
}

func TestCaptureScaffold_BlueprintCollision(t *testing.T) {
	repo := newMonorepoExemplar(t)
	profileDir := t.TempDir()
	// A pre-existing, non-empty _blueprints/web must not be clobbered — the
	// copy loop refuses it with ErrProfileExists.
	writeFile(t, filepath.Join(profileDir, "_blueprints", "web", "existing.ts"), "// keep me")

	svc := NewProfileService(t.TempDir(), false)
	_, err := svc.CaptureScaffold(ScaffoldCaptureInput{Repo: repo, Name: "saas", Into: profileDir})
	if !errors.Is(err, model.ErrProfileExists) {
		t.Fatalf("blueprint collision: got %v, want ErrProfileExists", err)
	}
	// The curated existing blueprint is left intact.
	data, rerr := os.ReadFile(filepath.Join(profileDir, "_blueprints", "web", "existing.ts"))
	if rerr != nil || string(data) != "// keep me" {
		t.Errorf("existing blueprint was clobbered: %q err=%v", data, rerr)
	}
}

func TestCaptureScaffold_MalformedPackageJSON(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "package.json"), "{ not json")
	writeFile(t, filepath.Join(repo, "pnpm-workspace.yaml"), "packages: []\n")
	profileDir := t.TempDir()

	svc := NewProfileService(t.TempDir(), false)
	// Malformed package.json falls back to the directory basename — never errors.
	res, err := svc.CaptureScaffold(ScaffoldCaptureInput{Repo: repo, Name: "mono", Into: profileDir})
	if err != nil {
		t.Fatalf("CaptureScaffold: %v", err)
	}
	if res.Layout != "monorepo" || res.Toolchain != "custom" {
		t.Errorf("layout/toolchain = %q/%q, want monorepo/custom", res.Layout, res.Toolchain)
	}
}

func TestCaptureScaffold_IntoDefaultsToCwd(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module github.com/acme/lib\n")

	profileDir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(profileDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	svc := NewProfileService(t.TempDir(), false)
	res, err := svc.CaptureScaffold(ScaffoldCaptureInput{Repo: repo, Name: "lib"})
	if err != nil {
		t.Fatalf("CaptureScaffold: %v", err)
	}
	if res.ProfileDir != profileDir {
		// macOS temp dirs can be symlinked (/var -> /private/var); compare via Stat.
		gotInfo, _ := os.Stat(res.ProfileDir)
		wantInfo, _ := os.Stat(profileDir)
		if !os.SameFile(gotInfo, wantInfo) {
			t.Errorf("ProfileDir = %q, want cwd %q", res.ProfileDir, profileDir)
		}
	}
	if _, err := os.Stat(filepath.Join(profileDir, "scaffolds", "lib", "scaffold.toml")); err != nil {
		t.Errorf("scaffold.toml not written under cwd: %v", err)
	}
}

func TestCaptureScaffold_Errors(t *testing.T) {
	svc := NewProfileService(t.TempDir(), false)

	// Nothing to capture (empty exemplar).
	empty := t.TempDir()
	if _, err := svc.CaptureScaffold(ScaffoldCaptureInput{Repo: empty, Name: "x", Into: t.TempDir()}); !errors.Is(err, model.ErrNothingToCapture) {
		t.Errorf("empty repo: got %v, want ErrNothingToCapture", err)
	}

	// Missing exemplar repo.
	if _, err := svc.CaptureScaffold(ScaffoldCaptureInput{Repo: filepath.Join(t.TempDir(), "nope"), Into: t.TempDir()}); err == nil {
		t.Error("missing exemplar: want error")
	}

	// Profile dir must exist.
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module x\n")
	if _, err := svc.CaptureScaffold(ScaffoldCaptureInput{Repo: repo, Name: "x", Into: filepath.Join(t.TempDir(), "absent")}); !errors.Is(err, model.ErrProfileNotFound) {
		t.Errorf("absent profile dir: got %v, want ErrProfileNotFound", err)
	}

	// Re-capture into the same scaffold name is refused (no clobber).
	profileDir := t.TempDir()
	in := ScaffoldCaptureInput{Repo: repo, Name: "dup", Into: profileDir}
	if _, err := svc.CaptureScaffold(in); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	if _, err := svc.CaptureScaffold(in); !errors.Is(err, model.ErrProfileExists) {
		t.Errorf("re-capture: got %v, want ErrProfileExists", err)
	}
}
