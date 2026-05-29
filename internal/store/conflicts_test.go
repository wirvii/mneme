package store

import (
	"context"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// insertTestMemory creates a minimal active memory in the store for testing.
func insertTestMemory(t *testing.T, s *MemoryStore, id, title, content, project string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	const q = `
		INSERT INTO memories (id, type, scope, title, content, project,
			created_at, updated_at, importance, confidence, decay_rate)
		VALUES (?, 'decision', 'project', ?, ?, ?, ?, ?, 0.7, 0.8, 0.01)`

	if _, err := s.db.ExecContext(ctx, q, id, title, content, project, now, now); err != nil {
		t.Fatalf("insertTestMemory %q: %v", id, err)
	}
}

// TestNormalizePair verifies that normalizePair always returns the smaller ID
// first.
func TestNormalizePair(t *testing.T) {
	cases := []struct {
		a, b       string
		wantFrom   string
		wantTo     string
	}{
		{"aaa", "bbb", "aaa", "bbb"},
		{"bbb", "aaa", "aaa", "bbb"},
		{"z", "a", "a", "z"},
		{"same", "same", "same", "same"},
	}
	for _, tc := range cases {
		from, to := normalizePair(tc.a, tc.b)
		if from != tc.wantFrom || to != tc.wantTo {
			t.Errorf("normalizePair(%q,%q) = (%q,%q), want (%q,%q)",
				tc.a, tc.b, from, to, tc.wantFrom, tc.wantTo)
		}
	}
}

// TestCreateAndListMemoryRelations verifies basic CRUD: insert a row, list it.
func TestCreateAndListMemoryRelations(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Insert memories so the project filter JOIN can find them.
	insertTestMemory(t, s, "mem-a", "Auth uses HMAC", "HMAC-SHA256 for tokens", "proj1")
	insertTestMemory(t, s, "mem-b", "Auth uses RS256", "RS256 JWT for tokens", "proj1")

	if err := s.CreateMemoryRelation(ctx, "mem-a", "mem-b", "conflicts_with", "manual", "contradictory token schemes"); err != nil {
		t.Fatalf("CreateMemoryRelation: %v", err)
	}

	rels, err := s.ListMemoryRelations(ctx, MemoryRelationListOptions{Project: "proj1"})
	if err != nil {
		t.Fatalf("ListMemoryRelations: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(rels))
	}

	r := rels[0]
	// After normalization mem-a < mem-b so from_id = mem-a.
	if r.FromID != "mem-a" || r.ToID != "mem-b" {
		t.Errorf("pair: from=%q to=%q, want mem-a/mem-b", r.FromID, r.ToID)
	}
	if r.Relation != "conflicts_with" {
		t.Errorf("relation = %q, want conflicts_with", r.Relation)
	}
	if r.JudgedBy != "manual" {
		t.Errorf("judged_by = %q, want manual", r.JudgedBy)
	}
	if r.Rationale != "contradictory token schemes" {
		t.Errorf("rationale = %q, want contradictory token schemes", r.Rationale)
	}
}

// TestCreateMemoryRelation_NormalizationIdempotent verifies that inserting (a,b)
// and (b,a) results in only one row.
func TestCreateMemoryRelation_NormalizationIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	insertTestMemory(t, s, "mem-x", "X title", "X content", "proj1")
	insertTestMemory(t, s, "mem-y", "Y title", "Y content", "proj1")

	if err := s.CreateMemoryRelation(ctx, "mem-x", "mem-y", "unrelated", "cli", "different topics"); err != nil {
		t.Fatalf("first CreateMemoryRelation: %v", err)
	}
	// Insert in reverse direction — should replace, not insert a second row.
	if err := s.CreateMemoryRelation(ctx, "mem-y", "mem-x", "unrelated", "manual", "confirmed different"); err != nil {
		t.Fatalf("second CreateMemoryRelation (reverse): %v", err)
	}

	rels, err := s.ListMemoryRelations(ctx, MemoryRelationListOptions{})
	if err != nil {
		t.Fatalf("ListMemoryRelations: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relation after idempotent insert, got %d", len(rels))
	}
}

// TestDeleteMemoryRelation verifies that a relation can be deleted in either
// direction and that a second delete returns ErrNotFound.
func TestDeleteMemoryRelation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	insertTestMemory(t, s, "del-a", "Title A", "Content A", "proj1")
	insertTestMemory(t, s, "del-b", "Title B", "Content B", "proj1")

	if err := s.CreateMemoryRelation(ctx, "del-a", "del-b", "conflicts_with", "manual", ""); err != nil {
		t.Fatalf("CreateMemoryRelation: %v", err)
	}

	// Delete using reversed pair — normalization ensures we find the row.
	if err := s.DeleteMemoryRelation(ctx, "del-b", "del-a"); err != nil {
		t.Fatalf("DeleteMemoryRelation: %v", err)
	}

	// Second delete should return ErrNotFound.
	err := s.DeleteMemoryRelation(ctx, "del-a", "del-b")
	if err == nil {
		t.Fatal("expected ErrNotFound on second delete, got nil")
	}
	if !isErr(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestGetMemoryConflicts verifies symmetric retrieval of conflicts_with edges.
func TestGetMemoryConflicts(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	insertTestMemory(t, s, "c1", "HMAC auth", "Use HMAC", "proj1")
	insertTestMemory(t, s, "c2", "RS256 auth", "Use RS256", "proj1")
	insertTestMemory(t, s, "c3", "Unrelated topic", "Something else", "proj1")

	if err := s.CreateMemoryRelation(ctx, "c1", "c2", "conflicts_with", "manual", ""); err != nil {
		t.Fatalf("CreateMemoryRelation c1-c2: %v", err)
	}
	if err := s.CreateMemoryRelation(ctx, "c2", "c3", "conflicts_with", "manual", ""); err != nil {
		t.Fatalf("CreateMemoryRelation c2-c3: %v", err)
	}

	// c1 conflicts with c2.
	got, err := s.GetMemoryConflicts(ctx, "c1")
	if err != nil {
		t.Fatalf("GetMemoryConflicts(c1): %v", err)
	}
	if len(got) != 1 || got[0] != "c2" {
		t.Errorf("GetMemoryConflicts(c1) = %v, want [c2]", got)
	}

	// c2 conflicts with both c1 and c3 (symmetric lookup).
	got2, err := s.GetMemoryConflicts(ctx, "c2")
	if err != nil {
		t.Fatalf("GetMemoryConflicts(c2): %v", err)
	}
	if len(got2) != 2 {
		t.Errorf("GetMemoryConflicts(c2) = %v, want 2 entries", got2)
	}
}

// TestIsJudged verifies that IsJudged returns true for an existing pair and
// false for an unknown pair.
func TestIsJudged(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	insertTestMemory(t, s, "j1", "Title J1", "Content J1", "proj1")
	insertTestMemory(t, s, "j2", "Title J2", "Content J2", "proj1")

	judged, err := s.IsJudged(ctx, "j1", "j2")
	if err != nil {
		t.Fatalf("IsJudged before insert: %v", err)
	}
	if judged {
		t.Error("expected IsJudged=false before insert")
	}

	if err := s.CreateMemoryRelation(ctx, "j1", "j2", "unrelated", "manual", ""); err != nil {
		t.Fatalf("CreateMemoryRelation: %v", err)
	}

	judged, err = s.IsJudged(ctx, "j2", "j1") // reversed order
	if err != nil {
		t.Fatalf("IsJudged after insert (reversed): %v", err)
	}
	if !judged {
		t.Error("expected IsJudged=true after insert (reversed order)")
	}
}

// TestFTS5Candidates verifies that FTS5Candidates returns semantically related
// memories, excludes self, and respects the negative cache (already-judged pairs).
func TestFTS5Candidates(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Insert a source memory and two candidate memories that share terms.
	insertTestMemory(t, s, "src", "JWT authentication token", "We use JWT tokens for authentication in the API", "proj1")
	insertTestMemory(t, s, "cand1", "Token authentication system", "Authentication using signed tokens for API access", "proj1")
	insertTestMemory(t, s, "cand2", "Database schema migrations", "How to run SQL migrations for the database", "proj1")
	// cand3 will be pre-judged to test the negative cache.
	insertTestMemory(t, s, "cand3", "JWT signing key rotation", "Rotate JWT authentication signing keys periodically", "proj1")

	// Pre-judge src+cand3 as unrelated.
	if err := s.CreateMemoryRelation(ctx, "src", "cand3", "unrelated", "manual", ""); err != nil {
		t.Fatalf("pre-judge src+cand3: %v", err)
	}

	// The FTS5 candidate query for "JWT authentication token" should match cand1
	// (token authentication) but not cand2 (database migrations). cand3 is excluded
	// by the negative cache.
	ids, err := s.FTS5Candidates(ctx, "src", `"JWT" OR "authentication" OR "token"`, "proj1", 10)
	if err != nil {
		t.Fatalf("FTS5Candidates: %v", err)
	}

	// Verify src is not in results.
	for _, id := range ids {
		if id == "src" {
			t.Error("FTS5Candidates must not include the source memory (self)")
		}
		if id == "cand3" {
			t.Error("FTS5Candidates must not include already-judged pairs (cand3)")
		}
	}

	// cand1 should be present.
	found := false
	for _, id := range ids {
		if id == "cand1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected cand1 in FTS5Candidates results, got %v", ids)
	}
}

// TestFTS5Candidates_EmptyQuery verifies that an empty FTS query returns nil
// rather than an error.
func TestFTS5Candidates_EmptyQuery(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	ids, err := s.FTS5Candidates(ctx, "src", "", "proj1", 5)
	if err != nil {
		t.Fatalf("FTS5Candidates with empty query: %v", err)
	}
	if ids != nil {
		t.Errorf("expected nil ids for empty query, got %v", ids)
	}
}

// isErr is a helper that checks whether target appears in the error chain.
func isErr(err, target error) bool {
	if err == nil {
		return false
	}
	if err == target {
		return true
	}
	// Use errors.Is-compatible unwrapping.
	type unwrapper interface{ Unwrap() error }
	if uw, ok := err.(unwrapper); ok {
		return isErr(uw.Unwrap(), target)
	}
	return false
}
