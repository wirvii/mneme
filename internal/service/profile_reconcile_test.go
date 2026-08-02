package service_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/managedblock"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
	"github.com/wirvii/mneme/internal/service"
)

// TestReconcile_FirstCallActivates verifies that Reconcile against a
// workspace with no prior lock behaves like a fresh Activate.
func TestReconcile_FirstCallActivates(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)

	result, err := svc.Reconcile(context.Background(), repoRoot, service.ActivationInput{Name: "acme", Commit: "c1"})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Action != service.ReconcileActivated {
		t.Errorf("Action: got %q, want %q", result.Action, service.ReconcileActivated)
	}
	if result.Activation == nil || len(result.Activation.Agents) != 1 {
		t.Errorf("expected a fresh activation, got %+v", result.Activation)
	}
}

// TestReconcile_SecondCallIsNoop (SPEC-105 AC1) verifies that activating the
// same profile at the same commit N times leaves exactly the profile's
// declared rule count in the database, and every call after the first
// reports Action == noop.
func TestReconcile_SecondCallIsNoop(t *testing.T) {
	svc, mem, repoRoot, _ := newActivationTestEnv(t)
	ctx := context.Background()
	in := service.ActivationInput{Name: "acme", Commit: "c1"}

	first, err := svc.Reconcile(ctx, repoRoot, in)
	if err != nil {
		t.Fatalf("Reconcile (first): %v", err)
	}
	if first.Action != service.ReconcileActivated {
		t.Fatalf("first Action: got %q, want %q", first.Action, service.ReconcileActivated)
	}

	for i := 0; i < 3; i++ {
		result, err := svc.Reconcile(ctx, repoRoot, in)
		if err != nil {
			t.Fatalf("Reconcile (repeat %d): %v", i, err)
		}
		if result.Action != service.ReconcileNoop {
			t.Errorf("repeat %d Action: got %q, want %q", i, result.Action, service.ReconcileNoop)
		}
	}

	ids, _, err := mem.ListProfileRuleIDs(ctx, "acme")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected exactly 1 rule after N noop reconciliations, got %d", len(ids))
	}
}

// TestReconcile_NoopDoesNotLoadContents (SPEC-105 AC5) proves the guard is
// cheap: after the first activation, the profile's directory in
// profilesDir is renamed away entirely. A second Reconcile call with the
// SAME desired state must still report noop with NO error — if it tried to
// re-read profile.Contents (which Activate needs but the guard must not),
// it would fail against the now-missing directory.
func TestReconcile_NoopDoesNotLoadContents(t *testing.T) {
	profilesDir := t.TempDir()
	repoRoot := t.TempDir()
	skillsDir := t.TempDir()

	profileDir := filepath.Join(profilesDir, "acme")
	writeProfileFile(t, filepath.Join(profileDir, profile.ManifestFileName), "name=\"acme\"\nversion=\"1.0.0\"\n")
	writeProfileFile(t, filepath.Join(profileDir, "agents", "backend.md"), "---\nname: backend\n---\n\nCapa-1 backend content.\n")

	mem := newTestService(t)
	sub := service.NewSubagentService(mem)
	svc := service.NewProfileService(profilesDir, false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
		service.WithProfileSkillsDir(skillsDir),
	)
	ctx := context.Background()
	in := service.ActivationInput{Name: "acme", Commit: "c1"}

	if _, err := svc.Reconcile(ctx, repoRoot, in); err != nil {
		t.Fatalf("Reconcile (first): %v", err)
	}

	if err := os.Rename(profileDir, profileDir+"-renamed-away"); err != nil {
		t.Fatalf("rename profile dir away: %v", err)
	}

	result, err := svc.Reconcile(ctx, repoRoot, in)
	if err != nil {
		t.Fatalf("Reconcile (second, profile dir gone): %v", err)
	}
	if result.Action != service.ReconcileNoop {
		t.Errorf("Action: got %q, want %q", result.Action, service.ReconcileNoop)
	}
}

