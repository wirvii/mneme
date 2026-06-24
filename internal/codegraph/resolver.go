package codegraph

import (
	"fmt"
	"path"
	"strings"
)

// ResolveResult summarises a single resolution pass over unresolved_refs.
// Resolved is the number of refs that were matched to a node and converted to
// edges. Unresolved is the number of refs for which no matching node was found
// and which remain in the database.
type ResolveResult struct {
	// Resolved is the count of refs that were successfully matched and promoted
	// to real edges.
	Resolved int

	// Unresolved is the count of refs that could not be matched against any
	// node in the current graph. They remain in unresolved_refs for future
	// resolution passes or inspection.
	Unresolved int
}

// Resolver resolves cross-file references after all files in a project have
// been indexed. It reads every row from the unresolved_refs table and attempts
// to match each against existing nodes using a four-tier lookup strategy:
//
//  1. Exact match on nodes.qualified_name.
//  2. Import-guided cross-package (SPEC-047 C3) — uses the file's import
//     declarations to resolve pkg.Func() or ns.member() to the correct node
//     in the imported package/file.  Only links when a single candidate
//     matches (candidato-único-o-nada).
//  3. Suffix match: nodes.qualified_name LIKE '%.' + referenceName.
//  4. Short-name fallback on nodes.name.
//
// When a match is found the resolver creates a directed edge from the referring
// node to the matched node with the stored reference kind, then deletes the ref.
// Refs that cannot be matched are left in the table and counted as Unresolved.
type Resolver struct {
	store     *Store
	tsAliases TSAliasMap // loaded once per Resolve(rootDir) call; nil = no aliases
}

// NewResolver constructs a Resolver backed by the given Store.
func NewResolver(store *Store) *Resolver {
	return &Resolver{store: store}
}

// fileImports is the per-file import index built once per Resolve() call.
// Outer key: file_path. Inner key: local binding name.
// For Go:  value = importPath (e.g. "internal/store")
// For TS:  value = moduleSource (e.g. "./foo", "../bar")
type fileImports map[string]map[string]string

// buildFileImports loads all import nodes from the store and builds the
// per-file import index. The result is used by resolveRef to perform
// import-guided resolution without issuing per-ref DB queries.
func (r *Resolver) buildFileImports() (fileImports, error) {
	imports, err := r.store.ListImportNodes()
	if err != nil {
		return nil, fmt.Errorf("resolver: build file imports: %w", err)
	}

	fi := make(fileImports)
	for _, n := range imports {
		if n.ImportAlias == "" || n.ImportAlias == "_" || n.ImportAlias == "." {
			// Blank/dot imports cannot be resolved by alias.
			continue
		}
		if _, ok := fi[n.FilePath]; !ok {
			fi[n.FilePath] = make(map[string]string)
		}
		switch n.Language {
		case "go":
			// n.Name is the importPath (e.g. "internal/store").
			// n.ImportAlias is the local binding (e.g. "store", "p", "yaml").
			fi[n.FilePath][n.ImportAlias] = n.Name
		case "typescript", "javascript":
			// For TS/JS the module source is encoded in the qualified_name:
			//   "import:<binding>:<source>"
			// n.Name is the local binding; we extract the source from QualifiedName.
			if src := tsImportSource(n.QualifiedName); src != "" {
				fi[n.FilePath][n.Name] = src
			}
		}
	}
	return fi, nil
}

// tsImportSource extracts the module source from a TS import qualified_name of
// the form "import:<name>:<source>". Returns "" for side-effect imports
// ("import:<source>" — no binding name).
func tsImportSource(qualifiedName string) string {
	if !strings.HasPrefix(qualifiedName, "import:") {
		return ""
	}
	rest := qualifiedName[len("import:"):]
	idx := strings.Index(rest, ":")
	if idx < 0 {
		// Side-effect import — no binding.
		return ""
	}
	return rest[idx+1:]
}

