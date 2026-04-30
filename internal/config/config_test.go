package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefault verifies that Default() returns the expected value for every
// field. This test acts as a regression guard: any accidental change to a
// default value will be caught immediately.
func TestDefault(t *testing.T) {
	cfg := Default()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Storage.DataDir", cfg.Storage.DataDir, filepath.Join(home, ".mneme")},
		{"Storage.ProjectBudget", cfg.Storage.ProjectBudget, 1000},
		{"Storage.GlobalBudget", cfg.Storage.GlobalBudget, 200},
		{"Search.DefaultLimit", cfg.Search.DefaultLimit, 10},
		{"Search.PreviewLength", cfg.Search.PreviewLength, 300},
		{"Search.MinRelevance", cfg.Search.MinRelevance, 0.01},
		{"Context.DefaultBudget", cfg.Context.DefaultBudget, 4000},
		{"Context.IncludeGlobal", cfg.Context.IncludeGlobal, true},
		{"Context.GlobalMinImportance", cfg.Context.GlobalMinImportance, 0.7},
		{"Consolidation.Enabled", cfg.Consolidation.Enabled, true},
		{"Consolidation.Interval", cfg.Consolidation.Interval, "6h"},
		{"Consolidation.RetentionDays", cfg.Consolidation.RetentionDays, 30},
		{"Consolidation.DedupThreshold", cfg.Consolidation.DedupThreshold, 0.92},
		{"Decay.Architecture", cfg.Decay.Architecture, 0.005},
		{"Decay.Decision", cfg.Decay.Decision, 0.005},
		{"Decay.Convention", cfg.Decay.Convention, 0.005},
		{"Decay.Pattern", cfg.Decay.Pattern, 0.01},
		{"Decay.Preference", cfg.Decay.Preference, 0.01},
		{"Decay.Bugfix", cfg.Decay.Bugfix, 0.02},
		{"Decay.Discovery", cfg.Decay.Discovery, 0.02},
		{"Decay.Config", cfg.Decay.Config, 0.02},
		{"Decay.SessionSummary", cfg.Decay.SessionSummary, 0.05},
		{"MCP.Tools", cfg.MCP.Tools, "all"},
		{"MCP.LogLevel", cfg.MCP.LogLevel, "info"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

// TestLoad_NoFile verifies that Load returns defaults without an error when
// the target file does not exist. This makes mneme usable without any
// configuration file present.
func TestLoad_NoFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}

	dflt := Default()
	if cfg.Storage.ProjectBudget != dflt.Storage.ProjectBudget {
		t.Errorf("ProjectBudget: got %d, want %d", cfg.Storage.ProjectBudget, dflt.Storage.ProjectBudget)
	}
	if cfg.Search.DefaultLimit != dflt.Search.DefaultLimit {
		t.Errorf("DefaultLimit: got %d, want %d", cfg.Search.DefaultLimit, dflt.Search.DefaultLimit)
	}
}

// TestLoad_PartialFile verifies the overlay behaviour: fields present in the
// TOML file overwrite the defaults while omitted fields retain their defaults.
func TestLoad_PartialFile(t *testing.T) {
	tomlContent := `
[storage]
project_budget = 500

[search]
default_limit = 25
`
	path := writeTempTOML(t, tomlContent)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Overridden values.
	if cfg.Storage.ProjectBudget != 500 {
		t.Errorf("ProjectBudget: got %d, want 500", cfg.Storage.ProjectBudget)
	}
	if cfg.Search.DefaultLimit != 25 {
		t.Errorf("DefaultLimit: got %d, want 25", cfg.Search.DefaultLimit)
	}

	// Non-overridden values must remain at their defaults.
	dflt := Default()
	if cfg.Storage.GlobalBudget != dflt.Storage.GlobalBudget {
		t.Errorf("GlobalBudget: got %d, want %d", cfg.Storage.GlobalBudget, dflt.Storage.GlobalBudget)
	}
	if cfg.Search.PreviewLength != dflt.Search.PreviewLength {
		t.Errorf("PreviewLength: got %d, want %d", cfg.Search.PreviewLength, dflt.Search.PreviewLength)
	}
	if cfg.MCP.LogLevel != dflt.MCP.LogLevel {
		t.Errorf("LogLevel: got %q, want %q", cfg.MCP.LogLevel, dflt.MCP.LogLevel)
	}
}

