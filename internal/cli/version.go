package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/db"
)

// newVersionCmd returns the "mneme version" subcommand. It prints the binary
// version, target platform, and the current DB schema version. The DB schema
// version is read from the global database if it exists; 0 is shown when it
// has not been initialised yet.
//
// Example output:
//
//	mneme v0.3.0 (linux/amd64)
//	DB schema: v3
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the mneme version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(os.Stdout, "mneme v%s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)

			schemaVer, err := globalSchemaVersion()
			if err != nil {
				// Non-fatal: show the version with a note about the missing DB.
				fmt.Fprintf(os.Stdout, "DB schema: unknown (%v)\n", err)
				return nil
			}
			fmt.Fprintf(os.Stdout, "DB schema: v%d\n", schemaVer)
			return nil
		},
	}
}

// globalSchemaVersion returns the schema version of the global mneme database.
// It resolves the path from the default config location (~/.mneme/global.db)
// without loading the full config or running any migrations, so it is safe to
// call even when no config file exists.
func globalSchemaVersion() (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, fmt.Errorf("home dir: %w", err)
	}
	globalDBPath := filepath.Join(home, ".mneme", "global.db")
	return db.SchemaVersion(globalDBPath)
}
