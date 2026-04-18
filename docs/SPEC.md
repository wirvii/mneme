# mneme -- Technical Specification

**Version:** 0.1.0-draft
**Date:** 2026-04-13
**Author:** Architect Agent (spec), Juan F. Tamayo (requirements)

---

## 1. Vision and Objectives

### Problem

AI coding agents (Claude Code, Gemini CLI, OpenCode, Codex, Cursor) lose all context between sessions. CLAUDE.md files become battlegrounds mixing agent config, team conventions, architecture docs, and personal preferences. Knowledge learned in one project is invisible in another.

### What mneme solves

mneme is a **persistent memory system for AI coding agents**. It provides:

1. **Separation of concerns** -- Memory lives in a structured database, not in CLAUDE.md. The agent's system prompt tells it *how to behave*; mneme tells it *what it knows*.
2. **Cross-project intelligence** -- Patterns, preferences, and skills learned in project A are available in project B.
3. **Hybrid retrieval** -- Exact search (function names, paths) and semantic search ("how do we handle auth") in a single query.
4. **Automatic consolidation** -- Old memories decay, contradictions merge, duplicates collapse. No manual curation needed.
5. **Agent-agnostic interface** -- MCP (stdio) as the primary protocol; any agent that speaks MCP can use mneme.

### Non-goals

- mneme is NOT an agent installer or configurator.
- mneme is NOT a vector database -- it uses vectors as one retrieval signal among three.
- mneme does NOT replace version control or documentation -- it complements them as working memory.
- mneme does NOT store code -- it stores *knowledge about* code.

### Target users

Solo developers and small teams using AI coding agents across multiple projects. Primary persona: a developer who uses Claude Code daily across 3-10 active projects and wants persistent cross-session, cross-project memory.

---

## 2. Architecture Overview

```
+------------------------------------------------------------------+
|                         mneme binary                             |
|                                                                  |
|  +------------+  +------------+  +------------+  +------------+  |
|  |  CLI layer |  | MCP server |  | HTTP API   |  |  TUI       |  |
|  |  (cobra)   |  | (stdio)    |  | (optional) |  | (bubbletea)|  |
|  +-----+------+  +-----+------+  +-----+------+  +-----+------+  |
|        |               |               |               |         |
|  +-----v---------------v---------------v---------------v------+  |
|  |                    Service Layer                            |  |
|  |  +-----------+ +-----------+ +-----------+ +-----------+   |  |
|  |  | MemoryService| | SearchSvc | | ConsolSvc | | SyncService|   |  |
|  |  +-----------+ +-----------+ +-----------+ +-----------+   |  |
|  +--------------------+--------------------+------------------+  |
|                       |                    |                     |
|  +--------------------v--------------------v------------------+  |
|  |                   Storage Layer                             |  |
|  |  +----------+ +----------+ +----------+ +--------------+   |  |
|  |  | SQLite   | | FTS5     | | VSS/Vec  | | Knowledge    |   |  |
|  |  | (core)   | | (search) | | (embed)  | | Graph        |   |  |
|  |  +----------+ +----------+ +----------+ +--------------+   |  |
|  +-------------------------------------------------------------+  |
|                                                                  |
|  +-------------------------------------------------------------+  |
|  |                   Infrastructure                             |  |
|  |  +----------+ +----------+ +----------+ +--------------+   |  |
|  |  | Config   | | Project  | | Embedder | | Git Sync     |   |  |
|  |  | (TOML)   | | Detector | | (local)  | | (JSONL.gz)   |   |  |
|  |  +----------+ +----------+ +----------+ +--------------+   |  |
|  +-------------------------------------------------------------+  |
+------------------------------------------------------------------+

Data flow (MCP request):

  Agent ──stdio──> MCP Server ──> Service Layer ──> Storage Layer ──> SQLite
                                       |
                                       +──> Embedder (if vector search)
                                       +──> Graph (if relation query)
```

### Key architectural decisions

1. **Single SQLite database per scope** -- `~/.mneme/global.db` for global/org memories, `~/.mneme/projects/{project-slug}.db` for project-specific memories. This avoids cross-project query contamination and makes backup/sync per-project.

2. **CGO required** -- `mattn/go-sqlite3` with FTS5 and JSON1 extensions. Pure-Go SQLite alternatives do not support FTS5 reliably.

3. **Embeddings are optional** -- Phase 1 works with FTS5 only. Phase 2 adds vector search. The retrieval pipeline gracefully degrades when embeddings are unavailable.

4. **MCP is the primary interface** -- The CLI is for human administration. Agents interact exclusively through MCP tools over stdio.

---

## 3. Data Model

### 3.1 SQLite Schema -- Core tables (Phase 1)

```sql
-- memories: the central fact store
CREATE TABLE memories (
    id              TEXT PRIMARY KEY,  -- UUIDv7 (time-sortable)
    type            TEXT NOT NULL,     -- decision|discovery|bugfix|pattern|preference|convention|architecture|config|session_summary
    scope           TEXT NOT NULL DEFAULT 'project',  -- global|org|project
    title           TEXT NOT NULL,     -- short, searchable summary
    content         TEXT NOT NULL,     -- structured markdown (What/Why/Where/Learned)
    topic_key       TEXT,              -- stable key for upserts (e.g. "architecture/auth-model")
    project         TEXT,              -- normalized project slug (NULL for global scope)
    session_id      TEXT,              -- session that created this memory
    created_by      TEXT,              -- agent identifier (e.g. "claude-code", "user")
    created_at      TEXT NOT NULL,     -- ISO 8601
    updated_at      TEXT NOT NULL,     -- ISO 8601
    importance      REAL NOT NULL DEFAULT 0.5,  -- 0.0-1.0
    confidence      REAL NOT NULL DEFAULT 0.8,  -- 0.0-1.0
    access_count    INTEGER NOT NULL DEFAULT 0,
    last_accessed   TEXT,              -- ISO 8601
    decay_rate      REAL NOT NULL DEFAULT 0.01, -- per-day decay multiplier
    revision_count  INTEGER NOT NULL DEFAULT 1,
    superseded_by   TEXT,              -- ID of memory that replaced this one (NULL if current)
    deleted_at      TEXT               -- soft delete (ISO 8601, NULL if active)
);

-- Indexes for common query patterns
CREATE INDEX idx_memories_project        ON memories(project) WHERE deleted_at IS NULL;
CREATE INDEX idx_memories_scope          ON memories(scope) WHERE deleted_at IS NULL;
CREATE INDEX idx_memories_type           ON memories(type) WHERE deleted_at IS NULL;
CREATE INDEX idx_memories_topic_key      ON memories(topic_key) WHERE deleted_at IS NULL;
CREATE INDEX idx_memories_created_at     ON memories(created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_memories_importance     ON memories(importance DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_memories_superseded     ON memories(superseded_by) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX idx_memories_upsert  ON memories(topic_key, project, scope) WHERE topic_key IS NOT NULL AND deleted_at IS NULL;

-- FTS5 virtual table for full-text search
CREATE VIRTUAL TABLE memories_fts USING fts5(
    title,
    content,
    type,
    topic_key,
    content=memories,
    content_rowid=rowid,
    tokenize='porter unicode61'
);

-- Triggers to keep FTS5 in sync
CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts(rowid, title, content, type, topic_key)
    VALUES (new.rowid, new.title, new.content, new.type, new.topic_key);
END;

CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, title, content, type, topic_key)
    VALUES ('delete', old.rowid, old.title, old.content, old.type, old.topic_key);
END;

CREATE TRIGGER memories_au AFTER UPDATE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, title, content, type, topic_key)
    VALUES ('delete', old.rowid, old.title, old.content, old.type, old.topic_key);
    INSERT INTO memories_fts(rowid, title, content, type, topic_key)
    VALUES (new.rowid, new.title, new.content, new.type, new.topic_key);
END;

-- files: tracks which files a memory references
CREATE TABLE memory_files (
    memory_id   TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    file_path   TEXT NOT NULL,
    PRIMARY KEY (memory_id, file_path)
);

CREATE INDEX idx_memory_files_path ON memory_files(file_path);

-- sessions: tracks agent sessions for grouping and summaries
CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,  -- session identifier
    project     TEXT,
    agent       TEXT,              -- which agent (claude-code, gemini-cli, etc.)
    started_at  TEXT NOT NULL,
    ended_at    TEXT,
    summary_id  TEXT REFERENCES memories(id)  -- link to session_summary memory
);

-- schema_version: for migrations
CREATE TABLE schema_version (
    version     INTEGER PRIMARY KEY,
    applied_at  TEXT NOT NULL
);
```

