package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
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

			// Limit stays zero (no window, SPEC-109 D9); printJSON receives the
			// bare spec slice so --json output stays byte-identical.
			req := model.SpecListRequest{
				Status: model.SpecStatus(flagStatus),
			}

			resp, err := svc.SpecList(cmd.Context(), req)
			if err != nil {
				return err
			}

			// SPEC-133 D13: same posture as backlog list — aviso on the
			// error channel, stdout's shape (table or --json) unchanged.
			if len(resp.Unreadable) > 0 {
				renderUnreadableRows(cmd.ErrOrStderr(), resp.Unreadable)
			}

			if flagJSON {
				return printJSON(os.Stdout, resp.Specs)
			}

			if len(resp.Specs) == 0 {
				if len(resp.Unreadable) > 0 {
					fmt.Fprintf(os.Stdout, "No specs could be read: %d row(s) exist but could not be fully read (see stderr).\n", len(resp.Unreadable))
					return nil
				}
				fmt.Fprintln(os.Stdout, "No specs found.")
				return nil
			}

			// SPEC-126 DD7/AC15-AC16: a suffix per row, printed ONLY for a
			// spec resp.Frozen names — with nothing frozen (the common
			// case), this loop prints byte-identical to before this spec.
			var firstMarkedID string
			marked := 0
			for _, s := range resp.Specs {
				suffix := ""
				if freeze, ok := resp.Frozen[s.ID]; ok {
					marked++
					if firstMarkedID == "" {
						firstMarkedID = s.ID
					}
					suffix = "  — frozen"
					if freeze.State == model.SpecFreezeMissing {
						suffix = "  — frozen (link missing)"
					}
				}
				fmt.Fprintf(os.Stdout, "  %-10s  [%-13s]  %s%s\n",
					s.ID, s.Status, s.Title, suffix)
			}

			if marked > 0 {
				specWord, verb := "specs", "are"
				if marked == 1 {
					specWord, verb = "spec", "is"
				}
				fmt.Fprintf(os.Stdout,
					"\n%d %s %s marked. \"frozen\" means the backlog item the spec came from was archived, so the\n"+
						"spec's status can no longer change; it can still be read. \"link missing\" means the backlog\n"+
						"item it names is not in this database, so its status cannot change either until that is\n"+
						"fixed. Run \"mneme spec status %s\" to see which item, and why it was archived.\n",
					marked, specWord, verb, firstMarkedID)
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
			// SPEC-157 D5/P4: additive, present only while resp.Spec.Status
			// is qa — the only way to recover the review range after
			// losing session context, since advancing again is impossible.
			svc.AttachReviewRange(cmd.Context(), resp)

			if flagJSON {
				return printJSON(os.Stdout, resp)
			}

			s := resp.Spec
			fmt.Fprintf(os.Stdout, "%s: %s\n", s.ID, s.Title)
			fmt.Fprintf(os.Stdout, "Status: %s\n", s.Status)

			// SPEC-126 DD7/AC17-AC19: immediately after Status, before the
			// timeline — it changes how everything else here should be
			// read. Absent entirely for a live spec (resp.Frozen == nil).
			if resp.Frozen != nil {
				printSpecFreezeBlock(resp.Frozen)
			}

			if resp.ReviewRange != nil {
				fmt.Fprintf(os.Stdout, "Review range: %s\n", resp.ReviewRange.Notice)
			}

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

// printSpecFreezeBlock writes the "Frozen:" block "spec status" prints
// immediately after "Status:", before the timeline (SPEC-126 DD7) — it
// changes how everything else in the output should be read, so it comes
// first. Two mutually exclusive shapes, both written for a person who has
// not read any prior spec about this: the backlog item was archived (names
// the item, its reason, the irreversibility, and the agreed way back — a
// NEW backlog item, never resurrecting the old one), or the item is not in
// this database at all — a different problem with a different remedy, so
// it is never described using the word "archived", which would claim
// something that was never actually read.
//
// Neither shape prints a date: backlog_items has no archived-at instant to
// show (AC19) — see archiveReasonQuotedOrPlaceholder's sibling in
// backlog.go for the same rule applied to the archive reason itself.
func printSpecFreezeBlock(freeze *model.SpecFreeze) {
	if freeze.State == model.SpecFreezeMissing {
		fmt.Fprintf(os.Stdout,
			"Frozen: backlog item %s is not in this database, so mneme cannot check whether it was\n"+
				"        archived. Every attempt to change this spec's status will fail with an error naming\n"+
				"        %s until that item exists again.\n",
			freeze.BacklogID, freeze.BacklogID)
		return
	}
	fmt.Fprintf(os.Stdout,
		"Frozen: backlog item %s was archived — %s\n"+
			"        This spec can still be read and documented, but its status can never change again,\n"+
			"        and that cannot be undone. To pick this work up again, create a new backlog item\n"+
			"        that mentions %s. This one does not reopen.\n",
		freeze.BacklogID, archiveReasonQuotedOrPlaceholder(freeze.Reason), freeze.BacklogID)
}

// archiveReasonQuotedOrPlaceholder mirrors backlog.go's
// archiveReasonOrPlaceholder for the one place that quotes the reason
// inline in a sentence rather than printing it as its own labelled line:
// an empty reason is a reachable case (archive_reason defaults to an empty
// string at the schema level, and only became mandatory in the service
// with SPEC-125 D1), so it is named as absent rather than shown as an empty
// pair of quotes.
func archiveReasonQuotedOrPlaceholder(reason string) string {
	if reason == "" {
		return "(no reason recorded)"
	}
	return fmt.Sprintf("%q", reason)
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

			// SPEC-157 D6: entering qa prints the review range's Notice —
			// the exact sentence the orchestrator copies into the
			// qa-tester's brief; leaving qa prints the frontier note, when
			// D2c actually declared one, read back from the persisted
			// reason (never recomputed).
			if spec.Status == model.SpecStatusQA {
				if rr := svc.ReviewRangeForEntered(cmd.Context(), spec); rr != nil {
					fmt.Fprintf(os.Stdout, "  %s\n", rr.Notice)
				}
			} else {
				printFrontierNoteFromLastTransition(cmd.Context(), svc, spec.ID)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagBy, "by", "", "Who triggers the advance (required)")
	cmd.Flags().StringVar(&flagReason, "reason", "", "Reason for the transition")

	return cmd
}

// printFrontierNoteFromLastTransition prints the frontier note SPEC-157
// appended to specID's most recent spec_history row, when there is one —
// read back from the PERSISTED reason via service.FrontierNoteOf, never
// recomputed (P5 of plan.md). Silent (no output, no error) whenever there
// is nothing to show: no history, a read failure, or a transition that
// never carries a frontier note at all — the overwhelming majority.
func printFrontierNoteFromLastTransition(ctx context.Context, svc *service.SDDService, specID string) {
	history, err := svc.SpecHistory(ctx, specID)
	if err != nil || len(history) == 0 {
		return
	}
	last := history[len(history)-1]
	if note := service.FrontierNoteOf(last.Reason); note != "" {
		fmt.Fprintf(os.Stdout, "  frontera: %s\n", note)
	}
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
// It sends a spec backward from qa (standard), audit (trivial), or done
// (either lane, SPEC-087 D6) to implementing, recording the reason in
// spec_history. Both --reason and --by are required.
func newSpecRejectCmd() *cobra.Command {
	var (
		flagReason   string
		flagBy       string
		flagFindings []string
	)

	cmd := &cobra.Command{
		Use:   "reject <id>",
		Short: "Reject a spec from qa/audit/done back to implementing",
		Long: `Reject a spec from review back to implementing.

Standard lane: qa → implementing, or done → implementing.
Trivial lane:  audit → implementing, or done → implementing.

The rejection reason is required and is persisted in spec_history. Use this
command to model a review that found defects requiring further implementation
work — during the normal gate, or after the fact once a spec already reached
done. For ambiguity or missing spec detail use "spec pushback" instead.

When the spec is standard lane, in qa status, and has a criteria.toml on
file, SPEC-156 requires at least one --finding, each naming a criterion id
declared in that document. Repeat --finding for more than one. Each value
has the form <criterion-id>=<detail>, split on the FIRST "=" (a detail may
itself contain "="). This flag does not carry evidence — the CLI is the
manual path and the detail is enough; a qa-tester subagent attaches
evidence via the spec_reject MCP tool instead.`,
		Example: `  mneme spec reject SPEC-012 --reason "edge case in payment flow" --by qa-agent
  mneme spec reject SPEC-012 --reason "review found issues" --by qa-agent \
    --finding "AC3=does not hold" --finding "AC7=also broken"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagReason == "" {
				return fmt.Errorf("--reason is required")
			}
			if flagBy == "" {
				return fmt.Errorf("--by is required")
			}

			findings := make([]model.RejectFinding, 0, len(flagFindings))
			for _, raw := range flagFindings {
				id, detail, ok := strings.Cut(raw, "=")
				if !ok {
					return fmt.Errorf("--finding %q must have the form <criterion-id>=<detail>", raw)
				}
				findings = append(findings, model.RejectFinding{CriterionID: id, Detail: detail})
			}

			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			spec, err := svc.SpecReject(cmd.Context(), model.SpecRejectRequest{
				ID:       args[0],
				Reason:   flagReason,
				By:       flagBy,
				Findings: findings,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: rejected back to implementing (by %s)\n", spec.ID, flagBy)
			// SPEC-157 D6: one line, only when D2c actually declared a
			// frontier note (e.g. rejecting from qa with HEAD moved).
			printFrontierNoteFromLastTransition(cmd.Context(), svc, spec.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagReason, "reason", "", "Rejection reason (required)")
	cmd.Flags().StringVar(&flagBy, "by", "", "Who triggers the rejection (required)")
	cmd.Flags().StringArrayVar(&flagFindings, "finding", nil,
		"Finding in the form <criterion-id>=<detail>, repeatable")

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
				// SPEC-157 D6: which of the two meanings reviewed_sha has
				// is decided by THIS ROW's own (from, to) pair, never by
				// the value alone (R3) — the label says which.
				reviewedPart := ""
				if h.ReviewedSHA != "" {
					if label := reviewedSHALabel(h.FromStatus, h.ToStatus); label != "" {
						reviewedPart = fmt.Sprintf("  %s: %s", label, abbreviateSHACLI(h.ReviewedSHA))
					}
				}
				fmt.Fprintf(os.Stdout, "  %s  [%-13s]%s%s%s\n",
					h.At.Format(time.RFC3339), h.ToStatus, byPart, reasonPart, reviewedPart)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// reviewedSHALabel names WHICH of SPEC-157's two meanings a history row's
// ReviewedSHA carries, decided ONLY by the row's own (from, to) pair
// (model.SpecHistory.ReviewedSHA's own contract) — "" for every row
// outside the closed three-transition set, which never sets the column.
func reviewedSHALabel(from, to model.SpecStatus) string {
	switch {
	case from == model.SpecStatusImplementing && to == model.SpecStatusQA:
		return "extremo entregado"
	case from == model.SpecStatusQA && (to == model.SpecStatusDone || to == model.SpecStatusImplementing):
		return "frontera revisada"
	default:
		return ""
	}
}

// abbreviateSHACLI truncates sha to 8 characters for human-facing CLI
// output — mirrors internal/service's unexported frontierSHAAbbrevLen
// constant; duplicated here rather than exported, since sddfile/model's
// own leaf-package posture keeps SHA formatting a presentation concern of
// each frontend, not a shared service detail.
func abbreviateSHACLI(sha string) string {
	const n = 8
	if len(sha) <= n {
		return sha
	}
	return sha[:n]
}
