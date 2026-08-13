-- 018_quality_certificates.sql: registro de certificados de calidad (SPEC-115).
CREATE TABLE IF NOT EXISTS quality_certificates (
    id                TEXT    PRIMARY KEY,          -- UUIDv7
    project           TEXT    NOT NULL,
    spec_id           TEXT    NOT NULL,
    head_sha          TEXT    NOT NULL,
    base_sha          TEXT    NOT NULL DEFAULT '',
    constitution_hash TEXT    NOT NULL,
    schema_version    INTEGER NOT NULL,
    verdict           TEXT    NOT NULL,             -- pass | fail | findings
    dirty             INTEGER NOT NULL DEFAULT 0,
    mneme_version     TEXT    NOT NULL DEFAULT '',
    started_at        TEXT    NOT NULL,
    finished_at       TEXT    NOT NULL,
    duration_ms       INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_quality_certs_spec ON quality_certificates(project, spec_id, created_at);
CREATE INDEX IF NOT EXISTS idx_quality_certs_sha  ON quality_certificates(project, spec_id, head_sha);

CREATE TABLE IF NOT EXISTS quality_checks (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    certificate_id TEXT    NOT NULL,
    seq            INTEGER NOT NULL,
    kind           TEXT    NOT NULL,   -- tree | constitution | gate (S2..S6 añaden más)
    name           TEXT    NOT NULL,
    status         TEXT    NOT NULL,   -- pass | fail | skipped | finding | acked
    exit_code      INTEGER NOT NULL DEFAULT 0,
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    output_sha256  TEXT    NOT NULL DEFAULT '',
    output_bytes   INTEGER NOT NULL DEFAULT 0,
    output_tail    TEXT    NOT NULL DEFAULT '',
    summary        TEXT    NOT NULL DEFAULT '',
    detail         TEXT    NOT NULL DEFAULT '',   -- JSON, abierto para S2..S6
    acked_by       TEXT    NOT NULL DEFAULT '',
    acked_at       TEXT    NOT NULL DEFAULT '',
    justification  TEXT    NOT NULL DEFAULT '',
    created_at     TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_quality_checks_cert ON quality_checks(certificate_id, seq);
INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (18, datetime('now'));
