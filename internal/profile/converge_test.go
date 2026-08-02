package profile

import (
	"path/filepath"
	"testing"
	"time"
)

func baseLock() *Lock {
	return &Lock{
		SchemaVersion: LockSchemaVersion,
		Profile:       "chatea-pro",
		Commit:        "abc123",
		Artifacts: []LockArtifact{
			{Kind: LockArtifactKindAgent, Path: "/repo/.claude/agents/backend.md"},
			{Kind: LockArtifactKindBlock, Path: "/repo/CLAUDE.md", Marker: "profile", Digest: "digest-a"},
		},
		Rules: []LockRule{
			{ID: "rule-1", Source: "profile:chatea-pro"},
			{ID: "rule-2", Source: "profile:chatea-pro"},
		},
	}
}

func baseWant() Desired {
	return Desired{Profile: "chatea-pro", Commit: "abc123"}
}

func baseObservation() Observation {
	return Observation{
		PathExists:   map[string]bool{"/repo/.claude/agents/backend.md": true},
		BlockPresent: map[string]bool{"/repo/CLAUDE.md": true},
		BlockDigest:  map[string]string{"/repo/CLAUDE.md": "digest-a"},
		RuleIDs:      []string{"rule-1", "rule-2"},
	}
}

// TestConverged_HappyPath verifies that a workspace matching lock/want/obs
// exactly converges with zero divergences.
func TestConverged_HappyPath(t *testing.T) {
	ok, divs := Converged(baseLock(), baseWant(), baseObservation())
	if !ok {
		t.Fatalf("expected converged, got divergences: %+v", divs)
	}
	if len(divs) != 0 {
		t.Errorf("expected zero divergences, got %+v", divs)
	}
}

// TestConverged_Matrix covers each of DD2's six (plus 6b) conditions, one
// entry per condition in its converging and diverging variant, asserting the
// emitted Divergence.Kind.
func TestConverged_Matrix(t *testing.T) {
	tests := []struct {
		name     string
		lock     *Lock
		want     Desired
		obs      Observation
		wantOK   bool
		wantKind string
	}{
		{
			name:   "1 converges: lock present",
			lock:   baseLock(),
			want:   baseWant(),
			obs:    baseObservation(),
			wantOK: true,
		},
		{
			name:     "1 diverges: no lock",
			lock:     nil,
			want:     baseWant(),
			obs:      baseObservation(),
			wantOK:   false,
			wantKind: DivergenceNoLock,
		},
		{
			name:   "2 converges: profile matches",
			lock:   baseLock(),
			want:   Desired{Profile: "chatea-pro", Commit: "abc123"},
			obs:    baseObservation(),
			wantOK: true,
		},
		{
			name:     "2 diverges: profile differs",
			lock:     baseLock(),
			want:     Desired{Profile: "other-profile", Commit: "abc123"},
			obs:      baseObservation(),
			wantOK:   false,
			wantKind: DivergenceProfile,
		},
		{
			name:   "3 converges: commit matches",
			lock:   baseLock(),
			want:   baseWant(),
			obs:    baseObservation(),
			wantOK: true,
		},
		{
			name:     "3 diverges: commit differs",
			lock:     baseLock(),
			want:     Desired{Profile: "chatea-pro", Commit: "different-sha"},
			obs:      baseObservation(),
			wantOK:   false,
			wantKind: DivergenceCommit,
		},
		{
			name:   "4 converges: artifact path exists",
			lock:   baseLock(),
			want:   baseWant(),
			obs:    baseObservation(),
			wantOK: true,
		},
		{
			name: "4 diverges: artifact path missing",
			lock: baseLock(),
			want: baseWant(),
			obs: Observation{
				PathExists:   map[string]bool{"/repo/.claude/agents/backend.md": false},
				BlockPresent: map[string]bool{"/repo/CLAUDE.md": true},
				BlockDigest:  map[string]string{"/repo/CLAUDE.md": "digest-a"},
				RuleIDs:      []string{"rule-1", "rule-2"},
			},
			wantOK:   false,
			wantKind: DivergenceMissingArtifact,
		},
		{
			name:   "5 converges: block present and digest matches",
			lock:   baseLock(),
			want:   baseWant(),
			obs:    baseObservation(),
			wantOK: true,
		},
		{
			name: "5 diverges: block marker missing",
			lock: baseLock(),
			want: baseWant(),
			obs: Observation{
				PathExists:   map[string]bool{"/repo/.claude/agents/backend.md": true},
				BlockPresent: map[string]bool{"/repo/CLAUDE.md": false},
				BlockDigest:  map[string]string{},
				RuleIDs:      []string{"rule-1", "rule-2"},
			},
			wantOK:   false,
			wantKind: DivergenceMissingBlock,
		},
		{
			name: "5 diverges: block content edited (digest drift)",
			lock: baseLock(),
			want: baseWant(),
			obs: Observation{
				PathExists:   map[string]bool{"/repo/.claude/agents/backend.md": true},
				BlockPresent: map[string]bool{"/repo/CLAUDE.md": true},
				BlockDigest:  map[string]string{"/repo/CLAUDE.md": "digest-changed"},
				RuleIDs:      []string{"rule-1", "rule-2"},
			},
			wantOK:   false,
			wantKind: DivergenceBlockDrift,
		},
		{
			name:   "6 converges: rule id sets match",
			lock:   baseLock(),
			want:   baseWant(),
			obs:    baseObservation(),
			wantOK: true,
		},
		{
			name: "6 diverges: rule id sets differ",
			lock: baseLock(),
			want: baseWant(),
			obs: Observation{
				PathExists:   map[string]bool{"/repo/.claude/agents/backend.md": true},
				BlockPresent: map[string]bool{"/repo/CLAUDE.md": true},
				BlockDigest:  map[string]string{"/repo/CLAUDE.md": "digest-a"},
				RuleIDs:      []string{"rule-1", "rule-2", "rule-3", "rule-4", "rule-5"},
			},
			wantOK:   false,
			wantKind: DivergenceRules,
		},
		{
			name: "6b diverges: rule scan truncated",
			lock: baseLock(),
			want: baseWant(),
			obs: Observation{
				PathExists:     map[string]bool{"/repo/.claude/agents/backend.md": true},
				BlockPresent:   map[string]bool{"/repo/CLAUDE.md": true},
				BlockDigest:    map[string]string{"/repo/CLAUDE.md": "digest-a"},
				RuleIDs:        []string{"rule-1", "rule-2"},
				RulesTruncated: true,
			},
			wantOK:   false,
			wantKind: DivergenceRulesTruncated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, divs := Converged(tt.lock, tt.want, tt.obs)
			if ok != tt.wantOK {
				t.Fatalf("Converged() ok = %v, want %v (divergences: %+v)", ok, tt.wantOK, divs)
			}
			if tt.wantOK {
				return
			}
			found := false
			for _, d := range divs {
				if d.Kind == tt.wantKind {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected a divergence of kind %q, got %+v", tt.wantKind, divs)
			}
		})
	}
}

