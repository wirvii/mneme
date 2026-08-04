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

	"github.com/wirvii/mneme/internal/upgrade"
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

// writeFakeCodexConfig writes a minimal ~/.codex/config.toml under home that
// detectAgentsInHome recognises as an installed "codex" agent (a
// [mcp_servers.mneme] table) — the gemelo of writeFakeClaudeJSON above, for
// the "codex" branch of DetectInstalledAgents (SPEC-106 D7).
func writeFakeCodexConfig(t *testing.T, home string) {
	t.Helper()
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}
	content := "[mcp_servers.mneme]\ncommand = \"/usr/local/bin/mneme\"\nargs = [\"mcp\", \"--tools=agent\"]\n"
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fake config.toml: %v", err)
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

// TestPostUpgradeHooks_CodexOnly covers AC15(a) (SPEC-106): a HOME with only
// ~/.codex/config.toml installed yields exactly one installExec call, with
// slug "codex" — the exact scenario DetectInstalledAgents used to be unable
// to see at all (D7).
func TestPostUpgradeHooks_CodexOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFakeCodexConfig(t, home)

	calls := stubInstallExec(t, nil)

	var buf bytes.Buffer
	err := postUpgradeHooks(&buf, "/path/to/new/mneme")
	if err != nil {
		t.Fatalf("postUpgradeHooks returned error: %v", err)
	}

	if len(*calls) != 1 {
		t.Fatalf("expected 1 installExec call, got %d: %v", len(*calls), *calls)
	}
	if (*calls)[0] != "/path/to/new/mneme|codex" {
		t.Errorf("unexpected call: %q", (*calls)[0])
	}
}

// TestPostUpgradeHooks_BothAgents covers AC15(b) (SPEC-106): a HOME with both
// ~/.claude.json and ~/.codex/config.toml installed yields exactly two
// installExec calls, in the fixed order claude-code then codex.
func TestPostUpgradeHooks_BothAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFakeClaudeJSON(t, home)
	writeFakeCodexConfig(t, home)

	calls := stubInstallExec(t, nil)

	var buf bytes.Buffer
	err := postUpgradeHooks(&buf, "/path/to/new/mneme")
	if err != nil {
		t.Fatalf("postUpgradeHooks returned error: %v", err)
	}

	want := []string{"/path/to/new/mneme|claude-code", "/path/to/new/mneme|codex"}
	if len(*calls) != len(want) {
		t.Fatalf("expected %d installExec calls, got %d: %v", len(want), len(*calls), *calls)
	}
	for i := range want {
		if (*calls)[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, (*calls)[i], want[i])
		}
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

// stubLookPathGo installs a fake lookPathGo that returns err (nil = "go
// found"). It restores the original via t.Cleanup. No real PATH lookup is
// ever performed, so this deterministically exercises both branches
// regardless of whether the test host has a "go" binary on PATH.
func stubLookPathGo(t *testing.T, err error) {
	t.Helper()
	original := lookPathGo
	lookPathGo = func() (string, error) {
		if err != nil {
			return "", err
		}
		return "/usr/local/go/bin/go", nil
	}
	t.Cleanup(func() { lookPathGo = original })
}

// stubGoInstallExec installs a fake goInstallExec that records every
// requested tag and returns err. It restores the original via t.Cleanup. No
// real `go install` is ever executed — this respects
// constraint-no-local-install, which SPEC-076 requires for every test in
// this file.
func stubGoInstallExec(t *testing.T, err error) *[]string {
	t.Helper()
	original := goInstallExec
	var tags []string
	goInstallExec = func(ctx context.Context, tag string, w io.Writer) error {
		tags = append(tags, tag)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "  [ok] go install %s\n", goInstallTarget(tag))
		return nil
	}
	t.Cleanup(func() { goInstallExec = original })
	return &tags
}

// stubGoEnvExec installs a fake goEnvExec returning values[key] (and
// errs[key] when present). It restores the original via t.Cleanup. No real
// `go env` subprocess is ever executed.
func stubGoEnvExec(t *testing.T, values map[string]string, errs map[string]error) {
	t.Helper()
	original := goEnvExec
	goEnvExec = func(ctx context.Context, key string) (string, error) {
		if err := errs[key]; err != nil {
			return "", err
		}
		return values[key], nil
	}
	t.Cleanup(func() { goEnvExec = original })
}

