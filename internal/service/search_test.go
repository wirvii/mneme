package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

func TestSearch_Success(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	memories := []model.SaveRequest{
		{
			Title:   "SQLite FTS5 fulltext search",
			Content: "SQLite FTS5 supports BM25 ranking for fulltext search queries using porter tokenizer.",
		},
		{
			Title:   "PostgreSQL connection pooling",
			Content: "Use pgbouncer for connection pooling with PostgreSQL in production.",
		},
		{
			Title:   "Redis cache eviction policy",
			Content: "Set maxmemory policy to allkeys lru for general purpose caching.",
		},
	}

	for _, req := range memories {
		if _, err := svc.Save(ctx, req); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	resp, err := svc.Search(ctx, model.SearchRequest{
		Query: "SQLite fulltext search",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}
	if resp.Query != "SQLite fulltext search" {
		t.Errorf("expected query echoed, got %q", resp.Query)
	}
	// The SQLite FTS5 memory should be the top result.
	if resp.Results[0].Title != "SQLite FTS5 fulltext search" {
		t.Errorf("expected SQLite memory first, got %q", resp.Results[0].Title)
	}
}

func TestSearch_Validation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Search(ctx, model.SearchRequest{Query: ""})
	if !errors.Is(err, model.ErrQueryRequired) {
		t.Errorf("expected ErrQueryRequired, got %v", err)
	}
}

func TestSearch_LimitCap(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Save enough memories to exercise the cap.
	for i := 0; i < 5; i++ {
		_, err := svc.Save(ctx, model.SaveRequest{
			Title:   "test memory",
			Content: "content for limit test",
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	// A limit over 50 should be silently capped at 50. We cannot exceed 50
	// results from 5 rows, but we verify no error occurs and Total <= 50.
	resp, err := svc.Search(ctx, model.SearchRequest{
		Query: "test memory content",
		Limit: 200,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Total > 50 {
		t.Errorf("expected Total <= 50, got %d", resp.Total)
	}
}

func TestSearch_ProjectFilter(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Save a memory explicitly in a different project.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:   "other project memory",
		Content: "this belongs to another project",
		Project: "other/project",
	})
	if err != nil {
		t.Fatalf("Save (other project): %v", err)
	}

	// Search within the default test project — should not return the above memory.
	resp, err := svc.Search(ctx, model.SearchRequest{
		Query:   "other project memory",
		Project: "test/project",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range resp.Results {
		if r.Project == "other/project" {
			t.Errorf("got memory from wrong project: %q", r.Project)
		}
	}
}

// ─── Graph expansion tests ────────────────────────────────────────────────────

// newTestServiceWithGraph returns a MemoryService and its underlying projectStore
// so tests can set up entities, relations, and memory-entity links directly.
func newTestServiceWithGraph(t *testing.T) (*service.MemoryService, *store.MemoryStore) {
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
	cfg.Graph.ExpansionEnabled = true
	cfg.Graph.ExpansionThreshold = 0.3
	cfg.Graph.ExpansionFanOutCap = 50
	cfg.Graph.ExpansionSeedTopK = 10

	svc := service.NewMemoryService(ps, gs, cfg, "test/graph", embed.NopEmbedder{})
	return svc, ps
}

// boolPtr is a small helper to get *bool values inline.
func boolPtr(b bool) *bool { return &b }

// TestSearch_GraphExpansion_ColdStart verifies that when no graph relations
// exist, the results are identical to 2-channel retrieval (no degradation).
func TestSearch_GraphExpansion_ColdStart(t *testing.T) {
	svc, _ := newTestServiceWithGraph(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:   "JWT RS256 configuration",
		Content: "JWT authentication with RS256 signing algorithm",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// With graph — should not panic or error even though no relations exist.
	resp, err := svc.Search(ctx, model.SearchRequest{
		Query:        "JWT RS256",
		IncludeGraph: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("Search with graph (cold start): %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}
}

// TestSearch_GraphExpansion_SurfacesNeighbor is the core acceptance criterion:
// a memory connected via a strong relation to an FTS5-matched memory should
// appear in results even when it shares no tokens with the query.
func TestSearch_GraphExpansion_SurfacesNeighbor(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	// Save two memories: one that matches "JWT RS256" and one that does not.
	seedResp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "JWT RS256 auth service",
		Content: "JWT authentication with RS256 signing algorithm",
	})
	if err != nil {
		t.Fatalf("Save seed: %v", err)
	}
	neighborResp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Key rotation schedule",
		Content: "Rotate cryptographic keys every 90 days",
	})
	if err != nil {
		t.Fatalf("Save neighbor: %v", err)
	}

	// Create entities and link them to both memories.
	authEntity, err := ps.FindOrCreateEntity(ctx, "auth-service", model.KindService, "test/graph")
	if err != nil {
		t.Fatalf("FindOrCreateEntity auth-service: %v", err)
	}
	keyEntity, err := ps.FindOrCreateEntity(ctx, "key-rotation", model.KindService, "test/graph")
	if err != nil {
		t.Fatalf("FindOrCreateEntity key-rotation: %v", err)
	}

	if err := ps.LinkMemoryEntity(ctx, seedResp.ID, authEntity.ID, "subject"); err != nil {
		t.Fatalf("LinkMemoryEntity seed-auth: %v", err)
	}
	if err := ps.LinkMemoryEntity(ctx, neighborResp.ID, keyEntity.ID, "subject"); err != nil {
		t.Fatalf("LinkMemoryEntity neighbor-key: %v", err)
	}

	// Create a strong relation between the two entities.
	_, err = ps.CreateRelation(ctx, &model.Relation{
		SourceID: authEntity.ID,
		TargetID: keyEntity.ID,
		Type:     model.RelDependsOn,
		Weight:   0.9,
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	// Search for "JWT RS256" with graph expansion enabled.
	resp, err := svc.Search(ctx, model.SearchRequest{
		Query:        "JWT RS256",
		IncludeGraph: boolPtr(true),
		Limit:        20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// The neighbor memory should appear in results.
	found := false
	for _, r := range resp.Results {
		if r.ID == neighborResp.ID {
			found = true
			if r.RelevanceScore <= 0 {
				t.Errorf("neighbor memory has non-positive relevance score %f", r.RelevanceScore)
			}
			break
		}
	}
	if !found {
		t.Errorf("neighbor memory %q not found in graph-expanded results", neighborResp.ID)
	}
}

// TestSearch_GraphExpansion_Disabled verifies that when include_graph=false,
// memories only reachable via graph do not appear in results.
func TestSearch_GraphExpansion_Disabled(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedResp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "JWT RS256 auth service",
		Content: "JWT authentication with RS256 signing",
	})
	if err != nil {
		t.Fatalf("Save seed: %v", err)
	}
	neighborResp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Unrelated but connected",
		Content: "Completely different content with no query tokens",
	})
	if err != nil {
		t.Fatalf("Save neighbor: %v", err)
	}

	authEntity, err := ps.FindOrCreateEntity(ctx, "auth2", model.KindService, "test/graph")
	if err != nil {
		t.Fatalf("FindOrCreateEntity: %v", err)
	}
	otherEntity, err := ps.FindOrCreateEntity(ctx, "other2", model.KindService, "test/graph")
	if err != nil {
		t.Fatalf("FindOrCreateEntity: %v", err)
	}

	if err := ps.LinkMemoryEntity(ctx, seedResp.ID, authEntity.ID, "subject"); err != nil {
		t.Fatalf("LinkMemoryEntity seed: %v", err)
	}
	if err := ps.LinkMemoryEntity(ctx, neighborResp.ID, otherEntity.ID, "subject"); err != nil {
		t.Fatalf("LinkMemoryEntity neighbor: %v", err)
	}
	if _, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: authEntity.ID,
		TargetID: otherEntity.ID,
		Type:     model.RelDependsOn,
		Weight:   0.9,
	}); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	// Search with graph DISABLED.
	resp, err := svc.Search(ctx, model.SearchRequest{
		Query:        "JWT RS256",
		IncludeGraph: boolPtr(false),
		Limit:        20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	for _, r := range resp.Results {
		if r.ID == neighborResp.ID {
			t.Errorf("neighbor memory appeared in results when include_graph=false")
		}
	}
}

// TestSearch_GraphExpansion_ConfigDisabled verifies that when ExpansionEnabled=false
// in config, graph expansion is skipped even when include_graph is nil (default).
func TestSearch_GraphExpansion_ConfigDisabled(t *testing.T) {
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
	cfg.Graph.ExpansionEnabled = false // global disable

	svc := service.NewMemoryService(ps, gs, cfg, "test/graph", embed.NopEmbedder{})
	ctx := context.Background()

	// Save a memory and a neighbor connected by a relation.
	seedResp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "JWT RS256 auth service",
		Content: "JWT authentication with RS256",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	neighborResp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Config disabled neighbor",
		Content: "No matching tokens here at all",
	})
	if err != nil {
		t.Fatalf("Save neighbor: %v", err)
	}

	authEnt, _ := ps.FindOrCreateEntity(ctx, "auth3", model.KindService, "test/graph")
	otherEnt, _ := ps.FindOrCreateEntity(ctx, "other3", model.KindService, "test/graph")
	_ = ps.LinkMemoryEntity(ctx, seedResp.ID, authEnt.ID, "subject")
	_ = ps.LinkMemoryEntity(ctx, neighborResp.ID, otherEnt.ID, "subject")
	_, _ = ps.CreateRelation(ctx, &model.Relation{
		SourceID: authEnt.ID,
		TargetID: otherEnt.ID,
		Type:     model.RelDependsOn,
		Weight:   0.9,
	})

	// Request with include_graph unset (nil = use config default = false).
	resp, err := svc.Search(ctx, model.SearchRequest{
		Query: "JWT RS256",
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	for _, r := range resp.Results {
		if r.ID == neighborResp.ID {
			t.Errorf("neighbor appeared when ExpansionEnabled=false in config")
		}
	}
}

// TestSearch_GraphExpansion_TouchRelations verifies that relations traversed
// during graph expansion have their last_traversed_at updated.
func TestSearch_GraphExpansion_TouchRelations(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedResp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "JWT RS256 auth",
		Content: "JWT authentication RS256",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	neighborResp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Key rotation for touch test",
		Content: "key rotation cryptography security",
	})
	if err != nil {
		t.Fatalf("Save neighbor: %v", err)
	}

	authEnt, _ := ps.FindOrCreateEntity(ctx, "auth-touch", model.KindService, "test/graph")
	keyEnt, _ := ps.FindOrCreateEntity(ctx, "key-touch", model.KindService, "test/graph")
	_ = ps.LinkMemoryEntity(ctx, seedResp.ID, authEnt.ID, "subject")
	_ = ps.LinkMemoryEntity(ctx, neighborResp.ID, keyEnt.ID, "subject")
	rel, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: authEnt.ID,
		TargetID: keyEnt.ID,
		Type:     model.RelDependsOn,
		Weight:   0.9,
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	_, err = svc.Search(ctx, model.SearchRequest{
		Query:        "JWT RS256",
		IncludeGraph: boolPtr(true),
		Limit:        20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// The relation should have last_traversed_at set now.
	rels, err := ps.GetRelationsFrom(ctx, authEnt.ID)
	if err != nil {
		t.Fatalf("GetRelationsFrom: %v", err)
	}
	var touched *model.Relation
	for _, r := range rels {
		if r.ID == rel.ID {
			touched = r
			break
		}
	}
	if touched == nil {
		t.Fatalf("relation %s not found after search", rel.ID)
	}
	if touched.LastTraversedAt.IsZero() {
		t.Errorf("relation %s LastTraversedAt is zero after graph expansion search", rel.ID)
	}
}
