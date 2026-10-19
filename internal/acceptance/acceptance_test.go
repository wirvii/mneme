package acceptance

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestParse_AcceptsEveryLegacyShape covers AC5: every shape the retired
// evaluator's strict parser used to accept — the three modes (assert with
// each of the four closed verbs, command, manual) plus a minimal id+text
// only document — still parses, with mode-specific keys silently discarded.
func TestParse_AcceptsEveryLegacyShape(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{
			name: "assert mode, file_exists verb",
			doc: `
schema_version = 1
[[criterion]]
id = "AC1"
mode = "assert"
text = "x existe"
  [[criterion.assert]]
  verb = "file_exists"
  path = "go.mod"
  new = false
`,
		},
		{
			name: "assert mode, pattern_count verb",
			doc: `
schema_version = 1
[[criterion]]
id = "AC2"
mode = "assert"
text = "contiene TODO"
  [[criterion.assert]]
  verb = "pattern_count"
  contains = "TODO"
  in = ["**/*.go"]
  word = false
  comparator = "=="
  count = 0
  new = false
`,
		},
		{
			name: "assert mode, symbol_defined verb",
			doc: `
schema_version = 1
[[criterion]]
id = "AC3"
mode = "assert"
text = "define Foo"
  [[criterion.assert]]
  verb = "symbol_defined"
  symbol = "Foo"
  in = ["**/*.go"]
  new = false
`,
		},
		{
			name: "assert mode, symbol_referenced verb",
			doc: `
schema_version = 1
[[criterion]]
id = "AC4"
mode = "assert"
text = "Foo se usa"
  [[criterion.assert]]
  verb = "symbol_referenced"
  symbol = "Foo"
  defined_in = ["**/*.go"]
  ignore = ["**/*_test.go"]
  new = false
`,
		},
		{
			name: "command mode",
			doc: `
schema_version = 1
[[criterion]]
id = "AC5"
mode = "command"
text = "la suite pasa"
command = ["make", "test"]
timeout = "10m"
`,
		},
		{
			name: "manual mode",
			doc: `
schema_version = 1
[[criterion]]
id = "AC6"
mode = "manual"
text = "el contraste es correcto"
evidence_required = "captura adjunta"
`,
		},
		{
			name: "minimal id+text only, no mode at all",
			doc: `
schema_version = 1
[[criterion]]
id = "AC7"
text = "una afirmacion que el qa-tester puede comprobar"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := Parse([]byte(tt.doc))
			if err != nil {
				t.Fatalf("Parse(%s): %v", tt.name, err)
			}
			if len(doc.Criteria) != 1 {
				t.Fatalf("Parse(%s): Criteria = %d, want 1", tt.name, len(doc.Criteria))
			}
			if doc.Criteria[0].Text == "" {
				t.Errorf("Parse(%s): Text is empty", tt.name)
			}
		})
	}
}

// TestParse_AcceptsDeliveredDocuments covers AC5's second half: two REAL
// criteria.toml documents delivered by SPEC-156 and SPEC-157, copied
// byte-for-byte into testdata/, both parse and yield the expected criterion
// ids.
func TestParse_AcceptsDeliveredDocuments(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantIDs []string
	}{
		{
			name: "SPEC-156",
			path: "testdata/spec-156-criteria.toml",
			wantIDs: []string{
				"AC1", "AC2", "AC3", "AC4", "AC5", "AC6", "AC7", "AC8", "AC9", "AC10", "AC11",
			},
		},
		{
			name: "SPEC-157",
			path: "testdata/spec-157-criteria.toml",
			wantIDs: []string{
				"AC1", "AC2", "AC3", "AC4", "AC5", "AC6", "AC7", "AC8", "AC9", "AC10",
				"AC11", "AC12", "AC13", "AC14", "AC15", "AC16", "AC17", "AC18",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatalf("read %s: %v", tt.path, err)
			}
			doc, err := Parse(raw)
			if err != nil {
				t.Fatalf("Parse(%s): %v", tt.path, err)
			}
			if len(doc.Criteria) != len(tt.wantIDs) {
				t.Fatalf("Parse(%s): Criteria = %d, want %d", tt.path, len(doc.Criteria), len(tt.wantIDs))
			}
			for i, want := range tt.wantIDs {
				if doc.Criteria[i].ID != want {
					t.Errorf("Parse(%s): Criteria[%d].ID = %q, want %q", tt.path, i, doc.Criteria[i].ID, want)
				}
			}
		})
	}
}

// TestParse_Rejects covers AC6: schema_version absent or wrong, zero
// criteria, id absent, id pattern violation, duplicate id, text absent or
// empty, and an unknown key at both the top level and the criterion level.
func TestParse_Rejects(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantErr error
	}{
		{
			name: "missing schema_version",
			doc: `
[[criterion]]
id = "AC1"
text = "x"
`,
			wantErr: ErrInvalid,
		},
		{
			name: "schema_version 2 unsupported",
			doc: `
schema_version = 2
[[criterion]]
id = "AC1"
text = "x"
`,
			wantErr: ErrUnsupportedSchema,
		},
		{
			name: "zero criteria",
			doc: `
schema_version = 1
`,
			wantErr: ErrInvalid,
		},
		{
			name: "missing id",
			doc: `
schema_version = 1
[[criterion]]
text = "x"
`,
			wantErr: ErrInvalid,
		},
		{
			name: "id does not match pattern",
			doc: `
schema_version = 1
[[criterion]]
id = " AC1"
text = "x"
`,
			wantErr: ErrInvalid,
		},
		{
			name: "duplicate id",
			doc: `
schema_version = 1
[[criterion]]
id = "AC1"
text = "x"
[[criterion]]
id = "AC1"
text = "y"
`,
			wantErr: ErrInvalid,
		},
		{
			name: "missing text",
			doc: `
schema_version = 1
[[criterion]]
id = "AC1"
`,
			wantErr: ErrInvalid,
		},
		{
			name: "empty text",
			doc: `
schema_version = 1
[[criterion]]
id = "AC1"
text = ""
`,
			wantErr: ErrInvalid,
		},
		{
			name: "unknown top-level key",
			doc: `
schema_version = 1
unknown_key = true
[[criterion]]
id = "AC1"
text = "x"
`,
			wantErr: ErrInvalid,
		},
		{
			name: "unknown criterion-level key",
			doc: `
schema_version = 1
[[criterion]]
id = "AC1"
text = "x"
verbo = "no existe"
`,
			wantErr: ErrInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.doc))
			if err == nil {
				t.Fatalf("Parse(%s): want error, got nil", tt.name)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Parse(%s) error = %v, want wrapping %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

// TestParse_TextNotTrimmed confirms text is checked for the empty string
// only, without strings.TrimSpace — a whitespace-only text (which the
// retired strict parser also accepted, since it applied the same bare ""
// check) still parses.
func TestParse_TextNotTrimmed(t *testing.T) {
	doc := `
schema_version = 1
[[criterion]]
id = "AC1"
text = "   "
`
	parsed, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse(whitespace text): %v", err)
	}
	if !strings.Contains(parsed.Criteria[0].Text, " ") {
		t.Errorf("Text = %q, want the whitespace preserved", parsed.Criteria[0].Text)
	}
}
