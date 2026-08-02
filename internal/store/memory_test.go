package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/wirvii/mneme/internal/model"
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

// TestCreate_SharedAuthorDefaults verifies that a memory created without
// explicitly setting Shared/Author persists the inert defaults (shared=0,
// author="") — the behaviour mem_save relies on before team-memory (SS-B)
// wires the resolution logic (SPEC-061 SS-A).
func TestCreate_SharedAuthorDefaults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, err := s.Create(ctx, makeMemory())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Shared != 0 {
		t.Errorf("Shared: got %d, want 0", got.Shared)
	}
	if got.Author != "" {
		t.Errorf("Author: got %q, want empty", got.Author)
	}
}

// TestCreate_SharedAuthorRoundTrip verifies that explicit non-default
// Shared/Author values survive a Create → Get round trip across every level
// of the shared flag (SPEC-053 D2).
func TestCreate_SharedAuthorRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		shared int
		author string
	}{
		{"local", 0, ""},
		{"auto-shared", 1, "Jane Doe <jane@example.com>"},
		{"team-curated", 2, "John Doe <john@example.com>"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()

			m := makeMemory()
			m.Shared = tc.shared
			m.Author = tc.author

			created, err := s.Create(ctx, m)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			got, err := s.Get(ctx, created.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Shared != tc.shared {
				t.Errorf("Shared: got %d, want %d", got.Shared, tc.shared)
			}
			if got.Author != tc.author {
				t.Errorf("Author: got %q, want %q", got.Author, tc.author)
			}
		})
	}
}

// TestCreateWithID verifies that CreateWithID persists the memory using the
// caller-supplied id instead of generating a fresh UUIDv7 (SPEC-053 SS-D).
func TestCreateWithID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	wantID := "01938f1b-abcd-7abc-8def-000000000042"
	m := makeMemory()
	m.ID = wantID
	m.Shared = 1
	m.Author = "Alice <alice@example.com>"

	created, err := s.CreateWithID(ctx, m)
	if err != nil {
		t.Fatalf("CreateWithID: %v", err)
	}
	if created.ID != wantID {
		t.Errorf("ID: got %s, want %s (caller-supplied id must be preserved)", created.ID, wantID)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Error("CreatedAt/UpdatedAt should still be stamped to the current time")
	}

	got, err := s.Get(ctx, wantID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected memory, got nil")
	}
	if got.ID != wantID {
		t.Errorf("Get ID: got %s, want %s", got.ID, wantID)
	}
	if got.Shared != 1 || got.Author != "Alice <alice@example.com>" {
		t.Errorf("Shared/Author not preserved: got shared=%d author=%q", got.Shared, got.Author)
	}
}

// TestCreateWithID_EmptyID verifies that CreateWithID rejects an empty id
// rather than silently falling back to a generated one.
func TestCreateWithID_EmptyID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := makeMemory()
	m.ID = ""

	if _, err := s.CreateWithID(ctx, m); err == nil {
		t.Fatal("expected an error for an empty id, got nil")
	}
}

// TestCreateWithID_DuplicateID verifies that CreateWithID surfaces an error
// (rather than silently overwriting) when the id already exists.
func TestCreateWithID_DuplicateID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id := "01938f1b-abcd-7abc-8def-000000000043"

	first := makeMemory()
	first.ID = id
	if _, err := s.CreateWithID(ctx, first); err != nil {
		t.Fatalf("first CreateWithID: %v", err)
	}

	second := makeMemory()
	second.ID = id
	if _, err := s.CreateWithID(ctx, second); err == nil {
		t.Fatal("expected an error when creating a second memory with a duplicate id, got nil")
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

// TestSetTeamMemoryFields verifies that SetTeamMemoryFields persists shared
// and author on an existing row — the write path Update/Upsert intentionally
// lack (SPEC-061 SS-A), which SPEC-063 SS-C's Promote depends on.
func TestSetTeamMemoryFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Created with team-memory inert (Shared=0, Author="") — the common case
	// for a memory saved before team-memory was ever active for this process.
	m, err := s.Create(ctx, makeMemory())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if m.Shared != 0 || m.Author != "" {
		t.Fatalf("precondition failed: expected inert shared/author, got shared=%d author=%q", m.Shared, m.Author)
	}

	if err := s.SetTeamMemoryFields(ctx, m.ID, 2, "Jane Doe <jane@example.com>"); err != nil {
		t.Fatalf("SetTeamMemoryFields: %v", err)
	}

	got, err := s.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get after SetTeamMemoryFields: %v", err)
	}
	if got.Shared != 2 {
		t.Errorf("Shared: got %d, want 2", got.Shared)
	}
	if got.Author != "Jane Doe <jane@example.com>" {
		t.Errorf("Author: got %q, want %q", got.Author, "Jane Doe <jane@example.com>")
	}

	// Idempotent: calling it again with the same values must not error and
	// must leave the row unchanged.
	if err := s.SetTeamMemoryFields(ctx, m.ID, 2, "Jane Doe <jane@example.com>"); err != nil {
		t.Fatalf("SetTeamMemoryFields (second call): %v", err)
	}
	got2, err := s.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get after second SetTeamMemoryFields: %v", err)
	}
	if got2.Shared != 2 || got2.Author != "Jane Doe <jane@example.com>" {
		t.Errorf("expected unchanged shared/author after idempotent call, got shared=%d author=%q", got2.Shared, got2.Author)
	}
}