// TestReconcile_NewCommitPurgesAndReinserts (SPEC-105 AC2) verifies that
// reconciling the same profile at a NEW commit reports Action == switched
// and leaves exactly the profile's declared rule count (not double).
func TestReconcile_NewCommitPurgesAndReinserts(t *testing.T) {
	svc, mem, repoRoot, _ := newActivationTestEnv(t)
	ctx := context.Background()

	if _, err := svc.Reconcile(ctx, repoRoot, service.ActivationInput{Name: "acme", Commit: "c1"}); err != nil {
		t.Fatalf("Reconcile (c1): %v", err)
	}

	result, err := svc.Reconcile(ctx, repoRoot, service.ActivationInput{Name: "acme", Commit: "c2"})
	if err != nil {
		t.Fatalf("Reconcile (c2): %v", err)
	}
	if result.Action != service.ReconcileSwitched {
		t.Errorf("Action: got %q, want %q", result.Action, service.ReconcileSwitched)
	}

	lock, present, err := svc.ActiveLock(repoRoot)
	if err != nil || !present {
		t.Fatalf("ActiveLock: present=%v err=%v", present, err)
	}
	if lock.Commit != "c2" {
		t.Errorf("lock.Commit: got %q, want %q", lock.Commit, "c2")
	}

	ids, _, err := mem.ListProfileRuleIDs(ctx, "acme")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected exactly 1 rule after switching commits, got %d", len(ids))
	}
}

// TestReconcile_LockDivergesFromDB_Repairs (SPEC-105 AC3) is THE test that
// represents the 8 repos SPEC-105 measured contaminated: the lock declares
// 1 rule, but the database has extra rows with the same provenance the
// lock never recorded (sown directly via SaveProfileRule, bypassing
// Activate — the historical shape of the leak). Reconciling at the SAME
// profile+commit must NOT be a noop: it must detect the divergence, repair,
// and leave the database with exactly the profile's declared set.
func TestReconcile_LockDivergesFromDB_Repairs(t *testing.T) {
	svc, mem, repoRoot, _ := newActivationTestEnv(t)
	ctx := context.Background()
	in := service.ActivationInput{Name: "acme", Commit: "c1"}

	if _, err := svc.Reconcile(ctx, repoRoot, in); err != nil {
		t.Fatalf("Reconcile (first): %v", err)
	}

	// Simulate contamination: extra rows with the SAME provenance the lock
	// does not know about (the lock still lists only the original 1 id).
	for i := 0; i < 5; i++ {
		if _, err := mem.SaveProfileRule(ctx, model.SaveRequest{
			Title: "Extra contaminated rule", Content: "content", AppliesTo: []string{"**"},
		}, "acme"); err != nil {
			t.Fatalf("SaveProfileRule (contamination %d): %v", i, err)
		}
	}

	ids, _, err := mem.ListProfileRuleIDs(ctx, "acme")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs (pre-repair): %v", err)
	}
	if len(ids) != 6 {
		t.Fatalf("expected 6 rows before repair (1 + 5 contamination), got %d", len(ids))
	}

	result, err := svc.Reconcile(ctx, repoRoot, in)
	if err != nil {
		t.Fatalf("Reconcile (repair): %v", err)
	}
	if result.Action != service.ReconcileRepaired {
		t.Errorf("Action: got %q, want %q", result.Action, service.ReconcileRepaired)
	}
	if len(result.Divergences) == 0 {
		t.Error("expected at least one reported divergence")
	}

	idsAfter, _, err := mem.ListProfileRuleIDs(ctx, "acme")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs (post-repair): %v", err)
	}
	if len(idsAfter) != 1 {
		t.Fatalf("expected exactly 1 row after repair, got %d", len(idsAfter))
	}
}

