package quality

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// BaselineSchemaVersion is the only schema_version ParseBaseline accepts —
// the baseline file's own schema, independent of the constitution's
// (D5/D10). It exists from day one at 1 because there is nothing yet to be
// backward compatible with.
const BaselineSchemaVersion = 1

// BaselineRelPath is the FIXED, repository-relative path of the ratchet's
// registered baseline — a constant, not a constitution key (D10): D13 of
// the grill reserves the constitution for THRESHOLDS a team revises, not
// for where mneme keeps its own bookkeeping files. One fewer key to get
// wrong.
const BaselineRelPath = ".mneme/quality-baseline.toml"

// ErrInvalidBaseline is returned by ParseBaseline when the document is
// missing a required key, declares an unknown one, or fails to parse — the
// baseline is machine-written (only `mneme quality baseline update` ever
// produces it, D10), so any deviation is a corrupted or hand-edited file,
// never a matter of taste.
var ErrInvalidBaseline = errors.New("quality: invalid baseline")

// Baseline is the ratchet's registered measurement (D10): the repository-
// wide coverage percentage mneme itself measured at a specific, ancestor
// commit, plus enough metadata (ScopeHash) to tell whether a later
// measurement is even comparable to it.
type Baseline struct {
	SchemaVersion int

	// MeasuredAtSHA is the exact commit the measurement was taken at — the
	// baseline-comparable check (D11) requires this to be an ancestor of
	// HEAD before comparing against it.
	MeasuredAtSHA string

	MeasuredAt time.Time

	// CertificateID is the certificate `baseline update` read these figures
	// from — provenance, never re-derived.
	CertificateID string

	LinesTotal    int
	LinesCovered  int
	GlobalLinePct float64

	// ScopeHash is ScopeHash(format, exclude) at measurement time — a later
	// measurement whose own ScopeHash differs was taken under a different
	// measurement scope and is not comparable to this one (D11 point 2).
	ScopeHash string
}

// rawBaseline is ParseBaseline's strict decode target — every field a
// pointer so a missing key is distinguishable from its zero value, the
// same mold constitution.go's rawConstitution uses (AC18: "tan estricto
// como la constitución").
type rawBaseline struct {
	SchemaVersion *int     `toml:"schema_version"`
	MeasuredAtSHA *string  `toml:"measured_at_sha"`
	MeasuredAt    *string  `toml:"measured_at"`
	CertificateID *string  `toml:"certificate_id"`
	LinesTotal    *int     `toml:"lines_total"`
	LinesCovered  *int     `toml:"lines_covered"`
	GlobalLinePct *float64 `toml:"global_line_pct"`
	ScopeHash     *string  `toml:"scope_hash"`
}

// ParseBaseline decodes and validates raw TOML bytes into a Baseline. Every
// documented key is required (AC18 — a missing key names itself in the
// returned error) and DisallowUnknownFields rejects any key it does not
// recognise. Nobody hand-writes this file (only `mneme quality baseline
// update` does, D10) — being just as strict as the constitution's own
// Parse means a corrupted or manually "fixed" baseline fails loudly instead
// of silently governing nothing.
func ParseBaseline(data []byte) (*Baseline, error) {
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var raw rawBaseline
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("quality: parse baseline: %s: %w", err, ErrInvalidBaseline)
	}

	if raw.SchemaVersion == nil {
		return nil, fmt.Errorf("quality: baseline missing required key %q: %w", "schema_version", ErrInvalidBaseline)
	}
	if *raw.SchemaVersion != BaselineSchemaVersion {
		return nil, fmt.Errorf("quality: baseline schema_version %d unsupported: %w", *raw.SchemaVersion, ErrInvalidBaseline)
	}
	if raw.MeasuredAtSHA == nil {
		return nil, fmt.Errorf("quality: baseline missing required key %q: %w", "measured_at_sha", ErrInvalidBaseline)
	}
	if raw.MeasuredAt == nil {
		return nil, fmt.Errorf("quality: baseline missing required key %q: %w", "measured_at", ErrInvalidBaseline)
	}
	measuredAt, err := time.Parse(time.RFC3339, *raw.MeasuredAt)
	if err != nil {
		return nil, fmt.Errorf("quality: baseline measured_at %q: %s: %w", *raw.MeasuredAt, err, ErrInvalidBaseline)
	}
	if raw.CertificateID == nil {
		return nil, fmt.Errorf("quality: baseline missing required key %q: %w", "certificate_id", ErrInvalidBaseline)
	}
	if raw.LinesTotal == nil {
		return nil, fmt.Errorf("quality: baseline missing required key %q: %w", "lines_total", ErrInvalidBaseline)
	}
	if raw.LinesCovered == nil {
		return nil, fmt.Errorf("quality: baseline missing required key %q: %w", "lines_covered", ErrInvalidBaseline)
	}
	if raw.GlobalLinePct == nil {
		return nil, fmt.Errorf("quality: baseline missing required key %q: %w", "global_line_pct", ErrInvalidBaseline)
	}
	if raw.ScopeHash == nil {
		return nil, fmt.Errorf("quality: baseline missing required key %q: %w", "scope_hash", ErrInvalidBaseline)
	}

	return &Baseline{
		SchemaVersion: *raw.SchemaVersion,
		MeasuredAtSHA: *raw.MeasuredAtSHA,
		MeasuredAt:    measuredAt,
		CertificateID: *raw.CertificateID,
		LinesTotal:    *raw.LinesTotal,
		LinesCovered:  *raw.LinesCovered,
		GlobalLinePct: *raw.GlobalLinePct,
		ScopeHash:     *raw.ScopeHash,
	}, nil
}