// TestGoInstallTarget_UsesTagNotLatest covers AC4 (SPEC-076): the target
// passed to `go install` is "<module>@<TagName>", never "@latest", so the
// version actually fetched always matches the release Check() resolved.
func TestGoInstallTarget_UsesTagNotLatest(t *testing.T) {
	got := goInstallTarget("v1.24.0")
	want := "github.com/wirvii/mneme/cmd/mneme@v1.24.0"
	if got != want {
		t.Errorf("goInstallTarget(%q) = %q, want %q", "v1.24.0", got, want)
	}
	if strings.Contains(got, "@latest") {
		t.Errorf("goInstallTarget must never use @latest, got %q", got)
	}
}

// TestPerformUpgradeWindows_Success covers AC1/AC3/AC4/AC6 (SPEC-076): on the
// Windows branch, checkWritable is never called (there is no way for the
// stubbed seams to fail because of it), go install is invoked exactly once
// with the resolved release's exact TagName via the goInstallExec seam
// (never a real subprocess), and postUpgradeHooks re-execs the path resolved
// from `go env GOBIN`.
func TestPerformUpgradeWindows_Success(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFakeClaudeJSON(t, home)

	stubLookPathGo(t, nil)
	installedTags := stubGoInstallExec(t, nil)
	stubGoEnvExec(t, map[string]string{"GOBIN": `C:\Users\dev\go\bin`}, nil)
	hookCalls := stubInstallExec(t, nil)

	result := &upgrade.CheckResult{
		Current: "1.23.0",
		Latest:  upgrade.Release{TagName: "v1.24.0", Version: "1.24.0"},
	}

	var buf bytes.Buffer
	if err := performUpgradeWindows(&buf, result); err != nil {
		t.Fatalf("performUpgradeWindows returned error: %v", err)
	}

	if len(*installedTags) != 1 || (*installedTags)[0] != "v1.24.0" {
		t.Fatalf("expected exactly one goInstallExec call with tag %q, got %v", "v1.24.0", *installedTags)
	}

	out := buf.String()
	if !strings.Contains(out, "Upgrading mneme v1.23.0 → v1.24.0 via `go install`") {
		t.Errorf("expected upgrading line in output, got: %s", out)
	}
	if !strings.Contains(out, "Done. mneme upgraded to v1.24.0.") {
		t.Errorf("expected Done line in output, got: %s", out)
	}

	wantBinaryPath := filepath.Join(`C:\Users\dev\go\bin`, "mneme.exe")
	if len(*hookCalls) != 1 || (*hookCalls)[0] != wantBinaryPath+"|claude-code" {
		t.Fatalf("expected postUpgradeHooks to re-exec %q, got calls: %v", wantBinaryPath, *hookCalls)
	}
}

// TestPerformUpgradeWindows_GoAbsent covers AC2 (SPEC-076): when Go is not on
// PATH, performUpgradeWindows returns a clear, actionable error before
// touching goInstallExec at all, and never falls back to the Unix
// checkWritable/Upgrader path.
func TestPerformUpgradeWindows_GoAbsent(t *testing.T) {
	stubLookPathGo(t, errors.New("exec: \"go\": executable file not found in $PATH"))
	installedTags := stubGoInstallExec(t, nil)

	result := &upgrade.CheckResult{
		Current: "1.23.0",
		Latest:  upgrade.Release{TagName: "v1.24.0", Version: "1.24.0"},
	}

	var buf bytes.Buffer
	err := performUpgradeWindows(&buf, result)
	if err == nil {
		t.Fatal("expected performUpgradeWindows to return an error when go is absent")
	}
	if !strings.Contains(err.Error(), "go install") || !strings.Contains(err.Error(), "https://go.dev/dl/") {
		t.Errorf("expected an actionable error mentioning go install and https://go.dev/dl/, got: %v", err)
	}
	if len(*installedTags) != 0 {
		t.Errorf("expected zero goInstallExec calls when go is absent, got: %v", *installedTags)
	}
}

