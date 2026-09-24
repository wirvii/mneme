package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// newTestSDDServiceWithGitRepo mirrors newTestSDDServiceWithRepoDir
// (criteria_test.go), except repoDir is a REAL git repository with one
// commit (newTestGitRepo, sdd_test.go), not an empty t.TempDir(): SPEC-157's
// frontier logic reads real HEAD SHAs, so exercising it needs a directory
// `git rev-parse HEAD` actually succeeds against.
func newTestSDDServiceWithGitRepo(t *testing.T, project string) *SDDService {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	cfg.Workflow.Dir = t.TempDir()

	svc := NewSDDService(sddStore, cfg, project, nil)
	svc.WithRepoDir(newTestGitRepo(t))
	return svc
}

// commitOneMore creates an additional commit in repoDir and returns its
// SHA — used to simulate the implementer committing again while QA is
// still reviewing (D2b's whole reason for existing).
func commitOneMore(t *testing.T, repoDir, filename, message string) string {
	t.Helper()
	path := filepath.Join(repoDir, filename)
	if err := os.WriteFile(path, []byte("content\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", message}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return headSHA(t, repoDir)
}

// mustParseRFC3339 parses s as time.RFC3339Nano, failing the test on error.
func mustParseRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse fixture time %q: %v", s, err)
	}
	return tm
}

// --- TestFrontier_AdvancesOnlyToDeliveredEnd ---

// TestFrontier_AdvancesOnlyToDeliveredEnd is SPEC-157's central test (D2c,
// AC5/AC6 of spec.md, AC5 of criteria.toml): with a commit intercalated
// between entering qa and leaving it, the recorded frontier is the
// DELIVERED end (HEAD at entry), never the HEAD at the moment the informe
// is emitted. Two sub-cases: qa -> implementing (SpecReject) and
// qa -> done (SpecAdvance).
func TestFrontier_AdvancesOnlyToDeliveredEnd(t *testing.T) {
	t.Run("qa -> implementing (SpecReject)", func(t *testing.T) {
		svc := newTestSDDServiceWithGitRepo(t, "project")
		ctx := context.Background()

		spec := advanceToQA(t, svc, ctx, "frontier reject")
		history, err := svc.SpecHistory(ctx, spec.ID)
		if err != nil {
			t.Fatalf("SpecHistory: %v", err)
		}
		entryRow := latestHistoryRow(history, func(h *model.SpecHistory) bool {
			return h.FromStatus == model.SpecStatusImplementing && h.ToStatus == model.SpecStatusQA
		})
		if entryRow == nil || entryRow.ReviewedSHA == "" {
			t.Fatalf("setup: expected implementing->qa row with a delivered end, got %+v", entryRow)
		}
		delivered := entryRow.ReviewedSHA

		movedHead := commitOneMore(t, svc.repoDir, "after-entry.txt", "commit while QA reviews")
		if movedHead == delivered {
			t.Fatalf("setup: commitOneMore did not move HEAD")
		}

		if _, err := svc.SpecReject(ctx, model.SpecRejectRequest{
			ID: spec.ID, Reason: "found issues", By: "qa-agent",
			// advanceToQA/advanceToImplementing write a criteria.toml
			// (SPEC-156 fixture), so this reject needs a declared finding.
			Findings: []model.RejectFinding{{CriterionID: "AC1", Detail: "does not hold"}},
		}); err != nil {
			t.Fatalf("SpecReject: %v", err)
		}

		history, err = svc.SpecHistory(ctx, spec.ID)
		if err != nil {
			t.Fatalf("SpecHistory after reject: %v", err)
		}
		last := history[len(history)-1]
		if last.FromStatus != model.SpecStatusQA || last.ToStatus != model.SpecStatusImplementing {
			t.Fatalf("expected last row to be qa->implementing, got %s->%s", last.FromStatus, last.ToStatus)
		}
		if last.ReviewedSHA != delivered {
			t.Errorf("ReviewedSHA = %q, want the DELIVERED end %q (never the moved HEAD %q)", last.ReviewedSHA, delivered, movedHead)
		}
		if !strings.Contains(last.Reason, delivered[:frontierSHAAbbrevLen]) {
			t.Errorf("reason does not name the delivered end %q:\n%s", delivered[:8], last.Reason)
		}
		if !strings.Contains(last.Reason, movedHead[:frontierSHAAbbrevLen]) {
			t.Errorf("reason does not name the moved HEAD %q:\n%s", movedHead[:8], last.Reason)
		}
	})

	t.Run("qa -> done (SpecAdvance)", func(t *testing.T) {
		svc := newTestSDDServiceWithGitRepo(t, "project")
		ctx := context.Background()

		spec := advanceToQA(t, svc, ctx, "frontier accept")
		history, err := svc.SpecHistory(ctx, spec.ID)
		if err != nil {
			t.Fatalf("SpecHistory: %v", err)
		}
		entryRow := latestHistoryRow(history, func(h *model.SpecHistory) bool {
			return h.FromStatus == model.SpecStatusImplementing && h.ToStatus == model.SpecStatusQA
		})
		delivered := entryRow.ReviewedSHA

		movedHead := commitOneMore(t, svc.repoDir, "after-entry.txt", "commit while QA reviews")
		if movedHead == delivered {
			t.Fatalf("setup: commitOneMore did not move HEAD")
		}

		updated, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "qa-agent"})
		if err != nil {
			t.Fatalf("SpecAdvance (qa->done): %v", err)
		}
		if updated.Status != model.SpecStatusDone {
			t.Fatalf("expected done, got %s", updated.Status)
		}

		history, err = svc.SpecHistory(ctx, spec.ID)
		if err != nil {
			t.Fatalf("SpecHistory after advance: %v", err)
		}
		last := history[len(history)-1]
		if last.ReviewedSHA != delivered {
			t.Errorf("ReviewedSHA = %q, want the DELIVERED end %q (never the moved HEAD %q)", last.ReviewedSHA, delivered, movedHead)
		}
		if !strings.Contains(last.Reason, "frontera: ") {
			t.Errorf("reason must carry a frontier note when HEAD moved:\n%s", last.Reason)
		}
	})
}

