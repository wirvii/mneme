package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// newNoSlugTestService builds a MemoryService the same way newTestService
// does, except with project="" — simulating internal/cli.initService's
// documented aliasing (SPEC-105 DD11: initService falls back to global.db as
// the project store when the working directory does not resolve a git
// remote slug). Both stores are still distinct in-memory databases here —
// what matters for these tests is HasProject()==false, not which physical
// file backs which store.
func newNoSlugTestService(t *testing.T) *service.MemoryService {
	t.Helper()
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
	return service.NewMemoryService(projectStore, globalStore, cfg, "", embed.NopEmbedder{})
}

// newSharedStoreTestService builds a MemoryService whose project store and
// global store are the SAME underlying database — the real-world shape of
// initService's global.db-as-projectStore aliasing (SPEC-105 §1.2), used by
// TestPurgeProfileRules_SweepsOrphansAndIsIdempotent to prove the second
// sweep in PurgeProfileRules is idempotent rather than harmful when both
// stores are literally one file.
func newSharedStoreTestService(t *testing.T, project string) (*service.MemoryService, *store.MemoryStore) {
	t.Helper()
	sharedDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open shared db: %v", err)
	}
	t.Cleanup(func() { sharedDB.Close() })
	shared := store.NewMemoryStore(sharedDB)
	cfg := config.Default()
	svc := service.NewMemoryService(shared, shared, cfg, project, embed.NopEmbedder{})
	return svc, shared
}

// TestSaveProfileRule_StampsProvenance verifies that SaveProfileRule always
// stamps source="profile:<name>", forces project scope and Shared=0
// regardless of what the caller's request carried on those fields, and that
// the resulting rule is picked up by Context's rules phase like any other
// active rule (SPEC-092 AC4).
func TestSaveProfileRule_StampsProvenance(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	global := model.ScopeGlobal
	shared := 2
	resp, err := svc.SaveProfileRule(ctx, model.SaveRequest{
		Title:     "No CGO",
		Content:   "This repo builds pure-Go, no CGO, no build tags.",
		AppliesTo: []string{"**"},
		Severity:  model.SeverityWarn,
		Scope:     global, // should be overridden to project
		Shared:    &shared,
	}, "chatea-pro")
	if err != nil {
		t.Fatalf("SaveProfileRule: %v", err)
	}

	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mem.Source != "profile:chatea-pro" {
		t.Errorf("Source: got %q, want %q", mem.Source, "profile:chatea-pro")
	}
	if mem.Scope != model.ScopeProject {
		t.Errorf("Scope: got %q, want %q (SaveProfileRule must force project scope)", mem.Scope, model.ScopeProject)
	}
	if mem.Type != model.TypeRule {
		t.Errorf("Type: got %q, want %q", mem.Type, model.TypeRule)
	}
	if mem.Shared != 0 {
		t.Errorf("Shared: got %d, want 0 (SaveProfileRule must force Shared=0)", mem.Shared)
	}

	// The rule must be visible through the normal rules phase like any other
	// active rule.
	ctxResp, err := svc.Context(ctx, model.ContextRequest{Project: svc.ProjectSlug()})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	found := false
	for _, r := range ctxResp.Rules {
		if r.ID == resp.ID {
			found = true
		}
	}
	if !found {
		t.Error("expected profile-stamped rule to appear in Context().Rules")
	}
}

// TestMemSave_CannotForgeSource verifies the security boundary AC4 requires:
// a SaveRequest built the way the public mem_save path builds it (from
// caller-controlled JSON) can never carry a non-empty Source, because the
// field is json:"-" and therefore never populated by json.Unmarshal —
// regardless of what key the raw payload contains.
func TestMemSave_CannotForgeSource(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	raw := []byte(`{"title":"t","content":"c","source":"profile:evil"}`)
	var req model.SaveRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Source != "" {
		t.Fatalf("expected Source to stay empty after unmarshaling attacker JSON, got %q", req.Source)
	}

	resp, err := svc.Save(ctx, req)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mem.Source != "" {
		t.Errorf("Source: got %q, want empty — mem_save must never be able to set provenance", mem.Source)
	}
}

