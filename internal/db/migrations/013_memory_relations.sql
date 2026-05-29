-- 013_memory_relations.sql: Memory-to-memory conflict/unrelated edges (SPEC-039).
-- Supersedes relation reuses memories.superseded_by + SetSupersededBy.
-- This table stores conflicts_with and unrelated edges only.

CREATE TABLE IF NOT EXISTS memory_relations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id    TEXT NOT NULL,
    to_id      TEXT NOT NULL,
    relation   TEXT NOT NULL CHECK (relation IN ('conflicts_with','unrelated')),
    judged_by  TEXT NOT NULL DEFAULT 'manual',
    rationale  TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    UNIQUE(from_id, to_id)
);

CREATE INDEX IF NOT EXISTS idx_memory_relations_from ON memory_relations(from_id);
CREATE INDEX IF NOT EXISTS idx_memory_relations_to ON memory_relations(to_id);
CREATE INDEX IF NOT EXISTS idx_memory_relations_relation ON memory_relations(relation);

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (13, datetime('now'));