// Resolve iterates over all rows in the unresolved_refs table and attempts to
// promote each to a real edge. It returns a ResolveResult summarising how many
// refs were resolved versus how many remain unresolvable with the current graph.
//
// rootDir is the absolute path of the indexed repository root. When non-empty
// and TS/JS nodes are present, Resolve will attempt to load tsconfig.json path
// aliases (via the TSExtractor subprocess) so that imports like "@/lib/utils"
// can be resolved. If Node.js is unavailable, no tsconfig is found, or rootDir
// is empty, resolution falls back gracefully to relative-only TS imports.
//
// Resolve is safe to call multiple times; already-resolved refs will not produce
// duplicate edges because EdgeExists is consulted before each insertion.
func (r *Resolver) Resolve(rootDir string) (*ResolveResult, error) {
	refs, err := r.store.ListUnresolvedRefs()
	if err != nil {
		return nil, fmt.Errorf("resolver: list unresolved refs: %w", err)
	}

	// Load tsconfig aliases once if rootDir is known and there are TS/JS nodes.
	// Fail-open: any error leaves r.tsAliases nil → resolveTSImport behaves as before.
	if rootDir != "" {
		hasTSNodes, nodeErr := r.store.HasNodesForLanguage("typescript")
		if nodeErr == nil && hasTSNodes {
			ex := NewTSExtractor()
			defer ex.Close() //nolint:errcheck
			r.tsAliases = LoadTSAliases(ex, rootDir)
		}
	}

	// Build the per-file import index once, before iterating refs.
	fi, err := r.buildFileImports()
	if err != nil {
		return nil, fmt.Errorf("resolver: %w", err)
	}

	result := &ResolveResult{}

	for _, ref := range refs {
		node, err := r.resolveRef(ref, fi)
		if err != nil {
			return nil, fmt.Errorf("resolver: find node for ref %q: %w", ref.ReferenceName, err)
		}

		if node == nil {
			// No matching node found — leave the ref for a future pass.
			result.Unresolved++
			continue
		}

		// Guard against duplicate edges before inserting.
		kind := ref.ReferenceKind
		exists, err := r.store.EdgeExists(ref.FromNodeID, node.ID, kind)
		if err != nil {
			return nil, fmt.Errorf("resolver: check edge exists: %w", err)
		}
		if !exists {
			// Determine provenance: import-guided edges carry "import" to
			// distinguish them from generic resolver edges.
			provenance := "resolver"
			if node.importGuided {
				provenance = "import"
			}
			edge := Edge{
				Source:     ref.FromNodeID,
				Target:     node.ID,
				Kind:       kind,
				Line:       ref.Line,
				Col:        ref.Col,
				Provenance: provenance,
			}
			if err := r.store.UpsertEdge(edge); err != nil {
				return nil, fmt.Errorf("resolver: upsert edge: %w", err)
			}
		}

		// Remove the resolved ref from the database.
		if err := r.store.DeleteUnresolvedRef(ref.ID); err != nil {
			return nil, fmt.Errorf("resolver: delete unresolved ref %d: %w", ref.ID, err)
		}

		result.Resolved++
	}

	return result, nil
}

// resolvedNode extends Node with a flag indicating whether the resolution was
// achieved via the import-guided tier (for provenance tagging).
type resolvedNode struct {
	*Node
	importGuided bool
}

