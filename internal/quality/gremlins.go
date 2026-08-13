package quality

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// gremlinsReport is the strict decode target for a gremlins `unleash
// --output` JSON report — written against a REAL report captured from
// `gremlins unleash -o report.json` (go-gremlins/gremlins v0.6.0), fixed
// in testdata/gremlins-v0.6.0.json (D2/P2): the documentation gremlins
// publishes describes the general shape, but this parser is written
// against the file, and where the two would have disagreed the file
// wins (testdata/README.md records the one entry this fixture had to
// force by hand, and why).
//
// Deliberately NOT DisallowUnknownFields (unlike mutantsV1Parser and the
// constitution's own Parse): gremlins is a THIRD-PARTY tool this package
// does not control the release cadence of, and a future gremlins version
// adding a new top-level field (another aggregate statistic, say) must
// not turn every constitution declaring format="gremlins" into an
// instant, repository-wide block the moment someone upgrades the
// dependency (P11's own precondition — mneme still has to build against
// whatever gremlins version is on PATH). What this parser DOES refuse
// loudly is a status string outside the vocabulary it recognises
// (gremlinsStatusMap) — that is the one place a genuine schema drift
// must surface as ErrInvalidMutantReport, never as a silent guess.
type gremlinsReport struct {
	// GoModule is required non-empty (below) as the ONE cheap, always-
	// present discriminator between a genuine gremlins report and a
	// document from an entirely different schema (e.g. mutants-v1's own
	// shape) that happens to decode without error because this parser
	// deliberately does not use DisallowUnknownFields (see the type's own
	// godoc). A document from another tool's schema virtually never
	// happens to declare this exact key.
	GoModule string         `json:"go_module"`
	Files    []gremlinsFile `json:"files"`
}

type gremlinsFile struct {
	FileName  string             `json:"file_name"`
	Mutations []gremlinsMutation `json:"mutations"`
}

type gremlinsMutation struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// gremlinsStatusMap is the EXPLICIT, EXHAUSTIVE mapping from gremlins' own
// seven-value vocabulary (internal/mutator.Status.String(), verified
// against the real dependency's source at
// go-gremlins/gremlins@v0.6.0/internal/mutator/mutator.go — not assumed
// from documentation) to mneme's own closed MutantStatus set. "NOT VIABLE"
// and "TIMED OUT" head the map because they are the two states D1's four
// legs depend on being reported distinctly (pata b/pata c) — everything
// else follows the same "declare it or refuse it" posture.
//
// "RUNNABLE" is deliberately ABSENT: it names a mutant gremlins identified
// and could run but has not yet — a state that belongs to an in-progress
// or dry-run execution, never a completed report `mneme quality verify`
// would read. Its presence in a report this parser is asked to read means
// something is wrong with how the report was produced (an incomplete run,
// or a --dry-run informe accidentally declared as the real thing) — surfacing
// it as ErrInvalidMutantReport is safer than silently treating it as
// "skipped", which would make an incomplete run look identical to a
// deliberately-skipped mutation.
var gremlinsStatusMap = map[string]MutantStatus{
	"NOT VIABLE":  MutantNotViable,
	"TIMED OUT":   MutantTimedOut,
	"KILLED":      MutantKilled,
	"LIVED":       MutantLived,
	"NOT COVERED": MutantNotCovered,
	"SKIPPED":     MutantSkipped,
}

// gremlinsParser implements MutantReportParser for go-gremlins/gremlins's
// native JSON output.
type gremlinsParser struct{}

// Parse implements MutantReportParser. A document that is not valid JSON,
// or whose `files[].mutations[].status` is outside gremlinsStatusMap, is
// ErrInvalidMutantReport (G6) — NEVER a report with an empty Mutants list:
// silently tolerating an unrecognised gremlins schema would produce
// exactly the empty-denominator, fabricated-green certificate D1 exists
// to prevent, just one layer further out (at the tool-integration boundary
// instead of the arithmetic).
func (gremlinsParser) Parse(data []byte) (*MutantReport, error) {
	dec := json.NewDecoder(bytes.NewReader(data))

	var doc gremlinsReport
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("quality: gremlins: %s: %w", err, ErrInvalidMutantReport)
	}
	if doc.GoModule == "" {
		return nil, fmt.Errorf("quality: gremlins: missing required key %q (document is not a gremlins report): %w", "go_module", ErrInvalidMutantReport)
	}

	report := &MutantReport{}
	for _, f := range doc.Files {
		for _, m := range f.Mutations {
			status, ok := gremlinsStatusMap[m.Status]
			if !ok {
				return nil, fmt.Errorf(
					"quality: gremlins: file %q: mutation status %q not recognised (known: KILLED|LIVED|NOT VIABLE|NOT COVERED|TIMED OUT|SKIPPED): %w",
					f.FileName, m.Status, ErrInvalidMutantReport)
			}
			report.Mutants = append(report.Mutants, Mutant{
				File: f.FileName, Line: m.Line, Column: m.Column, Mutator: m.Type, Status: status,
			})
		}
	}

	return report, nil
}
