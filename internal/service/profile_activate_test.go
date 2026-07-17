package service_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/managedblock"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// newClosedDBTestService builds a MemoryService whose two underlying SQLite
// connections are already closed — every store operation it attempts
// (Save/Get/List/GetByTopicKey/HardDeleteBySource) fails predictably. Used to
// force the "downstream store call fails" branches of Activate/Deactivate
// (ReadProfile, SaveProfileRule, PurgeProfileRules) that a normal, healthy
// in-memory SQLite store never exercises.
func newClosedDBTestService(t *testing.T) *service.MemoryService {
	t.Helper()
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	mem := service.NewMemoryService(projectStore, globalStore, cfg, "test/project", embed.NopEmbedder{})
	if err := projectDB.Close(); err != nil {
		t.Fatalf("close project db: %v", err)
	}
	if err := globalDB.Close(); err != nil {
		t.Fatalf("close global db: %v", err)
	}
	return mem
}

// mustRunGitProfile runs git with args in dir, failing the test on error —
// mirrors internal/profile/store_test.go's mustRunGit. Only "init" is used
// here: git check-ignore works against the working tree + .gitignore rules
// alone, no commits or identity required.
func mustRunGitProfile(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitCheckIgnored reports whether relPath (relative to repoRoot) is
// gitignored. Exit 0 = ignored, exit 1 = not ignored — both are valid,
// non-fatal outcomes here; only a genuine git failure fails the test.
func gitCheckIgnored(t *testing.T, repoRoot, relPath string) bool {
	t.Helper()
	cmd := exec.Command("git", "check-ignore", "-q", relPath)
	cmd.Dir = repoRoot
	err := cmd.Run()
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false
	}
	t.Fatalf("git check-ignore %s: %v", relPath, err)
	return false
}

