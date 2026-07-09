package vault

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestFrontmatter_FullMemory(t *testing.T) {
	m := &model.Memory{
		ID:            "019ddc45-a39b-76da-9ab7-c4546f962418",
		Type:          model.TypeArchitecture,
		Scope:         model.ScopeProject,
		Title:         "Memory model — types and scopes",
		TopicKey:      "architecture/memory-model",
		Project:       "wirvii/mneme",
		Importance:    0.9,
		Confidence:    0.8,
		DecayRate:     0.005,
		CreatedAt:     mustParseTime("2026-04-30T02:44:04Z"),
		UpdatedAt:     mustParseTime("2026-04-30T20:41:14Z"),
		RevisionCount: 1,
		CreatedBy:     "claude-code",
		Files:         []string{"internal/model/memory.go", "README.md"},
	}

	fm := FromMemory(m)
	var buf bytes.Buffer
	n, err := fm.WriteTo(&buf)
	if err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}
	if n <= 0 {
		t.Error("WriteTo returned n <= 0")
	}

	out := buf.String()

	// Must open and close with ---
	if !strings.HasPrefix(out, "---\n") {
		t.Error("frontmatter must start with ---")
	}
	if !strings.HasSuffix(out, "---\n") {
		t.Error("frontmatter must end with ---")
	}

	// Verify mandatory fields present.
	requiredFields := []string{
		"id: 019ddc45-a39b-76da-9ab7-c4546f962418",
		"type: architecture",
		"scope: project",
		`title: "Memory model`,
		"topic_key: architecture/memory-model",
		"project: wirvii/mneme",
		"importance: 0.90",
		"confidence: 0.80",
		"created_at: 2026-04-30T02:44:04Z",
		"updated_at: 2026-04-30T20:41:14Z",
		"revision_count: 1",
		"created_by: claude-code",
		"  - internal/model/memory.go",
		"  - README.md",
	}
	for _, f := range requiredFields {
		if !strings.Contains(out, f) {
			t.Errorf("frontmatter missing expected content %q\nGot:\n%s", f, out)
		}
	}

	// Verify field order: id before type before scope.
	idPos := strings.Index(out, "id:")
	typePos := strings.Index(out, "type:")
	scopePos := strings.Index(out, "scope:")
	titlePos := strings.Index(out, "title:")
	importancePos := strings.Index(out, "importance:")
	createdAtPos := strings.Index(out, "created_at:")
	updatedAtPos := strings.Index(out, "updated_at:")

	if idPos > typePos || typePos > scopePos || scopePos > titlePos {
		t.Error("field order violation: id, type, scope, title must appear in that order")
	}
	if importancePos > createdAtPos || createdAtPos > updatedAtPos {
		t.Error("field order violation: importance must precede created_at, which must precede updated_at")
	}
}

func TestFrontmatter_OmitEmpty(t *testing.T) {
	m := &model.Memory{
		ID:        "019ddc45-0000-0000-0000-000000000000",
		Type:      model.TypeDiscovery,
		Scope:     model.ScopeGlobal,
		Title:     "Simple note",
		CreatedAt: mustParseTime("2026-01-01T00:00:00Z"),
		UpdatedAt: mustParseTime("2026-01-01T00:00:00Z"),
	}

	fm := FromMemory(m)
	var buf bytes.Buffer
	if _, err := fm.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}

	out := buf.String()

	// Optional fields should be absent.
	absent := []string{"topic_key:", "project:", "created_by:", "files:", "applies_to:", "severity:", "superseded_by:", "shared:", "author:"}
	for _, f := range absent {
		if strings.Contains(out, f) {
			t.Errorf("frontmatter should not contain %q for memory with no value\nGot:\n%s", f, out)
		}
	}
}

func TestFrontmatter_RuleFields(t *testing.T) {
	m := &model.Memory{
		ID:        "019ddc45-0000-0000-0000-000000000001",
		Type:      model.TypeRule,
		Scope:     model.ScopeProject,
		Title:     "No direct DB access",
		CreatedAt: mustParseTime("2026-01-01T00:00:00Z"),
		UpdatedAt: mustParseTime("2026-01-01T00:00:00Z"),
		AppliesTo: []string{"internal/**/*.go", "tool:Edit"},
		Severity:  model.SeverityBlock,
	}

	fm := FromMemory(m)
	var buf bytes.Buffer
	if _, err := fm.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "applies_to:") {
		t.Error("rule memory should have applies_to in frontmatter")
	}
	if !strings.Contains(out, "severity: block") {
		t.Error("rule memory should have severity in frontmatter")
	}
	if !strings.Contains(out, "  - internal/**/*.go") {
		t.Error("rule memory applies_to entry missing")
	}
}

func TestFrontmatter_NonRuleDoesNotHaveRuleFields(t *testing.T) {
	// AppliesTo and Severity set on a non-rule memory should not appear in output.
	m := &model.Memory{
		ID:        "019ddc45-0000-0000-0000-000000000002",
		Type:      model.TypeDecision,
		Scope:     model.ScopeProject,
		Title:     "Decision note",
		CreatedAt: mustParseTime("2026-01-01T00:00:00Z"),
		UpdatedAt: mustParseTime("2026-01-01T00:00:00Z"),
		AppliesTo: []string{"internal/**/*.go"},
		Severity:  model.SeverityWarn,
	}

	fm := FromMemory(m)
	var buf bytes.Buffer
	if _, err := fm.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "applies_to:") {
		t.Error("non-rule memory should not have applies_to")
	}
	if strings.Contains(out, "severity:") {
		t.Error("non-rule memory should not have severity")
	}
}

