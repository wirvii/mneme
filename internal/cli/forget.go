package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newForgetCmd returns the "mneme forget" subcommand. It marks a memory for
// accelerated decay by setting its decay rate to 1.0, causing its importance
// score to drop rapidly on subsequent scoring passes.
func newForgetCmd() *cobra.Command {
	var flagReason string

	cmd := &cobra.Command{
		Use:   "forget <id>",
		Short: "Mark a memory for accelerated decay",
		Long: `Mark the memory identified by <id> for accelerated decay.

The memory is not immediately deleted. Instead its decay rate is set to the
maximum value (1.0) so that the next consolidation pass drops its importance
score to near zero, making it invisible in search results and context
injections before it is eventually evicted.

Use this command when a memory has become stale or incorrect. The optional
--reason flag is accepted for documentation purposes.`,
		Example: `  mneme forget 01938f1b-abcd-7abc-8def-000000000001
  mneme forget 01938f1b-abcd-7abc-8def-000000000001 --reason "API changed in v2"`,
		Args: cobra.ExactArgs(1),
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

			if err := svc.Forget(cmd.Context(), id, flagReason); err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "Marked for accelerated decay: %s\n", id)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagReason, "reason", "", "Reason for forgetting (informational)")

	return cmd
}
