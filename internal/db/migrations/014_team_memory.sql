-- 014_team_memory.sql: Add shared+author columns to memories for the
-- team-memory git-native EPIC (SPEC-053, SS-A/SPEC-061).
--
-- shared: 2-level sharing flag over the existing project scope (no new scope):
--   0 = local (never materialized to the shared vault) — the default, so
--       existing behaviour is completely unchanged until SS-B wires the
--       write-through materialisation.
--   1 = auto-shared (durable memory types, when team-memory is active).
--   2 = team-curated (explicitly promoted via `mneme promote`, SS-C).
-- author: human git identity ("Name <email>") distinct from created_by (the
--   saving agent, e.g. "claude-code"). Empty until SS-B's gitident helper
--   populates it on write-through.
--
-- Both columns default to inert values so this migration alone changes no
-- observable behaviour — mem_save keeps persisting shared=0, author=''.

ALTER TABLE memories ADD COLUMN shared INTEGER NOT NULL DEFAULT 0 CHECK (shared IN (0,1,2));
ALTER TABLE memories ADD COLUMN author  TEXT NOT NULL DEFAULT '';

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (14, datetime('now'));
