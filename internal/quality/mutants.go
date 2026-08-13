// Package quality — this file defines the format-agnostic mutation-report
// model (SPEC-119 EPIC-calidad S5 D1/D2): MutantStatus/Mutant/MutantReport,
// the MutantReportParser interface every dialect implements, and the
// registry MutantFormats()/ParseMutantReport() consult. It is the literal
// mold of ProfileParser/registry/Formats/ParseProfile (profile.go, S2 D18):
// nothing downstream ever learns which tool produced a MutantReport — that
// separation is what lets the mutation checks (service/quality.go) stay
// agnostic of gremlins, mutants-v1, or any future format.
package quality

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// MutantStatus is the CLOSED vocabulary every registered format's raw
// status is mapped into (D1 pata c). This set — and ONLY this set — is
// what the aritmetica in mutscope.go is allowed to reason about: a format
// whose parser invents a seventh value is a bug in that parser, not a new
// status this package understands.
type MutantStatus string

const (
	// MutantKilled means some test's assertion caught the mutation: the
	// mutated line ran and a test went from green to red because of it.
	// The ONLY status that counts toward the death tally (mutation/score).
	MutantKilled MutantStatus = "killed"

	// MutantLived means the mutation ran and NOTHING caught it: a
	// survivor. Produces its own `mutant/<id>` finding row (D6) — never
	// silently folded into an aggregate count.
	MutantLived MutantStatus = "lived"

	// MutantNotViable means the mutated code did not even compile (or
	// otherwise could never have run at all) — the tool's own signal that
	// this particular mutation was never a valid program in the first
	// place. NEVER counted as a death and NEVER counted as a survivor
	// (D1 pata c): a mutator that folds this into "killed" is exactly the
	// evidence-fabrication trap D1's four legs exist to close, which is
	// why MutantFormats' registry contract (AC6) refuses to admit a format
	// whose vocabulary cannot express this state on its own.
	MutantNotViable MutantStatus = "not_viable"

	// MutantNotCovered means no test executes the mutated line at all —
	// informative only (D10): counted, reported, but never degrades the
	// verdict on its own (mneme already judges test coverage of the diff
	// via S2's own min_diff_line_pct, and turning every uncovered mutable
	// line into a finding here would impose a silent, binary-authored 100%
	// on top of that declared number).
	MutantNotCovered MutantStatus = "not_covered"

	// MutantTimedOut means the mutated run exceeded its budget before a
	// verdict could be observed — neither a death nor a survival (D1 pata
	// c/D10): the suite hung, nobody asserted anything. Firmable via
	// `quality ack`, never via `quality sign` (it is a governance decision,
	// not a technical attestation).
	MutantTimedOut MutantStatus = "timed_out"

	// MutantSkipped is a bookkeeping-only status a format may report for a
	// mutation it chose not to run (e.g. an excluded operator) — counted,
	// nothing else.
	MutantSkipped MutantStatus = "skipped"
)

// ErrUnknownMutantFormat is returned by ParseMutantReport when format does
// not name a registered MutantReportParser. The set of accepted values is
// declared, never guessed (D2, mirroring D18 of S2's ErrUnknownFormat):
// Formats() is the single source of truth the constitution parser
// validates the `[mutation].format` key against.
var ErrUnknownMutantFormat = errors.New("quality: unknown mutation report format")

// ErrInvalidMutantReport is returned when a mutation report's bytes do not
// parse as its declared format, or parse into a status the format's own
// vocabulary does not recognise. NEVER returned as an empty, zero-mutant
// report for an unparseable document (D2): a format change silently
// tolerated as "zero mutants" is the empty-denominator trap in mutation
// form — the same failure D1 pata b exists to close at the registry, not
// just at the arithmetic.
var ErrInvalidMutantReport = errors.New("quality: invalid mutation report")

// Mutant is one mutation a report describes, normalized from whatever
// dialect produced it — the file/line/column identify WHERE the mutation
// landed (used both for the in-diff scoping of mutscope.go and for the
// `mutant/<id>` row's identity), Mutator names WHICH operator/mutation type
// produced it (tool-specific, e.g. "ARITHMETIC_BASE" for gremlins), and
// Status is always one of the six MutantStatus constants above — a parser
// that cannot map a tool status into this vocabulary must fail
// (ErrInvalidMutantReport), never guess.
type Mutant struct {
	// File is the path exactly as the producing tool wrote it —
	// NormalizeSourcePath (measure.go, reused verbatim, D3) reconciles that
	// against the repository's own file list, exactly as it already does
	// for a coverage profile's paths.
	File string

	// Line is the 1-based source line the mutation landed on.
	Line int

	// Column is the 1-based source column — kept for identity (D6's
	// `<path>:<line>:<column>:<mutator>`) even though mneme's own scoping
	// (mutscope.go) only ever reasons about Line.
	Column int

	// Mutator names the mutation operator/type, verbatim from the tool
	// (e.g. gremlins' "ARITHMETIC_BASE") — never normalized across tools:
	// a survivor's row keeps the producing tool's own vocabulary so a
	// human reading it can look the operator up in that tool's own docs.
	Mutator string

	Status MutantStatus
}

// ID returns the survivor row's `name` (D6): `<file>:<line>:<column>:<mutator>`.
// Deterministic and stable across two runs of the SAME informe — it is
// what mneme's own re-scoping (D3, never the tool's own --diff) keys a
// `mutant` row's identity on.
func (m Mutant) ID() string {
	return fmt.Sprintf("%s:%d:%d:%s", m.File, m.Line, m.Column, m.Mutator)
}

// MutantReport is the normalized shape every dialect parser produces —
// the mutation-report analogue of Profile (profile.go).
type MutantReport struct {
	Mutants []Mutant
}

