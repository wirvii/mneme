// Package quality — this file implements the strict parser for a spec's
// budget.toml (SPEC-118 EPIC-calidad S4 D2/D3): the document the architect
// declares via spec_doc_write kind "budget", describing how much a change is
// allowed to cost against the code graph — a nominal contract for existing
// symbols and a per-directory quota for new ones (D4 of the grill), with a
// single margin covering both halves (D4 of this spec).
//
// Same mould as Parse (constitution.go) and ParseCriteria (criteria.go):
// DisallowUnknownFields, every documented key required, and every failure
// names the offending key or value — a typo in budget.toml must explode, not
// silently govern nothing (the SPEC-087 AC12 scar, once more).
//
// Symbol is also defined here, ahead of its own file (symbols.go, which adds
// SymbolRef/BudgetedKinds/the delta machinery): ValidateBudgetAnchors needs
// it to resolve a [[modify]] entry's symbol against the working tree, and
// budget.go lands before symbols.go in the implementation plan. This is a
// deliberate, minor reordering relative to the plan's own file-per-step
// listing — see the delivered changes document for why.
package quality

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/pelletier/go-toml/v2"
)

// BudgetSchemaVersion is the only schema_version ParseBudget accepts today —
// S4 introduces budget.toml from scratch, so there is no earlier version to
// remain compatible with (mirroring CriteriaSchemaVersion's own posture).
const BudgetSchemaVersion = 1

// ErrInvalidBudget is returned by ParseBudget when the document is missing a
// required key, declares an unknown key, or fails a per-field validation
// rule. Every failure names the offending key or value in the wrapped
// message.
var ErrInvalidBudget = fmt.Errorf("quality: invalid budget document")

// ErrUnsupportedBudgetSchema is returned when budget.toml declares a
// schema_version ParseBudget does not recognise.
var ErrUnsupportedBudgetSchema = fmt.Errorf("quality: budget schema_version unsupported")

// Symbol is the leaf package's own, flat representation of a code symbol —
// never codegraph.Node (D15 of the design: this is what keeps internal/
// quality a leaf with zero internal/* imports while still holding the full
// decision logic). Key is the comparison identity DiffSymbols uses (the same
// (File, QualifiedName) pair codegraph.NodeID hashes, V4).
type Symbol struct {
	// Key is File + ":" + QualifiedName — the exact pair NodeID is a sha256
	// of (V4), so the same symbol in two commits always produces the same
	// Key.
	Key string

	// Name is the short, unqualified symbol name.
	Name string

	// QualifiedName is the symbol's fully qualified name within its file.
	QualifiedName string

	// File is the symbol's file path, relative to the repository root,
	// slash-separated.
	File string

	// Dir is File's directory, relative to the repository root — the unit
	// budget.toml's [[quota]] is keyed on.
	Dir string

	// Kind is the codegraph.NodeKind string (e.g. "function", "struct") —
	// kept as a string, not codegraph.NodeKind, so this leaf never imports
	// internal/codegraph.
	Kind string

	// Exported reports whether the symbol is exported/public.
	Exported bool

	// Signature is the symbol's normalised type signature — used by the
	// reinvention detection (D8 #7) to compare two symbols beyond their
	// name.
	Signature string

	// StartLine and EndLine are the symbol's 1-based line range in File, at
	// the ref it was collected from.
	StartLine int
	EndLine   int
}

// SymbolKey builds the comparison identity DiffSymbols and the budget
// evaluator use for a symbol — exported so both the collector (symbols.go)
// and its callers agree on exactly one key format.
func SymbolKey(file, qualifiedName string) string {
	return file + ":" + qualifiedName
}

// Quota is one `[[quota]]` entry: a per-directory allowance for NEW symbols
// (D4 of the grill — "cupo por paquete para lo nuevo").
type Quota struct {
	// Dir is the directory this quota governs, relative to the repository
	// root, without a trailing slash.
	Dir string

	// MaxNewSymbols is how many newly created, budgetable symbols Dir may
	// receive before they count as excess.
	MaxNewSymbols int
}

// ModifyEntry is one `[[modify]]` entry: a nominal declaration that an
// EXISTING symbol will be modified (D4 of the grill — "nominal para lo
// existente").
type ModifyEntry struct {
	// File is the symbol's file path, relative to the repository root.
	File string

	// Symbol is the symbol's qualified name within File.
	Symbol string
}

// Revision is the `[revision]` table: the architect's signed, after-the-fact
// widening of the budget (D3 of the grill — "la revision al alza la firma
// el architect"). Its presence is ALWAYS a firmable finding (D9's fila 4) —
// a revision without a rationale is not a revision, it is a number changed
// in secret.
type Revision struct {
	By        string
	At        time.Time
	Rationale string
	Margin    int
	Quota     []Quota
}

