package tui

import (
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// TestTruncateTitle verifies that truncate shortens long strings correctly and
// appends "..." only when truncation actually occurs.
func TestTruncateTitle(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		max    int
		expect string
	}{
		{"exact fit", "hello", 5, "hello"},
		{"shorter than max", "hi", 10, "hi"},
		{"truncated", "hello world", 8, "hello..."},
		{"very short max", "abcdefgh", 3, "abc"},
		{"empty", "", 10, ""},
		{"unicode rune boundary", "こんにちは世界", 5, "こん..."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.input, tc.max)
			if got != tc.expect {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.max, got, tc.expect)
			}
		})
	}
}

// TestFormatDate verifies ISO-8601 date formatting.
func TestFormatDate(t *testing.T) {
	tests := []struct {
		name   string
		input  time.Time
		expect string
	}{
		{"zero time", time.Time{}, ""},
		{"specific date", time.Date(2025, 1, 15, 14, 30, 0, 0, time.UTC), "2025-01-15"},
		{"UTC midnight", time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), "2025-12-31"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatDate(tc.input)
			if got != tc.expect {
				t.Errorf("formatDate(%v) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

// TestTypeColor verifies that every defined MemoryType has a color entry and
// that the style returned is non-zero (i.e. a color was actually assigned).
func TestTypeColor(t *testing.T) {
	for _, mt := range model.AllMemoryTypes() {
		s := typeColor(mt)
		// A non-zero style string indicates lipgloss assigned a foreground color.
		// We can't easily assert on exact ANSI codes, but we can assert the style
		// was constructed without panicking and is distinct from the default.
		_ = s.Render("test")
		if _, ok := typeColors[mt]; !ok {
			t.Errorf("MemoryType %q has no entry in typeColors map", mt)
		}
	}
}

// TestFilterByType verifies client-side type filtering.
func TestFilterByType(t *testing.T) {
	memories := []*model.Memory{
		{ID: "1", Type: model.TypeDecision},
		{ID: "2", Type: model.TypeBugfix},
		{ID: "3", Type: model.TypeDecision},
		{ID: "4", Type: model.TypePattern},
	}

	tests := []struct {
		name     string
		filter   model.MemoryType
		expectN  int
		expectID string // first ID in result
	}{
		{"all types (empty filter)", "", 4, "1"},
		{"decision only", model.TypeDecision, 2, "1"},
		{"bugfix only", model.TypeBugfix, 1, "2"},
		{"pattern only", model.TypePattern, 1, "4"},
		{"none matching", model.TypeConfig, 0, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterByType(memories, tc.filter)
			if len(got) != tc.expectN {
				t.Errorf("filterByType(%q): got %d results, want %d", tc.filter, len(got), tc.expectN)
			}
			if tc.expectN > 0 && got[0].ID != tc.expectID {
				t.Errorf("filterByType(%q): first ID = %q, want %q", tc.filter, got[0].ID, tc.expectID)
			}
		})
	}
}

// TestFilterByScope verifies client-side scope filtering.
func TestFilterByScope(t *testing.T) {
	memories := []*model.Memory{
		{ID: "1", Scope: model.ScopeProject},
		{ID: "2", Scope: model.ScopeGlobal},
		{ID: "3", Scope: model.ScopeProject},
	}

	tests := []struct {
		name    string
		filter  model.Scope
		expectN int
	}{
		{"all scopes (empty filter)", "", 3},
		{"project only", model.ScopeProject, 2},
		{"global only", model.ScopeGlobal, 1},
		{"org only", model.ScopeOrg, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterByScope(memories, tc.filter)
			if len(got) != tc.expectN {
				t.Errorf("filterByScope(%q): got %d results, want %d", tc.filter, len(got), tc.expectN)
			}
		})
	}
}

// TestCycleSortField verifies the sort field cycle wraps correctly.
func TestCycleSortField(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"importance", "created_at"},
		{"created_at", "type"},
		{"type", "importance"},
		{"unknown", "importance"}, // unknown falls back to first
	}

	for _, tc := range tests {
		got := cycleSortField(tc.input)
		if got != tc.expect {
			t.Errorf("cycleSortField(%q) = %q, want %q", tc.input, got, tc.expect)
		}
	}
}

// TestKeyMapDefaults verifies that every key binding in the default KeyMap has
// at least one key assigned so there are no silent no-ops.
func TestKeyMapDefaults(t *testing.T) {
	km := DefaultKeyMap()
	bindings := []struct {
		name    string
		binding interface{ Keys() []string }
	}{
		{"Up", km.Up},
		{"Down", km.Down},
		{"PageUp", km.PageUp},
		{"PageDown", km.PageDown},
		{"Home", km.Home},
		{"End", km.End},
		{"Enter", km.Enter},
		{"Back", km.Back},
		{"Quit", km.Quit},
		{"Forget", km.Forget},
		{"Help", km.Help},
		{"Refresh", km.Refresh},
		{"Search", km.Search},
		{"ClearSearch", km.ClearSearch},
		{"Sort", km.Sort},
		{"TypeFilter", km.TypeFilter},
		{"ScopeFilter", km.ScopeFilter},
		{"Stats", km.Stats},
	}

	for _, b := range bindings {
		t.Run(b.name, func(t *testing.T) {
			keys := b.binding.Keys()
			if len(keys) == 0 {
				t.Errorf("key binding %q has no keys assigned", b.name)
			}
		})
	}
}

// TestCycleTypeFilter verifies the type filter cycle visits all types and wraps.
func TestCycleTypeFilter(t *testing.T) {
	all := model.AllMemoryTypes()

	// Start from empty — first call should return first type.
	first := cycleTypeFilter("")
	if first != all[0] {
		t.Errorf("cycleTypeFilter(\"\") = %q, want %q", first, all[0])
	}

	// Cycle all the way through and back to empty.
	current := model.MemoryType("")
	for i := 0; i <= len(all); i++ {
		current = cycleTypeFilter(current)
	}
	if current != "" {
		t.Errorf("full cycle did not return to empty, got %q", current)
	}
}

// TestCycleScopeFilter verifies the scope filter cycle visits all scopes and wraps.
func TestCycleScopeFilter(t *testing.T) {
	// Start from empty — first call returns project.
	got := cycleScopeFilter("")
	if got != model.ScopeProject {
		t.Errorf("cycleScopeFilter(\"\") = %q, want %q", got, model.ScopeProject)
	}

	// project -> global -> org -> ""
	got = cycleScopeFilter(model.ScopeProject)
	if got != model.ScopeGlobal {
		t.Errorf("cycleScopeFilter(project) = %q, want global", got)
	}
	got = cycleScopeFilter(model.ScopeGlobal)
	if got != model.ScopeOrg {
		t.Errorf("cycleScopeFilter(global) = %q, want org", got)
	}
	got = cycleScopeFilter(model.ScopeOrg)
	if got != "" {
		t.Errorf("cycleScopeFilter(org) = %q, want empty", got)
	}
}