// writeProfileFile writes content to path, creating parent directories.
func writeProfileFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newActivationTestEnv builds a fixture profile store directory (containing
// one profile named "acme" with agents/skills/blocks/rules), a fresh
// MemoryService+SubagentService pair, and a ProfileService fully wired for
// activation. Every path is a t.TempDir() the test owns (SPEC-085/SPEC-092
// §5.5 isolation — nothing here ever resolves HOME).
func newActivationTestEnv(t *testing.T) (svc *service.ProfileService, mem *service.MemoryService, repoRoot, skillsDir string) {
	t.Helper()

	profilesDir := t.TempDir()
	repoRoot = t.TempDir()
	skillsDir = t.TempDir()

	profileDir := filepath.Join(profilesDir, "acme")
	writeProfileFile(t, filepath.Join(profileDir, profile.ManifestFileName), "name=\"acme\"\nversion=\"1.0.0\"\n")
	writeProfileFile(t, filepath.Join(profileDir, "agents", "backend.md"), "---\nname: backend\ntools: Read\n---\n\nCapa-1 backend content.\n")
	writeProfileFile(t, filepath.Join(profileDir, "blocks", "profile.md"), "## Metodología Acme\n\nUse conventional commits.")
	writeProfileFile(t, filepath.Join(profileDir, "skills", "acme-skill", "SKILL.md"), "---\nname: acme-skill\npinned: false\n---\n\n# Acme skill\n")
	writeProfileFile(t, filepath.Join(profileDir, "rules.jsonl"),
		`{"title":"No CGO","content":"Pure Go, no CGO.","applies_to":["**"],"severity":"warn"}`+"\n")

	mem = newTestService(t)
	sub := service.NewSubagentService(mem)

	svc = service.NewProfileService(profilesDir, false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
		service.WithProfileSkillsDir(skillsDir),
	)
	return svc, mem, repoRoot, skillsDir
}

// TestActivate_MaterializesEverythingAndWritesLock is the end-to-end
// happy-path AC9 test: agents/skills/blocks/rules all materialize, and the
// lock records every artifact and rule id.
func TestActivate_MaterializesEverythingAndWritesLock(t *testing.T) {
	svc, mem, repoRoot, skillsDir := newActivationTestEnv(t)
	ctx := context.Background()

	result, err := svc.Activate(ctx, service.ActivationInput{
		RepoRoot: repoRoot,
		Name:     "acme",
		Source:   "git@example.com:acme/profile.git",
		Ref:      "v1",
		Commit:   "abc123",
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}

	// Agent file written.
	agentPath := filepath.Join(repoRoot, ".claude", "agents", "backend.md")
	agentData, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("read agent file: %v", err)
	}
	if !strings.Contains(string(agentData), "Capa-1 backend content.") {
		t.Errorf("expected capa-1 content in agent file, got %q", string(agentData))
	}
	if len(result.Agents) != 1 || result.Agents[0] != agentPath {
		t.Errorf("ActivateResult.Agents: got %v, want [%s]", result.Agents, agentPath)
	}

	// Skill directory copied.
	skillFile := filepath.Join(skillsDir, "acme-skill", "SKILL.md")
	if _, err := os.Stat(skillFile); err != nil {
		t.Errorf("expected skill file at %s: %v", skillFile, err)
	}
	if len(result.Skills) != 1 || result.Skills[0] != "acme-skill" {
		t.Errorf("ActivateResult.Skills: got %v, want [acme-skill]", result.Skills)
	}

	// CLAUDE.md block upserted.
	claudeMD := filepath.Join(repoRoot, "CLAUDE.md")
	content, ver, present, err := managedblock.Read(claudeMD, "profile")
	if err != nil {
		t.Fatalf("managedblock.Read: %v", err)
	}
	if !present {
		t.Fatal("expected 'profile' managed block to be present in CLAUDE.md")
	}
	if ver != 1 {
		t.Errorf("expected block version 1, got %d", ver)
	}
	if !strings.Contains(content, "Metodología Acme") {
		t.Errorf("expected block content, got %q", content)
	}
	if len(result.Blocks) != 1 || result.Blocks[0] != claudeMD {
		t.Errorf("ActivateResult.Blocks: got %v, want [%s]", result.Blocks, claudeMD)
	}

	// Rule inserted and provenance-stamped.
	if len(result.RulesInserted) != 1 {
		t.Fatalf("expected 1 rule inserted, got %d", len(result.RulesInserted))
	}
	ruleMem, err := mem.Get(ctx, result.RulesInserted[0])
	if err != nil {
		t.Fatalf("Get inserted rule: %v", err)
	}
	if ruleMem.Source != "profile:acme" {
		t.Errorf("rule Source: got %q, want %q", ruleMem.Source, "profile:acme")
	}
	if ruleMem.Shared != 0 {
		t.Errorf("rule Shared: got %d, want 0", ruleMem.Shared)
	}

	// Lock written with everything above.
	lock, present, err := svc.ActiveLock(repoRoot)
	if err != nil {
		t.Fatalf("ActiveLock: %v", err)
	}
	if !present {
		t.Fatal("expected a lock to be present after Activate")
	}
	if lock.Profile != "acme" || lock.Commit != "abc123" || lock.Ref != "v1" {
		t.Errorf("unexpected lock identity: %+v", lock)
	}
	if len(lock.Artifacts) != 3 {
		t.Errorf("expected 3 artifacts (agent+skill+block), got %d: %+v", len(lock.Artifacts), lock.Artifacts)
	}
	if len(lock.Rules) != 1 || lock.Rules[0].ID != result.RulesInserted[0] || lock.Rules[0].Source != "profile:acme" {
		t.Errorf("unexpected lock rules: %+v", lock.Rules)
	}

	// The lock file itself lives under repoRoot/.mneme/, gitignored territory.
	lockPath := profile.LockPath(repoRoot)
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("expected lock file at %s: %v", lockPath, err)
	}
}

// TestActivate_SkillPinnedIsSkipped verifies that an already-installed,
// pinned skill is never overwritten by Activate.
func TestActivate_SkillPinnedIsSkipped(t *testing.T) {
	svc, _, repoRoot, skillsDir := newActivationTestEnv(t)
	ctx := context.Background()

	pinnedPath := filepath.Join(skillsDir, "acme-skill", "SKILL.md")
	writeProfileFile(t, pinnedPath, "---\nname: acme-skill\npinned: true\n---\n\n# Locally customised, do not overwrite\n")

	result, err := svc.Activate(ctx, service.ActivationInput{RepoRoot: repoRoot, Name: "acme"})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if len(result.Skills) != 0 {
		t.Errorf("expected pinned skill to be skipped, got Skills=%v", result.Skills)
	}

	data, err := os.ReadFile(pinnedPath)
	if err != nil {
		t.Fatalf("read pinned skill: %v", err)
	}
	if !strings.Contains(string(data), "Locally customised") {
		t.Errorf("expected pinned skill content untouched, got %q", string(data))
	}
}

// TestActivate_NotFound verifies that activating a profile absent from the
// store fails with profile.ErrProfileNotFound.
func TestActivate_NotFound(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)

	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: repoRoot, Name: "does-not-exist"})
	if err == nil {
		t.Fatal("expected an error for a profile absent from the store")
	}
}

// TestActivate_RequiresMemoryAndSubagentSeam verifies that Activate refuses
// to run when the service was constructed without the memory/subagent seam.
func TestActivate_RequiresMemoryAndSubagentSeam(t *testing.T) {
	profilesDir := t.TempDir()
	svc := service.NewProfileService(profilesDir, false)

	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: t.TempDir(), Name: "acme"})
	if err == nil {
		t.Fatal("expected an error when mem/sub are not wired")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected a 'not configured' error, got %v", err)
	}
}

