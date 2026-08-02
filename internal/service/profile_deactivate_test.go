package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// treeHash walks root and returns a deterministic hash of every regular
// file's relative path and content — used to prove a dry-run mutated
// nothing (SPEC-105 AC8) without hand-enumerating every artifact.
func treeHash(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	var paths []string
	files := make(map[string][]byte)

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		paths = append(paths, rel)
		files[rel] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(paths)
	for _, p := range paths {
		h.Write([]byte(p))
		h.Write(files[p])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// deactivationTestEnv bundles everything the DeactivateProject tests need:
// a fully-wired ProfileService (mem/sub/skillsDir/configPath, so
// ResolveActive/NextSession and SetDefault both work), plus the raw global
// *store.MemoryStore so a test can seed an orphan row directly — something
// SaveProfileRule can no longer do since SPEC-105 DD8 layer 1.
type deactivationTestEnv struct {
	svc         *service.ProfileService
	mem         *service.MemoryService
	globalStore *store.MemoryStore
	repoRoot    string
	skillsDir   string
	configPath  string
}

// newDeactivationTestEnv builds the "acme" fixture profile (same shape as
// newActivationTestEnv) plus a config.toml so SetDefault/ResolveActive work,
// and exposes the raw global store for orphan-seeding tests.
func newDeactivationTestEnv(t *testing.T) deactivationTestEnv {
	t.Helper()

	profilesDir := t.TempDir()
	repoRoot := t.TempDir()
	skillsDir := t.TempDir()

	profileDir := filepath.Join(profilesDir, "acme")
	writeProfileFile(t, filepath.Join(profileDir, profile.ManifestFileName), "name=\"acme\"\nversion=\"1.0.0\"\n")
	writeProfileFile(t, filepath.Join(profileDir, "agents", "backend.md"), "---\nname: backend\n---\n\nCapa-1 backend content.\n")
	writeProfileFile(t, filepath.Join(profileDir, "blocks", "profile.md"), "## Metodología Acme\n\nUse conventional commits.")
	writeProfileFile(t, filepath.Join(profileDir, "rules.jsonl"),
		`{"title":"No CGO","content":"Pure Go, no CGO.","applies_to":["**"],"severity":"warn"}`+"\n")

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

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.toml")
	cfg := config.Default()

	mem := service.NewMemoryService(projectStore, globalStore, cfg, "test/project", embed.NopEmbedder{})
	sub := service.NewSubagentService(mem)
	svc := service.NewProfileService(profilesDir, false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
		service.WithProfileSkillsDir(skillsDir),
		service.WithProfileConfigPath(configPath),
	)

	return deactivationTestEnv{
		svc: svc, mem: mem, globalStore: globalStore,
		repoRoot: repoRoot, skillsDir: skillsDir, configPath: configPath,
	}
}

// seedOrphanProfileRule inserts a row directly into env's global store with
// the exact shape SPEC-105 DD8 layer 1 now prevents SaveProfileRule from
// ever creating again: scope=project, project=NULL, source=profile:<name>.
// Returns the seeded row's id.
func seedOrphanProfileRule(t *testing.T, env deactivationTestEnv, profileName string) string {
	t.Helper()
	ctx := context.Background()
	orphan := &model.Memory{
		Type:       model.TypeRule,
		Scope:      model.ScopeProject,
		Title:      "Orphan rule",
		Content:    "content",
		Project:    "",
		AppliesTo:  []string{"**"},
		Source:     "profile:" + profileName,
		Importance: 0.5,
		DecayRate:  0.01,
	}
	created, err := env.globalStore.Create(ctx, orphan)
	if err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	return created.ID
}

// TestDeactivateProject_DryRunMutatesNothing (SPEC-105 AC8) verifies that a
// dry-run (Apply: false) mutates absolutely nothing in the repo — proven by
// a whole-tree content hash before and after — and that the rule count is
// unaffected.
func TestDeactivateProject_DryRunMutatesNothing(t *testing.T) {
	env := newDeactivationTestEnv(t)
	ctx := context.Background()

	if _, err := env.svc.Activate(ctx, service.ActivationInput{RepoRoot: env.repoRoot, Name: "acme", Commit: "c1"}); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	before := treeHash(t, env.repoRoot)
	idsBefore, _, err := env.mem.ListProfileRuleIDs(ctx, "acme")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs (before): %v", err)
	}

	result, err := env.svc.DeactivateProject(ctx, service.DeactivateInput{RepoRoot: env.repoRoot, Apply: false})
	if err != nil {
		t.Fatalf("DeactivateProject (dry-run): %v", err)
	}
	if result.Applied {
		t.Error("expected Applied=false in dry-run mode")
	}
	if len(result.Artifacts) == 0 {
		t.Error("expected the plan to list artifacts")
	}

	after := treeHash(t, env.repoRoot)
	if before != after {
		t.Error("expected the repo tree to be byte-identical after a dry-run")
	}

	idsAfter, _, err := env.mem.ListProfileRuleIDs(ctx, "acme")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs (after): %v", err)
	}
	if len(idsBefore) != len(idsAfter) {
		t.Errorf("expected the rule count unaffected by a dry-run: before=%d after=%d", len(idsBefore), len(idsAfter))
	}
}