// RenderBaseline renders b as the exact TOML text `mneme quality baseline
// update` writes to BaselineRelPath. ParseBaseline(RenderBaseline(b)) is an
// exact round-trip (AC18) — the pairing that lets `baseline update` and
// `quality status` (which reads the file back) share one unambiguous format.
func RenderBaseline(b *Baseline) string {
	return fmt.Sprintf(`schema_version  = %d
measured_at_sha = %q
measured_at     = %q
certificate_id  = %q
lines_total     = %d
lines_covered   = %d
global_line_pct = %s
scope_hash      = %q
`,
		b.SchemaVersion,
		b.MeasuredAtSHA,
		b.MeasuredAt.UTC().Format(time.RFC3339),
		b.CertificateID,
		b.LinesTotal,
		b.LinesCovered,
		formatPct(b.GlobalLinePct),
		b.ScopeHash,
	)
}

// formatPct renders a percentage with exactly two decimal places, valid
// TOML float syntax even for a whole number (e.g. "70.00", never "70.").
func formatPct(pct float64) string {
	return fmt.Sprintf("%.2f", pct)
}

// CompareRatchet is the pure half of check 6, `ratchet/global-line-pct`
// (D4/AC19): given the currently-measured global percentage, the
// registered baseline's percentage, and the constitution's declared
// max_global_line_pct_drop tolerance, it returns how many points coverage
// dropped (0 when it did not drop at all — an improvement is never a
// negative "drop") and whether that drop exceeds the tolerance. A drop
// beyond tolerance is a `finding`, never a `fail` (D17's own reasoning:
// the aggregate has legitimate reasons to fall that a single untested new
// line never has).
func CompareRatchet(currentPct, basePct, maxDropPct float64) (dropPct float64, finding bool) {
	dropPct = basePct - currentPct
	if dropPct < 0 {
		dropPct = 0
	}
	return dropPct, dropPct > maxDropPct
}

// CompareStaleness is the pure half of check 7, `ratchet/baseline-stale`
// (D17/AC22): given the currently-measured global percentage, the
// registered baseline's percentage, and the constitution's declared
// max_baseline_staleness_pct margin, it returns how far the measurement
// has pulled AHEAD of the registered mark (0 when the measurement is at or
// below the mark — that direction is check 6's problem, not staleness) and
// whether that gap exceeds the margin. This is what makes an improved-but-
// never-registered baseline visible instead of silently accumulating
// unbounded slack.
func CompareStaleness(currentPct, basePct, marginPct float64) (staleness float64, finding bool) {
	staleness = currentPct - basePct
	if staleness < 0 {
		staleness = 0
	}
	return staleness, staleness > marginPct
}

// BaselineDirection is the pure half of check 4, `ratchet/baseline-
// integrity` (D11/AC20) — the five-row table, isolated from git and the
// store so every row is exhaustively testable with plain values:
//
//	before   after    result    why
//	absent   absent   pass      nothing to compare
//	absent   present  pass      no mark existed to weaken; honest bootstrap
//	present  absent   FINDING   deleting it returns the ratchet to skipped — the obvious leak
//	present  present, pct >=    pass      raising the mark only hardens the ratchet — must be free (D17's remedy)
//	present  present, pct <     FINDING   weakening the mark mid-spec is the attack
//
// before/after are the baseline file's content at the spec's merge-base and
// at HEAD, respectively (nil meaning "the file did not exist there").
func BaselineDirection(before, after *Baseline) (finding bool, reason string) {
	switch {
	case before == nil:
		// Rows 1-2: no prior mark existed — nothing to weaken, whether or
		// not one exists now.
		return false, ""
	case after == nil:
		return true, "la linea base fue eliminada dentro del rango de commits de esta spec"
	case after.GlobalLinePct < before.GlobalLinePct:
		return true, fmt.Sprintf(
			"la linea base se debilito de %.2f%% a %.2f%% dentro del rango de commits de esta spec",
			before.GlobalLinePct, after.GlobalLinePct)
	default:
		// after.GlobalLinePct >= before.GlobalLinePct: raising the mark (or
		// leaving it exactly as it was) is monotonically safe.
		return false, ""
	}
}
