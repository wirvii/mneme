-- 019_sdd_anchors.sql: ancla invisible (UUIDv7) para backlog_items y specs,
-- mas el registro de referencias SDD que una memoria menciona (SPEC-128,
-- etapa 1 de BL-194).
--
-- Reversion: ALTER TABLE ADD COLUMN no admite DROP COLUMN portable en SQLite
-- antiguo; revertir a mano borra memory_sdd_refs, sdd_reference_backfill y
-- los dos indices unicos parciales. Las columnas `uuid` se quedan e inertes.
ALTER TABLE backlog_items ADD COLUMN uuid TEXT NOT NULL DEFAULT '';
ALTER TABLE specs         ADD COLUMN uuid TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_backlog_items_uuid
    ON backlog_items(uuid) WHERE uuid <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_specs_uuid
    ON specs(uuid) WHERE uuid <> '';

CREATE TABLE IF NOT EXISTS memory_sdd_refs (
    memory_id   TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    ref_id      TEXT NOT NULL,
    target_uuid TEXT NOT NULL,
    PRIMARY KEY (memory_id, ref_id)
);
CREATE INDEX IF NOT EXISTS idx_memory_sdd_refs_target
    ON memory_sdd_refs(target_uuid);

CREATE TABLE IF NOT EXISTS sdd_reference_backfill (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    completed_at     TEXT,
    memories_scanned INTEGER NOT NULL DEFAULT 0,
    refs_created     INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO sdd_reference_backfill (id) VALUES (1);

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (19, datetime('now'));
