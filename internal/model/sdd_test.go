package model

import "testing"

// TestBacklogStatusValid verifies the canonical set of valid backlog statuses.
func TestBacklogStatusValid(t *testing.T) {
	tests := []struct {
		status BacklogStatus
		want   bool
	}{
		{BacklogStatusRaw, true},
		{BacklogStatusRefined, true},
		{BacklogStatusPromoted, true},
		{BacklogStatusArchived, true},
		{"unknown", false},
		{"", false},
		{"Raw", false}, // case-sensitive
	}
	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			if got := tc.status.Valid(); got != tc.want {
				t.Errorf("BacklogStatus(%q).Valid() = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// TestPriorityValid verifies the canonical set of valid priorities.
func TestPriorityValid(t *testing.T) {
	tests := []struct {
		p    Priority
		want bool
	}{
		{PriorityCritical, true},
		{PriorityHigh, true},
		{PriorityMedium, true},
		{PriorityLow, true},
		{"urgent", false},
		{"", false},
		{"HIGH", false}, // case-sensitive
	}
	for _, tc := range tests {
		t.Run(string(tc.p), func(t *testing.T) {
			if got := tc.p.Valid(); got != tc.want {
				t.Errorf("Priority(%q).Valid() = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
}

// TestPriorityRank verifies that Rank returns the correct ordering and that
// higher priorities have lower rank numbers.
func TestPriorityRank(t *testing.T) {
	tests := []struct {
		p    Priority
		want int
	}{
		{PriorityCritical, 0},
		{PriorityHigh, 1},
		{PriorityMedium, 2},
		{PriorityLow, 3},
		{"unknown", 99},
	}
	for _, tc := range tests {
		t.Run(string(tc.p), func(t *testing.T) {
			if got := tc.p.Rank(); got != tc.want {
				t.Errorf("Priority(%q).Rank() = %d, want %d", tc.p, got, tc.want)
			}
		})
	}

	// Verify ordering invariant: critical < high < medium < low.
	if PriorityCritical.Rank() >= PriorityHigh.Rank() {
		t.Error("critical rank must be less than high rank")
	}
	if PriorityHigh.Rank() >= PriorityMedium.Rank() {
		t.Error("high rank must be less than medium rank")
	}
	if PriorityMedium.Rank() >= PriorityLow.Rank() {
		t.Error("medium rank must be less than low rank")
	}
}

// TestSpecStatusValid verifies the canonical set of valid spec statuses.
func TestSpecStatusValid(t *testing.T) {
	tests := []struct {
		s    SpecStatus
		want bool
	}{
		{SpecStatusDraft, true},
		{SpecStatusSpeccing, true},
		{SpecStatusNeedsGrill, true},
		{SpecStatusSpecced, true},
		{SpecStatusPlanning, true},
		{SpecStatusPlanned, true},
		{SpecStatusImplementing, true},
		{SpecStatusQA, true},
		{SpecStatusDone, true},
		{"unknown", false},
		{"", false},
		{"Draft", false}, // case-sensitive
	}
	for _, tc := range tests {
		t.Run(string(tc.s), func(t *testing.T) {
			if got := tc.s.Valid(); got != tc.want {
				t.Errorf("SpecStatus(%q).Valid() = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

// TestSpecStatusIsFinal verifies that only the done status is terminal.
func TestSpecStatusIsFinal(t *testing.T) {
	tests := []struct {
		s    SpecStatus
		want bool
	}{
		{SpecStatusDone, true},
		{SpecStatusDraft, false},
		{SpecStatusSpeccing, false},
		{SpecStatusNeedsGrill, false},
		{SpecStatusSpecced, false},
		{SpecStatusPlanning, false},
		{SpecStatusPlanned, false},
		{SpecStatusImplementing, false},
		{SpecStatusQA, false},
	}
	for _, tc := range tests {
		t.Run(string(tc.s), func(t *testing.T) {
			if got := tc.s.IsFinal(); got != tc.want {
				t.Errorf("SpecStatus(%q).IsFinal() = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

// TestSpecStatusIsActive verifies that active means in-progress (not draft, not done).
func TestSpecStatusIsActive(t *testing.T) {
	tests := []struct {
		s    SpecStatus
		want bool
	}{
		{SpecStatusDraft, false},
		{SpecStatusDone, false},
		{SpecStatusSpeccing, true},
		{SpecStatusNeedsGrill, true},
		{SpecStatusSpecced, true},
		{SpecStatusPlanning, true},
		{SpecStatusPlanned, true},
		{SpecStatusImplementing, true},
		{SpecStatusQA, true},
	}
	for _, tc := range tests {
		t.Run(string(tc.s), func(t *testing.T) {
			if got := tc.s.IsActive(); got != tc.want {
				t.Errorf("SpecStatus(%q).IsActive() = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

// TestSpecStatusCanTransitionTo verifies all valid and invalid transitions in the
// state machine. This table is the authoritative record of the allowed moves.
func TestSpecStatusCanTransitionTo(t *testing.T) {
	tests := []struct {
		from  SpecStatus
		to    SpecStatus
		valid bool
	}{
		// Valid transitions — forward path.
		{SpecStatusDraft, SpecStatusSpeccing, true},
		{SpecStatusSpeccing, SpecStatusSpecced, true},
		{SpecStatusSpeccing, SpecStatusNeedsGrill, true},
		{SpecStatusNeedsGrill, SpecStatusSpeccing, true},
		{SpecStatusSpecced, SpecStatusPlanning, true},
		{SpecStatusPlanning, SpecStatusPlanned, true},
		{SpecStatusPlanned, SpecStatusImplementing, true},
		{SpecStatusImplementing, SpecStatusQA, true},
		{SpecStatusImplementing, SpecStatusNeedsGrill, true},
		{SpecStatusQA, SpecStatusDone, true},
		{SpecStatusQA, SpecStatusImplementing, true},
		{SpecStatusQA, SpecStatusNeedsGrill, true},

		// Invalid transitions — skipping states.
		{SpecStatusDraft, SpecStatusDone, false},
		{SpecStatusDraft, SpecStatusImplementing, false},
		{SpecStatusDraft, SpecStatusPlanned, false},
		{SpecStatusSpeccing, SpecStatusImplementing, false},
		{SpecStatusSpeccing, SpecStatusDone, false},
		{SpecStatusSpeccing, SpecStatusPlanning, false},
		{SpecStatusNeedsGrill, SpecStatusDone, false},
		{SpecStatusNeedsGrill, SpecStatusImplementing, false},
		{SpecStatusSpecced, SpecStatusDone, false},
		{SpecStatusSpecced, SpecStatusImplementing, false},
		{SpecStatusPlanning, SpecStatusDone, false},
		{SpecStatusPlanned, SpecStatusDone, false},
		{SpecStatusQA, SpecStatusSpeccing, false},
		{SpecStatusQA, SpecStatusDraft, false},

		// Invalid — from terminal state.
		{SpecStatusDone, SpecStatusDraft, false},
		{SpecStatusDone, SpecStatusSpeccing, false},
		{SpecStatusDone, SpecStatusImplementing, false},
		{SpecStatusDone, SpecStatusQA, false},
	}

	for _, tc := range tests {
		name := string(tc.from) + " -> " + string(tc.to)
		t.Run(name, func(t *testing.T) {
			got := tc.from.CanTransitionTo(tc.to)
			if got != tc.valid {
				t.Errorf("CanTransitionTo() = %v, want %v", got, tc.valid)
			}
		})
	}
}
