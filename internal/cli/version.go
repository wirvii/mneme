package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newVersionCmd returns the "mneme version" subcommand. It prints the binary
// version and exits. Version is injected via ldflags at release time; local
// builds display "dev".
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the mneme version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(os.Stdout, "mneme v%s\n", Version)
			return nil
		},
	}
}
