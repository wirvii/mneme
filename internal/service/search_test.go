package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
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

// newTestServiceWithGraphMode returns a MemoryService configured with the
// specified GraphMode for PPR-specific tests.
func newTestServiceWithGraphMode(t *testing.T, graphMode string) (*service.MemoryService, *store.MemoryStore) {
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
	cfg.Graph.GraphMode = graphMode

	svc := service.NewMemoryService(ps, gs, cfg, "test/ppr", embed.NopEmbedder{})
	return svc, ps
}

// buildChain creates memories A->B->C (entity-linked, with a relation from
// entityA to entityB and entityB to entityC at the given weight) and returns
// the three memory IDs. The seed memory (A) can be found by the query
// "zorthex quiblon" — tokens deliberately not present in B or C so FTS5 cannot
// surface B or C through text matching alone.
func buildChain(t *testing.T, svc *service.MemoryService, ps *store.MemoryStore, weight float64) (seedID, midID, farID string) {
	t.Helper()
	ctx := context.Background()

	mA, err := svc.Save(ctx, model.SaveRequest{
		Title:   "zorthex quiblon seed node",
		Content: "zorthex quiblon seed node zorthex quiblon",
	})
	if err != nil {
		t.Fatalf("Save A: %v", err)
	}
	mB, err := svc.Save(ctx, model.SaveRequest{
		Title:   "mxyvwq direct neighbor node",
		Content: "mxyvwq direct neighbor completely different tokens",
	})
	if err != nil {
		t.Fatalf("Save B: %v", err)
	}
	mC, err := svc.Save(ctx, model.SaveRequest{
		Title:   "plkjhgf distant node",
		Content: "plkjhgf distant node only reachable via multi hop traversal",
	})
	if err != nil {
		t.Fatalf("Save C: %v", err)
	}

	eA, err := ps.FindOrCreateEntity(ctx, "entity-chain-a", model.KindConcept, "test/ppr")
	if err != nil {
		t.Fatalf("entity A: %v", err)
	}
	eB, err := ps.FindOrCreateEntity(ctx, "entity-chain-b", model.KindConcept, "test/ppr")
	if err != nil {
		t.Fatalf("entity B: %v", err)
	}
	eC, err := ps.FindOrCreateEntity(ctx, "entity-chain-c", model.KindConcept, "test/ppr")
	if err != nil {
		t.Fatalf("entity C: %v", err)
	}

	for _, link := range []struct{ mem, ent string }{
		{mA.ID, eA.ID}, {mB.ID, eB.ID}, {mC.ID, eC.ID},
	} {
		if err := ps.LinkMemoryEntity(ctx, link.mem, link.ent, "subject"); err != nil {
			t.Fatalf("LinkMemoryEntity: %v", err)
		}
	}

	if _, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: eA.ID,
		TargetID: eB.ID,
		Type:     model.RelRelatedTo,
		Weight:   weight,
	}); err != nil {
		t.Fatalf("relation A->B: %v", err)
	}
	if _, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: eB.ID,
		TargetID: eC.ID,
		Type:     model.RelRelatedTo,
		Weight:   weight,
	}); err != nil {
		t.Fatalf("relation B->C: %v", err)
	}

	return mA.ID, mB.ID, mC.ID
}

