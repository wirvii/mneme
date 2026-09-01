// Package quality — this file implements the pure function that renders
// SPEC-137 D6's "de que es evidencia este certificado" sentence: a single
// line, persisted once at emission time, that names every family the
// mechanism knows about — including the ones that did nothing — so a
// green certificate can never again be read as "everything was checked"
// when in fact almost nothing was declared.
package quality

import (
	"fmt"
	"strings"
)

// EvidenceGates is the puertas segment: how many of the project's declared
// gates ran green. Declared is false when the project declares none at
// all — AC15's "sin puertas declaradas" tramo.
type EvidenceGates struct {
	Declared bool
	Total    int
	Green    int
}

// EvidenceCriteria is the criterios segment: how many of the spec's
// executable acceptance criteria the certificate found met (pass or
// acked). Declared is false when [criteria] never ran.
type EvidenceCriteria struct {
	Declared bool
	Total    int
	Met      int
}

// EvidenceCoverage is the cobertura segment. Declared is false when
// [coverage] is off or the tramo never ran at all (gatesStopped).
// Evaluated is false when Declared is true but coverage/diff-lines itself
// produced no percentage (no base_sha, or too few eligible changed
// lines) — distinct from "not declared", since the two read differently
// to whoever certifies the spec.
type EvidenceCoverage struct {
	Declared  bool
	Evaluated bool
	Pct       float64

	// ModeSuffix is coverage/diff-lines' own resolved mode, already
	// decided by the caller from the row's Status/Effect ("hallazgo sin
	// firmar", "hallazgo firmado", or "" when the threshold was met
	// outright) — Evidence never re-derives wording from a raw status, so
	// the vocabulary D7 defines stays defined in exactly one place.
	ModeSuffix string
}

// EvidenceFamily is the shared shape for the four measure-only families
// (ratchet/budget/mutation/visual, D5/D8/D9): Evidence's sentence only
// ever needs to know whether each was declared — what each one measured
// travels in the certificate and the QA report, never in this one line.
type EvidenceFamily struct {
	Declared bool
}

// EvidenceInput is the flat, already-resolved snapshot Evidence needs —
// one field per family, each pre-computed by the caller from the
// certificate's real rows. Evidence never imports model.QualityCheck and
// never re-parses a row's own prose Summary: every number here arrives as
// a number, the same "arrives pre-formatted" posture ReportInput already
// established for RenderReport.
type EvidenceInput struct {
	Gates    EvidenceGates
	Criteria EvidenceCriteria
	Coverage EvidenceCoverage
	Ratchet  EvidenceFamily
	Budget   EvidenceFamily
	Mutation EvidenceFamily
	Visual   EvidenceFamily
}

// evidenceFamilyLabel names a measure-only family's declared/not-declared
// phrasing with its own grammatical gender, since Spanish agreement can't
// be derived mechanically from the family's English field name.
type evidenceFamilyLabel struct {
	declared    string
	notDeclared string
}

// EvidenceFamilyNames is the SINGLE ordered list this sentence's four
// measure-only segments are built from — AC14 derives its own expected
// family list from this same slice instead of copying the names by hand,
// so a family added here without updating a test can never go unnoticed
// the other way around.
var EvidenceFamilyNames = []string{"cliquet", "presupuesto", "mutacion", "verificacion visual"}

var evidenceFamilyLabels = map[string]evidenceFamilyLabel{
	"cliquet":             {declared: "cliquet declarado", notDeclared: "cliquet no declarado"},
	"presupuesto":         {declared: "presupuesto declarado", notDeclared: "presupuesto no declarado"},
	"mutacion":            {declared: "mutacion declarada", notDeclared: "mutacion no declarada"},
	"verificacion visual": {declared: "verificacion visual declarada", notDeclared: "verificacion visual no declarada"},
}

// Evidence renders SPEC-137 D6's sentence from in. Deterministic: the same
// input always produces the same bytes, which is what lets the certificate
// persist it once at Verify time and never recompute it on read (D6's
// "nunca se re-deriva al leer").
func Evidence(in EvidenceInput) string {
	segments := make([]string, 0, 7)

	if !in.Gates.Declared {
		segments = append(segments, "sin puertas declaradas")
	} else {
		segments = append(segments, fmt.Sprintf("%d/%d puertas del proyecto en verde", in.Gates.Green, in.Gates.Total))
	}

	if !in.Criteria.Declared {
		segments = append(segments, "criterios no declarados")
	} else {
		segments = append(segments, fmt.Sprintf("%d/%d criterios cumplidos", in.Criteria.Met, in.Criteria.Total))
	}

	segments = append(segments, coverageSegment(in.Coverage))

	families := map[string]EvidenceFamily{
		"cliquet":             in.Ratchet,
		"presupuesto":         in.Budget,
		"mutacion":            in.Mutation,
		"verificacion visual": in.Visual,
	}
	for _, name := range EvidenceFamilyNames {
		label := evidenceFamilyLabels[name]
		if families[name].Declared {
			segments = append(segments, label.declared)
		} else {
			segments = append(segments, label.notDeclared)
		}
	}

	return "Evidencia: " + strings.Join(segments, " · ")
}

// coverageSegment renders the cobertura tramo alone — its own function
// because, unlike the other six segments, it has three distinct shapes
// (not declared / declared-but-not-evaluated / evaluated-with-mode)
// instead of two.
func coverageSegment(c EvidenceCoverage) string {
	if !c.Declared {
		return "cobertura no declarada"
	}
	if !c.Evaluated {
		return "cobertura declarada, sin evaluar en este rango"
	}
	if c.ModeSuffix == "" {
		return fmt.Sprintf("cobertura del diff %.2f%%", c.Pct)
	}
	return fmt.Sprintf("cobertura del diff %.2f%% (%s)", c.Pct, c.ModeSuffix)
}