### 3.2 Knowledge Graph tables (Phase 2)

```sql
-- entities: nodes in the knowledge graph
CREATE TABLE entities (
    id          TEXT PRIMARY KEY,  -- UUIDv7
    name        TEXT NOT NULL,     -- normalized name (e.g. "auth-middleware")
    kind        TEXT NOT NULL,     -- module|service|library|concept|person|pattern|file
    project     TEXT,              -- NULL for cross-project entities
    metadata    TEXT,              -- JSON blob for extra attributes
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_entities_name_project ON entities(name, project);
CREATE INDEX idx_entities_kind ON entities(kind);

-- relations: edges in the knowledge graph
CREATE TABLE relations (
    id          TEXT PRIMARY KEY,
    source_id   TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    target_id   TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,     -- depends_on|implements|supersedes|related_to|part_of|uses|conflicts_with
    weight      REAL NOT NULL DEFAULT 1.0,
    metadata    TEXT,              -- JSON blob
    created_at  TEXT NOT NULL
);

CREATE INDEX idx_relations_source ON relations(source_id);
CREATE INDEX idx_relations_target ON relations(target_id);
CREATE INDEX idx_relations_type   ON relations(type);

-- memory_entities: junction table linking memories to entities
CREATE TABLE memory_entities (
    memory_id   TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    entity_id   TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    role        TEXT NOT NULL DEFAULT 'mention',  -- subject|object|mention
    PRIMARY KEY (memory_id, entity_id)
);
```

### 3.3 Embeddings table (Phase 2)

```sql
-- embeddings: vector representations for semantic search
CREATE TABLE embeddings (
    memory_id   TEXT PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
    vector      BLOB NOT NULL,    -- float32 array, serialized
    model       TEXT NOT NULL,    -- embedding model identifier
    dimensions  INTEGER NOT NULL, -- vector dimension count
    created_at  TEXT NOT NULL
);
```

Vector search uses brute-force cosine similarity over the embeddings table. For datasets under 100K memories (the expected scale), this is fast enough without an ANN index. If scale demands it, Phase 2 can add SQLite-VSS or an in-memory HNSW index.

### 3.4 UUIDv7

All IDs use UUIDv7 (RFC 9562) for time-sortable, globally unique identifiers. The timestamp prefix enables efficient range queries and natural chronological ordering.

Implementation: use `github.com/gofrs/uuid/v5` with `uuid.NewV7()`.

---

## 4. Go Package Structure

```
mneme/
  cmd/
    mneme/
      main.go              -- entrypoint, cobra root command setup
  internal/
    cli/                   -- cobra command definitions
      root.go              -- root command, global flags
      save.go              -- mneme save
      search.go            -- mneme search
      get.go               -- mneme get
      status.go            -- mneme status
      sync.go              -- mneme sync (Phase 2)
      forget.go            -- mneme forget (Phase 3)
      stats.go             -- mneme stats (Phase 3)
      mcp.go               -- mneme mcp (launches MCP server)
      serve.go             -- mneme serve (HTTP API, Phase 4)
    config/                -- configuration loading
      config.go            -- TOML parsing, defaults, env var overlay
      config_test.go
    db/                    -- database layer
      db.go                -- open/close/migrate, connection pool
      migrate.go           -- schema migrations (numbered SQL files)
      migrate_test.go
    store/                 -- data access layer (repository pattern)
      memory.go            -- CRUD for memories table
      memory_test.go
      search.go            -- FTS5 queries
      search_test.go
      session.go           -- session tracking
      entity.go            -- entity CRUD (Phase 2)
      relation.go          -- relation CRUD (Phase 2)
      embedding.go         -- vector storage (Phase 2)
    service/               -- business logic
      memory.go            -- save, get, upsert logic, importance scoring
      memory_test.go
      search.go            -- hybrid retrieval pipeline, RRF fusion
      search_test.go
      context.go           -- mem_context: project-aware context assembly
      context_test.go
      consolidation.go     -- decay, dedup, merge, eviction (Phase 3)
      session.go           -- session lifecycle, summaries
      graph.go             -- graph traversal, relation management (Phase 2)
      sync.go              -- git sync export/import (Phase 2)
    mcp/                   -- MCP server implementation
      server.go            -- stdio transport, JSON-RPC dispatch
      server_test.go
      tools.go             -- tool registration and schemas
      handlers.go          -- tool handler implementations
      handlers_test.go
      protocol.go          -- MCP protocol types (requests, responses)
    embed/                 -- embedding generation (Phase 2)
      embedder.go          -- interface + local embedding impl
      embedder_test.go
    project/               -- project detection
      detect.go            -- git remote parsing, slug normalization
      detect_test.go
    model/                 -- domain types
      memory.go            -- Memory, MemoryType, Scope, ContentBlock
      entity.go            -- Entity, Relation, EntityKind
      search.go            -- SearchQuery, SearchResult, SearchOptions
      session.go           -- Session
    scoring/               -- importance and relevance scoring
      importance.go        -- initial importance assignment
      decay.go             -- time-based decay calculation
      relevance.go         -- query-result relevance scoring
      rrf.go               -- Reciprocal Rank Fusion
    http/                  -- HTTP API (Phase 4)
      server.go
      handlers.go
    tui/                   -- TUI interface (Phase 4)
      app.go
      screens/
  testdata/                -- test fixtures
  docs/
    SPEC.md                -- this file
```