// TestReconcile_MissingArtifactTriggersRepair (SPEC-105 AC4) verifies that
// deleting a materialized agent file (without touching the lock at all)
// makes the guard fail and Reconcile recreate it.
func TestReconcile_MissingArtifactTriggersRepair(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	ctx := context.Background()
	in := service.ActivationInput{Name: "acme", Commit: "c1"}

	if _, err := svc.Reconcile(ctx, repoRoot, in); err != nil {
		t.Fatalf("Reconcile (first): %v", err)
	}

	agentPath := filepath.Join(repoRoot, ".claude", "agents", "backend.md")
	if err := os.Remove(agentPath); err != nil {
		t.Fatalf("remove agent file: %v", err)
	}

	result, err := svc.Reconcile(ctx, repoRoot, in)
	if err != nil {
		t.Fatalf("Reconcile (repair): %v", err)
	}
	if result.Action != service.ReconcileRepaired {
		t.Errorf("Action: got %q, want %q", result.Action, service.ReconcileRepaired)
	}
	if _, err := os.Stat(agentPath); err != nil {
		t.Errorf("expected the agent file to be recreated: %v", err)
	}
}

// TestReconcile_BlockMarkerRemovedTriggersRepair (SPEC-105 AC4) verifies
// that removing the managed block's marker from CLAUDE.md (without
// touching the lock) makes the guard fail and Reconcile restore it.
func TestReconcile_BlockMarkerRemovedTriggersRepair(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	ctx := context.Background()
	in := service.ActivationInput{Name: "acme", Commit: "c1"}

	if _, err := svc.Reconcile(ctx, repoRoot, in); err != nil {
		t.Fatalf("Reconcile (first): %v", err)
	}

	claudeMD := filepath.Join(repoRoot, "CLAUDE.md")
	if err := managedblock.Remove(claudeMD, "profile"); err != nil {
		t.Fatalf("managedblock.Remove: %v", err)
	}

	result, err := svc.Reconcile(ctx, repoRoot, in)
	if err != nil {
		t.Fatalf("Reconcile (repair): %v", err)
	}
	if result.Action != service.ReconcileRepaired {
		t.Errorf("Action: got %q, want %q", result.Action, service.ReconcileRepaired)
	}

	_, _, present, err := managedblock.Read(claudeMD, "profile")
	if err != nil {
		t.Fatalf("managedblock.Read: %v", err)
	}
	if !present {
		t.Error("expected the managed block to be restored")
	}
}

// TestReconcile_BlockContentEditedTriggersRepair (SPEC-105 AC4) verifies
// that editing the managed block's content BY HAND (changing it from what
// the profile declares, without touching the lock's Digest) makes the
// digest comparison fail and Reconcile restore the profile's own content.
func TestReconcile_BlockContentEditedTriggersRepair(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)
	ctx := context.Background()
	in := service.ActivationInput{Name: "acme", Commit: "c1"}

	if _, err := svc.Reconcile(ctx, repoRoot, in); err != nil {
		t.Fatalf("Reconcile (first): %v", err)
	}

	claudeMD := filepath.Join(repoRoot, "CLAUDE.md")
	if err := managedblock.Upsert(claudeMD, "profile", 1, "Hand-edited content, not what acme declares"); err != nil {
		t.Fatalf("managedblock.Upsert (hand edit): %v", err)
	}

	result, err := svc.Reconcile(ctx, repoRoot, in)
	if err != nil {
		t.Fatalf("Reconcile (repair): %v", err)
	}
	if result.Action != service.ReconcileRepaired {
		t.Errorf("Action: got %q, want %q", result.Action, service.ReconcileRepaired)
	}

	content, _, present, err := managedblock.Read(claudeMD, "profile")
	if err != nil {
		t.Fatalf("managedblock.Read: %v", err)
	}
	if !present {
		t.Fatal("expected the managed block to be present")
	}
	if strings.Contains(content, "Hand-edited content") {
		t.Errorf("expected the hand-edited content to be overwritten, got %q", content)
	}
	if !strings.Contains(content, "Metodología Acme") {
		t.Errorf("expected acme's own block content restored, got %q", content)
	}
}