// TestConverged_UnknownArtifactKindIsDivergent (SPEC-105 fail-safe, mirrors
// DD3's RulesTruncated philosophy) verifies that a lock artifact whose Kind
// this build does not recognise is never silently treated as "still
// matches" — it must always report a divergence, forcing Reconcile to
// repair (and Deactivate's own default case to surface a clear error)
// rather than pretending an unknown artifact type is fine.
func TestConverged_UnknownArtifactKindIsDivergent(t *testing.T) {
	lock := &Lock{
		SchemaVersion: LockSchemaVersion,
		Profile:       "chatea-pro",
		Commit:        "abc123",
		Artifacts: []LockArtifact{
			{Kind: "bogus", Path: "/nonexistent"},
		},
	}
	obs := Observation{}

	ok, divs := Converged(lock, Desired{Profile: "chatea-pro", Commit: "abc123"}, obs)
	if ok {
		t.Fatal("expected divergent for an unrecognized artifact kind")
	}
	found := false
	for _, d := range divs {
		if d.Kind == DivergenceUnknownArtifact {
			found = true
		}
	}
	if !found {
		t.Errorf("expected DivergenceUnknownArtifact, got %+v", divs)
	}
}

// TestConverged_RuleSetComparisonIsSetBased verifies that duplicate ids in
// lock.Rules (the topic_key-collapse scenario of DD3) converge against a
// single matching id in obs.RuleIDs — comparing lengths would wrongly report
// divergence here.
func TestConverged_RuleSetComparisonIsSetBased(t *testing.T) {
	lock := &Lock{
		SchemaVersion: LockSchemaVersion,
		Profile:       "chatea-pro",
		Commit:        "abc123",
		Rules: []LockRule{
			{ID: "rule-1", Source: "profile:chatea-pro"},
			{ID: "rule-1", Source: "profile:chatea-pro"}, // duplicate: same topic_key collapsed on upsert
		},
	}
	obs := Observation{RuleIDs: []string{"rule-1"}}

	ok, divs := Converged(lock, Desired{Profile: "chatea-pro", Commit: "abc123"}, obs)
	if !ok {
		t.Errorf("expected converged despite duplicate lock rule ids, got divergences: %+v", divs)
	}
}

