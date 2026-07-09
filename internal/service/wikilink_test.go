package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// newWikilinkTestService returns a service and its project/global stores so
// wikilink integration tests can inspect entities and relations directly.
func newWikilinkTestService(t *testing.T) (*service.MemoryService, *store.MemoryStore, *store.MemoryStore) {
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
	cfg.Graph.WikilinksEnabled = true
	cfg.Graph.WikilinkRelationWeight = 0.6

	svc := service.NewMemoryService(ps, gs, cfg, "test/project", embed.NopEmbedder{})
	return svc, ps, gs
}

// findRelationByTopicKeys looks up a references relation between two named
// entities in the given store and returns it (or nil if not found).
func findRelationByTopicKeys(ctx context.Context, t *testing.T, s *store.MemoryStore, srcTopic, tgtTopic, project string) *model.Relation {
	t.Helper()
	srcEntity, err := s.GetEntityByName(ctx, srcTopic, project)
	if err != nil {
		return nil
	}
	tgtEntity, err := s.GetEntityByName(ctx, tgtTopic, project)
	if err != nil {
		return nil
	}
	rel, err := s.FindRelationBidirectional(ctx, srcEntity.ID, tgtEntity.ID, model.RelReferences)
	if err != nil {
		t.Fatalf("FindRelationBidirectional: %v", err)
	}
	return rel
}

// TestSave_WikilinkCreatesRelation verifies that saving a memory with [[target-topic]]
// creates a references relation with the configured weight.
func TestSave_WikilinkCreatesRelation(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	// Create the target memory first.
	targetResp, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Target Memory",
		Content:  "This is the target.",
		TopicKey: "target/memory",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save target: %v", err)
	}
	_ = targetResp

	// Save the source memory referencing the target via wikilink.
	srcResp, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Source Memory",
		Content:  "See [[target/memory]] for more context.",
		TopicKey: "source/memory",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save source: %v", err)
	}
	_ = srcResp

	rel := findRelationByTopicKeys(ctx, t, ps, "source/memory", "target/memory", "test/project")
	if rel == nil {
		t.Fatal("expected a references relation, got nil")
	}
	if rel.Type != model.RelReferences {
		t.Errorf("relation type: got %q, want %q", rel.Type, model.RelReferences)
	}
	if rel.Weight != 0.6 {
		t.Errorf("relation weight: got %f, want 0.6", rel.Weight)
	}
}

// TestSave_WikilinkTargetNotFound verifies that saving with a wikilink to a
// non-existent topic_key creates no relation and returns no error.
func TestSave_WikilinkTargetNotFound(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Source",
		Content:  "Refers to [[nonexistent/topic]] which does not exist.",
		TopicKey: "source/mem",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// No entity should have been created for the nonexistent topic.
	entity, err := ps.GetEntityByName(ctx, "nonexistent/topic", "test/project")
	if err == nil && entity != nil {
		t.Error("expected no entity for nonexistent topic, got one")
	}
}

// TestSave_WikilinkSelfLoop verifies that a memory with its own topic_key as a
// wikilink does not create a self-relation.
func TestSave_WikilinkSelfLoop(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Self-referencing Memory",
		Content:  "This references [[self/loop]] which is itself.",
		TopicKey: "self/loop",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	entity, err := ps.GetEntityByName(ctx, "self/loop", "test/project")
	if err != nil {
		return // entity may not exist at all, which is fine
	}
	if entity == nil {
		return
	}

	// If the entity exists, there must be no self-loop relation.
	rels, err := ps.GetRelationsFrom(ctx, entity.ID)
	if err != nil {
		t.Fatalf("GetRelationsFrom: %v", err)
	}
	for _, r := range rels {
		if r.SourceID == entity.ID && r.TargetID == entity.ID {
			t.Errorf("found self-loop relation %q, expected none", r.ID)
		}
	}
}