### Package dependency rules

```
cmd/mneme --> internal/cli
internal/cli --> internal/config, internal/service, internal/mcp
internal/mcp --> internal/service, internal/model
internal/service --> internal/store, internal/model, internal/scoring, internal/embed, internal/project
internal/store --> internal/db, internal/model
internal/scoring --> internal/model
internal/embed --> internal/model
internal/db --> (mattn/go-sqlite3)
internal/model --> (no internal deps)
```

No circular dependencies. `internal/model` is the leaf package. `internal/store` never imports `internal/service`. `internal/mcp` never imports `internal/cli`.

---

## 5. MCP Protocol -- Tool Definitions

mneme exposes MCP tools over stdio using JSON-RPC 2.0. The MCP server is started with `mneme mcp`.

### 5.1 `mem_save` -- Save a memory

```json
{
  "name": "mem_save",
  "description": "Save a structured observation to persistent memory. Use for decisions, discoveries, bugfixes, patterns, conventions, and architecture knowledge. If topic_key matches an existing memory in the same project+scope, it performs an upsert (overwrite).",
  "inputSchema": {
    "type": "object",
    "required": ["title", "content"],
    "properties": {
      "title": {
        "type": "string",
        "description": "Short, searchable summary. Use 'Verb + what' format (e.g. 'Fixed N+1 query in UserList', 'Decided on JWT for auth')."
      },
      "content": {
        "type": "string",
        "description": "Structured content with What/Why/Where/Learned sections in markdown."
      },
      "type": {
        "type": "string",
        "enum": ["decision", "discovery", "bugfix", "pattern", "preference", "convention", "architecture", "config", "session_summary"],
        "default": "discovery",
        "description": "Memory classification type."
      },
      "scope": {
        "type": "string",
        "enum": ["global", "org", "project"],
        "default": "project",
        "description": "Visibility scope. 'global' for personal preferences, 'org' for team conventions, 'project' for project-specific knowledge."
      },
      "topic_key": {
        "type": "string",
        "description": "Stable key for upsert behavior. Same topic_key + project + scope = overwrite. Use for evolving topics (e.g. 'architecture/auth-model')."
      },
      "project": {
        "type": "string",
        "description": "Project slug override. Defaults to auto-detected project from git remote."
      },
      "session_id": {
        "type": "string",
        "description": "Session identifier for grouping related memories."
      },
      "files": {
        "type": "array",
        "items": { "type": "string" },
        "description": "File paths referenced by this memory."
      },
      "importance": {
        "type": "number",
        "minimum": 0.0,
        "maximum": 1.0,
        "description": "Override auto-calculated importance (0.0-1.0)."
      }
    }
  }
}
```

**Response:**
```json
{
  "id": "019530a1-7e2f-7000-8000-abcdef123456",
  "action": "created",       // or "updated" for upsert
  "revision_count": 1,       // increments on upsert
  "title": "Fixed N+1 query in UserList",
  "topic_key": null
}
```

### 5.2 `mem_search` -- Hybrid search

```json
{
  "name": "mem_search",
  "description": "Search persistent memory using full-text search (BM25). Returns truncated previews. Use mem_get for full content of specific memories.",
  "inputSchema": {
    "type": "object",
    "required": ["query"],
    "properties": {
      "query": {
        "type": "string",
        "description": "Search query. Supports natural language and exact terms."
      },
      "project": {
        "type": "string",
        "description": "Filter to specific project. Defaults to current project. Use '*' for cross-project search."
      },
      "scope": {
        "type": "string",
        "enum": ["global", "org", "project", "all"],
        "default": "all",
        "description": "Filter by scope. 'all' searches global + project."
      },
      "type": {
        "type": "string",
        "enum": ["decision", "discovery", "bugfix", "pattern", "preference", "convention", "architecture", "config", "session_summary"],
        "description": "Filter by memory type."
      },
      "limit": {
        "type": "integer",
        "default": 10,
        "minimum": 1,
        "maximum": 50,
        "description": "Maximum number of results."
      },
      "include_superseded": {
        "type": "boolean",
        "default": false,
        "description": "Include memories that have been superseded by newer versions."
      }
    }
  }
}
```

**Response:**
```json
{
  "results": [
    {
      "id": "019530a1-7e2f-7000-8000-abcdef123456",
      "title": "Fixed N+1 query in UserList",
      "type": "bugfix",
      "scope": "project",
      "project": "confio-pagos/platform",
      "topic_key": null,
      "preview": "## What\nFixed N+1 query in UserList component by adding DataLoader...",
      "importance": 0.85,
      "created_at": "2026-04-13T10:00:00Z",
      "updated_at": "2026-04-13T10:00:00Z",
      "relevance_score": 0.92
    }
  ],
  "total": 1,
  "query": "N+1 query"
}
```

The `preview` field is truncated to 300 characters. Use `mem_get` for full content.

### 5.3 `mem_get` -- Get full memory content

```json
{
  "name": "mem_get",
  "description": "Retrieve the full, untruncated content of a memory by ID. Use after mem_search to get complete content.",
  "inputSchema": {
    "type": "object",
    "required": ["id"],
    "properties": {
      "id": {
        "type": "string",
        "description": "Memory UUID."
      }
    }
  }
}
```

**Response:**
```json
{
  "id": "019530a1-7e2f-7000-8000-abcdef123456",
  "title": "Fixed N+1 query in UserList",
  "type": "bugfix",
  "scope": "project",
  "project": "confio-pagos/platform",
  "topic_key": null,
  "content": "## What\nFixed N+1 query in UserList component by adding DataLoader pattern.\n\n## Why\nPage load time was 3.2s due to 47 individual SQL queries.\n\n## Where\n- src/components/UserList.tsx\n- src/api/users.ts\n\n## Learned\nDataLoader batches within a single tick of the event loop. Must await all promises in the same microtask for batching to work.",
  "files": ["src/components/UserList.tsx", "src/api/users.ts"],
  "importance": 0.85,
  "confidence": 0.92,
  "access_count": 6,
  "revision_count": 1,
  "created_by": "claude-code",
  "created_at": "2026-04-13T10:00:00Z",
  "updated_at": "2026-04-13T10:00:00Z",
  "last_accessed": "2026-04-13T14:30:00Z",
  "session_id": "sess-abc123"
}
```

