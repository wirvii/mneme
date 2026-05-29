package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/model"
)

// newSpecCmd returns the "mneme spec" subcommand group.
// It provides operations for managing specs through the SDD state machine:
// creating drafts, advancing states, registering pushbacks, and resolving them.
func newSpecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "spec",
		Short: "Manage specs in the SDD lifecycle",
		Long: `Manage specs through their lifecycle.

Standard lane: draft -> speccing -> specced -> planning -> planned -> implementing -> qa -> done.
Trivial lane:  draft -> rationale -> implementing -> audit -> done.

Specs can be created directly or by promoting a refined backlog item. The state
machine enforces valid transitions; use pushback/resolve for detours via needs_grill.`,
	}

	cmd.AddCommand(
		newSpecNewCmd(),
		newSpecListCmd(),
		newSpecStatusCmd(),
		newSpecAdvanceCmd(),
		newSpecPushbackCmd(),
		newSpecResolveCmd(),
		newSpecHistoryCmd(),
		newSpecQuickCmd(),
		newSpecRejectCmd(),
	)

	return cmd
}

// newSpecNewCmd returns the "mneme spec new" subcommand.
func newSpecNewCmd() *cobra.Command {
	var (
		flagFromBacklog string
		flagLane        string
		flagScope       string
	)

	cmd := &cobra.Command{
		Use:   "new <title>",
		Short: "Create a new spec in draft status",
		Long: `Create a new spec with status draft.

--lane is required (trivial or standard). --scope is required when --lane=trivial.
Use --from-backlog to link the spec to an existing backlog item.`,
		Example: `  mneme spec new "SDD Engine" --lane standard
  mneme spec new "Fix typo" --lane trivial --scope "internal/model/*.go"
  mneme spec new "Push notifications" --lane standard --from-backlog BL-003`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := model.SpecNewRequest{
				Title:     args[0],
				BacklogID: flagFromBacklog,
				Lane:      model.Lane(flagLane),
				Scope:     flagScope,
			}

			spec, err := svc.SpecNew(cmd.Context(), req)
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "Created %s: %q [%s] lane:%s\n", spec.ID, spec.Title, spec.Status, spec.Lane)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagFromBacklog, "from-backlog", "", "Link to backlog item ID (e.g. BL-001)")
	cmd.Flags().StringVar(&flagLane, "lane", "", "SDD lane: trivial or standard (required)")
	cmd.Flags().StringVar(&flagScope, "scope", "", "Glob pattern for allowed file paths (required when --lane=trivial)")

	return cmd
}

