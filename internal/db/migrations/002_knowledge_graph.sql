-- 002_knowledge_graph.sql: Entity-relationship graph for cross-reference navigation.

CREATE TABLE IF NOT EXISTS entities (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL,
    project     TEXT,
    metadata    TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_entities_name_project ON entities(name, project);
CREATE INDEX IF NOT EXISTS idx_entities_kind ON entities(kind);

CREATE TABLE IF NOT EXISTS relations (
    id          TEXT PRIMARY KEY,
    source_id   TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    target_id   TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,
    weight      REAL NOT NULL DEFAULT 1.0,
    metadata    TEXT,
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_relations_source ON relations(source_id);
CREATE INDEX IF NOT EXISTS idx_relations_target ON relations(target_id);
CREATE INDEX IF NOT EXISTS idx_relations_type ON relations(type);

CREATE TABLE IF NOT EXISTS memory_entities (
    memory_id   TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    entity_id   TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    role        TEXT NOT NULL DEFAULT 'mention',
    PRIMARY KEY (memory_id, entity_id)
);

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (2, datetime('now'));
