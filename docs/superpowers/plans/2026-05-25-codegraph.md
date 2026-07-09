# CodeGraph Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replicate codegraph (semantic code knowledge graph) in Go as an internal module of mneme, enabling agents to query code structure without reading files.

**Architecture:** New package `internal/codegraph/` with its own SQLite DB per project (`<slug>-codegraph.db`). Uses `go/ast` for Go parsing and a Node.js subprocess for TS/JS. Exposed via 10 MCP tools and CLI subcommands under `mneme codegraph`. Connected to mneme's memory graph via a lightweight bridge.

**Tech Stack:** Go stdlib (`go/ast`, `go/parser`, `go/types`), SQLite + FTS5 (mattn/go-sqlite3), Node.js ≥18 (optional, for TS/JS), Cobra (CLI).

---

## File Structure

```
internal/codegraph/
  model.go              — Node, Edge, NodeKind, EdgeKind, FileRecord, ExtractionResult, etc.
  model_test.go         — Validation tests for model types
  db.go                 — Open, Close, InitSchema (embedded SQL)
  db_test.go            — Schema creation and migration tests
  schema.sql            — Embedded SQL schema (nodes, edges, files, FTS5, indexes)
  store.go              — CRUD nodes/edges/files, FTS5 search, batch upsert
  store_test.go         — Store tests against in-memory SQLite
  extractor.go          — Extractor interface, registry, language detection by extension
  extractor_go.go       — Go extractor using go/ast
  extractor_go_test.go  — Go extractor tests with real Go source
  extractor_ts.go       — TypeScript extractor via Node.js subprocess
  extractor_ts_test.go  — TS extractor tests (skipped if no Node.js)
  js/extract.js         — Node.js script for TS/JS extraction (embedded)
  indexer.go            — Orchestrates: scan dir, dispatch extractors, persist results
  indexer_test.go       — Indexer integration tests
  resolver.go           — Post-extraction reference resolution
  resolver_test.go      — Resolver tests
  query.go              — Callers, Callees, Impact (BFS), Trace (BFS shortest path)
  query_test.go         — Query traversal tests

internal/service/
  codegraph.go          — CodeGraphService: Index, Search, Context, Callers, etc.
  codegraph_test.go     — Service integration tests

internal/mcp/
  tools.go              — +10 codegraph_* tool definitions appended to allTools()
  handlers.go           — +10 codegraph_* handlers in handleToolCall switch
  handlers_codegraph_test.go — MCP handler tests for codegraph tools

internal/cli/
  codegraph.go          — newCodegraphCmd() with subcommands: index, status, search, callers, callees, impact, node, trace, files
  codegraph_test.go     — CLI tests
  root.go               — Register newCodegraphCmd()
```

---

## Task 1: Model types

**Files:**
- Create: `internal/codegraph/model.go`
- Create: `internal/codegraph/model_test.go`

- [ ] **Step 1: Write validation tests for NodeKind and EdgeKind**

```go
// internal/codegraph/model_test.go
package codegraph

import "testing"

func TestNodeKind_Valid(t *testing.T) {
	tests := []struct {
		kind NodeKind
		want bool
	}{
		{NodeFunction, true},
		{NodeStruct, true},
		{NodeKind("bogus"), false},
		{NodeKind(""), false},
	}
	for _, tt := range tests {
		if got := tt.kind.Valid(); got != tt.want {
			t.Errorf("NodeKind(%q).Valid() = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

func TestEdgeKind_Valid(t *testing.T) {
	tests := []struct {
		kind EdgeKind
		want bool
	}{
		{EdgeCalls, true},
		{EdgeContains, true},
		{EdgeKind("bogus"), false},
	}
	for _, tt := range tests {
		if got := tt.kind.Valid(); got != tt.want {
			t.Errorf("EdgeKind(%q).Valid() = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

func TestNodeID_Deterministic(t *testing.T) {
	id1 := NodeID("internal/service/search.go", "service.Search")
	id2 := NodeID("internal/service/search.go", "service.Search")
	if id1 != id2 {
		t.Errorf("NodeID not deterministic: %s != %s", id1, id2)
	}
	id3 := NodeID("internal/service/search.go", "service.Save")
	if id1 == id3 {
		t.Error("NodeID collision for different symbols")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestNodeKind|TestEdgeKind|TestNodeID" -v`
Expected: FAIL — package does not exist yet.

- [ ] **Step 3: Implement model.go**

