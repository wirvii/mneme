package quality

import (
	"errors"
	"strings"
	"testing"
)

// validCriteriaDoc is a minimal, fully valid criteria.toml with the three
// modes represented — the base fixture AC5/AC6 tables mutate from.
const validCriteriaDoc = `
schema_version = 1

[[criterion]]
id = "AC1"
mode = "assert"
text = "internal/quality gana el parser de criterios."
  [[criterion.assert]]
  verb = "file_exists"
  path = "internal/quality/criteria.go"
  new = true

[[criterion]]
id = "AC20"
mode = "command"
text = "La suite completa pasa con -race."
command = ["make", "test-race"]
timeout = "25m"

[[criterion]]
id = "AC28"
mode = "manual"
text = "El panel en modo oscuro no rompe el contraste."
evidence_required = "captura adjunta y ratio de contraste medido"
`

// TestParseCriteria_Valid confirms the fixture parses into the expected
// shape, one row per mode.
func TestParseCriteria_Valid(t *testing.T) {
	doc, err := ParseCriteria([]byte(validCriteriaDoc))
	if err != nil {
		t.Fatalf("ParseCriteria: %v", err)
	}
	if doc.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", doc.SchemaVersion)
	}
	if len(doc.Criteria) != 3 {
		t.Fatalf("Criteria = %d, want 3", len(doc.Criteria))
	}

	assertC := doc.Criteria[0]
	if assertC.Mode != ModeAssert || len(assertC.Assert) != 1 {
		t.Fatalf("Criteria[0] = %+v, want one assert-mode criterion with one assertion", assertC)
	}
	if assertC.Assert[0].Verb != VerbFileExists || !assertC.Assert[0].New {
		t.Errorf("Criteria[0].Assert[0] = %+v, want file_exists new=true", assertC.Assert[0])
	}

	commandC := doc.Criteria[1]
	if commandC.Mode != ModeCommand || len(commandC.Command) != 2 || commandC.Timeout <= 0 {
		t.Errorf("Criteria[1] = %+v, want a two-element command mode with positive timeout", commandC)
	}

	manualC := doc.Criteria[2]
	if manualC.Mode != ModeManual || manualC.EvidenceRequired == "" {
		t.Errorf("Criteria[2] = %+v, want manual mode with non-empty evidence_required", manualC)
	}
}

// TestParseCriteria_Rejections covers AC5: the eight rejection rows plus
// the hermana positive (the last row of validCriteriaDoc itself parsing
// clean, already covered by TestParseCriteria_Valid above).
func TestParseCriteria_Rejections(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantErr error
		wantKey string
	}{
		{
			name:    "unknown top-level key",
			doc:     validCriteriaDoc + "\nunknown_key = true\n",
			wantErr: ErrInvalidCriteria,
		},
		{
			name: "missing schema_version",
			doc: `
[[criterion]]
id = "AC1"
mode = "manual"
text = "x"
evidence_required = "y"
`,
			wantErr: ErrInvalidCriteria,
			wantKey: "schema_version",
		},
		{
			name:    "schema_version 2 unsupported",
			doc:     strings.Replace(validCriteriaDoc, "schema_version = 1", "schema_version = 2", 1),
			wantErr: ErrUnsupportedCriteriaSchema,
		},
		{
			name: "duplicate id",
			doc: validCriteriaDoc + `
[[criterion]]
id = "AC1"
mode = "manual"
text = "duplicado"
evidence_required = "y"
`,
			wantErr: ErrInvalidCriteria,
			wantKey: "AC1",
		},
		{
			name:    "id does not match pattern",
			doc:     strings.Replace(validCriteriaDoc, `id = "AC1"`, `id = " AC1"`, 1),
			wantErr: ErrInvalidCriteria,
		},
		{
			name:    "empty text",
			doc:     strings.Replace(validCriteriaDoc, `text = "internal/quality gana el parser de criterios."`, `text = ""`, 1),
			wantErr: ErrInvalidCriteria,
			wantKey: "text",
		},
		{
			name:    "mode outside the closed set",
			doc:     strings.Replace(validCriteriaDoc, `mode = "manual"`, `mode = "opinion"`, 1),
			wantErr: ErrInvalidCriteria,
			wantKey: "assert",
		},
		{
			name: "zero criteria",
			doc: `
schema_version = 1
`,
			wantErr: ErrInvalidCriteria,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCriteria([]byte(tt.doc))
			if err == nil {
				t.Fatalf("ParseCriteria(%s): want error, got nil", tt.name)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ParseCriteria(%s) error = %v, want wrapping %v", tt.name, err, tt.wantErr)
			}
			if tt.wantKey != "" && !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("ParseCriteria(%s) error = %q, want it to name %q", tt.name, err.Error(), tt.wantKey)
			}
		})
	}
}

