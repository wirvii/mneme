package quality

import (
	"errors"
	"fmt"
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
			// SPEC-116 D5/AC2 retarget: schema_version=2 is now VALID (it
			// requires [coverage]/[ratchet], covered separately by
			// TestParse_SchemaVersion2CoverageRatchet below) — this row now
			// targets schema_version=3, a value Parse still rejects, and
			// remains the guardian for "outside the accepted set" (R2: the
			// accepted set only ever WIDENS, never narrows).
			name:    "schema_version outside {1,2}",
			doc:     strings.Replace(validDoc, "schema_version = 1", "schema_version = 3", 1),
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
//
// SPEC-116 D5/AC2 retarget: the fixture used schema_version=2 specifically
// BECAUSE Parse rejected it before this spec; schema_version=2 is now
// valid, so the fixture moves to 3 — a value Parse still rejects — to keep
// testing the same "PeekSchemaVersion tolerates what Parse would reject"
// property.
func TestPeekSchemaVersion_ToleratesRejectedDocument(t *testing.T) {
	future := `
schema_version = 3
a_future_key_this_mneme_does_not_know = true
`
	if _, err := Parse([]byte(future)); err == nil {
		t.Fatal("expected Parse to reject the future document")
	}

	v, err := PeekSchemaVersion([]byte(future))
	if err != nil {
		t.Fatalf("PeekSchemaVersion: %v", err)
	}
	if v != 3 {
		t.Errorf("PeekSchemaVersion = %d, want 3", v)
	}
}

// TestTemplate_ParsesWithoutError anchors AC23 from this very first commit:
// the exact bytes mneme init will one day write must already be valid
// according to Parse, and must declare enabled=false.
//
// SPEC-116 extends the anchor: the template now declares schema_version 2
// with both [coverage]/[ratchet] present and OFF (AC32) — a template that
// failed to declare them complete would itself fail to Parse, since
// schema 2 requires both sections in full.
func TestTemplate_ParsesWithoutError(t *testing.T) {
	c, err := Parse([]byte(Template()))
	if err != nil {
		t.Fatalf("Parse(Template()): %v", err)
	}
	if c.Enabled {
		t.Error("Template() constitution has enabled=true, want false (R4)")
	}
	if c.SchemaVersion != 2 {
		t.Errorf("Template() SchemaVersion = %d, want 2", c.SchemaVersion)
	}
	if !c.CoverageDeclared || !c.RatchetDeclared {
		t.Errorf("Template() CoverageDeclared=%v RatchetDeclared=%v, want both true", c.CoverageDeclared, c.RatchetDeclared)
	}
	if c.Coverage.Enabled || c.Ratchet.Enabled {
		t.Errorf("Template() Coverage.Enabled=%v Ratchet.Enabled=%v, want both false", c.Coverage.Enabled, c.Ratchet.Enabled)
	}
}

// validDocV2 is a minimal, fully valid schema_version=2 document — the
// fixture SPEC-116's AC2-AC6 tables build from.
const validDocV2 = `
schema_version = 2
enabled = false

[execution]
output_tail_bytes = 4096

[[gate]]
name = "build"
command = ["make", "build"]
timeout = "5m"
required = true

[coverage]
enabled = false
format = "go-cover"
command = ["make", "coverage"]
profile_path = "tmp/coverage.out"
timeout = "20m"
min_diff_line_pct = 80.0
min_changed_lines = 5
exclude = []

[ratchet]
enabled = false
max_global_line_pct_drop = 0.0
max_baseline_staleness_pct = 1.0
`

// TestParse_SchemaVersion2CoverageRatchet covers AC2's positive row (2, with
// complete sections, parses) and AC3: CoverageDeclared/RatchetDeclared are
// true only under schema 2.
func TestParse_SchemaVersion2CoverageRatchet(t *testing.T) {
	c, err := Parse([]byte(validDocV2))
	if err != nil {
		t.Fatalf("Parse(validDocV2): %v", err)
	}
	if c.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", c.SchemaVersion)
	}
	if !c.CoverageDeclared {
		t.Error("CoverageDeclared = false, want true under schema_version 2")
	}
	if !c.RatchetDeclared {
		t.Error("RatchetDeclared = false, want true under schema_version 2")
	}
	if c.Coverage.Format != "go-cover" {
		t.Errorf("Coverage.Format = %q, want go-cover", c.Coverage.Format)
	}
	if c.Ratchet.MaxBaselineStalenessPct != 1.0 {
		t.Errorf("Ratchet.MaxBaselineStalenessPct = %v, want 1.0", c.Ratchet.MaxBaselineStalenessPct)
	}
}

