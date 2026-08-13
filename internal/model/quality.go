// Package model — this file defines the quality certificate types (SPEC-115
// EPIC-calidad S1): the durable, SHA-bound evidence that a spec's build and
// test gates actually ran and passed, replacing an agent's self-reported
// "it works" with a result mneme produced by executing something.
//
// model has zero external dependencies (the leaf of leaves) and does not
// import internal/quality — the two packages independently declare their
// own Verdict-shaped string enum, and the service layer translates between
// them, exactly as it already does for internal/lane and internal/conflicts.
package model

import "time"

// QualityVerdict is the certificate's overall outcome — model's own copy of
// quality.Verdict's three values, kept as a plain string type since model
// cannot import internal/quality (zero external deps, the leaf of leaves).
type QualityVerdict string

const (
	// QualityVerdictPass means every check on the certificate passed.
	QualityVerdictPass QualityVerdict = "pass"

	// QualityVerdictFail means at least one check failed.
	QualityVerdictFail QualityVerdict = "fail"

	// QualityVerdictFindings means no check failed, but at least one
	// finding has not yet been acknowledged by a human.
	QualityVerdictFindings QualityVerdict = "findings"
)

// QualityCertificate is the durable record of a single quality verification
// run, bound to an exact commit (SPEC-115 D4/D5). One row per `quality
// verify` invocation — both green and red runs are kept, so a stale or
// failed attempt is never silently lost.
type QualityCertificate struct {
	// ID is a UUIDv7 assigned at insert time.
	ID string `json:"id"`

	// Project is the project slug this certificate belongs to.
	Project string `json:"project"`

	// SpecID references the spec this certificate was verified for.
	SpecID string `json:"spec_id"`

	// HeadSHA is the git HEAD commit this certificate was produced against.
	// A certificate is usable only while HeadSHA still matches HEAD (D12).
	HeadSHA string `json:"head_sha"`

	// BaseSHA is the spec's base_sha at verification time (empty when the
	// spec has none), used for the constitution's unchanged-in-range check.
	BaseSHA string `json:"base_sha,omitempty"`

	// ConstitutionHash is the sha256 hex digest of the exact
	// .mneme/quality.toml bytes used for this run (D9) — a certificate is
	// usable only while this still matches the constitution's current hash.
	ConstitutionHash string `json:"constitution_hash"`

	// SchemaVersion is the constitution's schema_version at verification time.
	SchemaVersion int `json:"schema_version"`

	// Verdict is derived from this certificate's checks (D10) — never set
	// directly by a caller.
	Verdict QualityVerdict `json:"verdict"`

	// Dirty is true when the worktree had uncommitted changes at
	// verification time (D8) — always fails the certificate outright.
	Dirty bool `json:"dirty"`

	// MnemeVersion is the mneme binary version that produced this certificate.
	MnemeVersion string `json:"mneme_version,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	DurationMs int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

// QualityCheck is one row of evidence within a certificate: the dirty-tree
// check, one of the three constitution checks, or a gate run today (kind
// "tree"/"constitution"/"gate") — S2..S6 add further kinds without any
// schema change (D16). The certificate's Verdict is derived from these rows,
// never stored independently of them.
type QualityCheck struct {
	// ID is the auto-incremented row identifier assigned by SQLite.
	ID int64 `json:"id"`

	// CertificateID references the parent QualityCertificate.
	CertificateID string `json:"certificate_id"`

	// Seq is the 1-based position within the certificate's checks, in
	// execution order.
	Seq int `json:"seq"`

	// Kind identifies the check's category: "tree", "constitution", "gate"
	// today; S2..S6 add "coverage", "criterion", "budget", "mutant",
	// "visual", "lane-scope" (D16) — an open vocabulary, never a closed enum.
	Kind string `json:"kind"`

	// Name identifies the specific check within its Kind (e.g.
	// "clean-worktree", "tracked", "unchanged-in-range", "hash", or a gate's
	// own declared name).
	Name string `json:"name"`

	// Status is one of "pass", "fail", "skipped", "finding", "acked".
	Status string `json:"status"`

	// ExitCode is the gate's process exit code, or 0 for non-gate checks.
	ExitCode int `json:"exit_code,omitempty"`

	DurationMs int64 `json:"duration_ms,omitempty"`

	// OutputSHA256 is the sha256 hex digest of a gate's COMPLETE combined
	// stdout+stderr stream (D6) — empty for non-gate checks.
	OutputSHA256 string `json:"output_sha256,omitempty"`

	// OutputBytes is the total length of a gate's combined output, which may
	// be far larger than len(OutputTail).
	OutputBytes int64 `json:"output_bytes,omitempty"`

	// OutputTail is the last execution.output_tail_bytes of a gate's output.
	OutputTail string `json:"output_tail,omitempty"`

	// Summary is a short, human-readable note (dirty paths, a timeout
	// duration, the constitution's before/after hashes, a lane-audit
	// breach list).
	Summary string `json:"summary,omitempty"`

	// Detail is an open JSON payload for kind-specific structured data
	// (S2..S6 use this without a schema change, D16).
	Detail string `json:"detail,omitempty"`

	// AckedBy, AckedAt, and Justification are populated only once a
	// "finding" has been turned into "acked" via Ack (D10/D11) — a human
	// approval, never the author of the change under review.
	AckedBy       string     `json:"acked_by,omitempty"`
	AckedAt       *time.Time `json:"acked_at,omitempty"`
	Justification string     `json:"justification,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// QualityVerifyRequest is the input for `mneme quality verify` /
// quality_verify: run every declared gate for id's spec and emit (or deny) a
// certificate bound to the current commit.
type QualityVerifyRequest struct {
	// ID is the spec to verify. Must be in implementing or qa status
	// (ErrInvalidTransition otherwise, D5 — qa admits recertification when
	// HEAD moved during QA).
	ID string `json:"id"`
}

// QualityStatusRequest is the input for `mneme quality status` /
// quality_status. ID is optional: omitted, it reports only the
// constitution's own state (path, hash, enabled, declared gates); supplied,
// it also reports the spec's latest certificate and checks.
type QualityStatusRequest struct {
	ID string `json:"id,omitempty"`
}

// QualityStatusResponse is returned by quality_status.
type QualityStatusResponse struct {
	// Enabled is the constitution's current on/off switch. False both when
	// the file is absent and when it is present with enabled=false (D3) —
	// Exists distinguishes the two.
	Enabled bool `json:"enabled"`

	// Exists reports whether .mneme/quality.toml is present at all.
	Exists bool `json:"exists"`

	// Path is the constitution's path relative to the repository root.
	Path string `json:"path,omitempty"`

	// ConstitutionHash is the sha256 hex digest of the current constitution
	// bytes. Empty when Exists is false.
	ConstitutionHash string `json:"constitution_hash,omitempty"`

	// GateNames lists the declared gates' names, in declared order. Empty
	// when Exists is false or the constitution declares no gates.
	GateNames []string `json:"gate_names,omitempty"`

	// Note is a short, human-readable summary of the mechanism's state —
	// always populated, so a caller never has to reconstruct one from the
	// other fields (AC24: "apagado" prints something a human reads, not a
	// silent zero value).
	Note string `json:"note"`

	// LatestCertificate is the most recent certificate for the requested
	// spec, when ID was supplied and one exists.
	LatestCertificate *QualityCertificate `json:"latest_certificate,omitempty"`

	// Checks lists LatestCertificate's checks, in seq order.
	Checks []*QualityCheck `json:"checks,omitempty"`

	// Baseline reports the ratchet's registered baseline (SPEC-116 D10),
	// when the file exists. Nil is the common state before the repository's
	// first `mneme quality baseline update` — quality_status never invents
	// one. Reading and reporting only: computing Baseline never executes
	// anything (AC29).
	Baseline *QualityBaselineInfo `json:"baseline,omitempty"`
}

// QualityBaselineInfo is quality_status's read-only projection of the
// registered ratchet baseline (SPEC-116 D10) — path, provenance, and its
// own recorded percentage, plus how stale it is against the LATEST
// certificate's own measurement, when one with a comparable coverage
// profile is available. model does not import internal/quality (the leaf
// of leaves, zero external deps) — the service layer translates, exactly
// as it already does for QualityVerdict.
type QualityBaselineInfo struct {
	// Path is BaselineRelPath, repeated here so a caller never has to know
	// the constant.
	Path string `json:"path"`

	MeasuredAtSHA string    `json:"measured_at_sha"`
	MeasuredAt    time.Time `json:"measured_at"`
	GlobalLinePct float64   `json:"global_line_pct"`

	// StalenessKnown reports whether StalenessPct/Stale below were
	// actually computed against a recent measurement — false when no
	// certificate with a usable coverage/profile detail exists yet, in
	// which case StalenessPct/Stale are both the zero value and must not
	// be read as "not stale".
	StalenessKnown bool `json:"staleness_known"`

	// StalenessPct is how far the latest known measurement exceeds
	// GlobalLinePct, in percentage points (0 when at or below the mark —
	// D17's CompareStaleness never returns a negative value).
	StalenessPct float64 `json:"staleness_pct,omitempty"`

	// Stale reports whether StalenessPct exceeds the constitution's
	// declared ratchet.max_baseline_staleness_pct (D17).
	Stale bool `json:"stale,omitempty"`
}

// QualityAckRequest is the input for `mneme quality ack` / quality_ack: a
// human's justified approval of a finding, converting it to "acked" without
// re-running anything (D10/D11).
type QualityAckRequest struct {
	// CertificateID is the certificate the finding belongs to.
	CertificateID string `json:"cert_id"`

	// Seq is the finding's position within the certificate's checks.
	Seq int `json:"seq"`

	// By identifies who is acknowledging the finding — never the author of
	// the change under review (D11): the orchestrator channels this on the
	// human's behalf, and mcp__mneme__quality_ack is denied to subagents.
	By string `json:"by"`

	// Justification documents WHY the finding is acceptable. Required and
	// non-empty (ErrReasonRequired).
	Justification string `json:"justification"`
}

// QualityBaselineUpdateRequest is the input for `mneme quality baseline
// update` (SPEC-116 D10/D15) — CLI-only, deliberately NOT exposed over MCP
// (D15): writing the ratchet's baseline is an act of governance over a
// versioned file, the same class of act as hand-editing
// `.mneme/quality.toml`, which also has no MCP tool. Empty today —
// BaselineUpdate always reads the PROJECT's own latest `pass` certificate;
// there is nothing for a caller to parameterize yet, and the type exists so
// a future field (e.g. a specific certificate ID) never has to change a
// call signature.
type QualityBaselineUpdateRequest struct{}
