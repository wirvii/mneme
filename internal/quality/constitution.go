package quality

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/pelletier/go-toml/v2"
)

// MinSchemaVersion is the oldest schema_version Parse still accepts (D5).
// S1 shipped schema 1 with no [coverage]/[ratchet] tables; S2 adds
// CurrentSchemaVersion=2 without narrowing what schema 1 means — a
// schema-1 constitution from before this spec keeps parsing exactly as it
// always did. Every future spec that adds a schema version widens this
// accepted set; none may narrow it (R2): narrowing turns every existing
// constitution in the world into an instant block (V4).
const MinSchemaVersion = 1

// CurrentSchemaVersion is the newest schema_version Parse accepts. A
// constitution declaring anything outside [MinSchemaVersion,
// CurrentSchemaVersion] fails closed with ErrUnsupportedSchema rather than
// being silently reinterpreted.
//
// SPEC-117 S3 bumped this to 3 ([criteria] joins [coverage]/[ratchet]) —
// and, critically, did so together with correcting the comparison below
// to a RANGE (D9): the range check is what lets a schema-2 constitution
// (this repo's own .mneme/quality.toml among them) keep parsing exactly as
// it always did.
//
// SPEC-118 S4 bumped this to 4 ([budget] joins the other three) — the
// range is ALREADY in place (S3's own fix), so this bump is purely
// additive: nothing that parsed under schema 1/2/3 stops parsing. Every
// future spec that adds a schema version widens this range; none may
// narrow it.
//
// SPEC-119 S5 bumps this to 5 ([mutation] joins the other four) — S4 had
// already landed on this branch by the time S5 was implemented (verified
// per plan paso 0: `internal/quality/{criteria.go,evaluate.go,report.go}`
// and `budget.go` all existed, and CurrentSchemaVersion already read 4),
// so [mutation] takes the NEXT number rather than colliding with
// [budget]'s. The range is already in place; this bump is, again, purely
// additive.
//
// SPEC-120 S6 bumps this to 6 ([visual] and its nested [visual.compare]
// join the other five) — verified per THIS plan's own paso 0: S4 and S5
// had both already landed on this branch (CurrentSchemaVersion already
// read 5), so [visual] takes the next number. The range check below is
// already in place; this bump is, again, purely additive.
const CurrentSchemaVersion = 6

// ErrInvalid is returned by Parse when the constitution is missing a
// required key, declares an unknown key, or fails a per-field validation
// rule (safe-slug gate names, argv-shaped commands, positive timeouts, an
// output_tail_bytes within [1, 65536]). Every failure names the offending
// key or value in the wrapped message (D2/AC2/AC3).
var ErrInvalid = errors.New("quality: invalid constitution")

// ErrUnsupportedSchema is returned by Parse when schema_version is not
// CurrentSchemaVersion — the constitution was written by a different mneme
// version. Distinct from ErrInvalid because the remedy differs: upgrade
// mneme (or downgrade the file), not fix a typo. Mirrors the precedent of
// profile.ErrProfileLockUnsupported.
var ErrUnsupportedSchema = errors.New("quality: constitution schema_version unsupported")

// safeSlugPattern is the gate-name format required by D2: lowercase,
// starting with a letter, hyphen-friendly — the same convention mneme uses
// for scaffold/blueprint names.
var safeSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Gate is a single declared quality gate: a command mneme runs verbatim,
// with no shell involved (D7), bounded by Timeout, and marked Required when
// its failure must stop the run and skip the remaining gates (D6).
type Gate struct {
	// Name is the gate's safe-slug identifier, unique within the
	// constitution. Used as quality_checks.name for the gate's row.
	Name string

	// Command is the argv vector executed via exec.CommandContext(argv[0],
	// argv[1:]...) — never through a shell (D7). A single element
	// containing a space (e.g. ["make test"]) is rejected by Parse with a
	// dedicated message explaining the argv-per-element requirement.
	Command []string

	// Timeout bounds how long the gate may run before it is killed and
	// recorded as a failed, timed-out check (D6/AC9).
	Timeout time.Duration

	// Required marks a gate whose failure stops execution of the remaining
	// gates (they are recorded as "skipped") and fails the certificate.
	// A non-required gate that fails is still recorded but does not by
	// itself degrade the verdict beyond "fail" for that one check — S1
	// derives the certificate verdict from ALL check rows (D10), so a
	// failed non-required gate still surfaces, just without halting the run.
	Required bool
}

// ExecutionConfig is the [execution] table of the constitution: storage
// bounds for gate output, not a quality threshold (D2).
type ExecutionConfig struct {
	// OutputTailBytes is how many bytes of a gate's combined stdout+stderr
	// are retained verbatim in the certificate's quality_checks row. Must be
	// in [1, 65536]. The full output is still hashed in streaming (D6) —
	// this bound only caps what is stored, never what is fingerprinted.
	OutputTailBytes int
}

// Constitution is the parsed, validated form of a repository's
// .mneme/quality.toml. mneme never fills in a default for any of its
// fields (D13 of the grill) — every value Parse returns was written by a
// human, in a committed, revisable file.
type Constitution struct {
	// SchemaVersion is the document's own declared schema_version — 1 or
	// CurrentSchemaVersion after a successful Parse (D5), never anything
	// else.
	SchemaVersion int

	// Enabled is the mechanism's on/off switch (D3). While false, nothing in
	// this constitution blocks spec_advance.
	Enabled bool

	// Execution holds storage bounds for gate output.
	Execution ExecutionConfig

	// Gates is the ordered list of declared gates, executed sequentially in
	// this order (D6).
	Gates []Gate

	// CoverageDeclared reports whether [coverage] is present in the
	// document — true only under schema_version 2 (D5/AC3). Distinct from
	// Coverage.Enabled: a schema-2 document may declare the section with
	// enabled=false ("apagado por decisión"), which is a different fact
	// from schema_version=1 not declaring it at all ("apagado por
	// omisión") — D13's table of skip reasons depends on telling the two
	// apart.
	CoverageDeclared bool

	// Coverage is the zero value when CoverageDeclared is false — the
	// service layer branches on CoverageDeclared, never reads a field of a
	// zero Coverage as if it meant something (D5: "el binario no rellena
	// nada").
	Coverage CoverageConfig

	// RatchetDeclared mirrors CoverageDeclared for [ratchet].
	RatchetDeclared bool

	Ratchet RatchetConfig

	// CriteriaDeclared reports whether [criteria] is present in the
	// document — true only under schema_version 3 (SPEC-117 D9), the same
	// Declared/undeclared distinction CoverageDeclared already establishes.
	CriteriaDeclared bool

	// Criteria is the zero value when CriteriaDeclared is false — the
	// service layer branches on CriteriaDeclared, never reads a field of
	// an undeclared Criteria as if it meant something (D9: "el binario no
	// rellena nada").
	Criteria CriteriaConfig

	// BudgetDeclared reports whether [budget] is present — true only under
	// schema_version 4 (SPEC-118 D14), the same Declared/undeclared
	// distinction CoverageDeclared/CriteriaDeclared already establish.
	BudgetDeclared bool

	// Budget is the zero value when BudgetDeclared is false.
	Budget BudgetConfig

	// MutationDeclared reports whether [mutation] is present — true only
	// under schema_version 5 (SPEC-119 D11), the same Declared/undeclared
	// distinction every prior section already establishes.
	MutationDeclared bool

	// Mutation is the zero value when MutationDeclared is false.
	Mutation MutationConfig

	// VisualDeclared reports whether [visual] (and its nested
	// [visual.compare]) is present — true only under schema_version 6
	// (SPEC-120 D11), the same Declared/undeclared distinction every prior
	// section already establishes. There is a SINGLE flag for both tables,
	// never a separate VisualCompareDeclared: [visual.compare] is nested
	// syntactically inside [visual] and required whenever [visual] is (the
	// same posture the schema-2 bump gave [coverage]/[ratchet] together,
	// except here it is ONE section with a subtable, not two siblings).
	VisualDeclared bool

	// Visual is the zero value when VisualDeclared is false.
	Visual VisualConfig
}

