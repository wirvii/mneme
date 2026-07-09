package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// TestCreateEntity verifies that CreateEntity assigns a UUIDv7 ID, sets
// timestamps, and persists the entity so it can be retrieved by ID.
func TestCreateEntity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e := &model.Entity{
		Name:    "auth-service",
		Kind:    model.KindService,
		Project: "myproject",
	}

	created, err := s.CreateEntity(ctx, e)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	if created.ID == "" {
		t.Error("expected non-empty ID")
	}
	if created.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if created.UpdatedAt.IsZero() {
		t.Error("expected non-zero UpdatedAt")
	}

	// Retrieve and verify round-trip.
	got, err := s.GetEntity(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if got.Name != "auth-service" {
		t.Errorf("Name = %q, want %q", got.Name, "auth-service")
	}
	if got.Kind != model.KindService {
		t.Errorf("Kind = %q, want %q", got.Kind, model.KindService)
	}
	if got.Project != "myproject" {
		t.Errorf("Project = %q, want %q", got.Project, "myproject")
	}
}

// TestGetEntityByName verifies lookup by (name, project) unique pair.
func TestGetEntityByName(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e := &model.Entity{
		Name:    "postgres",
		Kind:    model.KindLibrary,
		Project: "proj-a",
	}
	created, err := s.CreateEntity(ctx, e)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	got, err := s.GetEntityByName(ctx, "postgres", "proj-a")
	if err != nil {
		t.Fatalf("GetEntityByName: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
}

// TestGetEntityByName_NotFound verifies ErrEntityNotFound is returned for
// an entity that does not exist.
func TestGetEntityByName_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.GetEntityByName(ctx, "nonexistent", "proj-x")
	if !errors.Is(err, model.ErrEntityNotFound) {
		t.Errorf("expected ErrEntityNotFound, got %v", err)
	}
}

// TestGetEntity_NotFound verifies ErrEntityNotFound is returned for an unknown ID.
func TestGetEntity_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.GetEntity(ctx, "00000000-0000-7000-8000-000000000000")
	if !errors.Is(err, model.ErrEntityNotFound) {
		t.Errorf("expected ErrEntityNotFound, got %v", err)
	}
}

// TestFindOrCreateEntity verifies that FindOrCreateEntity returns the existing
// entity on subsequent calls without creating duplicates.
func TestFindOrCreateEntity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, err := s.FindOrCreateEntity(ctx, "redis", model.KindService, "proj-b")
	if err != nil {
		t.Fatalf("FindOrCreateEntity (first): %v", err)
	}

	second, err := s.FindOrCreateEntity(ctx, "redis", model.KindService, "proj-b")
	if err != nil {
		t.Fatalf("FindOrCreateEntity (second): %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("expected same ID on second call: first=%q second=%q", first.ID, second.ID)
	}
}

// TestListEntities verifies filtering by project and kind.
func TestListEntities(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	project := "list-test"
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := s.CreateEntity(ctx, &model.Entity{
			Name:    name,
			Kind:    model.KindModule,
			Project: project,
		}); err != nil {
			t.Fatalf("CreateEntity %q: %v", name, err)
		}
	}
	// Create one with a different kind.
	if _, err := s.CreateEntity(ctx, &model.Entity{
		Name:    "external-lib",
		Kind:    model.KindLibrary,
		Project: project,
	}); err != nil {
		t.Fatalf("CreateEntity external-lib: %v", err)
	}

	// List all in project.
	all, err := s.ListEntities(ctx, project, "", 0)
	if err != nil {
		t.Fatalf("ListEntities (all): %v", err)
	}
	if len(all) != 4 {
		t.Errorf("got %d entities, want 4", len(all))
	}

	// List only modules.
	modules, err := s.ListEntities(ctx, project, model.KindModule, 0)
	if err != nil {
		t.Fatalf("ListEntities (modules): %v", err)
	}
	if len(modules) != 3 {
		t.Errorf("got %d module entities, want 3", len(modules))
	}
}

// TestCreateRelation verifies that relations can be created between two existing
// entities and retrieved via GetRelationsFrom / GetRelationsTo.
func TestCreateRelation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "api", Kind: model.KindService, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "db", Kind: model.KindService, Project: "p"})

	rel := &model.Relation{
		SourceID: src.ID,
		TargetID: tgt.ID,
		Type:     model.RelDependsOn,
		Weight:   1.0,
	}
	created, err := s.CreateRelation(ctx, rel)
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	if created.ID == "" {
		t.Error("expected non-empty relation ID")
	}

	// GetRelationsFrom should include the new relation.
	outgoing, err := s.GetRelationsFrom(ctx, src.ID)
	if err != nil {
		t.Fatalf("GetRelationsFrom: %v", err)
	}
	if len(outgoing) != 1 {
		t.Fatalf("GetRelationsFrom: got %d, want 1", len(outgoing))
	}
	if outgoing[0].TargetID != tgt.ID {
		t.Errorf("TargetID = %q, want %q", outgoing[0].TargetID, tgt.ID)
	}

	// GetRelationsTo should include the same relation.
	incoming, err := s.GetRelationsTo(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("GetRelationsTo: %v", err)
	}
	if len(incoming) != 1 {
		t.Fatalf("GetRelationsTo: got %d, want 1", len(incoming))
	}
	if incoming[0].SourceID != src.ID {
		t.Errorf("SourceID = %q, want %q", incoming[0].SourceID, src.ID)
	}
}

// TestFindRelation verifies that FindRelation returns nil when no relation exists
// and the relation when it does.
func TestFindRelation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "svc-a", Kind: model.KindService, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "svc-b", Kind: model.KindService, Project: "p"})

	// No relation yet.
	got, err := s.FindRelation(ctx, src.ID, tgt.ID, model.RelUses)
	if err != nil {
		t.Fatalf("FindRelation (before create): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil relation before creation, got %+v", got)
	}

	// Create the relation.
	_, err = s.CreateRelation(ctx, &model.Relation{
		SourceID: src.ID,
		TargetID: tgt.ID,
		Type:     model.RelUses,
		Weight:   1.0,
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	found, err := s.FindRelation(ctx, src.ID, tgt.ID, model.RelUses)
	if err != nil {
		t.Fatalf("FindRelation (after create): %v", err)
	}
	if found == nil {
		t.Fatal("expected non-nil relation after creation")
	}
	if found.Type != model.RelUses {
		t.Errorf("Type = %q, want %q", found.Type, model.RelUses)
	}
}

// TestLinkMemoryEntity verifies that a memory can be associated with an entity
// and retrieved via GetMemoryEntities.
func TestLinkMemoryEntity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create a memory first.
	mem, err := s.Create(ctx, &model.Memory{
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
		Title:   "Discovered postgres",
		Content: "The project uses PostgreSQL for persistence.",
		Project: "proj-link",
	})
	if err != nil {
		t.Fatalf("Create memory: %v", err)
	}

	entity, err := s.CreateEntity(ctx, &model.Entity{
		Name:    "postgres",
		Kind:    model.KindLibrary,
		Project: "proj-link",
	})
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	if err := s.LinkMemoryEntity(ctx, mem.ID, entity.ID, "subject"); err != nil {
		t.Fatalf("LinkMemoryEntity: %v", err)
	}

	// Idempotent — second call must not fail.
	if err := s.LinkMemoryEntity(ctx, mem.ID, entity.ID, "subject"); err != nil {
		t.Fatalf("LinkMemoryEntity (second call): %v", err)
	}

	entities, err := s.GetMemoryEntities(ctx, mem.ID)
	if err != nil {
		t.Fatalf("GetMemoryEntities: %v", err)
	}
	if len(entities) != 1 {
		t.Fatalf("GetMemoryEntities: got %d, want 1", len(entities))
	}
	if entities[0].Name != "postgres" {
		t.Errorf("entity name = %q, want %q", entities[0].Name, "postgres")
	}
}