// TestFuseAgent_DegradesCleanlyWithoutProjectProfile verifies that an agent
// is materialized with capa-1 alone when the repo has no subagent_profile
// yet (AC10, degradation branch).
func TestFuseAgent_DegradesCleanlyWithoutProjectProfile(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)

	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: repoRoot, Name: "acme"})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoRoot, ".claude", "agents", "backend.md"))
	if err != nil {
		t.Fatalf("read agent file: %v", err)
	}
	if strings.Contains(string(data), "Contexto del proyecto") {
		t.Errorf("expected no fused project-context section without a subagent_profile, got %q", string(data))
	}
}

// TestFuseAgent_FusesWhenProjectProfileExists verifies that an agent gets
// the repo's capa-2/3 project profile fused in, wrapped as untrusted content
// (AC10, fusion branch).
func TestFuseAgent_FusesWhenProjectProfileExists(t *testing.T) {
	svc, mem, repoRoot, _ := newActivationTestEnv(t)
	ctx := context.Background()

	sub := service.NewSubagentService(mem)
	if _, err := sub.SaveProfile(ctx, mem.ProjectSlug(), service.ProjectProfile{
		SchemaVersion: 1,
		Org:           "acme-corp",
		Repo:          service.ProjectProfileRepo{Commits: "Conventional Commits"},
		Mapping:       []service.ProjectProfileMapping{{App: "apps/core", Role: "backend"}},
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	if _, err := svc.Activate(ctx, service.ActivationInput{RepoRoot: repoRoot, Name: "acme"}); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoRoot, ".claude", "agents", "backend.md"))
	if err != nil {
		t.Fatalf("read agent file: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "Contexto del proyecto") {
		t.Errorf("expected fused project-context section, got %q", got)
	}
	if !strings.Contains(got, "acme-corp") || !strings.Contains(got, "apps/core") {
		t.Errorf("expected org and mapped area in fused section, got %q", got)
	}
	if !strings.Contains(got, "BEGIN GRILL-PROVIDED CONTENT") {
		t.Errorf("expected the fused section to be wrapped as untrusted content, got %q", got)
	}
}

// TestSwitch_RemovesOnlyDepartingProfileArtifacts is the AC11 test: switching
// from A to B removes only A's artifacts and hard-deletes only A's rules,
// while hand-authored files/rules and CLAUDE.md prose survive untouched.
func TestSwitch_RemovesOnlyDepartingProfileArtifacts(t *testing.T) {
	profilesDir := t.TempDir()
	repoRoot := t.TempDir()
	skillsDir := t.TempDir()

	profileADir := filepath.Join(profilesDir, "profile-a")
	writeProfileFile(t, filepath.Join(profileADir, profile.ManifestFileName), "name=\"profile-a\"\nversion=\"1.0.0\"\n")
	writeProfileFile(t, filepath.Join(profileADir, "agents", "backend.md"), "---\nname: backend\n---\n\nProfile A backend.\n")
	writeProfileFile(t, filepath.Join(profileADir, "blocks", "profile.md"), "## Profile A block")
	writeProfileFile(t, filepath.Join(profileADir, "rules.jsonl"),
		`{"title":"Rule A","content":"content A","applies_to":["**"]}`+"\n")

	profileBDir := filepath.Join(profilesDir, "profile-b")
	writeProfileFile(t, filepath.Join(profileBDir, profile.ManifestFileName), "name=\"profile-b\"\nversion=\"1.0.0\"\n")
	writeProfileFile(t, filepath.Join(profileBDir, "agents", "frontend.md"), "---\nname: frontend\n---\n\nProfile B frontend.\n")
	writeProfileFile(t, filepath.Join(profileBDir, "blocks", "profile.md"), "## Profile B block")
	writeProfileFile(t, filepath.Join(profileBDir, "rules.jsonl"),
		`{"title":"Rule B","content":"content B","applies_to":["**"]}`+"\n")

	mem := newTestService(t)
	sub := service.NewSubagentService(mem)
	svc := service.NewProfileService(profilesDir, false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
		service.WithProfileSkillsDir(skillsDir),
	)
	ctx := context.Background()

	// Hand-authored content the switch must never touch.
	handAgentPath := filepath.Join(repoRoot, ".claude", "agents", "hand-authored.md")
	writeProfileFile(t, handAgentPath, "hand-authored agent, never touched")

	claudeMD := filepath.Join(repoRoot, "CLAUDE.md")
	writeProfileFile(t, claudeMD, "# My project\n\nSome hand-authored prose.\n")

	handRuleResp, err := mem.Save(ctx, model.SaveRequest{
		Title:     "Hand-authored rule",
		Content:   "never touched",
		Type:      model.TypeRule,
		AppliesTo: []string{"**"},
	})
	if err != nil {
		t.Fatalf("Save hand-authored rule: %v", err)
	}

	// Activate A.
	resultA, err := svc.Activate(ctx, service.ActivationInput{RepoRoot: repoRoot, Name: "profile-a", Commit: "commitA"})
	if err != nil {
		t.Fatalf("Activate A: %v", err)
	}
	backendPath := filepath.Join(repoRoot, ".claude", "agents", "backend.md")
	if _, err := os.ReadFile(backendPath); err != nil {
		t.Fatalf("expected A's agent file, got err: %v", err)
	}

	// Re-add the CLAUDE.md prose check: activating A should have preserved
	// the hand-authored prose alongside its own upserted block.
	claudeAfterA, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("read CLAUDE.md after A: %v", err)
	}
	if !strings.Contains(string(claudeAfterA), "Some hand-authored prose.") {
		t.Fatalf("expected hand-authored prose preserved after Activate A, got %q", string(claudeAfterA))
	}

	// Switch to B.
	resultB, err := svc.Switch(ctx, repoRoot, service.ActivationInput{Name: "profile-b", Commit: "commitB"})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}

	// A's agent file is gone.
	if _, err := os.Stat(backendPath); !os.IsNotExist(err) {
		t.Errorf("expected A's agent file to be removed after switch, stat err = %v", err)
	}
	// B's agent file exists.
	frontendPath := filepath.Join(repoRoot, ".claude", "agents", "frontend.md")
	if _, err := os.ReadFile(frontendPath); err != nil {
		t.Errorf("expected B's agent file, got err: %v", err)
	}
	if len(resultB.Agents) != 1 || resultB.Agents[0] != frontendPath {
		t.Errorf("Switch result Agents: got %v", resultB.Agents)
	}

	// Hand-authored agent file untouched.
	if data, err := os.ReadFile(handAgentPath); err != nil || !strings.Contains(string(data), "hand-authored agent") {
		t.Errorf("expected hand-authored agent file untouched, data=%q err=%v", data, err)
	}

	// CLAUDE.md: hand-authored prose preserved, block now shows B's content, not A's.
	claudeAfterSwitch, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("read CLAUDE.md after switch: %v", err)
	}
	got := string(claudeAfterSwitch)
	if !strings.Contains(got, "Some hand-authored prose.") {
		t.Errorf("expected hand-authored prose preserved after switch, got %q", got)
	}
	if strings.Contains(got, "Profile A block") {
		t.Errorf("expected A's block content gone after switch, got %q", got)
	}
	if !strings.Contains(got, "Profile B block") {
		t.Errorf("expected B's block content present after switch, got %q", got)
	}

	// A's rule is hard-deleted (gone entirely).
	if len(resultA.RulesInserted) != 1 {
		t.Fatalf("expected 1 rule from A, got %d", len(resultA.RulesInserted))
	}
	_, err = mem.Get(ctx, resultA.RulesInserted[0])
	if err == nil {
		t.Error("expected A's rule to be gone after switch")
	}

	// B's rule exists.
	if len(resultB.RulesInserted) != 1 {
		t.Fatalf("expected 1 rule from B, got %d", len(resultB.RulesInserted))
	}
	bRule, err := mem.Get(ctx, resultB.RulesInserted[0])
	if err != nil {
		t.Fatalf("Get B's rule: %v", err)
	}
	if bRule.Source != "profile:profile-b" {
		t.Errorf("B's rule Source: got %q", bRule.Source)
	}

	// Hand-authored rule survives.
	handRuleAfter, err := mem.Get(ctx, handRuleResp.ID)
	if err != nil {
		t.Fatalf("Get hand-authored rule after switch: %v", err)
	}
	if handRuleAfter == nil {
		t.Error("expected hand-authored rule to survive the switch")
	}

	// The lock now reflects B.
	lock, present, err := svc.ActiveLock(repoRoot)
	if err != nil {
		t.Fatalf("ActiveLock: %v", err)
	}
	if !present || lock.Profile != "profile-b" {
		t.Errorf("expected lock to reflect profile-b, got present=%v lock=%+v", present, lock)
	}
}