// VisualConfig is the parsed, validated `[visual]` table (SPEC-120
// EPIC-calidad S6 D3/D11) — present only when SchemaVersion is 6 and
// VisualDeclared is true. Compare holds the nested `[visual.compare]`
// subtable (D7), always present alongside it.
type VisualConfig struct {
	// Enabled is [visual]'s own switch, independent of the top-level
	// Constitution.Enabled — the visual mechanism can be off while gates
	// still run, exactly like coverage/criteria/budget/mutation.
	Enabled bool

	// Format is one of VisualFormats() — declared, never guessed (D2): a
	// wrong declared format fails loudly via ParseVisualReport's
	// ErrInvalidVisualReport rather than silently producing an empty
	// report.
	Format string

	// Command is the argv vector that arranca, recorre, y apaga la
	// interfaz, ejecutado exactamente como un Gate's command — never
	// through a shell (D1/D7 of S1, mirrored by every prior command-bearing
	// section). The ENTIRE server lifecycle belongs to this command (D1):
	// mneme only waits for it to terminate.
	Command []string

	// ReportPath is where Command leaves the visual report, relative to
	// the repository root. mneme DELETES this path before running Command
	// (D4, mirroring coverage's/mutation's own posture) — validated here to
	// be relative and free of ".." so deletion can never escape the
	// repository.
	ReportPath string

	// Timeout bounds the ENTIRE visual phase: arrancar, recorrer, y apagar
	// la interfaz (D1). Exceeding it is a firmable `finding`
	// `budget-exceeded`, never a silent `fail`.
	Timeout time.Duration

	// Targets is the project-declared list of OPAQUE identifiers (D3):
	// mneme never interprets a single character of any entry — a route, a
	// theme, a width, a UI state are all doctrine of the PROJECT, never of
	// mneme. Presence of this key is ALWAYS required; an EMPTY list is only
	// a value-level error when Enabled is true (G4a/G4b) — a schema-6
	// document with `enabled = false` and `targets = []` is exactly the
	// legitimate "declared and off" shape D15 depends on.
	Targets []string

	// FailOnConsoleError mirrors the constitution's own
	// fail_on_console_error key (D5): a REQUIRED key with NO binary default
	// (D13 of the grill) — a project must decide explicitly whether
	// console.error blocks. An uncaught exception (page_errors) fails
	// regardless of this key's value; only console.error is conditioned on
	// it.
	FailOnConsoleError bool

	// A11yFailImpacts is the closed set of A11yImpact values that degrade
	// the verdict (D6) — REQUIRED, but may be the empty list: measuring
	// accessibility and never blocking on it is a legitimate, explicit
	// choice, the same posture MaxEquivalent=0 already establishes for S5.
	A11yFailImpacts []A11yImpact

	// Compare is the nested `[visual.compare]` subtable — the OPTIONAL,
	// separately-switched nivel 2 (D7).
	Compare VisualCompareConfig
}

// VisualCompareConfig is the parsed, validated `[visual.compare]` subtable
// (SPEC-120 D7/D11) — nested syntactically inside `[visual]`, but switched
// independently via its own Enabled: pixel comparison fails on its own
// (fonts, antialiasing, animations, timestamps in the UI) and every false
// positive trains a team to approve blindly, so a project that does not
// want it never turns it on.
type VisualCompareConfig struct {
	// Enabled is nivel 2's own switch.
	Enabled bool

	// ReferenceDir is where the VERSIONED reference images live, relative
	// to the repository root (D8) — mneme NEVER writes here; the only write
	// path is a human's own `cp` + commit. Validated non-empty, relative,
	// and clean ONLY when Enabled is true (D13's "presence always,
	// value-constraint conditional" posture) — an empty string is the
	// legitimate shape of a declared-and-off nivel 2.
	ReferenceDir string

	// CaptureDir is where Command leaves the CAPTURED screenshots, relative
	// to the repository root — an OUTPUT, ignored by git: mneme deletes
	// `<CaptureDir>/<id>.png` for every declared target before running
	// Command (D4). Same conditional-validation posture as ReferenceDir.
	CaptureDir string

	// MaxDiffPct is the tolerance, in percentage of differing pixels (D7).
	// The comparison is STRICT (`>`, never `>=`): with the tolerance
	// declared exactly at the measured difference, it passes.
	MaxDiffPct float64
}

// MutationConfig is the parsed, validated [mutation] table (SPEC-119
// EPIC-calidad S5 D1/D11) — present only when SchemaVersion is 5 and
// MutationDeclared is true. Every value here is either used verbatim by
// the mutation checks (P8) or carried, unread by Parse itself, straight
// into a certificate's mutation/score row for Sign to consult later (D9).
type MutationConfig struct {
	// Enabled is [mutation]'s own switch, independent of Constitution.Enabled
	// — the mutation mechanism does not consume the coverage/criteria/budget
	// state and can be on or off regardless of them.
	Enabled bool

	// Format is one of MutantFormats() — declared, never guessed (D1 pata
	// b/D2): a wrong declared format fails loudly via ParseMutantReport's
	// ErrInvalidMutantReport rather than silently producing an empty
	// report.
	Format string

	// Command is the argv vector that produces the mutation report,
	// executed exactly like a Gate's command — never through a shell (D7 of
	// S1, mirrored here). May contain the {{BASE_SHA}} substitution token
	// (ExpandCommand) any number of times, in any element; any OTHER
	// `{{...}}` sequence is rejected here, at Parse time (D3) — never
	// tolerated as a literal that would travel unexpanded to the mutator.
	Command []string

	// ReportPath is where Command leaves the mutation report, relative to
	// the repository root. mneme DELETES this path before running Command
	// (D4, mirroring coverage's own D12) — validated here to be relative
	// and free of ".." so deletion can never escape the repository.
	ReportPath string

	// Timeout bounds the ENTIRE mutation phase — mutating typically means
	// running the project's test suite once per mutant, the most expensive
	// check in the EPIC (D12). Exceeding it is a firmable `finding`
	// `budget-exceeded`, never a silent `fail` (D12).
	Timeout time.Duration

	// MaxEquivalent is the ABSOLUTE (never percentage, D9 — a percentage of
	// a small N means nothing) cap on mutants a qa-tester may sign as
	// equivalent for ONE certificate. 0 is a valid, meaningful value: no
	// escape hatch at all. Enforced by Sign (service layer, P9), never by
	// Verify — at verification time no signature has happened yet.
	MaxEquivalent int

	// MaxNotViablePct is the quota (D1 pata d): the proportion, in percent,
	// of in-diff mutants that may be MutantNotViable before mneme judges
	// the informe is talking about the mutator rather than about the
	// tests. Must be in (0, 100] — 0 would reject every real run outright,
	// which is never what a project means by declaring this key.
	MaxNotViablePct float64
}

// CriteriaConfig is the parsed, validated [criteria] table (SPEC-117 D9) —
// present only when SchemaVersion is 3 and CriteriaDeclared is true.
type CriteriaConfig struct {
	// Enabled is [criteria]'s own switch, independent of both the
	// top-level Constitution.Enabled and CoverageConfig.Enabled — the
	// criteria mechanism does not consume the coverage profile and can be
	// on or off regardless of it (D9).
	Enabled bool

	// Timeout bounds the structured phase's ENTIRE evaluation (every
	// ListFilesAtRef/GrepLinesAtRef call across both refs) — it does NOT
	// cover a mode=command criterion, which declares its own timeout
	// (D9).
	Timeout time.Duration

	// MaxManualPct is the quota, in percent of the total DECLARED
	// criteria, of mode=manual criteria (D10/D14 of the grill) — exceeding
	// it FAILS the certificate outright (never a firmable finding): the
	// remedy is rewriting the criteria, not a signature.
	MaxManualPct float64

	// MaxCommandPct mirrors MaxManualPct for the mode=command escape
	// hatch (D10) — not in the grill, added because the hatch has the
	// exact same degenerate failure mode as the manual quota.
	MaxCommandPct float64
}

