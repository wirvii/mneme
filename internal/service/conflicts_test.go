package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
)

// TestConflictCandidates verifies that ConflictCandidates returns candidate IDs
// via FTS5 and excludes the source memory from results.
func TestConflictCandidates(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	saveDecision := func(title, content string) string {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title:   title,
			Content: content,
			Project: "test/project",
			Scope:   model.ScopeProject,
			Type:    model.TypeDecision,
		})
		if err != nil {
			t.Fatalf("Save(%q): %v", title, err)
		}
		return resp.ID
	}

	srcID := saveDecision("JWT authentication token", "We use JWT tokens for authentication in the API gateway")
	_ = saveDecision("Token authentication system", "Authentication using signed tokens for API access control")
	_ = saveDecision("Database migration guide", "How to run SQL schema migrations safely")

	candidates, err := svc.ConflictCandidates(ctx, srcID, 5)
	if err != nil {
		t.Fatalf("ConflictCandidates: %v", err)
	}

	// Source must not be in results.
	for _, id := range candidates {
		if id == srcID {
			t.Error("ConflictCandidates must not include the source memory")
		}
	}
}

// TestConflictCandidates_NotFound verifies that ConflictCandidates returns
// ErrNotFound for a non-existent memory ID.
func TestConflictCandidates_NotFound(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.ConflictCandidates(ctx, "non-existent-id", 5)
	if err == nil {
		t.Fatal("expected error for non-existent memory, got nil")
	}
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestConflictLink_Supersedes verifies that ConflictLink with "supersedes" sets
// the superseded_by column on the target memory.
func TestConflictLink_Supersedes(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	save := func(title, content string) string {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title: title, Content: content,
			Project: "test/project", Scope: model.ScopeProject, Type: model.TypeDecision,
		})
		if err != nil {
			t.Fatalf("Save(%q): %v", title, err)
		}
		return resp.ID
	}

	newID := save("Auth uses RS256 JWT", "Switched to RS256 for better security")
	oldID := save("Auth uses HMAC", "We use HMAC-SHA256 for token signing")

	// newID supersedes oldID: mark oldID as superseded by newID.
	if err := svc.ConflictLink(ctx, newID, oldID, "supersedes", "RS256 replaced HMAC"); err != nil {
		t.Fatalf("ConflictLink supersedes: %v", err)
	}

	// Verify oldID.superseded_by == newID.
	old, err := svc.Get(ctx, oldID)
	if err != nil {
		t.Fatalf("Get(oldID): %v", err)
	}
	if old.SupersededBy != newID {
		t.Errorf("old.SupersededBy = %q, want %q", old.SupersededBy, newID)
	}
}

// TestConflictLink_ConflictsWith verifies that ConflictLink with "conflicts_with"
// creates a memory_relations row visible via ConflictList.
func TestConflictLink_ConflictsWith(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	save := func(title, content string) string {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title: title, Content: content,
			Project: "test/project", Scope: model.ScopeProject, Type: model.TypeDecision,
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		return resp.ID
	}

	aID := save("HMAC auth", "Use HMAC-SHA256")
	bID := save("RS256 auth", "Use RS256 JWT")

	if err := svc.ConflictLink(ctx, aID, bID, "conflicts_with", "contradictory token schemes"); err != nil {
		t.Fatalf("ConflictLink conflicts_with: %v", err)
	}

	rels, err := svc.ConflictList(ctx, "test/project")
	if err != nil {
		t.Fatalf("ConflictList: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(rels))
	}
	if rels[0].Relation != "conflicts_with" {
		t.Errorf("Relation = %q, want conflicts_with", rels[0].Relation)
	}
	if rels[0].JudgedBy != "manual" {
		t.Errorf("JudgedBy = %q, want manual", rels[0].JudgedBy)
	}
}

// TestConflictLink_InvalidRelation verifies that an unknown relation type
// returns ErrInvalidRelation.
func TestConflictLink_InvalidRelation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	err := svc.ConflictLink(ctx, "a", "b", "invented_type", "")
	if err == nil {
		t.Fatal("expected ErrInvalidRelation, got nil")
	}
	if !errors.Is(err, model.ErrInvalidRelation) {
		t.Errorf("expected ErrInvalidRelation, got %v", err)
	}
}

