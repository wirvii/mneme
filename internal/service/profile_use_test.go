package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
	"github.com/wirvii/mneme/internal/service"
)

// newUseTestEnv builds a fixture profile store directory containing one
// profile named "acme" whose checkout is a REAL local git repository (git
// init + commit + tag v1, no network) — unlike newActivationTestEnv's plain
// files, Use's PinFromStore round-trip requires actual git state (an
// "origin" remote, a checked-out tag) to reconstruct a pin from. Every path
// is a t.TempDir() the test owns (SPEC-085/SPEC-093 §5.4 isolation —
// nothing here ever resolves HOME).
func newUseTestEnv(t *testing.T) (svc *service.ProfileService, mem *service.MemoryService, repoRoot, configPath string) {
	t.Helper()

	profilesDir := t.TempDir()
	repoRoot = t.TempDir()
	skillsDir := t.TempDir()
	configPath = filepath.Join(t.TempDir(), "config.toml")

	profileDir := filepath.Join(profilesDir, "acme")
	writeProfileFile(t, filepath.Join(profileDir, profile.ManifestFileName), "name=\"acme\"\nversion=\"1.0.0\"\n")
	writeProfileFile(t, filepath.Join(profileDir, "agents", "backend.md"), "---\nname: backend\ntools: Read\n---\n\nCapa-1 backend content.\n")

	mustRunGitProfile(t, profileDir, "init", "-q")
	mustRunGitProfile(t, profileDir, "config", "user.name", "mneme-test")
	mustRunGitProfile(t, profileDir, "config", "user.email", "mneme-test@example.com")
	mustRunGitProfile(t, profileDir, "add", ".")
	mustRunGitProfile(t, profileDir, "commit", "-q", "-m", "initial commit")
	mustRunGitProfile(t, profileDir, "tag", "v1")
	mustRunGitProfile(t, profileDir, "remote", "add", "origin", "https://example.com/acme-profile.git")

	mem = newTestService(t)
	sub := service.NewSubagentService(mem)

	svc = service.NewProfileService(profilesDir, false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
		service.WithProfileSkillsDir(skillsDir),
		service.WithProfileConfigPath(configPath),
	)
	return svc, mem, repoRoot, configPath
}

// TestUse_WritesPinAndMaterializes covers AC3's happy path: an installed
// profile, activated for a repo with no prior pin, ends up with a pin
// reconstructed from the store's checkout AND a materialized agent file.
func TestUse_WritesPinAndMaterializes(t *testing.T) {
	svc, _, repoRoot, _ := newUseTestEnv(t)
	ctx := context.Background()

	res, err := svc.Use(ctx, repoRoot, "acme")
	if err != nil {
		t.Fatalf("Use: unexpected error: %v", err)
	}
	if res.Name != "acme" {
		t.Errorf("Name = %q, want %q", res.Name, "acme")
	}
	if res.Source != "https://example.com/acme-profile.git" {
		t.Errorf("Source = %q, want the origin remote", res.Source)
	}
	if res.Ref != "v1" {
		t.Errorf("Ref = %q, want %q (exact tag)", res.Ref, "v1")
	}
	if !res.Materialized {
		t.Error("Materialized = false, want true")
	}

	// Pin actually written to the repo root.
	pin, err := profile.ParsePinFile(filepath.Join(repoRoot, profile.PinFileName))
	if err != nil {
		t.Fatalf("ParsePinFile: %v", err)
	}
	if pin.Name != "acme" || pin.Source != res.Source || pin.Ref != "v1" {
		t.Errorf("written pin = %+v, want name=acme source=%s ref=v1", pin, res.Source)
	}

	// Materialization actually happened (agent file present).
	agentPath := filepath.Join(repoRoot, ".claude", "agents", "backend.md")
	if _, err := os.Stat(agentPath); err != nil {
		t.Errorf("expected materialized agent file at %s: %v", agentPath, err)
	}
}

// TestUse_PreservesExistingScaffold covers AC1/R5 through the Use path: a
// preexisting pin's Scaffold field must survive being overwritten by "use".
func TestUse_PreservesExistingScaffold(t *testing.T) {
	svc, _, repoRoot, _ := newUseTestEnv(t)
	ctx := context.Background()

	existing := &profile.Pin{Name: "old-profile", Scaffold: "saas-multitenant"}
	if err := profile.WritePin(repoRoot, existing); err != nil {
		t.Fatalf("seed WritePin: %v", err)
	}

	if _, err := svc.Use(ctx, repoRoot, "acme"); err != nil {
		t.Fatalf("Use: unexpected error: %v", err)
	}

	pin, err := profile.ParsePinFile(filepath.Join(repoRoot, profile.PinFileName))
	if err != nil {
		t.Fatalf("ParsePinFile: %v", err)
	}
	if pin.Name != "acme" || pin.Scaffold != "saas-multitenant" {
		t.Errorf("pin = %+v, want name=acme with scaffold preserved", pin)
	}
}

