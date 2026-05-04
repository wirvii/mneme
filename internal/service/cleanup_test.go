package service_test

import (
	"context"
	"testing"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
)

// SPEC-031 cleanup tests.

// TestCleanup_DryRunReportsButDoesNotDelete verifies that DryRun=true returns
// candidates without deleting any rows.
func TestCleanup_DryRunReportsButDoesNotDelete(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	// Build an orphan relation by creating two entities with no
	// memory_entities rows and a relation between them. This mimics what
	// the legacy mem_relate path produced in wirvii/migratio.
	srcEnt, err := ps.FindOrCreateEntity(ctx, "architecture/zombie-a", model.KindConcept, "test/graph")
	if err != nil {
		t.Fatalf("create src entity: %v", err)
	}
	tgtEnt, err := ps.FindOrCreateEntity(ctx, "architecture/zombie-b", model.KindConcept, "test/graph")
	if err != nil {
		t.Fatalf("create tgt entity: %v", err)
	}
	if _, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: srcEnt.ID,
		TargetID: tgtEnt.ID,
		Type:     model.RelDependsOn,
		Weight:   0.9,
	}); err != nil {
		t.Fatalf("create orphan relation: %v", err)
	}

	result, err := svc.CleanupOrphanRelations(ctx, service.CleanupOrphanRelationsRequest{
		Scope:  "project",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("CleanupOrphanRelations: %v", err)
	}
	if result.OrphanRelationsFound != 1 {
		t.Errorf("OrphanRelationsFound = %d, want 1", result.OrphanRelationsFound)
	}
	if result.RelationsDeleted != 0 {
		t.Errorf("RelationsDeleted = %d, want 0 (dry run)", result.RelationsDeleted)
	}
	if !result.DryRun {
		t.Error("expected DryRun=true")
	}

	// Confirm the relation still exists.
	rel, err := ps.FindRelation(ctx, srcEnt.ID, tgtEnt.ID, model.RelDependsOn)
	if err != nil {
		t.Fatalf("FindRelation: %v", err)
	}
	if rel == nil {
		t.Error("dry-run should not have deleted the relation")
	}
}

// TestCleanup_ApplyDeletesOrphans verifies that DryRun=false actually deletes
// the orphan relations and that re-running is a no-op.
func TestCleanup_ApplyDeletesOrphans(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	srcEnt, err := ps.FindOrCreateEntity(ctx, "zombie/src", model.KindConcept, "test/graph")
	if err != nil {
		t.Fatalf("create src entity: %v", err)
	}
	tgtEnt, err := ps.FindOrCreateEntity(ctx, "zombie/tgt", model.KindConcept, "test/graph")
	if err != nil {
		t.Fatalf("create tgt entity: %v", err)
	}
	if _, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: srcEnt.ID,
		TargetID: tgtEnt.ID,
		Type:     model.RelReferences,
		Weight:   0.4,
	}); err != nil {
		t.Fatalf("create orphan relation: %v", err)
	}

	result, err := svc.CleanupOrphanRelations(ctx, service.CleanupOrphanRelationsRequest{
		Scope:  "project",
		DryRun: false,
	})
	if err != nil {
		t.Fatalf("CleanupOrphanRelations: %v", err)
	}
	if result.OrphanRelationsFound != 1 {
		t.Errorf("OrphanRelationsFound = %d, want 1", result.OrphanRelationsFound)
	}
	if result.RelationsDeleted != 1 {
		t.Errorf("RelationsDeleted = %d, want 1", result.RelationsDeleted)
	}

	rel, err := ps.FindRelation(ctx, srcEnt.ID, tgtEnt.ID, model.RelReferences)
	if err != nil {
		t.Fatalf("FindRelation: %v", err)
	}
	if rel != nil {
		t.Error("expected relation to be deleted")
	}

	// Idempotent — second pass finds nothing.
	again, err := svc.CleanupOrphanRelations(ctx, service.CleanupOrphanRelationsRequest{
		Scope:  "project",
		DryRun: false,
	})
	if err != nil {
		t.Fatalf("second CleanupOrphanRelations: %v", err)
	}
	if again.OrphanRelationsFound != 0 {
		t.Errorf("second pass OrphanRelationsFound = %d, want 0", again.OrphanRelationsFound)
	}
}