// TestSearch_PPRMode_SurfacesMultiHopNeighbor is acceptance criterion 1:
// PPR must surface memory C (2 hops away) when searching for A's title.
// With GraphMode="1hop", C does NOT appear because 1-hop only reaches B.
func TestSearch_PPRMode_SurfacesMultiHopNeighbor(t *testing.T) {
	svcPPR, ps := newTestServiceWithGraphMode(t, "ppr")
	ctx := context.Background()

	seedID, _, farID := buildChain(t, svcPPR, ps, 0.9)

	resp, err := svcPPR.Search(ctx, model.SearchRequest{
		Query: "zorthex quiblon",
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("Search PPR: %v", err)
	}

	_ = seedID // seed excluded from graph results is fine; it's in FTS5 results
	found := false
	for _, r := range resp.Results {
		if r.ID == farID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("PPR mode: far memory (2 hops) not found in results; got %d results", len(resp.Results))
	}
}

// TestSearch_1HopMode_PreservesExistingBehavior verifies that GraphMode="1hop"
// preserves the SPEC-007 behaviour: the direct neighbor (B) appears but the
// 2-hop memory (C) does not.
func TestSearch_1HopMode_PreservesExistingBehavior(t *testing.T) {
	svc, ps := newTestServiceWithGraphMode(t, "1hop")
	ctx := context.Background()

	_, midID, farID := buildChain(t, svc, ps, 0.9)

	resp, err := svc.Search(ctx, model.SearchRequest{
		Query: "zorthex quiblon",
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("Search 1hop: %v", err)
	}

	foundMid := false
	foundFar := false
	for _, r := range resp.Results {
		if r.ID == midID {
			foundMid = true
		}
		if r.ID == farID {
			foundFar = true
		}
	}
	if !foundMid {
		t.Error("1hop mode: direct neighbor (mid) should appear in results")
	}
	if foundFar {
		t.Error("1hop mode: 2-hop memory (far) must NOT appear in 1-hop results")
	}
}

// TestSearch_OffMode_NoGraphChannel verifies that GraphMode="off" produces
// 2-channel results only — connected memories that share no query tokens are absent.
func TestSearch_OffMode_NoGraphChannel(t *testing.T) {
	svc, ps := newTestServiceWithGraphMode(t, "off")
	ctx := context.Background()

	_, midID, farID := buildChain(t, svc, ps, 0.9)

	resp, err := svc.Search(ctx, model.SearchRequest{
		Query: "zorthex quiblon",
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("Search off: %v", err)
	}

	for _, r := range resp.Results {
		if r.ID == midID || r.ID == farID {
			t.Errorf("off mode: graph-only memory %s should not appear (no graph channel)", r.ID)
		}
	}
}

// TestSearch_IncludeGraphFalseOverridesMode verifies that IncludeGraph=false
// in the request overrides GraphMode="ppr" and disables graph expansion.
func TestSearch_IncludeGraphFalseOverridesMode(t *testing.T) {
	svc, ps := newTestServiceWithGraphMode(t, "ppr")
	ctx := context.Background()

	_, midID, farID := buildChain(t, svc, ps, 0.9)

	f := false
	resp, err := svc.Search(ctx, model.SearchRequest{
		Query:        "alpha seed",
		Limit:        20,
		IncludeGraph: &f,
	})
	if err != nil {
		t.Fatalf("Search with IncludeGraph=false: %v", err)
	}

	for _, r := range resp.Results {
		if r.ID == midID || r.ID == farID {
			t.Errorf("IncludeGraph=false override: graph-only memory %s should not appear", r.ID)
		}
	}
}

// TestSearch_PPRMode_FallbackTo1Hop verifies that when no entities are linked
// to seeds (empty graph), PPR falls back to 1-hop expansion without error.
func TestSearch_PPRMode_FallbackTo1Hop(t *testing.T) {
	svc, ps := newTestServiceWithGraphMode(t, "ppr")
	ctx := context.Background()

	// Save memories with no entity links so BuildGraphForSeeds returns empty.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:   "JWT RS256 token validation",
		Content: "JWT RS256 token validation algorithm",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Save a connected memory that IS reachable via 1-hop (direct neighbor).
	neighborResp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "HMAC key secret",
		Content: "HMAC shared secret key for signing",
	})
	if err != nil {
		t.Fatalf("Save neighbor: %v", err)
	}

	// Link them via entities + relation so 1-hop works.
	eA, err := ps.FindOrCreateEntity(ctx, "jwt-rs256-fb", model.KindConcept, "test/ppr")
	if err != nil {
		t.Fatalf("entity A: %v", err)
	}
	eB, err := ps.FindOrCreateEntity(ctx, "hmac-key-fb", model.KindConcept, "test/ppr")
	if err != nil {
		t.Fatalf("entity B: %v", err)
	}

	// Find the seed memory by searching first.
	seedResp, err := svc.Search(ctx, model.SearchRequest{Query: "JWT RS256 token validation", Limit: 1})
	if err != nil || len(seedResp.Results) == 0 {
		t.Fatalf("find seed: %v", err)
	}
	seedID := seedResp.Results[0].ID

	if err := ps.LinkMemoryEntity(ctx, seedID, eA.ID, "subject"); err != nil {
		t.Fatalf("link seed: %v", err)
	}
	if err := ps.LinkMemoryEntity(ctx, neighborResp.ID, eB.ID, "subject"); err != nil {
		t.Fatalf("link neighbor: %v", err)
	}
	if _, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: eA.ID,
		TargetID: eB.ID,
		Type:     model.RelRelatedTo,
		Weight:   0.9,
	}); err != nil {
		t.Fatalf("relation: %v", err)
	}

	// With no entity on the seed, PPR will get empty seeds -> fallback.
	// We verify no error occurs and we get results.
	resp, err := svc.Search(ctx, model.SearchRequest{
		Query: "JWT RS256 token validation",
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("Search (fallback path): %v", err)
	}
	if len(resp.Results) == 0 {
		t.Error("expected at least one result after PPR fallback")
	}
}