// resolveRef attempts to locate a node matching ref using a four-tier strategy.
// It returns nil when no node matches in any tier.
//
//  1. Exact match on qualified_name.
//  2. Import-guided cross-package (new).
//  3. Suffix match: qualified_name ends with "." + referenceName.
//  4. Name match: nodes.name equals the last component after the last dot.
func (r *Resolver) resolveRef(ref UnresolvedRef, fi fileImports) (*resolvedNode, error) {
	// Tier 1: exact match on qualified_name.
	node, err := r.store.FindNodeByQualifiedName(ref.ReferenceName)
	if err != nil {
		return nil, err
	}
	if node != nil {
		return &resolvedNode{Node: node}, nil
	}

	// Tier 2: import-guided cross-package resolution.
	if n := r.resolveByImport(ref, fi); n != nil {
		return n, nil
	}

	// Tier 3: suffix match — qualified_name LIKE '%.' + referenceName.
	// Only attempted when the name contains a dot (otherwise it is a plain
	// short name and tier 4 is the right strategy).
	// Candidato-único-o-nada: only bind when exactly one node matches;
	// 0 or >=2 candidates leave the ref unresolved to preserve precision.
	if strings.Contains(ref.ReferenceName, ".") {
		candidates, err := r.store.FindNodesBySuffix(ref.ReferenceName)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 1 {
			return &resolvedNode{Node: &candidates[0]}, nil
		}
	}

	// Tier 4: match on the short name (last component after the final dot).
	// Candidato-único-o-nada: only bind when exactly one node matches;
	// 0 or >=2 candidates leave the ref unresolved to preserve precision.
	shortName := ref.ReferenceName
	if idx := strings.LastIndex(ref.ReferenceName, "."); idx >= 0 {
		shortName = ref.ReferenceName[idx+1:]
	}
	if shortName == "" {
		return nil, nil
	}
	nameCandidates, err := r.store.FindNodesByName(shortName)
	if err != nil {
		return nil, err
	}
	if len(nameCandidates) == 1 {
		return &resolvedNode{Node: &nameCandidates[0]}, nil
	}
	return nil, nil
}

// resolveByImport implements Tier 2: import-guided cross-package resolution.
// It uses the per-file import index (fi) to map a qualifier to its import
// path (Go) or module source (TS) and then looks up the symbol in the
// target package/file using candidato-único-o-nada semantics.
func (r *Resolver) resolveByImport(ref UnresolvedRef, fi fileImports) *resolvedNode {
	if fi == nil {
		return nil
	}
	fileBindings, ok := fi[ref.FilePath]
	if !ok {
		return nil
	}

	switch ref.Language {
	case "go":
		return r.resolveGoImport(ref.ReferenceName, ref.FilePath, fileBindings)
	case "typescript", "javascript":
		return r.resolveTSImport(ref.ReferenceName, ref.FilePath, fileBindings)
	}
	return nil
}

// resolveGoImport resolves a Go reference like "pkg.Func" using the file's
// import bindings. Returns nil if resolution is not possible or ambiguous.
func (r *Resolver) resolveGoImport(referenceName, refFilePath string, bindings map[string]string) *resolvedNode {
	// Only handle dotted references: qualifier.Symbol.
	dotIdx := strings.Index(referenceName, ".")
	if dotIdx < 0 {
		return nil
	}
	qualifier := referenceName[:dotIdx]
	symbol := referenceName[dotIdx+1:]

	// If symbol still contains a dot this is a chained selector (e.g. a.b.Func)
	// which is beyond a simple package qualifier — skip.
	if strings.Contains(symbol, ".") {
		return nil
	}

	importPath, ok := bindings[qualifier]
	if !ok {
		return nil
	}

	// Look up all nodes named symbol in the imported package directory.
	candidates, err := r.store.FindNodesByNameInDir(symbol, importPath)
	if err != nil || len(candidates) != 1 {
		// 0 candidates → not found; >1 → ambiguous; error → skip.
		return nil
	}
	return &resolvedNode{Node: &candidates[0], importGuided: true}
}

