package service

import (
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestBakeSharedDefault covers the full SPEC-071 share-by-default auto-share
// table: every project-scoped type defaults to shared=1 except the two
// excluded types (synthesis, session_summary), and global/org scope always
// wins to 0 regardless of type.
func TestBakeSharedDefault(t *testing.T) {
	cases := []struct {
		name  string
		typ   model.MemoryType
		scope model.Scope
		want  int
	}{
		// Auto-shared types, project scope (or unset — the zero value) -> 1.
		{"decision/project", model.TypeDecision, model.ScopeProject, 1},
		{"convention/project", model.TypeConvention, model.ScopeProject, 1},
		{"architecture/project", model.TypeArchitecture, model.ScopeProject, 1},
		{"pattern/project", model.TypePattern, model.ScopeProject, 1},
		{"bugfix/project", model.TypeBugfix, model.ScopeProject, 1},
		{"rule/project", model.TypeRule, model.ScopeProject, 1},
		{"config/project", model.TypeConfig, model.ScopeProject, 1},
		{"discovery/project", model.TypeDiscovery, model.ScopeProject, 1},
		{"preference/project", model.TypePreference, model.ScopeProject, 1},

		// Excluded types (auto-generated/ephemeral), project scope -> 0.
		{"synthesis/project", model.TypeSynthesis, model.ScopeProject, 0},
		{"session_summary/project", model.TypeSessionSummary, model.ScopeProject, 0},

		// An auto-shared type never overrides global/org scope — scope always wins.
		{"decision/global", model.TypeDecision, model.ScopeGlobal, 0},
		{"decision/org", model.TypeDecision, model.ScopeOrg, 0},
		{"rule/global", model.TypeRule, model.ScopeGlobal, 0},
		{"rule/org", model.TypeRule, model.ScopeOrg, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bakeSharedDefault(tc.typ, tc.scope)
			if got != tc.want {
				t.Errorf("bakeSharedDefault(%q, %q) = %d, want %d", tc.typ, tc.scope, got, tc.want)
			}
		})
	}
}