// TestPurgeProfileRules_HardDeletes verifies PurgeProfileRules physically
// removes every rule stamped with the given profile's provenance — the row
// no longer exists at all, not merely soft-deleted — while leaving
// hand-authored rules (Source == "") untouched (SPEC-092 AC5).
func TestPurgeProfileRules_HardDeletes(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	var stampedIDs []string
	for i := 0; i < 2; i++ {
		resp, err := svc.SaveProfileRule(ctx, model.SaveRequest{
			Title:     "Profile rule",
			Content:   "content",
			AppliesTo: []string{"**"},
		}, "chatea-pro")
		if err != nil {
			t.Fatalf("SaveProfileRule: %v", err)
		}
		stampedIDs = append(stampedIDs, resp.ID)
	}

	handSevr := model.SeverityWarn
	handResp, err := svc.Save(ctx, model.SaveRequest{
		Title:     "Hand-authored rule",
		Content:   "content",
		Type:      model.TypeRule,
		AppliesTo: []string{"**"},
		Severity:  handSevr,
	})
	if err != nil {
		t.Fatalf("Save hand-authored: %v", err)
	}

	removed, err := svc.PurgeProfileRules(ctx, svc.ProjectSlug(), "chatea-pro")
	if err != nil {
		t.Fatalf("PurgeProfileRules: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 removed ids, got %d (%v)", len(removed), removed)
	}

	for _, id := range stampedIDs {
		_, err := svc.Get(ctx, id)
		if !errors.Is(err, model.ErrNotFound) {
			t.Errorf("expected ErrNotFound for purged rule %s, got %v", id, err)
		}
	}

	// (a) Forget (decay) does NOT achieve this — it never touches deleted_at,
	// so a "forgotten" rule still satisfies deleted_at IS NULL and keeps
	// showing up in loadActiveRules. Demonstrated here against a *third*
	// profile-stamped rule so the assertion is about Forget specifically.
	forgottenResp, err := svc.SaveProfileRule(ctx, model.SaveRequest{
		Title:     "Forgettable",
		Content:   "content",
		AppliesTo: []string{"**"},
	}, "chatea-pro")
	if err != nil {
		t.Fatalf("SaveProfileRule (forgettable): %v", err)
	}
	if err := svc.Forget(ctx, forgottenResp.ID, "test"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	stillThere, err := svc.Get(ctx, forgottenResp.ID)
	if err != nil {
		t.Fatalf("Get after Forget: %v", err)
	}
	if stillThere == nil {
		t.Fatal("expected Forget to leave the row retrievable (it only decays importance, never deletes)")
	}
	// This is the regression this test exists to pin: Forget only decays
	// importance (SetDecayRate), it never touches deleted_at, so
	// loadActiveRules (which filters on deleted_at IS NULL, not importance)
	// still returns the "forgotten" rule. That insufficiency is exactly why
	// PurgeProfileRules/HardDeleteBySource exist.
	ctxResp, err := svc.Context(ctx, model.ContextRequest{Project: svc.ProjectSlug()})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	foundAfterForget := false
	for _, r := range ctxResp.Rules {
		if r.ID == forgottenResp.ID {
			foundAfterForget = true
		}
	}
	if !foundAfterForget {
		t.Error("expected Forget to be insufficient: the forgotten rule should still appear in Context().Rules")
	}

	// Hand-authored rule survives the purge untouched.
	got, err := svc.Get(ctx, handResp.ID)
	if err != nil {
		t.Fatalf("Get hand-authored: %v", err)
	}
	if got == nil {
		t.Fatal("expected hand-authored rule to survive PurgeProfileRules")
	}

	// (b) Idempotent — nothing left to purge.
	removedAgain, err := svc.PurgeProfileRules(ctx, svc.ProjectSlug(), "chatea-pro")
	if err != nil {
		t.Fatalf("PurgeProfileRules (second call, only forgottenResp left): %v", err)
	}
	if len(removedAgain) != 1 {
		t.Fatalf("expected 1 remaining profile rule (forgottenResp) to be purged, got %d", len(removedAgain))
	}

	final, err := svc.PurgeProfileRules(ctx, svc.ProjectSlug(), "chatea-pro")
	if err != nil {
		t.Fatalf("PurgeProfileRules (third call): %v", err)
	}
	if len(final) != 0 {
		t.Errorf("expected 0 removed on third call, got %d", len(final))
	}
}

// TestSaveProfileRule_NoSlugReturnsErrProjectSlugRequired (SPEC-105 AC18)
// verifies that a MemoryService with no resolved project slug rejects
// SaveProfileRule outright and writes nothing — the fix for the cross-repo
// leak: a project-scoped row with no project used to be served in every
// repo on the host.
func TestSaveProfileRule_NoSlugReturnsErrProjectSlugRequired(t *testing.T) {
	svc := newNoSlugTestService(t)
	ctx := context.Background()

	_, err := svc.SaveProfileRule(ctx, model.SaveRequest{
		Title:     "No slug rule",
		Content:   "content",
		AppliesTo: []string{"**"},
	}, "chatea-pro")
	if !errors.Is(err, model.ErrProjectSlugRequired) {
		t.Fatalf("expected ErrProjectSlugRequired, got %v", err)
	}

	rows, err := svc.List(ctx, store.ListOptions{Type: model.TypeRule, Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows written, got %d", len(rows))
	}
}

// TestSaveProfileRule_WithSlugStillWorks is a non-regression: a service with
// a resolved slug is unaffected by the new guard.
func TestSaveProfileRule_WithSlugStillWorks(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.SaveProfileRule(ctx, model.SaveRequest{
		Title:     "Slugged rule",
		Content:   "content",
		AppliesTo: []string{"**"},
	}, "chatea-pro")
	if err != nil {
		t.Fatalf("SaveProfileRule: %v", err)
	}
	if resp.ID == "" {
		t.Fatal("expected a non-empty id")
	}
}

// TestPurgeProfileRules_SweepsOrphansAndIsIdempotent (SPEC-105 AC23) sows
// orphan rows directly into the global store (bypassing SaveProfileRule,
// which now refuses to create them) plus normal rows in the project store,
// then verifies PurgeProfileRules zeroes out BOTH sets and that a second
// call is idempotent.
func TestPurgeProfileRules_SweepsOrphansAndIsIdempotent(t *testing.T) {
	svc, shared := newSharedStoreTestService(t, "proj-x")
	ctx := context.Background()

	// Normal project-scoped rows via the real write path.
	if _, err := svc.SaveProfileRule(ctx, model.SaveRequest{
		Title: "Scoped rule", Content: "content", AppliesTo: []string{"**"},
	}, "chatea-pro"); err != nil {
		t.Fatalf("SaveProfileRule: %v", err)
	}

	// Orphan rows: seeded directly against the store (SaveProfileRule would
	// now reject these) to simulate the pre-fix leak's leftovers.
	orphan := &model.Memory{
		Type:       model.TypeRule,
		Scope:      model.ScopeProject,
		Title:      "Orphan rule",
		Content:    "content",
		Project:    "",
		AppliesTo:  []string{"**"},
		Source:     "profile:chatea-pro",
		Importance: 0.5,
		DecayRate:  0.01,
	}
	if _, err := shared.Create(ctx, orphan); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	removed, err := svc.PurgeProfileRules(ctx, svc.ProjectSlug(), "chatea-pro")
	if err != nil {
		t.Fatalf("PurgeProfileRules: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 removed ids (1 scoped + 1 orphan), got %d (%v)", len(removed), removed)
	}

	ids, _, err := svc.ListProfileRuleIDs(ctx, "chatea-pro")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 remaining project-scoped rows, got %d", len(ids))
	}
	orphanIDs, err := svc.ListOrphanProfileRuleIDs(ctx, "chatea-pro")
	if err != nil {
		t.Fatalf("ListOrphanProfileRuleIDs: %v", err)
	}
	if len(orphanIDs) != 0 {
		t.Errorf("expected 0 remaining orphan rows, got %d", len(orphanIDs))
	}

	// Second call: idempotent, no error, nothing left to remove.
	removedAgain, err := svc.PurgeProfileRules(ctx, svc.ProjectSlug(), "chatea-pro")
	if err != nil {
		t.Fatalf("PurgeProfileRules (second call): %v", err)
	}
	if len(removedAgain) != 0 {
		t.Errorf("expected 0 removed on idempotent second call, got %d", len(removedAgain))
	}
}

// TestListProfileRuleIDs_ReturnsOnlyThatProvenance verifies the id set is
// scoped exactly to the requested profile's provenance, not to every rule in
// the project.
func TestListProfileRuleIDs_ReturnsOnlyThatProvenance(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.SaveProfileRule(ctx, model.SaveRequest{
		Title: "A", Content: "content", AppliesTo: []string{"**"},
	}, "chatea-pro")
	if err != nil {
		t.Fatalf("SaveProfileRule chatea-pro: %v", err)
	}
	if _, err := svc.SaveProfileRule(ctx, model.SaveRequest{
		Title: "B", Content: "content", AppliesTo: []string{"**"},
	}, "other-profile"); err != nil {
		t.Fatalf("SaveProfileRule other-profile: %v", err)
	}
	if _, err := svc.Save(ctx, model.SaveRequest{
		Title: "Hand-authored", Content: "content", Type: model.TypeRule,
		AppliesTo: []string{"**"}, Severity: model.SeverityWarn,
	}); err != nil {
		t.Fatalf("Save hand-authored: %v", err)
	}

	ids, truncated, err := svc.ListProfileRuleIDs(ctx, "chatea-pro")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs: %v", err)
	}
	if truncated {
		t.Error("expected truncated=false")
	}
	if len(ids) != 1 || ids[0] != resp.ID {
		t.Fatalf("expected exactly [%s], got %v", resp.ID, ids)
	}
}