// TestFrontier_CaseA_HeadUnmovedLeavesReasonByteIdentical is D2c's case
// (a): HEAD unchanged between entering and leaving qa produces a reason
// with NO note appended — byte-identical to what it would be without this
// spec (spec.md AC6).
func TestFrontier_CaseA_HeadUnmovedLeavesReasonByteIdentical(t *testing.T) {
	svc := newTestSDDServiceWithGitRepo(t, "project")
	ctx := context.Background()

	spec := advanceToQA(t, svc, ctx, "case a")

	updated, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "qa-agent", Reason: "looks good"})
	if err != nil {
		t.Fatalf("SpecAdvance (qa->done): %v", err)
	}
	if updated.Status != model.SpecStatusDone {
		t.Fatalf("expected done, got %s", updated.Status)
	}

	history, err := svc.SpecHistory(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecHistory: %v", err)
	}
	last := history[len(history)-1]
	if last.Reason != "looks good" {
		t.Errorf("Reason = %q, want unchanged %q (HEAD never moved, no note must be appended)", last.Reason, "looks good")
	}
	if last.ReviewedSHA == "" {
		t.Error("ReviewedSHA must still be set (copied from the delivered end) even with no note")
	}
}

// TestFrontier_CaseC_EntryWithoutDeliveredEndRecordsNothing is D2c's case
// (c): the current implementing->qa entry left no delivered end (repoDir
// unset at entry) — the exit records an EMPTY reviewed_sha and a note
// explaining why, never inventing HEAD (spec.md AC7).
func TestFrontier_CaseC_EntryWithoutDeliveredEndRecordsNothing(t *testing.T) {
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	cfg.Workflow.Dir = t.TempDir()

	svc := NewSDDService(sddStore, cfg, "project", nil)
	// repoDir left UNSET for the entry to qa — the delivered end cannot be
	// resolved.
	spec := advanceToImplementing(t, svc, context.Background(), "case c")
	ctx := context.Background()
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing->qa): %v", err)
	}
	if spec.Status != model.SpecStatusQA {
		t.Fatalf("expected qa, got %s", spec.Status)
	}

	// repoDir is set AFTER entering qa — the exit CAN read git now, but the
	// entry never left a delivered end to copy.
	svc.WithRepoDir(newTestGitRepo(t))

	updated, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "qa-agent"})
	if err != nil {
		t.Fatalf("SpecAdvance (qa->done): %v", err)
	}
	if updated.Status != model.SpecStatusDone {
		t.Fatalf("expected done, got %s", updated.Status)
	}

	history, err := svc.SpecHistory(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecHistory: %v", err)
	}
	last := history[len(history)-1]
	if last.ReviewedSHA != "" {
		t.Errorf("ReviewedSHA = %q, want empty (no delivered end to copy — never invent HEAD)", last.ReviewedSHA)
	}
	wantNote := "frontera: no se registra frontera: la entrada a revision no dejo extremo entregado, " +
		"asi que la siguiente pasada cubre desde el commit base."
	if !strings.HasSuffix(last.Reason, wantNote) {
		t.Errorf("Reason = %q, want suffix %q", last.Reason, wantNote)
	}
}

