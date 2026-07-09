package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
// DetectInstalledAgents stays in-process: it only reads ~/.claude.json, a
// stable, version-independent format, so running it with outgoing code is
// safe (D4).
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