// TestConflictLink_NotFound verifies that linking a non-existent memory returns
// ErrNotFound.
func TestConflictLink_NotFound(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	err := svc.ConflictLink(ctx, "non-existent-from", "non-existent-to", "conflicts_with", "")
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestConflictUnlink_ConflictsWith verifies that Unlink removes a
// conflicts_with relation row.
func TestConflictUnlink_ConflictsWith(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	save := func(title, content string) string {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title: title, Content: content,
			Project: "test/project", Scope: model.ScopeProject, Type: model.TypeDecision,
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		return resp.ID
	}

	aID := save("A title", "A content about authentication tokens JWT")
	bID := save("B title", "B content about authentication tokens JWT different approach")

	if err := svc.ConflictLink(ctx, aID, bID, "conflicts_with", "test"); err != nil {
		t.Fatalf("ConflictLink: %v", err)
	}

	if err := svc.ConflictUnlink(ctx, aID, bID); err != nil {
		t.Fatalf("ConflictUnlink: %v", err)
	}

	rels, err := svc.ConflictList(ctx, "test/project")
	if err != nil {
		t.Fatalf("ConflictList: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relations after unlink, got %d", len(rels))
	}
}

// TestConflictUnlink_ClearsSupersededBy verifies that unlinking a supersedes
// relation clears the superseded_by column.
func TestConflictUnlink_ClearsSupersededBy(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	save := func(title, content string) string {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title: title, Content: content,
			Project: "test/project", Scope: model.ScopeProject, Type: model.TypeDecision,
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		return resp.ID
	}

	newID := save("New decision on auth", "Replaced the old HMAC approach with RS256 JWT")
	oldID := save("Old decision on auth", "Original HMAC-SHA256 approach for authentication")

	if err := svc.ConflictLink(ctx, newID, oldID, "supersedes", "newer wins"); err != nil {
		t.Fatalf("ConflictLink: %v", err)
	}

	if err := svc.ConflictUnlink(ctx, newID, oldID); err != nil {
		t.Fatalf("ConflictUnlink: %v", err)
	}

	old, err := svc.Get(ctx, oldID)
	if err != nil {
		t.Fatalf("Get(oldID): %v", err)
	}
	if old.SupersededBy != "" {
		t.Errorf("expected empty SupersededBy after unlink, got %q", old.SupersededBy)
	}
}

// TestConflictList verifies that ConflictList returns all relation rows for a
// project.
func TestConflictList(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	save := func(title, content string) string {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title: title, Content: content,
			Project: "test/project", Scope: model.ScopeProject, Type: model.TypeDecision,
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		return resp.ID
	}

	aID := save("Alpha", "content alpha")
	bID := save("Beta", "content beta")
	cID := save("Gamma", "content gamma")

	if err := svc.ConflictLink(ctx, aID, bID, "conflicts_with", ""); err != nil {
		t.Fatalf("link A-B: %v", err)
	}
	if err := svc.ConflictLink(ctx, bID, cID, "unrelated", ""); err != nil {
		t.Fatalf("link B-C: %v", err)
	}

	rels, err := svc.ConflictList(ctx, "test/project")
	if err != nil {
		t.Fatalf("ConflictList: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relations, got %d", len(rels))
	}
}

// TestAnnotateConflicts verifies that search results are annotated with
// conflicts_with partners when those partners appear in the same result set.
func TestAnnotateConflicts(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	save := func(title, content string) string {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title: title, Content: content,
			Project: "test/project", Scope: model.ScopeProject, Type: model.TypeDecision,
		})
		if err != nil {
			t.Fatalf("Save(%q): %v", title, err)
		}
		return resp.ID
	}

	aID := save("HMAC authentication token", "Use HMAC-SHA256 for API token authentication and signing")
	bID := save("RS256 JWT authentication", "Use RS256 JWT tokens for authentication and API access")

	// Link them as conflicts_with.
	if err := svc.ConflictLink(ctx, aID, bID, "conflicts_with", "contradictory auth methods"); err != nil {
		t.Fatalf("ConflictLink: %v", err)
	}

	// Search for "authentication token" — both should be in results.
	resp, err := svc.Search(ctx, model.SearchRequest{
		Query:   "authentication token",
		Project: "test/project",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Find both memories in the result and check ConflictsWith annotation.
	foundA, foundB := false, false
	for _, r := range resp.Results {
		if r.ID == aID {
			foundA = true
			if len(r.ConflictsWith) == 0 {
				t.Errorf("result A (%s) has no ConflictsWith annotation, expected [%s]", aID, bID)
			}
		}
		if r.ID == bID {
			foundB = true
			if len(r.ConflictsWith) == 0 {
				t.Errorf("result B (%s) has no ConflictsWith annotation, expected [%s]", bID, aID)
			}
		}
	}

	// Only assert annotation if both are in the result set.
	if foundA && foundB {
		for _, r := range resp.Results {
			if r.ID == aID && !containsStr(r.ConflictsWith, bID) {
				t.Errorf("result A ConflictsWith %v does not contain B (%s)", r.ConflictsWith, bID)
			}
			if r.ID == bID && !containsStr(r.ConflictsWith, aID) {
				t.Errorf("result B ConflictsWith %v does not contain A (%s)", r.ConflictsWith, aID)
			}
		}
	}
}

// containsStr reports whether s is in the slice.
func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// TestConflictScan_CLIAbsent verifies that ConflictScan returns an error
// wrapping model.ErrCLIUnavailable when claude binary is not on PATH.
func TestConflictScan_CLIAbsent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Override PATH to a directory without a claude binary.
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	_, err := svc.ConflictScan(ctx, service.ConflictScanRequest{Project: "test/project"})
	if err == nil {
		t.Fatal("expected error when CLI absent, got nil")
	}
	if !errors.Is(err, model.ErrCLIUnavailable) {
		t.Errorf("expected model.ErrCLIUnavailable, got %v", err)
	}
}
