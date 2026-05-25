// Package codegraph defines the foundational types for the semantic code graph.
// A code graph represents the structure of a codebase as a directed graph of
// typed nodes (code symbols) connected by typed edges (relationships between
// symbols). It is distinct from the memory knowledge graph in internal/model —
// this graph is derived from static analysis of source files rather than from
// agent-authored memories.
package codegraph

import (
	"crypto/sha256"
	"fmt"
)

// NodeKind classifies a node in the code graph by its syntactic role within
// the source language. Each constant corresponds to a distinct code construct
// that static analysis tools can identify and extract.
type NodeKind string

const (
	// NodeKindFile represents a source file as a whole.
	NodeKindFile NodeKind = "file"

	// NodeKindModule represents a package, module, or similar grouping unit.
	NodeKindModule NodeKind = "module"

	// NodeKindClass represents an object-oriented class definition.
	NodeKindClass NodeKind = "class"

	// NodeKindStruct represents a struct or record type.
	NodeKindStruct NodeKind = "struct"

	// NodeKindInterface represents an interface or abstract type declaration.
	NodeKindInterface NodeKind = "interface"

	// NodeKindTrait represents a trait (used in Rust, Scala, PHP).
	NodeKindTrait NodeKind = "trait"

	// NodeKindProtocol represents a protocol (used in Swift, Objective-C).
	NodeKindProtocol NodeKind = "protocol"

	// NodeKindFunction represents a standalone function declaration.
	NodeKindFunction NodeKind = "function"

	// NodeKindMethod represents a method defined on a type.
	NodeKindMethod NodeKind = "method"

	// NodeKindProperty represents a computed or declared property on a type.
	NodeKindProperty NodeKind = "property"

	// NodeKindField represents a data field on a struct or class.
	NodeKindField NodeKind = "field"

	// NodeKindVariable represents a variable declaration.
	NodeKindVariable NodeKind = "variable"

	// NodeKindConstant represents a constant declaration.
	NodeKindConstant NodeKind = "constant"

	// NodeKindEnum represents an enumeration type.
	NodeKindEnum NodeKind = "enum"

	// NodeKindEnumMember represents a single member of an enumeration.
	NodeKindEnumMember NodeKind = "enum_member"

	// NodeKindTypeAlias represents a type alias or typedef.
	NodeKindTypeAlias NodeKind = "type_alias"

	// NodeKindNamespace represents a namespace or package scope declaration.
	NodeKindNamespace NodeKind = "namespace"

	// NodeKindParameter represents a parameter of a function or method.
	NodeKindParameter NodeKind = "parameter"

	// NodeKindImport represents an import or require statement.
	NodeKindImport NodeKind = "import"

	// NodeKindExport represents an explicit export declaration.
	NodeKindExport NodeKind = "export"

	// NodeKindRoute represents an HTTP route or API endpoint handler registration.
	NodeKindRoute NodeKind = "route"

	// NodeKindComponent represents a UI component (e.g. React, Vue, Svelte).
	NodeKindComponent NodeKind = "component"
)

// validNodeKinds is the canonical set of recognised NodeKind values.
// It is used by Valid() to reject unknown kinds without branching on each constant.
var validNodeKinds = map[NodeKind]struct{}{
	NodeKindFile:       {},
	NodeKindModule:     {},
	NodeKindClass:      {},
	NodeKindStruct:     {},
	NodeKindInterface:  {},
	NodeKindTrait:      {},
	NodeKindProtocol:   {},
	NodeKindFunction:   {},
	NodeKindMethod:     {},
	NodeKindProperty:   {},
	NodeKindField:      {},
	NodeKindVariable:   {},
	NodeKindConstant:   {},
	NodeKindEnum:       {},
	NodeKindEnumMember: {},
	NodeKindTypeAlias:  {},
	NodeKindNamespace:  {},
	NodeKindParameter:  {},
	NodeKindImport:     {},
	NodeKindExport:     {},
	NodeKindRoute:      {},
	NodeKindComponent:  {},
}

// Valid reports whether the NodeKind is one of the recognised constants.
// Extractors should call this before inserting nodes into the store.
func (k NodeKind) Valid() bool {
	_, ok := validNodeKinds[k]
	return ok
}

// EdgeKind classifies the semantic relationship between two nodes in the code graph.
// Edge directions are always Source → Target. The exact semantics depend on the
// node kinds at each end (e.g. contains: file → function, calls: function → function).
type EdgeKind string

