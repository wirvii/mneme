package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newSpecReviewPackageCmd returns the "mneme spec review-package" subcommand
// (SPEC-188 D1). It prints ONLY the path of the file it wrote: the
// orchestrator hands that path to the qa-tester, so the reviewed code never
// passes through the orchestrator's own context.
func newSpecReviewPackageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "review-package <id>",
		Short: "Write the review package (log, stat, diff -U10) of the spec's current review range and print its path",
		Long: `Write the review package of a spec's current review range and print its path.

The package is a Markdown file with the git log --oneline, the git diff --stat
and the git diff -U10 of the review_range mneme computed when the spec entered
qa, with the user's git configuration and environment neutralised so the same
range produces the same bytes on any machine. It is written to
<workflow dir>/<project>/specs/<ID>/review-package-<from8>-<to8>.md, replacing
any earlier file of the same range, and the command prints only that path.
Pass the path to the reviewer; never paste the content.

When the frontier was lost, the header says so in mneme's own words.

The command fails, writes nothing and exits 1 when the spec is not in qa, when
its review range is not available, or when the workflow or repository
directory is not configured.`,
		Example: `  mneme spec review-package SPEC-188`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			pkg, err := svc.SpecReviewPackage(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, pkg.Path)
			return nil
		},
	}
}