// TestListProfileRuleIDs_NoSlugReturnsSentinel verifies the DD9 contract: no
// resolved slug means there is no project-scoped set to compare against.
func TestListProfileRuleIDs_NoSlugReturnsSentinel(t *testing.T) {
	svc := newNoSlugTestService(t)
	ctx := context.Background()

	_, _, err := svc.ListProfileRuleIDs(ctx, "chatea-pro")
	if !errors.Is(err, model.ErrProjectSlugRequired) {
		t.Fatalf("expected ErrProjectSlugRequired, got %v", err)
	}
}

// TestListProfileRuleIDs_TruncatedFlag verifies the non-truncated case at a
// small, fast scale — a handful of rows well under the cap must report
// truncated=false and the exact count. The cap itself (5000) is a package-
// private constant not worth reproducing here; the truncated=true path is
// exercised structurally by TestConverged_TruncatedRuleScanIsDivergent in
// internal/profile, which is where the fail-safe's actual behaviour (always
// divergent) is asserted.
func TestListProfileRuleIDs_TruncatedFlag(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	const seedCount = 5
	for i := 0; i < seedCount; i++ {
		if _, err := svc.SaveProfileRule(ctx, model.SaveRequest{
			Title: "Rule", Content: "content", AppliesTo: []string{"**"},
		}, "chatea-pro"); err != nil {
			t.Fatalf("SaveProfileRule %d: %v", i, err)
		}
	}

	ids, truncated, err := svc.ListProfileRuleIDs(ctx, "chatea-pro")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs: %v", err)
	}
	if truncated {
		t.Error("expected truncated=false when the seed count is well under the cap")
	}
	if len(ids) != seedCount {
		t.Fatalf("expected %d ids, got %d", seedCount, len(ids))
	}
}