// Budget is the parsed, validated form of a spec's budget.toml.
type Budget struct {
	SchemaVersion int

	// Margin is the single pool of absolute-symbol slack shared by BOTH
	// halves of the contract (D4 of this spec) — never a percentage (S3's
	// small-N arithmetic trap applies here too).
	Margin int

	// Radius is the doublestar glob set every changed file must fall
	// within (D7) — the same evaluator EvaluateRadius (budgeteval.go, P4)
	// also runs for the trivial lane's own scope.
	Radius []string

	Quota  []Quota
	Modify []ModifyEntry

	// Revision is nil when the document declares no [revision] table — the
	// common, unrevised case.
	Revision *Revision
}

// rawBudget is the strict decode target for ParseBudget. Pointer fields
// distinguish "absent" from "present with the zero value" (margin=0 is a
// legitimate, strict contract — D3 of this spec) — the same convention
// rawConstitution/rawCriteria already establish.
type rawBudget struct {
	SchemaVersion *int         `toml:"schema_version"`
	Margin        *int         `toml:"margin"`
	Radius        *[]string    `toml:"radius"`
	Quota         []rawQuota   `toml:"quota"`
	Modify        []rawModify  `toml:"modify"`
	Revision      *rawRevision `toml:"revision"`
}

type rawQuota struct {
	Dir           *string `toml:"dir"`
	MaxNewSymbols *int    `toml:"max_new_symbols"`
}

type rawModify struct {
	File   *string `toml:"file"`
	Symbol *string `toml:"symbol"`
}

type rawRevision struct {
	By        *string    `toml:"by"`
	At        *string    `toml:"at"`
	Rationale *string    `toml:"rationale"`
	Margin    *int       `toml:"margin"`
	Quota     []rawQuota `toml:"quota"`
}

// ParseBudget decodes and validates raw TOML bytes into a Budget. As strict
// as Parse/ParseCriteria (D3): DisallowUnknownFields, every documented key
// required, every doublestar glob validated at parse time via
// doublestar.Match(p, "probe") (the same probe Parse's own coverage.exclude
// and ParseCriteria's globs use).
func ParseBudget(data []byte) (*Budget, error) {
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var raw rawBudget
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("quality: parse budget: %s: %w", err, ErrInvalidBudget)
	}

	if raw.SchemaVersion == nil {
		return nil, fmt.Errorf("quality: missing required key %q: %w", "schema_version", ErrInvalidBudget)
	}
	if *raw.SchemaVersion != BudgetSchemaVersion {
		return nil, fmt.Errorf(
			"quality: budget schema_version %d escrito por una mneme mas nueva/vieja (solo se acepta %d): %w",
			*raw.SchemaVersion, BudgetSchemaVersion, ErrUnsupportedBudgetSchema)
	}

	if raw.Margin == nil {
		return nil, fmt.Errorf("quality: missing required key %q: %w", "margin", ErrInvalidBudget)
	}
	if *raw.Margin < 0 {
		return nil, fmt.Errorf("quality: margin %d must be >= 0: %w", *raw.Margin, ErrInvalidBudget)
	}

	if raw.Radius == nil || len(*raw.Radius) == 0 {
		return nil, fmt.Errorf("quality: missing required key %q: %w", "radius", ErrInvalidBudget)
	}
	for _, pattern := range *raw.Radius {
		if _, err := doublestar.Match(pattern, "probe"); err != nil {
			return nil, fmt.Errorf("quality: radius pattern %q invalid: %s: %w", pattern, err, ErrInvalidBudget)
		}
	}

	quotas, err := parseQuotas(raw.Quota)
	if err != nil {
		return nil, err
	}

	modifies, err := parseModifies(raw.Modify)
	if err != nil {
		return nil, err
	}

	revision, err := parseRevision(raw.Revision)
	if err != nil {
		return nil, err
	}

	return &Budget{
		SchemaVersion: *raw.SchemaVersion,
		Margin:        *raw.Margin,
		Radius:        *raw.Radius,
		Quota:         quotas,
		Modify:        modifies,
		Revision:      revision,
	}, nil
}

// parseQuotas validates every `[[quota]]` entry: dir non-empty, relative,
// free of "..", and unique across the document; max_new_symbols >= 0.
func parseQuotas(raw []rawQuota) ([]Quota, error) {
	seen := make(map[string]bool, len(raw))
	quotas := make([]Quota, 0, len(raw))
	for i, rq := range raw {
		if rq.Dir == nil || *rq.Dir == "" {
			return nil, fmt.Errorf("quality: quota[%d]: missing required key %q: %w", i, "dir", ErrInvalidBudget)
		}
		if err := validateRelativeCleanPath(*rq.Dir, fmt.Sprintf("quota[%d].dir", i)); err != nil {
			return nil, fmt.Errorf("%s: %w", err, ErrInvalidBudget)
		}
		dir := strings.TrimSuffix(filepath.ToSlash(*rq.Dir), "/")
		if seen[dir] {
			return nil, fmt.Errorf("quality: duplicate quota dir %q: %w", dir, ErrInvalidBudget)
		}
		seen[dir] = true

		if rq.MaxNewSymbols == nil {
			return nil, fmt.Errorf("quality: quota[%d] (%s): missing required key %q: %w", i, dir, "max_new_symbols", ErrInvalidBudget)
		}
		if *rq.MaxNewSymbols < 0 {
			return nil, fmt.Errorf("quality: quota %q max_new_symbols %d must be >= 0: %w", dir, *rq.MaxNewSymbols, ErrInvalidBudget)
		}
		quotas = append(quotas, Quota{Dir: dir, MaxNewSymbols: *rq.MaxNewSymbols})
	}
	return quotas, nil
}

