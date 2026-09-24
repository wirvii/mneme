-- 022_spec_history_reviewed_sha.sql: una sola columna aditiva en
-- spec_history que cubre los DOS extremos de una revision de QA (SPEC-157):
-- el extremo ENTREGADO (grabado al entrar en qa) y el extremo REVISADO
-- (copiado del entregado al salir de qa, nunca recalculado de HEAD). Cual de
-- los dos significa una fila se decide por el par (from_status, to_status)
-- de esa misma fila, nunca por el valor solo.
--
-- DEFAULT '' es el comportamiento historico de cada fila escrita antes de
-- que la columna existiera -- la misma forma que la migracion 021 uso para
-- quality_checks.effect.
ALTER TABLE spec_history ADD COLUMN reviewed_sha TEXT NOT NULL DEFAULT '';

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (22, datetime('now'));
