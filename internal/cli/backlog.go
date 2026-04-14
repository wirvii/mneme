package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/model"
)

// newBacklogCmd returns the "mneme backlog" subcommand group.
// It provides operations for managing the backlog: adding raw ideas, refining
// them, promoting them to specs, and archiving discarded items.
func newBacklogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "Manage the project backlog",
		Long: `Manage backlog items through their lifecycle: raw -> refined -> promoted.

Items start as raw ideas, are refined with detailed descriptions and acceptance
criteria, then promoted to specs to enter the SDD lifecycle.`,
	}

	cmd.AddCommand(
		newBacklogAddCmd(),
		newBacklogListCmd(),
		newBacklogRefineCmd(),
		newBacklogPromoteCmd(),
		newBacklogArchiveCmd(),
	)

	return cmd
}

// newBacklogAddCmd returns the "mneme backlog add" subcommand.
func newBacklogAddCmd() *cobra.Command {
	var (
		flagDescription string
		flagPriority    string
	)

	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Add a new backlog item",
		Long: `Add a new idea to the backlog with status raw.

The title is required as the first positional argument. Description and priority
are optional; priority defaults to medium.`,
		Example: `  mneme backlog add "Agregar notificaciones push"
  mneme backlog add "Soporte Windows" --priority low --description "Support Windows builds"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := model.BacklogAddRequest{
				Title:       args[0],
				Description: flagDescription,
				Priority:    model.Priority(flagPriority),
			}

			item, err := svc.BacklogAdd(cmd.Context(), req)
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "Created %s: %q [%s] priority:%s\n",
				item.ID, item.Title, item.Status, item.Priority)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagDescription, "description", "", "Detailed description")
	cmd.Flags().StringVar(&flagPriority, "priority", "medium", "Priority: critical, high, medium, low")

	return cmd
}

// newBacklogListCmd returns the "mneme backlog list" subcommand.
func newBacklogListCmd() *cobra.Command {
	var (
		flagStatus string
		flagJSON   bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List backlog items",
		Long: `List backlog items for the current project, ordered by priority then position.

Filter by --status to narrow results. Without a filter all statuses are shown.`,
		Example: `  mneme backlog list
  mneme backlog list --status raw
  mneme backlog list --status refined --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := model.BacklogListRequest{
				Status: model.BacklogStatus(flagStatus),
			}

			items, err := svc.BacklogList(cmd.Context(), req)
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, items)
			}

			if len(items) == 0 {
				fmt.Fprintln(os.Stdout, "No backlog items found.")
				return nil
			}

			for _, item := range items {
				specRef := ""
				if item.SpecID != "" {
					specRef = " → " + item.SpecID
				}
				fmt.Fprintf(os.Stdout, "  %-8s  [%-8s]  %-40s  %s%s\n",
					item.ID, item.Status, item.Title, item.Priority, specRef)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagStatus, "status", "", "Filter by status: raw, refined, promoted, archived")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// newBacklogRefineCmd returns the "mneme backlog refine" subcommand.
func newBacklogRefineCmd() *cobra.Command {
	var flagRefinement string

	cmd := &cobra.Command{
		Use:   "refine <id>",
		Short: "Refine a raw backlog item",
		Long: `Refine a raw backlog item by adding detailed description content.

The refinement text is appended to the item's existing description and the
status is changed from raw to refined. Only raw items can be refined.`,
		Example: `  mneme backlog refine BL-001 --refinement "Acceptance criteria: push on iOS and Android..."`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagRefinement == "" {
				return fmt.Errorf("--refinement is required")
			}

			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := model.BacklogRefineRequest{
				ID:         args[0],
				Refinement: flagRefinement,
			}

			item, err := svc.BacklogRefine(cmd.Context(), req)
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "Refined %s: %q [raw -> refined]\n", item.ID, item.Title)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagRefinement, "refinement", "", "Refinement content to add (required)")

	return cmd
}

// newBacklogPromoteCmd returns the "mneme backlog promote" subcommand.
func newBacklogPromoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote <id>",
		Short: "Promote a refined backlog item to a spec",
		Long: `Promote a refined backlog item to a spec, entering the SDD lifecycle.

The item must be in refined status. This creates a new spec in draft status
linked to the backlog item.`,
		Example: `  mneme backlog promote BL-001`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			spec, err := svc.BacklogPromote(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "Promoted %s -> %s: %q\n", args[0], spec.ID, spec.Title)
			return nil
		},
	}

	return cmd
}

// newBacklogArchiveCmd returns the "mneme backlog archive" subcommand.
func newBacklogArchiveCmd() *cobra.Command {
	var flagReason string

	cmd := &cobra.Command{
		Use:   "archive <id>",
		Short: "Archive a backlog item",
		Long: `Archive a backlog item with a reason explaining why it was discarded.

The --reason flag is required to ensure the archive decision is documented.`,
		Example: `  mneme backlog archive BL-002 --reason "Superseded by BL-007"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagReason == "" {
				return fmt.Errorf("--reason is required")
			}

			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			if err := svc.BacklogArchive(cmd.Context(), args[0], flagReason); err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "Archived %s: %s\n", args[0], flagReason)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagReason, "reason", "", "Reason for archiving (required)")

	return cmd
}
