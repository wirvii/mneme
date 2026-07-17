package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
	"github.com/wirvii/mneme/internal/service"
)

// defaultProfileFixture builds a small fstest.MapFS shaped like the embedded
// OSS default profile (SPEC-096 §6): a manifest, one agent, one skill, and no
// blocks/rules — exercising exactly the same LoadContentsFS parse path
// install.DefaultProfileFS() feeds in production, without this package
// needing to import internal/install (which would violate the leaf/service
// layering the leaf_test.go import-guard and TestLeafPackage_* enforce).
func defaultProfileFixture() fstest.MapFS {
	return fstest.MapFS{
		"mneme-profile.toml": &fstest.MapFile{Data: []byte("name=\"mneme-default\"\nversion=\"1.0.0\"\n")},
		"agents/backend.md":  &fstest.MapFile{Data: []byte("---\nname: backend\n---\n\nDefault capa-1 backend.\n")},
		"skills/demo-skill/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: demo-skill\npinned: false\n---\n\n# Demo skill\n"),
		},
	}
}

// newDefaultActivationTestEnv builds a ProfileService wired with
// WithDefaultProfileFS(defaultProfileFixture()), plus fresh repoRoot/skillsDir
// t.TempDir()s (SPEC-085/SPEC-092 §5.5 isolation).
func newDefaultActivationTestEnv(t *testing.T) (svc *service.ProfileService, repoRoot, skillsDir string) {
	t.Helper()

	repoRoot = t.TempDir()
	skillsDir = t.TempDir()

	mem := newTestService(t)
	sub := service.NewSubagentService(mem)

	svc = service.NewProfileService(t.TempDir(), false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
		service.WithProfileSkillsDir(skillsDir),
		service.WithDefaultProfileFS(defaultProfileFixture()),
	)
	return svc, repoRoot, skillsDir
}

