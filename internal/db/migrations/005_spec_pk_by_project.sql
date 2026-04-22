-- 005_spec_pk_by_project.sql: Change the specs primary key from a single TEXT
-- column (id) to a composite key (project, id). This aligns the schema with
-- the per-project ID semantics enforced by NextSpecID: two different projects
-- can each have a SPEC-001 without conflicting.
--
-- Pre-flight check: the Go migration runner executes a collision query before
-- this SQL runs and aborts the migration if any spec ID appears in more than
-- one project. See internal/db/migrate.go (migration005PreFlight).
--
-- The child tables (spec_history, spec_pushbacks) reference specs by spec_id
-- TEXT only (not the full composite key). SQLite requires that a REFERENCES
-- clause points to the referenced table's PRIMARY KEY; since the PK is now
-- composite, the old REFERENCES specs(id) declaration would cause a "foreign
-- key mismatch" error at runtime. We therefore recreate both child tables
-- without the cross-table FK declaration — referential integrity is enforced
-- at the application layer (the service always resolves the spec before writing
-- history or pushbacks).
--
-- This migration is forward-only. There is no DOWN path.

-- Rebuild specs with composite PK (project, id).
CREATE TABLE specs_new (
    id              TEXT NOT NULL,
    title           TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'draft',
    project         TEXT NOT NULL,
    backlog_id      TEXT,
    assigned_agents TEXT NOT NULL DEFAULT '[]',
    files_changed   TEXT NOT NULL DEFAULT '[]',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    PRIMARY KEY (project, id)
);

INSERT INTO specs_new
SELECT id, title, status, project, backlog_id, assigned_agents, files_changed, created_at, updated_at
FROM specs;

DROP TABLE specs;
ALTER TABLE specs_new RENAME TO specs;

CREATE INDEX IF NOT EXISTS idx_specs_project ON specs(project);
CREATE INDEX IF NOT EXISTS idx_specs_status  ON specs(status);
CREATE INDEX IF NOT EXISTS idx_specs_backlog ON specs(backlog_id);

-- Rebuild spec_history without the REFERENCES specs(id) FK that is now
-- incompatible with the composite PK. Existing data is preserved.
CREATE TABLE spec_history_new (
    id          TEXT PRIMARY KEY,
    spec_id     TEXT NOT NULL,
    from_status TEXT NOT NULL,
    to_status   TEXT NOT NULL,
    by          TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    at          TEXT NOT NULL
);

INSERT INTO spec_history_new
SELECT id, spec_id, from_status, to_status, by, reason, at
FROM spec_history;

DROP TABLE spec_history;
ALTER TABLE spec_history_new RENAME TO spec_history;

CREATE INDEX IF NOT EXISTS idx_spec_history_spec ON spec_history(spec_id);

-- Rebuild spec_pushbacks without the REFERENCES specs(id) FK for the same reason.
CREATE TABLE spec_pushbacks_new (
    id          TEXT PRIMARY KEY,
    spec_id     TEXT NOT NULL,
    from_agent  TEXT NOT NULL,
    questions   TEXT NOT NULL DEFAULT '[]',
    resolved    INTEGER NOT NULL DEFAULT 0,
    resolution  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    resolved_at TEXT
);

INSERT INTO spec_pushbacks_new
SELECT id, spec_id, from_agent, questions, resolved, resolution, created_at, resolved_at
FROM spec_pushbacks;

DROP TABLE spec_pushbacks;
ALTER TABLE spec_pushbacks_new RENAME TO spec_pushbacks;

CREATE INDEX IF NOT EXISTS idx_spec_pushbacks_spec       ON spec_pushbacks(spec_id);
CREATE INDEX IF NOT EXISTS idx_spec_pushbacks_unresolved ON spec_pushbacks(spec_id) WHERE resolved = 0;
