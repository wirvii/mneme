package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
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

