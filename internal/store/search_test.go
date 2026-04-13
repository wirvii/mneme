package store

import (
	"context"
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/model"
)

// TestFTS5Search verifies that FTS5 returns the correct memories for a query.
func TestFTS5Search(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert memories with distinct content.
	postgres := &model.Memory{
		Type:       model.TypeDecision,
		Scope:      model.ScopeProject,
		Title:      "Database choice",
		Content:    "We chose PostgreSQL because of its reliability and JSONB support.",
		Project:    "myproject",
		Importance: 0.8,
		DecayRate:  0.01,
	}
	redis := &model.Memory{
		Type:       model.TypeConfig,
		Scope:      model.ScopeProject,
		Title:      "Caching layer",
		Content:    "Redis is used for session caching and rate limiting.",
		Project:    "myproject",
		Importance: 0.5,
		DecayRate:  0.01,
	}

	if _, err := s.Create(ctx, postgres); err != nil {
		t.Fatalf("Create postgres memory: %v", err)
	}
	if _, err := s.Create(ctx, redis); err != nil {
		t.Fatalf("Create redis memory: %v", err)
	}

	results, err := s.FTS5Search(ctx, "PostgreSQL", SearchOptions{Project: "myproject"})
	if err != nil {
		t.Fatalf("FTS5Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if !strings.Contains(results[0].Content, "PostgreSQL") {
		t.Errorf("expected first result to mention PostgreSQL, got: %s", results[0].Content)
	}
}

// TestFTS5Search_Filters verifies that project, scope, and type filters are applied.
func TestFTS5Search_Filters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m1 := &model.Memory{
		Type:      model.TypeDecision,
		Scope:     model.ScopeProject,
		Title:     "Auth architecture",
		Content:   "JWT tokens are used for authentication across all services.",
		Project:   "proj-a",
		DecayRate: 0.01,
	}
	m2 := &model.Memory{
		Type:      model.TypeBugfix,
		Scope:     model.ScopeProject,
		Title:     "Auth bug fix",
		Content:   "JWT expiry was not checked correctly; fixed the middleware.",
		Project:   "proj-b",
		DecayRate: 0.01,
	}

	if _, err := s.Create(ctx, m1); err != nil {
		t.Fatalf("Create m1: %v", err)
	}
	if _, err := s.Create(ctx, m2); err != nil {
		t.Fatalf("Create m2: %v", err)
	}

	// Filter by project — should only return proj-a.
	results, err := s.FTS5Search(ctx, "JWT", SearchOptions{Project: "proj-a"})
	if err != nil {
		t.Fatalf("FTS5Search by project: %v", err)
	}
	for _, r := range results {
		if r.Project != "proj-a" {
			t.Errorf("expected only proj-a, got project=%q", r.Project)
		}
	}

	// Filter by type — should only return bugfix.
	results, err = s.FTS5Search(ctx, "JWT", SearchOptions{Type: model.TypeBugfix})
	if err != nil {
		t.Fatalf("FTS5Search by type: %v", err)
	}
	for _, r := range results {
		if r.Type != model.TypeBugfix {
			t.Errorf("expected only bugfix type, got type=%q", r.Type)
		}
	}
}

// TestFTS5Search_Preview verifies that preview is truncated to PreviewLength.
func TestFTS5Search_Preview(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := &model.Memory{
		Type:      model.TypeDiscovery,
		Scope:     model.ScopeProject,
		Title:     "Long content memory",
		Content:   strings.Repeat("This is a very long content string. ", 20),
		Project:   "test",
		DecayRate: 0.01,
	}
	if _, err := s.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const previewLen = 50
	results, err := s.FTS5Search(ctx, "long content", SearchOptions{
		Project:       "test",
		PreviewLength: previewLen,
	})
	if err != nil {
		t.Fatalf("FTS5Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	preview := results[0].Preview
	// Preview should be at most previewLen runes + "..."
	runes := []rune(preview)
	if len(runes) > previewLen+3 {
		t.Errorf("preview too long: %d runes (max %d + 3 for ellipsis)", len(runes), previewLen)
	}
	if !strings.HasSuffix(preview, "...") {
		t.Errorf("expected preview to end with '...', got: %q", preview)
	}
}

// TestBuildFTS5Query covers the query builder with a table of cases.
func TestBuildFTS5Query(t *testing.T) {
	cases := []struct {
		name  string
		input string
		// wantContains is a substring we expect in the output.
		// For stop-word-only inputs, the output equals the input.
		wantEqual    string
		wantContains string
	}{
		{
			name:         "normal query keeps meaningful tokens",
			input:        "database migration strategy",
			wantContains: "database",
		},
		{
			name:         "quoted phrase is preserved",
			input:        `"exact phrase"`,
			wantContains: `"exact phrase"`,
		},
		{
			name:      "only stop words falls back to original",
			input:     "the and or is",
			wantEqual: "the and or is",
		},
		{
			name:      "empty input returns empty",
			input:     "",
			wantEqual: "",
		},
		{
			name:         "mixed stop and meaningful",
			input:        "the architecture decision",
			wantContains: "architecture",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildFTS5Query(tc.input)
			if tc.wantEqual != "" && got != tc.wantEqual {
				t.Errorf("buildFTS5Query(%q) = %q, want %q", tc.input, got, tc.wantEqual)
			}
			if tc.wantContains != "" && !strings.Contains(got, tc.wantContains) {
				t.Errorf("buildFTS5Query(%q) = %q, want it to contain %q", tc.input, got, tc.wantContains)
			}
		})
	}
}