// TestDeactivateProject_ApplyRestoresPreActivationState (SPEC-105 AC7)
// verifies that `--apply` leaves CLAUDE.md, rules, and agents exactly as
// they were before the activation that DeactivateProject undoes.
func TestDeactivateProject_ApplyRestoresPreActivationState(t *testing.T) {
	env := newDeactivationTestEnv(t)
	ctx := context.Background()

	agentPath := filepath.Join(env.repoRoot, ".claude", "agents", "backend.md")
	preexistingAgent := "hand-written backend agent, predates any activation"
	writeProfileFile(t, agentPath, preexistingAgent)

	claudeMD := filepath.Join(env.repoRoot, "CLAUDE.md")
	preexistingProse := "# My project\n\nHand-authored prose that predates any profile.\n"
	writeProfileFile(t, claudeMD, preexistingProse)

	if _, err := env.svc.Activate(ctx, service.ActivationInput{RepoRoot: env.repoRoot, Name: "acme", Commit: "c1"}); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	result, err := env.svc.DeactivateProject(ctx, service.DeactivateInput{RepoRoot: env.repoRoot, Apply: true})
	if err != nil {
		t.Fatalf("DeactivateProject (apply): %v", err)
	}
	if !result.Applied {
		t.Fatal("expected Applied=true")
	}

	agentData, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("read agent after deactivate: %v", err)
	}
	if string(agentData) != preexistingAgent {
		t.Errorf("agent content: got %q, want %q", string(agentData), preexistingAgent)
	}

	claudeData, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("read CLAUDE.md after deactivate: %v", err)
	}
	if !strings.Contains(string(claudeData), "Hand-authored prose that predates any profile.") {
		t.Errorf("expected pre-existing prose intact, got %q", string(claudeData))
	}
	if strings.Contains(string(claudeData), "Metodología Acme") {
		t.Errorf("expected the profile's block content gone, got %q", string(claudeData))
	}

	ids, _, err := env.mem.ListProfileRuleIDs(ctx, "acme")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 rules after deactivate, got %d", len(ids))
	}
}

// TestDeactivateProject_ApplyRemovesLockKeepsPin (SPEC-105 AC9) verifies
// that --apply deletes .mneme/profile.lock but never touches
// .mneme-profile.
func TestDeactivateProject_ApplyRemovesLockKeepsPin(t *testing.T) {
	env := newDeactivationTestEnv(t)
	ctx := context.Background()

	if _, err := env.svc.Activate(ctx, service.ActivationInput{RepoRoot: env.repoRoot, Name: "acme", Commit: "c1"}); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	pinPath := filepath.Join(env.repoRoot, ".mneme-profile")
	pinContent := "name = \"acme\"\n"
	writeProfileFile(t, pinPath, pinContent)

	result, err := env.svc.DeactivateProject(ctx, service.DeactivateInput{RepoRoot: env.repoRoot, Apply: true})
	if err != nil {
		t.Fatalf("DeactivateProject: %v", err)
	}
	if !result.Applied {
		t.Fatal("expected Applied=true")
	}

	if _, err := os.Stat(result.LockPath); !os.IsNotExist(err) {
		t.Errorf("expected the lock file to be removed, stat err = %v", err)
	}

	pinData, err := os.ReadFile(pinPath)
	if err != nil {
		t.Fatalf("expected the pin to survive: %v", err)
	}
	if string(pinData) != pinContent {
		t.Errorf("expected the pin untouched, got %q", string(pinData))
	}
}

// TestDeactivateProject_NextSessionText_ThreeCases (SPEC-105 AC10) verifies
// the three NextSession messages: pin present, no pin but a global default
// set, and neither.
func TestDeactivateProject_NextSessionText_ThreeCases(t *testing.T) {
	t.Run("pin present", func(t *testing.T) {
		env := newDeactivationTestEnv(t)
		ctx := context.Background()
		if _, err := env.svc.Activate(ctx, service.ActivationInput{RepoRoot: env.repoRoot, Name: "acme", Commit: "c1"}); err != nil {
			t.Fatalf("Activate: %v", err)
		}
		writeProfileFile(t, filepath.Join(env.repoRoot, ".mneme-profile"), "name = \"acme\"\n")

		result, err := env.svc.DeactivateProject(ctx, service.DeactivateInput{RepoRoot: env.repoRoot, Apply: false})
		if err != nil {
			t.Fatalf("DeactivateProject: %v", err)
		}
		if !strings.Contains(result.NextSession, "pin .mneme-profile") {
			t.Errorf("expected the pin-present message, got %q", result.NextSession)
		}
	})

	t.Run("no pin, global default set", func(t *testing.T) {
		env := newDeactivationTestEnv(t)
		ctx := context.Background()
		if _, err := env.svc.Activate(ctx, service.ActivationInput{RepoRoot: env.repoRoot, Name: "acme", Commit: "c1"}); err != nil {
			t.Fatalf("Activate: %v", err)
		}
		if _, err := env.svc.SetDefault("acme"); err != nil {
			t.Fatalf("SetDefault: %v", err)
		}

		result, err := env.svc.DeactivateProject(ctx, service.DeactivateInput{RepoRoot: env.repoRoot, Apply: false})
		if err != nil {
			t.Fatalf("DeactivateProject: %v", err)
		}
		if !strings.Contains(result.NextSession, "default global") {
			t.Errorf("expected the global-default message, got %q", result.NextSession)
		}
	})

	t.Run("no pin, no default", func(t *testing.T) {
		env := newDeactivationTestEnv(t)
		ctx := context.Background()
		if _, err := env.svc.Activate(ctx, service.ActivationInput{RepoRoot: env.repoRoot, Name: "acme", Commit: "c1"}); err != nil {
			t.Fatalf("Activate: %v", err)
		}

		result, err := env.svc.DeactivateProject(ctx, service.DeactivateInput{RepoRoot: env.repoRoot, Apply: false})
		if err != nil {
			t.Fatalf("DeactivateProject: %v", err)
		}
		if !strings.Contains(result.NextSession, "vanilla") {
			t.Errorf("expected the vanilla message, got %q", result.NextSession)
		}
	})
}

