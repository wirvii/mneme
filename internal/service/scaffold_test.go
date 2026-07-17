package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
)

// fakeBootstrapper materializes a fixture tree with ZERO network — the
// service-level guard that `go test ./...` never touches the network (SPEC-098
// §7a §4.4). It records whether Run was invoked so a single-layout test can
// assert the bootstrap seam is NOT called for a scaffold without a bootstrap.
type fakeBootstrapper struct {
	tree   fstest.MapFS
	called bool
}

func (f *fakeBootstrapper) Run(_ context.Context, step profile.BootstrapStep) error {
	f.called = true
	for name, file := range f.tree {
		target := filepath.Join(step.Dest, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, file.Data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// singleProfileFS is an embedded-default-shaped profile FS carrying one
// single-layout scaffold whose skeleton references a substitution variable.
func singleProfileFS() fstest.MapFS {
	return fstest.MapFS{
		"mneme-profile.toml":                       {Data: []byte("name = \"fake\"\nversion = \"0.1.0\"\n")},
		"scaffolds/library-go/scaffold.toml":       {Data: []byte("layout = \"single\"\n[vars]\nmodule_path = { prompt = \"Go module path\", default = \"github.com/acme/lib\" }\n")},
		"scaffolds/library-go/skeleton/go.mod":     {Data: []byte("module {{module_path}}\n\ngo 1.25\n")},
		"scaffolds/library-go/skeleton/README.md":  {Data: []byte("# {{module_path}}\n")},
		"scaffolds/library-go/skeleton/pkg/lib.go": {Data: []byte("package lib\n")},
	}
}

// newScaffoldTestService builds a ProfileService wired for NewProject against
// an injected default profile FS (so no store checkout / git clone is needed),
// with a fake bootstrapper and an isolated config path. It resolves the active
// profile from projectRoot — pass an isolated non-git temp dir so resolution
// lands on vanilla/default (SPEC-085 §3).
func newScaffoldTestService(t *testing.T, profileFS fstest.MapFS, boot Bootstrapper) *ProfileService {
	t.Helper()
	dataDir := t.TempDir()
	cfgPath := filepath.Join(dataDir, "config.toml")

	return NewProfileService(
		filepath.Join(dataDir, "profiles"), false,
		WithProfileConfigPath(cfgPath),
		WithDefaultProfileFS(profileFS),
		WithProfileBootstrapper(boot),
	)
}

func TestNewProject_Single(t *testing.T) {
	boot := &fakeBootstrapper{}
	svc := newScaffoldTestService(t, singleProfileFS(), boot)

	// projectRoot: an isolated non-git temp dir → active profile resolves to
	// the injected default (SPEC-085 §3).
	projectRoot := t.TempDir()
	dest := filepath.Join(t.TempDir(), "new-lib")

	res, err := svc.NewProject(context.Background(), ProjectNewInput{
		Scaffold:    "library-go",
		Dir:         dest,
		Vars:        map[string]string{"module_path": "github.com/wirvii/newlib"},
		ProjectRoot: projectRoot,
	})
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}

	if res.Layout != "single" {
		t.Errorf("Layout = %q, want single", res.Layout)
	}
	if res.Profile != profile.DefaultProfileName {
		t.Errorf("Profile = %q, want %q", res.Profile, profile.DefaultProfileName)
	}

	// The skeleton was copied with substitution applied.
	gomod, err := os.ReadFile(filepath.Join(dest, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if got := string(gomod); got != "module github.com/wirvii/newlib\n\ngo 1.25\n" {
		t.Errorf("go.mod substitution wrong:\n%q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "pkg", "lib.go")); err != nil {
		t.Errorf("nested file not copied: %v", err)
	}

	// git init ran (no commit).
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Errorf("git init did not run: %v", err)
	}

	// The pin was written with scaffold recorded.
	pin, err := profile.ParsePinFile(filepath.Join(dest, profile.PinFileName))
	if err != nil {
		t.Fatalf("parse written pin: %v", err)
	}
	if pin.Scaffold != "library-go" {
		t.Errorf("pin.Scaffold = %q, want library-go", pin.Scaffold)
	}
	if pin.Name != profile.DefaultProfileName {
		t.Errorf("pin.Name = %q, want %q", pin.Name, profile.DefaultProfileName)
	}

	// A single-layout scaffold has no bootstrap → the bootstrapper is never
	// invoked (network guard).
	if boot.called {
		t.Error("bootstrapper was invoked for a single-layout scaffold (must not be)")
	}
}

func TestNewProject_DefaultValueSubstitution(t *testing.T) {
	svc := newScaffoldTestService(t, singleProfileFS(), &fakeBootstrapper{})
	dest := filepath.Join(t.TempDir(), "lib")

	// No --var supplied → the scaffold's declared default is substituted.
	if _, err := svc.NewProject(context.Background(), ProjectNewInput{
		Scaffold:    "library-go",
		Dir:         dest,
		ProjectRoot: t.TempDir(),
	}); err != nil {
		t.Fatalf("NewProject: %v", err)
	}

	gomod, _ := os.ReadFile(filepath.Join(dest, "go.mod"))
	if got := string(gomod); got != "module github.com/acme/lib\n\ngo 1.25\n" {
		t.Errorf("default substitution wrong:\n%q", got)
	}
}

func TestNewProject_ScaffoldNotFound(t *testing.T) {
	svc := newScaffoldTestService(t, singleProfileFS(), &fakeBootstrapper{})
	_, err := svc.NewProject(context.Background(), ProjectNewInput{
		Scaffold:    "does-not-exist",
		Dir:         filepath.Join(t.TempDir(), "x"),
		ProjectRoot: t.TempDir(),
	})
	if !errors.Is(err, model.ErrScaffoldNotFound) {
		t.Fatalf("want model.ErrScaffoldNotFound, got %v", err)
	}
}

func TestNewProject_DestNotEmpty(t *testing.T) {
	svc := newScaffoldTestService(t, singleProfileFS(), &fakeBootstrapper{})
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "existing"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.NewProject(context.Background(), ProjectNewInput{
		Scaffold:    "library-go",
		Dir:         dest,
		ProjectRoot: t.TempDir(),
	})
	if !errors.Is(err, model.ErrProfileExists) {
		t.Fatalf("want model.ErrProfileExists, got %v", err)
	}
}

func TestNewProject_EmptyScaffoldName(t *testing.T) {
	svc := newScaffoldTestService(t, singleProfileFS(), &fakeBootstrapper{})
	_, err := svc.NewProject(context.Background(), ProjectNewInput{Dir: "/tmp/x", ProjectRoot: t.TempDir()})
	if err == nil {
		t.Fatal("want error for empty scaffold name")
	}
}

func TestNewProject_NoScaffoldsInVanillaDefault(t *testing.T) {
	// A profile with NO scaffolds/ (like the real embedded OSS default) → any
	// scaffold name is not found (clean degradation).
	empty := fstest.MapFS{"mneme-profile.toml": {Data: []byte("name = \"d\"\nversion = \"0.1.0\"\n")}}
	svc := newScaffoldTestService(t, empty, &fakeBootstrapper{})
	_, err := svc.NewProject(context.Background(), ProjectNewInput{
		Scaffold:    "anything",
		Dir:         filepath.Join(t.TempDir(), "x"),
		ProjectRoot: t.TempDir(),
	})
	if !errors.Is(err, model.ErrScaffoldNotFound) {
		t.Fatalf("want model.ErrScaffoldNotFound, got %v", err)
	}
}

// TestExecutePlan_BootstrapSeam exercises the bootstrap execution path in
// isolation with a fakeBootstrapper (zero network): a plan carrying a
// BootstrapStep runs the injected bootstrapper (materializing a fixture tree),
// then the overlay copy applies on top, then git init. This proves the seam
// §7b's monorepo planner will drive, deterministically and offline.
func TestExecutePlan_BootstrapSeam(t *testing.T) {
	boot := &fakeBootstrapper{tree: fstest.MapFS{
		"package.json": {Data: []byte("{\"name\":\"x\"}\n")},
	}}
	svc := NewProfileService(t.TempDir(), false, WithProfileBootstrapper(boot))

	profileFS := fstest.MapFS{
		"overlay/turbo.json": {Data: []byte("{\"$schema\":\"{{schema}}\"}\n")},
	}
	dest := filepath.Join(t.TempDir(), "mono")

	plan := profile.AssemblyPlan{
		Bootstrap: &profile.BootstrapStep{Generator: "create-turbo", Version: "2.3.1", Dest: dest},
		Copies: []profile.CopyStep{{
			Src:  "overlay",
			Dest: dest,
			Vars: map[string]string{"schema": "https://turbo.build/schema.json"},
		}},
		GitInit: true,
	}

	if err := svc.executePlan(context.Background(), plan, profileFS, dest); err != nil {
		t.Fatalf("executePlan: %v", err)
	}
	if !boot.called {
		t.Error("bootstrapper was not invoked for a bootstrap-bearing plan")
	}
	if _, err := os.Stat(filepath.Join(dest, "package.json")); err != nil {
		t.Errorf("bootstrap tree not materialized: %v", err)
	}
	turbo, _ := os.ReadFile(filepath.Join(dest, "turbo.json"))
	if got := string(turbo); got != "{\"$schema\":\"https://turbo.build/schema.json\"}\n" {
		t.Errorf("overlay substitution wrong:\n%q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Errorf("git init did not run: %v", err)
	}
}

func TestExecutePlan_BootstrapWithoutBootstrapper(t *testing.T) {
	svc := NewProfileService(t.TempDir(), false) // no bootstrapper wired
	plan := profile.AssemblyPlan{
		Bootstrap: &profile.BootstrapStep{Generator: "create-turbo", Version: "2.3.1", Dest: "/tmp/x"},
	}
	err := svc.executePlan(context.Background(), plan, fstest.MapFS{}, "/tmp/x")
	if !errors.Is(err, model.ErrProfileServiceNotConfigured) {
		t.Fatalf("want ErrProfileServiceNotConfigured, got %v", err)
	}
}

// TestExecBootstrapper_ToolMissing verifies the environment precondition — a
// runner absent from PATH yields an actionable ErrBootstrapToolMissing, never
// a panic. Simulated by emptying PATH (no network).
func TestExecBootstrapper_ToolMissing(t *testing.T) {
	t.Setenv("PATH", "")
	b := NewExecBootstrapper()
	err := b.Run(context.Background(), profile.BootstrapStep{Generator: "create-turbo", Version: "2.3.1", Dest: t.TempDir()})
	if !errors.Is(err, model.ErrBootstrapToolMissing) {
		t.Fatalf("want ErrBootstrapToolMissing, got %v", err)
	}
}

// newScaffoldFixtureRepoSvc builds a local git profile repo (no network)
// carrying one single-layout scaffold, tagged v1 — the source a store-backed
// NewProject test installs via ProfileService.Add.
func newScaffoldFixtureRepoSvc(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	mustGitScaffold(t, dir, "init", "-q")
	mustGitScaffold(t, dir, "config", "user.name", "mneme-test")
	mustGitScaffold(t, dir, "config", "user.email", "mneme-test@example.com")

	writeSvcFixtureFile(t, dir, "mneme-profile.toml", "name = \""+name+"\"\nversion = \"1.0.0\"\n")
	writeSvcFixtureFile(t, dir, "scaffolds/library-go/scaffold.toml",
		"layout = \"single\"\n[vars]\nmodule_path = { prompt = \"Go module\", default = \"github.com/acme/lib\" }\n")
	writeSvcFixtureFile(t, dir, "scaffolds/library-go/skeleton/go.mod", "module {{module_path}}\n")

	mustGitScaffold(t, dir, "add", ".")
	mustGitScaffold(t, dir, "commit", "-q", "-m", "initial")
	mustGitScaffold(t, dir, "tag", "v1")
	return dir
}

// mustGitScaffold runs git in dir, failing the test on error. Local to this
// internal (package service) test file — the analogous service_test helper is
// not visible from the internal test package.
func mustGitScaffold(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeSvcFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestNewProject_StoreBackedProfile exercises the PinInstalled path: the active
// profile is a real store checkout (installed via Add), so resolveActiveProfile
// reconstructs a reproducible pin via PinFromStore and reads the scaffold
// catalog from os.DirFS of the checkout. The fresh repo's pin records the
// profile's source + ref.
func TestNewProject_StoreBackedProfile(t *testing.T) {
	dataDir := t.TempDir()
	svc := NewProfileService(filepath.Join(dataDir, "profiles"), false,
		WithProfileConfigPath(filepath.Join(dataDir, "config.toml")),
		WithProfileBootstrapper(&fakeBootstrapper{}),
	)

	source := newScaffoldFixtureRepoSvc(t, "acme")
	if _, err := svc.Add(source, "", "v1", false); err != nil {
		t.Fatalf("Add: %v", err)
	}

	projectRoot := t.TempDir()
	pin := "name = \"acme\"\nsource = \"" + source + "\"\n"
	if err := os.WriteFile(filepath.Join(projectRoot, profile.PinFileName), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "newrepo")
	res, err := svc.NewProject(context.Background(), ProjectNewInput{
		Scaffold:    "library-go",
		Dir:         dest,
		Vars:        map[string]string{"module_path": "github.com/wirvii/y"},
		ProjectRoot: projectRoot,
	})
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	if res.Profile != "acme" {
		t.Errorf("Profile = %q, want acme", res.Profile)
	}

	written, err := profile.ParsePinFile(filepath.Join(dest, profile.PinFileName))
	if err != nil {
		t.Fatalf("parse written pin: %v", err)
	}
	if written.Name != "acme" || written.Scaffold != "library-go" {
		t.Errorf("written pin = %+v, want name=acme scaffold=library-go", written)
	}
	if written.Source == "" || written.Ref == "" {
		t.Errorf("written pin should carry a reproducible source+ref, got %+v", written)
	}
	gomod, _ := os.ReadFile(filepath.Join(dest, "go.mod"))
	if string(gomod) != "module github.com/wirvii/y\n" {
		t.Errorf("go.mod = %q", string(gomod))
	}
}

// TestNewProject_PinDefault covers the PinDefault branch: a sourceless pin in
// the project root resolves to the embedded default profile FS (here injected
// with scaffolds), and the fresh repo's pin carries that name, sourceless.
func TestNewProject_PinDefault(t *testing.T) {
	svc := newScaffoldTestService(t, singleProfileFS(), &fakeBootstrapper{})

	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, profile.PinFileName), []byte("name = \"mydefault\"\n"), 0o644); err != nil {
		t.Fatalf("write sourceless pin: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "lib")
	res, err := svc.NewProject(context.Background(), ProjectNewInput{
		Scaffold:    "library-go",
		Dir:         dest,
		ProjectRoot: projectRoot,
	})
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	if res.Profile != "mydefault" {
		t.Errorf("Profile = %q, want mydefault (the sourceless pin's name)", res.Profile)
	}
	written, _ := profile.ParsePinFile(filepath.Join(dest, profile.PinFileName))
	if !written.IsDefault() || written.Scaffold != "library-go" {
		t.Errorf("written pin = %+v, want sourceless + scaffold=library-go", written)
	}
}

// TestNewProject_PinMissing covers the PinMissing branch: a pin naming a
// profile absent from the store is a hard, actionable error.
func TestNewProject_PinMissing(t *testing.T) {
	dataDir := t.TempDir()
	svc := NewProfileService(filepath.Join(dataDir, "profiles"), false,
		WithProfileConfigPath(filepath.Join(dataDir, "config.toml")),
		WithProfileBootstrapper(&fakeBootstrapper{}),
	)

	projectRoot := t.TempDir()
	pin := "name = \"ghost\"\nsource = \"git@example.com:ghost.git\"\n"
	if err := os.WriteFile(filepath.Join(projectRoot, profile.PinFileName), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}

	_, err := svc.NewProject(context.Background(), ProjectNewInput{
		Scaffold:    "library-go",
		Dir:         filepath.Join(t.TempDir(), "x"),
		ProjectRoot: projectRoot,
	})
	if !errors.Is(err, model.ErrProfileNotFound) {
		t.Fatalf("want model.ErrProfileNotFound for an uninstalled pinned profile, got %v", err)
	}
}

// TestNewProject_CwdFallback covers the ProjectRoot=="" branch (os.Getwd): a
// chdir into an isolated non-git temp dir resolves to the injected default.
func TestNewProject_CwdFallback(t *testing.T) {
	svc := newScaffoldTestService(t, singleProfileFS(), &fakeBootstrapper{})

	cwd := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	dest := filepath.Join(t.TempDir(), "lib")
	if _, err := svc.NewProject(context.Background(), ProjectNewInput{
		Scaffold: "library-go",
		Dir:      dest,
	}); err != nil {
		t.Fatalf("NewProject with cwd fallback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "go.mod")); err != nil {
		t.Errorf("expected go.mod: %v", err)
	}
}

// TestExecBootstrapper_RunnerSuccessAndFailure exercises execBootstrapper.Run's
// offline branches (arg building, exec success, exec failure) without any
// network by substituting a trivial runner: `true` always exits 0, `false`
// exits 1. This covers everything but the real generator invocation (which the
// gated network test owns). Skipped on Windows, which lacks true/false.
func TestExecBootstrapper_RunnerSuccessAndFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("true/false are POSIX shells absent on Windows")
	}
	step := profile.BootstrapStep{Generator: "create-turbo", Version: "2.3.1", Dest: t.TempDir()}

	if err := (&execBootstrapper{runner: "true"}).Run(context.Background(), step); err != nil {
		t.Errorf("runner=true: want success, got %v", err)
	}
	if err := (&execBootstrapper{runner: "false"}).Run(context.Background(), step); err == nil {
		t.Error("runner=false: want failure, got nil")
	}
}

func TestSubstituteVars_Empty(t *testing.T) {
	data := []byte("no {{vars}} here")
	if got := substituteVars(data, nil); string(got) != string(data) {
		t.Errorf("empty vars must return data unchanged, got %q", got)
	}
}

func TestNewProject_NoDefaultFS(t *testing.T) {
	dataDir := t.TempDir()
	// Vanilla project root, but no default profile FS wired → the default
	// activation cannot proceed.
	svc := NewProfileService(filepath.Join(dataDir, "profiles"), false,
		WithProfileConfigPath(filepath.Join(dataDir, "config.toml")),
	)
	_, err := svc.NewProject(context.Background(), ProjectNewInput{
		Scaffold:    "library-go",
		Dir:         filepath.Join(t.TempDir(), "x"),
		ProjectRoot: t.TempDir(),
	})
	if !errors.Is(err, model.ErrDefaultProfileUnavailable) {
		t.Fatalf("want ErrDefaultProfileUnavailable, got %v", err)
	}
}

// TestNewProject_RealBootstrap_Network is the ONLY test that runs the real
// execBootstrapper against a real generator over the network. It is gated by
// MNEME_E2E_NETWORK AND testing.Short() so it is SKIPPED by default — it never
// runs in `make test`/`go test ./...`/CI (SPEC-098 §7a §4.4, AC5).
func TestNewProject_RealBootstrap_Network(t *testing.T) {
	if testing.Short() || os.Getenv("MNEME_E2E_NETWORK") == "" {
		t.Skip("network e2e gated: set MNEME_E2E_NETWORK=1 and drop -short to run")
	}
	b := NewExecBootstrapper()
	dest := filepath.Join(t.TempDir(), "real")
	if err := b.Run(context.Background(), profile.BootstrapStep{Generator: "create-turbo", Version: "2.3.1", Dest: dest}); err != nil {
		t.Fatalf("real bootstrap: %v", err)
	}
}