// TestLoad_EnvOverrides verifies that environment variables take precedence
// over both defaults and file-based configuration.
func TestLoad_EnvOverrides(t *testing.T) {
	wantDataDir := t.TempDir()
	t.Setenv("MNEME_DATA_DIR", wantDataDir)
	t.Setenv("MNEME_LOG_LEVEL", "debug")
	t.Setenv("MNEME_TOOLS", "mem_save,mem_search")

	cfg, err := Load("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Storage.DataDir != wantDataDir {
		t.Errorf("DataDir: got %q, want %q", cfg.Storage.DataDir, wantDataDir)
	}
	if cfg.MCP.LogLevel != "debug" {
		t.Errorf("LogLevel: got %q, want %q", cfg.MCP.LogLevel, "debug")
	}
	if cfg.MCP.Tools != "mem_save,mem_search" {
		t.Errorf("Tools: got %q, want %q", cfg.MCP.Tools, "mem_save,mem_search")
	}
}

// TestExpandHome verifies that expandHome correctly replaces a leading ~ with
// the user's real home directory, and that non-~ paths are returned unchanged.
func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "tilde only",
			input: "~",
			want:  home,
		},
		{
			name:  "tilde with subdirectory",
			input: "~/.mneme",
			want:  filepath.Join(home, ".mneme"),
		},
		{
			name:  "tilde with nested path",
			input: "~/foo/bar/baz",
			want:  filepath.Join(home, "foo", "bar", "baz"),
		},
		{
			name:  "absolute path unchanged",
			input: "/absolute/path",
			want:  "/absolute/path",
		},
		{
			name:  "relative path unchanged",
			input: "relative/path",
			want:  "relative/path",
		},
		{
			name:  "empty string unchanged",
			input: "",
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := expandHome(tc.input)
			if got != tc.want {
				t.Errorf("expandHome(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestProjectDBPath verifies that the returned path is rooted at DataDir,
// lives in the projects/ sub-directory, ends with .db, and has slashes in
// the slug replaced with dashes.
func TestProjectDBPath(t *testing.T) {
	tests := []struct {
		name    string
		slug    string
		wantSuf string // expected suffix relative to DataDir
	}{
		{
			name:    "simple slug",
			slug:    "myproject",
			wantSuf: filepath.Join("projects", "myproject.db"),
		},
		{
			name:    "slug with slashes",
			slug:    "org/repo",
			wantSuf: filepath.Join("projects", "org-repo.db"),
		},
		{
			name:    "slug with multiple slashes",
			slug:    "a/b/c",
			wantSuf: filepath.Join("projects", "a-b-c.db"),
		},
	}

	cfg := Default()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cfg.ProjectDBPath(tc.slug)
			want := filepath.Join(cfg.Storage.DataDir, tc.wantSuf)
			if got != want {
				t.Errorf("ProjectDBPath(%q) = %q, want %q", tc.slug, got, want)
			}
		})
	}
}

// TestGlobalDBPath verifies that the global database path is located directly
// inside DataDir as global.db.
func TestGlobalDBPath(t *testing.T) {
	cfg := Default()
	got := cfg.GlobalDBPath()
	want := filepath.Join(cfg.Storage.DataDir, "global.db")
	if got != want {
		t.Errorf("GlobalDBPath() = %q, want %q", got, want)
	}
	if !strings.HasSuffix(got, "global.db") {
		t.Errorf("expected path to end with global.db, got %q", got)
	}
}

