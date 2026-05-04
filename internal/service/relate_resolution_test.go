package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

// SPEC-031 tests: hybrid resolution + auto-link in mem_relate.

// TestRelate_ResolvesTopicKeyToMemory verifies that when both endpoints are
// existing topic_keys, mem_relate returns the memory IDs and links each memory
// to its proxy entity via memory_entities. The relation must be reachable from
// mem_explore as distance=1.
func TestRelate_ResolvesTopicKeyToMemory(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	a, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Backend hexagonal",
		Content:  "A document about backend architecture.",
		TopicKey: "architecture/backend-modular-hexagonal",
	})
	if err != nil {
		t.Fatalf("save A: %v", err)
	}
	b, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Bounded contexts",
		Content:  "Bounded contexts in DDD.",
		TopicKey: "architecture/bounded-contexts",
	})
	if err != nil {
		t.Fatalf("save B: %v", err)
	}

	resp, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "architecture/backend-modular-hexagonal",
		Target:   "architecture/bounded-contexts",
		Relation: model.RelReferences,
	})
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}
	if !resp.Created {
		t.Fatal("expected Created=true")
	}

	srcEntities, err := ps.GetMemoryEntities(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities A: %v", err)
	}
	if !entityListContains(srcEntities, resp.SourceID) {
		t.Errorf("source memory %s is not linked to source entity %s", a.ID, resp.SourceID)
	}

	tgtEntities, err := ps.GetMemoryEntities(ctx, b.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities B: %v", err)
	}
	if !entityListContains(tgtEntities, resp.TargetID) {
		t.Errorf("target memory %s is not linked to target entity %s", b.ID, resp.TargetID)
	}

	depthOne := 1
	exp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:  "architecture/backend-modular-hexagonal",
		Depth: &depthOne,
	})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if exp.SeedID != a.ID {
		t.Errorf("seed_id = %q, want %q", exp.SeedID, a.ID)
	}
	if len(exp.Nodes) == 0 {
		t.Fatal("expected at least one explored node, got 0")
	}
	found := false
	for _, n := range exp.Nodes {
		if n.MemoryID == b.ID {
			found = true
			if n.Distance != 1 {
				t.Errorf("expected distance=1, got %d", n.Distance)
			}
		}
	}
	if !found {
		t.Errorf("expected explored nodes to include target memory %s", b.ID)
	}
}

// TestRelate_LegacyEntitySemanticPreserved verifies that callers passing an
// explicit non-default kind retain the entity-only semantics: no topic_key
// resolution is attempted, no memory link is created, and the relation
// connects two entity-only nodes.
func TestRelate_LegacyEntitySemanticPreserved(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	resp, err := svc.Relate(ctx, model.RelateRequest{
		Source:     "core-srv",
		SourceKind: model.KindService,
		Target:     "redis",
		TargetKind: model.KindLibrary,
		Relation:   model.RelUses,
	})
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}
	if !resp.Created {
		t.Fatal("expected Created=true")
	}

	memIDs, err := ps.GetEntityMemoryIDs(ctx, resp.SourceID)
	if err != nil {
		t.Fatalf("GetEntityMemoryIDs source: %v", err)
	}
	if len(memIDs) != 0 {
		t.Errorf("legacy entity should have 0 memory links, got %d", len(memIDs))
	}
}

// TestRelate_ResolvesUUIDToMemory verifies that passing a memory's full UUID
// resolves to the memory and links the proxy entity.
func TestRelate_ResolvesUUIDToMemory(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	a, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Source memory",
		Content:  "source",
		TopicKey: "test/source",
	})
	if err != nil {
		t.Fatalf("save A: %v", err)
	}
	b, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Target memory",
		Content:  "target",
		TopicKey: "test/target",
	})
	if err != nil {
		t.Fatalf("save B: %v", err)
	}

	resp, err := svc.Relate(ctx, model.RelateRequest{
		Source:   a.ID,
		Target:   b.ID,
		Relation: model.RelDependsOn,
	})
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}

	srcEntities, err := ps.GetMemoryEntities(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities A: %v", err)
	}
	if !entityListContains(srcEntities, resp.SourceID) {
		t.Errorf("source memory not linked to its proxy entity")
	}
}

// TestRelate_ResolvesUUIDPrefixToMemory verifies that an 8-hex prefix of a
// memory ID is resolved to the same memory the full UUID would resolve to.
func TestRelate_ResolvesUUIDPrefixToMemory(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	a, err := svc.Save(ctx, model.SaveRequest{
		Title:    "A",
		Content:  "a",
		TopicKey: "prefix/a",
	})
	if err != nil {
		t.Fatalf("save A: %v", err)
	}
	b, err := svc.Save(ctx, model.SaveRequest{
		Title:    "B",
		Content:  "b",
		TopicKey: "prefix/b",
	})
	if err != nil {
		t.Fatalf("save B: %v", err)
	}

	// Use a 20-hex prefix (covers timestamp + part of random) so UUIDv7
	// collisions between near-simultaneous saves do not yield ErrAmbiguousSeed.
	prefixA := strings.ReplaceAll(a.ID, "-", "")[:20]
	prefixB := strings.ReplaceAll(b.ID, "-", "")[:20]

	resp, err := svc.Relate(ctx, model.RelateRequest{
		Source:   prefixA,
		Target:   prefixB,
		Relation: model.RelRelatedTo,
	})
	if err != nil {
		t.Fatalf("Relate by prefix: %v", err)
	}

	srcEntities, err := ps.GetMemoryEntities(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities A: %v", err)
	}
	if !entityListContains(srcEntities, resp.SourceID) {
		t.Errorf("source memory not linked via 12-hex prefix path")
	}
}

