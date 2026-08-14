package quality

import "strings"

// RequiresSignature reports whether kind is an ATESTACIÓN — a row the
// qa-tester's `quality sign` verb accepts because attesting to it is a
// technical verification a human must perform by reading code (SPEC-117
// S3 D11's own criterion rows, extended by SPEC-119 S5 D8 to cover a
// `mutant` survivor's equivalence claim) — as opposed to an ABSOLUCIÓN,
// which `quality ack` accepts because it is a governance decision an
// orchestrator channels on a human's behalf ("I approve this despite it
// being a problem").
//
// This is the SINGLE predicate that makes Sign's and Ack's domains
// complementary BY CONSTRUCTION (D8): before this function existed, the
// two verbs each carried their own, independently-written condition
// (Sign required a "criterion" prefix; Ack required its absence) — two
// symmetric assertions that happened to agree today but that nothing
// forced to keep agreeing. Sign now accepts iff RequiresSignature(kind);
// Ack accepts iff !RequiresSignature(kind). The same function, negated,
// can never drift apart from itself.
//
// A criterion* row is an atestación because SPEC-117 S3 designed it that
// way (a criterion is a structured claim about the code a human verifies
// against the tree). A `mutant` survivor row is an atestación for the
// same underlying reason, extended to a new subject: declaring a
// survivor "semantically equivalent" is not an approval of a known
// problem — it is a claim, checkable by reading the mutated line and the
// tests around it, that the mutation changed nothing a correct program
// could observe. Everything else this mechanism emits today (`tree`,
// `constitution`, `gate`, `coverage`, `ratchet`, `criteria`, `mutation`)
// is an absolución: a human's call on whether an already-observed fact
// is acceptable, never a technical re-verification of it.
func RequiresSignature(kind string) bool {
	return kind == "mutant" || strings.HasPrefix(kind, "criterion")
}
