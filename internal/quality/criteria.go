// Package quality — this file implements ParseCriteria (SPEC-117 EPIC-calidad
// S3 D1/D2/D3): the strict parser for a spec's criteria.toml, the document
// that replaces a prose acceptance criterion with a closed vocabulary of four
// verbs mneme itself evaluates. Same mould as Parse (constitution.go):
// DisallowUnknownFields, every documented key required, and every failure
// names the offending key or value (D2) — a typo in criteria.toml must
// explode, not silently govern nothing (the SPEC-087 AC12 scar, again).
package quality

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/pelletier/go-toml/v2"
)

// CriteriaSchemaVersion is the only schema_version ParseCriteria accepts
// today (D2). Unlike the constitution's own schema, S3 introduces
// criteria.toml from scratch — there is no earlier version to remain
// compatible with, so this is a single accepted value, not a range.
const CriteriaSchemaVersion = 1

// ErrInvalidCriteria is returned by ParseCriteria when the document is
// missing a required key, declares an unknown key, or fails a per-field or
// per-mode validation rule. Every failure names the offending key, id, or
// value in the wrapped message (D2).
var ErrInvalidCriteria = fmt.Errorf("quality: invalid criteria document")

// ErrUnsupportedCriteriaSchema is returned when criteria.toml declares a
// schema_version ParseCriteria does not recognise — the document was
// written by a different mneme version. Distinct from ErrInvalidCriteria
// because the remedy differs: upgrade mneme, not fix a typo.
var ErrUnsupportedCriteriaSchema = fmt.Errorf("quality: criteria schema_version unsupported")

// criterionIDPattern is the shape every criterion id must have — safe-slug
// adjacent, but allowing dots and underscores since ids commonly echo a
// spec's own "AC7" style naming (D2).
var criterionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

// Verb is the closed vocabulary of assertions ParseCriteria accepts (D3).
// Every verb is a pure function of the tree contents at a git ref — no
// checkout, no worktree, no build.
type Verb string

const (
	// VerbFileExists is satisfied when Path exists in the ref's tree.
	VerbFileExists Verb = "file_exists"

	// VerbPatternCount is satisfied when the number of LINES containing
	// Contains across the files matched by In satisfies Comparator Count.
	VerbPatternCount Verb = "pattern_count"

	// VerbSymbolDefined is satisfied when Symbol appears as a whole word in
	// at least one file matched by In.
	VerbSymbolDefined Verb = "symbol_defined"

	// VerbSymbolReferenced is satisfied when at least one file NOT matching
	// DefinedIn or Ignore contains Symbol as a whole word — the "only test
	// files use this" detector.
	VerbSymbolReferenced Verb = "symbol_referenced"
)

// Comparator is the closed set pattern_count accepts for comparing its
// measured count against Count.
type Comparator string

const (
	ComparatorGTE Comparator = ">="
	ComparatorLTE Comparator = "<="
	ComparatorEQ  Comparator = "=="
)

// Mode is the closed set a criterion declares itself as (D2) — deliberately
// explicit rather than inferred from which keys are present: inferring would
// turn an author's typo/omission into a silent change of semantics, exactly
// the pattern this whole EPIC exists to eliminate.
type Mode string

const (
	// ModeAssert evaluates a closed-vocabulary Assert list against HEAD and
	// the spec's base commit (D5's vacuity rule).
	ModeAssert Mode = "assert"

	// ModeCommand is the free-command escape hatch: runs once, against HEAD
	// only, through the injected Runner (D6). Always "improbable" to prove
	// vacuous when it passes.
	ModeCommand Mode = "command"

	// ModeManual requires a qa-tester's signature, with evidence, before it
	// can pass (D14 of the grill).
	ModeManual Mode = "manual"
)

