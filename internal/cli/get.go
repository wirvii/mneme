package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// newGetCmd returns the "mneme get" subcommand. It retrieves a single memory
// by its UUIDv7 ID and prints its full content in a human-readable format or
// JSON depending on flags.
func newGetCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Retrieve a memory by ID",
		Long: `Retrieve the full content of a memory identified by its UUIDv7 ID.

Accessing a memory increments its access counter, which reduces decay and
makes it more likely to appear in future context injections.`,
		Example: `  mneme get 019530a1-7e2f-7000-8000-abcdef123456
  mneme get 019530a1-7e2f-7000-8000-abcdef123456 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			m, err := svc.Get(cmd.Context(), id)
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, m)
			}

			// Human-readable format with labelled fields.
			fmt.Fprintf(os.Stdout, "ID:         %s\n", m.ID)
			fmt.Fprintf(os.Stdout, "Title:      %s\n", m.Title)
			fmt.Fprintf(os.Stdout, "Type:       %s\n", m.Type)
			fmt.Fprintf(os.Stdout, "Scope:      %s\n", m.Scope)
			if m.Project != "" {
				fmt.Fprintf(os.Stdout, "Project:    %s\n", m.Project)
			}
			fmt.Fprintf(os.Stdout, "Importance: %.2f\n", m.Importance)
			fmt.Fprintf(os.Stdout, "Created:    %s\n", m.CreatedAt.Format("2006-01-02 15:04:05"))
			fmt.Fprintf(os.Stdout, "Updated:    %s\n", m.UpdatedAt.Format("2006-01-02 15:04:05"))
			fmt.Fprintf(os.Stdout, "Revision:   %d\n", m.RevisionCount)
			if len(m.Files) > 0 {
				fmt.Fprintf(os.Stdout, "Files:      %s\n", strings.Join(m.Files, ", "))
			}

			fmt.Fprintln(os.Stdout)
			fmt.Fprintln(os.Stdout, "--- Content ---")
			fmt.Fprintln(os.Stdout, m.Content)

			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}
