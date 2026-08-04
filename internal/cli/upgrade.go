package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/upgrade"
)

// newUpgradeCmd returns the "mneme upgrade" subcommand. It checks for a newer
// release on GitHub, downloads and verifies it, atomically replaces the running
// binary, and re-applies agent integrations by re-executing the new binary
// (`<binaryPath> install <slug>`) so that hooks, protocol text, and slash
// commands are always in sync with the new version's own embedded assets.
//
// The --check flag makes the command report whether an update is available
// without downloading anything or modifying the filesystem.
func newUpgradeCmd() *cobra.Command {
	var flagCheck bool

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade mneme to the latest release",
		Long: `Upgrade mneme to the latest release from GitHub.

The command:
  1. Queries the GitHub Releases API for the latest version.
  2. Downloads the release archive and verifies its SHA256 checksum.
  3. Atomically replaces the running binary.
  4. Re-applies agent integrations (MCP config, hooks, protocol, slash commands).

Use --check to only print whether an update is available.`,
		Example: `  mneme upgrade
  mneme upgrade --check`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgrade(cmd.OutOrStdout(), flagCheck)
		},
	}

	cmd.Flags().BoolVar(&flagCheck, "check", false, "Only check for updates; do not download or install")

	return cmd
}

// runUpgrade implements the upgrade logic, writing progress to w.
func runUpgrade(w io.Writer, checkOnly bool) error {
	if Version == "dev" {
		return fmt.Errorf("upgrade: cannot upgrade a development build. Install a release version first")
	}

	checker := &upgrade.Checker{Repo: "wirvii/mneme"}

	result, err := checker.Check(Version)
	if err != nil {
		return fmt.Errorf("upgrade: check: %w", err)
	}

	if !result.UpdateAvail {
		fmt.Fprintf(w, "mneme is up to date (v%s)\n", result.Current)
		return nil
	}

	if checkOnly {
		fmt.Fprintf(w, "Update available: v%s → v%s\n", result.Current, result.Latest.Version)
		return nil
	}

	return performUpgrade(w, result, runtime.GOOS)
}

// performUpgrade applies an already-resolved, available upgrade. It is split
// out from runUpgrade so tests can drive the Windows/Unix branch
// deterministically via the goos parameter, instead of a //go:build
// constraint: SPEC-076/D1 keeps runtime.GOOS branching as plain injectable
// values so the leaf logic stays testable from any host OS.
func performUpgrade(w io.Writer, result *upgrade.CheckResult, goos string) error {
	if goos == "windows" {
		return performUpgradeWindows(w, result)
	}

	// Unix path — behaviour unchanged from before SPEC-076. internal/upgrade
	// (checkWritable + upgrader.Upgrade: download .tar.gz, verify checksum,
	// extract, atomic os.Rename) stays untouched, serving anyone who
	// installed via install.sh without a Go toolchain.

	// Resolve the absolute path of the running binary, following symlinks.
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("upgrade: resolve binary path: %w", err)
	}
	binaryPath, err = filepath.EvalSymlinks(binaryPath)
	if err != nil {
		return fmt.Errorf("upgrade: eval symlinks: %w", err)
	}

	// Ensure we can write to the directory that contains the binary.
	binaryDir := filepath.Dir(binaryPath)
	if err := checkWritable(binaryDir); err != nil {
		return fmt.Errorf("upgrade: cannot write to %s. Run with sudo or move mneme to a writable location", binaryDir)
	}

	fmt.Fprintf(w, "Upgrading mneme v%s → v%s...\n", result.Current, result.Latest.Version)

	upgrader := &upgrade.Upgrader{Repo: "wirvii/mneme"}
	if err := upgrader.Upgrade(result.Latest, binaryPath, w); err != nil {
		return err
	}

	// Post-upgrade: re-apply agent integrations so hooks, protocol text, and
	// slash commands are refreshed to the content embedded in the new binary.
	if err := postUpgradeHooks(w, binaryPath); err != nil {
		// Non-fatal: the binary was already replaced; report but don't fail.
		fmt.Fprintf(w, "  [warn] Post-upgrade hooks: %v\n", err)
	}

	fmt.Fprintf(w, "Done. mneme upgraded to v%s.\n", result.Latest.Version)
	return nil
}

// goInstallModule is the module path passed to `go install`. It must match
// go.mod's real module (github.com/wirvii/mneme, aligned in SPEC-070) plus
// the cmd/mneme entrypoint — not the legacy juanftp/mneme path.
const goInstallModule = "github.com/wirvii/mneme/cmd/mneme"

// goInstallTimeout bounds the `go install` subprocess launched by
// goInstallExec. Compiling mneme (pure-Go, no CGO) and resolving/downloading
// its module graph can legitimately take longer than a simple binary swap,
// so this is more generous than installExecTimeout.
const goInstallTimeout = 5 * time.Minute