// parseModifies validates every `[[modify]]` entry: file and symbol
// non-empty; the (file, symbol) pair unique across the document.
func parseModifies(raw []rawModify) ([]ModifyEntry, error) {
	seen := make(map[string]bool, len(raw))
	modifies := make([]ModifyEntry, 0, len(raw))
	for i, rm := range raw {
		if rm.File == nil || *rm.File == "" {
			return nil, fmt.Errorf("quality: modify[%d]: missing required key %q: %w", i, "file", ErrInvalidBudget)
		}
		if rm.Symbol == nil || *rm.Symbol == "" {
			return nil, fmt.Errorf("quality: modify[%d]: missing required key %q: %w", i, "symbol", ErrInvalidBudget)
		}
		key := *rm.File + ":" + *rm.Symbol
		if seen[key] {
			return nil, fmt.Errorf("quality: duplicate modify entry %s:%s: %w", *rm.File, *rm.Symbol, ErrInvalidBudget)
		}
		seen[key] = true
		modifies = append(modifies, ModifyEntry{File: *rm.File, Symbol: *rm.Symbol})
	}
	return modifies, nil
}

// parseRevision validates the optional `[revision]` table: when absent,
// returns (nil, nil) — a legitimate, unrevised document (AC6's first row).
// When present, by/at/rationale/margin are all required and non-empty; at
// must parse as RFC 3339. A revision without rationale is rejected — "una
// revision sin rationale no es una revision: es un numero cambiado a
// escondidas" (D3 of this spec).
func parseRevision(raw *rawRevision) (*Revision, error) {
	if raw == nil {
		return nil, nil
	}

	if raw.By == nil || *raw.By == "" {
		return nil, fmt.Errorf("quality: missing required key %q: %w", "revision.by", ErrInvalidBudget)
	}
	if raw.Rationale == nil || *raw.Rationale == "" {
		return nil, fmt.Errorf("quality: missing required key %q: %w", "revision.rationale", ErrInvalidBudget)
	}
	if raw.At == nil || *raw.At == "" {
		return nil, fmt.Errorf("quality: missing required key %q: %w", "revision.at", ErrInvalidBudget)
	}
	at, err := time.Parse(time.RFC3339, *raw.At)
	if err != nil {
		return nil, fmt.Errorf("quality: revision.at %q must be RFC 3339: %s: %w", *raw.At, err, ErrInvalidBudget)
	}
	if raw.Margin == nil {
		return nil, fmt.Errorf("quality: missing required key %q: %w", "revision.margin", ErrInvalidBudget)
	}
	if *raw.Margin < 0 {
		return nil, fmt.Errorf("quality: revision.margin %d must be >= 0: %w", *raw.Margin, ErrInvalidBudget)
	}

	quotas, err := parseQuotas(raw.Quota)
	if err != nil {
		return nil, err
	}

	return &Revision{
		By: *raw.By, At: at, Rationale: *raw.Rationale, Margin: *raw.Margin, Quota: quotas,
	}, nil
}

// ValidateBudgetAnchors implements D11's declare-time half: every
// [[modify]] entry must resolve against the working tree (the file exists
// AND the collected symbolsByFile map has a symbol with that qualified name
// in it), and every [[quota]].dir must be an existing directory (dirs, the
// caller's snapshot). Pure — never touches disk itself — so every row of
// AC21 is a table entry over fabricated facts, never a fixture working
// tree per case.
//
// The asymmetry with [[quota]] is deliberate (D11): what already EXISTS can
// be required to resolve; what is NEW cannot — a quota's directory need
// only exist as a place new files could land, never contain anything yet.
func ValidateBudgetAnchors(b *Budget, dirs []string, symbolsByFile map[string][]Symbol) error {
	dirSet := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		dirSet[filepath.ToSlash(strings.TrimSuffix(d, "/"))] = true
	}

	for _, q := range b.Quota {
		if !dirSet[q.Dir] {
			return fmt.Errorf("quality: quota dir %q does not exist in the working tree: %w", q.Dir, ErrInvalidBudget)
		}
	}

	for _, m := range b.Modify {
		syms, ok := symbolsByFile[m.File]
		if !ok {
			return fmt.Errorf("quality: modify file %q does not exist in the working tree: %w", m.File, ErrInvalidBudget)
		}
		found := false
		for _, s := range syms {
			if s.QualifiedName == m.Symbol {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("quality: modify symbol %q not found in %q: %w", m.Symbol, m.File, ErrInvalidBudget)
		}
	}

	return nil
}
