package profile

import (
	"errors"
	"testing"
	"testing/fstest"
)

func TestParseScaffold_Valid(t *testing.T) {
	tests := []struct {
		name          string
		toml          string
		wantLayout    Layout
		wantToolchain Toolchain
		wantBootstrap string
		wantVars      int
		wantWiring    bool
	}{
		{
			name:       "single minimal",
			toml:       "layout = \"single\"\n",
			wantLayout: LayoutSingle,
		},
		{
			name: "single with vars",
			toml: `layout = "single"
[vars]
module_path = { prompt = "Go module path", default = "github.com/" }
`,
			wantLayout: LayoutSingle,
			wantVars:   1,
		},
		{
			name: "monorepo turborepo pinned",
			toml: `layout = "monorepo"
toolchain = "turborepo"
bootstrap = "create-turbo@2.3.1"
blueprints = ["go-core-srv", "next-web-ui"]
[vars]
org_name = { prompt = "Org", default = "" }
`,
			wantLayout:    LayoutMonorepo,
			wantToolchain: ToolchainTurborepo,
			wantBootstrap: "create-turbo@2.3.1",
			wantVars:      1,
		},
		{
			name: "monorepo custom with wiring",
			toml: `layout = "monorepo"
toolchain = "custom"
[wiring]
apps_dir = "services/"
on_add = ["workspace:pnpm-workspace.yaml"]
`,
			wantLayout:    LayoutMonorepo,
			wantToolchain: ToolchainCustom,
			wantWiring:    true,
		},
		{
			name:          "scoped generator sha pin",
			toml:          "layout = \"monorepo\"\ntoolchain = \"turborepo\"\nbootstrap = \"@vercel/create-next-app@abc123def456\"\n",
			wantLayout:    LayoutMonorepo,
			wantToolchain: ToolchainTurborepo,
			wantBootstrap: "@vercel/create-next-app@abc123def456",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := ParseScaffold([]byte(tc.toml))
			if err != nil {
				t.Fatalf("ParseScaffold: unexpected error: %v", err)
			}
			if s.Layout != tc.wantLayout {
				t.Errorf("Layout = %q, want %q", s.Layout, tc.wantLayout)
			}
			if s.Toolchain != tc.wantToolchain {
				t.Errorf("Toolchain = %q, want %q", s.Toolchain, tc.wantToolchain)
			}
			if s.Bootstrap != tc.wantBootstrap {
				t.Errorf("Bootstrap = %q, want %q", s.Bootstrap, tc.wantBootstrap)
			}
			if len(s.Vars) != tc.wantVars {
				t.Errorf("len(Vars) = %d, want %d", len(s.Vars), tc.wantVars)
			}
			if (s.Wiring != nil) != tc.wantWiring {
				t.Errorf("Wiring present = %v, want %v", s.Wiring != nil, tc.wantWiring)
			}
		})
	}
}

func TestParseScaffold_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr error
	}{
		{
			name:    "missing layout",
			toml:    "toolchain = \"turborepo\"\n",
			wantErr: ErrInvalidLayout,
		},
		{
			name:    "bad layout",
			toml:    "layout = \"polyrepo\"\n",
			wantErr: ErrInvalidLayout,
		},
		{
			name:    "bad toolchain",
			toml:    "layout = \"monorepo\"\ntoolchain = \"nx\"\n",
			wantErr: ErrInvalidToolchain,
		},
		{
			name:    "bootstrap latest",
			toml:    "layout = \"monorepo\"\ntoolchain = \"turborepo\"\nbootstrap = \"create-turbo@latest\"\n",
			wantErr: ErrBootstrapNotPinned,
		},
		{
			name:    "bootstrap no version",
			toml:    "layout = \"monorepo\"\ntoolchain = \"turborepo\"\nbootstrap = \"create-turbo\"\n",
			wantErr: ErrBootstrapNotPinned,
		},
		{
			name:    "bootstrap caret range",
			toml:    "layout = \"monorepo\"\ntoolchain = \"turborepo\"\nbootstrap = \"create-turbo@^2.3.1\"\n",
			wantErr: ErrBootstrapNotPinned,
		},
		{
			name:    "single with toolchain",
			toml:    "layout = \"single\"\ntoolchain = \"turborepo\"\n",
			wantErr: ErrInvalidScaffold,
		},
		{
			name:    "single with bootstrap",
			toml:    "layout = \"single\"\nbootstrap = \"create-turbo@2.3.1\"\n",
			wantErr: ErrInvalidScaffold,
		},
		{
			name:    "single with blueprints",
			toml:    "layout = \"single\"\nblueprints = [\"go-core-srv\"]\n",
			wantErr: ErrInvalidScaffold,
		},
		{
			name:    "single with wiring",
			toml:    "layout = \"single\"\n[wiring]\napps_dir = \"apps/\"\n",
			wantErr: ErrInvalidScaffold,
		},
		{
			name:    "malformed toml",
			toml:    "layout = \n",
			wantErr: nil, // any error; asserted below
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseScaffold([]byte(tc.toml))
			if err == nil {
				t.Fatal("ParseScaffold: expected error, got nil")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("ParseScaffold error = %v, want errors.Is %v", err, tc.wantErr)
			}
		})
	}
}

