package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/rules"
	"github.com/juanftp/mneme/internal/service"
)

// ─── slugifyTitle tests ────────────────────────────────────────────────────

func TestSlugifyTitle_Simple(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "Never use time.Now()", "rule/never-use-time-now"},
		{"sql_files", "SQL in .sql files only", "rule/sql-in-sql-files-only"},
		{"unicode_accents", "No push directo al main", "rule/no-push-directo-al-main"},
		{"spaces", "Keep it simple", "rule/keep-it-simple"},
		{"already_lowercase", "lowercase title", "rule/lowercase-title"},
		{"special_chars", "C++ code style", "rule/c-code-style"},
		{"only_digits", "Rule 42 applies", "rule/rule-42-applies"},
		{"trailing_punct", "Don't do this!", "rule/don-t-do-this"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := slugifyTitle(tc.input)
			if got != tc.want {
				t.Errorf("slugifyTitle(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSlugifyTitle_Long(t *testing.T) {
	// Build a title longer than 60 chars.
	long := strings.Repeat("word ", 20) // 100 chars
	got := slugifyTitle(long)

	// The result must start with "rule/" and the slug part must be <= 60 chars.
	if !strings.HasPrefix(got, "rule/") {
		t.Errorf("slugifyTitle: missing rule/ prefix, got %q", got)
	}
	slug := strings.TrimPrefix(got, "rule/")
	if len(slug) > 60 {
		t.Errorf("slug length %d > 60: %q", len(slug), slug)
	}
}

func TestSlugifyTitle_Empty(t *testing.T) {
	got := slugifyTitle("")
	// Slug with empty title should be "rule/" — no characters after the prefix.
	if got != "rule/" {
		t.Errorf("slugifyTitle(\"\") = %q, want %q", got, "rule/")
	}
}

// ─── printRulesTable tests ─────────────────────────────────────────────────

func newTestRule(id, title string, sev model.Severity, scope model.Scope, appliesTo []string) *model.Memory {
	return &model.Memory{
		ID:        id,
		Title:     title,
		Type:      model.TypeRule,
		Severity:  sev,
		Scope:     scope,
		AppliesTo: appliesTo,
		Importance: 0.95,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

func TestPrintRulesTable_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := printRulesTable(&buf, nil); err != nil {
		t.Fatalf("printRulesTable: unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "No rules found.") {
		t.Errorf("expected 'No rules found.' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "mneme rule add") {
		t.Errorf("expected hint to 'mneme rule add' in output, got:\n%s", out)
	}
}

func TestPrintRulesTable_ThreeRows(t *testing.T) {
	list := []*model.Memory{
		newTestRule("019de100-abcd-7fff-0000-000000000001", "Never edit vendor/", model.SeverityBlock, model.ScopeProject, []string{"vendor/**"}),
		newTestRule("019de101-abcd-7fff-0000-000000000002", "SQL in .sql files only", model.SeverityWarn, model.ScopeProject, []string{"**/*.go"}),
		newTestRule("019de102-abcd-7fff-0000-000000000003", "Prefer Server Components", model.SeverityInfo, model.ScopeGlobal, []string{"tool:Edit+**/*.tsx"}),
	}

	var buf bytes.Buffer
	if err := printRulesTable(&buf, list); err != nil {
		t.Fatalf("printRulesTable: unexpected error: %v", err)
	}
	out := buf.String()

	// All three IDs (truncated to 8) must appear.
	if !strings.Contains(out, "019de100") {
		t.Errorf("expected ID 019de100 in output")
	}
	if !strings.Contains(out, "019de101") {
		t.Errorf("expected ID 019de101 in output")
	}
	if !strings.Contains(out, "019de102") {
		t.Errorf("expected ID 019de102 in output")
	}

	// Summary line must reflect counts.
	if !strings.Contains(out, "3 rules") {
		t.Errorf("expected '3 rules' in summary, got:\n%s", out)
	}
	if !strings.Contains(out, "1 block") {
		t.Errorf("expected '1 block' in summary, got:\n%s", out)
	}
	if !strings.Contains(out, "1 warn") {
		t.Errorf("expected '1 warn' in summary, got:\n%s", out)
	}
	if !strings.Contains(out, "1 info") {
		t.Errorf("expected '1 info' in summary, got:\n%s", out)
	}
}

func TestPrintRulesTable_LongTitleTruncated(t *testing.T) {
	long := strings.Repeat("a", 50)
	list := []*model.Memory{
		newTestRule("aaaaaaaa-0000-0000-0000-000000000001", long, model.SeverityWarn, model.ScopeProject, []string{"**"}),
	}

	var buf bytes.Buffer
	if err := printRulesTable(&buf, list); err != nil {
		t.Fatalf("printRulesTable: unexpected error: %v", err)
	}
	out := buf.String()

	// The full title should not appear; instead a truncated version.
	if strings.Contains(out, long) {
		t.Errorf("expected long title to be truncated, but got full title in output")
	}
}

// ─── printRulesJSON tests ──────────────────────────────────────────────────

func TestPrintRulesJSON_VersionWrapper(t *testing.T) {
	list := []*model.Memory{
		newTestRule("019de100-abcd-7fff-0000-000000000001", "Block rule", model.SeverityBlock, model.ScopeProject, []string{"vendor/**"}),
	}

	var buf bytes.Buffer
	if err := printRulesJSON(&buf, list); err != nil {
		t.Fatalf("printRulesJSON: unexpected error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `"version": "1"`) {
		t.Errorf("expected version:1 in JSON output, got:\n%s", out)
	}
	if !strings.Contains(out, `"rules"`) {
		t.Errorf("expected 'rules' key in JSON output, got:\n%s", out)
	}
	if !strings.Contains(out, `"severity": "block"`) {
		t.Errorf("expected severity in JSON output, got:\n%s", out)
	}
	if !strings.Contains(out, `"applies_to"`) {
		t.Errorf("expected applies_to in JSON output, got:\n%s", out)
	}
}

func TestPrintRulesJSON_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := printRulesJSON(&buf, nil); err != nil {
		t.Fatalf("printRulesJSON: unexpected error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `"version": "1"`) {
		t.Errorf("expected version:1 in JSON output")
	}
	if !strings.Contains(out, `"rules": []`) {
		t.Errorf("expected empty rules array in JSON output, got:\n%s", out)
	}
}

// ─── printTestOutput tests ─────────────────────────────────────────────────

func TestPrintTestOutput_NoMatch(t *testing.T) {
	var buf bytes.Buffer
	result := rules.MatchResult{Matched: nil, MaxSev: ""}
	if err := printTestOutput(&buf, "Edit", "docs/README.md", 5, result); err != nil {
		t.Fatalf("printTestOutput: unexpected error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Evaluated: 5 rules") {
		t.Errorf("expected evaluation count, got:\n%s", out)
	}
	if !strings.Contains(out, "Matched:   0 rules") {
		t.Errorf("expected match count, got:\n%s", out)
	}
	if !strings.Contains(out, "ALLOWED (no rules matched)") {
		t.Errorf("expected ALLOWED message, got:\n%s", out)
	}
}

func TestPrintTestOutput_BlockMatch(t *testing.T) {
	blockRule := model.Memory{
		ID:       "rule-block",
		Title:    "Never edit vendor/",
		Content:  "Do not edit vendor files.",
		Type:     model.TypeRule,
		Severity: model.SeverityBlock,
		AppliesTo: []string{"vendor/**"},
	}
	result := rules.MatchResult{
		Matched: []rules.MatchedRule{
			{Rule: blockRule, Entries: []string{"vendor/**"}},
		},
		MaxSev: model.SeverityBlock,
	}

	var buf bytes.Buffer
	if err := printTestOutput(&buf, "Edit", "vendor/foo.go", 3, result); err != nil {
		t.Fatalf("printTestOutput: unexpected error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "[BLOCK] Never edit vendor/") {
		t.Errorf("expected [BLOCK] tag and title, got:\n%s", out)
	}
	if !strings.Contains(out, "Matched by: vendor/**") {
		t.Errorf("expected matched entry, got:\n%s", out)
	}
	if !strings.Contains(out, "Result: BLOCKED") {
		t.Errorf("expected BLOCKED result, got:\n%s", out)
	}
}

func TestPrintTestOutput_WarnMatch(t *testing.T) {
	warnRule := model.Memory{
		ID:       "rule-warn",
		Title:    "SQL in .sql files only",
		Content:  "No inline SQL strings.",
		Type:     model.TypeRule,
		Severity: model.SeverityWarn,
		AppliesTo: []string{"**/*.go", "!**/*_test.go"},
	}
	result := rules.MatchResult{
		Matched: []rules.MatchedRule{
			{Rule: warnRule, Entries: []string{"**/*.go"}},
		},
		MaxSev: model.SeverityWarn,
	}

	var buf bytes.Buffer
	if err := printTestOutput(&buf, "Edit", "internal/store/memory.go", 5, result); err != nil {
		t.Fatalf("printTestOutput: unexpected error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "[WARN] SQL in .sql files only") {
		t.Errorf("expected [WARN] tag, got:\n%s", out)
	}
	if !strings.Contains(out, "ALLOWED (with 1 warning)") {
		t.Errorf("expected ALLOWED with warnings, got:\n%s", out)
	}
	// Negation hint should appear.
	if !strings.Contains(out, "!**/*_test.go") {
		t.Errorf("expected negation entry in output, got:\n%s", out)
	}
}

func TestPrintTestOutput_NoPath(t *testing.T) {
	var buf bytes.Buffer
	result := rules.MatchResult{Matched: nil, MaxSev: ""}
	if err := printTestOutput(&buf, "Edit", "", 0, result); err != nil {
		t.Fatalf("printTestOutput: unexpected error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "No --path specified") {
		t.Errorf("expected no-path warning, got:\n%s", out)
	}
}

func TestPrintTestJSON_Output(t *testing.T) {
	blockRule := model.Memory{
		ID:       "rule-block",
		Title:    "Never edit vendor/",
		Severity: model.SeverityBlock,
		AppliesTo: []string{"vendor/**"},
	}
	result := rules.MatchResult{
		Matched: []rules.MatchedRule{
			{Rule: blockRule, Entries: []string{"vendor/**"}},
		},
		MaxSev: model.SeverityBlock,
	}

	var buf bytes.Buffer
	if err := printTestJSON(&buf, "Edit", "vendor/foo.go", 3, result); err != nil {
		t.Fatalf("printTestJSON: unexpected error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `"tool": "Edit"`) {
		t.Errorf("expected tool field, got:\n%s", out)
	}
	if !strings.Contains(out, `"result": "BLOCKED"`) {
		t.Errorf("expected BLOCKED result, got:\n%s", out)
	}
	if !strings.Contains(out, `"matched_entries"`) {
		t.Errorf("expected matched_entries field, got:\n%s", out)
	}
}

// ─── ListRulesOptions integration (via service) ────────────────────────────
// These tests verify that the CLI struct types match the service layer.
// They do NOT call initService() — they just verify the type contract.

func TestListRulesOptions_Compile(t *testing.T) {
	// If this compiles, the types are compatible.
	opts := service.ListRulesOptions{
		Scope:    "project",
		Severity: model.SeverityBlock,
		Limit:    10,
	}
	_ = opts
}
