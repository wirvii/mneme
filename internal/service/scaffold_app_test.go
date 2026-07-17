package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
)

// monorepoProfileFS is an injected default-profile FS carrying one monorepo
// scaffold (turborepo) plus a composable blueprint under _blueprints/.
func monorepoProfileFS() fstest.MapFS {
	return fstest.MapFS{
		"mneme-profile.toml": {Data: []byte("name = \"fake\"\nversion = \"0.1.0\"\n")},
		"scaffolds/saas/scaffold.toml": {Data: []byte(
			"layout = \"monorepo\"\ntoolchain = \"turborepo\"\nbootstrap = \"create-turbo@2.3.1\"\nblueprints = [\"go-core-srv\"]\n[vars]\norg = { prompt = \"Org\", default = \"acme\" }\n")},
		"_blueprints/go-core-srv/package.json": {Data: []byte("{\"name\":\"{{org}}-core\"}\n")},
		"_blueprints/go-core-srv/main.go":      {Data: []byte("package main\n")},
	}
}

// customProfileFS carries a custom-toolchain monorepo scaffold declaring a full
// [wiring] block (workspace + json-merge + copy) and a fragment template.
func customProfileFS() fstest.MapFS {
	return fstest.MapFS{
		"mneme-profile.toml": {Data: []byte("name = \"fake\"\nversion = \"0.1.0\"\n")},
		"scaffolds/svc/scaffold.toml": {Data: []byte(
			"layout = \"monorepo\"\ntoolchain = \"custom\"\nblueprints = [\"go-svc\"]\n[wiring]\napps_dir = \"services/\"\non_add = [\"workspace:pnpm-workspace.yaml\", \"json-merge:turbo.json#pipeline\", \"copy:fragments\"]\n")},
		"scaffolds/svc/fragments/.env": {Data: []byte("SERVICE=on\n")},
		"_blueprints/go-svc/main.go":   {Data: []byte("package main\n")},
	}
}

