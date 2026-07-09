package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/project"
	"github.com/wirvii/mneme/internal/service"
)

// newCodegraphCmd returns the "mneme codegraph" parent command. It groups
// subcommands that index source code into a semantic graph and query
// relationships between symbols.
func newCodegraphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codegraph",
		Short: "Semantic code graph — index and query code structure",
		Long:  "Index source code into a semantic graph and query relationships between symbols.",
	}
	cmd.AddCommand(
		newCodegraphIndexCmd(),
		newCodegraphStatusCmd(),
		newCodegraphSearchCmd(),
		newCodegraphCallersCmd(),
		newCodegraphCalleesCmd(),
		newCodegraphImpactCmd(),
		newCodegraphNodeCmd(),
		newCodegraphTraceCmd(),
		newCodegraphFilesCmd(),
		newCodegraphHooksCmd(),
	)
	return cmd
}

// initCodeGraphService creates a CodeGraphService from the CLI flags and the
// config file. It applies the same project-detection and data-dir-override logic
// as initService. The caller must call svc.Close() when done.
func initCodeGraphService() (*service.CodeGraphService, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	cfgPath := filepath.Join(home, ".mneme", "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Apply flag overrides so CLI flags always win over the config file.
	if flagDataDir != "" {
		cfg.Storage.DataDir = flagDataDir
	}

	// Detect project: flag takes priority, then git-remote auto-detection.
	slug := flagProject
	if slug == "" {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return nil, fmt.Errorf("cannot determine working directory: %w", cwdErr)
		}
		det := project.NewDetector(cwd)
		detected, _ := det.DetectProject()
		slug = detected
	}

	projectsDir := filepath.Join(cfg.Storage.DataDir, "projects")
	return service.NewCodeGraphService(projectsDir, slug)
}

