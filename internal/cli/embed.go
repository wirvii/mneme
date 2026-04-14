package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// newEmbedCmd returns the "mneme embed" parent command with subcommands
// for managing embeddings. Currently the only subcommand is "backfill".
func newEmbedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "embed",
		Short: "Manage memory embeddings for semantic search",
		Long: `Commands for managing the vector embeddings used by mneme's semantic
search pipeline. Use "embed backfill" to generate embeddings for existing
memories that were saved before the TF-IDF embedder was active.`,
	}

	cmd.AddCommand(newEmbedBackfillCmd())
	return cmd
}

// newEmbedBackfillCmd returns the "mneme embed backfill" command which generates
// embeddings for active memories that do not yet have one stored.
func newEmbedBackfillCmd() *cobra.Command {
	var batchSize int

	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "Generate embeddings for existing memories that lack one",
		Long: `Iterates over all active memories that do not have a stored embedding
and generates one using the configured embedder. Progress is printed to stdout.

This command is safe to interrupt and re-run — it is idempotent. Memories that
already have an embedding are skipped.

When the configured embedding provider is "none", the command exits immediately
with a message.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return fmt.Errorf("embed backfill: init service: %w", err)
			}
			defer cleanup()

			ctx := context.Background()
			project := svc.ProjectSlug()

			start := time.Now()
			fmt.Fprintf(os.Stdout, "Starting embedding backfill for project %q...\n", project)

			result, err := svc.EmbedBackfill(ctx, project, batchSize, func(title string, current, total int) {
				pct := 100 * current / total
				fmt.Fprintf(os.Stdout, "  [%3d%%] (%d/%d) %s\n", pct, current, total, truncate(title, 60))
			})
			if err != nil {
				return fmt.Errorf("embed backfill: %w", err)
			}

			elapsed := time.Since(start).Round(time.Millisecond)

			if result.Total == 0 {
				fmt.Fprintf(os.Stdout, "Nothing to do — all active memories already have embeddings.\n")
				return nil
			}

			fmt.Fprintf(os.Stdout, "\nBackfill complete in %s:\n", elapsed)
			fmt.Fprintf(os.Stdout, "  Total without embedding: %d\n", result.Total)
			fmt.Fprintf(os.Stdout, "  Successfully embedded:   %d\n", result.Embedded)
			if result.Failed > 0 {
				fmt.Fprintf(os.Stdout, "  Failed (see logs):       %d\n", result.Failed)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&batchSize, "batch-size", 100, "Number of memories to process per batch")

	return cmd
}

// truncate shortens s to at most maxLen runes, appending "…" when truncated.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}
