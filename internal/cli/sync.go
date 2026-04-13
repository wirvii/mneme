package cli

import (
	"fmt"
	"os"
	"path/filepath"

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
// active memories for the detected project to a gzip-compressed JSONL archive
// and updates the sync manifest.
func newSyncExportCmd() *cobra.Command {
	var flagDir string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export project memories to JSONL.gz",
		Long: `Export all active memories for the current project to a compressed JSONL
archive at <dir>/.mneme/sync/<project-slug>.jsonl.gz.

The output file is suitable for committing to a git repository and importing
by other team members. Each run overwrites the previous archive for the same
project, keeping the repository diff minimal.`,
		Example: `  mneme sync export
  mneme sync export --dir /path/to/repo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			// Resolve the output directory: use the flag value when provided,
			// otherwise fall back to the current working directory.
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

			path, result, err := svc.ExportToFile(cmd.Context(), dir)
			if err != nil {
				return fmt.Errorf("sync export: %w", err)
			}

			// Update the sync manifest in the same directory.
			manifest, err := mnemeSync.LoadManifest(dir)
			if err != nil {
				return fmt.Errorf("sync export: load manifest: %w", err)
			}

			manifest.AddExport(mnemeSync.ExportEntry{
				Project:    result.Project,
				File:       filepath.Join(".mneme", "sync", filepath.Base(path)),
				Count:      result.Count,
				ExportedAt: result.ExportedAt,
			})

			if err := manifest.Save(dir); err != nil {
				return fmt.Errorf("sync export: save manifest: %w", err)
			}

			// Shorten home directory for readable output.
			displayPath := path
			if home, homeErr := os.UserHomeDir(); homeErr == nil && home != "" {
				if len(displayPath) > len(home) && displayPath[:len(home)] == home {
					displayPath = "~" + displayPath[len(home):]
				}
			}

			fmt.Fprintf(os.Stdout, "Exported %d memories to %s\n", result.Count, displayPath)
			fmt.Fprintf(os.Stdout, "Project: %s  |  Exported at: %s\n", project, result.ExportedAt)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagDir, "dir", "", "Output directory (default: current directory)")

	return cmd
}

// newSyncImportCmd returns the "mneme sync import" subcommand. It imports
// memories from a JSONL.gz archive into the project database.
func newSyncImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import memories from JSONL.gz",
		Long: `Import memories from a compressed JSONL archive into the project database.

Import is idempotent: memories with a TopicKey are merged by that key, and
memories without a TopicKey are skipped if they already exist by ID.`,
		Example: `  mneme sync import .mneme/sync/my-project.jsonl.gz
  mneme sync import /path/to/team-memories.jsonl.gz`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			result, err := svc.ImportFromFile(cmd.Context(), path)
			if err != nil {
				return fmt.Errorf("sync import: %w", err)
			}

			fmt.Fprintf(os.Stdout, "Imported: %d created, %d updated, %d skipped\n",
				result.Created, result.Updated, result.Skipped)
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
