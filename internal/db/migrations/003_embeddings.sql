-- Migration 003: embeddings table for vector similarity search.
-- Stores a pre-computed TF-IDF (or future ML) embedding per memory as a
-- raw float32 BLOB. The memory_id foreign key cascades on delete so embeddings
-- are removed automatically when their parent memory is hard-deleted.

CREATE TABLE IF NOT EXISTS embeddings (
    memory_id   TEXT PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
    vector      BLOB NOT NULL,
    model       TEXT NOT NULL,
    dimensions  INTEGER NOT NULL,
    created_at  TEXT NOT NULL
);

-- Index on model supports efficient queries when detecting stale embeddings
-- after a model upgrade (e.g. during backfill).
CREATE INDEX IF NOT EXISTS idx_embeddings_model ON embeddings(model);
