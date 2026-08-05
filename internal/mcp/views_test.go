package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestNewBacklogListView_TruncatesLongDescription is AC4: a 40,000-character
// description produces a 200-rune excerpt with truncated=true, and the
// projected item must never carry a "description" field at all — the JSON
// key literally does not exist on backlogListItem.
func TestNewBacklogListView_TruncatesLongDescription(t *testing.T) {
	longDesc := strings.Repeat("x", 40000)
	resp := model.BacklogListResponse{
		Items: []*model.BacklogItem{
			{ID: "BL-001", Title: "Ledger item", Description: longDesc, Status: model.BacklogStatusRaw, Priority: model.PriorityMedium},
		},
		Total: 1,
	}

	view := newBacklogListView(resp, model.ListExcerptRunes)

	if len(view.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(view.Items))
	}
	item := view.Items[0]
	if !item.Truncated {
		t.Error("expected Truncated=true for a 40,000-rune description")
	}
	if got := len([]rune(item.Excerpt)); got != model.ListExcerptRunes {
		t.Errorf("Excerpt has %d runes, want %d", got, model.ListExcerptRunes)
	}

	b, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal item: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}
	if _, hasDescription := raw["description"]; hasDescription {
		t.Error("projected item must not emit a 'description' field")
	}
	if _, hasExcerpt := raw["excerpt"]; !hasExcerpt {
		t.Error("projected item must emit 'excerpt'")
	}
	if _, hasTruncated := raw["truncated"]; !hasTruncated {
		t.Error("projected item must emit 'truncated' when true")
	}
}

// TestNewBacklogListView_ShortDescriptionIsNotTruncated is AC5: a 50-character
// description is returned whole, and truncated is absent/false.
func TestNewBacklogListView_ShortDescriptionIsNotTruncated(t *testing.T) {
	shortDesc := strings.Repeat("y", 50)
	resp := model.BacklogListResponse{
		Items: []*model.BacklogItem{
			{ID: "BL-002", Title: "Short item", Description: shortDesc, Status: model.BacklogStatusRaw, Priority: model.PriorityMedium},
		},
		Total: 1,
	}

	view := newBacklogListView(resp, model.ListExcerptRunes)

	if len(view.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(view.Items))
	}
	item := view.Items[0]
	if item.Truncated {
		t.Error("expected Truncated=false for a 50-rune description")
	}
	if item.Excerpt != shortDesc {
		t.Errorf("Excerpt = %q, want the full description %q", item.Excerpt, shortDesc)
	}

	b, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal item: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}
	if truncated, present := raw["truncated"]; present && truncated != false {
		t.Errorf("expected 'truncated' absent or false, got %v (present=%v)", truncated, present)
	}
}

// TestNewBacklogListView_PreservesOtherFields verifies every other
// BacklogItem field survives the projection unchanged, and that Total is
// carried through from the response envelope.
func TestNewBacklogListView_PreservesOtherFields(t *testing.T) {
	resp := model.BacklogListResponse{
		Items: []*model.BacklogItem{
			{
				ID: "BL-003", Title: "Full fields", Description: "d",
				Status: model.BacklogStatusPromoted, Priority: model.PriorityHigh,
				Project: "proj", SpecID: "SPEC-001", Position: 3,
				Lane: model.LaneTrivial, Scope: "internal/**",
			},
		},
		Total: 42,
	}

	view := newBacklogListView(resp, model.ListExcerptRunes)

	if view.Total != 42 {
		t.Errorf("Total = %d, want 42", view.Total)
	}
	item := view.Items[0]
	if item.ID != "BL-003" || item.Title != "Full fields" || item.Status != model.BacklogStatusPromoted ||
		item.Priority != model.PriorityHigh || item.Project != "proj" || item.SpecID != "SPEC-001" ||
		item.Position != 3 || item.Lane != model.LaneTrivial || item.Scope != "internal/**" {
		t.Errorf("projected item lost or altered a field: %+v", item)
	}
}

// TestNewBacklogListView_DoesNotMutateSourceItems guards against an
// in-place overwrite of the original *model.BacklogItem's Description —
// the entities are pointers, and a shared pointer being silently truncated
// would be a time bomb for any other code still holding it.
func TestNewBacklogListView_DoesNotMutateSourceItems(t *testing.T) {
	longDesc := strings.Repeat("z", 1000)
	original := &model.BacklogItem{ID: "BL-004", Description: longDesc}
	resp := model.BacklogListResponse{Items: []*model.BacklogItem{original}, Total: 1}

	_ = newBacklogListView(resp, model.ListExcerptRunes)

	if original.Description != longDesc {
		t.Error("newBacklogListView mutated the original BacklogItem's Description in place")
	}
}
