package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// TestBakeSharedDefault covers the full SPEC-071 share-by-default auto-share
// table: every project-scoped type defaults to shared=1 except the two
// excluded types (synthesis, session_summary), and global/org scope always
// wins to 0 regardless of type.
func TestBakeSharedDefault(t *testing.T) {
	cases := []struct {
		name     string
		typ      model.MemoryType
		scope    model.Scope
		topicKey string
		want     int
	}{
		// Auto-shared types, project scope (or unset — the zero value) -> 1.
		{"decision/project", model.TypeDecision, model.ScopeProject, "", 1},
		{"convention/project", model.TypeConvention, model.ScopeProject, "", 1},
		{"architecture/project", model.TypeArchitecture, model.ScopeProject, "", 1},
		{"pattern/project", model.TypePattern, model.ScopeProject, "", 1},
		{"bugfix/project", model.TypeBugfix, model.ScopeProject, "", 1},
		{"rule/project", model.TypeRule, model.ScopeProject, "", 1},
		{"config/project", model.TypeConfig, model.ScopeProject, "", 1},
		{"discovery/project", model.TypeDiscovery, model.ScopeProject, "", 1},
		{"preference/project", model.TypePreference, model.ScopeProject, "", 1},

		// Excluded types (auto-generated/ephemeral), project scope -> 0.
		{"synthesis/project", model.TypeSynthesis, model.ScopeProject, "", 0},
		{"session_summary/project", model.TypeSessionSummary, model.ScopeProject, "", 0},

		// An auto-shared type never overrides global/org scope — scope always wins.
		{"decision/global", model.TypeDecision, model.ScopeGlobal, "", 0},
		{"decision/org", model.TypeDecision, model.ScopeOrg, "", 0},
		{"rule/global", model.TypeRule, model.ScopeGlobal, "", 0},
		{"rule/org", model.TypeRule, model.ScopeOrg, "", 0},

		// SPEC-089 D4: the subagent manifest is excluded by topic_key, NOT by
		// type — type=config with a DIFFERENT topic_key still auto-shares
		// (see "config/project" above), only this specific topic_key is
		// forced to 0.
		{"manifest/project", model.TypeConfig, model.ScopeProject, SubagentManifestTopicKey, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bakeSharedDefault(tc.typ, tc.scope, tc.topicKey)
			if got != tc.want {
				t.Errorf("bakeSharedDefault(%q, %q, %q) = %d, want %d", tc.typ, tc.scope, tc.topicKey, got, tc.want)
			}
		})
	}
}

// TestMaterializeTeamMemory_ProfileProvenanceNeverWrites is SPEC-094 §4 AC3:
// the guard belt in materializeTeamMemory must reject a profile-provenance
// memory independent of its Shared value. This calls the unexported method
// directly with a memory hand-built to have Shared=2 (the value a corrupted
// caller, bypassing bakeTeamMemoryFields entirely, might reach it with) —
// proving the early-return does not depend on Shared ever having been baked
// correctly upstream.
func TestMaterializeTeamMemory_ProfileProvenanceNeverWrites(t *testing.T) {
	vaultRoot := t.TempDir()

	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	svc := NewMemoryService(
		store.NewMemoryStore(projectDB),
		store.NewMemoryStore(globalDB),
		config.Default(),
		"test/project",
		embed.NopEmbedder{},
		WithTeamMemory(TeamMemoryState{Enabled: true, VaultRoot: vaultRoot}),
	)

	m := &model.Memory{
		ID:     "019ddc45-0000-0000-0000-000000000099",
		Type:   model.TypeRule,
		Scope:  model.ScopeProject,
		Title:  "A profile rule reaching materialize with Shared already elevated",
		Shared: 2, // deliberately elevated, bypassing bakeTeamMemoryFields
		Source: model.ProfileSourcePrefix + "chatea-pro",
	}

	svc.materializeTeamMemory(context.Background(), m)

	notesDir := filepath.Join(vaultRoot, "notes")
	if _, statErr := os.Stat(notesDir); !os.IsNotExist(statErr) {
		t.Errorf("materializeTeamMemory must never write a profile-provenance memory regardless of Shared, but %s exists", notesDir)
	}
}
