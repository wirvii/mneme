package quality

// Verdict is the certificate's overall outcome, derived from its checks —
// never a field a caller sets directly (D10).
type Verdict string

const (
	// VerdictPass means every check passed (acked/skipped included).
	VerdictPass Verdict = "pass"

	// VerdictFail means at least one check failed.
	VerdictFail Verdict = "fail"

	// VerdictFindings means no check failed, but at least one finding has
	// not yet been acknowledged by a human (D9/D10).
	VerdictFindings Verdict = "findings"
)

// CheckStatus is the outcome of a single quality_checks row.
type CheckStatus string

const (
	// CheckStatusPass means the check succeeded outright.
	CheckStatusPass CheckStatus = "pass"

	// CheckStatusFail means the check failed and degrades the verdict to
	// VerdictFail regardless of any other row.
	CheckStatusFail CheckStatus = "fail"

	// CheckStatusSkipped means an earlier required gate already failed and
	// this one was never run (D6). Present in the record, never omitted.
	CheckStatusSkipped CheckStatus = "skipped"

	// CheckStatusFinding means the check surfaced something that needs a
	// human's justification before it stops degrading the verdict (D9/D10).
	CheckStatusFinding CheckStatus = "finding"

	// CheckStatusAcked means a human reviewed a former finding and
	// justified it via Ack — it no longer degrades the verdict.
	CheckStatusAcked CheckStatus = "acked"
)

// CheckResult is the minimal shape DeriveVerdict needs from a quality_checks
// row: just enough to decide the certificate's overall Verdict, independent
// of storage concerns.
type CheckResult struct {
	Status CheckStatus

	// Effect is SPEC-137 D4's closed vocabulary value for this row —
	// whether it counts toward the verdict at all. The zero value ("")
	// never counts (Effect("").CountsTowardVerdict() is false), which is
	// deliberate: a caller that forgets to populate this field gets a row
	// that is silently EXCLUDED from the verdict, never one that silently
	// blocks it — the safer failure direction for a field this new.
	Effect Effect
}

// DeriveVerdict computes the certificate's Verdict from its checks, in the
// order D10 fixes, counting ONLY rows whose Effect.CountsTowardVerdict() is
// true (SPEC-137 D4 — a "measures"/"absent"/"stopped" row can never tumble
// or degrade a certificate, whatever its Status says): any "fail" among the
// counted rows wins outright; otherwise any un-acked "finding" among them
// degrades to "findings"; otherwise "pass". "acked" and "skipped" never
// degrade anything even when counted — that is the whole point of acking a
// finding.
//
// An EMPTY set of checks is "fail" (AC7): a certificate that verified
// nothing at all is not a green certificate, it is an absence of evidence,
// and treating absence as success is exactly the dishonest "green report"
// this mechanism exists to eliminate. This rule is checked against the
// TOTAL slice length, never against how many rows counted — a certificate
// whose only declared mechanisms are measure-only ones (SPEC-137 D9) is a
// real, non-empty set of checks and must NOT be forced to "fail" just
// because none of them count; D13 explicitly rejected requiring a minimum
// of configuration to leave "pass" reachable.
func DeriveVerdict(checks []CheckResult) Verdict {
	if len(checks) == 0 {
		return VerdictFail
	}

	hasFinding := false
	for _, c := range checks {
		if !c.Effect.CountsTowardVerdict() {
			continue
		}
		if c.Status == CheckStatusFail {
			return VerdictFail
		}
		if c.Status == CheckStatusFinding {
			hasFinding = true
		}
	}
	if hasFinding {
		return VerdictFindings
	}
	return VerdictPass
}

// Reason names WHICH conjunction of CertificateUsable failed, so a caller
// (SpecAdvance's ensureCertified) can surface a distinct sentinel and a
// distinct remedy per D12 — each cause is fixed differently.
type Reason int

const (
	// ReasonUsable means every condition held: the certificate is usable.
	ReasonUsable Reason = iota

	// ReasonNotGreen means the certificate's verdict is not "pass".
	ReasonNotGreen

	// ReasonStale means the certificate's head_sha no longer matches HEAD.
	ReasonStale

	// ReasonConstitutionChanged means the constitution's hash no longer
	// matches the certificate's recorded hash.
	ReasonConstitutionChanged

	// ReasonWorktreeDirty means the worktree currently has uncommitted
	// changes, even if it was clean at certification time.
	ReasonWorktreeDirty
)

// CertificateUsable evaluates the D12 conjunction — verdict is "pass", the
// certificate's head_sha matches the current HEAD, its constitution_hash
// matches the constitution's current hash, and the worktree is clean right
// now — and reports the first condition that fails, in the order D12 lists
// them. A pure function: no I/O, no git, no store — every value it needs is
// passed in by the caller, which is what makes it independently testable and
// what AC27 mutation (1) removes and restores (the head_sha comparison).
func CertificateUsable(verdict Verdict, certHeadSHA, currentHeadSHA, certConstitutionHash, currentConstitutionHash string, worktreeDirty bool) (bool, Reason) {
	if verdict != VerdictPass {
		return false, ReasonNotGreen
	}
	if certHeadSHA != currentHeadSHA {
		return false, ReasonStale
	}
	if certConstitutionHash != currentConstitutionHash {
		return false, ReasonConstitutionChanged
	}
	if worktreeDirty {
		return false, ReasonWorktreeDirty
	}
	return true, ReasonUsable
}
