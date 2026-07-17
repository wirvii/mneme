package profile

import (
	"fmt"
	"time"
)

// Snapshot is what ProfileService caches in memory right after its own
// Activate call completes: enough of the lock's identity to later detect
// that a DIFFERENT session (or a later activation in this same session)
// changed the on-disk lock underneath it (SPEC-092 §3.7 — the same-repo race
// two concurrent sessions can hit).
type Snapshot struct {
	// Profile is the activated profile's name at snapshot time.
	Profile string

	// Commit is the resolved SHA at snapshot time.
	Commit string

	// ActivatedAt is when the snapshotted activation completed.
	ActivatedAt time.Time
}

// StalenessAgainst compares l — the lock as read from disk right now —
// against cached, a Snapshot taken at some earlier activation. stale is true
// when Profile, Commit, or ActivatedAt differ, meaning some other activity
// (typically another session running Switch/Activate on the same repo)
// changed the workspace's active profile since cached was taken. msg is a
// human-readable Spanish explanation, empty when stale is false.
//
// StalenessAgainst is pure comparison — it never re-reads the lock itself;
// callers needing the I/O should use ProfileService.DetectStaleness, which
// wraps this with the read from LockPath.
func (l Lock) StalenessAgainst(cached Snapshot) (stale bool, msg string) {
	if l.Profile == cached.Profile && l.Commit == cached.Commit && l.ActivatedAt.Equal(cached.ActivatedAt) {
		return false, ""
	}
	return true, fmt.Sprintf(
		"el profile de este workspace cambió a %q (ref %q); reinicia la sesión para sincronizar",
		l.Profile, l.Ref,
	)
}