// CoverageConfig is the parsed, validated [coverage] table (D6) — present
// only when SchemaVersion is 2 and CoverageDeclared is true.
type CoverageConfig struct {
	// Enabled is [coverage]'s own switch, independent of the top-level
	// Constitution.Enabled — the coverage/ratchet mechanism can be off
	// while gates still run, and vice versa is meaningless (gates are
	// unconditional today).
	Enabled bool

	// Format is one of quality.Formats() — declared, never guessed (D18):
	// a wrong declared format fails loudly via ParseProfile's
	// ErrInvalidProfile rather than silently producing an empty profile.
	Format string

	// Command is the argv vector that produces the coverage profile,
	// executed exactly like a Gate's command — never through a shell (D1
	// of the grill, mirroring gates' own R2).
	Command []string

	// ProfilePath is where Command leaves the profile, relative to the
	// repository root. mneme DELETES this path before running Command
	// (D12) — validated here to be relative and free of ".." so that
	// deletion can never escape the repository.
	ProfilePath string

	Timeout time.Duration

	// MinDiffLinePct is the coverage threshold for the spec's changed
	// lines, in (0, 100].
	MinDiffLinePct float64

	// MinChangedLines is the floor below which the diff-coverage check is
	// skipped rather than evaluated (D6) — the aritmetically-100%-at-tiny-N
	// trap.
	MinChangedLines int

	// Exclude is the list of doublestar glob patterns dropped from BOTH the
	// diff and the aggregate calculations (D6) — every pattern is
	// validated for syntax here, at parse time, never at evaluation time.
	Exclude []string
}

// RatchetConfig is the parsed, validated [ratchet] table (D6) — present
// only when SchemaVersion is 2 and RatchetDeclared is true.
type RatchetConfig struct {
	// Enabled requires CoverageConfig.Enabled (Parse rejects the
	// inconsistent combination outright, D6) — the ratchet feeds off the
	// same profile the coverage check produces (D1).
	Enabled bool

	// MaxGlobalLinePctDrop is the tolerated drop, in percentage points, of
	// the repository-wide coverage versus the registered baseline. >= 0;
	// 0 means no tolerated drop at all.
	MaxGlobalLinePctDrop float64

	// MaxBaselineStalenessPct is how far a fresh measurement may exceed the
	// registered baseline before the mark is declared stale (D17). Must be
	// > 0 and >= MaxGlobalLinePctDrop (D6's cross-bound) — a smaller
	// staleness margin than the tolerated drop would declare two
	// incompatible things at once.
	MaxBaselineStalenessPct float64
}

// rawConstitution is the strict decode target for Parse. Every field a human
// must supply is a pointer (or, for Gate.Required, decoded via rawGate) so
// Parse can tell "absent" from "present with the zero value" — a plain bool
// or int field would make a missing `enabled = false` indistinguishable from
// an explicitly declared one, defeating D13's "no defaults in the binary".
type rawConstitution struct {
	SchemaVersion *int              `toml:"schema_version"`
	Enabled       *bool             `toml:"enabled"`
	Execution     rawExecution      `toml:"execution"`
	Gates         []rawGate         `toml:"gate"`
	Coverage      *rawCoverage      `toml:"coverage"`
	Ratchet       *rawRatchet       `toml:"ratchet"`
	Criteria      *rawCriteria      `toml:"criteria"`
	Budget        *rawBudgetSection `toml:"budget"`
	Mutation      *rawMutation      `toml:"mutation"`
	Visual        *rawVisual        `toml:"visual"`
}

// rawVisual mirrors VisualConfig with pointer fields for presence detection
// (SPEC-120 D11) — the same convention every prior section's raw
// counterpart already establishes. Compare is itself a pointer to detect
// whether the nested `[visual.compare]` subtable is present at all.
type rawVisual struct {
	Enabled            *bool             `toml:"enabled"`
	Format             *string           `toml:"format"`
	Command            *[]string         `toml:"command"`
	ReportPath         *string           `toml:"report_path"`
	Timeout            *string           `toml:"timeout"`
	Targets            *[]string         `toml:"targets"`
	FailOnConsoleError *bool             `toml:"fail_on_console_error"`
	A11yFailImpacts    *[]string         `toml:"a11y_fail_impacts"`
	Compare            *rawVisualCompare `toml:"compare"`
}

// rawVisualCompare mirrors VisualCompareConfig with pointer fields for
// presence detection.
type rawVisualCompare struct {
	Enabled      *bool    `toml:"enabled"`
	ReferenceDir *string  `toml:"reference_dir"`
	CaptureDir   *string  `toml:"capture_dir"`
	MaxDiffPct   *float64 `toml:"max_diff_pct"`
}

// rawMutation mirrors MutationConfig with pointer fields for presence
// detection (SPEC-119 D11) — the same convention every prior section's raw
// counterpart already establishes.
type rawMutation struct {
	Enabled         *bool     `toml:"enabled"`
	Format          *string   `toml:"format"`
	Command         *[]string `toml:"command"`
	ReportPath      *string   `toml:"report_path"`
	Timeout         *string   `toml:"timeout"`
	MaxEquivalent   *int      `toml:"max_equivalent"`
	MaxNotViablePct *float64  `toml:"max_not_viable_pct"`
}

// rawBudgetSection mirrors BudgetConfig with pointer fields for presence
// detection (SPEC-118 D14) — named distinctly from budget.go's own
// rawBudget (the SEPARATE document, a spec's budget.toml) so the two can
// never be confused: this one decodes the constitution's `[budget]` TABLE,
// that one decodes an entire budget.toml FILE.
type rawBudgetSection struct {
	Enabled        *bool     `toml:"enabled"`
	Timeout        *string   `toml:"timeout"`
	TestGlobs      *[]string `toml:"test_globs"`
	TestReachDepth *int      `toml:"test_reach_depth"`
}

// rawCriteria mirrors CriteriaConfig with pointer fields for presence
// detection (SPEC-117 D9) — the same convention rawCoverage/rawRatchet
// already establish.
type rawCriteria struct {
	Enabled       *bool    `toml:"enabled"`
	Timeout       *string  `toml:"timeout"`
	MaxManualPct  *float64 `toml:"max_manual_pct"`
	MaxCommandPct *float64 `toml:"max_command_pct"`
}

// rawCoverage mirrors CoverageConfig with pointer fields (and a pointer
// slice for Command/Exclude) so Parse can tell "the key is entirely
// absent" from "present with its zero value" (e.g. `exclude = []` is
// present-and-empty, not absent) — the same presence-detection contract
// rawConstitution's own top-level fields already use.
type rawCoverage struct {
	Enabled         *bool     `toml:"enabled"`
	Format          *string   `toml:"format"`
	Command         *[]string `toml:"command"`
	ProfilePath     *string   `toml:"profile_path"`
	Timeout         *string   `toml:"timeout"`
	MinDiffLinePct  *float64  `toml:"min_diff_line_pct"`
	MinChangedLines *int      `toml:"min_changed_lines"`
	Exclude         *[]string `toml:"exclude"`
}

// rawRatchet mirrors RatchetConfig with pointer fields for presence
// detection.
type rawRatchet struct {
	Enabled                 *bool    `toml:"enabled"`
	MaxGlobalLinePctDrop    *float64 `toml:"max_global_line_pct_drop"`
	MaxBaselineStalenessPct *float64 `toml:"max_baseline_staleness_pct"`
}

// rawExecution mirrors ExecutionConfig with a pointer field for presence
// detection.
type rawExecution struct {
	OutputTailBytes *int `toml:"output_tail_bytes"`
}

// rawGate mirrors Gate with a pointer Required field for presence detection
// — a gate whose author forgot `required = ...` must fail Parse, not
// silently become required=false.
type rawGate struct {
	Name     string   `toml:"name"`
	Command  []string `toml:"command"`
	Timeout  string   `toml:"timeout"`
	Required *bool    `toml:"required"`
}