// TestCreateRelation_DefaultWeight verifies that CreateRelation uses the type-specific
// default weight when the caller does not provide an explicit weight (zero value).
func TestCreateRelation_DefaultWeight(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "svc", Kind: model.KindService, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "db", Kind: model.KindService, Project: "p"})

	rel := &model.Relation{
		SourceID: src.ID,
		TargetID: tgt.ID,
		Type:     model.RelDependsOn,
		// Weight intentionally zero — store should apply DefaultWeight.
	}
	created, err := s.CreateRelation(ctx, rel)
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	const wantWeight = 0.9 // DefaultRelationWeights[RelDependsOn]
	if created.Weight != wantWeight {
		t.Errorf("Weight = %v, want %v", created.Weight, wantWeight)
	}

	// Verify roundtrip via GetRelationsFrom.
	out, err := s.GetRelationsFrom(ctx, src.ID)
	if err != nil {
		t.Fatalf("GetRelationsFrom: %v", err)
	}
	if len(out) != 1 || out[0].Weight != wantWeight {
		t.Errorf("roundtrip weight = %v, want %v", out[0].Weight, wantWeight)
	}
}

// TestCreateRelation_ExplicitWeight verifies that an explicit weight is persisted.
func TestCreateRelation_ExplicitWeight(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "a", Kind: model.KindModule, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "b", Kind: model.KindModule, Project: "p"})

	rel := &model.Relation{
		SourceID: src.ID,
		TargetID: tgt.ID,
		Type:     model.RelRelatedTo,
		Weight:   0.75,
	}
	created, err := s.CreateRelation(ctx, rel)
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	if created.Weight != 0.75 {
		t.Errorf("Weight = %v, want 0.75", created.Weight)
	}
}

// TestCreateRelation_LastTraversedAtNull verifies that newly created relations
// have a zero LastTraversedAt (never traversed).
func TestCreateRelation_LastTraversedAtNull(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "x", Kind: model.KindConcept, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "y", Kind: model.KindConcept, Project: "p"})

	rel := &model.Relation{
		SourceID: src.ID,
		TargetID: tgt.ID,
		Type:     model.RelUses,
	}
	created, err := s.CreateRelation(ctx, rel)
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	if !created.LastTraversedAt.IsZero() {
		t.Errorf("expected zero LastTraversedAt, got %v", created.LastTraversedAt)
	}
}

// TestUpdateRelationWeight_Normal verifies a standard weight delta is applied.
func TestUpdateRelationWeight_Normal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "a", Kind: model.KindModule, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "b", Kind: model.KindModule, Project: "p"})

	rel := &model.Relation{SourceID: src.ID, TargetID: tgt.ID, Type: model.RelRelatedTo, Weight: 0.5}
	created, _ := s.CreateRelation(ctx, rel)

	now := time.Now().UTC()
	updated, err := s.UpdateRelationWeight(ctx, created.ID, 0.1, now)
	if err != nil {
		t.Fatalf("UpdateRelationWeight: %v", err)
	}

	const wantWeight = 0.6
	if updated.Weight != wantWeight {
		t.Errorf("Weight = %v, want %v", updated.Weight, wantWeight)
	}
}

// TestUpdateRelationWeight_ClampHigh verifies that weight is clamped to 1.0 when
// the delta would exceed the upper bound.
func TestUpdateRelationWeight_ClampHigh(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "a", Kind: model.KindModule, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "b", Kind: model.KindModule, Project: "p"})

	rel := &model.Relation{SourceID: src.ID, TargetID: tgt.ID, Type: model.RelDependsOn, Weight: 0.9}
	created, _ := s.CreateRelation(ctx, rel)

	updated, err := s.UpdateRelationWeight(ctx, created.ID, 10.0, time.Now().UTC())
	if err != nil {
		t.Fatalf("UpdateRelationWeight: %v", err)
	}
	if updated.Weight != 1.0 {
		t.Errorf("Weight = %v, want 1.0 (clamped)", updated.Weight)
	}
}

// TestUpdateRelationWeight_ClampLow verifies that weight is clamped to 0.0 when
// the delta would go below the lower bound.
func TestUpdateRelationWeight_ClampLow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "a", Kind: model.KindModule, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "b", Kind: model.KindModule, Project: "p"})

	rel := &model.Relation{SourceID: src.ID, TargetID: tgt.ID, Type: model.RelRelatedTo, Weight: 0.5}
	created, _ := s.CreateRelation(ctx, rel)

	updated, err := s.UpdateRelationWeight(ctx, created.ID, -100.0, time.Now().UTC())
	if err != nil {
		t.Fatalf("UpdateRelationWeight: %v", err)
	}
	if updated.Weight != 0.0 {
		t.Errorf("Weight = %v, want 0.0 (clamped)", updated.Weight)
	}
}

// TestUpdateRelationWeight_NotFound verifies that ErrRelationNotFound is returned
// when the relation ID does not exist.
func TestUpdateRelationWeight_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.UpdateRelationWeight(ctx, "00000000-0000-7000-8000-000000000000", 0.1, time.Now().UTC())
	if !errors.Is(err, model.ErrRelationNotFound) {
		t.Errorf("expected ErrRelationNotFound, got %v", err)
	}
}