// TestSetTeamMemoryFields_NotFound verifies that SetTeamMemoryFields returns
// ErrNotFound for a missing id.
func TestSetTeamMemoryFields_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.SetTeamMemoryFields(ctx, "nonexistent-id", 2, "Jane Doe <jane@example.com>")
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

// TestList_SharedAuthor verifies that List's SELECT includes the shared and
// author columns (SPEC-061 SS-A: all memory scan sites must return them).
func TestList_SharedAuthor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := makeMemory()
	m.Project = "list-shared-author"
	m.Shared = 2
	m.Author = "Jane Doe <jane@example.com>"
	if _, err := s.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	results, err := s.List(ctx, ListOptions{Project: "list-shared-author"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Shared != 2 {
		t.Errorf("Shared: got %d, want 2", results[0].Shared)
	}
	if results[0].Author != "Jane Doe <jane@example.com>" {
		t.Errorf("Author: got %q, want %q", results[0].Author, "Jane Doe <jane@example.com>")
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

// TestStore_CreateRule_Roundtrip verifies that a rule memory survives a full
// create → get cycle with applies_to and severity intact.
func TestStore_CreateRule_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rule := &model.Memory{
		Type:      model.TypeRule,
		Scope:     model.ScopeProject,
		Title:     "Never use time.Now() directly",
		Content:   "Use the injected clock from the service constructor.",
		Project:   "myproject",
		Importance: 0.95,
		Confidence: 0.8,
		DecayRate:  0.0,
		AppliesTo: []string{"internal/**/*.go", "!internal/**/*_test.go"},
		Severity:  model.SeverityWarn,
	}

	created, err := s.Create(ctx, rule)
	if err != nil {
		t.Fatalf("Create rule: %v", err)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}
	if got == nil {
		t.Fatal("expected memory, got nil")
	}

	if got.Type != model.TypeRule {
		t.Errorf("Type = %q, want %q", got.Type, model.TypeRule)
	}
	if got.Severity != model.SeverityWarn {
		t.Errorf("Severity = %q, want %q", got.Severity, model.SeverityWarn)
	}
	if len(got.AppliesTo) != 2 {
		t.Fatalf("AppliesTo len = %d, want 2", len(got.AppliesTo))
	}
	if got.AppliesTo[0] != "internal/**/*.go" {
		t.Errorf("AppliesTo[0] = %q, want %q", got.AppliesTo[0], "internal/**/*.go")
	}
	if got.AppliesTo[1] != "!internal/**/*_test.go" {
		t.Errorf("AppliesTo[1] = %q, want %q", got.AppliesTo[1], "!internal/**/*_test.go")
	}
}

// TestStore_UpsertRule_UpdatesAppliesTo verifies that upserting a rule by
// topic_key updates the applies_to and severity fields.
func TestStore_UpsertRule_UpdatesAppliesTo(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rule := &model.Memory{
		Type:      model.TypeRule,
		Scope:     model.ScopeProject,
		Title:     "No vendor edits",
		Content:   "All vendor changes must go through go mod vendor.",
		TopicKey:  "rule/no-vendor-edits",
		Project:   "myproject",
		Importance: 0.95,
		Confidence: 0.8,
		DecayRate:  0.0,
		AppliesTo: []string{"vendor/**"},
		Severity:  model.SeverityWarn,
	}

	first, created, err := s.Upsert(ctx, rule)
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	if !created {
		t.Fatal("expected created=true on first upsert")
	}

	// Now upsert again with changed applies_to and severity.
	rule2 := &model.Memory{
		Type:      model.TypeRule,
		Scope:     model.ScopeProject,
		Title:     "No vendor edits",
		Content:   "All vendor changes must go through go mod vendor.",
		TopicKey:  "rule/no-vendor-edits",
		Project:   "myproject",
		Importance: 0.95,
		Confidence: 0.8,
		DecayRate:  0.0,
		AppliesTo: []string{"vendor/**", "!vendor/modules.txt"},
		Severity:  model.SeverityBlock,
	}

	second, created2, err := s.Upsert(ctx, rule2)
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if created2 {
		t.Fatal("expected created=false on second upsert (should update)")
	}
	if second.ID != first.ID {
		t.Errorf("ID changed on upsert: got %s, want %s", second.ID, first.ID)
	}

	got, err := s.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get after upsert: %v", err)
	}

	if got.Severity != model.SeverityBlock {
		t.Errorf("Severity after upsert = %q, want %q", got.Severity, model.SeverityBlock)
	}
	if len(got.AppliesTo) != 2 {
		t.Fatalf("AppliesTo len after upsert = %d, want 2", len(got.AppliesTo))
	}
	if got.AppliesTo[1] != "!vendor/modules.txt" {
		t.Errorf("AppliesTo[1] after upsert = %q, want %q", got.AppliesTo[1], "!vendor/modules.txt")
	}
}

// TestStore_UpdateRule_PartialSeverity verifies that Update with only Severity
// set in UpdateRequest changes severity without touching other fields.
func TestStore_UpdateRule_PartialSeverity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rule := &model.Memory{
		Type:      model.TypeRule,
		Scope:     model.ScopeProject,
		Title:     "No SQL inline",
		Content:   "SQL must live in .sql files.",
		Project:   "myproject",
		Importance: 0.95,
		Confidence: 0.8,
		DecayRate:  0.0,
		AppliesTo: []string{"**/*.go"},
		Severity:  model.SeverityWarn,
	}

	created, err := s.Create(ctx, rule)
	if err != nil {
		t.Fatalf("Create rule: %v", err)
	}

	newSeverity := model.SeverityBlock
	if err := s.Update(ctx, created.ID, &model.UpdateRequest{Severity: &newSeverity}); err != nil {
		t.Fatalf("Update severity: %v", err)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}

	if got.Severity != model.SeverityBlock {
		t.Errorf("Severity after Update = %q, want %q", got.Severity, model.SeverityBlock)
	}
	// applies_to must be unchanged.
	if len(got.AppliesTo) != 1 || got.AppliesTo[0] != "**/*.go" {
		t.Errorf("AppliesTo changed unexpectedly: %v", got.AppliesTo)
	}
}

