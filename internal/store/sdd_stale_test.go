package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wirvii/mneme/internal/model"
)

// createStaleFixtureSpec creates one spec of project through the store, so
// the specs-table filter of LatestSpecHistoryAt has a real row to match.
func createStaleFixtureSpec(t *testing.T, s *SDDStore, project, id string) {
	t.Helper()
	if err := s.CreateSpec(context.Background(), &model.Spec{
		ID: id, Title: id, Status: model.SpecStatusDraft, Project: project, Lane: model.LaneStandard,
	}); err != nil {
		t.Fatalf("create spec %s: %v", id, err)
	}
}

// insertStaleFixtureHistory writes one spec_history row with a literal `at`
// string, so a case can choose the exact text (another UTC offset, or an
// unparseable value) the query must cope with.
func insertStaleFixtureHistory(t *testing.T, s *SDDStore, specID, at string) {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO spec_history (id, spec_id, from_status, to_status, by, reason, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id.String(), specID, "draft", "speccing", "test", "", at,
	); err != nil {
		t.Fatalf("insert history for %s: %v", specID, err)
	}
}

// TestLatestSpecHistoryAt pins SPEC-186 C3: the per-spec maximum is
// chronological (computed in Go, not by text order), scoped to the
// requested project, absent for a spec without history, and tolerant of an
// unparseable `at`.
func TestLatestSpecHistoryAt(t *testing.T) {
	ctx := context.Background()

	t.Run("MaxComputedInGo_NotTextOrder", func(t *testing.T) {
		s := newTestSDDStore(t)
		createStaleFixtureSpec(t, s, "proj", "SPEC-001")
		// Text order picks the +05:00 row (it sorts later as a string);
		// chronologically it is 07:00Z, before the 08:00Z row.
		insertStaleFixtureHistory(t, s, "SPEC-001", "2026-01-01T12:00:00+05:00")
		insertStaleFixtureHistory(t, s, "SPEC-001", "2026-01-01T08:00:00Z")

		got, err := s.LatestSpecHistoryAt(ctx, "proj")
		if err != nil {
			t.Fatalf("LatestSpecHistoryAt: %v", err)
		}
		want := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
		if !got["SPEC-001"].Equal(want) {
			t.Errorf("SPEC-001 latest = %v, want %v", got["SPEC-001"], want)
		}
	})

	t.Run("OnlyRequestedProject", func(t *testing.T) {
		s := newTestSDDStore(t)
		createStaleFixtureSpec(t, s, "proj", "SPEC-001")
		createStaleFixtureSpec(t, s, "other", "SPEC-500")
		insertStaleFixtureHistory(t, s, "SPEC-001", "2026-01-01T08:00:00Z")
		insertStaleFixtureHistory(t, s, "SPEC-500", "2026-06-01T08:00:00Z")

		got, err := s.LatestSpecHistoryAt(ctx, "proj")
		if err != nil {
			t.Fatalf("LatestSpecHistoryAt: %v", err)
		}
		if _, leaked := got["SPEC-500"]; leaked {
			t.Errorf("another project's spec leaked into the map: %v", got)
		}
		if len(got) != 1 {
			t.Errorf("len = %d, want 1: %v", len(got), got)
		}
	})

	t.Run("SpecWithoutHistoryAbsent", func(t *testing.T) {
		s := newTestSDDStore(t)
		createStaleFixtureSpec(t, s, "proj", "SPEC-001")

		got, err := s.LatestSpecHistoryAt(ctx, "proj")
		if err != nil {
			t.Fatalf("LatestSpecHistoryAt: %v", err)
		}
		if at, ok := got["SPEC-001"]; ok {
			t.Errorf("spec without history mapped to %v, want absent", at)
		}
	})

	t.Run("UnreadableAtIgnored", func(t *testing.T) {
		s := newTestSDDStore(t)
		createStaleFixtureSpec(t, s, "proj", "SPEC-001")
		createStaleFixtureSpec(t, s, "proj", "SPEC-002")
		insertStaleFixtureHistory(t, s, "SPEC-001", "not-a-timestamp")
		insertStaleFixtureHistory(t, s, "SPEC-001", "2026-01-01T08:00:00Z")
		insertStaleFixtureHistory(t, s, "SPEC-002", "not-a-timestamp")

		got, err := s.LatestSpecHistoryAt(ctx, "proj")
		if err != nil {
			t.Fatalf("LatestSpecHistoryAt returned an error for an unreadable row: %v", err)
		}
		want := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
		if !got["SPEC-001"].Equal(want) {
			t.Errorf("SPEC-001 latest = %v, want the readable %v", got["SPEC-001"], want)
		}
		if at, ok := got["SPEC-002"]; ok {
			t.Errorf("spec with only unreadable rows mapped to %v, want absent", at)
		}
	})
}
