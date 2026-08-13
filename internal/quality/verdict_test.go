package quality

import "testing"

// TestDeriveVerdict covers AC7's table: all-pass -> pass; a fail alongside
// passes -> fail; an un-acked finding with no fail -> findings; acked/
// skipped never degrade; and an empty set -> fail (a certificate that
// checked nothing is not green).
func TestDeriveVerdict(t *testing.T) {
	tests := []struct {
		name   string
		checks []CheckResult
		want   Verdict
	}{
		{
			name:   "empty checks is fail",
			checks: nil,
			want:   VerdictFail,
		},
		{
			name: "all pass",
			checks: []CheckResult{
				{Status: CheckStatusPass}, {Status: CheckStatusPass},
			},
			want: VerdictPass,
		},
		{
			name: "one fail among passes",
			checks: []CheckResult{
				{Status: CheckStatusPass}, {Status: CheckStatusFail}, {Status: CheckStatusPass},
			},
			want: VerdictFail,
		},
		{
			name: "un-acked finding, no fail",
			checks: []CheckResult{
				{Status: CheckStatusPass}, {Status: CheckStatusFinding},
			},
			want: VerdictFindings,
		},
		{
			name: "acked finding does not degrade",
			checks: []CheckResult{
				{Status: CheckStatusPass}, {Status: CheckStatusAcked},
			},
			want: VerdictPass,
		},
		{
			name: "skipped does not degrade",
			checks: []CheckResult{
				{Status: CheckStatusPass}, {Status: CheckStatusSkipped},
			},
			want: VerdictPass,
		},
		{
			name: "fail wins over finding",
			checks: []CheckResult{
				{Status: CheckStatusFinding}, {Status: CheckStatusFail},
			},
			want: VerdictFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveVerdict(tt.checks); got != tt.want {
				t.Errorf("DeriveVerdict(%+v) = %q, want %q", tt.checks, got, tt.want)
			}
		})
	}
}

// TestCertificateUsable covers D12's conjunction: each condition is checked
// in order and reports the FIRST failing reason. AC27 mutation (1) removes
// the head_sha comparison; this test is what turns red when that happens.
func TestCertificateUsable(t *testing.T) {
	tests := []struct {
		name                     string
		verdict                  Verdict
		certHeadSHA, currentHead string
		certHash, currentHash    string
		dirty                    bool
		wantUsable               bool
		wantReason               Reason
	}{
		{
			name: "usable", verdict: VerdictPass,
			certHeadSHA: "a", currentHead: "a",
			certHash: "h", currentHash: "h",
			dirty: false, wantUsable: true, wantReason: ReasonUsable,
		},
		{
			name: "not green", verdict: VerdictFindings,
			certHeadSHA: "a", currentHead: "a",
			certHash: "h", currentHash: "h",
			dirty: false, wantUsable: false, wantReason: ReasonNotGreen,
		},
		{
			name: "stale head", verdict: VerdictPass,
			certHeadSHA: "a", currentHead: "b",
			certHash: "h", currentHash: "h",
			dirty: false, wantUsable: false, wantReason: ReasonStale,
		},
		{
			name: "constitution changed", verdict: VerdictPass,
			certHeadSHA: "a", currentHead: "a",
			certHash: "h1", currentHash: "h2",
			dirty: false, wantUsable: false, wantReason: ReasonConstitutionChanged,
		},
		{
			name: "worktree dirty", verdict: VerdictPass,
			certHeadSHA: "a", currentHead: "a",
			certHash: "h", currentHash: "h",
			dirty: true, wantUsable: false, wantReason: ReasonWorktreeDirty,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usable, reason := CertificateUsable(tt.verdict, tt.certHeadSHA, tt.currentHead, tt.certHash, tt.currentHash, tt.dirty)
			if usable != tt.wantUsable {
				t.Errorf("usable = %v, want %v", usable, tt.wantUsable)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %v, want %v", reason, tt.wantReason)
			}
		})
	}
}
