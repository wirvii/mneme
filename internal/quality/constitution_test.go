package quality

import (
	"errors"
	"strings"
	"testing"
)

// validDoc is a minimal, fully valid constitution used as the base for the
// missing-key table below: each row deletes exactly one line from it.
const validDoc = `
schema_version = 1
enabled = false

[execution]
output_tail_bytes = 4096

[[gate]]
name = "build"
command = ["make", "build"]
timeout = "5m"
required = true
`

// TestParse_MissingRequiredKey covers AC2: Parse rejects a document missing
// any of schema_version, enabled, execution.output_tail_bytes, or (per
// gate) name/command/timeout/required — naming the missing key, and never
// filling in a default (D13 of the grill).
func TestParse_MissingRequiredKey(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantKey string
	}{
		{
			name: "missing schema_version",
			doc: `
enabled = false
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
command = ["make", "build"]
timeout = "5m"
required = true
`,
			wantKey: "schema_version",
		},
		{
			name: "missing enabled",
			doc: `
schema_version = 1
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
command = ["make", "build"]
timeout = "5m"
required = true
`,
			wantKey: "enabled",
		},
		{
			name: "missing execution.output_tail_bytes",
			doc: `
schema_version = 1
enabled = false
[[gate]]
name = "build"
command = ["make", "build"]
timeout = "5m"
required = true
`,
			wantKey: "execution.output_tail_bytes",
		},
		{
			name: "missing gate name",
			doc: `
schema_version = 1
enabled = false
[execution]
output_tail_bytes = 4096
[[gate]]
command = ["make", "build"]
timeout = "5m"
required = true
`,
			wantKey: "name",
		},
		{
			name: "missing gate command",
			doc: `
schema_version = 1
enabled = false
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
timeout = "5m"
required = true
`,
			wantKey: "command",
		},
		{
			name: "missing gate timeout",
			doc: `
schema_version = 1
enabled = false
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
command = ["make", "build"]
required = true
`,
			wantKey: "timeout",
		},
		{
			name: "missing gate required",
			doc: `
schema_version = 1
enabled = false
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
command = ["make", "build"]
timeout = "5m"
`,
			wantKey: "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.doc))
			if err == nil {
				t.Fatalf("Parse(%s) succeeded, want error naming %q", tt.name, tt.wantKey)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Parse(%s) error = %v, want wrapping ErrInvalid", tt.name, err)
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("Parse(%s) error = %q, want it to name key %q", tt.name, err.Error(), tt.wantKey)
			}
		})
	}
}

// TestParse_Rejections covers AC3: unknown keys, schema_version != 1,
// duplicate gate names, an invalid gate name, an empty command, a
// single-element shell-string command, a bad timeout, and an
// output_tail_bytes outside [1, 65536].
func TestParse_Rejections(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantErr error
	}{
		{
			name: "unknown top-level key",
			doc: validDoc + "\nunknown_key = true\n",
		},
		{
			name: "unknown gate key",
			doc: `
schema_version = 1
enabled = false
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
command = ["make", "build"]
timeout = "5m"
required = true
extra = "nope"
`,
		},
		{
			name:    "schema_version not 1",
			doc:     strings.Replace(validDoc, "schema_version = 1", "schema_version = 2", 1),
			wantErr: ErrUnsupportedSchema,
		},
		{
			name: "duplicate gate name",
			doc: validDoc + `
[[gate]]
name = "build"
command = ["make", "test"]
timeout = "5m"
required = true
`,
		},
		{
			name: "invalid gate name format",
			doc:  strings.Replace(validDoc, `name = "build"`, `name = "Build_1"`, 1),
		},
		{
			name: "empty command",
			doc:  strings.Replace(validDoc, `command = ["make", "build"]`, `command = []`, 1),
		},
		{
			name:    "single element shell string command",
			doc:     strings.Replace(validDoc, `command = ["make", "build"]`, `command = ["make test"]`, 1),
			wantErr: ErrInvalid,
		},
		{
			name: "unparseable timeout",
			doc:  strings.Replace(validDoc, `timeout = "5m"`, `timeout = "banana"`, 1),
		},
		{
			name: "zero timeout",
			doc:  strings.Replace(validDoc, `timeout = "5m"`, `timeout = "0s"`, 1),
		},
		{
			name: "output_tail_bytes below range",
			doc:  strings.Replace(validDoc, "output_tail_bytes = 4096", "output_tail_bytes = 0", 1),
		},
		{
			name: "output_tail_bytes above range",
			doc:  strings.Replace(validDoc, "output_tail_bytes = 4096", "output_tail_bytes = 70000", 1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.doc))
			if err == nil {
				t.Fatalf("Parse(%s) succeeded, want an error", tt.name)
			}
			want := tt.wantErr
			if want == nil {
				want = ErrInvalid
			}
			if !errors.Is(err, want) {
				t.Errorf("Parse(%s) error = %v, want wrapping %v", tt.name, err, want)
			}
		})
	}
}

