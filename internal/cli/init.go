package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/service"
)

// newInitCmd returns the "mneme init" subcommand.
// By default it performs a dry-run: it detects legacy workflow artifacts, prints
// the migration plan, and exits without touching the filesystem or the database.
// Pass --apply to execute the migration; pair with --yes to skip the confirmation
// prompt (useful for scripts and CI).
func newInitCmd() *cobra.Command {
	var flagApply, flagYes bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Migrate a project from legacy workflows to mneme SDD engine",
		Long: `mneme init detects legacy workflow artifacts (.workflow/, .claude/specs/, etc.),
classifies them using a weighted heuristic, and migrates them to the SDD engine.

By default it performs a dry-run and prints the migration plan. Use --apply to
execute the migration. The command is idempotent: a second run on a fully-migrated
project finds no artifacts and exits cleanly.`,
		Example: `  mneme init                  # dry-run: show migration plan
  mneme init --apply          # execute (asks for confirmation)
  mneme init --apply --yes    # execute without prompt (script-safe)
  mneme init --yes            # dry-run (--yes without --apply is ignored with warning)`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flagYes && !flagApply {
				fmt.Fprintln(cmd.ErrOrStderr(), "Warning: --yes has no effect without --apply. Running dry-run.")
			}

			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("cannot determine working directory: %w", err)
			}

			// Boot both services sharing the same project slug.
			sddSvc, sddCleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer sddCleanup()

			memSvc, memCleanup, err := initService()
			if err != nil {
				return err
			}
			defer memCleanup()

			// Load config for workflow dir resolution.
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("cannot determine home directory: %w", err)
			}
			cfg, err := config.Load(home + "/.mneme/config.toml")
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			initSvc := service.NewInitService(cfg, sddSvc, memSvc, sddSvc.ProjectSlug())

			// Step 1: always compute the plan first (dry-run).
			report, err := initSvc.Plan(cmd.Context(), cwd)
			if err != nil {
				return fmt.Errorf("plan: %w", err)
			}

			// Step 2: print plan.
			printPlan(cmd.OutOrStdout(), report.Plan)

			if !flagApply {
				fmt.Fprintln(cmd.OutOrStdout(), "\nDry run — usa --apply para ejecutar.")
				return nil
			}

			// Step 3: confirm interactively unless --yes.
			if !flagYes {
				if !promptYes(cmd.InOrStdin(), cmd.OutOrStdout(), "¿Ejecutar migración? [y/N] ") {
					fmt.Fprintln(cmd.OutOrStdout(), "Cancelado.")
					return nil
				}
			}

			// Step 4: apply.
			applied, err := initSvc.Apply(cmd.Context(), cwd)
			if err != nil {
				return err
			}

			// Step 5: emit report.
			reportPath, reportErr := initSvc.EmitReport(cmd.Context(), applied)
			if reportErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: no se pudo escribir el reporte: %v\n", reportErr)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "\nReporte: %s\n", reportPath)
			}

			if len(applied.Cleanup.Errors) > 0 {
				// Exit code 2 signals a partial migration: some cleanup steps failed
				// but the DB migration succeeded. The user should inspect the report
				// and decide whether to re-run. We call os.Exit directly because Cobra
				// always translates a non-nil RunE error to exit 1; there is no way to
				// produce exit 2 through the normal error return path.
				fmt.Fprintf(cmd.ErrOrStderr(), "Error: migración parcial — ver reporte en %s\n", reportPath)
				os.Exit(2)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagApply, "apply", false, "Execute the migration (default is dry-run)")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompt (only with --apply)")

	return cmd
}

// printPlan renders the InitPlan to w as a human-readable ASCII table.
func printPlan(w io.Writer, plan service.InitPlan) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SOURCE\tKIND\tCLASSIFICATION\tDESTINATION")
	fmt.Fprintln(tw, "------\t----\t--------------\t-----------")
	for _, a := range plan.Artifacts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			shortenPath(a.Source), a.Kind, a.Classification, a.Destination)
	}
	_ = tw.Flush()

	if len(plan.Deletions) > 0 {
		fmt.Fprintf(w, "\nFilesystem cleanup (%d paths):\n", len(plan.Deletions))
		for _, d := range plan.Deletions {
			fmt.Fprintf(w, "  rm -rf %s\n", d)
		}
	}

	if len(plan.Rewrites) > 0 {
		fmt.Fprintln(w, "\nRewrites:")
		for _, r := range plan.Rewrites {
			fmt.Fprintf(w, "  %s\n", r)
		}
	}
}

// promptYes displays prompt on w and reads a line from r. Returns true only when
// the user types "y" or "Y". Any other input (including empty/Enter) returns false.
func promptYes(r io.Reader, w io.Writer, prompt string) bool {
	fmt.Fprint(w, prompt)
	scanner := bufio.NewScanner(r)
	if scanner.Scan() {
		answer := strings.TrimSpace(scanner.Text())
		return strings.EqualFold(answer, "y")
	}
	return false
}

// shortenPath trims the home directory prefix to ~ for readability.
func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