// Assertion is one parsed `[[criterion.assert]]` entry. Only the fields
// relevant to Verb are populated — ParseCriteria rejects any document that
// declares a key Verb does not use, so a zero value in an unused field is
// never ambiguous with "declared but empty" (the shape Parse's own
// rawCoverage/rawRatchet already establish for cross-field validation).
type Assertion struct {
	Verb Verb

	// Path is VerbFileExists's anchor: the tree-relative path that must
	// exist.
	Path string

	// Contains is VerbPatternCount's literal search string (never a regex —
	// D3's `-F` decision).
	Contains string

	// In is the doublestar glob set VerbPatternCount/VerbSymbolDefined
	// filter git's output against (D4) — never passed to git itself.
	In []string

	// Word, when true, requires Contains/Symbol to match as a whole word
	// (`-w`) — load-bearing for VerbSymbolDefined/VerbSymbolReferenced,
	// declared explicitly for VerbPatternCount too (D2).
	Word bool

	Comparator Comparator
	Count      int

	// Symbol is VerbSymbolDefined/VerbSymbolReferenced's identifier,
	// matched as a whole word.
	Symbol string

	// DefinedIn is VerbSymbolReferenced's anchor: files considered the
	// symbol's own declaration, excluded from the "who references it" scan.
	DefinedIn []string

	// Ignore is VerbSymbolReferenced's additional exclusion (typically test
	// files) — NOT an anchor (D7): an `ignore` glob matching nothing is
	// harmless, so it is never checked by ValidateAnchors.
	Ignore []string

	// New declares whether this assertion's ANCHOR (Path, or the globs in
	// In/DefinedIn) is a promise to create something that does not exist yet
	// (true) or a claim about something that already exists (false, D7).
	// Never about the searched-for Contains/Symbol itself.
	New bool
}

// Criterion is one parsed `[[criterion]]` entry.
type Criterion struct {
	ID   string
	Mode Mode

	// Text is the normative prose a report prints — the criterion's actual
	// acceptance language, not a derived summary (D1).
	Text string

	// Assert holds ModeAssert's assertions — nil for the other two modes.
	Assert []Assertion

	// Command holds ModeCommand's argv vector — nil for the other two
	// modes. Validated identically to a gate's own command
	// (argvShellStringProblem, D2/AC7).
	Command []string

	// Timeout bounds ModeCommand's execution.
	Timeout time.Duration

	// EvidenceRequired names what a qa-tester's signature must attach for
	// ModeManual — empty for the other two modes.
	EvidenceRequired string
}

// CriteriaDoc is the parsed, validated form of a spec's criteria.toml.
type CriteriaDoc struct {
	SchemaVersion int
	Criteria      []Criterion
}

// rawCriteriaDoc is the strict decode target for ParseCriteria.
type rawCriteriaDoc struct {
	SchemaVersion *int           `toml:"schema_version"`
	Criteria      []rawCriterion `toml:"criterion"`
}

// rawCriterion mirrors Criterion with pointers where presence must be
// distinguished from an explicit zero value (D2's "no defaults in the
// binary" doctrine, mirroring rawConstitution's own convention).
type rawCriterion struct {
	ID               string         `toml:"id"`
	Mode             string         `toml:"mode"`
	Text             string         `toml:"text"`
	Assert           []rawAssertion `toml:"assert"`
	Command          *[]string      `toml:"command"`
	Timeout          *string        `toml:"timeout"`
	EvidenceRequired *string        `toml:"evidence_required"`
}

// rawAssertion is the union of every key ANY verb might declare — TOML's
// DisallowUnknownFields requires one shared struct for every
// `[[criterion.assert]]` entry regardless of its verb; per-verb
// required/prohibited validation happens in Go, in parseAssertion's four
// verb-specific helpers.
type rawAssertion struct {
	Verb       *string   `toml:"verb"`
	Path       *string   `toml:"path"`
	Contains   *string   `toml:"contains"`
	In         *[]string `toml:"in"`
	Word       *bool     `toml:"word"`
	Comparator *string   `toml:"comparator"`
	Count      *int      `toml:"count"`
	Symbol     *string   `toml:"symbol"`
	DefinedIn  *[]string `toml:"defined_in"`
	Ignore     *[]string `toml:"ignore"`
	New        *bool     `toml:"new"`
}

// acceptedModes is used both to validate an unknown `mode` value and to
// enumerate the accepted set in the error message (AC5).
var acceptedModes = []Mode{ModeAssert, ModeCommand, ModeManual}

// acceptedComparators enumerates pattern_count's accepted comparator set,
// reused both for validation and for the error message.
var acceptedComparators = []Comparator{ComparatorGTE, ComparatorLTE, ComparatorEQ}

// acceptedVerbs enumerates the closed vocabulary, reused both for
// validation and for the error message.
var acceptedVerbs = []Verb{VerbFileExists, VerbPatternCount, VerbSymbolDefined, VerbSymbolReferenced}

