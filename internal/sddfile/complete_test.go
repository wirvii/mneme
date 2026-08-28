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
}
