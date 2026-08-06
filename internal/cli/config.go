package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/config"
)

// validConfigSections is the ordered list of all recognised section names.
// The order matches the table of contents in docs/CONFIG.md.
var validConfigSections = []string{
	"storage", "search", "context", "consolidation", "decay",
	"mcp", "embedding", "personal", "workflow", "delegation",
	"spec", "graph", "suggestions", "speech",
}

// newConfigCmd returns the "mneme config" subcommand group. It provides
// human-readable and machine-readable inspection of the fully resolved
// configuration with provenance annotations.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect resolved configuration",
		Long: `Inspect the fully resolved mneme configuration.

Configuration is assembled from three layers in priority order:
  1. Built-in defaults
  2. ~/.mneme/config.toml (if present)
  3. Environment variable overrides

Subcommands:
  show  Print all resolved config fields with their origin (default/file/env).`,
	}

	cmd.AddCommand(newConfigShowCmd())

	return cmd
}

// newConfigShowCmd returns the "mneme config show [section]" subcommand.
// It loads the fully resolved config via LoadWithOrigins and prints each
// field alongside its value and provenance.
func newConfigShowCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "show [section]",
		Short: "Show resolved config with provenance",
		Long: `Print the fully resolved configuration with provenance per field.

Each field is annotated with where its value came from:
  default  — built-in default, no file or env override
  file     — set by ~/.mneme/config.toml
  env:VAR  — set by the named environment variable

Optional positional argument: a section name to filter output.
Valid sections: storage, search, context, consolidation, decay, mcp,
  embedding, personal, workflow, delegation, spec, graph, suggestions

Examples:
  mneme config show
  mneme config show graph
  mneme config show --json
  mneme config show suggestions --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("cannot determine home directory: %w", err)
			}
			cfgPath := filepath.Join(home, ".mneme", "config.toml")

			cfg, origins, err := config.LoadWithOrigins(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// Apply flag overrides so CLI flags still win.
			if flagDataDir != "" {
				cfg.Storage.DataDir = flagDataDir
			}
			if flagLogLevel != "" {
				cfg.MCP.LogLevel = flagLogLevel
			}
			_ = cfg // cfg is used for flag overrides; origins holds the resolved state

			// Section filter.
			var sectionFilter string
			if len(args) == 1 {
				sectionFilter = strings.ToLower(args[0])
				if !isValidSection(sectionFilter) {
					return fmt.Errorf("unknown config section: %q. Valid sections: %s",
						sectionFilter, strings.Join(validConfigSections, ", "))
				}
			}

			out := cmd.OutOrStdout()
			if flagJSON {
				return printConfigJSON(out, origins, sectionFilter)
			}
			printConfigTable(out, origins, sectionFilter)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// isValidSection reports whether name is one of the recognised section names.
func isValidSection(name string) bool {
	for _, s := range validConfigSections {
		if s == name {
			return true
		}
	}
	return false
}

// configShowJSON is the JSON envelope for mneme config show --json.
type configShowJSON struct {
	ConfigPath     string                               `json:"config_path"`
	ConfigFileExists bool                               `json:"config_file_exists"`
	Sections       map[string][]config.ConfigFieldInfo `json:"sections"`
}

// printConfigJSON writes machine-readable JSON to w.
func printConfigJSON(w io.Writer, origins *config.ConfigOrigins, section string) error {
	sections := origins.Sections
	if section != "" {
		sections = map[string][]config.ConfigFieldInfo{
			section: origins.Sections[section],
		}
	}
	out := configShowJSON{
		ConfigPath:       origins.Path,
		ConfigFileExists: origins.FileExists,
		Sections:         sections,
	}
	return printJSON(w, out)
}

// printConfigTable writes a human-readable table to w.
func printConfigTable(w io.Writer, origins *config.ConfigOrigins, section string) {
	// File status header.
	if origins.FileExists {
		fmt.Fprintf(w, "Config file: %s (found)\n\n", origins.Path)
	} else {
		fmt.Fprintf(w, "Config file: %s (not found, using defaults)\n\n", origins.Path)
	}

	sections := validConfigSections
	if section != "" {
		sections = []string{section}
	}

	for _, s := range sections {
		fields, ok := origins.Sections[s]
		if !ok || len(fields) == 0 {
			continue
		}
		fmt.Fprintf(w, "[%s]\n", s)
		fmt.Fprintf(w, "  %-30s %-40s %s\n", "FIELD", "VALUE", "ORIGIN")
		for _, f := range fields {
			valStr := fmt.Sprintf("%v", f.Value)
			originStr := formatOrigin(f.Origin, f.EnvVar)
			fmt.Fprintf(w, "  %-30s %-40s %s\n", f.Key, valStr, originStr)
		}
		fmt.Fprintln(w)
	}
}

// formatOrigin returns a display string for the origin. For env origin it
// includes the variable name: "env:MNEME_GRAPH_MODE".
func formatOrigin(origin config.FieldOrigin, envVar string) string {
	if origin == config.OriginEnv && envVar != "" {
		return "env:" + envVar
	}
	return string(origin)
}
