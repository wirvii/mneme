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
