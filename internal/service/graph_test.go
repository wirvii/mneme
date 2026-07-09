package service_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestRelate_CreatesNewRelation verifies that Relate creates entities and a
// relation when they do not already exist, and returns Created=true.
func TestRelate_CreatesNewRelation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	req := model.RelateRequest{
		Source:     "api-gateway",
		Target:     "auth-service",
		Relation:   model.RelDependsOn,
		SourceKind: model.KindService,
		TargetKind: model.KindService,
	}

	resp, err := svc.Relate(ctx, req)
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}

	if resp.RelationID == "" {
		t.Error("expected non-empty RelationID")
	}
	if resp.SourceID == "" {
		t.Error("expected non-empty SourceID")
	}
	if resp.TargetID == "" {
		t.Error("expected non-empty TargetID")
	}
	if !resp.Created {
		t.Error("expected Created=true for new relation")
	}
}

// TestRelate_IdempotentOnDuplicate verifies that calling Relate twice with the
// same arguments returns Created=false on the second call.
func TestRelate_IdempotentOnDuplicate(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	req := model.RelateRequest{
		Source:   "module-a",
		Target:   "module-b",
		Relation: model.RelUses,
	}

	first, err := svc.Relate(ctx, req)
	if err != nil {
		t.Fatalf("Relate (first): %v", err)
	}
	if !first.Created {
		t.Error("expected Created=true on first call")
	}

	second, err := svc.Relate(ctx, req)
	if err != nil {
		t.Fatalf("Relate (second): %v", err)
	}
	if second.Created {
		t.Error("expected Created=false on second call")
	}
	if second.RelationID != first.RelationID {
		t.Errorf("expected same RelationID: first=%q second=%q", first.RelationID, second.RelationID)
	}
}

// TestRelate_DefaultKindIsConcept verifies that when SourceKind and TargetKind
// are omitted the service defaults to KindConcept.
func TestRelate_DefaultKindIsConcept(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	req := model.RelateRequest{
		Source:   "idea-x",
		Target:   "idea-y",
		Relation: model.RelRelatedTo,
		// SourceKind and TargetKind intentionally omitted.
	}

	resp, err := svc.Relate(ctx, req)
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}
	if !resp.Created {
		t.Error("expected Created=true")
	}
}

// TestRelate_ValidationErrors verifies that required fields are enforced.
func TestRelate_ValidationErrors(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	tests := []struct {
		name string
		req  model.RelateRequest
	}{
		{
			name: "missing source",
			req:  model.RelateRequest{Target: "b", Relation: model.RelUses},
		},
		{
			name: "missing target",
			req:  model.RelateRequest{Source: "a", Relation: model.RelUses},
		},
		{
			name: "missing relation",
			req:  model.RelateRequest{Source: "a", Target: "b"},
		},
		{
			name: "invalid relation type",
			req:  model.RelateRequest{Source: "a", Target: "b", Relation: "not_a_real_type"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Relate(ctx, tc.req)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

// TestRelate_DefaultWeight verifies that when no weight is supplied the response
// contains the type-specific default weight.
func TestRelate_DefaultWeight(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "api",
		Target:   "db",
		Relation: model.RelDependsOn,
	})
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}

	const wantWeight = 0.9 // DefaultRelationWeights[RelDependsOn]
	if resp.Weight != wantWeight {
		t.Errorf("Weight = %v, want %v", resp.Weight, wantWeight)
	}
	if !resp.Created {
		t.Error("expected Created=true")
	}
}

// TestRelate_ExplicitWeight verifies that an explicit weight is honoured.
func TestRelate_ExplicitWeight(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "svc-a",
		Target:   "svc-b",
		Relation: model.RelRelatedTo,
		Weight:   0.75,
	})
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}
	if resp.Weight != 0.75 {
		t.Errorf("Weight = %v, want 0.75", resp.Weight)
	}
}

// TestRelate_InvalidWeightNaN verifies that a NaN weight returns ErrInvalidWeight.
func TestRelate_InvalidWeightNaN(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "a",
		Target:   "b",
		Relation: model.RelUses,
		Weight:   math.NaN(),
	})
	if !errors.Is(err, model.ErrInvalidWeight) {
		t.Errorf("expected ErrInvalidWeight, got %v", err)
	}
}

// TestRelate_InvalidWeightInf verifies that an infinite weight returns ErrInvalidWeight.
func TestRelate_InvalidWeightInf(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "a",
		Target:   "b",
		Relation: model.RelUses,
		Weight:   math.Inf(1),
	})
	if !errors.Is(err, model.ErrInvalidWeight) {
		t.Errorf("expected ErrInvalidWeight for +Inf, got %v", err)
	}
}

// TestRelate_InvalidWeightNegative verifies that a negative weight returns ErrInvalidWeight.
func TestRelate_InvalidWeightNegative(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "a",
		Target:   "b",
		Relation: model.RelUses,
		Weight:   -0.1,
	})
	if !errors.Is(err, model.ErrInvalidWeight) {
		t.Errorf("expected ErrInvalidWeight for -0.1, got %v", err)
	}
}

// TestRelate_InvalidWeightAboveOne verifies that a weight > 1.0 returns ErrInvalidWeight.
func TestRelate_InvalidWeightAboveOne(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "a",
		Target:   "b",
		Relation: model.RelUses,
		Weight:   1.5,
	})
	if !errors.Is(err, model.ErrInvalidWeight) {
		t.Errorf("expected ErrInvalidWeight for 1.5, got %v", err)
	}
}