Side effect: increments `access_count` and updates `last_accessed`.

### 5.4 `mem_context` -- Project context assembly

```json
{
  "name": "mem_context",
  "description": "Get the most relevant memories for the current project context. Returns a curated set of high-importance memories ordered by relevance. Use at session start or when the agent needs project orientation.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "project": {
        "type": "string",
        "description": "Project slug. Defaults to auto-detected."
      },
      "budget": {
        "type": "integer",
        "default": 4000,
        "description": "Approximate token budget for the context payload."
      },
      "focus": {
        "type": "string",
        "description": "Optional focus area to bias retrieval (e.g. 'authentication', 'database layer')."
      }
    }
  }
}
```

**Response:**
```json
{
  "project": "confio-pagos/platform",
  "memories": [
    {
      "id": "...",
      "title": "Project uses NestJS + Prisma + PostgreSQL",
      "type": "architecture",
      "content": "...",
      "importance": 0.95
    }
  ],
  "token_estimate": 3200,
  "total_available": 47,
  "included": 12,
  "last_session": {
    "id": "sess-abc123",
    "summary": "Worked on payment refund flow...",
    "ended_at": "2026-04-12T18:00:00Z"
  }
}
```

Context assembly algorithm:
1. Fetch the most recent session summary for the project.
2. Fetch all active (non-superseded, non-deleted) memories for the project, ordered by `importance * recency_factor`.
3. Include all global-scope memories with importance > 0.7.
4. Pack memories into the token budget, highest scored first.
5. If `focus` is provided, boost memories that FTS-match the focus term.

### 5.5 `mem_update` -- Update existing memory

```json
{
  "name": "mem_update",
  "description": "Update an existing memory by ID. Increments revision_count.",
  "inputSchema": {
    "type": "object",
    "required": ["id"],
    "properties": {
      "id": {
        "type": "string",
        "description": "Memory UUID to update."
      },
      "title": { "type": "string" },
      "content": { "type": "string" },
      "type": { "type": "string", "enum": ["decision", "discovery", "bugfix", "pattern", "preference", "convention", "architecture", "config", "session_summary"] },
      "importance": { "type": "number", "minimum": 0.0, "maximum": 1.0 },
      "confidence": { "type": "number", "minimum": 0.0, "maximum": 1.0 },
      "files": { "type": "array", "items": { "type": "string" } }
    }
  }
}
```

**Response:** Same shape as `mem_save` response with `"action": "updated"`.

### 5.6 `mem_session_end` -- End session with summary

```json
{
  "name": "mem_session_end",
  "description": "End the current session and save a session summary to memory. MANDATORY before ending a conversation. The summary enables the next session to pick up where this one left off.",
  "inputSchema": {
    "type": "object",
    "required": ["summary"],
    "properties": {
      "summary": {
        "type": "string",
        "description": "Session summary in markdown with sections: Goal, Instructions, Discoveries, Accomplished, Next Steps, Relevant Files."
      },
      "session_id": {
        "type": "string",
        "description": "Session identifier. Defaults to current session."
      },
      "project": {
        "type": "string",
        "description": "Project slug. Defaults to auto-detected."
      }
    }
  }
}
```

**Response:**
```json
{
  "session_id": "sess-abc123",
  "summary_memory_id": "019530a1-...",
  "memories_created": 1,
  "session_duration": "2h15m"
}
```

### 5.7 `mem_relate` -- Create entity relation (Phase 2)

```json
{
  "name": "mem_relate",
  "description": "Create or update a relationship between two entities in the knowledge graph.",
  "inputSchema": {
    "type": "object",
    "required": ["source", "target", "relation"],
    "properties": {
      "source": {
        "type": "string",
        "description": "Source entity name (auto-created if not exists)."
      },
      "target": {
        "type": "string",
        "description": "Target entity name (auto-created if not exists)."
      },
      "relation": {
        "type": "string",
        "enum": ["depends_on", "implements", "supersedes", "related_to", "part_of", "uses", "conflicts_with"],
        "description": "Relationship type."
      },
      "source_kind": {
        "type": "string",
        "enum": ["module", "service", "library", "concept", "person", "pattern", "file"],
        "default": "concept"
      },
      "target_kind": {
        "type": "string",
        "enum": ["module", "service", "library", "concept", "person", "pattern", "file"],
        "default": "concept"
      },
      "project": { "type": "string" }
    }
  }
}
```

### 5.8 `mem_timeline` -- Chronological neighborhood (Phase 2)

```json
{
  "name": "mem_timeline",
  "description": "Get memories around a specific point in time or memory, ordered chronologically.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "around": {
        "type": "string",
        "description": "Memory ID or ISO 8601 timestamp to center the timeline on."
      },
      "project": { "type": "string" },
      "window": {
        "type": "string",
        "default": "7d",
        "description": "Time window (e.g. '24h', '7d', '30d')."
      },
      "limit": {
        "type": "integer",
        "default": 20
      }
    }
  }
}
```

### 5.9 `mem_forget` -- Accelerated decay (Phase 3)

```json
{
  "name": "mem_forget",
  "description": "Mark a memory for accelerated decay. It will be evicted sooner during consolidation. Does not delete immediately.",
  "inputSchema": {
    "type": "object",
    "required": ["id"],
    "properties": {
      "id": {
        "type": "string",
        "description": "Memory UUID to mark for accelerated decay."
      },
      "reason": {
        "type": "string",
        "description": "Why this memory should be forgotten (e.g. 'outdated', 'incorrect', 'superseded')."
      }
    }
  }
}
```

### 5.10 `mem_stats` -- Memory statistics (Phase 3)

```json
{
  "name": "mem_stats",
  "description": "Get statistics about the memory store.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "project": {
        "type": "string",
        "description": "Project slug. Omit for global stats."
      }
    }
  }
}
```

**Response:**
```json
{
  "project": "confio-pagos/platform",
  "total_memories": 142,
  "by_type": {
    "decision": 23,
    "discovery": 45,
    "bugfix": 18,
    "pattern": 12,
    "convention": 8,
    "architecture": 15,
    "session_summary": 21
  },
  "by_scope": {
    "global": 15,
    "project": 127
  },
  "active": 130,
  "superseded": 8,
  "forgotten": 4,
  "db_size_bytes": 2457600,
  "oldest_memory": "2026-01-15T08:00:00Z",
  "newest_memory": "2026-04-13T14:30:00Z",
  "avg_importance": 0.68
}
```

### 5.11 `mem_suggest_topic_key` -- Topic key suggestion