// TestUpdateRelationWeight_SetsLastTraversed verifies that last_traversed_at is
// updated to the supplied now value by UpdateRelationWeight.
func TestUpdateRelationWeight_SetsLastTraversed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "a", Kind: model.KindModule, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "b", Kind: model.KindModule, Project: "p"})

	rel := &model.Relation{SourceID: src.ID, TargetID: tgt.ID, Type: model.RelUses}
	created, _ := s.CreateRelation(ctx, rel)

	if !created.LastTraversedAt.IsZero() {
		t.Fatal("expected zero LastTraversedAt before update")
	}

	now := time.Now().UTC().Truncate(time.Second)
	updated, err := s.UpdateRelationWeight(ctx, created.ID, 0.05, now)
	if err != nil {
		t.Fatalf("UpdateRelationWeight: %v", err)
	}
	if updated.LastTraversedAt.IsZero() {
		t.Error("expected non-zero LastTraversedAt after UpdateRelationWeight")
	}
	if !updated.LastTraversedAt.Equal(now) {
		t.Errorf("LastTraversedAt = %v, want %v", updated.LastTraversedAt, now)
	}
}

// TestTouchRelation_OnlyTimestamp verifies that TouchRelation updates only
// last_traversed_at without changing weight.
func TestTouchRelation_OnlyTimestamp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "a", Kind: model.KindModule, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "b", Kind: model.KindModule, Project: "p"})

	rel := &model.Relation{SourceID: src.ID, TargetID: tgt.ID, Type: model.RelImplements, Weight: 0.8}
	created, _ := s.CreateRelation(ctx, rel)

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.TouchRelation(ctx, created.ID, now); err != nil {
		t.Fatalf("TouchRelation: %v", err)
	}

	// Re-read via GetRelationsFrom to verify both fields.
	out, err := s.GetRelationsFrom(ctx, src.ID)
	if err != nil {
		t.Fatalf("GetRelationsFrom: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(out))
	}
	r := out[0]
	if r.Weight != 0.8 {
		t.Errorf("Weight changed after touch: got %v, want 0.8", r.Weight)
	}
	if r.LastTraversedAt.IsZero() {
		t.Error("expected non-zero LastTraversedAt after touch")
	}
	if !r.LastTraversedAt.Equal(now) {
		t.Errorf("LastTraversedAt = %v, want %v", r.LastTraversedAt, now)
	}
}

// TestScanRelation_WithLastTraversed verifies the full roundtrip: create,
// touch, then retrieve and scan last_traversed_at correctly.
func TestScanRelation_WithLastTraversed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "a", Kind: model.KindConcept, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "b", Kind: model.KindConcept, Project: "p"})

	rel := &model.Relation{SourceID: src.ID, TargetID: tgt.ID, Type: model.RelReferences}
	created, _ := s.CreateRelation(ctx, rel)

	// Default weight for references should be 0.4.
	if created.Weight != 0.4 {
		t.Errorf("default weight for RelReferences = %v, want 0.4", created.Weight)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.TouchRelation(ctx, created.ID, now); err != nil {
		t.Fatalf("TouchRelation: %v", err)
	}

	got, err := s.FindRelation(ctx, src.ID, tgt.ID, model.RelReferences)
	if err != nil {
		t.Fatalf("FindRelation: %v", err)
	}
	if got == nil {
		t.Fatal("FindRelation returned nil")
	}
	if got.LastTraversedAt.IsZero() {
		t.Error("LastTraversedAt is zero after touch")
	}
	if !got.LastTraversedAt.Equal(now) {
		t.Errorf("LastTraversedAt = %v, want %v", got.LastTraversedAt, now)
	}
}

// TestDecayRelationWeights_AfterGracePeriod verifies that relations with
// last_traversed_at older than graceDays receive weight reduction.
func TestDecayRelationWeights_AfterGracePeriod(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "A", Kind: model.KindModule, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "B", Kind: model.KindModule, Project: "p"})

	// Create relation traversed 60 days ago.
	rel, err := s.CreateRelation(ctx, &model.Relation{
		SourceID:        src.ID,
		TargetID:        tgt.ID,
		Type:            model.RelRelatedTo,
		Weight:          0.5,
		LastTraversedAt: time.Now().UTC().AddDate(0, 0, -60),
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	n, err := s.DecayRelationWeights(ctx, 0.02, 30)
	if err != nil {
		t.Fatalf("DecayRelationWeights: %v", err)
	}
	if n != 1 {
		t.Errorf("rows affected = %d, want 1", n)
	}

	got, err := s.getRelationByID(ctx, rel.ID)
	if err != nil {
		t.Fatalf("getRelationByID: %v", err)
	}
	// weight * EXP(-0.02 * 60) = 0.5 * EXP(-1.2) ≈ 0.5 * 0.301 = 0.150
	if got.Weight >= 0.5 {
		t.Errorf("weight should have decreased from 0.5, got %f", got.Weight)
	}
	if got.Weight < 0 {
		t.Errorf("weight should not be negative, got %f", got.Weight)
	}
}

// TestDecayRelationWeights_WithinGracePeriod verifies that recently traversed
// relations are not decayed.
func TestDecayRelationWeights_WithinGracePeriod(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "C", Kind: model.KindModule, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "D", Kind: model.KindModule, Project: "p"})

	rel, err := s.CreateRelation(ctx, &model.Relation{
		SourceID:        src.ID,
		TargetID:        tgt.ID,
		Type:            model.RelRelatedTo,
		Weight:          0.5,
		LastTraversedAt: time.Now().UTC().AddDate(0, 0, -10), // within 30-day grace
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	n, err := s.DecayRelationWeights(ctx, 0.02, 30)
	if err != nil {
		t.Fatalf("DecayRelationWeights: %v", err)
	}
	if n != 0 {
		t.Errorf("rows affected = %d, want 0", n)
	}

	got, err := s.getRelationByID(ctx, rel.ID)
	if err != nil {
		t.Fatalf("getRelationByID: %v", err)
	}
	if got.Weight != 0.5 {
		t.Errorf("weight = %f, want 0.5 (unchanged)", got.Weight)
	}
}

// TestDecayRelationWeights_NullLastTraversed verifies that relations without
// last_traversed_at (explicit, never-traversed relations) are not decayed.
func TestDecayRelationWeights_NullLastTraversed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "E", Kind: model.KindModule, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "F", Kind: model.KindModule, Project: "p"})

	// Relation with no last_traversed_at (zero time → NULL in DB).
	rel, err := s.CreateRelation(ctx, &model.Relation{
		SourceID: src.ID,
		TargetID: tgt.ID,
		Type:     model.RelDependsOn,
		Weight:   0.9,
		// LastTraversedAt is zero — stored as NULL.
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	n, err := s.DecayRelationWeights(ctx, 0.02, 0)
	if err != nil {
		t.Fatalf("DecayRelationWeights: %v", err)
	}
	if n != 0 {
		t.Errorf("rows affected = %d, want 0 (NULL excluded)", n)
	}

	got, err := s.getRelationByID(ctx, rel.ID)
	if err != nil {
		t.Fatalf("getRelationByID: %v", err)
	}
	if got.Weight != 0.9 {
		t.Errorf("weight = %f, want 0.9 (unchanged)", got.Weight)
	}
}

// TestDecayRelationWeights_RateZero verifies that a zero decay rate is a no-op.
func TestDecayRelationWeights_RateZero(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "G", Kind: model.KindModule, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "H", Kind: model.KindModule, Project: "p"})
	_, _ = s.CreateRelation(ctx, &model.Relation{
		SourceID:        src.ID,
		TargetID:        tgt.ID,
		Type:            model.RelRelatedTo,
		Weight:          0.5,
		LastTraversedAt: time.Now().UTC().AddDate(0, 0, -60),
	})

	n, err := s.DecayRelationWeights(ctx, 0, 30)
	if err != nil {
		t.Fatalf("DecayRelationWeights: %v", err)
	}
	if n != 0 {
		t.Errorf("rows affected = %d, want 0 (rate=0 disables decay)", n)
	}
}