// newSpecQuickCmd returns the "mneme spec quick" subcommand.
// It is only valid for trivial-lane specs in draft status.
func newSpecQuickCmd() *cobra.Command {
	var flagBy string

	cmd := &cobra.Command{
		Use:   "quick <id> <rationale>",
		Short: "Advance a trivial spec from draft to implementing with a rationale",
		Long: `Advance a trivial-lane spec from draft directly to implementing by recording
a 1-3 sentence rationale. The spec must be on the trivial lane and in draft status.
For standard-lane specs use "mneme spec advance" instead.`,
		Example: `  mneme spec quick SPEC-007 "One-line fix to a comment typo in audit.go" --by orchestrator`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagBy == "" {
				return fmt.Errorf("--by is required")
			}

			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			spec, err := svc.SpecQuick(cmd.Context(), model.SpecQuickRequest{
				ID:        args[0],
				Rationale: args[1],
				By:        flagBy,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: draft -> implementing (trivial, by %s)\n",
				spec.ID, flagBy)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagBy, "by", "", "Who triggers the advance (required)")

	return cmd
}

// newSpecListCmd returns the "mneme spec list" subcommand.
func newSpecListCmd() *cobra.Command {
	var (
		flagStatus string
		flagJSON   bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List specs",
		Long: `List specs for the current project.

Filter by --status to narrow results. Without a filter all specs are shown.`,
		Example: `  mneme spec list
  mneme spec list --status implementing
  mneme spec list --status done --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := model.SpecListRequest{
				Status: model.SpecStatus(flagStatus),
			}

			specs, err := svc.SpecList(cmd.Context(), req)
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, specs)
			}

			if len(specs) == 0 {
				fmt.Fprintln(os.Stdout, "No specs found.")
				return nil
			}

			for _, s := range specs {
				fmt.Fprintf(os.Stdout, "  %-10s  [%-13s]  %s\n",
					s.ID, s.Status, s.Title)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagStatus, "status", "", "Filter: draft, speccing, needs_grill, specced, planning, planned, implementing, qa, done")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// newSpecStatusCmd returns the "mneme spec status" subcommand.
func newSpecStatusCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "status <id>",
		Short: "Show detailed status of a spec",
		Long: `Show detailed status of a spec including its full timeline and pushbacks.

The timeline shows each state transition with timestamp, new state, and who
triggered it. Pushbacks are summarised at the bottom.`,
		Example: `  mneme spec status SPEC-001
  mneme spec status SPEC-001 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			resp, err := svc.SpecStatus(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, resp)
			}

			s := resp.Spec
			fmt.Fprintf(os.Stdout, "%s: %s\n", s.ID, s.Title)
			fmt.Fprintf(os.Stdout, "Status: %s\n", s.Status)

			if len(resp.History) > 0 {
				fmt.Fprintln(os.Stdout, "\nTimeline:")
				for _, h := range resp.History {
					byPart := ""
					if h.By != "" {
						byPart = fmt.Sprintf("  by %s", h.By)
					}
					reasonPart := ""
					if h.Reason != "" {
						reasonPart = fmt.Sprintf(": %q", h.Reason)
					}
					fmt.Fprintf(os.Stdout, "  %s  [%s]%s%s\n",
						h.At.Format("15:04"), h.ToStatus, byPart, reasonPart)
				}
			}

			if len(resp.Pushbacks) > 0 {
				resolved := 0
				for _, pb := range resp.Pushbacks {
					if pb.Resolved {
						resolved++
					}
				}
				fmt.Fprintf(os.Stdout, "\nPushbacks: %d (%d resolved)\n",
					len(resp.Pushbacks), resolved)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// newSpecAdvanceCmd returns the "mneme spec advance" subcommand.
func newSpecAdvanceCmd() *cobra.Command {
	var (
		flagBy     string
		flagReason string
	)

	cmd := &cobra.Command{
		Use:   "advance <id>",
		Short: "Advance a spec to its next state",
		Long: `Advance a spec to its next logical state in the SDD lifecycle.

The next state is determined by the current state — there is exactly one
forward path. Use "spec pushback" to deviate into needs_grill instead.`,
		Example: `  mneme spec advance SPEC-001 --by orchestrator
  mneme spec advance SPEC-001 --by architect --reason "All quality gates passed"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagBy == "" {
				return fmt.Errorf("--by is required")
			}

			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := model.SpecAdvanceRequest{
				ID:     args[0],
				By:     flagBy,
				Reason: flagReason,
			}

			spec, err := svc.SpecAdvance(cmd.Context(), req)
			if err != nil {
				return err
			}

			// Show the transition — history has the from status, but we can
			// infer it from what the spec was before the call. We just show
			// the resulting state for simplicity.
			fmt.Fprintf(os.Stdout, "%s: advanced to %s (by %s)\n",
				spec.ID, spec.Status, flagBy)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagBy, "by", "", "Who triggers the advance (required)")
	cmd.Flags().StringVar(&flagReason, "reason", "", "Reason for the transition")

	return cmd
}

// newSpecPushbackCmd returns the "mneme spec pushback" subcommand.
func newSpecPushbackCmd() *cobra.Command {
	var (
		flagFrom      string
		flagQuestions []string
	)

	cmd := &cobra.Command{
		Use:   "pushback <id>",
		Short: "Register a pushback, moving the spec to needs_grill",
		Long: `Register a pushback from an agent, transitioning the spec to needs_grill.

Provide at least one question that blocks progress. The spec will remain in
needs_grill until all pushbacks are resolved with "spec resolve".`,
		Example: `  mneme spec pushback SPEC-001 --from backend --questions "API contract impossible with current auth model?" "Missing dependency on user service?"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagFrom == "" {
				return fmt.Errorf("--from is required")
			}
			if len(flagQuestions) == 0 {
				return fmt.Errorf("--questions requires at least one question")
			}

			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := model.SpecPushbackRequest{
				ID:        args[0],
				FromAgent: flagFrom,
				Questions: flagQuestions,
			}

			spec, err := svc.SpecPushback(cmd.Context(), req)
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: -> needs_grill (pushback from %s)\n", spec.ID, flagFrom)
			fmt.Fprintln(os.Stdout, "Questions:")
			for i, q := range flagQuestions {
				fmt.Fprintf(os.Stdout, "  %d. %s\n", i+1, q)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagFrom, "from", "", "Agent raising the pushback (required)")
	cmd.Flags().StringArrayVar(&flagQuestions, "questions", nil, "Questions blocking progress (required, at least 1)")

	return cmd
}

// newSpecResolveCmd returns the "mneme spec resolve" subcommand.
func newSpecResolveCmd() *cobra.Command {
	var flagResolution string

	cmd := &cobra.Command{
		Use:   "resolve <id>",
		Short: "Resolve the oldest pushback, returning the spec to speccing",
		Long: `Resolve the oldest unresolved pushback on a spec, transitioning it
back to speccing so work can continue.

The spec must be in needs_grill status. If multiple unresolved pushbacks exist,
they must be resolved one at a time.`,
		Example: `  mneme spec resolve SPEC-001 --resolution "Use service accounts, not user JWTs"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagResolution == "" {
				return fmt.Errorf("--resolution is required")
			}

			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := model.SpecResolveRequest{
				ID:         args[0],
				Resolution: flagResolution,
			}

			spec, err := svc.SpecResolve(cmd.Context(), req)
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: needs_grill -> %s (pushback resolved)\n", spec.ID, spec.Status)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagResolution, "resolution", "", "Resolution of the pushback (required)")

	return cmd
}

// newSpecRejectCmd returns the "mneme spec reject" subcommand.
// It sends a spec backward from qa (standard) or audit (trivial) to implementing,
// recording the reason in spec_history. Both --reason and --by are required.
func newSpecRejectCmd() *cobra.Command {
	var (
		flagReason string
		flagBy     string
	)

	cmd := &cobra.Command{
		Use:   "reject <id>",
		Short: "Reject a spec from qa/audit back to implementing",
		Long: `Reject a spec from QA review back to implementing.

Standard lane: qa → implementing.
Trivial lane:  audit → implementing.

The rejection reason is required and is persisted in spec_history. Use this
command to model a QA review that found defects requiring further implementation
work. For ambiguity or missing spec detail use "spec pushback" instead.`,
		Example: `  mneme spec reject SPEC-012 --reason "edge case in payment flow" --by qa-agent`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagReason == "" {
				return fmt.Errorf("--reason is required")
			}
			if flagBy == "" {
				return fmt.Errorf("--by is required")
			}

			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			spec, err := svc.SpecReject(cmd.Context(), model.SpecRejectRequest{
				ID:     args[0],
				Reason: flagReason,
				By:     flagBy,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: rejected back to implementing (by %s)\n", spec.ID, flagBy)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagReason, "reason", "", "Rejection reason (required)")
	cmd.Flags().StringVar(&flagBy, "by", "", "Who triggers the rejection (required)")

	return cmd
}

// newSpecHistoryCmd returns the "mneme spec history" subcommand.
func newSpecHistoryCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "history <id>",
		Short: "Show the full state transition timeline for a spec",
		Long: `Show the full state transition timeline for a spec in chronological order.

Each entry shows the timestamp, new state, who triggered it, and an optional
reason. This is the same timeline shown in "spec status" but without the header.`,
		Example: `  mneme spec history SPEC-001
  mneme spec history SPEC-001 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			history, err := svc.SpecHistory(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, history)
			}

			if len(history) == 0 {
				fmt.Fprintln(os.Stdout, "No history entries found.")
				return nil
			}

			for _, h := range history {
				byPart := ""
				if h.By != "" {
					byPart = fmt.Sprintf("  by %s", h.By)
				}
				reasonPart := ""
				if h.Reason != "" {
					reasonPart = fmt.Sprintf(": %q", h.Reason)
				}
				fmt.Fprintf(os.Stdout, "  %s  [%-13s]%s%s\n",
					h.At.Format(time.RFC3339), h.ToStatus, byPart, reasonPart)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}
