package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/store"
)

// newStatusCmd returns the "mneme status" subcommand. It displays a summary of
// the current project, the database location, and the total number of memories
// stored in both the project and global scopes.
func newStatusCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show mneme status",
		Long: `Show the current mneme status including the detected project,
database path, and memory counts for the active project and global scope.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			cfg := svc.Config()
			slug := svc.ProjectSlug()

			// Count project-scoped memories from the project DB.
			ctx := context.Background()
			projectCount, err := svc.Count(ctx, slug)
			if err != nil {
				// Non-fatal: display 0 on error so status is still useful.
				projectCount = 0
			}

			// Count global memories from the global DB (a separate file).
			globalCount := 0
			globalDB, err := db.Open(cfg.GlobalDBPath())
			if err == nil {
				globalStore := store.NewMemoryStore(globalDB)
				// Global memories have no project slug — query with empty string.
				n, countErr := globalStore.Count(ctx, "")
				_ = globalDB.Close()
				if countErr == nil {
					globalCount = n
				}
			}

			var dbPath string
			if slug != "" {
				dbPath = cfg.ProjectDBPath(slug)
			} else {
				dbPath = cfg.GlobalDBPath()
			}

			// Shorten the home directory to ~ for readability.
			home, _ := os.UserHomeDir()
			if home != "" {
				if len(dbPath) > len(home) && dbPath[:len(home)] == home {
					dbPath = "~" + dbPath[len(home):]
				}
			}

			detected := "auto-detected"
			if flagProject != "" {
				detected = "from --project flag"
			}

			if flagJSON {
				type statusOutput struct {
					Version      string `json:"version"`
					Project      string `json:"project"`
					Detected     string `json:"detected"`
					Database     string `json:"database"`
					ProjectCount int    `json:"project_memories"`
					GlobalCount  int    `json:"global_memories"`
				}
				return printJSON(os.Stdout, statusOutput{
					Version:      Version,
					Project:      slug,
					Detected:     detected,
					Database:     dbPath,
					ProjectCount: projectCount,
					GlobalCount:  globalCount,
				})
			}

			fmt.Fprintf(os.Stdout, "mneme v%s\n\n", Version)

			if slug != "" {
				fmt.Fprintf(os.Stdout, "Project:  %s (%s)\n", slug, detected)
			} else {
				fmt.Fprintf(os.Stdout, "Project:  (none detected — using global database)\n")
			}
			fmt.Fprintf(os.Stdout, "Database: %s\n\n", dbPath)

			fmt.Fprintf(os.Stdout, "Memories: %d active\n", projectCount)
			fmt.Fprintf(os.Stdout, "Global:   %d memories\n", globalCount)

			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}