// ParseCriteria decodes and validates raw TOML bytes into a CriteriaDoc. It
// is as strict as constitution.Parse (D2): DisallowUnknownFields, every
// documented key required, mode declared (never inferred) and its
// cross-validation enforced in BOTH directions — what it requires and what
// it prohibits (AC6) — and a document with zero criteria is rejected: an
// empty document that parses is a silent green (D2).
func ParseCriteria(data []byte) (*CriteriaDoc, error) {
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var raw rawCriteriaDoc
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("quality: parse criteria: %s: %w", err, ErrInvalidCriteria)
	}

	if raw.SchemaVersion == nil {
		return nil, fmt.Errorf("quality: missing required key %q: %w", "schema_version", ErrInvalidCriteria)
	}
	if *raw.SchemaVersion != CriteriaSchemaVersion {
		return nil, fmt.Errorf(
			"quality: criteria schema_version %d escrito por una mneme mas nueva/vieja (solo se acepta %d): %w",
			*raw.SchemaVersion, CriteriaSchemaVersion, ErrUnsupportedCriteriaSchema)
	}

	if len(raw.Criteria) == 0 {
		return nil, fmt.Errorf("quality: criteria document declares zero [[criterion]] entries: %w", ErrInvalidCriteria)
	}

	seen := make(map[string]bool, len(raw.Criteria))
	criteria := make([]Criterion, 0, len(raw.Criteria))
	for i, rc := range raw.Criteria {
		c, err := parseCriterion(i, rc, seen)
		if err != nil {
			return nil, err
		}
		criteria = append(criteria, c)
	}

	return &CriteriaDoc{SchemaVersion: *raw.SchemaVersion, Criteria: criteria}, nil
}

