-- 006_rule_fields.sql: Add applies_to and severity columns to memories
-- for the new "rule" memory type (SPEC-001, EPIC-1).
--
-- applies_to: JSON array of pattern strings (globs, tool selectors, negations).
-- severity: enforcement level for rules (info, warn, block).
--
-- Both columns have defaults that make them transparent to existing
-- non-rule memories. The CHECK constraint on severity admits '' (empty)
-- so that non-rule memories stored without a severity value pass validation.
--
-- UP

ALTER TABLE memories ADD COLUMN applies_to TEXT NOT NULL DEFAULT '[]';
ALTER TABLE memories ADD COLUMN severity   TEXT NOT NULL DEFAULT ''
    CHECK (severity IN ('', 'info', 'warn', 'block'));

-- Index to efficiently list all active rules for a given project.
-- Used by mem_context (SPEC-R2) and the matching engine (SPEC-R3).
-- The partial index covers only rule rows so it stays compact regardless
-- of total memory count.
CREATE INDEX IF NOT EXISTS idx_memories_rules
    ON memories(project, type)
    WHERE type = 'rule' AND deleted_at IS NULL;

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (6, datetime('now'));

-- DOWN
--
-- SQLite supports DROP COLUMN since 3.35.0 (2021-03-12). The mattn/go-sqlite3
-- version bundled with mneme (3.46+) supports it. If running on an older SQLite,
-- use the table-rebuild technique from migration 005 instead.

-- DROP INDEX IF EXISTS idx_memories_rules;
-- ALTER TABLE memories DROP COLUMN severity;
-- ALTER TABLE memories DROP COLUMN applies_to;
-- DELETE FROM schema_version WHERE version = 6;
