package cli

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
)

// reviewStatsDefaultLimit is how many specs `spec review-stats` shows when
// --limit is not given; 0 lists every spec, which is what the SPEC-181 P1
// measurement needs.
const reviewStatsDefaultLimit = 10

// newSpecReviewStatsCmd returns the "mneme spec review-stats" subcommand,
// the EXPERIMENTAL measurement behind SPEC-181 P1: it dumps one row per
// standard spec and never computes the decision rule.
func newSpecReviewStatsCmd() *cobra.Command {
	var (
		flagLimit int
		flagJSON  bool
	)

	cmd := &cobra.Command{
		Use:   "review-stats",
		Short: "Experimental: fresh-review numbers per standard spec (SPEC-181 P1)",
		Long: `Experimental. Dump the fresh-review numbers of the standard specs, newest first,
so the owner can decide whether the review stays, is adjusted or is removed
(SPEC-181 P1). It lists rows; it does not compute the decision rule. The command
is retired or consolidated once that decision is made and promises no stable fields.

The P1 measurement needs every spec, so use --limit 0.

Columns: REV is whether the spec-moment report has at least one completed
reviewer; M1 and M2 are completed/requested reviewers of each moment, "—" when
the report is missing and "error" when it cannot be read; B/M/N are blocker,
major and nit findings and CORR/REF/ESC/DESC/RIESGO the outcomes, both summed
over the two moments; RECH.QA counts qa rejections and REAP. done reopenings;
FICHAS counts backlog items created within 21 days after done that cite the
spec id (ids are local to this machine).`,
		Example: `  mneme spec review-stats
  mneme spec review-stats --limit 0
  mneme spec review-stats --limit 0 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			rows, err := svc.SpecReviewStats(cmd.Context(), "", flagLimit)
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, rows)
			}
			return renderReviewStatsTable(os.Stdout, rows)
		},
	}

	cmd.Flags().IntVar(&flagLimit, "limit", reviewStatsDefaultLimit, "Max specs to show, newest first (0 = all)")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// renderReviewStatsTable writes the Spanish table of review-stats rows.
func renderReviewStatsTable(w io.Writer, rows []*model.SpecReviewStats) error {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No hay specs standard.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SPEC\tESTADO\tREV\tM1\tM2\tB/M/N\tCORR/REF/ESC/DESC/RIESGO\tRECH.QA\tREAP.\tFICHAS\tDONE")
	for _, r := range rows {
		a, b := r.SpecReview, r.PlanReview
		done := "—"
		if r.DoneAt != nil {
			done = r.DoneAt.UTC().Format("2006-01-02")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d/%d/%d\t%d/%d/%d/%d/%d\t%d\t%d\t%d\t%s\n",
			r.SpecID, r.Status, yesNo(r.Reviewed), momentCell(a), momentCell(b),
			a.Blocker+b.Blocker, a.Major+b.Major, a.Nit+b.Nit,
			a.Fix+b.Fix, a.Refute+b.Refute, a.Escalate+b.Escalate, a.Discard+b.Discard, a.AcceptedRisk+b.AcceptedRisk,
			r.QARejections, r.DoneReopenings, len(r.FollowUpItems), done)
	}
	return tw.Flush()
}

// momentCell renders one moment as completed/requested, "—" when the report
// is missing and "error" when it could not be read.
func momentCell(m model.ReviewMomentStats) string {
	switch {
	case m.Error != "":
		return "error"
	case !m.Present:
		return "—"
	default:
		return fmt.Sprintf("%d/%d", m.Completed, m.Requested)
	}
}

// yesNo renders a boolean as "sí" or "no".
func yesNo(v bool) string {
	if v {
		return "sí"
	}
	return "no"
}
