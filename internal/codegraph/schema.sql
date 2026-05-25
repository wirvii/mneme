-- schema.sql is the complete SQLite schema for the codegraph database.
-- It is embedded into the binary via go:embed and executed on every OpenDB
-- call. All statements use IF NOT EXISTS / INSERT OR IGNORE so the file is
-- safe to run on an already-initialised database (idempotent).

-- schema_versions tracks which version of the schema has been applied.
-- Unlike the main mneme DB (which uses a versioned migration sequence),
-- the codegraph schema is a single-file baseline: version 1 is written on
-- first open and the table is never downgraded.
CREATE TABLE IF NOT EXISTS schema_versions (
    version    INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL,
    description TEXT
);
INSERT OR IGNORE INTO schema_versions (version, applied_at, description)
VALUES (1, strftime('%s', 'now') * 1000, 'Initial schema');

-- nodes stores one row per named code symbol extracted from a source file.
-- The primary key is a deterministic 16-hex-char ID computed from the file
-- path and the fully-qualified symbol name (see NodeID in model.go).
CREATE TABLE IF NOT EXISTS nodes (
    id             TEXT    PRIMARY KEY,
    kind           TEXT    NOT NULL,
    name           TEXT    NOT NULL,
    qualified_name TEXT    NOT NULL,
    file_path      TEXT    NOT NULL,
    language       TEXT    NOT NULL,
    start_line     INTEGER NOT NULL,
    end_line       INTEGER NOT NULL,
    start_column   INTEGER NOT NULL,
    end_column     INTEGER NOT NULL,
    docstring      TEXT,
    signature      TEXT,
    visibility     TEXT,
    is_exported    INTEGER DEFAULT 0,
    is_async       INTEGER DEFAULT 0,
    is_static      INTEGER DEFAULT 0,
    is_abstract    INTEGER DEFAULT 0,
    decorators     TEXT,
    type_parameters TEXT,
    updated_at     INTEGER NOT NULL
);

-- edges stores directed relationships between nodes. The (source, target, kind)
-- triple is not enforced as UNIQUE at the DB level to allow fast bulk inserts;
-- the store layer de-duplicates on upsert.
CREATE TABLE IF NOT EXISTS edges (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    source     TEXT    NOT NULL,
    target     TEXT    NOT NULL,
    kind       TEXT    NOT NULL,
    metadata   TEXT,
    line       INTEGER,
    col        INTEGER,
    provenance TEXT    DEFAULT NULL,
    FOREIGN KEY (source) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (target) REFERENCES nodes(id) ON DELETE CASCADE
);

-- files records the indexing state of each tracked source file. The content
-- hash enables incremental re-indexing: files whose hash has not changed can
-- be skipped without re-parsing.
CREATE TABLE IF NOT EXISTS files (
    path        TEXT    PRIMARY KEY,
    content_hash TEXT   NOT NULL,
    language    TEXT    NOT NULL,
    size        INTEGER NOT NULL,
    modified_at INTEGER NOT NULL,
    indexed_at  INTEGER NOT NULL,
    node_count  INTEGER DEFAULT 0,
    errors      TEXT
);

-- unresolved_refs captures references to symbols that could not be resolved
-- during extraction. A second-pass resolution phase can promote these to real
-- edges once all files in the project have been indexed.
CREATE TABLE IF NOT EXISTS unresolved_refs (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    from_node_id   TEXT    NOT NULL,
    reference_name TEXT    NOT NULL,
    reference_kind TEXT    NOT NULL,
    line           INTEGER NOT NULL,
    col            INTEGER NOT NULL,
    candidates     TEXT,
    file_path      TEXT    NOT NULL DEFAULT '',
    language       TEXT    NOT NULL DEFAULT 'unknown',
    FOREIGN KEY (from_node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

-- project_metadata is a generic key/value store for per-project settings and
-- state flags (e.g. last indexed commit, root path, language list).
CREATE TABLE IF NOT EXISTS project_metadata (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
);

-- nodes_fts is an FTS5 content table backed by nodes. It enables full-text
-- symbol search over name, qualified_name, docstring, and signature.
-- The content/content_rowid directives keep the FTS index in sync with nodes
-- via the triggers below; they do not duplicate storage.
CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
    id, name, qualified_name, docstring, signature,
    content='nodes', content_rowid='rowid'
);

-- Trigger: keep nodes_fts in sync after INSERT on nodes.
CREATE TRIGGER IF NOT EXISTS nodes_ai AFTER INSERT ON nodes BEGIN
    INSERT INTO nodes_fts(rowid, id, name, qualified_name, docstring, signature)
    VALUES (NEW.rowid, NEW.id, NEW.name, NEW.qualified_name, NEW.docstring, NEW.signature);
END;

-- Trigger: keep nodes_fts in sync after DELETE on nodes.
CREATE TRIGGER IF NOT EXISTS nodes_ad AFTER DELETE ON nodes BEGIN
    INSERT INTO nodes_fts(nodes_fts, rowid, id, name, qualified_name, docstring, signature)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.name, OLD.qualified_name, OLD.docstring, OLD.signature);
END;

-- Trigger: keep nodes_fts in sync after UPDATE on nodes (delete old, insert new).
CREATE TRIGGER IF NOT EXISTS nodes_au AFTER UPDATE ON nodes BEGIN
    INSERT INTO nodes_fts(nodes_fts, rowid, id, name, qualified_name, docstring, signature)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.name, OLD.qualified_name, OLD.docstring, OLD.signature);
    INSERT INTO nodes_fts(rowid, id, name, qualified_name, docstring, signature)
    VALUES (NEW.rowid, NEW.id, NEW.name, NEW.qualified_name, NEW.docstring, NEW.signature);
END;

-- Indexes on nodes for common query patterns.
CREATE INDEX IF NOT EXISTS idx_nodes_kind           ON nodes(kind);
CREATE INDEX IF NOT EXISTS idx_nodes_name           ON nodes(name);
CREATE INDEX IF NOT EXISTS idx_nodes_qualified_name ON nodes(qualified_name);
CREATE INDEX IF NOT EXISTS idx_nodes_file_path      ON nodes(file_path);
CREATE INDEX IF NOT EXISTS idx_nodes_language       ON nodes(language);
CREATE INDEX IF NOT EXISTS idx_nodes_file_line      ON nodes(file_path, start_line);
CREATE INDEX IF NOT EXISTS idx_nodes_lower_name     ON nodes(lower(name));

-- Indexes on edges for traversal and provenance queries.
CREATE INDEX IF NOT EXISTS idx_edges_kind          ON edges(kind);
CREATE INDEX IF NOT EXISTS idx_edges_source_kind   ON edges(source, kind);
CREATE INDEX IF NOT EXISTS idx_edges_target_kind   ON edges(target, kind);
CREATE INDEX IF NOT EXISTS idx_edges_provenance    ON edges(provenance);

-- Indexes on files for language-filtered and recency-based queries.
CREATE INDEX IF NOT EXISTS idx_files_language    ON files(language);
CREATE INDEX IF NOT EXISTS idx_files_modified_at ON files(modified_at);

-- Indexes on unresolved_refs for resolution pass queries.
CREATE INDEX IF NOT EXISTS idx_unresolved_from_node  ON unresolved_refs(from_node_id);
CREATE INDEX IF NOT EXISTS idx_unresolved_name       ON unresolved_refs(reference_name);
CREATE INDEX IF NOT EXISTS idx_unresolved_file_path  ON unresolved_refs(file_path);
CREATE INDEX IF NOT EXISTS idx_unresolved_from_name  ON unresolved_refs(from_node_id, reference_name);