```json
{
  "name": "mem_suggest_topic_key",
  "description": "Suggest a topic_key for a new memory based on existing topic keys. Helps avoid duplicates and maintain consistent naming.",
  "inputSchema": {
    "type": "object",
    "required": ["title"],
    "properties": {
      "title": {
        "type": "string",
        "description": "The title of the memory you want to save."
      },
      "project": { "type": "string" }
    }
  }
}
```

**Response:**
```json
{
  "suggestion": "architecture/auth-model",
  "existing_matches": [
    {
      "topic_key": "architecture/auth-middleware",
      "title": "Auth middleware uses JWT verification",
      "id": "..."
    }
  ],
  "is_new_topic": true
}
```

---

## 6. Retrieval Pipeline

### 6.1 Phase 1: FTS5-only retrieval

```
Query ──> Tokenize ──> FTS5 BM25 ──> Score ──> Rank ──> Truncate ──> Return
               |
               +──> Scope filter (project + global)
               +──> Type filter (if specified)
               +──> Superseded filter (exclude by default)
```

FTS5 query construction:
- Input: `"how do we handle auth middleware"`
- FTS5 query: `"handle" OR "auth" OR "middleware"` (stop words removed)
- Exact phrases detected with quotes: `"auth middleware"` becomes `"auth middleware"` in FTS5

Scoring in Phase 1:
```
final_score = bm25_score * importance_weight * recency_weight

importance_weight = 0.5 + (importance * 0.5)    -- range [0.5, 1.0]
recency_weight = exp(-decay_rate * days_since_update)  -- exponential decay
```

### 6.2 Phase 2: Hybrid retrieval with RRF

```
Query ──+──> FTS5 BM25 ──────> Rank list A
        |
        +──> Embed query ──> Cosine sim ──> Rank list B
        |
        +──> Entity extract ──> Graph walk ──> Rank list C
        |
        +──> RRF Fusion (A, B, C) ──> Final ranking ──> Truncate ──> Return
```

**Reciprocal Rank Fusion (RRF):**
```
RRF_score(d) = SUM over all rank lists R:
    weight_R / (k + rank_R(d))

where:
    k = 60 (constant, standard RRF)
    weight_FTS5 = 1.0
    weight_vector = 0.8
    weight_graph = 0.6
```

The weights reflect that FTS5 is the most reliable signal (exact term matches), vectors are strong for semantic similarity, and graph traversal adds relationship context but is noisier.

### 6.3 Context assembly algorithm (`mem_context`)

```
1. Load last session summary for project
2. Load all active memories for project (importance DESC)
3. Load global memories with importance > 0.7
4. If focus provided:
     boost = FTS5_score(memory, focus) * 0.3
     memory.effective_score = memory.importance + boost
5. Sort by effective_score DESC
6. Token estimation: count(content) * 1.3 (rough chars-to-tokens ratio)
7. Pack memories until budget exhausted:
     - Session summary always included first (exempt from budget)
     - Architecture memories get 1.5x weight (prioritized)
     - Remaining memories fill by score
8. Return packed context
```

---

## 7. Consolidation Pipeline (Phase 3)

### 7.1 Importance scoring

Initial importance is assigned at save time based on:

```
base_importance = type_weight[type]

type_weight = {
    "architecture": 0.9,
    "decision": 0.85,
    "convention": 0.8,
    "pattern": 0.75,
    "bugfix": 0.7,
    "discovery": 0.6,
    "config": 0.5,
    "preference": 0.5,
    "session_summary": 0.4
}

importance = clamp(base_importance + agent_override, 0.0, 1.0)
```

If the agent passes an explicit `importance`, it overrides the type-based default.

### 7.2 Decay model

```
effective_importance(m) = m.importance * exp(-m.decay_rate * days_since_last_access)
```

Decay rates by type:
- Architecture/decision/convention: 0.005/day (slow -- these are stable)
- Pattern/preference: 0.01/day (medium)
- Bugfix/discovery/config: 0.02/day (faster -- specifics fade)
- Session summary: 0.05/day (fast -- recent sessions matter, old ones do not)

Accessing a memory (`mem_get`) resets its `last_accessed` timestamp, effectively refreshing its decay clock. Frequently accessed memories never decay.

### 7.3 Consolidation cycle

Runs automatically when `mneme mcp` starts and then every 6 hours (configurable):

```
1. SWEEP: For each project DB:
   a. Calculate effective_importance for all active memories
   b. Soft-delete memories with effective_importance < 0.05
   c. Hard-delete memories soft-deleted > 30 days ago

2. DEDUP: Find memories with high content similarity:
   a. Phase 1 (FTS5): exact title matches + high BM25 overlap
   b. Phase 2 (vector): cosine similarity > 0.92
   c. Merge: keep the one with higher importance, supersede the other
   d. Merged content = union of unique information from both

3. CONFLICT RESOLUTION:
   a. Detect contradictions: same topic_key, different content, both recent
   b. Strategy: keep the newer one, supersede the older
   c. Log the conflict for human review (mem_stats shows conflict count)

4. BUDGET ENFORCEMENT:
   a. If total memories > configured budget (default 1000 per project):
   b. Evict lowest effective_importance memories until under budget
   c. Global memories have a separate budget (default 200)
```

### 7.4 Accelerated decay (`mem_forget`)

When `mem_forget` is called:
- Sets `decay_rate = 1.0` (decays to zero in ~5 days)
- Records reason in memory metadata
- Does NOT delete immediately -- the consolidation cycle handles it

---

## 8. CLI Commands

### 8.1 Phase 1 commands

```
mneme save        Save a memory from the command line
mneme search      Search memories
mneme get         Get full content of a memory by ID
mneme status      Show memory store status (counts, sizes, project detection)
mneme mcp         Start MCP server (stdio)
mneme version     Show version
mneme help        Show help
```

**`mneme save`:**
```
Usage: mneme save [flags]

Flags:
  -t, --title string       Memory title (required)
  -c, --content string     Memory content (or read from stdin with -)
  -T, --type string        Memory type (default "discovery")
  -s, --scope string       Memory scope (default "project")
  -k, --topic-key string   Topic key for upserts
  -p, --project string     Project slug (default: auto-detect)
  -f, --file strings       Referenced file paths
  -i, --importance float   Importance override (0.0-1.0)
      --stdin              Read content from stdin
```

Example:
```bash
mneme save -t "Auth uses JWT with RS256" -T decision -c "## What\nSwitched to RS256..."
echo "Long content..." | mneme save -t "Session notes" --stdin
```