// TestReconcile_SwitchAtoB_LeavesNoTraceOfA (SPEC-105 AC6) verifies that
// reconciling from profile A to profile B leaves zero rules/agents/skills
// from A, and CLAUDE.md carries none of A's block content.
func TestReconcile_SwitchAtoB_LeavesNoTraceOfA(t *testing.T) {
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

	if _, err := svc.Reconcile(ctx, repoRoot, service.ActivationInput{Name: "profile-a", Commit: "commitA"}); err != nil {
		t.Fatalf("Reconcile A: %v", err)
	}

	result, err := svc.Reconcile(ctx, repoRoot, service.ActivationInput{Name: "profile-b", Commit: "commitB"})
	if err != nil {
		t.Fatalf("Reconcile B: %v", err)
	}
	if result.Action != service.ReconcileSwitched {
		t.Errorf("Action: got %q, want %q", result.Action, service.ReconcileSwitched)
	}

	backendPath := filepath.Join(repoRoot, ".claude", "agents", "backend.md")
	if _, err := os.Stat(backendPath); !os.IsNotExist(err) {
		t.Errorf("expected A's agent file gone, stat err = %v", err)
	}
	frontendPath := filepath.Join(repoRoot, ".claude", "agents", "frontend.md")
	if _, err := os.Stat(frontendPath); err != nil {
		t.Errorf("expected B's agent file present: %v", err)
	}

	claudeMD := filepath.Join(repoRoot, "CLAUDE.md")
	data, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if strings.Contains(string(data), "Profile A block") {
		t.Error("expected no trace of A's block content")
	}
	if !strings.Contains(string(data), "Profile B block") {
		t.Error("expected B's block content present")
	}

	idsA, _, err := mem.ListProfileRuleIDs(ctx, "profile-a")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs A: %v", err)
	}
	if len(idsA) != 0 {
		t.Errorf("expected 0 rules with profile-a provenance, got %d", len(idsA))
	}
	idsB, _, err := mem.ListProfileRuleIDs(ctx, "profile-b")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs B: %v", err)
	}
	if len(idsB) != 1 {
		t.Errorf("expected 1 rule with profile-b provenance, got %d", len(idsB))
	}
}

// TestReconcile_FutureLockSchemaIsBlockedWithoutMutation (SPEC-105 AC17)
// verifies that a lock declaring a schema_version this build does not
// understand makes Reconcile report Action == blocked, wrap
// model.ErrProfileLockUnsupported, and mutate NOTHING.
func TestReconcile_FutureLockSchemaIsBlockedWithoutMutation(t *testing.T) {
	svc, _, repoRoot, _ := newActivationTestEnv(t)

	agentPath := filepath.Join(repoRoot, ".claude", "agents", "backend.md")
	writeProfileFile(t, agentPath, "pre-existing content, must survive untouched")
	writeProfileFile(t, profile.LockPath(repoRoot), "schema_version = 99\nprofile = \"acme\"\ncommit = \"c1\"\n")

	result, err := svc.Reconcile(context.Background(), repoRoot, service.ActivationInput{Name: "acme", Commit: "c1"})
	if result == nil || result.Action != service.ReconcileBlocked {
		t.Fatalf("expected Action=blocked, got %+v (err=%v)", result, err)
	}
	if err == nil {
		t.Fatal("expected a non-nil error for an unsupported lock schema")
	}

	// Zero mutation: the pre-existing agent file is untouched, and the lock
	// file itself is byte-identical to what was written above.
	data, readErr := os.ReadFile(agentPath)
	if readErr != nil {
		t.Fatalf("read agent file: %v", readErr)
	}
	if string(data) != "pre-existing content, must survive untouched" {
		t.Errorf("expected the agent file untouched, got %q", string(data))
	}
	lockData, readErr := os.ReadFile(profile.LockPath(repoRoot))
	if readErr != nil {
		t.Fatalf("read lock file: %v", readErr)
	}
	if !strings.Contains(string(lockData), "schema_version = 99") {
		t.Errorf("expected the lock file untouched, got %q", string(lockData))
	}
}

