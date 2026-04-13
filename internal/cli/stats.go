package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/model"
)

// newStatsCmd returns the "mneme stats" subcommand. It queries aggregate
// statistics from the project store and prints a human-readable summary or
// JSON output depending on flags.
func newStatsCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show memory store statistics",
		Long: `Display aggregate statistics about the current project's memory store.

Reports memory counts grouped by type and scope, active vs. superseded vs.
forgotten tallies, database file size, oldest and newest memory timestamps,
and the average importance score across all active memories.`,
		Example: `  mneme stats
  mneme stats --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			project := svc.ProjectSlug()

			resp, err := svc.Stats(cmd.Context(), project)
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, resp)
			}

			printStats(os.Stdout, resp)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// printStats writes a human-readable representation of resp to w.
func printStats(w *os.File, resp *model.StatsResponse) {
	fmt.Fprintf(w, "Project: %s\n\n", resp.Project)

	fmt.Fprintf(w, "Memories\n")
	fmt.Fprintf(w, "  Total:      %d\n", resp.TotalMemories)
	fmt.Fprintf(w, "  Active:     %d\n", resp.Active)
	fmt.Fprintf(w, "  Superseded: %d\n", resp.Superseded)
	fmt.Fprintf(w, "  Forgotten:  %d\n\n", resp.Forgotten)

	if len(resp.ByType) > 0 {
		fmt.Fprintf(w, "By type\n")
		// Sort types for deterministic output.
		types := make([]string, 0, len(resp.ByType))
		for t := range resp.ByType {
			types = append(types, string(t))
		}
		sort.Strings(types)
		for _, t := range types {
			fmt.Fprintf(w, "  %-20s %d\n", t, resp.ByType[model.MemoryType(t)])
		}
		fmt.Fprintln(w)
	}

	if len(resp.ByScope) > 0 {
		fmt.Fprintf(w, "By scope\n")
		scopes := make([]string, 0, len(resp.ByScope))
		for s := range resp.ByScope {
			scopes = append(scopes, string(s))
		}
		sort.Strings(scopes)
		for _, s := range scopes {
			fmt.Fprintf(w, "  %-20s %d\n", s, resp.ByScope[model.Scope(s)])
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "Database\n")
	fmt.Fprintf(w, "  Size:       %s\n", formatBytes(resp.DBSizeBytes))

	if resp.OldestMemory != nil {
		fmt.Fprintf(w, "  Oldest:     %s\n", resp.OldestMemory.Format("2006-01-02 15:04:05 UTC"))
	}
	if resp.NewestMemory != nil {
		fmt.Fprintf(w, "  Newest:     %s\n", resp.NewestMemory.Format("2006-01-02 15:04:05 UTC"))
	}
	fmt.Fprintf(w, "  Avg import: %.3f\n", resp.AvgImportance)
}

// formatBytes converts a byte count into a human-readable string with an
// appropriate unit (B, KB, MB, GB).
func formatBytes(n int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.2f GB", float64(n)/gb)
	case n >= mb:
		return fmt.Sprintf("%.2f MB", float64(n)/mb)
	case n >= kb:
		return fmt.Sprintf("%.2f KB", float64(n)/kb)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
