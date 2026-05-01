-- 009_unresolved_references.sql: Track wikilinks that could not be resolved.
-- Part of EPIC-3 (SPEC-012): unresolved_references table + auto-resolve.
--
-- When a memory contains [[topic_key]] and no memory with that topic_key
-- exists at save time, a row is inserted here. When a memory with a matching
-- topic_key is later saved, the row is deleted and the relation is created
-- automatically (auto-resolve).
--
-- Rollback: DROP TABLE IF EXISTS unresolved_references;

CREATE TABLE IF NOT EXISTS unresolved_references (
    id                TEXT PRIMARY KEY,
    source_memory_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    target_topic_key  TEXT NOT NULL,
    project           TEXT,
    mention_count     INTEGER NOT NULL DEFAULT 1,
    first_seen_at     TEXT NOT NULL,
    last_seen_at      TEXT NOT NULL
);

-- UPSERT key: a (source, target) pair must be unique.
-- Enables INSERT ... ON CONFLICT DO UPDATE for atomic mention-count increment.
CREATE UNIQUE INDEX IF NOT EXISTS idx_unresolved_source_target
    ON unresolved_references(source_memory_id, target_topic_key);

-- Auto-resolve hot path: find all unresolved refs pointing to a topic_key.
CREATE INDEX IF NOT EXISTS idx_unresolved_target_key
    ON unresolved_references(target_topic_key);

-- mem_gaps query (SPEC-W3): list gaps by project ordered by frequency.
CREATE INDEX IF NOT EXISTS idx_unresolved_project
    ON unresolved_references(project, mention_count DESC);

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (9, datetime('now'));
