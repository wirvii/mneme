package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wirvii/mneme/internal/codegraph"
)

// CodeGraphService orchestrates code graph operations. It owns the DB lifecycle
// and provides high-level, frontend-agnostic methods used by MCP tools and CLI
// commands. The service delegates to the codegraph package for all persistence
// and graph-traversal concerns.
type CodeGraphService struct {
	cdb   *codegraph.CodeGraphDB
	store *codegraph.Store
	query *codegraph.QueryEngine
}

// NewCodeGraphService opens the codegraph DB for the given project slug and
// returns a ready-to-use service. projectsDir is the parent directory that holds
// per-project databases (e.g. ~/.mneme/projects). The DB file path is derived
// via codegraph.DBPath — the caller must not assume its exact location.
func NewCodeGraphService(projectsDir, slug string) (*CodeGraphService, error) {
	path := codegraph.DBPath(projectsDir, slug)
	cdb, err := codegraph.OpenDB(path)
	if err != nil {
		return nil, fmt.Errorf("service: codegraph: open: %w", err)
	}
	store := codegraph.NewStore(cdb)
	query := codegraph.NewQueryEngine(store)
	return &CodeGraphService{cdb: cdb, store: store, query: query}, nil
}

// NewCodeGraphServiceFromDB creates a service from an already-open CodeGraphDB.
// Ownership of cdb is NOT transferred — the caller is responsible for calling
// cdb.Close(). Primarily used in tests that need an in-memory database.
func NewCodeGraphServiceFromDB(cdb *codegraph.CodeGraphDB) *CodeGraphService {
	store := codegraph.NewStore(cdb)
	query := codegraph.NewQueryEngine(store)
	return &CodeGraphService{cdb: cdb, store: store, query: query}
}

// Close closes the underlying database connection. Must be called when the
// service was created via NewCodeGraphService. Do not call when created via
// NewCodeGraphServiceFromDB (the caller owns the DB there).
func (s *CodeGraphService) Close() error {
	return s.cdb.Close()
}

// Index indexes a directory tree and, after a successful run, resolves any
// cross-file references on a best-effort basis. Resolver failures are silently
// ignored so that a partial index is always written even when resolution
// encounters ambiguous references. DryRun mode skips both writes and resolution.
func (s *CodeGraphService) Index(opts codegraph.IndexOptions) (*codegraph.IndexResult, error) {
	ix := codegraph.NewIndexer(s.store)
	result, err := ix.Index(opts)
	if err != nil {
		return nil, err
	}
	if !opts.DryRun {
		resolver := codegraph.NewResolver(s.store)
		_, _ = resolver.Resolve(opts.RootDir) // best-effort: resolution errors are non-fatal
	}
	return result, nil
}

// LastIndexedSHA returns the git commit SHA recorded as the last successfully
// indexed state for this project, or "" when none has been recorded yet (a
// fresh DB, or one just rebuilt with --force). It is the anchor the CLI git
// orchestration diffs against HEAD to drive the scoped incremental re-index
// (SPEC-101).
func (s *CodeGraphService) LastIndexedSHA() (string, error) {
	return s.store.GetMetadata(codegraph.MetaKeyLastIndexedSHA)
}

// SetLastIndexedSHA records sha as the last successfully indexed commit for this
// project. Callers must only advance it after a successful index run, so a
// discarded or failed run never moves the anchor past what is actually in the
// graph (the last_sha invariant that makes coalescing safe).
func (s *CodeGraphService) SetLastIndexedSHA(sha string) error {
	return s.store.SetMetadata(codegraph.MetaKeyLastIndexedSHA, sha)
}

// Search finds symbols by name using FTS5 prefix matching. An empty kinds or
// languages slice means no filtering on that dimension. limit is clamped to
// the range [1, 50]; a zero or negative value defaults to 20.
func (s *CodeGraphService) Search(query string, kinds []codegraph.NodeKind, languages []string, limit int) ([]codegraph.Node, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	return s.store.SearchNodes(query, kinds, languages, limit)
}

// Callers returns nodes that call the given symbol, traversing incoming "calls"
// edges up to depth hops. depth=0 and limit=0 use the engine's defaults.
func (s *CodeGraphService) Callers(symbol string, depth, limit int) ([]codegraph.Node, error) {
	nodeID, err := s.resolveSymbol(symbol)
	if err != nil {
		return nil, err
	}
	return s.query.Callers(nodeID, depth, limit)
}

// Callees returns nodes that the given symbol calls, traversing outgoing "calls"
// edges up to depth hops. depth=0 and limit=0 use the engine's defaults.
func (s *CodeGraphService) Callees(symbol string, depth, limit int) ([]codegraph.Node, error) {
	nodeID, err := s.resolveSymbol(symbol)
	if err != nil {
		return nil, err
	}
	return s.query.Callees(nodeID, depth, limit)
}

