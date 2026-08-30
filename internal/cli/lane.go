package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
)

// newLaneCmd returns the "mneme lane" subcommand group.
// It provides operations for managing the lane classification of trivial-lane
// specs: auditing the diff, reclassifying to standard, overriding a failed
// audit, and inspecting the current lane status.
func newLaneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lane",
		Short: "Manage trivial-lane SDD classification and auditing",
		Long: `Manage lane classification for SDD specs.

Every spec carries a lane (trivial or standard). Trivial items take a shortened
path (draft → rationale → implementing → audit → done) without the full spec/plan
cycle. The deterministic auditor checks the actual diff against declared thresholds
when the spec enters audit status.

Subcommands:
  audit       Run the deterministic post-implementation auditor.
  reclassify  Reclassify a trivial spec to standard.
  override    Override a failed audit and advance to done.
  status      Show lane, scope, and latest audit summary.`,
	}

	cmd.AddCommand(
		newLaneAuditCmd(),
		newLaneReclassifyCmd(),
		newLaneOverrideCmd(),
		newLaneStatusCmd(),
		newLaneStatsCmd(),
	)

	return cmd
}

// newLaneAuditCmd returns the "mneme lane audit" subcommand.
func newLaneAuditCmd() *cobra.Command {
	var flagBase string

	cmd := &cobra.Command{
		Use:   "audit <id>",
		Short: "Run the deterministic post-implementation auditor",
		Long: `Run the deterministic auditor for a trivial-lane spec in audit status.

The auditor checks:
  - File count ≤ 3
  - Line count ≤ 20
  - No forbidden paths (*.sql, migrations/**, cmd/**, install/assets/**)
  - Files within declared scope
  - No exported Go symbol changes
  - No TypeScript/JS export changes

On pass: advances the spec to done.
On fail: prints breaches to stderr and exits non-zero. Use "lane reclassify"
or "lane override" to resolve.`,
		Example: `  mneme lane audit SPEC-007
  mneme lane audit SPEC-007 --base HEAD~2`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			result, err := svc.LaneAudit(cmd.Context(), model.LaneAuditRequest{
				ID:      args[0],
				BaseRef: flagBase,
			})

			// Print audit results regardless of outcome.
			if result != nil {
				fmt.Fprintf(os.Stdout, "Audit %s: files=%d lines=%d passed=%v\n",
					args[0], result.FileCount, result.LinesChanged, result.Passed)
				if len(result.Breaches) > 0 {
					fmt.Fprintln(os.Stderr, "Breaches:")
					for _, b := range result.Breaches {
						fmt.Fprintf(os.Stderr, "  - %s\n", b)
					}
				}
			}

			if err != nil {
				if errors.Is(err, model.ErrAuditFailed) {
					// Exit code 1 so scripts can detect failure.
					return fmt.Errorf("audit failed: use 'lane reclassify' or 'lane override' to resolve")
				}
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: advanced to done\n", args[0])
			return nil
		},
	}

	cmd.Flags().StringVar(&flagBase, "base", "", "Git base ref to diff against (default: merge-base with default branch)")

	return cmd
}

