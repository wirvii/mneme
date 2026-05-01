package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/model"
)

// gapsListJSON is the versioned JSON wrapper emitted by "mneme gaps --json".
// The version field lets consumers detect schema changes without introspection.
type gapsListJSON struct {
	Version string     `json:"version"`
	Gaps    []model.Gap `json:"gaps"`
	Total   int        `json:"total"`
}

// newGapsCmd returns the "mneme gaps" top-level command. It queries aggregated
// knowledge gaps from the unresolved_references table and renders them either
// as a fixed-width table (default) or a versioned JSON envelope (--json).
func newGapsCmd() *cobra.Command {
	var (
		flagScope    string
		flagLimit    int
		flagMinCount int
		flagJSON     bool
	)

	cmd := &cobra.Command{
		Use:   "gaps",
		Short: "List knowledge gaps (unresolved [[wikilinks]])",
		Long: `List aggregated knowledge gaps — topic_keys referenced via [[wikilinks]]
in existing memories but not yet backed by a memory of their own.

Each gap shows how many times it has been mentioned (MENTIONS), how many
distinct memories reference it (SOURCES), and when it was last seen. Use
--json for a versioned JSON envelope suitable for programmatic consumption.`,
		Example: `  mneme gaps
  mneme gaps --scope all --limit 50
  mneme gaps --min-count 3
  mneme gaps --json | jq '.gaps[].target_topic_key'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := model.GapsRequest{
				Scope:       flagScope,
				Limit:       flagLimit,
				MinMentions: flagMinCount,
			}

			resp, err := svc.Gaps(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("gaps: %w", err)
			}

			if flagJSON {
				return printGapsJSON(os.Stdout, resp)
			}

			return printGapsTable(os.Stdout, resp)
		},
	}

	cmd.Flags().StringVarP(&flagScope, "scope", "s", "project", "Query scope: project, global, or all")
	cmd.Flags().IntVarP(&flagLimit, "limit", "n", 20, "Maximum number of gaps to return")
	cmd.Flags().IntVar(&flagMinCount, "min-count", 1, "Minimum total_mentions to include a gap")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as versioned JSON envelope")

	_ = cmd.RegisterFlagCompletionFunc("scope", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"project", "global", "all"}, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// printGapsTable renders the gaps as a plain fixed-width table with a bold
// header. No lipgloss colors are applied — the numeric data is self-explanatory.
// The table has four columns: TARGET_TOPIC_KEY (38 chars), MENTIONS (8),
// SOURCES (7), LAST SEEN (9, relative time).
func printGapsTable(w io.Writer, resp *model.GapsResponse) error {
	if len(resp.Gaps) == 0 {
		fmt.Fprintln(w, "No knowledge gaps found.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Gaps appear when memories reference [[topic_keys]] that don't exist yet.")
		return nil
	}

	bold := lipgloss.NewStyle().Bold(true)

	// Header.
	fmt.Fprintf(w, "%s  %s  %s  %s\n",
		bold.Render(fmt.Sprintf("%-38s", "TARGET_TOPIC_KEY")),
		bold.Render(fmt.Sprintf("%8s", "MENTIONS")),
		bold.Render(fmt.Sprintf("%7s", "SOURCES")),
		bold.Render(fmt.Sprintf("%-9s", "LAST SEEN")),
	)
	fmt.Fprintf(w, "%s  %s  %s  %s\n",
		"──────────────────────────────────────",
		"────────",
		"───────",
		"─────────",
	)

	for _, g := range resp.Gaps {
		key := g.TargetTopicKey
		if len(key) > 35 {
			key = key[:35] + "..."
		}
		fmt.Fprintf(w, "%-38s  %8d  %7d  %-9s\n",
			key,
			g.TotalMentions,
			g.SourceCount,
			relativeTime(g.LastSeenAt),
		)
	}

	total := resp.Total
	shown := len(resp.Gaps)
	var totalMentions int
	for _, g := range resp.Gaps {
		totalMentions += g.TotalMentions
	}

	fmt.Fprintln(w)
	if shown == total {
		fmt.Fprintf(w, "%d knowledge %s | %d total mentions\n",
			total, pluralize(total, "gap", "gaps"), totalMentions)
	} else {
		fmt.Fprintf(w, "%d of %d knowledge gaps shown | %d total mentions\n",
			shown, total, totalMentions)
	}
	return nil
}

// printGapsJSON emits a versioned JSON envelope to w. The envelope contains a
// "version" field so consumers can detect future schema changes.
func printGapsJSON(w io.Writer, resp *model.GapsResponse) error {
	out := gapsListJSON{
		Version: "1",
		Gaps:    resp.Gaps,
		Total:   resp.Total,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// relativeTime converts t to a human-readable relative duration string:
//   - < 1 hour:  "Xm ago"
//   - < 24 hrs:  "Xh ago"
//   - < 30 days: "Xd ago"
//   - >= 30 days:"Xmo ago"
func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	}
}

// pluralize returns singular when n==1, plural otherwise.
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