// TestDecayRelationWeights_WeightFloor verifies that weight never goes below 0.
func TestDecayRelationWeights_WeightFloor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "I", Kind: model.KindModule, Project: "p"})
	tgt, _ := s.CreateEntity(ctx, &model.Entity{Name: "J", Kind: model.KindModule, Project: "p"})

	rel, err := s.CreateRelation(ctx, &model.Relation{
		SourceID:        src.ID,
		TargetID:        tgt.ID,
		Type:            model.RelRelatedTo,
		Weight:          0.001, // very small weight, aggressive decay should floor at 0
		LastTraversedAt: time.Now().UTC().AddDate(0, 0, -365),
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	_, err = s.DecayRelationWeights(ctx, 1.0, 0) // very aggressive rate, no grace
	if err != nil {
		t.Fatalf("DecayRelationWeights: %v", err)
	}

	got, err := s.getRelationByID(ctx, rel.ID)
	if err != nil {
		t.Fatalf("getRelationByID: %v", err)
	}
	if got.Weight < 0 {
		t.Errorf("weight = %f, want >= 0.0 (floor enforced)", got.Weight)
	}
}

// TestDecayRelationWeights_MultipleRelations verifies batch behaviour: only
// relations outside the grace period are decayed.
func TestDecayRelationWeights_MultipleRelations(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src, _ := s.CreateEntity(ctx, &model.Entity{Name: "K", Kind: model.KindModule, Project: "p"})
	t1, _ := s.CreateEntity(ctx, &model.Entity{Name: "L", Kind: model.KindModule, Project: "p"})
	t2, _ := s.CreateEntity(ctx, &model.Entity{Name: "M", Kind: model.KindModule, Project: "p"})
	t3, _ := s.CreateEntity(ctx, &model.Entity{Name: "N", Kind: model.KindModule, Project: "p"})

	// Eligible: traversed 60 days ago.
	old, _ := s.CreateRelation(ctx, &model.Relation{
		SourceID:        src.ID,
		TargetID:        t1.ID,
		Type:            model.RelRelatedTo,
		Weight:          0.5,
		LastTraversedAt: time.Now().UTC().AddDate(0, 0, -60),
	})
	// Not eligible: within grace.
	recent, _ := s.CreateRelation(ctx, &model.Relation{
		SourceID:        src.ID,
		TargetID:        t2.ID,
		Type:            model.RelRelatedTo,
		Weight:          0.5,
		LastTraversedAt: time.Now().UTC().AddDate(0, 0, -5),
	})
	// Not eligible: NULL.
	explicit, _ := s.CreateRelation(ctx, &model.Relation{
		SourceID: src.ID,
		TargetID: t3.ID,
		Type:     model.RelDependsOn,
		Weight:   0.9,
	})

	n, err := s.DecayRelationWeights(ctx, 0.02, 30)
	if err != nil {
		t.Fatalf("DecayRelationWeights: %v", err)
	}
	if n != 1 {
		t.Errorf("rows affected = %d, want 1", n)
	}

	gotOld, _ := s.getRelationByID(ctx, old.ID)
	gotRecent, _ := s.getRelationByID(ctx, recent.ID)
	gotExplicit, _ := s.getRelationByID(ctx, explicit.ID)

	if gotOld.Weight >= 0.5 {
		t.Errorf("old relation weight should have decayed from 0.5, got %f", gotOld.Weight)
	}
	if gotRecent.Weight != 0.5 {
		t.Errorf("recent relation weight = %f, want 0.5 (unchanged)", gotRecent.Weight)
	}
	if gotExplicit.Weight != 0.9 {
		t.Errorf("explicit relation weight = %f, want 0.9 (unchanged)", gotExplicit.Weight)
	}
}

// TestFindRelationBidirectional verifies that FindRelationBidirectional finds
// a relation stored in either direction.
func TestFindRelationBidirectional(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a, _ := s.CreateEntity(ctx, &model.Entity{Name: "X", Kind: model.KindModule, Project: "p"})
	b, _ := s.CreateEntity(ctx, &model.Entity{Name: "Y", Kind: model.KindModule, Project: "p"})

	// Store relation as B→A (reverse of what the tracker generates).
	rel, err := s.CreateRelation(ctx, &model.Relation{
		SourceID: b.ID,
		TargetID: a.ID,
		Type:     model.RelRelatedTo,
		Weight:   0.5,
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	// Forward lookup (A→B) should find nothing.
	fwd, err := s.FindRelation(ctx, a.ID, b.ID, model.RelRelatedTo)
	if err != nil {
		t.Fatalf("FindRelation forward: %v", err)
	}
	if fwd != nil {
		t.Errorf("FindRelation forward: expected nil, got relation %q", fwd.ID)
	}

	// Bidirectional lookup should find the B→A relation.
	found, err := s.FindRelationBidirectional(ctx, a.ID, b.ID, model.RelRelatedTo)
	if err != nil {
		t.Fatalf("FindRelationBidirectional: %v", err)
	}
	if found == nil {
		t.Fatal("FindRelationBidirectional: expected relation, got nil")
	}
	if found.ID != rel.ID {
		t.Errorf("found relation ID = %q, want %q", found.ID, rel.ID)
	}
}

// TestListMemoriesInRange verifies that only memories within the time range are returned.
func TestListMemoriesInRange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	project := "range-test"

	// Create three memories.
	for _, title := range []string{"first", "second", "third"} {
		if _, err := s.Create(ctx, &model.Memory{
			Type:    model.TypeDiscovery,
			Scope:   model.ScopeProject,
			Title:   title,
			Content: "content",
			Project: project,
		}); err != nil {
			t.Fatalf("Create %q: %v", title, err)
		}
	}

	// Use a broad window to capture all three.
	from := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

	memories, err := s.ListMemoriesInRange(ctx, from, to, project, 0)
	if err != nil {
		t.Fatalf("ListMemoriesInRange: %v", err)
	}
	if len(memories) < 3 {
		t.Errorf("got %d memories, want at least 3", len(memories))
	}
}

// TestListMemoriesInRange_SharedAuthor verifies that ListMemoriesInRange's
// SELECT includes the shared and author columns (SPEC-061 SS-A).
func TestListMemoriesInRange_SharedAuthor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	project := "range-shared-author"
	if _, err := s.Create(ctx, &model.Memory{
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
		Title:   "shared range memory",
		Content: "content",
		Project: project,
		Shared:  1,
		Author:  "Jane Doe <jane@example.com>",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	from := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

	memories, err := s.ListMemoriesInRange(ctx, from, to, project, 0)
	if err != nil {
		t.Fatalf("ListMemoriesInRange: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(memories))
	}
	if memories[0].Shared != 1 {
		t.Errorf("Shared: got %d, want 1", memories[0].Shared)
	}
	if memories[0].Author != "Jane Doe <jane@example.com>" {
		t.Errorf("Author: got %q, want %q", memories[0].Author, "Jane Doe <jane@example.com>")
	}
}

// ─── GetStrongRelations tests ─────────────────────────────────────────────────

// TestGetStrongRelations_AboveThreshold verifies that only relations whose
// weight strictly exceeds the threshold are returned.
func TestGetStrongRelations_AboveThreshold(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e1 := mustCreateEntity(t, s, "e1", model.KindModule, "")
	e2 := mustCreateEntity(t, s, "e2", model.KindModule, "")
	e3 := mustCreateEntity(t, s, "e3", model.KindModule, "")
	e4 := mustCreateEntity(t, s, "e4", model.KindModule, "")

	// Create relations with various weights from e1.
	mustCreateRelationWithWeight(t, s, e1.ID, e2.ID, model.RelRelatedTo, 0.8)
	mustCreateRelationWithWeight(t, s, e1.ID, e3.ID, model.RelRelatedTo, 0.3) // at threshold, excluded (>0.3)
	mustCreateRelationWithWeight(t, s, e1.ID, e4.ID, model.RelRelatedTo, 0.1) // below threshold

	rels, err := s.GetStrongRelations(ctx, e1.ID, 0.3, 50)
	if err != nil {
		t.Fatalf("GetStrongRelations: %v", err)
	}
	// Only weight=0.8 exceeds 0.3 (strictly greater than).
	if len(rels) != 1 {
		t.Errorf("got %d relations, want 1 (weight>0.3 strictly)", len(rels))
	}
	if len(rels) == 1 && rels[0].Weight != 0.8 {
		t.Errorf("unexpected weight %f, want 0.8", rels[0].Weight)
	}
}

// TestGetStrongRelations_Bidirectional verifies that relations in both
// directions (source→entity and entity→target) are returned.
func TestGetStrongRelations_Bidirectional(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	hub := mustCreateEntity(t, s, "hub", model.KindModule, "")
	left := mustCreateEntity(t, s, "left", model.KindModule, "")
	right := mustCreateEntity(t, s, "right", model.KindModule, "")

	// hub → right (outgoing from hub)
	mustCreateRelationWithWeight(t, s, hub.ID, right.ID, model.RelRelatedTo, 0.7)
	// left → hub (incoming to hub)
	mustCreateRelationWithWeight(t, s, left.ID, hub.ID, model.RelRelatedTo, 0.9)

	rels, err := s.GetStrongRelations(ctx, hub.ID, 0.5, 50)
	if err != nil {
		t.Fatalf("GetStrongRelations: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("got %d relations, want 2 (one outgoing + one incoming)", len(rels))
	}
}

// TestGetStrongRelations_FanOutCap verifies that the limit parameter caps
// the number of results and that the strongest relations are retained.
func TestGetStrongRelations_FanOutCap(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	hub := mustCreateEntity(t, s, "hub", model.KindModule, "")

	// Create 10 outgoing relations with decreasing weights.
	for i := 0; i < 10; i++ {
		target := mustCreateEntity(t, s, "tgt"+string(rune('a'+i)), model.KindModule, "")
		weight := 1.0 - float64(i)*0.05 // 1.0, 0.95, 0.90 ...
		mustCreateRelationWithWeight(t, s, hub.ID, target.ID, model.RelRelatedTo, weight)
	}

	cap := 3
	rels, err := s.GetStrongRelations(ctx, hub.ID, 0.0, cap)
	if err != nil {
		t.Fatalf("GetStrongRelations: %v", err)
	}
	if len(rels) != cap {
		t.Errorf("got %d relations, want %d (fan-out cap)", len(rels), cap)
	}
	// Verify top relations are sorted weight DESC.
	for i := 1; i < len(rels); i++ {
		if rels[i].Weight > rels[i-1].Weight {
			t.Errorf("relations not sorted weight DESC at index %d: %f > %f", i, rels[i].Weight, rels[i-1].Weight)
		}
	}
}

// TestGetStrongRelations_NoResults verifies that an empty slice (not an error)
// is returned when no relations exceed the threshold.
func TestGetStrongRelations_NoResults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e := mustCreateEntity(t, s, "lonely", model.KindModule, "")
	rels, err := s.GetStrongRelations(ctx, e.ID, 0.5, 50)
	if err != nil {
		t.Fatalf("GetStrongRelations on entity with no relations: %v", err)
	}
	if rels == nil {
		rels = []*model.Relation{}
	}
	if len(rels) != 0 {
		t.Errorf("got %d relations, want 0", len(rels))
	}
}

// ─── GetEntityMemoryIDs tests ─────────────────────────────────────────────────

// TestGetEntityMemoryIDs_Basic verifies that linked memory IDs are returned.
func TestGetEntityMemoryIDs_Basic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e := mustCreateEntity(t, s, "auth-svc", model.KindService, "")

	m1 := mustCreateMemory(t, s, "mem-one", "")
	m2 := mustCreateMemory(t, s, "mem-two", "")

	if err := s.LinkMemoryEntity(ctx, m1.ID, e.ID, "mention"); err != nil {
		t.Fatalf("LinkMemoryEntity m1: %v", err)
	}
	if err := s.LinkMemoryEntity(ctx, m2.ID, e.ID, "mention"); err != nil {
		t.Fatalf("LinkMemoryEntity m2: %v", err)
	}

	ids, err := s.GetEntityMemoryIDs(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetEntityMemoryIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("got %d IDs, want 2", len(ids))
	}

	idSet := map[string]bool{m1.ID: true, m2.ID: true}
	for _, id := range ids {
		if !idSet[id] {
			t.Errorf("unexpected memory ID %q in result", id)
		}
	}
}

// TestGetEntityMemoryIDs_NoLinks verifies that an empty slice (not an error)
// is returned for an entity with no linked memories.
func TestGetEntityMemoryIDs_NoLinks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e := mustCreateEntity(t, s, "orphan-entity", model.KindModule, "")

	ids, err := s.GetEntityMemoryIDs(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetEntityMemoryIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("got %d IDs, want 0", len(ids))
	}
}

// ─── BatchTouchRelations tests ────────────────────────────────────────────────

// TestBatchTouchRelations_UpdatesTimestamp verifies that last_traversed_at is
// set on all specified relations after a batch touch.
func TestBatchTouchRelations_UpdatesTimestamp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e1 := mustCreateEntity(t, s, "e1", model.KindModule, "")
	e2 := mustCreateEntity(t, s, "e2", model.KindModule, "")
	e3 := mustCreateEntity(t, s, "e3", model.KindModule, "")

	r1 := mustCreateRelationWithWeight(t, s, e1.ID, e2.ID, model.RelRelatedTo, 0.7)
	r2 := mustCreateRelationWithWeight(t, s, e2.ID, e3.ID, model.RelRelatedTo, 0.5)

	before := time.Now().Add(-time.Second)
	touchTime := time.Now().UTC()

	if err := s.BatchTouchRelations(ctx, []string{r1.ID, r2.ID}, touchTime); err != nil {
		t.Fatalf("BatchTouchRelations: %v", err)
	}

	for _, id := range []string{r1.ID, r2.ID} {
		rel, err := s.getRelationByID(ctx, id)
		if err != nil {
			t.Fatalf("getRelationByID(%s): %v", id, err)
		}
		if rel.LastTraversedAt.IsZero() {
			t.Errorf("relation %s: LastTraversedAt is zero after batch touch", id)
		}
		if rel.LastTraversedAt.Before(before) {
			t.Errorf("relation %s: LastTraversedAt %v is before %v", id, rel.LastTraversedAt, before)
		}
	}
}

// TestBatchTouchRelations_EmptySlice verifies that an empty slice is a no-op
// and does not return an error.
func TestBatchTouchRelations_EmptySlice(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.BatchTouchRelations(ctx, []string{}, time.Now()); err != nil {
		t.Errorf("BatchTouchRelations with empty slice: got error %v, want nil", err)
	}
}

// TestBatchTouchRelations_Dedup verifies that passing duplicate IDs does not
// cause a SQL error (the query is idempotent for the same ID).
func TestBatchTouchRelations_Dedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e1 := mustCreateEntity(t, s, "src", model.KindModule, "")
	e2 := mustCreateEntity(t, s, "tgt", model.KindModule, "")
	r := mustCreateRelationWithWeight(t, s, e1.ID, e2.ID, model.RelRelatedTo, 0.8)

	// Pass the same ID twice — should succeed.
	err := s.BatchTouchRelations(ctx, []string{r.ID, r.ID}, time.Now())
	if err != nil {
		t.Errorf("BatchTouchRelations with duplicate IDs: %v", err)
	}
}