// TestActivate_DefaultProfile_MaterializesFromEmbeddedFS is the AC5 happy
// path: Default:true reads contents from the injected fs.FS (never the
// host-level store), materializes the agent + skill, and writes a lock with
// profile="mneme-default", source="", and the caller-supplied synthetic
// commit.
func TestActivate_DefaultProfile_MaterializesFromEmbeddedFS(t *testing.T) {
	svc, repoRoot, skillsDir := newDefaultActivationTestEnv(t)
	ctx := context.Background()

	result, err := svc.Activate(ctx, service.ActivationInput{
		RepoRoot: repoRoot,
		Default:  true,
		Commit:   "bundled:1.28.0+1.0.0",
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if result.Profile != profile.DefaultProfileName {
		t.Errorf("ActivateResult.Profile = %q, want %q", result.Profile, profile.DefaultProfileName)
	}

	agentPath := filepath.Join(repoRoot, ".claude", "agents", "backend.md")
	agentData, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("read agent file: %v", err)
	}
	if !strings.Contains(string(agentData), "Default capa-1 backend.") {
		t.Errorf("expected default capa-1 content, got %q", string(agentData))
	}

	skillFile := filepath.Join(skillsDir, "demo-skill", "SKILL.md")
	if _, err := os.Stat(skillFile); err != nil {
		t.Errorf("expected skill file at %s: %v", skillFile, err)
	}

	lock, present, err := svc.ActiveLock(repoRoot)
	if err != nil || !present {
		t.Fatalf("ActiveLock: present=%v err=%v", present, err)
	}
	if lock.Profile != profile.DefaultProfileName {
		t.Errorf("lock.Profile = %q, want %q", lock.Profile, profile.DefaultProfileName)
	}
	if lock.Source != "" {
		t.Errorf("lock.Source = %q, want empty for the default profile", lock.Source)
	}
	if lock.Commit != "bundled:1.28.0+1.0.0" {
		t.Errorf("lock.Commit = %q, want the caller-supplied synthetic commit", lock.Commit)
	}
}

// TestActivate_DefaultProfile_NameIsIgnoredInFavorOfReservedConstant verifies
// that a caller-supplied Name is irrelevant once Default is true — the lock
// always records profile.DefaultProfileName, since a sourceless pin's own
// Name field is purely informational (SPEC-096 §6 §3.4).
func TestActivate_DefaultProfile_NameIsIgnoredInFavorOfReservedConstant(t *testing.T) {
	svc, repoRoot, _ := newDefaultActivationTestEnv(t)

	result, err := svc.Activate(context.Background(), service.ActivationInput{
		RepoRoot: repoRoot,
		Name:     "whatever-the-pin-happened-to-say",
		Default:  true,
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if result.Profile != profile.DefaultProfileName {
		t.Errorf("ActivateResult.Profile = %q, want %q", result.Profile, profile.DefaultProfileName)
	}
}

// TestActivate_DefaultProfile_ByNameAlone verifies that naming
// profile.DefaultProfileName directly (without setting Default) also routes
// through the default branch — the two conditions in
// ActivationInput.isDefaultActivation are equivalent entry points.
func TestActivate_DefaultProfile_ByNameAlone(t *testing.T) {
	svc, repoRoot, _ := newDefaultActivationTestEnv(t)

	result, err := svc.Activate(context.Background(), service.ActivationInput{
		RepoRoot: repoRoot,
		Name:     profile.DefaultProfileName,
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if result.Profile != profile.DefaultProfileName {
		t.Errorf("ActivateResult.Profile = %q, want %q", result.Profile, profile.DefaultProfileName)
	}
}

// TestActivate_DefaultProfile_ErrDefaultProfileUnavailable verifies the R4
// defensive guard: a ProfileService constructed without
// WithDefaultProfileFS rejects a default activation instead of silently
// falling through to the host-level store.
func TestActivate_DefaultProfile_ErrDefaultProfileUnavailable(t *testing.T) {
	mem := newTestService(t)
	sub := service.NewSubagentService(mem)
	svc := service.NewProfileService(t.TempDir(), false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
	)

	_, err := svc.Activate(context.Background(), service.ActivationInput{
		RepoRoot: t.TempDir(),
		Default:  true,
	})
	if err == nil {
		t.Fatal("expected an error when defaultFS is not configured")
	}
	if !strings.Contains(err.Error(), "default profile") {
		t.Errorf("expected a default-profile-unavailable error, got %v", err)
	}
}

// TestDefaultManifest_ReturnsManifest verifies that DefaultManifest parses
// the injected fs.FS's mneme-profile.toml — the accessor
// activateDefaultProfileForSession (internal/cli) uses to build the
// version-locked synthetic commit (AC9).
func TestDefaultManifest_ReturnsManifest(t *testing.T) {
	svc, _, _ := newDefaultActivationTestEnv(t)

	m, err := svc.DefaultManifest()
	if err != nil {
		t.Fatalf("DefaultManifest: %v", err)
	}
	if m.Name != "mneme-default" || m.Version != "1.0.0" {
		t.Errorf("unexpected manifest: %+v", m)
	}
}

// TestDefaultManifest_ErrDefaultProfileUnavailable verifies DefaultManifest's
// own guard when defaultFS was never wired.
func TestDefaultManifest_ErrDefaultProfileUnavailable(t *testing.T) {
	svc := service.NewProfileService(t.TempDir(), false)

	_, err := svc.DefaultManifest()
	if err == nil {
		t.Fatal("expected an error when defaultFS is not configured")
	}
}

// TestDetectStaleness_DefaultProfile_VersionChangeMarksStale is the AC9
// reproducibility test: two synthetic commits differing only in the
// "mneme-version" segment (simulating a `mneme upgrade` between sessions)
// make StalenessAgainst/DetectStaleness report stale — the mechanism a fresh
// SessionStart relies on to re-materialize the default after an upgrade.
func TestDetectStaleness_DefaultProfile_VersionChangeMarksStale(t *testing.T) {
	svc, repoRoot, _ := newDefaultActivationTestEnv(t)
	ctx := context.Background()

	result, err := svc.Activate(ctx, service.ActivationInput{
		RepoRoot: repoRoot,
		Default:  true,
		Commit:   "bundled:1.28.0+1.0.0",
	})
	if err != nil {
		t.Fatalf("Activate (first, pre-upgrade): %v", err)
	}
	cached := profile.Snapshot{Profile: result.Profile, Commit: result.Commit}

	// Simulate `mneme upgrade`: the binary's version changed, so the next
	// SessionStart builds a different synthetic commit and re-activates.
	if _, err := svc.Activate(ctx, service.ActivationInput{
		RepoRoot: repoRoot,
		Default:  true,
		Commit:   "bundled:1.29.0+1.0.0",
	}); err != nil {
		t.Fatalf("Activate (second, post-upgrade): %v", err)
	}

	stale, msg, err := svc.DetectStaleness(repoRoot, cached)
	if err != nil {
		t.Fatalf("DetectStaleness: %v", err)
	}
	if !stale {
		t.Error("expected stale=true after the synthetic commit's version segment changed")
	}
	if msg == "" {
		t.Error("expected a non-empty staleness message")
	}
}

// TestActivate_DefaultProfile_RequiresMemoryAndSubagentSeam verifies that the
// default branch shares Activate's existing seam guard (model.ErrProfileServiceNotConfigured)
// rather than bypassing it.
func TestActivate_DefaultProfile_RequiresMemoryAndSubagentSeam(t *testing.T) {
	svc := service.NewProfileService(t.TempDir(), false, service.WithDefaultProfileFS(defaultProfileFixture()))

	_, err := svc.Activate(context.Background(), service.ActivationInput{RepoRoot: t.TempDir(), Default: true})
	if err == nil {
		t.Fatal("expected an error when mem/sub are not wired")
	}
	if !errors.Is(err, model.ErrProfileServiceNotConfigured) {
		t.Errorf("expected model.ErrProfileServiceNotConfigured, got %v", err)
	}
}
