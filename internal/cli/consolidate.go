package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newConsolidateCmd returns the "mneme consolidate" subcommand. It runs a
// single consolidation cycle against the project store (and optionally the
// global store when config.Context.IncludeGlobal is true) and prints a
// summary of what was done.
func newConsolidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "consolidate",
		Short: "Run the memory consolidation pipeline",
		Long: `Run the memory consolidation pipeline immediately.

The consolidation pipeline applies four steps in order:

  1. Sweep   — soft-deletes memories whose decayed importance dropped
               below the configured threshold.
  2. Purge   — hard-deletes soft-deleted memories older than the
               configured retention window.
  3. Dedup   — merges pairs of memories whose semantic similarity
               exceeds the configured threshold.
  4. Evict   — soft-deletes the lowest-scoring memories when the store
               exceeds its configured budget.

Consolidation runs automatically in the background when enabled in the
configuration. Use this command to trigger it manually (e.g. before a
backup or when storage is critically low).`,
		Example: `  mneme consolidate`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			result, err := svc.RunConsolidation(cmd.Context())
			if err != nil {
				// Print partial results so the operator knows what completed
				// before the failure, then surface the error.
				if result != nil {
					fmt.Fprintf(os.Stdout,
						"Consolidation complete: %d swept, %d hard-deleted, %d duplicates merged, %d evicted\n",
						result.Swept, result.HardDeleted, result.Duplicates, result.Evicted,
					)
				}
				return err
			}

			fmt.Fprintf(os.Stdout,
				"Consolidation complete: %d swept, %d hard-deleted, %d duplicates merged, %d evicted\n",
				result.Swept, result.HardDeleted, result.Duplicates, result.Evicted,
			)
			return nil
		},
	}
}