// ─── helpers used only in this test file ─────────────────────────────────────

// mustCreateEntity creates an entity and fails the test if it errors.
func mustCreateEntity(t *testing.T, s *MemoryStore, name string, kind model.EntityKind, project string) *model.Entity {
	t.Helper()
	e, err := s.CreateEntity(context.Background(), &model.Entity{
		Name:    name,
		Kind:    kind,
		Project: project,
	})
	if err != nil {
		t.Fatalf("mustCreateEntity(%q): %v", name, err)
	}
	return e
}

// mustCreateRelationWithWeight creates a relation with an explicit weight and
// fails the test if it errors.
func mustCreateRelationWithWeight(t *testing.T, s *MemoryStore, srcID, tgtID string, relType model.RelationType, weight float64) *model.Relation {
	t.Helper()
	r, err := s.CreateRelation(context.Background(), &model.Relation{
		SourceID: srcID,
		TargetID: tgtID,
		Type:     relType,
		Weight:   weight,
	})
	if err != nil {
		t.Fatalf("mustCreateRelationWithWeight(%q->%q): %v", srcID, tgtID, err)
	}
	return r
}

// mustCreateMemory creates a basic discovery memory and fails the test on error.
func mustCreateMemory(t *testing.T, s *MemoryStore, title string, project string) *model.Memory {
	t.Helper()
	m, err := s.Create(context.Background(), &model.Memory{
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
		Title:   title,
		Content: "test content",
		Project: project,
	})
	if err != nil {
		t.Fatalf("mustCreateMemory(%q): %v", title, err)
	}
	return m
}