// TestValidate covers the validation rules documented on (*Config).Validate.
func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid default config",
			mutate:  func(*Config) {},
			wantErr: false,
		},
		{
			name: "empty data dir",
			mutate: func(c *Config) {
				c.Storage.DataDir = ""
			},
			wantErr: true,
		},
		{
			name: "zero project budget",
			mutate: func(c *Config) {
				c.Storage.ProjectBudget = 0
			},
			wantErr: true,
		},
		{
			name: "negative project budget",
			mutate: func(c *Config) {
				c.Storage.ProjectBudget = -1
			},
			wantErr: true,
		},
		{
			name: "zero global budget",
			mutate: func(c *Config) {
				c.Storage.GlobalBudget = 0
			},
			wantErr: true,
		},
		{
			name: "zero default limit",
			mutate: func(c *Config) {
				c.Search.DefaultLimit = 0
			},
			wantErr: true,
		},
		{
			name: "zero preview length",
			mutate: func(c *Config) {
				c.Search.PreviewLength = 0
			},
			wantErr: true,
		},
		{
			name: "invalid log level",
			mutate: func(c *Config) {
				c.MCP.LogLevel = "verbose"
			},
			wantErr: true,
		},
		{
			name: "valid log level debug",
			mutate: func(c *Config) {
				c.MCP.LogLevel = "debug"
			},
			wantErr: false,
		},
		{
			name: "valid log level warn",
			mutate: func(c *Config) {
				c.MCP.LogLevel = "warn"
			},
			wantErr: false,
		},
		{
			name: "valid log level error",
			mutate: func(c *Config) {
				c.MCP.LogLevel = "error"
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestWorkflowDefaults verifies that Default() sets the expected workflow fields.
func TestWorkflowDefaults(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}
	cfg := Default()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Workflow.Dir", cfg.Workflow.Dir, filepath.Join(home, ".mneme", "workflows")},
		{"Delegation.Enabled", cfg.Delegation.Enabled, true},
		{"Spec.AutoGrill", cfg.Spec.AutoGrill, true},
		{"Spec.QualityGates.MinAcceptanceCriteria", cfg.Spec.QualityGates.MinAcceptanceCriteria, 3},
		{"Spec.QualityGates.RequireOutOfScope", cfg.Spec.QualityGates.RequireOutOfScope, true},
		{"Spec.QualityGates.RequireDependencies", cfg.Spec.QualityGates.RequireDependencies, true},
		{"Spec.QualityGates.MaxAmbiguousTerms", cfg.Spec.QualityGates.MaxAmbiguousTerms, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v, want %v", tc.got, tc.want)
			}
		})
	}

	// ProtectedPaths must include all expected prefixes.
	wantProtected := []string{"cmd/", "internal/", "src/", "apps/", "packages/", "lib/"}
	for _, p := range wantProtected {
		found := false
		for _, g := range cfg.Delegation.ProtectedPaths {
			if g == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Delegation.ProtectedPaths missing %q", p)
		}
	}
}

// TestWorkflowEnvOverride verifies that MNEME_WORKFLOW_DIR overrides the default.
func TestWorkflowEnvOverride(t *testing.T) {
	wantDir := t.TempDir()
	t.Setenv("MNEME_WORKFLOW_DIR", wantDir)

	cfg, err := Load("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Workflow.Dir != wantDir {
		t.Errorf("Workflow.Dir: got %q, want %q", cfg.Workflow.Dir, wantDir)
	}
}

// TestProjectWorkflowDir verifies path construction and slug sanitisation.
func TestProjectWorkflowDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}
	cfg := Default()

	tests := []struct {
		name string
		slug string
		want string
	}{
		{
			name: "simple slug",
			slug: "mneme",
			want: filepath.Join(home, ".mneme", "workflows", "mneme"),
		},
		{
			name: "slug with slashes",
			slug: "wirvii/mneme",
			want: filepath.Join(home, ".mneme", "workflows", "wirvii-mneme"),
		},
		{
			name: "slug with multiple slashes",
			slug: "org/team/project",
			want: filepath.Join(home, ".mneme", "workflows", "org-team-project"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cfg.ProjectWorkflowDir(tc.slug)
			if got != tc.want {
				t.Errorf("ProjectWorkflowDir(%q) = %q, want %q", tc.slug, got, tc.want)
			}
		})
	}
}