// TestConverged_TruncatedRuleScanIsDivergent verifies the fail-safe: even
// when the observed ids happen to match the lock's rule set exactly, a
// truncated scan is still reported as divergent.
func TestConverged_TruncatedRuleScanIsDivergent(t *testing.T) {
	lock := baseLock()
	obs := baseObservation()
	obs.RulesTruncated = true

	ok, divs := Converged(lock, baseWant(), obs)
	if ok {
		t.Fatal("expected divergent when RulesTruncated is true, got converged")
	}
	found := false
	for _, d := range divs {
		if d.Kind == DivergenceRulesTruncated {
			found = true
		}
	}
	if !found {
		t.Errorf("expected DivergenceRulesTruncated, got %+v", divs)
	}
}

// TestConverged_V1BlockWithoutDigestChecksPresenceOnly verifies that a block
// artifact with an empty Digest (a v1 lock, predating SPEC-105) converges as
// long as the marker is present — content drift cannot be detected without a
// recorded digest, and an empty Digest must not be misread as "digest is the
// empty string".
func TestConverged_V1BlockWithoutDigestChecksPresenceOnly(t *testing.T) {
	lock := &Lock{
		SchemaVersion: 1,
		Profile:       "chatea-pro",
		Commit:        "abc123",
		Artifacts: []LockArtifact{
			{Kind: LockArtifactKindBlock, Path: "/repo/CLAUDE.md", Marker: "profile"}, // no Digest
		},
	}
	obs := Observation{
		BlockPresent: map[string]bool{"/repo/CLAUDE.md": true},
		BlockDigest:  map[string]string{"/repo/CLAUDE.md": "whatever-it-is-now"},
	}

	ok, divs := Converged(lock, Desired{Profile: "chatea-pro", Commit: "abc123"}, obs)
	if !ok {
		t.Errorf("expected converged for v1 block artifact with marker present, got divergences: %+v", divs)
	}
}

// TestConverged_AccumulatesAllDivergences verifies Converged does not
// short-circuit: multiple simultaneous divergences are all reported.
func TestConverged_AccumulatesAllDivergences(t *testing.T) {
	lock := baseLock()
	want := Desired{Profile: "other", Commit: "different"}
	obs := Observation{
		PathExists:     map[string]bool{"/repo/.claude/agents/backend.md": false},
		BlockPresent:   map[string]bool{"/repo/CLAUDE.md": false},
		BlockDigest:    map[string]string{},
		RuleIDs:        nil,
		RulesTruncated: true,
	}

	ok, divs := Converged(lock, want, obs)
	if ok {
		t.Fatal("expected divergent")
	}

	wantKinds := map[string]bool{
		DivergenceProfile:         false,
		DivergenceCommit:          false,
		DivergenceMissingArtifact: false,
		DivergenceMissingBlock:    false,
		DivergenceRulesTruncated:  false,
	}
	for _, d := range divs {
		if _, ok := wantKinds[d.Kind]; ok {
			wantKinds[d.Kind] = true
		}
	}
	for kind, seen := range wantKinds {
		if !seen {
			t.Errorf("expected divergence kind %q to be present, got %+v", kind, divs)
		}
	}
}

// TestBlockDigest_StableAndSensitive verifies BlockDigest is deterministic
// for identical content and changes when content changes.
func TestBlockDigest_StableAndSensitive(t *testing.T) {
	a := BlockDigest("hello world")
	b := BlockDigest("hello world")
	if a != b {
		t.Errorf("BlockDigest not stable: %q != %q", a, b)
	}

	c := BlockDigest("hello world!")
	if a == c {
		t.Errorf("BlockDigest not sensitive to content change: both %q", a)
	}
}

// TestBackupDir_Deterministic verifies BackupDir is a pure function of
// repoRoot and at, formatted as documented.
func TestBackupDir_Deterministic(t *testing.T) {
	at := time.Date(2026, 8, 2, 19, 11, 3, 0, time.UTC)
	got := BackupDir("/repo", at)
	want := filepath.Join("/repo", ".mneme", "backups", "20260802T191103Z")
	if got != want {
		t.Errorf("BackupDir: got %q, want %q", got, want)
	}

	// Same inputs, called twice, must produce byte-identical output.
	got2 := BackupDir("/repo", at)
	if got != got2 {
		t.Errorf("BackupDir not deterministic: %q != %q", got, got2)
	}
}
