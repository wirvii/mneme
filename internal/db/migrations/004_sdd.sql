-- 004_sdd.sql: Tables for the SDD (Spec-Driven Development) engine.
-- Adds backlog items, specs with state machine, history tracking, and pushbacks.

-- Backlog items table
CREATE TABLE IF NOT EXISTS backlog_items (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'raw',
    priority        TEXT NOT NULL DEFAULT 'medium',
    project         TEXT NOT NULL,
    spec_id         TEXT,
    archive_reason  TEXT NOT NULL DEFAULT '',
    position        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_backlog_project ON backlog_items(project);
CREATE INDEX IF NOT EXISTS idx_backlog_status ON backlog_items(status);
CREATE INDEX IF NOT EXISTS idx_backlog_priority_position ON backlog_items(priority, position);

-- Specs table (SDD state machine)
CREATE TABLE IF NOT EXISTS specs (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'draft',
    project         TEXT NOT NULL,
    backlog_id      TEXT,
    assigned_agents TEXT NOT NULL DEFAULT '[]',
    files_changed   TEXT NOT NULL DEFAULT '[]',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_specs_project ON specs(project);
CREATE INDEX IF NOT EXISTS idx_specs_status ON specs(status);
CREATE INDEX IF NOT EXISTS idx_specs_backlog ON specs(backlog_id);

-- Spec history (state transitions)
CREATE TABLE IF NOT EXISTS spec_history (
    id              TEXT PRIMARY KEY,
    spec_id         TEXT NOT NULL REFERENCES specs(id) ON DELETE CASCADE,
    from_status     TEXT NOT NULL,
    to_status       TEXT NOT NULL,
    by              TEXT NOT NULL,
    reason          TEXT NOT NULL DEFAULT '',
    at              TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_spec_history_spec ON spec_history(spec_id);

-- Spec pushbacks (questions that block progress)
CREATE TABLE IF NOT EXISTS spec_pushbacks (
    id              TEXT PRIMARY KEY,
    spec_id         TEXT NOT NULL REFERENCES specs(id) ON DELETE CASCADE,
    from_agent      TEXT NOT NULL,
    questions       TEXT NOT NULL DEFAULT '[]',
    resolved        INTEGER NOT NULL DEFAULT 0,
    resolution      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    resolved_at     TEXT
);

CREATE INDEX IF NOT EXISTS idx_spec_pushbacks_spec ON spec_pushbacks(spec_id);
CREATE INDEX IF NOT EXISTS idx_spec_pushbacks_unresolved ON spec_pushbacks(spec_id) WHERE resolved = 0;

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (4, datetime('now'));
