package service_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/managedblock"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
	"github.com/wirvii/mneme/internal/service"
)

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
