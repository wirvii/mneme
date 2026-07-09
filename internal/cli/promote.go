package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newPromoteCmd returns the "mneme promote" subcommand. It marks a memory as
// team-curated (shared=2, SPEC-053 D8) and persists that change to the
// database, materializing it to the shared git vault immediately when
// team-memory is active for the current repository.
func newPromoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote <id>",
		Short: "Mark a memory as team-curated and share it with the team",
		Long: `Mark the memory identified by <id> as team-curated (SPEC-053 D8).

This explicitly opts the memory into team-memory sharing regardless of its
type, setting its sharing level to 2 (team-curated) in the database. When the
current repository has team-memory active (a shared vault marker exists),
the memory is also written to the shared vault immediately so it does not
have to wait for a subsequent save/update.

Idempotent: promoting an already-promoted memory is a no-op beyond
re-confirming shared=2.`,
		Example: `  mneme promote 01938f1b-abcd-7abc-8def-000000000001`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if id == "" {
				return fmt.Errorf("id is required")
			}

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			m, err := svc.Promote(cmd.Context(), id)
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "Promoted to team-curated: %s (shared=%d, author=%q)\n", m.ID, m.Shared, m.Author)
			return nil
		},
	}

	return cmd
}
