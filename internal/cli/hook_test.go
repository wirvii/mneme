package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// TestPrintContextHook_WithRules verifies that when resp.Rules is non-empty the
// output contains the "## Active Rules" heading with [SEVERITY] tags and the
// _Applies to:_ line for each rule.
func TestPrintContextHook_WithRules(t *testing.T) {
	blockRule := model.Memory{
		ID:       "rule-1",
		Title:    "Never store plain passwords",
		Content:  "Always use bcrypt with cost >= 12.",
		Type:     model.TypeRule,
		Severity: model.SeverityBlock,
		AppliesTo: []string{"internal/**/*.go"},
	}
	warnRule := model.Memory{
		ID:        "rule-2",
		Title:     "SQL in .sql files only",
		Content:   "No inline SQL in Go code.",
		Type:      model.TypeRule,
		Severity:  model.SeverityWarn,
		AppliesTo: []string{"**/*.go", "!**/*_test.go"},
	}

	resp := &model.ContextResponse{
		Project:     "test/project",
		Rules:       []model.Memory{blockRule, warnRule},
		RulesCount:  2,
		RulesTokens: 42,
		Memories:    []model.Memory{},
		Included:    0,
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	checks := []struct {
		name    string
		want    string
	}{
		{"active rules heading", "## Active Rules (2 rules, ~42 tokens)"},
		{"block tag", "[BLOCK] Never store plain passwords"},
		{"warn tag", "[WARN] SQL in .sql files only"},
		{"applies to block", "_Applies to: internal/**/*.go_"},
		{"applies to warn", "_Applies to: **/*.go, !**/*_test.go_"},
		{"separator", "---"},
		{"context start", "<!-- mneme:context:start -->"},
		{"context end", "<!-- mneme:context:end -->"},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(got, tc.want) {
				t.Errorf("output does not contain %q\nfull output:\n%s", tc.want, got)
			}
		})
	}

	// Rules section must appear before Memories section.
	rulesPos := strings.Index(got, "## Active Rules")
	memoriesPos := strings.Index(got, "## Loaded Memories")
	if memoriesPos != -1 && rulesPos > memoriesPos {
		t.Error("## Active Rules section appears after ## Loaded Memories")
	}
}

// TestPrintContextHook_WithoutRules verifies that when resp.Rules is empty the
// "## Active Rules" heading does not appear in the output.
func TestPrintContextHook_WithoutRules(t *testing.T) {
	resp := &model.ContextResponse{
		Project: "test/project",
		Memories: []model.Memory{
			{
				ID:      "mem-1",
				Title:   "Architecture decision",
				Content: "Hexagonal architecture.",
				Type:    model.TypeArchitecture,
			},
		},
		Included:       1,
		TotalAvailable: 5,
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	if strings.Contains(got, "## Active Rules") {
		t.Errorf("output should not contain '## Active Rules' when resp.Rules is empty")
	}
	if !strings.Contains(got, "## Loaded Memories") {
		t.Error("expected ## Loaded Memories heading")
	}
}

// TestPrintContextHook_RulesTruncated verifies that when RulesTruncated > 0 the
// output includes the truncation notice so the user knows to increase rules_budget.
func TestPrintContextHook_RulesTruncated(t *testing.T) {
	resp := &model.ContextResponse{
		Project: "test/project",
		Rules: []model.Memory{
			{
				ID:       "rule-1",
				Title:    "Block rule",
				Content:  "Reject this.",
				Type:     model.TypeRule,
				Severity: model.SeverityBlock,
			},
		},
		RulesCount:     1,
		RulesTokens:    10,
		RulesTruncated: 3,
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	want := "3 rules truncated"
	if !strings.Contains(got, want) {
		t.Errorf("output does not contain %q\nfull output:\n%s", want, got)
	}
	if !strings.Contains(got, "rules_budget") {
		t.Error("truncation notice should mention rules_budget config")
	}
}

// TestPrintContextHook_LastSessionBeforeRules verifies that the Last Session
// section appears before Active Rules in the rendered output — matching the
// documented output order.
func TestPrintContextHook_LastSessionBeforeRules(t *testing.T) {
	ended := time.Now()
	resp := &model.ContextResponse{
		Project: "test/project",
		LastSession: &model.SessionSummary{
			ID:      "sess-1",
			Summary: "Previous session summary.",
			EndedAt: &ended,
		},
		Rules: []model.Memory{
			{
				ID:       "rule-1",
				Title:    "Block rule",
				Content:  "Must not do X.",
				Type:     model.TypeRule,
				Severity: model.SeverityBlock,
			},
		},
		RulesCount:  1,
		RulesTokens: 8,
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	lastPos := strings.Index(got, "## Last Session")
	rulesPos := strings.Index(got, "## Active Rules")

	if lastPos == -1 {
		t.Fatal("expected ## Last Session in output")
	}
	if rulesPos == -1 {
		t.Fatal("expected ## Active Rules in output")
	}
	if lastPos > rulesPos {
		t.Error("## Last Session should appear before ## Active Rules")
	}
}

// TestPrintContextHook_NoMemoriesNoRules verifies the "no memories" message
// is shown when both Memories and Rules are empty.
func TestPrintContextHook_NoMemoriesNoRules(t *testing.T) {
	resp := &model.ContextResponse{
		Project:        "test/project",
		TotalAvailable: 0,
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	if !strings.Contains(got, "No Memories Found") {
		t.Errorf("expected 'No Memories Found', got:\n%s", got)
	}
}
