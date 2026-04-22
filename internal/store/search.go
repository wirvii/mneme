package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/juanftp/mneme/internal/model"
)

// SearchOptions parameterises a full-text search query.
type SearchOptions struct {
	// Project restricts results to a specific project slug. Empty means no filter.
	Project string

	// Scope restricts results to a specific scope. Zero value means no filter.
	Scope model.Scope

	// Type restricts results to a specific memory type. Zero value means no filter.
	Type model.MemoryType

	// Limit caps the number of results. Defaults to 20 when zero.
	Limit int

	// IncludeSuperseded includes superseded memories in results when true.
	IncludeSuperseded bool

	// MinRelevance is currently unused but reserved for future score blending.
	MinRelevance float64

	// PreviewLength is the maximum character length of the generated preview snippet.
	// Defaults to 200 when zero.
	PreviewLength int
}

// FTS5Search performs a full-text search over memories using SQLite's FTS5 engine.
// The input query is tokenised, stripped of common stop words, and converted to an
// FTS5 OR query. When the input contains quoted phrases those are preserved as
// exact-match phrase queries.
//
// Results are ordered by BM25 score (ascending — SQLite FTS5 returns negative values,
// so ascending = best match first). Files are populated for each result.
func (s *MemoryStore) FTS5Search(ctx context.Context, query string, opts SearchOptions) ([]model.SearchResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.PreviewLength <= 0 {
		opts.PreviewLength = 200
	}

	ftsQuery := buildFTS5Query(query)

	where := []string{
		"m.deleted_at IS NULL",
		"memories_fts MATCH ?",
	}
	args := []any{ftsQuery}

	if opts.Project != "" {
		where = append(where, "m.project = ?")
		args = append(args, opts.Project)
	}
	if opts.Scope != "" {
		where = append(where, "m.scope = ?")
		args = append(args, string(opts.Scope))
	}
	if opts.Type != "" {
		where = append(where, "m.type = ?")
		args = append(args, string(opts.Type))
	}
	if !opts.IncludeSuperseded {
		where = append(where, "m.superseded_by IS NULL")
	}

	q := fmt.Sprintf(`
		SELECT m.id, m.type, m.scope, m.title, m.content, m.topic_key, m.project,
		       m.session_id, m.created_by, m.created_at, m.updated_at,
		       m.importance, m.confidence, m.access_count, m.last_accessed,
		       m.decay_rate, m.revision_count, m.superseded_by, m.deleted_at,
		       bm25(memories_fts) AS bm25_score
		FROM memories m
		JOIN memories_fts ON m.rowid = memories_fts.rowid
		WHERE %s
		ORDER BY bm25_score ASC
		LIMIT ?`,
		strings.Join(where, " AND "),
	)
	args = append(args, opts.Limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: fts5 search: %w", err)
	}
	defer rows.Close()

	// Collect all rows before loading files. Keeping rows open while issuing
	// a second query causes a deadlock when the pool is limited to one connection
	// (as in tests using SQLite in-memory databases).
	type interim struct {
		m    *model.Memory
		bm25 float64
	}
	var interims []interim

	for rows.Next() {
		var bm25 float64
		m, scanErr := scanMemoryWithExtra(rows, &bm25)
		if scanErr != nil {
			return nil, fmt.Errorf("store: fts5 search: scan: %w", scanErr)
		}
		interims = append(interims, interim{m: m, bm25: bm25})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: fts5 search: iterate: %w", err)
	}
	rows.Close() // release the connection before issuing file queries

	var results []model.SearchResult
	for _, it := range interims {
		if err := s.loadFiles(ctx, it.m); err != nil {
			return nil, err
		}
		results = append(results, model.SearchResult{
			Memory:         it.m,
			Preview:        makePreview(it.m.Content, opts.PreviewLength),
			BM25Score:      it.bm25,
			RelevanceScore: bm25ToRelevance(it.bm25),
		})
	}

	return results, nil
}

// buildFTS5Query converts a human-readable query string into an FTS5 query.
// Quoted phrases are preserved. Unquoted tokens are stripped of common English
// stop words, wrapped in FTS5 double-quotes (neutralising operator characters
// such as -, *, :, ^, (, )), and joined with OR.
//
// If all tokens are stop words and there are no quoted phrases, the original
// input is escaped with ftsQuoteToken and returned as a single term so that
// FTS5 operator characters in the raw input do not cause a syntax error.
func buildFTS5Query(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return input
	}

	// Extract quoted phrases and replace them with placeholders so we don't
	// split inside them.
	var phrases []string
	var cleaned string

	inQuote := false
	var buf strings.Builder
	var phraseBuf strings.Builder

	for _, ch := range input {
		switch {
		case ch == '"' && !inQuote:
			inQuote = true
			phraseBuf.Reset()
			phraseBuf.WriteRune(ch)
		case ch == '"' && inQuote:
			inQuote = false
			phraseBuf.WriteRune(ch)
			phrases = append(phrases, phraseBuf.String())
			buf.WriteString(" ")
		case inQuote:
			phraseBuf.WriteRune(ch)
		default:
			buf.WriteRune(ch)
		}
	}
	cleaned = buf.String()

	// Tokenise the non-quoted portion and remove stop words.
	// Each surviving token is wrapped in FTS5 double-quotes so that operator
	// characters (-, *, :, ^, (, )) are treated as literals, not operators.
	rawTokens := strings.Fields(cleaned)
	var kept []string
	for _, tok := range rawTokens {
		lower := strings.ToLower(tok)
		if !isStopWord(lower) {
			kept = append(kept, ftsQuoteToken(tok))
		}
	}

	// If all unquoted tokens were stop words, fall back to a quoted version of
	// the original input so that any operator characters are neutralised.
	if len(kept) == 0 && len(phrases) == 0 {
		return ftsQuoteToken(input)
	}

	parts := append(phrases, kept...)
	return strings.Join(parts, " OR ")
}

