package service_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

// newRepoTestService constructs a MemoryService whose process working
// directory is chdir'd into a fresh temporary git repository for the
// duration of the test (restored via t.Cleanup). This exercises
// MemoryService's real repo-root/team-memory detection (SPEC-053 D3), which
// resolves relative to os.Getwd() at construction time — unlike
// newTestService, which runs from wherever `go test` happens to invoke from
// (inside the mneme repo itself, where no shared vault marker exists, so
// team-memory always resolves inactive there — see
// TestSave_SharedAuthorDefaults_Inert in memory_test.go).
//
// When withMarker is true, <repoDir>/.mneme/shared/.mneme-vault is created
// before the service is constructed so team-memory resolves as active.
// Returns the service and the repo directory (equal to the new cwd).
//
// This helper never touches the real mneme repository or ~/.mneme — it
// creates and chdirs into an isolated t.TempDir() git repo, per
// constraint-no-local-install. It also isolates the process from the
// developer's real global git config so gitident.Author() is deterministic.
func newRepoTestService(t *testing.T, withMarker bool) (*service.MemoryService, string) {
	t.Helper()

	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "nonexistent-gitconfig"))

	repoDir := t.TempDir()
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.name", "Team Member")
	runGit(t, repoDir, "config", "user.email", "team@example.com")

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
	cfg := config.Default()

	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test/project", embed.NopEmbedder{})
	return svc, repoDir
}

// runGit runs git with args in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// sharedVaultFile returns the expected path of the materialized note for a
// memory with the given ID under the shared vault at repoDir.
func sharedVaultFile(repoDir, id string) string {
	return filepath.Join(repoDir, ".mneme", "shared", "notes", id+".md")
}

func TestSave_TeamMemoryActive_MaterializesDurableType(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Use write-through materialization",
		Content: "Decisions materialize synchronously to the shared vault.",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if mem.Shared != 1 {
		t.Errorf("expected Shared=1 for a decision with team-memory active, got %d", mem.Shared)
	}
	if mem.Author != "Team Member <team@example.com>" {
		t.Errorf("expected Author to be baked from git identity, got %q", mem.Author)
	}

	path := sharedVaultFile(repoDir, resp.ID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected materialized vault file at %s: %v", path, err)
	}
	out := string(data)
	if !strings.Contains(out, "shared: 1") {
		t.Errorf("materialized file should contain shared: 1, got:\n%s", out)
	}
	if !strings.Contains(out, "author: Team Member <team@example.com>") {
		t.Errorf("materialized file should contain the author line, got:\n%s", out)
	}
}

func TestSave_TeamMemoryInactive_NoMarker_NeverMaterializes(t *testing.T) {
	svc, repoDir := newRepoTestService(t, false)
	ctx := context.Background()

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "A decision without an active shared vault",
		Content: "Content",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if mem.Shared != 0 {
		t.Errorf("expected Shared=0 without an active team-memory marker, got %d", mem.Shared)
	}
	if mem.Author != "" {
		t.Errorf("expected empty Author without an active team-memory marker, got %q", mem.Author)
	}

	if _, err := os.Stat(filepath.Join(repoDir, ".mneme", "shared")); !os.IsNotExist(err) {
		t.Errorf("expected .mneme/shared to not exist when team-memory is inactive, stat err: %v", err)
	}
}

func TestSave_TeamMemoryActive_SessionSummaryNeverMaterializes(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Session wrap-up",
		Content: "What happened this session.",
		Type:    model.TypeSessionSummary,
	})
	if err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if mem.Shared != 0 {
		t.Errorf("expected Shared=0 for session_summary even with team-memory active, got %d", mem.Shared)
	}

	path := sharedVaultFile(repoDir, resp.ID)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("session_summary must never be materialized, but found %s", path)
	}
}

func TestSave_TeamMemoryActive_GlobalScopeNeverMaterializes(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "A personal preference",
		Content: "I prefer tabs.",
		Type:    model.TypeDecision, // durable type, but scope=global must still win
		Scope:   model.ScopeGlobal,
	})
	if err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if mem.Shared != 0 {
		t.Errorf("expected Shared=0 for scope=global even with a durable type, got %d", mem.Shared)
	}

	path := sharedVaultFile(repoDir, resp.ID)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("global-scoped memories must never be materialized, but found %s", path)
	}
}

func TestSave_TeamMemoryActive_ExplicitSharedOverride(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	zero := 0
	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Opt out of auto-share for this one decision",
		Content: "Content",
		Type:    model.TypeDecision,
		Shared:  &zero,
	})
	if err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if mem.Shared != 0 {
		t.Errorf("explicit Shared=0 override should be honoured, got %d", mem.Shared)
	}

	path := sharedVaultFile(repoDir, resp.ID)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("an explicit opt-out must never be materialized, but found %s", path)
	}
}

