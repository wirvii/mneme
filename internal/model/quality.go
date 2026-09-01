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
	// spec has none) — used by coverage's diff-lines row, the ratchet, the
	// executable criteria, the budget, and mutation's own diff scoping.
	// (Until SPEC-137, this godoc named only the constitution's own
	// unchanged-in-range check, the sole consumer when this field was
	// introduced; that check no longer exists, and every remaining
	// consumer of BaseSHA predates this correction.)
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

	// Evidence is SPEC-137 D6's "de que es evidencia este certificado"
	// sentence, computed once by the leaf's pure Evidence function and
	// persisted here at emission time — never re-derived when this
	// certificate is later read. Empty for every certificate emitted
	// before this field existed; the three rendering channels (verify,
	// status, the QA report) treat that emptiness as "certificado emitido
	// antes de esta version: sin linea de evidencia", never fabricating
	// one after the fact.
	Evidence string `json:"evidence,omitempty"`
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
	// "clean-worktree", "tracked", "hash", or a gate's own declared name).
	// (Until SPEC-137 D5, the constitution family also had a third row,
	// "unchanged-in-range" — removed entirely, not merely renamed.)
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

	// Effect is one of quality.Effect's five closed values
	// (blocks/signable/measures/absent/stopped, SPEC-137 D4), persisted at
	// emission time from the leaf's own resolution — never recomputed when
	// this row is later read. Defaults to "blocks" for every row inserted
	// before this field existed, which is exactly the historical behaviour
	// of every prior row: it counted toward the verdict, unconditionally.
	Effect string `json:"effect"`
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

	// Budget reports the spec's budget.toml state (SPEC-118 S4 D16): the
	// disk hash alongside the hash the LATEST certificate recorded, so a
	// divergence between the two is visible without ampliar
	// CertificateUsable's own conjunction (the window D16 documents, closed
	// with a row, not an argument). Nil when ID was not supplied, or no
	// budget.toml exists for it yet.
	Budget *QualityBudgetInfo `json:"budget,omitempty"`

	// Mutation reports the mutation mechanism's declared state and, when
	// req.ID was supplied and a certificate exists, its last certified
	// figures (SPEC-119 S5 D14) — read-only, never executes anything. Nil
	// when the constitution does not declare [mutation] at all (schema <
	// 5).
	Mutation *QualityMutationInfo `json:"mutation,omitempty"`

	// Visual reports the visual mechanism's declared state and, when
	// req.ID was supplied and a certificate exists, its last certified
	// figures (SPEC-120 S6 D14) — read-only, never executes anything. Nil
	// when the constitution does not declare [visual] at all (schema < 6).
	Visual *QualityVisualInfo `json:"visual,omitempty"`
}

// QualityVisualInfo is quality_status's read-only projection of the visual
// mechanism's declared configuration plus the latest certificate's own
// recorded figures (SPEC-120 D14) — mirrors QualityMutationInfo's/
// QualityBudgetInfo's shape for the same reason: a human running `mneme
// quality status` should see the declared format, how many targets are
// declared, and whether the nivel-2 comparison is switched on WITHOUT
// having to open .mneme/quality.toml, and the last certified figures
// WITHOUT re-parsing a visual report.
type QualityVisualInfo struct {
	// Format is the declared [visual].format ("visual-v1" today).
	Format string `json:"format"`

	// DeclaredTargets is len([visual].targets) — how many opaque objetivos
	// the project has committed to verifying (D3). mneme does not know, and
	// this field does not reveal, what any of them MEAN.
	DeclaredTargets int `json:"declared_targets"`

	// CompareEnabled mirrors [visual.compare].enabled — whether nivel 2
	// (pixel comparison against versioned references, D7) is switched on.
	CompareEnabled bool `json:"compare_enabled"`

	// ReferenceDir is [visual.compare].reference_dir, repeated here so a
	// caller never has to know the constitution's own key. Empty when
	// CompareEnabled is false.
	ReferenceDir string `json:"reference_dir,omitempty"`

	// VerifiedTargets is how many targets the LATEST certificate's report
	// actually verified (the report's own target count) — zero when no
	// certificate exists yet.
	VerifiedTargets int `json:"verified_targets"`

	// FailedTargets is the LATEST certificate's count of `visual-target`
	// rows in `fail` — zero when there is none, or when no certificate
	// exists yet.
	FailedTargets int `json:"failed_targets"`

	// MissingReferences is the LATEST certificate's count of objetivos
	// named by the `visual/compare` row's `reference-missing` finding —
	// zero when there is none, when nivel 2 is off, or when no certificate
	// exists yet.
	MissingReferences int `json:"missing_references"`
}

// QualityMutationInfo is quality_status's read-only projection of the
// mutation mechanism's declared configuration plus the latest
// certificate's own recorded figures (SPEC-119 D14) — mirrors
// QualityBudgetInfo's shape for the same reason: a human running `mneme
// quality status` should see the declared format/report_path/cupo
// WITHOUT having to open .mneme/quality.toml, and the last certified
// counts WITHOUT re-parsing a mutation report.
type QualityMutationInfo struct {
	// Format is the declared [mutation].format ("gremlins" | "mutants-v1").
	Format string `json:"format"`

	// ReportPath is [mutation].report_path, relative to the repository root.
	ReportPath string `json:"report_path"`

	// MaxEquivalent is the declared cupo (D9) — 0 means no escape hatch at
	// all, a legitimate, explicit choice.
	MaxEquivalent int `json:"max_equivalent"`

	// SignedEquivalent is how many `mutant` rows are currently `acked` on
	// the LATEST certificate — the count D9's own cupo is compared
	// against.
	SignedEquivalent int `json:"signed_equivalent"`

	// SurvivorCount is the LATEST certificate's total count of unsigned
	// `mutant` rows still in `finding` — zero when there is none, or when
	// no certificate exists yet.
	SurvivorCount int `json:"survivor_count"`

	// ByStatus is the LATEST certificate's full per-status mutant tally,
	// verbatim from mutation/score's own detail (D1 pata c's vocabulary) —
	// nil when no certificate has evaluated the mutation mechanism yet.
	ByStatus map[string]int `json:"by_status,omitempty"`
}

