package delivery

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// SchemaVersion is the only schema_version Parse accepts. The block lives in
// the local tarea.md and never travels between machines, so there is nothing
// for a version bump to widen; the key exists so the format can change later
// without ambiguity.
const SchemaVersion = 1

// StartMarker and EndMarker delimit the contract block inside the entrega
// section; each must sit alone on its own line (surrounding spaces allowed).
// HTML comments are used instead of a code fence because any ```toml fence in
// the prose would be mistaken for the block, while these lines cannot be.
const (
	StartMarker = "<!-- mneme:entrega:contrato:start -->"
	EndMarker   = "<!-- mneme:entrega:contrato:end -->"
)

// MaxOutputLines and MaxOutputBytes cap the salida excerpt of one evidencia
// entry: an excerpt, not a log.
const (
	MaxOutputLines = 40
	MaxOutputBytes = 4096
)

// ErrMissing is returned when the text carries no delimiter at all. Callers
// decide whether that is a warning or a refusal; SPEC-182 refuses it only for
// a standard spec in implementing.
var ErrMissing = errors.New("delivery: contract block missing")

// ErrInvalid is returned when a block was found but is malformed: misplaced or
// repeated delimiters, an unclosed fence, broken TOML, an unknown key, or a
// value that breaks one of the rules of SPEC-182.
var ErrInvalid = errors.New("delivery: invalid contract block")

// ErrUnsupportedSchema is returned when the block declares a schema_version
// this reader does not recognise.
var ErrUnsupportedSchema = errors.New("delivery: contract schema_version unsupported")

// Delivery states (the "estado" key). Their meaning is fixed by SPEC-182 and
// repeated in the mneme-flow skill (references/entrega.md).
const (
	// StateDone means everything asked for is done and the checks ran.
	StateDone = "hecho"
	// StateDoneConcerns means done, with doubts about correctness or scope
	// explained in the prose.
	StateDoneConcerns = "hecho_con_dudas"
	// StateNeedsContext means the work cannot start or continue without
	// information or a decision the implementer does not have.
	StateNeedsContext = "falta_contexto"
	// StateBlocked means the work cannot be completed, including partial work
	// cut short by the end of a turn or of the context window.
	StateBlocked = "bloqueado"
)

// Red-test actions (the "accion" key of [[prueba_en_rojo]]).
const (
	// ActionStopped means the implementer stopped and reported the red test.
	ActionStopped = "parada_y_reportada"
	// ActionEdited means the test was edited because the spec's design
	// foresaw that edit.
	ActionEdited = "editada"
)

// RiskNone is the string value of riesgo when no risk item applies.
const RiskNone = "none"

// MinRiskItem and MaxRiskItem bound riesgo.item: the closed list of six risks
// lives in the mneme-flow skill, not here.
const (
	MinRiskItem = 1
	MaxRiskItem = 6
)

// fence is the only code fence Parse strips around the TOML: three backticks.
const fence = "```"

// idPattern is the shape a criterio must have. It is a private copy of the
// same regular expression in internal/acceptance (criterionIDPattern) and in
// internal/specreview (idPattern); the three packages are leaves and cannot
// share it, so a change to one must be made to all three.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

// treePattern is the shape of arbol: an abbreviated or full commit SHA.
var treePattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// validStates lists the four states in the order error messages name them.
var validStates = []string{StateDone, StateDoneConcerns, StateNeedsContext, StateBlocked}

// Evidence is one parsed [[evidencia]] entry: a verification command the
// implementer says it ran, with its exit code and an output excerpt.
type Evidence struct {
	Command   string
	ExitCode  int64
	Output    string
	Criterion string
}

// RedTest is one parsed [[prueba_en_rojo]] entry: an existing test that went
// red, what was done about it and why.
type RedTest struct {
	Name   string
	Action string
	Reason string
}

// Risk is the parsed riesgo key. None is true for riesgo = "none"; otherwise
// Item is in MinRiskItem..MaxRiskItem and Reason is non-empty.
type Risk struct {
	None   bool
	Item   int
	Reason string
}

// Contract is the parsed and validated delivery contract block.
type Contract struct {
	SchemaVersion int
	State         string
	Tree          string
	Evidence      []Evidence
	RedTests      []RedTest
	Risk          Risk
}

