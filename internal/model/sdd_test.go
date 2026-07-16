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

// TestSpecStatusCanTransitionTo verifies all valid and invalid transitions in
// the state machine for both lanes. This table is the authoritative record of
// the allowed moves.
func TestSpecStatusCanTransitionTo(t *testing.T) {
	tests := []struct {
		from  SpecStatus
		to    SpecStatus
		lane  Lane
		valid bool
	}{
		// Standard lane — valid forward path.
		{SpecStatusDraft, SpecStatusSpeccing, LaneStandard, true},
		{SpecStatusSpeccing, SpecStatusSpecced, LaneStandard, true},
		{SpecStatusSpeccing, SpecStatusNeedsGrill, LaneStandard, true},
		{SpecStatusNeedsGrill, SpecStatusSpeccing, LaneStandard, true},
		{SpecStatusSpecced, SpecStatusPlanning, LaneStandard, true},
		{SpecStatusPlanning, SpecStatusPlanned, LaneStandard, true},
		{SpecStatusPlanned, SpecStatusImplementing, LaneStandard, true},
		{SpecStatusImplementing, SpecStatusQA, LaneStandard, true},
		{SpecStatusImplementing, SpecStatusNeedsGrill, LaneStandard, true},
		{SpecStatusQA, SpecStatusDone, LaneStandard, true},
		{SpecStatusQA, SpecStatusImplementing, LaneStandard, true},
		{SpecStatusQA, SpecStatusNeedsGrill, LaneStandard, true},

		// Standard lane — invalid transitions (skipping states).
		{SpecStatusDraft, SpecStatusDone, LaneStandard, false},
		{SpecStatusDraft, SpecStatusImplementing, LaneStandard, false},
		{SpecStatusDraft, SpecStatusPlanned, LaneStandard, false},
		{SpecStatusSpeccing, SpecStatusImplementing, LaneStandard, false},
		{SpecStatusSpeccing, SpecStatusDone, LaneStandard, false},
		{SpecStatusSpeccing, SpecStatusPlanning, LaneStandard, false},
		{SpecStatusNeedsGrill, SpecStatusDone, LaneStandard, false},
		{SpecStatusNeedsGrill, SpecStatusImplementing, LaneStandard, false},
		{SpecStatusSpecced, SpecStatusDone, LaneStandard, false},
		{SpecStatusSpecced, SpecStatusImplementing, LaneStandard, false},
		{SpecStatusPlanning, SpecStatusDone, LaneStandard, false},
		{SpecStatusPlanned, SpecStatusDone, LaneStandard, false},
		{SpecStatusQA, SpecStatusSpeccing, LaneStandard, false},
		{SpecStatusQA, SpecStatusDraft, LaneStandard, false},

		// Standard lane — from terminal state: everything invalid except
		// SPEC-087 D6's one reasoned exception, done -> implementing
		// (spec_reject only — spec_advance stays impossible from done, see
		// nextForwardStatusForLane, which has no "done" key at all).
		{SpecStatusDone, SpecStatusDraft, LaneStandard, false},
		{SpecStatusDone, SpecStatusSpeccing, LaneStandard, false},
		{SpecStatusDone, SpecStatusImplementing, LaneStandard, true},
		{SpecStatusDone, SpecStatusQA, LaneStandard, false},

		// Trivial lane — valid forward path.
		{SpecStatusDraft, SpecStatusRationale, LaneTrivial, true},
		{SpecStatusRationale, SpecStatusImplementing, LaneTrivial, true},
		{SpecStatusImplementing, SpecStatusAudit, LaneTrivial, true},
		{SpecStatusImplementing, SpecStatusNeedsGrill, LaneTrivial, true},
		{SpecStatusAudit, SpecStatusDone, LaneTrivial, true},
		{SpecStatusAudit, SpecStatusImplementing, LaneTrivial, true},
		{SpecStatusNeedsGrill, SpecStatusRationale, LaneTrivial, true},

		// Trivial lane — standard states must not be reachable.
		{SpecStatusDraft, SpecStatusSpeccing, LaneTrivial, false},
		{SpecStatusDraft, SpecStatusDone, LaneTrivial, false},
		{SpecStatusRationale, SpecStatusSpeccing, LaneTrivial, false},
		{SpecStatusRationale, SpecStatusDone, LaneTrivial, false},

		// Trivial lane — SPEC-087 D6's reasoned exception mirrors standard.
		{SpecStatusDone, SpecStatusImplementing, LaneTrivial, true},
		{SpecStatusDone, SpecStatusAudit, LaneTrivial, false},
		{SpecStatusDone, SpecStatusDraft, LaneTrivial, false},

		// Lanes must not mix: trivial paths rejected for standard lane.
		{SpecStatusDraft, SpecStatusRationale, LaneStandard, false},
		{SpecStatusRationale, SpecStatusImplementing, LaneStandard, false},
	}

	for _, tc := range tests {
		name := string(tc.lane) + "/" + string(tc.from) + "->" + string(tc.to)
		t.Run(name, func(t *testing.T) {
			got := tc.from.CanTransitionTo(tc.to, tc.lane)
			if got != tc.valid {
				t.Errorf("CanTransitionTo(%q, %q) = %v, want %v", tc.to, tc.lane, got, tc.valid)
			}
		})
	}
}

// TestLaneValid verifies Lane.Valid returns true only for recognised constants.
func TestLaneValid(t *testing.T) {
	tests := []struct {
		lane  Lane
		valid bool
	}{
		{LaneTrivial, true},
		{LaneStandard, true},
		{Lane(""), false},
		{Lane("fast"), false},
		{Lane("TRIVIAL"), false},
	}
	for _, tc := range tests {
		t.Run(string(tc.lane), func(t *testing.T) {
			if got := tc.lane.Valid(); got != tc.valid {
				t.Errorf("Lane(%q).Valid() = %v, want %v", tc.lane, got, tc.valid)
			}
		})
	}
}

// TestSpecDocKindFilename pins the closed kind -> filename mapping
// (SPEC-087 D3): a caller only ever selects a kind, never a filename.
func TestSpecDocKindFilename(t *testing.T) {
	tests := []struct {
		kind     SpecDocKind
		wantName string
		wantOK   bool
	}{
		{SpecDocKindSpec, "spec.md", true},
		{SpecDocKindPlan, "plan.md", true},
		{SpecDocKindQAReport, "qa-report.md", true},
		{SpecDocKindChanges, "changes.md", true},
		{SpecDocKind("bogus"), "", false},
		{SpecDocKind(""), "", false},
	}
	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			name, ok := tc.kind.Filename()
			if name != tc.wantName || ok != tc.wantOK {
				t.Errorf("SpecDocKind(%q).Filename() = (%q, %v), want (%q, %v)",
					tc.kind, name, ok, tc.wantName, tc.wantOK)
			}
		})
	}
}
