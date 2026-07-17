package profile

import (
	"errors"
	"reflect"
	"testing"
)

func TestInferLayout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		rs        RepoStructure
		wantLay   Layout
		wantChain Toolchain
	}{
		{"turborepo via turbo.json", RepoStructure{HasTurboJSON: true, Apps: []string{"web"}}, LayoutMonorepo, ToolchainTurborepo},
		{"custom via pnpm-workspace", RepoStructure{HasPnpmWorkspace: true, Apps: []string{"api"}}, LayoutMonorepo, ToolchainCustom},
		{"monorepo via apps only", RepoStructure{Apps: []string{"api"}}, LayoutMonorepo, ToolchainCustom},
		{"single, no signals", RepoStructure{RootFiles: []string{"go.mod"}}, LayoutSingle, ""},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotLay, gotChain := InferLayout(tt.rs)
			if gotLay != tt.wantLay || gotChain != tt.wantChain {
				t.Errorf("InferLayout = (%q,%q), want (%q,%q)", gotLay, gotChain, tt.wantLay, tt.wantChain)
			}
		})
	}
}

func TestPlanCapture_Single(t *testing.T) {
	t.Parallel()
	rs := RepoStructure{
		ProjectName: "acme-lib",
		ModulePath:  "github.com/acme/acme-lib",
		RootFiles:   []string{"go.mod", "README.md"},
	}
	plan, err := PlanCapture("library-go", rs)
	if err != nil {
		t.Fatalf("PlanCapture: %v", err)
	}
	if plan.Def.Layout != LayoutSingle {
		t.Errorf("layout = %q, want single", plan.Def.Layout)
	}
	if plan.Def.Toolchain != "" {
		t.Errorf("toolchain = %q, want empty for single", plan.Def.Toolchain)
	}
	if len(plan.Copies) != 1 || plan.Copies[0].Src != "." || !plan.Copies[0].IsDir {
		t.Fatalf("single capture should copy the whole repo into skeleton/, got %+v", plan.Copies)
	}
	if plan.Copies[0].Dest != "scaffolds/library-go/skeleton" {
		t.Errorf("skeleton dest = %q", plan.Copies[0].Dest)
	}
	// Vars: both PROJECT_NAME and MODULE_PATH (go module present).
	if _, ok := plan.Def.Vars[CaptureVarProjectName]; !ok {
		t.Error("missing PROJECT_NAME var")
	}
	if _, ok := plan.Def.Vars[CaptureVarModulePath]; !ok {
		t.Error("missing MODULE_PATH var")
	}
	// Params rewrite exemplar identity to placeholders.
	if plan.Params["github.com/acme/acme-lib"] != "{{MODULE_PATH}}" {
		t.Errorf("module path param = %q", plan.Params["github.com/acme/acme-lib"])
	}
	if plan.Params["acme-lib"] != "{{PROJECT_NAME}}" {
		t.Errorf("project name param = %q", plan.Params["acme-lib"])
	}
	// The rendered TOML round-trips through ParseScaffold (AC12).
	if _, err := ParseScaffold(plan.TOML); err != nil {
		t.Errorf("rendered single scaffold.toml is invalid: %v\n%s", err, plan.TOML)
	}
}

func TestPlanCapture_MonorepoTurborepo(t *testing.T) {
	t.Parallel()
	rs := RepoStructure{
		ProjectName:  "acme",
		HasTurboJSON: true,
		HasPackages:  true,
		Apps:         []string{"web", "api"},
		RootFiles:    []string{"turbo.json", "package.json", "pnpm-workspace.yaml"},
	}
	plan, err := PlanCapture("saas", rs)
	if err != nil {
		t.Fatalf("PlanCapture: %v", err)
	}
	if plan.Def.Layout != LayoutMonorepo || plan.Def.Toolchain != ToolchainTurborepo {
		t.Fatalf("layout/toolchain = %q/%q, want monorepo/turborepo", plan.Def.Layout, plan.Def.Toolchain)
	}
	if plan.Def.Wiring != nil {
		t.Error("turborepo capture must not emit a [wiring] block")
	}
	// Blueprints sorted.
	if !reflect.DeepEqual(plan.Def.Blueprints, []string{"api", "web"}) {
		t.Errorf("blueprints = %v, want sorted [api web]", plan.Def.Blueprints)
	}
	// Copies: 3 shell files + overlay/packages + 2 blueprints.
	var shell, blueprint, overlay int
	for _, c := range plan.Copies {
		switch {
		case c.Dest == "_blueprints/web" || c.Dest == "_blueprints/api":
			blueprint++
		case c.Dest == "scaffolds/saas/overlay/packages":
			overlay++
		case len(c.Dest) > len("scaffolds/saas/shell/") && c.Dest[:len("scaffolds/saas/shell/")] == "scaffolds/saas/shell/":
			shell++
		}
	}
	if shell != 3 || blueprint != 2 || overlay != 1 {
		t.Errorf("copies breakdown shell=%d blueprint=%d overlay=%d, want 3/2/1", shell, blueprint, overlay)
	}
	if _, err := ParseScaffold(plan.TOML); err != nil {
		t.Errorf("rendered turborepo scaffold.toml is invalid: %v\n%s", err, plan.TOML)
	}
}

func TestPlanCapture_MonorepoCustom(t *testing.T) {
	t.Parallel()
	rs := RepoStructure{
		ProjectName:      "acme",
		HasPnpmWorkspace: true,
		Apps:             []string{"svc"},
		RootFiles:        []string{"pnpm-workspace.yaml"},
	}
	plan, err := PlanCapture("custom-mono", rs)
	if err != nil {
		t.Fatalf("PlanCapture: %v", err)
	}
	if plan.Def.Toolchain != ToolchainCustom {
		t.Fatalf("toolchain = %q, want custom", plan.Def.Toolchain)
	}
	if plan.Def.Wiring == nil {
		t.Fatal("custom capture must emit a [wiring] block")
	}
	if plan.Def.Wiring.AppsDir != "apps/" {
		t.Errorf("wiring apps_dir = %q", plan.Def.Wiring.AppsDir)
	}
	if _, err := ParseScaffold(plan.TOML); err != nil {
		t.Errorf("rendered custom scaffold.toml is invalid: %v\n%s", err, plan.TOML)
	}
}

func TestPlanCapture_Errors(t *testing.T) {
	t.Parallel()
	if _, err := PlanCapture("Bad Name", RepoStructure{RootFiles: []string{"x"}}); !errors.Is(err, ErrInvalidScaffold) {
		t.Errorf("bad slug: got %v, want ErrInvalidScaffold", err)
	}
	if _, err := PlanCapture("empty", RepoStructure{}); !errors.Is(err, ErrNothingToCapture) {
		t.Errorf("empty repo: got %v, want ErrNothingToCapture", err)
	}
}

func TestParametrizeContent_LongestFirst(t *testing.T) {
	t.Parallel()
	params := map[string]string{
		"acme":                 "{{PROJECT_NAME}}",
		"github.com/acme/acme": "{{MODULE_PATH}}",
	}
	in := []byte("module github.com/acme/acme // name acme")
	got := string(ParametrizeContent(in, params))
	want := "module {{MODULE_PATH}} // name {{PROJECT_NAME}}"
	if got != want {
		t.Errorf("ParametrizeContent = %q, want %q", got, want)
	}
	if string(ParametrizeContent(in, nil)) != string(in) {
		t.Error("nil params must be a no-op")
	}
}
