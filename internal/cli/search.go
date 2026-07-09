package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
)

// newSearchCmd returns the "mneme search" subcommand. It performs a full-text
// search across the project's memories and prints results in a human-readable
// table or JSON depending on flags.
func newSearchCmd() *cobra.Command {
	var (
		flagScope   string
		flagType    string
		flagLimit   int
		flagFull    bool
		flagJSON    bool
		flagGraph   bool
		flagNoGraph bool
	)

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search memories",
		Long: `Search persistent memory using full-text search.

Results are ranked by a combined score that blends BM25 text relevance,
memory importance, and recency. Use --full to see complete memory content
below each result row.`,
		Example: `  mneme search "JWT RS256 auth"
  mneme search "N+1 query" --type bugfix --full
  mneme search "patterns" --json
  mneme search "patterns" --no-graph`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			// --no-graph takes priority over --graph when both are specified.
			// Default behaviour (neither flag set) leaves IncludeGraph nil,
			// which lets the service fall back to the config default (true).
			includeGraph := true
			if flagNoGraph {
				includeGraph = false
			} else if cmd.Flags().Changed("graph") {
				includeGraph = flagGraph
			}

			req := model.SearchRequest{
				Query:        query,
				Limit:        flagLimit,
				IncludeGraph: &includeGraph,
			}
			if flagScope != "" && flagScope != "all" {
				scope := model.Scope(flagScope)
				req.Scope = &scope
			}
			if flagType != "" {
				memType := model.MemoryType(flagType)
				req.Type = &memType
			}

			resp, err := svc.Search(cmd.Context(), req)
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, resp)
			}

			if len(resp.Results) == 0 {
				fmt.Fprintln(os.Stdout, "No results found.")
				return nil
			}

			// Table header with fixed-width columns for readability in terminals.
			fmt.Fprintf(os.Stdout, "%-10s  %-12s  %-38s  %s\n", "ID", "TYPE", "TITLE", "SCORE")
			fmt.Fprintf(os.Stdout, "%-10s  %-12s  %-38s  %s\n",
				"----------", "------------", "--------------------------------------", "-----")

			for _, r := range resp.Results {
				// Display the first 8 characters of the UUID for compact display.
				shortID := r.ID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}

				title := r.Title
				if len(title) > 38 {
					title = title[:35] + "..."
				}

				memType := string(r.Type)
				if len(memType) > 12 {
					memType = memType[:12]
				}

				fmt.Fprintf(os.Stdout, "%-10s  %-12s  %-38s  %.2f\n",
					shortID, memType, title, r.RelevanceScore)

				if flagFull {
					fmt.Fprintln(os.Stdout)
					fmt.Fprintln(os.Stdout, r.Content)
					fmt.Fprintln(os.Stdout)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&flagScope, "scope", "s", "all", "Scope filter: global, org, project, or all")
	cmd.Flags().StringVarP(&flagType, "type", "T", "", "Type filter")
	cmd.Flags().IntVarP(&flagLimit, "limit", "n", 10, "Max results")
	cmd.Flags().BoolVar(&flagFull, "full", false, "Show full content below each result")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&flagGraph, "graph", true, "Enable 1-hop graph expansion in search results")
	cmd.Flags().BoolVar(&flagNoGraph, "no-graph", false, "Disable 1-hop graph expansion (overrides --graph)")

	return cmd
}
