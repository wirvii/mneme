package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
)

// newMemCmd returns the "mneme mem" command group. SPEC-164 moved the memory
// administration verbs out of the MCP surface (the agent keeps only six
// mem_* tools); this group is where a person finds them. The top-level
// aliases (mneme stats, mneme forget, ...) stay registered so scripts and the
// team-memory docs keep working.
func newMemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mem",
		Short: "Memory administration (stats, timeline, forget, promote, relate, gaps, explore)",
		Long: `Memory administration verbs that are not exposed to agents over MCP.

Each subcommand is a thin wrapper over the same service the MCP tools and the
HTTP API use.`,
	}
	cmd.AddCommand(
		newStatsCmd(),
		newTimelineCmd(),
		newForgetCmd(),
		newPromoteCmd(),
		newRelateCmd(),
		newGapsCmd(),
		newExploreCmd(),
	)
	return cmd
}

// newTimelineCmd returns "mneme mem timeline": the memories created around a
// memory (by id) or a point in time (ISO 8601), in chronological order.
func newTimelineCmd() *cobra.Command {
	var (
		flagWindow  string
		flagLimit   int
		flagProject string
		flagJSON    bool
	)

	cmd := &cobra.Command{
		Use:   "timeline <around>",
		Short: "Show memories around a memory id or timestamp",
		Long: `List memories created around a point in time, oldest first.

<around> is a memory UUID (its creation time is the centre) or an ISO 8601
timestamp. --window is the total width of the range (default 7d).`,
		Example: `  mneme mem timeline 01938f1b-abcd-7abc-8def-000000000001
  mneme mem timeline 2026-10-01T12:00:00Z --window 24h --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			resp, err := svc.Timeline(cmd.Context(), model.TimelineRequest{
				Around:  args[0],
				Project: flagProject,
				Window:  flagWindow,
				Limit:   flagLimit,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if flagJSON {
				return printJSON(out, resp)
			}
			if len(resp.Results) == 0 {
				fmt.Fprintln(out, "No memories in the window.")
				return nil
			}
			fmt.Fprintf(out, "%-20s  %-12s  %-8s  %s\n", "CREATED", "TYPE", "ID", "TITLE")
			for _, r := range resp.Results {
				fmt.Fprintf(out, "%-20s  %-12s  %-8s  %s\n",
					r.CreatedAt.Format("2006-01-02 15:04:05"), r.Type, shortID(r.ID), r.Title)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagWindow, "window", "", "Total time range, e.g. 7d, 24h (default 7d)")
	cmd.Flags().IntVar(&flagLimit, "limit", 0, "Maximum results (default 20)")
	cmd.Flags().StringVar(&flagProject, "project", "", "Project slug (default: detected project)")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")
	return cmd
}

// newRelateCmd returns "mneme mem relate": creates or updates a relation
// between two graph endpoints (memory id, topic_key, or entity name).
func newRelateCmd() *cobra.Command {
	var (
		flagSourceKind string
		flagTargetKind string
		flagWeight     float64
		flagProject    string
		flagJSON       bool
	)

	cmd := &cobra.Command{
		Use:   "relate <source> <target> <relation>",
		Short: "Create a relation between two graph endpoints",
		Long: `Create or update a relation between two endpoints. Each endpoint is a memory
UUID (or 8+ hex prefix), a memory topic_key, or an entity name.

Relations: depends_on, implements, supersedes, related_to, part_of, uses,
conflicts_with, references.`,
		Example: `  mneme mem relate auth/jwt auth/sessions depends_on
  mneme mem relate PaymentService StripeClient uses --target-kind library --json`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			relation := model.RelationType(args[2])
			if !relation.Valid() {
				return fmt.Errorf("mem relate: %w: %q", model.ErrInvalidRelationType, args[2])
			}

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			resp, err := svc.Relate(cmd.Context(), model.RelateRequest{
				Source:     args[0],
				Target:     args[1],
				Relation:   relation,
				SourceKind: model.EntityKind(flagSourceKind),
				TargetKind: model.EntityKind(flagTargetKind),
				Project:    flagProject,
				Weight:     flagWeight,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if flagJSON {
				return printJSON(out, resp)
			}
			verb := "Updated"
			if resp.Created {
				verb = "Created"
			}
			fmt.Fprintf(out, "%s relation %s (%s -[%s]-> %s, weight %.2f)\n",
				verb, resp.RelationID, args[0], relation, args[1], resp.Weight)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagSourceKind, "source-kind", "", "Entity kind to create for the source (default concept)")
	cmd.Flags().StringVar(&flagTargetKind, "target-kind", "", "Entity kind to create for the target (default concept)")
	cmd.Flags().Float64Var(&flagWeight, "weight", 0, "Relation weight in [0.0, 1.0] (default: per relation type)")
	cmd.Flags().StringVar(&flagProject, "project", "", "Project slug (default: detected project)")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")
	return cmd
}

// shortID returns the first eight characters of id for compact tables.
func shortID(id string) string {
	const shortIDLen = 8
	if len(id) <= shortIDLen {
		return id
	}
	return id[:shortIDLen]
}
