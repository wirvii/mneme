-- 020_sdd_previous_ids.sql: previous_ids en backlog_items y specs (SPEC-130
-- D32), la constancia de renumeracion que BL-202 escribira. Nace INERTE:
-- esta migracion solo anade la columna; ninguna escritura de este repo la
-- usa todavia (D32/AC10). Segura de anadir al principio de la rama porque
-- no hay un solo SELECT * en todo internal/ (V11) -- ningun escaner por
-- posicion puede desajustarse.
--
-- Array JSON, mismo patron que assigned_agents/files_changed
-- (store/sdd.go): '' se lee como lista vacia via COALESCE + marshalStringSlice.
ALTER TABLE backlog_items ADD COLUMN previous_ids TEXT NOT NULL DEFAULT '';
ALTER TABLE specs         ADD COLUMN previous_ids TEXT NOT NULL DEFAULT '';

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (20, datetime('now'));
