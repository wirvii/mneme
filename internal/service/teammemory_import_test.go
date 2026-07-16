package service_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// writeSharedNote writes a minimal, valid team-memory vault note (UUID-flat
// layout, matching PathModeUUID) at <notesDir>/<id>.md. shared and author are
// written verbatim into the frontmatter when non-empty, exactly mirroring
// what materializeTeamMemory would have produced on a peer's machine.
func writeSharedNote(t *testing.T, notesDir, id, topicKey, title, content, shared, author string, updatedAt time.Time) string {
	t.Helper()
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", notesDir, err)
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "id: %s\n", id)
	sb.WriteString("type: decision\n")
	sb.WriteString("scope: project\n")
	fmt.Fprintf(&sb, "title: %q\n", title)
	if topicKey != "" {
		fmt.Fprintf(&sb, "topic_key: %s\n", topicKey)
	}
	sb.WriteString("project: test/project\n")
	sb.WriteString("importance: 0.80\n")
	sb.WriteString("confidence: 0.80\n")
	sb.WriteString("decay_rate: 0.01\n")
	sb.WriteString("created_at: 2026-01-01T00:00:00Z\n")
	fmt.Fprintf(&sb, "updated_at: %s\n", updatedAt.UTC().Format(time.RFC3339Nano))
	sb.WriteString("revision_count: 0\n")
	if shared != "" {
		fmt.Fprintf(&sb, "shared: %s\n", shared)
	}
	if author != "" {
		fmt.Fprintf(&sb, "author: %s\n", author)
	}
	sb.WriteString("---\n\n")
	sb.WriteString(content)
	sb.WriteString("\n")

	path := filepath.Join(notesDir, id+".md")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// findByTopicKey lists all project memories and returns the one matching
