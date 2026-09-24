package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestReviewRange_FirstPass_Empty covers spec.md AC15's first_kind="base"
// case, and its Empty variant: advanceToQA never commits between entering
// implementing (which captures base_sha) and entering qa, so From==To.
func TestReviewRange_FirstPass_Empty(t *testing.T) {
	svc := newTestSDDServiceWithGitRepo(t, "project")
	ctx := context.Background()
	spec := advanceToQA(t, svc, ctx, "first pass empty")

	got := svc.ReviewRange(ctx, spec)
	if !got.Available {
		t.Fatalf("Available = false, want true: %+v", got)
	}
	if got.FromKind != "base" {
		t.Errorf("FromKind = %q, want %q", got.FromKind, "base")
	}
	if got.From != spec.BaseSHA {
		t.Errorf("From = %q, want spec.BaseSHA %q", got.From, spec.BaseSHA)
	}
	if !got.Empty {
		t.Error("Empty = false, want true (nothing committed since base)")
	}
	if got.FrontierLost {
		t.Error("FrontierLost = true, want false on a first pass")
	}
	if !strings.Contains(got.Notice, "no hay nada nuevo") {
		t.Errorf("Notice = %q, want it to say nothing new", got.Notice)
	}
}

// TestReviewRange_FirstPass_NotEmpty is spec.md AC15's base case with real
// work in the range: a commit lands between entering implementing (base)
// and entering qa (the delivered end), so From != To.
func TestReviewRange_FirstPass_NotEmpty(t *testing.T) {
	svc := newTestSDDServiceWithGitRepo(t, "project")
	ctx := context.Background()
	spec := advanceToImplementing(t, svc, ctx, "first pass not empty")
	commitOneMore(t, svc.repoDir, "work.txt", "implement the feature")

	spec, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing->qa): %v", err)
	}

	got := svc.ReviewRange(ctx, spec)
	if !got.Available {
		t.Fatalf("Available = false, want true: %+v", got)
	}
	if got.FromKind != "base" {
		t.Errorf("FromKind = %q, want %q", got.FromKind, "base")
	}
	if got.From != spec.BaseSHA {
		t.Errorf("From = %q, want spec.BaseSHA %q", got.From, spec.BaseSHA)
	}
	if got.To == got.From {
		t.Fatalf("setup: To == From, want a real commit between them")
	}
	if got.Empty {
		t.Error("Empty = true, want false — a commit landed since base")
	}
	if !strings.Contains(got.Notice, "primera pasada") {
		t.Errorf("Notice = %q, want it to say primera pasada", got.Notice)
	}
}

// TestReviewRange_SecondPass_UsesFrontier is spec.md AC15's from_kind
// "frontier" case: after a first review pass leaves a frontier, a second
// entry into qa (with new commits since) resolves From from that frontier
// — never from base_sha again.
func TestReviewRange_SecondPass_UsesFrontier(t *testing.T) {
	svc := newTestSDDServiceWithGitRepo(t, "project")
	ctx := context.Background()
	spec := advanceToQA(t, svc, ctx, "second pass")

	// Leave qa (reject), recording a frontier.
	spec, err := svc.SpecReject(ctx, model.SpecRejectRequest{ID: spec.ID, Reason: "needs work", By: "qa-agent", Findings: []model.RejectFinding{{CriterionID: "AC1", Detail: "does not hold"}}})
	if err != nil {
		t.Fatalf("SpecReject: %v", err)
	}
	history, err := svc.SpecHistory(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecHistory: %v", err)
	}
	exit1 := latestHistoryRow(history, func(h *model.SpecHistory) bool {
		return h.FromStatus == model.SpecStatusQA && h.ReviewedSHA != ""
	})
	if exit1 == nil {
		t.Fatalf("setup: expected a frontier-recording exit row")
	}
	frontier := exit1.ReviewedSHA

	// New commit, re-enter qa.
	commitOneMore(t, svc.repoDir, "fix.txt", "address review feedback")
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing->qa again): %v", err)
	}

	got := svc.ReviewRange(ctx, spec)
	if !got.Available {
		t.Fatalf("Available = false, want true: %+v", got)
	}
	if got.FromKind != "frontier" {
		t.Errorf("FromKind = %q, want %q", got.FromKind, "frontier")
	}
	if got.From != frontier {
		t.Errorf("From = %q, want the recorded frontier %q", got.From, frontier)
	}
	if got.FrontierLost {
		t.Error("FrontierLost = true, want false — linear history, frontier still an ancestor")
	}
	if got.Empty {
		t.Error("Empty = true, want false — a new commit landed since the frontier")
	}
	if !strings.Contains(got.Notice, "frontera anterior") {
		t.Errorf("Notice = %q, want it to mention the previous frontier", got.Notice)
	}
}

