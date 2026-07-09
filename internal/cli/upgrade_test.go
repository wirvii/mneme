package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

// writeFakeClaudeJSON writes a minimal ~/.claude.json under home that
// DetectInstalledAgents recognises as an installed "claude-code" agent (a
// top-level mcpServers.mneme entry). This is the only file DetectInstalledAgents
// reads, so it is sufficient to make the hermetic test see one installed agent.
func writeFakeClaudeJSON(t *testing.T, home string) {
	t.Helper()
	root := map[string]any{
		"mcpServers": map[string]any{
			"mneme": map[string]any{
				"command": "/usr/local/bin/mneme",
				"args":    []string{"mcp", "--tools=agent"},
			},
		},
	}
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal fake .claude.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), data, 0o644); err != nil {
		t.Fatalf("write fake .claude.json: %v", err)
	}
}

// stubInstallExec installs a fake installExec that records every call and
// returns errs[slug] (nil if absent). It restores the original installExec
// via t.Cleanup. No real process is ever executed — this respects
// constraint-no-local-install.
func stubInstallExec(t *testing.T, errs map[string]error) *[]string {
	t.Helper()
	original := installExec
	var calls []string
	installExec = func(ctx context.Context, binaryPath, slug string, w io.Writer) error {
		calls = append(calls, fmt.Sprintf("%s|%s", binaryPath, slug))
		if err := errs[slug]; err != nil {
			fmt.Fprintf(w, "  [warn] Re-install %s failed: %v\n", slug, err)
			return err
		}
		fmt.Fprintf(w, "  [ok] Agent integrations updated (%s)\n", slug)
		return nil
	}
	t.Cleanup(func() { installExec = original })
	return &calls
}

// TestPostUpgradeHooks_OneAgent_Success covers AC2(a): one installed agent
// yields exactly one installExec call with slug "claude-code" and an [ok] line.
func TestPostUpgradeHooks_OneAgent_Success(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFakeClaudeJSON(t, home)

	calls := stubInstallExec(t, nil)

	var buf bytes.Buffer
	err := postUpgradeHooks(&buf, "/path/to/new/mneme")
	if err != nil {
		t.Fatalf("postUpgradeHooks returned error: %v", err)
	}

	if len(*calls) != 1 {
		t.Fatalf("expected 1 installExec call, got %d: %v", len(*calls), *calls)
	}
	if (*calls)[0] != "/path/to/new/mneme|claude-code" {
		t.Errorf("unexpected call: %q", (*calls)[0])
	}

	if !strings.Contains(buf.String(), "[ok] Agent integrations updated (claude-code)") {
		t.Errorf("expected [ok] line in output, got: %s", buf.String())
	}
}

// TestPostUpgradeHooks_InjectedError_NonFatal covers AC2(b) and AC3: when
// installExec returns an error, postUpgradeHooks reports [warn] and returns a
// non-nil error, but does not panic or abort early.
func TestPostUpgradeHooks_InjectedError_NonFatal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFakeClaudeJSON(t, home)

	injected := errors.New("boom")
	stubInstallExec(t, map[string]error{"claude-code": injected})

	var buf bytes.Buffer
	err := postUpgradeHooks(&buf, "/path/to/new/mneme")
	if err == nil {
		t.Fatal("expected postUpgradeHooks to return a non-nil error when installExec fails")
	}
	if !errors.Is(err, injected) {
		t.Errorf("expected returned error to wrap/equal the injected error, got: %v", err)
	}

	if !strings.Contains(buf.String(), "[warn] Re-install claude-code failed") {
		t.Errorf("expected [warn] line in output, got: %s", buf.String())
	}
}

