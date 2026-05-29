-- 012_add_spec_base_sha_and_audits.sql: base-SHA binding + structured lane audit records (SPEC-036).
ALTER TABLE specs ADD COLUMN base_sha TEXT NOT NULL DEFAULT '';
CREATE TABLE lane_audits (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    spec_id       TEXT    NOT NULL,
    passed        INTEGER NOT NULL,
    file_count    INTEGER NOT NULL,
    lines_changed INTEGER NOT NULL,
    breaches      TEXT    NOT NULL DEFAULT '',
    base_sha      TEXT    NOT NULL DEFAULT '',
    created_at    TEXT    NOT NULL
);
CREATE INDEX idx_lane_audits_spec ON lane_audits(spec_id, created_at);
INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (12, datetime('now'));