// Parse decodes and validates raw TOML bytes into a Constitution. It is
// strict in both directions (D2): every documented key is required (missing
// keys name themselves in the returned error), and DisallowUnknownFields
// rejects any key Parse does not recognise — a typo must explode, not
// silently govern nothing (the SPEC-087 AC12 scar, in TOML form).
func Parse(data []byte) (*Constitution, error) {
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var raw rawConstitution
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("quality: parse constitution: %s: %w", err, ErrInvalid)
	}

	if raw.SchemaVersion == nil {
		return nil, fmt.Errorf("quality: missing required key %q: %w", "schema_version", ErrInvalid)
	}
	schemaVersion := *raw.SchemaVersion
	// SPEC-117 D9: a RANGE, never an equality check against two named
	// values. The equality form this replaces (`!= 1 && != CurrentSchemaVersion`)
	// is exactly the trap that bricks every existing schema-2 constitution
	// the moment CurrentSchemaVersion moves to 3 — Parse would fail BEFORE
	// ever reading `enabled`, so even an `enabled = false` document (this
	// repo's own .mneme/quality.toml included) would stop parsing entirely.
	if schemaVersion < MinSchemaVersion || schemaVersion > CurrentSchemaVersion {
		return nil, fmt.Errorf(
			"quality: schema_version %d escrito por una mneme más nueva/vieja: actualiza mneme: %w",
			schemaVersion, ErrUnsupportedSchema)
	}

	if raw.Enabled == nil {
		return nil, fmt.Errorf("quality: missing required key %q: %w", "enabled", ErrInvalid)
	}

	if raw.Execution.OutputTailBytes == nil {
		return nil, fmt.Errorf("quality: missing required key %q: %w", "execution.output_tail_bytes", ErrInvalid)
	}
	if *raw.Execution.OutputTailBytes < 1 || *raw.Execution.OutputTailBytes > 65536 {
		return nil, fmt.Errorf(
			"quality: execution.output_tail_bytes %d out of range [1, 65536]: %w",
			*raw.Execution.OutputTailBytes, ErrInvalid)
	}

	gates, err := parseGates(raw.Gates)
	if err != nil {
		return nil, err
	}
	// BL-220: enabled=true with zero declared gates parsed cleanly and let
	// runAllChecks emit a certificate made only of the four checks that
	// always run (tree/clean-worktree + the three constitution ones),
	// deriving `pass` in ~68ms without verifying anything the project
	// actually declared. DeriveVerdict's own "an EMPTY set is fail" guard
	// (AC7) can never fire for this case, because those four rows are
	// never empty — the defect is upstream, at parse time. Same molde as
	// visual.targets' own enabled-implies-non-empty check above (D3/G4a).
	if *raw.Enabled && len(gates) == 0 {
		return nil, fmt.Errorf(
			"quality: gates must not be empty when enabled=true (declare at least one [[gate]], or set enabled=false): %w", ErrInvalid)
	}

	coverageDeclared, coverageCfg, ratchetDeclared, ratchetCfg, err := parseCoverageAndRatchet(schemaVersion, raw.Coverage, raw.Ratchet)
	if err != nil {
		return nil, err
	}

	criteriaDeclared, criteriaCfg, err := parseCriteriaSection(schemaVersion, raw.Criteria)
	if err != nil {
		return nil, err
	}

	budgetDeclared, budgetCfg, err := parseBudgetSection(schemaVersion, raw.Budget)
	if err != nil {
		return nil, err
	}

	mutationDeclared, mutationCfg, err := parseMutationSection(schemaVersion, raw.Mutation)
	if err != nil {
		return nil, err
	}

	visualDeclared, visualCfg, err := parseVisualSection(schemaVersion, raw.Visual)
	if err != nil {
		return nil, err
	}

	return &Constitution{
		SchemaVersion:    schemaVersion,
		Enabled:          *raw.Enabled,
		Execution:        ExecutionConfig{OutputTailBytes: *raw.Execution.OutputTailBytes},
		Gates:            gates,
		CoverageDeclared: coverageDeclared,
		Coverage:         coverageCfg,
		RatchetDeclared:  ratchetDeclared,
		Ratchet:          ratchetCfg,
		CriteriaDeclared: criteriaDeclared,
		Criteria:         criteriaCfg,
		BudgetDeclared:   budgetDeclared,
		Budget:           budgetCfg,
		MutationDeclared: mutationDeclared,
		Mutation:         mutationCfg,
		VisualDeclared:   visualDeclared,
		Visual:           visualCfg,
	}, nil
}

// mutationTokenPattern matches every `{{...}}` occurrence in a mutation
// command element — used to validate that the ONLY token present is
// {{BASE_SHA}} (D3): an unrecognised token (a typo like {{BASESHA}}, or
// anything else) must explode HERE, at Parse time, rather than travel as
// an inert literal all the way to the mutator — the SPEC-087 AC12 scar,
// applied to a substitution token instead of a shell flag.
var mutationTokenPattern = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// mutationBaseSHAToken is the ONLY substitution token ExpandCommand
// recognises (D3). Declared once so Parse's validation and ExpandCommand's
// own substitution can never name it differently.
const mutationBaseSHAToken = "{{BASE_SHA}}"

// parseMutationSection implements SPEC-119 D11's schema-version branching
// for `[mutation]` — the same shape parseBudgetSection/parseCriteriaSection
// already establish: a strict single threshold (only schema 5 accepts it,
// declaring it under 1-4 is an explicit, named "sube schema_version a 5"
// error), schema 5 requires it present and complete. Deliberately its OWN
// function, never folded into parseBudgetSection or any other section's
// parser (D11 point 5, mirroring S3/S4's own reasoning): [mutation] and
// [budget] are independent sections with independent parsers, so which of
// S4/S5 lands first on a branch changes nothing about either one's logic —
// only a number and this function's own threshold.
func parseMutationSection(schemaVersion int, raw *rawMutation) (declared bool, cfg MutationConfig, err error) {
	if schemaVersion < 5 {
		if raw != nil {
			return false, MutationConfig{}, fmt.Errorf(
				"quality: [mutation] declared under schema_version %d — sube schema_version a 5: %w", schemaVersion, ErrInvalid)
		}
		return false, MutationConfig{}, nil
	}

	if raw == nil {
		return false, MutationConfig{}, fmt.Errorf("quality: missing required section %q for schema_version %d: %w", "mutation", schemaVersion, ErrInvalid)
	}

	if raw.Enabled == nil {
		return false, MutationConfig{}, fmt.Errorf("quality: missing required key %q: %w", "mutation.enabled", ErrInvalid)
	}

	if raw.Format == nil {
		return false, MutationConfig{}, fmt.Errorf("quality: missing required key %q: %w", "mutation.format", ErrInvalid)
	}
	acceptedFormats := MutantFormats()
	if !slices.Contains(acceptedFormats, *raw.Format) {
		return false, MutationConfig{}, fmt.Errorf(
			"quality: mutation.format %q must be one of %v: %w", *raw.Format, acceptedFormats, ErrInvalid)
	}

	if raw.Command == nil || len(*raw.Command) == 0 {
		return false, MutationConfig{}, fmt.Errorf("quality: missing required key %q: %w", "mutation.command", ErrInvalid)
	}
	if msg, bad := argvShellStringProblem(*raw.Command); bad {
		return false, MutationConfig{}, fmt.Errorf("quality: mutation.command %s: %w", msg, ErrInvalid)
	}
	for _, elem := range *raw.Command {
		for _, tok := range mutationTokenPattern.FindAllString(elem, -1) {
			if tok != mutationBaseSHAToken {
				return false, MutationConfig{}, fmt.Errorf(
					"quality: mutation.command element %q contains unknown substitution token %q (only %q is recognised): %w",
					elem, tok, mutationBaseSHAToken, ErrInvalid)
			}
		}
	}

	if raw.ReportPath == nil {
		return false, MutationConfig{}, fmt.Errorf("quality: missing required key %q: %w", "mutation.report_path", ErrInvalid)
	}
	if err := validateRelativeCleanPath(*raw.ReportPath, "mutation.report_path"); err != nil {
		return false, MutationConfig{}, err
	}

	if raw.Timeout == nil {
		return false, MutationConfig{}, fmt.Errorf("quality: missing required key %q: %w", "mutation.timeout", ErrInvalid)
	}
	dur, durErr := time.ParseDuration(*raw.Timeout)
	if durErr != nil || dur <= 0 {
		return false, MutationConfig{}, fmt.Errorf(
			"quality: mutation.timeout %q must be a positive parseable duration: %w", *raw.Timeout, ErrInvalid)
	}

	if raw.MaxEquivalent == nil {
		return false, MutationConfig{}, fmt.Errorf("quality: missing required key %q: %w", "mutation.max_equivalent", ErrInvalid)
	}
	if *raw.MaxEquivalent < 0 {
		return false, MutationConfig{}, fmt.Errorf(
			"quality: mutation.max_equivalent %d must be >= 0 (0 is valid: no escape hatch at all): %w", *raw.MaxEquivalent, ErrInvalid)
	}

	if raw.MaxNotViablePct == nil {
		return false, MutationConfig{}, fmt.Errorf("quality: missing required key %q: %w", "mutation.max_not_viable_pct", ErrInvalid)
	}
	if *raw.MaxNotViablePct <= 0 || *raw.MaxNotViablePct > 100 {
		return false, MutationConfig{}, fmt.Errorf(
			"quality: mutation.max_not_viable_pct %v must be in (0, 100]: %w", *raw.MaxNotViablePct, ErrInvalid)
	}

	return true, MutationConfig{
		Enabled: *raw.Enabled, Format: *raw.Format, Command: *raw.Command, ReportPath: *raw.ReportPath,
		Timeout: dur, MaxEquivalent: *raw.MaxEquivalent, MaxNotViablePct: *raw.MaxNotViablePct,
	}, nil
}

