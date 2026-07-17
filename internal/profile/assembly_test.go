package profile

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanNewProject_Single(t *testing.T) {
	s := ScaffoldDef{Name: "library-go", Layout: LayoutSingle}
	vars := map[string]string{"module_path": "github.com/wirvii/x"}

	got, err := PlanNewProject(s, ProjectChoices{Dest: "/tmp/newrepo", Vars: vars})
	if err != nil {
		t.Fatalf("PlanNewProject: %v", err)
	}

	want := AssemblyPlan{
		Bootstrap: nil,
		Copies: []CopyStep{{
			Src:  "scaffolds/library-go/skeleton",
			Dest: "/tmp/newrepo",
			Vars: vars,
		}},
		GitInit: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %+v, want %+v", got, want)
	}
}

func TestPlanNewProject_Deterministic(t *testing.T) {
	s := ScaffoldDef{Name: "lib", Layout: LayoutSingle}
	choices := ProjectChoices{Dest: "/tmp/x", Vars: map[string]string{"a": "b"}}

	p1, err1 := PlanNewProject(s, choices)
	p2, err2 := PlanNewProject(s, choices)
	if err1 != nil || err2 != nil {
		t.Fatalf("errs: %v %v", err1, err2)
	}
	if !reflect.DeepEqual(p1, p2) {
		t.Errorf("plan not deterministic:\n%+v\n%+v", p1, p2)
	}
}

func TestPlanNewProject_MonorepoTurborepo(t *testing.T) {
	s := ScaffoldDef{
		Name:       "saas",
		Layout:     LayoutMonorepo,
		Toolchain:  ToolchainTurborepo,
		Bootstrap:  "create-turbo@2.3.1",
		Blueprints: []string{"go-core-srv", "next-web-ui"},
	}
	vars := map[string]string{"org": "acme"}

	got, err := PlanNewProject(s, ProjectChoices{
		Dest:       "/tmp/mono",
		Vars:       vars,
		Blueprints: []string{"go-core-srv"},
	})
	if err != nil {
		t.Fatalf("PlanNewProject monorepo: %v", err)
	}

	if got.Bootstrap == nil {
		t.Fatal("expected a BootstrapStep for a turborepo scaffold")
	}
	if got.Bootstrap.Generator != "create-turbo" || got.Bootstrap.Version != "2.3.1" || got.Bootstrap.Dest != "/tmp/mono" {
		t.Errorf("Bootstrap = %+v", *got.Bootstrap)
	}
	// overlay copy is present and Optional.
	if len(got.Copies) != 1 || got.Copies[0].Src != "scaffolds/saas/overlay" || !got.Copies[0].Optional {
		t.Errorf("Copies = %+v, want one optional overlay copy", got.Copies)
	}
	// one blueprint dropped under apps/<name> (name defaults to blueprint).
	if len(got.Blueprints) != 1 || got.Blueprints[0].Src != "_blueprints/go-core-srv" {
		t.Errorf("Blueprints = %+v", got.Blueprints)
	}
	wantDest := filepath.Join("/tmp/mono", "apps", "go-core-srv")
	if got.Blueprints[0].Dest != wantDest {
		t.Errorf("blueprint dest = %q, want %q", got.Blueprints[0].Dest, wantDest)
	}
	// turborepo built-in wiring: one workspace edit, no turbo.json edit.
	if len(got.Wiring) != 1 || got.Wiring[0].Kind != WiringEditWorkspace || got.Wiring[0].File != "pnpm-workspace.yaml" {
		t.Errorf("Wiring = %+v, want one workspace edit", got.Wiring)
	}
	if got.Wiring[0].Entry != "apps/go-core-srv" {
		t.Errorf("workspace entry = %q, want apps/go-core-srv", got.Wiring[0].Entry)
	}
	if !got.GitInit {
		t.Error("GitInit should be true for /new-project")
	}
}