// TestIsDelegationProtected verifies the delegation enforcement logic.
func TestIsDelegationProtected(t *testing.T) {
	cfg := Default()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "protected cmd/", path: "cmd/main.go", want: true},
		{name: "protected internal/", path: "internal/store/sdd.go", want: true},
		{name: "protected src/", path: "src/index.ts", want: true},
		{name: "allowed docs/", path: "docs/README.md", want: false},
		{name: "allowed *.md", path: "README.md", want: false},
		{name: "allowed CLAUDE.md", path: "CLAUDE.md", want: false},
		{name: "unprotected root file", path: "go.mod", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cfg.IsDelegationProtected(tc.path)
			if got != tc.want {
				t.Errorf("IsDelegationProtected(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}

	// Disabled delegation returns false for everything.
	cfg.Delegation.Enabled = false
	if cfg.IsDelegationProtected("cmd/main.go") {
		t.Error("IsDelegationProtected with Enabled=false should return false")
	}
}

// TestContextConfig_RulesBudgetDefault verifies that Default() sets RulesBudget
// to 1500, the value that guarantees rules always appear in the context bundle.
func TestContextConfig_RulesBudgetDefault(t *testing.T) {
	cfg := Default()
	if cfg.Context.RulesBudget != 1500 {
		t.Errorf("Context.RulesBudget: got %d, want 1500", cfg.Context.RulesBudget)
	}
}

// TestContextConfig_RulesBudgetEnvOverride verifies that MNEME_RULES_BUDGET
// takes precedence over the compiled-in default.
func TestContextConfig_RulesBudgetEnvOverride(t *testing.T) {
	t.Setenv("MNEME_RULES_BUDGET", "2000")
	cfg, err := Load("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Context.RulesBudget != 2000 {
		t.Errorf("Context.RulesBudget: got %d, want 2000", cfg.Context.RulesBudget)
	}
}

// TestContextConfig_RulesBudgetValidation verifies that a negative rules_budget
// is rejected by Validate so the binary fails early with a clear message.
func TestContextConfig_RulesBudgetValidation(t *testing.T) {
	cfg := Default()
	cfg.Context.RulesBudget = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for negative RulesBudget, got nil")
	}
}

// TestContextConfig_RulesBudgetZero verifies that rules_budget=0 is a valid
// configuration (it disables rule injection without being an error).
func TestContextConfig_RulesBudgetZero(t *testing.T) {
	cfg := Default()
	cfg.Context.RulesBudget = 0
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error for RulesBudget=0, got: %v", err)
	}
}