// ExpandCommand substitutes every occurrence of {{BASE_SHA}} in argv with
// baseSHA, leaving every other element untouched — PURE, no I/O, and never
// splitting or joining an element (D3/D7 of S1): the substitution happens
// WITHIN an element's own string, so a command declared as
// ["make", "mutation", "BASE={{BASE_SHA}}"] with baseSHA "abc123" becomes
// ["make", "mutation", "BASE=abc123"], never four elements or three
// differently-shaped ones. The token is optional (a command that never
// mentions it is returned unchanged, D3's own "omitting it is legitimate,
// it just costs more time") — Parse (parseMutationSection) is what already
// guarantees, before this function ever runs, that no OTHER `{{...}}`
// sequence survived in argv.
func ExpandCommand(argv []string, baseSHA string) []string {
	expanded := make([]string, len(argv))
	for i, elem := range argv {
		expanded[i] = strings.ReplaceAll(elem, mutationBaseSHAToken, baseSHA)
	}
	return expanded
}

// parseVisualSection implements SPEC-120 D11's schema-version branching for
// `[visual]` (and its nested `[visual.compare]`) — the same shape
// parseMutationSection/parseCriteriaSection/parseBudgetSection already
// establish: a strict single threshold (only schema 6 accepts it, declaring
// it under 1-5 is an explicit, named "sube schema_version a 6" error),
// schema 6 requires it present and complete. Deliberately its OWN function,
// never folded into any other section's parser (D11 point 1, mirroring
// S3/S4/S5's own reasoning): [visual] is an independent section with its
// own parser, so which of S4/S5/S6 lands first on a branch changes nothing
// about any one section's logic — only a number and this function's own
// threshold.
func parseVisualSection(schemaVersion int, raw *rawVisual) (declared bool, cfg VisualConfig, err error) {
	if schemaVersion < 6 {
		if raw != nil {
			return false, VisualConfig{}, fmt.Errorf(
				"quality: [visual] declared under schema_version %d — sube schema_version a 6: %w", schemaVersion, ErrInvalid)
		}
		return false, VisualConfig{}, nil
	}

	if raw == nil {
		return false, VisualConfig{}, fmt.Errorf("quality: missing required section %q for schema_version %d: %w", "visual", schemaVersion, ErrInvalid)
	}

	if raw.Enabled == nil {
		return false, VisualConfig{}, fmt.Errorf("quality: missing required key %q: %w", "visual.enabled", ErrInvalid)
	}

	if raw.Format == nil {
		return false, VisualConfig{}, fmt.Errorf("quality: missing required key %q: %w", "visual.format", ErrInvalid)
	}
	acceptedFormats := VisualFormats()
	if !slices.Contains(acceptedFormats, *raw.Format) {
		return false, VisualConfig{}, fmt.Errorf(
			"quality: visual.format %q must be one of %v: %w", *raw.Format, acceptedFormats, ErrInvalid)
	}

	if raw.Command == nil || len(*raw.Command) == 0 {
		return false, VisualConfig{}, fmt.Errorf("quality: missing required key %q: %w", "visual.command", ErrInvalid)
	}
	if msg, bad := argvShellStringProblem(*raw.Command); bad {
		return false, VisualConfig{}, fmt.Errorf("quality: visual.command %s: %w", msg, ErrInvalid)
	}

	if raw.ReportPath == nil {
		return false, VisualConfig{}, fmt.Errorf("quality: missing required key %q: %w", "visual.report_path", ErrInvalid)
	}
	if err := validateRelativeCleanPath(*raw.ReportPath, "visual.report_path"); err != nil {
		return false, VisualConfig{}, err
	}

	if raw.Timeout == nil {
		return false, VisualConfig{}, fmt.Errorf("quality: missing required key %q: %w", "visual.timeout", ErrInvalid)
	}
	dur, durErr := time.ParseDuration(*raw.Timeout)
	if durErr != nil || dur <= 0 {
		return false, VisualConfig{}, fmt.Errorf(
			"quality: visual.timeout %q must be a positive parseable duration: %w", *raw.Timeout, ErrInvalid)
	}

	// D3/G4a/G4b: presence of `targets` is ALWAYS required; an EMPTY list
	// is a value-level error ONLY when `enabled = true` — a schema-6
	// document with enabled=false and targets=[] is exactly what lets a
	// project without a graphical interface (this repository included, D15)
	// declare the section honestly, without inventing objectives.
	if raw.Targets == nil {
		return false, VisualConfig{}, fmt.Errorf("quality: missing required key %q: %w", "visual.targets", ErrInvalid)
	}
	seenTargets := make(map[string]bool, len(*raw.Targets))
	for _, id := range *raw.Targets {
		if id == "" || strings.ContainsFunc(id, unicode.IsSpace) {
			return false, VisualConfig{}, fmt.Errorf("quality: visual.targets contains an empty or whitespace identifier %q: %w", id, ErrInvalid)
		}
		if seenTargets[id] {
			return false, VisualConfig{}, fmt.Errorf("quality: visual.targets contains duplicate identifier %q: %w", id, ErrInvalid)
		}
		seenTargets[id] = true
	}
	if *raw.Enabled && len(*raw.Targets) == 0 {
		return false, VisualConfig{}, fmt.Errorf(
			"quality: visual.targets must not be empty when visual.enabled=true (declare at least one, or set enabled=false): %w", ErrInvalid)
	}

	if raw.FailOnConsoleError == nil {
		return false, VisualConfig{}, fmt.Errorf("quality: missing required key %q: %w", "visual.fail_on_console_error", ErrInvalid)
	}

	if raw.A11yFailImpacts == nil {
		return false, VisualConfig{}, fmt.Errorf("quality: missing required key %q: %w", "visual.a11y_fail_impacts", ErrInvalid)
	}
	impacts := make([]A11yImpact, 0, len(*raw.A11yFailImpacts))
	seenImpacts := make(map[string]bool, len(*raw.A11yFailImpacts))
	for _, rawImpact := range *raw.A11yFailImpacts {
		impact, ok := parseA11yImpact(rawImpact)
		if !ok {
			return false, VisualConfig{}, fmt.Errorf(
				"quality: visual.a11y_fail_impacts contains %q, must be one of %v: %w", rawImpact, a11yImpacts, ErrInvalid)
		}
		if seenImpacts[rawImpact] {
			return false, VisualConfig{}, fmt.Errorf("quality: visual.a11y_fail_impacts contains duplicate %q: %w", rawImpact, ErrInvalid)
		}
		seenImpacts[rawImpact] = true
		impacts = append(impacts, impact)
	}

	if raw.Compare == nil {
		return false, VisualConfig{}, fmt.Errorf("quality: missing required section %q for schema_version %d: %w", "visual.compare", schemaVersion, ErrInvalid)
	}
	compareCfg, err := parseVisualCompareConfig(raw.Compare, *raw.ReportPath)
	if err != nil {
		return false, VisualConfig{}, err
	}

	return true, VisualConfig{
		Enabled: *raw.Enabled, Format: *raw.Format, Command: *raw.Command, ReportPath: *raw.ReportPath,
		Timeout: dur, Targets: *raw.Targets, FailOnConsoleError: *raw.FailOnConsoleError,
		A11yFailImpacts: impacts, Compare: compareCfg,
	}, nil
}

