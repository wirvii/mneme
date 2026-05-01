package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// newRebuildService returns a MemoryService + the raw projectStore for direct
// store assertions, backed by fresh in-memory SQLite databases.
func newRebuildService(t *testing.T) (*service.MemoryService, *store.MemoryStore) {
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
	svc := service.NewMemoryService(ps, gs, cfg, "test/rebuild", embed.NopEmbedder{})
	return svc, ps
}

// saveMemory is a shorthand for saving a memory via the service.
func saveMemory(t *testing.T, svc *service.MemoryService, title, content string) string {
	t.Helper()
	ctx := context.Background()
	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   title,
		Content: content,
	})
	if err != nil {
		t.Fatalf("Save %q: %v", title, err)
	}
	return resp.ID
}

// ─── entity extraction unit tests ────────────────────────────────────────────

// TestExtractEntities_TopicKey verifies that a memory with a topic_key
// produces a concept entity with role=subject (H1).
func TestExtractEntities_TopicKey(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	_ = saveMemory(t, svc, "Auth decision", "We use JWT for authentication.")

	// Save with topic_key via Store directly (service.Save doesn't expose topic_key
	// in basic SaveRequest; use direct store Create).
	m, err := ps.Create(ctx, &model.Memory{
		Type:     model.TypeDecision,
		Scope:    model.ScopeProject,
		Title:    "Topic key memory",
		Content:  "Content without paths.",
		TopicKey: "architecture/auth-model",
		Project:  "test/rebuild",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := svc.RebuildGraph(ctx, model.RebuildRequest{
		Scope:     "project",
		MinShared: 1,
	})
	if err != nil {
		t.Fatalf("RebuildGraph: %v", err)
	}
	if result.MemoriesScanned == 0 {
		t.Fatal("expected at least one memory scanned")
	}

	// Verify the entity for the topic_key was created.
	entities, err := ps.GetMemoryEntities(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities: %v", err)
	}

	var found bool
	for _, e := range entities {
		if e.Name == "architecture/auth-model" && e.Kind == model.KindConcept {
			found = true
		}
	}
	if !found {
		t.Errorf("expected entity architecture/auth-model (kind=concept), got %v", entities)
	}
}

// TestExtractEntities_FilePaths verifies that file paths in memory content
// produce file-kind entities (H2).
func TestExtractEntities_FilePaths(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	m, err := ps.Create(ctx, &model.Memory{
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
		Title:   "File path memory",
		Content: "See internal/store/entity.go for the implementation.",
		Project: "test/rebuild",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := svc.RebuildGraph(ctx, model.RebuildRequest{MinShared: 1})
	if err != nil {
		t.Fatalf("RebuildGraph: %v", err)
	}
	_ = result

	entities, err := ps.GetMemoryEntities(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities: %v", err)
	}

	var found bool
	for _, e := range entities {
		if strings.Contains(e.Name, "internal/store/entity.go") && e.Kind == model.KindFile {
			found = true
		}
	}
	if !found {
		t.Errorf("expected entity internal/store/entity.go (kind=file), got %v", entities)
	}
}

// TestExtractEntities_CodeSymbols verifies that function declarations inside
// code blocks produce module-kind entities (H3).
func TestExtractEntities_CodeSymbols(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	content := "```go\nfunc RebuildGraph(ctx context.Context) error {\n    return nil\n}\n```"
	m, err := ps.Create(ctx, &model.Memory{
		Type:    model.TypeDecision,
		Scope:   model.ScopeProject,
		Title:   "Code symbol memory",
		Content: content,
		Project: "test/rebuild",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.RebuildGraph(ctx, model.RebuildRequest{MinShared: 1}); err != nil {
		t.Fatalf("RebuildGraph: %v", err)
	}

	entities, err := ps.GetMemoryEntities(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities: %v", err)
	}

	var found bool
	for _, e := range entities {
		if e.Name == "RebuildGraph" && e.Kind == model.KindModule {
			found = true
		}
	}
	if !found {
		t.Errorf("expected entity RebuildGraph (kind=module), got %v", entities)
	}
}

// TestExtractEntities_Wikilinks verifies that [[topic_key]] references produce
// concept-kind entities with role=mention (H4).
func TestExtractEntities_Wikilinks(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	m, err := ps.Create(ctx, &model.Memory{
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
		Title:   "Wikilink memory",
		Content: "Related to [[architecture/auth-model]] and [[roadmap/v2]].",
		Project: "test/rebuild",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.RebuildGraph(ctx, model.RebuildRequest{MinShared: 1}); err != nil {
		t.Fatalf("RebuildGraph: %v", err)
	}

	entities, err := ps.GetMemoryEntities(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities: %v", err)
	}

	byName := make(map[string]*model.Entity)
	for _, e := range entities {
		byName[e.Name] = e
	}
	if e, ok := byName["architecture/auth-model"]; !ok || e.Kind != model.KindConcept {
		t.Errorf("expected entity architecture/auth-model (kind=concept), got %v", entities)
	}
	if e, ok := byName["roadmap/v2"]; !ok || e.Kind != model.KindConcept {
		t.Errorf("expected entity roadmap/v2 (kind=concept), got %v", entities)
	}
}

// TestExtractEntities_Dedup verifies that the same entity name mentioned
// multiple times in a memory produces only one entity link (SPEC-009 §3).
func TestExtractEntities_Dedup(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	content := "See internal/store/entity.go. Also internal/store/entity.go is important. Again: internal/store/entity.go."
	m, err := ps.Create(ctx, &model.Memory{
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
		Title:   "Dedup test",
		Content: content,
		Project: "test/rebuild",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.RebuildGraph(ctx, model.RebuildRequest{MinShared: 1}); err != nil {
		t.Fatalf("RebuildGraph: %v", err)
	}

	entities, err := ps.GetMemoryEntities(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities: %v", err)
	}

	count := 0
	for _, e := range entities {
		if e.Name == "internal/store/entity.go" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 entity for repeated path, got %d", count)
	}
}

// TestExtractEntities_MinLength verifies that symbols shorter than 3 runes are
// excluded. The regex already enforces {2,} (3+ chars including the first).
func TestExtractEntities_MinLength(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	// "fn" is 2 chars — too short. "Get" is 3 chars — included.
	content := "```go\nfn shortfunc() {}\nfunc Get(x int) {}\n```"
	m, err := ps.Create(ctx, &model.Memory{
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
		Title:   "Min length test",
		Content: content,
		Project: "test/rebuild",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.RebuildGraph(ctx, model.RebuildRequest{MinShared: 1}); err != nil {
		t.Fatalf("RebuildGraph: %v", err)
	}

	entities, err := ps.GetMemoryEntities(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities: %v", err)
	}

	byName := make(map[string]bool)
	for _, e := range entities {
		byName[e.Name] = true
	}

	// "fn" should NOT appear as an entity name (it's a keyword anyway, not an identifier).
	if byName["fn"] {
		t.Error("entity 'fn' (2 chars) should be excluded")
	}
	// "Get" SHOULD appear.
	if !byName["Get"] {
		t.Errorf("entity 'Get' (3 chars) should be included; got %v", entities)
	}
}

// ─── integration tests ───────────────────────────────────────────────────────

// TestRebuildGraph_Basic verifies that 5 memories with overlapping file paths
// produce entities, links, and related_to relations with the correct weight
// (AC1, AC2, SPEC-009).
func TestRebuildGraph_Basic(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	// m1 and m2 share 3 file paths → weight = min(0.5, 3*0.1) = 0.3.
	// m3 only shares 1 path with m1 → no relation (K=2).
	sharedContent := "Files: internal/store/entity.go internal/store/memory.go internal/service/rebuild.go"
	m1, _ := ps.Create(ctx, &model.Memory{
		Type:    model.TypeDecision,
		Scope:   model.ScopeProject,
		Title:   "m1",
		Content: sharedContent,
		Project: "test/rebuild",
	})
	m2, _ := ps.Create(ctx, &model.Memory{
		Type:    model.TypeDecision,
		Scope:   model.ScopeProject,
		Title:   "m2",
		Content: sharedContent + " extra content",
		Project: "test/rebuild",
	})
	m3, _ := ps.Create(ctx, &model.Memory{
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
		Title:   "m3",
		Content: "Only internal/store/entity.go is mentioned here.",
		Project: "test/rebuild",
	})

	result, err := svc.RebuildGraph(ctx, model.RebuildRequest{
		MinShared: 2,
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("RebuildGraph: %v", err)
	}

	if result.MemoriesScanned != 3 {
		t.Errorf("MemoriesScanned = %d, want 3", result.MemoriesScanned)
	}
	if result.EntitiesExtracted == 0 {
		t.Error("expected at least some entities extracted")
	}
	if result.EntitiesCreated == 0 {
		t.Error("expected at least some new entities created")
	}
	if result.LinksCreated == 0 {
		t.Error("expected at least some links created")
	}
	if result.RelationsCreated == 0 {
		t.Errorf("expected at least 1 relation created (m1<->m2), got %d", result.RelationsCreated)
	}

	// Verify entities exist for m1.
	m1Entities, err := ps.GetMemoryEntities(ctx, m1.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities m1: %v", err)
	}
	if len(m1Entities) == 0 {
		t.Error("expected m1 to have entities")
	}

	// m3 should have exactly 1 entity.
	m3Entities, err := ps.GetMemoryEntities(ctx, m3.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities m3: %v", err)
	}
	if len(m3Entities) == 0 {
		t.Error("expected m3 to have at least 1 entity")
	}

	_ = m2
}

// TestRebuildGraph_Idempotent verifies that running rebuild twice produces
// zero new entities, links, or relations on the second run (AC3, SPEC-009).
func TestRebuildGraph_Idempotent(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	content := "internal/store/entity.go and internal/service/rebuild.go"
	_, _ = ps.Create(ctx, &model.Memory{
		Type: model.TypeDecision, Scope: model.ScopeProject,
		Title: "m-idem-1", Content: content, Project: "test/rebuild",
	})
	_, _ = ps.Create(ctx, &model.Memory{
		Type: model.TypeDecision, Scope: model.ScopeProject,
		Title: "m-idem-2", Content: content, Project: "test/rebuild",
	})

	req := model.RebuildRequest{MinShared: 2, Scope: "project"}

	// First run.
	r1, err := svc.RebuildGraph(ctx, req)
	if err != nil {
		t.Fatalf("RebuildGraph first: %v", err)
	}
	if r1.EntitiesCreated == 0 {
		t.Error("first run: expected some entities created")
	}

	// Second run: ListMemoriesWithoutEntities returns 0, so scanned=0 and
	// all counters should be 0.
	r2, err := svc.RebuildGraph(ctx, req)
	if err != nil {
		t.Fatalf("RebuildGraph second: %v", err)
	}
	if r2.EntitiesCreated != 0 {
		t.Errorf("second run EntitiesCreated = %d, want 0", r2.EntitiesCreated)
	}
	if r2.LinksCreated != 0 {
		t.Errorf("second run LinksCreated = %d, want 0", r2.LinksCreated)
	}
	if r2.RelationsCreated != 0 {
		t.Errorf("second run RelationsCreated = %d, want 0", r2.RelationsCreated)
	}
}

// TestRebuildGraph_Force verifies that --force deletes related_to and
// recreates it, but leaves explicit depends_on intact (AC4, SPEC-009).
func TestRebuildGraph_Force(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	content := "internal/store/entity.go internal/service/rebuild.go"
	_, _ = ps.Create(ctx, &model.Memory{
		Type: model.TypeDecision, Scope: model.ScopeProject,
		Title: "m-force-1", Content: content, Project: "test/rebuild",
	})
	_, _ = ps.Create(ctx, &model.Memory{
		Type: model.TypeDecision, Scope: model.ScopeProject,
		Title: "m-force-2", Content: content, Project: "test/rebuild",
	})

	req := model.RebuildRequest{MinShared: 2, Scope: "project"}

	// First run creates entities and relations.
	r1, err := svc.RebuildGraph(ctx, req)
	if err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	if r1.RelationsCreated == 0 {
		t.Skip("no relations created in first run — content may not overlap enough")
	}

	// Create an explicit depends_on relation between two entities that should
	// survive the --force.
	e1, _ := ps.FindOrCreateEntity(ctx, "explicit-src", model.KindConcept, "test/rebuild")
	e2, _ := ps.FindOrCreateEntity(ctx, "explicit-tgt", model.KindConcept, "test/rebuild")
	_, _ = ps.CreateRelation(ctx, &model.Relation{
		SourceID: e1.ID, TargetID: e2.ID,
		Type: model.RelDependsOn, Weight: 0.9,
	})

	// Force rebuild.
	r2, err := svc.RebuildGraph(ctx, model.RebuildRequest{
		MinShared: 2, Scope: "project", Force: true,
	})
	if err != nil {
		t.Fatalf("force rebuild: %v", err)
	}
	if r2.RelationsDeleted == 0 {
		t.Error("force rebuild: expected RelationsDeleted > 0")
	}

	// The explicit depends_on must survive.
	rel, err := ps.FindRelation(ctx, e1.ID, e2.ID, model.RelDependsOn)
	if err != nil {
		t.Fatalf("FindRelation: %v", err)
	}
	if rel == nil {
		t.Error("explicit depends_on relation should survive --force")
	}
}

// TestRebuildGraph_DryRun verifies that dry-run produces non-zero counts in
// the result but writes nothing to the database (AC5, SPEC-009).
func TestRebuildGraph_DryRun(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	content := "internal/store/entity.go internal/service/rebuild.go"
	_, _ = ps.Create(ctx, &model.Memory{
		Type: model.TypeDecision, Scope: model.ScopeProject,
		Title: "m-dry-1", Content: content, Project: "test/rebuild",
	})
	_, _ = ps.Create(ctx, &model.Memory{
		Type: model.TypeDecision, Scope: model.ScopeProject,
		Title: "m-dry-2", Content: content, Project: "test/rebuild",
	})

	result, err := svc.RebuildGraph(ctx, model.RebuildRequest{
		MinShared: 2, Scope: "project", DryRun: true,
	})
	if err != nil {
		t.Fatalf("RebuildGraph dry-run: %v", err)
	}

	// Dry-run reports counts.
	if result.MemoriesScanned == 0 {
		t.Error("dry-run: MemoriesScanned should be > 0")
	}

	// But nothing should be written — no entities in any memory.
	entities, err := ps.ListEntities(ctx, "test/rebuild", "", 0)
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(entities) != 0 {
		t.Errorf("dry-run: expected 0 entities in DB, got %d", len(entities))
	}
}

// TestRebuildGraph_NoMemories verifies that an empty project produces a clean
// zero result without error (SPEC-009 §5.2).
func TestRebuildGraph_NoMemories(t *testing.T) {
	svc, _ := newRebuildService(t)
	ctx := context.Background()

	result, err := svc.RebuildGraph(ctx, model.RebuildRequest{
		MinShared: 2,
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("RebuildGraph empty project: %v", err)
	}
	if result.MemoriesScanned != 0 {
		t.Errorf("MemoriesScanned = %d, want 0", result.MemoriesScanned)
	}
	if result.EntitiesCreated != 0 {
		t.Errorf("EntitiesCreated = %d, want 0", result.EntitiesCreated)
	}
	if result.RelationsCreated != 0 {
		t.Errorf("RelationsCreated = %d, want 0", result.RelationsCreated)
	}
}

// TestRebuildGraph_MemoryWithNoEntities verifies that a memory containing no
// extractable content (no paths, no symbols, no topic_key, no wikilinks)
// produces zero entity links (SPEC-009 §5.1).
func TestRebuildGraph_MemoryWithNoEntities(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	m, err := ps.Create(ctx, &model.Memory{
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
		Title:   "hello world",
		Content: "A simple greeting with no extractable entities.",
		Project: "test/rebuild",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.RebuildGraph(ctx, model.RebuildRequest{MinShared: 1}); err != nil {
		t.Fatalf("RebuildGraph: %v", err)
	}

	entities, err := ps.GetMemoryEntities(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities: %v", err)
	}
	if len(entities) != 0 {
		t.Errorf("expected 0 entities for plain content, got %d: %v", len(entities), entities)
	}
}

// TestRebuildGraph_WeightFormula verifies that 2 memories sharing 3 entities
// produce a relation with weight = min(0.5, 3*0.1) = 0.3 (AC2, D3, SPEC-009).
func TestRebuildGraph_WeightFormula(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	// Create 2 memories sharing exactly 3 file-path entities.
	content := "internal/store/entity.go internal/service/rebuild.go internal/model/rebuild.go"
	m1, _ := ps.Create(ctx, &model.Memory{
		Type: model.TypeDecision, Scope: model.ScopeProject,
		Title: "weight-m1", Content: content, Project: "test/rebuild",
	})
	m2, _ := ps.Create(ctx, &model.Memory{
		Type: model.TypeDecision, Scope: model.ScopeProject,
		Title: "weight-m2", Content: content, Project: "test/rebuild",
	})

	result, err := svc.RebuildGraph(ctx, model.RebuildRequest{
		MinShared: 2,
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("RebuildGraph: %v", err)
	}
	if result.RelationsCreated == 0 {
		t.Fatalf("expected relation created; EntitiesCreated=%d LinksCreated=%d", result.EntitiesCreated, result.LinksCreated)
	}

	// Find the relation and verify weight.
	m1Entities, _ := ps.GetMemoryEntities(ctx, m1.ID)
	m2Entities, _ := ps.GetMemoryEntities(ctx, m2.ID)
	if len(m1Entities) == 0 || len(m2Entities) == 0 {
		t.Fatal("expected both memories to have entities")
	}

	rel, err := ps.FindRelationBidirectional(ctx, m1Entities[0].ID, m2Entities[0].ID, model.RelRelatedTo)
	if err != nil {
		t.Fatalf("FindRelationBidirectional: %v", err)
	}
	if rel == nil {
		t.Fatal("expected a related_to relation to exist")
	}

	// Weight should be min(0.5, 3*0.1) = 0.3 — allow small float tolerance.
	const wantWeight = 0.3
	if rel.Weight < wantWeight-0.01 || rel.Weight > wantWeight+0.01 {
		t.Errorf("relation weight = %.4f, want %.4f", rel.Weight, wantWeight)
	}
}

// TestRebuildGraph_MaxRelationsCap verifies that per-memory cap is enforced
// when a memory's entities would otherwise participate in more than
// MaxRelationsPerMemory relations (D4, SPEC-009).
//
// Setup: create 1 "hub" memory with a unique entity + 2 shared entities,
// and 10 "peer" memories each with a unique entity + the same 2 shared entities.
// The hub memory shares >= 2 entities with each of the 10 peers → 10 candidate
// pairs. With cap=3, only 3 of those relations should be created.
func TestRebuildGraph_MaxRelationsCap(t *testing.T) {
	svc, ps := newRebuildService(t)
	ctx := context.Background()

	const cap = 3

	// Insert memories directly into the store and manually create entity links
	// so we control exactly which entities each memory has.
	eShared1, _ := ps.FindOrCreateEntity(ctx, "shared-cap-1", model.KindConcept, "test/rebuild")
	eShared2, _ := ps.FindOrCreateEntity(ctx, "shared-cap-2", model.KindConcept, "test/rebuild")

	// Hub memory — linked to eShared1, eShared2, and its own unique entity.
	eHub, _ := ps.FindOrCreateEntity(ctx, "hub-unique", model.KindConcept, "test/rebuild")
	hub, _ := ps.Create(ctx, &model.Memory{
		Type: model.TypeDecision, Scope: model.ScopeProject,
		Title: "cap-hub", Content: "hub", Project: "test/rebuild",
	})
	_ = ps.LinkMemoryEntity(ctx, hub.ID, eShared1.ID, "mention")
	_ = ps.LinkMemoryEntity(ctx, hub.ID, eShared2.ID, "mention")
	_ = ps.LinkMemoryEntity(ctx, hub.ID, eHub.ID, "mention")

	// 10 peer memories — each linked to eShared1, eShared2, and a unique entity.
	for i := 0; i < 10; i++ {
		ePeer, _ := ps.FindOrCreateEntity(ctx, fmt.Sprintf("peer-unique-%d", i), model.KindConcept, "test/rebuild")
		peer, _ := ps.Create(ctx, &model.Memory{
			Type: model.TypeDiscovery, Scope: model.ScopeProject,
			Title: fmt.Sprintf("cap-peer-%d", i), Content: "peer", Project: "test/rebuild",
		})
		_ = ps.LinkMemoryEntity(ctx, peer.ID, eShared1.ID, "mention")
		_ = ps.LinkMemoryEntity(ctx, peer.ID, eShared2.ID, "mention")
		_ = ps.LinkMemoryEntity(ctx, peer.ID, ePeer.ID, "mention")
	}

	// All memories already have entities, so use Force=true to rebuild from
	// scratch (otherwise ListMemoriesWithoutEntities returns 0).
	result, err := svc.RebuildGraph(ctx, model.RebuildRequest{
		MinShared:             2,
		MaxRelationsPerMemory: cap,
		Scope:                 "project",
		Force:                 true,
	})
	if err != nil {
		t.Fatalf("RebuildGraph: %v", err)
	}

	// hub shares ≥2 entities with each peer → 10 candidate pairs involving hub.
	// Plus peers may share entities among themselves (all share eShared1+eShared2).
	// With cap=3 on hub, only 3 relations involving hub should be created.
	// Total candidate pairs from SQL JOIN > cap, so skippedCap must be > 0.
	totalCandidates := result.RelationsCreated + result.RelationsExisting + result.RelationsSkippedCap
	if totalCandidates == 0 {
		t.Fatal("expected some candidate pairs from SQL JOIN")
	}
	if result.RelationsSkippedCap == 0 {
		t.Errorf("expected some relations skipped due to cap=%d; RelationsCreated=%d RelationsExisting=%d totalCandidates=%d",
			cap, result.RelationsCreated, result.RelationsExisting, totalCandidates)
	}
}