// TestStore_ListRules_FilterByType verifies that List with Type=TypeRule only
// returns rule memories and not other types.
func TestStore_ListRules_FilterByType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create one rule and one decision.
	rule := &model.Memory{
		Type:      model.TypeRule,
		Scope:     model.ScopeProject,
		Title:     "A rule",
		Content:   "Rule content.",
		Project:   "myproject",
		Importance: 0.95,
		Confidence: 0.8,
		DecayRate:  0.0,
		AppliesTo: []string{"**"},
		Severity:  model.SeverityInfo,
	}
	if _, err := s.Create(ctx, rule); err != nil {
		t.Fatalf("Create rule: %v", err)
	}

	decision := makeMemory()
	decision.Type = model.TypeDecision
	if _, err := s.Create(ctx, decision); err != nil {
		t.Fatalf("Create decision: %v", err)
	}

	rules, err := s.List(ctx, ListOptions{Project: "myproject", Type: model.TypeRule})
	if err != nil {
		t.Fatalf("List rules: %v", err)
	}

	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Type != model.TypeRule {
		t.Errorf("Type = %q, want %q", rules[0].Type, model.TypeRule)
	}
}

// TestStore_AppliesTo_JSONMarshalling verifies that complex pattern strings
// survive a full roundtrip through JSON marshalling and unmarshalling.
func TestStore_AppliesTo_JSONMarshalling(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	patterns := []string{
		"tool:Edit+internal/**",
		"!docs/**",
		"**/*.go",
		"tool:Write",
	}

	rule := &model.Memory{
		Type:      model.TypeRule,
		Scope:     model.ScopeProject,
		Title:     "Complex patterns rule",
		Content:   "Testing pattern roundtrip.",
		Project:   "myproject",
		Importance: 0.95,
		Confidence: 0.8,
		DecayRate:  0.0,
		AppliesTo: patterns,
		Severity:  model.SeverityBlock,
	}

	created, err := s.Create(ctx, rule)
	if err != nil {
		t.Fatalf("Create rule with complex patterns: %v", err)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}

	if len(got.AppliesTo) != len(patterns) {
		t.Fatalf("AppliesTo len = %d, want %d", len(got.AppliesTo), len(patterns))
	}
	for i, want := range patterns {
		if got.AppliesTo[i] != want {
			t.Errorf("AppliesTo[%d] = %q, want %q", i, got.AppliesTo[i], want)
		}
	}
}