// parseCriterion validates one [[criterion]] entry: id (required, pattern,
// unique), text (required), mode (required, closed set), and mode's
// cross-validation in both directions (AC6) — what it requires and what it
// prohibits.
func parseCriterion(idx int, raw rawCriterion, seen map[string]bool) (Criterion, error) {
	if raw.ID == "" {
		return Criterion{}, fmt.Errorf("quality: criterion at index %d: missing required key %q: %w", idx, "id", ErrInvalidCriteria)
	}
	if !criterionIDPattern.MatchString(raw.ID) {
		return Criterion{}, fmt.Errorf("quality: criterion id %q must match %s: %w", raw.ID, criterionIDPattern.String(), ErrInvalidCriteria)
	}
	if seen[raw.ID] {
		return Criterion{}, fmt.Errorf("quality: duplicate criterion id %q: %w", raw.ID, ErrInvalidCriteria)
	}
	seen[raw.ID] = true

	if raw.Text == "" {
		return Criterion{}, fmt.Errorf("quality: criterion %q: missing required key %q: %w", raw.ID, "text", ErrInvalidCriteria)
	}

	if raw.Mode == "" {
		return Criterion{}, fmt.Errorf("quality: criterion %q: missing required key %q: %w", raw.ID, "mode", ErrInvalidCriteria)
	}
	mode := Mode(raw.Mode)
	switch mode {
	case ModeAssert, ModeCommand, ModeManual:
	default:
		return Criterion{}, fmt.Errorf("quality: criterion %q: mode %q must be one of %v: %w", raw.ID, raw.Mode, acceptedModes, ErrInvalidCriteria)
	}

	hasCommand := raw.Command != nil && len(*raw.Command) > 0
	hasTimeout := raw.Timeout != nil && *raw.Timeout != ""
	hasEvidence := raw.EvidenceRequired != nil && *raw.EvidenceRequired != ""
	hasAssert := len(raw.Assert) > 0

	crit := Criterion{ID: raw.ID, Mode: mode, Text: raw.Text}

	switch mode {
	case ModeAssert:
		if !hasAssert {
			return Criterion{}, fmt.Errorf("quality: criterion %q: mode %q requires at least one [[criterion.assert]]: %w", raw.ID, mode, ErrInvalidCriteria)
		}
		if hasCommand || hasTimeout {
			return Criterion{}, fmt.Errorf("quality: criterion %q: mode %q must not declare command/timeout: %w", raw.ID, mode, ErrInvalidCriteria)
		}
		if hasEvidence {
			return Criterion{}, fmt.Errorf("quality: criterion %q: mode %q must not declare evidence_required: %w", raw.ID, mode, ErrInvalidCriteria)
		}
		asserts := make([]Assertion, 0, len(raw.Assert))
		for i, ra := range raw.Assert {
			a, err := parseAssertion(raw.ID, i, ra)
			if err != nil {
				return Criterion{}, err
			}
			asserts = append(asserts, a)
		}
		crit.Assert = asserts

	case ModeCommand:
		if hasAssert {
			return Criterion{}, fmt.Errorf("quality: criterion %q: mode %q must not declare [[criterion.assert]]: %w", raw.ID, mode, ErrInvalidCriteria)
		}
		if hasEvidence {
			return Criterion{}, fmt.Errorf("quality: criterion %q: mode %q must not declare evidence_required: %w", raw.ID, mode, ErrInvalidCriteria)
		}
		if !hasCommand {
			return Criterion{}, fmt.Errorf("quality: criterion %q: missing required key %q: %w", raw.ID, "command", ErrInvalidCriteria)
		}
		// The SAME shared validator a gate's command and coverage.command
		// use (AC7) — one place this explanatory sentence is written, so
		// the three can never drift apart.
		if msg, bad := argvShellStringProblem(*raw.Command); bad {
			return Criterion{}, fmt.Errorf("quality: criterion %q %s: %w", raw.ID, msg, ErrInvalidCriteria)
		}
		if !hasTimeout {
			return Criterion{}, fmt.Errorf("quality: criterion %q: missing required key %q: %w", raw.ID, "timeout", ErrInvalidCriteria)
		}
		dur, err := time.ParseDuration(*raw.Timeout)
		if err != nil || dur <= 0 {
			return Criterion{}, fmt.Errorf("quality: criterion %q: timeout %q must be a positive parseable duration: %w", raw.ID, *raw.Timeout, ErrInvalidCriteria)
		}
		crit.Command = *raw.Command
		crit.Timeout = dur

	case ModeManual:
		if hasAssert {
			return Criterion{}, fmt.Errorf("quality: criterion %q: mode %q must not declare [[criterion.assert]]: %w", raw.ID, mode, ErrInvalidCriteria)
		}
		if hasCommand || hasTimeout {
			return Criterion{}, fmt.Errorf("quality: criterion %q: mode %q must not declare command/timeout: %w", raw.ID, mode, ErrInvalidCriteria)
		}
		if !hasEvidence {
			return Criterion{}, fmt.Errorf("quality: criterion %q: missing required key %q: %w", raw.ID, "evidence_required", ErrInvalidCriteria)
		}
		crit.EvidenceRequired = *raw.EvidenceRequired
	}

	return crit, nil
}

// parseAssertion dispatches to the verb-specific parser — verb is required
// and closed (AC5).
func parseAssertion(critID string, idx int, raw rawAssertion) (Assertion, error) {
	if raw.Verb == nil || *raw.Verb == "" {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "verb", ErrInvalidCriteria)
	}
	switch Verb(*raw.Verb) {
	case VerbFileExists:
		return parseFileExistsAssertion(critID, idx, raw)
	case VerbPatternCount:
		return parsePatternCountAssertion(critID, idx, raw)
	case VerbSymbolDefined:
		return parseSymbolDefinedAssertion(critID, idx, raw)
	case VerbSymbolReferenced:
		return parseSymbolReferencedAssertion(critID, idx, raw)
	default:
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: verb %q must be one of %v: %w",
			critID, idx, *raw.Verb, acceptedVerbs, ErrInvalidCriteria)
	}
}

// prohibitKeys returns an error naming the first (alphabetically) key that
// present marks true — verb-specific parsers use this to reject the keys
// their verb does not use, so a stray `symbol` on a file_exists assertion
// explodes instead of being silently ignored.
func prohibitKeys(critID string, idx int, verb Verb, present map[string]bool) error {
	keys := make([]string, 0, len(present))
	for k := range present {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if present[k] {
			return fmt.Errorf("quality: criterion %q assert[%d]: verb %q must not declare %q: %w", critID, idx, verb, k, ErrInvalidCriteria)
		}
	}
	return nil
}