// ─── SPEC-009: graph rebuild store tests ─────────────────────────────────────

// TestStore_DeleteRelatedToRelations_OnlyRelatedTo verifies that
// DeleteRelatedToRelations removes only related_to edges and leaves all other
// relation types intact (D6, SPEC-009).
func TestStore_DeleteRelatedToRelations_OnlyRelatedTo(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "del-test"
	e1, _ := s.FindOrCreateEntity(ctx, "alpha", model.KindConcept, project)
	e2, _ := s.FindOrCreateEntity(ctx, "beta", model.KindConcept, project)
	e3, _ := s.FindOrCreateEntity(ctx, "gamma", model.KindConcept, project)

	// Create one of each type to verify only related_to is removed.
	mustCreateRelationWithWeight(t, s, e1.ID, e2.ID, model.RelRelatedTo, 0.3)
	mustCreateRelationWithWeight(t, s, e1.ID, e3.ID, model.RelDependsOn, 0.9)
	mustCreateRelationWithWeight(t, s, e2.ID, e3.ID, model.RelImplements, 0.8)

	n, err := s.DeleteRelatedToRelations(ctx, project)
	if err != nil {
		t.Fatalf("DeleteRelatedToRelations: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted %d, want 1", n)
	}

	// depends_on and implements must survive.
	deps, err := s.GetRelationsFrom(ctx, e1.ID)
	if err != nil {
		t.Fatalf("GetRelationsFrom: %v", err)
	}
	if len(deps) != 1 || deps[0].Type != model.RelDependsOn {
		t.Errorf("expected depends_on to survive; got %v", deps)
	}

	impls, err := s.GetRelationsFrom(ctx, e2.ID)
	if err != nil {
		t.Fatalf("GetRelationsFrom e2: %v", err)
	}
	if len(impls) != 1 || impls[0].Type != model.RelImplements {
		t.Errorf("expected implements to survive; got %v", impls)
	}
}

