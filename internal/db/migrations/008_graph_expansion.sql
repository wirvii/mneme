-- 008_graph_expansion.sql: Add index for entity-based memory lookups.
-- Part of EPIC-2 (SPEC-007): 1-hop graph expansion in mem_search.
--
-- GetEntityMemories queries memory_entities by entity_id, but the table's
-- primary key is (memory_id, entity_id), so scans by entity_id alone require
-- a full table scan. This index makes GetEntityMemories O(log n) instead of
-- O(n), which is critical for the graph expansion loop:
--   - With 20K memory_entity rows and ~200 unique neighbor entities per query,
--     the full-scan path would add ~1s overhead. With this index: ~20ms.
--
-- Rollback: DROP INDEX IF EXISTS idx_memory_entities_entity;

CREATE INDEX IF NOT EXISTS idx_memory_entities_entity ON memory_entities(entity_id);

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (8, datetime('now'));