```go
// internal/codegraph/model.go
package codegraph

import (
	"crypto/sha256"
	"encoding/hex"
)

// NodeKind classifies code symbols in the knowledge graph.
type NodeKind string

const (
	NodeFile       NodeKind = "file"
	NodeModule     NodeKind = "module"
	NodeClass      NodeKind = "class"
	NodeStruct     NodeKind = "struct"
	NodeInterface  NodeKind = "interface"
	NodeTrait      NodeKind = "trait"
	NodeProtocol   NodeKind = "protocol"
	NodeFunction   NodeKind = "function"
	NodeMethod     NodeKind = "method"
	NodeProperty   NodeKind = "property"
	NodeField      NodeKind = "field"
	NodeVariable   NodeKind = "variable"
	NodeConstant   NodeKind = "constant"
	NodeEnum       NodeKind = "enum"
	NodeEnumMember NodeKind = "enum_member"
	NodeTypeAlias  NodeKind = "type_alias"
	NodeNamespace  NodeKind = "namespace"
	NodeParameter  NodeKind = "parameter"
	NodeImport     NodeKind = "import"
	NodeExport     NodeKind = "export"
	NodeRoute      NodeKind = "route"
	NodeComponent  NodeKind = "component"
)

var validNodeKinds = map[NodeKind]struct{}{
	NodeFile: {}, NodeModule: {}, NodeClass: {}, NodeStruct: {},
	NodeInterface: {}, NodeTrait: {}, NodeProtocol: {}, NodeFunction: {},
	NodeMethod: {}, NodeProperty: {}, NodeField: {}, NodeVariable: {},
	NodeConstant: {}, NodeEnum: {}, NodeEnumMember: {}, NodeTypeAlias: {},
	NodeNamespace: {}, NodeParameter: {}, NodeImport: {}, NodeExport: {},
	NodeRoute: {}, NodeComponent: {},
}

func (k NodeKind) Valid() bool {
	_, ok := validNodeKinds[k]
	return ok
}

// EdgeKind defines relationship types between code nodes.
type EdgeKind string

const (
	EdgeContains     EdgeKind = "contains"
	EdgeCalls        EdgeKind = "calls"
	EdgeImports      EdgeKind = "imports"
	EdgeExports      EdgeKind = "exports"
	EdgeExtends      EdgeKind = "extends"
	EdgeImplements   EdgeKind = "implements"
	EdgeReferences   EdgeKind = "references"
	EdgeTypeOf       EdgeKind = "type_of"
	EdgeReturns      EdgeKind = "returns"
	EdgeInstantiates EdgeKind = "instantiates"
	EdgeOverrides    EdgeKind = "overrides"
	EdgeDecorates    EdgeKind = "decorates"
)

var validEdgeKinds = map[EdgeKind]struct{}{
	EdgeContains: {}, EdgeCalls: {}, EdgeImports: {}, EdgeExports: {},
	EdgeExtends: {}, EdgeImplements: {}, EdgeReferences: {}, EdgeTypeOf: {},
	EdgeReturns: {}, EdgeInstantiates: {}, EdgeOverrides: {}, EdgeDecorates: {},
}

func (k EdgeKind) Valid() bool {
	_, ok := validEdgeKinds[k]
	return ok
}

// NodeID produces a deterministic hash ID from file path and qualified name.
func NodeID(filePath, qualifiedName string) string {
	h := sha256.Sum256([]byte(filePath + ":" + qualifiedName))
	return hex.EncodeToString(h[:8])
}

// Node represents a code symbol in the graph.
type Node struct {
	ID             string   `json:"id"`
	Kind           NodeKind `json:"kind"`
	Name           string   `json:"name"`
	QualifiedName  string   `json:"qualified_name"`
	FilePath       string   `json:"file_path"`
	Language       string   `json:"language"`
	StartLine      int      `json:"start_line"`
	EndLine        int      `json:"end_line"`
	StartColumn    int      `json:"start_column"`
	EndColumn      int      `json:"end_column"`
	Docstring      string   `json:"docstring,omitempty"`
	Signature      string   `json:"signature,omitempty"`
	Visibility     string   `json:"visibility,omitempty"`
	IsExported     bool     `json:"is_exported"`
	IsAsync        bool     `json:"is_async"`
	IsStatic       bool     `json:"is_static"`
	IsAbstract     bool     `json:"is_abstract"`
	Decorators     []string `json:"decorators,omitempty"`
	TypeParameters []string `json:"type_parameters,omitempty"`
	UpdatedAt      int64    `json:"updated_at"`
}

// Edge represents a directed relationship between two nodes.
type Edge struct {
	ID         int64    `json:"id,omitempty"`
	Source     string   `json:"source"`
	Target     string   `json:"target"`
	Kind       EdgeKind `json:"kind"`
	Metadata   string   `json:"metadata,omitempty"`
	Line       int      `json:"line,omitempty"`
	Col        int      `json:"col,omitempty"`
	Provenance string   `json:"provenance,omitempty"`
}

// FileRecord tracks an indexed source file.
type FileRecord struct {
	Path        string `json:"path"`
	ContentHash string `json:"content_hash"`
	Language    string `json:"language"`
	Size        int64  `json:"size"`
	ModifiedAt  int64  `json:"modified_at"`
	IndexedAt   int64  `json:"indexed_at"`
	NodeCount   int    `json:"node_count"`
	Errors      string `json:"errors,omitempty"`
}

// UnresolvedRef tracks a reference that could not be resolved during extraction.
type UnresolvedRef struct {
	ID            int64  `json:"id,omitempty"`
	FromNodeID    string `json:"from_node_id"`
	ReferenceName string `json:"reference_name"`
	ReferenceKind string `json:"reference_kind"`
	Line          int    `json:"line"`
	Col           int    `json:"col"`
	FilePath      string `json:"file_path"`
	Language      string `json:"language"`
	Candidates    string `json:"candidates,omitempty"`
}

// ExtractionResult is the output of parsing a single source file.
type ExtractionResult struct {
	Nodes          []Node            `json:"nodes"`
	Edges          []Edge            `json:"edges"`
	UnresolvedRefs []UnresolvedRef   `json:"unresolved_refs"`
	Errors         []ExtractionError `json:"errors"`
	DurationMs     int64             `json:"duration_ms"`
}

// ExtractionError records a non-fatal error during extraction.
type ExtractionError struct {
	Message  string `json:"message"`
	FilePath string `json:"file_path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Col      int    `json:"col,omitempty"`
	Severity string `json:"severity"`
	Code     string `json:"code,omitempty"`
}

// Extractor is the interface for language-specific code parsers.
type Extractor interface {
	Extract(filePath string, content []byte) (*ExtractionResult, error)
	Language() string
}

// GraphStats holds aggregate statistics about the code graph.
type GraphStats struct {
	NodeCount       int            `json:"node_count"`
	EdgeCount       int            `json:"edge_count"`
	FileCount       int            `json:"file_count"`
	NodesByKind     map[string]int `json:"nodes_by_kind"`
	EdgesByKind     map[string]int `json:"edges_by_kind"`
	FilesByLanguage map[string]int `json:"files_by_language"`
	DBSizeBytes     int64          `json:"db_size_bytes"`
	LastUpdated     int64          `json:"last_updated"`
}

