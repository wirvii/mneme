package cli

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
)

// Table layout of the sweep (SPEC-187, PL9). Each column has an explicit
// separator so a project or path longer than its width shifts the rest of
// the row instead of gluing two columns together. fmt pads by runes, so the
// accented header SÍNTESIS still takes eight cells.
const (
	sweepRowFormat     = "%-16s %-29s %8s  %8s  %8s"
	sweepRetiredFormat = "  %9s"
	sweepGlobalLabel   = "(global)"
	sweepTotalLabel    = "TOTAL"
)

// newSynthesisSweepCmd returns "mneme mem synthesis-sweep" (SPEC-187, D3):
// it measures the automatic synthesis memories of every database and, only
// with --apply, retires them. It is CLI only and a lifecycle command the
// enforce-delegation hook denies to subagents.
func newSynthesisSweepCmd() *cobra.Command {
	var (
		flagApply bool
		flagJSON  bool
	)

	cmd := &cobra.Command{
		Use:   "synthesis-sweep",
		Short: "Measure, and with --apply retire, automatic synthesis memories",
		Long: `Measure the synthesis memories that consolidation generated automatically,
per project: how many there are, how many were never read and how many bytes
they take. Nothing is written: this is a dry run by default, and the databases
are opened read-only.

With --apply the same syntheses are retired with a soft delete (deleted_at is
set), only rows of type synthesis and never any other type. Consolidation
deletes retired rows for good once retention_days have passed. There is no
--restore: to have syntheses again, set [graph] synthesis_enabled = true and
run mneme consolidate, which regenerates them from the communities.

Without --project every database under the data directory is visited
(projects/*.db except the code graph files, then global.db); the current
project is not auto-detected. --project accepts both forms of the slug
(org/repo or org-repo) and leaves global.db out.

This command is an act of the person or the orchestrator: the
enforce-delegation hook denies it to subagents, dry run included.`,
		Example: `  mneme mem synthesis-sweep
  mneme mem synthesis-sweep --project wirvii/mneme --json
  mneme mem synthesis-sweep --apply`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(config.DefaultPath())
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if flagDataDir != "" {
				cfg.Storage.DataDir = flagDataDir
			}

			report, sweepErr := service.SweepSyntheses(cmd.Context(), cfg, model.SynthesisSweepRequest{
				Project: flagProject,
				Apply:   flagApply,
			})
			if report == nil {
				return fmt.Errorf("mem synthesis-sweep: %w", sweepErr)
			}

			out := cmd.OutOrStdout()
			if flagJSON {
				if err := printJSON(out, report); err != nil {
					return err
				}
			} else {
				printSynthesisSweep(out, report, cfg, flagProject)
			}

			if sweepErr != nil {
				printSweepErrors(cmd.ErrOrStderr(), report.Errors)
				return fmt.Errorf("mem synthesis-sweep: %w", sweepErr)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagApply, "apply", false, "Retire the syntheses found (soft delete); without it nothing changes")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output the report as JSON")
	return cmd
}

// printSynthesisSweep renders the human report, in Spanish because a person
// reads it and the command has no earlier English panel to stay consistent
// with.
func printSynthesisSweep(w io.Writer, report *model.SynthesisSweepReport, cfg *config.Config, projectFlag string) {
	if report.Apply {
		fmt.Fprintln(w, "Barrido de síntesis — aplicado")
	} else {
		fmt.Fprintln(w, "Barrido de síntesis — en seco: no se cambió nada.")
	}

	header := fmt.Sprintf(sweepRowFormat, "PROYECTO", "BASE", "SÍNTESIS", "SIN LEER", "BYTES")
	if report.Apply {
		header += fmt.Sprintf(sweepRetiredFormat, "RETIRADAS")
	}
	fmt.Fprintln(w, header)

	for _, r := range report.Rows {
		label := r.Project
		if label == "" {
			label = sweepGlobalLabel
		}
		fmt.Fprintln(w, sweepRow(label, r.Database, r, report.Apply))
	}
	fmt.Fprintln(w, sweepRow(sweepTotalLabel, "", report.Totals(), report.Apply))

	if projectFlag != "" && len(report.Rows) == 0 && len(report.Errors) == 0 {
		fmt.Fprintf(w, "La base existe pero no tiene síntesis con project = %s (ni su forma equivalente).\n", projectFlag)
	}

	if !report.Apply {
		fmt.Fprintln(w, "Para retirarlas (borrado en blando, solo memorias de tipo synthesis): mneme mem synthesis-sweep --apply")
		return
	}
	fmt.Fprintf(w, "La consolidación borra de forma definitiva las filas retiradas cuando pasan retention_days (%d días).\n", cfg.Consolidation.RetentionDays)
	fmt.Fprintln(w, "Para volver a tenerlas: [graph] synthesis_enabled = true y `mneme consolidate` las regeneran desde las comunidades.")
	if cfg.Graph.SynthesisEnabled {
		fmt.Fprintln(w, "Aviso: synthesis_enabled está en true; la próxima consolidación volverá a generarlas.")
	}
}

// sweepRow formats one table line with the PL9 widths.
func sweepRow(project, database string, r model.SynthesisSweepRow, apply bool) string {
	line := fmt.Sprintf(sweepRowFormat, project, database,
		strconv.Itoa(r.Synthesis), strconv.Itoa(r.NeverAccessed), strconv.FormatInt(r.Bytes, 10))
	if apply {
		line += fmt.Sprintf(sweepRetiredFormat, strconv.Itoa(r.Retired))
	}
	return line
}

// printSweepErrors lists the databases that failed, after the table, so the
// person sees what was measured before what was not.
func printSweepErrors(w io.Writer, errs []model.SynthesisSweepError) {
	for _, e := range errs {
		fmt.Fprintf(w, "error: %s: %s\n", e.Database, e.Error)
	}
}