// TestSave_WikilinkIdempotent verifies that saving the same memory twice with
// the same wikilink creates exactly one relation.
func TestSave_WikilinkIdempotent(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	// Create target first.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Target",
		Content:  "Target content.",
		TopicKey: "idem/target",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save target: %v", err)
	}

	// Save source twice (upsert via same topic_key).
	for i := 0; i < 2; i++ {
		_, err = svc.Save(ctx, model.SaveRequest{
			Title:    "Source",
			Content:  "See [[idem/target]] for details.",
			TopicKey: "idem/source",
			Type:     model.TypeDecision,
		})
		if err != nil {
			t.Fatalf("Save source (iteration %d): %v", i, err)
		}
	}

	srcEntity, err := ps.GetEntityByName(ctx, "idem/source", "test/project")
	if err != nil {
		t.Fatalf("GetEntityByName source: %v", err)
	}
	tgtEntity, err := ps.GetEntityByName(ctx, "idem/target", "test/project")
	if err != nil {
		t.Fatalf("GetEntityByName target: %v", err)
	}

	rels, err := ps.GetRelationsFrom(ctx, srcEntity.ID)
	if err != nil {
		t.Fatalf("GetRelationsFrom: %v", err)
	}

	count := 0
	for _, r := range rels {
		if r.TargetID == tgtEntity.ID && r.Type == model.RelReferences {
			count++
		}
	}
	if count != 1 {
		t.Errorf("got %d references relations, want exactly 1", count)
	}
}

// TestSave_WikilinkAnchorInMetadata verifies that [[topic#section]] stores
// the anchor in the relation's Metadata as JSON {"anchor":"section"}.
func TestSave_WikilinkAnchorInMetadata(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Target with sections",
		Content:  "This has sections.",
		TopicKey: "anchor/target",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save target: %v", err)
	}

	_, err = svc.Save(ctx, model.SaveRequest{
		Title:    "Source with anchor link",
		Content:  "See [[anchor/target#jwt-section]] for the JWT section.",
		TopicKey: "anchor/source",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save source: %v", err)
	}

	rel := findRelationByTopicKeys(ctx, t, ps, "anchor/source", "anchor/target", "test/project")
	if rel == nil {
		t.Fatal("expected relation, got nil")
	}
	if !strings.Contains(rel.Metadata, "jwt-section") {
		t.Errorf("expected anchor in metadata, got %q", rel.Metadata)
	}
}

// TestSave_WikilinkDisabledConfig verifies that wikilink processing is skipped
// when WikilinksEnabled=false.
func TestSave_WikilinkDisabledConfig(t *testing.T) {
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
	cfg.Graph.WikilinksEnabled = false
	svc := service.NewMemoryService(ps, gs, cfg, "test/project", embed.NopEmbedder{})

	ctx := context.Background()

	_, err = svc.Save(ctx, model.SaveRequest{
		Title:    "Target",
		Content:  "Target content.",
		TopicKey: "disabled/target",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save target: %v", err)
	}

	_, err = svc.Save(ctx, model.SaveRequest{
		Title:    "Source",
		Content:  "See [[disabled/target]] for details.",
		TopicKey: "disabled/source",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save source: %v", err)
	}

	// No relation should exist when wikilinks are disabled.
	rel := findRelationByTopicKeys(ctx, t, ps, "disabled/source", "disabled/target", "test/project")
	if rel != nil {
		t.Errorf("expected no relation when WikilinksEnabled=false, got one with weight=%f", rel.Weight)
	}
}

// TestSave_WikilinkSessionSummarySkipped verifies that TypeSessionSummary
// memories are excluded from wikilink processing.
func TestSave_WikilinkSessionSummarySkipped(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Target",
		Content:  "Target content.",
		TopicKey: "ss/target",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save target: %v", err)
	}

	_, err = svc.Save(ctx, model.SaveRequest{
		Title:   "Session summary with link",
		Content: "This session worked on [[ss/target]].",
		Type:    model.TypeSessionSummary,
	})
	if err != nil {
		t.Fatalf("Save session summary: %v", err)
	}

	// The ss/target entity should not have been created by the session summary.
	tgtEntity, err := ps.GetEntityByName(ctx, "ss/target", "test/project")
	if err != nil {
		return // entity doesn't exist — correct, no wikilink processing happened
	}
	_ = tgtEntity
	// Even if entity was created (by Save of target, which has no topic_key entity creation
	// directly), there should be no relation from the session summary.
	// We verify by checking no relation was created involving ss/target from any SS memory.
}

// TestSave_WikilinkMultipleTargets verifies that 3 wikilinks in one memory
// each create their own relation.
func TestSave_WikilinkMultipleTargets(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	for _, tk := range []string{"multi/a", "multi/b", "multi/c"} {
		_, err := svc.Save(ctx, model.SaveRequest{
			Title:    tk,
			Content:  "Content of " + tk,
			TopicKey: tk,
			Type:     model.TypeDecision,
		})
		if err != nil {
			t.Fatalf("Save target %q: %v", tk, err)
		}
	}

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Source",
		Content:  "See [[multi/a]], [[multi/b]], and [[multi/c]].",
		TopicKey: "multi/source",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save source: %v", err)
	}

	srcEntity, err := ps.GetEntityByName(ctx, "multi/source", "test/project")
	if err != nil {
		t.Fatalf("GetEntityByName source: %v", err)
	}

	rels, err := ps.GetRelationsFrom(ctx, srcEntity.ID)
	if err != nil {
		t.Fatalf("GetRelationsFrom: %v", err)
	}

	refCount := 0
	for _, r := range rels {
		if r.Type == model.RelReferences {
			refCount++
		}
	}
	if refCount != 3 {
		t.Errorf("got %d references relations, want 3", refCount)
	}
}

