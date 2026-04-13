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
