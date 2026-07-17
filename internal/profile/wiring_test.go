package profile

import (
	"errors"
	"testing"
)

func TestParseWiringAction(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantKind WiringActionKind
		wantArg  string
		wantErr  error
	}{
		{name: "workspace", raw: "workspace:pnpm-workspace.yaml", wantKind: WiringWorkspace, wantArg: "pnpm-workspace.yaml"},
		{name: "json-merge", raw: "json-merge:turbo.json#pipeline", wantKind: WiringJSONMerge, wantArg: "turbo.json#pipeline"},
		{name: "copy", raw: "copy:fragments/app.env", wantKind: WiringCopy, wantArg: "fragments/app.env"},
		{name: "unknown verb", raw: "delete:everything", wantErr: ErrUnknownWiringAction},
		{name: "no colon", raw: "workspace", wantErr: ErrUnknownWiringAction},
		{name: "empty arg", raw: "workspace:", wantErr: ErrUnknownWiringAction},
		{name: "leading colon", raw: ":x", wantErr: ErrUnknownWiringAction},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			act, err := parseWiringAction(tc.raw)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want errors.Is %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if act.Kind != tc.wantKind || act.Arg != tc.wantArg {
				t.Errorf("got {%q %q}, want {%q %q}", act.Kind, act.Arg, tc.wantKind, tc.wantArg)
			}
		})
	}
}

func TestParseScaffold_UnknownWiringAction(t *testing.T) {
	toml := "layout = \"monorepo\"\ntoolchain = \"custom\"\n[wiring]\napps_dir = \"apps/\"\non_add = [\"workspace:pnpm-workspace.yaml\", \"nuke:all\"]\n"
	_, err := ParseScaffold([]byte(toml))
	if !errors.Is(err, ErrUnknownWiringAction) {
		t.Fatalf("want ErrUnknownWiringAction at parse time, got %v", err)
	}
}

func TestWirerFor(t *testing.T) {
	tb, err := WirerFor(ScaffoldDef{Name: "s", Layout: LayoutMonorepo, Toolchain: ToolchainTurborepo})
	if err != nil {
		t.Fatalf("turborepo: %v", err)
	}
	if tb.AppsDir() != "apps" {
		t.Errorf("turborepo AppsDir = %q, want apps", tb.AppsDir())
	}

	cw, err := WirerFor(ScaffoldDef{Name: "s", Layout: LayoutMonorepo, Toolchain: ToolchainCustom, Wiring: &WiringSpec{AppsDir: "services/"}})
	if err != nil {
		t.Fatalf("custom: %v", err)
	}
	if cw.AppsDir() != "services" {
		t.Errorf("custom AppsDir = %q, want services (trailing slash trimmed)", cw.AppsDir())
	}

	// custom with no [wiring] defaults apps dir to "apps".
	cwDefault, err := WirerFor(ScaffoldDef{Name: "s", Layout: LayoutMonorepo, Toolchain: ToolchainCustom})
	if err != nil {
		t.Fatalf("custom default: %v", err)
	}
	if cwDefault.AppsDir() != "apps" {
		t.Errorf("custom default AppsDir = %q, want apps", cwDefault.AppsDir())
	}

	if _, err := WirerFor(ScaffoldDef{Name: "lib", Layout: LayoutSingle}); !errors.Is(err, ErrAppAddNotApplicable) {
		t.Errorf("single: want ErrAppAddNotApplicable, got %v", err)
	}
}

func TestTurborepoWirer_PlanWire(t *testing.T) {
	edits, err := turborepoWirer{}.PlanWire("/repo", "web", "next-web-ui", nil)
	if err != nil {
		t.Fatalf("PlanWire: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("want exactly one edit (workspace), got %d: %+v", len(edits), edits)
	}
	e := edits[0]
	if e.Kind != WiringEditWorkspace || e.File != "pnpm-workspace.yaml" || e.Entry != "apps/web" {
		t.Errorf("edit = %+v", e)
	}
}

func TestNormalizeAppsDir(t *testing.T) {
	cases := map[string]string{"": "apps", "apps/": "apps", "services": "services", " packages/ ": "packages"}
	for in, want := range cases {
		if got := normalizeAppsDir(in); got != want {
			t.Errorf("normalizeAppsDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitJSONMergeArg(t *testing.T) {
	f, p := splitJSONMergeArg("turbo.json#pipeline")
	if f != "turbo.json" || p != "pipeline" {
		t.Errorf("got (%q,%q)", f, p)
	}
	f, p = splitJSONMergeArg("package.json")
	if f != "package.json" || p != "" {
		t.Errorf("no-hash got (%q,%q)", f, p)
	}
}