// MutantReportParser translates one mutation-report dialect's raw bytes
// into the normalized MutantReport model.
//
// THE CONTRACT (D1 pata b): a format is only admissible when its tool's own
// vocabulary can express "this mutation never compiled" as a status
// DISTINCT from "a test killed it" — MutantFormats' registry contract test
// (AC6) enforces this mechanically, for every registered format, by
// requiring a fixture whose parse produces at least one MutantNotViable,
// one MutantKilled, and one MutantLived. A mutator whose tool folds
// "didn't compile" into "killed" cannot be registered here: that is
// precisely the evidence a mutation gate exists to never fabricate.
type MutantReportParser interface {
	Parse(data []byte) (*MutantReport, error)
}

// mutantRegistry is the SINGLE source of truth for which mutation-report
// formats mneme understands (D2) — the literal mold of profile.go's
// registry. Parse (constitution.go's parseMutationSection) validates the
// constitution's declared `[mutation].format` key against MutantFormats(),
// never against a second, parallel literal list.
var mutantRegistry = map[string]MutantReportParser{
	"mutants-v1": mutantsV1Parser{},
	"gremlins":   gremlinsParser{},
}

// MutantFormats returns the sorted list of format names ParseMutantReport
// accepts — sorted for the same reason Formats() is (a deterministic order
// for an error message enumerating "valores aceptados").
func MutantFormats() []string {
	names := make([]string, 0, len(mutantRegistry))
	for name := range mutantRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ParseMutantReport parses data as the named format, returning
// ErrUnknownMutantFormat when format is not registered rather than
// guessing a dialect from the bytes.
func ParseMutantReport(format string, data []byte) (*MutantReport, error) {
	parser, ok := mutantRegistry[format]
	if !ok {
		return nil, fmt.Errorf("quality: mutation report format %q: %w", format, ErrUnknownMutantFormat)
	}
	return parser.Parse(data)
}

// mutantsV1Document is the strict decode target for the mutants-v1 lingua
// franca — the franc format any tool in any language can produce with a
// twenty-line adapter (D2). Every key documented here is required; an
// unknown key or an unrecognised status is a parse error, never a silent
// tolerance (the same posture Parse(constitution.go) already applies to
// the constitution itself).
type mutantsV1Document struct {
	Schema  string            `json:"schema"`
	Mutants []mutantsV1Mutant `json:"mutants"`
}

type mutantsV1Mutant struct {
	File    *string `json:"file"`
	Line    *int    `json:"line"`
	Column  int     `json:"column"`
	Mutator string  `json:"mutator"`
	Status  *string `json:"status"`
}

// mutantsV1Schema is the ONLY value mutantsV1Document.Schema may declare —
// exact, never a prefix match: a franc producer bumping its own schema
// must be a deliberate, visible change on mneme's side, not a silent
// reinterpretation.
const mutantsV1Schema = "mutants-v1"

// mutantsV1StatusMap is the exhaustive, closed mapping from the franc
// format's own status strings to MutantStatus — deliberately spelled the
// same as the enum's own values (this format IS mneme's vocabulary,
// verbatim) so the enumeration below is also what an adapter author reads
// as "these are the six values I may write".
var mutantsV1StatusMap = map[string]MutantStatus{
	"killed":      MutantKilled,
	"lived":       MutantLived,
	"not_viable":  MutantNotViable,
	"not_covered": MutantNotCovered,
	"timed_out":   MutantTimedOut,
	"skipped":     MutantSkipped,
}

// mutantsV1Parser implements MutantReportParser for the franc format.
type mutantsV1Parser struct{}

// Parse implements MutantReportParser. Strict in both directions (D2,
// mirroring Parse's own posture for the constitution): an unknown JSON key
// anywhere in the document, a wrong or missing `schema`, a missing
// `file`/`line`, or a `status` outside the six-value vocabulary is
// ErrInvalidMutantReport, naming what is wrong. A document with ZERO
// mutants parses successfully with an empty list — that is not this
// parser's error to raise; the acotado (mutscope.go via the service layer)
// is what turns "nothing in the diff" into a finding (D1 pata b's own
// carve-out, P1 point 3).
func (mutantsV1Parser) Parse(data []byte) (*MutantReport, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var doc mutantsV1Document
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("quality: mutants-v1: %s: %w", err, ErrInvalidMutantReport)
	}

	if doc.Schema != mutantsV1Schema {
		return nil, fmt.Errorf("quality: mutants-v1: schema %q must be %q: %w", doc.Schema, mutantsV1Schema, ErrInvalidMutantReport)
	}

	report := &MutantReport{Mutants: make([]Mutant, 0, len(doc.Mutants))}
	for i, m := range doc.Mutants {
		if m.File == nil || *m.File == "" {
			return nil, fmt.Errorf("quality: mutants-v1: mutant at index %d missing required key %q: %w", i, "file", ErrInvalidMutantReport)
		}
		if m.Line == nil {
			return nil, fmt.Errorf("quality: mutants-v1: mutant at index %d missing required key %q: %w", i, "line", ErrInvalidMutantReport)
		}
		if m.Status == nil {
			return nil, fmt.Errorf("quality: mutants-v1: mutant at index %d missing required key %q: %w", i, "status", ErrInvalidMutantReport)
		}
		status, ok := mutantsV1StatusMap[*m.Status]
		if !ok {
			return nil, fmt.Errorf(
				"quality: mutants-v1: mutant at index %d status %q must be one of killed|lived|not_viable|not_covered|timed_out|skipped: %w",
				i, *m.Status, ErrInvalidMutantReport)
		}
		report.Mutants = append(report.Mutants, Mutant{
			File: *m.File, Line: *m.Line, Column: m.Column, Mutator: m.Mutator, Status: status,
		})
	}

	return report, nil
}