const (
	// EdgeKindContains indicates the source node lexically contains the target node.
	// Example: a file contains a function; a class contains a method.
	EdgeKindContains EdgeKind = "contains"

	// EdgeKindCalls indicates the source function or method invokes the target.
	EdgeKindCalls EdgeKind = "calls"

	// EdgeKindImports indicates the source file or module imports the target module.
	EdgeKindImports EdgeKind = "imports"

	// EdgeKindExports indicates the source module exports the target symbol.
	EdgeKindExports EdgeKind = "exports"

	// EdgeKindExtends indicates the source type extends or inherits from the target.
	EdgeKindExtends EdgeKind = "extends"

	// EdgeKindImplements indicates the source type implements the target interface or protocol.
	EdgeKindImplements EdgeKind = "implements"

	// EdgeKindReferences indicates the source node references the target by name
	// without a direct call or type relationship (e.g. a variable used as an argument).
	EdgeKindReferences EdgeKind = "references"

	// EdgeKindTypeOf indicates the source node has the target node as its type
	// (e.g. a field whose type is a struct defined elsewhere).
	EdgeKindTypeOf EdgeKind = "type_of"

	// EdgeKindReturns indicates the source function or method returns the target type.
	EdgeKindReturns EdgeKind = "returns"

	// EdgeKindInstantiates indicates the source node creates an instance of the target type.
	EdgeKindInstantiates EdgeKind = "instantiates"

	// EdgeKindOverrides indicates the source method overrides the target method in a supertype.
	EdgeKindOverrides EdgeKind = "overrides"

	// EdgeKindDecorates indicates the source decorator or annotation is applied to the target.
	EdgeKindDecorates EdgeKind = "decorates"
)

// validEdgeKinds is the canonical set of recognised EdgeKind values.
var validEdgeKinds = map[EdgeKind]struct{}{
	EdgeKindContains:     {},
	EdgeKindCalls:        {},
	EdgeKindImports:      {},
	EdgeKindExports:      {},
	EdgeKindExtends:      {},
	EdgeKindImplements:   {},
	EdgeKindReferences:   {},
	EdgeKindTypeOf:       {},
	EdgeKindReturns:      {},
	EdgeKindInstantiates: {},
	EdgeKindOverrides:    {},
	EdgeKindDecorates:    {},
}

// Valid reports whether the EdgeKind is one of the recognised constants.
func (k EdgeKind) Valid() bool {
	_, ok := validEdgeKinds[k]
	return ok
}

// NodeID returns a deterministic, stable identifier for a code symbol. The ID
// is the first 16 hex characters (64 bits) of the SHA-256 digest of the
// concatenation "<filePath>:<qualifiedName>". Collisions are astronomically
// unlikely at the scale of any single codebase.
//
// The ID does not encode language or version — it is stable across re-indexing
// as long as the file path and qualified name remain unchanged.
func NodeID(filePath, qualifiedName string) string {
	h := sha256.Sum256([]byte(filePath + ":" + qualifiedName))
	return fmt.Sprintf("%x", h[:8])
}

// Node is a vertex in the code graph representing a single named code symbol.
// Every node has a stable ID derived from its file path and qualified name so
// that re-indexing the same symbol produces the same node without duplicates.
type Node struct {
	// ID is a 16-hex-character stable identifier derived from FilePath and QualifiedName.
	// Computed by NodeID(FilePath, QualifiedName).
	ID string `json:"id"`

	// Kind classifies the syntactic role of this node in the source language.
	Kind NodeKind `json:"kind"`

	// Name is the short, unqualified symbol name (e.g. "Search").
	Name string `json:"name"`

	// QualifiedName is the fully qualified symbol path within its file or module
	// (e.g. "(*MemoryService).Search", "com.example.MyClass.myMethod").
	QualifiedName string `json:"qualified_name"`

	// FilePath is the relative path to the source file that defines this node.
	FilePath string `json:"file_path"`

	// Language is the programming language of the source file (e.g. "go", "typescript").
	Language string `json:"language"`

	// StartLine is the 1-based line number where the symbol definition begins.
	StartLine int `json:"start_line"`

	// EndLine is the 1-based line number where the symbol definition ends.
	EndLine int `json:"end_line"`

	// StartColumn is the 0-based column offset where the symbol starts on StartLine.
	StartColumn int `json:"start_column"`

	// EndColumn is the 0-based column offset where the symbol ends on EndLine.
	EndColumn int `json:"end_column"`

	// Docstring is the documentation comment attached to the symbol, if any.
	Docstring string `json:"docstring,omitempty"`

	// Signature is the full type signature or declaration text of the symbol.
	// For functions this includes parameters and return types; for types it
	// includes the full type expression.
	Signature string `json:"signature,omitempty"`

	// Visibility is the access modifier as a language-specific string
	// (e.g. "public", "private", "exported", "internal").
	Visibility string `json:"visibility,omitempty"`

	// IsExported indicates whether the symbol is exported / public. Language-agnostic
	// boolean counterpart to Visibility for cross-language consumers.
	IsExported bool `json:"is_exported,omitempty"`

	// IsAsync indicates whether the function or method is declared async.
	IsAsync bool `json:"is_async,omitempty"`

	// IsStatic indicates whether the method or field is static / class-level.
	IsStatic bool `json:"is_static,omitempty"`

	// IsAbstract indicates whether the type or method is abstract.
	IsAbstract bool `json:"is_abstract,omitempty"`

	// Decorators holds the names of decorator or annotation symbols applied to this node.
	Decorators []string `json:"decorators,omitempty"`

	// TypeParameters holds the names of generic / template type parameters for
	// generic types and functions (e.g. ["T", "U"]).
	TypeParameters []string `json:"type_parameters,omitempty"`

	// UpdatedAt is a Unix timestamp (seconds) recording the last time this node
	// was written to the store. Used to detect stale entries after re-indexing.
	UpdatedAt int64 `json:"updated_at"`
}