// schemaProbe is the first-pass decode target: only the version, decoded
// without strictness so that a future schema with unknown keys still reports
// ErrUnsupportedSchema instead of a misleading unknown-key error.
type schemaProbe struct {
	SchemaVersion *int `toml:"schema_version"`
}

type rawEvidence struct {
	Command   string `toml:"comando"`
	ExitCode  *int64 `toml:"exit_code"`
	Output    string `toml:"salida"`
	Criterion string `toml:"criterio"`
}

type rawRedTest struct {
	Name   string `toml:"nombre"`
	Action string `toml:"accion"`
	Reason string `toml:"motivo"`
}

type rawContract struct {
	SchemaVersion *int          `toml:"schema_version"`
	State         string        `toml:"estado"`
	Tree          string        `toml:"arbol"`
	Risk          any           `toml:"riesgo"`
	Evidence      []rawEvidence `toml:"evidencia"`
	RedTests      []rawRedTest  `toml:"prueba_en_rojo"`
}

// Parse locates the single contract block in text (the body of the entrega
// section, never the whole tarea.md), strips an optional backtick fence, and
// validates it by rules R1..R10 of SPEC-182. Pure: no I/O, no clock. Every
// error wraps exactly one of ErrMissing, ErrInvalid or ErrUnsupportedSchema
// and names the failing field.
func Parse(text []byte) (*Contract, error) {
	inner, err := extractBlock(string(text))
	if err != nil {
		return nil, err
	}
	data, err := stripFence(inner)
	if err != nil {
		return nil, err
	}

	var probe schemaProbe
	if err := toml.Unmarshal([]byte(data), &probe); err != nil {
		return nil, fmt.Errorf("delivery: parse delivery contract: %s: %w", err, ErrInvalid)
	}
	if probe.SchemaVersion == nil {
		return nil, fmt.Errorf("delivery: missing required key %q: %w", "schema_version", ErrInvalid)
	}
	if *probe.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("delivery: schema_version %d not supported (only %d): %w",
			*probe.SchemaVersion, SchemaVersion, ErrUnsupportedSchema)
	}

	raw, err := decodeStrict(data)
	if err != nil {
		return nil, err
	}
	return validate(raw)
}

// extractBlock applies R1: it counts the delimiter lines wherever they are
// (inside a salida too) and returns the lines between exactly one start and
// one later end.
func extractBlock(text string) ([]string, error) {
	lines := strings.Split(text, "\n")
	var starts, ends []int
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case StartMarker:
			starts = append(starts, i)
		case EndMarker:
			ends = append(ends, i)
		}
	}

	switch {
	case len(starts) == 0 && len(ends) == 0:
		return nil, fmt.Errorf("delivery: no contract block between %s and %s: %w", StartMarker, EndMarker, ErrMissing)
	case len(starts) > 1:
		return nil, fmt.Errorf("delivery: start marker duplicated (%d lines): %w", len(starts), ErrInvalid)
	case len(ends) > 1:
		return nil, fmt.Errorf("delivery: end marker duplicated (%d lines): %w", len(ends), ErrInvalid)
	case len(ends) == 0:
		return nil, fmt.Errorf("delivery: end marker missing: %w", ErrInvalid)
	case len(starts) == 0:
		return nil, fmt.Errorf("delivery: start marker missing: %w", ErrInvalid)
	case ends[0] < starts[0]:
		return nil, fmt.Errorf("delivery: end marker before start marker: %w", ErrInvalid)
	}

	inner := lines[starts[0]+1 : ends[0]]
	out := make([]string, len(inner))
	for i, line := range inner {
		out[i] = strings.TrimRight(line, "\r")
	}
	return out, nil
}