// TestParse_Valid confirms the minimal valid document parses into the
// expected Constitution shape.
func TestParse_Valid(t *testing.T) {
	c, err := Parse([]byte(validDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", c.SchemaVersion)
	}
	if c.Enabled {
		t.Error("Enabled = true, want false")
	}
	if c.Execution.OutputTailBytes != 4096 {
		t.Errorf("OutputTailBytes = %d, want 4096", c.Execution.OutputTailBytes)
	}
	if len(c.Gates) != 1 || c.Gates[0].Name != "build" {
		t.Fatalf("Gates = %+v, want one gate named build", c.Gates)
	}
	if !c.Gates[0].Required {
		t.Error("Gates[0].Required = false, want true")
	}
}

// TestHashBytes_ChangesWithComment covers AC4: HashBytes hashes the raw
// bytes, not the parsed struct — changing only a comment must change the
// hash.
func TestHashBytes_ChangesWithComment(t *testing.T) {
	original := validDoc
	commented := "# a harmless comment\n" + validDoc

	h1 := HashBytes([]byte(original))
	h2 := HashBytes([]byte(commented))
	if h1 == h2 {
		t.Fatal("HashBytes did not change when a comment was added — it must hash raw bytes, not the parsed struct")
	}

	// Parsed content is identical either way — proving the difference is
	// purely byte-level, not semantic.
	c1, err := Parse([]byte(original))
	if err != nil {
		t.Fatalf("Parse(original): %v", err)
	}
	c2, err := Parse([]byte(commented))
	if err != nil {
		t.Fatalf("Parse(commented): %v", err)
	}
	if c1.SchemaVersion != c2.SchemaVersion || c1.Enabled != c2.Enabled {
		t.Fatal("expected identical parsed content for original vs commented doc")
	}
}

// TestPeekSchemaVersion_ToleratesRejectedDocument covers the reason
// PeekSchemaVersion exists at all: reading schema_version out of a document
// Parse itself would reject (e.g. a future, higher schema_version with keys
// this mneme does not recognise).
func TestPeekSchemaVersion_ToleratesRejectedDocument(t *testing.T) {
	future := `
schema_version = 2
a_future_key_this_mneme_does_not_know = true
`
	if _, err := Parse([]byte(future)); err == nil {
		t.Fatal("expected Parse to reject the future document")
	}

	v, err := PeekSchemaVersion([]byte(future))
	if err != nil {
		t.Fatalf("PeekSchemaVersion: %v", err)
	}
	if v != 2 {
		t.Errorf("PeekSchemaVersion = %d, want 2", v)
	}
}

// TestTemplate_ParsesWithoutError anchors AC23 from this very first commit:
// the exact bytes mneme init will one day write must already be valid
// according to Parse, and must declare enabled=false.
func TestTemplate_ParsesWithoutError(t *testing.T) {
	c, err := Parse([]byte(Template()))
	if err != nil {
		t.Fatalf("Parse(Template()): %v", err)
	}
	if c.Enabled {
		t.Error("Template() constitution has enabled=true, want false (R4)")
	}
}
