package quality

import (
	"strings"
	"testing"
)

func TestEvidence_AllDeclaredAndGreen(t *testing.T) {
	in := EvidenceInput{
		Gates:    EvidenceGates{Declared: true, Total: 3, Green: 3},
		Criteria: EvidenceCriteria{Declared: true, Total: 12, Met: 12},
		Coverage: EvidenceCoverage{Declared: true, Evaluated: true, Pct: 84.64, ModeSuffix: "hallazgo sin firmar"},
		Ratchet:  EvidenceFamily{Declared: false},
		Budget:   EvidenceFamily{Declared: false},
		Mutation: EvidenceFamily{Declared: false},
		Visual:   EvidenceFamily{Declared: false},
	}

	got := Evidence(in)

	want := "Evidencia: 3/3 puertas del proyecto en verde · 12/12 criterios cumplidos · " +
		"cobertura del diff 84.64% (hallazgo sin firmar) · cliquet no declarado · " +
		"presupuesto no declarado · mutacion no declarada · verificacion visual no declarada"
	if got != want {
		t.Errorf("Evidence() =\n%q\nwant\n%q", got, want)
	}
}

// TestEvidence_NoGatesDeclared covers AC15: the fixture declares no gates
// at all, and the sentence's first segment says so literally instead of
// rendering "0/0".
func TestEvidence_NoGatesDeclared(t *testing.T) {
	in := EvidenceInput{
		Gates:    EvidenceGates{Declared: false},
		Criteria: EvidenceCriteria{Declared: false},
		Coverage: EvidenceCoverage{Declared: false},
	}

	got := Evidence(in)

	if !strings.Contains(got, "sin puertas declaradas") {
		t.Errorf("Evidence() = %q, want it to contain %q", got, "sin puertas declaradas")
	}
	if strings.Contains(got, "0/0") {
		t.Errorf("Evidence() = %q, must never render a fabricated 0/0", got)
	}
}

// TestEvidence_EnumeratesEveryMeasureOnlyFamily covers AC14: a project with
// gates+criteria but none of the four measure-only families declared must
// still NAME every one of them — the expected list is derived from
// EvidenceFamilyNames itself, never copied by hand into the test.
func TestEvidence_EnumeratesEveryMeasureOnlyFamily(t *testing.T) {
	in := EvidenceInput{
		Gates:    EvidenceGates{Declared: true, Total: 1, Green: 1},
		Criteria: EvidenceCriteria{Declared: false},
		Coverage: EvidenceCoverage{Declared: false},
	}

	got := Evidence(in)

	for _, family := range EvidenceFamilyNames {
		label := evidenceFamilyLabels[family]
		if !strings.Contains(got, label.notDeclared) {
			t.Errorf("Evidence() = %q, missing family segment %q", got, label.notDeclared)
		}
	}
}

func TestEvidence_CoverageDeclaredButNotEvaluated(t *testing.T) {
	in := EvidenceInput{
		Gates:    EvidenceGates{Declared: true, Total: 1, Green: 1},
		Criteria: EvidenceCriteria{Declared: false},
		Coverage: EvidenceCoverage{Declared: true, Evaluated: false},
	}

	got := Evidence(in)

	if !strings.Contains(got, "cobertura declarada, sin evaluar") {
		t.Errorf("Evidence() = %q, want the declared-but-not-evaluated coverage phrasing", got)
	}
}

func TestEvidence_CoverageWithoutModeSuffixWhenThresholdMet(t *testing.T) {
	in := EvidenceInput{
		Gates:    EvidenceGates{Declared: true, Total: 1, Green: 1},
		Criteria: EvidenceCriteria{Declared: false},
		Coverage: EvidenceCoverage{Declared: true, Evaluated: true, Pct: 96.0, ModeSuffix: ""},
	}

	got := Evidence(in)

	if !strings.Contains(got, "cobertura del diff 96.00%") {
		t.Errorf("Evidence() = %q, want the plain percentage with no mode suffix", got)
	}
	if strings.Contains(got, "()") {
		t.Errorf("Evidence() = %q, must never render an empty mode parenthesis", got)
	}
}

// TestEvidence_Deterministic guards against a future change accidentally
// introducing map-iteration order into the sentence — the SAME input must
// always produce the SAME bytes, since this string is persisted once.
func TestEvidence_Deterministic(t *testing.T) {
	in := EvidenceInput{
		Gates:    EvidenceGates{Declared: true, Total: 2, Green: 2},
		Criteria: EvidenceCriteria{Declared: true, Total: 5, Met: 5},
		Coverage: EvidenceCoverage{Declared: true, Evaluated: true, Pct: 100},
		Ratchet:  EvidenceFamily{Declared: true},
		Budget:   EvidenceFamily{Declared: true},
		Mutation: EvidenceFamily{Declared: true},
		Visual:   EvidenceFamily{Declared: true},
	}

	first := Evidence(in)
	for i := 0; i < 20; i++ {
		if got := Evidence(in); got != first {
			t.Fatalf("Evidence() is non-deterministic: run %d = %q, first = %q", i, got, first)
		}
	}
}
