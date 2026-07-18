package service

import (
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestRefinementAdvisory verifies the lane-gating behaviour of
// RefinementAdvisory (SPEC-103 AC1): standard produces a non-empty advisory
// mentioning grill-me and explicitly negating brainstorming; every other lane
// value (including trivial and the empty string) produces "".
func TestRefinementAdvisory(t *testing.T) {
	tests := []struct {
		name string
		lane model.Lane
		want string
	}{
		{"trivial returns empty", model.LaneTrivial, ""},
		{"empty lane returns empty", model.Lane(""), ""},
		{"unknown lane returns empty", model.Lane("otro"), ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RefinementAdvisory(tc.lane)
			if got != tc.want {
				t.Errorf("RefinementAdvisory(%q) = %q, want %q", tc.lane, got, tc.want)
			}
		})
	}

	t.Run("standard mentions grill-me and negates brainstorming", func(t *testing.T) {
		got := RefinementAdvisory(model.LaneStandard)
		if got == "" {
			t.Fatal("RefinementAdvisory(LaneStandard) = \"\", want non-empty advisory")
		}
		if !strings.Contains(got, "grill-me") {
			t.Errorf("advisory does not mention grill-me: %q", got)
		}
		if !strings.Contains(got, "NOT use") || !strings.Contains(got, "brainstorming") {
			t.Errorf("advisory does not negate brainstorming: %q", got)
		}
	})
}