// goEnvTimeout bounds the `go env` subprocess used to resolve go install's
// target directory (GOBIN/GOPATH). `go env` is a fast, local, read-only
// query, so a short timeout is enough.
const goEnvTimeout = 10 * time.Second

// lookPathGo reports whether a "go" toolchain is available on PATH. It is a
// package-level var (mirrors installExec/goInstallExec below) so tests can
// force the "no toolchain" branch deterministically: the real exec.LookPath
// always succeeds under `go test` (the test binary itself was built with
// Go), so it cannot otherwise exercise that error path.
var lookPathGo = func() (string, error) { return exec.LookPath("go") }

// goInstallExec runs `go install <goInstallModule>@<tag>`, installing (or
// updating, on a subsequent run) mneme the same way every Windows user
// already does per SPEC-070/SPEC-074 — there is no other supported Windows
// install path. It is a package-level var so tests can substitute a fake
// that records the requested tag without ever executing `go install` for
// real (respects constraint-no-local-install).
//
// Worst case: if go install cannot even rename-aside the target .exe (an AV
// scanner, or another process — e.g. this very Claude Code session running
// mneme as an MCP server — holds a lock on it), `go install` fails with a
// file-in-use style error. The documented recovery is to close Claude Code
// (which releases the MCP `mneme` process) and re-run `mneme upgrade`.
var goInstallExec = runGoInstall

// goInstallTarget builds the "<module>@<tag>" argument passed to `go install`
// for the given release tag (e.g. "v1.24.0"). Split out as a pure function so
// tests can assert the exact target string — module path and tag, not
// "@latest" — without ever invoking exec.Command (constraint-no-local-install).
func goInstallTarget(tag string) string {
	return fmt.Sprintf("%s@%s", goInstallModule, tag)
}

// runGoInstall is the default implementation of goInstallExec.
func runGoInstall(ctx context.Context, tag string, w io.Writer) error {
	target := goInstallTarget(tag)

	cmd := exec.CommandContext(ctx, "go", "install", target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > 0 {
			fmt.Fprintf(w, "%s\n", indentLines(string(out)))
		}
		return fmt.Errorf("go install %s: %w", target, err)
	}

	fmt.Fprintf(w, "  [ok] go install %s\n", target)
	return nil
}

// goEnvExec runs `go env <key>` and returns its trimmed output. It is a
// package-level var (same seam pattern as goInstallExec) so tests can
// control GOBIN/GOPATH resolution without depending on the test host's real
// Go environment.
var goEnvExec = runGoEnv

// runGoEnv is the default implementation of goEnvExec.
func runGoEnv(ctx context.Context, key string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "env", key)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// performUpgradeWindows implements the Windows branch of SPEC-076/SS-2 (D4′):
// Windows has no downloadable mneme binary (see SPEC-074 §3), so both install
// and upgrade go through `go install`. checkWritable is intentionally never
// called here — `go install` manages and reports on its own destination
// directory (GOBIN or GOPATH\bin) — and internal/upgrade.Upgrader is never
// invoked on this branch.
func performUpgradeWindows(w io.Writer, result *upgrade.CheckResult) error {
	if _, err := lookPathGo(); err != nil {
		return fmt.Errorf("upgrade: en Windows mneme se instala y actualiza con `go install`; " +
			"no se encontró el toolchain de Go en PATH. Instálalo desde https://go.dev/dl/ y reintenta")
	}

	fmt.Fprintf(w, "Upgrading mneme v%s → v%s via `go install`...\n", result.Current, result.Latest.Version)

	ctx, cancel := context.WithTimeout(context.Background(), goInstallTimeout)
	defer cancel()

	// Target the exact release tag Check() resolved, not @latest: this keeps
	// the "vX → vY" message above and the version go install actually fetches
	// in sync, even if the module proxy's notion of "latest" briefly lags the
	// GitHub release Check() just queried.
	if err := goInstallExec(ctx, result.Latest.TagName, w); err != nil {
		return fmt.Errorf("upgrade: go install: %w", err)
	}

	// Re-exec the freshly installed binary for postUpgradeHooks so the new
	// version's own embedded assets are applied (see postUpgradeHooks
	// godoc). Resolve go install's actual target directory rather than
	// os.Executable(): the running process is still the OUTGOING binary
	// (which may not even live under GOBIN), so os.Executable() is only the
	// last-resort fallback.
	if binaryPath := resolveGoInstallBinary(w); binaryPath != "" {
		if err := postUpgradeHooks(w, binaryPath); err != nil {
			// Non-fatal: go install already succeeded; report but don't fail.
			fmt.Fprintf(w, "  [warn] Post-upgrade hooks: %v\n", err)
		}
	}

	fmt.Fprintf(w, "Done. mneme upgraded to v%s.\n", result.Latest.Version)
	return nil
}

