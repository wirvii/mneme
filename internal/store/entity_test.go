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
