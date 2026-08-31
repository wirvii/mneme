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
			doc:  validDoc + "\nunknown_key = true\n",
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
			// SPEC-119 D11 point 4 retarget (third consecutive retarget of
			// this same row — SPEC-117 moved it 3->4, SPEC-118 moved it
			// 4->5): this row now derives its rejected value from
			// CurrentSchemaVersion+1 INSTEAD OF a literal, so the NEXT
			// schema bump no longer has to touch this test at all — it is
			// the guardian for "outside the accepted set" (R2/D9: the
			// accepted set only ever WIDENS, never narrows), and that
			// property should survive every future bump automatically.
			name: "schema_version outside the accepted range",
			doc: strings.Replace(validDoc, "schema_version = 1",
				fmt.Sprintf("schema_version = %d", CurrentSchemaVersion+1), 1),
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

// TestParse_EnabledRequiresGates covers BL-220: enabled=true with zero
// declared [[gate]] entries used to parse cleanly, letting a certificate
// made only of the always-run checks (tree/clean-worktree + the three
// constitution ones) derive `pass` without verifying anything the project
// actually declared. Same molde as visual.targets' own
// enabled-implies-non-empty check (D3/G4a).
func TestParse_EnabledRequiresGates(t *testing.T) {
	// zeroGatesDoc has no [[gate]] section at all — the exact shape BL-220
	// reproduced.
	const zeroGatesDoc = `
schema_version = 1
enabled = false

[execution]
output_tail_bytes = 4096
`

	tests := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		{
			name:    "enabled=true, zero gates -> rejected",
			doc:     strings.Replace(zeroGatesDoc, "enabled = false", "enabled = true", 1),
			wantErr: true,
		},
		{
			// The status quo for most repos today (mechanism off, no
			// gates yet declared) must keep parsing without error.
			name:    "enabled=false, zero gates -> still parses",
			doc:     zeroGatesDoc,
			wantErr: false,
		},
		{
			// enabled=true WITH a declared gate -> still parses (validDoc
			// already declares one gate named "build"; only enabled
			// changes).
			name:    "enabled=true, one gate -> still parses",
			doc:     strings.Replace(validDoc, "enabled = false", "enabled = true", 1),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.doc))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%s) succeeded, want an error", tt.name)
				}
				if !errors.Is(err, ErrInvalid) {
					t.Errorf("Parse(%s) error = %v, want wrapping ErrInvalid", tt.name, err)
				}
				if !strings.Contains(err.Error(), "gates must not be empty") {
					t.Errorf("Parse(%s) error = %q, want it to name the empty-gates condition", tt.name, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%s): %v", tt.name, err)
			}
		})
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
// SPEC-119 D11 point 4 retarget (third consecutive retarget — SPEC-117
// moved this fixture 3->4, SPEC-118 moved it 4->5): the fixture now derives
// its rejected value from CurrentSchemaVersion+1 INSTEAD OF a literal, so
// it stays immune to every future schema bump — this is the LAST time this
// retarget should need to happen in the EPIC (D11 point 4's own promise).
func TestPeekSchemaVersion_ToleratesRejectedDocument(t *testing.T) {
	future := fmt.Sprintf(`
schema_version = %d
a_future_key_this_mneme_does_not_know = true
`, CurrentSchemaVersion+1)
	if _, err := Parse([]byte(future)); err == nil {
		t.Fatal("expected Parse to reject the future document")
	}

	v, err := PeekSchemaVersion([]byte(future))
	if err != nil {
		t.Fatalf("PeekSchemaVersion: %v", err)
	}
	if v != CurrentSchemaVersion+1 {
		t.Errorf("PeekSchemaVersion = %d, want %d", v, CurrentSchemaVersion+1)
	}
}

