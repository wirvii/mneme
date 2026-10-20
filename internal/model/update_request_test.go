package model

import "testing"

// TestUpdateRequest_HasFieldChanges covers SPEC-166 D5: each of the eight
// content fields counts as an edit; relation lists and SDDRefs do not.
func TestUpdateRequest_HasFieldChanges(t *testing.T) {
	str := "x"
	typ := TypeDecision
	num := 0.5
	files := []string{"a.go"}
	applies := []string{"**"}
	sev := SeverityWarn
	refs := []SDDRef{}

	tests := []struct {
		name string
		req  UpdateRequest
		want bool
	}{
		{"empty", UpdateRequest{}, false},
		{"only relations", UpdateRequest{Supersedes: []string{"a"}, ConflictsWith: []string{"b"}, UnrelatedTo: []string{"c"}}, false},
		{"only sdd refs", UpdateRequest{SDDRefs: &refs}, false},
		{"title", UpdateRequest{Title: &str}, true},
		{"content", UpdateRequest{Content: &str}, true},
		{"type", UpdateRequest{Type: &typ}, true},
		{"importance", UpdateRequest{Importance: &num}, true},
		{"confidence", UpdateRequest{Confidence: &num}, true},
		{"files", UpdateRequest{Files: &files}, true},
		{"applies_to", UpdateRequest{AppliesTo: &applies}, true},
		{"severity", UpdateRequest{Severity: &sev}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.req.HasFieldChanges(); got != tt.want {
				t.Errorf("HasFieldChanges() = %v, want %v", got, tt.want)
			}
		})
	}
}
