package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// VectorSearchOptions controls the scope of a vector similarity search.
// Zero values produce an unfiltered scan over all embeddings.
type VectorSearchOptions struct {
	// Project restricts results to memories belonging to this project slug.
	// Empty means no project filter.
	Project string

	// Scope restricts results to memories with this scope.
	// Zero value means no scope filter.
	Scope model.Scope

	// Limit caps the number of results returned. Zero defaults to 20.
	Limit int
}

// VectorResult carries a memory ID and its cosine similarity score relative
// to the query vector used in VectorSearch.
type VectorResult struct {
	// MemoryID is the UUIDv7 of the memory.
	MemoryID string

	// Similarity is the cosine similarity in [-1, 1]. Higher values indicate
	// a closer semantic match. Pre-normalised embeddings make this equal to
	// the dot product, which is cheaper to compute.
	Similarity float64
}

// SaveEmbedding persists or replaces the embedding for the given memory ID.
// The vector is serialised as a contiguous little-endian float32 BLOB.
// Uses INSERT OR REPLACE so repeated saves are idempotent upserts.
func (s *MemoryStore) SaveEmbedding(ctx context.Context, e *model.Embedding) error {
	blob := vectorToBlob(e.Vector)

	const q = `
		INSERT OR REPLACE INTO embeddings (memory_id, vector, model, dimensions, created_at)
		VALUES (?, ?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, q,
		e.MemoryID,
		blob,
		e.Model,
		e.Dimensions,
		e.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("store: save embedding: %w", err)
	}

	return nil
}

// GetEmbedding retrieves the embedding for a single memory ID.
// Returns (nil, nil) when no embedding exists for that memory.
func (s *MemoryStore) GetEmbedding(ctx context.Context, memoryID string) (*model.Embedding, error) {
	const q = `
		SELECT memory_id, vector, model, dimensions, created_at
		FROM embeddings
		WHERE memory_id = ?`

	row := s.db.QueryRowContext(ctx, q, memoryID)

	var (
		blob      []byte
		createdAt string
		e         model.Embedding
	)

	err := row.Scan(&e.MemoryID, &blob, &e.Model, &e.Dimensions, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: get embedding: %w", err)
	}

	e.Vector = blobToVector(blob, e.Dimensions)

	if t, err := parseTime(createdAt); err == nil {
		e.CreatedAt = t
	}

	return &e, nil
}

// DeleteEmbedding removes the embedding for the given memory ID. It is
// idempotent — deleting a non-existent embedding is not an error.
func (s *MemoryStore) DeleteEmbedding(ctx context.Context, memoryID string) error {
	const q = `DELETE FROM embeddings WHERE memory_id = ?`

	_, err := s.db.ExecContext(ctx, q, memoryID)
	if err != nil {
		return fmt.Errorf("store: delete embedding: %w", err)
	}

	return nil
}

// VectorSearch loads all embeddings that match the scope/project filters,
// computes cosine similarity against queryVec in Go, and returns results
// sorted by similarity descending. Limit caps the result count.
//
// The brute-force scan is O(N) where N is the number of stored embeddings.
// For <10 K vectors this is comfortably within the <50 ms target. If
// performance becomes an issue this is the single function to optimise
// (e.g. with an in-memory HNSW index) without changing its signature.
func (s *MemoryStore) VectorSearch(ctx context.Context, queryVec []float32, opts VectorSearchOptions) ([]VectorResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	// Build a JOIN query so we can filter by project/scope without loading
	// embeddings for memories that would be excluded anyway.
	where := []string{"m.deleted_at IS NULL"}
	args := []any{}

	if opts.Project != "" {
		where = append(where, "m.project = ?")
		args = append(args, opts.Project)
	}
	if opts.Scope != "" {
		where = append(where, "m.scope = ?")
		args = append(args, string(opts.Scope))
	}

	// Build WHERE clause by hand — safe because all placeholders are positional.
	whereClause := "TRUE"
	if len(where) > 0 {
		whereClause = joinAnd(where)
	}

	q := fmt.Sprintf(`
		SELECT e.memory_id, e.vector, e.dimensions
		FROM embeddings e
		JOIN memories m ON m.id = e.memory_id
		WHERE %s`, whereClause)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: vector search: %w", err)
	}
	defer rows.Close()

	var results []VectorResult

	for rows.Next() {
		var (
			memID string
			blob  []byte
			dims  int
		)
		if err := rows.Scan(&memID, &blob, &dims); err != nil {
			return nil, fmt.Errorf("store: vector search: scan: %w", err)
		}

		vec := blobToVector(blob, dims)
		if len(vec) != len(queryVec) {
			// Dimension mismatch — skip stale embeddings from a previous model.
			continue
		}

		sim := cosineSimilarity(vec, queryVec)
		results = append(results, VectorResult{MemoryID: memID, Similarity: sim})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: vector search: iterate: %w", err)
	}

	// Sort by similarity descending so the best matches come first.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Similarity != results[j].Similarity {
			return results[i].Similarity > results[j].Similarity
		}
		return results[i].MemoryID < results[j].MemoryID
	})

	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results, nil
}

// CountEmbeddings returns the number of embedding records stored for memories
// belonging to the given project. An empty project string counts all embeddings
// in the database (useful for the global store).
func (s *MemoryStore) CountEmbeddings(ctx context.Context, project string) (int, error) {
	var (
		q    string
		args []any
	)

	if project == "" {
		q = `SELECT COUNT(*) FROM embeddings`
	} else {
		q = `
			SELECT COUNT(*)
			FROM embeddings e
			JOIN memories m ON m.id = e.memory_id
			WHERE m.project = ? AND m.deleted_at IS NULL`
		args = []any{project}
	}

	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count embeddings: %w", err)
	}

	return n, nil
}

// ListMemoriesWithoutEmbedding returns IDs and titles of active memories that
// do not yet have a stored embedding. An empty project string returns results
// across all projects in the database (used for global store backfill).
// Limit caps the number of returned rows; pass 0 for no cap.
func (s *MemoryStore) ListMemoriesWithoutEmbedding(ctx context.Context, project string, limit int) ([]model.Memory, error) {
	where := []string{
		"m.deleted_at IS NULL",
		"m.superseded_by IS NULL",
		"e.memory_id IS NULL",
	}
	args := []any{}

	if project != "" {
		where = append(where, "m.project = ?")
		args = append(args, project)
	}

	q := fmt.Sprintf(`
		SELECT m.id, m.type, m.scope, m.title, m.content, m.topic_key, m.project,
		       m.session_id, m.created_by, m.created_at, m.updated_at,
		       m.importance, m.confidence, m.access_count, m.last_accessed,
		       m.decay_rate, m.revision_count, m.superseded_by, m.deleted_at
		FROM memories m
		LEFT JOIN embeddings e ON e.memory_id = m.id
		WHERE %s
		ORDER BY m.created_at ASC`, strings.Join(where, " AND "))

	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list memories without embedding: %w", err)
	}
	defer rows.Close()

	var memories []model.Memory
	for rows.Next() {
		m, err := scanMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list memories without embedding: scan: %w", err)
		}
		memories = append(memories, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list memories without embedding: iterate: %w", err)
	}

	return memories, nil
}

// ─── vector serialisation helpers ────────────────────────────────────────────

// vectorToBlob encodes a float32 slice as a contiguous little-endian BLOB.
// Each float32 occupies exactly 4 bytes; the total BLOB size is len(v)*4.
func vectorToBlob(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		bits := math.Float32bits(f)
		binary.LittleEndian.PutUint32(b[i*4:], bits)
	}
	return b
}

// blobToVector decodes a little-endian BLOB back into a float32 slice.
// dims is used to pre-allocate; if the BLOB encodes a different number of
// values, dims is ignored and the actual count is used.
func blobToVector(b []byte, dims int) []float32 {
	n := len(b) / 4
	if n == 0 {
		return nil
	}
	v := make([]float32, n)
	for i := range v {
		bits := binary.LittleEndian.Uint32(b[i*4:])
		v[i] = math.Float32frombits(bits)
	}
	_ = dims // kept for caller documentation clarity
	return v
}

// cosineSimilarity computes the dot product of two L2-normalised vectors.
// Because embeddings are L2-normalised before storage, the dot product equals
// cosine similarity. Both vectors must have the same length; this is a
// precondition, not checked at runtime for performance.
func cosineSimilarity(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// joinAnd concatenates conditions with " AND ".
func joinAnd(parts []string) string {
	result := parts[0]
	for _, p := range parts[1:] {
		result += " AND " + p
	}
	return result
}
