CREATE TABLE IF NOT EXISTS execution_contracts (
    id TEXT PRIMARY KEY, project TEXT NOT NULL, uuid TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL, source_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
    goal TEXT NOT NULL, scope_json TEXT NOT NULL DEFAULT '[]', verification_json TEXT NOT NULL DEFAULT '[]',
    development_method TEXT NOT NULL, base_sha TEXT NOT NULL DEFAULT '', contract_revision INTEGER NOT NULL DEFAULT 0,
    contract_hash TEXT NOT NULL DEFAULT '', correction_rounds INTEGER NOT NULL DEFAULT 0,
    max_correction_rounds INTEGER NOT NULL DEFAULT 1, dev_evidence_command TEXT NOT NULL DEFAULT '',
    dev_evidence_exit INTEGER NOT NULL DEFAULT 0, dev_evidence_tail TEXT NOT NULL DEFAULT '',
    dev_evidence_sha TEXT NOT NULL DEFAULT '', dev_evidence_at TEXT NOT NULL DEFAULT '', created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL, locked_at TEXT NOT NULL DEFAULT '', completed_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_contracts_uuid ON execution_contracts(uuid) WHERE uuid <> '';
CREATE INDEX IF NOT EXISTS idx_execution_contracts_status ON execution_contracts(project,status);
CREATE INDEX IF NOT EXISTS idx_execution_contracts_source ON execution_contracts(project,source_type,source_id);

CREATE TABLE IF NOT EXISTS execution_criteria (
    id TEXT PRIMARY KEY, work_id TEXT NOT NULL REFERENCES execution_contracts(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL, criterion_key TEXT NOT NULL, declaration TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
    evidence TEXT NOT NULL DEFAULT '', checked_by TEXT NOT NULL DEFAULT '', checked_at TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_criteria_key ON execution_criteria(work_id,criterion_key);
CREATE INDEX IF NOT EXISTS idx_execution_criteria_work ON execution_criteria(work_id,seq);

CREATE TABLE IF NOT EXISTS execution_constraints (
    id TEXT PRIMARY KEY, work_id TEXT NOT NULL REFERENCES execution_contracts(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL, constraint_key TEXT NOT NULL, text TEXT NOT NULL, source TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_constraints_key ON execution_constraints(work_id,constraint_key);
CREATE INDEX IF NOT EXISTS idx_execution_constraints_work ON execution_constraints(work_id,seq);

CREATE TABLE IF NOT EXISTS execution_findings (
    id TEXT PRIMARY KEY, work_id TEXT NOT NULL REFERENCES execution_contracts(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL, category TEXT NOT NULL, severity TEXT NOT NULL, description TEXT NOT NULL,
    location TEXT NOT NULL DEFAULT '', evidence TEXT NOT NULL DEFAULT '', origin TEXT NOT NULL, review_phase TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open', backlog_id TEXT NOT NULL DEFAULT '', resolution_reason TEXT NOT NULL DEFAULT '',
    resolved_by TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, resolved_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_execution_findings_work ON execution_findings(work_id,seq);
CREATE INDEX IF NOT EXISTS idx_execution_findings_status ON execution_findings(work_id,status);

CREATE TABLE IF NOT EXISTS execution_history (
    id TEXT PRIMARY KEY, work_id TEXT NOT NULL REFERENCES execution_contracts(id) ON DELETE CASCADE,
    from_status TEXT NOT NULL, to_status TEXT NOT NULL, contract_revision INTEGER NOT NULL,
    by TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '', at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_execution_history_work ON execution_history(work_id,at);

CREATE TABLE IF NOT EXISTS delivery_certificates (
    id TEXT PRIMARY KEY, project TEXT NOT NULL, work_id TEXT NOT NULL REFERENCES execution_contracts(id) ON DELETE CASCADE,
    contract_revision INTEGER NOT NULL, contract_hash TEXT NOT NULL, head_sha TEXT NOT NULL, base_sha TEXT NOT NULL DEFAULT '',
    verdict TEXT NOT NULL, dirty INTEGER NOT NULL DEFAULT 0, evidence TEXT NOT NULL DEFAULT '', mneme_version TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL, finished_at TEXT NOT NULL, duration_ms INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_delivery_certs_work ON delivery_certificates(project,work_id,created_at);

CREATE TABLE IF NOT EXISTS delivery_checks (
    id INTEGER PRIMARY KEY AUTOINCREMENT, certificate_id TEXT NOT NULL REFERENCES delivery_certificates(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL, kind TEXT NOT NULL, name TEXT NOT NULL, status TEXT NOT NULL,
    effect TEXT NOT NULL DEFAULT 'blocks', detail TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0,
    output_sha256 TEXT NOT NULL DEFAULT '', output_tail TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_delivery_checks_cert ON delivery_checks(certificate_id,seq);

ALTER TABLE specs ADD COLUMN execution_model TEXT NOT NULL DEFAULT 'legacy';