// TestStore_DeleteRelatedToRelations_ProjectScoped verifies that only relations
// whose source entity belongs to the given project are deleted — relations for
// a different project are not affected (D6, SPEC-009).
func TestStore_DeleteRelatedToRelations_ProjectScoped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const projA = "proj-a"
	const projB = "proj-b"

	eA1, _ := s.FindOrCreateEntity(ctx, "ea1", model.KindConcept, projA)
	eA2, _ := s.FindOrCreateEntity(ctx, "ea2", model.KindConcept, projA)
	eB1, _ := s.FindOrCreateEntity(ctx, "eb1", model.KindConcept, projB)
	eB2, _ := s.FindOrCreateEntity(ctx, "eb2", model.KindConcept, projB)

	mustCreateRelationWithWeight(t, s, eA1.ID, eA2.ID, model.RelRelatedTo, 0.2)
	mustCreateRelationWithWeight(t, s, eB1.ID, eB2.ID, model.RelRelatedTo, 0.2)

	n, err := s.DeleteRelatedToRelations(ctx, projA)
	if err != nil {
		t.Fatalf("DeleteRelatedToRelations: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted %d, want 1 (only proj-a)", n)
	}

	// proj-b relation must survive.
	bRels, err := s.GetRelationsFrom(ctx, eB1.ID)
	if err != nil {
		t.Fatalf("GetRelationsFrom eB1: %v", err)
	}
	if len(bRels) != 1 {
		t.Errorf("proj-b related_to should survive; got %d relations", len(bRels))
	}
}

// TestStore_FindCandidatePairs_Basic verifies that 3 memories where 2 share
// 2 entities produce exactly 1 candidate pair (D7, SPEC-009).
func TestStore_FindCandidatePairs_Basic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "cand-basic"
	m1 := mustCreateMemory(t, s, "memory-1", project)
	m2 := mustCreateMemory(t, s, "memory-2", project)
	m3 := mustCreateMemory(t, s, "memory-3", project)

	e1, _ := s.FindOrCreateEntity(ctx, "shared-entity-1", model.KindFile, project)
	e2, _ := s.FindOrCreateEntity(ctx, "shared-entity-2", model.KindFile, project)
	eX, _ := s.FindOrCreateEntity(ctx, "unique-entity", model.KindConcept, project)

	// m1 and m2 share e1 and e2 — should produce 1 pair.
	_ = s.LinkMemoryEntity(ctx, m1.ID, e1.ID, "mention")
	_ = s.LinkMemoryEntity(ctx, m1.ID, e2.ID, "mention")
	_ = s.LinkMemoryEntity(ctx, m2.ID, e1.ID, "mention")
	_ = s.LinkMemoryEntity(ctx, m2.ID, e2.ID, "mention")
	// m3 only has a unique entity — shares nothing with m1/m2.
	_ = s.LinkMemoryEntity(ctx, m3.ID, eX.ID, "mention")

	pairs, err := s.FindCandidatePairs(ctx, project, 2)
	if err != nil {
		t.Fatalf("FindCandidatePairs: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("got %d pairs, want 1", len(pairs))
	}
	// The pair must contain m1 and m2 (in lexicographic order).
	p := pairs[0]
	if (p.MemoryID1 != m1.ID || p.MemoryID2 != m2.ID) &&
		(p.MemoryID1 != m2.ID || p.MemoryID2 != m1.ID) {
		t.Errorf("unexpected pair IDs: %v", p)
	}
	if p.SharedCount != 2 {
		t.Errorf("SharedCount = %d, want 2", p.SharedCount)
	}
}

// TestStore_FindCandidatePairs_BelowThreshold verifies that pairs with fewer
// than K shared entities are not returned (D2, SPEC-009).
func TestStore_FindCandidatePairs_BelowThreshold(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "cand-thresh"
	m1 := mustCreateMemory(t, s, "mem-t1", project)
	m2 := mustCreateMemory(t, s, "mem-t2", project)

	e1, _ := s.FindOrCreateEntity(ctx, "only-one", model.KindFile, project)
	_ = s.LinkMemoryEntity(ctx, m1.ID, e1.ID, "mention")
	_ = s.LinkMemoryEntity(ctx, m2.ID, e1.ID, "mention")

	// K=2: sharing only 1 entity should produce 0 pairs.
	pairs, err := s.FindCandidatePairs(ctx, project, 2)
	if err != nil {
		t.Fatalf("FindCandidatePairs: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("got %d pairs, want 0 (below threshold)", len(pairs))
	}
}

// TestStore_FindCandidatePairs_NoDuplicates verifies that pair (A,B) appears
// exactly once regardless of how many entities are shared (D7, SPEC-009).
func TestStore_FindCandidatePairs_NoDuplicates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "cand-nodup"
	m1 := mustCreateMemory(t, s, "mem-nd1", project)
	m2 := mustCreateMemory(t, s, "mem-nd2", project)

	// Share 3 entities so SharedCount = 3.
	for i, name := range []string{"e-nd-1", "e-nd-2", "e-nd-3"} {
		e, _ := s.FindOrCreateEntity(ctx, name, model.KindFile, project)
		_ = s.LinkMemoryEntity(ctx, m1.ID, e.ID, "mention")
		_ = s.LinkMemoryEntity(ctx, m2.ID, e.ID, "mention")
		_ = i // suppress unused warning
	}

	pairs, err := s.FindCandidatePairs(ctx, project, 2)
	if err != nil {
		t.Fatalf("FindCandidatePairs: %v", err)
	}
	if len(pairs) != 1 {
		t.Errorf("got %d pairs, want exactly 1", len(pairs))
	}
	if pairs[0].SharedCount != 3 {
		t.Errorf("SharedCount = %d, want 3", pairs[0].SharedCount)
	}
}

