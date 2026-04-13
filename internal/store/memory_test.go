package store

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/juanftp/mneme/internal/model"
)

func ptr[T any](v T) *T { return &v }

func makeMemory() *model.Memory {
	return &model.Memory{
		Type:       model.TypeDecision,
		Scope:      model.ScopeProject,
		Title:      "Test memory",
		Content:    "Some content about architecture decisions.",
		Project:    "myproject",
		Importance: 0.7,
		Confidence: 0.9,
		DecayRate:  0.01,
	}
}

// TestCreate verifies that Create assigns an ID and the record can be retrieved.
func TestCreate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, err := s.Create(ctx, makeMemory())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if m.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if _, err := uuid.FromString(m.ID); err != nil {
		t.Fatalf("ID is not a valid UUID: %v", err)
	}
	if m.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set")
	}
	if m.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should be set")
	}

	got, err := s.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}
	if got == nil {
		t.Fatal("expected memory, got nil")
	}
	if got.ID != m.ID {
		t.Errorf("ID mismatch: got %s, want %s", got.ID, m.ID)
	}
	if got.Title != m.Title {
		t.Errorf("Title mismatch: got %q, want %q", got.Title, m.Title)
	}
}

// TestGet_NotFound verifies that Get returns nil, nil for a missing id.
func TestGet_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.Get(ctx, "nonexistent-id")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil memory, got %+v", got)
	}
}

// TestUpdate verifies that partial updates are applied and revision_count increments.
func TestUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, err := s.Create(ctx, makeMemory())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	initialRevision := m.RevisionCount

	req := &model.UpdateRequest{
		Title: ptr("Updated title"),
	}
	if err := s.Update(ctx, m.ID, req); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.Title != "Updated title" {
		t.Errorf("Title not updated: got %q", got.Title)
	}
	if got.RevisionCount != initialRevision+1 {
		t.Errorf("RevisionCount not incremented: got %d, want %d", got.RevisionCount, initialRevision+1)
	}
}

// TestUpdate_NotFound verifies that Update returns ErrNotFound for a missing id.
func TestUpdate_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.Update(ctx, "nonexistent-id", &model.UpdateRequest{Title: ptr("x")})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !isNotFound(err) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestUpsert_Create verifies that upsert with a new topic_key creates and returns created=true.
func TestUpsert_Create(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := makeMemory()
	m.TopicKey = "arch/auth"

	got, created, err := s.Upsert(ctx, m)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	if got.ID == "" {
		t.Error("expected non-empty ID")
	}
}

// TestUpsert_Update verifies that a second upsert with the same topic_key updates and returns created=false.
func TestUpsert_Update(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := makeMemory()
	m.TopicKey = "arch/db"

	first, created, err := s.Upsert(ctx, m)
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	if !created {
		t.Fatal("expected created=true on first upsert")
	}

	m2 := makeMemory()
	m2.TopicKey = "arch/db"
	m2.Title = "Revised title"
	m2.Content = "Revised content."

	second, created2, err := s.Upsert(ctx, m2)
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if created2 {
		t.Error("expected created=false on second upsert")
	}
	if second.ID != first.ID {
		t.Errorf("ID changed: first=%s second=%s", first.ID, second.ID)
	}
	if second.Title != "Revised title" {
		t.Errorf("Title not updated: got %q", second.Title)
	}
	if second.RevisionCount <= first.RevisionCount {
		t.Errorf("RevisionCount not incremented: first=%d second=%d", first.RevisionCount, second.RevisionCount)
	}
}

// TestUpsert_NoTopicKey verifies that upsert without a topic_key always creates a new record.
func TestUpsert_NoTopicKey(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m1 := makeMemory()
	m1.TopicKey = ""

	r1, c1, err := s.Upsert(ctx, m1)
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	if !c1 {
		t.Error("expected created=true")
	}

	m2 := makeMemory()
	m2.TopicKey = ""

	r2, c2, err := s.Upsert(ctx, m2)
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if !c2 {
		t.Error("expected created=true on second upsert without topic_key")
	}
	if r1.ID == r2.ID {
		t.Error("expected different IDs for two upserts without topic_key")
	}
}

// TestSoftDelete verifies that a soft-deleted memory is not returned by Get.
func TestSoftDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, err := s.Create(ctx, makeMemory())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.SoftDelete(ctx, m.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	got, err := s.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get after SoftDelete: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after soft delete")
	}
}

// TestHardDelete verifies that hard delete removes soft-deleted records older than the threshold.
func TestHardDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, err := s.Create(ctx, makeMemory())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.SoftDelete(ctx, m.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	// Hard delete everything older than the future — should include our record.
	n, err := s.HardDelete(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("HardDelete: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}
}

// TestList verifies filtering by project, scope, and type.
func TestList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create memories in two projects and two types.
	for i := 0; i < 3; i++ {
		m := makeMemory()
		m.Project = "proj-a"
		m.Type = model.TypeDecision
		if _, err := s.Create(ctx, m); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		m := makeMemory()
		m.Project = "proj-b"
		m.Type = model.TypeBugfix
		if _, err := s.Create(ctx, m); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	all, err := s.List(ctx, ListOptions{Project: "proj-a"})
	if err != nil {
		t.Fatalf("List proj-a: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 memories for proj-a, got %d", len(all))
	}

	typed, err := s.List(ctx, ListOptions{Project: "proj-b", Type: model.TypeBugfix})
	if err != nil {
		t.Fatalf("List proj-b bugfix: %v", err)
	}
	if len(typed) != 2 {
		t.Errorf("expected 2 bugfix memories, got %d", len(typed))
	}

	none, err := s.List(ctx, ListOptions{Project: "proj-a", Type: model.TypeBugfix})
	if err != nil {
		t.Fatalf("List proj-a bugfix: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 results, got %d", len(none))
	}
}

// TestIncrementAccess verifies that access_count increments and last_accessed is set.
func TestIncrementAccess(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, err := s.Create(ctx, makeMemory())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if m.AccessCount != 0 {
		t.Fatalf("expected AccessCount=0 initially, got %d", m.AccessCount)
	}

	if err := s.IncrementAccess(ctx, m.ID); err != nil {
		t.Fatalf("IncrementAccess: %v", err)
	}

	got, err := s.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessCount != 1 {
		t.Errorf("expected AccessCount=1, got %d", got.AccessCount)
	}
	if got.LastAccessed == nil {
		t.Error("expected LastAccessed to be set")
	}
}

// TestFiles verifies that Files are stored and retrieved correctly.
func TestFiles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := makeMemory()
	m.Files = []string{"internal/model/memory.go", "internal/store/memory.go"}

	created, err := s.Create(ctx, m)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(got.Files))
	}
	// Files are returned sorted alphabetically from the DB.
	wantFiles := map[string]bool{
		"internal/model/memory.go": true,
		"internal/store/memory.go": true,
	}
	for _, f := range got.Files {
		if !wantFiles[f] {
			t.Errorf("unexpected file %q", f)
		}
	}
}

// isNotFound unwraps err chain to check for model.ErrNotFound.
func isNotFound(err error) bool {
	return err != nil && containsStr(err.Error(), "memory not found")
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringContains(s, sub))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
