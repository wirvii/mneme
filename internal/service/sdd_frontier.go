// Package service — sdd_frontier.go is SPEC-157's home for the review
// frontier: how far a QA pass was asked to look (the DELIVERED end,
// recorded when a spec enters qa) and how far a QA pass actually reached
// (the REVIEWED frontier, COPIED — never recomputed from HEAD — from the
// delivered end when the pass leaves qa). See
// model.SpecHistory.ReviewedSHA for the full two-meanings-one-column
// contract this file implements, and D2/D2b/D2c of spec.md for the design.
//
// Every function that touches git or the store here returns a VALUE,
// never an error (D2's "never blocks" restriction — spec.md AC9): a
// repository dir that is not configured, a git command that fails, a
// history read that errors — none of these stop a spec's transition. They
// produce an empty SHA and/or a Note explaining why, exactly like
// captureBaseSHA (sdd.go) already does for base_sha. The I/O this file
// performs from inside updateSpecStatus (sdd_export.go) is best-effort in
// the same sense materializeSpec already is.
package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
)

// frontierSHAAbbrevLen is how many leading hex characters of a commit SHA
// a frontier note shows a human — long enough to be practically unique in
// conversation, short enough to read (P6 of plan.md).
const frontierSHAAbbrevLen = 8

// frontierNoteLabel prefixes the frontier note appendFrontierNote adds to
// a spec_history row's reason (D2d). Never used bare — always through
// appendFrontierNote/FrontierNoteOf.
const frontierNoteLabel = "frontera: "

// frontierNoteSeparator is the blank line appendFrontierNote inserts
// between whatever reason text already exists and its own note — the same
// separation SPEC-156's renderRejectReason (sdd_contract.go) uses between
// the human reason and its finding blocks, so a rejection carrying both
// reads as one document with clearly delimited sections in a fixed order:
// human reason → findings (SPEC-156) → frontera (SPEC-157, D2d/AC18).
const frontierNoteSeparator = "\n\n"

// frontierDecision is what frontierForTransition resolves for ONE status
// transition: the value to persist in spec_history.reviewed_sha (SHA) and,
// when there is something worth declaring, the sentence appendFrontierNote
// adds to the transition's reason (Note).
type frontierDecision struct {
	// SHA is what updateSpecStatus (sdd_export.go) passes through to
	// store.UpdateSpecStatus's reviewedSHA parameter for this transition's
	// row. Empty for every transition outside D2's closed three-member
	// set, and for the two review-bearing transitions when nothing could
	// be resolved (D2c's third case, or entering qa with no usable HEAD).
	SHA string

	// Note, when non-empty, is appended to the transition's reason via
	// appendFrontierNote. Empty — the common case — means nothing needs
	// declaring, which is what keeps AC6/spec.md AC6's byte-identity
	// promise: a reason with no note appended reads exactly as it would
	// have before this spec existed.
	Note string
}

// frontierForTransition resolves SPEC-157's closed three-transition set
// (D2/D2b/D2c): what a status change from `from` to `to` should record
// about the review frontier. Every other transition — including
// qa → needs_grill (a pushback, not a review verdict), done → implementing
// (a post-hoc reject, not a fresh review pass), and the trivial lane's
// audit pair (out of scope, §7 of spec.md) — returns the zero
// frontierDecision immediately, with NO I/O at all: the fast path for the
// overwhelming majority of calls this wrapper makes.
func (svc *SDDService) frontierForTransition(
	ctx context.Context, specID string, from, to model.SpecStatus,
) frontierDecision {
	switch {
	case from == model.SpecStatusImplementing && to == model.SpecStatusQA:
		return svc.frontierOnEnterQA()
	case from == model.SpecStatusQA && (to == model.SpecStatusDone || to == model.SpecStatusImplementing):
		return svc.frontierOnLeaveQA(ctx, specID)
	default:
		return frontierDecision{}
	}
}

// frontierOnEnterQA implements D2's first row: implementing → qa records
// HEAD as the DELIVERED end — how far the qa-tester is being asked to
// look. Never an error: an unconfigured repoDir or a failing git command
// produce an empty SHA and a Note naming why, and the transition still
// commits (D2, "nunca bloquea").
func (svc *SDDService) frontierOnEnterQA() frontierDecision {
	repoDir := svc.repoDir
	if repoDir == "" {
		return frontierDecision{
			Note: "no se registra extremo entregado: directorio del repositorio sin configurar",
		}
	}

	g := &quality.Git{RepoDir: repoDir}
	sha, err := g.HeadSHA()
	if err != nil {
		return frontierDecision{Note: fmt.Sprintf("no se registra extremo entregado: %v", err)}
	}
	return frontierDecision{SHA: sha}
}

