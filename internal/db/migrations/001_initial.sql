-- 001_initial.sql: Core tables for mneme Phase 1
-- Creates the memories table with FTS5 full-text search, file tracking, and session management.

CREATE TABLE IF NOT EXISTS memories (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,
    scope           TEXT NOT NULL DEFAULT 'project',
    title           TEXT NOT NULL,
    content         TEXT NOT NULL,
    topic_key       TEXT,
    project         TEXT,
    session_id      TEXT,
    created_by      TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    importance      REAL NOT NULL DEFAULT 0.5,
    confidence      REAL NOT NULL DEFAULT 0.8,
    access_count    INTEGER NOT NULL DEFAULT 0,
    last_accessed   TEXT,
    decay_rate      REAL NOT NULL DEFAULT 0.01,
    revision_count  INTEGER NOT NULL DEFAULT 1,
    superseded_by   TEXT,
    deleted_at      TEXT
);

CREATE INDEX IF NOT EXISTS idx_memories_project ON memories(project) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_memories_scope ON memories(scope) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(type) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_memories_topic_key ON memories(topic_key) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_memories_created_at ON memories(created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_memories_importance ON memories(importance DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_memories_superseded ON memories(superseded_by) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_memories_upsert ON memories(topic_key, project, scope) WHERE topic_key IS NOT NULL AND deleted_at IS NULL;

-- FTS5 virtual table for full-text search
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    title,
    content,
    type,
    topic_key,
    content=memories,
    content_rowid=rowid,
    tokenize='porter unicode61'
);

-- Triggers to keep FTS5 synchronized with the memories table
CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts(rowid, title, content, type, topic_key)
    VALUES (new.rowid, new.title, new.content, new.type, new.topic_key);
END;

CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, title, content, type, topic_key)
    VALUES ('delete', old.rowid, old.title, old.content, old.type, old.topic_key);
END;

CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, title, content, type, topic_key)
    VALUES ('delete', old.rowid, old.title, old.content, old.type, old.topic_key);
    INSERT INTO memories_fts(rowid, title, content, type, topic_key)
    VALUES (new.rowid, new.title, new.content, new.type, new.topic_key);
END;

-- File references for memories
CREATE TABLE IF NOT EXISTS memory_files (
    memory_id   TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    file_path   TEXT NOT NULL,
    PRIMARY KEY (memory_id, file_path)
);

CREATE INDEX IF NOT EXISTS idx_memory_files_path ON memory_files(file_path);

-- Session tracking
CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT PRIMARY KEY,
    project     TEXT,
    agent       TEXT,
    started_at  TEXT NOT NULL,
    ended_at    TEXT,
    summary_id  TEXT REFERENCES memories(id)
);

-- Schema version tracking for migrations
CREATE TABLE IF NOT EXISTS schema_version (
    version     INTEGER PRIMARY KEY,
    applied_at  TEXT NOT NULL
);

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (1, datetime('now'));
