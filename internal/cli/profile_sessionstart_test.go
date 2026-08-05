package cli

import (
	"bytes"
	"context"
	"io"
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
//
// payload is variadic (SPEC-108 plan §0.4) so the 13 pre-existing call sites
// in this file — none of which cares about the session-start stdin payload —
// stay unchanged; a new test that DOES care passes payload[0] as the raw
// JSON body.
func runSessionStartHook(t *testing.T, root, dataDir string, payload ...string) (stdout, stderr string) {
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

	var in io.Reader = strings.NewReader("")
	if len(payload) > 0 {
		in = strings.NewReader(payload[0])
	}

	var outBuf, errBuf bytes.Buffer
	if err := runHookSessionStart(context.Background(), in, &outBuf, &errBuf); err != nil {
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
// fail-open guarantee (updated for SPEC-105 AC26): a materialization
// failure still degrades to a WARN on stderr and the hook still returns nil
// (exit 0) — but now ALSO emits a profile block on stdout describing the
// failure (SPEC-105 DD16: the agent reads stdout as context, never stderr,
// so a failure that only logged to stderr was invisible to it). The
// failure is induced deterministically by pre-creating <root>/.mneme as a
// REGULAR FILE, so Reconcile's own ActiveLock/writeLock machinery fails.
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
	// Force Reconcile's underlying ActiveLock/writeLock to fail.
	if err := os.WriteFile(filepath.Join(root, ".mneme"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed conflicting .mneme file: %v", err)
	}

	stdout, stderr := runSessionStartHook(t, root, dataDir)
	if !strings.Contains(stdout, "<!-- mneme:profile:start -->") || !strings.Contains(stdout, "## Fallo activando el profile") {
		t.Errorf("expected a failure block on stdout (SPEC-105 AC26), got: %s", stdout)
	}
	if !strings.Contains(stderr, "profile activation failed") {
		t.Errorf("expected a WARN on stderr, got: %s", stderr)
	}
}

// TestMaybeActivateProfile_PinDefault_ActivationFailure_FailsOpen mirrors
// TestMaybeActivateProfile_ActivationFailure_FailsOpen for the default-profile
// branch (SPEC-096 §6 AC6, updated for SPEC-105 AC26): a materialization
// failure degrades to a WARN on stderr AND a failure block on stdout, exit
// 0 either way — the same fail-open contract as every other SessionStart
// branch, never a special case for the default.
func TestMaybeActivateProfile_PinDefault_ActivationFailure_FailsOpen(t *testing.T) {
	dataDir := t.TempDir()
	root := t.TempDir()

	pin := "name = \"mneme-default\"\n"
	if err := os.WriteFile(filepath.Join(root, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}
	// Force Reconcile's underlying ActiveLock/writeLock to fail.
	if err := os.WriteFile(filepath.Join(root, ".mneme"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed conflicting .mneme file: %v", err)
	}

	stdout, stderr := runSessionStartHook(t, root, dataDir)
	if !strings.Contains(stdout, "<!-- mneme:profile:start -->") || !strings.Contains(stdout, "## Fallo activando el profile") {
		t.Errorf("expected a failure block on stdout (SPEC-105 AC26), got: %s", stdout)
	}
	if !strings.Contains(stderr, "default profile activation failed") {
		t.Errorf("expected a WARN on stderr, got: %s", stderr)
	}
}

// TestSessionStart_PartialFailureReportsOnStdoutAndExitsZero (SPEC-105 AC26)
// is the canonical, explicitly-named regression test for the fix
// TestMaybeActivateProfile_ActivationFailure_FailsOpen's updated assertions
// already exercise: a profile activation failure during SessionStart
// produces a "<!-- mneme:profile:start -->" block on STDOUT describing the
// failure, and the hook still returns nil (exit code 0, fail-open).
func TestSessionStart_PartialFailureReportsOnStdoutAndExitsZero(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(root, ".mneme"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed conflicting .mneme file: %v", err)
	}

	// runSessionStartHook itself calls t.Fatalf if runHookSessionStart
	// returns a non-nil error, so simply reaching the assertions below
	// already proves exit code 0 (fail-open).
	stdout, _ := runSessionStartHook(t, root, dataDir)
	if !strings.Contains(stdout, "<!-- mneme:profile:start -->") {
		t.Errorf("expected the profile block on stdout, got: %s", stdout)
	}
	if !strings.Contains(stdout, "## Fallo activando el profile") {
		t.Errorf("expected the failure heading on stdout, got: %s", stdout)
	}
}

// TestSessionStart_SecondRunIsNoop (SPEC-105 DD15) verifies that running the
// SessionStart hook twice against the same pinned profile still prints the
// confirmation block both times (the agent needs to know which profile
// governs the session every time), even though the second run is a
// Reconcile noop under the hood.
func TestSessionStart_SecondRunIsNoop(t *testing.T) {
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

	first, _ := runSessionStartHook(t, root, dataDir)
	if !strings.Contains(first, "chatea-pro") {
		t.Fatalf("expected the first run to confirm chatea-pro, got: %s", first)
	}

	second, _ := runSessionStartHook(t, root, dataDir)
	if !strings.Contains(second, "<!-- mneme:profile:start -->") || !strings.Contains(second, "chatea-pro") {
		t.Errorf("expected the second (noop) run to still confirm chatea-pro, got: %s", second)
	}
	if strings.Contains(second, "## Fallo activando el profile") {
		t.Errorf("expected no failure block on a converged second run, got: %s", second)
	}
}

// TestSessionStart_OrphanLockEmitsActionableBlock (SPEC-105 AC28) verifies
// that a lock left behind with NO pin pointing at it emits the actionable
// orphan-lock block — naming the profile, ActivatedAt, and the exact
// `mneme profile deactivate --apply` command — and that it does NOT
// deactivate anything: the materialized artifacts stay exactly as they
// were.
func TestSessionStart_OrphanLockEmitsActionableBlock(t *testing.T) {
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

	// Activate once (writes the lock), then remove the pin — simulating a
	// repo whose pin was deleted (or never committed) after activation,
	// leaving a lock with nothing pointing at it.
	runSessionStartHook(t, root, dataDir)
	if err := os.Remove(filepath.Join(root, ".mneme-profile")); err != nil {
		t.Fatalf("remove pin: %v", err)
	}

	lockPath := filepath.Join(root, ".mneme", "profile.lock")
	lockBefore, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock before: %v", err)
	}

	stdout, _ := runSessionStartHook(t, root, dataDir)
	if !strings.Contains(stdout, "## Lock de profile huérfano") {
		t.Errorf("expected the orphan-lock heading, got: %s", stdout)
	}
	if !strings.Contains(stdout, "chatea-pro") {
		t.Errorf("expected the profile name in the orphan-lock block, got: %s", stdout)
	}
	if !strings.Contains(stdout, "mneme profile deactivate --apply") {
		t.Errorf("expected the exact deactivate command, got: %s", stdout)
	}

	// No deactivation happened: the lock survives byte-for-byte.
	lockAfter, err := os.ReadFile(lockPath)
	if err != nil {
		t.Errorf("expected the lock to survive (never auto-deactivated): %v", err)
	}
	if string(lockAfter) != string(lockBefore) {
		t.Errorf("expected the lock untouched by the orphan-lock report")
	}
}
