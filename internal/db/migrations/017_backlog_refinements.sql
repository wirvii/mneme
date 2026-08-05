-- 017_backlog_refinements.sql: refinamientos de un ítem de backlog como filas
-- propias (SPEC-110 D2). backlog_items.description deja de crecer: pasa a ser
-- write-once (la escribe backlog_add y nada más, D15).
--
-- Migración CONSERVADORA y solo-DDL (D10): los ítems existentes conservan su
-- description tal cual, como descripción heredada. NO se parte por heurística —
-- el "\n\n" no es delimitador fiable (aparece dentro del texto de los ledgers) y
-- partir texto concatenado es como se corrompen datos. Solo los refinamientos
-- NUEVOS van a filas.
--
-- Sin columna id (a diferencia de spec_history): nada referencia una fila de
-- refinamiento. La PK compuesta (item_id, seq) hace además cumplir la invariante
-- de que no puedan existir dos refinamientos con el mismo seq para un ítem.
--
-- SIN CREATE INDEX (D9): una PK compuesta no-INTEGER crea un sqlite_autoindex
-- UNIQUE sobre (item_id, seq) que sirve las DOS consultas de esta spec — la
-- lectura ordenada por ítem (WHERE item_id = ? ORDER BY seq) y el agregado del
-- contador (GROUP BY item_id, prefijo izquierdo del índice). Un índice extra
-- sobre (item_id) sería un duplicado estricto de ese prefijo: dos B-trees para el
-- mismo acceso y doble coste de escritura en cada append. spec_history sí
-- necesita el suyo porque su PK es un UUID aleatorio que no ordena por nada útil.
--
-- `by` sin comillas: spec_history ya usa ese nombre de columna en INSERT y SELECT
-- (004_sdd.sql:46, store/sdd.go:435) — precedente probado en este mismo motor.
CREATE TABLE IF NOT EXISTS backlog_refinements (
    item_id  TEXT    NOT NULL REFERENCES backlog_items(id) ON DELETE CASCADE,
    seq      INTEGER NOT NULL,
    body     TEXT    NOT NULL,
    by       TEXT    NOT NULL DEFAULT '',
    at       TEXT    NOT NULL,
    PRIMARY KEY (item_id, seq)
);

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (17, datetime('now'));
