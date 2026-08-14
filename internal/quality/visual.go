// Package quality — this file defines the format-agnostic visual-report
// model (SPEC-120 EPIC-calidad S6 D2): VisualReport/VisualTarget/
// ConsoleCounts/A11yResult/A11yViolation/A11yImpact, the VisualReportParser
// interface a project's own harness reporter implements, and the registry
// VisualFormats()/ParseVisualReport() consult. It is the literal mold of
// ProfileParser/registry/Formats/ParseProfile (profile.go, S2 D18) and
// MutantReportParser/mutantRegistry/MutantFormats/ParseMutantReport
// (mutants.go, S5 D2): nothing downstream ever learns which browser harness
// produced a VisualReport — that separation is what lets the visual checks
// (service/quality.go) stay agnostic of Playwright, Cypress, or any future
// tool.
//
// UNLIKE S2 and S5, this spec registers NO native tool format alongside the
// lingua franca (D2): S2's `lcov`/S5's `gremlins` exist because, without
// them, THIS repository could never exercise the coverage/mutation chain at
// all (D17 of the grill). That argument does not apply here — the gap this
// spec cannot close is not a format, it is that mneme itself has no
// graphical interface (D15). Adding a native browser-harness parser would
// not fix that, and every native browser-test-runner report is itself a
// test-RESULT report: it carries no console or accessibility data as a
// first-class citizen, so any harness has to emit visual-v1 through a small
// reporter regardless (D2). `visual-v1` is therefore the lingua franca and,
// today, the ONLY member of the registry.
package quality

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// A11yImpact is the CLOSED vocabulary an accessibility violation's impact
// must be one of (D6) — a value outside this set is ErrInvalidVisualReport,
// enumerating the four accepted values, never a silent "doesn't count"
// tolerance.
type A11yImpact string

const (
	// A11yCritical is axe-core's own top severity: certain to affect users
	// with disabilities.
	A11yCritical A11yImpact = "critical"

	// A11ySerious is highly likely to affect users with disabilities.
	A11ySerious A11yImpact = "serious"

	// A11yModerate may affect some users with disabilities.
	A11yModerate A11yImpact = "moderate"

	// A11yMinor has a minor accessibility impact.
	A11yMinor A11yImpact = "minor"
)

// a11yImpacts is the ordered enumeration A11yImpact's parser and its error
// messages consult — a single source of truth so "which four values" is
// never spelled out twice.
var a11yImpacts = []A11yImpact{A11yCritical, A11ySerious, A11yModerate, A11yMinor}

// ConsoleCounts is the per-severity console tally a target's report declares
// (D5) — ALWAYS recorded, never judged on its own: whether it degrades the
// verdict depends on the project's OWN fail_on_console_error key
// (constitution.go), read by the service layer, never by this package.
type ConsoleCounts struct {
	Error   int
	Warning int
	Info    int
}

// A11yViolation is one accessibility rule violation a target's report
// declares (D6): Rule names the check (verbatim from whatever engine
// produced it), Impact is one of the four A11yImpact constants, and Nodes
// counts how many DOM nodes matched it.
type A11yViolation struct {
	Rule   string
	Impact A11yImpact
	Nodes  int
}

// A11yResult is a target's accessibility block (D6). Reported distinguishes
// "the harness measured accessibility and found nothing" (Reported==true,
// Violations empty) from "the harness never measured it at all"
// (Reported==false) — a distinction D6's own "declarado y no medido = fail"
// rule depends on; it is a FIELD of this model, never inferred later from an
// empty slice, because an empty slice is ambiguous between the two exactly
// the way it would be if this were a plain nil check.
type A11yResult struct {
	Reported      bool
	Engine        string
	EngineVersion string
	Violations    []A11yViolation
}

// VisualTarget is one declared visual objetivo's own verification result
// (D3): ID is the OPAQUE identifier mneme never interprets (a route, a
// theme, a width, a UI state — all doctrine of the PROJECT, never of
// mneme); Rendered/Error are complementary in BOTH directions (D3's own
// rendered=false REQUIRES a non-empty Error, and rendered=true PROHIBITS
// one, AC8); PageErrors lists every uncaught exception or unhandled promise
// rejection the harness observed (D5 — a FACT, never conditioned on any
// constitution key); Console is the per-severity tally; A11y is nil-shaped
// via A11yResult.Reported, never via a nil pointer, so a target that never
// declares the block at all still has a well-formed (if empty) A11yResult.
type VisualTarget struct {
	ID         string
	Rendered   bool
	Error      string
	PageErrors []string
	Console    ConsoleCounts
	A11y       A11yResult
}

// VisualReport is the normalized shape every dialect parser produces — the
// visual-report analogue of Profile (profile.go) and MutantReport
// (mutants.go). Harness/HarnessVersion are informational only (never
// interpreted by mneme, D15) — carried through so a certificate's detail can
// name what produced the evidence.
type VisualReport struct {
	Harness        string
	HarnessVersion string
	Targets        []VisualTarget
}