// TestRelate_MixedTopicKeyAndEntityName verifies that one endpoint can be a
// topic_key while the other is a brand-new entity name; both resolve correctly
// in the same call.
func TestRelate_MixedTopicKeyAndEntityName(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	mem, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Memory M",
		Content:  "m",
		TopicKey: "test/m",
	})
	if err != nil {
		t.Fatalf("save M: %v", err)
	}

	resp, err := svc.Relate(ctx, model.RelateRequest{
		Source:     "test/m",
		Target:     "Postgres",
		TargetKind: model.KindLibrary,
		Relation:   model.RelUses,
	})
	if err != nil {
		t.Fatalf("Relate mixed: %v", err)
	}

	srcEntities, err := ps.GetMemoryEntities(ctx, mem.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities source: %v", err)
	}
	if !entityListContains(srcEntities, resp.SourceID) {
		t.Errorf("memory side should be linked")
	}

	tgtMems, err := ps.GetEntityMemoryIDs(ctx, resp.TargetID)
	if err != nil {
		t.Fatalf("GetEntityMemoryIDs target: %v", err)
	}
	if len(tgtMems) != 0 {
		t.Errorf("entity side should have no memory links, got %d", len(tgtMems))
	}
}

// TestRelate_IdempotentLinkOnRepeatedCall verifies that calling Relate twice
// with the same memory pair is idempotent both for the relation row and for
// the memory_entities link rows.
func TestRelate_IdempotentLinkOnRepeatedCall(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	a, err := svc.Save(ctx, model.SaveRequest{
		Title:    "A",
		Content:  "a",
		TopicKey: "idem/a",
	})
	if err != nil {
		t.Fatalf("save A: %v", err)
	}
	if _, err := svc.Save(ctx, model.SaveRequest{
		Title:    "B",
		Content:  "b",
		TopicKey: "idem/b",
	}); err != nil {
		t.Fatalf("save B: %v", err)
	}

	first, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "idem/a",
		Target:   "idem/b",
		Relation: model.RelDependsOn,
	})
	if err != nil {
		t.Fatalf("Relate first: %v", err)
	}
	second, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "idem/a",
		Target:   "idem/b",
		Relation: model.RelDependsOn,
	})
	if err != nil {
		t.Fatalf("Relate second: %v", err)
	}
	if second.Created {
		t.Error("expected Created=false on second call")
	}
	if first.RelationID != second.RelationID {
		t.Errorf("relation IDs differ: first=%s second=%s", first.RelationID, second.RelationID)
	}

	srcEntities, err := ps.GetMemoryEntities(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities A: %v", err)
	}
	if !entityListContains(srcEntities, first.SourceID) {
		t.Error("idempotent link missing on memory A")
	}
}

// TestRelate_CrossScopeRejected verifies that relating a global-scoped source
// memory to a project-scoped target returns ErrCrossScopeRelation.
func TestRelate_CrossScopeRejected(t *testing.T) {
	svc, _ := newTestServiceWithGraph(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Global mem",
		Content:  "g",
		Scope:    model.ScopeGlobal,
		TopicKey: "global/preference",
	})
	if err != nil {
		t.Fatalf("save global: %v", err)
	}
	_, err = svc.Save(ctx, model.SaveRequest{
		Title:    "Project mem",
		Content:  "p",
		Scope:    model.ScopeProject,
		TopicKey: "project/decision",
	})
	if err != nil {
		t.Fatalf("save project: %v", err)
	}

	_, relErr := svc.Relate(ctx, model.RelateRequest{
		Source:   "global/preference",
		Target:   "project/decision",
		Relation: model.RelRelatedTo,
	})
	if !errors.Is(relErr, model.ErrCrossScopeRelation) {
		t.Errorf("expected ErrCrossScopeRelation, got %v", relErr)
	}
}

// TestRelate_TopicKeyNotFoundFallsBackToEntity verifies that a topic_key-shaped
// string that does not correspond to any memory falls back to creating a
// concept entity (no zombie alarm — this is the legitimate path).
func TestRelate_TopicKeyNotFoundFallsBackToEntity(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	resp, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "team/concept-a",
		Target:   "team/concept-b",
		Relation: model.RelRelatedTo,
	})
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}
	if !resp.Created {
		t.Fatal("expected Created=true")
	}
	srcEnt, err := ps.GetEntity(ctx, resp.SourceID)
	if err != nil {
		t.Fatalf("GetEntity source: %v", err)
	}
	if srcEnt.Kind != model.KindConcept {
		t.Errorf("expected KindConcept, got %v", srcEnt.Kind)
	}
}

// entityListContains reports whether any entity in es has the given id.
func entityListContains(es []*model.Entity, id string) bool {
	for _, e := range es {
		if e.ID == id {
			return true
		}
	}
	return false
}

// Compile-time check that store-side types are still used inside this file
// so the import isn't dropped accidentally during refactors.
var _ = (*store.MemoryStore)(nil)
var _ = service.CleanupOrphanRelationsRequest{}
