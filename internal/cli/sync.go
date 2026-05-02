package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	mnemeSync "github.com/juanftp/mneme/internal/sync"
)

// newSyncCmd returns the "mneme sync" subcommand group. Child commands handle
// exporting, importing, and inspecting the sync manifest that drives git-based
// memory sharing across team members.
func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync memories via git",
		Long: `Export and import mneme memories as compressed JSONL archives that can
be committed to a git repository and shared across team members.

Use 'mneme sync export' to write the current project's memories to disk, then
commit the resulting .jsonl.gz file. Team members run 'mneme sync import' to
ingest those memories into their own database.`,
	}

	cmd.AddCommand(
		newSyncExportCmd(),
		newSyncImportCmd(),
		newSyncStatusCmd(),
	)

	return cmd
}

// newSyncExportCmd returns the "mneme sync export" subcommand. It exports all
// active memories (and optionally entities, relations, sessions) for the
// detected project to a compressed archive and updates the sync manifest.
func newSyncExportCmd() *cobra.Command {
	var flagDir string
	var flagFormat string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export project memories to JSONL.gz or manifest.tar.gz",
		Long: `Export all active memories for the current project to a compressed archive.

Default format (--format jsonl):
  Writes <dir>/.mneme/sync/<project-slug>.jsonl.gz — memories only.
  Suitable for committing to a git repository and sharing with team members.

Manifest format (--format manifest):
  Writes <dir>/.mneme/sync/<project-slug>.manifest.tar.gz — memories, entities,
  relations, and sessions together as a Memory Manifest v1.0 archive.
  Use this format for full-fidelity interchange with other tools.`,
		Example: `  mneme sync export
  mneme sync export --format manifest
  mneme sync export --format jsonl --dir /path/to/repo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			// Resolve the output directory.
			dir := flagDir
			if dir == "" {
				if dir, err = os.Getwd(); err != nil {
					return fmt.Errorf("sync export: determine working directory: %w", err)
				}
			}
			dir, err = filepath.Abs(dir)
			if err != nil {
				return fmt.Errorf("sync export: resolve directory path: %w", err)
			}

			project := svc.ProjectSlug()

			// Dispatch based on format.
			switch strings.ToLower(flagFormat) {
			case "manifest":
				path, result, exportErr := svc.ExportManifestToFile(cmd.Context(), dir, Version)
				if exportErr != nil {
					return fmt.Errorf("sync export: %w", exportErr)
				}
				fmt.Fprintf(os.Stdout, "Exported %d memories, %d entities, %d relations, %d sessions to %s\n",
					result.MemoryCount, result.EntityCount, result.RelationCount, result.SessionCount,
					shortenHome(path))
				fmt.Fprintf(os.Stdout, "Project: %s  |  Format: manifest  |  Exported at: %s\n",
					project, result.ExportedAt)
				return nil

			default: // "jsonl" or empty
				path, result, exportErr := svc.ExportToFile(cmd.Context(), dir)
				if exportErr != nil {
					return fmt.Errorf("sync export: %w", exportErr)
				}

				manifest, manifestErr := mnemeSync.LoadManifest(dir)
				if manifestErr != nil {
					return fmt.Errorf("sync export: load manifest: %w", manifestErr)
				}
				manifest.AddExport(mnemeSync.ExportEntry{
					Project:    result.Project,
					File:       filepath.Join(".mneme", "sync", filepath.Base(path)),
					Count:      result.Count,
					ExportedAt: result.ExportedAt,
				})
				if saveErr := manifest.Save(dir); saveErr != nil {
					return fmt.Errorf("sync export: save manifest: %w", saveErr)
				}

				fmt.Fprintf(os.Stdout, "Exported %d memories to %s\n", result.Count, shortenHome(path))
				fmt.Fprintf(os.Stdout, "Project: %s  |  Exported at: %s\n", project, result.ExportedAt)
				return nil
			}
		},
	}

	cmd.Flags().StringVar(&flagDir, "dir", "", "Output directory (default: current directory)")
	cmd.Flags().StringVar(&flagFormat, "format", "jsonl", "Export format: jsonl (default) or manifest")

	return cmd
}


// newSyncImportCmd returns the "mneme sync import" subcommand. It imports
// records from a JSONL.gz or manifest.tar.gz archive into the project database,
// auto-detecting the format from the file name and content.
func newSyncImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import memories (and entities/relations/sessions) from a sync archive",
		Long: `Import records from a compressed sync archive into the project database.

The format is auto-detected:
  .jsonl.gz             — legacy JSONL format, imports memories only
  .manifest.tar.gz      — Memory Manifest v1.0, imports memories + entities +
                          relations + sessions

Import is idempotent: memories with a TopicKey are merged by that key; memories
without a TopicKey are skipped if they already exist by ID. Entities, relations,
and sessions use their own deduplication keys.`,
		Example: `  mneme sync import .mneme/sync/my-project.jsonl.gz
  mneme sync import .mneme/sync/my-project.manifest.tar.gz
  mneme sync import /path/to/shared-memories.manifest.tar.gz`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			result, err := svc.ImportManifestFromFile(cmd.Context(), path)
			if err != nil {
				return fmt.Errorf("sync import: %w", err)
			}

			// Always print memory counts. Print entity/relation/session counts
			// only when any non-zero value was recorded (i.e., manifest format).
			fmt.Fprintf(os.Stdout, "Memories: %d created, %d updated, %d skipped\n",
				result.MemoriesCreated, result.MemoriesUpdated, result.MemoriesSkipped)
			if result.EntitiesCreated+result.EntitiesSkipped+
				result.RelationsCreated+result.RelationsSkipped+
				result.SessionsCreated+result.SessionsSkipped > 0 {
				fmt.Fprintf(os.Stdout, "Entities:  %d created, %d skipped\n",
					result.EntitiesCreated, result.EntitiesSkipped)
				fmt.Fprintf(os.Stdout, "Relations: %d created, %d skipped\n",
					result.RelationsCreated, result.RelationsSkipped)
				fmt.Fprintf(os.Stdout, "Sessions:  %d created, %d skipped\n",
					result.SessionsCreated, result.SessionsSkipped)
			}
			return nil
		},
	}

	return cmd
}

// newSyncStatusCmd returns the "mneme sync status" subcommand. It reads the
// sync manifest and displays the last export info for the current project.
func newSyncStatusCmd() *cobra.Command {
	var flagDir string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show sync status",
		Long: `Display the last export information for the current project from the sync
manifest at <dir>/.mneme/sync/manifest.json.

Reports the project slug, archive file path, memory count, and the timestamp
of the most recent export. If no export has been recorded a short notice is
printed instead.`,
		Example: `  mneme sync status
  mneme sync status --dir /path/to/repo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			project := svc.ProjectSlug()

			dir := flagDir
			if dir == "" {
				var cwdErr error
				if dir, cwdErr = os.Getwd(); cwdErr != nil {
					return fmt.Errorf("sync status: determine working directory: %w", cwdErr)
				}
			}
			var absErr error
			if dir, absErr = filepath.Abs(dir); absErr != nil {
				return fmt.Errorf("sync status: resolve directory path: %w", absErr)
			}

			manifest, err := mnemeSync.LoadManifest(dir)
			if err != nil {
				return fmt.Errorf("sync status: load manifest: %w", err)
			}

			// Find the entry for the current project.
			for _, entry := range manifest.Exports {
				if entry.Project == project {
					fmt.Fprintf(os.Stdout, "Project:     %s\n", entry.Project)
					fmt.Fprintf(os.Stdout, "File:        %s\n", entry.File)
					fmt.Fprintf(os.Stdout, "Memories:    %d\n", entry.Count)
					fmt.Fprintf(os.Stdout, "Exported at: %s\n", entry.ExportedAt)
					return nil
				}
			}

			if project == "" {
				fmt.Fprintln(os.Stdout, "No project detected and no export found.")
			} else {
				fmt.Fprintf(os.Stdout, "No export recorded for project %q.\n", project)
				fmt.Fprintln(os.Stdout, "Run 'mneme sync export' to create one.")
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&flagDir, "dir", "", "Directory containing the manifest (default: current directory)")

	return cmd
}
