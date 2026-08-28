package model

import (
	"encoding/json"
	"testing"
	"time"
)

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
		{SpecDocKindCriteria, "criteria.toml", true},
		{SpecDocKindBudget, "budget.toml", true},
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

// TestFreezeJSON_AdditiveContract is SPEC-126 plan.md paso 1's closure: the
// freeze fields on SpecStatusResponse/SpecListResponse must stay ABSENT from
// the wire when there is nothing to report (DD6's additive contract), and
// present with the exact key set when there is. A missing omitempty here
// would silently break every existing spec_status/spec_list consumer that
// has never seen a "frozen" key before.
func TestFreezeJSON_AdditiveContract(t *testing.T) {
	tests := []struct {
		name     string
		v        any
		wantKeys []string
	}{
		{
			name:     "SpecStatusResponse zero value has no frozen key",
			v:        SpecStatusResponse{},
			wantKeys: []string{"spec", "history", "pushbacks"},
		},
		{
			name:     "SpecListResponse zero value has no frozen key",
			v:        SpecListResponse{},
			wantKeys: []string{"specs", "total"},
		},
		{
			name:     "SpecListResponse with an empty (non-nil) Frozen map has no frozen key",
			v:        SpecListResponse{Frozen: map[string]SpecFreeze{}},
			wantKeys: []string{"specs", "total"},
		},
		{
			name: "SpecFreeze archived carries state, backlog_id, reason",
			v: SpecFreeze{
				State:     SpecFreezeArchived,
				BacklogID: "BL-1",
				Reason:    "x",
			},
			wantKeys: []string{"state", "backlog_id", "reason"},
		},
		{
			name: "SpecFreeze missing carries state, backlog_id but no reason",
			v: SpecFreeze{
				State:     SpecFreezeMissing,
				BacklogID: "BL-1",
			},
			wantKeys: []string{"state", "backlog_id"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("json.Unmarshal: %v", err)
			}
			if len(decoded) != len(tc.wantKeys) {
				t.Errorf("key count: got %d (%v), want %d (%v)", len(decoded), keysOf(decoded), len(tc.wantKeys), tc.wantKeys)
			}
			for _, k := range tc.wantKeys {
				if _, ok := decoded[k]; !ok {
					t.Errorf("missing expected key %q in %s", k, raw)
				}
			}
		})
	}
}

// keysOf returns the keys of a decoded JSON object, for readable test
// failure messages in TestFreezeJSON_AdditiveContract.
func keysOf(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestPreviousID_StringRoundTrip verifies D44's literal wire format
// round-trips through ParsePreviousID exactly — the format sddfile persists
// unmodified in a record's previous_ids list.
func TestPreviousID_StringRoundTrip(t *testing.T) {
	at, err := time.Parse(time.RFC3339Nano, "2026-08-28T10:00:00Z")
	if err != nil {
		t.Fatalf("parse fixture time: %v", err)
	}
	p := PreviousID{ID: "BL-050", Origin: "local", Reason: "enable-collision", At: at}

	line := p.String()
	const want = "BL-050 origin=local reason=enable-collision at=2026-08-28T10:00:00Z"
	if line != want {
		t.Fatalf("String() = %q, want %q", line, want)
	}

	got, ok := ParsePreviousID(line)
	if !ok {
		t.Fatalf("ParsePreviousID(%q) failed to parse", line)
	}
	if got != p {
		t.Errorf("ParsePreviousID round trip = %+v, want %+v", got, p)
	}
}

// TestParsePreviousID_RejectsMalformed verifies ParsePreviousID discards
// (ok=false), rather than partially populates, anything that does not
// match the exact four-field shape — the same tolerance-by-rejection
// posture vault.ParseSDDRefLines uses for hand-edited content.
func TestParsePreviousID_RejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"BL-050",
		"BL-050 origin=local",
		"BL-050 origin=local reason=enable-collision at=not-a-time",
		"BL-050 origin=local reason=enable-collision unknown=x",
		"BL-050 origin=local reason=enable-collision at=2026-08-28T10:00:00Z extra=y",
	}
	for _, c := range cases {
		if _, ok := ParsePreviousID(c); ok {
			t.Errorf("ParsePreviousID(%q) unexpectedly succeeded", c)
		}
	}
}
