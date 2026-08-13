package quality

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// validBaseline is a minimal, fully valid baseline document — the fixture
// AC18's missing-key rows each delete one line from.
const validBaseline = `
schema_version  = 1
measured_at_sha = "9f3c000000000000000000000000000000000c"
measured_at     = "2026-08-13T11:04:22Z"
certificate_id  = "0193abcd-0000-7000-8000-000000000000"
lines_total     = 100
lines_covered   = 70
global_line_pct = 70.50
scope_hash      = "b21f0000"
`

// TestParseBaseline_MissingRequiredKey covers AC18: one row per required
// key, each naming itself in the error.
func TestParseBaseline_MissingRequiredKey(t *testing.T) {
	tests := []struct {
		key string
		doc string
	}{
		{"schema_version", `
measured_at_sha = "abc"
measured_at     = "2026-08-13T11:04:22Z"
certificate_id  = "cid"
lines_total     = 1
lines_covered   = 1
global_line_pct = 1.0
scope_hash      = "h"
`},
		{"measured_at_sha", `
schema_version  = 1
measured_at     = "2026-08-13T11:04:22Z"
certificate_id  = "cid"
lines_total     = 1
lines_covered   = 1
global_line_pct = 1.0
scope_hash      = "h"
`},
		{"measured_at", `
schema_version  = 1
measured_at_sha = "abc"
certificate_id  = "cid"
lines_total     = 1
lines_covered   = 1
global_line_pct = 1.0
scope_hash      = "h"
`},
		{"certificate_id", `
schema_version  = 1
measured_at_sha = "abc"
measured_at     = "2026-08-13T11:04:22Z"
lines_total     = 1
lines_covered   = 1
global_line_pct = 1.0
scope_hash      = "h"
`},
		{"lines_total", `
schema_version  = 1
measured_at_sha = "abc"
measured_at     = "2026-08-13T11:04:22Z"
certificate_id  = "cid"
lines_covered   = 1
global_line_pct = 1.0
scope_hash      = "h"
`},
		{"lines_covered", `
schema_version  = 1
measured_at_sha = "abc"
measured_at     = "2026-08-13T11:04:22Z"
certificate_id  = "cid"
lines_total     = 1
global_line_pct = 1.0
scope_hash      = "h"
`},
		{"global_line_pct", `
schema_version  = 1
measured_at_sha = "abc"
measured_at     = "2026-08-13T11:04:22Z"
certificate_id  = "cid"
lines_total     = 1
lines_covered   = 1
scope_hash      = "h"
`},
		{"scope_hash", `
schema_version  = 1
measured_at_sha = "abc"
measured_at     = "2026-08-13T11:04:22Z"
certificate_id  = "cid"
lines_total     = 1
lines_covered   = 1
global_line_pct = 1.0
`},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			_, err := ParseBaseline([]byte(tt.doc))
			if err == nil {
				t.Fatalf("ParseBaseline missing %s: want error, got nil", tt.key)
			}
			if !errors.Is(err, ErrInvalidBaseline) {
				t.Errorf("error = %v, want wrapping ErrInvalidBaseline", err)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error = %q, want it to name key %q", err.Error(), tt.key)
			}
		})
	}
}

// TestParseBaseline_UnknownKeyRejected is the "clave desconocida
// rechazada" row of AC18.
func TestParseBaseline_UnknownKeyRejected(t *testing.T) {
	_, err := ParseBaseline([]byte(validBaseline + "\nextra_key = true\n"))
	if !errors.Is(err, ErrInvalidBaseline) {
		t.Fatalf("ParseBaseline(unknown key) error = %v, want ErrInvalidBaseline", err)
	}
}

// TestParseBaseline_Valid is the "fila valida completa que parsea" row.
func TestParseBaseline_Valid(t *testing.T) {
	b, err := ParseBaseline([]byte(validBaseline))
	if err != nil {
		t.Fatalf("ParseBaseline: %v", err)
	}
	if b.LinesTotal != 100 || b.LinesCovered != 70 || b.GlobalLinePct != 70.50 {
		t.Fatalf("unexpected fields: %+v", b)
	}
}

