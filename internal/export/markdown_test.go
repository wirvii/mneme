package export_test

import (
	"strings"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/export"
	"github.com/juanftp/mneme/internal/model"
)

// ─── fixtures ────────────────────────────────────────────────────────────────

var fixedDate = time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
var fixedDate2 = time.Date(2026, 4, 12, 15, 30, 0, 0, time.UTC)

func decisionMemory() *model.Memory {
	return &model.Memory{
		ID:         "019d891c-abcd-ef01-2345-6789abcdef01",
		Type:       model.TypeDecision,
		Scope:      model.ScopeProject,
		Title:      "Use Redis for session storage",
		Content:    "## What\nSwitched to Redis.\n\n## Why\nLower latency.",
		TopicKey:   "architecture/session",
		Project:    "owner/repo",
		CreatedAt:  fixedDate,
		UpdatedAt:  fixedDate2,
		Importance: 0.9,
	}
}

func discoveryMemory() *model.Memory {
	return &model.Memory{
		ID:        "02abc123-0000-0000-0000-000000000000",
		Type:      model.TypeDiscovery,
		Scope:     model.ScopeProject,
		Title:     "Legacy API returns 200 for errors",
		Content:   "The upstream API always returns HTTP 200 with an error field in the body.",
		TopicKey:  "",
		Project:   "owner/repo",
		CreatedAt: fixedDate,
		UpdatedAt: fixedDate,
		Importance: 0.75,
	}
}

func bugfixMemory() *model.Memory {
	return &model.Memory{
		ID:        "03def456-0000-0000-0000-000000000000",
		Type:      model.TypeBugfix,
		Scope:     model.ScopeProject,
		Title:     "Fix N+1 query in UserList",
		Content:   "Added DataLoader to batch user fetches.",
		TopicKey:  "bugfix/userlist-n+1",
		Project:   "owner/repo",
		CreatedAt: fixedDate,
		UpdatedAt: fixedDate,
		Importance: 0.8,
	}
}

// ─── typeTitle ───────────────────────────────────────────────────────────────

func TestTypeTitle(t *testing.T) {
	// typeTitle is unexported but its effect is visible through RenderAll output.
	// We test it indirectly via RenderType which calls typeTitle for the header.
	tests := []struct {
		memType  model.MemoryType
		contains string
	}{
		{model.TypeDecision, "Decision"},
		{model.TypeDiscovery, "Discovery"},
		{model.TypeBugfix, "Bugfix"},
		{model.TypePattern, "Pattern"},
		{model.TypePreference, "Preference"},
		{model.TypeConvention, "Convention"},
		{model.TypeArchitecture, "Architecture"},
		{model.TypeConfig, "Config"},
		{model.TypeSessionSummary, "Session Summary"},
	}

	opts := export.MarkdownOptions{Project: "test/project"}

	for _, tt := range tests {
		t.Run(string(tt.memType), func(t *testing.T) {
			m := &model.Memory{
				ID: "00000000-0000-0000-0000-000000000000",
				Type: tt.memType,
				Title: "test",
				Content: "body",
				CreatedAt: fixedDate,
				UpdatedAt: fixedDate,
				Importance: 0.5,
			}
			var buf strings.Builder
			if err := export.RenderType(&buf, tt.memType, []*model.Memory{m}, opts); err != nil {
				t.Fatalf("RenderType error: %v", err)
			}
			got := buf.String()
			if !strings.Contains(got, tt.contains) {
				t.Errorf("expected header to contain %q, got:\n%s", tt.contains, got)
			}
		})
	}
}

// ─── RenderType ──────────────────────────────────────────────────────────────