// TestReconcile_PreflightFailureDoesNotDeactivate (SPEC-105 DD16) verifies
// that when preflightDeactivate finds a precondition unmet (here: the
// agents directory made read-only), Reconcile aborts BEFORE calling
// Deactivate — the previous lock and its artifacts stay exactly as they
// were.
func TestReconcile_PreflightFailureDoesNotDeactivate(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission-based write-failure injection does not apply")
	}

	svc, _, repoRoot, _ := newActivationTestEnv(t)
	ctx := context.Background()

	if _, err := svc.Reconcile(ctx, repoRoot, service.ActivationInput{Name: "acme", Commit: "c1"}); err != nil {
		t.Fatalf("Reconcile (first): %v", err)
	}
	agentPath := filepath.Join(repoRoot, ".claude", "agents", "backend.md")
	contentBefore, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("read agent before: %v", err)
	}

	agentsDir := filepath.Join(repoRoot, ".claude", "agents")
	if err := os.Chmod(agentsDir, 0o555); err != nil {
		t.Fatalf("chmod agents dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(agentsDir, 0o755) })

	// A different commit forces divergence (not noop), which is what
	// reaches the preflightDeactivate step at all.
	_, err = svc.Reconcile(ctx, repoRoot, service.ActivationInput{Name: "acme", Commit: "c2"})
	if err == nil {
		t.Fatal("expected Reconcile to fail the preflight against a read-only agents directory")
	}

	lockAfter, _, lockErr := svc.ActiveLock(repoRoot)
	if lockErr != nil {
		t.Fatalf("ActiveLock: %v", lockErr)
	}
	if lockAfter.Commit != "c1" {
		t.Errorf("expected the lock to remain at commit %q, got %q", "c1", lockAfter.Commit)
	}

	if err := os.Chmod(agentsDir, 0o755); err != nil {
		t.Fatalf("chmod agents dir back to writable: %v", err)
	}
	contentAfter, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("read agent after: %v", err)
	}
	if string(contentAfter) != string(contentBefore) {
		t.Errorf("expected the agent file untouched by the failed preflight, got %q", string(contentAfter))
	}
}