// ─── GetByIDPrefix tests ─────────────────────────────────────────────────────

// TestStore_GetByIDPrefix_ExactOneMatch verifies that a unique prefix returns
// the matching memory.
func TestStore_GetByIDPrefix_ExactOneMatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, err := s.Create(ctx, makeMemory())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	prefix := m.ID[:8]
	got, err := s.GetByIDPrefix(ctx, prefix)
	if err != nil {
		t.Fatalf("GetByIDPrefix: %v", err)
	}
	if got == nil {
		t.Fatal("expected memory, got nil")
	}
	if got.ID != m.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, m.ID)
	}
}

// TestStore_GetByIDPrefix_MultipleMatches verifies that ErrAmbiguousSeed is
// returned when two memories share the same 8-char prefix. We manipulate the
// IDs after creation via direct SQL since UUID prefixes are random.
func TestStore_GetByIDPrefix_MultipleMatches(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create two memories.
	m1, _ := s.Create(ctx, makeMemory())
	m2, _ := s.Create(ctx, makeMemory())

	// Choose a prefix that is the common prefix of both IDs. If IDs happen to
	// share a prefix naturally this test catches that case. Otherwise we use a
	// prefix that matches multiple rows by lowering it to just 1 hex char.
	// The safest approach: search for a prefix that returns >1 row by trying the
	// first character (all UUIDs start with "0" in UUIDv7).
	_ = m1
	_ = m2

	// The first 1 character of a UUIDv7 ID is always '0' so "0" is a valid prefix
	// that matches all memories. Use length 8 to satisfy minimum-prefix semantics.
	// Find a common prefix by using the first 8 chars of m1's ID, then insert a
	// second memory with the same prefix via direct Update.
	prefix := m1.ID[:3]
	// Brute-force: count how many match; if only 1, use "0" family.
	// Instead, force a known prefix collision by updating m2's id via SQL.
	_, err := s.db.ExecContext(ctx,
		"UPDATE memories SET id = ? WHERE id = ?",
		prefix+"00000-0000-0000-0000-000000000001",
		m1.ID,
	)
	if err != nil {
		t.Skipf("direct ID update failed (expected): %v", err)
	}
	_, err = s.db.ExecContext(ctx,
		"UPDATE memories SET id = ? WHERE id = ?",
		prefix+"00000-0000-0000-0000-000000000002",
		m2.ID,
	)
	if err != nil {
		t.Skipf("direct ID update failed (expected): %v", err)
	}

	_, err = s.GetByIDPrefix(ctx, prefix)
	if err == nil {
		t.Fatal("expected ErrAmbiguousSeed, got nil error")
	}
	if !errors.Is(err, model.ErrAmbiguousSeed) {
		t.Errorf("expected ErrAmbiguousSeed wrapped in error, got: %v", err)
	}
}

// TestStore_GetByIDPrefix_NoMatch verifies that (nil, nil) is returned when
// the prefix matches nothing.
func TestStore_GetByIDPrefix_NoMatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetByIDPrefix(ctx, "ffffffff")
	if err != nil {
		t.Fatalf("GetByIDPrefix: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

// TestStore_GetByIDPrefix_DeletedExcluded verifies that deleted memories are
// not returned by GetByIDPrefix.
func TestStore_GetByIDPrefix_DeletedExcluded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, _ := s.Create(ctx, makeMemory())
	if err := s.SoftDelete(ctx, m.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	got, err := s.GetByIDPrefix(ctx, m.ID[:8])
	if err != nil {
		t.Fatalf("GetByIDPrefix: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for deleted memory, got %+v", got)
	}
}

// TestStore_GetByIDPrefix_LongPrefix12 verifies that a 12-hex-char prefix (which
// crosses the hyphen at position 9 of the stored UUID) correctly matches the
// memory. This was broken before the REPLACE fix: LIKE '019de0f50a94%' would
// never match '019de0f5-0a94-...' because the hyphen is at position 9.
func TestStore_GetByIDPrefix_LongPrefix12(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, err := s.Create(ctx, makeMemory())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Build a 12-char hex prefix from the UUID without hyphens.
	rawID := strings.ReplaceAll(m.ID, "-", "")
	prefix := rawID[:12]

	got, err := s.GetByIDPrefix(ctx, prefix)
	if err != nil {
		t.Fatalf("GetByIDPrefix (12-char prefix): %v", err)
	}
	if got == nil {
		t.Fatal("expected memory, got nil")
	}
	if got.ID != m.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, m.ID)
	}
}