func TestFrontmatter_TitleQuoting(t *testing.T) {
	// Titles with colons or dashes must be double-quoted in frontmatter.
	cases := []struct {
		title   string
		wantSub string
	}{
		{"Memory model — types and scopes", `title: "Memory model`},
		{"Tech stack: Go 1.24+, SQLite", `title: "Tech stack:`},
		{"Simple note", `title: "Simple note"`},
	}
	for _, tc := range cases {
		m := &model.Memory{
			ID:        "019ddc45-0000-0000-0000-000000000003",
			Type:      model.TypeDiscovery,
			Scope:     model.ScopeProject,
			Title:     tc.title,
			CreatedAt: mustParseTime("2026-01-01T00:00:00Z"),
			UpdatedAt: mustParseTime("2026-01-01T00:00:00Z"),
		}
		fm := FromMemory(m)
		var buf bytes.Buffer
		if _, err := fm.WriteTo(&buf); err != nil {
			t.Fatalf("WriteTo returned error for title %q: %v", tc.title, err)
		}
		out := buf.String()
		if !strings.Contains(out, tc.wantSub) {
			t.Errorf("title %q: expected %q in frontmatter\nGot:\n%s", tc.title, tc.wantSub, out)
		}
	}
}

func TestParseUpdatedAt_Found(t *testing.T) {
	header := []byte("---\nid: abc\nupdated_at: 2026-04-30T20:41:14Z\ntype: decision\n---\n")
	got, ok := parseUpdatedAt(header)
	if !ok {
		t.Fatal("parseUpdatedAt returned ok=false")
	}
	want := mustParseTime("2026-04-30T20:41:14Z")
	if !got.Equal(want) {
		t.Errorf("parseUpdatedAt got %v; want %v", got, want)
	}
}

func TestParseUpdatedAt_NotFound(t *testing.T) {
	header := []byte("---\nid: abc\ntype: decision\n---\n")
	_, ok := parseUpdatedAt(header)
	if ok {
		t.Error("parseUpdatedAt should return ok=false when updated_at is missing")
	}
}

// TestFrontmatter_SharedAuthor_RoundTrip verifies that FromMemory + WriteTo +
// parseFrontmatter round-trips the shared and author fields, and that
// shared=0 (the local/inert default, SPEC-053 D2) is omitted from the YAML so
// notes written before team-memory existed stay byte-identical.
func TestFrontmatter_SharedAuthor_RoundTrip(t *testing.T) {
	cases := []struct {
		name       string
		shared     int
		author     string
		wantOmit   bool // whether "shared:" must be absent from the YAML
		wantAuthor bool // whether "author:" must be present
	}{
		{name: "local default omitted", shared: 0, author: "", wantOmit: true, wantAuthor: false},
		{name: "auto-shared with author", shared: 1, author: "Jane Doe <jane@example.com>", wantOmit: false, wantAuthor: true},
		{name: "team-curated with author", shared: 2, author: "John Doe <john@example.com>", wantOmit: false, wantAuthor: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &model.Memory{
				ID:        "019ddc45-0000-0000-0000-000000000010",
				Type:      model.TypeDecision,
				Scope:     model.ScopeProject,
				Title:     "Shared decision",
				CreatedAt: mustParseTime("2026-01-01T00:00:00Z"),
				UpdatedAt: mustParseTime("2026-01-01T00:00:00Z"),
				Shared:    tc.shared,
				Author:    tc.author,
			}

			fm := FromMemory(m)
			var buf bytes.Buffer
			if _, err := fm.WriteTo(&buf); err != nil {
				t.Fatalf("WriteTo returned error: %v", err)
			}
			out := buf.String()

			if tc.wantOmit && strings.Contains(out, "shared:") {
				t.Errorf("shared=0 should be omitted from frontmatter\nGot:\n%s", out)
			}
			if !tc.wantOmit && !strings.Contains(out, "shared: "+itoa(tc.shared)) {
				t.Errorf("expected shared: %d in frontmatter\nGot:\n%s", tc.shared, out)
			}
			if tc.wantAuthor && !strings.Contains(out, "author: "+tc.author) {
				t.Errorf("expected author: %s in frontmatter\nGot:\n%s", tc.author, out)
			}
			if !tc.wantAuthor && strings.Contains(out, "author:") {
				t.Errorf("author should be omitted when empty\nGot:\n%s", out)
			}

			// Round-trip: parse the written frontmatter back and verify the
			// values match what was written (parseFrontmatter lives in reader.go).
			parsed, _, err := parseFrontmatter(buf.Bytes())
			if err != nil {
				t.Fatalf("parseFrontmatter returned error: %v", err)
			}
			if parsed.Shared != tc.shared {
				t.Errorf("round-trip Shared: got %d, want %d", parsed.Shared, tc.shared)
			}
			if parsed.Author != tc.author {
				t.Errorf("round-trip Author: got %q, want %q", parsed.Author, tc.author)
			}
		})
	}
}

// itoa is a tiny local helper avoiding an extra strconv import just for this
// test's string-building.
func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

func TestFrontmatter_SupersededBy(t *testing.T) {
	m := &model.Memory{
		ID:           "019ddc45-0000-0000-0000-000000000004",
		Type:         model.TypeDecision,
		Scope:        model.ScopeProject,
		Title:        "Old decision",
		CreatedAt:    mustParseTime("2026-01-01T00:00:00Z"),
		UpdatedAt:    mustParseTime("2026-01-01T00:00:00Z"),
		SupersededBy: "019ddc45-0000-0000-0000-000000000099",
	}
	fm := FromMemory(m)
	var buf bytes.Buffer
	if _, err := fm.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "superseded_by: 019ddc45-0000-0000-0000-000000000099") {
		t.Errorf("superseded_by not found in frontmatter\nGot:\n%s", out)
	}
}
