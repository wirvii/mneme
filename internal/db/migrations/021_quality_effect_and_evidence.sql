-- 021_quality_effect_and_evidence.sql: dos ALTER TABLE aditivos (SPEC-137
-- etapa 1 de BL-221, D4/D6).
--
-- quality_checks.effect: el vocabulario cerrado de cinco valores
-- (blocks/signable/measures/absent/stopped) que dice si una fila cuenta
-- para el veredicto. DEFAULT 'blocks' es el comportamiento historico de
-- TODA fila ya emitida antes de esta spec -- es lo que impide que un
-- certificado antiguo se reetiquete solo porque el codigo nuevo aterrizo.
--
-- quality_certificates.evidence: la frase de D6 ("de que es evidencia este
-- certificado"), persistida UNA SOLA VEZ al emitir. DEFAULT '' para todo lo
-- ya emitido -- esa vacuidad es lo que los tres canales (verify/status/
-- informe) traducen a "certificado emitido antes de esta version: sin
-- linea de evidencia", nunca una frase fabricada a posteriori.
ALTER TABLE quality_checks       ADD COLUMN effect   TEXT NOT NULL DEFAULT 'blocks';
ALTER TABLE quality_certificates ADD COLUMN evidence TEXT NOT NULL DEFAULT '';

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (21, datetime('now'));