// TestListOrphanProfileRuleIDs_FindsGlobalProjectScopedRows verifies the
// orphan lookup surfaces exactly the rows SaveProfileRule can no longer
// create but that a pre-fix leak already left behind.
func TestListOrphanProfileRuleIDs_FindsGlobalProjectScopedRows(t *testing.T) {
	svc, shared := newSharedStoreTestService(t, "")
	ctx := context.Background()

	orphan := &model.Memory{
		Type:       model.TypeRule,
		Scope:      model.ScopeProject,
		Title:      "Orphan",
		Content:    "content",
		Project:    "",
		AppliesTo:  []string{"**"},
		Source:     "profile:chatea-pro",
		Importance: 0.5,
		DecayRate:  0.01,
	}
	created, err := shared.Create(ctx, orphan)
	if err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	ids, err := svc.ListOrphanProfileRuleIDs(ctx, "chatea-pro")
	if err != nil {
		t.Fatalf("ListOrphanProfileRuleIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != created.ID {
		t.Fatalf("expected exactly [%s], got %v", created.ID, ids)
	}
}

// TestListProfileRuleIDs_IncludesSupersededRows (SPEC-105 AC32 live
// verification, post-implementation) verifies that ListProfileRuleIDs sees
// a profile rule even after it has been marked superseded by an unrelated
// `conflicts scan --apply` — PurgeProfileRules/HardDeleteBySource deletes a
// profile-sourced row regardless of superseded_by, so excluding it here
// would make the observer disagree with what the purge actually does,
// which is exactly what made Converged report permanent divergence for a
// workspace that had, in every practical sense, already converged.
func TestListProfileRuleIDs_IncludesSupersededRows(t *testing.T) {
	svc, shared := newSharedStoreTestService(t, "test/project")
	ctx := context.Background()

	winner, err := svc.SaveProfileRule(ctx, model.SaveRequest{
		Title: "Winner", Content: "content", AppliesTo: []string{"**"},
	}, "acme")
	if err != nil {
		t.Fatalf("SaveProfileRule (winner): %v", err)
	}
	loser, err := svc.SaveProfileRule(ctx, model.SaveRequest{
		Title: "Loser", Content: "content", AppliesTo: []string{"**"},
	}, "acme")
	if err != nil {
		t.Fatalf("SaveProfileRule (loser): %v", err)
	}

	// This is the store-level primitive conflicts.persistVerdict uses for a
	// "supersedes" judgment — accessed directly here since the test only
	// needs the resulting column state, not the LLM judge pipeline.
	if err := shared.SetSupersededBy(ctx, loser.ID, winner.ID); err != nil {
		t.Fatalf("SetSupersededBy: %v", err)
	}

	ids, truncated, err := svc.ListProfileRuleIDs(ctx, "acme")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs: %v", err)
	}
	if truncated {
		t.Error("expected truncated=false")
	}
	if len(ids) != 2 {
		t.Fatalf("expected both ids (including the superseded one), got %d: %v", len(ids), ids)
	}
	foundLoser := false
	for _, id := range ids {
		if id == loser.ID {
			foundLoser = true
		}
	}
	if !foundLoser {
		t.Errorf("expected the superseded rule %s among %v", loser.ID, ids)
	}
}

