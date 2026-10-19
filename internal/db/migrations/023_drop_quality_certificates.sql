-- 023_drop_quality_certificates.sql: SPEC-158 retires the EPIC-calidad
-- certification mechanism entirely — quality_certificates/quality_checks
-- (introduced by migration 018, extended by 021) are no longer written or
-- read by any production code. This migration is IRREVERSIBLE and carries
-- no backup: an explicit, owner-approved exception to the "nothing
-- irreversible automatic" rule (BL-238), justified by the measurement
-- already saved as the memory proceso/certificacion-6h-medida. Migrations
-- 018 and 021 themselves are NOT deleted — a migration file is immutable
-- history; deleting it would break the numbering of every existing
-- database that already applied it.
--
-- Children before parent; their indexes drop with them. IF EXISTS makes
-- this safe over any base — a fresh database creates the tables in 018
-- and drops them here, at negligible cost, with its history intact.
DROP TABLE IF EXISTS quality_checks;
DROP TABLE IF EXISTS quality_certificates;

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (23, datetime('now'));