// frontierOnLeaveQA implements D2c: qa → done and qa → implementing COPY
// the delivered end of the CURRENT entry into qa — never HEAD at the
// moment the informe is emitted. This is what makes it structurally
// impossible for the frontier to claim more than what was actually handed
// to review, even if the implementer committed again while QA was
// reading (D2b's whole reason for existing).
//
// Three cases (D2c's table):
//  1. the current qa entry left no delivered end (no implementing → qa
//     row, or one whose ReviewedSHA is empty) → SHA stays "", Note
//     explains why. Ambiguity resolves toward NOT recording a frontier:
//     the next pass simply covers more (from base_sha), which costs time
//     but never claims a review that did not happen (D4's principle).
//  2. the delivered end is found and HEAD (read now, ONLY to compare)
//     equals it → SHA = delivered end, Note stays "" — the reason ends
//     up byte-identical to what it would be without this spec (AC6).
//  3. the delivered end is found but HEAD has moved (or could not be
//     read) → SHA is STILL the delivered end (never HEAD), and Note
//     declares what happened.
func (svc *SDDService) frontierOnLeaveQA(ctx context.Context, specID string) frontierDecision {
	history, err := svc.store.GetSpecHistory(ctx, specID)
	if err != nil {
		return frontierDecision{Note: fmt.Sprintf("no se registra frontera: %v", err)}
	}

	entry := latestHistoryRow(history, func(h *model.SpecHistory) bool {
		return h.FromStatus == model.SpecStatusImplementing && h.ToStatus == model.SpecStatusQA
	})
	if entry == nil || entry.ReviewedSHA == "" {
		return frontierDecision{
			Note: "no se registra frontera: la entrada a revision no dejo extremo entregado, " +
				"asi que la siguiente pasada cubre desde el commit base.",
		}
	}

	delivered := entry.ReviewedSHA
	decision := frontierDecision{SHA: delivered}

	repoDir := svc.repoDir
	if repoDir == "" {
		decision.Note = "no se pudo comprobar si el HEAD se movio durante la revision: " +
			"directorio del repositorio sin configurar"
		return decision
	}

	g := &quality.Git{RepoDir: repoDir}
	head, err := g.HeadSHA()
	if err != nil {
		decision.Note = fmt.Sprintf("no se pudo comprobar si el HEAD se movio durante la revision: %v", err)
		return decision
	}
	if head == delivered {
		return decision // Note stays "" — case 2, byte-identical reason.
	}

	decision.Note = fmt.Sprintf(
		"la frontera avanza solo hasta el extremo entregado %s; al emitir el informe el HEAD era %s. "+
			"Lo que llego despues NO se reviso y entra en el rango de la siguiente pasada.",
		abbreviateSHA(delivered), abbreviateSHA(head),
	)
	return decision
}

// abbreviateSHA truncates sha to frontierSHAAbbrevLen characters for
// human-facing notes — never used for comparison or persistence, only for
// what a person reads.
func abbreviateSHA(sha string) string {
	if len(sha) <= frontierSHAAbbrevLen {
		return sha
	}
	return sha[:frontierSHAAbbrevLen]
}

// appendFrontierNote appends note to reason using frontierNoteLabel and
// frontierNoteSeparator (D2d). Pure and idempotent:
//   - note == "" returns reason UNCHANGED, byte for byte — this is what
//     keeps AC6/spec.md AC6's promise that a transition with nothing to
//     declare produces exactly the reason it would have produced before
//     this spec existed.
//   - reason == "" (note non-empty) returns just the note block, with no
//     leading separator.
//   - otherwise appends frontierNoteSeparator + the note block — but
//     never twice: if reason already ends in that exact block, it is
//     returned unchanged (protects a caller that might resolve the same
//     transition's note more than once from ever duplicating it).
func appendFrontierNote(reason, note string) string {
	if note == "" {
		return reason
	}
	block := frontierNoteLabel + note
	if reason == "" {
		return block
	}
	if strings.HasSuffix(reason, frontierNoteSeparator+block) {
		return reason
	}
	return reason + frontierNoteSeparator + block
}

// FrontierNoteOf extracts the frontier note appendFrontierNote most
// recently appended to reason, "" when none is present. Exported for the
// CLI (P5 of plan.md): the note is read back from the PERSISTED reason,
// never recomputed — the persisted spec_history row is the single source
// of truth for what was actually declared.
func FrontierNoteOf(reason string) string {
	sep := frontierNoteSeparator + frontierNoteLabel
	if idx := strings.LastIndex(reason, sep); idx != -1 {
		return reason[idx+len(sep):]
	}
	// The note IS the entire reason — appendFrontierNote's reason=="" case.
	if strings.HasPrefix(reason, frontierNoteLabel) {
		return reason[len(frontierNoteLabel):]
	}
	return ""
}

// latestHistoryRow returns the row in history matching pred with the
// LATEST (At, ID) pair — never simply the last row satisfying pred in
// slice order (P2 of plan.md). GetSpecHistory orders by `at` as a
// time.RFC3339Nano TEXT column, which is NOT lexicographically
// chronological (Format trims trailing zeros from the fractional second —
// the same defect SPEC-110 D21 found elsewhere), so two rows sharing an
// `at` string need an independent, parsed-time comparison; ID (a UUIDv7,
// monotonic by insertion) is the deterministic tie-break. Returns nil when
// nothing matches pred.
func latestHistoryRow(history []*model.SpecHistory, pred func(*model.SpecHistory) bool) *model.SpecHistory {
	var latest *model.SpecHistory
	for _, h := range history {
		if !pred(h) {
			continue
		}
		if latest == nil || h.At.After(latest.At) || (h.At.Equal(latest.At) && h.ID > latest.ID) {
			latest = h
		}
	}
	return latest
}