// resolveGoInstallBinary resolves the absolute path of the mneme.exe that
// `go install` just wrote, in the same order Go itself picks a target: GOBIN
// first, then GOPATH's bin directory. If neither `go env` call succeeds
// (e.g. go disappeared from PATH between the install and this call), it
// falls back to os.Executable(), which may point at a stale copy if the user
// runs mneme from outside GOBIN — a known edge (same class as the
// SPEC-067 bug), acceptable because the sanctioned Windows install path
// always resolves through GOBIN. Returns "" only if every fallback fails, in
// which case the caller skips postUpgradeHooks rather than re-exec'ing an
// unresolved path.
func resolveGoInstallBinary(w io.Writer) string {
	ctx, cancel := context.WithTimeout(context.Background(), goEnvTimeout)
	defer cancel()

	if gobin, err := goEnvExec(ctx, "GOBIN"); err == nil && gobin != "" {
		return filepath.Join(gobin, "mneme.exe")
	}
	if gopath, err := goEnvExec(ctx, "GOPATH"); err == nil && gopath != "" {
		return filepath.Join(gopath, "bin", "mneme.exe")
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}

	fmt.Fprintf(w, "  [warn] could not resolve the go install target; skipping post-upgrade hooks\n")
	return ""
}

// installExecTimeout bounds the "mneme install <slug>" subprocess launched by
// postUpgradeHooks. install is non-interactive and idempotent in its default
// path (no --personal), so this timeout only protects against unexpected
// hangs — aligned with the skills validate timeout.
const installExecTimeout = 120 * time.Second

// installExec runs `mneme install <slug>` on the freshly written binary at
// binaryPath as a subprocess, so the incoming version's own embedded assets
// (skills, commands, agent profiles, etc.) are applied — not the assets of
// the outgoing version still resident in this process's memory. It is a
// package-level var so tests can substitute a fake that records calls
// without executing anything (respects constraint-no-local-install).
var installExec = runInstallExec

// runInstallExec is the default implementation of installExec. See D2/D3 in
// SPEC-067 for the rationale: CombinedOutput avoids interleaving the
// subprocess's own progress output with the upgrade's; a non-nil error is
// always treated as non-fatal by the caller.
func runInstallExec(ctx context.Context, binaryPath, slug string, w io.Writer) error {
	cmd := exec.CommandContext(ctx, binaryPath, "install", slug)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(w, "  [warn] Re-install %s failed: %v\n", slug, err)
		if len(out) > 0 {
			fmt.Fprintf(w, "%s\n", indentLines(string(out)))
		}
		fmt.Fprintf(w, "  [hint] Run `mneme install %s` manually to finish provisioning.\n", slug)
		return err
	}
	fmt.Fprintf(w, "  [ok] Agent integrations updated (%s)\n", slug)
	return nil
}

// indentLines prefixes every line of s with two spaces, for nesting captured
// subprocess output under a "[warn]" progress line.
func indentLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

// postUpgradeHooks detects which agents have mneme installed and re-provisions
// each one by re-executing the freshly written binary as a subprocess
// (`<binaryPath> install <slug>`). This is required because the code and
// embedded assets running in THIS process are still those of the outgoing
// version — a process cannot load the logic or assets of a binary it hasn't
// exec'd (see SPEC-067 / bug/upgrade-in-process-install-stale-assets).
//
// DetectInstalledAgents stays in-process: it reads ~/.claude.json AND
// ~/.codex/config.toml (SPEC-106 D7 — Codex detection was previously
// missing entirely), both stable, version-independent formats, so running
// it with outgoing code is safe (D4).
//
// Failures are non-fatal: the binary on disk has already been replaced by
// upgrader.Upgrade, so a failed re-provision step is reported as a warning
// and does not abort the upgrade.
func postUpgradeHooks(w io.Writer, binaryPath string) error {
	agents, err := upgrade.DetectInstalledAgents()
	if err != nil {
		return fmt.Errorf("detect installed agents: %w", err)
	}

	if len(agents) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), installExecTimeout)
	defer cancel()

	var lastErr error
	for _, slug := range agents {
		if err := installExec(ctx, binaryPath, slug, w); err != nil {
			lastErr = err
			continue
		}
	}
	return lastErr
}

// checkWritable reports whether the process can create files in dir by
// attempting to create and immediately remove a temporary file.
func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".mneme-write-check-*")
	if err != nil {
		return err
	}
	f.Close()
	os.Remove(f.Name())
	return nil
}
