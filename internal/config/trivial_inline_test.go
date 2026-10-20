package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOrchestratorTrivialInlineEnabled covers criterion "config": default
// off, global TOML, per-project override in both directions, and the env
// override of the GLOBAL value (which never beats a project declaration).
func TestOrchestratorTrivialInlineEnabled(t *testing.T) {
	yes, no := true, false
	const slug = "wirvii/mneme"

	tests := []struct {
		name string
		toml string
		env  string
		want bool
	}{
		{"default off", "", "", false},
		{"global toml true", "[delegation]\norchestrator_trivial_inline = true\n", "", true},
		{"project false beats global true", "[delegation]\norchestrator_trivial_inline = true\n[delegation.projects.\"" + slug + "\"]\norchestrator_trivial_inline = false\n", "", false},
		{"project true beats global false", "[delegation.projects.\"" + slug + "\"]\norchestrator_trivial_inline = true\n", "", true},
		{"other project does not apply", "[delegation.projects.\"other/x\"]\norchestrator_trivial_inline = true\n", "", false},
		{"env 1 enables", "", "1", true},
		{"env true enables", "", "true", true},
		{"env 0 disables global true", "[delegation]\norchestrator_trivial_inline = true\n", "0", false},
		{"env false disables", "[delegation]\norchestrator_trivial_inline = true\n", "false", false},
		{"env garbage ignored", "[delegation]\norchestrator_trivial_inline = true\n", "maybe", true},
		{"env does not beat project", "[delegation.projects.\"" + slug + "\"]\norchestrator_trivial_inline = false\n", "1", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MNEME_DELEGATION_TRIVIAL_INLINE", tc.env)
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.toml), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.OrchestratorTrivialInlineEnabled(slug); got != tc.want {
				t.Errorf("enabled = %v, want %v", got, tc.want)
			}
		})
	}

	// Direct struct construction: the pointer distinguishes "not declared" from false.
	cfg := Default()
	cfg.Delegation.OrchestratorTrivialInline = true
	cfg.Delegation.Projects = map[string]DelegationProjectConfig{"a": {OrchestratorTrivialInline: &no}, "b": {OrchestratorTrivialInline: &yes}, "c": {}}
	for slug, want := range map[string]bool{"a": false, "b": true, "c": true, "zzz": true} {
		if got := cfg.OrchestratorTrivialInlineEnabled(slug); got != want {
			t.Errorf("project %q: %v, want %v", slug, got, want)
		}
	}
}

// TestLoadWithOrigins_TrivialInline checks that mneme config lists the field
// and records its env var.
func TestLoadWithOrigins_TrivialInline(t *testing.T) {
	t.Setenv("MNEME_DELEGATION_TRIVIAL_INLINE", "1")
	_, origins, err := LoadWithOrigins("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("LoadWithOrigins: %v", err)
	}
	for _, f := range origins.Sections["delegation"] {
		if f.Key == "orchestrator_trivial_inline" {
			if f.Origin != OriginEnv || f.EnvVar != "MNEME_DELEGATION_TRIVIAL_INLINE" {
				t.Errorf("origin = %q env = %q", f.Origin, f.EnvVar)
			}
			return
		}
	}
	t.Error("orchestrator_trivial_inline missing from delegation origins")
}

// TestSetSubagentContainmentMode_KeepsTrivialInlineOverride guards against
// the promote write path dropping the SPEC-171 per-project opt-in.
func TestSetSubagentContainmentMode_KeepsTrivialInlineOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := "[delegation.projects.\"wirvii/mneme\"]\norchestrator_trivial_inline = true\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetSubagentContainmentMode(path, "wirvii/mneme", "block"); err != nil {
		t.Fatalf("SetSubagentContainmentMode: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OrchestratorTrivialInlineEnabled("wirvii/mneme") {
		t.Error("per-project trivial-inline opt-in was lost")
	}
	if cfg.SubagentContainmentMode("wirvii/mneme") != "block" {
		t.Error("containment mode not written")
	}
}