// TestSwitch_NoExistingLockActivatesFresh verifies that Switch on a
// workspace with no prior activation behaves like a plain Activate.
func TestSwitch_NoExistingLockActivatesFresh(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)

	result, err := svc.Switch(context.Background(), repoRoot, service.ActivationInput{Name: "acme"})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if len(result.Agents) != 1 {
		t.Errorf("expected a fresh activation to materialize agents, got %+v", result)
	}
}

// TestDetectStaleness_NoLockIsNeverStale verifies that a workspace with no
// lock at all is reported as not stale.
func TestDetectStaleness_NoLockIsNeverStale(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)

	stale, msg, err := svc.DetectStaleness(repoRoot, profile.Snapshot{Profile: "acme"})
	if err != nil {
		t.Fatalf("DetectStaleness: %v", err)
	}
	if stale {
		t.Errorf("expected not stale with no lock present, got msg=%q", msg)
	}
}

// TestDetectStaleness_DetectsMutatedLock verifies AC12: activating (caching
// a snapshot), then mutating the on-disk lock (simulating another session's
// Switch), makes the next DetectStaleness call report stale with a message.
func TestDetectStaleness_DetectsMutatedLock(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	ctx := context.Background()

	result, err := svc.Activate(ctx, service.ActivationInput{RepoRoot: repoRoot, Name: "acme", Commit: "commit1"})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}

	cached := profile.Snapshot{Profile: result.Profile, Commit: result.Commit, ActivatedAt: mustLockActivatedAt(t, svc, repoRoot)}

	// Not stale immediately after activation.
	stale, _, err := svc.DetectStaleness(repoRoot, cached)
	if err != nil {
		t.Fatalf("DetectStaleness (fresh): %v", err)
	}
	if stale {
		t.Error("expected not stale right after activation")
	}

	// Simulate another session's Switch by mutating the lock on disk.
	lock, _, err := svc.ActiveLock(repoRoot)
	if err != nil {
		t.Fatalf("ActiveLock: %v", err)
	}
	lock.Profile = "other-profile"
	lock.Commit = "commit2"
	lock.ActivatedAt = time.Now().UTC().Add(time.Hour)
	data, err := profile.RenderLock(*lock)
	if err != nil {
		t.Fatalf("RenderLock: %v", err)
	}
	if err := os.WriteFile(profile.LockPath(repoRoot), data, 0o644); err != nil {
		t.Fatalf("write mutated lock: %v", err)
	}

	stale, msg, err := svc.DetectStaleness(repoRoot, cached)
	if err != nil {
		t.Fatalf("DetectStaleness (mutated): %v", err)
	}
	if !stale {
		t.Error("expected stale after another session mutated the lock")
	}
	if msg == "" {
		t.Error("expected a non-empty staleness message")
	}
}