// Impact returns the transitive set of nodes affected by a change to symbol —
// the blast radius — following incoming calls, imports, extends, and implements
// edges up to depth hops. depth=0 and limit=0 use the engine's defaults.
func (s *CodeGraphService) Impact(symbol string, depth, limit int) ([]codegraph.Node, error) {
	nodeID, err := s.resolveSymbol(symbol)
	if err != nil {
		return nil, err
	}
	return s.query.Impact(nodeID, depth, limit)
}

// Trace finds the shortest call path between two symbols via BFS on outgoing
// "calls" edges. Returns ordered node and edge slices; both are nil when no
// path is found within maxDepth hops. maxDepth=0 uses the engine's default.
func (s *CodeGraphService) Trace(from, to string, maxDepth int) ([]codegraph.Node, []codegraph.Edge, error) {
	fromID, err := s.resolveSymbol(from)
	if err != nil {
		return nil, nil, err
	}
	toID, err := s.resolveSymbol(to)
	if err != nil {
		return nil, nil, err
	}
	return s.query.Trace(fromID, toID, maxDepth)
}

// NodeDetail returns the full node record for symbol and the source lines that
// define it, read from the filesystem. rootDir is joined with node.FilePath to
// locate the source file; when the file cannot be read, source is an empty
// string (not an error — the node record is still returned).
func (s *CodeGraphService) NodeDetail(symbol string, rootDir string) (*codegraph.Node, string, error) {
	nodeID, err := s.resolveSymbol(symbol)
	if err != nil {
		return nil, "", err
	}
	node, err := s.store.GetNode(nodeID)
	if err != nil {
		return nil, "", fmt.Errorf("service: codegraph: get node %q: %w", nodeID, err)
	}
	if node == nil {
		return nil, "", fmt.Errorf("service: codegraph: node %q not found", nodeID)
	}

	source := ""
	absPath := filepath.Join(rootDir, node.FilePath)
	if data, readErr := os.ReadFile(absPath); readErr == nil {
		lines := strings.Split(string(data), "\n")
		if node.StartLine > 0 && node.EndLine <= len(lines) {
			source = strings.Join(lines[node.StartLine-1:node.EndLine], "\n")
		}
	}
	return node, source, nil
}

// Status returns aggregate statistics about the code graph: total node, edge,
// and file counts broken down by kind and language.
func (s *CodeGraphService) Status() (*codegraph.GraphStats, error) {
	return s.store.GetStats()
}

// Files returns all tracked file records, optionally filtered by language and/or
// a filepath glob pattern (filepath.Match semantics). Both filters are applied in
// that order; an empty string disables the corresponding filter.
func (s *CodeGraphService) Files(pattern, language string) ([]codegraph.FileRecord, error) {
	files, err := s.store.ListFiles()
	if err != nil {
		return nil, err
	}
	if language != "" {
		filtered := files[:0]
		for _, f := range files {
			if f.Language == language {
				filtered = append(filtered, f)
			}
		}
		files = filtered
	}
	if pattern != "" {
		filtered := files[:0]
		for _, f := range files {
			if matched, _ := filepath.Match(pattern, f.Path); matched {
				filtered = append(filtered, f)
			}
		}
		files = filtered
	}
	return files, nil
}

// CodeGraphDBExists checks whether a codegraph database file exists for the
// given project without opening it. Returns false when the file is missing or
// inaccessible.
func CodeGraphDBExists(projectsDir, slug string) bool {
	path := codegraph.DBPath(projectsDir, slug)
	_, err := os.Stat(path)
	return err == nil
}

// resolveSymbol maps a symbol name (short or fully qualified) to its node ID.
// Resolution order:
//  1. Exact qualified_name match.
//  2. Exact name match.
//  3. FTS5 search — first result wins.
//
// Returns a descriptive error when the symbol is not found by any strategy.
func (s *CodeGraphService) resolveSymbol(symbol string) (string, error) {
	node, err := s.store.FindNodeByQualifiedName(symbol)
	if err == nil && node != nil {
		return node.ID, nil
	}

	node, err = s.store.FindNodeByName(symbol)
	if err == nil && node != nil {
		return node.ID, nil
	}

	results, err := s.store.SearchNodes(symbol, nil, nil, 1)
	if err != nil {
		return "", fmt.Errorf("service: codegraph: resolve symbol %q: %w", symbol, err)
	}
	if len(results) == 0 {
		return "", fmt.Errorf("service: codegraph: symbol %q not found", symbol)
	}
	return results[0].ID, nil
}