// TestFrontier_ClosedTransitionSet_NeverTouchesGit is spec.md AC8/AC9,
// criteria.toml AC8/AC9: EVERY transition outside the three-member set
// (implementing->qa, qa->done, qa->implementing) returns the ZERO
// frontierDecision, with repoDir pointed at a directory that DOES NOT
// EXIST — proving there is no I/O and no error for any of them, not just
// that the visible SHA/Note happen to be empty.
func TestFrontier_ClosedTransitionSet_NeverTouchesGit(t *testing.T) {
	svc := newTestSDDService(t, "project")
	svc.WithRepoDir(filepath.Join(t.TempDir(), "does-not-exist"))
	ctx := context.Background()

	excluded := []struct {
		name     string
		from, to model.SpecStatus
	}{
		{"qa -> needs_grill (pushback, no informe)", model.SpecStatusQA, model.SpecStatusNeedsGrill},
		{"done -> implementing (post-hoc reject)", model.SpecStatusDone, model.SpecStatusImplementing},
		{"audit -> done (trivial lane auditor)", model.SpecStatusAudit, model.SpecStatusDone},
		{"audit -> implementing (trivial lane auditor)", model.SpecStatusAudit, model.SpecStatusImplementing},
		{"draft -> speccing", model.SpecStatusDraft, model.SpecStatusSpeccing},
		{"speccing -> specced", model.SpecStatusSpeccing, model.SpecStatusSpecced},
		{"planned -> implementing", model.SpecStatusPlanned, model.SpecStatusImplementing},
		{"implementing -> needs_grill", model.SpecStatusImplementing, model.SpecStatusNeedsGrill},
		{"draft -> rationale (trivial)", model.SpecStatusDraft, model.SpecStatusRationale},
		{"rationale -> implementing (trivial)", model.SpecStatusRationale, model.SpecStatusImplementing},
	}

	for _, tc := range excluded {
		t.Run(tc.name, func(t *testing.T) {
			d := svc.frontierForTransition(ctx, "SPEC-000", tc.from, tc.to)
			if d.SHA != "" || d.Note != "" {
				t.Errorf("frontierForTransition(%s->%s) = %+v, want zero value (no I/O for this pair)", tc.from, tc.to, d)
			}
		})
	}
}

// --- TestFrontier_NoteAppendsAfterRejectFindings (SPEC-156 cross-check) ---

// TestFrontier_NoteAppendsAfterRejectFindings is spec.md AC18 (D2d/R4):
// the frontier note is appended AFTER whatever SPEC-156 already composed
// (human reason -> findings -> frontera), never reordering or rewriting
// it. Verified against renderRejectReason's own real output, with HEAD
// moved so a note is actually produced.
func TestFrontier_NoteAppendsAfterRejectFindings(t *testing.T) {
	svc := newTestSDDServiceWithGitRepo(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "frontier + findings", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	writeSpecCriteria(t, svc, spec, "AC1", "AC2")
	for _, by := range []string{"orch", "arch", "arch", "arch", "backend", "backend"} {
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: by})
		if err != nil {
			t.Fatalf("SpecAdvance (to qa, status=%s): %v", spec.Status, err)
		}
	}

	history, err := svc.SpecHistory(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecHistory: %v", err)
	}
	entryRow := latestHistoryRow(history, func(h *model.SpecHistory) bool {
		return h.FromStatus == model.SpecStatusImplementing && h.ToStatus == model.SpecStatusQA
	})
	delivered := entryRow.ReviewedSHA
	movedHead := commitOneMore(t, svc.repoDir, "after-entry.txt", "commit while QA reviews")

	findings := []model.RejectFinding{
		{CriterionID: "AC1", Detail: "does not hold", Evidence: "go test output"},
		{CriterionID: "AC2", Detail: "also broken"},
	}
	if _, err := svc.SpecReject(ctx, model.SpecRejectRequest{
		ID: spec.ID, Reason: "review found issues", By: "qa-agent", Findings: findings,
	}); err != nil {
		t.Fatalf("SpecReject: %v", err)
	}

	history, err = svc.SpecHistory(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecHistory after reject: %v", err)
	}
	last := history[len(history)-1]

	wantPrefix := renderRejectReason("review found issues", findings)
	if !strings.HasPrefix(last.Reason, wantPrefix) {
		t.Fatalf("reason does not start with SPEC-156's own composed prefix:\nwant prefix=%q\ngot=%q", wantPrefix, last.Reason)
	}
	rest := last.Reason[len(wantPrefix):]
	wantNote := "\n\nfrontera: la frontera avanza solo hasta el extremo entregado " + delivered[:frontierSHAAbbrevLen] +
		"; al emitir el informe el HEAD era " + movedHead[:frontierSHAAbbrevLen] +
		". Lo que llego despues NO se reviso y entra en el rango de la siguiente pasada."
	if rest != wantNote {
		t.Errorf("suffix after SPEC-156's prefix =\n%q\nwant\n%q", rest, wantNote)
	}
}

