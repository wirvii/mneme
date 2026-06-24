package codegraph

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Store provides CRUD, graph traversal, full-text search, and batch operations
// against the codegraph SQLite database. All methods use raw SQL — no ORM.
// The Store is the only layer that touches *CodeGraphDB directly; callers above
// (service, CLI, MCP) must go through Store.
type Store struct {
	db *CodeGraphDB
}

// NewStore constructs a Store backed by the given CodeGraphDB.
// Ownership of cdb is retained by the caller; Close must be called separately.
func NewStore(cdb *CodeGraphDB) *Store {
	return &Store{db: cdb}
}

// ---------------------------------------------------------------------------
// Node operations
// ---------------------------------------------------------------------------

// UpsertNode inserts or replaces a node in the nodes table. Because the Node ID
// is deterministic (derived from file path + qualified name), a second indexing
// pass for the same symbol produces the same ID and silently replaces the row.
//
// The FTS5 triggers (nodes_ai / nodes_au) keep nodes_fts in sync automatically.
func (s *Store) UpsertNode(n Node) error {
	decorators, err := marshalStringSlice(n.Decorators)
	if err != nil {
		return fmt.Errorf("codegraph: store: upsert node: marshal decorators: %w", err)
	}
	typeParams, err := marshalStringSlice(n.TypeParameters)
	if err != nil {
		return fmt.Errorf("codegraph: store: upsert node: marshal type_parameters: %w", err)
	}

	const q = `
		INSERT OR REPLACE INTO nodes (
			id, kind, name, qualified_name, file_path, language,
			start_line, end_line, start_column, end_column,
			docstring, signature, visibility,
			is_exported, is_async, is_static, is_abstract,
			decorators, type_parameters, updated_at
		) VALUES (
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?
		)`

	_, err = s.db.DB.Exec(q,
		n.ID, string(n.Kind), n.Name, n.QualifiedName, n.FilePath, n.Language,
		n.StartLine, n.EndLine, n.StartColumn, n.EndColumn,
		nullableString(n.Docstring), nullableString(n.Signature), nullableString(n.Visibility),
		boolToInt(n.IsExported), boolToInt(n.IsAsync), boolToInt(n.IsStatic), boolToInt(n.IsAbstract),
		decorators, typeParams, n.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("codegraph: store: upsert node: %w", err)
	}
	return nil
}