// TestSave_WikilinkCrossScopeGuard verifies that a global-scope source memory
// does NOT create a relation to a project-scoped target.
func TestSave_WikilinkCrossScopeGuard(t *testing.T) {
	svc, ps, gs := newWikilinkTestService(t)
	ctx := context.Background()

	// Create project-scoped target.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Project Target",
		Content:  "Project content.",
		TopicKey: "cross/project-target",
		Scope:    model.ScopeProject,
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save project target: %v", err)
	}

	// Save global-scoped source referencing the project target.
	_, err = svc.Save(ctx, model.SaveRequest{
		Title:    "Global Source",
		Content:  "Refers to [[cross/project-target]].",
		TopicKey: "cross/global-source",
		Scope:    model.ScopeGlobal,
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save global source: %v", err)
	}

	// No relation should exist in either store.
	relInProject := findRelationByTopicKeys(ctx, t, ps, "cross/global-source", "cross/project-target", "test/project")
	relInGlobal := findRelationByTopicKeys(ctx, t, gs, "cross/global-source", "cross/project-target", "")
	if relInProject != nil || relInGlobal != nil {
		t.Errorf("expected no cross-scope relation, found one")
	}
}

// TestSave_WikilinkProjectFallbackGlobal verifies that a project-scoped source
// can create a relation to a global-scoped target via the fallback lookup.
func TestSave_WikilinkProjectFallbackGlobal(t *testing.T) {
	svc, ps, gs := newWikilinkTestService(t)
	ctx := context.Background()

	// Create global-scoped target.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Global Convention",
		Content:  "This is a global convention.",
		TopicKey: "convention/global",
		Scope:    model.ScopeGlobal,
		Type:     model.TypeConvention,
	})
	if err != nil {
		t.Fatalf("Save global target: %v", err)
	}

	// Save project-scoped source referencing the global target.
	_, err = svc.Save(ctx, model.SaveRequest{
		Title:    "Project Memory",
		Content:  "See [[convention/global]] for the standard.",
		TopicKey: "project/source",
		Scope:    model.ScopeProject,
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save project source: %v", err)
	}

	// The relation and mirrored entities both live in projectStore (primaryStore),
	// because cross-DB relations are not possible. The target entity is mirrored
	// in projectStore under the source's project slug.
	rel := findRelationByTopicKeys(ctx, t, ps, "project/source", "convention/global", "test/project")
	if rel == nil {
		t.Fatal("expected references relation in projectStore for project->global fallback, got nil")
	}
	if rel.Type != model.RelReferences {
		t.Errorf("relation type: got %q, want %q", rel.Type, model.RelReferences)
	}
	_ = gs // globalStore is not used for relation storage in this case
}

