package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
)

// newGraphCmd returns the "mneme graph" parent command. It groups subcommands
// related to the knowledge graph — currently only "rebuild" (SPEC-009).
func newGraphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Manage the knowledge graph",
		Long: `Commands for managing mneme's knowledge graph.

The graph stores entities (files, modules, concepts) and typed relations
between them. Use "graph rebuild" to bootstrap the graph from existing
memories using entity extraction heuristics.`,
	}

	cmd.AddCommand(newGraphRebuildCmd())
	cmd.AddCommand(newGraphCleanupOrphanCmd())
	return cmd
}

// newGraphCleanupOrphanCmd returns the "mneme graph cleanup-orphan-relations"
// command (SPEC-031). It detects relations whose source or target entity has
// no row in memory_entities — these are unreachable from mem_explore and were
// created by the legacy mem_relate path before the SPEC-031 fix.
//
// Default behaviour is dry-run for safety: the command lists candidates
// without deleting anything. Use --apply to actually delete. The optional
// --also-delete-entities flag additionally removes entities that became fully
// unreferenced after their relations were dropped.
func newGraphCleanupOrphanCmd() *cobra.Command {
	var (
		flagScope             string
		flagApply             bool
		flagAlsoDeleteEnts    bool
		flagOutput            string
		flagYes               bool
	)

	cmd := &cobra.Command{
		Use:   "cleanup-orphan-relations",
		Short: "Remove relations not connected to any memory (SPEC-031)",
		Long: `Detects and optionally deletes relations whose source or target
entity has no link to any memory through memory_entities. Such relations
are unreachable from mem_explore and were created by the legacy mem_relate
path before the SPEC-031 fix.

By default this command runs in dry-run mode and only lists candidates.
Use --apply to actually delete. Use --also-delete-entities to additionally
remove entities that become fully unreferenced.

After cleanup, run "mneme graph rebuild" to regenerate entity links from
existing wikilinks and heuristics:

    mneme graph cleanup-orphan-relations --apply
    mneme graph rebuild --force`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return fmt.Errorf("graph cleanup-orphan-relations: init service: %w", err)
			}
			defer cleanup()

			ctx := context.Background()

			if flagApply && !flagYes {
				fmt.Fprintln(os.Stderr, "Refusing to apply without --yes. Re-run with --apply --yes to proceed.")
				return fmt.Errorf("graph cleanup-orphan-relations: --apply requires --yes")
			}

			req := service.CleanupOrphanRelationsRequest{
				Project:            svc.ProjectSlug(),
				Scope:              flagScope,
				DryRun:             !flagApply,
				AlsoDeleteEntities: flagAlsoDeleteEnts,
			}

			result, err := svc.CleanupOrphanRelations(ctx, req)
			if err != nil {
				return fmt.Errorf("graph cleanup-orphan-relations: %w", err)
			}

			switch flagOutput {
			case "json":
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if encErr := enc.Encode(result); encErr != nil {
					return fmt.Errorf("graph cleanup-orphan-relations: encode json: %w", encErr)
				}
			default:
				printCleanupOrphanResult(result)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&flagScope, "scope", "s", "project", "Scope to scan: project, global, or all")
	cmd.Flags().BoolVar(&flagApply, "apply", false, "Actually delete (default is dry-run)")
	cmd.Flags().BoolVar(&flagAlsoDeleteEnts, "also-delete-entities", false, "Also delete entities that become fully unreferenced")
	cmd.Flags().StringVarP(&flagOutput, "output", "o", "text", "Output format: text or json")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Confirm destructive deletion (required with --apply)")
	return cmd
}

// printCleanupOrphanResult writes a human-readable summary of a cleanup pass.
func printCleanupOrphanResult(result *service.CleanupOrphanRelationsResult) {
	if result.DryRun {
		fmt.Fprintln(os.Stdout, "Dry-run mode — no changes were written.")
	}
	fmt.Fprintf(os.Stdout, "Orphan relations found: %d\n", result.OrphanRelationsFound)
	if !result.DryRun {
		fmt.Fprintf(os.Stdout, "Relations deleted:      %d\n", result.RelationsDeleted)
		fmt.Fprintf(os.Stdout, "Entities deleted:       %d\n", result.EntitiesDeleted)
	}
	if len(result.Examples) == 0 {
		return
	}
	fmt.Fprintln(os.Stdout, "\nExamples:")
	for _, ex := range result.Examples {
		fmt.Fprintf(os.Stdout, "  - %s --[%s]--> %s\n", ex.SourceName, ex.Type, ex.TargetName)
	}
	if result.OrphanRelationsFound > len(result.Examples) {
		fmt.Fprintf(os.Stdout, "  ... and %d more.\n", result.OrphanRelationsFound-len(result.Examples))
	}
}

