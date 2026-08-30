package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
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
		newBacklogGetCmd(),
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
		flagLane        string
		flagScope       string
	)

	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Add a new backlog item",
		Long: `Add a new idea to the backlog with status raw.

The title is required as the first positional argument. --lane is required
(trivial or standard). --scope is required when --lane=trivial.

Trivial items (≤3 files, ≤20 lines, no public API change, no SQL/cmd paths)
follow a shortened SDD path. All other items should use standard.`,
		Example: `  mneme backlog add "Fix comment typo" --lane trivial --scope "internal/model/*.go"
  mneme backlog add "Add push notifications" --lane standard
  mneme backlog add "Soporte Windows" --lane standard --priority low --description "Support Windows builds"`,
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
				Lane:        model.Lane(flagLane),
				Scope:       flagScope,
			}

			item, err := svc.BacklogAdd(cmd.Context(), req)
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "Created %s: %q [%s] priority:%s lane:%s\n",
				item.ID, item.Title, item.Status, item.Priority, item.Lane)

			if advisory := service.RefinementAdvisory(item.Lane); advisory != "" {
				fmt.Fprintf(os.Stdout, "\n%s\n", advisory)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagDescription, "description", "", "Detailed description")
	cmd.Flags().StringVar(&flagPriority, "priority", "medium", "Priority: critical, high, medium, low")
	cmd.Flags().StringVar(&flagLane, "lane", "", "SDD lane: trivial or standard (required)")
	cmd.Flags().StringVar(&flagScope, "scope", "", "Glob pattern for allowed file paths (required when --lane=trivial)")

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

			// Limit stays zero: the CLI's contract is full fidelity, never a
			// windowed view (SPEC-109 D9) — printJSON below still receives the
			// bare item slice, not the {items,total} envelope, so --json output
			// is byte-identical to before this spec.
			req := model.BacklogListRequest{
				Status: model.BacklogStatus(flagStatus),
			}

			resp, err := svc.BacklogList(cmd.Context(), req)
			if err != nil {
				return err
			}

			// SPEC-133 D13: the aviso goes on the error channel and never
			// changes stdout's shape — table or --json alike stay exactly
			// what they were before this row existed.
			if len(resp.Unreadable) > 0 {
				renderUnreadableRows(cmd.ErrOrStderr(), resp.Unreadable)
			}

			if flagJSON {
				return printJSON(os.Stdout, resp.Items)
			}

			if len(resp.Items) == 0 {
				if len(resp.Unreadable) > 0 {
					fmt.Fprintf(os.Stdout, "No backlog items could be read: %d row(s) exist but could not be fully read (see stderr).\n", len(resp.Unreadable))
					return nil
				}
				fmt.Fprintln(os.Stdout, "No backlog items found.")
				return nil
			}

			for _, item := range resp.Items {
				specRef := ""
				if item.SpecID != "" {
					specRef = " → " + item.SpecID
				}
				// The refinement count suffix only appears when non-zero
				// (SPEC-110 D19): items with no refinements print byte-identical
				// to before this spec, preserving TestBacklogList_TableOutputFormatUnchanged.
				refs := ""
				if item.RefinementCount > 0 {
					refs = fmt.Sprintf("  refs:%d", item.RefinementCount)
				}
				// SPEC-126 DD8/AC3: a third suffix, on the SAME row — no new
				// line — following the mold of the two above. Only an
				// archived item gets one, so a non-archived row's output
				// stays byte-identical to before this spec.
				archived := ""
				if item.Status == model.BacklogStatusArchived {
					archived = fmt.Sprintf("  archived: %s", archiveReasonOrPlaceholder(item.ArchiveReason))
				}
				fmt.Fprintf(os.Stdout, "  %-8s  [%-8s]  %-40s  %s%s%s%s\n",
					item.ID, item.Status, item.Title, item.Priority, specRef, refs, archived)
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
	var (
		flagRefinement string
		flagBy         string
	)

	cmd := &cobra.Command{
		Use:   "refine <id>",
		Short: "Refine a backlog item",
		Long: `Appends a refinement row to a backlog item (SPEC-110).

An item accepts N refinements: raw becomes refined on the first one, and
refined stays refined on every subsequent one. The description is never
modified — each refinement is stored as its own row; use "backlog get" to
read them all.`,
		Example: `  mneme backlog refine BL-001 --refinement "Acceptance criteria: push on iOS and Android..."
  mneme backlog refine BL-001 --refinement "Merged with BL-002" --by architect`,
		Args: cobra.ExactArgs(1),
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
				By:         flagBy,
			}

			item, err := svc.BacklogRefine(cmd.Context(), req)
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "Refined %s: %q (refinement #%d, status %s)\n",
				item.ID, item.Title, item.RefinementCount, item.Status)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagRefinement, "refinement", "", "Refinement content to add (required)")
	cmd.Flags().StringVar(&flagBy, "by", "", "Who appends the refinement (e.g. orchestrator, architect). Optional.")

	return cmd
}