func TestScaffoldBootstrapParts(t *testing.T) {
	tests := []struct {
		spec    string
		gen     string
		version string
		ok      bool
	}{
		{"create-turbo@2.3.1", "create-turbo", "2.3.1", true},
		{"@vercel/create-next-app@1.2.3", "@vercel/create-next-app", "1.2.3", true},
		{"create-turbo@latest", "", "", false},
		{"create-turbo", "", "", false},
		{"", "", "", false},
		{"@scope/pkg", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.spec, func(t *testing.T) {
			s := ScaffoldDef{Bootstrap: tc.spec}
			gen, version, ok := s.BootstrapParts()
			if ok != tc.ok || gen != tc.gen || version != tc.version {
				t.Errorf("BootstrapParts(%q) = (%q,%q,%v), want (%q,%q,%v)",
					tc.spec, gen, version, ok, tc.gen, tc.version, tc.ok)
			}
		})
	}
}

func TestResolveVars(t *testing.T) {
	s := ScaffoldDef{
		Vars: map[string]VarSpec{
			"org_name":    {Default: "acme"},
			"module_path": {Default: "github.com/"},
		},
	}

	got := s.ResolveVars(map[string]string{
		"org_name": "wirvii", // overrides default
		"extra":    "passed", // undeclared pass-through
	})

	if got["org_name"] != "wirvii" {
		t.Errorf("org_name = %q, want wirvii", got["org_name"])
	}
	if got["module_path"] != "github.com/" {
		t.Errorf("module_path = %q, want default github.com/", got["module_path"])
	}
	if got["extra"] != "passed" {
		t.Errorf("extra = %q, want passed", got["extra"])
	}
	if len(got) != 3 {
		t.Errorf("len(resolved) = %d, want 3", len(got))
	}
}

func TestListScaffolds(t *testing.T) {
	fsys := fstest.MapFS{
		"scaffolds/library-go/scaffold.toml":     {Data: []byte("layout = \"single\"\n")},
		"scaffolds/library-go/skeleton/go.mod":   {Data: []byte("module x\n")},
		"scaffolds/saas/scaffold.toml":           {Data: []byte("layout = \"monorepo\"\ntoolchain = \"turborepo\"\nbootstrap = \"create-turbo@2.3.1\"\n")},
		"scaffolds/_blueprints/go-core-srv/x.go": {Data: []byte("package x\n")},
		"scaffolds/no-toml/readme.md":            {Data: []byte("nothing here\n")},
	}

	got, err := ListScaffolds(fsys)
	if err != nil {
		t.Fatalf("ListScaffolds: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (library-go, saas); got %+v", len(got), got)
	}
	// sorted by Name
	if got[0].Name != "library-go" || got[1].Name != "saas" {
		t.Errorf("names = [%q, %q], want [library-go, saas]", got[0].Name, got[1].Name)
	}
	if got[0].Layout != LayoutSingle {
		t.Errorf("library-go layout = %q, want single", got[0].Layout)
	}
}

func TestListScaffolds_NoDir(t *testing.T) {
	got, err := ListScaffolds(fstest.MapFS{})
	if err != nil {
		t.Fatalf("ListScaffolds on empty FS: %v", err)
	}
	if got != nil {
		t.Errorf("want nil for a profile with no scaffolds/, got %+v", got)
	}
}

func TestListScaffolds_MalformedFailsLoud(t *testing.T) {
	fsys := fstest.MapFS{
		"scaffolds/broken/scaffold.toml": {Data: []byte("layout = \"polyrepo\"\n")},
	}
	_, err := ListScaffolds(fsys)
	if !errors.Is(err, ErrInvalidLayout) {
		t.Fatalf("want ErrInvalidLayout for a malformed catalog entry, got %v", err)
	}
}

func TestFindScaffold(t *testing.T) {
	fsys := fstest.MapFS{
		"scaffolds/library-go/scaffold.toml": {Data: []byte("layout = \"single\"\n")},
	}
	s, err := FindScaffold(fsys, "library-go")
	if err != nil {
		t.Fatalf("FindScaffold: %v", err)
	}
	if s.Name != "library-go" {
		t.Errorf("Name = %q, want library-go", s.Name)
	}

	_, err = FindScaffold(fsys, "nope")
	if !errors.Is(err, ErrScaffoldNotFound) {
		t.Errorf("want ErrScaffoldNotFound, got %v", err)
	}
}
