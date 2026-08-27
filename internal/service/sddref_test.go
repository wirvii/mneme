package service_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/gitident"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// sddTestFixture bundles a MemoryService with its own SDDStore and repo
// directory — newRepoTestService's shape (SPEC-085 isolation discipline)
// plus the SDD store WithSDDStore needs, and a handle to the SDDStore
// itself so a test can create backlog/spec rows to anchor against.
type sddTestFixture struct {
	svc          *service.MemoryService
	sddStore     *store.SDDStore
	projectStore *store.MemoryStore
	repoDir      string
}

// newSDDTestFixture mirrors newRepoTestServiceWithIdentity (teammemory_test.go)
// exactly — same HOME/git-config isolation, same chdir+cleanup, same
// gitident.Reset() discipline (SPEC-085 A1: mayAnchor depends on
// gitident.Author(), which memoizes for the whole test binary; skipping
// Reset() here would leak an earlier subtest's identity into this one's
// anchoring decisions) — plus WithSDDStore, which the shared helper doesn't
// wire.
func newSDDTestFixture(t *testing.T, withMarker bool, gitUserName, gitUserEmail string) sddTestFixture {
	t.Helper()

	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "nonexistent-gitconfig"))

	repoDir := t.TempDir()
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.name", gitUserName)
	runGit(t, repoDir, "config", "user.email", gitUserEmail)

	if withMarker {
		sharedRoot := filepath.Join(repoDir, ".mneme", "shared")
		writeMarker(t, sharedRoot, "test/project", "shared")
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir into temp repo: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	gitident.Reset()
	t.Cleanup(gitident.Reset)

	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	sddStore := store.NewSDDStore(projectDB)
	cfg := config.Default()

	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test/project", embed.NopEmbedder{},
		service.WithTeamMemory(service.DetectTeamMemory()),
		service.WithSDDStore(sddStore))

	return sddTestFixture{svc: svc, sddStore: sddStore, projectStore: projectStore, repoDir: repoDir}
}