// parseVisualCompareConfig validates every `[visual.compare]` key (D4/D7).
// Presence of every key is ALWAYS required; the DIRECTORY constraints
// (non-empty, relative, clean, distinct, non-nested, and clear of
// reportPath) apply ONLY when Enabled is true (D13's posture) — with
// enabled=false, both directories may legitimately be empty strings, the
// shape D15 needs for a repository with no graphical interface.
func parseVisualCompareConfig(raw *rawVisualCompare, reportPath string) (VisualCompareConfig, error) {
	if raw.Enabled == nil {
		return VisualCompareConfig{}, fmt.Errorf("quality: missing required key %q: %w", "visual.compare.enabled", ErrInvalid)
	}
	if raw.ReferenceDir == nil {
		return VisualCompareConfig{}, fmt.Errorf("quality: missing required key %q: %w", "visual.compare.reference_dir", ErrInvalid)
	}
	if raw.CaptureDir == nil {
		return VisualCompareConfig{}, fmt.Errorf("quality: missing required key %q: %w", "visual.compare.capture_dir", ErrInvalid)
	}
	if raw.MaxDiffPct == nil {
		return VisualCompareConfig{}, fmt.Errorf("quality: missing required key %q: %w", "visual.compare.max_diff_pct", ErrInvalid)
	}
	if *raw.MaxDiffPct < 0 || *raw.MaxDiffPct > 100 {
		return VisualCompareConfig{}, fmt.Errorf(
			"quality: visual.compare.max_diff_pct %v must be in [0, 100]: %w", *raw.MaxDiffPct, ErrInvalid)
	}

	referenceDir, captureDir := *raw.ReferenceDir, *raw.CaptureDir

	if *raw.Enabled {
		if err := validateRelativeCleanPath(referenceDir, "visual.compare.reference_dir"); err != nil {
			return VisualCompareConfig{}, err
		}
		if err := validateRelativeCleanPath(captureDir, "visual.compare.capture_dir"); err != nil {
			return VisualCompareConfig{}, err
		}
		// D4: distinct and NON-NESTED in either direction, compared WITH a
		// separator (the same G5b lesson FilterUnderDir already applies) —
		// two sibling directories that merely share a string prefix
		// (".mneme/visual/reference" vs ".mneme/visual/reference-old") must
		// both be accepted.
		if referenceDir == captureDir {
			return VisualCompareConfig{}, fmt.Errorf(
				"quality: visual.compare.reference_dir and visual.compare.capture_dir must not be the same directory (%q): %w", referenceDir, ErrInvalid)
		}
		if dirContains(referenceDir, captureDir) || dirContains(captureDir, referenceDir) {
			return VisualCompareConfig{}, fmt.Errorf(
				"quality: visual.compare.reference_dir (%q) and visual.compare.capture_dir (%q) must not be nested inside one another: %w",
				referenceDir, captureDir, ErrInvalid)
		}
		if dirContains(referenceDir, reportPath) {
			return VisualCompareConfig{}, fmt.Errorf(
				"quality: visual.report_path (%q) must not fall inside visual.compare.reference_dir (%q): %w", reportPath, referenceDir, ErrInvalid)
		}
	}

	return VisualCompareConfig{
		Enabled: *raw.Enabled, ReferenceDir: referenceDir, CaptureDir: captureDir, MaxDiffPct: *raw.MaxDiffPct,
	}, nil
}

// dirContains reports whether child is parent itself or falls strictly
// under it, compared WITH a trailing separator after normalizing both to
// forward slashes (R-G/G5b) — never a bare string prefix: without the
// separator, ".mneme/visual/reference-old" would count as being "inside"
// ".mneme/visual/reference".
func dirContains(parent, child string) bool {
	p := strings.TrimSuffix(filepath.ToSlash(parent), "/")
	c := filepath.ToSlash(child)
	return c == p || strings.HasPrefix(c, p+"/")
}

// parseBudgetSection implements SPEC-118 D14's schema-version branching for
// `[budget]` — the same shape parseCriteriaSection already establishes for
// `[criteria]`: a strict single threshold (only schema 4 accepts it,
// declaring it under 1/2/3 is an explicit, named "sube schema_version a 4"
// error), schema 4 requires it present and complete. test_reach_depth is
// bounded to [1, 10] (D14): above 10 the traversal stops being a check and
// becomes a whole-repository walk.
func parseBudgetSection(schemaVersion int, raw *rawBudgetSection) (declared bool, cfg BudgetConfig, err error) {
	if schemaVersion < 4 {
		if raw != nil {
			return false, BudgetConfig{}, fmt.Errorf(
				"quality: [budget] declared under schema_version %d — sube schema_version a 4: %w", schemaVersion, ErrInvalid)
		}
		return false, BudgetConfig{}, nil
	}

	if raw == nil {
		return false, BudgetConfig{}, fmt.Errorf("quality: missing required section %q for schema_version %d: %w", "budget", schemaVersion, ErrInvalid)
	}

	if raw.Enabled == nil {
		return false, BudgetConfig{}, fmt.Errorf("quality: missing required key %q: %w", "budget.enabled", ErrInvalid)
	}

	if raw.Timeout == nil {
		return false, BudgetConfig{}, fmt.Errorf("quality: missing required key %q: %w", "budget.timeout", ErrInvalid)
	}
	dur, durErr := time.ParseDuration(*raw.Timeout)
	if durErr != nil || dur <= 0 {
		return false, BudgetConfig{}, fmt.Errorf(
			"quality: budget.timeout %q must be a positive parseable duration: %w", *raw.Timeout, ErrInvalid)
	}

	if raw.TestGlobs == nil || len(*raw.TestGlobs) == 0 {
		return false, BudgetConfig{}, fmt.Errorf("quality: missing required key %q: %w", "budget.test_globs", ErrInvalid)
	}
	for _, pattern := range *raw.TestGlobs {
		if _, matchErr := doublestar.Match(pattern, "probe"); matchErr != nil {
			return false, BudgetConfig{}, fmt.Errorf(
				"quality: budget.test_globs pattern %q invalid: %s: %w", pattern, matchErr, ErrInvalid)
		}
	}

	if raw.TestReachDepth == nil {
		return false, BudgetConfig{}, fmt.Errorf("quality: missing required key %q: %w", "budget.test_reach_depth", ErrInvalid)
	}
	if *raw.TestReachDepth < 1 || *raw.TestReachDepth > 10 {
		return false, BudgetConfig{}, fmt.Errorf(
			"quality: budget.test_reach_depth %d must be in [1, 10]: %w", *raw.TestReachDepth, ErrInvalid)
	}

	return true, BudgetConfig{
		Enabled: *raw.Enabled, Timeout: dur, TestGlobs: *raw.TestGlobs, TestReachDepth: *raw.TestReachDepth,
	}, nil
}