// TestParseCriteria_ModeCrossValidation covers AC6: what each mode EXIGE
// and what it PROHIBITS, in the same six-plus-three-row shape as the
// constitution's own coverage/ratchet coherence table.
func TestParseCriteria_ModeCrossValidation(t *testing.T) {
	assertCriterion := func(extra string) string {
		return `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "assert"
text = "x"
` + extra
	}
	commandCriterion := func(extra string) string {
		return `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "command"
text = "x"
` + extra
	}
	manualCriterion := func(extra string) string {
		return `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "manual"
text = "x"
` + extra
	}

	tests := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		{"assert without [[criterion.assert]] rejected", assertCriterion(""), true},
		{"assert with one [[criterion.assert]] accepted", assertCriterion(`
  [[criterion.assert]]
  verb = "file_exists"
  path = "go.mod"
  new = false
`), false},
		{"command without timeout rejected", commandCriterion(`command = ["make", "build"]`), true},
		{"command with command+timeout accepted", commandCriterion(`
command = ["make", "build"]
timeout = "5m"
`), false},
		{"manual without evidence_required rejected", manualCriterion(""), true},
		{"manual with evidence_required accepted", manualCriterion(`evidence_required = "revision visual"`), false},
		{"manual WITH command prohibited", manualCriterion(`
evidence_required = "x"
command = ["make", "build"]
timeout = "5m"
`), true},
		{"command WITH [[criterion.assert]] prohibited", commandCriterion(`
command = ["make", "build"]
timeout = "5m"
  [[criterion.assert]]
  verb = "file_exists"
  path = "go.mod"
  new = false
`), true},
		{"assert WITH evidence_required prohibited", assertCriterion(`
evidence_required = "x"
  [[criterion.assert]]
  verb = "file_exists"
  path = "go.mod"
  new = false
`), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCriteria([]byte(tt.doc))
			if tt.wantErr && err == nil {
				t.Fatalf("ParseCriteria(%s): want error, got nil\ndoc:\n%s", tt.name, tt.doc)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ParseCriteria(%s): want no error, got %v\ndoc:\n%s", tt.name, err, tt.doc)
			}
			if tt.wantErr && !errors.Is(err, ErrInvalidCriteria) {
				t.Errorf("error = %v, want wrapping ErrInvalidCriteria", err)
			}
		})
	}
}

// TestParseCriteria_CommandArgvSharesMessageWithGate covers AC7: a
// criterion's command produces the byte-identical argv message a gate's own
// command does, verified by comparing the two texts rather than eyeballing
// them — both pass through argvShellStringProblem.
func TestParseCriteria_CommandArgvSharesMessageWithGate(t *testing.T) {
	criteriaDoc := `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "command"
text = "x"
command = ["make test-race"]
timeout = "25m"
`
	_, criteriaErr := ParseCriteria([]byte(criteriaDoc))
	if criteriaErr == nil {
		t.Fatal("ParseCriteria(shell-string command): want error, got nil")
	}

	gateDoc := strings.Replace(validDoc, `command = ["make", "build"]`, `command = ["make test-race"]`, 1)
	_, gateErr := Parse([]byte(gateDoc))
	if gateErr == nil {
		t.Fatal("Parse(gate shell-string command): want error, got nil")
	}

	const sharedSentence = "command is an argv vector, declare each argument as its own list element"
	if !strings.Contains(criteriaErr.Error(), sharedSentence) {
		t.Fatalf("criteria command error %q does not contain shared sentence %q", criteriaErr.Error(), sharedSentence)
	}
	if !strings.Contains(gateErr.Error(), sharedSentence) {
		t.Fatalf("gate command error %q does not contain shared sentence %q", gateErr.Error(), sharedSentence)
	}

	argvDoc := strings.Replace(criteriaDoc, `command = ["make test-race"]`, `command = ["make", "test-race"]`, 1)
	if _, err := ParseCriteria([]byte(argvDoc)); err != nil {
		t.Errorf("ParseCriteria(argv command): want no error, got %v", err)
	}
}