// TestParse_DeclaredVsUndeclared covers AC3's three rows.
func TestParse_DeclaredVsUndeclared(t *testing.T) {
	// Row 1: schema_version=1, no sections -> OK, both Declared false.
	c1, err := Parse([]byte(validDoc))
	if err != nil {
		t.Fatalf("Parse(validDoc, schema 1): %v", err)
	}
	if c1.CoverageDeclared || c1.RatchetDeclared {
		t.Errorf("schema 1 with no sections: CoverageDeclared=%v RatchetDeclared=%v, want both false", c1.CoverageDeclared, c1.RatchetDeclared)
	}

	// Row 2: schema_version=1 WITH [coverage] -> error naming schema_version.
	withCoverageUnderSchema1 := validDoc + `
[coverage]
enabled = false
format = "go-cover"
command = ["make", "coverage"]
profile_path = "tmp/coverage.out"
timeout = "20m"
min_diff_line_pct = 80.0
min_changed_lines = 5
exclude = []
`
	_, err = Parse([]byte(withCoverageUnderSchema1))
	if err == nil {
		t.Fatal("Parse([coverage] under schema_version 1): want error, got nil")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error = %q, want it to name schema_version", err.Error())
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("error = %v, want wrapping ErrInvalid", err)
	}

	// Row 3: schema_version=2 -> CoverageDeclared == true (already the
	// primary assertion of TestParse_SchemaVersion2CoverageRatchet; repeated
	// here as the third row of this specific AC).
	c2, err := Parse([]byte(validDocV2))
	if err != nil {
		t.Fatalf("Parse(validDocV2): %v", err)
	}
	if !c2.CoverageDeclared {
		t.Error("schema 2: CoverageDeclared = false, want true")
	}
}

// TestParse_CoverageRatchet_MissingRequiredKey covers AC4: one row per
// missing key, each naming itself, and NONE filled in with a default.
func TestParse_CoverageRatchet_MissingRequiredKey(t *testing.T) {
	tests := []struct {
		removeLine string
		wantKey    string
	}{
		{"enabled = false\nformat", "coverage.enabled"},
		{"format = \"go-cover\"\ncommand", "coverage.format"},
		{"command = [\"make\", \"coverage\"]\nprofile_path", "coverage.command"},
		{"profile_path = \"tmp/coverage.out\"\ntimeout", "coverage.profile_path"},
		{"timeout = \"20m\"\nmin_diff_line_pct", "coverage.timeout"},
		{"min_diff_line_pct = 80.0\nmin_changed_lines", "coverage.min_diff_line_pct"},
		{"min_changed_lines = 5\nexclude", "coverage.min_changed_lines"},
		{"exclude = []\n\n[ratchet]", "coverage.exclude"},
	}

	for _, tt := range tests {
		t.Run(tt.wantKey, func(t *testing.T) {
			// Delete exactly the named key's line by replacing "key = val\n"
			// with "" — done via a targeted single-line removal per key,
			// constructed from validDocV2.
			doc := removeTOMLKeyLine(validDocV2, tt.wantKey)
			_, err := Parse([]byte(doc))
			if err == nil {
				t.Fatalf("Parse missing %s: want error, got nil", tt.wantKey)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("error = %v, want wrapping ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error = %q, want it to name key %q", err.Error(), tt.wantKey)
			}
		})
	}

	// The three [ratchet] keys, same pattern.
	ratchetTests := []struct {
		wantKey string
	}{
		{"ratchet.enabled"},
		{"ratchet.max_global_line_pct_drop"},
		{"ratchet.max_baseline_staleness_pct"},
	}
	for _, tt := range ratchetTests {
		t.Run(tt.wantKey, func(t *testing.T) {
			doc := removeTOMLKeyLine(validDocV2, tt.wantKey)
			_, err := Parse([]byte(doc))
			if err == nil {
				t.Fatalf("Parse missing %s: want error, got nil", tt.wantKey)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("error = %v, want wrapping ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error = %q, want it to name key %q", err.Error(), tt.wantKey)
			}
		})
	}
}

