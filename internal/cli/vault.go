package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/vault"
)

// newVaultCmd returns the "mneme vault" parent command. It groups subcommands
// related to the filesystem vault mirror.
func newVaultCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Manage the filesystem vault mirror",
		Long: `Commands for managing mneme's filesystem vault — a directory tree of
Markdown files that mirrors the SQLite database. Each memory is written as a
.md file with YAML frontmatter, organised by topic_key segments.

The vault is a valid Obsidian vault. Open it directly:
  open -a Obsidian ~/.mneme/vaults/<project-slug>`,
	}

	cmd.AddCommand(newVaultExportCmd())
	cmd.AddCommand(newVaultImportCmd())
	return cmd
}

// newVaultExportCmd returns the "mneme vault export" command which writes each
// active memory as a .md file under a vault root directory.
//
// Export is idempotent: memories whose on-disk updated_at matches the DB value
// are skipped. Writes are atomic (tmp + os.Rename) so interrupted exports do
// not leave partial files.
func newVaultExportCmd() *cobra.Command {
	var (
		flagScope             string
		flagOutput            string
		flagDryRun            bool
		flagIncludeSuperseded bool
		flagType              string
	)

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export memories to Markdown files with YAML frontmatter",
		Long: `Writes each active memory as an individual .md file to a vault directory.

The directory tree mirrors topic_key structure:
  topic_key=architecture/tech-stack → notes/architecture/tech-stack.md
  no topic_key                      → notes/_no-topic/<id8>.md

Each file has YAML frontmatter with id, type, scope, title, importance,
confidence, decay_rate, created_at, updated_at and other fields.

The vault is idempotent: running export twice writes 0 files on the second
run when no memories changed. A .mneme-vault marker file is written at the
vault root to track metadata.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return fmt.Errorf("vault export: init service: %w", err)
			}
			defer cleanup()

			ctx := context.Background()

			if flagDryRun {
				fmt.Fprintf(os.Stdout, "Dry run — no files will be written.\n\n")
			}

			opts := service.VaultExportOptions{
				Scope:             flagScope,
				OutputDir:         flagOutput,
				DryRun:            flagDryRun,
				IncludeSuperseded: flagIncludeSuperseded,
			}

			if flagType != "" {
				mt := model.MemoryType(flagType)
				if !mt.Valid() {
					return fmt.Errorf("vault export: unknown memory type %q; valid types: decision, discovery, bugfix, pattern, preference, convention, architecture, config, session_summary, rule, synthesis", flagType)
				}
				opts.Type = mt
			}

			result, err := svc.VaultExport(ctx, opts)
			if err != nil {
				return fmt.Errorf("vault export: %w", err)
			}

			if result.Project != nil {
				printVaultResult(result.Project, flagDryRun)
			}
			if result.Global != nil {
				printVaultResult(result.Global, flagDryRun)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&flagScope, "scope", "s", "project", "Scope to export: project, global, or all")
	cmd.Flags().StringVarP(&flagOutput, "output", "o", "", "Vault root directory (default: ~/.mneme/vaults/<slug>)")
	cmd.Flags().BoolVarP(&flagDryRun, "dry-run", "n", false, "Show what would be written without writing")
	cmd.Flags().BoolVar(&flagIncludeSuperseded, "include-superseded", false, "Include superseded memories")
	cmd.Flags().StringVarP(&flagType, "type", "t", "", "Filter by memory type (e.g. decision, architecture)")

	return cmd
}

// newVaultImportCmd returns the "mneme vault import" command which reads .md
// files from a vault directory and upserts their memories into SQLite.
//
// Import respects the .mneme-vault marker file for project verification and
// supports two conflict-resolution strategies:
//   - merge (default): file.updated_at > DB.updated_at → update; else skip.
//   - overwrite: file always wins.
//
// --dry-run performs full parse + conflict resolution with zero DB writes.
func newVaultImportCmd() *cobra.Command {
	var (
		flagScope    string
		flagInput    string
		flagStrategy string
		flagDryRun   bool
	)

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import memories from vault Markdown files into SQLite",
		Long: `Reads .md files from the vault's notes/ directory, parses their YAML
frontmatter, and upserts the memories into the SQLite store.

The vault must contain a .mneme-vault marker file (created by vault export).
Only files under notes/ are processed; other files (images, config) are ignored.

Merge strategy (default):
  Files with updated_at newer than the DB record update the DB.
  Files with updated_at older than or equal to the DB record are skipped.
  Running "vault export && vault import" with no edits results in 0 changes.

Overwrite strategy:
  File always wins, regardless of timestamps. Use for "restore from vault".

Files without an id field are always inserted as new memories. Files with a
topic_key trigger upsert dedup — if a memory with the same topic_key exists,
it is updated rather than duplicated.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return fmt.Errorf("vault import: init service: %w", err)
			}
			defer cleanup()

			ctx := context.Background()

			if flagDryRun {
				fmt.Fprintf(os.Stdout, "Dry run — no changes will be written to the database.\n\n")
			}

			result, err := svc.VaultImport(ctx, service.VaultImportOptions{
				Scope:    flagScope,
				InputDir: flagInput,
				Strategy: flagStrategy,
				DryRun:   flagDryRun,
			})
			if err != nil {
				return fmt.Errorf("vault import: %w", err)
			}

			printVaultImportResult(result, flagDryRun)
			return nil
		},
	}

	cmd.Flags().StringVarP(&flagScope, "scope", "s", "project", "Scope to import into: project or global")
	cmd.Flags().StringVarP(&flagInput, "input", "i", "", "Vault root directory (default: ~/.mneme/vaults/<slug>)")
	cmd.Flags().StringVar(&flagStrategy, "strategy", "merge", "Conflict resolution: merge (default) or overwrite")
	cmd.Flags().BoolVarP(&flagDryRun, "dry-run", "n", false, "Show what would be imported without writing to DB")

	return cmd
}

// printVaultImportResult writes a summary of the vault import operation to stdout.
func printVaultImportResult(result *service.VaultImportResult, dryRun bool) {
	if dryRun {
		fmt.Fprintf(os.Stdout, "Vault import (dry run): %d would create, %d would update, %d would skip, %d errors\n",
			result.Created, result.Updated, result.Skipped, result.Errors)
		if len(result.Paths) > 0 {
			fmt.Fprintf(os.Stdout, "Paths (first %d):\n", len(result.Paths))
			for _, p := range result.Paths {
				fmt.Fprintf(os.Stdout, "  %s\n", p)
			}
		}
	} else {
		fmt.Fprintf(os.Stdout, "Vault import: %d created, %d updated, %d skipped, %d errors\n",
			result.Created, result.Updated, result.Skipped, result.Errors)
	}
	fmt.Fprintf(os.Stdout, "Vault root: %s\n", result.VaultRoot)
}

// printVaultResult writes a summary line for a single vault export result to stdout.
func printVaultResult(result *vault.ExportResult, dryRun bool) {
	if dryRun {
		fmt.Fprintf(os.Stdout, "Vault export (dry run): %d would write, %d would skip\n",
			result.Written, result.Skipped)
		if len(result.Paths) > 0 {
			fmt.Fprintf(os.Stdout, "Paths (first %d):\n", len(result.Paths))
			for _, p := range result.Paths {
				fmt.Fprintf(os.Stdout, "  %s\n", p)
			}
		}
	} else {
		fmt.Fprintf(os.Stdout, "Vault export: %d written, %d skipped, %d errors\n",
			result.Written, result.Skipped, result.Errors)
	}

	fmt.Fprintf(os.Stdout, "Vault root: %s\n", result.VaultRoot)
}