// TestValidateAnchors covers AC8: the four anchor-resolution rows plus
// their positive halves, and the glob-syntax row that fires regardless of
// new.
func TestValidateAnchors(t *testing.T) {
	repoFiles := []string{"internal/quality/git.go", "internal/quality/constitution.go", "docs/quality.md"}

	docWith := func(assertTOML string) *CriteriaDoc {
		doc, err := ParseCriteria([]byte(`
schema_version = 1
[[criterion]]
id = "AC1"
mode = "assert"
text = "x"
` + assertTOML))
		if err != nil {
			t.Fatalf("ParseCriteria fixture: %v", err)
		}
		return doc
	}

	tests := []struct {
		name    string
		assert  string
		wantErr bool
	}{
		{
			name: "file_exists new=false nonexistent path rejected",
			assert: `
  [[criterion.assert]]
  verb = "file_exists"
  path = "docs/api/mcp.md"
  new = false
`,
			wantErr: true,
		},
		{
			name: "file_exists new=false existing path accepted",
			assert: `
  [[criterion.assert]]
  verb = "file_exists"
  path = "docs/quality.md"
  new = false
`,
			wantErr: false,
		},
		{
			name: "pattern_count new=false in matching nothing rejected",
			assert: `
  [[criterion.assert]]
  verb = "pattern_count"
  contains = "TODO"
  in = ["nonexistent/**"]
  word = false
  comparator = "=="
  count = 0
  new = false
`,
			wantErr: true,
		},
		{
			name: "pattern_count new=false in matching at least one file accepted",
			assert: `
  [[criterion.assert]]
  verb = "pattern_count"
  contains = "TODO"
  in = ["internal/quality/*.go"]
  word = false
  comparator = "=="
  count = 0
  new = false
`,
			wantErr: false,
		},
		{
			name: "file_exists new=true nonexistent path accepted (promise to create)",
			assert: `
  [[criterion.assert]]
  verb = "file_exists"
  path = "internal/quality/does-not-exist-yet.go"
  new = true
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := docWith(tt.assert)
			err := ValidateAnchors(doc, repoFiles)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateAnchors(%s): want error, got nil", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateAnchors(%s): want no error, got %v", tt.name, err)
			}
			if tt.wantErr && !errors.Is(err, ErrInvalidCriteria) {
				t.Errorf("error = %v, want wrapping ErrInvalidCriteria", err)
			}
		})
	}
}

// TestParseCriteria_InvalidGlobSyntax covers AC8's last row: a syntactically
// invalid doublestar pattern is rejected at PARSE time — independent of
// new, since malformed syntax is never a valid promise either.
func TestParseCriteria_InvalidGlobSyntax(t *testing.T) {
	for _, newVal := range []string{"true", "false"} {
		t.Run("new="+newVal, func(t *testing.T) {
			doc := `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "assert"
text = "x"
  [[criterion.assert]]
  verb = "pattern_count"
  contains = "TODO"
  in = ["["]
  word = false
  comparator = "=="
  count = 0
  new = ` + newVal + `
`
			_, err := ParseCriteria([]byte(doc))
			if err == nil {
				t.Fatal("ParseCriteria(invalid glob syntax): want error, got nil")
			}
			if !errors.Is(err, ErrInvalidCriteria) {
				t.Errorf("error = %v, want wrapping ErrInvalidCriteria", err)
			}
		})
	}
}

// TestSymbolReferenced_IgnoreMayBeExplicitlyEmpty confirms `ignore = []` is
// accepted (present, nothing to exclude) — only its ABSENCE is rejected.
func TestSymbolReferenced_IgnoreMayBeExplicitlyEmpty(t *testing.T) {
	doc := `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "assert"
text = "x"
  [[criterion.assert]]
  verb = "symbol_referenced"
  symbol = "Foo"
  defined_in = ["internal/quality/*.go"]
  ignore = []
  new = false
`
	parsed, err := ParseCriteria([]byte(doc))
	if err != nil {
		t.Fatalf("ParseCriteria(ignore=[]): %v", err)
	}
	if parsed.Criteria[0].Assert[0].Ignore == nil {
		t.Error("Ignore = nil, want a non-nil empty slice")
	}

	missing := strings.Replace(doc, "ignore = []\n", "", 1)
	if _, err := ParseCriteria([]byte(missing)); err == nil {
		t.Fatal("ParseCriteria(ignore absent): want error, got nil")
	}
}