// Edge is a directed relationship between two nodes in the code graph.
// Edges are identified by their (Source, Target, Kind) triple; the store
// enforces uniqueness on this triple to prevent duplicate edges.
type Edge struct {
	// ID is the database row identifier assigned by the store on insertion.
	ID int64 `json:"id"`

	// Source is the NodeID of the originating node.
	Source string `json:"source"`

	// Target is the NodeID of the destination node.
	Target string `json:"target"`

	// Kind is the semantic relationship type from source to target.
	Kind EdgeKind `json:"kind"`

	// Metadata is an optional JSON blob for language-specific edge attributes
	// (e.g. call argument types, import aliases).
	Metadata string `json:"metadata,omitempty"`

	// Line is the 1-based source line where the relationship was detected.
	Line int `json:"line,omitempty"`

	// Col is the 0-based column where the relationship was detected.
	Col int `json:"col,omitempty"`

	// Provenance identifies the extractor that produced this edge (e.g. "go-ast", "ts-morph").
	Provenance string `json:"provenance,omitempty"`
}

// FileRecord tracks the indexing state of a single source file. It enables
// incremental re-indexing: files whose ContentHash has not changed since the
// last IndexedAt time can be skipped without re-parsing.
type FileRecord struct {
	// Path is the relative path to the source file within the repository root.
	Path string `json:"path"`

	// ContentHash is a hex-encoded SHA-256 digest of the file's content at
	// the time it was last indexed. Used to detect unchanged files.
	ContentHash string `json:"content_hash"`

	// Language is the detected programming language of the file.
	Language string `json:"language"`

	// Size is the file size in bytes at last index time.
	Size int64 `json:"size"`

	// ModifiedAt is the Unix timestamp of the file's mtime at last index time.
	ModifiedAt int64 `json:"modified_at"`

	// IndexedAt is the Unix timestamp when this file was last successfully indexed.
	IndexedAt int64 `json:"indexed_at"`

	// NodeCount is the number of nodes extracted from this file during the last
	// successful index pass.
	NodeCount int `json:"node_count"`

	// Errors is the list of extraction error messages encountered during the last
	// index pass. Non-empty means the file was partially indexed.
	Errors string `json:"errors,omitempty"`
}

// UnresolvedRef records a reference to a symbol that could not be resolved to
// an existing node at extraction time. Unresolved references arise when a call
// site or type annotation targets a symbol defined in a file not yet indexed,
// or in an external dependency. They are candidates for a second-pass resolution
// phase once all files in the repository have been indexed.
type UnresolvedRef struct {
	// ID is the database row identifier assigned by the store on insertion.
	ID int64 `json:"id"`

	// FromNodeID is the NodeID of the node that contains the unresolved reference.
	FromNodeID string `json:"from_node_id"`

	// ReferenceName is the raw symbol name as it appears in the source
	// (e.g. "http.ListenAndServe", "MyClass").
	ReferenceName string `json:"reference_name"`

	// ReferenceKind is the EdgeKind that would be created once the reference is resolved.
	ReferenceKind EdgeKind `json:"reference_kind"`

	// Line is the 1-based source line where the reference appears.
	Line int `json:"line,omitempty"`

	// Col is the 0-based column where the reference appears.
	Col int `json:"col,omitempty"`

	// FilePath is the source file that contains the unresolved reference.
	FilePath string `json:"file_path"`

	// Language is the programming language of the source file.
	Language string `json:"language"`

	// Candidates is a JSON array of NodeIDs that partially matched the reference
	// name during a fuzzy resolution attempt. Used for disambiguation UI.
	Candidates string `json:"candidates,omitempty"`
}