// mustLockActivatedAt reads the just-written lock's ActivatedAt, used to
// build a cached Snapshot matching exactly what Activate wrote (avoiding a
// races-prone "time.Now() at the test level" comparison).
func mustLockActivatedAt(t *testing.T, svc *service.ProfileService, repoRoot string) time.Time {
	t.Helper()
	lock, present, err := svc.ActiveLock(repoRoot)
	if err != nil || !present {
		t.Fatalf("ActiveLock: present=%v err=%v", present, err)
	}
	return lock.ActivatedAt
}

// TestActivate_LockFileIsGitignored is the AC13 regression test (QA
// observation 1): Activate must leave .mneme/profile.lock gitignored in the
// destination project, WITHOUT sweeping up .mneme/shared/ (the team-memory
// vault, SPEC-053, which must stay trackable) or the root-level
// .mneme-profile pin (which lives outside .mneme/ entirely) — a blanket
// ".mneme/" ignore would silently break both.
func TestActivate_LockFileIsGitignored(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	mustRunGitProfile(t, repoRoot, "init", "-q")

	if _, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: repoRoot, Name: "acme"}); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if !gitCheckIgnored(t, repoRoot, filepath.Join(".mneme", "profile.lock")) {
		t.Error("expected .mneme/profile.lock to be gitignored after Activate (AC13)")
	}

	writeProfileFile(t, filepath.Join(repoRoot, ".mneme", "shared", "note.md"), "shared vault note")
	if gitCheckIgnored(t, repoRoot, filepath.Join(".mneme", "shared", "note.md")) {
		t.Error("expected .mneme/shared/ (team-memory vault) to remain trackable, not gitignored")
	}

	writeProfileFile(t, filepath.Join(repoRoot, ".mneme-profile"), "profile = \"acme\"\n")
	if gitCheckIgnored(t, repoRoot, ".mneme-profile") {
		t.Error("expected the .mneme-profile pin to remain trackable, not gitignored")
	}
}

// TestActivate_RepoRootRequired verifies Activate's input-validation guard
// for a missing RepoRoot.
func TestActivate_RepoRootRequired(t *testing.T) {
	svc, _, _, _ := newActivationTestEnv(t)
	_, err := svc.Activate(context.Background(), service.ActivationInput{Name: "acme"})
	if err == nil {
		t.Fatal("expected an error when RepoRoot is empty")
	}
}