// GetNode retrieves a node by its deterministic ID. Returns nil, nil when no
// node with that ID exists in the database.
func (s *Store) GetNode(id string) (*Node, error) {
	const q = `
		SELECT id, kind, name, qualified_name, file_path, language,
		       start_line, end_line, start_column, end_column,
		       docstring, signature, visibility,
		       is_exported, is_async, is_static, is_abstract,
		       decorators, type_parameters, updated_at
		FROM nodes
		WHERE id = ?`

	row := s.db.DB.QueryRow(q, id)
	n, err := scanNode(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("codegraph: store: get node: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// Edge operations
// ---------------------------------------------------------------------------

// UpsertEdge inserts an edge into the edges table. The edges table does not
// enforce a unique constraint on (source, target, kind) at the DB level to allow
// fast bulk inserts; callers are responsible for de-duplicating before calling
// UpsertEdge when uniqueness matters.
func (s *Store) UpsertEdge(e Edge) error {
	const q = `
		INSERT INTO edges (source, target, kind, metadata, line, col, provenance)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.DB.Exec(q,
		e.Source, e.Target, string(e.Kind),
		nullableString(e.Metadata),
		e.Line, e.Col,
		nullableString(e.Provenance),
	)
	if err != nil {
		return fmt.Errorf("codegraph: store: upsert edge: %w", err)
	}
	return nil
}

// GetEdgesFrom returns all edges originating from nodeID. When kind is non-empty
// only edges of that kind are returned. Results are ordered by id (insertion
// order) for determinism.
func (s *Store) GetEdgesFrom(nodeID string, kind string) ([]Edge, error) {
	q := `
		SELECT id, source, target, kind, metadata, line, col, provenance
		FROM edges
		WHERE source = ?`
	args := []any{nodeID}
	if kind != "" {
		q += " AND kind = ?"
		args = append(args, kind)
	}
	q += " ORDER BY id"

	rows, err := s.db.DB.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("codegraph: store: get edges from: %w", err)
	}
	defer rows.Close()

	edges, err := scanEdges(rows)
	if err != nil {
		return nil, fmt.Errorf("codegraph: store: get edges from: %w", err)
	}
	return edges, nil
}

// GetEdgesTo returns all edges whose target is nodeID. When kind is non-empty
// only edges of that kind are returned. Results are ordered by id.
func (s *Store) GetEdgesTo(nodeID string, kind string) ([]Edge, error) {
	q := `
		SELECT id, source, target, kind, metadata, line, col, provenance
		FROM edges
		WHERE target = ?`
	args := []any{nodeID}
	if kind != "" {
		q += " AND kind = ?"
		args = append(args, kind)
	}
	q += " ORDER BY id"

	rows, err := s.db.DB.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("codegraph: store: get edges to: %w", err)
	}
	defer rows.Close()

	edges, err := scanEdges(rows)
	if err != nil {
		return nil, fmt.Errorf("codegraph: store: get edges to: %w", err)
	}
	return edges, nil
}

// ---------------------------------------------------------------------------
// FTS5 search
// ---------------------------------------------------------------------------

// SearchNodes performs a full-text search over the nodes_fts virtual table and
// returns the matching Node records. The query is passed directly to the MATCH
// operator after wrapping it in a wildcard suffix (*) for prefix matching.
//
// Optional filters:
//   - kinds: when non-empty, only nodes whose kind is in the list are returned.
//   - languages: when non-empty, only nodes whose language is in the list are returned.
//   - limit: maximum number of results; 0 defaults to 20.
func (s *Store) SearchNodes(query string, kinds []NodeKind, languages []string, limit int) ([]Node, error) {
	if limit <= 0 {
		limit = 20
	}

	// Wrap query for prefix matching; FTS5 MATCH treats bare terms as full-word.
	ftsQuery := strings.TrimSpace(query) + "*"

	where := []string{"nodes_fts MATCH ?"}
	args := []any{ftsQuery}

	if len(kinds) > 0 {
		placeholders := make([]string, len(kinds))
		for i, k := range kinds {
			placeholders[i] = "?"
			args = append(args, string(k))
		}
		where = append(where, fmt.Sprintf("n.kind IN (%s)", strings.Join(placeholders, ",")))
	}
	if len(languages) > 0 {
		placeholders := make([]string, len(languages))
		for i, l := range languages {
			placeholders[i] = "?"
			args = append(args, l)
		}
		where = append(where, fmt.Sprintf("n.language IN (%s)", strings.Join(placeholders, ",")))
	}

	args = append(args, limit)

	q := fmt.Sprintf(`
		SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path, n.language,
		       n.start_line, n.end_line, n.start_column, n.end_column,
		       n.docstring, n.signature, n.visibility,
		       n.is_exported, n.is_async, n.is_static, n.is_abstract,
		       n.decorators, n.type_parameters, n.updated_at
		FROM nodes n
		JOIN nodes_fts ON n.rowid = nodes_fts.rowid
		WHERE %s
		ORDER BY bm25(nodes_fts) ASC
		LIMIT ?`,
		strings.Join(where, " AND "),
	)

	rows, err := s.db.DB.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("codegraph: store: search nodes: %w", err)
	}
	defer rows.Close()

	var results []Node
	for rows.Next() {
		n, err := scanNodeRow(rows)
		if err != nil {
			return nil, fmt.Errorf("codegraph: store: search nodes: scan: %w", err)
		}
		results = append(results, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("codegraph: store: search nodes: rows: %w", err)
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// File operations
// ---------------------------------------------------------------------------

// ListDistinctNodeFilePaths returns the set of all distinct file_path values
// that appear in the nodes table. This is used by pruneDeleted to detect orphan
// nodes — nodes whose file_path has no corresponding entry in the files table
// (e.g. because the file record was lost due to an earlier write failure, or
// because the file was indexed in a prior run before the directory was added to
// ignoredDirs). Returning only the distinct paths keeps the result small even
// for large codegraphs.
func (s *Store) ListDistinctNodeFilePaths() ([]string, error) {
	const q = `SELECT DISTINCT file_path FROM nodes ORDER BY file_path`
	rows, err := s.db.DB.Query(q)
	if err != nil {
		return nil, fmt.Errorf("codegraph: store: list distinct node file paths: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("codegraph: store: list distinct node file paths: scan: %w", err)
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("codegraph: store: list distinct node file paths: rows: %w", err)
	}
	return paths, nil
}

// DeleteNodesByFile deletes all nodes whose file_path equals filePath. Because
// the edges table has ON DELETE CASCADE foreign keys on both source and target,
// all edges referencing those nodes are removed automatically by SQLite.
// Returns the number of nodes deleted.
func (s *Store) DeleteNodesByFile(filePath string) (int64, error) {
	res, err := s.db.DB.Exec(`DELETE FROM nodes WHERE file_path = ?`, filePath)
	if err != nil {
		return 0, fmt.Errorf("codegraph: store: delete nodes by file: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("codegraph: store: delete nodes by file: rows affected: %w", err)
	}
	return n, nil
}

// UpsertFile inserts or replaces a FileRecord in the files table. The path is
// the primary key; upserting the same path updates all other fields.
func (s *Store) UpsertFile(f FileRecord) error {
	const q = `
		INSERT OR REPLACE INTO files (
			path, content_hash, language, size, modified_at, indexed_at, node_count, errors
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.DB.Exec(q,
		f.Path, f.ContentHash, f.Language, f.Size,
		f.ModifiedAt, f.IndexedAt, f.NodeCount,
		nullableString(f.Errors),
	)
	if err != nil {
		return fmt.Errorf("codegraph: store: upsert file: %w", err)
	}
	return nil
}

// GetFile retrieves a FileRecord by its path. Returns nil, nil when no record
// with that path exists.
func (s *Store) GetFile(path string) (*FileRecord, error) {
	const q = `
		SELECT path, content_hash, language, size, modified_at, indexed_at, node_count, errors
		FROM files
		WHERE path = ?`

	row := s.db.DB.QueryRow(q, path)
	f, err := scanFileRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("codegraph: store: get file: %w", err)
	}
	return f, nil
}

// ListFiles returns all FileRecords stored in the files table, ordered by path.
func (s *Store) ListFiles() ([]FileRecord, error) {
	const q = `
		SELECT path, content_hash, language, size, modified_at, indexed_at, node_count, errors
		FROM files
		ORDER BY path`

	rows, err := s.db.DB.Query(q)
	if err != nil {
		return nil, fmt.Errorf("codegraph: store: list files: %w", err)
	}
	defer rows.Close()

	var files []FileRecord
	for rows.Next() {
		f, err := scanFileRecordRow(rows)
		if err != nil {
			return nil, fmt.Errorf("codegraph: store: list files: scan: %w", err)
		}
		files = append(files, *f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("codegraph: store: list files: rows: %w", err)
	}
	return files, nil
}

// DeleteFile removes the file record identified by path. No error is returned
// when the path does not exist (idempotent).
func (s *Store) DeleteFile(path string) error {
	_, err := s.db.DB.Exec(`DELETE FROM files WHERE path = ?`, path)
	if err != nil {
		return fmt.Errorf("codegraph: store: delete file: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

// GetStats returns aggregate counts and per-kind/language breakdowns for the
// current state of the codegraph database. DBSizeBytes is reported as 0 for
// in-memory databases (no filesystem path).
func (s *Store) GetStats() (*GraphStats, error) {
	stats := &GraphStats{
		NodesByKind:     make(map[string]int),
		EdgesByKind:     make(map[string]int),
		FilesByLanguage: make(map[string]int),
	}

	// Total counts.
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&stats.NodeCount); err != nil {
		return nil, fmt.Errorf("codegraph: store: get stats: node count: %w", err)
	}
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&stats.EdgeCount); err != nil {
		return nil, fmt.Errorf("codegraph: store: get stats: edge count: %w", err)
	}
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&stats.FileCount); err != nil {
		return nil, fmt.Errorf("codegraph: store: get stats: file count: %w", err)
	}

	// Nodes by kind.
	{
		rows, err := s.db.DB.Query(`SELECT kind, COUNT(*) FROM nodes GROUP BY kind`)
		if err != nil {
			return nil, fmt.Errorf("codegraph: store: get stats: nodes by kind: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var kind string
			var cnt int
			if err := rows.Scan(&kind, &cnt); err != nil {
				return nil, fmt.Errorf("codegraph: store: get stats: nodes by kind: scan: %w", err)
			}
			stats.NodesByKind[kind] = cnt
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("codegraph: store: get stats: nodes by kind: rows: %w", err)
		}
	}

	// Edges by kind.
	{
		rows, err := s.db.DB.Query(`SELECT kind, COUNT(*) FROM edges GROUP BY kind`)
		if err != nil {
			return nil, fmt.Errorf("codegraph: store: get stats: edges by kind: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var kind string
			var cnt int
			if err := rows.Scan(&kind, &cnt); err != nil {
				return nil, fmt.Errorf("codegraph: store: get stats: edges by kind: scan: %w", err)
			}
			stats.EdgesByKind[kind] = cnt
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("codegraph: store: get stats: edges by kind: rows: %w", err)
		}
	}

	// Files by language.
	{
		rows, err := s.db.DB.Query(`SELECT language, COUNT(*) FROM files GROUP BY language`)
		if err != nil {
			return nil, fmt.Errorf("codegraph: store: get stats: files by language: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var lang string
			var cnt int
			if err := rows.Scan(&lang, &cnt); err != nil {
				return nil, fmt.Errorf("codegraph: store: get stats: files by language: scan: %w", err)
			}
			stats.FilesByLanguage[lang] = cnt
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("codegraph: store: get stats: files by language: rows: %w", err)
		}
	}

	// Last updated — most recent updated_at across all nodes.
	{
		var lastUpdated sql.NullInt64
		if err := s.db.DB.QueryRow(`SELECT MAX(updated_at) FROM nodes`).Scan(&lastUpdated); err != nil {
			return nil, fmt.Errorf("codegraph: store: get stats: last updated: %w", err)
		}
		if lastUpdated.Valid {
			stats.LastUpdated = lastUpdated.Int64
		}
	}

	// DB size — use page_count * page_size pragma; 0 for in-memory.
	if s.db.Path != ":memory:" {
		var pageCount, pageSize int64
		_ = s.db.DB.QueryRow(`PRAGMA page_count`).Scan(&pageCount)
		_ = s.db.DB.QueryRow(`PRAGMA page_size`).Scan(&pageSize)
		stats.DBSizeBytes = pageCount * pageSize
	}

	return stats, nil
}

// ---------------------------------------------------------------------------
// Batch operations
// ---------------------------------------------------------------------------

// BatchUpsertNodes inserts or replaces all nodes in a single transaction.
// This is significantly faster than individual UpsertNode calls when indexing
// a large file because SQLite commits only once.
func (s *Store) BatchUpsertNodes(nodes []Node) error {
	if len(nodes) == 0 {
		return nil
	}

	tx, err := s.db.DB.Begin()
	if err != nil {
		return fmt.Errorf("codegraph: store: batch upsert nodes: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO nodes (
			id, kind, name, qualified_name, file_path, language,
			start_line, end_line, start_column, end_column,
			docstring, signature, visibility,
			is_exported, is_async, is_static, is_abstract,
			decorators, type_parameters, updated_at
		) VALUES (
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?
		)`)
	if err != nil {
		return fmt.Errorf("codegraph: store: batch upsert nodes: prepare: %w", err)
	}
	for _, n := range nodes {
		decorators, merr := marshalStringSlice(n.Decorators)
		if merr != nil {
			_ = stmt.Close()
			err = fmt.Errorf("codegraph: store: batch upsert nodes: marshal decorators: %w", merr)
			return err
		}
		typeParams, merr := marshalStringSlice(n.TypeParameters)
		if merr != nil {
			_ = stmt.Close()
			err = fmt.Errorf("codegraph: store: batch upsert nodes: marshal type_parameters: %w", merr)
			return err
		}

		_, serr := stmt.Exec(
			n.ID, string(n.Kind), n.Name, n.QualifiedName, n.FilePath, n.Language,
			n.StartLine, n.EndLine, n.StartColumn, n.EndColumn,
			nullableString(n.Docstring), nullableString(n.Signature), nullableString(n.Visibility),
			boolToInt(n.IsExported), boolToInt(n.IsAsync), boolToInt(n.IsStatic), boolToInt(n.IsAbstract),
			decorators, typeParams, n.UpdatedAt,
		)
		if serr != nil {
			_ = stmt.Close()
			err = fmt.Errorf("codegraph: store: batch upsert nodes: exec: %w", serr)
			return err
		}
	}

	if cerr := stmt.Close(); cerr != nil {
		err = fmt.Errorf("codegraph: store: batch upsert nodes: close stmt: %w", cerr)
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("codegraph: store: batch upsert nodes: commit: %w", err)
	}
	return nil
}

// BatchUpsertEdges inserts all edges in a single transaction.
// This is significantly faster than individual UpsertEdge calls when indexing
// a large file because SQLite commits only once.
func (s *Store) BatchUpsertEdges(edges []Edge) error {
	if len(edges) == 0 {
		return nil
	}

	tx, err := s.db.DB.Begin()
	if err != nil {
		return fmt.Errorf("codegraph: store: batch upsert edges: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.Prepare(`
		INSERT INTO edges (source, target, kind, metadata, line, col, provenance)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("codegraph: store: batch upsert edges: prepare: %w", err)
	}
	for _, e := range edges {
		_, serr := stmt.Exec(
			e.Source, e.Target, string(e.Kind),
			nullableString(e.Metadata),
			e.Line, e.Col,
			nullableString(e.Provenance),
		)
		if serr != nil {
			_ = stmt.Close()
			err = fmt.Errorf("codegraph: store: batch upsert edges: exec: %w", serr)
			return err
		}
	}

	if cerr := stmt.Close(); cerr != nil {
		err = fmt.Errorf("codegraph: store: batch upsert edges: close stmt: %w", cerr)
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("codegraph: store: batch upsert edges: commit: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal scan helpers
// ---------------------------------------------------------------------------

// scannable is a common interface for *sql.Row and *sql.Rows so the same scan
// function can serve both QueryRow and rows.Next() contexts.
type scannable interface {
	Scan(dest ...any) error
}

// scanNode scans one node from a *sql.Row (QueryRow result). It returns
// sql.ErrNoRows when the row does not exist.
func scanNode(row *sql.Row) (*Node, error) {
	return scanNodeRow(row)
}

// scanNodeRow scans a single Node from any scannable (row or rows.Next).
func scanNodeRow(s scannable) (*Node, error) {
	var n Node
	var docstring, signature, visibility sql.NullString
	var decoratorsJSON, typeParamsJSON sql.NullString
	var isExported, isAsync, isStatic, isAbstract int

	err := s.Scan(
		&n.ID, &n.Kind, &n.Name, &n.QualifiedName, &n.FilePath, &n.Language,
		&n.StartLine, &n.EndLine, &n.StartColumn, &n.EndColumn,
		&docstring, &signature, &visibility,
		&isExported, &isAsync, &isStatic, &isAbstract,
		&decoratorsJSON, &typeParamsJSON, &n.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	n.Docstring = docstring.String
	n.Signature = signature.String
	n.Visibility = visibility.String
	n.IsExported = isExported != 0
	n.IsAsync = isAsync != 0
	n.IsStatic = isStatic != 0
	n.IsAbstract = isAbstract != 0

	if decoratorsJSON.Valid && decoratorsJSON.String != "" && decoratorsJSON.String != "null" {
		if err := json.Unmarshal([]byte(decoratorsJSON.String), &n.Decorators); err != nil {
			return nil, fmt.Errorf("unmarshal decorators: %w", err)
		}
	}
	if typeParamsJSON.Valid && typeParamsJSON.String != "" && typeParamsJSON.String != "null" {
		if err := json.Unmarshal([]byte(typeParamsJSON.String), &n.TypeParameters); err != nil {
			return nil, fmt.Errorf("unmarshal type_parameters: %w", err)
		}
	}

	return &n, nil
}

// scanEdges drains a *sql.Rows cursor into a slice of Edge values.
// The caller must close rows after this call.
func scanEdges(rows *sql.Rows) ([]Edge, error) {
	var edges []Edge
	for rows.Next() {
		var e Edge
		var metadata, provenance sql.NullString
		var line, col sql.NullInt64

		if err := rows.Scan(
			&e.ID, &e.Source, &e.Target, &e.Kind,
			&metadata, &line, &col, &provenance,
		); err != nil {
			return nil, err
		}
		e.Metadata = metadata.String
		e.Provenance = provenance.String
		if line.Valid {
			e.Line = int(line.Int64)
		}
		if col.Valid {
			e.Col = int(col.Int64)
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// scanFileRecord scans one FileRecord from a *sql.Row (QueryRow result).
func scanFileRecord(row *sql.Row) (*FileRecord, error) {
	return scanFileRecordRow(row)
}

// scanFileRecordRow scans a single FileRecord from any scannable.
func scanFileRecordRow(s scannable) (*FileRecord, error) {
	var f FileRecord
	var errors sql.NullString

	err := s.Scan(
		&f.Path, &f.ContentHash, &f.Language, &f.Size,
		&f.ModifiedAt, &f.IndexedAt, &f.NodeCount, &errors,
	)
	if err != nil {
		return nil, err
	}
	f.Errors = errors.String
	return &f, nil
}

// ---------------------------------------------------------------------------
// Small conversion helpers (package-private)
// ---------------------------------------------------------------------------

// boolToInt converts a Go bool to a SQLite INTEGER (0 or 1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullableString returns a sql.NullString so that empty Go strings are stored
// as SQL NULL rather than as empty text, matching the schema columns that allow
// NULL (docstring, signature, visibility, metadata, provenance, errors).
func nullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// marshalStringSlice JSON-encodes a []string for storage in a TEXT column.
// A nil or empty slice is stored as SQL NULL (returned as empty string here,
// combined with nullableString for the INSERT).
func marshalStringSlice(ss []string) (sql.NullString, error) {
	if len(ss) == 0 {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(ss)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

// ---------------------------------------------------------------------------
// Unresolved reference operations
// ---------------------------------------------------------------------------

// UpsertUnresolvedRef inserts an UnresolvedRef row into the unresolved_refs
// table. It does not enforce uniqueness beyond the FK on from_node_id; callers
// should avoid inserting identical refs. Used primarily in tests and by the
// indexer when writing extraction results.
func (s *Store) UpsertUnresolvedRef(ref UnresolvedRef) error {
	const q = `
		INSERT INTO unresolved_refs (
			from_node_id, reference_name, reference_kind,
			line, col, candidates, file_path, language
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.DB.Exec(q,
		ref.FromNodeID, ref.ReferenceName, string(ref.ReferenceKind),
		ref.Line, ref.Col,
		nullableString(ref.Candidates),
		ref.FilePath, ref.Language,
	)
	if err != nil {
		return fmt.Errorf("codegraph: store: upsert unresolved ref: %w", err)
	}
	return nil
}

// ListUnresolvedRefs returns all rows from the unresolved_refs table ordered by
// id (insertion order). The list is intended for use by the Resolver to iterate
// over pending cross-file references.
func (s *Store) ListUnresolvedRefs() ([]UnresolvedRef, error) {
	const q = `
		SELECT id, from_node_id, reference_name, reference_kind,
		       line, col, candidates, file_path, language
		FROM unresolved_refs
		ORDER BY id`

	rows, err := s.db.DB.Query(q)
	if err != nil {
		return nil, fmt.Errorf("codegraph: store: list unresolved refs: %w", err)
	}
	defer rows.Close()

	var refs []UnresolvedRef
	for rows.Next() {
		var ref UnresolvedRef
		var candidates sql.NullString
		if err := rows.Scan(
			&ref.ID, &ref.FromNodeID, &ref.ReferenceName, &ref.ReferenceKind,
			&ref.Line, &ref.Col, &candidates, &ref.FilePath, &ref.Language,
		); err != nil {
			return nil, fmt.Errorf("codegraph: store: list unresolved refs: scan: %w", err)
		}
		ref.Candidates = candidates.String
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("codegraph: store: list unresolved refs: rows: %w", err)
	}
	return refs, nil
}

// DeleteUnresolvedRef removes the unresolved_refs row with the given id.
// No error is returned when the id does not exist (idempotent).
func (s *Store) DeleteUnresolvedRef(id int64) error {
	_, err := s.db.DB.Exec(`DELETE FROM unresolved_refs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("codegraph: store: delete unresolved ref: %w", err)
	}
	return nil
}

// FindNodeByQualifiedName returns the first node whose qualified_name exactly
// matches qualifiedName. Returns nil, nil when no such node exists.
func (s *Store) FindNodeByQualifiedName(qualifiedName string) (*Node, error) {
	const q = `
		SELECT id, kind, name, qualified_name, file_path, language,
		       start_line, end_line, start_column, end_column,
		       docstring, signature, visibility,
		       is_exported, is_async, is_static, is_abstract,
		       decorators, type_parameters, updated_at
		FROM nodes
		WHERE qualified_name = ?
		LIMIT 1`

	row := s.db.DB.QueryRow(q, qualifiedName)
	n, err := scanNode(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("codegraph: store: find node by qualified name: %w", err)
	}
	return n, nil
}

// FindNodeByName returns the first node whose name field exactly matches name.
// This is the fallback resolution strategy for references that carry only the
// short, unqualified symbol name (e.g. "Bar" instead of "pkg.Bar").
// Returns nil, nil when no matching node exists.
func (s *Store) FindNodeByName(name string) (*Node, error) {
	const q = `
		SELECT id, kind, name, qualified_name, file_path, language,
		       start_line, end_line, start_column, end_column,
		       docstring, signature, visibility,
		       is_exported, is_async, is_static, is_abstract,
		       decorators, type_parameters, updated_at
		FROM nodes
		WHERE name = ?
		LIMIT 1`

	row := s.db.DB.QueryRow(q, name)
	n, err := scanNode(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("codegraph: store: find node by name: %w", err)
	}
	return n, nil
}

// FindNodeBySuffix returns the first node whose qualified_name ends with
// "." + suffix. This supports partial-qualification resolution: a reference to
// "store.Create" will match a node with qualified_name "internal/store.Create".
// Returns nil, nil when no matching node exists.
func (s *Store) FindNodeBySuffix(suffix string) (*Node, error) {
	const q = `
		SELECT id, kind, name, qualified_name, file_path, language,
		       start_line, end_line, start_column, end_column,
		       docstring, signature, visibility,
		       is_exported, is_async, is_static, is_abstract,
		       decorators, type_parameters, updated_at
		FROM nodes
		WHERE qualified_name LIKE ?
		LIMIT 1`

	row := s.db.DB.QueryRow(q, "%."+suffix)
	n, err := scanNode(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("codegraph: store: find node by suffix: %w", err)
	}
	return n, nil
}

// GetNodesByFilePath returns all nodes whose file_path equals filePath and whose
// kind is not NodeKindFile, ordered by start_line ascending. This is used by the
// Bridge to list the symbols defined in a specific source file.
func (s *Store) GetNodesByFilePath(filePath string) ([]Node, error) {
	const q = `
		SELECT id, kind, name, qualified_name, file_path, language,
		       start_line, end_line, start_column, end_column,
		       docstring, signature, visibility,
		       is_exported, is_async, is_static, is_abstract,
		       decorators, type_parameters, updated_at
		FROM nodes
		WHERE file_path = ? AND kind != 'file'
		ORDER BY start_line`

	rows, err := s.db.DB.Query(q, filePath)
	if err != nil {
		return nil, fmt.Errorf("codegraph: store: get nodes by file path: %w", err)
	}
	defer rows.Close()

	var results []Node
	for rows.Next() {
		n, err := scanNodeRow(rows)
		if err != nil {
			return nil, fmt.Errorf("codegraph: store: get nodes by file path: scan: %w", err)
		}
		results = append(results, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("codegraph: store: get nodes by file path: rows: %w", err)
	}
	return results, nil
}

// NodeExistsForPath reports whether any node in the store has file_path equal to
// path. It is a lightweight existence check used by the Bridge to annotate memory
// search results without loading full node data.
func (s *Store) NodeExistsForPath(path string) (bool, error) {
	const q = `SELECT 1 FROM nodes WHERE file_path = ? LIMIT 1`
	var dummy int
	err := s.db.DB.QueryRow(q, path).Scan(&dummy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("codegraph: store: node exists for path: %w", err)
	}
	return true, nil
}

// EdgeExists reports whether an edge with the given source, target, and kind
// already exists in the edges table. Used by the Resolver to prevent creating
// duplicate edges when Resolve is called more than once.
func (s *Store) EdgeExists(source, target string, kind EdgeKind) (bool, error) {
	const q = `
		SELECT COUNT(*) FROM edges
		WHERE source = ? AND target = ? AND kind = ?`

	var count int
	if err := s.db.DB.QueryRow(q, source, target, string(kind)).Scan(&count); err != nil {
		return false, fmt.Errorf("codegraph: store: edge exists: %w", err)
	}
	return count > 0, nil
}