// TestStore_GetByIDPrefix_FullWithoutDashes verifies that the full UUID without
// hyphens (32 hex chars) resolves to exactly the matching memory.
func TestStore_GetByIDPrefix_FullWithoutDashes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, err := s.Create(ctx, makeMemory())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Strip all hyphens to get a 32-char hex string.
	prefix := strings.ReplaceAll(m.ID, "-", "")

	got, err := s.GetByIDPrefix(ctx, prefix)
	if err != nil {
		t.Fatalf("GetByIDPrefix (full-without-dashes): %v", err)
	}
	if got == nil {
		t.Fatal("expected memory, got nil")
	}
	if got.ID != m.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, m.ID)
	}
}

// ─── GetByTopicKey tests ──────────────────────────────────────────────────────

// TestStore_GetByTopicKey_Found verifies that a memory with the given topic_key
// in the correct project is returned.
func TestStore_GetByTopicKey_Found(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := makeMemory()
	m.TopicKey = "architecture/test-service"
	m.Project = "myproject"
	created, err := s.Create(ctx, m)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetByTopicKey(ctx, "architecture/test-service", "myproject")
	if err != nil {
		t.Fatalf("GetByTopicKey: %v", err)
	}
	if got == nil {
		t.Fatal("expected memory, got nil")
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, created.ID)
	}
	if got.TopicKey != "architecture/test-service" {
		t.Errorf("TopicKey mismatch: got %q", got.TopicKey)
	}
}

// TestStore_GetByTopicKey_NotFound verifies (nil, nil) when the topic_key does
// not exist.
func TestStore_GetByTopicKey_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetByTopicKey(ctx, "nonexistent/key", "myproject")
	if err != nil {
		t.Fatalf("GetByTopicKey: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

// TestStore_GetByTopicKey_WrongProject verifies that a topic_key for a
// different project is not returned.
func TestStore_GetByTopicKey_WrongProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := makeMemory()
	m.TopicKey = "architecture/shared"
	m.Project = "project-a"
	_, err := s.Create(ctx, m)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetByTopicKey(ctx, "architecture/shared", "project-b")
	if err != nil {
		t.Fatalf("GetByTopicKey: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for wrong project, got %+v", got)
	}
}

// ─── GetMemoryMetadata tests ──────────────────────────────────────────────────

// TestStore_GetMemoryMetadata_Found verifies that the lightweight projection
// returns the correct title, topic_key, type, and a non-zero ContentLen.
func TestStore_GetMemoryMetadata_Found(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := &model.Memory{
		Type:     model.TypeDecision,
		Scope:    model.ScopeProject,
		Title:    "Metadata test memory",
		Content:  "Content for metadata testing purposes.",
		TopicKey: "test/metadata-key",
		Project:  "myproject",
	}
	created, err := s.Create(ctx, m)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	meta, err := s.GetMemoryMetadata(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMemoryMetadata: %v", err)
	}
	if meta == nil {
		t.Fatal("expected metadata, got nil")
	}
	if meta.ID != created.ID {
		t.Errorf("ID: got %q, want %q", meta.ID, created.ID)
	}
	if meta.Title != "Metadata test memory" {
		t.Errorf("Title: got %q, want %q", meta.Title, "Metadata test memory")
	}
	if meta.TopicKey != "test/metadata-key" {
		t.Errorf("TopicKey: got %q, want %q", meta.TopicKey, "test/metadata-key")
	}
	if meta.Type != model.TypeDecision {
		t.Errorf("Type: got %q, want %q", meta.Type, model.TypeDecision)
	}
	if meta.ContentLen <= 0 {
		t.Errorf("ContentLen: expected > 0, got %d", meta.ContentLen)
	}
}

// TestStore_GetMemoryMetadata_NotFound verifies that (nil, nil) is returned
// when no active memory exists with the given id.
func TestStore_GetMemoryMetadata_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	meta, err := s.GetMemoryMetadata(ctx, "nonexistent-id")
	if err != nil {
		t.Fatalf("GetMemoryMetadata: %v", err)
	}
	if meta != nil {
		t.Fatalf("expected nil, got %+v", meta)
	}
}