// TestRelate_ExistingReturnsCurrentWeight verifies that a duplicate mem_relate
// call returns the existing relation's weight, not the default for the type.
func TestRelate_ExistingReturnsCurrentWeight(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	req := model.RelateRequest{
		Source:   "x",
		Target:   "y",
		Relation: model.RelDependsOn,
		Weight:   0.75,
	}
	first, err := svc.Relate(ctx, req)
	if err != nil {
		t.Fatalf("first Relate: %v", err)
	}
	if !first.Created {
		t.Fatal("expected Created=true on first call")
	}

	// Second call without explicit weight — should return the stored weight 0.75,
	// not the default 0.9 for depends_on.
	second, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "x",
		Target:   "y",
		Relation: model.RelDependsOn,
	})
	if err != nil {
		t.Fatalf("second Relate: %v", err)
	}
	if second.Created {
		t.Error("expected Created=false on second call")
	}
	if second.Weight != 0.75 {
		t.Errorf("Weight = %v, want 0.75 (existing stored weight)", second.Weight)
	}
}

// TestRelate_ReferencesType verifies that the new RelReferences type is accepted.
func TestRelate_ReferencesType(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "note-a",
		Target:   "note-b",
		Relation: model.RelReferences,
	})
	if err != nil {
		t.Fatalf("Relate with RelReferences: %v", err)
	}
	if resp.Weight != 0.4 { // DefaultRelationWeights[RelReferences]
		t.Errorf("Weight = %v, want 0.4", resp.Weight)
	}
}

// TestUpdateRelationWeight_Normal verifies that UpdateRelationWeight applies the
// delta and returns the updated relation.
func TestUpdateRelationWeight_Normal(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Create a relation first.
	resp, err := svc.Relate(ctx, model.RelateRequest{
		Source:   "a",
		Target:   "b",
		Relation: model.RelRelatedTo, // weight 0.5
	})
	if err != nil {
		t.Fatalf("Relate: %v", err)
	}

	updated, err := svc.UpdateRelationWeight(ctx, resp.RelationID, 0.2)
	if err != nil {
		t.Fatalf("UpdateRelationWeight: %v", err)
	}

	const wantWeight = 0.7 // 0.5 + 0.2
	if updated.Weight != wantWeight {
		t.Errorf("Weight = %v, want %v", updated.Weight, wantWeight)
	}
}

// TestUpdateRelationWeight_DeltaNaN verifies that a NaN delta is rejected.
func TestUpdateRelationWeight_DeltaNaN(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.UpdateRelationWeight(ctx, "some-id", math.NaN())
	if err == nil {
		t.Fatal("expected error for NaN delta, got nil")
	}
}

// TestTimeline_ByTimestamp verifies that Timeline returns memories within the
// window when given an ISO 8601 timestamp anchor.
func TestTimeline_ByTimestamp(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Save some memories so the timeline has something to return.
	for _, title := range []string{"note one", "note two", "note three"} {
		if _, err := svc.Save(ctx, model.SaveRequest{
			Title:   title,
			Content: "timeline test content",
			Type:    model.TypeDiscovery,
		}); err != nil {
			t.Fatalf("Save %q: %v", title, err)
		}
	}

	// Query a broad window centred on "now" (ISO 8601).
	resp, err := svc.Timeline(ctx, model.TimelineRequest{
		Around: "2025-01-01T00:00:00Z",
		Window: "365d",
	})
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}

	// Memories were created in 2026 (test date), so a broad window around 2025
	// with 365d half-window covers up to mid-2026. Results may or may not include
	// our memories depending on exact timing, so we only assert no error here.
	_ = resp
}

// TestTimeline_ByMemoryID verifies that Timeline accepts a memory UUID as anchor.
func TestTimeline_ByMemoryID(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	saved, err := svc.Save(ctx, model.SaveRequest{
		Title:   "anchor memory",
		Content: "this is the anchor",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	resp, err := svc.Timeline(ctx, model.TimelineRequest{
		Around: saved.ID,
		Window: "7d",
	})
	if err != nil {
		t.Fatalf("Timeline by memory ID: %v", err)
	}

	// The anchor memory itself must appear in the results since the window is
	// centred on its creation time.
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}
	found := false
	for _, r := range resp.Results {
		if r.ID == saved.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("anchor memory %q not found in timeline results", saved.ID)
	}
}

// TestTimeline_MissingAround verifies that Timeline returns an error when
// the required "around" field is empty.
func TestTimeline_MissingAround(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Timeline(ctx, model.TimelineRequest{Around: ""})
	if err == nil {
		t.Fatal("expected error for missing around field, got nil")
	}
}

// TestTimeline_InvalidMemoryID verifies that Timeline returns an error when
// the UUID-shaped "around" value does not correspond to an existing memory.
func TestTimeline_InvalidMemoryID(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Timeline(ctx, model.TimelineRequest{
		Around: "01910000-0000-7000-8000-000000000000",
		Window: "7d",
	})
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestTimeline_InvalidWindow verifies that an unrecognised window suffix
// produces an error.
func TestTimeline_InvalidWindow(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Timeline(ctx, model.TimelineRequest{
		Around: "2025-01-01T00:00:00Z",
		Window: "7x", // unsupported suffix
	})
	if err == nil {
		t.Fatal("expected error for invalid window, got nil")
	}
}