// TestFindCandidatePairs_ExcludesSoftDeleted verifies that soft-deleted memories
// are not returned as candidate pairs even though their memory_entities rows are
// retained (ON DELETE CASCADE only fires on hard delete). Specifically: given
// memories A, B, C all sharing 2 entities, soft-deleting C must remove (A,C) and
// (B,C) from the result set while preserving (A,B) (IMP-1, SPEC-009).
func TestFindCandidatePairs_ExcludesSoftDeleted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "cand-softdel"
	mA := mustCreateMemory(t, s, "mem-A", project)
	mB := mustCreateMemory(t, s, "mem-B", project)
	mC := mustCreateMemory(t, s, "mem-C", project)

	e1, _ := s.FindOrCreateEntity(ctx, "shared-sd-1", model.KindFile, project)
	e2, _ := s.FindOrCreateEntity(ctx, "shared-sd-2", model.KindFile, project)

	// All three memories share e1 and e2 — would produce 3 pairs without soft-delete.
	for _, m := range []*model.Memory{mA, mB, mC} {
		_ = s.LinkMemoryEntity(ctx, m.ID, e1.ID, "mention")
		_ = s.LinkMemoryEntity(ctx, m.ID, e2.ID, "mention")
	}

	// Verify 3 pairs exist before the soft-delete.
	pairsBefore, err := s.FindCandidatePairs(ctx, project, 2)
	if err != nil {
		t.Fatalf("FindCandidatePairs before soft-delete: %v", err)
	}
	if len(pairsBefore) != 3 {
		t.Fatalf("before soft-delete: got %d pairs, want 3", len(pairsBefore))
	}

	// Soft-delete C — its memory_entities rows remain in the DB.
	if err := s.SoftDelete(ctx, mC.ID); err != nil {
		t.Fatalf("SoftDelete(C): %v", err)
	}

	// After soft-delete: only (A,B) must appear; (A,C) and (B,C) must not.
	pairsAfter, err := s.FindCandidatePairs(ctx, project, 2)
	if err != nil {
		t.Fatalf("FindCandidatePairs after soft-delete: %v", err)
	}
	if len(pairsAfter) != 1 {
		t.Fatalf("after soft-delete: got %d pairs, want 1", len(pairsAfter))
	}
	p := pairsAfter[0]
	hasA := p.MemoryID1 == mA.ID || p.MemoryID2 == mA.ID
	hasB := p.MemoryID1 == mB.ID || p.MemoryID2 == mB.ID
	hasC := p.MemoryID1 == mC.ID || p.MemoryID2 == mC.ID
	if !hasA || !hasB {
		t.Errorf("remaining pair does not contain (A,B): %+v", p)
	}
	if hasC {
		t.Errorf("soft-deleted memory C still appears in pair: %+v", p)
	}
}

// TestStore_ListMemoriesWithoutEntities_Basic verifies that only memories with
// no memory_entities entry are returned (SPEC-009).
func TestStore_ListMemoriesWithoutEntities_Basic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "lmwe-basic"
	m1 := mustCreateMemory(t, s, "has-entity", project)
	m2 := mustCreateMemory(t, s, "no-entity-1", project)
	m3 := mustCreateMemory(t, s, "no-entity-2", project)

	// Link m1 to an entity — it should be excluded from results.
	e, _ := s.FindOrCreateEntity(ctx, "linked-entity", model.KindConcept, project)
	if err := s.LinkMemoryEntity(ctx, m1.ID, e.ID, "mention"); err != nil {
		t.Fatalf("LinkMemoryEntity: %v", err)
	}
	_ = m1

	result, err := s.ListMemoriesWithoutEntities(ctx, project, 0)
	if err != nil {
		t.Fatalf("ListMemoriesWithoutEntities: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("got %d memories, want 2", len(result))
	}
	// Both m2 and m3 must be in the result.
	ids := map[string]bool{result[0].ID: true, result[1].ID: true}
	if !ids[m2.ID] || !ids[m3.ID] {
		t.Errorf("expected m2 and m3 in result, got IDs %v", ids)
	}
}

// TestListMemoriesWithoutEntities_SharedAuthor verifies that
// ListMemoriesWithoutEntities' SELECT includes the shared and author columns
// (SPEC-061 SS-A).
func TestListMemoriesWithoutEntities_SharedAuthor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "lmwe-shared-author"
	if _, err := s.Create(ctx, &model.Memory{
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
		Title:   "no-entity-shared",
		Content: "content",
		Project: project,
		Shared:  2,
		Author:  "John Doe <john@example.com>",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := s.ListMemoriesWithoutEntities(ctx, project, 0)
	if err != nil {
		t.Fatalf("ListMemoriesWithoutEntities: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(result))
	}
	if result[0].Shared != 2 {
		t.Errorf("Shared: got %d, want 2", result[0].Shared)
	}
	if result[0].Author != "John Doe <john@example.com>" {
		t.Errorf("Author: got %q, want %q", result[0].Author, "John Doe <john@example.com>")
	}
}

// TestListRelationsByProject verifies that all relations whose source entity
// belongs to the given project are returned, and that relations from other
// projects are excluded.
func TestListRelationsByProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const projectA = "proj-a"
	const projectB = "proj-b"

	// Create entities for both projects.
	eA1, err := s.FindOrCreateEntity(ctx, "entity-a1", model.KindModule, projectA)
	if err != nil {
		t.Fatalf("FindOrCreateEntity A1: %v", err)
	}
	eA2, err := s.FindOrCreateEntity(ctx, "entity-a2", model.KindModule, projectA)
	if err != nil {
		t.Fatalf("FindOrCreateEntity A2: %v", err)
	}
	eB1, err := s.FindOrCreateEntity(ctx, "entity-b1", model.KindModule, projectB)
	if err != nil {
		t.Fatalf("FindOrCreateEntity B1: %v", err)
	}
	eB2, err := s.FindOrCreateEntity(ctx, "entity-b2", model.KindModule, projectB)
	if err != nil {
		t.Fatalf("FindOrCreateEntity B2: %v", err)
	}

	// Create two relations for project A, one for project B.
	_, err = s.CreateRelation(ctx, &model.Relation{SourceID: eA1.ID, TargetID: eA2.ID, Type: model.RelRelatedTo})
	if err != nil {
		t.Fatalf("CreateRelation A-A: %v", err)
	}
	_, err = s.CreateRelation(ctx, &model.Relation{SourceID: eA2.ID, TargetID: eA1.ID, Type: model.RelDependsOn})
	if err != nil {
		t.Fatalf("CreateRelation A2-A1: %v", err)
	}
	_, err = s.CreateRelation(ctx, &model.Relation{SourceID: eB1.ID, TargetID: eB2.ID, Type: model.RelRelatedTo})
	if err != nil {
		t.Fatalf("CreateRelation B-B: %v", err)
	}

	got, err := s.ListRelationsByProject(ctx, projectA)
	if err != nil {
		t.Fatalf("ListRelationsByProject: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: got %d, want 2", len(got))
	}
	for _, r := range got {
		// Source entity must belong to project A.
		src, err := s.GetEntity(ctx, r.SourceID)
		if err != nil {
			t.Fatalf("GetEntity: %v", err)
		}
		if src.Project != projectA {
			t.Errorf("expected source entity in %q, got %q", projectA, src.Project)
		}
	}
}

// TestListRelationsByProject_Empty verifies that an empty (not nil) slice is
// returned when no relations exist for the given project.
func TestListRelationsByProject_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.ListRelationsByProject(ctx, "no-such-project")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 relations, got %d", len(got))
	}
}
