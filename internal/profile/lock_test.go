package profile

import (
	"errors"
	"path/filepath"
	"strings"
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

// TestLockValidate_AcceptsV1AndV2_RejectsFuture (SPEC-105 DD7) verifies the
// widened range check: 0 (and negative) is malformed, 1 and 2 both validate
// (backward compatibility with a pre-SPEC-105 lock), and anything above
// LockSchemaVersion is rejected as written-by-a-newer-mneme.
func TestLockValidate_AcceptsV1AndV2_RejectsFuture(t *testing.T) {
	tests := []struct {
		name    string
		version int
		wantErr bool
	}{
		{"zero is malformed", 0, true},
		{"v1 validates", 1, false},
		{"v2 validates", 2, false},
		{"future version rejected", 99, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := Lock{SchemaVersion: tt.version}
			err := l.Validate()
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidLock) {
					t.Errorf("version %d: expected ErrInvalidLock, got %v", tt.version, err)
				}
				return
			}
			if err != nil {
				t.Errorf("version %d: expected no error, got %v", tt.version, err)
			}
		})
	}
}

// TestParseLock_V1RoundTripsWithZeroValuedNewFields verifies that a literal
// v1 TOML lock (predating Backup/Created/Digest) parses, validates, and that
// the three new LockArtifact fields come back at their zero value — which is
// semantically correct: "no backup was taken", "existence before activation
// is unknown", "no digest was recorded" (SPEC-105 DD7).
func TestParseLock_V1RoundTripsWithZeroValuedNewFields(t *testing.T) {
	v1TOML := `
schema_version = 1
profile = "chatea-pro"
source = "git@github.com:chateapro/mneme-profile.git"
ref = "v3"
commit = "a1b2c3d4e5f6"
activated_at = 2026-07-16T23:00:00Z

[[artifact]]
kind = "agent"
path = "/repo/.claude/agents/backend.md"

[[artifact]]
kind = "block"
path = "/repo/CLAUDE.md"
marker = "profile"

[[rule]]
id = "019f6d2a-5489-7fa4-a7e9-021dc73fe1b5"
source = "profile:chatea-pro"
`
	got, err := ParseLock([]byte(v1TOML))
	if err != nil {
		t.Fatalf("ParseLock: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	for i, a := range got.Artifacts {
		if a.Backup != "" {
			t.Errorf("Artifacts[%d].Backup: got %q, want zero value", i, a.Backup)
		}
		if a.Created {
			t.Errorf("Artifacts[%d].Created: got true, want zero value (false)", i)
		}
		if a.Digest != "" {
			t.Errorf("Artifacts[%d].Digest: got %q, want zero value", i, a.Digest)
		}
	}
}

// TestRenderLock_OmitsEmptyNewFields verifies that an artifact with no
// displacement (no Backup, Created false, no Digest) does not write those
// three keys into the rendered TOML at all — omitempty keeps a plain
// activation's lock exactly as compact as it was pre-SPEC-105.
func TestRenderLock_OmitsEmptyNewFields(t *testing.T) {
	l := Lock{
		SchemaVersion: LockSchemaVersion,
		Profile:       "chatea-pro",
		ActivatedAt:   time.Date(2026, 7, 16, 23, 0, 0, 0, time.UTC),
		Artifacts: []LockArtifact{
			{Kind: "agent", Path: "/repo/.claude/agents/backend.md"},
		},
	}

	data, err := RenderLock(l)
	if err != nil {
		t.Fatalf("RenderLock: %v", err)
	}

	for _, key := range []string{"backup", "created", "digest"} {
		if strings.Contains(string(data), key+" =") {
			t.Errorf("RenderLock output unexpectedly contains %q key:\n%s", key, data)
		}
	}
}
