package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/vault"
)

// teamMemoryStatusResult is "mneme team-memory status"'s report (SPEC-140
// D8): whether the shared vault is present, whether THIS machine's own
// import hooks are installed, and — when they are not — the exact command
// that fixes it, so a diagnosis never ends without an instruction.
type teamMemoryStatusResult struct {
	VaultRoot      string `json:"vault_root"`
	VaultPresent   bool   `json:"vault_present"`
	HooksInstalled bool   `json:"hooks_installed"`
}

// newTeamMemoryStatusCmd returns "mneme team-memory status" (SPEC-140 D8):
// a read-only diagnosis that names the vault's presence and whether this
// machine's own import hooks are installed — the missing half of
// team-memory's own diagnostic surface (SDD already has "mneme sdd
// status").
func newTeamMemoryStatusCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show team-memory activation and this machine's import-hook state",
		Long: `Reports, read-only:
  - whether the shared vault (.mneme/shared/) is present in this repository;
  - whether THIS machine's own import hooks (post-merge/post-checkout) are
    installed.

Never writes anything — run "mneme team-memory hooks install" or
"mneme team-memory import" to act on what this reports.`,
		Example: `  mneme team-memory status
  mneme team-memory status --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("team-memory status: cannot determine cwd: %w", err)
			}
			root, err := repoRoot(cwd)
			if err != nil {
				return fmt.Errorf("team-memory status: %w", err)
			}

			vaultRoot := filepath.Join(root, ".mneme", "shared")
			_, statErr := os.Stat(filepath.Join(vaultRoot, vault.MarkerFileName))
			result := teamMemoryStatusResult{
				VaultRoot:      vaultRoot,
				VaultPresent:   statErr == nil,
				HooksInstalled: TeamMemoryHooksInstalled(root),
			}

			if flagJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			renderTeamMemoryStatusResult(cmd.OutOrStdout(), result)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")
	return cmd
}

// renderTeamMemoryStatusResult prints teamMemoryStatusResult in plain text,
// naming the exact command that fixes whatever is missing (D8).
func renderTeamMemoryStatusResult(out io.Writer, result teamMemoryStatusResult) {
	if result.VaultPresent {
		fmt.Fprintf(out, "Shared vault: present (%s)\n", result.VaultRoot)
	} else {
		fmt.Fprintf(out, "Shared vault: not present (%s) — run `mneme team-memory enable` to activate.\n", result.VaultRoot)
	}

	if result.HooksInstalled {
		fmt.Fprintln(out, "This machine's import hooks: installed.")
		return
	}
	fmt.Fprintln(out, "This machine's import hooks: NOT installed — run `mneme team-memory hooks install`.")
}
