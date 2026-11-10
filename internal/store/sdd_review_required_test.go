package store

import (
	"context"
	"errors"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestSetSpecReviewRequired covers SPEC-181 D3 against a real SQLite: the
// mark persists and every read path returns it; creation paths give false;
// UpdateSpecFromRecord leaves an existing true untouched.
func TestSetSpecReviewRequired(t *testing.T) {
	ctx := context.Background()

	newSpec := func(t *testing.T, s *SDDStore, id string, status model.SpecStatus) *model.Spec {
		t.Helper()
		spec := &model.Spec{ID: id, Title: id, Status: status, Project: "p", Lane: model.LaneStandard}
		if err := s.CreateSpec(ctx, spec); err != nil {
			t.Fatalf("CreateSpec: %v", err)
		}
		return spec
	}

	t.Run("persists true and false through every read path", func(t *testing.T) {
		s := newTestSDDStore(t)
		newSpec(t, s, "SPEC-001", model.SpecStatusDone)

		for _, want := range []bool{true, false, true} {
			if err := s.SetSpecReviewRequired(ctx, "SPEC-001", want); err != nil {
				t.Fatalf("SetSpecReviewRequired(%v): %v", want, err)
			}
			got, err := s.GetSpec(ctx, "SPEC-001")
			if err != nil {
				t.Fatalf("GetSpec: %v", err)
			}
			if got.ReviewRequired != want {
				t.Errorf("GetSpec.ReviewRequired = %v, want %v", got.ReviewRequired, want)
			}
			listed, _, _, err := s.ListSpecs(ctx, "p", "", 0)
			if err != nil || len(listed) != 1 {
				t.Fatalf("ListSpecs: %v (%d rows)", err, len(listed))
			}
			if listed[0].ReviewRequired != want {
				t.Errorf("ListSpecs.ReviewRequired = %v, want %v", listed[0].ReviewRequired, want)
			}
			recent, _, err := s.RecentlyCompletedSpecs(ctx, "p", 5)
			if err != nil || len(recent) != 1 {
				t.Fatalf("RecentlyCompletedSpecs: %v (%d rows)", err, len(recent))
			}
			if recent[0].ReviewRequired != want {
				t.Errorf("RecentlyCompletedSpecs.ReviewRequired = %v, want %v", recent[0].ReviewRequired, want)
			}
		}
	})

	t.Run("creation paths give false", func(t *testing.T) {
		s := newTestSDDStore(t)
		newSpec(t, s, "SPEC-001", model.SpecStatusDraft)
		fromRecord := &model.Spec{
			ID: "SPEC-002", Title: "r", Status: model.SpecStatusDraft, Project: "p",
			Lane: model.LaneStandard, ReviewRequired: true,
		}
		if err := s.CreateSpecFromRecord(ctx, fromRecord); err != nil {
			t.Fatalf("CreateSpecFromRecord: %v", err)
		}
		for _, id := range []string{"SPEC-001", "SPEC-002"} {
			got, err := s.GetSpec(ctx, id)
			if err != nil {
				t.Fatalf("GetSpec(%s): %v", id, err)
			}
			if got.ReviewRequired {
				t.Errorf("%s created with ReviewRequired = true, want false", id)
			}
		}
	})

	t.Run("UpdateSpecFromRecord keeps an existing true", func(t *testing.T) {
		s := newTestSDDStore(t)
		spec := newSpec(t, s, "SPEC-001", model.SpecStatusDraft)
		if err := s.SetSpecReviewRequired(ctx, "SPEC-001", true); err != nil {
			t.Fatalf("SetSpecReviewRequired: %v", err)
		}
		update := &model.Spec{
			ID: "SPEC-001", UUID: spec.UUID, Title: "changed", Status: model.SpecStatusSpeccing,
			Project: "p", Lane: model.LaneStandard, UpdatedAt: fixedPast,
		}
		if err := s.UpdateSpecFromRecord(ctx, update); err != nil {
			t.Fatalf("UpdateSpecFromRecord: %v", err)
		}
		got, err := s.GetSpec(ctx, "SPEC-001")
		if err != nil {
			t.Fatalf("GetSpec: %v", err)
		}
		if !got.ReviewRequired {
			t.Error("UpdateSpecFromRecord cleared review_required")
		}
		if got.Title != "changed" {
			t.Errorf("title = %q, want the record's title", got.Title)
		}
	})

	t.Run("unknown id returns ErrSpecNotFound", func(t *testing.T) {
		s := newTestSDDStore(t)
		err := s.SetSpecReviewRequired(ctx, "SPEC-404", true)
		if !errors.Is(err, model.ErrSpecNotFound) {
			t.Errorf("err = %v, want ErrSpecNotFound", err)
		}
	})
}
