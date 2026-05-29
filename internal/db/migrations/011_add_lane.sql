-- 011_add_lane.sql: Add lane and scope columns for graduated lanes (SPEC-035).
-- Lane controls the SDD workflow path: trivial items skip the spec/plan phases
-- and go directly to implementing after a short rationale. Scope is a glob pattern
-- used by the post-implementation auditor to verify files were not changed outside
-- the declared boundary.
ALTER TABLE backlog_items ADD COLUMN lane  TEXT NOT NULL DEFAULT 'standard' CHECK (lane IN ('trivial','standard'));
ALTER TABLE backlog_items ADD COLUMN scope TEXT NOT NULL DEFAULT '';
ALTER TABLE specs ADD COLUMN lane  TEXT NOT NULL DEFAULT 'standard' CHECK (lane IN ('trivial','standard'));
ALTER TABLE specs ADD COLUMN scope TEXT NOT NULL DEFAULT '';
INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (11, datetime('now'));
