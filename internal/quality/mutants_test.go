package quality

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestMutant_ID covers Mutant.ID()'s exact shape: <file>:<line>:<column>:<mutator>.
func TestMutant_ID(t *testing.T) {
	m := Mutant{File: "a/b.go", Line: 10, Column: 5, Mutator: "ARITHMETIC_BASE"}
	want := "a/b.go:10:5:ARITHMETIC_BASE"
	if got := m.ID(); got != want {
		t.Fatalf("Mutant.ID() = %q, want %q", got, want)
	}
}

// TestMutantFormats_IsSortedRegistrySnapshot pins MutantFormats() to the
// exact registered set, sorted — a future format addition is a one-line
// diff to this list, mirroring TestFormats_IsSortedRegistrySnapshot
// (profile_test.go).
func TestMutantFormats_IsSortedRegistrySnapshot(t *testing.T) {
	got := MutantFormats()
	want := []string{"gremlins", "mutants-v1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MutantFormats() = %v, want %v", got, want)
	}
}

// TestParseMutantReport_UnknownFormat covers ErrUnknownMutantFormat: a
// format not in the registry is rejected, never guessed from the bytes.
func TestParseMutantReport_UnknownFormat(t *testing.T) {
	_, err := ParseMutantReport("not-a-format", []byte("whatever"))
	if !errors.Is(err, ErrUnknownMutantFormat) {
		t.Fatalf("ParseMutantReport(unknown format) error = %v, want ErrUnknownMutantFormat", err)
	}
}

// TestParseMutantReport_DispatchesToRegisteredParser is the hermana pasa:
// a registered format dispatches to its parser.
func TestParseMutantReport_DispatchesToRegisteredParser(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "mutants-v1-six-states.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	report, err := ParseMutantReport("mutants-v1", raw)
	if err != nil {
		t.Fatalf("ParseMutantReport(mutants-v1): %v", err)
	}
	if len(report.Mutants) != 6 {
		t.Fatalf("len(report.Mutants) = %d, want 6", len(report.Mutants))
	}
}

// TestMutantFormats_RegistryContract is G5/AC6: EVERY registered format
// must be able to say "this mutation never compiled" as a status distinct
// from a kill or a survival — proven here by requiring, for each format's
// OWN real-or-forced fixture, at least one not_viable, one killed, and one
// lived mutant. A format that cannot satisfy this can never be registered
// (D1 pata b) — this is the guardian G5 protects: registering a third,
// toy format whose fixture has only killed/lived must make this loop fail
// by naming the missing not_viable.
func TestMutantFormats_RegistryContract(t *testing.T) {
	fixtures := map[string]string{
		"mutants-v1": "mutants-v1-six-states.json",
		"gremlins":   "gremlins-v0.6.0.json",
	}

	formats := MutantFormats()
	if len(formats) != len(fixtures) {
		t.Fatalf("MutantFormats() = %v has %d entries, but this test only has fixtures for %d — every registered format needs a contract fixture", formats, len(formats), len(fixtures))
	}

	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			fixtureName, ok := fixtures[format]
			if !ok {
				t.Fatalf("format %q has no contract fixture registered in this test", format)
			}
			raw, err := os.ReadFile(filepath.Join("testdata", fixtureName))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			report, err := ParseMutantReport(format, raw)
			if err != nil {
				t.Fatalf("ParseMutantReport(%s): %v", format, err)
			}

			var hasNotViable, hasKilled, hasLived bool
			for _, m := range report.Mutants {
				switch m.Status {
				case MutantNotViable:
					hasNotViable = true
				case MutantKilled:
					hasKilled = true
				case MutantLived:
					hasLived = true
				}
			}
			if !hasNotViable {
				t.Errorf("format %q's fixture has no not_viable mutant — a format that cannot express this state must never be registered (D1 pata b)", format)
			}
			if !hasKilled {
				t.Errorf("format %q's fixture has no killed mutant", format)
			}
			if !hasLived {
				t.Errorf("format %q's fixture has no lived mutant", format)
			}
		})
	}
}

// TestMutantsV1Parser_Strict covers AC8: mutants-v1 is as strict as any
// other parser in this leaf. Table-driven with the two mandatory hermanas
// at the bottom (zero mutants OK, all six states OK).
func TestMutantsV1Parser_Strict(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		{
			name:    "unknown key",
			doc:     `{"schema":"mutants-v1","mutants":[],"bogus":true}`,
			wantErr: true,
		},
		{
			name:    "wrong schema",
			doc:     `{"schema":"mutants-v0","mutants":[]}`,
			wantErr: true,
		},
		{
			name:    "status outside vocabulary",
			doc:     `{"schema":"mutants-v1","mutants":[{"file":"a.go","line":1,"mutator":"x","status":"exterminated"}]}`,
			wantErr: true,
		},
		{
			name:    "mutant missing file",
			doc:     `{"schema":"mutants-v1","mutants":[{"line":1,"mutator":"x","status":"killed"}]}`,
			wantErr: true,
		},
		{
			name:    "mutant missing line",
			doc:     `{"schema":"mutants-v1","mutants":[{"file":"a.go","mutator":"x","status":"killed"}]}`,
			wantErr: true,
		},
		{
			name:    "zero mutants parses OK — not this parser's error to raise",
			doc:     `{"schema":"mutants-v1","mutants":[]}`,
			wantErr: false,
		},
		{
			name:    "all six states parse OK",
			doc:     `{"schema":"mutants-v1","mutants":[{"file":"a.go","line":1,"mutator":"x","status":"killed"},{"file":"a.go","line":2,"mutator":"x","status":"lived"},{"file":"a.go","line":3,"mutator":"x","status":"not_viable"},{"file":"a.go","line":4,"mutator":"x","status":"not_covered"},{"file":"a.go","line":5,"mutator":"x","status":"timed_out"},{"file":"a.go","line":6,"mutator":"x","status":"skipped"}]}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseMutantReport("mutants-v1", []byte(tt.doc))
			if tt.wantErr && !errors.Is(err, ErrInvalidMutantReport) {
				t.Fatalf("ParseMutantReport() error = %v, want ErrInvalidMutantReport", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ParseMutantReport() unexpected error: %v", err)
			}
		})
	}
}