// VisualReportParser translates one visual-harness dialect's raw bytes into
// the normalized VisualReport model. Adding a format mneme understands is
// exactly this: a type implementing VisualReportParser, plus one entry in
// visualRegistry — nothing else in the package changes.
//
// The format is DECLARED in the constitution and never guessed (D2,
// mirroring D18 of S2 and D2 of S5): a wrong or unrecognised format
// producing a silently-empty report — zero targets, zero failures — is the
// SAME empty-denominator trap this whole EPIC exists to close, now in
// visual-evidence form. Guessing wrong here is worse than most: it would
// look exactly like "the project verified nothing", which is D3's own
// mandatory-scope row's entire reason to exist.
type VisualReportParser interface {
	Parse(data []byte) (*VisualReport, error)
}

// ErrUnknownVisualFormat is returned by ParseVisualReport when format does
// not name a registered VisualReportParser.
var ErrUnknownVisualFormat = errors.New("quality: unknown visual report format")

// ErrInvalidVisualReport is returned when a visual report's bytes do not
// parse as its declared format — including an unrecognised a11y impact, a
// rendered/error pair that violates the complementary rule, a negative
// console count, or a duplicate/empty target id. NEVER returned as an
// empty, zero-target report for an unparseable document: a document with
// ZERO targets is a legitimate, successfully-parsed report (D3's own scope
// row, not this parser, judges that) — but a document that FAILS to parse
// must never be silently reinterpreted as one with no targets.
var ErrInvalidVisualReport = errors.New("quality: invalid visual report")

// visualRegistry is the SINGLE source of truth for which visual-report
// formats mneme understands (D2) — the literal mold of profile.go's
// registry and mutants.go's mutantRegistry. Parse (constitution.go's
// parseVisualSection) validates the constitution's declared
// `[visual].format` key against VisualFormats(), never against a second,
// parallel literal list (AC7).
var visualRegistry = map[string]VisualReportParser{
	"visual-v1": visualV1Parser{},
}

// VisualFormats returns the sorted list of format names ParseVisualReport
// accepts.
func VisualFormats() []string {
	names := make([]string, 0, len(visualRegistry))
	for name := range visualRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ParseVisualReport parses data as the named format, returning
// ErrUnknownVisualFormat when format is not registered rather than guessing
// a dialect from the bytes.
func ParseVisualReport(format string, data []byte) (*VisualReport, error) {
	parser, ok := visualRegistry[format]
	if !ok {
		return nil, fmt.Errorf("quality: visual report format %q: %w", format, ErrUnknownVisualFormat)
	}
	return parser.Parse(data)
}

// visualV1Document is the strict decode target for the visual-v1 lingua
// franca (D2) — every documented key is required except where noted; an
// unknown key anywhere in the document is a parse error, never a silent
// tolerance (the same posture Parse(constitution.go) already applies to the
// constitution, and ParseMutantReport's mutantsV1Parser already applies to
// its own franc format).
type visualV1Document struct {
	Schema         string           `json:"schema"`
	Harness        string           `json:"harness"`
	HarnessVersion string           `json:"harness_version"`
	Targets        []visualV1Target `json:"targets"`
}

type visualV1Target struct {
	ID         *string         `json:"id"`
	Rendered   *bool           `json:"rendered"`
	Error      string          `json:"error"`
	PageErrors []string        `json:"page_errors"`
	Console    visualV1Console `json:"console"`
	A11y       *visualV1A11y   `json:"a11y"`
}

type visualV1Console struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
}

type visualV1A11y struct {
	Engine        *string             `json:"engine"`
	EngineVersion *string             `json:"engine_version"`
	Violations    []visualV1Violation `json:"violations"`
}

type visualV1Violation struct {
	Rule   *string `json:"rule"`
	Impact *string `json:"impact"`
	Nodes  int     `json:"nodes"`
}

// visualV1Schema is the ONLY value visualV1Document.Schema may declare —
// exact, never a prefix match (the same posture mutantsV1Schema already
// establishes).
const visualV1Schema = "visual-v1"

// visualV1Parser implements VisualReportParser for the franc format.
type visualV1Parser struct{}

