package quality

import "testing"

// TestDeriveVerdict covers AC7's table (Effect always EffectBlocks here,
// the historical behaviour every row had before SPEC-137's Effect field
// existed): all-pass -> pass; a fail alongside passes -> fail; an un-acked
// finding with no fail -> findings; acked/skipped never degrade; and an
// empty set -> fail (a certificate that checked nothing is not green).
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
				{Status: CheckStatusPass, Effect: EffectBlocks}, {Status: CheckStatusPass, Effect: EffectBlocks},
			},
			want: VerdictPass,
		},
		{
			name: "one fail among passes",
			checks: []CheckResult{
				{Status: CheckStatusPass, Effect: EffectBlocks}, {Status: CheckStatusFail, Effect: EffectBlocks}, {Status: CheckStatusPass, Effect: EffectBlocks},
			},
			want: VerdictFail,
		},
		{
			name: "un-acked finding, no fail",
			checks: []CheckResult{
				{Status: CheckStatusPass, Effect: EffectBlocks}, {Status: CheckStatusFinding, Effect: EffectBlocks},
			},
			want: VerdictFindings,
		},
		{
			name: "acked finding does not degrade",
			checks: []CheckResult{
				{Status: CheckStatusPass, Effect: EffectBlocks}, {Status: CheckStatusAcked, Effect: EffectBlocks},
			},
			want: VerdictPass,
		},
		{
			name: "skipped does not degrade",
			checks: []CheckResult{
				{Status: CheckStatusPass, Effect: EffectBlocks}, {Status: CheckStatusSkipped, Effect: EffectStopped},
			},
			want: VerdictPass,
		},
		{
			name: "fail wins over finding",
			checks: []CheckResult{
				{Status: CheckStatusFinding, Effect: EffectBlocks}, {Status: CheckStatusFail, Effect: EffectBlocks},
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

// TestDeriveVerdict_MeasuresNeverCountsTowardVerdict is AC10's own test at
// the pure-function level (SPEC-137 D4/D5): a "fail" status on a row whose
// Effect is EffectMeasures must NEVER tumble the certificate — mutation and
// budget rows keep computing their real result, but that result travels in
// the certificate and report, never in the verdict.
func TestDeriveVerdict_MeasuresNeverCountsTowardVerdict(t *testing.T) {
	checks := []CheckResult{
		{Status: CheckStatusPass, Effect: EffectBlocks},
		{Status: CheckStatusFail, Effect: EffectMeasures},  // e.g. mutation/score
		{Status: CheckStatusFail, Effect: EffectMeasures},  // e.g. budget/*
	}
	if got := DeriveVerdict(checks); got != VerdictPass {
		t.Errorf("DeriveVerdict() = %q, want pass — measures-effect rows must never count", got)
	}
}

// TestDeriveVerdict_NonEmptySetWithNothingCountedIsStillPass guards D13's
// own explicit rejection of "a minimum of configuration to go green": a
// certificate whose every row is measures/absent/stopped is a REAL,
// non-empty set of checks (unlike the truly-empty-slice case, which is
// AC7's own "fail"), and must resolve to pass, not fail.
func TestDeriveVerdict_NonEmptySetWithNothingCountedIsStillPass(t *testing.T) {
	checks := []CheckResult{
		{Status: CheckStatusSkipped, Effect: EffectAbsent},
		{Status: CheckStatusSkipped, Effect: EffectAbsent},
		{Status: CheckStatusFail, Effect: EffectMeasures},
	}
	if got := DeriveVerdict(checks); got != VerdictPass {
		t.Errorf("DeriveVerdict() = %q, want pass — a non-empty set with nothing counted must not be forced to fail", got)
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