// TestListOrphanProfileRuleIDs_IncludesSupersededRows mirrors
// TestListProfileRuleIDs_IncludesSupersededRows for the global-store orphan
// sweep: a superseded orphan row must still be reported, for the same
// reason (the purge doesn't respect superseded_by either).
func TestListOrphanProfileRuleIDs_IncludesSupersededRows(t *testing.T) {
	svc, shared := newSharedStoreTestService(t, "")
	ctx := context.Background()

	orphan := &model.Memory{
		Type:       model.TypeRule,
		Scope:      model.ScopeProject,
		Title:      "Orphan",
		Content:    "content",
		Project:    "",
		AppliesTo:  []string{"**"},
		Source:     "profile:chatea-pro",
		Importance: 0.5,
		DecayRate:  0.01,
	}
	created, err := shared.Create(ctx, orphan)
	if err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	if err := shared.SetSupersededBy(ctx, created.ID, "019f0000-0000-7000-8000-000000000000"); err != nil {
		t.Fatalf("SetSupersededBy: %v", err)
	}

	ids, err := svc.ListOrphanProfileRuleIDs(ctx, "chatea-pro")
	if err != nil {
		t.Fatalf("ListOrphanProfileRuleIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != created.ID {
		t.Fatalf("expected exactly [%s] (including superseded), got %v", created.ID, ids)
	}
}