// writeMonorepoRoot creates a monorepo root dir with a sourceless pin recording
// scaffold=<name> (so ResolvePin yields it and resolveActiveProfile lands on the
// injected default) plus optional workspace/turbo root files.
func writeMonorepoRoot(t *testing.T, scaffold string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	pin := "name = \"mydefault\"\nscaffold = \"" + scaffold + "\"\n"
	if err := os.WriteFile(filepath.Join(root, profile.PinFileName), []byte(pin), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestAddApp_Turborepo_GlobNoOp(t *testing.T) {
	svc := newScaffoldTestService(t, monorepoProfileFS(), &fakeBootstrapper{})
	ws := "packages:\n  - \"apps/*\"\n  - \"packages/*\"\n"
	root := writeMonorepoRoot(t, "saas", map[string]string{"pnpm-workspace.yaml": ws})

	res, err := svc.AddApp(context.Background(), AppAddInput{
		Blueprint: "go-core-srv",
		Name:      "billing",
		Dir:       root,
		Vars:      map[string]string{"org": "wirvii"},
	})
	if err != nil {
		t.Fatalf("AddApp: %v", err)
	}
	if res.App != "billing" || res.Scaffold != "saas" {
		t.Errorf("result = %+v", res)
	}

	// Blueprint copied with substitution under apps/billing.
	pkg, err := os.ReadFile(filepath.Join(root, "apps", "billing", "package.json"))
	if err != nil {
		t.Fatalf("read app package.json: %v", err)
	}
	if string(pkg) != "{\"name\":\"wirvii-core\"}\n" {
		t.Errorf("substitution wrong: %q", pkg)
	}

	// Workspace glob apps/* already covers apps/billing → file unchanged (no-op).
	after, _ := os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml"))
	if string(after) != ws {
		t.Errorf("pnpm-workspace.yaml changed on a covered glob:\n%q", after)
	}

	// app add never git-inits an existing monorepo.
	if _, err := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
		t.Error("app add must not create .git")
	}
}

func TestAddApp_Turborepo_InsertsWhenUncovered(t *testing.T) {
	svc := newScaffoldTestService(t, monorepoProfileFS(), &fakeBootstrapper{})
	ws := "packages:\n  - \"packages/*\"\n"
	root := writeMonorepoRoot(t, "saas", map[string]string{"pnpm-workspace.yaml": ws})

	if _, err := svc.AddApp(context.Background(), AppAddInput{Blueprint: "go-core-srv", Name: "billing", Dir: root}); err != nil {
		t.Fatalf("AddApp: %v", err)
	}

	after, _ := os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml"))
	if !strings.Contains(string(after), "apps/billing") {
		t.Errorf("expected apps/billing inserted, got:\n%q", after)
	}
	// packages/* still there, untouched.
	if !strings.Contains(string(after), "packages/*") {
		t.Errorf("existing entry lost:\n%q", after)
	}
}

func TestAddApp_CustomWiring(t *testing.T) {
	svc := newScaffoldTestService(t, customProfileFS(), &fakeBootstrapper{})
	ws := "packages:\n  - \"packages/*\"\n"
	turbo := "{\n  \"pipeline\": {\n    \"build\": {}\n  }\n}\n"
	root := writeMonorepoRoot(t, "svc", map[string]string{
		"pnpm-workspace.yaml": ws,
		"turbo.json":          turbo,
	})

	res, err := svc.AddApp(context.Background(), AppAddInput{Blueprint: "go-svc", Name: "orders", Dir: root})
	if err != nil {
		t.Fatalf("AddApp custom: %v", err)
	}

	// Blueprint under the custom services/ dir.
	if _, err := os.Stat(filepath.Join(root, "services", "orders", "main.go")); err != nil {
		t.Errorf("blueprint not copied to services/orders: %v", err)
	}
	// workspace insert.
	after, _ := os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml"))
	if !strings.Contains(string(after), "services/orders") {
		t.Errorf("workspace not wired:\n%q", after)
	}
	// json-merge: pipeline now has an "orders" member.
	turboAfter, _ := os.ReadFile(filepath.Join(root, "turbo.json"))
	if !strings.Contains(string(turboAfter), "\"orders\"") {
		t.Errorf("turbo.json pipeline not merged:\n%q", turboAfter)
	}
	// copy: the fragment landed in the app dir.
	if _, err := os.Stat(filepath.Join(root, "services", "orders", ".env")); err != nil {
		t.Errorf("copy fragment not applied: %v", err)
	}
	if len(res.Wired) == 0 {
		t.Error("expected wired files reported")
	}
}

func TestAddApp_SingleNotApplicable(t *testing.T) {
	single := fstest.MapFS{
		"mneme-profile.toml":            {Data: []byte("name = \"fake\"\nversion = \"0.1.0\"\n")},
		"scaffolds/lib/scaffold.toml":   {Data: []byte("layout = \"single\"\n")},
		"scaffolds/lib/skeleton/go.mod": {Data: []byte("module x\n")},
	}
	svc := newScaffoldTestService(t, single, &fakeBootstrapper{})
	root := writeMonorepoRoot(t, "lib", nil)

	_, err := svc.AddApp(context.Background(), AppAddInput{Blueprint: "x", Name: "y", Dir: root})
	if !errors.Is(err, model.ErrAppAddNotApplicable) {
		t.Fatalf("want ErrAppAddNotApplicable, got %v", err)
	}
}

func TestAddApp_NoScaffoldInPin(t *testing.T) {
	svc := newScaffoldTestService(t, monorepoProfileFS(), &fakeBootstrapper{})
	root := t.TempDir()
	// A pin with no scaffold field.
	if err := os.WriteFile(filepath.Join(root, profile.PinFileName), []byte("name = \"mydefault\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.AddApp(context.Background(), AppAddInput{Blueprint: "go-core-srv", Name: "billing", Dir: root})
	if !errors.Is(err, model.ErrScaffoldNotFound) {
		t.Fatalf("want ErrScaffoldNotFound when pin records no scaffold, got %v", err)
	}
}

func TestAddApp_ScaffoldOverride(t *testing.T) {
	svc := newScaffoldTestService(t, monorepoProfileFS(), &fakeBootstrapper{})
	root := t.TempDir()
	// Pin without scaffold; supply it via override.
	if err := os.WriteFile(filepath.Join(root, profile.PinFileName), []byte("name = \"mydefault\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddApp(context.Background(), AppAddInput{Blueprint: "go-core-srv", Name: "billing", Dir: root, Scaffold: "saas"}); err != nil {
		t.Fatalf("AddApp with override: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "apps", "billing", "main.go")); err != nil {
		t.Errorf("app not created via override: %v", err)
	}
}

func TestAddApp_CwdFallback(t *testing.T) {
	svc := newScaffoldTestService(t, monorepoProfileFS(), &fakeBootstrapper{})
	root := writeMonorepoRoot(t, "saas", map[string]string{"pnpm-workspace.yaml": "packages:\n  - \"apps/*\"\n"})

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// Dir omitted → cwd (the monorepo root) is used.
	if _, err := svc.AddApp(context.Background(), AppAddInput{Blueprint: "go-core-srv", Name: "billing"}); err != nil {
		t.Fatalf("AddApp cwd fallback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "apps", "billing", "main.go")); err != nil {
		t.Errorf("app not created via cwd fallback: %v", err)
	}
}

func TestAddApp_MissingArgs(t *testing.T) {
	svc := newScaffoldTestService(t, monorepoProfileFS(), &fakeBootstrapper{})
	if _, err := svc.AddApp(context.Background(), AppAddInput{Name: "x", Dir: t.TempDir()}); err == nil {
		t.Error("want error for empty blueprint")
	}
	if _, err := svc.AddApp(context.Background(), AppAddInput{Blueprint: "b", Dir: t.TempDir()}); err == nil {
		t.Error("want error for empty name")
	}
}

func TestAddApp_UnknownBlueprint(t *testing.T) {
	svc := newScaffoldTestService(t, monorepoProfileFS(), &fakeBootstrapper{})
	root := writeMonorepoRoot(t, "saas", nil)
	_, err := svc.AddApp(context.Background(), AppAddInput{Blueprint: "ghost", Name: "x", Dir: root})
	if !errors.Is(err, model.ErrScaffoldNotFound) {
		t.Fatalf("want ErrScaffoldNotFound for an undeclared blueprint, got %v", err)
	}
}

func TestAddApp_DestNotEmpty(t *testing.T) {
	svc := newScaffoldTestService(t, monorepoProfileFS(), &fakeBootstrapper{})
	root := writeMonorepoRoot(t, "saas", nil)
	appDir := filepath.Join(root, "apps", "billing")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "existing"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.AddApp(context.Background(), AppAddInput{Blueprint: "go-core-srv", Name: "billing", Dir: root})
	if !errors.Is(err, model.ErrProfileExists) {
		t.Fatalf("want ErrProfileExists for a non-empty app dir, got %v", err)
	}
}

func TestEnsureWorkspacePackage(t *testing.T) {
	t.Run("creates when absent", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "pnpm-workspace.yaml")
		if err := ensureWorkspacePackage(p, "apps/web"); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(p)
		if !strings.Contains(string(data), "apps/web") {
			t.Errorf("got %q", data)
		}
	})
	t.Run("no-op when glob covers", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "pnpm-workspace.yaml")
		orig := "packages:\n  - \"apps/*\"\n"
		os.WriteFile(p, []byte(orig), 0o644)
		if err := ensureWorkspacePackage(p, "apps/web"); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(p)
		if string(data) != orig {
			t.Errorf("changed on covered glob: %q", data)
		}
	})
	t.Run("inserts when uncovered", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "pnpm-workspace.yaml")
		os.WriteFile(p, []byte("packages:\n  - \"packages/*\"\n"), 0o644)
		if err := ensureWorkspacePackage(p, "apps/web"); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(p)
		if !strings.Contains(string(data), "apps/web") || !strings.Contains(string(data), "packages/*") {
			t.Errorf("got %q", data)
		}
	})
	t.Run("appends packages block when missing", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "pnpm-workspace.yaml")
		os.WriteFile(p, []byte("# a comment\n"), 0o644)
		if err := ensureWorkspacePackage(p, "apps/web"); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(p)
		if !strings.Contains(string(data), "packages:") || !strings.Contains(string(data), "apps/web") {
			t.Errorf("got %q", data)
		}
	})
	t.Run("no-op when exact match present", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "pnpm-workspace.yaml")
		orig := "packages:\n  - \"apps/web\"\n"
		os.WriteFile(p, []byte(orig), 0o644)
		if err := ensureWorkspacePackage(p, "apps/web"); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(p)
		if string(data) != orig {
			t.Errorf("changed on exact match: %q", data)
		}
	})
}