// Parse implements VisualReportParser. Strict in both directions (D2,
// mirroring mutantsV1Parser's own posture): an unknown JSON key anywhere in
// the document, a wrong or missing `schema`, a missing/duplicate/empty
// target `id`, a `rendered`/`error` pair that violates the complementary
// rule in EITHER direction (AC8), an `impact` outside the four-value
// vocabulary, or a negative console count is ErrInvalidVisualReport, naming
// what is wrong. A document with ZERO targets parses successfully with an
// empty list — that is D3's own scope row's finding to raise, never this
// parser's error (P1 point 2). A target whose `a11y` block is entirely
// absent parses successfully with A11y.Reported==false (P1 point 5) —
// distinct from a present-but-empty violations list, which is
// A11y.Reported==true with zero entries.
func (visualV1Parser) Parse(data []byte) (*VisualReport, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var doc visualV1Document
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("quality: visual-v1: %s: %w", err, ErrInvalidVisualReport)
	}

	if doc.Schema != visualV1Schema {
		return nil, fmt.Errorf("quality: visual-v1: schema %q must be %q: %w", doc.Schema, visualV1Schema, ErrInvalidVisualReport)
	}

	report := &VisualReport{
		Harness: doc.Harness, HarnessVersion: doc.HarnessVersion,
		Targets: make([]VisualTarget, 0, len(doc.Targets)),
	}

	seen := make(map[string]bool, len(doc.Targets))
	for i, t := range doc.Targets {
		if t.ID == nil || *t.ID == "" {
			return nil, fmt.Errorf("quality: visual-v1: target at index %d missing required key %q: %w", i, "id", ErrInvalidVisualReport)
		}
		if seen[*t.ID] {
			return nil, fmt.Errorf("quality: visual-v1: duplicate target id %q: %w", *t.ID, ErrInvalidVisualReport)
		}
		seen[*t.ID] = true

		if t.Rendered == nil {
			return nil, fmt.Errorf("quality: visual-v1: target %q missing required key %q: %w", *t.ID, "rendered", ErrInvalidVisualReport)
		}

		// G7a/G7b: the complementary rule holds in BOTH directions.
		if !*t.Rendered && t.Error == "" {
			return nil, fmt.Errorf("quality: visual-v1: target %q rendered=false requires a non-empty %q: %w", *t.ID, "error", ErrInvalidVisualReport)
		}
		if *t.Rendered && t.Error != "" {
			return nil, fmt.Errorf("quality: visual-v1: target %q rendered=true must not declare %q: %w", *t.ID, "error", ErrInvalidVisualReport)
		}

		if t.Console.Error < 0 || t.Console.Warning < 0 || t.Console.Info < 0 {
			return nil, fmt.Errorf("quality: visual-v1: target %q console counts must be >= 0: %w", *t.ID, ErrInvalidVisualReport)
		}

		target := VisualTarget{
			ID: *t.ID, Rendered: *t.Rendered, Error: t.Error, PageErrors: t.PageErrors,
			Console: ConsoleCounts{Error: t.Console.Error, Warning: t.Console.Warning, Info: t.Console.Info},
		}

		if t.A11y != nil {
			if t.A11y.Engine == nil || *t.A11y.Engine == "" {
				return nil, fmt.Errorf("quality: visual-v1: target %q a11y missing required key %q: %w", *t.ID, "engine", ErrInvalidVisualReport)
			}
			if t.A11y.EngineVersion == nil || *t.A11y.EngineVersion == "" {
				return nil, fmt.Errorf("quality: visual-v1: target %q a11y missing required key %q: %w", *t.ID, "engine_version", ErrInvalidVisualReport)
			}

			violations := make([]A11yViolation, 0, len(t.A11y.Violations))
			for j, v := range t.A11y.Violations {
				if v.Rule == nil || *v.Rule == "" {
					return nil, fmt.Errorf("quality: visual-v1: target %q a11y violation at index %d missing required key %q: %w", *t.ID, j, "rule", ErrInvalidVisualReport)
				}
				if v.Impact == nil {
					return nil, fmt.Errorf("quality: visual-v1: target %q a11y violation at index %d missing required key %q: %w", *t.ID, j, "impact", ErrInvalidVisualReport)
				}
				impact, ok := parseA11yImpact(*v.Impact)
				if !ok {
					return nil, fmt.Errorf(
						"quality: visual-v1: target %q a11y violation impact %q must be one of %v: %w",
						*t.ID, *v.Impact, a11yImpacts, ErrInvalidVisualReport)
				}
				violations = append(violations, A11yViolation{Rule: *v.Rule, Impact: impact, Nodes: v.Nodes})
			}

			target.A11y = A11yResult{
				Reported: true, Engine: *t.A11y.Engine, EngineVersion: *t.A11y.EngineVersion, Violations: violations,
			}
		}

		report.Targets = append(report.Targets, target)
	}

	return report, nil
}

// parseA11yImpact maps a raw impact string into the closed A11yImpact
// vocabulary — a single place both the parser's validation and any future
// caller consult, so the four accepted values are never spelled out twice
// (G6a).
func parseA11yImpact(raw string) (A11yImpact, bool) {
	for _, imp := range a11yImpacts {
		if string(imp) == raw {
			return imp, true
		}
	}
	return "", false
}