// newGraphRebuildCmd returns the "mneme graph rebuild" command which extracts
// entities from existing memories, links them, and creates co-occurrence
// related_to relations between memories that share >= K entities.
//
// The rebuild is idempotent: existing entities and links are skipped; existing
// relations are not duplicated. Use --force to delete and regenerate all
// related_to relations (explicit relation types are never touched).
func newGraphRebuildCmd() *cobra.Command {
	var (
		flagScope      string
		flagMinShared  int
		flagMaxRels    int
		flagBatchSize  int
		flagForce      bool
		flagDryRun     bool
	)

	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Backfill the knowledge graph from existing memories",
		Long: `Iterates over all active memories and extracts entities using four
heuristics:
  H1: topic_key  — each memory with a topic_key becomes a concept entity
  H2: file paths — recognises paths like internal/store/entity.go
  H3: code symbols — func/type/struct declarations in code blocks
  H4: wikilinks  — [[topic_key]] references

Memories that share >= --min-shared entities receive a related_to relation
with weight = min(0.5, shared_count * 0.1).

This command is safe to re-run — it is idempotent. Use --force to
regenerate all related_to relations from scratch (only related_to is
deleted; depends_on, implements, and other explicit relation types are
never touched).`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return fmt.Errorf("graph rebuild: init service: %w", err)
			}
			defer cleanup()

			ctx := context.Background()
			project := svc.ProjectSlug()

			// Print header.
			if flagDryRun {
				fmt.Fprintf(os.Stdout, "Dry run — no changes will be written.\n\n")
			}
			fmt.Fprintf(os.Stdout, "Starting graph rebuild for project %q...\n", project)
			fmt.Fprintf(os.Stdout, "  Scope:       %s\n", flagScope)
			fmt.Fprintf(os.Stdout, "  Min shared:  %d\n", flagMinShared)
			fmt.Fprintf(os.Stdout, "  Force:       %v\n", flagForce)
			fmt.Fprintf(os.Stdout, "  Batch size:  %d\n\n", flagBatchSize)

			var lastPhase string
			start := time.Now()

			result, err := svc.RebuildGraph(ctx, model.RebuildRequest{
				Project:               project,
				Scope:                 flagScope,
				MinShared:             flagMinShared,
				MaxRelationsPerMemory: flagMaxRels,
				BatchSize:             flagBatchSize,
				Force:                 flagForce,
				DryRun:                flagDryRun,
				ProgressFn: func(phase string, current, total int) {
					if phase != lastPhase {
						switch phase {
						case "extraction":
							fmt.Fprintf(os.Stdout, "Phase 1: Entity extraction\n")
						case "relations":
							fmt.Fprintf(os.Stdout, "\nPhase 2: Relation generation\n")
						}
						lastPhase = phase
					}
					if total > 0 {
						pct := 100 * current / total
						fmt.Fprintf(os.Stdout, "\r  [%3d%%] (%d/%d)", pct, current, total)
					}
				},
			})
			if err != nil {
				return fmt.Errorf("graph rebuild: %w", err)
			}

			if lastPhase != "" {
				fmt.Fprintln(os.Stdout) // newline after progress line
			}

			elapsed := time.Since(start).Round(time.Millisecond)

			if result.MemoriesScanned == 0 {
				fmt.Fprintf(os.Stdout, "\nNothing to do — all memories already have entity links.\n")
				return nil
			}

			fmt.Fprintf(os.Stdout, "\nRebuild complete in %s:\n", elapsed)
			fmt.Fprintf(os.Stdout, "  Memories scanned:     %6d\n", result.MemoriesScanned)
			fmt.Fprintf(os.Stdout, "  Entities extracted:   %6d\n", result.EntitiesExtracted)
			fmt.Fprintf(os.Stdout, "  New entities:         %6d\n", result.EntitiesCreated)
			fmt.Fprintf(os.Stdout, "  Existing entities:    %6d\n", result.EntitiesExisting)
			fmt.Fprintf(os.Stdout, "  Memory-entity links:  %6d\n", result.LinksCreated)
			if result.RelationsDeleted > 0 {
				fmt.Fprintf(os.Stdout, "  Relations deleted:    %6d (--force)\n", result.RelationsDeleted)
			}
			fmt.Fprintf(os.Stdout, "  Relations created:    %6d\n", result.RelationsCreated)
			if result.RelationsExisting > 0 {
				fmt.Fprintf(os.Stdout, "  Relations skipped:    %6d (existing)\n", result.RelationsExisting)
			}
			if result.RelationsSkippedCap > 0 {
				fmt.Fprintf(os.Stdout, "  Relations skipped:    %6d (cap %d)\n", result.RelationsSkippedCap, flagMaxRels)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&flagScope, "scope", "s", "project", "Scope to rebuild: project, global, or all")
	cmd.Flags().IntVarP(&flagMinShared, "min-shared", "k", 2, "Minimum shared entities required to create a relation")
	cmd.Flags().IntVar(&flagMaxRels, "max-relations", 50, "Maximum relations created per memory")
	cmd.Flags().IntVarP(&flagBatchSize, "batch-size", "b", 500, "Number of memories processed per transaction")
	cmd.Flags().BoolVarP(&flagForce, "force", "f", false, "Delete existing related_to relations before rebuilding")
	cmd.Flags().BoolVarP(&flagDryRun, "dry-run", "n", false, "Preview changes without writing to the database")

	return cmd
}