// TestBaseline_RenderParseRoundTrip covers AC18's round-trip requirement:
// RenderBaseline -> ParseBaseline reproduces the same content exactly.
func TestBaseline_RenderParseRoundTrip(t *testing.T) {
	measuredAt, err := time.Parse(time.RFC3339, "2026-08-13T11:04:22Z")
	if err != nil {
		t.Fatalf("time.Parse: %v", err)
	}
	original := &Baseline{
		SchemaVersion: 1,
		MeasuredAtSHA: "9f3c000000000000000000000000000000000c",
		MeasuredAt:    measuredAt,
		CertificateID: "0193abcd-0000-7000-8000-000000000000",
		LinesTotal:    41230,
		LinesCovered:  29066,
		GlobalLinePct: 70.50,
		ScopeHash:     "b21f0000",
	}

	rendered := RenderBaseline(original)
	roundTripped, err := ParseBaseline([]byte(rendered))
	if err != nil {
		t.Fatalf("ParseBaseline(RenderBaseline(original)): %v", err)
	}

	if *roundTripped != *original {
		t.Fatalf("round-trip mismatch:\noriginal:  %+v\nroundtrip: %+v", original, roundTripped)
	}
}

// TestCompareRatchet covers AC19's pure half: a drop beyond tolerance is a
// finding; within tolerance, or an improvement, is not.
func TestCompareRatchet(t *testing.T) {
	tests := []struct {
		name             string
		currentPct       float64
		basePct          float64
		maxDropPct       float64
		wantFinding      bool
		wantDropAtLeast0 bool
	}{
		{"drop beyond tolerance is a finding", 68.0, 70.5, 1.0, true, true},
		{"drop within tolerance passes", 70.0, 70.5, 1.0, false, true},
		{"improvement passes", 72.0, 70.5, 0.0, false, true},
		{"no baseline file has no meaning here (caller skips) — exact equality passes", 70.5, 70.5, 0.0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drop, finding := CompareRatchet(tt.currentPct, tt.basePct, tt.maxDropPct)
			if finding != tt.wantFinding {
				t.Errorf("CompareRatchet(%v, %v, %v) finding = %v, want %v (drop=%v)", tt.currentPct, tt.basePct, tt.maxDropPct, finding, tt.wantFinding, drop)
			}
			if drop < 0 {
				t.Errorf("CompareRatchet returned a negative drop %v — an improvement must clamp to 0", drop)
			}
		})
	}
}

// TestCompareStaleness covers AC22's pure half.
func TestCompareStaleness(t *testing.T) {
	tests := []struct {
		name        string
		currentPct  float64
		basePct     float64
		marginPct   float64
		wantFinding bool
	}{
		{"exceeds margin is a finding", 74.2, 70.5, 1.0, true},
		{"within margin passes", 71.0, 70.5, 1.0, false},
		{"below the mark passes (that direction is check 6's problem)", 65.0, 70.5, 1.0, false},
		{"exactly at the margin passes (strictly greater required)", 71.5, 70.5, 1.0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			staleness, finding := CompareStaleness(tt.currentPct, tt.basePct, tt.marginPct)
			if finding != tt.wantFinding {
				t.Errorf("CompareStaleness(%v, %v, %v) finding = %v, want %v (staleness=%v)", tt.currentPct, tt.basePct, tt.marginPct, finding, tt.wantFinding, staleness)
			}
			if staleness < 0 {
				t.Errorf("CompareStaleness returned negative staleness %v — must clamp to 0", staleness)
			}
		})
	}
}

// TestBaselineDirection covers AC20's five-row table at the pure level —
// P8 wires this to real git-read baselines; here every row is exhaustively
// testable with plain values. The two middle rows are the ones that
// prevent the two opposite vacuous guardians (G11a/G11b): a guardian that
// marks ANY change as a finding fails the "pct mayor -> pass" row; one
// that marks NO change fails the "pct menor -> finding" row.
func TestBaselineDirection(t *testing.T) {
	lower := &Baseline{GlobalLinePct: 60.0}
	higher := &Baseline{GlobalLinePct: 75.0}
	same := &Baseline{GlobalLinePct: 60.0}

	tests := []struct {
		name        string
		before      *Baseline
		after       *Baseline
		wantFinding bool
	}{
		{"absent -> absent: pass", nil, nil, false},
		{"absent -> present: pass (honest bootstrap)", nil, higher, false},
		{"present -> absent: FINDING (the obvious leak)", lower, nil, true},
		{"present -> present, pct higher: pass (raising the mark is free)", lower, higher, false},
		{"present -> present, pct lower: FINDING (weakening mid-spec)", higher, lower, true},
		{"present -> present, pct unchanged: pass", lower, same, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finding, reason := BaselineDirection(tt.before, tt.after)
			if finding != tt.wantFinding {
				t.Errorf("BaselineDirection(%+v, %+v) finding = %v, want %v (reason=%q)", tt.before, tt.after, finding, tt.wantFinding, reason)
			}
			if finding && reason == "" {
				t.Error("a finding must carry a non-empty reason")
			}
		})
	}
}