// TestActivate_NameRequired verifies Activate's input-validation guard for a
// missing Name.
func TestActivate_NameRequired(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: repoRoot})
	if err == nil {
		t.Fatal("expected an error when Name is empty")
	}
}

// TestActivate_ProfilePathInvalidName verifies that a Name failing the
// safe-slug check surfaces as an Activate error instead of ever reaching the
// filesystem (defense-in-depth, R2).
func TestActivate_ProfilePathInvalidName(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: repoRoot, Name: "../evil"})
	if err == nil {
		t.Fatal("expected an error for a profile name that fails the safe-slug check")
	}
}

// TestActivate_StatOtherError verifies that a os.Stat failure other than
// "not exist" (here: profilesDir itself is not a directory, so
// profilesDir/<name> can never resolve) surfaces as a distinct Activate
// error path from ErrProfileNotFound.
func TestActivate_StatOtherError(t *testing.T) {
	tmp := t.TempDir()
	profilesFile := filepath.Join(tmp, "profiles-as-file")
	if err := os.WriteFile(profilesFile, []byte("blocker"), 0o644); err != nil {
		t.Fatalf("write profiles-as-file blocker: %v", err)
	}

	mem := newTestService(t)
	sub := service.NewSubagentService(mem)
	svc := service.NewProfileService(profilesFile, false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
	)

	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: t.TempDir(), Name: "acme"})
	if err == nil {
		t.Fatal("expected a stat error when profilesDir is not actually a directory")
	}
	if errors.Is(err, profile.ErrProfileNotFound) {
		t.Errorf("expected an error distinct from ErrProfileNotFound, got %v", err)
	}
}

// TestActivate_LoadContentsError verifies that a malformed rules.jsonl fails
// Activate at the LoadContents step.
func TestActivate_LoadContentsError(t *testing.T) {
	profilesDir := t.TempDir()
	repoRoot := t.TempDir()
	profileDir := filepath.Join(profilesDir, "broken")
	writeProfileFile(t, filepath.Join(profileDir, profile.ManifestFileName), "name=\"broken\"\nversion=\"1.0.0\"\n")
	writeProfileFile(t, filepath.Join(profileDir, "rules.jsonl"), "not-json\n")

	mem := newTestService(t)
	sub := service.NewSubagentService(mem)
	svc := service.NewProfileService(profilesDir, false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
	)

	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: repoRoot, Name: "broken"})
	if err == nil {
		t.Fatal("expected an error loading a profile with a malformed rules.jsonl")
	}
}

// TestActivate_ReadProfileError verifies that a failure reading the repo's
// capa-2/3 project profile (SubagentService.ReadProfile) surfaces as an
// Activate error, using a MemoryService whose store is already closed to
// force the failure deterministically.
func TestActivate_ReadProfileError(t *testing.T) {
	profilesDir := t.TempDir()
	profileDir := filepath.Join(profilesDir, "acme")
	writeProfileFile(t, filepath.Join(profileDir, profile.ManifestFileName), "name=\"acme\"\nversion=\"1.0.0\"\n")

	mem := newClosedDBTestService(t)
	sub := service.NewSubagentService(mem)
	svc := service.NewProfileService(profilesDir, false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
	)

	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: t.TempDir(), Name: "acme"})
	if err == nil {
		t.Fatal("expected an error when reading the project profile fails")
	}
}

// TestActivate_MaterializeAgentsError verifies that a failure writing an
// agent file (here: .claude already exists as a regular file, blocking the
// agents/ mkdir) surfaces as an Activate error.
func TestActivate_MaterializeAgentsError(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	if err := os.WriteFile(filepath.Join(repoRoot, ".claude"), []byte("blocker"), 0o644); err != nil {
		t.Fatalf("write .claude blocker file: %v", err)
	}

	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: repoRoot, Name: "acme"})
	if err == nil {
		t.Fatal("expected an error when .claude exists as a regular file blocking agents/ mkdir")
	}
}

// TestActivate_MaterializeSkillsError verifies that a profile declaring
// skills fails Activate when no skills directory was configured
// (WithProfileSkillsDir omitted).
func TestActivate_MaterializeSkillsError(t *testing.T) {
	profilesDir := t.TempDir()
	repoRoot := t.TempDir()
	profileDir := filepath.Join(profilesDir, "acme")
	writeProfileFile(t, filepath.Join(profileDir, profile.ManifestFileName), "name=\"acme\"\nversion=\"1.0.0\"\n")
	writeProfileFile(t, filepath.Join(profileDir, "skills", "acme-skill", "SKILL.md"),
		"---\nname: acme-skill\npinned: false\n---\n\n# Acme skill\n")

	mem := newTestService(t)
	sub := service.NewSubagentService(mem)
	svc := service.NewProfileService(profilesDir, false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
		// deliberately no WithProfileSkillsDir.
	)

	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: repoRoot, Name: "acme"})
	if err == nil {
		t.Fatal("expected an error when a profile declares skills but no skills directory is configured")
	}
	if !strings.Contains(err.Error(), "skills directory") {
		t.Errorf("expected a skills-directory-related error, got %v", err)
	}
}

