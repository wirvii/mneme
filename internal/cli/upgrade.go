package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/install"
	"github.com/juanftp/mneme/internal/upgrade"
)

// newUpgradeCmd returns the "mneme upgrade" subcommand. It checks for a newer
// release on GitHub, downloads and verifies it, atomically replaces the running
// binary, and re-applies agent integrations via install.Install so that hooks,
// protocol text, and slash commands are always in sync with the new version.
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

// postUpgradeHooks detects which agents have mneme installed and re-runs the
// full install sequence for each. This ensures that MCP config, hooks, the
// injected protocol block, and slash commands all reflect the new binary.
func postUpgradeHooks(w io.Writer, binaryPath string) error {
	agents, err := upgrade.DetectInstalledAgents()
	if err != nil {
		return fmt.Errorf("detect installed agents: %w", err)
	}

	if len(agents) == 0 {
		return nil
	}

	var lastErr error
	for _, slug := range agents {
		agent, err := agentBySlug(slug, binaryPath)
		if err != nil {
			fmt.Fprintf(w, "  [warn] Unknown agent %q: %v\n", slug, err)
			continue
		}
		if err := install.Install(agent, binaryPath); err != nil {
			fmt.Fprintf(w, "  [warn] Re-install %s: %v\n", slug, err)
			lastErr = err
			continue
		}
		fmt.Fprintf(w, "  [ok] Agent integrations updated (%s)\n", slug)
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