// newCodegraphIndexCmd returns the "mneme codegraph index [path]" subcommand.
// It walks the given directory tree (or the current directory by default),
// extracts code symbols, and writes them to the project's codegraph database.
func newCodegraphIndexCmd() *cobra.Command {
	var (
		flagForce    bool
		flagDryRun   bool
		flagLanguage string
	)

	cmd := &cobra.Command{
		Use:   "index [path]",
		Short: "Index source code into the semantic graph",
		Long: `Walk a directory tree and extract code symbols (functions, types, methods,
variables, etc.) into the project's codegraph database.

The index is incremental: files whose content hash matches the stored record
are skipped automatically. Use --force to re-index all files regardless. After
indexing, cross-file references are resolved in a best-effort second pass.

If [path] is omitted the current directory is used.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rootDir := "."
			if len(args) == 1 {
				rootDir = args[0]
			}
			absRoot, err := filepath.Abs(rootDir)
			if err != nil {
				return fmt.Errorf("codegraph index: resolve path: %w", err)
			}

			svc, err := initCodeGraphService()
			if err != nil {
				return fmt.Errorf("codegraph index: %w", err)
			}
			defer func() { _ = svc.Close() }()

			if flagDryRun {
				fmt.Fprintln(cmd.OutOrStdout(), "Dry run — no changes will be written.")
			}

			start := time.Now()
			result, err := svc.Index(codegraph.IndexOptions{
				RootDir:  absRoot,
				Language: flagLanguage,
				Force:    flagForce,
				DryRun:   flagDryRun,
			})
			if err != nil {
				return fmt.Errorf("codegraph index: %w", err)
			}
			elapsed := time.Since(start).Round(time.Millisecond)

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Index complete in %s\n", elapsed)
			fmt.Fprintf(out, "  Files scanned:  %d\n", result.FilesScanned)
			fmt.Fprintf(out, "  Files indexed:  %d\n", result.FilesIndexed)
			fmt.Fprintf(out, "  Files skipped:  %d\n", result.FilesSkipped)
			fmt.Fprintf(out, "  Files errored:  %d\n", result.FilesErrored)
			fmt.Fprintf(out, "  Nodes created:  %d\n", result.NodesCreated)
			fmt.Fprintf(out, "  Edges created:  %d\n", result.EdgesCreated)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&flagForce, "force", "f", false, "Re-index all files regardless of content hash")
	cmd.Flags().BoolVarP(&flagDryRun, "dry-run", "n", false, "Report without writing to the database")
	cmd.Flags().StringVarP(&flagLanguage, "language", "l", "", "Force language detection (e.g. go, typescript)")
	return cmd
}

// newCodegraphStatusCmd returns the "mneme codegraph status" subcommand.
// It prints aggregate statistics about the project's codegraph database.
func newCodegraphStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show code graph statistics",
		Long:  "Print node, edge, and file counts broken down by kind and language.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := initCodeGraphService()
			if err != nil {
				return fmt.Errorf("codegraph status: %w", err)
			}
			defer func() { _ = svc.Close() }()

			stats, err := svc.Status()
			if err != nil {
				return fmt.Errorf("codegraph status: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Nodes:  %d\n", stats.NodeCount)
			fmt.Fprintf(out, "Edges:  %d\n", stats.EdgeCount)
			fmt.Fprintf(out, "Files:  %d\n", stats.FileCount)
			fmt.Fprintf(out, "DB size: %d bytes\n", stats.DBSizeBytes)

			if len(stats.NodesByKind) > 0 {
				fmt.Fprintln(out, "\nNodes by kind:")
				for kind, count := range stats.NodesByKind {
					fmt.Fprintf(out, "  %-20s %d\n", kind, count)
				}
			}
			if len(stats.EdgesByKind) > 0 {
				fmt.Fprintln(out, "\nEdges by kind:")
				for kind, count := range stats.EdgesByKind {
					fmt.Fprintf(out, "  %-20s %d\n", kind, count)
				}
			}
			if len(stats.FilesByLanguage) > 0 {
				fmt.Fprintln(out, "\nFiles by language:")
				for lang, count := range stats.FilesByLanguage {
					fmt.Fprintf(out, "  %-20s %d\n", lang, count)
				}
			}
			return nil
		},
	}
	return cmd
}

// newCodegraphSearchCmd returns the "mneme codegraph search <query>" subcommand.
// It finds symbols by name using FTS5 prefix matching and prints results.
func newCodegraphSearchCmd() *cobra.Command {
	var (
		flagKind     string
		flagLanguage string
		flagLimit    int
	)

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search symbols by name",
		Long: `Search for code symbols by name using FTS5 prefix matching.

Results show the symbol name, kind, and source location (file:line).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]

			svc, err := initCodeGraphService()
			if err != nil {
				return fmt.Errorf("codegraph search: %w", err)
			}
			defer func() { _ = svc.Close() }()

			var kinds []codegraph.NodeKind
			if flagKind != "" {
				kinds = []codegraph.NodeKind{codegraph.NodeKind(flagKind)}
			}

			var languages []string
			if flagLanguage != "" {
				languages = []string{flagLanguage}
			}

			nodes, err := svc.Search(query, kinds, languages, flagLimit)
			if err != nil {
				return fmt.Errorf("codegraph search: %w", err)
			}

			out := cmd.OutOrStdout()
			if len(nodes) == 0 {
				fmt.Fprintln(out, "No symbols found.")
				return nil
			}

			fmt.Fprintf(out, "%-40s  %-14s  %s\n", "SYMBOL", "KIND", "LOCATION")
			fmt.Fprintf(out, "%-40s  %-14s  %s\n",
				strings.Repeat("-", 40), strings.Repeat("-", 14), strings.Repeat("-", 30))
			for _, n := range nodes {
				name := n.QualifiedName
				if name == "" {
					name = n.Name
				}
				if len(name) > 40 {
					name = name[:37] + "..."
				}
				location := fmt.Sprintf("%s:%d", n.FilePath, n.StartLine)
				fmt.Fprintf(out, "%-40s  %-14s  %s\n", name, string(n.Kind), location)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&flagKind, "kind", "k", "", "Filter by node kind (e.g. function, method, struct)")
	cmd.Flags().StringVarP(&flagLanguage, "language", "l", "", "Filter by language (e.g. go, typescript)")
	cmd.Flags().IntVarP(&flagLimit, "limit", "n", 20, "Maximum number of results")
	return cmd
}

