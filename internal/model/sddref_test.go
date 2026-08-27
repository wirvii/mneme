package model

import "testing"

// TestParseSDDRefs covers the mention grammar SPEC-128 D4 settles on: bare
// BL-<n>/SPEC-<n> identifiers, captured EVEN inside backticks and fenced code
// blocks (the deliberate departure from internal/wikilink), normalized to a
// minimum-3-digit form, deduplicated in order of first appearance.
func TestParseSDDRefs(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "BL-1 and BL-001 collapse to the same mention",
			text: "See BL-1 and later BL-001 again.",
			want: []string{"BL-001"},
		},
		{
			name: "SPEC-1234 keeps its four digits",
			text: "Ref SPEC-1234 here.",
			want: []string{"SPEC-1234"},
		},
		{
			name: "captured inside backticks",
			text: "Fixed by `SPEC-125`.",
			want: []string{"SPEC-125"},
		},
		{
			name: "captured inside a fenced code block",
			text: "```\nrelates to SPEC-126\n```",
			want: []string{"SPEC-126"},
		},
		{
			name: "EPIC- prefix is not captured",
			text: "See EPIC-calidad for context.",
			want: nil,
		},
		{
			name: "SPEC- without a number is not captured",
			text: "SPEC- is not a valid reference.",
			want: nil,
		},
		{
			name: "BL- embedded in another word is not captured",
			text: "TABLE-001 is not a backlog item.",
			want: nil,
		},
		{
			name: "order of appearance is preserved",
			text: "First SPEC-002, then BL-001, then SPEC-002 again, then BL-003.",
			want: []string{"SPEC-002", "BL-001", "BL-003"},
		},
		{
			name: "empty text yields no refs",
			text: "",
			want: nil,
		},
		{
			name: "no mentions yields no refs",
			text: "Nothing to see here.",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseSDDRefs(tt.text)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseSDDRefs(%q) = %v, want %v", tt.text, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("ParseSDDRefs(%q)[%d] = %q, want %q", tt.text, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestSDDRefKind covers the pure prefix classifier used to decide which SDD
// table a normalized reference names.
func TestSDDRefKind(t *testing.T) {
	tests := []struct {
		name  string
		refID string
		want  string
	}{
		{name: "backlog prefix", refID: "BL-001", want: "backlog"},
		{name: "spec prefix", refID: "SPEC-125", want: "spec"},
		{name: "unrecognised prefix", refID: "EPIC-001", want: ""},
		{name: "empty string", refID: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SDDRefKind(tt.refID); got != tt.want {
				t.Errorf("SDDRefKind(%q) = %q, want %q", tt.refID, got, tt.want)
			}
		})
	}
}

// TestSDDRefStatus_Valid follows the same shape as Lane.Valid and
// Priority.Valid: every recognised constant is valid, and an invented value
// is rejected.
func TestSDDRefStatus_Valid(t *testing.T) {
	tests := []struct {
		name   string
		status SDDRefStatus
		want   bool
	}{
		{name: "local", status: SDDRefLocal, want: true},
		{name: "foreign", status: SDDRefForeign, want: true},
		{name: "unanchored", status: SDDRefUnanchored, want: true},
		{name: "invented value", status: SDDRefStatus("bogus"), want: false},
		{name: "empty value", status: SDDRefStatus(""), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.Valid(); got != tt.want {
				t.Errorf("SDDRefStatus(%q).Valid() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}
