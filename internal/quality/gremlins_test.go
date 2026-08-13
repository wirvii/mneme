package quality

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestGremlinsParser_RealFixture covers AC7's positive anchor: parsing the
// REAL captured report (testdata/README.md documents provenance — six of
// its seven entries are byte-for-byte from a real `gremlins unleash -o`
// run; the seventh, a NOT VIABLE entry, is hand-forced and documented)
// yields the exact per-status counts and at least one mutant's full detail
// (path, line, column, mutator).
func TestGremlinsParser_RealFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "gremlins-v0.6.0.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	report, err := ParseMutantReport("gremlins", raw)
	if err != nil {
		t.Fatalf("ParseMutantReport(gremlins): %v", err)
	}

	counts := map[MutantStatus]int{}
	for _, m := range report.Mutants {
		counts[m.Status]++
	}

	want := map[MutantStatus]int{
		MutantKilled:    5,
		MutantLived:     1,
		MutantNotViable: 1,
	}
	for status, wantCount := range want {
		if counts[status] != wantCount {
			t.Errorf("counts[%s] = %d, want %d (full counts: %v)", status, counts[status], wantCount, counts)
		}
	}

	found := false
	for _, m := range report.Mutants {
		if m.File == "calc.go" && m.Line == 5 && m.Column == 11 && m.Mutator == "ARITHMETIC_BASE" && m.Status == MutantKilled {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected calc.go:5:11 ARITHMETIC_BASE killed mutant not found in parsed report")
	}
}

// TestGremlinsParser_FailsLoudly covers AC7's three mandatory hermanas
// (G6): a JSON of another schema, a truncated document, and an unknown
// tool status must ALL be ErrInvalidMutantReport — NEVER a silently empty
// MutantReport, which would leave the mechanism green and dead (G6).
func TestGremlinsParser_FailsLoudly(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{
			name: "JSON of another schema entirely (e.g. mutants-v1's own shape)",
			doc:  `{"schema":"mutants-v1","mutants":[{"file":"a.go","line":1,"mutator":"x","status":"killed"}]}`,
		},
		{
			name: "truncated JSON",
			doc:  `{"files":[{"file_name":"a.go","mutations":[{"type":"ARITHMETIC_BASE","stat`,
		},
		{
			name: "unknown gremlins status",
			doc:  `{"go_module":"m","files":[{"file_name":"a.go","mutations":[{"type":"ARITHMETIC_BASE","status":"RUNNABLE","line":1,"column":1}]}]}`,
		},
		{
			name: "missing go_module (not a gremlins report at all)",
			doc:  `{"files":[]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := ParseMutantReport("gremlins", []byte(tt.doc))
			if !errors.Is(err, ErrInvalidMutantReport) {
				t.Fatalf("ParseMutantReport() error = %v, want ErrInvalidMutantReport", err)
			}
			if report != nil {
				t.Fatalf("ParseMutantReport() report = %+v, want nil on error", report)
			}
		})
	}
}

// TestGremlinsParser_ZeroMutantsIsNotAnError is the hermana that gives the
// positive/negative pair its point: a document with a `files` list but no
// mutations at all parses successfully with an empty MutantReport — the
// acotado (mutscope.go, wired in the service layer) is what turns "nothing
// in the diff" into a finding, never this parser.
func TestGremlinsParser_ZeroMutantsIsNotAnError(t *testing.T) {
	report, err := ParseMutantReport("gremlins", []byte(`{"go_module":"m","files":[]}`))
	if err != nil {
		t.Fatalf("ParseMutantReport(empty files): %v", err)
	}
	if len(report.Mutants) != 0 {
		t.Fatalf("len(report.Mutants) = %d, want 0", len(report.Mutants))
	}
}
