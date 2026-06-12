package rules

import (
	"testing"
)

func TestValidatePattern(t *testing.T) {
	tests := []struct {
		name    string
		entry   string
		wantErr bool
		errFrag string // substring expected in error message
	}{
		// Valid patterns — existing
		{name: "global_wildcard", entry: "**", wantErr: false},
		{name: "tool_selector", entry: "tool:Edit", wantErr: false},
		{name: "tool_selector_write", entry: "tool:Write", wantErr: false},
		{name: "path_glob", entry: "internal/**/*.go", wantErr: false},
		{name: "path_glob_simple", entry: "**/*.go", wantErr: false},
		{name: "combined_tool_path", entry: "tool:Edit+internal/**", wantErr: false},
		{name: "combined_tool_tsx", entry: "tool:Write+**/*.tsx", wantErr: false},
		{name: "negation_glob", entry: "!docs/**", wantErr: false},
		{name: "negation_specific", entry: "!internal/**/*_test.go", wantErr: false},
		{name: "negation_global_wildcard", entry: "!**", wantErr: false},

		// Valid patterns — agent: selectors (A2)
		{name: "agent_orchestrator", entry: "agent:orchestrator", wantErr: false},
		{name: "agent_subagent", entry: "agent:subagent", wantErr: false},
		{name: "agent_wildcard", entry: "agent:*", wantErr: false},
		// Unknown agent type is accepted without error (forward-compat / D7).
		{name: "agent_unknown_type", entry: "agent:backend", wantErr: false},
		{name: "agent_combined_3parts", entry: "agent:orchestrator+tool:Edit+internal/**", wantErr: false},
		{name: "negation_agent_subagent", entry: "!agent:subagent", wantErr: false},

		// Invalid patterns — existing
		{
			name:    "empty_string",
			entry:   "",
			wantErr: true,
			errFrag: "must not be empty",
		},
		{
			name:    "two_tool_selectors",
			entry:   "tool:Edit+tool:Write",
			wantErr: true,
			errFrag: "cannot have two tool selectors",
		},
		{
			name:    "two_tool_selectors_different",
			entry:   "tool:Read+tool:MultiEdit",
			wantErr: true,
			errFrag: "cannot have two tool selectors",
		},
		{
			name:    "empty_tool_name",
			entry:   "tool:",
			wantErr: true,
			errFrag: "tool name must not be empty",
		},
		{
			name:    "bad_glob_syntax",
			entry:   "[[bad",
			wantErr: true,
		},
		{
			name:    "empty_path_after_plus",
			entry:   "tool:Edit+",
			wantErr: true,
			errFrag: "path is empty after '+'",
		},
		{
			name:    "negation_bare_exclamation",
			entry:   "!",
			wantErr: true,
			errFrag: "negation requires a non-empty pattern",
		},

		// Invalid patterns — agent: selectors (A2)
		{
			name:    "agent_empty_name",
			entry:   "agent:",
			wantErr: true,
			errFrag: "agent name must not be empty",
		},
		{
			name:    "two_agent_selectors",
			entry:   "agent:orchestrator+agent:subagent",
			wantErr: true,
			errFrag: "cannot have two agent selectors",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePattern(tc.entry)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidatePattern(%q): expected error, got nil", tc.entry)
				}
				if tc.errFrag != "" && !contains(err.Error(), tc.errFrag) {
					t.Errorf("ValidatePattern(%q): error %q does not contain %q", tc.entry, err.Error(), tc.errFrag)
				}
			} else {
				if err != nil {
					t.Errorf("ValidatePattern(%q): unexpected error: %v", tc.entry, err)
				}
			}
		})
	}
}

// contains is a helper to avoid importing strings in test code.
func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