// QualityBudgetInfo is quality_status's read-only projection of a spec's
// declared budget (SPEC-118 D16) — the disk hash next to the certificate's
// own recorded hash, plus the last certified figures, when one exists.
type QualityBudgetInfo struct {
	// Path is budget.toml's path relative to the workflow directory.
	Path string `json:"path"`

	// DiskHash is the sha256 hex digest of budget.toml's CURRENT bytes on
	// disk.
	DiskHash string `json:"disk_hash"`

	// CertificateHash is the hash the latest certificate's budget/declared
	// row recorded — "" when no certificate has evaluated it yet. A
	// mismatch against DiskHash means the document changed since
	// certification (D16's own window, made visible here).
	CertificateHash string `json:"certificate_hash,omitempty"`

	// Margin, Budgeted, Delivered, Overrun are the last certified figures
	// (D3 of the grill) — zero when no certificate has evaluated the
	// budget yet.
	Margin    int `json:"margin"`
	Budgeted  int `json:"budgeted"`
	Delivered int `json:"delivered"`
	Overrun   int `json:"overrun"`
}

// LaneAuditResult is the trivial lane's audit outcome (SPEC-118 P11) — it
// replaces lane.AuditResult once internal/lane is deleted, with the EXACT
// SAME field names and (absent) JSON tags, so `mneme lane audit`'s and
// `lane_audit`'s serialised shape does not change by one byte (AC28):
// neither type declared a `json:"..."` tag, so Go's default
// field-name-verbatim encoding is what AC28's literal-string comparison
// pins.
type LaneAuditResult struct {
	// FileCount is the number of files changed in the diff.
	FileCount int

	// LinesChanged is the total added+removed lines across all changed files.
	LinesChanged int

	// OutOfScopeFiles lists files that fall outside the declared scope glob.
	OutOfScopeFiles []string

	// ForbiddenPaths lists files that match the forbidden-path patterns.
	ForbiddenPaths []string

	// PublicSymbolChanges lists exported symbols whose existence changed
	// (created or deleted) between base and HEAD, outside test files.
	PublicSymbolChanges []string

	// Breaches is the union of all threshold violations as human-readable
	// strings. An audit passes when len(Breaches)==0.
	Breaches []string

	// Passed is true when len(Breaches)==0.
	Passed bool
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
// `.mneme/quality.toml`, which also has no MCP tool.
type QualityBaselineUpdateRequest struct {
	// ID is the spec whose latest certificate BaselineUpdate reads from —
	// the same per-spec certificate lookup `quality verify`/`quality
	// status` already use (GetLatestCertificate), so this needs no new
	// store query. Required.
	ID string `json:"id"`
}

// QualitySignRequest is the input for `mneme quality sign` / quality_sign
// (SPEC-117 S3 D11): a qa-tester's ATTESTATION that one criterion row
// genuinely holds — a verb distinct from Ack (an ABSOLUTION), reusing
// AckCheck's mechanism (store.AckCheck is untouched) but never its verb,
// so a COUNT(*) never confuses "we forgave 3 findings" with "we verified 4
// manuals".
type QualitySignRequest struct {
	// CertificateID is the certificate the criterion row belongs to.
	CertificateID string `json:"cert_id"`

	// Seq is the attested row's position within the certificate's checks.
	// Sign accepts a row iff quality.RequiresSignature(kind) — a criterion
	// row (SPEC-117) or a `mutant` survivor row (SPEC-119 D8) —
	// ErrNotSignable otherwise.
	Seq int `json:"seq"`

	// By identifies who is signing — the qa-tester, channelled through the
	// role-scoped hook rule (internal/cli/hook.go's roleScopedTools,
	// D11) that restricts mcp__mneme__quality_sign to that one role and
	// fails CLOSED when a subagent's role cannot be resolved.
	By string `json:"by"`

	// Evidence documents WHAT was verified and how — required and
	// non-empty (model.ErrReasonRequired), persisted verbatim as the row's
	// Justification.
	Evidence string `json:"evidence"`
}

// QualityReportRequest is the input for `mneme quality report` /
// quality_report (SPEC-117 S3 D12): render the QA report from the spec's
// LATEST certificate and write it via SpecDocWrite — never from
// criteria.toml, which may have changed since certification (D1).
type QualityReportRequest struct {
	// ID is the spec whose latest certificate is rendered.
	ID string `json:"id"`

	// Force allows overwriting an existing qa-report.md that does NOT
	// carry mneme's own generation marker (D12) — without it, Report
	// refuses to silently destroy a manually-authored report
	// (ErrReportNotGenerated).
	Force bool `json:"force,omitempty"`
}

// QualityReportResponse is returned by quality_report.
type QualityReportResponse struct {
	// Path is the absolute path the report was written to.
	Path string `json:"path"`

	// Bytes is len(content) as written.
	Bytes int `json:"bytes"`

	// CertificateID is the certificate the report was rendered from.
	CertificateID string `json:"certificate_id"`
}