func TestPlanNewProject_MonorepoCustomShell(t *testing.T) {
	s := ScaffoldDef{
		Name:      "svc",
		Layout:    LayoutMonorepo,
		Toolchain: ToolchainCustom,
		Wiring:    &WiringSpec{AppsDir: "services/", OnAdd: []string{"workspace:pnpm-workspace.yaml"}},
	}
	got, err := PlanNewProject(s, ProjectChoices{Dest: "/tmp/svc"})
	if err != nil {
		t.Fatalf("PlanNewProject custom: %v", err)
	}
	if got.Bootstrap != nil {
		t.Error("custom scaffold without a bootstrap must have Bootstrap=nil")
	}
	// shell + overlay copies, both optional.
	if len(got.Copies) != 2 {
		t.Fatalf("Copies = %+v, want shell + overlay", got.Copies)
	}
	if got.Copies[0].Src != "scaffolds/svc/shell" || !got.Copies[0].Optional {
		t.Errorf("first copy = %+v, want optional shell", got.Copies[0])
	}
	if got.Copies[1].Src != "scaffolds/svc/overlay" || !got.Copies[1].Optional {
		t.Errorf("second copy = %+v, want optional overlay", got.Copies[1])
	}
}

func TestPlanNewProject_MonorepoUnknownBlueprint(t *testing.T) {
	s := ScaffoldDef{Name: "saas", Layout: LayoutMonorepo, Toolchain: ToolchainTurborepo, Bootstrap: "create-turbo@2.3.1", Blueprints: []string{"a"}}
	_, err := PlanNewProject(s, ProjectChoices{Dest: "/tmp/x", Blueprints: []string{"nope"}})
	if !errors.Is(err, ErrScaffoldNotFound) {
		t.Fatalf("want ErrScaffoldNotFound for an undeclared blueprint, got %v", err)
	}
}

func TestPlanAddApp_Turborepo(t *testing.T) {
	s := ScaffoldDef{Name: "saas", Layout: LayoutMonorepo, Toolchain: ToolchainTurborepo, Bootstrap: "create-turbo@2.3.1", Blueprints: []string{"go-core-srv"}}
	got, err := PlanAddApp(s, AddAppChoices{Blueprint: "go-core-srv", AppName: "billing", MonorepoRoot: "/repo", Vars: map[string]string{"x": "y"}})
	if err != nil {
		t.Fatalf("PlanAddApp: %v", err)
	}
	if got.GitInit {
		t.Error("app add must not git init an existing monorepo")
	}
	if len(got.Blueprints) != 1 || got.Blueprints[0].Dest != filepath.Join("/repo", "apps", "billing") {
		t.Errorf("Blueprints = %+v", got.Blueprints)
	}
	if len(got.Wiring) != 1 || got.Wiring[0].Entry != "apps/billing" {
		t.Errorf("Wiring = %+v", got.Wiring)
	}
}

func TestPlanAddApp_CustomWiring(t *testing.T) {
	s := ScaffoldDef{
		Name:       "svc",
		Layout:     LayoutMonorepo,
		Toolchain:  ToolchainCustom,
		Blueprints: []string{"go-svc"},
		Wiring: &WiringSpec{
			AppsDir: "services/",
			OnAdd:   []string{"workspace:pnpm-workspace.yaml", "json-merge:turbo.json#pipeline", "copy:fragments/app.env"},
		},
	}
	got, err := PlanAddApp(s, AddAppChoices{Blueprint: "go-svc", AppName: "orders", MonorepoRoot: "/repo"})
	if err != nil {
		t.Fatalf("PlanAddApp custom: %v", err)
	}
	if len(got.Wiring) != 3 {
		t.Fatalf("Wiring = %+v, want 3 edits", got.Wiring)
	}
	ws, jm, cp := got.Wiring[0], got.Wiring[1], got.Wiring[2]
	if ws.Kind != WiringEditWorkspace || ws.Entry != "services/orders" {
		t.Errorf("workspace edit = %+v", ws)
	}
	if jm.Kind != WiringEditJSONMerge || jm.File != "turbo.json" || jm.JSONPath != "pipeline" || jm.Entry != "orders" {
		t.Errorf("json-merge edit = %+v", jm)
	}
	if cp.Kind != WiringEditCopy || cp.Src != "scaffolds/svc/fragments/app.env" || cp.Dest != filepath.Join("/repo", "services", "orders") {
		t.Errorf("copy edit = %+v", cp)
	}
	// blueprint lands under the custom apps dir.
	if got.Blueprints[0].Dest != filepath.Join("/repo", "services", "orders") {
		t.Errorf("blueprint dest = %q", got.Blueprints[0].Dest)
	}
}