// parseCriteriaSection implements SPEC-117 D9's schema-version branching
// for [criteria] — the same shape parseCoverageAndRatchet already
// establishes for [coverage]/[ratchet], but as its OWN function rather
// than folded into that one: [criteria]'s presence rule is a strict single
// threshold (only schema 3 accepts it) rather than "schema 1 forbids it,
// everything else requires it", so sharing the function would smuggle a
// silent schema-4 assumption into code this spec has no way to test.
// Declaring [criteria] under schema 1 or 2 is a explicit, named error
// ("sube schema_version a 3") — never silently ignored — and schema 3
// requires it present and complete.
func parseCriteriaSection(schemaVersion int, raw *rawCriteria) (declared bool, cfg CriteriaConfig, err error) {
	if schemaVersion < 3 {
		if raw != nil {
			return false, CriteriaConfig{}, fmt.Errorf(
				"quality: [criteria] declared under schema_version %d — sube schema_version a 3: %w", schemaVersion, ErrInvalid)
		}
		return false, CriteriaConfig{}, nil
	}

	if raw == nil {
		return false, CriteriaConfig{}, fmt.Errorf("quality: missing required section %q for schema_version %d: %w", "criteria", schemaVersion, ErrInvalid)
	}

	if raw.Enabled == nil {
		return false, CriteriaConfig{}, fmt.Errorf("quality: missing required key %q: %w", "criteria.enabled", ErrInvalid)
	}

	if raw.Timeout == nil {
		return false, CriteriaConfig{}, fmt.Errorf("quality: missing required key %q: %w", "criteria.timeout", ErrInvalid)
	}
	dur, durErr := time.ParseDuration(*raw.Timeout)
	if durErr != nil || dur <= 0 {
		return false, CriteriaConfig{}, fmt.Errorf(
			"quality: criteria.timeout %q must be a positive parseable duration: %w", *raw.Timeout, ErrInvalid)
	}

	if raw.MaxManualPct == nil {
		return false, CriteriaConfig{}, fmt.Errorf("quality: missing required key %q: %w", "criteria.max_manual_pct", ErrInvalid)
	}
	if *raw.MaxManualPct < 0 || *raw.MaxManualPct > 100 {
		return false, CriteriaConfig{}, fmt.Errorf(
			"quality: criteria.max_manual_pct %v must be in [0, 100]: %w", *raw.MaxManualPct, ErrInvalid)
	}

	if raw.MaxCommandPct == nil {
		return false, CriteriaConfig{}, fmt.Errorf("quality: missing required key %q: %w", "criteria.max_command_pct", ErrInvalid)
	}
	if *raw.MaxCommandPct < 0 || *raw.MaxCommandPct > 100 {
		return false, CriteriaConfig{}, fmt.Errorf(
			"quality: criteria.max_command_pct %v must be in [0, 100]: %w", *raw.MaxCommandPct, ErrInvalid)
	}

	return true, CriteriaConfig{
		Enabled:       *raw.Enabled,
		Timeout:       dur,
		MaxManualPct:  *raw.MaxManualPct,
		MaxCommandPct: *raw.MaxCommandPct,
	}, nil
}

// parseCoverageAndRatchet implements D5's schema-version branching for
// [coverage]/[ratchet]: under schema_version 1 both sections MUST be
// absent (declaring either is an explicit "bump schema_version to 2"
// error, never a silent tolerance); under CurrentSchemaVersion (2) both
// are REQUIRED and fully validated. The binary never fills in a default
// for either — a schema-1 document's CoverageConfig/RatchetConfig are the
// exact zero value and the service layer branches on the Declared flags,
// never on a field of an undeclared config (D5).
func parseCoverageAndRatchet(schemaVersion int, rawCov *rawCoverage, rawRat *rawRatchet) (
	coverageDeclared bool, coverageCfg CoverageConfig,
	ratchetDeclared bool, ratchetCfg RatchetConfig,
	err error,
) {
	if schemaVersion == 1 {
		if rawCov != nil {
			return false, CoverageConfig{}, false, RatchetConfig{}, fmt.Errorf(
				"quality: [coverage] declared under schema_version 1 — sube schema_version a 2: %w", ErrInvalid)
		}
		if rawRat != nil {
			return false, CoverageConfig{}, false, RatchetConfig{}, fmt.Errorf(
				"quality: [ratchet] declared under schema_version 1 — sube schema_version a 2: %w", ErrInvalid)
		}
		return false, CoverageConfig{}, false, RatchetConfig{}, nil
	}

	// schemaVersion == CurrentSchemaVersion (2): both sections required.
	if rawCov == nil {
		return false, CoverageConfig{}, false, RatchetConfig{}, fmt.Errorf(
			"quality: missing required section %q for schema_version %d: %w", "coverage", schemaVersion, ErrInvalid)
	}
	if rawRat == nil {
		return false, CoverageConfig{}, false, RatchetConfig{}, fmt.Errorf(
			"quality: missing required section %q for schema_version %d: %w", "ratchet", schemaVersion, ErrInvalid)
	}

	coverageCfg, err = parseCoverageConfig(rawCov)
	if err != nil {
		return false, CoverageConfig{}, false, RatchetConfig{}, err
	}
	ratchetCfg, err = parseRatchetConfig(rawRat)
	if err != nil {
		return false, CoverageConfig{}, false, RatchetConfig{}, err
	}

	if ratchetCfg.Enabled && !coverageCfg.Enabled {
		return false, CoverageConfig{}, false, RatchetConfig{}, fmt.Errorf(
			"quality: ratchet.enabled=true requires coverage.enabled=true (the ratchet feeds off the same profile): %w", ErrInvalid)
	}

	return true, coverageCfg, true, ratchetCfg, nil
}

// parseCoverageConfig validates every [coverage] key, each missing or
// invalid value naming itself in the returned error (D6).
func parseCoverageConfig(raw *rawCoverage) (CoverageConfig, error) {
	if raw.Enabled == nil {
		return CoverageConfig{}, fmt.Errorf("quality: missing required key %q: %w", "coverage.enabled", ErrInvalid)
	}

	if raw.Format == nil {
		return CoverageConfig{}, fmt.Errorf("quality: missing required key %q: %w", "coverage.format", ErrInvalid)
	}
	accepted := Formats()
	if !slices.Contains(accepted, *raw.Format) {
		return CoverageConfig{}, fmt.Errorf(
			"quality: coverage.format %q must be one of %v: %w", *raw.Format, accepted, ErrInvalid)
	}

	if raw.Command == nil {
		return CoverageConfig{}, fmt.Errorf("quality: missing required key %q: %w", "coverage.command", ErrInvalid)
	}
	if len(*raw.Command) == 0 {
		return CoverageConfig{}, fmt.Errorf("quality: missing required key %q: %w", "coverage.command", ErrInvalid)
	}
	if msg, bad := argvShellStringProblem(*raw.Command); bad {
		return CoverageConfig{}, fmt.Errorf("quality: coverage.command %s: %w", msg, ErrInvalid)
	}

	if raw.ProfilePath == nil {
		return CoverageConfig{}, fmt.Errorf("quality: missing required key %q: %w", "coverage.profile_path", ErrInvalid)
	}
	if err := validateRelativeCleanPath(*raw.ProfilePath, "coverage.profile_path"); err != nil {
		return CoverageConfig{}, err
	}

	if raw.Timeout == nil {
		return CoverageConfig{}, fmt.Errorf("quality: missing required key %q: %w", "coverage.timeout", ErrInvalid)
	}
	dur, err := time.ParseDuration(*raw.Timeout)
	if err != nil || dur <= 0 {
		return CoverageConfig{}, fmt.Errorf(
			"quality: coverage.timeout %q must be a positive parseable duration: %w", *raw.Timeout, ErrInvalid)
	}

	if raw.MinDiffLinePct == nil {
		return CoverageConfig{}, fmt.Errorf("quality: missing required key %q: %w", "coverage.min_diff_line_pct", ErrInvalid)
	}
	if *raw.MinDiffLinePct <= 0 || *raw.MinDiffLinePct > 100 {
		return CoverageConfig{}, fmt.Errorf(
			"quality: coverage.min_diff_line_pct %v must be in (0, 100]: %w", *raw.MinDiffLinePct, ErrInvalid)
	}

	if raw.MinChangedLines == nil {
		return CoverageConfig{}, fmt.Errorf("quality: missing required key %q: %w", "coverage.min_changed_lines", ErrInvalid)
	}
	if *raw.MinChangedLines < 1 {
		return CoverageConfig{}, fmt.Errorf(
			"quality: coverage.min_changed_lines %d must be >= 1: %w", *raw.MinChangedLines, ErrInvalid)
	}

	if raw.Exclude == nil {
		return CoverageConfig{}, fmt.Errorf("quality: missing required key %q: %w", "coverage.exclude", ErrInvalid)
	}
	for _, pattern := range *raw.Exclude {
		if _, err := doublestar.Match(pattern, "probe"); err != nil {
			return CoverageConfig{}, fmt.Errorf(
				"quality: coverage.exclude pattern %q invalid: %s: %w", pattern, err, ErrInvalid)
		}
	}

	return CoverageConfig{
		Enabled:         *raw.Enabled,
		Format:          *raw.Format,
		Command:         *raw.Command,
		ProfilePath:     *raw.ProfilePath,
		Timeout:         dur,
		MinDiffLinePct:  *raw.MinDiffLinePct,
		MinChangedLines: *raw.MinChangedLines,
		Exclude:         *raw.Exclude,
	}, nil
}

