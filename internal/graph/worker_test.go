package graph

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// newTestStore creates a fully-migrated in-memory SQLite store for worker tests.
func newTestStore(t *testing.T) *store.MemoryStore {
	t.Helper()
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return store.NewMemoryStore(d)
}

// newTestPool creates a HebbianWorkerPool backed by a real store for integration tests.
func newTestPool(t *testing.T, s *store.MemoryStore) *HebbianWorkerPool {
	t.Helper()
	cfg := config.GraphConfig{
		HebbianBufferSize:    1000,
		HebbianIncrement:     0.05,
		HebbianInitialWeight: 0.1,
	}
	return NewHebbianWorkerPool(s, cfg, slog.Default())
}

// createEntities is a helper that creates two entities and returns their IDs.
func createEntities(t *testing.T, s *store.MemoryStore) (string, string) {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateEntity(ctx, &model.Entity{Name: "entity-A", Kind: model.KindModule, Project: "test"})
	if err != nil {
		t.Fatalf("CreateEntity A: %v", err)
	}
	b, err := s.CreateEntity(ctx, &model.Entity{Name: "entity-B", Kind: model.KindModule, Project: "test"})
	if err != nil {
		t.Fatalf("CreateEntity B: %v", err)
	}
	return a.ID, b.ID
}

// TestWorkerPool_CreateNewRelation verifies that when no relation exists between
// two entities, the worker creates one with HebbianInitialWeight.
func TestWorkerPool_CreateNewRelation(t *testing.T) {
	s := newTestStore(t)
	pool := newTestPool(t, s)

	aID, bID := createEntities(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	pool.Enqueue(StrengtheningEvent{
		SourceEntityID: aID,
		TargetEntityID: bID,
		RelationType:   model.RelRelatedTo,
		Delta:          0.05,
	})

	pool.Drain(time.Second)

	rel, err := s.FindRelation(ctx, aID, bID, model.RelRelatedTo)
	if err != nil {
		t.Fatalf("FindRelation: %v", err)
	}
	if rel == nil {
		// Try reverse direction.
		rel, err = s.FindRelation(ctx, bID, aID, model.RelRelatedTo)
		if err != nil {
			t.Fatalf("FindRelation reverse: %v", err)
		}
	}
	if rel == nil {
		t.Fatal("expected relation to be created, got nil")
	}
	if rel.Weight != 0.1 {
		t.Errorf("weight = %f, want 0.1 (HebbianInitialWeight)", rel.Weight)
	}
	if rel.LastTraversedAt.IsZero() {
		t.Error("LastTraversedAt should be set on Hebbian-created relation")
	}
}

// TestWorkerPool_StrengthenExistingRelation verifies that when a relation already
// exists the worker applies UpdateRelationWeight rather than creating a duplicate.
func TestWorkerPool_StrengthenExistingRelation(t *testing.T) {
	s := newTestStore(t)
	pool := newTestPool(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aID, bID := createEntities(t, s)

	// Pre-create a relation with weight 0.5.
	existing, err := s.CreateRelation(ctx, &model.Relation{
		SourceID:        aID,
		TargetID:        bID,
		Type:            model.RelRelatedTo,
		Weight:          0.5,
		LastTraversedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	pool.Start(ctx)
	pool.Enqueue(StrengtheningEvent{
		SourceEntityID: aID,
		TargetEntityID: bID,
		RelationType:   model.RelRelatedTo,
		Delta:          0.05,
	})
	pool.Drain(time.Second)

	updated, err := s.FindRelation(ctx, aID, bID, model.RelRelatedTo)
	if err != nil {
		t.Fatalf("FindRelation: %v", err)
	}
	if updated == nil {
		t.Fatal("relation disappeared after strengthening")
	}
	if updated.ID != existing.ID {
		t.Error("a new relation was created instead of updating the existing one")
	}
	if updated.Weight <= 0.5 {
		t.Errorf("weight = %f, want > 0.5 (strengthened)", updated.Weight)
	}
}

// TestWorkerPool_ChannelFull verifies that Enqueue returns false and does not
// block when the channel is full.
func TestWorkerPool_ChannelFull(t *testing.T) {
	s := newTestStore(t)
	cfg := config.GraphConfig{
		HebbianBufferSize:    2,
		HebbianIncrement:     0.05,
		HebbianInitialWeight: 0.1,
	}
	pool := NewHebbianWorkerPool(s, cfg, slog.Default())
	// Do NOT start the worker — events accumulate until the buffer is full.

	aID, bID := createEntities(t, s)
	evt := StrengtheningEvent{
		SourceEntityID: aID,
		TargetEntityID: bID,
		RelationType:   model.RelRelatedTo,
		Delta:          0.05,
	}

	// Fill the buffer.
	if !pool.Enqueue(evt) {
		t.Fatal("first enqueue should succeed")
	}
	if !pool.Enqueue(evt) {
		t.Fatal("second enqueue should succeed")
	}
	// Third enqueue should fail (channel full).
	if pool.Enqueue(evt) {
		t.Error("third enqueue should return false (channel full)")
	}
}

// TestWorkerPool_DrainCompletes verifies that Drain processes pending events
// and returns before the timeout when the worker is running.
func TestWorkerPool_DrainCompletes(t *testing.T) {
	s := newTestStore(t)
	pool := newTestPool(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aID, bID := createEntities(t, s)
	pool.Start(ctx)

	for range 10 {
		pool.Enqueue(StrengtheningEvent{
			SourceEntityID: aID,
			TargetEntityID: bID,
			RelationType:   model.RelRelatedTo,
			Delta:          0.001,
		})
	}

	start := time.Now()
	pool.Drain(2 * time.Second)
	if time.Since(start) >= 2*time.Second {
		t.Error("Drain did not complete before the timeout")
	}
}