**`mneme search`:**
```
Usage: mneme search <query> [flags]

Flags:
  -p, --project string     Project filter (default: auto-detect, '*' for all)
  -s, --scope string       Scope filter (default "all")
  -T, --type string        Type filter
  -n, --limit int          Max results (default 10)
      --full               Show full content (not truncated)
      --json               Output as JSON
      --superseded         Include superseded memories
```

**`mneme get`:**
```
Usage: mneme get <id>

Flags:
      --json    Output as JSON
```

**`mneme status`:**
```
Usage: mneme status [flags]

Flags:
  -p, --project string    Project slug (default: auto-detect)
      --json               Output as JSON
```

Output:
```
mneme v0.1.0

Project: confio-pagos/platform (auto-detected)
Database: ~/.mneme/projects/confio-pagos-platform.db

Memories: 142 active, 8 superseded, 4 forgotten
  architecture: 15  decision: 23  convention: 8
  discovery: 45     bugfix: 18    pattern: 12
  session_summary: 21

Global: 15 memories in ~/.mneme/global.db
Database size: 2.3 MB (project) + 156 KB (global)
Last session: 2026-04-12T18:00:00Z (2h15m ago)
```

**`mneme mcp`:**
```
Usage: mneme mcp [flags]

Flags:
      --project string    Project override (default: auto-detect from cwd git remote)
      --tools string      Tool visibility: "all" or "agent" (default "all")
      --log-file string   Log file path (default: stderr)
```

The `--tools=agent` flag filters tool visibility for agent-facing MCP sessions (hides admin tools like `mem_stats`, `mem_forget`).

### 8.2 Phase 2 commands

```
mneme sync export     Export project memories to JSONL.gz
mneme sync import     Import memories from JSONL.gz
mneme relate          Create entity relation from CLI
mneme entities        List entities for a project
```

### 8.3 Phase 3 commands

```
mneme forget <id>     Mark memory for accelerated decay
mneme stats           Detailed statistics
mneme consolidate     Run consolidation manually
```

### 8.4 Migration command

```
mneme init            Migrate a project from legacy workflows to mneme SDD engine
```

**`mneme init`:**
```
Usage: mneme init [flags]

Flags:
  --apply    Execute the migration (default is dry-run)
  -y, --yes  Skip confirmation prompt (only with --apply)
  -p, --project  Project slug override (inherited from root)

By default (no flags), performs a dry-run: detects legacy workflow artifacts
(.workflow/, .claude/specs/, etc.), classifies them with a weighted heuristic,
and prints the migration plan without modifying filesystem or database.

With --apply: migrates artifacts to the SDD engine (backlog items, specs, memories),
cleans up legacy directories, and rewrites CLAUDE.local.md with the SDD template.

Idempotent: a second run on a fully-migrated project finds no artifacts.
```

---

## 9. Configuration

### 9.1 Config file: `~/.mneme/config.toml`

```toml
# mneme configuration

[storage]
# Base directory for all mneme data
data_dir = "~/.mneme"

# Maximum memories per project before eviction
project_budget = 1000

# Maximum global memories before eviction
global_budget = 200

[search]
# Default result limit
default_limit = 10

# Preview truncation length (characters)
preview_length = 300

# Minimum BM25 score to include in results (0.0-1.0)
min_relevance = 0.01

[context]
# Default token budget for mem_context
default_budget = 4000

# Include global memories in project context
include_global = true

# Minimum importance for global memories in context
global_min_importance = 0.7

[consolidation]
# Enable automatic consolidation
enabled = true

# Consolidation interval
interval = "6h"

# Days to keep soft-deleted memories before hard delete
retention_days = 30

# Similarity threshold for dedup (0.0-1.0)
dedup_threshold = 0.92

[decay]
# Decay rates by memory type (per day)
architecture = 0.005
decision = 0.005
convention = 0.005
pattern = 0.01
preference = 0.01
bugfix = 0.02
discovery = 0.02
config = 0.02
session_summary = 0.05

[mcp]
# Tool visibility: "all" or "agent"
tools = "all"

# Log level: "debug", "info", "warn", "error"
log_level = "info"

[embedding]
# Embedding provider (Phase 2): "local" or "none"
provider = "none"

# Local embedding model (Phase 2)
model = "all-MiniLM-L6-v2"

# Vector dimensions
dimensions = 384

[sync]
# Git sync enabled (Phase 2)
enabled = false

# Sync format
format = "jsonl.gz"
```

### 9.2 Environment variables

All config values can be overridden via environment variables with the `MNEME_` prefix:

| Variable | Config equivalent | Description |
|----------|------------------|-------------|
| `MNEME_DATA_DIR` | `storage.data_dir` | Base data directory |
| `MNEME_PROJECT` | (runtime) | Override project auto-detection |
| `MNEME_LOG_LEVEL` | `mcp.log_level` | Log verbosity |
| `MNEME_LOG_FILE` | (runtime) | Log file path |
| `MNEME_TOOLS` | `mcp.tools` | MCP tool visibility |

### 9.3 Directory structure

```
~/.mneme/
  config.toml           -- user configuration
  global.db             -- global + org scope memories
  projects/
    confio-pagos-platform.db   -- project-specific memories
    juan-mneme.db              -- project-specific memories
  logs/
    mneme.log            -- rotating log file
  cache/
    embeddings/          -- cached embedding model (Phase 2)
```

### 9.4 Project detection

Project slug is derived from the git remote origin URL:

```
git@github.com:confio-pagos/platform.git  -->  confio-pagos/platform
https://github.com/juan/mneme.git         -->  juan/mneme
git@gitlab.com:team/sub/repo.git          -->  team/sub/repo
```

Normalization rules:
1. Strip protocol prefix (`https://`, `git@`, `ssh://`)
2. Strip `.git` suffix
3. Strip hostname (`github.com:`, `gitlab.com/`, etc.)
4. Lowercase everything
5. Replace path separators with `/`
6. The resulting slug is used as both the project identifier and the database filename (with `/` replaced by `-` for the filename)

Fallback: if not in a git repo or no remote, use the directory name.

---

## 10. Implementation Phases

### Phase 1: MVP -- Replace CLAUDE.md as source of truth

**Goal:** A working memory system that an agent can save to and search from. Enough to stop using CLAUDE.md as a knowledge base.

**Deliverables:**
- Single Go binary (`mneme`)
- SQLite + FTS5 storage
- MCP server (stdio) with tools: `mem_save`, `mem_search`, `mem_get`, `mem_context`, `mem_update`, `mem_session_end`, `mem_suggest_topic_key`
- CLI: `mneme save`, `mneme search`, `mneme get`, `mneme status`, `mneme mcp`, `mneme version`
- Scopes: `global` + `project`
- Project auto-detection from git remote
- Config file: `~/.mneme/config.toml`
- Upsert behavior via `topic_key`
- Basic importance scoring (type-based defaults)
- Preview truncation in search results