// TestGraphConfig_Defaults verifies that Default() returns the canonical values
// documented in D8 of the SPEC-006 design document.
func TestGraphConfig_Defaults(t *testing.T) {
	cfg := Default()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Graph.HebbianWindow", cfg.Graph.HebbianWindow, 5},
		{"Graph.HebbianIncrement", cfg.Graph.HebbianIncrement, 0.05},
		{"Graph.HebbianInitialWeight", cfg.Graph.HebbianInitialWeight, 0.1},
		{"Graph.HebbianBufferSize", cfg.Graph.HebbianBufferSize, 1000},
		{"Graph.EdgeDecayRate", cfg.Graph.EdgeDecayRate, 0.02},
		{"Graph.EdgeDecayAfterDays", cfg.Graph.EdgeDecayAfterDays, 30},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

// TestGraphConfig_Validation covers every validation rule for the [graph]
// config section.
func TestGraphConfig_Validation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid default",
			mutate:  func(*Config) {},
			wantErr: false,
		},
		{
			name: "HebbianWindow zero is valid (toggle off)",
			mutate: func(c *Config) {
				c.Graph.HebbianWindow = 0
			},
			wantErr: false,
		},
		{
			name: "HebbianWindow one is valid (no-op)",
			mutate: func(c *Config) {
				c.Graph.HebbianWindow = 1
			},
			wantErr: false,
		},
		{
			name: "HebbianWindow negative errors",
			mutate: func(c *Config) {
				c.Graph.HebbianWindow = -1
			},
			wantErr: true,
		},
		{
			name: "HebbianIncrement below 0 errors",
			mutate: func(c *Config) {
				c.Graph.HebbianIncrement = -0.01
			},
			wantErr: true,
		},
		{
			name: "HebbianIncrement above 1 errors",
			mutate: func(c *Config) {
				c.Graph.HebbianIncrement = 1.01
			},
			wantErr: true,
		},
		{
			name: "HebbianIncrement zero is valid (no strengthening)",
			mutate: func(c *Config) {
				c.Graph.HebbianIncrement = 0
			},
			wantErr: false,
		},
		{
			name: "HebbianInitialWeight below 0 errors",
			mutate: func(c *Config) {
				c.Graph.HebbianInitialWeight = -0.01
			},
			wantErr: true,
		},
		{
			name: "HebbianInitialWeight above 1 errors",
			mutate: func(c *Config) {
				c.Graph.HebbianInitialWeight = 1.01
			},
			wantErr: true,
		},
		{
			name: "HebbianBufferSize negative errors",
			mutate: func(c *Config) {
				c.Graph.HebbianBufferSize = -1
			},
			wantErr: true,
		},
		{
			name: "HebbianBufferSize zero is valid",
			mutate: func(c *Config) {
				c.Graph.HebbianBufferSize = 0
			},
			wantErr: false,
		},
		{
			name: "EdgeDecayRate negative errors",
			mutate: func(c *Config) {
				c.Graph.EdgeDecayRate = -0.01
			},
			wantErr: true,
		},
		{
			name: "EdgeDecayRate zero is valid (toggle off)",
			mutate: func(c *Config) {
				c.Graph.EdgeDecayRate = 0
			},
			wantErr: false,
		},
		{
			name: "EdgeDecayAfterDays negative errors",
			mutate: func(c *Config) {
				c.Graph.EdgeDecayAfterDays = -1
			},
			wantErr: true,
		},
		{
			name: "EdgeDecayAfterDays zero is valid",
			mutate: func(c *Config) {
				c.Graph.EdgeDecayAfterDays = 0
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestGraphConfig_TOMLOverride verifies that [graph] values in config.toml
// correctly override the defaults when a file is loaded.
func TestGraphConfig_TOMLOverride(t *testing.T) {
	tomlContent := `
[graph]
hebbian_window = 10
hebbian_increment = 0.1
hebbian_initial_weight = 0.2
hebbian_buffer_size = 500
edge_decay_rate = 0.01
edge_decay_after_days = 60
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Graph.HebbianWindow != 10 {
		t.Errorf("HebbianWindow: got %d, want 10", cfg.Graph.HebbianWindow)
	}
	if cfg.Graph.HebbianIncrement != 0.1 {
		t.Errorf("HebbianIncrement: got %f, want 0.1", cfg.Graph.HebbianIncrement)
	}
	if cfg.Graph.HebbianInitialWeight != 0.2 {
		t.Errorf("HebbianInitialWeight: got %f, want 0.2", cfg.Graph.HebbianInitialWeight)
	}
	if cfg.Graph.HebbianBufferSize != 500 {
		t.Errorf("HebbianBufferSize: got %d, want 500", cfg.Graph.HebbianBufferSize)
	}
	if cfg.Graph.EdgeDecayRate != 0.01 {
		t.Errorf("EdgeDecayRate: got %f, want 0.01", cfg.Graph.EdgeDecayRate)
	}
	if cfg.Graph.EdgeDecayAfterDays != 60 {
		t.Errorf("EdgeDecayAfterDays: got %d, want 60", cfg.Graph.EdgeDecayAfterDays)
	}
}

// TestGraphConfig_ExpansionDefaults verifies that Default() sets all four
// expansion parameters to the values documented in D6 of SPEC-007.
func TestGraphConfig_ExpansionDefaults(t *testing.T) {
	cfg := Default()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Graph.ExpansionEnabled", cfg.Graph.ExpansionEnabled, true},
		{"Graph.ExpansionThreshold", cfg.Graph.ExpansionThreshold, 0.3},
		{"Graph.ExpansionFanOutCap", cfg.Graph.ExpansionFanOutCap, 50},
		{"Graph.ExpansionSeedTopK", cfg.Graph.ExpansionSeedTopK, 10},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

// TestGraphConfig_ExpansionValidation verifies that invalid expansion parameter
// values are rejected by Validate() with a clear error message.
func TestGraphConfig_ExpansionValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid defaults",
			mutate:  func(*Config) {},
			wantErr: false,
		},
		{
			name: "ExpansionThreshold below 0 errors",
			mutate: func(c *Config) {
				c.Graph.ExpansionThreshold = -0.1
			},
			wantErr: true,
		},
		{
			name: "ExpansionThreshold above 1 errors",
			mutate: func(c *Config) {
				c.Graph.ExpansionThreshold = 1.1
			},
			wantErr: true,
		},
		{
			name: "ExpansionThreshold zero is valid (no filtering)",
			mutate: func(c *Config) {
				c.Graph.ExpansionThreshold = 0.0
			},
			wantErr: false,
		},
		{
			name: "ExpansionThreshold 1.0 is valid (strict)",
			mutate: func(c *Config) {
				c.Graph.ExpansionThreshold = 1.0
			},
			wantErr: false,
		},
		{
			name: "ExpansionFanOutCap negative errors",
			mutate: func(c *Config) {
				c.Graph.ExpansionFanOutCap = -1
			},
			wantErr: true,
		},
		{
			name: "ExpansionFanOutCap zero is valid (disables expansion via cap)",
			mutate: func(c *Config) {
				c.Graph.ExpansionFanOutCap = 0
			},
			wantErr: false,
		},
		{
			name: "ExpansionSeedTopK negative errors",
			mutate: func(c *Config) {
				c.Graph.ExpansionSeedTopK = -1
			},
			wantErr: true,
		},
		{
			name: "ExpansionSeedTopK zero is valid (disables expansion via seeds)",
			mutate: func(c *Config) {
				c.Graph.ExpansionSeedTopK = 0
			},
			wantErr: false,
		},
		{
			name: "ExpansionEnabled false is valid",
			mutate: func(c *Config) {
				c.Graph.ExpansionEnabled = false
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestGraphConfig_ExpansionTOMLOverride verifies that expansion fields in a
// config file are parsed correctly, overriding defaults.
func TestGraphConfig_ExpansionTOMLOverride(t *testing.T) {
	tomlContent := `
[graph]
expansion_enabled = false
expansion_threshold = 0.5
expansion_fan_out_cap = 25
expansion_seed_top_k = 5
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Graph.ExpansionEnabled {
		t.Errorf("ExpansionEnabled: got true, want false")
	}
	if cfg.Graph.ExpansionThreshold != 0.5 {
		t.Errorf("ExpansionThreshold: got %f, want 0.5", cfg.Graph.ExpansionThreshold)
	}
	if cfg.Graph.ExpansionFanOutCap != 25 {
		t.Errorf("ExpansionFanOutCap: got %d, want 25", cfg.Graph.ExpansionFanOutCap)
	}
	if cfg.Graph.ExpansionSeedTopK != 5 {
		t.Errorf("ExpansionSeedTopK: got %d, want 5", cfg.Graph.ExpansionSeedTopK)
	}
}

// TestGraphConfig_ExpansionEnvOverride verifies that MNEME_EXPANSION_* env
// variables override both defaults and file-based configuration.
func TestGraphConfig_ExpansionEnvOverride(t *testing.T) {
	t.Setenv("MNEME_EXPANSION_ENABLED", "false")
	t.Setenv("MNEME_EXPANSION_THRESHOLD", "0.6")
	t.Setenv("MNEME_EXPANSION_FAN_OUT_CAP", "20")
	t.Setenv("MNEME_EXPANSION_SEED_TOP_K", "7")

	cfg, err := Load("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Graph.ExpansionEnabled {
		t.Errorf("ExpansionEnabled: got true, want false")
	}
	if cfg.Graph.ExpansionThreshold != 0.6 {
		t.Errorf("ExpansionThreshold: got %f, want 0.6", cfg.Graph.ExpansionThreshold)
	}
	if cfg.Graph.ExpansionFanOutCap != 20 {
		t.Errorf("ExpansionFanOutCap: got %d, want 20", cfg.Graph.ExpansionFanOutCap)
	}
	if cfg.Graph.ExpansionSeedTopK != 7 {
		t.Errorf("ExpansionSeedTopK: got %d, want 7", cfg.Graph.ExpansionSeedTopK)
	}
}

// writeTempTOML writes content to a temporary TOML file and returns its path.
// The file is automatically removed when the test ends.
func writeTempTOML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "mneme-config-*.toml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	return f.Name()
}
