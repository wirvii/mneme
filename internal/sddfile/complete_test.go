package sddfile

import (
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

func fullBacklogItem() *model.BacklogItem {
	return &model.BacklogItem{
		ID:        "BL-001",
		UUID:      "0198f000-0000-7000-8000-000000000001",
		Project:   "wirvii-mneme",
		Title:     "a title",
		Status:    model.BacklogStatusRaw,
		Priority:  model.PriorityMedium,
		Lane:      model.LaneStandard,
		CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func fullSpec() *model.Spec {
	return &model.Spec{
		ID:        "SPEC-001",
		UUID:      "0198f000-0000-7000-8000-000000000002",
		Project:   "wirvii-mneme",
		Title:     "a title",
		Status:    model.SpecStatusDraft,
		Lane:      model.LaneStandard,
		CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// TestSDDFile_Missing is AC3.
func TestSDDFile_Missing(t *testing.T) {
	t.Run("backlog: full record has no gaps", func(t *testing.T) {
		rec := &BacklogRecord{Item: fullBacklogItem()}
		if got := rec.Missing(); got != nil {
			t.Errorf("Missing() = %v, want nil", got)
		}
	})

	t.Run("backlog: missing uuid is reported", func(t *testing.T) {
		item := fullBacklogItem()
		item.UUID = ""
		rec := &BacklogRecord{Item: item}
		got := rec.Missing()
		if len(got) != 1 || got[0] != "uuid" {
			t.Errorf("Missing() = %v, want [uuid]", got)
		}
	})

	t.Run("backlog: absent schema line is NOT missing (D28: absence means 1)", func(t *testing.T) {
		// A record round-tripped from a file with no `schema:` line parses
		// with every field populated exactly like fullBacklogItem — schema
		// is not a struct field at all, so there is nothing to assert empty
		// here beyond confirming Missing() has no "schema" entry in its
		// vocabulary, proven by the full-record case above returning nil.
		rec := &BacklogRecord{Item: fullBacklogItem()}
		for _, m := range rec.Missing() {
			if m == "schema" {
				t.Fatalf("Missing() must never report schema (D28)")
			}
		}
	})

	t.Run("backlog: missing title is NOT reported (it is broken, not incomplete — AC10)", func(t *testing.T) {
		item := fullBacklogItem()
		item.Title = ""
		rec := &BacklogRecord{Item: item}
		for _, m := range rec.Missing() {
			if m == "title" {
				t.Fatalf("Missing() must never report title — an untitled record is broken, not incomplete")
			}
		}
	})

	t.Run("spec: full record has no gaps", func(t *testing.T) {
		rec := &SpecRecord{Spec: fullSpec()}
		if got := rec.Missing(); got != nil {
			t.Errorf("Missing() = %v, want nil", got)
		}
	})

	t.Run("spec: missing lane is reported", func(t *testing.T) {
		spec := fullSpec()
		spec.Lane = ""
		rec := &SpecRecord{Spec: spec}
		got := rec.Missing()
		if len(got) != 1 || got[0] != "lane" {
			t.Errorf("Missing() = %v, want [lane]", got)
		}
	})

	// The remaining subtests are a targeted addition (SPEC-131 commit 13,
	// AC29): the first coverage pass left both Missing() methods at
	// 63-65%, because only ONE gap-branch per method (uuid, lane) had a
	// dedicated case above — the other five/four branches of each method's
	// seven/six-way closed vocabulary, plus the nil-guard, never ran.

	t.Run("backlog: nil receiver reports no gaps", func(t *testing.T) {
		var rec *BacklogRecord
		if got := rec.Missing(); got != nil {
			t.Errorf("Missing() = %v, want nil", got)
		}
	})

	t.Run("backlog: nil Item reports no gaps", func(t *testing.T) {
		rec := &BacklogRecord{Item: nil}
		if got := rec.Missing(); got != nil {
			t.Errorf("Missing() = %v, want nil", got)
		}
	})

	t.Run("backlog: every remaining gap is reported individually", func(t *testing.T) {
		tests := []struct {
			name string
			zero func(*model.BacklogItem)
			want string
		}{
			{"project", func(i *model.BacklogItem) { i.Project = "" }, "project"},
			{"status", func(i *model.BacklogItem) { i.Status = "" }, "status"},
			{"priority", func(i *model.BacklogItem) { i.Priority = "" }, "priority"},
			{"created_at", func(i *model.BacklogItem) { i.CreatedAt = time.Time{} }, "created_at"},
			{"updated_at", func(i *model.BacklogItem) { i.UpdatedAt = time.Time{} }, "updated_at"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				item := fullBacklogItem()
				tt.zero(item)
				rec := &BacklogRecord{Item: item}
				got := rec.Missing()
				if len(got) != 1 || got[0] != tt.want {
					t.Errorf("Missing() = %v, want [%s]", got, tt.want)
				}
			})
		}
	})

	t.Run("spec: nil receiver reports no gaps", func(t *testing.T) {
		var rec *SpecRecord
		if got := rec.Missing(); got != nil {
			t.Errorf("Missing() = %v, want nil", got)
		}
	})

	t.Run("spec: nil Spec reports no gaps", func(t *testing.T) {
		rec := &SpecRecord{Spec: nil}
		if got := rec.Missing(); got != nil {
			t.Errorf("Missing() = %v, want nil", got)
		}
	})

	t.Run("spec: every remaining gap is reported individually", func(t *testing.T) {
		tests := []struct {
			name string
			zero func(*model.Spec)
			want string
		}{
			{"project", func(s *model.Spec) { s.Project = "" }, "project"},
			{"status", func(s *model.Spec) { s.Status = "" }, "status"},
			{"created_at", func(s *model.Spec) { s.CreatedAt = time.Time{} }, "created_at"},
			{"updated_at", func(s *model.Spec) { s.UpdatedAt = time.Time{} }, "updated_at"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				spec := fullSpec()
				tt.zero(spec)
				rec := &SpecRecord{Spec: spec}
				got := rec.Missing()
				if len(got) != 1 || got[0] != tt.want {
					t.Errorf("Missing() = %v, want [%s]", got, tt.want)
				}
			})
		}
	})
}