**Acceptance criteria:**
1. `mneme mcp` starts and responds to MCP `initialize` and `tools/list` correctly over stdio.
2. `mem_save` with `topic_key` performs upsert: second save with same `topic_key`/`project`/`scope` overwrites content and increments `revision_count`.
3. `mem_search` returns BM25-ranked results with truncated previews; `mem_get` returns full untruncated content with `access_count` increment.
4. `mem_context` returns a token-budget-constrained set of memories sorted by importance, with the last session summary always included.
5. `mneme save -t "test" -c "content" && mneme search "test"` works end-to-end from CLI.
6. Project auto-detection correctly parses `git@github.com:org/repo.git` and `https://github.com/org/repo.git` formats.

**Estimated effort:** 2-3 weeks

---

### Phase 2: Cross-project intelligence

**Goal:** Semantic search, knowledge graph, and shared memories across projects and teams.

**Deliverables:**
- Local embedding generation (all-MiniLM-L6-v2 or similar, via Go binding)
- Vector storage in SQLite (BLOB column with brute-force cosine similarity)
- Hybrid retrieval with RRF fusion (FTS5 + vector)
- Knowledge graph: entities, relations, `mem_relate`, `mem_timeline`
- Org/team scope
- Git sync: `mneme sync export`, `mneme sync import` (JSONL.gz chunks)
- Cross-project search (`--project '*'`)

**Acceptance criteria:**
1. `mem_search` with a semantically similar but lexically different query (e.g., "authentication flow" finds a memory titled "JWT auth middleware setup") returns relevant results via vector similarity.
2. `mem_relate` creates entities and relations; `mem_timeline` shows chronological neighbors correctly.
3. `mneme sync export` produces a valid JSONL.gz file; `mneme sync import` on another machine restores memories without duplicates.
4. RRF fusion produces better ranking than FTS5 alone (measured by manual relevance judgment on a test set of 20 queries).

**Estimated effort:** 3-4 weeks

---

### Phase 3: Automatic consolidation

**Goal:** Memory stays healthy without manual curation. Old irrelevant memories fade, duplicates merge, budgets are enforced.

**Deliverables:**
- Background consolidation pipeline (runs in MCP server process)
- Time-based decay with configurable rates per type
- Duplicate detection and merge
- Contradiction detection
- Memory budget enforcement with eviction
- `mem_forget` for accelerated decay
- `mem_stats` for observability
- `mneme consolidate` CLI for manual runs

**Acceptance criteria:**
1. A memory with `decay_rate=0.02` that has not been accessed in 60 days has `effective_importance < 0.05` and is soft-deleted by the consolidation sweep.
2. Two memories with cosine similarity > 0.92 (or exact title match) are detected as duplicates; the lower-importance one is marked superseded.
3. `mem_forget` sets `decay_rate=1.0`; the memory is soft-deleted within the next consolidation cycle.
4. When project memories exceed `project_budget`, the lowest-scored memories are evicted until under budget.
5. `mem_stats` correctly reports counts by type, scope, and status (active/superseded/forgotten).

**Estimated effort:** 2-3 weeks

---

### Phase 4: Hooks and auto-capture (v1.0)

**Goal:** Memory capture happens automatically, not just when the agent explicitly calls `mem_save`.

**Deliverables:**
- Claude Code hooks integration (`SessionStart`, `SessionEnd`, `PostCompaction`)
- Auto-capture: session summaries generated from conversation context
- PostCompaction recovery: automatic `mem_context` call after compaction
- HTTP API for external integrations
- TUI for human memory browsing and administration (bubbletea)
- `mneme serve` for HTTP API
- Memory export to markdown (for human review)

**Acceptance criteria:**
1. Claude Code `SessionStart` hook triggers `mem_context` automatically; the agent starts with relevant context without manual search.
2. `SessionEnd` hook triggers `mem_session_end` automatically.
3. `PostCompaction` hook triggers context recovery automatically.
4. HTTP API serves `GET /v1/memories/search?q=...` and `POST /v1/memories` with JSON responses matching MCP tool schemas.
5. TUI shows a searchable, filterable list of memories with full-content preview.

**Estimated effort:** 3-4 weeks

---

## Appendix A: MCP Protocol Details

### Transport

mneme uses **stdio** transport (stdin/stdout) for MCP communication. JSON-RPC 2.0 messages are newline-delimited on stdout.

### Initialization handshake

```json
// Client -> Server
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2024-11-05",
    "capabilities": {},
    "clientInfo": {
      "name": "claude-code",
      "version": "1.0.0"
    }
  }
}

// Server -> Client
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2024-11-05",
    "capabilities": {
      "tools": { "listChanged": false }
    },
    "serverInfo": {
      "name": "mneme",
      "version": "0.1.0"
    }
  }
}

// Client -> Server
{
  "jsonrpc": "2.0",
  "method": "notifications/initialized"
}
```

### Tool listing

```json
// Client -> Server
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/list"
}

// Server -> Client
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "tools": [
      {
        "name": "mem_save",
        "description": "Save a structured observation to persistent memory...",
        "inputSchema": { ... }
      }
      // ... all tools
    ]
  }
}
```

### Tool invocation

```json
// Client -> Server
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "mem_save",
    "arguments": {
      "title": "Auth uses JWT with RS256",
      "type": "decision",
      "content": "## What\nSwitched to RS256 for JWT signing.\n\n## Why\n..."
    }
  }
}

// Server -> Client
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"id\":\"019530a1-...\",\"action\":\"created\",\"revision_count\":1,\"title\":\"Auth uses JWT with RS256\"}"
      }
    ]
  }
}
```

### Error handling

MCP errors use the standard JSON-RPC error codes:

| Code | Meaning | When |
|------|---------|------|
| -32600 | Invalid request | Malformed JSON-RPC |
| -32601 | Method not found | Unknown MCP method |
| -32602 | Invalid params | Missing required params, invalid types |
| -32603 | Internal error | Database error, unexpected failure |
| -32000 | Memory not found | `mem_get` with invalid ID |
| -32001 | Upsert conflict | Concurrent upsert on same topic_key (unlikely with SQLite) |

### Logging

MCP server logs go to stderr (or `--log-file` if specified). Log format is structured JSON:

```json
{"level":"info","ts":"2026-04-13T10:00:00Z","msg":"tool called","tool":"mem_save","project":"confio-pagos/platform","duration_ms":12}
```

Log levels: `debug`, `info`, `warn`, `error`. Default: `info`.

---

## Appendix B: Key Go Interfaces