// TestReviewRange_FrontierLost_RealRebase is spec.md AC15/AC16's
// frontier_lost case, exercised against a REAL rewritten history (git
// commit --amend), not a simulated flag: the frontier recorded on a first
// exit stops being an ancestor of the new delivered end, so the range
// falls back to base_sha — the safe direction (D4).
func TestReviewRange_FrontierLost_RealRebase(t *testing.T) {
	svc := newTestSDDServiceWithGitRepo(t, "project")
	ctx := context.Background()
	spec := advanceToImplementing(t, svc, ctx, "frontier lost")
	// advanceToImplementing's own returned spec predates
	// onAdvanceSideEffects' captureBaseSHA write (SpecAdvance reloads
	// BEFORE running side effects) — reload to see the persisted value.
	spec, err := svc.store.GetSpec(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	baseSHA := spec.BaseSHA
	if baseSHA == "" {
		t.Fatalf("setup: expected base_sha to be captured by now")
	}

	commitOneMore(t, svc.repoDir, "work.txt", "first attempt")
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing->qa): %v", err)
	}
	spec, err = svc.SpecReject(ctx, model.SpecRejectRequest{ID: spec.ID, Reason: "needs work", By: "qa-agent", Findings: []model.RejectFinding{{CriterionID: "AC1", Detail: "does not hold"}}})
	if err != nil {
		t.Fatalf("SpecReject: %v", err)
	}
	history, err := svc.SpecHistory(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecHistory: %v", err)
	}
	exit1 := latestHistoryRow(history, func(h *model.SpecHistory) bool {
		return h.FromStatus == model.SpecStatusQA && h.ReviewedSHA != ""
	})
	lostFrontier := exit1.ReviewedSHA

	// A REAL rebase: amend the commit the frontier points at. The amended
	// commit gets a NEW SHA sharing the same parent — lostFrontier becomes
	// unreachable from the new HEAD, not merely "old".
	amendCommit(t, svc.repoDir, "amended after review")

	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing->qa, after amend): %v", err)
	}

	got := svc.ReviewRange(ctx, spec)
	if !got.Available {
		t.Fatalf("Available = false, want true (falls back to base, never blocks): %+v", got)
	}
	if !got.FrontierLost {
		t.Fatal("FrontierLost = false, want true — the amended commit is not an ancestor of the new delivered end")
	}
	if got.LostFrontier != lostFrontier {
		t.Errorf("LostFrontier = %q, want the dead SHA %q", got.LostFrontier, lostFrontier)
	}
	if got.FromKind != "base" || got.From != baseSHA {
		t.Errorf("From/FromKind = %q/%q, want base_sha %q/\"base\"", got.From, got.FromKind, baseSHA)
	}
	if !strings.Contains(got.Notice, "ya no es antepasada") {
		t.Errorf("Notice = %q, want it to name the lost ancestry", got.Notice)
	}
}

// TestReviewRange_ToNeverMovesAfterEntry is spec.md AC14: a commit landing
// AFTER entering qa (before any exit) must never move To — it stays the
// delivered end recorded at entry, read from the row, never HeadSHA.
func TestReviewRange_ToNeverMovesAfterEntry(t *testing.T) {
	svc := newTestSDDServiceWithGitRepo(t, "project")
	ctx := context.Background()
	spec := advanceToQA(t, svc, ctx, "to does not move")

	before := svc.ReviewRange(ctx, spec)
	commitOneMore(t, svc.repoDir, "sneaky.txt", "commit after entering qa, before any exit")
	after := svc.ReviewRange(ctx, spec)

	if before.To != after.To {
		t.Errorf("To moved from %q to %q after a commit landed post-entry — it must stay the delivered end", before.To, after.To)
	}
}

// TestReviewRange_Unavailable_NoDeliveredEnd is D5's first closed cause:
// the entry to qa left no delivered end (repoDir unset at entry).
func TestReviewRange_Unavailable_NoDeliveredEnd(t *testing.T) {
	svc := newTestSDDService(t, "project") // no repoDir at all
	ctx := context.Background()
	spec := advanceToImplementing(t, svc, ctx, "no delivered end")
	spec, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing->qa): %v", err)
	}

	got := svc.ReviewRange(ctx, spec)
	if got.Available {
		t.Fatalf("Available = true, want false: %+v", got)
	}
	if got.Unavailable != reviewUnavailableNoDeliveredEnd {
		t.Errorf("Unavailable = %q, want %q", got.Unavailable, reviewUnavailableNoDeliveredEnd)
	}
	if got.Notice == "" {
		t.Error("Notice must never be empty even when Available is false")
	}
}

