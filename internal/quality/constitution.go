package quality

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// CurrentSchemaVersion is the only schema_version Parse accepts. A
// constitution written by a newer or older mneme fails closed with
// ErrUnsupportedSchema rather than being silently reinterpreted.
const CurrentSchemaVersion = 1

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
	// SchemaVersion is always CurrentSchemaVersion after a successful Parse.
	SchemaVersion int

	// Enabled is the mechanism's on/off switch (D3). While false, nothing in
	// this constitution blocks spec_advance.
	Enabled bool

	// Execution holds storage bounds for gate output.
	Execution ExecutionConfig

	// Gates is the ordered list of declared gates, executed sequentially in
	// this order (D6).
	Gates []Gate
}

// rawConstitution is the strict decode target for Parse. Every field a human
// must supply is a pointer (or, for Gate.Required, decoded via rawGate) so
// Parse can tell "absent" from "present with the zero value" — a plain bool
// or int field would make a missing `enabled = false` indistinguishable from
// an explicitly declared one, defeating D13's "no defaults in the binary".
type rawConstitution struct {
	SchemaVersion *int         `toml:"schema_version"`
	Enabled       *bool        `toml:"enabled"`
	Execution     rawExecution `toml:"execution"`
	Gates         []rawGate    `toml:"gate"`
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
	if *raw.SchemaVersion != CurrentSchemaVersion {
		return nil, fmt.Errorf(
			"quality: schema_version %d escrito por una mneme más nueva/vieja: actualiza mneme: %w",
			*raw.SchemaVersion, ErrUnsupportedSchema)
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

	return &Constitution{
		SchemaVersion: *raw.SchemaVersion,
		Enabled:       *raw.Enabled,
		Execution:     ExecutionConfig{OutputTailBytes: *raw.Execution.OutputTailBytes},
		Gates:         gates,
	}, nil
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
		if len(rg.Command) == 1 && strings.Contains(rg.Command[0], " ") {
			return nil, fmt.Errorf(
				"quality: gate %q command %q looks like a single shell string — command is an argv vector, declare each argument as its own list element (e.g. [\"make\", \"test\"], not [\"make test\"]): %w",
				rg.Name, rg.Command[0], ErrInvalid)
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
