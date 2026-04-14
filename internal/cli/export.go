package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/export"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/store"
)

// newExportCmd returns the "mneme export" subcommand group. Child commands
// handle exporting memories in various human-readable formats. The group exists
// so future formats (e.g. CSV, JSON-LD) can be added without polluting the
// root namespace.
func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export memories in various formats",
		Long: `Export mneme memories to human-readable formats.

The primary subcommand is "markdown", which produces Markdown documents
suitable for reading in any text editor, sharing in pull-request comments,
or committing alongside project documentation.`,
	}

	cmd.AddCommand(newExportMarkdownCmd())
	return cmd
}

// newExportMarkdownCmd returns the "mneme export markdown" subcommand. It
// exports active memories to a Markdown document, either as a single file,
// per-type files in a directory, or written to stdout.
func newExportMarkdownCmd() *cobra.Command {
	var (
		flagOutput string
		flagDir    string
		flagScope  string
		flagType   string
	)

	cmd := &cobra.Command{
		Use:   "markdown",
		Short: "Export memories to Markdown",
		Long: `Export active memories to a human-readable Markdown document.

Memories are grouped by type and sorted by importance (highest first).
By default output goes to stdout so it can be piped directly to a pager
or other tools.

Use --output to write to a single file, or --dir to produce one file per
memory type. The two flags are mutually exclusive.`,
		Example: `  # Write all project memories to stdout
  mneme export markdown

  # Write to a single file
  mneme export markdown -o memories.md

  # Write one file per type to a directory
  mneme export markdown --dir ./docs/memories

  # Filter by scope and pipe to less
  mneme export markdown --scope global | less

  # Export only decisions to stdout
  mneme export markdown --type decision`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagOutput != "" && flagDir != "" {
				return fmt.Errorf("export markdown: --output and --dir are mutually exclusive")
			}

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx := cmd.Context()

			// Build list options from flags.
			opts := store.ListOptions{
				Limit: 100000,
			}

			if flagScope != "" {
				s := model.Scope(flagScope)
				if !s.Valid() {
					return fmt.Errorf("export markdown: invalid scope %q (use: project, global, org)", flagScope)
				}
				opts.Scope = s
			}

			if flagType != "" {
				mt := model.MemoryType(flagType)
				if !mt.Valid() {
					return fmt.Errorf("export markdown: invalid type %q", flagType)
				}
				opts.Type = mt
			}

			memories, err := svc.List(ctx, opts)
			if err != nil {
				return fmt.Errorf("export markdown: %w", err)
			}

			renderOpts := export.MarkdownOptions{
				Project:    svc.ProjectSlug(),
				SingleFile: flagDir == "",
			}

			// ── Directory mode: one file per type ──────────────────────────
			if flagDir != "" {
				return exportToDir(memories, flagDir, renderOpts, flagType)
			}

			// ── Single-file or stdout mode ──────────────────────────────────
			w := os.Stdout
			if flagOutput != "" {
				absPath, err := filepath.Abs(flagOutput)
				if err != nil {
					return fmt.Errorf("export markdown: resolve output path: %w", err)
				}
				f, err := os.Create(absPath)
				if err != nil {
					return fmt.Errorf("export markdown: create output file: %w", err)
				}
				defer f.Close()
				w = f

				// Print summary to stderr — keep stdout clean for the file content
				// (even though we redirect to a file, the pattern is consistent).
				defer func() {
					fmt.Fprintf(os.Stderr, "Exported %d memories to %s\n", len(memories), absPath)
				}()
			}

			return export.RenderAll(w, memories, renderOpts)
		},
	}

	cmd.Flags().StringVarP(&flagOutput, "output", "o", "", "Output file path (default: stdout)")
	cmd.Flags().StringVar(&flagDir, "dir", "", "Output directory — writes one .md file per type")
	cmd.Flags().StringVar(&flagScope, "scope", "", "Filter by scope: project, global, org")
	cmd.Flags().StringVarP(&flagType, "type", "t", "", "Filter by type: decision, bugfix, discovery, ...")

	return cmd
}

// exportToDir writes one Markdown file per type into dir. If flagType is set
// only that type is written. The directory is created if it does not exist.
func exportToDir(memories []*model.Memory, dir string, opts export.MarkdownOptions, filterType string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("export markdown: resolve directory path: %w", err)
	}

	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return fmt.Errorf("export markdown: create output directory: %w", err)
	}

	// Group memories by type.
	grouped := make(map[model.MemoryType][]*model.Memory)
	for _, m := range memories {
		grouped[m.Type] = append(grouped[m.Type], m)
	}

	// Determine which types to emit, respecting the optional type filter.
	var types []model.MemoryType
	if filterType != "" {
		types = []model.MemoryType{model.MemoryType(filterType)}
	} else {
		types = model.AllMemoryTypes()
	}

	written := 0
	for _, mt := range types {
		group, ok := grouped[mt]
		if !ok || len(group) == 0 {
			continue
		}

		fileName := filepath.Join(absDir, typeFileName(mt))
		f, err := os.Create(fileName)
		if err != nil {
			return fmt.Errorf("export markdown: create file for type %s: %w", mt, err)
		}

		renderErr := export.RenderType(f, mt, group, opts)
		closeErr := f.Close()

		if renderErr != nil {
			return fmt.Errorf("export markdown: render type %s: %w", mt, renderErr)
		}
		if closeErr != nil {
			return fmt.Errorf("export markdown: close file for type %s: %w", mt, closeErr)
		}

		written++
	}

	fmt.Fprintf(os.Stderr, "Exported %d memories across %d file(s) to %s\n",
		len(memories), written, absDir)
	return nil
}

// typeFileName maps a MemoryType to its canonical export filename.
// Each type maps to a human-readable plural form suitable for directory layout.
func typeFileName(t model.MemoryType) string {
	names := map[model.MemoryType]string{
		model.TypeDecision:       "decisions",
		model.TypeDiscovery:      "discoveries",
		model.TypeBugfix:         "bugfixes",
		model.TypePattern:        "patterns",
		model.TypePreference:     "preferences",
		model.TypeConvention:     "conventions",
		model.TypeArchitecture:   "architecture",
		model.TypeConfig:         "config",
		model.TypeSessionSummary: "session-summaries",
	}
	if name, ok := names[t]; ok {
		return name + ".md"
	}
	return string(t) + ".md"
}