// newBacklogGetCmd returns the "mneme backlog get" subcommand.
//
// It exists because SPEC-110 D2 moved refinements out of the description:
// `backlog list --json` used to be the CLI's full-fidelity path, and without
// this command the refinement bodies would be unreachable from the CLI
// altogether. It is a SUBCOMMAND of `mneme backlog`, so the top-level command
// count does not change.
func newBacklogGetCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a backlog item with all of its refinements",
		Long: `Get a single backlog item by ID, plus ALL of its refinements — no excerpt, no
limit (SPEC-110 D6). This is the CLI's full-fidelity path for refinements,
the way "backlog list --json" already is for the item fields.`,
		Example: `  mneme backlog get BL-001
  mneme backlog get BL-001 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			resp, err := svc.BacklogGet(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, resp)
			}

			item := resp.Item
			specRef := ""
			if item.SpecID != "" {
				specRef = " → " + item.SpecID
			}
			fmt.Fprintf(os.Stdout, "%s: %q [%s] priority:%s lane:%s%s\n",
				item.ID, item.Title, item.Status, item.Priority, item.Lane, specRef)
			// SPEC-126 DD8/AC1: the archive reason, printed only for an
			// archived item — everything else about this header is
			// unchanged, so a non-archived item's output stays identical to
			// before this spec (AC2).
			if item.Status == model.BacklogStatusArchived {
				fmt.Fprintf(os.Stdout, "archived: %s\n", archiveReasonOrPlaceholder(item.ArchiveReason))
			}
			if item.Description != "" {
				fmt.Fprintf(os.Stdout, "\ndescription:\n%s\n", item.Description)
			}

			if len(resp.Refinements) == 0 {
				fmt.Fprintln(os.Stdout, "\nNo refinements.")
				return nil
			}
			fmt.Fprintf(os.Stdout, "\nrefinements (%d):\n", len(resp.Refinements))
			for _, r := range resp.Refinements {
				by := r.By
				if by == "" {
					by = "(unattributed)"
				}
				fmt.Fprintf(os.Stdout, "\n#%d  by:%s  at:%s\n%s\n", r.Seq, by, r.At.Format(time.RFC3339), r.Body)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

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

// archiveReasonOrPlaceholder returns reason verbatim, or a placeholder that
// SAYS there is none (SPEC-126 DD8) rather than printing an empty label with
// nothing after it. Empty is a reachable case: archive_reason defaults to ”
// at the schema level (004_sdd.sql) and only became mandatory in the
// service with SPEC-125 D1, so an item archived before that spec can carry
// none.
func archiveReasonOrPlaceholder(reason string) string {
	if reason == "" {
		return "(no reason recorded)"
	}
	return reason
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

			result, err := svc.BacklogArchive(cmd.Context(), model.BacklogArchiveRequest{
				ID:     args[0],
				Reason: flagReason,
			})
			if err != nil {
				return err
			}

			// Byte-identical to the pre-SPEC-125 line: anyone already
			// scripting against this output sees no change (DD8).
			fmt.Fprintf(os.Stdout, "Archived %s: %s\n", args[0], flagReason)

			if result.FrozenSpec != nil {
				fs := result.FrozenSpec
				fmt.Fprintf(os.Stdout,
					"%s (%q) is now frozen in status %s: it can still be read and "+
						"documented, but its status can never change again, and this cannot be undone.\n"+
						"If you want to pick this work back up later, create a new backlog item that "+
						"mentions %s. This one does not reopen.\n",
					fs.ID, fs.Title, fs.Status, args[0])
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagReason, "reason", "", "Reason for archiving (required)")

	return cmd
}
