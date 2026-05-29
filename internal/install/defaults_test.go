package install

import (
	"sort"
	"testing"
)

// TestDefaultAgentModels_KeysMatchBundled verifies that every key in
// defaultAgentModels corresponds to a bundled agent file, and that every
// bundled agent has a default model. This ensures the defaults map is
// never silently out of sync with the embedded assets.
func TestDefaultAgentModels_KeysMatchBundled(t *testing.T) {
	bundled, err := BundledAgentNames()
	if err != nil {
		t.Fatalf("BundledAgentNames error: %v", err)
	}

	bundledSet := make(map[string]bool, len(bundled))
	for _, name := range bundled {
		bundledSet[name] = true
	}

	// Every default key must be a bundled agent.
	for agent := range defaultAgentModels {
		if !bundledSet[agent] {
			t.Errorf("defaultAgentModels has key %q which is not a bundled agent", agent)
		}
	}

	// Every bundled agent must have a default model.
	for _, name := range bundled {
		if _, ok := defaultAgentModels[name]; !ok {
			t.Errorf("bundled agent %q has no entry in defaultAgentModels", name)
		}
	}
}

// TestResolveEffectiveModels_Override verifies that overrides take precedence
// over defaults when the override value is non-empty.
func TestResolveEffectiveModels_Override(t *testing.T) {
	overrides := map[string]string{"bug-hunter": "opus"}
	result := ResolveEffectiveModels(overrides)

	if got := result["bug-hunter"]; got != "opus" {
		t.Errorf("bug-hunter: got %q, want opus", got)
	}
	// Other agents should still use defaults.
	if got := result["architect"]; got != "opus" {
		t.Errorf("architect: got %q, want opus", got)
	}
	if got := result["backend"]; got != "sonnet" {
		t.Errorf("backend: got %q, want sonnet", got)
	}
}

// TestResolveEffectiveModels_Default verifies that when overrides is empty,
// every bundled agent gets its default model.
func TestResolveEffectiveModels_Default(t *testing.T) {
	result := ResolveEffectiveModels(nil)

	if got := result["architect"]; got != "opus" {
		t.Errorf("architect: got %q, want opus", got)
	}
	if got := result["backend"]; got != "sonnet" {
		t.Errorf("backend: got %q, want sonnet", got)
	}
	if got := result["qa-tester"]; got != "sonnet" {
		t.Errorf("qa-tester: got %q, want sonnet", got)
	}
}

// TestResolveEffectiveModels_EmptyOverrideIgnored verifies that an empty
// override value falls back to the default (not the empty string).
func TestResolveEffectiveModels_EmptyOverrideIgnored(t *testing.T) {
	overrides := map[string]string{"backend": ""}
	result := ResolveEffectiveModels(overrides)

	if got := result["backend"]; got != "sonnet" {
		t.Errorf("backend with empty override: got %q, want sonnet (default)", got)
	}
}

// TestResolveEffectiveModels_AllAgentsPresent verifies that ResolveEffectiveModels
// always returns an entry for every bundled agent, even with an empty override map.
func TestResolveEffectiveModels_AllAgentsPresent(t *testing.T) {
	bundled, err := BundledAgentNames()
	if err != nil {
		t.Fatalf("BundledAgentNames error: %v", err)
	}

	result := ResolveEffectiveModels(nil)

	var missing []string
	for _, name := range bundled {
		if _, ok := result[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("ResolveEffectiveModels missing agents: %v", missing)
	}
}

// TestIsKnownAlias verifies that known aliases return true and unknown ones false.
func TestIsKnownAlias(t *testing.T) {
	cases := []struct {
		alias string
		want  bool
	}{
		{"opus", true},
		{"sonnet", true},
		{"haiku", true},
		{"inherit", true},
		{"claude-opus-4-6", false},
		{"banana", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsKnownAlias(tc.alias); got != tc.want {
			t.Errorf("IsKnownAlias(%q) = %v, want %v", tc.alias, got, tc.want)
		}
	}
}

// TestDefaultModelFor verifies the helper returns the expected default or
// empty string for unknown agents.
func TestDefaultModelFor(t *testing.T) {
	cases := []struct {
		agent string
		want  string
	}{
		{"architect", "opus"},
		{"backend", "sonnet"},
		{"qa-tester", "sonnet"},
		{"nosuchagent", ""},
	}
	for _, tc := range cases {
		if got := DefaultModelFor(tc.agent); got != tc.want {
			t.Errorf("DefaultModelFor(%q) = %q, want %q", tc.agent, got, tc.want)
		}
	}
}
