package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// synthesisContextProject is the slug insertCommunityWithSynthesis writes
// its communities and syntheses under.
const synthesisContextProject = "test/commpack"

// newSynthesisContextService mirrors newCommunityPackingService but lets the
// test choose context_packing_mode, so every row of the SPEC-187 matrix can
// be exercised over the same fixtures.
func newSynthesisContextService(t *testing.T, mode string) (*service.MemoryService, *store.MemoryStore, *config.Config) {
	t.Helper()
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Context.ContextPackingMode = mode
	cfg.Context.ClusterOverviewsBudget = 1500
	cfg.Context.TopClusterMaxMembers = 10
	cfg.Graph.CommunityDetectionEnabled = true
	cfg.Graph.CommunityMinSize = 1

	svc := service.NewMemoryService(ps, gs, cfg, synthesisContextProject, embed.NopEmbedder{})
	return svc, ps, cfg
}

// seedThreeCommunities inserts three communities, each with its linked
// synthesis, the fixture every matrix row runs over.
func seedThreeCommunities(t *testing.T, ctx context.Context, ps *store.MemoryStore) {
	t.Helper()
	insertCommunityWithSynthesis(t, ctx, ps, "Auth", 5, nil, "synthesis/community-comm-Auth")
	insertCommunityWithSynthesis(t, ctx, ps, "Database", 4, nil, "synthesis/community-comm-Database")
	insertCommunityWithSynthesis(t, ctx, ps, "API", 3, nil, "synthesis/community-comm-API")
}

// countSynthesis reports how many memories of the slice are syntheses.
func countSynthesis(mems []model.Memory) int {
	n := 0
	for _, m := range mems {
		if m.Type == model.TypeSynthesis {
			n++
		}
	}
	return n
}

// TestContext_IncludeSynthesis_Matrix covers the four-row resolution of
// SPEC-187 DS3: an explicit value always wins, omitted means true for
// communities and flat, and false only for auto (and its alias "").
func TestContext_IncludeSynthesis_Matrix(t *testing.T) {
	const humans = 6
	tests := []struct {
		name     string
		mode     string
		explicit *bool
		// overviews: community packing with cluster overviews expected.
		overviews bool
		// synthInMemories: syntheses expected among the general memories.
		synthInMemories bool
	}{
		{"auto explicit true", "auto", boolPtr(true), true, false},
		{"auto explicit false", "auto", boolPtr(false), false, false},
		{"communities explicit true", "communities", boolPtr(true), true, false},
		{"communities explicit false", "communities", boolPtr(false), false, false},
		{"flat explicit true", "flat", boolPtr(true), false, true},
		{"auto omitted", "auto", nil, false, false},
		{"empty mode omitted", "", nil, false, false},
		{"communities omitted", "communities", nil, true, false},
		{"flat omitted", "flat", nil, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, ps, _ := newSynthesisContextService(t, tc.mode)
			ctx := context.Background()
			for i := 0; i < humans; i++ {
				savePackMem(t, ctx, svc, fmt.Sprintf("human %d", i), fmt.Sprintf("human content %d", i), model.TypeDecision)
			}
			seedThreeCommunities(t, ctx, ps)

			resp, err := svc.Context(ctx, model.ContextRequest{IncludeSynthesis: tc.explicit})
			if err != nil {
				t.Fatalf("Context: %v", err)
			}

			if tc.overviews {
				if resp.PackingMode != "communities" {
					t.Errorf("PackingMode = %q, want communities", resp.PackingMode)
				}
				if resp.ClusterOverviewsCount < 1 {
					t.Errorf("ClusterOverviewsCount = %d, want >= 1", resp.ClusterOverviewsCount)
				}
				return
			}
			if resp.PackingMode != "" {
				t.Errorf("PackingMode = %q, want empty (flat)", resp.PackingMode)
			}
			if len(resp.ClusterOverviews) != 0 || resp.ClusterOverviewsCount != 0 {
				t.Errorf("cluster overviews = %d/%d, want none", len(resp.ClusterOverviews), resp.ClusterOverviewsCount)
			}
			if resp.TopCluster != "" || resp.TopClusterMembers != 0 {
				t.Errorf("top cluster = %q/%d, want none", resp.TopCluster, resp.TopClusterMembers)
			}
			got := countSynthesis(resp.Memories)
			if tc.synthInMemories {
				if got == 0 {
					t.Error("no synthesis in Memories, want them competing as ordinary memories (flat)")
				}
				return
			}
			if got != 0 {
				t.Errorf("%d synthesis memories in Memories, want 0", got)
			}
			if resp.TotalAvailable != humans {
				t.Errorf("TotalAvailable = %d, want %d (non-synthesis candidates only)", resp.TotalAvailable, humans)
			}
		})
	}
}