// TestActivate_MaterializeBlocksError verifies that a failure upserting the
// "profile" managed block (here: CLAUDE.md already exists as a directory)
// surfaces as an Activate error.
func TestActivate_MaterializeBlocksError(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	if err := os.MkdirAll(filepath.Join(repoRoot, "CLAUDE.md"), 0o755); err != nil {
		t.Fatalf("mkdir CLAUDE.md as directory: %v", err)
	}

	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: repoRoot, Name: "acme"})
	if err == nil {
		t.Fatal("expected an error when CLAUDE.md exists as a directory")
	}
}

// TestActivate_MaterializeRulesError verifies that a rule whose applies_to
// contains an empty pattern (valid per RuleSpec.Validate's len-only check,
// rejected by model.SaveRequest's per-pattern check) fails Activate at the
// materializeRules/SaveProfileRule step.
func TestActivate_MaterializeRulesError(t *testing.T) {
	profilesDir := t.TempDir()
	repoRoot := t.TempDir()
	profileDir := filepath.Join(profilesDir, "bad-rules")
	writeProfileFile(t, filepath.Join(profileDir, profile.ManifestFileName), "name=\"bad-rules\"\nversion=\"1.0.0\"\n")
	writeProfileFile(t, filepath.Join(profileDir, "rules.jsonl"),
		`{"title":"Bad rule","content":"content","applies_to":[""]}`+"\n")

	mem := newTestService(t)
	sub := service.NewSubagentService(mem)
	svc := service.NewProfileService(profilesDir, false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
	)

	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: repoRoot, Name: "bad-rules"})
	if err == nil {
		t.Fatal("expected an error when a rule's applies_to contains an empty pattern")
	}
}

// TestActivate_WriteLockError verifies that a failure writing profile.lock
// (here: .mneme already exists as a regular file, blocking its own mkdir)
// surfaces as an Activate error.
func TestActivate_WriteLockError(t *testing.T) {
	profilesDir := t.TempDir()
	repoRoot := t.TempDir()
	profileDir := filepath.Join(profilesDir, "acme")
	writeProfileFile(t, filepath.Join(profileDir, profile.ManifestFileName), "name=\"acme\"\nversion=\"1.0.0\"\n")

	mem := newTestService(t)
	sub := service.NewSubagentService(mem)
	svc := service.NewProfileService(profilesDir, false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
	)

	if err := os.WriteFile(filepath.Join(repoRoot, ".mneme"), []byte("blocker"), 0o644); err != nil {
		t.Fatalf("write .mneme blocker file: %v", err)
	}

	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: repoRoot, Name: "acme"})
	if err == nil {
		t.Fatal("expected an error writing the lock when .mneme exists as a regular file")
	}
}

// TestActiveLock_ReadErrorOtherThanNotExist verifies that ActiveLock
// surfaces a read failure distinct from "no lock yet" (here: the lock path
// itself is a directory).
func TestActiveLock_ReadErrorOtherThanNotExist(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	lockPath := profile.LockPath(repoRoot)
	if err := os.MkdirAll(lockPath, 0o755); err != nil {
		t.Fatalf("mkdir lock path as directory: %v", err)
	}

	_, _, err := svc.ActiveLock(repoRoot)
	if err == nil {
		t.Fatal("expected an error when the lock path is a directory")
	}
}

// TestActiveLock_ParseLockError verifies that ActiveLock surfaces a parse
// failure for a corrupted lock file.
func TestActiveLock_ParseLockError(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	writeProfileFile(t, profile.LockPath(repoRoot), "not valid toml{{{")

	_, _, err := svc.ActiveLock(repoRoot)
	if err == nil {
		t.Fatal("expected an error parsing an invalid lock file")
	}
}

// TestDeactivate_NilLockIsNoop verifies that Deactivate on a nil lock (a
// workspace that never activated anything) is a true no-op.
func TestDeactivate_NilLockIsNoop(t *testing.T) {
	svc, _, _, _ := newActivationTestEnv(t)
	if err := svc.Deactivate(context.Background(), nil); err != nil {
		t.Fatalf("expected Deactivate(nil) to be a no-op, got %v", err)
	}
}

