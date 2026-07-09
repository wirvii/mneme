package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/install"
	"github.com/wirvii/mneme/internal/service"
)

// newInitCmd returns the "mneme init" subcommand.
//
// Default behaviour (no flags): applies managed blocks to ~/.claude/CLAUDE.md
// and <repo>/CLAUDE.md, prints drift findings, and shows the legacy migration
// plan in dry-run mode (does not touch the DB or run rm-rf).
//
// --check: all report mode — does not write any managed blocks, only reports.
// --apply: also executes the destructive legacy migration (rm-rf, DB writes).
// --yes:   skip confirmation prompt (only with --apply).
func newInitCmd() *cobra.Command {
	var flagApply, flagCheck, flagYes bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialise a project with mneme managed blocks and show drift report",
		Long: `mneme init sets up the mneme managed blocks for a project and reports drift.

Default: applies managed blocks (global manual + repo block), prints drift
findings, and shows the legacy migration plan in dry-run mode.

--check: report-only mode — no files are written, only findings printed.
--apply: also executes the destructive legacy migration (DB writes + rm-rf).
--yes:   skip confirmation prompt (only with --apply).

The command is idempotent: re-running on an already-configured project is safe.`,
		Example: `  mneme init                  # apply blocks + drift report + dry-run plan
  mneme init --check          # report-only (no writes)
  mneme init --apply          # also execute legacy migration (asks confirmation)
  mneme init --apply --yes    # execute without prompt (script-safe)`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flagYes && !flagApply {
				fmt.Fprintln(cmd.ErrOrStderr(), "Warning: --yes has no effect without --apply. Ignored.")
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

			opts := service.InitServiceOptions{}
			if !flagCheck {
				// Wire the real managed-block primitive from internal/install.
				opts.UpsertBlock = install.UpsertManagedBlock
				opts.ManualContent = install.OperatingManual
			}

			initSvc := service.NewInitService(cfg, sddSvc, memSvc, sddSvc.ProjectSlug(), opts)

			// Step 1: greenfield scaffold (no-op if CLAUDE.md already exists).
			if !flagCheck {
				if err := initSvc.EnsureGreenfieldScaffold(cwd); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: greenfield scaffold: %v\n", err)
				}
			}

			// Step 2: ensure global manual present.
			if !flagCheck {
				if err := initSvc.EnsureGlobalManual(); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: ensure global manual: %v\n", err)
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "[ok] Global operating manual present (~/.claude/CLAUDE.md)")
				}
			}

			// Step 3: upsert repo block.
			if !flagCheck {
				if err := initSvc.UpsertRepoBlock(cwd); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: repo block: %v\n", err)
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "[ok] Repo managed block present (CLAUDE.md)")
				}
			}

			// Step 4: drift report (always, even in --check mode).
			findings, driftErr := initSvc.RunDrift(cwd)
			if driftErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: drift detection: %v\n", driftErr)
			}
			if len(findings) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "\n[drift] Advisory findings in CLAUDE.md:")
				for _, f := range findings {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", f)
				}
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "[ok] No drift detected in CLAUDE.md")
			}

			// Step 5: always compute the legacy migration plan (dry-run view).
			report, err := initSvc.Plan(cmd.Context(), cwd)
			if err != nil {
				return fmt.Errorf("plan: %w", err)
			}

			if len(report.Plan.Artifacts) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "\n[legacy] Migration plan (dry-run):")
				printPlan(cmd.OutOrStdout(), report.Plan)
			}

			if !flagApply {
				if len(report.Plan.Artifacts) > 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "\nUse --apply to execute the legacy migration.")
				}
				return nil
			}

			// --apply: execute the destructive legacy migration.
			if !flagYes {
				if !promptYes(cmd.InOrStdin(), cmd.OutOrStdout(), "¿Ejecutar migración legacy? [y/N] ") {
					fmt.Fprintln(cmd.OutOrStdout(), "Cancelado.")
					return nil
				}
			}

			applied, err := initSvc.Apply(cmd.Context(), cwd)
			if err != nil {
				return err
			}

			reportPath, reportErr := initSvc.EmitReport(cmd.Context(), applied)
			if reportErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: no se pudo escribir el reporte: %v\n", reportErr)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "\nReporte: %s\n", reportPath)
			}

			if len(applied.Cleanup.Errors) > 0 {
				// Exit code 2 signals a partial migration: some cleanup steps failed
				// but the DB migration succeeded. The user should inspect the report
				// and decide whether to re-run.
				fmt.Fprintf(cmd.ErrOrStderr(), "Error: migración parcial — ver reporte en %s\n", reportPath)
				os.Exit(2)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagApply, "apply", false, "Also execute the legacy migration (DB writes + rm-rf)")
	cmd.Flags().BoolVar(&flagCheck, "check", false, "Report-only mode: no files are written")
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