// removeTOMLKeyLine removes the line declaring dottedKey's final segment
// (e.g. "enabled" for "coverage.enabled") — but ONLY while inside the
// section dottedKey's prefix names ("coverage" or "ratchet"). Section-aware
// on purpose: validDocV2 has an "enabled" key in three different sections
// ([execution]'s sibling top-level, [coverage], [ratchet]) and a "command"/
// "timeout" key in both [[gate]] and [coverage] — a section-BLIND removal
// would delete the wrong section's line and silently test a different row
// than the one named.
func removeTOMLKeyLine(doc, dottedKey string) string {
	parts := strings.SplitN(dottedKey, ".", 2)
	wantSection, key := "", dottedKey
	if len(parts) == 2 {
		wantSection, key = parts[0], parts[1]
	}

	lines := strings.Split(doc, "\n")
	out := make([]string, 0, len(lines))
	currentSection := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			currentSection = strings.Trim(trimmed, "[]")
		}
		if wantSection != "" && currentSection == wantSection &&
			(strings.HasPrefix(trimmed, key+" ") || strings.HasPrefix(trimmed, key+"=")) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// TestParse_CoverageRatchet_FieldValidation covers AC5: each rejected value
// alongside its accepted sibling, same fixture.
func TestParse_CoverageRatchet_FieldValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr bool
	}{
		{"format: unknown value rejected", replaceOnce(`format = "go-cover"`, `format = "cobertura"`), true},
		{"format: lcov accepted", replaceOnce(`format = "go-cover"`, `format = "lcov"`), false},
		{"format: go-cover accepted", replaceOnce(`format = "go-cover"`, `format = "go-cover"`), false},
		{"profile_path: absolute rejected", replaceOnce(`profile_path = "tmp/coverage.out"`, `profile_path = "/tmp/coverage.out"`), true},
		{"profile_path: relative accepted", replaceOnce(`profile_path = "tmp/coverage.out"`, `profile_path = "tmp/coverage.out"`), false},
		{"profile_path: contains .. rejected", replaceOnce(`profile_path = "tmp/coverage.out"`, `profile_path = "../coverage.out"`), true},
		{"profile_path: clean relative accepted", replaceOnce(`profile_path = "tmp/coverage.out"`, `profile_path = "tmp/nested/coverage.out"`), false},
		{"profile_path: empty rejected", replaceOnce(`profile_path = "tmp/coverage.out"`, `profile_path = ""`), true},
		{"command: shell string rejected", replaceOnce(`command = ["make", "coverage"]`, `command = ["make coverage"]`), true},
		{"command: argv accepted", replaceOnce(`command = ["make", "coverage"]`, `command = ["make", "coverage"]`), false},
		{"min_diff_line_pct: 0 rejected", replaceOnce(`min_diff_line_pct = 80.0`, `min_diff_line_pct = 0`), true},
		{"min_diff_line_pct: 100.1 rejected", replaceOnce(`min_diff_line_pct = 80.0`, `min_diff_line_pct = 100.1`), true},
		{"min_diff_line_pct: 80.0 accepted", replaceOnce(`min_diff_line_pct = 80.0`, `min_diff_line_pct = 80.0`), false},
		{"min_changed_lines: 0 rejected", replaceOnce(`min_changed_lines = 5`, `min_changed_lines = 0`), true},
		{"min_changed_lines: 1 accepted", replaceOnce(`min_changed_lines = 5`, `min_changed_lines = 1`), false},
		{"max_global_line_pct_drop: -0.1 rejected", replaceOnce(`max_global_line_pct_drop = 0.0`, `max_global_line_pct_drop = -0.1`), true},
		{"max_global_line_pct_drop: 0.0 accepted", replaceOnce(`max_global_line_pct_drop = 0.0`, `max_global_line_pct_drop = 0.0`), false},
		{"max_baseline_staleness_pct: 0 rejected", replaceOnce(`max_baseline_staleness_pct = 1.0`, `max_baseline_staleness_pct = 0`), true},
		{"max_baseline_staleness_pct: 1.0 accepted", replaceOnce(`max_baseline_staleness_pct = 1.0`, `max_baseline_staleness_pct = 1.0`), false},
		{
			"cross-bound: staleness < drop rejected",
			func(doc string) string {
				doc = strings.Replace(doc, `max_global_line_pct_drop = 0.0`, `max_global_line_pct_drop = 2.0`, 1)
				doc = strings.Replace(doc, `max_baseline_staleness_pct = 1.0`, `max_baseline_staleness_pct = 0.5`, 1)
				return doc
			},
			true,
		},
		{"cross-bound: staleness >= drop accepted (defaults)", replaceOnce(`max_global_line_pct_drop = 0.0`, `max_global_line_pct_drop = 0.0`), false},
		{
			"coherence: ratchet enabled with coverage disabled rejected",
			func(doc string) string {
				return strings.Replace(doc, "[ratchet]\nenabled = false", "[ratchet]\nenabled = true", 1)
			},
			true,
		},
		{
			"coherence: both enabled accepted",
			func(doc string) string {
				doc = strings.Replace(doc, "[coverage]\nenabled = false", "[coverage]\nenabled = true", 1)
				doc = strings.Replace(doc, "[ratchet]\nenabled = false", "[ratchet]\nenabled = true", 1)
				return doc
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := tt.mutate(validDocV2)
			_, err := Parse([]byte(doc))
			if tt.wantErr && err == nil {
				t.Fatalf("Parse(%s): want error, got nil\ndoc:\n%s", tt.name, doc)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Parse(%s): want no error, got %v\ndoc:\n%s", tt.name, err, doc)
			}
			if tt.wantErr && !errors.Is(err, ErrInvalid) {
				t.Errorf("error = %v, want wrapping ErrInvalid", err)
			}
		})
	}
}