func TestPlanAddApp_SingleNotApplicable(t *testing.T) {
	s := ScaffoldDef{Name: "lib", Layout: LayoutSingle}
	_, err := PlanAddApp(s, AddAppChoices{Blueprint: "x", AppName: "y", MonorepoRoot: "/repo"})
	if !errors.Is(err, ErrAppAddNotApplicable) {
		t.Fatalf("want ErrAppAddNotApplicable for single layout, got %v", err)
	}
}

func TestPlanAddApp_UnknownBlueprint(t *testing.T) {
	s := ScaffoldDef{Name: "saas", Layout: LayoutMonorepo, Toolchain: ToolchainTurborepo, Bootstrap: "create-turbo@2.3.1", Blueprints: []string{"a"}}
	_, err := PlanAddApp(s, AddAppChoices{Blueprint: "ghost", AppName: "y", MonorepoRoot: "/repo"})
	if !errors.Is(err, ErrScaffoldNotFound) {
		t.Fatalf("want ErrScaffoldNotFound, got %v", err)
	}
}

func TestPlanAddApp_Guards(t *testing.T) {
	s := ScaffoldDef{Name: "saas", Layout: LayoutMonorepo, Toolchain: ToolchainTurborepo, Bootstrap: "create-turbo@2.3.1", Blueprints: []string{"a"}}
	cases := []AddAppChoices{
		{AppName: "y", MonorepoRoot: "/repo"},   // empty blueprint
		{Blueprint: "a", MonorepoRoot: "/repo"}, // empty app name
		{Blueprint: "a", AppName: "y"},          // empty monorepo root
	}
	for i, c := range cases {
		if _, err := PlanAddApp(s, c); err == nil {
			t.Errorf("case %d: expected a guard error, got nil", i)
		}
	}
}

func TestPlanMonorepo_MissingToolchain(t *testing.T) {
	// A monorepo scaffold with no toolchain axis: ParseScaffold permits an
	// empty toolchain, but the planner cannot pick a Wirer for it.
	s := ScaffoldDef{Name: "x", Layout: LayoutMonorepo, Blueprints: []string{"a"}}
	_, err := PlanNewProject(s, ProjectChoices{Dest: "/tmp/x", Blueprints: []string{"a"}})
	if !errors.Is(err, ErrInvalidScaffold) {
		t.Fatalf("want ErrInvalidScaffold for a monorepo without a toolchain, got %v", err)
	}
}

func TestPlanAddApp_UnsafeAppName(t *testing.T) {
	s := ScaffoldDef{Name: "saas", Layout: LayoutMonorepo, Toolchain: ToolchainTurborepo, Bootstrap: "create-turbo@2.3.1", Blueprints: []string{"a"}}
	_, err := PlanAddApp(s, AddAppChoices{Blueprint: "a", AppName: "../evil", MonorepoRoot: "/repo"})
	if !errors.Is(err, ErrInvalidScaffold) {
		t.Fatalf("want ErrInvalidScaffold for a traversal app name, got %v", err)
	}
}

func TestPlanNewProject_MissingDest(t *testing.T) {
	s := ScaffoldDef{Name: "lib", Layout: LayoutSingle}
	_, err := PlanNewProject(s, ProjectChoices{})
	if err == nil {
		t.Fatal("want error for empty dest")
	}
}

func TestPlanNewProject_BadLayout(t *testing.T) {
	s := ScaffoldDef{Name: "x", Layout: Layout("weird")}
	_, err := PlanNewProject(s, ProjectChoices{Dest: "/tmp/x"})
	if !errors.Is(err, ErrInvalidLayout) {
		t.Fatalf("want ErrInvalidLayout, got %v", err)
	}
}