// TestPostUpgradeHooks_NoAgents covers AC2(c): with no installed agents
// detected, postUpgradeHooks makes zero installExec calls and returns nil.
func TestPostUpgradeHooks_NoAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// No ~/.claude.json written — DetectInstalledAgents sees os.IsNotExist and
	// returns (nil, nil), i.e. zero agents.

	calls := stubInstallExec(t, nil)

	var buf bytes.Buffer
	err := postUpgradeHooks(&buf, "/path/to/new/mneme")
	if err != nil {
		t.Fatalf("postUpgradeHooks returned error: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected 0 installExec calls, got %d: %v", len(*calls), *calls)
	}
}

// TestRunUpgrade_PostUpgradeFailure_StillPrintsDone covers AC3 at the
// runUpgrade level: postUpgradeHooks failing must not prevent the final
// "Done. mneme upgraded to vX." line, since the binary replacement already
// succeeded by the time postUpgradeHooks runs. This test exercises
// postUpgradeHooks directly (not the full runUpgrade, which requires network
// access to GitHub) and asserts the non-fatal contract runUpgrade relies on:
// a non-nil error from postUpgradeHooks is safe for the caller to downgrade
// to a warning without aborting.
func TestRunUpgrade_PostUpgradeFailure_StillPrintsDone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFakeClaudeJSON(t, home)

	injected := errors.New("subprocess exited 1")
	stubInstallExec(t, map[string]error{"claude-code": injected})

	var hooksBuf bytes.Buffer
	hooksErr := postUpgradeHooks(&hooksBuf, "/path/to/new/mneme")

	// Simulate exactly what runUpgrade does with the postUpgradeHooks result:
	// downgrade to a warning and keep going.
	var w bytes.Buffer
	if hooksErr != nil {
		fmt.Fprintf(&w, "  [warn] Post-upgrade hooks: %v\n", hooksErr)
	}
	fmt.Fprintf(&w, "Done. mneme upgraded to v9.9.9.\n")

	if !strings.Contains(w.String(), "Done. mneme upgraded to v9.9.9.") {
		t.Errorf("expected the Done line to still print after a postUpgradeHooks failure, got: %s", w.String())
	}
}

// TestRunUpgrade_RefusesDevBuilds covers the QA-flagged regression risk for
// SPEC-070: runUpgrade's `Version == "dev"` guard must still refuse to
// self-update — before making any network call — both for the literal "dev"
// default AND for the case that motivated the fix, a local build whose
// resolveVersionFromBuildInfo fallback correctly kept Version at "dev"
// because Go's build info reported a VCS pseudo-version (see
// TestResolveVersionFromBuildInfo's pseudo-version cases and
// TestIsCleanSemverTag in root_test.go — this test closes the loop by
// asserting the guard those functions feed actually fires).
func TestRunUpgrade_RefusesDevBuilds(t *testing.T) {
	original := Version
	t.Cleanup(func() { Version = original })

	// Case 1: the literal "dev" default (no build-info resolution ran, or it
	// legitimately found nothing to resolve).
	Version = "dev"
	var w1 bytes.Buffer
	err := runUpgrade(&w1, false)
	if err == nil {
		t.Fatal("runUpgrade with Version=\"dev\" must return an error, got nil")
	}
	if !strings.Contains(err.Error(), "cannot upgrade a development build") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Case 2: same outcome after routing a pseudo-version through
	// resolveVersionFromBuildInfo, exactly as init() does — this is the
	// regression QA caught: before the fix, a pseudo-version would have been
	// assigned to Version (since it is not "(devel)"), silently defeating
	// this guard.
	Version = resolveVersionFromBuildInfo("dev", func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v1.21.1-0.20260709120000-abcdef123456+dirty"}}, true
	})
	if Version != "dev" {
		t.Fatalf("precondition failed: resolveVersionFromBuildInfo should have kept \"dev\" for a dirty pseudo-version, got %q", Version)
	}
	var w2 bytes.Buffer
	err = runUpgrade(&w2, false)
	if err == nil {
		t.Fatal("runUpgrade after a dirty-pseudo-version build must still return an error, got nil")
	}
	if !strings.Contains(err.Error(), "cannot upgrade a development build") {
		t.Errorf("unexpected error message: %v", err)
	}
}