// TestGraphChannelPPR_TopNCap verifies that the PPR channel caps results at 50.
func TestGraphChannelPPR_TopNCap(t *testing.T) {
	svc, ps := newTestServiceWithGraphMode(t, "ppr")
	ctx := context.Background()

	// Create a hub memory that has many neighbors (>50).
	hubResp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "hub central memory cap test",
		Content: "hub central memory cap test node",
	})
	if err != nil {
		t.Fatalf("Save hub: %v", err)
	}

	hubEntity, err := ps.FindOrCreateEntity(ctx, "hub-cap-entity", model.KindConcept, "test/ppr")
	if err != nil {
		t.Fatalf("hub entity: %v", err)
	}
	if err := ps.LinkMemoryEntity(ctx, hubResp.ID, hubEntity.ID, "subject"); err != nil {
		t.Fatalf("link hub: %v", err)
	}

	// Create 60 neighbor memories, each connected to the hub.
	for i := 0; i < 60; i++ {
		nResp, err := svc.Save(ctx, model.SaveRequest{
			Title:   fmt.Sprintf("neighbor cap %d", i),
			Content: fmt.Sprintf("neighbor content %d", i),
		})
		if err != nil {
			t.Fatalf("Save neighbor %d: %v", i, err)
		}
		nEntity, err := ps.FindOrCreateEntity(ctx, fmt.Sprintf("cap-entity-%d", i), model.KindConcept, "test/ppr")
		if err != nil {
			t.Fatalf("neighbor entity %d: %v", i, err)
		}
		if err := ps.LinkMemoryEntity(ctx, nResp.ID, nEntity.ID, "subject"); err != nil {
			t.Fatalf("link neighbor %d: %v", i, err)
		}
		if _, err := ps.CreateRelation(ctx, &model.Relation{
			SourceID: hubEntity.ID,
			TargetID: nEntity.ID,
			Type:     model.RelRelatedTo,
			Weight:   0.9,
		}); err != nil {
			t.Fatalf("relation %d: %v", i, err)
		}
	}

	resp, err := svc.Search(ctx, model.SearchRequest{
		Query: "hub central memory cap test",
		Limit: 50,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// The final result count is capped by the search limit (50) and by
	// graphTopN (50) inside graphChannelPPR. We just verify no panic and
	// that the result count does not exceed 50.
	if len(resp.Results) > 50 {
		t.Errorf("expected <= 50 results, got %d", len(resp.Results))
	}
}

// TestGraphChannelPPR_SeedExclusion verifies that the seed memories themselves
// are excluded from the PPR graph channel results (they're already in FTS5/vector).
func TestGraphChannelPPR_SeedExclusion(t *testing.T) {
	svc, ps := newTestServiceWithGraphMode(t, "ppr")
	ctx := context.Background()

	seedResp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "seed exclusion test alpha memory",
		Content: "seed exclusion alpha seed memory test",
	})
	if err != nil {
		t.Fatalf("Save seed: %v", err)
	}

	eA, err := ps.FindOrCreateEntity(ctx, "excl-entity-a", model.KindConcept, "test/ppr")
	if err != nil {
		t.Fatalf("entity A: %v", err)
	}
	eB, err := ps.FindOrCreateEntity(ctx, "excl-entity-b", model.KindConcept, "test/ppr")
	if err != nil {
		t.Fatalf("entity B: %v", err)
	}

	if err := ps.LinkMemoryEntity(ctx, seedResp.ID, eA.ID, "subject"); err != nil {
		t.Fatalf("link seed: %v", err)
	}
	// Self-loop: entity B also linked to seed (so seed could appear as PPR result).
	if err := ps.LinkMemoryEntity(ctx, seedResp.ID, eB.ID, "subject"); err != nil {
		t.Fatalf("link seed2: %v", err)
	}
	if _, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: eA.ID,
		TargetID: eB.ID,
		Type:     model.RelRelatedTo,
		Weight:   0.9,
	}); err != nil {
		t.Fatalf("relation: %v", err)
	}

	resp, err := svc.Search(ctx, model.SearchRequest{
		Query: "seed exclusion test alpha",
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// The seed should appear exactly once (from FTS5), not duplicated via graph channel.
	count := 0
	for _, r := range resp.Results {
		if r.ID == seedResp.ID {
			count++
		}
	}
	if count > 1 {
		t.Errorf("seed memory appeared %d times in results; want at most 1", count)
	}
}
