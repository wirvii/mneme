package profile

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestLock_RoundTrip verifies that RenderLock followed by ParseLock preserves
// every field: profile/source/ref/commit/activated_at, artifacts, and rules.
func TestLock_RoundTrip(t *testing.T) {
	activatedAt := time.Date(2026, 7, 16, 23, 0, 0, 0, time.UTC)
	l := Lock{
		SchemaVersion: LockSchemaVersion,
		Profile:       "chatea-pro",
		Source:        "git@github.com:chateapro/mneme-profile.git",
		Ref:           "v3",
		Commit:        "a1b2c3d4e5f6",
		ActivatedAt:   activatedAt,
		Artifacts: []LockArtifact{
			{Kind: "agent", Path: "/repo/.claude/agents/backend.md"},
			{Kind: "skill", Path: "/home/dev/.claude/skills/new-project"},
			{Kind: "block", Path: "/repo/CLAUDE.md", Marker: "profile"},
		},
		Rules: []LockRule{
			{ID: "019f6d2a-5489-7fa4-a7e9-021dc73fe1b5", Source: "profile:chatea-pro"},
		},
	}

	data, err := RenderLock(l)
	if err != nil {
		t.Fatalf("RenderLock: %v", err)
	}

	got, err := ParseLock(data)
	if err != nil {
		t.Fatalf("ParseLock: %v", err)
	}

	if got.SchemaVersion != l.SchemaVersion {
		t.Errorf("SchemaVersion: got %d, want %d", got.SchemaVersion, l.SchemaVersion)
	}
	if got.Profile != l.Profile || got.Source != l.Source || got.Ref != l.Ref || got.Commit != l.Commit {
		t.Errorf("identity fields mismatch: got %+v, want %+v", got, l)
	}
	if !got.ActivatedAt.Equal(l.ActivatedAt) {
		t.Errorf("ActivatedAt: got %v, want %v", got.ActivatedAt, l.ActivatedAt)
	}
	if len(got.Artifacts) != len(l.Artifacts) {
		t.Fatalf("Artifacts length: got %d, want %d", len(got.Artifacts), len(l.Artifacts))
	}
	for i := range l.Artifacts {
		if got.Artifacts[i] != l.Artifacts[i] {
			t.Errorf("Artifacts[%d]: got %+v, want %+v", i, got.Artifacts[i], l.Artifacts[i])
		}
	}
	if len(got.Rules) != 1 || got.Rules[0] != l.Rules[0] {
		t.Errorf("Rules: got %+v, want %+v", got.Rules, l.Rules)
	}
}

// TestLockFileName_LockPath verifies LockPath joins repoRoot, ".mneme", and
// LockFileName.
func TestLockFileName_LockPath(t *testing.T) {
	got := LockPath("/repo")
	want := filepath.Join("/repo", ".mneme", "profile.lock")
	if got != want {
		t.Errorf("LockPath: got %q, want %q", got, want)
	}
}

// TestParseLock_MalformedTOML verifies ParseLock rejects invalid TOML.
func TestParseLock_MalformedTOML(t *testing.T) {
	if _, err := ParseLock([]byte("not = = valid")); err == nil {
		t.Error("ParseLock: expected error for malformed TOML")
	}
}

// TestLock_Validate_UnknownSchemaVersion verifies that a lock declaring a
// schema_version this build does not recognise fails Validate.
func TestLock_Validate_UnknownSchemaVersion(t *testing.T) {
	l := Lock{SchemaVersion: 99}
	if err := l.Validate(); !errors.Is(err, ErrInvalidLock) {
		t.Errorf("expected ErrInvalidLock, got %v", err)
	}

	ok := Lock{SchemaVersion: LockSchemaVersion}
	if err := ok.Validate(); err != nil {
		t.Errorf("expected current schema_version to validate, got %v", err)
	}
}
