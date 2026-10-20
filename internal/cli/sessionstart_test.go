package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/gitident"
)

// runSessionStartHook chdirs into root, sets --data-dir=dataDir for the
// duration of the call, and drives runHookSessionStart directly (no cobra
// Execute needed — the function's own w/errW parameters make it directly
// testable). gitident.Reset() guards against any ambient git identity
// leaking into a path that might call gitident.Author() (SPEC-085).
//
// payload is variadic so a test that does not care about the session-start
// stdin payload omits it; a test that does passes payload[0] as the raw JSON
// body.
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

// TestSessionStart_IgnoresLegacyProfilePin covers SPEC-162 C4: team profiles
// were retired, so a repo that still carries a .mneme-profile pin (and a host
// config naming a default profile) gets no profile block, no activation lock,
// and no materialized agents — only the ordinary context block.
func TestSessionStart_IgnoresLegacyProfilePin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := filepath.Join(home, ".mneme")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("[profiles]\ndefault = \"chatea-pro\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := t.TempDir()
	pin := "name   = \"chatea-pro\"\nsource = \"git@example.com:acme/chatea-pro.git\"\nref    = \"v3\"\n"
	if err := os.WriteFile(filepath.Join(root, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}

	stdout, _ := runSessionStartHook(t, root, t.TempDir())

	if strings.Contains(stdout, "mneme:profile") {
		t.Errorf("session-start must not emit a profile block any more, got: %s", stdout)
	}
	if !strings.Contains(stdout, "<!-- mneme:context:start -->") {
		t.Errorf("expected the context block to still print, got: %s", stdout)
	}
	for _, rel := range []string{filepath.Join(".mneme", "profile.lock"), filepath.Join(".claude", "agents")} {
		if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Errorf("%s must not exist after session-start (stat err = %v)", rel, err)
		}
	}
}