// TestDeactivate_RequiresMemorySeam verifies that Deactivate refuses to run
// without the memory-service seam wired.
func TestDeactivate_RequiresMemorySeam(t *testing.T) {
	svc := service.NewProfileService(t.TempDir(), false)
	err := svc.Deactivate(context.Background(), &profile.Lock{Profile: "acme"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected a 'not configured' error, got %v", err)
	}
}

// TestDeactivate_RemoveArtifactErrorPropagates verifies that a failure
// removing one artifact (here: an unrecognised Kind) aborts Deactivate with
// a wrapped error.
func TestDeactivate_RemoveArtifactErrorPropagates(t *testing.T) {
	svc, _, _, _ := newActivationTestEnv(t)
	lock := &profile.Lock{
		Profile:   "acme",
		Artifacts: []profile.LockArtifact{{Kind: "bogus", Path: "/nonexistent"}},
	}
	if err := svc.Deactivate(context.Background(), lock); err == nil {
		t.Fatal("expected an error deactivating a lock with an unknown artifact kind")
	}
}

// TestDeactivate_PurgeProfileRulesErrorPropagates verifies that a failure
// hard-deleting a profile's rules (here: a closed underlying store) aborts
// Deactivate with a wrapped error.
func TestDeactivate_PurgeProfileRulesErrorPropagates(t *testing.T) {
	mem := newClosedDBTestService(t)
	sub := service.NewSubagentService(mem)
	svc := service.NewProfileService(t.TempDir(), false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
	)

	if err := svc.Deactivate(context.Background(), &profile.Lock{Profile: "acme"}); err == nil {
		t.Fatal("expected an error purging rules against a closed memory store")
	}
}

// TestSwitch_ActiveLockErrorPropagates verifies that Switch surfaces a
// failure reading the current lock instead of silently treating it as
// absent.
func TestSwitch_ActiveLockErrorPropagates(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	lockPath := profile.LockPath(repoRoot)
	if err := os.MkdirAll(lockPath, 0o755); err != nil {
		t.Fatalf("mkdir lock path as directory: %v", err)
	}

	_, err := svc.Switch(context.Background(), repoRoot, service.ActivationInput{Name: "acme"})
	if err == nil {
		t.Fatal("expected Switch to fail when ActiveLock cannot read the lock")
	}
}

// TestSwitch_DeactivateErrorPropagates verifies that Switch aborts (without
// activating the destination profile) when Deactivate fails on a corrupted
// departing lock.
func TestSwitch_DeactivateErrorPropagates(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	ctx := context.Background()

	if _, err := svc.Activate(ctx, service.ActivationInput{RepoRoot: repoRoot, Name: "acme"}); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	lock, present, err := svc.ActiveLock(repoRoot)
	if err != nil || !present {
		t.Fatalf("ActiveLock: present=%v err=%v", present, err)
	}
	lock.Artifacts = append(lock.Artifacts, profile.LockArtifact{Kind: "bogus", Path: "/nonexistent"})
	data, err := profile.RenderLock(*lock)
	if err != nil {
		t.Fatalf("RenderLock: %v", err)
	}
	if err := os.WriteFile(profile.LockPath(repoRoot), data, 0o644); err != nil {
		t.Fatalf("write corrupted lock: %v", err)
	}

	if _, err := svc.Switch(ctx, repoRoot, service.ActivationInput{Name: "acme"}); err == nil {
		t.Fatal("expected Switch to fail when Deactivate cannot remove a corrupted artifact")
	}
}

// TestSwitch_ActivateToErrorPropagates verifies that Switch surfaces the
// destination Activate's own error (here: an unknown profile) after already
// deactivating the departing one.
func TestSwitch_ActivateToErrorPropagates(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	ctx := context.Background()

	if _, err := svc.Activate(ctx, service.ActivationInput{RepoRoot: repoRoot, Name: "acme"}); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if _, err := svc.Switch(ctx, repoRoot, service.ActivationInput{Name: "does-not-exist"}); err == nil {
		t.Fatal("expected Switch to fail when the destination profile does not exist")
	}
}

// TestDetectStaleness_ActiveLockErrorPropagates verifies that DetectStaleness
// surfaces a failure reading the lock instead of silently reporting "not
// stale".
func TestDetectStaleness_ActiveLockErrorPropagates(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	lockPath := profile.LockPath(repoRoot)
	if err := os.MkdirAll(lockPath, 0o755); err != nil {
		t.Fatalf("mkdir lock path as directory: %v", err)
	}

	_, _, err := svc.DetectStaleness(repoRoot, profile.Snapshot{})
	if err == nil {
		t.Fatal("expected DetectStaleness to fail when ActiveLock cannot read the lock")
	}
}