// ftsQuoteToken wraps a single token in FTS5 double-quotes and escapes any
// embedded double-quote characters by doubling them. This neutralises FTS5
// operator characters (-, *, :, ^, (, )) that would otherwise be interpreted
// as query syntax, turning the token into a phrase-match literal.
func ftsQuoteToken(tok string) string {
	return `"` + strings.ReplaceAll(tok, `"`, `""`) + `"`
}

// isStopWord reports whether a lowercase token should be removed from the FTS query.
func isStopWord(w string) bool {
	return stopWords[w]
}

// stopWords is the set of common English words excluded from FTS queries to
// reduce noise and improve match precision.
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"might": true, "can": true, "shall": true, "to": true, "of": true,
	"in": true, "for": true, "on": true, "with": true, "at": true, "by": true,
	"from": true, "as": true, "into": true, "through": true, "during": true,
	"before": true, "after": true, "above": true, "below": true,
	"between": true, "out": true, "off": true, "over": true, "under": true,
	"again": true, "further": true, "then": true, "once": true, "here": true,
	"there": true, "when": true, "where": true, "why": true, "how": true,
	"all": true, "each": true, "every": true, "both": true, "few": true,
	"more": true, "most": true, "other": true, "some": true, "such": true,
	"no": true, "nor": true, "not": true, "only": true, "own": true,
	"same": true, "so": true, "than": true, "too": true, "very": true,
	"just": true, "because": true, "but": true, "and": true, "or": true,
	"if": true, "while": true, "about": true, "up": true, "it": true,
	"its": true, "this": true, "that": true, "these": true, "those": true,
	"i": true, "me": true, "my": true, "we": true, "our": true, "he": true,
	"she": true, "they": true, "them": true, "their": true, "what": true,
	"which": true, "who": true, "whom": true,
}

// makePreview returns a truncated excerpt of content. If content is shorter than
// maxLen the full content is returned. Otherwise the first maxLen characters are
// returned followed by "...".
func makePreview(content string, maxLen int) string {
	runes := []rune(content)
	if len(runes) <= maxLen {
		return content
	}
	return string(runes[:maxLen]) + "..."
}

// bm25ToRelevance converts a raw BM25 score (negative float, closer to 0 = better)
// into a normalised 0.0–1.0 relevance score. The conversion is approximate and
// intended only to give callers a human-friendly signal.
func bm25ToRelevance(bm25 float64) float64 {
	if bm25 >= 0 {
		return 0
	}
	// Simple sigmoid-like normalisation: -bm25 / (1 + -bm25).
	abs := -bm25
	return abs / (1 + abs)
}

// scanMemoryWithExtra scans a row that has an extra trailing column (bm25 score)
// beyond the standard memory columns.
func scanMemoryWithExtra(rows scannerRow, extra ...any) (*model.Memory, error) {
	var m model.Memory

	// Re-use the null-string pattern from scanMemoryRow but add the extra dest.
	var (
		tKey         interface{}
		proj         interface{}
		sessID       interface{}
		createdBy    interface{}
		supersededBy interface{}
		lastAccessed interface{}
		deletedAt    interface{}
		createdAt    string
		updatedAt    string
	)

	dests := []any{
		&m.ID, &m.Type, &m.Scope, &m.Title, &m.Content,
		&tKey, &proj,
		&sessID, &createdBy,
		&createdAt, &updatedAt,
		&m.Importance, &m.Confidence, &m.AccessCount, &lastAccessed,
		&m.DecayRate, &m.RevisionCount, &supersededBy, &deletedAt,
	}
	dests = append(dests, extra...)

	if err := rows.Scan(dests...); err != nil {
		return nil, err
	}

	m.TopicKey = nullableToString(tKey)
	m.Project = nullableToString(proj)
	m.SessionID = nullableToString(sessID)
	m.CreatedBy = nullableToString(createdBy)
	m.SupersededBy = nullableToString(supersededBy)

	if t, err := parseTime(createdAt); err == nil {
		m.CreatedAt = t
	}
	if t, err := parseTime(updatedAt); err == nil {
		m.UpdatedAt = t
	}
	if s := nullableToString(lastAccessed); s != "" {
		if t, err := parseTime(s); err == nil {
			m.LastAccessed = &t
		}
	}
	if s := nullableToString(deletedAt); s != "" {
		if t, err := parseTime(s); err == nil {
			m.DeletedAt = &t
		}
	}

	return &m, nil
}

// nullableToString converts a scanned nullable interface value to a string.
// Handles *string, string, []byte, and nil gracefully.
func nullableToString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case *string:
		if val == nil {
			return ""
		}
		return *val
	case []byte:
		return string(val)
	}
	return fmt.Sprintf("%v", v)
}