// TestStore_GetMemoryMetadata_Deleted verifies that deleted memories are
// excluded from GetMemoryMetadata results.
func TestStore_GetMemoryMetadata_Deleted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, _ := s.Create(ctx, makeMemory())
	if err := s.SoftDelete(ctx, m.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	meta, err := s.GetMemoryMetadata(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMemoryMetadata: %v", err)
	}
	if meta != nil {
		t.Fatalf("expected nil for deleted memory, got %+v", meta)
	}
}

// TestCreate_SourceRoundTrip verifies that Memory.Source (SPEC-092 provenance)
// survives a Create → Get round trip and that a memory created without an
// explicit Source resolves to "" (hand-authored default, unchanged behaviour
// for every memory saved before this field existed).
func TestCreate_SourceRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	plain, err := s.Create(ctx, makeMemory())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(ctx, plain.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Source != "" {
		t.Errorf("Source: got %q, want empty for hand-authored memory", got.Source)
	}

	stamped := makeMemory()
	stamped.Type = model.TypeRule
	stamped.AppliesTo = []string{"**"}
	stamped.Source = "profile:chatea-pro"
	created, err := s.Create(ctx, stamped)
	if err != nil {
		t.Fatalf("Create stamped: %v", err)
	}
	gotStamped, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get stamped: %v", err)
	}
	if gotStamped.Source != "profile:chatea-pro" {
		t.Errorf("Source: got %q, want %q", gotStamped.Source, "profile:chatea-pro")
	}
}

// TestList_SourceFilter verifies that ListOptions.Source restricts results to
// memories with an exact provenance match, and that the zero value (empty
// string) leaves existing listings unaffected (SPEC-092 AC3).
func TestList_SourceFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		m := makeMemory()
		m.Project = "proj-src"
		m.Source = "profile:chatea-pro"
		if _, err := s.Create(ctx, m); err != nil {
			t.Fatalf("Create profile-sourced: %v", err)
		}
	}
	handAuthored := makeMemory()
	handAuthored.Project = "proj-src"
	if _, err := s.Create(ctx, handAuthored); err != nil {
		t.Fatalf("Create hand-authored: %v", err)
	}

	filtered, err := s.List(ctx, ListOptions{Project: "proj-src", Source: "profile:chatea-pro"})
	if err != nil {
		t.Fatalf("List with Source filter: %v", err)
	}
	if len(filtered) != 2 {
		t.Errorf("expected 2 profile-sourced memories, got %d", len(filtered))
	}

	unfiltered, err := s.List(ctx, ListOptions{Project: "proj-src"})
	if err != nil {
		t.Fatalf("List without Source filter: %v", err)
	}
	if len(unfiltered) != 3 {
		t.Errorf("expected 3 memories with no Source filter, got %d", len(unfiltered))
	}
}

