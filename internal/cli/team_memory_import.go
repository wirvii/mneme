package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/service"
)

// newTeamMemoryImportCmd returns "mneme team-memory import" (SPEC-140 D8):
// the manual, visible counterpart of "mneme team-memory hooks run-import" —
// same underlying import (ImportFromShared), but reachable directly instead
// of only through the hidden hook subcommand. Redaction, posture, and help
// text are calcada de "mneme sdd import" (internal/cli/sdd.go:239-283):
// executes by default (this only populates the LOCAL database, never
// publishes anything), --dry-run previews without writing.
func newTeamMemoryImportCmd() *cobra.Command {
	var flagDryRun bool

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import this repository's shared team memory into the local database",
		Long: `Reads .mneme/shared/ and creates/updates the local database accordingly —
the same read path the installed git hooks run automatically after every
pull/checkout (see "mneme team-memory hooks"). Executes by default: this
only populates the LOCAL database, never publishes anything — pass
--dry-run to preview without writing.

A file whose memory is unchanged locally is skipped; one newer than the
local row updates it; one not found locally is created.`,
		Example: `  mneme team-memory import
  mneme team-memory import --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("team-memory import: cannot determine cwd: %w", err)
			}
			root, err := repoRoot(cwd)
			if err != nil {
				return fmt.Errorf("team-memory import: %w", err)
			}

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			var result *service.TeamMemoryImportResult
			if flagDryRun {
				result, err = svc.ImportFromSharedPreview(cmd.Context(), root)
			} else {
				result, err = svc.ImportFromShared(cmd.Context(), root)
			}
			if err != nil {
				return fmt.Errorf("team-memory import: %w", err)
			}

			renderTeamMemoryImportResult(cmd.OutOrStdout(), result, flagDryRun)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview without writing (default: executes)")
	return cmd
}

// renderTeamMemoryImportResult prints a TeamMemoryImportResult in plain
// text, labeling the header with "would be" when dryRun is true so the two
// modes are never confused when scripted output is piped to a human.
func renderTeamMemoryImportResult(out io.Writer, result *service.TeamMemoryImportResult, dryRun bool) {
	verb := "Imported"
	if dryRun {
		verb = "Would import"
	}
	fmt.Fprintf(out, "%s from %s: %d created, %d updated, %d skipped, %d error(s) (of %d file(s) found).\n",
		verb, result.VaultRoot, result.Created, result.Updated, result.Skipped, result.Errors, result.Total)
	if result.ConflictCandidates > 0 {
		fmt.Fprintf(out, "%d potential conflict candidate(s) found — run `mneme conflicts scan` to review.\n", result.ConflictCandidates)
	}
}