func TestMergeJSONMember(t *testing.T) {
	t.Run("creates when absent", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "turbo.json")
		if err := mergeJSONMember(p, "pipeline", "orders"); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(p)
		if !strings.Contains(string(data), "pipeline") || !strings.Contains(string(data), "orders") {
			t.Errorf("got %q", data)
		}
	})
	t.Run("merges into existing node", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "turbo.json")
		os.WriteFile(p, []byte("{\"pipeline\":{\"build\":{}}}"), 0o644)
		if err := mergeJSONMember(p, "pipeline", "orders"); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(p)
		if !strings.Contains(string(data), "build") || !strings.Contains(string(data), "orders") {
			t.Errorf("lost sibling or missing new: %q", data)
		}
	})
	t.Run("root-level insert with empty path", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "x.json")
		os.WriteFile(p, []byte("{\"a\":1}"), 0o644)
		if err := mergeJSONMember(p, "", "b"); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(p)
		if !strings.Contains(string(data), "\"b\"") {
			t.Errorf("got %q", data)
		}
	})
	t.Run("rejects malformed json", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "bad.json")
		os.WriteFile(p, []byte("{not json"), 0o644)
		if err := mergeJSONMember(p, "pipeline", "x"); err == nil {
			t.Error("want error for malformed json")
		}
	})
}