// TestHardDeleteBySource verifies the core deletion primitive behind a
// profile switch (SPEC-092 AC5/R1): it physically removes every memory
// carrying the given provenance — the row no longer exists at all, not even
// as a soft-deleted tombstone — leaves hand-authored memories (Source="")
// intact, is scoped by project, and is idempotent.
func TestHardDeleteBySource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const source = "profile:chatea-pro"
	var stampedIDs []string
	for i := 0; i < 3; i++ {
		m := makeMemory()
		m.Project = "proj-x"
		m.Type = model.TypeRule
		m.AppliesTo = []string{"**"}
		m.Source = source
		created, err := s.Create(ctx, m)
		if err != nil {
			t.Fatalf("Create stamped %d: %v", i, err)
		}
		stampedIDs = append(stampedIDs, created.ID)
	}

	handAuthored := makeMemory()
	handAuthored.Project = "proj-x"
	handAuthoredCreated, err := s.Create(ctx, handAuthored)
	if err != nil {
		t.Fatalf("Create hand-authored: %v", err)
	}

	otherProject := makeMemory()
	otherProject.Project = "proj-y"
	otherProject.Type = model.TypeRule
	otherProject.AppliesTo = []string{"**"}
	otherProject.Source = source
	otherCreated, err := s.Create(ctx, otherProject)
	if err != nil {
		t.Fatalf("Create other-project stamped: %v", err)
	}

	deleted, err := s.HardDeleteBySource(ctx, "proj-x", source)
	if err != nil {
		t.Fatalf("HardDeleteBySource: %v", err)
	}
	if len(deleted) != 3 {
		t.Fatalf("expected 3 deleted ids, got %d (%v)", len(deleted), deleted)
	}
	for _, id := range stampedIDs {
		found := false
		for _, d := range deleted {
			if d == id {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %s among deleted ids %v", id, deleted)
		}
	}

	// The rows must be gone entirely — not even a soft-deleted tombstone: a
	// direct COUNT(*) with no deleted_at filter must be zero.
	var remaining int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM memories WHERE id IN (?, ?, ?)",
		stampedIDs[0], stampedIDs[1], stampedIDs[2],
	).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 0 {
		t.Errorf("expected 0 rows remaining for purged ids, got %d", remaining)
	}

	// Hand-authored memory in the same project is untouched.
	got, err := s.Get(ctx, handAuthoredCreated.ID)
	if err != nil {
		t.Fatalf("Get hand-authored after purge: %v", err)
	}
	if got == nil {
		t.Fatal("expected hand-authored memory to survive the purge")
	}

	// A same-source memory in a different project is untouched.
	gotOther, err := s.Get(ctx, otherCreated.ID)
	if err != nil {
		t.Fatalf("Get other-project after purge: %v", err)
	}
	if gotOther == nil {
		t.Fatal("expected other-project memory with the same source to survive the purge")
	}

	// Idempotent: a second call finds nothing left to delete.
	deletedAgain, err := s.HardDeleteBySource(ctx, "proj-x", source)
	if err != nil {
		t.Fatalf("HardDeleteBySource (second call): %v", err)
	}
	if len(deletedAgain) != 0 {
		t.Errorf("expected 0 deleted on second call, got %d", len(deletedAgain))
	}
}

// TestHardDeleteBySource_FTSInvariant verifies that the DELETE fired by
// HardDeleteBySource passes through the existing memories_ad AFTER DELETE
// trigger, keeping memories_fts consistent — the same invariant the
// retention-based HardDelete already relies on (SPEC-092 R6).
func TestHardDeleteBySource_FTSInvariant(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := makeMemory()
	m.Project = "proj-fts"
	m.Type = model.TypeRule
	m.AppliesTo = []string{"**"}
	m.Source = "profile:chatea-pro"
	m.Title = "Unmistakable searchable title xyzzy"
	created, err := s.Create(ctx, m)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	before, err := s.FTS5Search(ctx, "xyzzy", SearchOptions{Project: "proj-fts"})
	if err != nil {
		t.Fatalf("FTS5Search before purge: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("expected 1 FTS hit before purge, got %d", len(before))
	}

	if _, err := s.HardDeleteBySource(ctx, "proj-fts", "profile:chatea-pro"); err != nil {
		t.Fatalf("HardDeleteBySource: %v", err)
	}

	after, err := s.FTS5Search(ctx, "xyzzy", SearchOptions{Project: "proj-fts"})
	if err != nil {
		t.Fatalf("FTS5Search after purge: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("expected 0 FTS hits after purge, got %d (ids: %v)", len(after), func() []string {
			ids := make([]string, len(after))
			for i, r := range after {
				ids[i] = r.ID
			}
			return ids
		}())
	}
	_ = created
}

// TestHardDeleteBySource_BeginTxErrorPropagates verifies that HardDeleteBySource
// surfaces a failure starting the transaction (here: the underlying *sql.DB is
// already closed) instead of panicking or silently succeeding.
// TestHardDeleteBySource_EmptyProjectMatchesNullRows (SPEC-105 AC22) proves
// the exact bug found in §1.2 of the spec: insertMemory persists an empty
// Project as SQL NULL (toNullString), so `WHERE project = ''` never matches
// those rows. HardDeleteBySource(ctx, "", source) must reach them.
func TestHardDeleteBySource_EmptyProjectMatchesNullRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const source = "profile:chatea-pro"
	var orphanIDs []string
	for i := 0; i < 3; i++ {
		m := makeMemory()
		m.Project = "" // persisted as NULL, not ""
		m.Type = model.TypeRule
		m.AppliesTo = []string{"**"}
		m.Source = source
		created, err := s.Create(ctx, m)
		if err != nil {
			t.Fatalf("Create orphan %d: %v", i, err)
		}
		orphanIDs = append(orphanIDs, created.ID)
	}

	deleted, err := s.HardDeleteBySource(ctx, "", source)
	if err != nil {
		t.Fatalf("HardDeleteBySource: %v", err)
	}
	if len(deleted) != 3 {
		t.Fatalf("expected 3 deleted rows with project=NULL, got %d (ids: %v)", len(deleted), deleted)
	}

	var remaining int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM memories WHERE id IN (?, ?, ?)",
		orphanIDs[0], orphanIDs[1], orphanIDs[2],
	).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 0 {
		t.Errorf("expected 0 rows remaining for the NULL-project orphans, got %d", remaining)
	}
}

