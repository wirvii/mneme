// Package quality — this file implements the closed five-value vocabulary
// that says whether a single evaluated row counts toward the certificate's
// verdict (SPEC-137 etapa 1 de BL-221, D4). Before this file existed, every
// row's participation was implicit in DeriveVerdict's own switch over
// Status — a mutation or visual row that merely MEASURED something could
// still tumble a certificate the same way a broken build could, which is
// exactly the wall this spec exists to remove.
package quality

import (
	"fmt"
	"strings"
)

// Effect is the closed vocabulary a row's Effect field holds. It answers
// one question only: does this row's outcome count toward DeriveVerdict?
type Effect string

const (
	// EffectBlocks means a "fail" here tumbles the certificate outright,
	// and an un-acked "finding" degrades it until a human closes it — the
	// historical behaviour of every row this mechanism has ever emitted.
	EffectBlocks Effect = "blocks"

	// EffectSignable means the row can never be "fail": an adverse result
	// is a "finding" a human absolves (ack) or attests (sign) — coverage's
	// new home (D2/D3).
	EffectSignable Effect = "signable"

	// EffectMeasures means the row ran and produced a real measurement,
	// and that measurement never changes the verdict, whatever it says
	// (mutation, budget, ratchet, visual — D5/D8).
	EffectMeasures Effect = "measures"

	// EffectAbsent means the row never ran because nothing was declared
	// for it — there was no constitution section to run, or no base to
	// compare against.
	EffectAbsent Effect = "absent"

	// EffectStopped means the row was declared but never evaluated because
	// an earlier REQUIRED gate already failed (the gatesStopped cascade).
	EffectStopped Effect = "stopped"
)

// CountsTowardVerdict reports whether e is one of the two effects
// DeriveVerdict counts (blocks/signable) — the single predicate both
// DeriveVerdict (Go) and AckCheck's SQL filter (SPEC-137 §7's "the two
// implementations move together or not at all") derive their behaviour
// from, so the two can never independently drift on which values count.
func (e Effect) CountsTowardVerdict() bool {
	return e == EffectBlocks || e == EffectSignable
}

// ErrUnknownCheckKind is returned by EffectForKind when kind is not one of
// the kinds this mechanism is known to emit — a deliberate loud failure
// instead of a silent default, since a silent default is exactly the mode
// of failure an overlooked emitter would produce (D4's "central sweep").
var ErrUnknownCheckKind = fmt.Errorf("quality: unknown check kind")

// evaluatedEffectByKind is the CLOSED, non-configurable table mneme fixes
// for a row's effect once it is known to have been EVALUATED (never
// skipped) — SPEC-137 D4's own table, verbatim. There is no constitution
// key for this: the reparto is mneme's decision, not the project's.
//
// "coverage" resolves through EffectForKind's name-based special case
// below, not through this map, since all three coverage rows share one
// kind but must all resolve to "signable" regardless of name.
var evaluatedEffectByKind = map[string]Effect{
	"tree":         EffectBlocks,
	"constitution": EffectBlocks,
	"gate":         EffectBlocks,
	"criteria":     EffectBlocks,

	"coverage": EffectSignable,

	"ratchet":       EffectMeasures,
	"budget":        EffectMeasures,
	"detection":     EffectMeasures,
	"mutation":      EffectMeasures,
	"mutant":        EffectMeasures,
	"visual":        EffectMeasures,
	"visual-target": EffectMeasures,
}

// EffectForKind resolves the effect of an EVALUATED row (never a skipped
// one — those get their effect from their emitter's skip-reason function,
// not from this table) from its kind and, for a criterion*-prefixed kind,
// treats every such prefix as "criteria"'s own EffectBlocks — the same
// prefix convention RequiresSignature already uses for "criterion*" rows.
//
// Returns ErrUnknownCheckKind for any kind this table does not resolve —
// never a silent default — so a new emitter that forgets to route through
// here fails loudly (caught by AC9's two-sided comparison) instead of
// quietly falling into whichever effect happens to be the zero value.
func EffectForKind(kind string) (Effect, error) {
	if kind == "criteria" || hasCriterionPrefix(kind) {
		return EffectBlocks, nil
	}
	if e, ok := evaluatedEffectByKind[kind]; ok {
		return e, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownCheckKind, kind)
}

// hasCriterionPrefix mirrors RequiresSignature's own "criterion" prefix
// check (signature.go) as a tiny private helper — both files live in the
// same package, so this could call RequiresSignature directly, but that
// predicate also accepts "mutant" (an unrelated kind for THIS table), so a
// dedicated check keeps the two rules from silently coupling.
func hasCriterionPrefix(kind string) bool {
	return strings.HasPrefix(kind, "criterion")
}