// TestUse_NotInstalled covers AC3's error path: Use never clones — a
// not-installed profile must fail with model.ErrProfileNotFound, writing no
// pin at all.
func TestUse_NotInstalled(t *testing.T) {
	svc, _, repoRoot, _ := newUseTestEnv(t)
	ctx := context.Background()

	if _, err := svc.Use(ctx, repoRoot, "nonexistent"); !errors.Is(err, model.ErrProfileNotFound) {
		t.Errorf("Use: err = %v, want model.ErrProfileNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, profile.PinFileName)); !os.IsNotExist(err) {
		t.Errorf("expected no pin written, stat err = %v", err)
	}
}

// TestUse_RequiresProjectRootAndName covers the argument-validation guards.
func TestUse_RequiresProjectRootAndName(t *testing.T) {
	svc, _, repoRoot, _ := newUseTestEnv(t)
	ctx := context.Background()

	if _, err := svc.Use(ctx, "", "acme"); err == nil {
		t.Error("Use: expected error for empty project root")
	}
	if _, err := svc.Use(ctx, repoRoot, ""); err == nil {
		t.Error("Use: expected error for empty name")
	}
}

// TestUse_NotConfigured covers Activate's own ErrProfileServiceNotConfigured
// guard, reached transitively through Use when mem/sub are not wired.
func TestUse_NotConfigured(t *testing.T) {
	svc := service.NewProfileService(t.TempDir(), false)
	if _, err := svc.Use(context.Background(), t.TempDir(), "acme"); err == nil {
		t.Error("Use: expected error when ProfileService has no mem/sub wired")
	}
}

// TestResolveCommit covers the standalone helper the SessionStart
// integration uses.
func TestResolveCommit(t *testing.T) {
	svc, _, _, _ := newUseTestEnv(t)

	commit, err := svc.ResolveCommit("acme")
	if err != nil {
		t.Fatalf("ResolveCommit: unexpected error: %v", err)
	}
	if commit == "" {
		t.Error("ResolveCommit: expected a non-empty commit SHA")
	}
}

func TestResolveCommit_NotInstalled(t *testing.T) {
	svc, _, _, _ := newUseTestEnv(t)
	if _, err := svc.ResolveCommit("nonexistent"); !errors.Is(err, model.ErrProfileNotFound) {
		t.Errorf("ResolveCommit: err = %v, want model.ErrProfileNotFound", err)
	}
}

// TestSetDefault_ClearDefault_Default_RoundTrip covers AC4/AC5's happy path
// end-to-end through the service layer.
func TestSetDefault_ClearDefault_Default_RoundTrip(t *testing.T) {
	svc, _, _, _ := newUseTestEnv(t)

	res, err := svc.SetDefault("acme")
	if err != nil {
		t.Fatalf("SetDefault: unexpected error: %v", err)
	}
	if res.Default != "acme" {
		t.Errorf("SetDefault result = %+v, want Default=acme", res)
	}

	got, err := svc.Default()
	if err != nil {
		t.Fatalf("Default: unexpected error: %v", err)
	}
	if got.Default != "acme" {
		t.Errorf("Default() = %+v, want Default=acme", got)
	}

	cleared, err := svc.ClearDefault()
	if err != nil {
		t.Fatalf("ClearDefault: unexpected error: %v", err)
	}
	if cleared.Default != "" {
		t.Errorf("ClearDefault result = %+v, want empty Default", cleared)
	}

	got, err = svc.Default()
	if err != nil {
		t.Fatalf("Default (after clear): unexpected error: %v", err)
	}
	if got.Default != "" {
		t.Errorf("Default() after clear = %+v, want empty", got)
	}
}

// TestSetDefault_EmptyNameClears covers the "name==''" alias for --clear.
func TestSetDefault_EmptyNameClears(t *testing.T) {
	svc, _, _, _ := newUseTestEnv(t)

	if _, err := svc.SetDefault("acme"); err != nil {
		t.Fatalf("SetDefault: unexpected error: %v", err)
	}
	res, err := svc.SetDefault("")
	if err != nil {
		t.Fatalf("SetDefault(\"\"): unexpected error: %v", err)
	}
	if res.Default != "" {
		t.Errorf("SetDefault(\"\") result = %+v, want empty Default", res)
	}
}

// TestSetDefault_NotInstalled covers AC4's fail-fast guard (design decision
// A1): a default naming a profile that is not in the store must error,
// never silently persist.
func TestSetDefault_NotInstalled(t *testing.T) {
	svc, _, _, _ := newUseTestEnv(t)
	if _, err := svc.SetDefault("nonexistent"); !errors.Is(err, model.ErrProfileNotFound) {
		t.Errorf("SetDefault: err = %v, want model.ErrProfileNotFound", err)
	}
}

// TestProfileConfigMethods_NotConfigured covers the ErrProfileServiceNotConfigured
// guard shared by SetDefault/ClearDefault/Default/ResolveActive when
// configPath was never wired.
func TestProfileConfigMethods_NotConfigured(t *testing.T) {
	svc := service.NewProfileService(t.TempDir(), false)

	if _, err := svc.SetDefault("acme"); !errors.Is(err, model.ErrProfileServiceNotConfigured) {
		t.Errorf("SetDefault: err = %v, want ErrProfileServiceNotConfigured", err)
	}
	if _, err := svc.ClearDefault(); !errors.Is(err, model.ErrProfileServiceNotConfigured) {
		t.Errorf("ClearDefault: err = %v, want ErrProfileServiceNotConfigured", err)
	}
	if _, err := svc.Default(); !errors.Is(err, model.ErrProfileServiceNotConfigured) {
		t.Errorf("Default: err = %v, want ErrProfileServiceNotConfigured", err)
	}
	if _, err := svc.ResolveActive(t.TempDir()); !errors.Is(err, model.ErrProfileServiceNotConfigured) {
		t.Errorf("ResolveActive: err = %v, want ErrProfileServiceNotConfigured", err)
	}
}

// TestUse_WritePinFailure covers the WritePin error branch: a projectRoot
// that is actually a FILE (not a directory) makes WritePin's temp-file write
// fail deterministically, without any error injection framework.
func TestUse_WritePinFailure(t *testing.T) {
	svc, _, _, _ := newUseTestEnv(t)
	ctx := context.Background()

	notADir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if _, err := svc.Use(ctx, notADir, "acme"); err == nil {
		t.Error("Use: expected error when projectRoot is not a directory")
	}
}

// TestUse_ActivateFailure covers the Activate error branch reached after
// WritePin succeeds: pre-creating <root>/.mneme as a regular FILE makes
// writeLock's os.MkdirAll fail deterministically (mirrors
// TestMaybeActivateProfile_ActivationFailure_FailsOpen's fault injection).
func TestUse_ActivateFailure(t *testing.T) {
	svc, _, repoRoot, _ := newUseTestEnv(t)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(repoRoot, ".mneme"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed conflicting .mneme file: %v", err)
	}

	if _, err := svc.Use(ctx, repoRoot, "acme"); err == nil {
		t.Error("Use: expected error when Activate's lock write fails")
	}

	// The pin IS written before Activate runs — Use does not roll it back.
	if _, err := os.Stat(filepath.Join(repoRoot, profile.PinFileName)); err != nil {
		t.Errorf("expected pin to have been written despite Activate failing: %v", err)
	}
}

// TestSetDefault_InvalidName covers the safe-slug validation branch (an
// unsafe name must never reach the filesystem).
func TestSetDefault_InvalidName(t *testing.T) {
	svc, _, _, _ := newUseTestEnv(t)
	if _, err := svc.SetDefault("../evil"); err == nil {
		t.Error("SetDefault: expected error for an unsafe name")
	}
}

// newUnreadableConfigPathSvc builds a ProfileService whose configPath points
// at a DIRECTORY rather than a file — every config.Load/config.SetProfilesDefault
// call against it fails with a "read: is a directory" error, exercising the
// error-wrapping branches of SetDefault/ClearDefault/Default/ResolveActive
// without any error-injection framework.
func newUnreadableConfigPathSvc(t *testing.T) *service.ProfileService {
	t.Helper()
	profilesDir := t.TempDir()
	unreadableConfigPath := t.TempDir() // a directory, not a file

	return service.NewProfileService(profilesDir, false,
		service.WithProfileConfigPath(unreadableConfigPath),
	)
}

func TestSetDefault_ConfigWriteFailure(t *testing.T) {
	// SetDefault's stat-in-store check runs BEFORE the config write, so the
	// profile named "acme" must resolve in this service's own store first.
	profilesDir := t.TempDir()
	source := newFixtureRepoForProfileUse(t, "acme")
	st := service.NewProfileService(profilesDir, false)
	if _, err := st.Add(source, "", "v1", false); err != nil {
		t.Fatalf("Add: %v", err)
	}

	broken := service.NewProfileService(profilesDir, false,
		service.WithProfileConfigPath(t.TempDir()), // a directory, not a file
	)
	if _, err := broken.SetDefault("acme"); err == nil {
		t.Error("SetDefault: expected error when configPath is unreadable")
	}
}

func TestClearDefault_ConfigWriteFailure(t *testing.T) {
	svc := newUnreadableConfigPathSvc(t)
	if _, err := svc.ClearDefault(); err == nil {
		t.Error("ClearDefault: expected error when configPath is unreadable")
	}
}

func TestDefault_ConfigLoadFailure(t *testing.T) {
	svc := newUnreadableConfigPathSvc(t)
	if _, err := svc.Default(); err == nil {
		t.Error("Default: expected error when configPath is unreadable")
	}
}

func TestResolveActive_ConfigLoadFailure(t *testing.T) {
	svc := newUnreadableConfigPathSvc(t)
	if _, err := svc.ResolveActive(t.TempDir()); err == nil {
		t.Error("ResolveActive: expected error when configPath is unreadable")
	}
}

// TestResolveActive_LeafFailure covers the store.ResolveActive error branch:
// a malformed pin file makes the leaf's ResolvePin fail, which
// ResolveActive must propagate rather than swallow.
func TestResolveActive_LeafFailure(t *testing.T) {
	svc, _, repoRoot, _ := newUseTestEnv(t)

	if err := os.WriteFile(filepath.Join(repoRoot, profile.PinFileName), []byte("this is not = = valid toml"), 0o644); err != nil {
		t.Fatalf("write malformed pin: %v", err)
	}

	if _, err := svc.ResolveActive(repoRoot); err == nil {
		t.Error("ResolveActive: expected error for a malformed pin file")
	}
}

// newFixtureRepoForProfileUse creates a local git repository (t.TempDir(),
// no network) with a valid mneme-profile.toml, tagged "v1" — a minimal local
// mirror of internal/profile's own newFixtureRepo, needed here because that
// helper lives in a different package.
func newFixtureRepoForProfileUse(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	mustRunGitProfile(t, dir, "init", "-q")
	mustRunGitProfile(t, dir, "config", "user.name", "mneme-test")
	mustRunGitProfile(t, dir, "config", "user.email", "mneme-test@example.com")
	manifest := "name = \"" + name + "\"\nversion = \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, profile.ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	mustRunGitProfile(t, dir, "add", ".")
	mustRunGitProfile(t, dir, "commit", "-q", "-m", "initial commit")
	mustRunGitProfile(t, dir, "tag", "v1")
	return dir
}

// TestResolveActive_ServiceLayer_PrecedenceEndToEnd covers AC6 through the
// service layer (config load + leaf composition), complementing the leaf's
// own exhaustive table-driven tests.
func TestResolveActive_ServiceLayer_PrecedenceEndToEnd(t *testing.T) {
	svc, _, repoRoot, _ := newUseTestEnv(t)

	// No pin, no default: vanilla.
	res, err := svc.ResolveActive(repoRoot)
	if err != nil {
		t.Fatalf("ResolveActive (vanilla): unexpected error: %v", err)
	}
	if res.Source != service.ProfileSourceVanilla {
		t.Errorf("Source = %v, want ProfileSourceVanilla", res.Source)
	}

	// Set a host default: no pin still, so the default applies.
	if _, err := svc.SetDefault("acme"); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	res, err = svc.ResolveActive(repoRoot)
	if err != nil {
		t.Fatalf("ResolveActive (default): unexpected error: %v", err)
	}
	if res.Source != service.ProfileSourceGlobalDefault {
		t.Errorf("Source = %v, want ProfileSourceGlobalDefault", res.Source)
	}
	if res.Resolution.State != service.ProfilePinInstalled {
		t.Errorf("State = %v, want ProfilePinInstalled", res.Resolution.State)
	}

	// Now "use" writes a pin: it must win over the default, unconditionally.
	if _, err := svc.Use(context.Background(), repoRoot, "acme"); err != nil {
		t.Fatalf("Use: %v", err)
	}
	res, err = svc.ResolveActive(repoRoot)
	if err != nil {
		t.Fatalf("ResolveActive (pin): unexpected error: %v", err)
	}
	if res.Source != service.ProfileSourcePin {
		t.Errorf("Source = %v, want ProfileSourcePin", res.Source)
	}
}