// resolveTSImport resolves a TS/JS reference using the file's import bindings.
// Handles:
//   - Namespace import: "ns.member" where ns is a namespace binding (* as ns).
//   - Named/default import: "member" where member is a direct binding.
//
// Returns nil when resolution is not possible or ambiguous.
func (r *Resolver) resolveTSImport(referenceName, refFilePath string, bindings map[string]string) *resolvedNode {
	var binding, symbol string

	dotIdx := strings.Index(referenceName, ".")
	if dotIdx >= 0 {
		// Possibly "ns.member" — namespace import.
		binding = referenceName[:dotIdx]
		symbol = referenceName[dotIdx+1:]
		// Reject deeper chains.
		if strings.Contains(symbol, ".") {
			return nil
		}
	} else {
		// Simple identifier — named or default import.
		binding = referenceName
		symbol = referenceName
	}

	moduleSource, ok := bindings[binding]
	if !ok {
		return nil
	}

	// Try tsconfig path aliases for non-relative imports (e.g. "@/lib/utils").
	// Only attempted when the resolver has a loaded alias map (rootDir was
	// provided and at least one tsconfig was found). Candidato-único-o-nada.
	if !strings.HasPrefix(moduleSource, ".") {
		if len(r.tsAliases) == 0 {
			// No aliases loaded — bare imports stay unresolved (as before).
			return nil
		}
		basePaths := r.tsAliases.ResolveAlias(moduleSource, refFilePath)
		if len(basePaths) == 0 {
			// Alias prefix did not match any entry in the map.
			return nil
		}
		// For each resolved base path, probe extensions via tsCandidatePaths.
		// tsCandidatePaths treats the second arg as a module specifier relative
		// to the importer directory, but here we already have an absolute-ish
		// candidate directory.  Use an empty importer dir and the base path as a
		// pseudo-relative spec so tsCandidatePaths appends extensions correctly.
		for _, basePath := range basePaths {
			for _, candidate := range tsCandidatePaths("", basePath) {
				// tsCandidatePaths("", basePath) calls path.Join(".", basePath)
				// which equals basePath — correct.
				nodes, err := r.store.FindNodesByNameInFile(symbol, candidate)
				if err != nil {
					continue
				}
				if len(nodes) == 1 {
					return &resolvedNode{Node: &nodes[0], importGuided: true}
				}
				if len(nodes) > 1 {
					return nil // ambiguous
				}
			}
		}
		return nil
	}

	// Probe candidate file paths with common TS/JS extensions and index files.
	// Try each in sequence; use the first path that has exactly one matching node.
	for _, candidate := range tsCandidatePaths(refFilePath, moduleSource) {
		nodes, err := r.store.FindNodesByNameInFile(symbol, candidate)
		if err != nil {
			continue
		}
		if len(nodes) == 1 {
			return &resolvedNode{Node: &nodes[0], importGuided: true}
		}
		if len(nodes) > 1 {
			// Ambiguous within this candidate file — do not link.
			return nil
		}
	}
	return nil
}

// tsCandidatePaths returns the ordered list of file path candidates to probe
// when resolving a relative TS/JS module specifier. refFilePath is the
// importer's repo-relative path; moduleSource is the specifier (e.g. "./foo").
func tsCandidatePaths(refFilePath, moduleSource string) []string {
	baseDir := path.Dir(refFilePath)
	base := path.Join(baseDir, moduleSource)

	// If the specifier already carries a recognizable extension, use it directly.
	switch path.Ext(base) {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return []string{base}
	}

	exts := []string{".ts", ".tsx", ".js", ".jsx"}
	var candidates []string
	for _, ext := range exts {
		candidates = append(candidates, base+ext)
	}
	// Index file fallback.
	for _, ext := range exts {
		candidates = append(candidates, base+"/index"+ext)
	}
	return candidates
}

// findNode is kept for backward compatibility with tests that call it directly.
// New code should use resolveRef which includes the import-guided tier.
func (r *Resolver) findNode(referenceName string) (*Node, error) {
	rn, err := r.resolveRef(UnresolvedRef{ReferenceName: referenceName}, nil)
	if err != nil {
		return nil, err
	}
	if rn == nil {
		return nil, nil
	}
	return rn.Node, nil
}
