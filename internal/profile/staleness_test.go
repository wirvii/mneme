package profile

import (
	"testing"
	"time"
)

// TestStalenessAgainst_SameCommit verifies that identical Profile/Commit/
// ActivatedAt values are never reported as stale.
func TestStalenessAgainst_SameCommit(t *testing.T) {
	activatedAt := time.Date(2026, 7, 16, 23, 0, 0, 0, time.UTC)
	l := Lock{Profile: "chatea-pro", Commit: "abc123", Ref: "v3", ActivatedAt: activatedAt}
	cached := Snapshot{Profile: "chatea-pro", Commit: "abc123", ActivatedAt: activatedAt}

	stale, msg := l.StalenessAgainst(cached)
	if stale {
		t.Errorf("expected not stale, got stale=true msg=%q", msg)
	}
	if msg != "" {
		t.Errorf("expected empty msg when not stale, got %q", msg)
	}
}

// TestStalenessAgainst_DifferentProfileCommitOrTime verifies that a
// difference in Profile, Commit, or ActivatedAt each independently produces
// stale=true with a non-empty message.
func TestStalenessAgainst_DifferentProfileCommitOrTime(t *testing.T) {
	base := time.Date(2026, 7, 16, 23, 0, 0, 0, time.UTC)
	cached := Snapshot{Profile: "chatea-pro", Commit: "abc123", ActivatedAt: base}

	cases := []struct {
		name string
		lock Lock
	}{
		{"different profile", Lock{Profile: "other", Commit: "abc123", Ref: "v1", ActivatedAt: base}},
		{"different commit", Lock{Profile: "chatea-pro", Commit: "def456", Ref: "v1", ActivatedAt: base}},
		{"different activated_at", Lock{Profile: "chatea-pro", Commit: "abc123", Ref: "v1", ActivatedAt: base.Add(time.Hour)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stale, msg := tc.lock.StalenessAgainst(cached)
			if !stale {
				t.Error("expected stale=true")
			}
			if msg == "" {
				t.Error("expected a non-empty message when stale")
			}
		})
	}
}