// seedBudgetHumans inserts the same 40 human memories, with fixed ids and
// strictly decreasing importance, so two databases rank them identically.
func seedBudgetHumans(t *testing.T, ctx context.Context, ps *store.MemoryStore) {
	t.Helper()
	const (
		count       = 40
		contentSize = 600
		topImp      = 0.500
		impStep     = 0.001
	)
	body := strings.Repeat("budget human memory content. ", contentSize)[:contentSize]
	for i := 0; i < count; i++ {
		if _, err := ps.CreateWithID(ctx, &model.Memory{
			ID:         fmt.Sprintf("01a1de00-0000-7000-8000-%012d", i),
			Type:       model.TypeDecision,
			Scope:      model.ScopeProject,
			Project:    synthesisContextProject,
			Title:      fmt.Sprintf("budget-human-%02d", i),
			Content:    body,
			Importance: topImp - impStep*float64(i),
		}); err != nil {
			t.Fatalf("CreateWithID %d: %v", i, err)
		}
	}
}

// TestContext_IncludeSynthesis_BudgetNotRefilled verifies D2: the budget the
// syntheses no longer use is not handed to human memories. Base A (humans
// plus syntheses, field omitted) must pack exactly what base B (humans only)
// packs, and no more tokens than with include_synthesis: true.
func TestContext_IncludeSynthesis_BudgetNotRefilled(t *testing.T) {
	ctx := context.Background()

	svcA, psA, cfg := newSynthesisContextService(t, "auto")
	seedBudgetHumans(t, ctx, psA)
	seedThreeCommunities(t, ctx, psA)

	svcB, psB, _ := newSynthesisContextService(t, "auto")
	seedBudgetHumans(t, ctx, psB)

	resp1, err := svcA.Context(ctx, model.ContextRequest{})
	if err != nil {
		t.Fatalf("Context A omitted: %v", err)
	}
	resp2, err := svcA.Context(ctx, model.ContextRequest{IncludeSynthesis: boolPtr(true)})
	if err != nil {
		t.Fatalf("Context A true: %v", err)
	}
	resp3, err := svcB.Context(ctx, model.ContextRequest{})
	if err != nil {
		t.Fatalf("Context B omitted: %v", err)
	}

	if resp1.TokenEstimate > resp2.TokenEstimate {
		t.Errorf("TokenEstimate omitted %d > with include_synthesis %d", resp1.TokenEstimate, resp2.TokenEstimate)
	}
	if resp1.Included != resp3.Included {
		t.Fatalf("Included A=%d, B=%d: the freed budget was refilled", resp1.Included, resp3.Included)
	}
	for i := range resp1.Memories {
		a, b := resp1.Memories[i], resp3.Memories[i]
		if a.ID != b.ID || a.Title != b.Title {
			t.Errorf("Memories[%d]: A=%s/%s, B=%s/%s", i, a.ID, a.Title, b.ID, b.Title)
		}
	}
	if limit := cfg.Context.DefaultBudget + resp1.RulesTokens; resp1.TokenEstimate > limit {
		t.Errorf("TokenEstimate %d > DefaultBudget + RulesTokens %d", resp1.TokenEstimate, limit)
	}
}

// TestContext_SynthesisStarvation_HumansSurvive reproduces the starvation
// that forced the SQL filter: 1000 syntheses at 0.85 fill the
// ProjectBudget-sized candidate query, so with a Go-side filter the five
// low-importance human memories would never reach the packer.
func TestContext_SynthesisStarvation_HumansSurvive(t *testing.T) {
	const (
		syntheses  = 1000
		humans     = 5
		synthImp   = 0.85
		humanImp   = 0.3
		humanTitle = "starving human %d"
	)
	svc, ps, cfg := newSynthesisContextService(t, "auto")
	if cfg.Storage.ProjectBudget != syntheses {
		t.Fatalf("ProjectBudget = %d, the fixture assumes the default %d", cfg.Storage.ProjectBudget, syntheses)
	}
	ctx := context.Background()

	for i := 0; i < syntheses; i++ {
		if _, _, err := ps.Upsert(ctx, &model.Memory{
			Type:       model.TypeSynthesis,
			Scope:      model.ScopeProject,
			Project:    synthesisContextProject,
			Title:      fmt.Sprintf("synthesis %04d", i),
			Content:    "synthesis body",
			TopicKey:   fmt.Sprintf("synthesis/community-starve-%04d", i),
			Importance: synthImp,
		}); err != nil {
			t.Fatalf("Upsert synthesis %d: %v", i, err)
		}
	}
	want := make(map[string]bool, humans)
	for i := 0; i < humans; i++ {
		title := fmt.Sprintf(humanTitle, i)
		if _, _, err := ps.Upsert(ctx, &model.Memory{
			Type:       model.TypeDecision,
			Scope:      model.ScopeProject,
			Project:    synthesisContextProject,
			Title:      title,
			Content:    "human body",
			Importance: humanImp,
		}); err != nil {
			t.Fatalf("Upsert human %d: %v", i, err)
		}
		want[title] = true
	}

	resp, err := svc.Context(ctx, model.ContextRequest{})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	for _, m := range resp.Memories {
		delete(want, m.Title)
	}
	if len(want) != 0 {
		t.Errorf("%d human memories missing from Memories: %v", len(want), want)
	}
}