// ExtractionResult is the output of a single file extraction pass. It bundles
// all nodes, edges, and unresolved references found in one source file together
// with any errors that occurred and timing information.
type ExtractionResult struct {
	// Nodes is the list of code symbol nodes extracted from the file.
	Nodes []Node `json:"nodes"`

	// Edges is the list of directed relationships between nodes.
	Edges []Edge `json:"edges"`

	// UnresolvedRefs is the list of references that could not be resolved
	// to existing nodes during extraction.
	UnresolvedRefs []UnresolvedRef `json:"unresolved_refs,omitempty"`

	// Errors is the list of non-fatal extraction errors. A non-empty list means
	// the result is partial — some constructs could not be parsed.
	Errors []ExtractionError `json:"errors,omitempty"`

	// DurationMs is the wall-clock duration of the extraction in milliseconds.
	DurationMs int64 `json:"duration_ms"`
}

// ExtractionError describes a single non-fatal error encountered while parsing
// a source file. Extraction continues after recording these errors so that
// partial results are preserved.
type ExtractionError struct {
	// Message is a human-readable description of the error.
	Message string `json:"message"`

	// FilePath is the source file where the error occurred.
	FilePath string `json:"file_path"`

	// Line is the 1-based line number where the error was detected. Zero means
	// the error is not attributable to a specific line.
	Line int `json:"line,omitempty"`

	// Col is the 0-based column where the error was detected.
	Col int `json:"col,omitempty"`

	// Severity is the error level: "error", "warning", or "info".
	Severity string `json:"severity"`

	// Code is an extractor-specific error code for programmatic classification.
	Code string `json:"code,omitempty"`
}

// Extractor is the interface that each language-specific parser must implement.
// Extractors are stateless and safe for concurrent use. They receive raw file
// content so that the caller controls file I/O and caching.
type Extractor interface {
	// Extract parses the source content at filePath and returns all nodes, edges,
	// and unresolved references found. filePath is provided for error attribution
	// and NodeID computation; content is the actual bytes to parse.
	// Extract must not return nil even when errors occur — it should return a
	// partial ExtractionResult alongside the error.
	Extract(filePath string, content []byte) (*ExtractionResult, error)

	// Language returns the programming language identifier this extractor handles
	// (e.g. "go", "typescript", "python"). Used by the indexer to route files.
	Language() string
}

// GraphStats summarises the current state of the code graph database. It is
// returned by the store's Stats method and exposed through the HTTP and CLI
// frontends for monitoring and debugging.
type GraphStats struct {
	// NodeCount is the total number of nodes across all files.
	NodeCount int `json:"node_count"`

	// EdgeCount is the total number of directed edges in the graph.
	EdgeCount int `json:"edge_count"`

	// FileCount is the number of source files recorded in the file index.
	FileCount int `json:"file_count"`

	// NodesByKind is the distribution of node counts by NodeKind string.
	NodesByKind map[string]int `json:"nodes_by_kind"`

	// EdgesByKind is the distribution of edge counts by EdgeKind string.
	EdgesByKind map[string]int `json:"edges_by_kind"`

	// FilesByLanguage is the distribution of file counts by language identifier.
	FilesByLanguage map[string]int `json:"files_by_language"`

	// DBSizeBytes is the size of the underlying database file in bytes.
	DBSizeBytes int64 `json:"db_size_bytes"`

	// LastUpdated is the Unix timestamp of the most recent write to the graph store.
	LastUpdated int64 `json:"last_updated"`
}

// IndexResult reports the outcome of a full or incremental index run over a
// directory tree. Counters are cumulative across all files processed in the run.
type IndexResult struct {
	// FilesScanned is the total number of files examined by the indexer.
	FilesScanned int `json:"files_scanned"`

	// FilesIndexed is the number of files that were parsed and written to the store.
	FilesIndexed int `json:"files_indexed"`

	// FilesSkipped is the number of files skipped because their content hash
	// matched the stored hash (no changes since last index).
	FilesSkipped int `json:"files_skipped"`

	// FilesErrored is the number of files where extraction produced a fatal error
	// and no nodes or edges were written.
	FilesErrored int `json:"files_errored"`

	// NodesCreated is the total number of new nodes inserted into the store.
	NodesCreated int `json:"nodes_created"`

	// EdgesCreated is the total number of new edges inserted into the store.
	EdgesCreated int `json:"edges_created"`

	// DurationMs is the total wall-clock duration of the index run in milliseconds.
	DurationMs int64 `json:"duration_ms"`
}
