# mneme -- API Reference

Exhaustive reference for all three frontends: MCP (stdio JSON-RPC), HTTP REST, and CLI. The same service layer powers all three; this document covers the transport-specific details of each.

---

## Table of Contents

1. [MCP Tools (24 tools)](#mcp-tools)
   - [Memory tools (14)](#memory-tools)
   - [Backlog tools (4)](#backlog-tools)
   - [Spec tools (6)](#spec-tools)
   - [Error codes](#mcp-error-codes)
2. [HTTP API (12 endpoints)](#http-api)
3. [CLI Commands](#cli-commands)
   - [Summary table](#cli-summary-table)

---

## MCP Tools

**Protocol:** JSON-RPC 2.0 over stdio (line-delimited)
**ProtocolVersion:** `2024-11-05`
**Start:** `mneme mcp` (or `mneme mcp --tools agent`)

The server responds to three methods: `initialize`, `tools/list`, and `tools/call`. All tool results are returned as a single `text` content block containing a JSON string.

### Handshake

```json
// Request
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"my-agent","version":"1.0"}}}

// Response
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"mneme","version":"0.5.0"}}}

// Notification (no response expected)
{"jsonrpc":"2.0","method":"notifications/initialized"}
```

---

### Memory Tools

#### mem_save

Save a structured observation to persistent memory. If `topic_key` matches an existing memory in the same project/scope, the existing memory is updated (upsert).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `title` | string | yes | Short, searchable summary |
| `content` | string | yes | Full knowledge body (Markdown) |
| `type` | string | no | `decision`, `discovery`, `bugfix`, `pattern`, `preference`, `convention`, `architecture`, `config`, `session_summary`, `rule`, `synthesis`. Default: `discovery` |
| `scope` | string | no | `global`, `org`, `project`. Default: `project` |
| `topic_key` | string | no | Stable key for idempotent upserts (e.g. `architecture/auth-model`) |
| `project` | string | no | Project slug. Default: auto-detected |
| `session_id` | string | no | Agent session ID |
| `created_by` | string | no | Identifier of the saving agent |
| `files` | string[] | no | Source file paths related to this memory |
| `importance` | number | no | Initial importance (0.0-1.0). Default: type-based |
| `applies_to` | string[] | no | Rule patterns. Required when `type` is `rule` |
| `severity` | string | no | `info`, `warn`, `block`. Default: `warn` (for rules) |

**Response:**

```json
{
  "id": "019de100-abcd-7fff-8000-000000000001",
  "title": "Auth uses JWT RS256",
  "action": "created",
  "topic_key": "architecture/auth-model"
}
```

`action` is `"created"` or `"updated"` (upsert).

**Errors:** `-32602` missing title/content, invalid type/scope/severity, applies_to required for rules.

---

#### mem_search

Search persistent memory using full-text search with optional graph expansion.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | yes | Full-text search query |
| `project` | string | no | Restrict to project slug |
| `scope` | string | no | `global`, `org`, `project` |
| `type` | string | no | Filter by memory type |
| `limit` | integer | no | Max results (1-50). Default: config value |
| `include_superseded` | boolean | no | Include superseded versions. Default: `false` |
| `include_graph` | boolean | no | Enable 1-hop graph expansion. Default: `true` |

**Response:**

```json
{
  "results": [
    {
      "id": "019de100-...",
      "title": "Auth uses JWT RS256",
      "content": "...",
      "type": "decision",
      "scope": "project",
      "project": "wirvii/mneme",
      "importance": 0.9,
      "confidence": 0.8,
      "created_at": "2026-04-30T02:43:17Z",
      "updated_at": "2026-04-30T02:43:17Z",
      "relevance_score": 12.5,
      "bm25_score": -14.2,
      "vector_score": 0.45
    }
  ],
  "total": 1,
  "query": "JWT RS256"
}
```

**Errors:** `-32602` missing query, invalid type/scope.

---

#### mem_get

Retrieve the full content of a memory by ID. Increments the access counter.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | UUIDv7 of the memory |

**Response:** Full `Memory` object (same fields as search result, plus `files`, `revision_count`, `access_count`, `decay_rate`).

**Errors:** `-32000` memory not found, `-32602` missing id.

---

#### mem_context

Get the most relevant memories for the current project context. Returns rules first (separate budget), then scored memories.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `project` | string | no | Project slug. Default: auto-detected |
| `budget` | integer | no | Token budget (min 1). Default: config value |
| `focus` | string | no | Topic or question that biases selection |
| `include_graph` | boolean | no | Enable graph expansion for focus. Default: `true` |

**Response:**

```json
{
  "project": "wirvii/mneme",
  "memories": [ ... ],
  "token_estimate": 3500,
  "total_available": 142,
  "included": 12,
  "last_session": "Summary of last session...",
  "active_rules": [ ... ],
  "cluster_overviews": [ ... ],
  "cluster_overviews_count": 3,
  "cluster_overviews_tokens": 900,
  "top_cluster": "community-uuid",
  "top_cluster_members": 5,
  "packing_mode": "communities"
}
```

Fields `cluster_overviews`, `cluster_overviews_count`, `cluster_overviews_tokens`, `top_cluster`, `top_cluster_members`, and `packing_mode` are omitted when community packing is inactive (flat mode).

**Errors:** `-32602` invalid params.

---

#### mem_update

Update an existing memory by ID. Only provided fields are changed.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | UUIDv7 of the memory |
| `title` | string | no | New title |
| `content` | string | no | New content |
| `type` | string | no | New type (excludes `rule`, `synthesis`) |
| `importance` | number | no | New importance (0.0-1.0) |
| `confidence` | number | no | New confidence (0.0-1.0) |
| `files` | string[] | no | Replacement file list |

**Response:** Full updated `Memory` object.

**Errors:** `-32000` not found, `-32602` invalid type/params.

---

#### mem_forget

Mark a memory for accelerated decay. Sets decay rate to 1.0.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | UUIDv7 of the memory |
| `reason` | string | no | Reason for forgetting |

**Response:**

```json
{"id": "019de100-...", "status": "marked_for_decay"}
```

**Errors:** `-32000` not found, `-32602` missing id.

---

#### mem_session_end

End the current session and save a summary.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `summary` | string | yes | Human-readable session summary |
| `session_id` | string | no | Session ID to close. Generated if omitted |
| `project` | string | no | Project slug. Default: auto-detected |

**Response:**

```json
{"id": "019de100-...", "session_id": "sess-abc", "status": "saved"}
```

**Errors:** `-32602` missing summary.

---

#### mem_relate

Create or update a relationship between two graph endpoints. Each endpoint string is resolved hybrid (SPEC-031):

1. Memory UUID full or 8+ hex prefix → memory in either store
2. Memory `topic_key` (only when the corresponding `*_kind` is omitted or `concept`)
3. Entity by name (creates with `kind` when missing)

When resolution lands on a memory, a proxy entity is created and `memory_entities` is auto-linked so the relation is reachable from `mem_explore`. Pass an explicit non-default `*_kind` (e.g. `"service"`, `"library"`) to force entity-only semantics and skip topic_key resolution.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `source` | string | yes | Source endpoint: memory UUID, UUID prefix, topic_key, or entity name |
| `target` | string | yes | Target endpoint: same semantics as `source` |
| `relation` | string | yes | `depends_on`, `implements`, `supersedes`, `related_to`, `part_of`, `uses`, `conflicts_with`, `references` |
| `source_kind` | string | no | `module`, `service`, `library`, `concept`, `person`, `pattern`, `file`. Default: `concept` (enables topic_key resolution) |
| `target_kind` | string | no | Same enum. Default: `concept` |
| `project` | string | no | Project slug |
| `weight` | number | no | Override default weight (0.0-1.0) |

**Response:**

```json
{
  "relation_id": "019df133-d294-7c81-97b4-0bc1dfd16608",
  "source_id": "019df115-f9d6-7c70-9679-592598f8533a",
  "target_id": "019df116-5035-7c70-9d4b-8c49e76d3aa3",
  "created": true,
  "weight": 0.9
}
```

`created` is `true` for new relations, `false` when an identical relation already existed (idempotent).

**Errors:** `-32602` invalid relation type, entity kind, weight; `-32603` cross-scope relation rejected (global/org → project memory).

---

#### mem_stats

Return aggregate statistics about the project memory store.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `project` | string | no | Project slug. Empty string for global stats |

**Response:**

```json
{
  "project": "wirvii/mneme",
  "total_memories": 142,
  "active": 130,
  "superseded": 8,
  "forgotten": 4,
  "by_type": {"decision": 25, "discovery": 40, ...},
  "by_scope": {"project": 120, "global": 22},
  "embeddings_count": 130,
  "knowledge_gaps": {"total": 5, "top": [...]},
  "db_size_bytes": 524288,
  "oldest_memory": "2026-04-01T00:00:00Z",
  "newest_memory": "2026-04-30T12:00:00Z",
  "avg_importance": 0.72
}
```

---

#### mem_timeline

Get memories around a specific point in time, ordered chronologically.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `around` | string | yes | UUID or ISO 8601 timestamp |
| `project` | string | no | Project slug |
| `window` | string | no | Time range (e.g. `7d`, `24h`, `30d`). Default: `7d` |
| `limit` | integer | no | Max results (1-100). Default: 20 |

**Response:**

```json
{
  "memories": [ ... ],
  "center": "2026-04-25T12:00:00Z",
  "window": "7d"
}
```

---

#### mem_checkpoint

Save a checkpoint of the current work state. Overwrites the previous checkpoint automatically.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `summary` | string | yes | Brief summary of current work state |
| `decisions` | string | no | Decisions made since last checkpoint |
| `next_steps` | string | no | What to do next if context is lost |
| `project` | string | no | Project slug |

**Response:**

```json
{"id": "019de100-...", "status": "saved", "action": "created"}
```

**Errors:** `-32602` missing summary.

---

#### mem_suggest_topic_key

Suggest a topic_key for a new memory. Searches existing keys and unresolved gaps.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `title` | string | yes | Title of the memory |
| `project` | string | no | Project slug |

**Response:**

```json
{
  "suggestions": [
    {"key": "architecture/auth-model", "score": 0.85, "source": "existing"},
    {"key": "decision/retry-strategy", "score": 0.72, "source": "gap"}
  ]
}
```

**Errors:** `-32602` missing title.

---

#### mem_explore

Explore the knowledge graph from a seed memory using prioritised BFS traversal.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `seed` | string | yes | Full UUID, short prefix (8+ hex), or topic_key |
| `depth` | integer | no | Max hops (0-5). Default: 2 |
| `budget` | integer | no | Token budget. Default: 4000 |
| `threshold` | number | no | Min relation weight (0.0-1.0). Default: 0.3 |
| `project` | string | no | Project slug |

**Response:**

```json
{
  "seed_id": "019de100-...",
  "seed_title": "Architecture: Auth model",
  "nodes": [
    {
      "memory_id": "019de101-...",
      "parent_memory_id": "019de100-...",
      "title": "JWT token rotation",
      "topic_key": "security/jwt-rotation",
      "type": "decision",
      "distance": 1,
      "accumulated_weight": 0.9,
      "relation_type": "depends_on",
      "token_estimate": 245
    }
  ],
  "total_nodes": 5,
  "tokens_used": 1111,
  "max_depth_reached": 2
}
```

**Errors:** `-32602` missing seed, ambiguous seed (matches multiple), invalid depth. `-32000` seed not found.

---

#### mem_gaps

List knowledge gaps -- unresolved `[[wikilink]]` references.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `scope` | string | no | `project` (default), `global`, `all` |
| `limit` | integer | no | Max gaps (1-100). Default: 20 |
| `min_mentions` | integer | no | Minimum mentions. Default: 1 |
| `include_samples` | boolean | no | Include sample source memories. Default: `true` |
| `project` | string | no | Project slug |

**Response:**

```json
{
  "gaps": [
    {
      "target_topic_key": "decision/retry-strategy",
      "total_mentions": 5,
      "source_count": 3,
      "first_seen_at": "2026-04-20T10:00:00Z",
      "last_seen_at": "2026-04-30T14:00:00Z",
      "urgency_score": 0.8,
      "sample_sources": [...]
    }
  ],
  "total": 12
}
```

---

### Backlog Tools

#### backlog_add

Add a new item to the project backlog.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `title` | string | yes | Short description |
| `description` | string | no | Detailed explanation |
| `priority` | string | no | `critical`, `high`, `medium`, `low`. Default: `medium` |
| `project` | string | no | Project slug |

**Response:**

```json
{
  "id": "BL-001",
  "title": "Push notifications",
  "status": "raw",
  "priority": "medium",
  "project": "wirvii/mneme",
  "created_at": "2026-04-30T12:00:00Z"
}
```

---

#### backlog_list

List backlog items for the current project.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `status` | string | no | `raw`, `refined`, `promoted`, `archived` |
| `project` | string | no | Project slug |

**Response:** Array of backlog items.

---

#### backlog_refine

Refine a raw backlog item with additional details.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Backlog item ID (e.g. `BL-001`) |
| `refinement` | string | yes | Refinement content to add |

**Response:** Updated backlog item with `status: "refined"`.

**Errors:** `-32602` missing id/refinement. `-32000` not found.

---

#### backlog_promote

Promote a refined backlog item to a spec. The item must have status `refined`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Backlog item ID |

**Response:** New spec object.

```json
{
  "id": "SPEC-001",
  "title": "Push notifications",
  "status": "draft",
  "backlog_id": "BL-001",
  "project": "wirvii/mneme",
  "created_at": "2026-04-30T12:00:00Z"
}
```

**Errors:** `-32602` not refined, missing id. `-32000` not found.

---

### Spec Tools

#### spec_new

Create a new spec in draft status.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `title` | string | yes | Spec title |
| `backlog_id` | string | no | Originating backlog item ID |
| `project` | string | no | Project slug |

**Response:** Spec object with `status: "draft"`.

---

#### spec_status

Get the full status of a spec including history and pushbacks.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID (e.g. `SPEC-001`) |

**Response:**

```json
{
  "spec": {
    "id": "SPEC-001",
    "title": "Push notifications",
    "status": "implementing",
    "project": "wirvii/mneme",
    "backlog_id": "BL-001",
    "created_at": "2026-04-30T12:00:00Z",
    "updated_at": "2026-04-30T14:00:00Z"
  },
  "history": [
    {
      "from_status": "draft",
      "to_status": "speccing",
      "by": "orchestrator",
      "reason": "Ready for architect",
      "at": "2026-04-30T12:30:00Z"
    }
  ],
  "pushbacks": [
    {
      "from_agent": "backend",
      "questions": ["API contract with auth?"],
      "resolution": "Use service accounts",
      "resolved": true,
      "created_at": "2026-04-30T13:00:00Z"
    }
  ]
}
```

**Errors:** `-32000` not found, `-32602` missing id.

---

#### spec_advance

Advance a spec to its next lifecycle state.

State machine: `draft -> speccing -> specced -> planning -> planned -> implementing -> qa -> done`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID |
| `by` | string | yes | Who triggers the advance |
| `reason` | string | no | Reason for the transition |

**Response:** Updated spec object.

**Errors:** `-32602` invalid transition, missing id/by.

---

#### spec_pushback

Register a pushback from an agent, transitioning the spec to `needs_grill`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID |
| `from_agent` | string | yes | Agent raising the pushback |
| `questions` | string[] | yes | Questions blocking progress (min 1) |

**Response:** Updated spec object with `status: "needs_grill"`.

**Errors:** `-32602` missing fields. `-32000` not found.

---

#### spec_resolve

Resolve the latest pushback on a spec, returning it to `speccing`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID |
| `resolution` | string | yes | Answer to the pushback questions |

**Response:** Updated spec object.

**Errors:** `-32602` missing fields. `-32000` pushback or spec not found.

---

#### spec_list

List specs for the current project.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `status` | string | no | `draft`, `speccing`, `needs_grill`, `specced`, `planning`, `planned`, `implementing`, `qa`, `done` |
| `project` | string | no | Project slug |

**Response:** Array of spec objects.

---

### MCP Error Codes

| Code | Name | Triggered when |
|------|------|----------------|
| `-32600` | Invalid Request | Malformed JSON-RPC envelope |
| `-32601` | Method not found | Unknown MCP method |
| `-32602` | Invalid params | Missing required params, type mismatch, schema validation |
| `-32603` | Internal error | DB error, unexpected failure, SDD service unavailable |
| `-32000` | Not found | `mem_get`/`mem_update`/`mem_forget` with unknown ID; spec/backlog/entity not found |

---

## HTTP API

**Start:** `mneme serve --addr :7437`
**Base URL:** `http://localhost:7437/v1`
**Content-Type:** `application/json` (all requests and responses)
**Auth:** None (local, single-user)

### Error envelope

All errors follow this format:

```json
{
  "error": {
    "code": "not_found",
    "message": "memory with id ... not found"
  }
}
```

Error codes: `not_found` (404), `invalid_request` (400), `invalid_json` (400), `method_not_allowed` (405), `internal_error` (500).

---

### GET /v1/health

Health check. Returns 200 unconditionally.

```bash
curl http://localhost:7437/v1/health
```

```json
{"status": "ok"}
```

---

### POST /v1/memories

Save a new memory (or upsert by `topic_key`).

**Request body:** Same schema as `mem_save` MCP tool.

```bash
curl -X POST http://localhost:7437/v1/memories \
  -H 'Content-Type: application/json' \
  -d '{
    "title": "Auth uses JWT RS256",
    "content": "RS256 with 2048-bit keys...",
    "type": "decision",
    "topic_key": "architecture/auth-model"
  }'
```

**Response:** `201 Created`

```json
{
  "id": "019de100-...",
  "title": "Auth uses JWT RS256",
  "action": "created",
  "topic_key": "architecture/auth-model"
}
```

---

### GET /v1/memories/{id}

Retrieve a memory by UUID.

```bash
curl http://localhost:7437/v1/memories/019de100-abcd-7fff-8000-000000000001
```

**Response:** `200 OK` -- full Memory object.

**Errors:** `404` not found.

---

### PATCH /v1/memories/{id}

Partial update of a memory.

**Request body:** Same schema as `mem_update` MCP tool (minus `id`).

```bash
curl -X PATCH http://localhost:7437/v1/memories/019de100-... \
  -H 'Content-Type: application/json' \
  -d '{"title": "Auth uses JWT RS256 (updated)"}'
```

**Response:** `200 OK` -- updated Memory object.

---

### DELETE /v1/memories/{id}

Mark a memory for accelerated decay.

```bash
curl -X DELETE http://localhost:7437/v1/memories/019de100-... \
  -H 'Content-Type: application/json' \
  -d '{"reason": "Outdated after v2 migration"}'
```

Body is optional.

**Response:** `200 OK`

```json
{"status": "forgotten", "id": "019de100-..."}
```

---

### GET /v1/memories/search

Full-text search with optional graph expansion.

| Query param | Type | Required | Description |
|-------------|------|----------|-------------|
| `q` | string | yes | Search query |
| `project` | string | no | Project slug |
| `scope` | string | no | `global`, `org`, `project` |
| `type` | string | no | Memory type filter |
| `limit` | integer | no | Max results |
| `include_graph` | boolean | no | `true`/`false`/`1`/`0`. Default: config |

```bash
curl 'http://localhost:7437/v1/memories/search?q=JWT+RS256&limit=5'
```

**Response:** `200 OK` -- same shape as `mem_search` MCP response.

---

### GET /v1/memories/context

Get contextual memories with token budgeting.

| Query param | Type | Required | Description |
|-------------|------|----------|-------------|
| `project` | string | no | Project slug |
| `budget` | integer | no | Token budget |
| `focus` | string | no | Focus topic |
| `include_graph` | boolean | no | `true`/`false`/`1`/`0` |

```bash
curl 'http://localhost:7437/v1/memories/context?focus=auth&budget=4000'
```

**Response:** `200 OK` -- same shape as `mem_context` MCP response.

---

### GET /v1/memories/{id}/explore

Explore the knowledge graph from a seed memory.

| Query param | Type | Required | Description |
|-------------|------|----------|-------------|
| `depth` | integer | no | Max hops (0-5). Default: 2 |
| `budget` | integer | no | Token budget. Default: 4000 |
| `threshold` | float | no | Min weight (0.0-1.0). Default: 0.3 |

```bash
curl 'http://localhost:7437/v1/memories/019de100-.../explore?depth=3'
```

**Response:** `200 OK` -- same shape as `mem_explore` MCP response.

---

### POST /v1/sessions/end

End the current session and save a summary.

**Request body:** Same schema as `mem_session_end`.

```bash
curl -X POST http://localhost:7437/v1/sessions/end \
  -H 'Content-Type: application/json' \
  -d '{"summary": "Implemented auth middleware"}'
```

**Response:** `200 OK`

---

### POST /v1/entities/relate

Create or update a relationship between entities.

**Request body:** Same schema as `mem_relate`.

```bash
curl -X POST http://localhost:7437/v1/entities/relate \
  -H 'Content-Type: application/json' \
  -d '{
    "source": "internal/store",
    "target": "internal/db",
    "relation": "depends_on"
  }'
```

**Response:** `201 Created` (new) or `200 OK` (existing).

---

### GET /v1/stats

Aggregate statistics.

| Query param | Type | Required | Description |
|-------------|------|----------|-------------|
| `project` | string | no | Project slug. Empty for global |

```bash
curl http://localhost:7437/v1/stats
```

**Response:** `200 OK` -- same shape as `mem_stats` MCP response.

---

### GET /v1/gaps

List knowledge gaps (unresolved wikilinks).

| Query param | Type | Required | Description |
|-------------|------|----------|-------------|
| `project` | string | no | Project slug |
| `scope` | string | no | `project`, `global`, `all` |
| `limit` | integer | no | Max gaps |
| `min_mentions` | integer | no | Min mentions threshold |

```bash
curl 'http://localhost:7437/v1/gaps?scope=all&limit=10'
```

**Response:** `200 OK` -- same shape as `mem_gaps` MCP response.

---

### POST /v1/consolidate

Run the consolidation pipeline synchronously.

```bash
curl -X POST http://localhost:7437/v1/consolidate
```

**Response:** `200 OK`

```json
{
  "swept": 3,
  "hard_deleted": 1,
  "duplicates": 0,
  "evicted": 2,
  "edge_decayed": 5,
  "communities_detected": 8,
  "communities_new": 2,
  "communities_deleted": 1,
  "synthesis_created": 2,
  "synthesis_updated": 1,
  "synthesis_deleted": 0,
  "synthesis_skipped": 5
}
```

---

### HTTP coverage gaps

The HTTP API does **not** expose:

| MCP tool | HTTP equivalent | Status |
|----------|----------------|--------|
| `mem_checkpoint` | -- | Not implemented |
| `mem_timeline` | -- | Not implemented |
| `mem_suggest_topic_key` | -- | Not implemented |
| `backlog_*` | -- | Not implemented |
| `spec_*` | -- | Not implemented |

---

## CLI Commands

**Binary:** `mneme`
**Global flags** (available on all subcommands):

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--project` | `-p` | auto-detect | Project slug override |
| `--data-dir` | | `~/.mneme` | Data directory override |
| `--log-level` | | (config) | `debug`, `info`, `warn`, `error` |

---

### mneme save

Save a memory to the project store.

```bash
mneme save --title "Auth uses JWT RS256" --content "..." --type decision
echo "content" | mneme save --title "My note" --stdin
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--title` | `-t` | required | Memory title |
| `--content` | `-c` | required | Content (or use `--stdin`) |
| `--type` | `-T` | `discovery` | Memory type |
| `--scope` | `-s` | `project` | Scope |
| `--topic-key` | `-k` | | Topic key for upserts |
| `--file` | `-f` | | Referenced files (repeatable) |
| `--importance` | `-i` | type-based | Importance (0.0-1.0) |
| `--stdin` | | false | Read content from stdin |
| `--applies-to` | `-a` | | Rule patterns (repeatable) |
| `--severity` | | `warn` | Rule severity |

---

### mneme search

Search memories with full-text search.

```bash
mneme search "JWT RS256 auth"
mneme search "N+1 query" --type bugfix --full
mneme search "patterns" --json
mneme search "patterns" --no-graph
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--scope` | `-s` | `all` | Scope filter |
| `--type` | `-T` | | Type filter |
| `--limit` | `-n` | 10 | Max results |
| `--full` | | false | Show full content |
| `--json` | | false | JSON output |
| `--graph` | | true | Enable graph expansion |
| `--no-graph` | | false | Disable graph expansion (overrides `--graph`) |

---

### mneme get

Retrieve a memory by ID.

```bash
mneme get 019de100-abcd-7fff-8000-000000000001
mneme get 019de100-abcd-7fff-8000-000000000001 --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

---

### mneme update

Partial update of an existing memory.

```bash
mneme update 019de100-... --title "New title"
echo "updated" | mneme update 019de100-... --stdin
mneme update 019de100-... --type decision --importance 0.9
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--title` | `-t` | | New title |
| `--content` | `-c` | | New content |
| `--type` | `-T` | | New type |
| `--importance` | `-i` | | New importance (0.0-1.0) |
| `--stdin` | | false | Read content from stdin |
| `--json` | | false | JSON output |

---

### mneme forget

Mark a memory for accelerated decay.

```bash
mneme forget 019de100-... --reason "API changed in v2"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--reason` | | Reason (informational) |

---

### mneme status

Show project dashboard with memory stats, backlog, and specs.

```bash
mneme status
mneme status --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

---

### mneme stats

Show aggregate memory store statistics.

```bash
mneme stats
mneme stats --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

---

### mneme consolidate

Run the consolidation pipeline manually: sweep, purge, dedup, evict, edge decay, community detection, synthesis generation.

```bash
mneme consolidate
```

No flags.

---

### mneme mcp

Start the MCP server over stdio for agent integration.

```bash
mneme mcp
mneme mcp --tools agent
```

| Flag | Default | Description |
|------|---------|-------------|
| `--tools` | config | `all` or `agent` |

---

### mneme serve

Start the HTTP REST API server.

```bash
mneme serve
mneme serve --addr :8080
```

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `:7437` | Listen address |

---

### mneme explore

Explore the knowledge graph from a seed memory.

```bash
mneme explore "architecture/auth-model" --depth 3
mneme explore 019de100 --json
mneme explore "ops/key-rotation" --budget 2000 --threshold 0.5
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--depth` | `-d` | 2 | Max hops (0-5) |
| `--budget` | `-b` | 4000 | Token budget |
| `--threshold` | `-t` | 0.3 | Min relation weight (0.0-1.0) |
| `--json` | | false | JSON output |

---

### mneme gaps

List knowledge gaps (unresolved wikilinks).

```bash
mneme gaps
mneme gaps --scope all --limit 50
mneme gaps --min-count 3
mneme gaps --json | jq '.gaps[].target_topic_key'
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--scope` | `-s` | `project` | `project`, `global`, `all` |
| `--limit` | `-n` | 20 | Max gaps |
| `--min-count` | | 1 | Min mentions |
| `--json` | | false | JSON output |

---

### mneme graph rebuild

Backfill the knowledge graph from existing memories.

```bash
mneme graph rebuild
mneme graph rebuild --dry-run
mneme graph rebuild --force --min-shared 3
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--scope` | `-s` | `project` | `project`, `global`, `all` |
| `--min-shared` | `-k` | 2 | Min shared entities for relation |
| `--max-relations` | | 50 | Cap per memory |
| `--batch-size` | `-b` | 500 | Memories per transaction |
| `--force` | `-f` | false | Delete existing `related_to` and regenerate |
| `--dry-run` | `-n` | false | Preview without writing |

---

### mneme graph cleanup-orphan-relations

Remove relations whose endpoint entities are not linked to any memory through `memory_entities`. Such relations are unreachable from `mem_explore` and were created by the legacy `mem_relate` path before SPEC-031.

```bash
# Default is dry-run — list candidates
mneme graph cleanup-orphan-relations

# Actually delete (requires --yes)
mneme graph cleanup-orphan-relations --apply --yes

# After cleanup, rebuild the graph from scratch
mneme graph rebuild --force
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--scope` | `-s` | `project` | `project`, `global`, `all` |
| `--apply` | | false | Default is dry-run; pass to actually delete |
| `--also-delete-entities` | | false | Also delete entities that become fully unreferenced |
| `--output` | `-o` | `text` | `text` or `json` |
| `--yes` | `-y` | false | Required with `--apply` to confirm destructive deletion |

---

### mneme rule add

Create a rule (memory with type `rule`).

```bash
mneme rule add --title "No vendor edits" \
  --content "Never edit vendor/ files." \
  --applies-to "vendor/**" --severity block
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--title` | `-t` | required | Rule title |
| `--content` | `-c` | required | Rule instruction |
| `--applies-to` | `-a` | required | Pattern (repeatable) |
| `--severity` | `-s` | `warn` | `info`, `warn`, `block` |
| `--scope` | | `project` | `project` or `global` |
| `--topic-key` | `-k` | auto | Topic key for upserts |
| `--importance` | `-i` | 0.95 | Importance |
| `--stdin` | | false | Read content from stdin |

---

### mneme rule list

List active rules with severity coloring.

```bash
mneme rule list
mneme rule list --scope global --severity block
mneme rule list --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--scope` | | Scope filter |
| `--severity` | | Severity filter |
| `--json` | false | JSON output |

---

### mneme rule test

Evaluate rules against a simulated tool invocation.

```bash
mneme rule test --tool Edit --path vendor/foo/bar.go
mneme rule test --tool Write --path internal/store/memory.go --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--tool` | required | Tool name |
| `--path` | | File path |
| `--json` | false | JSON output |

---

### mneme sync export

Export memories to a compressed archive.

```bash
mneme sync export
mneme sync export --format manifest
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | cwd | Output directory |
| `--format` | `jsonl` | `jsonl` or `manifest` |

---

### mneme sync import

Import memories from a sync archive.

```bash
mneme sync import .mneme/sync/my-project.jsonl.gz
mneme sync import .mneme/sync/my-project.manifest.tar.gz
```

Format is auto-detected from the file extension.

---

### mneme sync status

Show sync status for the current project.

```bash
mneme sync status
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | cwd | Directory containing the manifest |

---

### mneme backlog add

Add a new backlog item.

```bash
mneme backlog add "Push notifications" --priority high --description "..."
```

| Flag | Default | Description |
|------|---------|-------------|
| `--description` | | Detailed description |
| `--priority` | `medium` | `critical`, `high`, `medium`, `low` |

---

### mneme backlog list

List backlog items.

```bash
mneme backlog list
mneme backlog list --status refined --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--status` | | `raw`, `refined`, `promoted`, `archived` |
| `--json` | false | JSON output |

---

### mneme backlog refine

Refine a raw backlog item.

```bash
mneme backlog refine BL-001 --refinement "Acceptance criteria..."
```

| Flag | Default | Description |
|------|---------|-------------|
| `--refinement` | required | Refinement content |

---

### mneme backlog promote

Promote a refined backlog item to a spec.

```bash
mneme backlog promote BL-001
```

No flags.

---

### mneme backlog archive

Archive a backlog item with a reason.

```bash
mneme backlog archive BL-002 --reason "Superseded by BL-007"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--reason` | required | Reason for archiving |

---

### mneme spec new

Create a new spec in draft status.

```bash
mneme spec new "SDD Engine"
mneme spec new "Push notifications" --from-backlog BL-003
```

| Flag | Default | Description |
|------|---------|-------------|
| `--from-backlog` | | Link to backlog item |

---

### mneme spec list

List specs for the current project.

```bash
mneme spec list
mneme spec list --status implementing --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--status` | | Status filter |
| `--json` | false | JSON output |

---

### mneme spec status

Show detailed spec status with timeline and pushbacks.

```bash
mneme spec status SPEC-001
mneme spec status SPEC-001 --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

---

### mneme spec advance

Advance a spec to its next lifecycle state.

```bash
mneme spec advance SPEC-001 --by orchestrator
mneme spec advance SPEC-001 --by architect --reason "All gates passed"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--by` | required | Who triggers the advance |
| `--reason` | | Reason for transition |

---

### mneme spec pushback

Register a pushback, moving the spec to `needs_grill`.

```bash
mneme spec pushback SPEC-001 --from backend \
  --questions "API contract?" "Missing dependency?"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--from` | required | Agent raising pushback |
| `--questions` | required | Questions (repeatable, min 1) |

---

### mneme spec resolve

Resolve the oldest pushback on a spec.

```bash
mneme spec resolve SPEC-001 --resolution "Use service accounts"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--resolution` | required | Resolution text |

---

### mneme spec history

Show the full state transition timeline for a spec.

```bash
mneme spec history SPEC-001
mneme spec history SPEC-001 --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

---

### mneme install

Configure an AI coding agent to use mneme.

```bash
mneme install claude-code
mneme install claude-code --dry-run
mneme install claude-code --reinstall-hooks
mneme install claude-code --personal
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | false | Preview changes |
| `--personal` | false | Install personal ecosystem |
| `--force` | false | Overwrite existing files |
| `--source` | config | Personal ecosystem source |
| `--reinstall-hooks` | false | Replace PreToolUse hooks with `mneme hook pre-tool-use` |

---

### mneme init

Migrate a project from legacy workflows to the SDD engine.

```bash
mneme init                  # dry-run
mneme init --apply          # execute (asks confirmation)
mneme init --apply --yes    # execute without prompt
```

| Flag | Default | Description |
|------|---------|-------------|
| `--apply` | false | Execute migration |
| `--yes` | false | Skip confirmation prompt |

---

### mneme embed backfill

Generate embeddings for memories that lack one.

```bash
mneme embed backfill
mneme embed backfill --batch-size 100
```

| Flag | Default | Description |
|------|---------|-------------|
| `--batch-size` | 50 | Memories per batch |

---

### mneme export markdown

Export memories to Markdown files.

```bash
mneme export markdown
mneme export markdown -o memories.md
mneme export markdown --dir /path/to/docs
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output` | `-o` | stdout | Single output file |
| `--dir` | | | One file per type |
| `--scope` | | | Scope filter |
| `--type` | | | Type filter |

---

### mneme vault export

Export memories to Markdown files with YAML frontmatter (Obsidian-compatible).

```bash
mneme vault export
mneme vault export --output /path/to/vault --dry-run
```

| Flag | Default | Description |
|------|---------|-------------|
| `--output` | `~/.mneme/vaults/<slug>` | Vault root directory |
| `--scope` | `project` | Scope filter |
| `--type` | | Type filter |
| `--dry-run` | false | Preview changes |
| `--include-superseded` | false | Include superseded memories |

---

### mneme vault import

Import memories from a vault directory.

```bash
mneme vault import /path/to/vault
```

---

### mneme config show

Inspect resolved configuration with provenance.

```bash
mneme config show
mneme config show graph
mneme config show --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

Valid sections: `storage`, `search`, `context`, `consolidation`, `decay`, `mcp`, `embedding`, `personal`, `workflow`, `delegation`, `spec`, `graph`, `suggestions`.

---

### mneme hook

Run hook handlers (invoked by agent hooks, not by humans).

| Subcommand | Event | Description |
|------------|-------|-------------|
| `session-start` | SessionStart | Load and print project context |
| `session-end` | SessionEnd | Remind agent to call `mem_session_end` |
| `pre-tool-use` | PreToolUse | Evaluate rules against tool invocation |
| `enforce-delegation` | PreToolUse | Legacy config-based delegation (deprecated) |

---

### mneme tui

Launch interactive terminal UI (Bubble Tea).

```bash
mneme tui
```

No flags.

---

### mneme version

Print version and DB schema version.

```bash
mneme version
```

Output: `mneme v0.5.0 (darwin/arm64)` + `DB schema: v10`

---

### mneme upgrade

Upgrade mneme to the latest GitHub release.

```bash
mneme upgrade
mneme upgrade --check
```

| Flag | Default | Description |
|------|---------|-------------|
| `--check` | false | Only check for updates |

---

### CLI Summary Table

| Command | Description | Key flags |
|---------|-------------|-----------|
| `save` | Save a memory | `--title`, `--content`, `--type`, `--topic-key` |
| `search` | Full-text search | `<query>`, `--type`, `--limit`, `--json` |
| `get` | Retrieve by ID | `<id>`, `--json` |
| `update` | Partial update | `<id>`, `--title`, `--content` |
| `forget` | Accelerated decay | `<id>`, `--reason` |
| `status` | Project dashboard | `--json` |
| `stats` | Store statistics | `--json` |
| `consolidate` | Run consolidation | |
| `explore` | Graph BFS | `<seed>`, `--depth`, `--threshold` |
| `gaps` | Knowledge gaps | `--scope`, `--limit`, `--json` |
| `graph rebuild` | Backfill graph | `--dry-run`, `--force` |
| `graph cleanup-orphan-relations` | Remove relations not linked to memories (SPEC-031) | `--apply`, `--yes`, `--also-delete-entities` |
| `rule add` | Create rule | `--title`, `--applies-to`, `--severity` |
| `rule list` | List rules | `--scope`, `--severity`, `--json` |
| `rule test` | Test rules | `--tool`, `--path` |
| `backlog add` | Add idea | `<title>`, `--priority` |
| `backlog list` | List backlog | `--status`, `--json` |
| `backlog refine` | Refine item | `<id>`, `--refinement` |
| `backlog promote` | Promote to spec | `<id>` |
| `backlog archive` | Archive item | `<id>`, `--reason` |
| `spec new` | Create spec | `<title>`, `--from-backlog` |
| `spec list` | List specs | `--status`, `--json` |
| `spec status` | Spec details | `<id>`, `--json` |
| `spec advance` | Advance state | `<id>`, `--by` |
| `spec pushback` | Register pushback | `<id>`, `--from`, `--questions` |
| `spec resolve` | Resolve pushback | `<id>`, `--resolution` |
| `spec history` | State timeline | `<id>`, `--json` |
| `sync export` | Export archive | `--format`, `--dir` |
| `sync import` | Import archive | `<file>` |
| `sync status` | Sync info | `--dir` |
| `install` | Agent setup | `<agent>`, `--dry-run`, `--personal` |
| `init` | Legacy migration | `--apply`, `--yes` |
| `mcp` | MCP server | `--tools` |
| `serve` | HTTP server | `--addr` |
| `embed backfill` | Generate embeddings | `--batch-size` |
| `export markdown` | Markdown export | `--output`, `--dir` |
| `vault export` | Vault mirror | `--output`, `--dry-run` |
| `vault import` | Vault import | `<path>` |
| `config show` | Show config | `[section]`, `--json` |
| `hook` | Agent hooks | `<event>` |
| `tui` | Terminal UI | |
| `version` | Print version | |
| `upgrade` | Self-update | `--check` |
