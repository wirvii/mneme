package profile

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// LockFileName is the filename of the activation lock, always written at
// <repoRoot>/.mneme/profile.lock. Unlike Manifest/Pin, the lock is
// machine-local and gitignored (SPEC-092 §3.3) — it is never committed and
// never travels between machines, so it is safe for it to hold absolute
// paths.
const LockFileName = "profile.lock"

// LockSchemaVersion is the current schema_version RenderLock writes and
// ParseLock/Lock.Validate expects. Bump this if the TOML shape changes in a
// way that requires a reader/writer to know which version it is looking at.
const LockSchemaVersion = 1

// ErrInvalidLock is the sentinel returned by Lock.Validate when the lock's
// schema_version is not one this build of mneme understands.
var ErrInvalidLock = errors.New("profile: invalid lock")

// LockArtifact records one file-based thing an Activate materialized, so a
// later Deactivate/Switch knows exactly what to remove — and, just as
// importantly, what NOT to remove (anything not in this list is
// hand-authored and must never be touched).
type LockArtifact struct {
	// Kind is one of "agent", "skill", "block".
	Kind string `toml:"kind"`

	// Path is the absolute filesystem path of the materialized artifact (a
	// file for "agent"/"block", a directory for "skill").
	Path string `toml:"path"`

	// Marker is the managedblock marker used for a "block" artifact (e.g.
	// "profile"). Empty for "agent"/"skill".
	Marker string `toml:"marker,omitempty"`
}

// LockRule records one memory Activate inserted via SaveProfileRule, so a
// later Deactivate/Switch can cross-check PurgeProfileRules' provenance-based
// delete against what the lock believes it materialized (SPEC-092 §3.2/§3.6:
// the id here is an audit cross-check, never the deletion key — the deletion
// key is always the Source column).
type LockRule struct {
	// ID is the memory's UUIDv7.
	ID string `toml:"id"`

	// Source is the provenance stamp the memory carries, "profile:<name>".
	Source string `toml:"source"`
}

// Lock is the parsed shape of .mneme/profile.lock: an exact, machine-local
// record of what a profile activation materialized, so a later
// switch/deactivate can undo precisely that and nothing else.
type Lock struct {
	// SchemaVersion is the lock format version. See LockSchemaVersion.
	SchemaVersion int `toml:"schema_version"`

	// Profile is the activated profile's name.
	Profile string `toml:"profile"`

	// Source is the git remote the profile was cloned from, or "" for
	// mneme's internal default profile.
	Source string `toml:"source"`

	// Ref is the tag/branch/commit checked out at activation time.
	Ref string `toml:"ref"`

	// Commit is the resolved SHA of Ref at activation time, used by
	// StalenessAgainst to detect drift.
	Commit string `toml:"commit"`

	// ActivatedAt is when this activation completed.
	ActivatedAt time.Time `toml:"activated_at"`

	// Artifacts is every file-based thing this activation materialized.
	Artifacts []LockArtifact `toml:"artifact"`

	// Rules is every memory this activation inserted via SaveProfileRule.
	Rules []LockRule `toml:"rule"`
}

// LockPath returns the absolute path of the activation lock for the project
// rooted at repoRoot: <repoRoot>/.mneme/profile.lock.
func LockPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".mneme", LockFileName)
}

// ParseLock parses raw TOML bytes into a Lock. It does not validate the
// result — call Validate() separately, mirroring ParseManifest/ParsePin.
func ParseLock(data []byte) (*Lock, error) {
	var l Lock
	if err := toml.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("profile: parse lock: %w", err)
	}
	return &l, nil
}

// RenderLock serialises l to TOML, suitable for writing to LockPath.
func RenderLock(l Lock) ([]byte, error) {
	data, err := toml.Marshal(l)
	if err != nil {
		return nil, fmt.Errorf("profile: render lock: %w", err)
	}
	return data, nil
}

// Validate checks that l carries a schema_version this build of mneme
// understands. An unrecognised version means a newer mneme wrote the lock —
// callers should surface this as a warning rather than silently trusting a
// shape they might not parse correctly.
func (l Lock) Validate() error {
	if l.SchemaVersion != LockSchemaVersion {
		return fmt.Errorf("profile: lock: unsupported schema_version %d (expected %d): %w",
			l.SchemaVersion, LockSchemaVersion, ErrInvalidLock)
	}
	return nil
}