// parseRatchetConfig validates every [ratchet] key.
func parseRatchetConfig(raw *rawRatchet) (RatchetConfig, error) {
	if raw.Enabled == nil {
		return RatchetConfig{}, fmt.Errorf("quality: missing required key %q: %w", "ratchet.enabled", ErrInvalid)
	}
	if raw.MaxGlobalLinePctDrop == nil {
		return RatchetConfig{}, fmt.Errorf("quality: missing required key %q: %w", "ratchet.max_global_line_pct_drop", ErrInvalid)
	}
	if *raw.MaxGlobalLinePctDrop < 0 {
		return RatchetConfig{}, fmt.Errorf(
			"quality: ratchet.max_global_line_pct_drop %v must be >= 0: %w", *raw.MaxGlobalLinePctDrop, ErrInvalid)
	}
	if raw.MaxBaselineStalenessPct == nil {
		return RatchetConfig{}, fmt.Errorf("quality: missing required key %q: %w", "ratchet.max_baseline_staleness_pct", ErrInvalid)
	}
	if *raw.MaxBaselineStalenessPct <= 0 {
		return RatchetConfig{}, fmt.Errorf(
			"quality: ratchet.max_baseline_staleness_pct %v must be > 0: %w", *raw.MaxBaselineStalenessPct, ErrInvalid)
	}
	if *raw.MaxBaselineStalenessPct < *raw.MaxGlobalLinePctDrop {
		return RatchetConfig{}, fmt.Errorf(
			"quality: ratchet.max_baseline_staleness_pct (%v) must be >= ratchet.max_global_line_pct_drop (%v): %w",
			*raw.MaxBaselineStalenessPct, *raw.MaxGlobalLinePctDrop, ErrInvalid)
	}

	return RatchetConfig{
		Enabled:                 *raw.Enabled,
		MaxGlobalLinePctDrop:    *raw.MaxGlobalLinePctDrop,
		MaxBaselineStalenessPct: *raw.MaxBaselineStalenessPct,
	}, nil
}

// validateRelativeCleanPath validates that p is non-empty, relative, and
// contains no ".." component after cleaning — the shared rule
// coverage.profile_path needs (D6, R6): the value D12 later deletes must
// never be able to escape the repository root.
func validateRelativeCleanPath(p, keyName string) error {
	if p == "" {
		return fmt.Errorf("quality: %s must not be empty: %w", keyName, ErrInvalid)
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("quality: %s %q must be a relative path: %w", keyName, p, ErrInvalid)
	}
	slashed := filepath.ToSlash(p)
	cleaned := strings.TrimPrefix(slashed, "./")
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") || strings.HasSuffix(cleaned, "/..") {
		return fmt.Errorf("quality: %s %q must not contain '..': %w", keyName, p, ErrInvalid)
	}
	return nil
}

// argvShellStringProblem is the SHARED validator behind both a gate's
// `command` and `coverage.command` (D6/AC5): a single-element command
// containing a space looks like a shell string rather than an argv vector.
// Returning the message fragment (rather than a ready-made error) is what
// lets both call sites produce byte-identical wording for the shared part
// of the message while still naming their own subject (a gate's name, or
// the literal key "coverage.command") — the two can never drift apart
// because there is only one place this sentence is written.
func argvShellStringProblem(cmd []string) (msg string, bad bool) {
	if len(cmd) == 1 && strings.Contains(cmd[0], " ") {
		return fmt.Sprintf(
			"command %q looks like a single shell string — command is an argv vector, declare each argument as its own list element (e.g. [\"make\", \"test\"], not [\"make test\"])",
			cmd[0]), true
	}
	return "", false
}

// parseGates validates every [[gate]] entry: a required name (safe-slug,
// unique), a non-empty argv command (never a single shell-string element), a
// positive parseable timeout, and a present required flag.
func parseGates(raw []rawGate) ([]Gate, error) {
	seen := make(map[string]bool, len(raw))
	gates := make([]Gate, 0, len(raw))

	for i, rg := range raw {
		if rg.Name == "" {
			return nil, fmt.Errorf("quality: missing required key %q for gate at index %d: %w", "name", i, ErrInvalid)
		}
		if !safeSlugPattern.MatchString(rg.Name) {
			return nil, fmt.Errorf("quality: gate name %q must match %s: %w", rg.Name, safeSlugPattern.String(), ErrInvalid)
		}
		if seen[rg.Name] {
			return nil, fmt.Errorf("quality: duplicate gate name %q: %w", rg.Name, ErrInvalid)
		}
		seen[rg.Name] = true

		if len(rg.Command) == 0 {
			return nil, fmt.Errorf("quality: missing required key %q for gate %q: %w", "command", rg.Name, ErrInvalid)
		}
		// The SAME shared validator coverage.command uses (AC5) — the
		// explanatory sentence is written in exactly one place
		// (argvShellStringProblem) so the two can never drift apart.
		if msg, bad := argvShellStringProblem(rg.Command); bad {
			return nil, fmt.Errorf("quality: gate %q %s: %w", rg.Name, msg, ErrInvalid)
		}

		if rg.Timeout == "" {
			return nil, fmt.Errorf("quality: missing required key %q for gate %q: %w", "timeout", rg.Name, ErrInvalid)
		}
		dur, err := time.ParseDuration(rg.Timeout)
		if err != nil || dur <= 0 {
			return nil, fmt.Errorf("quality: gate %q timeout %q must be a positive parseable duration: %w", rg.Name, rg.Timeout, ErrInvalid)
		}

		if rg.Required == nil {
			return nil, fmt.Errorf("quality: missing required key %q for gate %q: %w", "required", rg.Name, ErrInvalid)
		}

		gates = append(gates, Gate{
			Name:     rg.Name,
			Command:  rg.Command,
			Timeout:  dur,
			Required: *rg.Required,
		})
	}

	return gates, nil
}

// schemaPeek is the lax decode target of PeekSchemaVersion — only the one
// field, no unknown-field rejection, no range/format validation.
type schemaPeek struct {
	SchemaVersion int `toml:"schema_version"`
}

// PeekSchemaVersion reads only the schema_version key from raw TOML bytes,
// tolerating everything else about the document — including a
// schema_version Parse itself would reject. It is deliberately separate from
// Parse: mneme init's drift detector (D15) exists precisely to warn about a
// constitution written by an older/newer schema, which by definition Parse
// cannot read. Without this laxer path that drift branch would be
// unreachable and untestable against a real fixture.
func PeekSchemaVersion(data []byte) (int, error) {
	var p schemaPeek
	if err := toml.Unmarshal(data, &p); err != nil {
		return 0, fmt.Errorf("quality: peek schema version: %w", err)
	}
	return p.SchemaVersion, nil
}

// HashBytes returns the sha256 hex digest of raw constitution bytes — never
// of the parsed struct. Hashing the struct would make the hash blind to a
// changed comment or whitespace; hashing the bytes means ANY edit to the
// file, however cosmetic, produces a different hash (AC4), which is what
// lets D9's tamper-detection checks work without false negatives.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