// IndexResult summarises an indexing run.
type IndexResult struct {
	FilesScanned int   `json:"files_scanned"`
	FilesIndexed int   `json:"files_indexed"`
	FilesSkipped int   `json:"files_skipped"`
	FilesErrored int   `json:"files_errored"`
	NodesCreated int   `json:"nodes_created"`
	EdgesCreated int   `json:"edges_created"`
	DurationMs   int64 `json:"duration_ms"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestNodeKind|TestEdgeKind|TestNodeID" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/codegraph/model.go internal/codegraph/model_test.go
git commit -m "feat(codegraph): add model types — Node, Edge, NodeKind, EdgeKind, Extractor interface"
```

---

## Task 2: Database layer

**Files:**
- Create: `internal/codegraph/schema.sql`
- Create: `internal/codegraph/db.go`
- Create: `internal/codegraph/db_test.go`

- [ ] **Step 1: Write tests for DB open and schema initialization**

```go
// internal/codegraph/db_test.go
package codegraph

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDB_CreatesSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-codegraph.db")

	cdb, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer cdb.Close()

	// Verify tables exist
	tables := []string{"nodes", "edges", "files", "unresolved_refs", "project_metadata", "schema_versions"}
	for _, tbl := range tables {
		var name string
		err := cdb.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", tbl, err)
		}
	}

	// Verify FTS5 virtual table
	var ftsName string
	err = cdb.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='nodes_fts'").Scan(&ftsName)
	if err != nil {
		t.Error("nodes_fts virtual table not found")
	}
}

func TestOpenDB_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-codegraph.db")

	cdb1, err := OpenDB(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	cdb1.Close()

	cdb2, err := OpenDB(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer cdb2.Close()
}

func TestOpenDB_InMemory(t *testing.T) {
	cdb, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB(:memory:): %v", err)
	}
	defer cdb.Close()
}

func TestDBPath_ForProject(t *testing.T) {
	got := DBPath("/home/user/.mneme/projects", "wirvii-mneme")
	want := "/home/user/.mneme/projects/wirvii-mneme-codegraph.db"
	if got != want {
		t.Errorf("DBPath = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestOpenDB|TestDBPath" -v`
Expected: FAIL — OpenDB not defined.

- [ ] **Step 3: Create schema.sql**

```sql
-- internal/codegraph/schema.sql
-- CodeGraph SQLite Schema — Version 1

CREATE TABLE IF NOT EXISTS schema_versions (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL,
    description TEXT
);

INSERT OR IGNORE INTO schema_versions (version, applied_at, description)
VALUES (1, strftime('%s', 'now') * 1000, 'Initial schema');

CREATE TABLE IF NOT EXISTS nodes (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    qualified_name TEXT NOT NULL,
    file_path TEXT NOT NULL,
    language TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    start_column INTEGER NOT NULL,
    end_column INTEGER NOT NULL,
    docstring TEXT,
    signature TEXT,
    visibility TEXT,
    is_exported INTEGER DEFAULT 0,
    is_async INTEGER DEFAULT 0,
    is_static INTEGER DEFAULT 0,
    is_abstract INTEGER DEFAULT 0,
    decorators TEXT,
    type_parameters TEXT,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    target TEXT NOT NULL,
    kind TEXT NOT NULL,
    metadata TEXT,
    line INTEGER,
    col INTEGER,
    provenance TEXT DEFAULT NULL,
    FOREIGN KEY (source) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (target) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS files (
    path TEXT PRIMARY KEY,
    content_hash TEXT NOT NULL,
    language TEXT NOT NULL,
    size INTEGER NOT NULL,
    modified_at INTEGER NOT NULL,
    indexed_at INTEGER NOT NULL,
    node_count INTEGER DEFAULT 0,
    errors TEXT
);

CREATE TABLE IF NOT EXISTS unresolved_refs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_node_id TEXT NOT NULL,
    reference_name TEXT NOT NULL,
    reference_kind TEXT NOT NULL,
    line INTEGER NOT NULL,
    col INTEGER NOT NULL,
    candidates TEXT,
    file_path TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT 'unknown',
    FOREIGN KEY (from_node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS project_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
    id,
    name,
    qualified_name,
    docstring,
    signature,
    content='nodes',
    content_rowid='rowid'
);

CREATE TRIGGER IF NOT EXISTS nodes_ai AFTER INSERT ON nodes BEGIN
    INSERT INTO nodes_fts(rowid, id, name, qualified_name, docstring, signature)
    VALUES (NEW.rowid, NEW.id, NEW.name, NEW.qualified_name, NEW.docstring, NEW.signature);
END;

CREATE TRIGGER IF NOT EXISTS nodes_ad AFTER DELETE ON nodes BEGIN
    INSERT INTO nodes_fts(nodes_fts, rowid, id, name, qualified_name, docstring, signature)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.name, OLD.qualified_name, OLD.docstring, OLD.signature);
END;

CREATE TRIGGER IF NOT EXISTS nodes_au AFTER UPDATE ON nodes BEGIN
    INSERT INTO nodes_fts(nodes_fts, rowid, id, name, qualified_name, docstring, signature)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.name, OLD.qualified_name, OLD.docstring, OLD.signature);
    INSERT INTO nodes_fts(rowid, id, name, qualified_name, docstring, signature)
    VALUES (NEW.rowid, NEW.id, NEW.name, NEW.qualified_name, NEW.docstring, NEW.signature);
END;

CREATE INDEX IF NOT EXISTS idx_nodes_kind ON nodes(kind);
CREATE INDEX IF NOT EXISTS idx_nodes_name ON nodes(name);
CREATE INDEX IF NOT EXISTS idx_nodes_qualified_name ON nodes(qualified_name);
CREATE INDEX IF NOT EXISTS idx_nodes_file_path ON nodes(file_path);
CREATE INDEX IF NOT EXISTS idx_nodes_language ON nodes(language);
CREATE INDEX IF NOT EXISTS idx_nodes_file_line ON nodes(file_path, start_line);
CREATE INDEX IF NOT EXISTS idx_nodes_lower_name ON nodes(lower(name));

CREATE INDEX IF NOT EXISTS idx_edges_kind ON edges(kind);
CREATE INDEX IF NOT EXISTS idx_edges_source_kind ON edges(source, kind);
CREATE INDEX IF NOT EXISTS idx_edges_target_kind ON edges(target, kind);
CREATE INDEX IF NOT EXISTS idx_edges_provenance ON edges(provenance);

CREATE INDEX IF NOT EXISTS idx_files_language ON files(language);
CREATE INDEX IF NOT EXISTS idx_files_modified_at ON files(modified_at);

CREATE INDEX IF NOT EXISTS idx_unresolved_from_node ON unresolved_refs(from_node_id);
CREATE INDEX IF NOT EXISTS idx_unresolved_name ON unresolved_refs(reference_name);
CREATE INDEX IF NOT EXISTS idx_unresolved_file_path ON unresolved_refs(file_path);
CREATE INDEX IF NOT EXISTS idx_unresolved_from_name ON unresolved_refs(from_node_id, reference_name);
```

- [ ] **Step 4: Implement db.go**

```go
// internal/codegraph/db.go
package codegraph

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schemaSQL string

// CodeGraphDB wraps a SQLite connection for the code graph.
type CodeGraphDB struct {
	DB   *sql.DB
	Path string
}

// DBPath returns the conventional codegraph DB path for a project slug.
func DBPath(projectsDir, slug string) string {
	return filepath.Join(projectsDir, slug+"-codegraph.db")
}

// OpenDB opens or creates a codegraph SQLite database at path.
// Pass ":memory:" for in-memory test databases.
func OpenDB(path string) (*CodeGraphDB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("codegraph: open db: mkdir: %w", err)
		}
	}

	dsn := path
	if path != ":memory:" {
		dsn = fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=ON&_busy_timeout=5000&_synchronous=NORMAL", path)
	} else {
		dsn = "file::memory:?_foreign_keys=ON"
	}

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("codegraph: open db: %w", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("codegraph: open db: ping: %w", err)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("codegraph: open db: init schema: %w", err)
	}

	return &CodeGraphDB{DB: db, Path: path}, nil
}

// Close closes the underlying database connection.
func (c *CodeGraphDB) Close() error {
	return c.DB.Close()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestOpenDB|TestDBPath" -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/codegraph/schema.sql internal/codegraph/db.go internal/codegraph/db_test.go
git commit -m "feat(codegraph): add SQLite database layer with embedded schema"
```

---

## Task 3: Store layer

**Files:**
- Create: `internal/codegraph/store.go`
- Create: `internal/codegraph/store_test.go`

- [ ] **Step 1: Write store tests**

```go
// internal/codegraph/store_test.go
package codegraph

import (
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	cdb, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { cdb.Close() })
	return NewStore(cdb)
}

func TestStore_UpsertNode(t *testing.T) {
	s := newTestStore(t)
	node := Node{
		ID: NodeID("main.go", "main"), Kind: NodeFunction,
		Name: "main", QualifiedName: "main",
		FilePath: "main.go", Language: "go",
		StartLine: 5, EndLine: 10, StartColumn: 0, EndColumn: 1,
		IsExported: false, UpdatedAt: time.Now().UnixMilli(),
	}

	err := s.UpsertNode(node)
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	got, err := s.GetNode(node.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got.Name != "main" || got.Kind != NodeFunction {
		t.Errorf("got name=%q kind=%q, want main/function", got.Name, got.Kind)
	}
}

func TestStore_UpsertEdge(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()
	src := Node{ID: "src1", Kind: NodeFunction, Name: "caller", QualifiedName: "pkg.caller",
		FilePath: "a.go", Language: "go", StartLine: 1, EndLine: 5, UpdatedAt: now}
	tgt := Node{ID: "tgt1", Kind: NodeFunction, Name: "callee", QualifiedName: "pkg.callee",
		FilePath: "b.go", Language: "go", StartLine: 1, EndLine: 5, UpdatedAt: now}
	_ = s.UpsertNode(src)
	_ = s.UpsertNode(tgt)

	edge := Edge{Source: "src1", Target: "tgt1", Kind: EdgeCalls, Line: 3, Provenance: "ast"}
	err := s.UpsertEdge(edge)
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	edges, err := s.GetEdgesFrom("src1", "")
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 1 || edges[0].Kind != EdgeCalls {
		t.Errorf("got %d edges, want 1 calls edge", len(edges))
	}
}

func TestStore_SearchNodes_FTS5(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()
	nodes := []Node{
		{ID: "n1", Kind: NodeFunction, Name: "SaveMemory", QualifiedName: "service.SaveMemory",
			FilePath: "svc.go", Language: "go", StartLine: 1, EndLine: 10, UpdatedAt: now},
		{ID: "n2", Kind: NodeFunction, Name: "SearchMemory", QualifiedName: "service.SearchMemory",
			FilePath: "svc.go", Language: "go", StartLine: 12, EndLine: 20, UpdatedAt: now},
		{ID: "n3", Kind: NodeStruct, Name: "MemoryStore", QualifiedName: "store.MemoryStore",
			FilePath: "store.go", Language: "go", StartLine: 1, EndLine: 30, UpdatedAt: now},
	}
	for _, n := range nodes {
		if err := s.UpsertNode(n); err != nil {
			t.Fatalf("UpsertNode(%s): %v", n.ID, err)
		}
	}

	results, err := s.SearchNodes("Memory", nil, nil, 10)
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("got %d results, want >= 2", len(results))
	}
}

func TestStore_DeleteNodesByFile(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()
	_ = s.UpsertNode(Node{ID: "n1", Kind: NodeFunction, Name: "Foo",
		QualifiedName: "pkg.Foo", FilePath: "a.go", Language: "go",
		StartLine: 1, EndLine: 5, UpdatedAt: now})
	_ = s.UpsertNode(Node{ID: "n2", Kind: NodeFunction, Name: "Bar",
		QualifiedName: "pkg.Bar", FilePath: "a.go", Language: "go",
		StartLine: 7, EndLine: 12, UpdatedAt: now})

	deleted, err := s.DeleteNodesByFile("a.go")
	if err != nil {
		t.Fatalf("DeleteNodesByFile: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted %d, want 2", deleted)
	}
}

func TestStore_UpsertFile(t *testing.T) {
	s := newTestStore(t)
	fr := FileRecord{
		Path: "main.go", ContentHash: "abc123", Language: "go",
		Size: 1024, ModifiedAt: time.Now().UnixMilli(),
		IndexedAt: time.Now().UnixMilli(), NodeCount: 5,
	}
	err := s.UpsertFile(fr)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	got, err := s.GetFile("main.go")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.ContentHash != "abc123" {
		t.Errorf("hash = %q, want abc123", got.ContentHash)
	}
}

func TestStore_GetStats(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()
	_ = s.UpsertNode(Node{ID: "n1", Kind: NodeFunction, Name: "Foo",
		QualifiedName: "pkg.Foo", FilePath: "a.go", Language: "go",
		StartLine: 1, EndLine: 5, UpdatedAt: now})
	_ = s.UpsertFile(FileRecord{Path: "a.go", ContentHash: "x", Language: "go",
		Size: 100, ModifiedAt: now, IndexedAt: now, NodeCount: 1})

	stats, err := s.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.NodeCount != 1 {
		t.Errorf("NodeCount = %d, want 1", stats.NodeCount)
	}
	if stats.FileCount != 1 {
		t.Errorf("FileCount = %d, want 1", stats.FileCount)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestStore_" -v`
Expected: FAIL — Store type not defined.

- [ ] **Step 3: Implement store.go**

Implement `Store` struct wrapping `*CodeGraphDB` with methods:
- `NewStore(cdb *CodeGraphDB) *Store`
- `UpsertNode(n Node) error` — INSERT OR REPLACE
- `GetNode(id string) (*Node, error)`
- `UpsertEdge(e Edge) error` — INSERT (edges are replaced by file deletion cascade)
- `GetEdgesFrom(nodeID string, kind string) ([]Edge, error)`
- `GetEdgesTo(nodeID string, kind string) ([]Edge, error)`
- `SearchNodes(query string, kinds []NodeKind, languages []string, limit int) ([]Node, error)` — FTS5
- `DeleteNodesByFile(filePath string) (int64, error)` — CASCADE deletes edges
- `UpsertFile(f FileRecord) error`
- `GetFile(path string) (*FileRecord, error)`
- `ListFiles() ([]FileRecord, error)`
- `DeleteFile(path string) error`
- `GetStats() (*GraphStats, error)`
- `BatchUpsertNodes(nodes []Node) error` — transaction wrapper for bulk inserts
- `BatchUpsertEdges(edges []Edge) error`

Each method uses raw SQL queries (no ORM), consistent with mneme's `internal/store/` patterns.

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestStore_" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/codegraph/store.go internal/codegraph/store_test.go
git commit -m "feat(codegraph): add store layer — CRUD, FTS5 search, batch operations"
```

---

## Task 4: Go extractor

**Files:**
- Create: `internal/codegraph/extractor.go`
- Create: `internal/codegraph/extractor_go.go`
- Create: `internal/codegraph/extractor_go_test.go`

- [ ] **Step 1: Write Go extractor tests**

```go
// internal/codegraph/extractor_go_test.go
package codegraph

import "testing"

const testGoSource = `package service

import (
	"context"
	"fmt"

	"github.com/wirvii/mneme/internal/store"
)

// MemoryService orchestrates memory operations.
type MemoryService struct {
	store *store.MemoryStore
}

// Save persists a new memory.
func (s *MemoryService) Save(ctx context.Context, title string) error {
	fmt.Println("saving")
	return s.store.Create(ctx, title)
}

// Search finds memories matching a query.
func Search(query string) []string {
	return nil
}

type Searcher interface {
	Search(q string) []string
}
`

func TestGoExtractor_ExtractsFunctions(t *testing.T) {
	ext := NewGoExtractor()
	result, err := ext.Extract("service.go", []byte(testGoSource))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	funcs := filterNodes(result.Nodes, NodeFunction)
	if len(funcs) != 1 {
		t.Fatalf("got %d functions, want 1 (Search)", len(funcs))
	}
	if funcs[0].Name != "Search" {
		t.Errorf("function name = %q, want Search", funcs[0].Name)
	}
	if !funcs[0].IsExported {
		t.Error("Search should be exported")
	}
}

func TestGoExtractor_ExtractsMethods(t *testing.T) {
	ext := NewGoExtractor()
	result, err := ext.Extract("service.go", []byte(testGoSource))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	methods := filterNodes(result.Nodes, NodeMethod)
	if len(methods) != 1 {
		t.Fatalf("got %d methods, want 1 (Save)", len(methods))
	}
	if methods[0].Name != "Save" {
		t.Errorf("method name = %q, want Save", methods[0].Name)
	}
	if methods[0].QualifiedName != "MemoryService.Save" {
		t.Errorf("qualified = %q, want MemoryService.Save", methods[0].QualifiedName)
	}
}

func TestGoExtractor_ExtractsStructs(t *testing.T) {
	ext := NewGoExtractor()
	result, err := ext.Extract("service.go", []byte(testGoSource))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	structs := filterNodes(result.Nodes, NodeStruct)
	if len(structs) != 1 || structs[0].Name != "MemoryService" {
		t.Errorf("structs = %v, want [MemoryService]", nodeNames(structs))
	}
}

func TestGoExtractor_ExtractsInterfaces(t *testing.T) {
	ext := NewGoExtractor()
	result, err := ext.Extract("service.go", []byte(testGoSource))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	ifaces := filterNodes(result.Nodes, NodeInterface)
	if len(ifaces) != 1 || ifaces[0].Name != "Searcher" {
		t.Errorf("interfaces = %v, want [Searcher]", nodeNames(ifaces))
	}
}

func TestGoExtractor_ExtractsImports(t *testing.T) {
	ext := NewGoExtractor()
	result, err := ext.Extract("service.go", []byte(testGoSource))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	imports := filterNodes(result.Nodes, NodeImport)
	if len(imports) != 3 {
		t.Errorf("got %d imports, want 3", len(imports))
	}
}

func TestGoExtractor_ContainsEdges(t *testing.T) {
	ext := NewGoExtractor()
	result, err := ext.Extract("service.go", []byte(testGoSource))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	contains := filterEdges(result.Edges, EdgeContains)
	if len(contains) == 0 {
		t.Error("expected contains edges (file→symbols)")
	}
}

func TestGoExtractor_CallEdges(t *testing.T) {
	ext := NewGoExtractor()
	result, err := ext.Extract("service.go", []byte(testGoSource))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	calls := filterEdges(result.Edges, EdgeCalls)
	// Save calls fmt.Println and s.store.Create
	if len(calls) < 1 {
		t.Errorf("got %d call edges, want >= 1", len(calls))
	}
}

func TestGoExtractor_Docstring(t *testing.T) {
	ext := NewGoExtractor()
	result, err := ext.Extract("service.go", []byte(testGoSource))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	for _, n := range result.Nodes {
		if n.Name == "MemoryService" && n.Kind == NodeStruct {
			if n.Docstring == "" {
				t.Error("MemoryService should have docstring")
			}
			return
		}
	}
	t.Error("MemoryService struct not found")
}

func TestGoExtractor_Language(t *testing.T) {
	ext := NewGoExtractor()
	if ext.Language() != "go" {
		t.Errorf("Language() = %q, want go", ext.Language())
	}
}

// helpers
func filterNodes(nodes []Node, kind NodeKind) []Node {
	var out []Node
	for _, n := range nodes {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

func filterEdges(edges []Edge, kind EdgeKind) []Edge {
	var out []Edge
	for _, e := range edges {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func nodeNames(nodes []Node) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestGoExtractor_" -v`
Expected: FAIL — NewGoExtractor not defined.

- [ ] **Step 3: Implement extractor.go (interface + registry)**

```go
// internal/codegraph/extractor.go
package codegraph

import "path/filepath"

var extractorRegistry = map[string]func() Extractor{}

// RegisterExtractor adds an extractor factory to the registry.
func RegisterExtractor(lang string, factory func() Extractor) {
	extractorRegistry[lang] = factory
}

// DetectLanguage returns the language for a file based on extension.
func DetectLanguage(filePath string) string {
	switch filepath.Ext(filePath) {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs":
		return "javascript"
	default:
		return ""
	}
}

// GetExtractor returns the appropriate extractor for the given language.
func GetExtractor(language string) Extractor {
	factory, ok := extractorRegistry[language]
	if !ok {
		return nil
	}
	return factory()
}

func init() {
	RegisterExtractor("go", func() Extractor { return NewGoExtractor() })
}
```

- [ ] **Step 4: Implement extractor_go.go**

Implement `GoExtractor` struct with `Extract(filePath string, content []byte) (*ExtractionResult, error)`:
- Uses `go/parser.ParseFile` with `parser.ParseComments`
- Visits `*ast.FuncDecl` → function or method (if has receiver)
- Visits `*ast.GenDecl` with `token.TYPE` → struct/interface/type_alias
- Visits `*ast.GenDecl` with `token.VAR`/`token.CONST` → variable/constant
- Visits `*ast.ImportSpec` → import
- Creates file node as root container
- Generates `contains` edges from file → top-level declarations
- Generates `calls` edges by walking `ast.CallExpr` inside function bodies
- Extracts signatures from `*ast.FuncType`
- Sets `IsExported` via `ast.IsExported(name)`
- Extracts docstrings from associated `*ast.CommentGroup`
- Sets `Provenance = "ast"` on all edges

- [ ] **Step 5: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestGoExtractor_" -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/codegraph/extractor.go internal/codegraph/extractor_go.go internal/codegraph/extractor_go_test.go
git commit -m "feat(codegraph): add Go extractor using go/ast — functions, structs, interfaces, calls"
```

---

## Task 5: Indexer

**Files:**
- Create: `internal/codegraph/indexer.go`
- Create: `internal/codegraph/indexer_test.go`

- [ ] **Step 1: Write indexer tests**

Tests should create a temp directory with `.go` files, run the indexer, and verify nodes/edges were persisted. Test cases:
- `TestIndexer_IndexesGoFiles` — creates 2 .go files, verifies nodes created
- `TestIndexer_Incremental` — indexes, modifies one file, re-indexes, verifies only that file re-processed
- `TestIndexer_RespectsGitignore` — creates vendor/ directory, verifies skipped
- `TestIndexer_DeletedFile` — indexes, deletes a file, re-indexes, verifies nodes removed
- `TestIndexer_DryRun` — verifies no DB writes with DryRun=true
- `TestIndexer_Force` — verifies all files re-indexed even if hash unchanged

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestIndexer_" -v`
Expected: FAIL

- [ ] **Step 3: Implement indexer.go**

```go
// internal/codegraph/indexer.go
package codegraph

// IndexOptions configures an indexing run.
type IndexOptions struct {
	RootDir  string
	Language string // force language (empty = auto-detect)
	Force    bool   // re-index all, ignore hashes
	DryRun   bool   // report only, no writes
}

// Indexer orchestrates code extraction and persistence.
type Indexer struct {
	store *Store
}

// NewIndexer creates an Indexer backed by the given store.
func NewIndexer(store *Store) *Indexer {
	return &Indexer{store: store}
}

// Index scans RootDir, dispatches extractors, and persists results.
func (ix *Indexer) Index(opts IndexOptions) (*IndexResult, error) {
    // 1. Walk directory, respect .gitignore patterns
    // 2. For each supported file:
    //    a. Compute content_hash (SHA256)
    //    b. If not Force and hash matches stored file record → skip
    //    c. Detect language, get extractor
    //    d. Extract nodes/edges/unresolved_refs
    //    e. If not DryRun:
    //       - Delete existing nodes for this file (CASCADE removes edges)
    //       - BatchUpsertNodes + BatchUpsertEdges
    //       - UpsertFile record
    // 3. For files in DB but not on disk → delete (CASCADE)
    // 4. Return IndexResult summary
}
```

Key implementation details:
- Walk using `filepath.WalkDir`
- Skip directories matching common ignore patterns: `.git`, `vendor`, `node_modules`, `dist`, `build`, `.codegraph`
- Parse `.gitignore` if present (use `github.com/go-git/go-git/v5/plumbing/format/gitignore` or simple glob matching)
- Content hash: `crypto/sha256` of file bytes
- Batch size for DB operations: 500 nodes per transaction

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestIndexer_" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/codegraph/indexer.go internal/codegraph/indexer_test.go
git commit -m "feat(codegraph): add indexer — incremental scan, gitignore, force/dry-run"
```

---

## Task 6: Query engine

**Files:**
- Create: `internal/codegraph/query.go`
- Create: `internal/codegraph/query_test.go`

- [ ] **Step 1: Write query tests**

Tests need a pre-populated store with a known graph structure. Create a helper that builds:
```
file:a.go
  └─ contains → func:A
  └─ contains → func:B
func:A
  └─ calls → func:B
  └─ calls → func:C (in file b.go)
file:b.go
  └─ contains → func:C
  └─ contains → func:D
func:C
  └─ calls → func:D
```

Test cases:
- `TestQuery_Callers` — callers of B = [A], callers of C = [A]
- `TestQuery_Callees` — callees of A = [B, C]
- `TestQuery_CallersDepth2` — callers of D at depth 2 = [C, A]
- `TestQuery_Impact` — impact of D = [C, A] (transitive callers)
- `TestQuery_Trace` — trace from A to D = [A → C → D]
- `TestQuery_TraceNoPath` — trace between unconnected nodes returns empty
- `TestQuery_Limit` — respects limit parameter

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestQuery_" -v`
Expected: FAIL

- [ ] **Step 3: Implement query.go**

```go
// internal/codegraph/query.go
package codegraph

// QueryEngine provides graph traversal operations.
type QueryEngine struct {
	store *Store
}

func NewQueryEngine(store *Store) *QueryEngine {
	return &QueryEngine{store: store}
}

// Callers returns nodes that call/reference the given symbol (incoming edges).
func (q *QueryEngine) Callers(nodeID string, depth, limit int) ([]Node, error) {
    // BFS incoming traversal on "calls" edges
}

// Callees returns nodes that the given symbol calls/references (outgoing edges).
func (q *QueryEngine) Callees(nodeID string, depth, limit int) ([]Node, error) {
    // BFS outgoing traversal on "calls" edges
}

// Impact returns the transitive set of nodes affected by a change to the given symbol.
func (q *QueryEngine) Impact(nodeID string, depth, limit int) ([]Node, error) {
    // BFS incoming on "calls", "imports", "extends", "implements" edges
}

// Trace finds the shortest call path between two nodes using BFS.
func (q *QueryEngine) Trace(fromID, toID string, maxDepth int) ([]Node, []Edge, error) {
    // BFS from source, tracking parent pointers, reconstruct path when target found
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestQuery_" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/codegraph/query.go internal/codegraph/query_test.go
git commit -m "feat(codegraph): add query engine — callers, callees, impact, trace (BFS)"
```

---

## Task 7: Resolver

**Files:**
- Create: `internal/codegraph/resolver.go`
- Create: `internal/codegraph/resolver_test.go`

- [ ] **Step 1: Write resolver tests**

```go
func TestResolver_ResolvesCallsByQualifiedName(t *testing.T) {
    // Setup: node A has unresolved ref "pkg.Foo", node Foo exists in store
    // After resolve: unresolved ref deleted, edge A→Foo created
}

func TestResolver_UnresolvableStays(t *testing.T) {
    // Setup: node A refs "unknown.Bar", no match in store
    // After resolve: unresolved ref still present
}

func TestResolver_ResolvesImports(t *testing.T) {
    // Setup: import node refs "github.com/x/y", file node for that package exists
    // After resolve: edge import→package created
}
```

- [ ] **Step 2: Run tests, verify fail**

- [ ] **Step 3: Implement resolver.go**

The resolver iterates over `unresolved_refs`, attempts to match `reference_name` against `nodes.qualified_name` or `nodes.name`. On match: creates the edge and deletes the unresolved ref. On no match: keeps the ref.

- [ ] **Step 4: Run tests, verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/codegraph/resolver.go internal/codegraph/resolver_test.go
git commit -m "feat(codegraph): add post-extraction reference resolver"
```

---

## Task 8: Service layer

**Files:**
- Create: `internal/service/codegraph.go`
- Create: `internal/service/codegraph_test.go`

- [ ] **Step 1: Write service tests**

```go
func TestCodeGraphService_Index(t *testing.T) {
    // Creates temp dir with Go files, calls svc.Index, verifies result counts
}

func TestCodeGraphService_Search(t *testing.T) {
    // Indexes a project, then searches for a known symbol name
}

func TestCodeGraphService_Callers(t *testing.T) {
    // Indexes project with known call graph, queries callers
}

func TestCodeGraphService_Status(t *testing.T) {
    // Indexes, then checks stats are non-zero
}
```

- [ ] **Step 2: Run tests, verify fail**

- [ ] **Step 3: Implement service/codegraph.go**

```go
// internal/service/codegraph.go
package service

import "github.com/wirvii/mneme/internal/codegraph"

// CodeGraphService orchestrates code graph operations.
type CodeGraphService struct {
	store  *codegraph.Store
	query  *codegraph.QueryEngine
	dbPath string
}

// NewCodeGraphService creates a new CodeGraphService for the given project.
func NewCodeGraphService(projectsDir, slug string) (*CodeGraphService, error) {
    path := codegraph.DBPath(projectsDir, slug)
    cdb, err := codegraph.OpenDB(path)
    // ...
}

func (s *CodeGraphService) Index(opts codegraph.IndexOptions) (*codegraph.IndexResult, error)
func (s *CodeGraphService) Search(query string, kinds []codegraph.NodeKind, languages []string, limit int) ([]codegraph.Node, error)
func (s *CodeGraphService) Callers(symbol string, depth, limit int) ([]codegraph.Node, error)
func (s *CodeGraphService) Callees(symbol string, depth, limit int) ([]codegraph.Node, error)
func (s *CodeGraphService) Impact(symbol string, depth, limit int) ([]codegraph.Node, error)
func (s *CodeGraphService) Context(symbol string, depth int) (*codegraph.ContextResult, error)
func (s *CodeGraphService) NodeDetail(symbol string) (*codegraph.Node, string, error) // node + source code
func (s *CodeGraphService) Trace(from, to string, maxDepth int) ([]codegraph.Node, []codegraph.Edge, error)
func (s *CodeGraphService) Explore(symbols []string, budget int) (string, error) // formatted output
func (s *CodeGraphService) Status() (*codegraph.GraphStats, error)
func (s *CodeGraphService) Files(pattern, language string) ([]codegraph.FileRecord, error)
func (s *CodeGraphService) Close() error
```

Symbol resolution: when methods receive a `symbol` string (not an ID), they resolve it via FTS5 search or qualified_name lookup. If ambiguous, return the best match.

- [ ] **Step 4: Run tests, verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/service/codegraph.go internal/service/codegraph_test.go
git commit -m "feat(service): add CodeGraphService — orchestration for codegraph operations"
```

---

## Task 9: MCP tools

**Files:**
- Modify: `internal/mcp/tools.go` — append 10 tool definitions to `allTools()`
- Modify: `internal/mcp/handlers.go` — add 10 cases to `handleToolCall` switch
- Create: `internal/mcp/handlers_codegraph_test.go`

- [ ] **Step 1: Write MCP handler tests**

```go
func TestMCP_CodegraphSearch(t *testing.T) {
    // Setup: index a temp project, then call codegraph_search via handleToolCall
    // Verify: response contains matching nodes
}

func TestMCP_CodegraphCallers(t *testing.T) {
    // Similar pattern
}

func TestMCP_CodegraphStatus(t *testing.T) {
    // Verify: returns valid stats JSON
}

func TestMCP_AllCodegraphToolsRegistered(t *testing.T) {
    tools := allTools()
    wantTools := []string{
        "codegraph_search", "codegraph_context", "codegraph_callers",
        "codegraph_callees", "codegraph_impact", "codegraph_node",
        "codegraph_explore", "codegraph_trace", "codegraph_status", "codegraph_files",
    }
    // verify all are present
}
```

- [ ] **Step 2: Run tests, verify fail**

- [ ] **Step 3: Add tool definitions to tools.go**

Append 10 tool definitions to the `allTools()` slice. Each follows the existing pattern:
```go
{
    Name:        "codegraph_search",
    Description: "Search code symbols by name using full-text search.",
    InputSchema: map[string]any{
        "type":     "object",
        "required": []string{"query"},
        "properties": map[string]any{
            "query": map[string]any{"type": "string", "description": "Search query for symbol names."},
            "kind":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter by node kind."},
            "language": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter by language."},
            "limit": map[string]any{"type": "integer", "description": "Max results (default 20, max 50)."},
        },
    },
},
```

- [ ] **Step 4: Add handlers to handlers.go**

Add cases in `handleToolCall`:
```go
case "codegraph_search":
    return h.handleCodegraphSearch(ctx, params.Arguments)
case "codegraph_context":
    return h.handleCodegraphContext(ctx, params.Arguments)
// ... 8 more
```

Each handler:
1. Deserializes arguments from `map[string]any`
2. Calls the corresponding `CodeGraphService` method
3. Formats the result as a text content block (capped at 30K chars)
4. Returns `*ToolCallResult`

The handlers struct needs a `cgSvc *service.CodeGraphService` field. This is initialized lazily (on first codegraph tool call) since not every session needs the code graph.

- [ ] **Step 5: Run tests, verify pass**

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/handlers.go internal/mcp/handlers_codegraph_test.go
git commit -m "feat(mcp): add 10 codegraph_* MCP tools"
```

---

## Task 10: CLI commands

**Files:**
- Create: `internal/cli/codegraph.go`
- Create: `internal/cli/codegraph_test.go`
- Modify: `internal/cli/root.go` — register `newCodegraphCmd()`

- [ ] **Step 1: Write CLI tests**

```go
func TestCodegraphCmd_Index(t *testing.T) {
    // Creates temp dir with Go files, runs index subcommand, verifies output
}

func TestCodegraphCmd_Status(t *testing.T) {
    // After indexing, runs status, verifies table output
}

func TestCodegraphCmd_Search(t *testing.T) {
    // After indexing, runs search, verifies results printed
}

func TestCodegraphCmd_Help(t *testing.T) {
    // Verifies help text lists all subcommands
}
```

- [ ] **Step 2: Run tests, verify fail**

- [ ] **Step 3: Implement cli/codegraph.go**

```go
func newCodegraphCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "codegraph",
        Short: "Semantic code graph — index and query code structure",
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
    )
    return cmd
}
```

Each subcommand:
1. Parses flags
2. Initializes `CodeGraphService` (opens codegraph DB for current project)
3. Calls the service method
4. Formats and prints output to `cmd.OutOrStdout()`

- [ ] **Step 4: Register in root.go**

Add `newCodegraphCmd()` to the `root.AddCommand(...)` list.

- [ ] **Step 5: Run tests, verify pass**

- [ ] **Step 6: Commit**

```bash
git add internal/cli/codegraph.go internal/cli/codegraph_test.go internal/cli/root.go
git commit -m "feat(cli): add mneme codegraph subcommands — index, status, search, callers, callees, impact, node, trace, files"
```

---

## Task 11: TypeScript extractor

**Files:**
- Create: `internal/codegraph/js/extract.js`
- Create: `internal/codegraph/extractor_ts.go`
- Create: `internal/codegraph/extractor_ts_test.go`

- [ ] **Step 1: Write TS extractor tests (skip if no Node.js)**

```go
func TestTSExtractor_ExtractsFunction(t *testing.T) {
    if !nodeJSAvailable() {
        t.Skip("Node.js not available")
    }
    ext := NewTSExtractor()
    defer ext.Close()

    source := `export function greet(name: string): string {
        return "Hello " + name;
    }`
    result, err := ext.Extract("greet.ts", []byte(source))
    if err != nil {
        t.Fatalf("Extract: %v", err)
    }
    funcs := filterNodes(result.Nodes, NodeFunction)
    if len(funcs) != 1 || funcs[0].Name != "greet" {
        t.Errorf("functions = %v, want [greet]", nodeNames(funcs))
    }
    if !funcs[0].IsExported {
        t.Error("greet should be exported")
    }
}

func TestTSExtractor_ExtractsClass(t *testing.T) { /* ... */ }
func TestTSExtractor_ExtractsImports(t *testing.T) { /* ... */ }
func TestTSExtractor_CallEdges(t *testing.T) { /* ... */ }
```

- [ ] **Step 2: Implement extract.js**

Node.js script that:
1. Reads file paths from stdin (one per line)
2. For each file, uses `typescript.createSourceFile` to parse
3. Walks the AST extracting nodes and edges
4. Outputs one JSON line per file to stdout (ExtractionResult format)
5. Exits when stdin closes

The script is self-contained — bundles only the `typescript` package. Size: ~200-300 LOC.

- [ ] **Step 3: Implement extractor_ts.go**

```go
type TSExtractor struct {
    cmd    *exec.Cmd
    stdin  io.WriteCloser
    stdout *bufio.Scanner
    tmpDir string
}

func NewTSExtractor() *TSExtractor { /* write extract.js to tmpDir, start process */ }
func (e *TSExtractor) Extract(filePath string, content []byte) (*ExtractionResult, error) { /* ... */ }
func (e *TSExtractor) Language() string { return "typescript" }
func (e *TSExtractor) Close() error { /* kill process, cleanup tmpDir */ }
```

Register in `extractor.go` init:
```go
RegisterExtractor("typescript", func() Extractor { return NewTSExtractor() })
RegisterExtractor("javascript", func() Extractor { return NewTSExtractor() })
```

- [ ] **Step 4: Run tests, verify pass (or skip)**

Run: `CGO_ENABLED=1 go test -tags fts5 ./internal/codegraph/ -run "TestTSExtractor_" -v`
Expected: PASS (or SKIP if no Node.js)

- [ ] **Step 5: Commit**

```bash
git add internal/codegraph/js/extract.js internal/codegraph/extractor_ts.go internal/codegraph/extractor_ts_test.go internal/codegraph/extractor.go
git commit -m "feat(codegraph): add TypeScript/JS extractor via Node.js subprocess"
```

---

## Task 12: Bridge with mneme

**Files:**
- Create: `internal/codegraph/bridge.go`
- Create: `internal/codegraph/bridge_test.go`

- [ ] **Step 1: Write bridge tests**

```go
func TestBridge_FindCodeForPath(t *testing.T) {
    // Setup: codegraph store with nodes for "internal/service/search.go"
    // Query: bridge.FindCodeContext("internal/service/search.go")
    // Verify: returns nodes contained by that file
}

func TestBridge_NoMatch(t *testing.T) {
    // Query non-existent path, verify empty result
}
```

- [ ] **Step 2: Run tests, verify fail**

- [ ] **Step 3: Implement bridge.go**

```go
// Bridge provides cross-query between mneme memory entities and code graph nodes.
type Bridge struct {
    store *Store
}

func NewBridge(store *Store) *Bridge {
    return &Bridge{store: store}
}

// FindCodeContext returns code nodes matching a file path or module name.
func (b *Bridge) FindCodeContext(nameOrPath string) ([]Node, error) {
    // 1. Try exact match on nodes.file_path
    // 2. Try LIKE match on nodes.qualified_name
    // 3. Return matched nodes with their contained children
}

// HasCodeContext checks if any code graph node matches the given name.
func (b *Bridge) HasCodeContext(nameOrPath string) bool {
    // Quick existence check
}
```

- [ ] **Step 4: Run tests, verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/codegraph/bridge.go internal/codegraph/bridge_test.go
git commit -m "feat(codegraph): add bridge — cross-query between mneme entities and code nodes"
```

---

## Task 13: Integration test and full build verification

**Files:** No new files — runs existing tests end-to-end.

- [ ] **Step 1: Run full test suite**

```bash
CGO_ENABLED=1 go test -tags fts5 ./...
```
Expected: All 22+ packages pass (including the new `internal/codegraph`).

- [ ] **Step 2: Run lint**

```bash
golangci-lint run
```
Expected: 0 issues.

- [ ] **Step 3: Build binary**

```bash
CGO_ENABLED=1 go build -tags fts5 -o mneme ./cmd/mneme
```
Expected: Compiles cleanly.

- [ ] **Step 4: Smoke test CLI**

```bash
./mneme codegraph --help
./mneme codegraph index .
./mneme codegraph status
./mneme codegraph search "Save"
./mneme codegraph callers "MemoryService.Save"
```
Expected: All commands produce sensible output.

- [ ] **Step 5: Commit any final fixes**

```bash
git commit -m "test: codegraph integration — full suite green, lint clean"
```

---

## Execution Notes

- **Build tag**: All test commands need `CGO_ENABLED=1` and `-tags fts5`
- **No Claude signatures**: Never add Co-Authored-By or Generated-with-Claude to commits
- **Dependency rule**: `codegraph` package imports only stdlib + `mattn/go-sqlite3`. It does NOT import `internal/model`, `internal/store`, or `internal/service`. The service layer imports codegraph.
- **Error wrapping**: Use `fmt.Errorf("codegraph: <context>: %w", err)` pattern
- **Tests**: Real SQLite in-memory (`:memory:`), no mocks. Table-driven where applicable.
- **Output cap**: MCP tool responses must be capped at configurable max chars (default 30K). Use a `truncateOutput(s string, max int) string` helper.