// TestTemplate_ParsesWithoutError anchors AC23 from this very first commit:
// the exact bytes mneme init will one day write must already be valid
// according to Parse, and must declare enabled=false.
//
// SPEC-118 extended the anchor: the template declared schema_version 4
// with [coverage]/[ratchet]/[criteria]/[budget] all present and OFF
// (AC34). SPEC-119 extends it again: schema_version 5, with [mutation]
// joining the other four, all present and OFF (AC29) — a template that
// failed to declare any of the five complete would itself fail to Parse,
// since schema 5 requires all five sections in full.
func TestTemplate_ParsesWithoutError(t *testing.T) {
	c, err := Parse([]byte(Template()))
	if err != nil {
		t.Fatalf("Parse(Template()): %v", err)
	}
	if c.Enabled {
		t.Error("Template() constitution has enabled=true, want false (R4)")
	}
	if c.SchemaVersion != 6 {
		t.Errorf("Template() SchemaVersion = %d, want 6", c.SchemaVersion)
	}
	if !c.CoverageDeclared || !c.RatchetDeclared || !c.CriteriaDeclared || !c.BudgetDeclared || !c.MutationDeclared || !c.VisualDeclared {
		t.Errorf("Template() CoverageDeclared=%v RatchetDeclared=%v CriteriaDeclared=%v BudgetDeclared=%v MutationDeclared=%v VisualDeclared=%v, want all true",
			c.CoverageDeclared, c.RatchetDeclared, c.CriteriaDeclared, c.BudgetDeclared, c.MutationDeclared, c.VisualDeclared)
	}
	if c.Coverage.Enabled || c.Ratchet.Enabled || c.Criteria.Enabled || c.Budget.Enabled || c.Mutation.Enabled || c.Visual.Enabled {
		t.Errorf("Template() Coverage.Enabled=%v Ratchet.Enabled=%v Criteria.Enabled=%v Budget.Enabled=%v Mutation.Enabled=%v Visual.Enabled=%v, want all false",
			c.Coverage.Enabled, c.Ratchet.Enabled, c.Criteria.Enabled, c.Budget.Enabled, c.Mutation.Enabled, c.Visual.Enabled)
	}
	if c.Visual.Compare.Enabled {
		t.Error("Template() Visual.Compare.Enabled = true, want false")
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
	// SPEC-120 P5: split on the LAST dot, not the first — a nested table
	// key like "visual.compare.enabled" has section "visual.compare" and
	// key "enabled", not section "visual" and key "compare.enabled". Every
	// PRIOR call site (single-level sections like "mutation.enabled") gets
	// the exact same result either way, so this is a pure generalisation.
	wantSection, key := "", dottedKey
	if idx := strings.LastIndex(dottedKey, "."); idx >= 0 {
		wantSection, key = dottedKey[:idx], dottedKey[idx+1:]
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

// validDocV3 is a minimal, fully valid schema_version=3 document — adds
// [criteria] complete-and-off to validDocV2's own shape (SPEC-117 AC2-AC4).
const validDocV3 = `
schema_version = 3
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

[criteria]
enabled = false
timeout = "5m"
max_manual_pct = 25.0
max_command_pct = 30.0
`

// TestParse_SchemaVersion3Criteria covers AC2's positive row (3, with all
// three sections complete, parses) and AC3: CriteriaDeclared is true only
// under schema 3.
func TestParse_SchemaVersion3Criteria(t *testing.T) {
	c, err := Parse([]byte(validDocV3))
	if err != nil {
		t.Fatalf("Parse(validDocV3): %v", err)
	}
	if c.SchemaVersion != 3 {
		t.Errorf("SchemaVersion = %d, want 3", c.SchemaVersion)
	}
	if !c.CriteriaDeclared {
		t.Error("CriteriaDeclared = false, want true under schema_version 3")
	}
	if c.Criteria.Timeout.String() != "5m0s" {
		t.Errorf("Criteria.Timeout = %v, want 5m0s", c.Criteria.Timeout)
	}
	if c.Criteria.MaxManualPct != 25.0 {
		t.Errorf("Criteria.MaxManualPct = %v, want 25.0", c.Criteria.MaxManualPct)
	}
	if c.Criteria.MaxCommandPct != 30.0 {
		t.Errorf("Criteria.MaxCommandPct = %v, want 30.0", c.Criteria.MaxCommandPct)
	}
}

// TestParse_CriteriaDeclaredVsUndeclared covers AC3's three rows for
// [criteria] specifically: schema 2 (no [criteria]) -> OK, declared false;
// schema 2 WITH [criteria] -> error naming schema_version; schema 3 ->
// declared true (already the primary assertion of
// TestParse_SchemaVersion3Criteria, repeated here as this AC's third row).
func TestParse_CriteriaDeclaredVsUndeclared(t *testing.T) {
	c1, err := Parse([]byte(validDocV2))
	if err != nil {
		t.Fatalf("Parse(validDocV2): %v", err)
	}
	if c1.CriteriaDeclared {
		t.Error("schema 2 with no [criteria]: CriteriaDeclared = true, want false")
	}

	withCriteriaUnderSchema2 := validDocV2 + `
[criteria]
enabled = false
timeout = "5m"
max_manual_pct = 25.0
max_command_pct = 30.0
`
	_, err = Parse([]byte(withCriteriaUnderSchema2))
	if err == nil {
		t.Fatal("Parse([criteria] under schema_version 2): want error, got nil")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error = %q, want it to name schema_version", err.Error())
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("error = %v, want wrapping ErrInvalid", err)
	}

	c3, err := Parse([]byte(validDocV3))
	if err != nil {
		t.Fatalf("Parse(validDocV3): %v", err)
	}
	if !c3.CriteriaDeclared {
		t.Error("schema 3: CriteriaDeclared = false, want true")
	}
}

// TestParse_Criteria_MissingRequiredKey covers AC4: one row per missing
// [criteria] key, each naming itself, and none filled in with a default.
func TestParse_Criteria_MissingRequiredKey(t *testing.T) {
	tests := []struct {
		wantKey string
	}{
		{"criteria.enabled"},
		{"criteria.timeout"},
		{"criteria.max_manual_pct"},
		{"criteria.max_command_pct"},
	}
	for _, tt := range tests {
		t.Run(tt.wantKey, func(t *testing.T) {
			doc := removeTOMLKeyLine(validDocV3, tt.wantKey)
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

// TestParse_Criteria_FieldValidation covers AC4's paired validation rows:
// timeout, max_manual_pct, and max_command_pct.
func TestParse_Criteria_FieldValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr bool
	}{
		{"timeout: 0s rejected", replaceOnce("[criteria]\nenabled = false\ntimeout = \"5m\"", "[criteria]\nenabled = false\ntimeout = \"0s\""), true},
		{"timeout: 5m accepted", replaceOnce(`max_manual_pct = 25.0`, `max_manual_pct = 25.0`), false},
		{"max_manual_pct: -1 rejected", replaceOnce(`max_manual_pct = 25.0`, `max_manual_pct = -1`), true},
		{"max_manual_pct: 101 rejected", replaceOnce(`max_manual_pct = 25.0`, `max_manual_pct = 101`), true},
		{"max_manual_pct: 25.0 accepted", replaceOnce(`max_manual_pct = 25.0`, `max_manual_pct = 25.0`), false},
		{"max_command_pct: -1 rejected", replaceOnce(`max_command_pct = 30.0`, `max_command_pct = -1`), true},
		{"max_command_pct: 101 rejected", replaceOnce(`max_command_pct = 30.0`, `max_command_pct = 101`), true},
		{"max_command_pct: 30.0 accepted", replaceOnce(`max_command_pct = 30.0`, `max_command_pct = 30.0`), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := tt.mutate(validDocV3)
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

// --- SPEC-118 EPIC-calidad S4 P6: schema 4, [budget] ---

// validDocV4 is a minimal, fully valid schema_version=4 document — adds
// [budget] complete-and-off to validDocV3's own shape.
const validDocV4 = `
schema_version = 4
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

[criteria]
enabled = false
timeout = "5m"
max_manual_pct = 25.0
max_command_pct = 30.0

[budget]
enabled = false
timeout = "2m"
test_globs = ["**/*_test.go"]
test_reach_depth = 3
`

// TestParse_SchemaVersion4Budget covers AC2's positive row (4, with all
// four sections complete, parses) and AC3: BudgetDeclared is true only
// under schema 4.
func TestParse_SchemaVersion4Budget(t *testing.T) {
	c, err := Parse([]byte(validDocV4))
	if err != nil {
		t.Fatalf("Parse(validDocV4): %v", err)
	}
	if c.SchemaVersion != 4 {
		t.Errorf("SchemaVersion = %d, want 4", c.SchemaVersion)
	}
	if !c.BudgetDeclared {
		t.Error("BudgetDeclared = false, want true under schema_version 4")
	}
	if c.Budget.Timeout.String() != "2m0s" {
		t.Errorf("Budget.Timeout = %v, want 2m0s", c.Budget.Timeout)
	}
	if len(c.Budget.TestGlobs) != 1 || c.Budget.TestGlobs[0] != "**/*_test.go" {
		t.Errorf("Budget.TestGlobs = %v, want [**/*_test.go]", c.Budget.TestGlobs)
	}
	if c.Budget.TestReachDepth != 3 {
		t.Errorf("Budget.TestReachDepth = %d, want 3", c.Budget.TestReachDepth)
	}
}

// TestParse_BudgetDeclaredVsUndeclared covers AC3's three rows for
// [budget] specifically: schema 3 (no [budget]) -> OK, declared false;
// schema 3 WITH [budget] -> error naming schema_version; schema 4 ->
// declared true.
func TestParse_BudgetDeclaredVsUndeclared(t *testing.T) {
	c3, err := Parse([]byte(validDocV3))
	if err != nil {
		t.Fatalf("Parse(validDocV3): %v", err)
	}
	if c3.BudgetDeclared {
		t.Error("schema 3 with no [budget]: BudgetDeclared = true, want false")
	}

	withBudgetUnderSchema3 := validDocV3 + `
[budget]
enabled = false
timeout = "2m"
test_globs = ["**/*_test.go"]
test_reach_depth = 3
`
	_, err = Parse([]byte(withBudgetUnderSchema3))
	if err == nil {
		t.Fatal("Parse([budget] under schema_version 3): want error, got nil")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error = %q, want it to name schema_version", err.Error())
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("error = %v, want wrapping ErrInvalid", err)
	}

	c4, err := Parse([]byte(validDocV4))
	if err != nil {
		t.Fatalf("Parse(validDocV4): %v", err)
	}
	if !c4.BudgetDeclared {
		t.Error("schema 4: BudgetDeclared = false, want true")
	}
}

// TestParse_Budget_MissingRequiredKey covers AC4: one row per missing
// [budget] key, each naming itself, and none filled in with a default.
func TestParse_Budget_MissingRequiredKey(t *testing.T) {
	tests := []struct {
		wantKey string
	}{
		{"budget.enabled"},
		{"budget.timeout"},
		{"budget.test_globs"},
		{"budget.test_reach_depth"},
	}
	for _, tt := range tests {
		t.Run(tt.wantKey, func(t *testing.T) {
			doc := removeTOMLKeyLine(validDocV4, tt.wantKey)
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

// TestParse_Budget_FieldValidation covers AC4's paired validation rows:
// timeout, test_globs (empty and invalid glob), and test_reach_depth.
func TestParse_Budget_FieldValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr bool
	}{
		{"timeout: 0s rejected", replaceOnce(`timeout = "2m"`, `timeout = "0s"`), true},
		{"timeout: 2m accepted", replaceOnce(`timeout = "2m"`, `timeout = "2m"`), false},
		{"test_globs: empty rejected", replaceOnce(`test_globs = ["**/*_test.go"]`, `test_globs = []`), true},
		{"test_globs: one pattern accepted", replaceOnce(`test_globs = ["**/*_test.go"]`, `test_globs = ["**/*_test.go"]`), false},
		{"test_globs: invalid glob rejected", replaceOnce(`test_globs = ["**/*_test.go"]`, `test_globs = ["["]`), true},
		{"test_reach_depth: 0 rejected", replaceOnce("test_reach_depth = 3", "test_reach_depth = 0"), true},
		{"test_reach_depth: 11 rejected", replaceOnce("test_reach_depth = 3", "test_reach_depth = 11"), true},
		{"test_reach_depth: 3 accepted", replaceOnce("test_reach_depth = 3", "test_reach_depth = 3"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := tt.mutate(validDocV4)
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

// --- SPEC-119 EPIC-calidad S5 P5: schema 5, [mutation] ---

// validDocV5 is a minimal, fully valid schema_version=5 document — adds
// [mutation] complete-and-off to validDocV4's own shape.
const validDocV5 = `
schema_version = 5
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

[criteria]
enabled = false
timeout = "5m"
max_manual_pct = 25.0
max_command_pct = 30.0

[budget]
enabled = false
timeout = "2m"
test_globs = ["**/*_test.go"]
test_reach_depth = 3

[mutation]
enabled = false
format = "gremlins"
command = ["make", "mutation", "BASE={{BASE_SHA}}"]
report_path = "tmp/mutants.json"
timeout = "30m"
max_equivalent = 2
max_not_viable_pct = 25.0
`

// TestParse_SchemaVersion5Mutation covers AC2's positive row (5, with all
// five sections complete, parses) and AC3: MutationDeclared is true only
// under schema 5.
func TestParse_SchemaVersion5Mutation(t *testing.T) {
	c, err := Parse([]byte(validDocV5))
	if err != nil {
		t.Fatalf("Parse(validDocV5): %v", err)
	}
	if c.SchemaVersion != 5 {
		t.Errorf("SchemaVersion = %d, want 5", c.SchemaVersion)
	}
	if !c.MutationDeclared {
		t.Error("MutationDeclared = false, want true under schema_version 5")
	}
	if c.Mutation.Format != "gremlins" {
		t.Errorf("Mutation.Format = %q, want gremlins", c.Mutation.Format)
	}
	if c.Mutation.ReportPath != "tmp/mutants.json" {
		t.Errorf("Mutation.ReportPath = %q, want tmp/mutants.json", c.Mutation.ReportPath)
	}
	if c.Mutation.Timeout.String() != "30m0s" {
		t.Errorf("Mutation.Timeout = %v, want 30m0s", c.Mutation.Timeout)
	}
	if c.Mutation.MaxEquivalent != 2 {
		t.Errorf("Mutation.MaxEquivalent = %d, want 2", c.Mutation.MaxEquivalent)
	}
	if c.Mutation.MaxNotViablePct != 25.0 {
		t.Errorf("Mutation.MaxNotViablePct = %v, want 25.0", c.Mutation.MaxNotViablePct)
	}
	if len(c.Mutation.Command) != 3 || c.Mutation.Command[2] != "BASE={{BASE_SHA}}" {
		t.Errorf("Mutation.Command = %v, want the raw {{BASE_SHA}} token preserved (ExpandCommand substitutes it later, not Parse)", c.Mutation.Command)
	}
}

// TestParse_MutationDeclaredVsUndeclared covers AC3's three rows for
// [mutation] specifically: schema 4 (no [mutation]) -> OK, declared false;
// schema 4 WITH [mutation] -> error naming schema_version; schema 5 ->
// declared true. This is G2's positive hermana: the range must NOT
// narrow, so every prior schema keeps parsing.
func TestParse_MutationDeclaredVsUndeclared(t *testing.T) {
	c4, err := Parse([]byte(validDocV4))
	if err != nil {
		t.Fatalf("Parse(validDocV4): %v", err)
	}
	if c4.MutationDeclared {
		t.Error("schema 4 with no [mutation]: MutationDeclared = true, want false")
	}

	withMutationUnderSchema4 := validDocV4 + `
[mutation]
enabled = false
format = "gremlins"
command = ["make", "mutation"]
report_path = "tmp/mutants.json"
timeout = "30m"
max_equivalent = 2
max_not_viable_pct = 25.0
`
	_, err = Parse([]byte(withMutationUnderSchema4))
	if err == nil {
		t.Fatal("Parse([mutation] under schema_version 4): want error, got nil")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error = %q, want it to name schema_version", err.Error())
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("error = %v, want wrapping ErrInvalid", err)
	}

	c5, err := Parse([]byte(validDocV5))
	if err != nil {
		t.Fatalf("Parse(validDocV5): %v", err)
	}
	if !c5.MutationDeclared {
		t.Error("schema 5: MutationDeclared = false, want true")
	}
}

// TestParse_Mutation_MissingRequiredKey covers AC4: one row per missing
// [mutation] key, each naming itself, and none filled in with a default.
func TestParse_Mutation_MissingRequiredKey(t *testing.T) {
	tests := []struct {
		wantKey string
	}{
		{"mutation.enabled"},
		{"mutation.format"},
		{"mutation.command"},
		{"mutation.report_path"},
		{"mutation.timeout"},
		{"mutation.max_equivalent"},
		{"mutation.max_not_viable_pct"},
	}
	for _, tt := range tests {
		t.Run(tt.wantKey, func(t *testing.T) {
			doc := removeTOMLKeyLine(validDocV5, tt.wantKey)
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

// TestParse_Mutation_FieldValidation covers AC4's paired validation rows
// (G3 for format, hermanas for the rest).
func TestParse_Mutation_FieldValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr bool
	}{
		{"format: lcov rejected (not a mutation format)", replaceOnce(`format = "gremlins"`, `format = "lcov"`), true},
		{"format: mutants-v1 accepted", replaceOnce(`format = "gremlins"`, `format = "mutants-v1"`), false},
		{"command: single-element shell string rejected", replaceOnce(`command = ["make", "mutation", "BASE={{BASE_SHA}}"]`, `command = ["make mutation"]`), true},
		{"report_path: empty rejected", replaceOnce(`report_path = "tmp/mutants.json"`, `report_path = ""`), true},
		{"report_path: traversal rejected", replaceOnce(`report_path = "tmp/mutants.json"`, `report_path = "../mutants.json"`), true},
		{"timeout: 0s rejected", replaceOnce(`timeout = "30m"`, `timeout = "0s"`), true},
		{"max_equivalent: -1 rejected", replaceOnce("max_equivalent = 2", "max_equivalent = -1"), true},
		{"max_equivalent: 0 accepted (no escape hatch is legitimate)", replaceOnce("max_equivalent = 2", "max_equivalent = 0"), false},
		{"max_not_viable_pct: 0 rejected", replaceOnce("max_not_viable_pct = 25.0", "max_not_viable_pct = 0"), true},
		{"max_not_viable_pct: 101 rejected", replaceOnce("max_not_viable_pct = 25.0", "max_not_viable_pct = 101"), true},
		{"max_not_viable_pct: 100 accepted (boundary)", replaceOnce("max_not_viable_pct = 25.0", "max_not_viable_pct = 100"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := tt.mutate(validDocV5)
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

// TestParse_MutationFormatSet_MatchesMutantFormats covers AC6's own
// contract at the constitution layer: the set of `format` values Parse
// accepts for [mutation] is EXACTLY MutantFormats() — never a second,
// parallel literal list, and never the COVERAGE format list (a wrong
// error message here would send an implementer looking for "lcov" in the
// wrong registry).
func TestParse_MutationFormatSet_MatchesMutantFormats(t *testing.T) {
	for _, format := range MutantFormats() {
		t.Run(format, func(t *testing.T) {
			doc := strings.Replace(validDocV5, `format = "gremlins"`, fmt.Sprintf("format = %q", format), 1)
			if _, err := Parse([]byte(doc)); err != nil {
				t.Errorf("Parse(mutation.format=%s), a registered mutant format: %v", format, err)
			}
		})
	}

	doc := strings.Replace(validDocV5, `format = "gremlins"`, `format = "lcov"`, 1)
	_, err := Parse([]byte(doc))
	if err == nil {
		t.Fatal("Parse(mutation.format=lcov, a COVERAGE format): want error, got nil")
	}
	if !strings.Contains(err.Error(), "mutation.format") {
		t.Errorf("error = %q, want it to name mutation.format specifically (not coverage.format)", err.Error())
	}
}

// TestParse_MutationCommand_SharesArgvMessageWithGate covers the shared
// argv validator (D6): a mutation.command shell-string rejection shares
// its explanatory sentence with a gate's own rejection, byte for byte —
// both call argvShellStringProblem.
func TestParse_MutationCommand_SharesArgvMessageWithGate(t *testing.T) {
	gateDoc := strings.Replace(validDocV5, `command = ["make", "build"]`, `command = ["make build"]`, 1)
	_, gateErr := Parse([]byte(gateDoc))
	if gateErr == nil {
		t.Fatal("Parse(gate shell-string command): want error, got nil")
	}

	mutationDoc := strings.Replace(validDocV5, `command = ["make", "mutation", "BASE={{BASE_SHA}}"]`, `command = ["make mutation"]`, 1)
	_, mutationErr := Parse([]byte(mutationDoc))
	if mutationErr == nil {
		t.Fatal("Parse(mutation.command shell-string): want error, got nil")
	}

	const sharedSentence = "command is an argv vector, declare each argument as its own list element"
	if !strings.Contains(gateErr.Error(), sharedSentence) {
		t.Fatalf("gate error %q does not contain shared sentence %q", gateErr.Error(), sharedSentence)
	}
	if !strings.Contains(mutationErr.Error(), sharedSentence) {
		t.Fatalf("mutation.command error %q does not contain shared sentence %q", mutationErr.Error(), sharedSentence)
	}
}

// TestParse_MutationCommand_UnknownToken covers AC5's third row / G4b: any
// `{{...}}` sequence other than {{BASE_SHA}} is rejected AT PARSE TIME,
// naming the unknown token — it must never survive as a literal all the
// way to ExpandCommand.
func TestParse_MutationCommand_UnknownToken(t *testing.T) {
	doc := strings.Replace(validDocV5, `"BASE={{BASE_SHA}}"`, `"BASE={{BASESHA}}"`, 1)
	_, err := Parse([]byte(doc))
	if err == nil {
		t.Fatal("Parse(unknown {{...}} token): want error, got nil")
	}
	if !strings.Contains(err.Error(), "{{BASESHA}}") {
		t.Errorf("error = %q, want it to name the unknown token {{BASESHA}}", err.Error())
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("error = %v, want wrapping ErrInvalid", err)
	}
}

// TestParse_MutationCommand_TokenIsOptional covers AC5's second row: a
// command that never mentions {{BASE_SHA}} at all is valid, passed
// through untouched — the token is an optimisation, never a requirement
// (D3).
func TestParse_MutationCommand_TokenIsOptional(t *testing.T) {
	doc := strings.Replace(validDocV5, `command = ["make", "mutation", "BASE={{BASE_SHA}}"]`, `command = ["make", "mutation"]`, 1)
	c, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse(command without {{BASE_SHA}}): %v", err)
	}
	if len(c.Mutation.Command) != 2 {
		t.Fatalf("Mutation.Command = %v, want exactly [\"make\",\"mutation\"]", c.Mutation.Command)
	}
}

// TestExpandCommand_SubstitutesOnlyTheToken covers AC5's first row: the
// token is substituted in every element that carries it, byte for byte,
// and every OTHER element is left completely unchanged.
func TestExpandCommand_SubstitutesOnlyTheToken(t *testing.T) {
	argv := []string{"make", "mutation", "BASE={{BASE_SHA}}"}
	got := ExpandCommand(argv, "abc123")
	want := []string{"make", "mutation", "BASE=abc123"}
	if len(got) != len(want) {
		t.Fatalf("ExpandCommand() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ExpandCommand()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// The original argv must not have been mutated in place.
	if argv[2] != "BASE={{BASE_SHA}}" {
		t.Errorf("ExpandCommand mutated its input argv in place: %v", argv)
	}
}

// TestExpandCommand_TokenAbsent_ReturnsUnchanged is TestExpandCommand's own
// hermana: an argv with no token at all round-trips byte for byte.
func TestExpandCommand_TokenAbsent_ReturnsUnchanged(t *testing.T) {
	argv := []string{"make", "mutation"}
	got := ExpandCommand(argv, "abc123")
	if len(got) != 2 || got[0] != "make" || got[1] != "mutation" {
		t.Fatalf("ExpandCommand(no token) = %v, want unchanged %v", got, argv)
	}
}

// validDocV6 is a minimal, fully valid schema_version=6 document — adds
// [visual] and its nested [visual.compare] complete-and-off to validDocV5's
// own shape (SPEC-120 AC2-AC6).
const validDocV6 = `
schema_version = 6
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

[criteria]
enabled = false
timeout = "5m"
max_manual_pct = 25.0
max_command_pct = 30.0

[budget]
enabled = false
timeout = "2m"
test_globs = ["**/*_test.go"]
test_reach_depth = 3

[mutation]
enabled = false
format = "gremlins"
command = ["make", "mutation", "BASE={{BASE_SHA}}"]
report_path = "tmp/mutants.json"
timeout = "30m"
max_equivalent = 2
max_not_viable_pct = 25.0

[visual]
enabled = false
format = "visual-v1"
command = ["make", "visual"]
report_path = "tmp/visual/report.json"
timeout = "15m"
targets = []
fail_on_console_error = false
a11y_fail_impacts = []

[visual.compare]
enabled = false
reference_dir = ".mneme/visual/reference"
capture_dir = "tmp/visual/captures"
max_diff_pct = 0.1
`

// TestParse_SchemaVersion6Visual covers AC2's positive row (6, with all six
// sections complete, parses) and AC3: VisualDeclared is true only under
// schema 6.
func TestParse_SchemaVersion6Visual(t *testing.T) {
	c, err := Parse([]byte(validDocV6))
	if err != nil {
		t.Fatalf("Parse(validDocV6): %v", err)
	}
	if c.SchemaVersion != 6 {
		t.Errorf("SchemaVersion = %d, want 6", c.SchemaVersion)
	}
	if !c.VisualDeclared {
		t.Error("VisualDeclared = false, want true under schema_version 6")
	}
	if c.Visual.Format != "visual-v1" {
		t.Errorf("Visual.Format = %q, want visual-v1", c.Visual.Format)
	}
	if c.Visual.ReportPath != "tmp/visual/report.json" {
		t.Errorf("Visual.ReportPath = %q, want tmp/visual/report.json", c.Visual.ReportPath)
	}
	if c.Visual.Timeout.String() != "15m0s" {
		t.Errorf("Visual.Timeout = %v, want 15m0s", c.Visual.Timeout)
	}
	if len(c.Visual.Targets) != 0 {
		t.Errorf("Visual.Targets = %v, want empty (enabled=false)", c.Visual.Targets)
	}
	if c.Visual.Compare.ReferenceDir != ".mneme/visual/reference" {
		t.Errorf("Visual.Compare.ReferenceDir = %q, want .mneme/visual/reference", c.Visual.Compare.ReferenceDir)
	}
	if c.Visual.Compare.CaptureDir != "tmp/visual/captures" {
		t.Errorf("Visual.Compare.CaptureDir = %q, want tmp/visual/captures", c.Visual.Compare.CaptureDir)
	}
	if c.Visual.Compare.MaxDiffPct != 0.1 {
		t.Errorf("Visual.Compare.MaxDiffPct = %v, want 0.1", c.Visual.Compare.MaxDiffPct)
	}
}

// TestParse_VisualDeclaredVsUndeclared covers AC3's three rows for [visual]
// specifically: schema 5 (no [visual]) -> OK, declared false; schema 5 WITH
// [visual] -> error naming schema_version; schema 6 -> declared true. This
// is G2's positive hermana: the range must NOT narrow, so every prior
// schema keeps parsing.
func TestParse_VisualDeclaredVsUndeclared(t *testing.T) {
	c5, err := Parse([]byte(validDocV5))
	if err != nil {
		t.Fatalf("Parse(validDocV5): %v", err)
	}
	if c5.VisualDeclared {
		t.Error("schema 5 with no [visual]: VisualDeclared = true, want false")
	}

	withVisualUnderSchema5 := validDocV5 + `
[visual]
enabled = false
format = "visual-v1"
command = ["make", "visual"]
report_path = "tmp/visual/report.json"
timeout = "15m"
targets = []
fail_on_console_error = false
a11y_fail_impacts = []

[visual.compare]
enabled = false
reference_dir = ".mneme/visual/reference"
capture_dir = "tmp/visual/captures"
max_diff_pct = 0.1
`
	_, err = Parse([]byte(withVisualUnderSchema5))
	if err == nil {
		t.Fatal("Parse([visual] under schema_version 5): want error, got nil")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error = %q, want it to name schema_version", err.Error())
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("error = %v, want wrapping ErrInvalid", err)
	}

	c6, err := Parse([]byte(validDocV6))
	if err != nil {
		t.Fatalf("Parse(validDocV6): %v", err)
	}
	if !c6.VisualDeclared {
		t.Error("schema 6: VisualDeclared = false, want true")
	}
}

// TestParse_Visual_MissingRequiredKey covers AC4: one row per missing
// [visual]/[visual.compare] key, each naming itself, and none filled in
// with a default.
func TestParse_Visual_MissingRequiredKey(t *testing.T) {
	tests := []struct {
		wantKey string
	}{
		{"visual.enabled"},
		{"visual.format"},
		{"visual.command"},
		{"visual.report_path"},
		{"visual.timeout"},
		{"visual.targets"},
		{"visual.fail_on_console_error"},
		{"visual.a11y_fail_impacts"},
		{"visual.compare.enabled"},
		{"visual.compare.reference_dir"},
		{"visual.compare.capture_dir"},
		{"visual.compare.max_diff_pct"},
	}
	for _, tt := range tests {
		t.Run(tt.wantKey, func(t *testing.T) {
			doc := removeTOMLKeyLine(validDocV6, tt.wantKey)
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

// TestParse_Visual_TargetsEmptyDenominator covers AC5's two opposite
// hermanas (G4a/G4b): enabled=true with an empty targets list is an error
// naming targets; enabled=false with an empty list is OK — the shape this
// repository's own constitution needs (D15).
func TestParse_Visual_TargetsEmptyDenominator(t *testing.T) {
	// G4b (already true in validDocV6): enabled=false, targets=[] -> OK.
	if _, err := Parse([]byte(validDocV6)); err != nil {
		t.Fatalf("Parse(validDocV6, enabled=false targets=[]): %v", err)
	}

	// G4a: flip enabled to true with targets still empty -> error naming
	// targets.
	doc := strings.Replace(validDocV6, "[visual]\nenabled = false", "[visual]\nenabled = true", 1)
	_, err := Parse([]byte(doc))
	if err == nil {
		t.Fatal("Parse(visual.enabled=true, targets=[]): want error, got nil")
	}
	if !strings.Contains(err.Error(), "targets") {
		t.Errorf("error = %q, want it to name targets", err.Error())
	}

	// Hermana: enabled=true WITH a declared target -> OK.
	docWithTarget := strings.Replace(doc, "targets = []", `targets = ["home-light-360"]`, 1)
	if _, err := Parse([]byte(docWithTarget)); err != nil {
		t.Fatalf("Parse(visual.enabled=true, targets=[home-light-360]): %v", err)
	}
}

// TestParse_Visual_TargetsValidation covers the rest of AC5: duplicate and
// whitespace-containing identifiers are rejected.
func TestParse_Visual_TargetsValidation(t *testing.T) {
	enabledDoc := strings.Replace(validDocV6, "[visual]\nenabled = false", "[visual]\nenabled = true", 1)

	tests := []struct {
		name    string
		targets string
		wantErr bool
	}{
		{"duplicate identifier rejected", `targets = ["a", "a"]`, true},
		{"whitespace identifier rejected", `targets = ["home light"]`, true},
		{"empty-string identifier rejected", `targets = [""]`, true},
		{"distinct identifiers accepted", `targets = ["a", "b"]`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := strings.Replace(enabledDoc, "targets = []", tt.targets, 1)
			_, err := Parse([]byte(doc))
			if tt.wantErr && err == nil {
				t.Fatalf("Parse(%s): want error, got nil", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Parse(%s): want no error, got %v", tt.name, err)
			}
		})
	}
}

// TestParse_Visual_FieldValidation covers AC4's paired validation rows for
// the scalar [visual] keys (format/command/report_path/timeout).
func TestParse_Visual_FieldValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr bool
	}{
		{"format: lcov rejected (not a visual format)", replaceOnce(`format = "visual-v1"`, `format = "lcov"`), true},
		{"format: visual-v1 accepted", replaceOnce(`format = "visual-v1"`, `format = "visual-v1"`), false},
		{"command: single-element shell string rejected", replaceOnce(`command = ["make", "visual"]`, `command = ["make visual"]`), true},
		{"report_path: empty rejected", replaceOnce(`report_path = "tmp/visual/report.json"`, `report_path = ""`), true},
		{"report_path: traversal rejected", replaceOnce(`report_path = "tmp/visual/report.json"`, `report_path = "../report.json"`), true},
		{"timeout: 0s rejected", replaceOnce(`timeout = "15m"`, `timeout = "0s"`), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := tt.mutate(validDocV6)
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

// TestParse_VisualA11yImpacts covers AC4/D6: an impact outside the closed
// vocabulary is rejected; the four accepted values are OK; a duplicate is
// rejected.
func TestParse_VisualA11yImpacts(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"unknown impact rejected", `["catastrophic"]`, true},
		{"duplicate impact rejected", `["critical", "critical"]`, true},
		{"all four accepted", `["critical", "serious", "moderate", "minor"]`, false},
		{"empty accepted (measure, never block)", `[]`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := strings.Replace(validDocV6, "a11y_fail_impacts = []", "a11y_fail_impacts = "+tt.value, 1)
			_, err := Parse([]byte(doc))
			if tt.wantErr && err == nil {
				t.Fatalf("Parse(%s): want error, got nil", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Parse(%s): want no error, got %v", tt.name, err)
			}
		})
	}
}

// TestParse_VisualCompare_DirectoryValidation covers AC6: distinct and
// non-nested directories, with its permissive hermana (sibling directories
// sharing a string prefix are OK, G5b), and the enabled=false shape where
// both may be empty.
func TestParse_VisualCompare_DirectoryValidation(t *testing.T) {
	enabledDoc := strings.Replace(validDocV6, "[visual.compare]\nenabled = false", "[visual.compare]\nenabled = true", 1)

	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr bool
	}{
		{
			"capture_dir inside reference_dir rejected",
			replaceOnce(`capture_dir = "tmp/visual/captures"`, `capture_dir = ".mneme/visual/reference/captures"`),
			true,
		},
		{
			"reference_dir inside capture_dir rejected",
			func(doc string) string {
				doc = strings.Replace(doc, `capture_dir = "tmp/visual/captures"`, `capture_dir = "tmp/visual"`, 1)
				doc = strings.Replace(doc, `reference_dir = ".mneme/visual/reference"`, `reference_dir = "tmp/visual/reference"`, 1)
				return doc
			},
			true,
		},
		{
			"identical directories rejected",
			replaceOnce(`capture_dir = "tmp/visual/captures"`, `capture_dir = ".mneme/visual/reference"`),
			true,
		},
		{
			"report_path inside reference_dir rejected",
			replaceOnce(`report_path = "tmp/visual/report.json"`, `report_path = ".mneme/visual/reference/report.json"`),
			true,
		},
		{
			"sibling directories sharing a string prefix accepted (G5b)",
			replaceOnce(`reference_dir = ".mneme/visual/reference"`, `reference_dir = ".mneme/visual/reference-old"`),
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := tt.mutate(enabledDoc)
			_, err := Parse([]byte(doc))
			if tt.wantErr && err == nil {
				t.Fatalf("Parse(%s): want error, got nil\ndoc:\n%s", tt.name, doc)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Parse(%s): want no error, got %v\ndoc:\n%s", tt.name, err, doc)
			}
		})
	}

	// enabled=false with BOTH directories empty is legitimate (D15's own
	// shape for a repository with no graphical interface).
	offEmptyDoc := strings.Replace(validDocV6, `reference_dir = ".mneme/visual/reference"`, `reference_dir = ""`, 1)
	offEmptyDoc = strings.Replace(offEmptyDoc, `capture_dir = "tmp/visual/captures"`, `capture_dir = ""`, 1)
	if _, err := Parse([]byte(offEmptyDoc)); err != nil {
		t.Fatalf("Parse(compare.enabled=false, both dirs empty): %v", err)
	}
}

// TestParse_VisualCompare_MaxDiffPct covers the tolerance's own bounds.
func TestParse_VisualCompare_MaxDiffPct(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"negative rejected", "-0.1", true},
		{"101 rejected", "101", true},
		{"0 accepted (zero tolerance is legitimate)", "0", false},
		{"100 accepted (boundary)", "100", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := strings.Replace(validDocV6, "max_diff_pct = 0.1", "max_diff_pct = "+tt.value, 1)
			_, err := Parse([]byte(doc))
			if tt.wantErr && err == nil {
				t.Fatalf("Parse(%s): want error, got nil", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Parse(%s): want no error, got %v", tt.name, err)
			}
		})
	}
}

// TestParse_VisualFormatSet_MatchesFormats covers AC7's contract at the
// constitution layer: the set of `format` values Parse accepts for
// [visual] is EXACTLY VisualFormats() — never a second, parallel literal
// list, and never the coverage or mutation format lists.
func TestParse_VisualFormatSet_MatchesFormats(t *testing.T) {
	for _, format := range VisualFormats() {
		t.Run(format, func(t *testing.T) {
			doc := strings.Replace(validDocV6, `format = "visual-v1"`, fmt.Sprintf("format = %q", format), 1)
			if _, err := Parse([]byte(doc)); err != nil {
				t.Errorf("Parse(visual.format=%s), a registered visual format: %v", format, err)
			}
		})
	}

	doc := strings.Replace(validDocV6, `format = "visual-v1"`, `format = "gremlins"`, 1)
	_, err := Parse([]byte(doc))
	if err == nil {
		t.Fatal("Parse(visual.format=gremlins, a MUTATION format): want error, got nil")
	}
	if !strings.Contains(err.Error(), "visual.format") {
		t.Errorf("error = %q, want it to name visual.format specifically", err.Error())
	}
}

// TestParse_VisualCommand_SharesArgvMessageWithGate covers the shared argv
// validator (D6 of S1): a visual.command shell-string rejection shares its
// explanatory sentence with a gate's own rejection, byte for byte.
func TestParse_VisualCommand_SharesArgvMessageWithGate(t *testing.T) {
	gateDoc := strings.Replace(validDocV6, `command = ["make", "build"]`, `command = ["make build"]`, 1)
	_, gateErr := Parse([]byte(gateDoc))
	if gateErr == nil {
		t.Fatal("Parse(gate shell-string command): want error, got nil")
	}

	visualDoc := strings.Replace(validDocV6, `command = ["make", "visual"]`, `command = ["make visual"]`, 1)
	_, visualErr := Parse([]byte(visualDoc))
	if visualErr == nil {
		t.Fatal("Parse(visual shell-string command): want error, got nil")
	}

	const sharedSentence = "command is an argv vector, declare each argument as its own list element"
	if !strings.Contains(gateErr.Error(), sharedSentence) {
		t.Fatalf("gate error %q does not contain shared sentence %q", gateErr.Error(), sharedSentence)
	}
	if !strings.Contains(visualErr.Error(), sharedSentence) {
		t.Fatalf("visual.command error %q does not contain shared sentence %q", visualErr.Error(), sharedSentence)
	}
}