func TestSave_TeamMemoryActive_ResaveRewritesSameFile(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Original title",
		Content:  "Original content",
		Type:     model.TypeDecision,
		TopicKey: "team/shared-decision",
	})
	if err != nil {
		t.Fatalf("first Save: unexpected error: %v", err)
	}

	resp2, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Updated title",
		Content:  "Updated content",
		Type:     model.TypeDecision,
		TopicKey: "team/shared-decision",
	})
	if err != nil {
		t.Fatalf("second Save: unexpected error: %v", err)
	}
	if resp2.ID != resp.ID {
		t.Fatalf("re-saving the same topic_key should update the same memory: first=%s second=%s", resp.ID, resp2.ID)
	}
	if resp2.Action != "updated" {
		t.Errorf("expected action=updated on re-save, got %q", resp2.Action)
	}

	path := sharedVaultFile(repoDir, resp.ID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected the same materialized file to still exist at %s: %v", path, err)
	}
	if !strings.Contains(string(data), "Updated content") {
		t.Errorf("re-materialized file should reflect the updated content, got:\n%s", data)
	}
}

func TestSave_TeamMemoryActive_TwoMemoriesTwoFiles(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	resp1, err := svc.Save(ctx, model.SaveRequest{
		Title:   "First decision",
		Content: "Content one",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("first Save: unexpected error: %v", err)
	}

	resp2, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Second decision",
		Content: "Content two",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("second Save: unexpected error: %v", err)
	}

	if resp1.ID == resp2.ID {
		t.Fatal("two distinct Save calls must produce two distinct memory IDs")
	}

	for _, id := range []string{resp1.ID, resp2.ID} {
		path := sharedVaultFile(repoDir, id)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected materialized file for %s at %s: %v", id, path, err)
		}
	}
}

// TestSave_TeamMemoryActive_BestEffort_UnwritableSharedDir verifies that Save
// still succeeds when the shared vault cannot be written to, per the
// best-effort contract (SPEC-053 "log, never fail the save"). We force a
// write failure by making the "notes" path component a regular file instead
// of a directory, so os.MkdirAll fails deterministically regardless of the
// user running the test (unlike a permission-bit test, which root bypasses).
func TestSave_TeamMemoryActive_BestEffort_UnwritableSharedDir(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	sharedRoot := filepath.Join(repoDir, ".mneme", "shared")
	notesPath := filepath.Join(sharedRoot, "notes")
	if err := os.WriteFile(notesPath, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("setup: write blocking file at %s: %v", notesPath, err)
	}

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "This save must still succeed",
		Content: "Even though materialization cannot write to disk",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save should succeed even when the shared vault is unwritable: %v", err)
	}

	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if mem.Shared != 1 {
		t.Errorf("Shared should still be baked to 1 even though materialization failed, got %d", mem.Shared)
	}
}

func TestUpdate_TeamMemoryActive_RematerializesSharedMemory(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := context.Background()

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Original title",
		Content: "Original content",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	newContent := "Content changed via Update"
	if _, err := svc.Update(ctx, resp.ID, model.UpdateRequest{Content: &newContent}); err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}

	path := sharedVaultFile(repoDir, resp.ID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected materialized file to still exist at %s: %v", path, err)
	}
	if !strings.Contains(string(data), newContent) {
		t.Errorf("re-materialized file should reflect the Update, got:\n%s", data)
	}
}

// TestSave_SuppressedContext_NeverMaterializes is the regression guard for the
// SPEC-053 D5 anti-loop guard: WithSuppressMaterialize must still allow
// Shared to be baked (a suppressed Save is still a normal Save from the
// caller's point of view) but must prevent the write-through disk write.
// This is the exact mechanism the future vault-import path (SS-D) depends on
// to replay shared memories through Save/Update without re-triggering
// materialization back into the vault it just read from.
func TestSave_SuppressedContext_NeverMaterializes(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	ctx := service.WithSuppressMaterialize(context.Background())

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Imported from a peer's shared vault",
		Content: "Content",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	// Shared must still be baked — suppression only affects the disk write,
	// not the in-DB resolution of the sharing level.
	mem, err := svc.Get(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if mem.Shared != 1 {
		t.Errorf("expected Shared=1 to still be baked under a suppressed context, got %d", mem.Shared)
	}

	path := sharedVaultFile(repoDir, resp.ID)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("WithSuppressMaterialize must prevent the write-through disk write, but found %s", path)
	}
}

// TestUpdate_SuppressedContext_NeverMaterializes mirrors the Save guard test
// for Update: an already-shared memory re-materializes normally, but not when
// the call is made under a suppressed context.
func TestUpdate_SuppressedContext_NeverMaterializes(t *testing.T) {
	svc, repoDir := newRepoTestService(t, true)
	bg := context.Background()

	resp, err := svc.Save(bg, model.SaveRequest{
		Title:   "Original title",
		Content: "Original content",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	// Sanity check: materialization did happen for the initial (non-suppressed) Save.
	path := sharedVaultFile(repoDir, resp.ID)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected the initial Save to materialize %s: %v", path, err)
	}
	initialData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read initial materialized file: %v", err)
	}

	suppressedCtx := service.WithSuppressMaterialize(bg)
	newContent := "Content changed under a suppressed context"
	if _, err := svc.Update(suppressedCtx, resp.ID, model.UpdateRequest{Content: &newContent}); err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}

	afterData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file after suppressed Update: %v", err)
	}
	if string(afterData) != string(initialData) {
		t.Errorf("suppressed Update must not rewrite the materialized file, but content changed:\nbefore:\n%s\nafter:\n%s", initialData, afterData)
	}
	if strings.Contains(string(afterData), newContent) {
		t.Error("suppressed Update must not propagate its content change into the vault file")
	}
}