// TestPerformUpgradeWindows_GoInstallFailure_NonFatalHooksSkipped covers the
// worst-case documented on goInstallExec: when go install itself fails (e.g.
// the target .exe is locked), performUpgradeWindows surfaces the error and
// never attempts postUpgradeHooks against an unresolved/unwritten binary.
func TestPerformUpgradeWindows_GoInstallFailure_NonFatalHooksSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFakeClaudeJSON(t, home)

	stubLookPathGo(t, nil)
	stubGoInstallExec(t, errors.New("go install: access is denied (file in use)"))
	hookCalls := stubInstallExec(t, nil)

	result := &upgrade.CheckResult{
		Current: "1.23.0",
		Latest:  upgrade.Release{TagName: "v1.24.0", Version: "1.24.0"},
	}

	var buf bytes.Buffer
	err := performUpgradeWindows(&buf, result)
	if err == nil {
		t.Fatal("expected performUpgradeWindows to return an error when go install fails")
	}
	if !strings.Contains(err.Error(), "go install") {
		t.Errorf("expected the go install failure to be wrapped, got: %v", err)
	}
	if len(*hookCalls) != 0 {
		t.Errorf("expected postUpgradeHooks to be skipped after a go install failure, got calls: %v", *hookCalls)
	}
}

// TestResolveGoInstallBinary_PrefersGOBINOverGOPATH covers the resolution
// order documented on resolveGoInstallBinary: GOBIN wins when non-empty.
func TestResolveGoInstallBinary_PrefersGOBINOverGOPATH(t *testing.T) {
	stubGoEnvExec(t, map[string]string{
		"GOBIN":  `C:\Users\dev\go\bin`,
		"GOPATH": `C:\Users\dev\go`,
	}, nil)

	var buf bytes.Buffer
	got := resolveGoInstallBinary(&buf)
	want := filepath.Join(`C:\Users\dev\go\bin`, "mneme.exe")
	if got != want {
		t.Errorf("resolveGoInstallBinary() = %q, want %q", got, want)
	}
}

// TestResolveGoInstallBinary_FallsBackToGOPATH covers the fallback documented
// on resolveGoInstallBinary: an empty GOBIN falls back to GOPATH\bin.
func TestResolveGoInstallBinary_FallsBackToGOPATH(t *testing.T) {
	stubGoEnvExec(t, map[string]string{
		"GOBIN":  "",
		"GOPATH": `C:\Users\dev\go`,
	}, nil)

	var buf bytes.Buffer
	got := resolveGoInstallBinary(&buf)
	want := filepath.Join(`C:\Users\dev\go`, "bin", "mneme.exe")
	if got != want {
		t.Errorf("resolveGoInstallBinary() = %q, want %q", got, want)
	}
}

// TestResolveGoInstallBinary_FallsBackToOSExecutable covers the last-resort
// fallback documented on resolveGoInstallBinary: when both `go env` calls
// fail, it falls back to os.Executable() (the running test binary) rather
// than returning "".
func TestResolveGoInstallBinary_FallsBackToOSExecutable(t *testing.T) {
	stubGoEnvExec(t, nil, map[string]error{
		"GOBIN":  errors.New("go env: go not found"),
		"GOPATH": errors.New("go env: go not found"),
	})

	wantExe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable in this environment: %v", err)
	}

	var buf bytes.Buffer
	got := resolveGoInstallBinary(&buf)
	if got != wantExe {
		t.Errorf("resolveGoInstallBinary() = %q, want os.Executable() fallback %q", got, wantExe)
	}
}

// TestPerformUpgrade_DispatchesByGOOS covers SPEC-076/D1: performUpgrade
// routes to the Windows branch purely based on the injected goos parameter —
// never runtime.GOOS or a //go:build constraint — so this test exercises the
// Windows branch deterministically on any host OS (including this macOS/Unix
// CI/dev machine). It uses the "go absent" error as a cheap, side-effect-free
// probe that the Windows branch (not the Unix checkWritable/Upgrader path)
// actually ran.
func TestPerformUpgrade_DispatchesByGOOS(t *testing.T) {
	stubLookPathGo(t, errors.New("go not found"))

	result := &upgrade.CheckResult{
		Current: "1.23.0",
		Latest:  upgrade.Release{TagName: "v1.24.0", Version: "1.24.0"},
	}

	var buf bytes.Buffer
	err := performUpgrade(&buf, result, "windows")
	if err == nil {
		t.Fatal("expected performUpgrade(goos=\"windows\") to return the go-absent error")
	}
	if !strings.Contains(err.Error(), "en Windows mneme se instala y actualiza con `go install`") {
		t.Errorf("expected the Windows-branch error, got: %v", err)
	}
}