// TestCleanup_DoesNotTouchHealthyRelations verifies that relations whose
// entities are linked to memories via memory_entities (i.e. relations created
// after the SPEC-031 fix or via wikilinks) are NOT considered orphan.
func TestCleanup_DoesNotTouchHealthyRelations(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	// Two memories with topic_keys.
	memA, err := svc.Save(ctx, model.SaveRequest{
		Title: "A", Content: "a", TopicKey: "ok/a",
	})
	if err != nil {
		t.Fatalf("save A: %v", err)
	}
	memB, err := svc.Save(ctx, model.SaveRequest{
		Title: "B", Content: "b", TopicKey: "ok/b",
	})
	if err != nil {
		t.Fatalf("save B: %v", err)
	}

	// Healthy relation built through the SPEC-031 path.
	relResp, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "ok/a",
		Target:   "ok/b",
		Relation: model.RelDependsOn,
	})
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}

	// Plus an orphan relation we craft manually.
	zSrc, _ := ps.FindOrCreateEntity(ctx, "z/src", model.KindConcept, "test/graph")
	zTgt, _ := ps.FindOrCreateEntity(ctx, "z/tgt", model.KindConcept, "test/graph")
	orphanRel, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: zSrc.ID, TargetID: zTgt.ID, Type: model.RelRelatedTo, Weight: 0.5,
	})
	if err != nil {
		t.Fatalf("create orphan: %v", err)
	}

	result, err := svc.CleanupOrphanRelations(ctx, service.CleanupOrphanRelationsRequest{
		Scope:  "project",
		DryRun: false,
	})
	if err != nil {
		t.Fatalf("CleanupOrphanRelations: %v", err)
	}
	if result.RelationsDeleted != 1 {
		t.Errorf("RelationsDeleted = %d, want 1 (only the orphan)", result.RelationsDeleted)
	}

	// Healthy relation must still exist.
	healthy, err := ps.FindRelation(ctx, relResp.SourceID, relResp.TargetID, model.RelDependsOn)
	if err != nil {
		t.Fatalf("FindRelation healthy: %v", err)
	}
	if healthy == nil {
		t.Error("healthy relation was incorrectly deleted")
	}

	// Orphan must be gone.
	gone, err := ps.FindRelation(ctx, zSrc.ID, zTgt.ID, model.RelRelatedTo)
	if err != nil {
		t.Fatalf("FindRelation orphan: %v", err)
	}
	if gone != nil {
		t.Errorf("orphan relation %s still present", orphanRel.ID)
	}

	// Memory-side entities must still exist.
	if _, err := ps.GetEntity(ctx, relResp.SourceID); err != nil {
		t.Errorf("healthy source entity should remain: %v", err)
	}

	// Sanity: memories untouched.
	if _, err := svc.Get(ctx, memA.ID); err != nil {
		t.Errorf("memory A unexpectedly affected: %v", err)
	}
	if _, err := svc.Get(ctx, memB.ID); err != nil {
		t.Errorf("memory B unexpectedly affected: %v", err)
	}
}

// TestCleanup_AlsoDeleteEntities verifies that orphan entities are removed
// when AlsoDeleteEntities=true and the entity is left fully unreferenced.
func TestCleanup_AlsoDeleteEntities(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	srcEnt, _ := ps.FindOrCreateEntity(ctx, "ent/zombie-src", model.KindConcept, "test/graph")
	tgtEnt, _ := ps.FindOrCreateEntity(ctx, "ent/zombie-tgt", model.KindConcept, "test/graph")
	if _, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: srcEnt.ID, TargetID: tgtEnt.ID, Type: model.RelDependsOn, Weight: 0.9,
	}); err != nil {
		t.Fatalf("create orphan relation: %v", err)
	}

	result, err := svc.CleanupOrphanRelations(ctx, service.CleanupOrphanRelationsRequest{
		Scope:              "project",
		DryRun:             false,
		AlsoDeleteEntities: true,
	})
	if err != nil {
		t.Fatalf("CleanupOrphanRelations: %v", err)
	}
	if result.RelationsDeleted != 1 {
		t.Errorf("RelationsDeleted = %d, want 1", result.RelationsDeleted)
	}
	if result.EntitiesDeleted != 2 {
		t.Errorf("EntitiesDeleted = %d, want 2", result.EntitiesDeleted)
	}

	if _, err := ps.GetEntity(ctx, srcEnt.ID); err == nil {
		t.Error("expected source entity to be deleted")
	}
	if _, err := ps.GetEntity(ctx, tgtEnt.ID); err == nil {
		t.Error("expected target entity to be deleted")
	}
}