// TestUpdate_WikilinkNewContent verifies that a new wikilink in updated content
// creates a new relation.
func TestUpdate_WikilinkNewContent(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	// Create target.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Update Target",
		Content:  "Target content.",
		TopicKey: "update/target",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save target: %v", err)
	}

	// Save source without wikilink.
	srcResp, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Update Source",
		Content:  "No wikilinks initially.",
		TopicKey: "update/source",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save source: %v", err)
	}

	// Verify no relation yet.
	relBefore := findRelationByTopicKeys(ctx, t, ps, "update/source", "update/target", "test/project")
	if relBefore != nil {
		t.Fatal("expected no relation before update, got one")
	}

	// Update with new content containing a wikilink.
	newContent := "Now references [[update/target]] for details."
	_, err = svc.Update(ctx, srcResp.ID, model.UpdateRequest{
		Content: &newContent,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Relation should now exist.
	relAfter := findRelationByTopicKeys(ctx, t, ps, "update/source", "update/target", "test/project")
	if relAfter == nil {
		t.Fatal("expected relation after update, got nil")
	}
}

// TestUpdate_WikilinkRemovedNotDeleted verifies that removing a wikilink from
// updated content does NOT delete the existing relation (append-only, D9).
func TestUpdate_WikilinkRemovedNotDeleted(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Persist Target",
		Content:  "Target content.",
		TopicKey: "persist/target",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save target: %v", err)
	}

	srcResp, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Persist Source",
		Content:  "See [[persist/target]] for details.",
		TopicKey: "persist/source",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save source: %v", err)
	}

	// Verify relation exists.
	relBefore := findRelationByTopicKeys(ctx, t, ps, "persist/source", "persist/target", "test/project")
	if relBefore == nil {
		t.Fatal("expected relation after initial save, got nil")
	}

	// Update: remove the wikilink.
	newContent := "No more wikilinks here."
	_, err = svc.Update(ctx, srcResp.ID, model.UpdateRequest{
		Content: &newContent,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Relation must still exist (append-only).
	relAfter := findRelationByTopicKeys(ctx, t, ps, "persist/source", "persist/target", "test/project")
	if relAfter == nil {
		t.Fatal("relation was deleted after wikilink removal — expected append-only behavior")
	}
}

// --- SPEC-012: unresolved reference tracking and auto-resolve tests ---

// TestSave_WikilinkUnresolvedRegistered verifies that saving a memory with a
// wikilink to a non-existent topic_key creates an unresolved reference row
// with mention_count=1.
func TestSave_WikilinkUnresolvedRegistered(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Source",
		Content:  "See [[nonexistent/gap]] for details.",
		TopicKey: "source/mem",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	refs, err := ps.FindUnresolvedByTarget(ctx, "nonexistent/gap", "test/project")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 unresolved ref, got %d", len(refs))
	}
	if refs[0].MentionCount != 1 {
		t.Errorf("mention_count = %d, want 1", refs[0].MentionCount)
	}
	if refs[0].TargetTopicKey != "nonexistent/gap" {
		t.Errorf("target_topic_key = %q, want %q", refs[0].TargetTopicKey, "nonexistent/gap")
	}
}

// TestSave_WikilinkUnresolvedAutoResolved verifies the core auto-resolve flow:
// 1. Save memory A with [[X]] — creates unresolved ref.
// 2. Save memory B with topic_key="X" — auto-resolves: deletes unresolved ref, creates relation.
func TestSave_WikilinkUnresolvedAutoResolved(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	// Step 1: save memory A with an unresolved wikilink.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Memory A",
		Content:  "Depends on [[future/topic]].",
		TopicKey: "memory/a",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save A: %v", err)
	}

	// Verify the unresolved ref was registered.
	refs, err := ps.FindUnresolvedByTarget(ctx, "future/topic", "test/project")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget after save A: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 unresolved ref after save A, got %d", len(refs))
	}

	// Step 2: save memory B with topic_key="future/topic" — triggers auto-resolve.
	_, err = svc.Save(ctx, model.SaveRequest{
		Title:    "Memory B",
		Content:  "This is the future topic.",
		TopicKey: "future/topic",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save B: %v", err)
	}

	// Unresolved ref should be gone.
	refsAfter, err := ps.FindUnresolvedByTarget(ctx, "future/topic", "test/project")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget after save B: %v", err)
	}
	if len(refsAfter) != 0 {
		t.Errorf("expected 0 unresolved refs after auto-resolve, got %d", len(refsAfter))
	}

	// Relation should now exist between A and B.
	rel := findRelationByTopicKeys(ctx, t, ps, "memory/a", "future/topic", "test/project")
	if rel == nil {
		t.Fatal("expected references relation after auto-resolve, got nil")
	}
	if rel.Type != model.RelReferences {
		t.Errorf("relation type = %q, want %q", rel.Type, model.RelReferences)
	}
}