// newLaneReclassifyCmd returns the "mneme lane reclassify" subcommand.
func newLaneReclassifyCmd() *cobra.Command {
	var (
		flagScope string
		flagBy    string
	)

	cmd := &cobra.Command{
		Use:   "reclassify <id> <lane>",
		Short: "Reclassify a trivial spec to standard",
		Long: `Reclassify a trivial-lane spec to standard. Only trivial→standard is allowed.

After reclassification the spec moves to speccing so the full SDD workflow
can proceed. This is the recommended response to a failed audit when the
change turned out to be larger than trivial.`,
		Example: `  mneme lane reclassify SPEC-007 standard --by orchestrator`,
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

			spec, err := svc.LaneReclassify(cmd.Context(), model.LaneReclassifyRequest{
				ID:    args[0],
				Lane:  model.Lane(args[1]),
				Scope: flagScope,
				By:    flagBy,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: reclassified to %s, now in %s\n",
				spec.ID, spec.Lane, spec.Status)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagScope, "scope", "", "Updated scope glob (optional when moving to standard)")
	cmd.Flags().StringVar(&flagBy, "by", "", "Who triggers the reclassification (required)")

	return cmd
}

// newLaneOverrideCmd returns the "mneme lane override" subcommand.
func newLaneOverrideCmd() *cobra.Command {
	var (
		flagReason string
		flagBy     string
	)

	cmd := &cobra.Command{
		Use:   "override <id>",
		Short: "Override a failed lane audit and advance to done",
		Long: `Override a failed lane audit, bypassing threshold checks and advancing the
spec from audit to done. Requires a documented reason that is persisted as a
discovery memory. Use sparingly — prefer reclassify when possible.`,
		Example: `  mneme lane override SPEC-007 --reason "Build tooling file is autogenerated; not a real change" --by orchestrator`,
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

			spec, err := svc.LaneOverride(cmd.Context(), model.LaneOverrideRequest{
				ID:     args[0],
				Reason: flagReason,
				By:     flagBy,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: override applied, now %s\n", spec.ID, spec.Status)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagReason, "reason", "", "Reason for bypassing the audit (required)")
	cmd.Flags().StringVar(&flagBy, "by", "", "Who triggers the override (required)")

	return cmd
}

// newLaneStatsCmd returns the "mneme lane stats" subcommand.
// It reports trivial lane compliance metrics: trivial count, audit-fail
// count and rate, override count, and reclassify count.
func newLaneStatsCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show trivial-lane compliance statistics",
		Long: `Show lane compliance statistics for the project.

Reported metrics:
  trivial_count      Total number of trivial-lane specs.
  audit_fail_count   Number whose latest audit failed.
  audit_fail_rate    audit_fail_count / trivial_count.
  override_count     Number completed via lane_override.
  reclassify_count   Number reclassified from trivial to standard.`,
		Example: `  mneme lane stats
  mneme lane stats --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			resp, err := svc.LaneStats(cmd.Context(), "")
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, resp)
			}

			fmt.Fprintf(os.Stdout, "Trivial specs:   %d\n", resp.TrivialCount)
			fmt.Fprintf(os.Stdout, "Audit failures:  %d (rate: %.1f%%)\n",
				resp.AuditFailCount, resp.AuditFailRate*100)
			fmt.Fprintf(os.Stdout, "Overrides:       %d\n", resp.OverrideCount)
			fmt.Fprintf(os.Stdout, "Reclassified:    %d\n", resp.ReclassifyCount)
			// SPEC-133 D13: lane stats' output is already an informe with
			// sections, so the aviso goes in the normal (stdout) output,
			// not stderr.
			if len(resp.Unreadable) > 0 {
				fmt.Fprintln(os.Stdout)
				renderUnreadableRows(os.Stdout, resp.Unreadable)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// newLaneStatusCmd returns the "mneme lane status" subcommand.
func newLaneStatusCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "status <id>",
		Short: "Show lane classification and latest audit summary",
		Long: `Show the lane, scope, and latest audit outcome for a spec.`,
		Example: `  mneme lane status SPEC-007
  mneme lane status SPEC-007 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			resp, err := svc.LaneStatus(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, resp)
			}

			fmt.Fprintf(os.Stdout, "%s: %s\n", resp.Spec.ID, resp.Spec.Title)
			fmt.Fprintf(os.Stdout, "Lane:   %s\n", resp.Lane)
			fmt.Fprintf(os.Stdout, "Scope:  %s\n", resp.Scope)
			fmt.Fprintf(os.Stdout, "Status: %s\n", resp.Spec.Status)
			if resp.LatestAudit != nil {
				a := resp.LatestAudit
				fmt.Fprintf(os.Stdout, "Audit:  passed=%v at=%s\n",
					a.Passed, a.At.Format("2006-01-02 15:04:05"))
				for _, b := range a.Breaches {
					fmt.Fprintf(os.Stdout, "  - %s\n", b)
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}