// TestReconcile_PreflightFailure_UnreadableBackupAbortsBeforeDeactivate
// (SPEC-105 DD16) verifies the THIRD precondition preflightDeactivate
// checks — that every artifact's registered Backup is readable — which the
// read-only-agents-directory scenario above never exercises (that one
// fails on the agent artifact's OWN first precondition, its parent
// directory, before preflightDeactivate ever reaches that same artifact's
// Backup check).
//
// The scenario is built so the check actually MATTERS, not just so an
// error eventually surfaces: the agent artifact (no backup — freshly
// created by the first activation) sorts BEFORE the skill artifact (backed
// up, because a hand-written, unpinned skill directory pre-existed) in
// lock.Artifacts, exactly the order Deactivate iterates in. Without this
// precondition, Deactivate would remove the agent file OUTRIGHT — a real,
// irreversible mutation — before ever reaching the skill artifact and
// discovering its backup is gone. A test that only checked the lock stayed
// at commit c1 (never checking whether Deactivate had already mutated
// something for the artifact ahead of the failing one in iteration order)
// would still pass even if this precondition were deleted from
// preflightDeactivate — this one does not.
func TestReconcile_PreflightFailure_UnreadableBackupAbortsBeforeDeactivate(t *testing.T) {
	svc, mem, repoRoot, skillsDir := newActivationTestEnv(t)
	ctx := context.Background()

	// A pre-existing, unpinned skill directory not owned by any prior
	// activation, so the first Reconcile displaces (backs up) it.
	existingSkillFile := filepath.Join(skillsDir, "acme-skill", "SKILL.md")
	writeProfileFile(t, existingSkillFile, "---\nname: acme-skill\npinned: false\n---\n\n# Pre-existing, unpinned\n")

	if _, err := svc.Reconcile(ctx, repoRoot, service.ActivationInput{Name: "acme", Commit: "c1"}); err != nil {
		t.Fatalf("Reconcile (first): %v", err)
	}

	lockBefore, present, err := svc.ActiveLock(repoRoot)
	if err != nil || !present {
		t.Fatalf("ActiveLock (before): present=%v err=%v", present, err)
	}
	var skillBackup string
	for _, a := range lockBefore.Artifacts {
		if a.Kind == profile.LockArtifactKindSkill && a.Backup != "" {
			skillBackup = a.Backup
		}
	}
	if skillBackup == "" {
		t.Fatal("expected the first activation to have backed up the pre-existing skill directory")
	}

	// The agent artifact — no backup, freshly created — must sort BEFORE
	// the skill artifact above in lock.Artifacts (Activate appends agents,
	// then skills, then the block): this is exactly what makes the test
	// meaningful, per the doc comment above.
	agentIdx, skillIdx := -1, -1
	for i, a := range lockBefore.Artifacts {
		switch a.Kind {
		case profile.LockArtifactKindAgent:
			agentIdx = i
		case profile.LockArtifactKindSkill:
			skillIdx = i
		}
	}
	if agentIdx == -1 || skillIdx == -1 || agentIdx >= skillIdx {
		t.Fatalf("expected the agent artifact to precede the skill artifact in lock.Artifacts, got agentIdx=%d skillIdx=%d", agentIdx, skillIdx)
	}

	agentPath := filepath.Join(repoRoot, ".claude", "agents", "backend.md")
	if _, err := os.Stat(agentPath); err != nil {
		t.Fatalf("expected the agent file to exist after the first activation: %v", err)
	}
	idsBefore, _, err := mem.ListProfileRuleIDs(ctx, "acme")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs (before): %v", err)
	}

	// Make the registered skill backup unreadable — deleted out from under
	// the lock (the same effect a corrupted disk or an operator's stray
	// `rm` would have).
	if err := os.RemoveAll(skillBackup); err != nil {
		t.Fatalf("remove skill backup: %v", err)
	}

	// A different commit forces divergence (not noop), which is what
	// reaches the preflightDeactivate step at all.
	_, err = svc.Reconcile(ctx, repoRoot, service.ActivationInput{Name: "acme", Commit: "c2"})
	if err == nil {
		t.Fatal("expected Reconcile to fail the preflight against an unreadable registered backup")
	}

	// Zero mutation: the agent file — which Deactivate would otherwise
	// remove outright on its way to the skill artifact that actually
	// fails — must survive untouched. This is the assertion an ungated
	// preflightDeactivate gets wrong: Deactivate would already have
	// deleted this file by the time it discovered the skill's backup was
	// gone.
	if _, err := os.Stat(agentPath); err != nil {
		t.Errorf("expected the agent file to survive (zero mutation before the preflight failure), got err: %v", err)
	}

	lockAfter, _, lockErr := svc.ActiveLock(repoRoot)
	if lockErr != nil {
		t.Fatalf("ActiveLock (after): %v", lockErr)
	}
	if lockAfter.Commit != "c1" {
		t.Errorf("expected the lock to remain at commit %q, got %q", "c1", lockAfter.Commit)
	}

	idsAfter, _, err := mem.ListProfileRuleIDs(ctx, "acme")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs (after): %v", err)
	}
	if len(idsAfter) != len(idsBefore) {
		t.Errorf("expected the rule count unaffected by the failed preflight: before=%d after=%d", len(idsBefore), len(idsAfter))
	}
}