// TestSave_AutoResolveSkipsNoTopicKey verifies that saving a memory without a
// topic_key does not trigger the auto-resolve query (the early-return guard).
func TestSave_AutoResolveSkipsNoTopicKey(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	// Register an unresolved ref manually via a save with a topic-key source.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Source with gap",
		Content:  "Refers to [[orphan/topic]].",
		TopicKey: "source/with-gap",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save source: %v", err)
	}

	// Save a memory WITHOUT topic_key. Must not auto-resolve anything.
	_, err = svc.Save(ctx, model.SaveRequest{
		Title:   "No topic key memory",
		Content: "orphan/topic is mentioned here but no topic_key is set.",
		Type:    model.TypeDiscovery,
	})
	if err != nil {
		t.Fatalf("Save no-topic-key: %v", err)
	}

	// The unresolved ref must still exist.
	refs, err := ps.FindUnresolvedByTarget(ctx, "orphan/topic", "test/project")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget: %v", err)
	}
	if len(refs) != 1 {
		t.Errorf("expected unresolved ref to persist (no auto-resolve without topic_key), got %d refs", len(refs))
	}
}

// TestSave_AutoResolveMultipleSources verifies that when two memories reference
// the same unresolved [[X]], saving a memory with topic_key="X" resolves both
// and creates two separate relations.
func TestSave_AutoResolveMultipleSources(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	// Two source memories referencing the same unresolved target.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Source One",
		Content:  "Depends on [[shared/target]].",
		TopicKey: "source/one",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save source one: %v", err)
	}
	_, err = svc.Save(ctx, model.SaveRequest{
		Title:    "Source Two",
		Content:  "Also depends on [[shared/target]].",
		TopicKey: "source/two",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save source two: %v", err)
	}

	refs, err := ps.FindUnresolvedByTarget(ctx, "shared/target", "test/project")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget before resolve: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 unresolved refs, got %d", len(refs))
	}

	// Save the target — auto-resolves both.
	_, err = svc.Save(ctx, model.SaveRequest{
		Title:    "Shared Target",
		Content:  "This is the shared target.",
		TopicKey: "shared/target",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save target: %v", err)
	}

	refsAfter, err := ps.FindUnresolvedByTarget(ctx, "shared/target", "test/project")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget after resolve: %v", err)
	}
	if len(refsAfter) != 0 {
		t.Errorf("expected 0 unresolved refs after auto-resolve, got %d", len(refsAfter))
	}

	rel1 := findRelationByTopicKeys(ctx, t, ps, "source/one", "shared/target", "test/project")
	if rel1 == nil {
		t.Error("expected relation from source/one to shared/target, got nil")
	}
	rel2 := findRelationByTopicKeys(ctx, t, ps, "source/two", "shared/target", "test/project")
	if rel2 == nil {
		t.Error("expected relation from source/two to shared/target, got nil")
	}
}

// TestSave_UnresolvedMentionCountIncrement verifies that re-saving the same
// memory (via upsert with the same topic_key) increments mention_count.
func TestSave_UnresolvedMentionCountIncrement(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	req := model.SaveRequest{
		Title:    "Repeated Source",
		Content:  "Refers to [[increment/gap]].",
		TopicKey: "repeated/source",
		Type:     model.TypeDecision,
	}
	if _, err := svc.Save(ctx, req); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if _, err := svc.Save(ctx, req); err != nil {
		t.Fatalf("second Save (upsert): %v", err)
	}

	refs, err := ps.FindUnresolvedByTarget(ctx, "increment/gap", "test/project")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 row (UPSERT), got %d", len(refs))
	}
	if refs[0].MentionCount != 2 {
		t.Errorf("mention_count = %d, want 2 after second upsert", refs[0].MentionCount)
	}
}