// TestDeactivateProject_NoLockIsNotAnError verifies that calling
// DeactivateProject against a workspace with no activation lock at all
// returns a result with a Warnings entry, never an error.
func TestDeactivateProject_NoLockIsNotAnError(t *testing.T) {
	env := newDeactivationTestEnv(t)

	result, err := env.svc.DeactivateProject(context.Background(), service.DeactivateInput{RepoRoot: env.repoRoot, Apply: false})
	if err != nil {
		t.Fatalf("DeactivateProject: %v", err)
	}
	if result.Applied {
		t.Error("expected Applied=false")
	}
	if len(result.Warnings) == 0 {
		t.Error("expected a warning explaining there is nothing to deactivate")
	}
}

// TestDeactivateProject_ReportsOrphanRules (SPEC-105 DD18) verifies that
// the plan surfaces orphaned global-store rows with the departing
// profile's provenance, alongside the project-scoped ones.
func TestDeactivateProject_ReportsOrphanRules(t *testing.T) {
	env := newDeactivationTestEnv(t)
	ctx := context.Background()

	if _, err := env.svc.Activate(ctx, service.ActivationInput{RepoRoot: env.repoRoot, Name: "acme", Commit: "c1"}); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	orphanID := seedOrphanProfileRule(t, env, "acme")

	result, err := env.svc.DeactivateProject(ctx, service.DeactivateInput{RepoRoot: env.repoRoot, Apply: false})
	if err != nil {
		t.Fatalf("DeactivateProject: %v", err)
	}

	found := false
	for _, id := range result.OrphanRuleIDs {
		if id == orphanID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected orphan rule %s in OrphanRuleIDs, got %v", orphanID, result.OrphanRuleIDs)
	}
}

// TestDeactivateProject_ListsResidualBackups verifies that a backup run
// directory belonging to an EARLIER, unrelated displacement is reported in
// ResidualBackups and left untouched by this operation.
func TestDeactivateProject_ListsResidualBackups(t *testing.T) {
	env := newDeactivationTestEnv(t)
	ctx := context.Background()

	// First activation displaces a hand-written agent file, producing a
	// backup run directory that the SECOND activation (whose own lock
	// DeactivateProject will act on) never references.
	agentPath := filepath.Join(env.repoRoot, ".claude", "agents", "backend.md")
	writeProfileFile(t, agentPath, "hand-written, displaced by the first activation")
	if _, err := env.svc.Activate(ctx, service.ActivationInput{RepoRoot: env.repoRoot, Name: "acme", Commit: "c1"}); err != nil {
		t.Fatalf("Activate (first): %v", err)
	}

	lock1, _, err := env.svc.ActiveLock(env.repoRoot)
	if err != nil {
		t.Fatalf("ActiveLock (first): %v", err)
	}
	var firstBackup string
	for _, a := range lock1.Artifacts {
		if a.Backup != "" {
			firstBackup = a.Backup
		}
	}
	if firstBackup == "" {
		t.Fatal("expected the first activation to have produced a backup")
	}
	firstRunDir := filepath.Dir(filepath.Dir(filepath.Dir(firstBackup))) // .../<UTC>/.claude/agents -> .../<UTC>

	// Second activation of the SAME profile: the agent path is now owned
	// by the first activation's lock, so nothing new is displaced — the
	// first run's backup directory is untouched by this call and survives
	// as pure residue once THIS activation's lock takes over.
	if _, err := env.svc.Activate(ctx, service.ActivationInput{RepoRoot: env.repoRoot, Name: "acme", Commit: "c1"}); err != nil {
		t.Fatalf("Activate (second): %v", err)
	}

	result, err := env.svc.DeactivateProject(ctx, service.DeactivateInput{RepoRoot: env.repoRoot, Apply: false})
	if err != nil {
		t.Fatalf("DeactivateProject: %v", err)
	}

	found := false
	for _, dir := range result.ResidualBackups {
		if dir == firstRunDir {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %s among ResidualBackups, got %v", firstRunDir, result.ResidualBackups)
	}
}