```go
// internal/model/memory.go

type MemoryType string

const (
    TypeDecision       MemoryType = "decision"
    TypeDiscovery      MemoryType = "discovery"
    TypeBugfix         MemoryType = "bugfix"
    TypePattern        MemoryType = "pattern"
    TypePreference     MemoryType = "preference"
    TypeConvention     MemoryType = "convention"
    TypeArchitecture   MemoryType = "architecture"
    TypeConfig         MemoryType = "config"
    TypeSessionSummary MemoryType = "session_summary"
)

type Scope string

const (
    ScopeGlobal  Scope = "global"
    ScopeOrg     Scope = "org"
    ScopeProject Scope = "project"
)

type Memory struct {
    ID            string     `json:"id"`
    Type          MemoryType `json:"type"`
    Scope         Scope      `json:"scope"`
    Title         string     `json:"title"`
    Content       string     `json:"content"`
    TopicKey      string     `json:"topic_key,omitempty"`
    Project       string     `json:"project,omitempty"`
    SessionID     string     `json:"session_id,omitempty"`
    CreatedBy     string     `json:"created_by,omitempty"`
    CreatedAt     time.Time  `json:"created_at"`
    UpdatedAt     time.Time  `json:"updated_at"`
    Importance    float64    `json:"importance"`
    Confidence    float64    `json:"confidence"`
    AccessCount   int        `json:"access_count"`
    LastAccessed  *time.Time `json:"last_accessed,omitempty"`
    DecayRate     float64    `json:"decay_rate"`
    RevisionCount int        `json:"revision_count"`
    SupersededBy  string     `json:"superseded_by,omitempty"`
    DeletedAt     *time.Time `json:"deleted_at,omitempty"`
    Files         []string   `json:"files,omitempty"`
}
```

```go
// internal/store/memory.go

type MemoryStore interface {
    // Create inserts a new memory. Returns the created memory with generated ID.
    Create(ctx context.Context, m *model.Memory) (*model.Memory, error)

    // Get retrieves a memory by ID. Returns nil, nil if not found.
    Get(ctx context.Context, id string) (*model.Memory, error)

    // Update modifies an existing memory. Only non-zero fields are updated.
    Update(ctx context.Context, id string, fields UpdateFields) error

    // Upsert creates or updates based on topic_key + project + scope.
    // Returns the memory and whether it was created (true) or updated (false).
    Upsert(ctx context.Context, m *model.Memory) (*model.Memory, bool, error)

    // SoftDelete marks a memory as deleted without removing it.
    SoftDelete(ctx context.Context, id string) error

    // HardDelete permanently removes soft-deleted memories older than retention.
    HardDelete(ctx context.Context, olderThan time.Time) (int, error)

    // List returns memories matching the filter, ordered by the given sort.
    List(ctx context.Context, filter ListFilter, sort SortOrder, limit int) ([]*model.Memory, error)

    // IncrementAccess bumps access_count and updates last_accessed.
    IncrementAccess(ctx context.Context, id string) error
}
```

```go
// internal/store/search.go

type SearchStore interface {
    // FTS5Search performs a full-text search and returns ranked results.
    FTS5Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
}

type SearchOptions struct {
    Project          string
    Scope            model.Scope // empty = all
    Type             model.MemoryType // empty = all
    Limit            int
    IncludeSuperseded bool
    MinRelevance     float64
}

type SearchResult struct {
    Memory         *model.Memory
    BM25Score      float64
    RelevanceScore float64 // final combined score
    Preview        string  // truncated content
}
```

```go
// internal/service/memory.go

type MemoryService interface {
    Save(ctx context.Context, req SaveRequest) (*SaveResponse, error)
    Get(ctx context.Context, id string) (*model.Memory, error)
    Update(ctx context.Context, id string, req UpdateRequest) (*SaveResponse, error)
    Search(ctx context.Context, req SearchRequest) (*SearchResponse, error)
    Context(ctx context.Context, req ContextRequest) (*ContextResponse, error)
    SessionEnd(ctx context.Context, req SessionEndRequest) (*SessionEndResponse, error)
    SuggestTopicKey(ctx context.Context, title, project string) (*TopicKeySuggestion, error)
    Forget(ctx context.Context, id, reason string) error
    Stats(ctx context.Context, project string) (*StatsResponse, error)
}
```

```go
// internal/project/detect.go

type Detector interface {
    // DetectProject returns the project slug from the current working directory.
    // Returns empty string if detection fails.
    DetectProject(dir string) (string, error)

    // NormalizeSlug normalizes a git remote URL to a project slug.
    NormalizeSlug(remoteURL string) string

    // SlugToFilename converts a project slug to a safe filename.
    // "confio-pagos/platform" -> "confio-pagos-platform"
    SlugToFilename(slug string) string
}
```

```go
// internal/mcp/server.go

type Server struct {
    service  service.MemoryService
    project  string
    tools    []Tool
    logger   *slog.Logger
}

// Run starts the MCP server, reading from stdin and writing to stdout.
// It blocks until stdin is closed or ctx is cancelled.
func (s *Server) Run(ctx context.Context) error
```

---

## Appendix C: Migration Strategy

Migrations are numbered SQL files executed in order. The `schema_version` table tracks which migrations have been applied.

```
internal/db/migrations/
  001_initial.sql       -- memories, memories_fts, memory_files, sessions, schema_version
  002_knowledge_graph.sql   -- entities, relations, memory_entities (Phase 2)
  003_embeddings.sql        -- embeddings table (Phase 2)
```

Migration runner:
1. Read current version from `schema_version` (0 if table does not exist).
2. Apply all migrations with version > current, in order.
3. Each migration runs in a transaction.
4. On failure, the transaction is rolled back and the error is returned.

Migrations are embedded in the binary via `go:embed`.

---

## Appendix D: Dependencies

### Required (Phase 1)
- `github.com/mattn/go-sqlite3` -- SQLite with CGO (FTS5, JSON1)
- `github.com/gofrs/uuid/v5` -- UUIDv7 generation
- `github.com/spf13/cobra` -- CLI framework
- `github.com/pelletier/go-toml/v2` -- TOML config parsing
- `log/slog` -- structured logging (stdlib)

### Phase 2
- Embedding library TBD (options: `github.com/nicholasgasior/goembed`, ONNX runtime Go bindings, or shell out to a Python process)
- `compress/gzip` -- JSONL.gz sync format (stdlib)

### Phase 4
- `github.com/charmbracelet/bubbletea` -- TUI framework
- `github.com/charmbracelet/lipgloss` -- TUI styling
- `net/http` -- HTTP API (stdlib)

### Explicitly avoided
- No ORM -- raw SQL with `database/sql` interface
- No web framework -- stdlib `net/http` for Phase 4
- No external vector database -- SQLite brute-force is sufficient at expected scale
