package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
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
		flagSupersedes []string
		flagConflicts  []string
		flagUnrelated  []string
		flagConfirm    bool
	)

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an existing memory",
		Long: `Apply a partial update to the memory identified by <id>.

Only the flags you provide are applied — all other fields remain unchanged.
At least one update flag (--title, --content, --type, --importance, --stdin,
--supersedes, --conflicts-with, --unrelated-to, or --confirm) must be specified.

--confirm says the memory is still true: it restarts the memory's review
clock without counting as an edit.

--supersedes, --conflicts-with and --unrelated-to record your judgement of
other memories (repeatable, one id each). A call with only those flags is not
counted as a revision of the memory.`,
		Example: `  mneme update 019530a1-7e2f-7000-8000-abcdef123456 --title "New title"
  echo "updated content" | mneme update 019530a1-7e2f-7000-8000-abcdef123456 --stdin
  mneme update 019530a1-7e2f-7000-8000-abcdef123456 --type decision --importance 0.9
  mneme update 019530a1-7e2f-7000-8000-abcdef123456 --title "New title" --json
  mneme update 019530a1-7e2f-7000-8000-abcdef123456 --supersedes <old-id> --unrelated-to <other-id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			// Require at least one update flag to avoid no-op invocations.
			updateFlags := []string{"title", "content", "type", "importance", "stdin", "supersedes", "conflicts-with", "unrelated-to", "confirm"}
			anyChanged := slices.ContainsFunc(updateFlags, func(name string) bool {
				return cmd.Flags().Changed(name)
			})
			if !anyChanged {
				return fmt.Errorf("at least one update flag is required (--title, --content, --type, --importance, --stdin, --supersedes, --conflicts-with, --unrelated-to, or --confirm)")
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

			req.Supersedes = flagSupersedes
			req.ConflictsWith = flagConflicts
			req.UnrelatedTo = flagUnrelated
			req.Confirm = flagConfirm

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			resp, err := svc.Update(cmd.Context(), id, req)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if flagJSON {
				return printJSON(out, resp)
			}

			verb := "Updated"
			if resp.ReviewedAt != nil && !req.HasFieldChanges() && len(resp.RelationsApplied) == 0 {
				verb = "Confirmed"
			}
			fmt.Fprintf(out, "%s: %s — %s\n", verb, resp.ID, resp.Title)
			for _, r := range resp.RelationsApplied {
				fmt.Fprintf(out, "  %s → %s\n", r.Relation, shortID(r.TargetID))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&flagTitle, "title", "t", "", "New title")
	cmd.Flags().StringVarP(&flagContent, "content", "c", "", "New content")
	cmd.Flags().StringVarP(&flagType, "type", "T", "", "New memory type")
	cmd.Flags().Float64VarP(&flagImportance, "importance", "i", 0, "New importance score (0.0-1.0)")
	cmd.Flags().BoolVar(&flagStdin, "stdin", false, "Read new content from stdin")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output result as JSON")
	cmd.Flags().StringArrayVar(&flagSupersedes, "supersedes", nil, "Id of an older memory this one replaces (repeatable)")
	cmd.Flags().StringArrayVar(&flagConflicts, "conflicts-with", nil, "Id of a memory this one contradicts (repeatable)")
	cmd.Flags().BoolVar(&flagConfirm, "confirm", false, "Confirm the memory is still true (restarts its review clock)")
	cmd.Flags().StringArrayVar(&flagUnrelated, "unrelated-to", nil, "Id of a memory judged unrelated (repeatable)")

	return cmd
}
