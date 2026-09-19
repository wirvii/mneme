package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/quality"
)

// ErrInvalidAffectedRequest reports an invalid or ambiguous affected query.
var ErrInvalidAffectedRequest = errors.New("service: codegraph: invalid affected request")

type affectedGit interface {
	HeadSHA() (string, error)
	MergeBase(string, string) (string, error)
	ChangedFilesInRange(string, string) ([]quality.FileChange, error)
	IsDirty() (bool, []string, error)
}

// CodeGraphService orchestrates code graph operations. It owns the DB lifecycle
// and provides high-level, frontend-agnostic methods used by MCP tools and CLI
// commands. The service delegates to the codegraph package for all persistence
// and graph-traversal concerns.
type CodeGraphService struct {
	cdb         *codegraph.CodeGraphDB
	store       *codegraph.Store
	query       *codegraph.QueryEngine
	affectedGit affectedGit
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
	return &CodeGraphService{cdb: cdb, store: store, query: query, affectedGit: newAffectedGit()}, nil
}

// NewCodeGraphServiceFromDB creates a service from an already-open CodeGraphDB.
// Ownership of cdb is NOT transferred — the caller is responsible for calling
// cdb.Close(). Primarily used in tests that need an in-memory database.
func NewCodeGraphServiceFromDB(cdb *codegraph.CodeGraphDB) *CodeGraphService {
	store := codegraph.NewStore(cdb)
	query := codegraph.NewQueryEngine(store)
	return &CodeGraphService{cdb: cdb, store: store, query: query, affectedGit: newAffectedGit()}
}

func newAffectedGit() affectedGit {
	repoDir, err := os.Getwd()
	if err != nil {
		repoDir = "."
	}
	return &quality.Git{RepoDir: repoDir}
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

// Affected resolves explicit changed paths or a Git-derived path set and asks
// the graph query layer for their reverse dependency closure. It never mutates
// Git, source files, graph records, or graph metadata.
func (s *CodeGraphService) Affected(req codegraph.AffectedRequest) (codegraph.AffectedResult, error) {
	if len(req.Paths) > 0 && (req.Base != "" || req.Head != "") {
		return codegraph.AffectedResult{}, fmt.Errorf("%w: paths cannot be combined with base or head", ErrInvalidAffectedRequest)
	}
	if req.Depth < 0 || req.Limit < 0 {
		return codegraph.AffectedResult{}, fmt.Errorf("%w: depth and limit cannot be negative", ErrInvalidAffectedRequest)
	}

	indexedSHA, err := s.store.GetMetadata(codegraph.MetaKeyLastIndexedSHA)
	if err != nil {
		return codegraph.AffectedResult{}, fmt.Errorf("service: codegraph: affected indexed SHA: %w", err)
	}
	currentHead, headErr := s.affectedGit.HeadSHA()
	if headErr != nil && len(req.Paths) == 0 {
		return codegraph.AffectedResult{}, fmt.Errorf("service: codegraph: affected HEAD: %w", headErr)
	}

	paths := req.Paths
	untracked := make([]string, 0)
	forcedMissing := make([]string, 0)
	comparisonHead := currentHead
	switch {
	case len(req.Paths) > 0:
		// Explicit paths need no Git diff. HEAD is used only when available to
		// disclose that the graph was indexed at another revision.
	case req.Base != "" || req.Head != "":
		head := req.Head
		if head == "" {
			head = currentHead
		}
		base := req.Base
		if base == "" {
			base = indexedSHA
		}
		if base == "" || head == "" {
			return codegraph.AffectedResult{}, fmt.Errorf("%w: a Git range requires a base and head", ErrInvalidAffectedRequest)
		}
		mergeBase, err := s.affectedGit.MergeBase(base, head)
		if err != nil {
			return codegraph.AffectedResult{}, fmt.Errorf("service: codegraph: affected merge base: %w", err)
		}
		changes, err := s.affectedGit.ChangedFilesInRange(mergeBase, head)
		if err != nil {
			return codegraph.AffectedResult{}, fmt.Errorf("service: codegraph: affected changed files: %w", err)
		}
		paths = make([]string, 0, len(changes))
		for _, change := range changes {
			paths = append(paths, change.Path)
			if change.Status == quality.FileStatusDeleted {
				forcedMissing = append(forcedMissing, change.Path)
			}
		}
		comparisonHead = head
	default:
		_, dirtyPaths, err := s.affectedGit.IsDirty()
		if err != nil {
			return codegraph.AffectedResult{}, fmt.Errorf("service: codegraph: affected worktree: %w", err)
		}
		paths, untracked = affectedWorktreePaths(dirtyPaths)
	}

	paths, err = normalizeAffectedPaths(paths)
	if err != nil {
		return codegraph.AffectedResult{}, err
	}
	result, err := s.query.Affected(paths, req.Depth, req.Limit)
	if err != nil {
		return codegraph.AffectedResult{}, fmt.Errorf("service: codegraph: affected query: %w", err)
	}
	result.UntrackedPaths, err = normalizeAffectedPaths(untracked)
	if err != nil {
		return codegraph.AffectedResult{}, err
	}
	result.MissingPaths, err = normalizeAffectedPaths(append(result.MissingPaths, forcedMissing...))
	if err != nil {
		return codegraph.AffectedResult{}, err
	}
	result.IndexedSHA = indexedSHA
	result.Stale = indexedSHA != "" && comparisonHead != "" && indexedSHA != comparisonHead
	stats, err := s.store.GetStats()
	if err != nil {
		return codegraph.AffectedResult{}, fmt.Errorf("service: codegraph: affected graph status: %w", err)
	}
	result.MissingGraph = stats.NodeCount == 0
	return result, nil
}

func normalizeAffectedPaths(paths []string) ([]string, error) {
	normalized := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%w: path %q must stay inside the repository", ErrInvalidAffectedRequest, path)
		}
		clean = filepath.ToSlash(strings.TrimPrefix(clean, "."+string(filepath.Separator)))
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		normalized = append(normalized, clean)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func affectedWorktreePaths(porcelain []string) (paths, untracked []string) {
	for _, line := range porcelain {
		if len(line) < 4 {
			continue
		}
		status := line[:2]
		path := strings.TrimSpace(line[3:])
		if arrow := strings.LastIndex(path, " -> "); arrow >= 0 {
			path = path[arrow+4:]
		}
		paths = append(paths, path)
		if status == "??" {
			untracked = append(untracked, path)
		}
	}
	return paths, untracked
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

// DegradedLanguages returns the languages this graph could not fully index
// (SPEC-142 D1) — a thin pass-through to the store, added so MCP/CLI
// presentation code never needs to reach past the service into store/db
// directly. No logic beyond the store call: see internal/codegraph for the
// actual contract and its D16 fail-open-to-synthetic-record behaviour.
func (s *CodeGraphService) DegradedLanguages() ([]codegraph.DegradedLanguage, error) {
	return s.store.GetDegradedLanguages()
}

// GraphNotice combines DegradedLanguages with codegraph.Notice so a caller
// that already has a live CodeGraphService (MCP handlers, the CLI's
// PersistentPreRunE) does not need to repeat that two-step dance itself. It
// is NOT used on the pre-tool-use hook's hot path — that path uses
// codegraph.ProbeDegraded instead, precisely so probing for a notice never
// opens (or creates) a database of its own.
func (s *CodeGraphService) GraphNotice() (string, bool) {
	langs, err := s.DegradedLanguages()
	return codegraph.Notice(langs, err)
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
