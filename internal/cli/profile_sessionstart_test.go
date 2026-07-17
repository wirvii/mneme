package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/gitident"
)

// runSessionStartHook chdirs into root, sets --data-dir=dataDir for the
// duration of the call, and drives runHookSessionStart directly (no cobra
// Execute needed — the function's own w/errW parameters make it directly
// testable). gitident.Reset() guards against any ambient git identity
// leaking into a materialization path that might call gitident.Author()
// (SPEC-085 §5.3/§5.4 note 3) — team-memory is never enabled by these
// fixtures, so no call is expected, but the reset is defensive per the
// established pattern.
func runSessionStartHook(t *testing.T, root, dataDir string) (stdout, stderr string) {
	t.Helper()

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	gitident.Reset()
	t.Cleanup(gitident.Reset)

	oldDataDir := flagDataDir
	flagDataDir = dataDir
	t.Cleanup(func() { flagDataDir = oldDataDir })

	var outBuf, errBuf bytes.Buffer
	if err := runHookSessionStart(context.Background(), &outBuf, &errBuf); err != nil {
		t.Fatalf("runHookSessionStart: unexpected error: %v", err)
	}
	return outBuf.String(), errBuf.String()
}

// TestMaybeActivateProfile_Vanilla covers AC8's silence clause: no pin, no
// host default — the profile block must never appear.
func TestMaybeActivateProfile_Vanilla(t *testing.T) {
	root := t.TempDir() // non-git, no pin
	dataDir := t.TempDir()

	stdout, _ := runSessionStartHook(t, root, dataDir)
	if strings.Contains(stdout, "<!-- mneme:profile:start -->") {
		t.Errorf("vanilla resolution must emit no profile block, got: %s", stdout)
	}
}

