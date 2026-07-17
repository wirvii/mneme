-- 015: columna de proveniencia para memorias inyectadas por un profile (SPEC-092).
-- source = "profile:<name>" cuando la memoria (típicamente una rule) fue
-- materializada por la activación de un profile; "" (default) = hand-authored,
-- intocable por el switch y excluible del vault (la exclusión la cablea §4).
-- Valor inerte por defecto: mem_save sigue persistiendo source='' sin cambio
-- observable.
ALTER TABLE memories ADD COLUMN source TEXT NOT NULL DEFAULT '';

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (15, datetime('now'));
