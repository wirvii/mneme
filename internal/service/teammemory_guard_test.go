package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// TestNewMemoryService_NeverAutoDetectsTeamMemory is the primary regression
// guard for SPEC-085 D1/D2/G1: even with the process cwd chdir'd into a git
// repository that has an ACTIVE shared vault marker, a MemoryService built
// with NO options must remain fully inert — Shared stays 0, Author stays
// empty, and nothing is ever written under .mneme/shared/notes/.
//
// This deliberately does NOT use newRepoTestService (which opts in via
// WithTeamMemory(DetectTeamMemory()) since SPEC-085 D3) — the whole point
// here is to prove the constructor itself never resolves team-memory state
// on its own. If a future change reintroduces DetectTeamMemory() into
// NewMemoryService's body (or into an Option applied unconditionally), this
// test fails loudly.
func TestNewMemoryService_NeverAutoDetectsTeamMemory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "nonexistent-gitconfig"))

	repoDir := t.TempDir()
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.name", "Team Member")
	runGit(t, repoDir, "config", "user.email", "team@example.com")

	sharedRoot := filepath.Join(repoDir, ".mneme", "shared")
	writeMarker(t, sharedRoot, "test/project", "shared")

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

	// No options — the exact call shape every one of the ~20 pre-existing
	// test call sites uses, and the shape a future non-CLI caller might use
	// if it forgets to opt in.
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test/project", embed.NopEmbedder{})

	ctx := context.Background()
	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Must stay inert despite an active marker in cwd",
		Content: "NewMemoryService must never auto-detect team-memory.",
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
		t.Errorf("expected Shared=0 (no auto-detection without an explicit Option), got %d", mem.Shared)
	}
	if mem.Author != "" {
		t.Errorf("expected empty Author (no auto-detection without an explicit Option), got %q", mem.Author)
	}

	notesDir := filepath.Join(repoDir, ".mneme", "shared", "notes")
	if _, statErr := os.Stat(notesDir); !os.IsNotExist(statErr) {
		t.Errorf("expected %s to not exist — a default-constructed MemoryService must never materialize to the vault, stat err: %v", notesDir, statErr)
	}
}