// TestReviewRange_Unavailable_GitFailsAtIsAncestor is D5's second closed
// cause: a frontier row exists, but git fails NOW, while checking whether
// it is still an ancestor. The frontier is treated as lost, never valid,
// and the WHOLE range is unavailable rather than silently trusting base.
func TestReviewRange_Unavailable_GitFailsAtIsAncestor(t *testing.T) {
	svc := newTestSDDServiceWithGitRepo(t, "project")
	ctx := context.Background()
	spec := advanceToQA(t, svc, ctx, "git fails at ancestor check")
	spec, err := svc.SpecReject(ctx, model.SpecRejectRequest{ID: spec.ID, Reason: "needs work", By: "qa-agent", Findings: []model.RejectFinding{{CriterionID: "AC1", Detail: "does not hold"}}})
	if err != nil {
		t.Fatalf("SpecReject: %v", err)
	}
	commitOneMore(t, svc.repoDir, "more.txt", "more work")
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing->qa again): %v", err)
	}

	// A frontier row now exists (from the reject above). Point repoDir at a
	// directory that does not exist so g.IsAncestor genuinely fails to run
	// git, rather than returning a clean "not an ancestor".
	svc.WithRepoDir(filepath.Join(t.TempDir(), "does-not-exist"))

	got := svc.ReviewRange(ctx, spec)
	if got.Available {
		t.Fatalf("Available = true, want false: %+v", got)
	}
	if !strings.HasPrefix(got.Unavailable, "no se pudo comprobar si la frontera sigue siendo valida:") {
		t.Errorf("Unavailable = %q, want it to name the ancestry-check failure", got.Unavailable)
	}
}

// TestReviewRange_Unavailable_NoBaseSHA is D5's third closed cause: no
// usable frontier (first pass) AND no base_sha either.
func TestReviewRange_Unavailable_NoBaseSHA(t *testing.T) {
	svc := newTestSDDServiceWithGitRepo(t, "project")
	ctx := context.Background()

	spec := &model.Spec{
		ID: "SPEC-NOBASE", Title: "no base sha", Status: model.SpecStatusImplementing,
		Project: "project", Lane: model.LaneStandard,
		// BaseSHA left empty on purpose.
	}
	if err := svc.store.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	// Record a delivered end directly via the store, bypassing the normal
	// SpecAdvance flow — the only way to reach "delivered end present, but
	// base_sha absent", since production always captures base_sha before
	// a spec can ever reach qa.
	if err := svc.store.UpdateSpecStatus(ctx, spec.ID, model.SpecStatusImplementing, model.SpecStatusQA,
		"backend", "entrega", headSHA(t, svc.repoDir)); err != nil {
		t.Fatalf("UpdateSpecStatus: %v", err)
	}
	spec, err := svc.store.GetSpec(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if spec.BaseSHA != "" {
		t.Fatalf("setup: expected empty BaseSHA, got %q", spec.BaseSHA)
	}

	got := svc.ReviewRange(ctx, spec)
	if got.Available {
		t.Fatalf("Available = true, want false: %+v", got)
	}
	if got.Unavailable != reviewUnavailableNoBaseSHA {
		t.Errorf("Unavailable = %q, want %q", got.Unavailable, reviewUnavailableNoBaseSHA)
	}
}

// amendCommit rewrites repoDir's current HEAD commit in place — a REAL
// history rewrite (new SHA, same parent), used to exercise D4's frontier-
// lost detection against actual git behaviour rather than a simulated flag.
func amendCommit(t *testing.T, repoDir, message string) {
	t.Helper()
	runGitFrontierTest(t, repoDir, "commit", "--amend", "-m", message)
}

func TestRenderReviewNotice_Table(t *testing.T) {
	tests := []struct {
		name string
		r    model.ReviewRange
		want string // substring
	}{
		{"unavailable", model.ReviewRange{Unavailable: "esta spec no tiene commit base registrado"}, "no esta disponible"},
		{"empty", model.ReviewRange{Available: true, Empty: true, From: "aaa", To: "aaa"}, "no hay nada nuevo"},
		{"frontier lost", model.ReviewRange{Available: true, FrontierLost: true, LostFrontier: "dead1234", From: "base1234", To: "to123456"}, "ya no es antepasada"},
		{"frontier", model.ReviewRange{Available: true, FromKind: "frontier", From: "aaa11111", To: "bbb22222"}, "frontera anterior"},
		{"base", model.ReviewRange{Available: true, FromKind: "base", From: "aaa11111", To: "bbb22222"}, "primera pasada"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := renderReviewNotice(tc.r)
			if !strings.Contains(got, tc.want) {
				t.Errorf("renderReviewNotice(%+v) = %q, want substring %q", tc.r, got, tc.want)
			}
		})
	}
}