// validateGlobs validates doublestar syntax for every pattern in globs,
// naming keyName and the offending pattern on failure (parse-time only —
// whether a glob MATCHES anything is ValidateAnchors's job, not this one).
func validateGlobs(critID string, idx int, keyName string, globs []string) error {
	for _, g := range globs {
		if _, err := doublestar.Match(g, "probe"); err != nil {
			return fmt.Errorf("quality: criterion %q assert[%d]: %s pattern %q invalid: %s: %w", critID, idx, keyName, g, err, ErrInvalidCriteria)
		}
	}
	return nil
}

// parseFileExistsAssertion validates a file_exists assertion: path and new
// required; every other key prohibited.
func parseFileExistsAssertion(critID string, idx int, raw rawAssertion) (Assertion, error) {
	if err := prohibitKeys(critID, idx, VerbFileExists, map[string]bool{
		"contains": raw.Contains != nil, "in": raw.In != nil, "word": raw.Word != nil,
		"comparator": raw.Comparator != nil, "count": raw.Count != nil,
		"symbol": raw.Symbol != nil, "defined_in": raw.DefinedIn != nil, "ignore": raw.Ignore != nil,
	}); err != nil {
		return Assertion{}, err
	}
	if raw.Path == nil || *raw.Path == "" {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "path", ErrInvalidCriteria)
	}
	if raw.New == nil {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "new", ErrInvalidCriteria)
	}
	return Assertion{Verb: VerbFileExists, Path: *raw.Path, New: *raw.New}, nil
}

// parsePatternCountAssertion validates a pattern_count assertion: contains,
// in, word, comparator, count, new required; path/symbol/defined_in/ignore
// prohibited.
func parsePatternCountAssertion(critID string, idx int, raw rawAssertion) (Assertion, error) {
	if err := prohibitKeys(critID, idx, VerbPatternCount, map[string]bool{
		"path": raw.Path != nil, "symbol": raw.Symbol != nil,
		"defined_in": raw.DefinedIn != nil, "ignore": raw.Ignore != nil,
	}); err != nil {
		return Assertion{}, err
	}
	if raw.Contains == nil || *raw.Contains == "" {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "contains", ErrInvalidCriteria)
	}
	if raw.In == nil || len(*raw.In) == 0 {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "in", ErrInvalidCriteria)
	}
	if err := validateGlobs(critID, idx, "in", *raw.In); err != nil {
		return Assertion{}, err
	}
	if raw.Word == nil {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "word", ErrInvalidCriteria)
	}
	if raw.Comparator == nil {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "comparator", ErrInvalidCriteria)
	}
	cmp := Comparator(*raw.Comparator)
	switch cmp {
	case ComparatorGTE, ComparatorLTE, ComparatorEQ:
	default:
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: comparator %q must be one of %v: %w",
			critID, idx, *raw.Comparator, acceptedComparators, ErrInvalidCriteria)
	}
	if raw.Count == nil {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "count", ErrInvalidCriteria)
	}
	if *raw.Count < 0 {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: count %d must be >= 0: %w", critID, idx, *raw.Count, ErrInvalidCriteria)
	}
	if raw.New == nil {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "new", ErrInvalidCriteria)
	}
	return Assertion{
		Verb: VerbPatternCount, Contains: *raw.Contains, In: *raw.In, Word: *raw.Word,
		Comparator: cmp, Count: *raw.Count, New: *raw.New,
	}, nil
}

// parseSymbolDefinedAssertion validates a symbol_defined assertion: symbol,
// in, new required; every other key prohibited.
func parseSymbolDefinedAssertion(critID string, idx int, raw rawAssertion) (Assertion, error) {
	if err := prohibitKeys(critID, idx, VerbSymbolDefined, map[string]bool{
		"path": raw.Path != nil, "contains": raw.Contains != nil, "word": raw.Word != nil,
		"comparator": raw.Comparator != nil, "count": raw.Count != nil,
		"defined_in": raw.DefinedIn != nil, "ignore": raw.Ignore != nil,
	}); err != nil {
		return Assertion{}, err
	}
	if raw.Symbol == nil || *raw.Symbol == "" {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "symbol", ErrInvalidCriteria)
	}
	if raw.In == nil || len(*raw.In) == 0 {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "in", ErrInvalidCriteria)
	}
	if err := validateGlobs(critID, idx, "in", *raw.In); err != nil {
		return Assertion{}, err
	}
	if raw.New == nil {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "new", ErrInvalidCriteria)
	}
	return Assertion{Verb: VerbSymbolDefined, Symbol: *raw.Symbol, In: *raw.In, New: *raw.New}, nil
}

