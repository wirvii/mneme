package acceptance

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"

	"github.com/pelletier/go-toml/v2"
)

// SchemaVersion is the only schema_version Parse accepts. It does NOT rise
// past 1 (unlike the retired evaluator's schema, which only ever widened):
// criteria.toml lives in the local workflow directory, never travels by git
// to another mneme, and this reader's whole job is "still accept every
// document the old evaluator accepted" — there is nothing for a schema bump
// to widen.
const SchemaVersion = 1

// ErrInvalid is returned when a criteria document is missing a required
// key, declares an unknown key, or fails a structural validation rule
// (duplicate id, id pattern, empty text).
var ErrInvalid = errors.New("acceptance: invalid criteria document")

// ErrUnsupportedSchema is returned when a criteria document declares a
// schema_version this reader does not recognise.
var ErrUnsupportedSchema = errors.New("acceptance: criteria schema_version unsupported")

// criterionIDPattern is the shape every criterion id must have — unchanged
// from the retired evaluator's own pattern, since existing criteria.toml
// documents already rely on it.
var criterionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

// Criterion is one parsed `[[criterion]]` entry: only the two fields this
// reader still cares about. Every other legacy key (mode, command, timeout,
// evidence_required, [[criterion.assert]]) is accepted at parse time and
// silently discarded — mneme no longer evaluates any of it.
type Criterion struct {
	ID   string
	Text string
}

// Doc is the parsed form of a spec's criteria.toml.
type Doc struct {
	SchemaVersion int
	Criteria      []Criterion
}

// rawDoc is the lenient decode target: DisallowUnknownFields still applies
// (a typo must explode, never silently govern nothing), but every
// mode-specific key a legacy document might carry is declared here so it
// parses without error and is then discarded.
type rawDoc struct {
	SchemaVersion *int           `toml:"schema_version"`
	Criteria      []rawCriterion `toml:"criterion"`
}

// rawCriterion mirrors every key ANY of the retired evaluator's three modes
// could declare. Types are `any` for everything but id/text: this reader
// never inspects their shape, only that they decode without an unknown-field
// error.
type rawCriterion struct {
	ID               string           `toml:"id"`
	Text             string           `toml:"text"`
	Mode             any              `toml:"mode"`
	Command          any              `toml:"command"`
	Timeout          any              `toml:"timeout"`
	EvidenceRequired any              `toml:"evidence_required"`
	Assert           []map[string]any `toml:"assert"`
}

// Parse decodes and structurally validates raw TOML bytes into a Doc.
// schema_version must be present and equal to SchemaVersion; at least one
// [[criterion]] must be declared; every criterion needs a non-empty id
// (matching the same pattern the retired evaluator enforced, and unique
// within the document) and a non-empty text — deliberately without
// strings.TrimSpace, so a document the old strict parser accepted (which
// only checked for the empty string too) still parses identically here.
// Any other unknown key, at the document's top level or within a criterion,
// is rejected: a typo must still explode, never silently govern nothing.
func Parse(data []byte) (*Doc, error) {
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var raw rawDoc
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("acceptance: parse criteria: %s: %w", err, ErrInvalid)
	}

	if raw.SchemaVersion == nil {
		return nil, fmt.Errorf("acceptance: missing required key %q: %w", "schema_version", ErrInvalid)
	}
	if *raw.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf(
			"acceptance: criteria schema_version %d not supported (only %d): %w",
			*raw.SchemaVersion, SchemaVersion, ErrUnsupportedSchema)
	}

	if len(raw.Criteria) == 0 {
		return nil, fmt.Errorf("acceptance: criteria document declares zero [[criterion]] entries: %w", ErrInvalid)
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

	return &Doc{SchemaVersion: *raw.SchemaVersion, Criteria: criteria}, nil
}

// parseCriterion validates one [[criterion]] entry's id/text — every other
// key was already accepted (or rejected, if unknown) by the TOML decode
// itself and is discarded here.
func parseCriterion(idx int, raw rawCriterion, seen map[string]bool) (Criterion, error) {
	if raw.ID == "" {
		return Criterion{}, fmt.Errorf("acceptance: criterion at index %d: missing required key %q: %w", idx, "id", ErrInvalid)
	}
	if !criterionIDPattern.MatchString(raw.ID) {
		return Criterion{}, fmt.Errorf("acceptance: criterion id %q must match %s: %w", raw.ID, criterionIDPattern.String(), ErrInvalid)
	}
	if seen[raw.ID] {
		return Criterion{}, fmt.Errorf("acceptance: duplicate criterion id %q: %w", raw.ID, ErrInvalid)
	}
	seen[raw.ID] = true

	if raw.Text == "" {
		return Criterion{}, fmt.Errorf("acceptance: criterion %q: missing required key %q: %w", raw.ID, "text", ErrInvalid)
	}

	return Criterion{ID: raw.ID, Text: raw.Text}, nil
}
