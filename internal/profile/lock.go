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

// LockSchemaVersion is the current schema_version RenderLock writes and the
// upper bound Lock.Validate accepts. Bump this if the TOML shape changes in a
// way that requires a reader/writer to know which version it is looking at.
// SPEC-105 bumped it from 1 to 2 when LockArtifact gained Backup/Created/
// Digest — all optional, so a v1 lock still parses and validates (DD7).
const LockSchemaVersion = 2

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

	// Backup is the absolute path of the pre-activation copy this activation
	// saved because Path already existed and did NOT belong to the previous
	// activation (SPEC-105 DD5/DD7). Empty when nothing was displaced —
	// either Path did not exist (see Created) or it belonged to the profile's
	// own previous activation and was safely overwritten.
	Backup string `toml:"backup,omitempty"`

	// Created is true when Path did not exist before this activation — the
	// state to restore on Deactivate is "the file does not exist" (SPEC-105
	// DD5/DD7), most notably for the "block" artifact's CLAUDE.md (DD14).
	Created bool `toml:"created,omitempty"`

	// Digest is the sha256 hex of the managed block's content, set only for
	// Kind == "block" (SPEC-105 DD7/DD13). It is the only way to confirm the
	// real presence of an artifact whose containing file (CLAUDE.md) may
	// exist for reasons entirely unrelated to the profile.
	Digest string `toml:"digest,omitempty"`
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
// understands. SPEC-105 DD7 widened this from strict equality to a range:
// any version from 1 up to LockSchemaVersion validates, because a v1 lock
// (predating Backup/Created/Digest) still parses correctly — its new fields
// simply come back as their zero value, which is semantically correct ("no
// backup", "existence unknown"). Only a version ABOVE LockSchemaVersion is
// rejected: that means a newer mneme wrote this lock, and this build cannot
// safely know how to undo it.
func (l Lock) Validate() error {
	if l.SchemaVersion < 1 || l.SchemaVersion > LockSchemaVersion {
		return fmt.Errorf("profile: lock: unsupported schema_version %d (expected 1..%d): %w",
			l.SchemaVersion, LockSchemaVersion, ErrInvalidLock)
	}
	return nil
}