// --- Pure function tables ---

func TestAppendFrontierNote_Table(t *testing.T) {
	tests := []struct {
		name, reason, note, want string
	}{
		{"empty note leaves reason untouched", "some reason", "", "some reason"},
		{"empty note and empty reason stay empty", "", "", ""},
		{"empty reason, non-empty note", "", "moved on", "frontera: moved on"},
		{"non-empty reason and note", "human reason", "moved on", "human reason\n\nfrontera: moved on"},
		{"idempotent: already carries this exact note", "human reason\n\nfrontera: moved on", "moved on", "human reason\n\nfrontera: moved on"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := appendFrontierNote(tc.reason, tc.note)
			if got != tc.want {
				t.Errorf("appendFrontierNote(%q, %q) = %q, want %q", tc.reason, tc.note, got, tc.want)
			}
		})
	}
}

func TestFrontierNoteOf_Table(t *testing.T) {
	tests := []struct {
		name, reason, want string
	}{
		{"no note present", "just a reason", ""},
		{"note is the whole reason", "frontera: moved on", "moved on"},
		{"note appended after a reason", "human reason\n\nfrontera: moved on", "moved on"},
		{"note appended after SPEC-156 findings", "rejected: x\n\n[AC1] y\n\nfrontera: moved on", "moved on"},
		{"empty reason", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FrontierNoteOf(tc.reason)
			if got != tc.want {
				t.Errorf("FrontierNoteOf(%q) = %q, want %q", tc.reason, got, tc.want)
			}
		})
	}
}

// TestLatestHistoryRow_UsesParsedTimeNotSliceOrder proves latestHistoryRow
// picks the row with the LATEST parsed instant — never simply "the last one
// in the slice satisfying pred" (P2 of plan.md, guarding against exactly
// the class of bug GetSpecHistory's own godoc documents for `at` as
// RFC3339Nano text: it is not lexicographically chronological). The later
// instant is placed FIRST in the slice specifically to catch an
// implementation that trusted slice order instead of comparing At.
func TestLatestHistoryRow_UsesParsedTimeNotSliceOrder(t *testing.T) {
	earlier := mustParseRFC3339(t, "2026-09-24T10:00:00Z")
	later := mustParseRFC3339(t, "2026-09-24T10:00:05Z")

	rows := []*model.SpecHistory{
		{ID: "row-later-but-first-in-slice", FromStatus: model.SpecStatusImplementing, ToStatus: model.SpecStatusQA, At: later},
		{ID: "row-earlier-but-last-in-slice", FromStatus: model.SpecStatusImplementing, ToStatus: model.SpecStatusQA, At: earlier},
	}
	got := latestHistoryRow(rows, func(h *model.SpecHistory) bool { return true })
	if got == nil || got.ID != "row-later-but-first-in-slice" {
		t.Errorf("latestHistoryRow = %+v, want the row with the LATER instant regardless of slice position", got)
	}
}

// TestLatestHistoryRow_TieBreaksByID covers the deterministic tie-break
// (P2): two rows sharing the EXACT same instant (the case GetSpecHistory's
// own `at ASC, id ASC` ordering exists for) resolve by ID, never by chance.
func TestLatestHistoryRow_TieBreaksByID(t *testing.T) {
	shared := mustParseRFC3339(t, "2026-09-24T10:00:00Z")
	rows := []*model.SpecHistory{
		{ID: "01a04600-0000-7000-8000-000000000001", FromStatus: model.SpecStatusImplementing, ToStatus: model.SpecStatusQA, At: shared},
		{ID: "01a04600-0000-7000-8000-000000000002", FromStatus: model.SpecStatusImplementing, ToStatus: model.SpecStatusQA, At: shared},
	}
	got := latestHistoryRow(rows, func(h *model.SpecHistory) bool { return true })
	if got == nil || got.ID != "01a04600-0000-7000-8000-000000000002" {
		t.Errorf("latestHistoryRow = %+v, want the row with the greater ID (the deterministic tie-break)", got)
	}
}