// writeSharedNoteWithSDDRefs is writeSharedNote (teammemory_import_test.go)
// plus a trailing sdd_refs: block, since AC6/AC11 need to hand-craft a note
// carrying anchors exactly as materializeTeamMemory would have written it
// on a peer's machine.
func writeSharedNoteWithSDDRefs(
	t *testing.T, notesDir, id, topicKey, title, content, shared, author string,
	sddRefs []string, updatedAt time.Time,
) string {
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
	if len(sddRefs) > 0 {
		sb.WriteString("sdd_refs:\n")
		for _, ref := range sddRefs {
			fmt.Fprintf(&sb, "  - %s\n", ref)
		}
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

// TestSDDAnchor_Save_ResolvesLocal covers the straightforward case: a
// mention of a LOCALLY-anchored spec resolves to "local" with its current
// correlative.
func TestSDDAnchor_Save_ResolvesLocal(t *testing.T) {
	fx := newSDDTestFixture(t, false, "Team Member", "team@example.com")
	ctx := context.Background()

	spec := &model.Spec{ID: "SPEC-001", Title: "s", Status: model.SpecStatusDraft, Project: "test/project", Lane: model.LaneStandard}
	if err := fx.sddStore.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}

	resp, err := fx.svc.Save(ctx, model.SaveRequest{
		Title:   "Cites a spec",
		Content: "See SPEC-001 for details.",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := fx.svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.SDDRefs) != 1 {
		t.Fatalf("expected 1 resolved ref, got %d: %+v", len(got.SDDRefs), got.SDDRefs)
	}
	ref := got.SDDRefs[0]
	if ref.RefID != "SPEC-001" || ref.Status != model.SDDRefLocal || ref.LocalID != "SPEC-001" {
		t.Errorf("unexpected resolved ref: %+v", ref)
	}
	if ref.TargetUUID != spec.UUID {
		t.Errorf("TargetUUID = %q, want %q", ref.TargetUUID, spec.UUID)
	}
}

// TestSDDAnchor_Save_UnanchoredMention covers a mention of something never
// registered locally: it resolves to "unanchored", with no TargetUUID.
func TestSDDAnchor_Save_UnanchoredMention(t *testing.T) {
	fx := newSDDTestFixture(t, false, "Team Member", "team@example.com")
	ctx := context.Background()

	resp, err := fx.svc.Save(ctx, model.SaveRequest{
		Title:   "Cites nothing real",
		Content: "See SPEC-999, which does not exist here.",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := fx.svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.SDDRefs) != 1 {
		t.Fatalf("expected 1 resolved ref, got %d: %+v", len(got.SDDRefs), got.SDDRefs)
	}
	ref := got.SDDRefs[0]
	if ref.RefID != "SPEC-999" || ref.Status != model.SDDRefUnanchored || ref.TargetUUID != "" || ref.LocalID != "" {
		t.Errorf("unexpected resolved ref: %+v", ref)
	}
}

// TestSDDAnchor_Update_LocallyAuthored covers bakeSDDRefsForUpdate's main
// branches directly through MemoryService.Update (not Save's topic_key
// re-save path, which TestSDDAnchor_NeverRecomputed already exercises):
// updating a field OTHER than Title/Content never touches refs; updating
// only Content (Title stays nil, falls back to existing.Title) anchors a
// brand-new mention; updating only Title (Content stays nil, falls back to
// existing.Content) still re-resolves the SAME mention already anchored
// (idempotent, since D5's INSERT OR IGNORE keeps the original anchor).
func TestSDDAnchor_Update_LocallyAuthored(t *testing.T) {
	fx := newSDDTestFixture(t, false, "Team Member", "team@example.com")
	ctx := context.Background()

	spec1 := &model.Spec{ID: "SPEC-001", Title: "s1", Status: model.SpecStatusDraft, Project: "test/project", Lane: model.LaneStandard}
	if err := fx.sddStore.CreateSpec(ctx, spec1); err != nil {
		t.Fatalf("CreateSpec SPEC-001: %v", err)
	}
	spec2 := &model.Spec{ID: "SPEC-002", Title: "s2", Status: model.SpecStatusDraft, Project: "test/project", Lane: model.LaneStandard}
	if err := fx.sddStore.CreateSpec(ctx, spec2); err != nil {
		t.Fatalf("CreateSpec SPEC-002: %v", err)
	}

	resp, err := fx.svc.Save(ctx, model.SaveRequest{
		Title:   "No mentions yet",
		Content: "Nothing anchorable here.",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Updating a field OTHER than Title/Content (Importance) must leave
	// SDDRefs untouched (bakeSDDRefsForUpdate's early return when both are
	// nil) — still zero refs.
	imp := 0.5
	if _, err := fx.svc.Update(ctx, resp.ID, model.UpdateRequest{Importance: &imp}); err != nil {
		t.Fatalf("Update (importance only): %v", err)
	}
	got, err := fx.svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get (after importance update): %v", err)
	}
	if len(got.SDDRefs) != 0 {
		t.Fatalf("expected 0 refs after a non-title/content update, got %+v", got.SDDRefs)
	}

	// Updating ONLY Content (Title stays nil -> falls back to existing.Title,
	// which has no mention) anchors the new SPEC-001 mention.
	newContent := "Now cites SPEC-001."
	if _, err := fx.svc.Update(ctx, resp.ID, model.UpdateRequest{Content: &newContent}); err != nil {
		t.Fatalf("Update (content only): %v", err)
	}
	got, err = fx.svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get (after content update): %v", err)
	}
	if len(got.SDDRefs) != 1 || got.SDDRefs[0].RefID != "SPEC-001" || got.SDDRefs[0].Status != model.SDDRefLocal {
		t.Fatalf("expected SPEC-001 anchored locally, got %+v", got.SDDRefs)
	}

	// Updating ONLY Title (Content stays nil -> falls back to existing.Content,
	// which still mentions SPEC-001) re-resolves the SAME mention: the
	// anchor must be unchanged (D5), and the title's own new mention
	// (SPEC-002) gets anchored too.
	newTitle := "Now the title also cites SPEC-002"
	if _, err := fx.svc.Update(ctx, resp.ID, model.UpdateRequest{Title: &newTitle}); err != nil {
		t.Fatalf("Update (title only): %v", err)
	}
	got, err = fx.svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get (after title update): %v", err)
	}
	if len(got.SDDRefs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %+v", len(got.SDDRefs), got.SDDRefs)
	}
	byRef := map[string]model.SDDRef{}
	for _, ref := range got.SDDRefs {
		byRef[ref.RefID] = ref
	}
	if byRef["SPEC-001"].TargetUUID != spec1.UUID {
		t.Errorf("SPEC-001's anchor should survive unchanged, got %+v", byRef["SPEC-001"])
	}
	if byRef["SPEC-002"].Status != model.SDDRefLocal || byRef["SPEC-002"].LocalID != "SPEC-002" {
		t.Errorf("SPEC-002 should have been freshly anchored from the title, got %+v", byRef["SPEC-002"])
	}
}

// TestSDDAnchor_ForeignAuthorNeverAnchored is AC9: a memory whose author is
// someone else never receives new anchors, even when its content is
// updated to add a fresh mention — the mention resolves "unanchored", not
// "local", proving Update never anchored it.
func TestSDDAnchor_ForeignAuthorNeverAnchored(t *testing.T) {
	fx := newSDDTestFixture(t, false, "Team Member", "team@example.com")
	ctx := context.Background()

	spec := &model.Spec{ID: "SPEC-001", Title: "s", Status: model.SpecStatusDraft, Project: "test/project", Lane: model.LaneStandard}
	if err := fx.sddStore.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}

	// Simulate an imported memory: authored by someone else, created
	// directly at the store layer (bypassing Save, which never sets
	// Author itself — only the write-through bake path does, from THIS
	// machine's own identity).
	foreign := &model.Memory{
		Type:    model.TypeDecision,
		Scope:   model.ScopeProject,
		Title:   "Someone else's note",
		Content: "No mentions yet.",
		Project: "test/project",
		Author:  "Someone Else <else@example.com>",
	}
	created, err := fx.projectStore.Create(ctx, foreign)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newContent := "Now mentions SPEC-001, but this machine cannot vouch for the author."
	if _, err := fx.svc.Update(ctx, created.ID, model.UpdateRequest{Content: &newContent}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := fx.svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.SDDRefs) != 1 {
		t.Fatalf("expected 1 resolved ref, got %d: %+v", len(got.SDDRefs), got.SDDRefs)
	}
	ref := got.SDDRefs[0]
	if ref.Status != model.SDDRefUnanchored {
		t.Errorf("expected unanchored (a foreign-authored memory must never anchor), got %+v", ref)
	}
	if ref.TargetUUID != "" {
		t.Errorf("expected no TargetUUID, got %q", ref.TargetUUID)
	}
}

// TestSDDAnchor_NeverRecomputed is AC10: once (memory_id, ref_id) has an
// anchor, re-saving the SAME memory with the SAME mention leaves the
// original target_uuid intact — even if the local SDD row's own anchor
// somehow changed underneath it (a whitebox scenario proving the guarantee
// at the SQL level, D5 rule 3) — while a genuinely NEW mention in the same
// re-save DOES get freshly anchored.
func TestSDDAnchor_NeverRecomputed(t *testing.T) {
	fx := newSDDTestFixture(t, false, "Team Member", "team@example.com")
	ctx := context.Background()

	spec := &model.Spec{ID: "SPEC-001", Title: "s", Status: model.SpecStatusDraft, Project: "test/project", Lane: model.LaneStandard}
	if err := fx.sddStore.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	originalAnchor := spec.UUID

	resp, err := fx.svc.Save(ctx, model.SaveRequest{
		TopicKey: "sdd-refs/never-recomputed",
		Title:    "Cites a spec",
		Content:  "See SPEC-001 for details.",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save (first): %v", err)
	}

	got, err := fx.svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get (first): %v", err)
	}
	if len(got.SDDRefs) != 1 || got.SDDRefs[0].TargetUUID != originalAnchor {
		t.Fatalf("expected the original anchor after the first save, got %+v", got.SDDRefs)
	}

	// Whitebox: mutate the spec's own anchor directly, simulating "the
	// local row's anchor somehow changed" — an anchor is supposed to be
	// immutable in production (D11), so this is only exercisable by
	// reaching under the store layer, which is exactly what this test
	// needs to prove D5 rule 3 at the SQL level.
	if err := fx.sddStore.CreateSpec(ctx, &model.Spec{ID: "SPEC-002", Title: "s2", Status: model.SpecStatusDraft, Project: "test/project", Lane: model.LaneStandard}); err != nil {
		t.Fatalf("CreateSpec SPEC-002: %v", err)
	}

	// Re-save the SAME memory (same topic_key): SPEC-001's mention
	// survives unchanged, and the newly-added SPEC-002 mention gets
	// freshly anchored.
	resp2, err := fx.svc.Save(ctx, model.SaveRequest{
		TopicKey: "sdd-refs/never-recomputed",
		Title:    "Cites a spec",
		Content:  "See SPEC-001 and now also SPEC-002.",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save (second): %v", err)
	}
	if resp2.ID != resp.ID {
		t.Fatalf("expected the same memory (topic_key upsert), got a different id")
	}

	got2, err := fx.svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get (second): %v", err)
	}
	if len(got2.SDDRefs) != 2 {
		t.Fatalf("expected 2 resolved refs, got %d: %+v", len(got2.SDDRefs), got2.SDDRefs)
	}
	byRef := map[string]model.SDDRef{}
	for _, ref := range got2.SDDRefs {
		byRef[ref.RefID] = ref
	}
	if byRef["SPEC-001"].TargetUUID != originalAnchor {
		t.Errorf("SPEC-001's anchor changed: got %q, want the original %q", byRef["SPEC-001"].TargetUUID, originalAnchor)
	}
	if byRef["SPEC-002"].Status != model.SDDRefLocal || byRef["SPEC-002"].LocalID != "SPEC-002" {
		t.Errorf("SPEC-002 should have been freshly anchored, got %+v", byRef["SPEC-002"])
	}
}

// TestSDDAnchor_ImportSharedNote_ForcesRefs is AC11: importing a note with
// sdd_refs: leaves EXACTLY those references, even when the importing
// machine has its own local row for the same correlative with a DIFFERENT
// anchor — SetSDDRefs' forced-replace, not bakeSDDRefsForUpdate's
// INSERT-OR-IGNORE derivation, is what must win.
func TestSDDAnchor_ImportSharedNote_ForcesRefs(t *testing.T) {
	fx := newSDDTestFixture(t, true, "Team Member", "team@example.com")
	ctx := context.Background()

	localSpec := &model.Spec{ID: "SPEC-001", Title: "local", Status: model.SpecStatusDraft, Project: "test/project", Lane: model.LaneStandard}
	if err := fx.sddStore.CreateSpec(ctx, localSpec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	foreignAnchor := "0198f2c1-4a7b-7c3d-9e10-3f4a5b6c7d8e" // deliberately NOT localSpec.UUID.
	if foreignAnchor == localSpec.UUID {
		t.Fatal("test setup invariant broken: foreignAnchor collided with the local anchor")
	}

	notesDir := filepath.Join(fx.repoDir, ".mneme", "shared", "notes")
	noteID := "01938f1b-abcd-7abc-8def-000000000050"
	writeSharedNoteWithSDDRefs(t, notesDir, noteID, "sdd-refs/import-forces",
		"Imported note", "Cites SPEC-001, from the writer's machine.",
		"1", "Writer <writer@example.com>",
		[]string{"SPEC-001=" + foreignAnchor},
		time.Now().UTC(),
	)

	result, err := fx.svc.ImportFromShared(ctx, fx.repoDir)
	if err != nil {
		t.Fatalf("ImportFromShared: %v", err)
	}
	if result.Errors != 0 {
		t.Fatalf("ImportFromShared reported %d errors", result.Errors)
	}

	got, err := fx.svc.Get(ctx, noteID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.SDDRefs) != 1 {
		t.Fatalf("expected exactly 1 ref, got %d: %+v", len(got.SDDRefs), got.SDDRefs)
	}
	ref := got.SDDRefs[0]
	if ref.TargetUUID != foreignAnchor {
		t.Errorf("TargetUUID = %q, want the FORCED foreign anchor %q (not the local row's %q)", ref.TargetUUID, foreignAnchor, localSpec.UUID)
	}
	// The forced anchor resolves "foreign" here: this machine's SPEC-001
	// carries a DIFFERENT anchor, so no local row matches foreignAnchor.
	if ref.Status != model.SDDRefForeign {
		t.Errorf("Status = %q, want foreign", ref.Status)
	}
}

// TestSDDAnchor_CrossDatabaseResolution is AC6, the criterion that defines
// this stage: two independent project databases, each with its own
// SPEC-125 at a DISTINCT anchor; the same memory (same UUID, same text
// mentioning SPEC-125) travels from A to B through the REAL writer
// (materializeTeamMemory, via Save) and the REAL reader (ImportFromShared)
// — never a hand-built row. In A the reference resolves local, to A's own
// SPEC-125. In B it resolves foreign: no local_id, and NOT B's own
// SPEC-125 — which the test verifies exists and differs, so "foreign"
// cannot be an artifact of an empty database (R6).
func TestSDDAnchor_CrossDatabaseResolution(t *testing.T) {
	ctx := context.Background()

	fxA := newSDDTestFixture(t, true, "Machine A", "a@example.com")
	specA := &model.Spec{ID: "SPEC-125", Title: "A's spec", Status: model.SpecStatusDraft, Project: "test/project", Lane: model.LaneStandard}
	if err := fxA.sddStore.CreateSpec(ctx, specA); err != nil {
		t.Fatalf("CreateSpec (A): %v", err)
	}

	resp, err := fxA.svc.Save(ctx, model.SaveRequest{
		TopicKey: "sdd-refs/cross-database",
		Title:    "Cites SPEC-125",
		Content:  "See SPEC-125 for the real work.",
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save (A): %v", err)
	}

	gotA, err := fxA.svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get (A): %v", err)
	}
	if len(gotA.SDDRefs) != 1 {
		t.Fatalf("A: expected 1 resolved ref, got %d: %+v", len(gotA.SDDRefs), gotA.SDDRefs)
	}
	if gotA.SDDRefs[0].Status != model.SDDRefLocal || gotA.SDDRefs[0].LocalID != "SPEC-125" {
		t.Fatalf("A: expected local resolution to SPEC-125, got %+v", gotA.SDDRefs[0])
	}
	if gotA.SDDRefs[0].TargetUUID != specA.UUID {
		t.Fatalf("A: TargetUUID = %q, want %q", gotA.SDDRefs[0].TargetUUID, specA.UUID)
	}

	// The note the REAL writer produced on A's machine.
	aNotePath := filepath.Join(fxA.repoDir, ".mneme", "shared", "notes", resp.ID+".md")
	noteBytes, err := os.ReadFile(aNotePath)
	if err != nil {
		t.Fatalf("read A's materialized note: %v", err)
	}
	if !strings.Contains(string(noteBytes), "sdd_refs:") {
		t.Fatalf("A's materialized note has no sdd_refs: block:\n%s", noteBytes)
	}

	fxB := newSDDTestFixture(t, true, "Machine B", "b@example.com")
	specB := &model.Spec{ID: "SPEC-125", Title: "B's spec", Status: model.SpecStatusDraft, Project: "test/project", Lane: model.LaneStandard}
	if err := fxB.sddStore.CreateSpec(ctx, specB); err != nil {
		t.Fatalf("CreateSpec (B): %v", err)
	}
	if specB.UUID == specA.UUID {
		t.Fatal("test setup invariant broken: A and B minted the same anchor for SPEC-125")
	}

	// The note travels from A to B (substituting for "git push, git pull",
	// which this repo's own .gitignore prevents exercising for real — R6):
	// copy the REAL file the writer produced into B's own vault.
	bNotesDir := filepath.Join(fxB.repoDir, ".mneme", "shared", "notes")
	if err := os.MkdirAll(bNotesDir, 0o755); err != nil {
		t.Fatalf("mkdir B notes dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bNotesDir, resp.ID+".md"), noteBytes, 0o644); err != nil {
		t.Fatalf("copy note into B's vault: %v", err)
	}

	result, err := fxB.svc.ImportFromShared(ctx, fxB.repoDir)
	if err != nil {
		t.Fatalf("ImportFromShared (B): %v", err)
	}
	if result.Errors != 0 {
		t.Fatalf("ImportFromShared (B) reported %d errors", result.Errors)
	}

	gotB, err := fxB.svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get (B): %v", err)
	}
	if len(gotB.SDDRefs) != 1 {
		t.Fatalf("B: expected 1 resolved ref, got %d: %+v", len(gotB.SDDRefs), gotB.SDDRefs)
	}
	refB := gotB.SDDRefs[0]
	if refB.Status != model.SDDRefForeign {
		t.Errorf("B: expected foreign resolution, got %+v", refB)
	}
	if refB.LocalID != "" {
		t.Errorf("B: expected no local_id, got %q", refB.LocalID)
	}
	if refB.TargetUUID != specA.UUID {
		t.Errorf("B: TargetUUID = %q, want A's anchor %q (the note's own anchor must survive import unchanged)", refB.TargetUUID, specA.UUID)
	}
	if refB.TargetUUID == specB.UUID {
		t.Fatal("B: resolved to B's own SPEC-125 — the anchor must be A's, never re-derived locally")
	}
}