// stripFence applies R2: blank lines at both ends are dropped, an optional
// backtick fence is removed, and an empty result is invalid.
func stripFence(lines []string) (string, error) {
	lines = trimBlankLines(lines)
	if len(lines) == 0 {
		return "", fmt.Errorf("delivery: empty contract block: %w", ErrInvalid)
	}
	if strings.HasPrefix(strings.TrimSpace(lines[0]), fence) {
		if len(lines) < 2 || strings.TrimSpace(lines[len(lines)-1]) != fence {
			return "", fmt.Errorf("delivery: unclosed fence: the last line of the block must be %s: %w", fence, ErrInvalid)
		}
		lines = trimBlankLines(lines[1 : len(lines)-1])
		if len(lines) == 0 {
			return "", fmt.Errorf("delivery: empty contract block inside the fence: %w", ErrInvalid)
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// trimBlankLines drops whitespace-only lines from both ends.
func trimBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// decodeStrict applies R4. go-toml's StrictMissingError does not name the
// unknown key in its Error(), and DecodeError.Error() names the Go field, not
// the document line, so the message is composed here (amendment E1).
func decodeStrict(data string) (*rawContract, error) {
	dec := toml.NewDecoder(bytes.NewReader([]byte(data)))
	dec.DisallowUnknownFields()
	var raw rawContract
	err := dec.Decode(&raw)
	if err == nil {
		return &raw, nil
	}

	var strict *toml.StrictMissingError
	if errors.As(err, &strict) {
		keys := make([]string, 0, len(strict.Errors))
		for i := range strict.Errors {
			keys = append(keys, fmt.Sprintf("%q", strings.Join(strict.Errors[i].Key(), ".")))
		}
		return nil, fmt.Errorf("delivery: unknown key %s: %w", strings.Join(keys, ", "), ErrInvalid)
	}
	var de *toml.DecodeError
	if errors.As(err, &de) {
		return nil, fmt.Errorf("delivery: parse delivery contract: %s: %w", de.String(), ErrInvalid)
	}
	return nil, fmt.Errorf("delivery: parse delivery contract: %s: %w", err, ErrInvalid)
}

// validate applies R5..R10 in order; the first failing rule wins.
func validate(raw *rawContract) (*Contract, error) {
	c := &Contract{SchemaVersion: *raw.SchemaVersion, State: raw.State, Tree: raw.Tree}

	if err := checkState(raw.State); err != nil {
		return nil, err
	}
	if err := checkTree(raw.State, raw.Tree); err != nil {
		return nil, err
	}
	evidence, err := checkEvidence(raw.State, raw.Evidence)
	if err != nil {
		return nil, err
	}
	c.Evidence = evidence
	redTests, err := checkRedTests(raw.RedTests)
	if err != nil {
		return nil, err
	}
	c.RedTests = redTests
	risk, err := checkRisk(raw.Risk)
	if err != nil {
		return nil, err
	}
	c.Risk = risk

	if c.State == StateDone {
		for i, rt := range c.RedTests {
			if rt.Action == ActionStopped {
				return nil, fmt.Errorf("delivery: prueba_en_rojo[%d] %q: accion %s is incompatible with estado %q: %w",
					i, rt.Name, ActionStopped, StateDone, ErrInvalid)
			}
		}
	}
	return c, nil
}

// done reports whether state claims the work is finished, the two states that
// require arbol and at least one evidencia.
func done(state string) bool {
	return state == StateDone || state == StateDoneConcerns
}

// checkState applies R5.
func checkState(state string) error {
	if strings.TrimSpace(state) == "" {
		return fmt.Errorf("delivery: missing required key %q: %w", "estado", ErrInvalid)
	}
	for _, s := range validStates {
		if state == s {
			return nil
		}
	}
	return fmt.Errorf("delivery: estado %q (valid: %s): %w", state, strings.Join(validStates, ", "), ErrInvalid)
}

// checkTree applies R6.
func checkTree(state, tree string) error {
	if tree == "" {
		if done(state) {
			return fmt.Errorf("delivery: estado %q requires arbol: %w", state, ErrInvalid)
		}
		return nil
	}
	if !treePattern.MatchString(tree) {
		return fmt.Errorf("delivery: arbol %q must match %s: %w", tree, treePattern, ErrInvalid)
	}
	return nil
}

// checkEvidence applies R7.
func checkEvidence(state string, raw []rawEvidence) ([]Evidence, error) {
	if done(state) && len(raw) == 0 {
		return nil, fmt.Errorf("delivery: estado %q requires at least one evidencia: %w", state, ErrInvalid)
	}
	out := make([]Evidence, 0, len(raw))
	for i, e := range raw {
		if strings.TrimSpace(e.Command) == "" {
			return nil, fmt.Errorf("delivery: evidencia[%d]: blank comando: %w", i, ErrInvalid)
		}
		if e.ExitCode == nil {
			return nil, fmt.Errorf("delivery: evidencia[%d]: missing required key %q: %w", i, "exit_code", ErrInvalid)
		}
		if e.Output != "" {
			if n := len(strings.Split(strings.TrimRight(e.Output, "\n"), "\n")); n > MaxOutputLines {
				return nil, fmt.Errorf("delivery: evidencia[%d]: salida has %d lines (max %d): %w",
					i, n, MaxOutputLines, ErrInvalid)
			}
			if n := len(e.Output); n > MaxOutputBytes {
				return nil, fmt.Errorf("delivery: evidencia[%d]: salida is %d bytes (max %d): %w",
					i, n, MaxOutputBytes, ErrInvalid)
			}
		}
		if e.Criterion != "" && !idPattern.MatchString(e.Criterion) {
			return nil, fmt.Errorf("delivery: evidencia[%d]: criterio %q must match %s: %w",
				i, e.Criterion, idPattern, ErrInvalid)
		}
		out = append(out, Evidence{Command: e.Command, ExitCode: *e.ExitCode, Output: e.Output, Criterion: e.Criterion})
	}
	return out, nil
}

// checkRedTests applies R8.
func checkRedTests(raw []rawRedTest) ([]RedTest, error) {
	out := make([]RedTest, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for i, rt := range raw {
		if strings.TrimSpace(rt.Name) == "" {
			return nil, fmt.Errorf("delivery: prueba_en_rojo[%d]: missing required key %q: %w", i, "nombre", ErrInvalid)
		}
		if seen[rt.Name] {
			return nil, fmt.Errorf("delivery: prueba_en_rojo[%d]: duplicate nombre %q: %w", i, rt.Name, ErrInvalid)
		}
		seen[rt.Name] = true
		if rt.Action != ActionStopped && rt.Action != ActionEdited {
			return nil, fmt.Errorf("delivery: prueba_en_rojo[%d]: unknown accion %q (valid: %s, %s): %w",
				i, rt.Action, ActionStopped, ActionEdited, ErrInvalid)
		}
		if strings.TrimSpace(rt.Reason) == "" {
			return nil, fmt.Errorf("delivery: prueba_en_rojo[%d]: missing required key %q: %w", i, "motivo", ErrInvalid)
		}
		out = append(out, RedTest(rt))
	}
	return out, nil
}

// checkRisk applies R9. riesgo decodes to any, and the strict decoder does not
// look inside any, so this check also rejects extra keys in the table.
func checkRisk(raw any) (Risk, error) {
	switch v := raw.(type) {
	case nil:
		return Risk{}, fmt.Errorf("delivery: missing required key %q: %w", "riesgo", ErrInvalid)
	case string:
		if v != RiskNone {
			return Risk{}, fmt.Errorf("delivery: riesgo %q must be %q or a table { item, razon }: %w", v, RiskNone, ErrInvalid)
		}
		return Risk{None: true}, nil
	case map[string]any:
		return checkRiskTable(v)
	default:
		return Risk{}, fmt.Errorf("delivery: riesgo has type %T (want %q or a table { item, razon }): %w", raw, RiskNone, ErrInvalid)
	}
}

// checkRiskTable validates the table form of riesgo: exactly item and razon.
func checkRiskTable(m map[string]any) (Risk, error) {
	var extra []string
	for k := range m {
		if k != "item" && k != "razon" {
			extra = append(extra, fmt.Sprintf("%q", k))
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return Risk{}, fmt.Errorf("delivery: riesgo: unknown key %s: %w", strings.Join(extra, ", "), ErrInvalid)
	}

	var item int64
	switch v := m["item"].(type) {
	case nil:
		return Risk{}, fmt.Errorf("delivery: riesgo.item is required: %w", ErrInvalid)
	case int64:
		item = v
	case int:
		item = int64(v)
	default:
		return Risk{}, fmt.Errorf("delivery: riesgo.item has type %T (want integer): %w", v, ErrInvalid)
	}
	if item < MinRiskItem || item > MaxRiskItem {
		return Risk{}, fmt.Errorf("delivery: riesgo.item %d out of range %d..%d: %w", item, MinRiskItem, MaxRiskItem, ErrInvalid)
	}

	reason, ok := m["razon"].(string)
	if !ok {
		return Risk{}, fmt.Errorf("delivery: riesgo.razon has type %T (want string): %w", m["razon"], ErrInvalid)
	}
	if strings.TrimSpace(reason) == "" {
		return Risk{}, fmt.Errorf("delivery: riesgo.razon is empty: %w", ErrInvalid)
	}
	return Risk{Item: int(item), Reason: reason}, nil
}