// parseSymbolReferencedAssertion validates a symbol_referenced assertion:
// symbol, defined_in, ignore, new required; every other key prohibited.
// ignore may be an explicitly empty list (present, but nothing to exclude)
// — only its ABSENCE is rejected, mirroring coverage.exclude's own
// convention.
func parseSymbolReferencedAssertion(critID string, idx int, raw rawAssertion) (Assertion, error) {
	if err := prohibitKeys(critID, idx, VerbSymbolReferenced, map[string]bool{
		"path": raw.Path != nil, "contains": raw.Contains != nil, "in": raw.In != nil,
		"word": raw.Word != nil, "comparator": raw.Comparator != nil, "count": raw.Count != nil,
	}); err != nil {
		return Assertion{}, err
	}
	if raw.Symbol == nil || *raw.Symbol == "" {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "symbol", ErrInvalidCriteria)
	}
	if raw.DefinedIn == nil || len(*raw.DefinedIn) == 0 {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "defined_in", ErrInvalidCriteria)
	}
	if err := validateGlobs(critID, idx, "defined_in", *raw.DefinedIn); err != nil {
		return Assertion{}, err
	}
	if raw.Ignore == nil {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "ignore", ErrInvalidCriteria)
	}
	if err := validateGlobs(critID, idx, "ignore", *raw.Ignore); err != nil {
		return Assertion{}, err
	}
	if raw.New == nil {
		return Assertion{}, fmt.Errorf("quality: criterion %q assert[%d]: missing required key %q: %w", critID, idx, "new", ErrInvalidCriteria)
	}
	return Assertion{
		Verb: VerbSymbolReferenced, Symbol: *raw.Symbol, DefinedIn: *raw.DefinedIn, Ignore: *raw.Ignore, New: *raw.New,
	}, nil
}

// ValidateAnchors implements D7's declare-time half: for every assertion
// whose New is false, its ANCHOR — file_exists's Path, or the glob set in
// pattern_count/symbol_defined's In or symbol_referenced's DefinedIn — must
// resolve against repoFiles, the caller's snapshot of the working tree
// (never read from disk here: this function is pure, so the four anchor
// cases can be covered with tables instead of a fixture working tree per
// row). New == true anchors are never checked here: they are a promise to
// create something that does not exist yet, verified instead at `verify`
// time against the base commit (D7 point 2, anchor-not-new).
func ValidateAnchors(doc *CriteriaDoc, repoFiles []string) error {
	fileSet := make(map[string]bool, len(repoFiles))
	for _, f := range repoFiles {
		fileSet[f] = true
	}

	for _, c := range doc.Criteria {
		if c.Mode != ModeAssert {
			continue
		}
		for i, a := range c.Assert {
			if a.New {
				continue
			}
			switch a.Verb {
			case VerbFileExists:
				if !fileSet[a.Path] {
					return fmt.Errorf(
						"quality: criterion %q assert[%d]: new=false but path %q does not exist in the working tree: %w",
						c.ID, i, a.Path, ErrInvalidCriteria)
				}
			case VerbPatternCount, VerbSymbolDefined:
				if !anyGlobMatchesAny(a.In, repoFiles) {
					return fmt.Errorf(
						"quality: criterion %q assert[%d]: new=false but in %v matches no file in the working tree: %w",
						c.ID, i, a.In, ErrInvalidCriteria)
				}
			case VerbSymbolReferenced:
				if !anyGlobMatchesAny(a.DefinedIn, repoFiles) {
					return fmt.Errorf(
						"quality: criterion %q assert[%d]: new=false but defined_in %v matches no file in the working tree: %w",
						c.ID, i, a.DefinedIn, ErrInvalidCriteria)
				}
			}
		}
	}
	return nil
}

// anyGlobMatchesAny reports whether at least one of globs matches at least
// one of files — the "does this anchor resolve to something real" test
// ValidateAnchors needs for its glob-shaped anchors.
func anyGlobMatchesAny(globs, files []string) bool {
	for _, g := range globs {
		for _, f := range files {
			if ok, _ := doublestar.Match(g, f); ok {
				return true
			}
		}
	}
	return false
}
