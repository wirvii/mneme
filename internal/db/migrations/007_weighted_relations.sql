-- 007_weighted_relations.sql: Normalise relation weights and add traversal tracking.
-- Part of EPIC-2 (SPEC-005).
--
-- The relations table already has a weight column (002_knowledge_graph.sql)
-- with DEFAULT 1.0 and all existing rows at weight=1.0. This migration:
--   1. Backfills existing rows with type-appropriate default weights.
--   2. Adds last_traversed_at for temporal tracking of graph navigation.
--   3. Creates indices for weight-ordered and recency-ordered queries.
--
-- Weight semantics change from "unbounded, always 1.0" to
-- "normalised [0.0, 1.0] with type-based defaults". No schema change is
-- needed for the weight column itself since REAL already covers the new range.

-- Backfill existing relations with type-appropriate default weights.
-- All existing rows have weight=1.0 from migration 002. The ELSE 0.5 branch
-- handles any rows whose type is not in the known enum (defensive fallback).
UPDATE relations SET weight = CASE type
    WHEN 'depends_on'     THEN 0.9
    WHEN 'implements'     THEN 0.8
    WHEN 'part_of'        THEN 0.85
    WHEN 'uses'           THEN 0.7
    WHEN 'supersedes'     THEN 0.6
    WHEN 'related_to'     THEN 0.5
    WHEN 'conflicts_with' THEN 0.7
    WHEN 'references'     THEN 0.4
    ELSE 0.5
END;

-- Add traversal tracking column. NULL means "never traversed since tracking
-- began". TEXT stores ISO 8601 timestamps, consistent with other *_at columns
-- in the schema (memories.created_at, entities.created_at, etc.).
ALTER TABLE relations ADD COLUMN last_traversed_at TEXT;

-- Index for weight-ordered queries (1-hop expansion ORDER BY weight DESC, PPR).
CREATE INDEX IF NOT EXISTS idx_relations_weight ON relations(weight);

-- Index for recency-ordered traversal queries (Hebbian decay, teleportation bias).
CREATE INDEX IF NOT EXISTS idx_relations_last_traversed ON relations(last_traversed_at);

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (7, datetime('now'));