// replaceOnce returns a mutate func replacing old with new exactly once —
// a small helper keeping TestParse_CoverageRatchet_FieldValidation's table
// declarative.
func replaceOnce(old, new string) func(string) string {
	return func(doc string) string {
		return strings.Replace(doc, old, new, 1)
	}
}

// TestParse_CoverageCommand_SharesArgvMessageWithGate covers AC5's argv
// message-identity requirement, verified by COMPARING both error texts
// rather than eyeballing them: a gate's shell-string rejection and
// coverage.command's must share the exact same explanatory sentence,
// because both call the same argvShellStringProblem helper.
func TestParse_CoverageCommand_SharesArgvMessageWithGate(t *testing.T) {
	gateDoc := strings.Replace(validDocV2, `command = ["make", "build"]`, `command = ["make build"]`, 1)
	_, gateErr := Parse([]byte(gateDoc))
	if gateErr == nil {
		t.Fatal("Parse(gate shell-string command): want error, got nil")
	}

	coverageDoc := strings.Replace(validDocV2, `command = ["make", "coverage"]`, `command = ["make coverage"]`, 1)
	_, coverageErr := Parse([]byte(coverageDoc))
	if coverageErr == nil {
		t.Fatal("Parse(coverage.command shell-string): want error, got nil")
	}

	const sharedSentence = "command is an argv vector, declare each argument as its own list element"
	if !strings.Contains(gateErr.Error(), sharedSentence) {
		t.Fatalf("gate error %q does not contain shared sentence %q", gateErr.Error(), sharedSentence)
	}
	if !strings.Contains(coverageErr.Error(), sharedSentence) {
		t.Fatalf("coverage.command error %q does not contain shared sentence %q", coverageErr.Error(), sharedSentence)
	}
}

// TestParse_FormatSet_MatchesFormats covers AC6: the set of `format` values
// Parse accepts is EXACTLY quality.Formats() — never a second, parallel
// literal list that could drift out of sync. Demonstrated by asserting
// every registered format parses, and one clearly outside the registry
// does not.
func TestParse_FormatSet_MatchesFormats(t *testing.T) {
	for _, format := range Formats() {
		t.Run(format, func(t *testing.T) {
			doc := strings.Replace(validDocV2, `format = "go-cover"`, fmt.Sprintf("format = %q", format), 1)
			if _, err := Parse([]byte(doc)); err != nil {
				t.Errorf("Parse(format=%s), a registered format: %v", format, err)
			}
		})
	}

	doc := strings.Replace(validDocV2, `format = "go-cover"`, `format = "not-a-registered-format"`, 1)
	if _, err := Parse([]byte(doc)); err == nil {
		t.Fatal("Parse(unregistered format): want error, got nil")
	}
}
