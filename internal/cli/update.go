package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/model"
)

// newUpdateCmd returns the "mneme update" subcommand. It applies a partial
// update to an existing memory by ID, delegating to service.Update(). Only
// flags explicitly provided by the caller are applied — omitted flags leave
// the corresponding fields unchanged. This mirrors the pointer-based partial
// update semantics of model.UpdateRequest.
func newUpdateCmd() *cobra.Command {
	var (
		flagTitle      string
		flagContent    string
		flagType       string
		flagImportance float64
		flagStdin      bool
		flagJSON       bool
	)

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an existing memory",
		Long: `Apply a partial update to the memory identified by <id>.

Only the flags you provide are applied — all other fields remain unchanged.
At least one update flag (--title, --content, --type, --importance, or --stdin)
must be specified.`,
		Example: `  mneme update 019530a1-7e2f-7000-8000-abcdef123456 --title "New title"
  echo "updated content" | mneme update 019530a1-7e2f-7000-8000-abcdef123456 --stdin
  mneme update 019530a1-7e2f-7000-8000-abcdef123456 --type decision --importance 0.9
  mneme update 019530a1-7e2f-7000-8000-abcdef123456 --title "New title" --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			// Require at least one update flag to avoid no-op invocations.
			updateFlags := []string{"title", "content", "type", "importance", "stdin"}
			anyChanged := slices.ContainsFunc(updateFlags, func(name string) bool {
				return cmd.Flags().Changed(name)
			})
			if !anyChanged {
				return fmt.Errorf("at least one update flag is required (--title, --content, --type, --importance, or --stdin)")
			}

			// Read content from stdin when --stdin is set; --stdin takes precedence over --content.
			content := flagContent
			if cmd.Flags().Changed("stdin") && flagStdin {
				data, err := io.ReadAll(bufio.NewReader(os.Stdin))
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				content = string(data)
			}

			var req model.UpdateRequest

			if cmd.Flags().Changed("title") {
				req.Title = &flagTitle
			}
			if cmd.Flags().Changed("content") || (cmd.Flags().Changed("stdin") && flagStdin) {
				req.Content = &content
			}
			if cmd.Flags().Changed("type") {
				t := model.MemoryType(flagType)
				if !t.Valid() {
					return fmt.Errorf("invalid type %q: must be one of: decision, discovery, bugfix, pattern, preference, convention, architecture, config, session_summary", flagType)
				}
				req.Type = &t
			}
			if cmd.Flags().Changed("importance") {
				req.Importance = &flagImportance
			}

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			resp, err := svc.Update(cmd.Context(), id, req)
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, resp)
			}

			fmt.Fprintf(os.Stdout, "Updated: %s — %s\n", resp.ID, resp.Title)
			return nil
		},
	}

	cmd.Flags().StringVarP(&flagTitle, "title", "t", "", "New title")
	cmd.Flags().StringVarP(&flagContent, "content", "c", "", "New content")
	cmd.Flags().StringVarP(&flagType, "type", "T", "", "New memory type")
	cmd.Flags().Float64VarP(&flagImportance, "importance", "i", 0, "New importance score (0.0-1.0)")
	cmd.Flags().BoolVar(&flagStdin, "stdin", false, "Read new content from stdin")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output result as JSON")

	return cmd
}