// TestHardDeleteBySource_NamedProjectDoesNotTouchNullRows (SPEC-105 AC22)
// verifies the companion guarantee: calling with a non-empty project must
// never reach the NULL-project rows — the two clauses of DD9's new WHERE
// must stay mutually exclusive.
func TestHardDeleteBySource_NamedProjectDoesNotTouchNullRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const source = "profile:chatea-pro"

	orphan := makeMemory()
	orphan.Project = ""
	orphan.Type = model.TypeRule
	orphan.AppliesTo = []string{"**"}
	orphan.Source = source
	orphanCreated, err := s.Create(ctx, orphan)
	if err != nil {
		t.Fatalf("Create orphan: %v", err)
	}

	deleted, err := s.HardDeleteBySource(ctx, "some-project", source)
	if err != nil {
		t.Fatalf("HardDeleteBySource: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("expected 0 deleted rows for a named project with no matching rows, got %d", len(deleted))
	}

	if _, err := s.Get(ctx, orphanCreated.ID); err != nil {
		t.Fatalf("expected orphan row to survive a named-project purge, got err=%v", err)
	}
}

// TestHardDeleteBySource_ReturnedIDsMatchDeletedRows guards against the
// SELECT and DELETE clauses of HardDeleteBySource silently diverging: the
// ids it reports must be exactly the ids that no longer exist afterward.
func TestHardDeleteBySource_ReturnedIDsMatchDeletedRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const source = "profile:chatea-pro"
	m := makeMemory()
	m.Project = ""
	m.Type = model.TypeRule
	m.AppliesTo = []string{"**"}
	m.Source = source
	created, err := s.Create(ctx, m)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	deleted, err := s.HardDeleteBySource(ctx, "", source)
	if err != nil {
		t.Fatalf("HardDeleteBySource: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != created.ID {
		t.Fatalf("expected deleted ids [%s], got %v", created.ID, deleted)
	}

	var remaining int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM memories WHERE id = ?", created.ID,
	).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 0 {
		t.Errorf("expected the reported id to actually be gone, found %d rows", remaining)
	}
}

// TestList_BySourceAndScope_IsolatesOrphans verifies that List with Source
// and Scope filters (and no Project filter) surfaces exactly the
// project-scoped, provenance-marked rows that leaked into the store with an
// empty project — the primitive PurgeProfileRules' orphan sweep relies on
// (SPEC-105 DD9 — no new store primitive needed, ListOptions.Source already
// exists).
func TestList_BySourceAndScope_IsolatesOrphans(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const source = "profile:chatea-pro"

	orphan := makeMemory()
	orphan.Project = ""
	orphan.Type = model.TypeRule
	orphan.AppliesTo = []string{"**"}
	orphan.Source = source
	if _, err := s.Create(ctx, orphan); err != nil {
		t.Fatalf("Create orphan: %v", err)
	}

	scoped := makeMemory()
	scoped.Project = "proj-x"
	scoped.Type = model.TypeRule
	scoped.AppliesTo = []string{"**"}
	scoped.Source = source
	if _, err := s.Create(ctx, scoped); err != nil {
		t.Fatalf("Create scoped: %v", err)
	}

	unrelated := makeMemory()
	unrelated.Project = ""
	if _, err := s.Create(ctx, unrelated); err != nil {
		t.Fatalf("Create unrelated: %v", err)
	}

	got, err := s.List(ctx, ListOptions{
		Type:   model.TypeRule,
		Scope:  model.ScopeProject,
		Source: source,
		Limit:  100,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 rows (orphan + scoped) with source=%q, got %d", source, len(got))
	}
	for _, m := range got {
		if m.Source != source {
			t.Errorf("unexpected source %q in result", m.Source)
		}
	}
}

func TestHardDeleteBySource_BeginTxErrorPropagates(t *testing.T) {
	s := newTestStore(t)
	if err := s.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	if _, err := s.HardDeleteBySource(context.Background(), "proj-x", "profile:chatea-pro"); err == nil {
		t.Fatal("expected an error hard-deleting by source against a closed database")
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