// TestMaybeActivateProfile_PinInstalled covers AC7: a repo pinned to an
// installed profile materializes and prints the confirmation block.
func TestMaybeActivateProfile_PinInstalled(t *testing.T) {
	dataDir := t.TempDir()
	source := newProfileCmdFixtureRepo(t, "chatea-pro", "1.0.0")

	if _, _, err := execProfileCmd(t, dataDir, "profile", "add", source); err != nil {
		t.Fatalf("profile add: unexpected error: %v", err)
	}

	root := t.TempDir()
	pin := "name   = \"chatea-pro\"\nsource = \"" + source + "\"\nref    = \"v1\"\n"
	if err := os.WriteFile(filepath.Join(root, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}

	stdout, stderr := runSessionStartHook(t, root, dataDir)
	if !strings.Contains(stdout, "<!-- mneme:profile:start -->") || !strings.Contains(stdout, "chatea-pro") {
		t.Errorf("expected profile confirmation block mentioning chatea-pro, got stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stdout, "via pin") {
		t.Errorf("expected confirmation to name the pin as the source, got: %s", stdout)
	}
}

// TestMaybeActivateProfile_PinMissing covers AC8: a pin naming a profile NOT
// installed emits the actionable nudge with the correct commands, never
// clones (the store directory for the name must never appear).
func TestMaybeActivateProfile_PinMissing(t *testing.T) {
	dataDir := t.TempDir()
	root := t.TempDir()
	pin := "name   = \"chatea-pro\"\nsource = \"git@example.com:acme/chatea-pro.git\"\nref    = \"v3\"\n"
	if err := os.WriteFile(filepath.Join(root, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}

	stdout, _ := runSessionStartHook(t, root, dataDir)
	if !strings.Contains(stdout, "## Profile no instalado") {
		t.Errorf("expected nudge heading, got: %s", stdout)
	}
	if !strings.Contains(stdout, "mneme profile add git@example.com:acme/chatea-pro.git --ref v3") {
		t.Errorf("expected exact add command, got: %s", stdout)
	}
	if !strings.Contains(stdout, "mneme profile use chatea-pro") {
		t.Errorf("expected exact use command, got: %s", stdout)
	}
	if !strings.Contains(stdout, "vanilla") {
		t.Errorf("expected mention of vanilla fallback, got: %s", stdout)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "profiles", "chatea-pro")); !os.IsNotExist(err) {
		t.Errorf("expected no clone to have happened, stat err = %v", err)
	}
}

// TestMaybeActivateProfile_GlobalDefaultMissing covers the "default names an
// absent profile" branch of PinMissing — no known Source, so the nudge asks
// for the URL instead of printing an add command it cannot know.
func TestMaybeActivateProfile_GlobalDefaultMissing(t *testing.T) {
	dataDir := t.TempDir()
	root := t.TempDir() // no pin at all

	if err := config.SetProfilesDefault(config.DefaultPath(), "nonexistent"); err != nil {
		t.Fatalf("SetProfilesDefault: %v", err)
	}
	t.Cleanup(func() { _ = config.SetProfilesDefault(config.DefaultPath(), "") })

	stdout, _ := runSessionStartHook(t, root, dataDir)
	if !strings.Contains(stdout, "## Profile no instalado") {
		t.Errorf("expected nudge heading, got: %s", stdout)
	}
	if !strings.Contains(stdout, "necesitas la URL") {
		t.Errorf("expected the no-known-source variant of the nudge, got: %s", stdout)
	}
	if !strings.Contains(stdout, "mneme profile use nonexistent") {
		t.Errorf("expected mention of the use command, got: %s", stdout)
	}
}

// TestMaybeActivateProfile_PinDefault covers SPEC-096 §6 AC6: a repo pinned
// to the sourceless default (.mneme-profile with no "source") materializes
// the embedded OSS default profile — closing the hole SPEC-093 §3.6 left
// open (a mere "materialización pendiente" confirmation, no real files) —
// and prints the "mneme-default (OSS built-in)" confirmation block.
func TestMaybeActivateProfile_PinDefault(t *testing.T) {
	dataDir := t.TempDir()
	root := t.TempDir()

	pin := "name = \"mneme-default\"\n"
	if err := os.WriteFile(filepath.Join(root, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}

	stdout, stderr := runSessionStartHook(t, root, dataDir)
	if !strings.Contains(stdout, "<!-- mneme:profile:start -->") || !strings.Contains(stdout, "mneme-default (OSS built-in)") {
		t.Errorf("expected the OSS built-in confirmation block, got stdout=%q stderr=%q", stdout, stderr)
	}

	// The embedded default profile's 6 agents must actually be on disk now —
	// AC6 is about REAL materialization, not just a confirmation message.
	agentPath := filepath.Join(root, ".claude", "agents", "backend.md")
	if _, err := os.Stat(agentPath); err != nil {
		t.Errorf("expected the default profile's backend agent to be materialized at %s: %v", agentPath, err)
	}

	lockPath := filepath.Join(root, ".mneme", "profile.lock")
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read profile.lock: %v", err)
	}
	if !strings.Contains(string(lockData), "profile") || !strings.Contains(string(lockData), "mneme-default") {
		t.Errorf("expected the lock to record profile = mneme-default, got: %s", lockData)
	}
	if !strings.Contains(string(lockData), "bundled:") {
		t.Errorf("expected the lock's commit to carry the synthetic \"bundled:\" marker, got: %s", lockData)
	}
}

// TestMaybeActivateProfile_ActivationFailure_FailsOpen covers AC7's
// fail-open guarantee: a materialization failure degrades to a WARN on
// stderr and the hook still returns nil (exit 0), printing no confirmation
// block. The failure is induced deterministically by pre-creating
// <root>/.mneme as a REGULAR FILE, so writeLock's os.MkdirAll(".mneme", ...)
// fails.
func TestMaybeActivateProfile_ActivationFailure_FailsOpen(t *testing.T) {
	dataDir := t.TempDir()
	source := newProfileCmdFixtureRepo(t, "chatea-pro", "1.0.0")

	if _, _, err := execProfileCmd(t, dataDir, "profile", "add", source); err != nil {
		t.Fatalf("profile add: unexpected error: %v", err)
	}

	root := t.TempDir()
	pin := "name   = \"chatea-pro\"\nsource = \"" + source + "\"\nref    = \"v1\"\n"
	if err := os.WriteFile(filepath.Join(root, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}
	// Force writeLock's os.MkdirAll(filepath.Join(root, ".mneme"), ...) to fail.
	if err := os.WriteFile(filepath.Join(root, ".mneme"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed conflicting .mneme file: %v", err)
	}

	stdout, stderr := runSessionStartHook(t, root, dataDir)
	if strings.Contains(stdout, "<!-- mneme:profile:start -->") {
		t.Errorf("expected no confirmation block on activation failure, got: %s", stdout)
	}
	if !strings.Contains(stderr, "profile activation failed") {
		t.Errorf("expected a WARN on stderr, got: %s", stderr)
	}
}

// TestMaybeActivateProfile_PinDefault_ActivationFailure_FailsOpen mirrors
// TestMaybeActivateProfile_ActivationFailure_FailsOpen for the default-profile
// branch (SPEC-096 §6 AC6): a materialization failure degrades to a WARN on
// stderr, exit 0, no confirmation block — the same fail-open contract as
// every other SessionStart branch, never a special case for the default.
func TestMaybeActivateProfile_PinDefault_ActivationFailure_FailsOpen(t *testing.T) {
	dataDir := t.TempDir()
	root := t.TempDir()

	pin := "name = \"mneme-default\"\n"
	if err := os.WriteFile(filepath.Join(root, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}
	// Force writeLock's os.MkdirAll(filepath.Join(root, ".mneme"), ...) to fail.
	if err := os.WriteFile(filepath.Join(root, ".mneme"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed conflicting .mneme file: %v", err)
	}

	stdout, stderr := runSessionStartHook(t, root, dataDir)
	if strings.Contains(stdout, "<!-- mneme:profile:start -->") {
		t.Errorf("expected no confirmation block on activation failure, got: %s", stdout)
	}
	if !strings.Contains(stderr, "default profile activation failed") {
		t.Errorf("expected a WARN on stderr, got: %s", stderr)
	}
}
