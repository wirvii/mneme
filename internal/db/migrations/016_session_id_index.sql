-- 016: índice parcial para la consulta agregada por session_id (SPEC-108 D18).
-- La consulta corre en el camino latency-sensitive de SessionStart (el agente
-- espera al hook) y `memories` crece sin techo. El predicado del índice casa
-- con el de sessionWorkWhere (deleted_at IS NULL + session_id no nulo), así que
-- SQLite lo puede usar para el GROUP BY. Solo índice: sin columnas nuevas, sin
-- datos tocados, reaplicable.
CREATE INDEX IF NOT EXISTS idx_memories_session_id
    ON memories(session_id)
    WHERE deleted_at IS NULL AND session_id IS NOT NULL;

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (16, datetime('now'));