func TestRenderType_SingleMemory(t *testing.T) {
	m := decisionMemory()
	opts := export.MarkdownOptions{Project: "owner/repo"}

	var buf strings.Builder
	if err := export.RenderType(&buf, model.TypeDecision, []*model.Memory{m}, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()

	checks := []struct {
		label   string
		present string
	}{
		{"header title", "# mneme -- Decision Memories"},
		{"project", "Project: owner/repo"},
		{"count", "Count: 1"},
		{"memory heading", "### Use Redis for session storage"},
		{"id truncated", "019d891c"},
		{"created date", "2026-04-10"},
		{"updated date", "2026-04-12"},
		{"importance", "0.90"},
		{"topic key", "architecture/session"},
		{"content", "Switched to Redis."},
	}

	for _, c := range checks {
		t.Run(c.label, func(t *testing.T) {
			if !strings.Contains(got, c.present) {
				t.Errorf("expected output to contain %q\nfull output:\n%s", c.present, got)
			}
		})
	}
}

func TestRenderType_NoTopicKey(t *testing.T) {
	m := discoveryMemory() // TopicKey is empty
	opts := export.MarkdownOptions{Project: "owner/repo"}

	var buf strings.Builder
	if err := export.RenderType(&buf, model.TypeDiscovery, []*model.Memory{m}, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()

	if strings.Contains(got, "**Topic:**") {
		t.Errorf("expected no Topic line when TopicKey is empty, got:\n%s", got)
	}
}

func TestRenderType_Separator(t *testing.T) {
	memories := []*model.Memory{decisionMemory(), decisionMemory()}

	var buf strings.Builder
	opts := export.MarkdownOptions{Project: "owner/repo"}
	if err := export.RenderType(&buf, model.TypeDecision, memories, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()

	// Exactly one separator between two entries.
	count := strings.Count(got, "\n---\n")
	if count != 1 {
		t.Errorf("expected exactly 1 separator for 2 memories, got %d\noutput:\n%s", count, got)
	}
}

func TestRenderType_NoSeparatorAfterLast(t *testing.T) {
	m := decisionMemory()
	opts := export.MarkdownOptions{Project: "owner/repo"}

	var buf strings.Builder
	if err := export.RenderType(&buf, model.TypeDecision, []*model.Memory{m}, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()

	if strings.Contains(got, "\n---\n") {
		t.Errorf("expected no separator when only one memory, got:\n%s", got)
	}
}

// ─── RenderAll ───────────────────────────────────────────────────────────────

func TestRenderAll_GroupsAndOrders(t *testing.T) {
	memories := []*model.Memory{
		bugfixMemory(),    // TypeBugfix
		decisionMemory(),  // TypeDecision
		discoveryMemory(), // TypeDiscovery
	}
	opts := export.MarkdownOptions{Project: "owner/repo", SingleFile: true}

	var buf strings.Builder
	if err := export.RenderAll(&buf, memories, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()

	// Check that the canonical order is respected: Decision before Discovery before Bugfix.
	posDecision := strings.Index(got, "## Decision")
	posDiscovery := strings.Index(got, "## Discovery")
	posBugfix := strings.Index(got, "## Bugfix")

	if posDecision < 0 {
		t.Fatal("missing ## Decision section")
	}
	if posDiscovery < 0 {
		t.Fatal("missing ## Discovery section")
	}
	if posBugfix < 0 {
		t.Fatal("missing ## Bugfix section")
	}
	if !(posDecision < posDiscovery && posDiscovery < posBugfix) {
		t.Errorf("sections not in canonical order: Decision=%d Discovery=%d Bugfix=%d",
			posDecision, posDiscovery, posBugfix)
	}
}

func TestRenderAll_SkipsEmptyTypes(t *testing.T) {
	memories := []*model.Memory{decisionMemory()}
	opts := export.MarkdownOptions{Project: "owner/repo"}

	var buf strings.Builder
	if err := export.RenderAll(&buf, memories, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()

	// Only Decision section should be present.
	for _, absent := range []string{"## Discovery", "## Bugfix", "## Pattern"} {
		if strings.Contains(got, absent) {
			t.Errorf("expected section %q to be absent for empty type\noutput:\n%s", absent, got)
		}
	}
}

func TestRenderAll_Header(t *testing.T) {
	opts := export.MarkdownOptions{Project: "myorg/myrepo"}

	var buf strings.Builder
	if err := export.RenderAll(&buf, nil, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()

	if !strings.HasPrefix(got, "# mneme -- Project Memories\n") {
		t.Errorf("unexpected header prefix:\n%s", got)
	}
	if !strings.Contains(got, "myorg/myrepo") {
		t.Errorf("header missing project slug:\n%s", got)
	}
}

func TestRenderAll_SectionCount(t *testing.T) {
	// 3 entries: 2 decisions, 1 discovery
	d1 := decisionMemory()
	d2 := decisionMemory()
	d2.Title = "Another decision"
	memories := []*model.Memory{d1, d2, discoveryMemory()}

	opts := export.MarkdownOptions{Project: "owner/repo"}
	var buf strings.Builder
	if err := export.RenderAll(&buf, memories, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()

	if !strings.Contains(got, "## Decision (2)") {
		t.Errorf("expected '## Decision (2)' in output:\n%s", got)
	}
	if !strings.Contains(got, "## Discovery (1)") {
		t.Errorf("expected '## Discovery (1)' in output:\n%s", got)
	}
}

// ─── truncateID edge cases ────────────────────────────────────────────────────

func TestRenderType_IDTruncation(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		expected string
	}{
		{"full uuid", "019d891c-abcd-ef01-2345-6789abcdef01", "019d891c"},
		{"short id", "abc", "abc"},
		{"exactly 8", "12345678", "12345678"},
	}

	opts := export.MarkdownOptions{Project: "proj"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &model.Memory{
				ID:        tt.id,
				Type:      model.TypeDecision,
				Title:     "test title",
				Content:   "body",
				CreatedAt: fixedDate,
				UpdatedAt: fixedDate,
				Importance: 0.5,
			}
			var buf strings.Builder
			if err := export.RenderType(&buf, model.TypeDecision, []*model.Memory{m}, opts); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := buf.String()
			if !strings.Contains(got, tt.expected) {
				t.Errorf("expected truncated ID %q in output:\n%s", tt.expected, got)
			}
		})
	}
}