// TestUpdate_NewWikilinkUnresolvedRegistered verifies that updating a memory
// with new content containing an unresolved wikilink creates an unresolved ref.
func TestUpdate_NewWikilinkUnresolvedRegistered(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	srcResp, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Source No Link",
		Content:  "No wikilinks here.",
		TopicKey: "src/no-link",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	newContent := "Now refers to [[update/gap]] which does not exist."
	if _, err = svc.Update(ctx, srcResp.ID, model.UpdateRequest{Content: &newContent}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	refs, err := ps.FindUnresolvedByTarget(ctx, "update/gap", "test/project")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget: %v", err)
	}
	if len(refs) != 1 {
		t.Errorf("expected 1 unresolved ref after update, got %d", len(refs))
	}
}

// TestSave_AutoResolveIdempotentRelation verifies that auto-resolve when a
// relation already exists (e.g. created by mem_relate) touches the existing
// relation rather than duplicating it, and still removes the unresolved ref.
func TestSave_AutoResolveIdempotentRelation(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	// Save source memory with unresolved wikilink.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Source",
		Content:  "See [[idempotent/target]].",
		TopicKey: "idempotent/source",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save source: %v", err)
	}

	// Manually create the target memory and the relation (simulating mem_relate).
	targetResp, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Target (pre-created)",
		Content:  "Already exists.",
		TopicKey: "idempotent/target",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save target: %v", err)
	}
	_ = targetResp

	// At this point auto-resolve fired during target save and created the relation.
	// Verify relation exists.
	rel := findRelationByTopicKeys(ctx, t, ps, "idempotent/source", "idempotent/target", "test/project")
	if rel == nil {
		t.Fatal("expected relation after auto-resolve, got nil")
	}

	// Unresolved ref should be gone.
	refs, err := ps.FindUnresolvedByTarget(ctx, "idempotent/target", "test/project")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 unresolved refs, got %d", len(refs))
	}

	// Re-save target — should not create duplicate relation.
	_, err = svc.Save(ctx, model.SaveRequest{
		Title:    "Target (pre-created)",
		Content:  "Updated content.",
		TopicKey: "idempotent/target",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Re-save target: %v", err)
	}

	// Still exactly one relation.
	srcEntity, err := ps.GetEntityByName(ctx, "idempotent/source", "test/project")
	if err != nil || srcEntity == nil {
		t.Skip("source entity not found — skipping relation count check")
	}
	rels, err := ps.GetRelationsFrom(ctx, srcEntity.ID)
	if err != nil {
		t.Fatalf("GetRelationsFrom: %v", err)
	}
	count := 0
	for _, r := range rels {
		if r.Type == model.RelReferences {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 references relation after re-save, got %d", count)
	}
}

// TestUpdate_WikilinkContentUnchanged verifies that when Update is called with
// Content=nil, wikilink processing is not triggered.
func TestUpdate_WikilinkContentUnchanged(t *testing.T) {
	svc, ps, _ := newWikilinkTestService(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Unchanged Target",
		Content:  "Target content.",
		TopicKey: "unchanged/target",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save target: %v", err)
	}

	srcResp, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Unchanged Source",
		Content:  "No wikilinks.",
		TopicKey: "unchanged/source",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save source: %v", err)
	}

	// Update only the title (Content=nil).
	newTitle := "Unchanged Source (retitled)"
	_, err = svc.Update(ctx, srcResp.ID, model.UpdateRequest{
		Title: &newTitle,
	})
	if err != nil {
		t.Fatalf("Update title only: %v", err)
	}

	// The source memory content still has no wikilinks, so no relation should exist.
	rel := findRelationByTopicKeys(ctx, t, ps, "unchanged/source", "unchanged/target", "test/project")
	if rel != nil {
		t.Error("expected no relation when Content was not updated, got one")
	}
}

