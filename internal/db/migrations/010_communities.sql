-- 010_communities.sql: Persist detected communities from Louvain.
-- Part of EPIC-5 (SPEC-020): community tables for GraphRAG pipeline.
--
-- Communities are detected by the Louvain algorithm (SPEC-019) and persisted
-- here for incremental updates, synthesis generation (SPEC-C3), and context
-- packing (SPEC-C4).
--
-- Rollback:
--   DROP TABLE IF EXISTS community_members;
--   DROP TABLE IF EXISTS communities;

CREATE TABLE IF NOT EXISTS communities (
    id                TEXT PRIMARY KEY,
    project           TEXT,
    scope             TEXT NOT NULL DEFAULT 'project',
    membership_hash   TEXT NOT NULL,
    member_count      INTEGER NOT NULL,
    modularity        REAL NOT NULL DEFAULT 0.0,
    label             TEXT,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

-- Diff lookup: find existing community by (project, membership_hash).
-- UNIQUE enforces one community per member-set per project.
CREATE UNIQUE INDEX IF NOT EXISTS idx_communities_project_hash
    ON communities(project, membership_hash);

-- List communities by project, ordered by size descending.
CREATE INDEX IF NOT EXISTS idx_communities_project_size
    ON communities(project, member_count DESC);

CREATE TABLE IF NOT EXISTS community_members (
    community_id  TEXT NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    entity_id     TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    PRIMARY KEY (community_id, entity_id)
);

-- Reverse lookup: which communities contain this entity?
CREATE INDEX IF NOT EXISTS idx_community_members_entity
    ON community_members(entity_id);

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (10, datetime('now'));