// topicKey, failing the test if none is found.
func findByTopicKey(t *testing.T, svc interface {
	List(ctx context.Context, opts store.ListOptions) ([]*model.Memory, error)
}, ctx context.Context, project, topicKey string) *model.Memory {
	t.Helper()
	mems, err := svc.List(ctx, store.ListOptions{Project: project, Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, m := range mems {
		if m.TopicKey == topicKey {
			return m
		}
	}
	t.Fatalf("no memory found with topic_key %q", topicKey)
	return nil
}

func TestImportFromShared_CreatesAndPreservesSharedAuthor(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	notesDir := filepath.Join(repoDir, ".mneme", "shared", "notes")
	writeSharedNote(t, notesDir, "01938f1b-abcd-7abc-8def-000000000001", "team/decision-a",
		"Decision A", "Content A", "1", "Alice <alice@example.com>", time.Now().UTC())
	writeSharedNote(t, notesDir, "01938f1b-abcd-7abc-8def-000000000002", "team/decision-b",
		"Decision B", "Content B", "2", "Bob <bob@example.com>", time.Now().UTC())

	result, err := svc.ImportFromShared(ctx, repoDir)
	if err != nil {
		t.Fatalf("ImportFromShared: %v", err)
	}
	if result.Created != 2 {
		t.Errorf("Created: got %d, want 2", result.Created)
	}
	if result.Errors != 0 {
		t.Errorf("Errors: got %d, want 0", result.Errors)
	}

	a := findByTopicKey(t, svc, ctx, "test/project", "team/decision-a")
	if a.ID != "01938f1b-abcd-7abc-8def-000000000001" {
		t.Errorf("decision-a ID = %q, want the frontmatter id preserved (SPEC-053 D1)", a.ID)
	}
	if a.Shared != 1 {
		t.Errorf("decision-a Shared = %d, want 1 (from frontmatter)", a.Shared)
	}
	if a.Author != "Alice <alice@example.com>" {
		t.Errorf("decision-a Author = %q, want the frontmatter author, not the local git identity", a.Author)
	}

	b := findByTopicKey(t, svc, ctx, "test/project", "team/decision-b")
	if b.Shared != 2 {
		t.Errorf("decision-b Shared = %d, want 2 (from frontmatter)", b.Shared)
	}
	if b.Author != "Bob <bob@example.com>" {
		t.Errorf("decision-b Author = %q, want the frontmatter author, not the local git identity", b.Author)
	}
}

// TestImportFromShared_NeverRematerializes is the anti-loop guard test
// (SPEC-053 D5): a freshly-created memory always receives a NEW local id
// (the public store API cannot preserve a caller-supplied id), so if
// materialization were NOT suppressed during import, Save would write a
// brand new file under notes/<newID>.md — doubling the file count. Asserting
// the file count stays exactly what was written by hand proves the guard.
func TestImportFromShared_NeverRematerializes(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	notesDir := filepath.Join(repoDir, ".mneme", "shared", "notes")
	writeSharedNote(t, notesDir, "01938f1b-abcd-7abc-8def-000000000003", "team/decision-c",
		"Decision C", "Content C", "1", "Carol <carol@example.com>", time.Now().UTC())

	result, err := svc.ImportFromShared(ctx, repoDir)
	if err != nil {
		t.Fatalf("ImportFromShared: %v", err)
	}
	if result.Created != 1 {
		t.Fatalf("Created: got %d, want 1", result.Created)
	}

	entries, err := os.ReadDir(notesDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected exactly 1 file under notes/ after import (no re-materialization), found %d: %v", len(entries), entries)
	}
}

// TestImportFromShared_RerunDoesNotDuplicate_NoTopicKey is the regression
// test for the QA-flagged C1 defect: a peer's note WITHOUT a topic_key must
// not duplicate on a second import run. Before the fix, a note not found
// locally by id was always routed through Save, which assigns a fresh
// UUIDv7 on every Create — so the second run's lookup by the note's
// original fm.ID would still fail (the locally-created row has a DIFFERENT
// id), producing a second row via Save's topic_key-less Upsert-Create path.
// Preserving fm.ID via store.CreateWithID makes the second run's lookup by
// id succeed, so it correctly resolves to "skipped" (or "updated") instead
// of creating a duplicate.
func TestImportFromShared_RerunDoesNotDuplicate_NoTopicKey(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	const noteID = "01938f1b-abcd-7abc-8def-0000000000aa"
	notesDir := filepath.Join(repoDir, ".mneme", "shared", "notes")
	writeSharedNote(t, notesDir, noteID, "", // no topic_key — the defect's exact trigger condition
		"Peer note without a topic key", "This must not duplicate on re-import",
		"1", "Heidi <heidi@example.com>", time.Now().UTC())

	first, err := svc.ImportFromShared(ctx, repoDir)
	if err != nil {
		t.Fatalf("first ImportFromShared: %v", err)
	}
	if first.Created != 1 {
		t.Fatalf("first run Created: got %d, want 1", first.Created)
	}

	second, err := svc.ImportFromShared(ctx, repoDir)
	if err != nil {
		t.Fatalf("second ImportFromShared: %v", err)
	}
	if second.Created != 0 {
		t.Errorf("second run Created: got %d, want 0 (must not duplicate)", second.Created)
	}

	mems, err := svc.List(ctx, store.ListOptions{Project: "test/project", Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	matches := 0
	var found *model.Memory
	for _, m := range mems {
		if m.ID == noteID {
			matches++
			found = m
		}
	}
	if matches != 1 {
		t.Fatalf("expected exactly 1 local memory with id %s after two import runs, found %d", noteID, matches)
	}
	if found.Content != "This must not duplicate on re-import" {
		t.Errorf("unexpected content: %q", found.Content)
	}
}

// TestImportFromShared_UpdatesExistingByID_ForcesFrontmatterFields verifies
// the merge branch: when the vault note's id already exists locally and the
// file is newer, the local record is updated and its shared/author columns
// are forced to match the frontmatter — not the local git identity that
// Update's own team-memory bake logic would otherwise apply.
func TestImportFromShared_UpdatesExistingByID_ForcesFrontmatterFields(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	// Use WithSuppressMaterialize so this setup Save doesn't write a vault
	// file of its own that could confuse the later file-count reasoning
	// (SPEC-071: discovery now auto-shares by default, so without this guard
	// Save itself would materialize a file before the test's writeSharedNote
	// overwrites it).
	resp, err := svc.Save(service.WithSuppressMaterialize(ctx), model.SaveRequest{
		Title:   "Original title",
		Content: "Original content",
		Type:    model.TypeDiscovery, // auto-shares to 1 (SPEC-071); the import below forces it to 2 regardless.
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	existing, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	notesDir := filepath.Join(repoDir, ".mneme", "shared", "notes")
	writeSharedNote(t, notesDir, resp.ID, "", "Peer's updated title", "Updated content from a peer",
		"2", "Carol <carol@example.com>", existing.UpdatedAt.Add(2*time.Second))

	result, err := svc.ImportFromShared(ctx, repoDir)
	if err != nil {
		t.Fatalf("ImportFromShared: %v", err)
	}
	if result.Updated != 1 {
		t.Errorf("Updated: got %d, want 1", result.Updated)
	}
	if result.Created != 0 {
		t.Errorf("Created: got %d, want 0", result.Created)
	}

	after, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get after import: %v", err)
	}
	if after.Content != "Updated content from a peer" {
		t.Errorf("Content = %q, want the imported content", after.Content)
	}
	if after.Shared != 2 {
		t.Errorf("Shared = %d, want 2 (forced from frontmatter)", after.Shared)
	}
	if after.Author != "Carol <carol@example.com>" {
		t.Errorf("Author = %q, want the frontmatter author", after.Author)
	}
}

// TestImportFromShared_SkipsWhenDBNewer verifies the merge strategy: a vault
// note older than (or equal to) the local record's updated_at is skipped —
// the local content is never overwritten by stale peer data.
func TestImportFromShared_SkipsWhenDBNewer(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	// Explicit Shared=0 override (SPEC-071: discovery auto-shares to 1 by
	// default now) so the test can assert a skipped import leaves an
	// intentionally-local memory's Shared field untouched.
	zero := 0
	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Local, newer title",
		Content: "Local, newer content",
		Type:    model.TypeDiscovery,
		Shared:  &zero,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	existing, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	notesDir := filepath.Join(repoDir, ".mneme", "shared", "notes")
	writeSharedNote(t, notesDir, resp.ID, "", "Stale peer title", "Stale peer content",
		"1", "Dave <dave@example.com>", existing.UpdatedAt.Add(-1*time.Hour))

	result, err := svc.ImportFromShared(ctx, repoDir)
	if err != nil {
		t.Fatalf("ImportFromShared: %v", err)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped: got %d, want 1", result.Skipped)
	}

	after, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get after import: %v", err)
	}
	if after.Content != "Local, newer content" {
		t.Errorf("Content changed unexpectedly: %q", after.Content)
	}
	if after.Shared != 0 {
		t.Errorf("Shared changed unexpectedly on a skipped import: got %d, want 0", after.Shared)
	}
}

// TestImportFromShared_SkipsSubagentManifestNote_LocalManifestSurvives is
// SPEC-089 Part 3's AC9, exercised against the ACTUAL production entry point
// a corrupted manifest arrives through: the post-merge/post-checkout git
// hook ("mneme team-memory hooks run-import") calls ImportFromShared, not
// VaultImport — patching only vault_import.go's importNote would leave the
// real incident (novo's manifest clobbered after a plain "git pull")
// unfixed.
//
// Reproduces the exact clobber mechanism: the foreign note reuses the LOCAL
// manifest's own id (the realistic shape once two peers' manifests have
// already converged on one id via an earlier import — TestImportFromShared_
// CreatesAndPreservesSharedAuthor above shows ImportFromShared always
// preserves the frontmatter id) with a NEWER timestamp and DIVERGENT
// content. Without the topic_key skip, importSharedNote's existing!=nil
// branch would call svc.Update and clobber the local manifest in place.
func TestImportFromShared_SkipsSubagentManifestNote_LocalManifestSurvives(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	subSvc := service.NewSubagentService(svc)
	localResp, err := subSvc.SaveManifest(ctx, "test/project", []service.ManifestEntry{
		{Role: "backend", Path: ".claude/agents/backend.md", Version: 2},
	})
	if err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	before, err := svc.Get(ctx, localResp.ID)
	if err != nil {
		t.Fatalf("Get local manifest: %v", err)
	}

	notesDir := filepath.Join(repoDir, ".mneme", "shared", "notes")
	foreignContent := `[{"role":"bug-hunter","path":"/Users/other/chateaprov3/.claude/agents/bug-hunter.md","version":1}]`
	writeManifestSharedNote(t, notesDir, localResp.ID, foreignContent, before.UpdatedAt.Add(1*time.Hour))

	result, err := svc.ImportFromShared(ctx, repoDir)
	if err != nil {
		t.Fatalf("ImportFromShared: %v", err)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 — the manifest note must always be skipped", result.Skipped)
	}
	if result.Created != 0 || result.Updated != 0 {
		t.Errorf("Created=%d Updated=%d, want both 0 — the manifest note must never be persisted", result.Created, result.Updated)
	}

	after, err := svc.Get(ctx, localResp.ID)
	if err != nil {
		t.Fatalf("Get local manifest after import: %v", err)
	}
	if after.Content != before.Content {
		t.Errorf("local manifest was clobbered by the foreign note:\nbefore: %s\nafter:  %s", before.Content, after.Content)
	}
	if strings.Contains(after.Content, "bug-hunter") || strings.Contains(after.Content, "chateaprov3") {
		t.Errorf("local manifest content shows foreign contamination: %s", after.Content)
	}
}

// writeManifestSharedNote writes a minimal, valid team-memory vault note
// shaped like a materialized subagent manifest (type=config, topic_key
// subagents/manifest) at <notesDir>/<id>.md — writeSharedNote (above)
// hardcodes type=decision, so this is a dedicated variant for the one
// SPEC-089 fixture that needs a different type.
func writeManifestSharedNote(t *testing.T, notesDir, id, content string, updatedAt time.Time) string {
	t.Helper()
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", notesDir, err)
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "id: %s\n", id)
	sb.WriteString("type: config\n")
	sb.WriteString("scope: project\n")
	sb.WriteString("title: \"Subagent manifest\"\n")
	sb.WriteString("topic_key: subagents/manifest\n")
	sb.WriteString("project: test/project\n")
	sb.WriteString("importance: 0.90\n")
	sb.WriteString("confidence: 0.80\n")
	sb.WriteString("decay_rate: 0.005\n")
	sb.WriteString("created_at: 2026-01-01T00:00:00Z\n")
	fmt.Fprintf(&sb, "updated_at: %s\n", updatedAt.UTC().Format(time.RFC3339Nano))
	sb.WriteString("revision_count: 0\n")
	sb.WriteString("shared: 1\n")
	sb.WriteString("author: Foreign Peer <foreign@example.com>\n")
	sb.WriteString("---\n\n")
	sb.WriteString(content)
	sb.WriteString("\n")

	path := filepath.Join(notesDir, id+".md")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestImportFromShared_ReportsConflictCandidates verifies SPEC-053 D6: after
// importing a memory whose title/content shares salient terms with an
// existing local memory, ImportFromShared reports a non-zero
// ConflictCandidates count. No judgment happens — this is a deterministic
// FTS5 count only.
func TestImportFromShared_ReportsConflictCandidates(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	if _, err := svc.Save(ctx, model.SaveRequest{
		Title:   "JWT authentication token",
		Content: "We use JWT tokens for authentication in the API gateway",
		Type:    model.TypeDecision,
	}); err != nil {
		t.Fatalf("Save (existing local decision): %v", err)
	}

	notesDir := filepath.Join(repoDir, ".mneme", "shared", "notes")
	writeSharedNote(t, notesDir, "01938f1b-abcd-7abc-8def-000000000004", "team/decision-conflict",
		"Token authentication system", "Authentication using signed tokens for API access control",
		"1", "Erin <erin@example.com>", time.Now().UTC())

	result, err := svc.ImportFromShared(ctx, repoDir)
	if err != nil {
		t.Fatalf("ImportFromShared: %v", err)
	}
	if result.Created != 1 {
		t.Fatalf("Created: got %d, want 1", result.Created)
	}
	if result.ConflictCandidates == 0 {
		t.Error("expected ConflictCandidates > 0 for overlapping salient terms, got 0")
	}
}

// TestImportFromShared_MissingVaultDir verifies the fatal-error contract:
// a repository without .mneme/shared returns an error instead of silently
// importing nothing.
func TestImportFromShared_MissingVaultDir(t *testing.T) {
	svc, repoDir := newRepoTestService(t, false)
	ctx := context.Background()

	_, err := svc.ImportFromShared(ctx, repoDir)
	if err == nil {
		t.Fatal("expected an error when .mneme/shared does not exist, got nil")
	}
}

// TestImportFromShared_ProjectMismatch verifies the fatal-error contract:
// a vault marker belonging to a different project aborts the import.
func TestImportFromShared_ProjectMismatch(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	// Overwrite the marker (written by newRepoTestService with project
	// "test/project") with one for a different project.
	markerPath := filepath.Join(repoDir, ".mneme", "shared", ".mneme-vault")
	if err := os.WriteFile(markerPath, []byte(`{"vault_version":1,"project":"someone/else","scope":"shared"}`), 0o644); err != nil {
		t.Fatalf("overwrite marker: %v", err)
	}

	_, err := svc.ImportFromShared(ctx, repoDir)
	if err == nil {
		t.Fatal("expected an error for a project-mismatched marker, got nil")
	}
}

// TestImportFromShared_InvalidIDIsRecoverable verifies that a note with a
// missing/invalid id is counted as a per-file error and does not abort the
// rest of the import.
func TestImportFromShared_InvalidIDIsRecoverable(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	notesDir := filepath.Join(repoDir, ".mneme", "shared", "notes")
	writeSharedNote(t, notesDir, "not-a-valid-uuid", "team/bad-note",
		"Bad note", "Content", "1", "Frank <frank@example.com>", time.Now().UTC())
	writeSharedNote(t, notesDir, "01938f1b-abcd-7abc-8def-000000000005", "team/good-note",
		"Good note", "Content", "1", "Grace <grace@example.com>", time.Now().UTC())

	result, err := svc.ImportFromShared(ctx, repoDir)
	if err != nil {
		t.Fatalf("ImportFromShared: %v", err)
	}
	if result.Errors != 1 {
		t.Errorf("Errors: got %d, want 1", result.Errors)
	}
	if result.Created != 1 {
		t.Errorf("Created: got %d, want 1", result.Created)
	}
}