// newCodegraphCallersCmd returns the "mneme codegraph callers <symbol>" subcommand.
// It prints all nodes that call the given symbol, traversing incoming "calls"
// edges up to --depth hops.
func newCodegraphCallersCmd() *cobra.Command {
	var (
		flagDepth int
		flagLimit int
	)

	cmd := &cobra.Command{
		Use:   "callers <symbol>",
		Short: "List callers of a symbol",
		Long: `Find all nodes that call the given symbol by traversing incoming
"calls" edges in the code graph up to --depth hops.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			symbol := args[0]

			svc, err := initCodeGraphService()
			if err != nil {
				return fmt.Errorf("codegraph callers: %w", err)
			}
			defer func() { _ = svc.Close() }()

			nodes, err := svc.Callers(symbol, flagDepth, flagLimit)
			if err != nil {
				return fmt.Errorf("codegraph callers: %w", err)
			}

			printNodeList(cmd, "Callers", symbol, nodes)
			return nil
		},
	}

	cmd.Flags().IntVarP(&flagDepth, "depth", "d", 1, "Traversal depth (number of hops)")
	cmd.Flags().IntVarP(&flagLimit, "limit", "n", 20, "Maximum number of results")
	return cmd
}

// newCodegraphCalleesCmd returns the "mneme codegraph callees <symbol>" subcommand.
// It prints all nodes that the given symbol calls, traversing outgoing "calls"
// edges up to --depth hops.
func newCodegraphCalleesCmd() *cobra.Command {
	var (
		flagDepth int
		flagLimit int
	)

	cmd := &cobra.Command{
		Use:   "callees <symbol>",
		Short: "List callees of a symbol",
		Long: `Find all nodes called by the given symbol by traversing outgoing
"calls" edges in the code graph up to --depth hops.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			symbol := args[0]

			svc, err := initCodeGraphService()
			if err != nil {
				return fmt.Errorf("codegraph callees: %w", err)
			}
			defer func() { _ = svc.Close() }()

			nodes, err := svc.Callees(symbol, flagDepth, flagLimit)
			if err != nil {
				return fmt.Errorf("codegraph callees: %w", err)
			}

			printNodeList(cmd, "Callees", symbol, nodes)
			return nil
		},
	}

	cmd.Flags().IntVarP(&flagDepth, "depth", "d", 1, "Traversal depth (number of hops)")
	cmd.Flags().IntVarP(&flagLimit, "limit", "n", 20, "Maximum number of results")
	return cmd
}

// newCodegraphImpactCmd returns the "mneme codegraph impact <symbol>" subcommand.
// It prints the transitive set of nodes affected by a change to the given symbol,
// traversing incoming calls, imports, extends, and implements edges.
func newCodegraphImpactCmd() *cobra.Command {
	var (
		flagDepth int
		flagLimit int
	)

	cmd := &cobra.Command{
		Use:   "impact <symbol>",
		Short: "Show the blast radius of changing a symbol",
		Long: `Find all nodes transitively affected by a change to the given symbol
by following incoming calls, imports, extends, and implements edges up to
--depth hops. Useful for assessing the risk of a refactor.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			symbol := args[0]

			svc, err := initCodeGraphService()
			if err != nil {
				return fmt.Errorf("codegraph impact: %w", err)
			}
			defer func() { _ = svc.Close() }()

			nodes, err := svc.Impact(symbol, flagDepth, flagLimit)
			if err != nil {
				return fmt.Errorf("codegraph impact: %w", err)
			}

			printNodeList(cmd, "Impact", symbol, nodes)
			return nil
		},
	}

	cmd.Flags().IntVarP(&flagDepth, "depth", "d", 3, "Traversal depth (number of hops)")
	cmd.Flags().IntVarP(&flagLimit, "limit", "n", 50, "Maximum number of results")
	return cmd
}

// newCodegraphNodeCmd returns the "mneme codegraph node <symbol>" subcommand.
// It prints all node fields and the source lines that define the symbol.
func newCodegraphNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node <symbol>",
		Short: "Show details for a symbol node",
		Long: `Print the full node record for the given symbol, including its kind,
qualified name, file location, signature, docstring, and source code.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			symbol := args[0]

			svc, err := initCodeGraphService()
			if err != nil {
				return fmt.Errorf("codegraph node: %w", err)
			}
			defer func() { _ = svc.Close() }()

			// Attempt to read source relative to the current working directory.
			rootDir, cwdErr := os.Getwd()
			if cwdErr != nil {
				rootDir = "."
			}

			node, source, err := svc.NodeDetail(symbol, rootDir)
			if err != nil {
				return fmt.Errorf("codegraph node: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "ID:             %s\n", node.ID)
			fmt.Fprintf(out, "Name:           %s\n", node.Name)
			fmt.Fprintf(out, "Qualified name: %s\n", node.QualifiedName)
			fmt.Fprintf(out, "Kind:           %s\n", string(node.Kind))
			fmt.Fprintf(out, "Language:       %s\n", node.Language)
			fmt.Fprintf(out, "File:           %s\n", node.FilePath)
			fmt.Fprintf(out, "Lines:          %d-%d\n", node.StartLine, node.EndLine)
			if node.Visibility != "" {
				fmt.Fprintf(out, "Visibility:     %s\n", node.Visibility)
			}
			if node.Signature != "" {
				fmt.Fprintf(out, "Signature:      %s\n", node.Signature)
			}
			if node.Docstring != "" {
				fmt.Fprintf(out, "\nDoc:\n%s\n", node.Docstring)
			}
			if source != "" {
				fmt.Fprintf(out, "\nSource:\n%s\n", source)
			}
			return nil
		},
	}
	return cmd
}

// newCodegraphTraceCmd returns the "mneme codegraph trace <from> <to>" subcommand.
// It finds the shortest call path between two symbols via BFS on outgoing
// "calls" edges and prints the path as a chain of symbol names.
func newCodegraphTraceCmd() *cobra.Command {
	var flagMaxDepth int

	cmd := &cobra.Command{
		Use:   "trace <from> <to>",
		Short: "Find the shortest call path between two symbols",
		Long: `Find the shortest call path between <from> and <to> by performing
BFS on outgoing "calls" edges in the code graph, up to --max-depth hops.
Prints each step in the path as "symbol  →  symbol".`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			from := args[0]
			to := args[1]

			svc, err := initCodeGraphService()
			if err != nil {
				return fmt.Errorf("codegraph trace: %w", err)
			}
			defer func() { _ = svc.Close() }()

			nodes, edges, err := svc.Trace(from, to, flagMaxDepth)
			if err != nil {
				return fmt.Errorf("codegraph trace: %w", err)
			}

			out := cmd.OutOrStdout()
			if len(nodes) == 0 {
				fmt.Fprintf(out, "No path found between %q and %q within %d hops.\n", from, to, flagMaxDepth)
				return nil
			}

			fmt.Fprintf(out, "Path (%d hop(s), %d edge(s)):\n", len(nodes)-1, len(edges))
			for i, n := range nodes {
				name := n.QualifiedName
				if name == "" {
					name = n.Name
				}
				if i < len(nodes)-1 {
					edgeKind := ""
					if i < len(edges) {
						edgeKind = string(edges[i].Kind)
					}
					fmt.Fprintf(out, "  %s  --[%s]-->  ", name, edgeKind)
				} else {
					fmt.Fprintf(out, "%s\n", name)
				}
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&flagMaxDepth, "max-depth", 5, "Maximum traversal depth")
	return cmd
}

// newCodegraphFilesCmd returns the "mneme codegraph files [pattern]" subcommand.
// It lists all tracked source files, optionally filtered by a glob pattern and
// language.
func newCodegraphFilesCmd() *cobra.Command {
	var flagLanguage string

	cmd := &cobra.Command{
		Use:   "files [pattern]",
		Short: "List indexed source files",
		Long: `List all source files tracked by the codegraph index. An optional
glob [pattern] is matched against file paths using filepath.Match semantics.
Use --language to filter by programming language.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pattern := ""
			if len(args) == 1 {
				pattern = args[0]
			}

			svc, err := initCodeGraphService()
			if err != nil {
				return fmt.Errorf("codegraph files: %w", err)
			}
			defer func() { _ = svc.Close() }()

			files, err := svc.Files(pattern, flagLanguage)
			if err != nil {
				return fmt.Errorf("codegraph files: %w", err)
			}

			out := cmd.OutOrStdout()
			if len(files) == 0 {
				fmt.Fprintln(out, "No files found.")
				return nil
			}

			fmt.Fprintf(out, "%-50s  %-12s  %s\n", "PATH", "LANGUAGE", "NODES")
			fmt.Fprintf(out, "%-50s  %-12s  %s\n",
				strings.Repeat("-", 50), strings.Repeat("-", 12), strings.Repeat("-", 5))
			for _, f := range files {
				path := f.Path
				if len(path) > 50 {
					path = "..." + path[len(path)-47:]
				}
				fmt.Fprintf(out, "%-50s  %-12s  %d\n", path, f.Language, f.NodeCount)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&flagLanguage, "language", "l", "", "Filter by language (e.g. go, typescript)")
	return cmd
}

// printNodeList is a shared helper that writes a labelled list of nodes to
// cmd.OutOrStdout(). It is used by callers, callees, and impact subcommands
// which all produce the same tabular output.
func printNodeList(cmd *cobra.Command, label, symbol string, nodes []codegraph.Node) {
	out := cmd.OutOrStdout()
	if len(nodes) == 0 {
		fmt.Fprintf(out, "No %s found for %q.\n", strings.ToLower(label), symbol)
		return
	}
	fmt.Fprintf(out, "%s of %q (%d):\n\n", label, symbol, len(nodes))
	fmt.Fprintf(out, "%-40s  %-14s  %s\n", "SYMBOL", "KIND", "LOCATION")
	fmt.Fprintf(out, "%-40s  %-14s  %s\n",
		strings.Repeat("-", 40), strings.Repeat("-", 14), strings.Repeat("-", 30))
	for _, n := range nodes {
		name := n.QualifiedName
		if name == "" {
			name = n.Name
		}
		if len(name) > 40 {
			name = name[:37] + "..."
		}
		location := fmt.Sprintf("%s:%d", n.FilePath, n.StartLine)
		fmt.Fprintf(out, "%-40s  %-14s  %s\n", name, string(n.Kind), location)
	}
}
