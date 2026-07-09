# API Reference — Memory Tools (`mem_*`)

15 MCP tools over JSON-RPC 2.0 stdio (`mneme mcp`). Concept guide:
[docs/GRAPH.md](../GRAPH.md) (graph/relations), [docs/RULES.md](../RULES.md)
(rule type, `applies_to`, severity), [docs/team-memory.md](../team-memory.md)
(`mem_promote`, git-native shared vault). Index: [docs/API.md](../API.md).

All responses are returned as a single JSON-encoded `text` content block. See
[MCP Error Codes](#error-codes) at the bottom for the shared error code table.

---

## mem_save

Save a structured observation to persistent memory. If `topic_key` matches an
existing memory in the same project/scope, the existing memory is updated
(upsert) instead of creating a duplicate.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `title` | string | yes | Short, searchable summary of the memory |
| `content` | string | yes | Full knowledge body, typically structured Markdown |
| `type` | string | no | `decision`, `discovery`, `bugfix`, `pattern`, `preference`, `convention`, `architecture`, `config`, `session_summary`, `rule`, `synthesis`. Default: `discovery` |
| `scope` | string | no | `global`, `org`, `project`. Default: `project` |
| `topic_key` | string | no | Stable dot-delimited key enabling idempotent upserts (e.g. `architecture/auth-model`) |
| `project` | string | no | Project slug. Default: auto-detected from git remote |
| `session_id` | string | no | Agent session ID to associate this memory with |
| `created_by` | string | no | Identifier of the saving agent (e.g. `claude-code`) |
| `files` | string[] | no | Source file paths related to this memory |
| `importance` | number | no | Initial importance (0.0-1.0). Default: type-based |
| `applies_to` | string[] | no | Rule patterns. **Required** when `type` is `rule`. Supports path globs (`internal/**/*.go`), tool selectors (`tool:Edit`), combined (`tool:Edit+internal/**`), negations (`!docs/**`), and global wildcard (`**`) |
| `severity` | string | no | `info`, `warn`, `block`. Default: `warn` (ignored for non-rule types) |

**Returns:**

```json
{"id": "019de100-abcd-7fff-8000-000000000001", "title": "Auth uses JWT RS256", "action": "created", "topic_key": "architecture/auth-model"}
```

`action` is `"created"` or `"updated"` (upsert).

**Errors:** `-32602` missing `title`/`content`, invalid `type`/`scope`/`severity`, `applies_to` required when `type=rule`.

**Example:**

```bash
mneme save --type decision --title "Auth uses JWT RS256" \
  --content "Switched to RS256 for asymmetric key verification." \
  --topic-key architecture/auth-model
```

---

## mem_search

Search persistent memory using full-text search (BM25) with optional 1-hop
graph expansion and vector similarity fusion (RRF).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | yes | Full-text search query string |
| `project` | string | no | Restrict results to this project slug |
| `scope` | string | no | `global`, `org`, `project` |
| `type` | string | no | Filter by memory type |
| `limit` | integer | no | Max results (1-50). Default: configured default limit |
| `include_superseded` | boolean | no | Include memories superseded by newer versions. Default: `false` |
| `include_graph` | boolean | no | Enable 1-hop graph expansion. Default: `true` |

**Returns:**

```json
{
  "results": [
    {"id": "019de100-...", "title": "Auth uses JWT RS256", "content": "...", "type": "decision",
     "scope": "project", "project": "wirvii/mneme", "importance": 0.9, "confidence": 0.8,
     "created_at": "2026-04-30T02:43:17Z", "updated_at": "2026-04-30T02:43:17Z",
     "relevance_score": 12.5, "bm25_score": -14.2, "vector_score": 0.45}
  ],
  "total": 1,
  "query": "JWT RS256"
}
```

**Errors:** `-32602` missing `query`, invalid `type`/`scope`.

**Example:** `mneme search "authentication" --type decision --limit 5`

---

## mem_get

Retrieve the full content of a memory by ID. Increments the access counter
(feeds Hebbian auto-strengthening).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | UUIDv7 of the memory to retrieve |

**Returns:** Full `Memory` object (same fields as a search result, plus
`files`, `revision_count`, `access_count`, `decay_rate`).

**Errors:** `-32000` memory not found, `-32602` missing `id`.

**Example:** `mneme get 019de100-abcd-7fff-8000-000000000001`

---

## mem_context

Get the most relevant memories for the current project context. Rules are
always injected first with a separate token budget, then scored memories
(BM25 + vector + graph, community-packed when enabled).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `project` | string | no | Project slug. Default: auto-detected |
| `budget` | integer | no | Maximum token budget for returned memories (min 1). Default: config value |
| `focus` | string | no | Optional topic or question that biases memory selection |
| `include_graph` | boolean | no | Enable graph expansion for focus matching (+0.3 boost via PPR or 1-hop). Default: `true` |

**Returns:**

```json
{
  "project": "wirvii/mneme", "memories": [ ... ], "token_estimate": 3500,
  "total_available": 142, "included": 12, "last_session": "Summary of last session...",
  "active_rules": [ ... ], "cluster_overviews": [ ... ], "cluster_overviews_count": 3,
  "cluster_overviews_tokens": 900, "top_cluster": "community-uuid", "top_cluster_members": 5,
  "packing_mode": "communities"
}
```

`cluster_overviews*`, `top_cluster*`, and `packing_mode` are omitted when
community packing is inactive (flat mode).

**Errors:** `-32602` invalid params.

---

## mem_update

Update an existing memory by ID. Only the fields provided are changed.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | UUIDv7 of the memory to update |
| `title` | string | no | New title |
| `content` | string | no | New content |
| `type` | string | no | New memory type (excludes `rule`, `synthesis`) |
| `importance` | number | no | New importance (0.0-1.0) |
| `confidence` | number | no | New confidence (0.0-1.0) |
| `files` | string[] | no | Replacement list of associated source file paths |

**Returns:** Full updated `Memory` object.

**Errors:** `-32000` not found, `-32602` invalid `type`/params.

---

## mem_session_end

End the current session and save a summary.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `summary` | string | yes | Human-readable description of what was accomplished this session |
| `session_id` | string | no | Session ID to close. Generated when omitted |
| `project` | string | no | Project slug. Default: auto-detected |

**Returns:** `{"id": "019de100-...", "session_id": "sess-abc", "status": "saved"}`

**Errors:** `-32602` missing `summary`.

---

## mem_suggest_topic_key

Suggest a `topic_key` for a new memory. Searches existing topic keys and
unresolved knowledge gaps for the best match. Gap matches signal that the
project already needs this key.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `title` | string | yes | Title of the memory for which to suggest a topic key |
| `project` | string | no | Project slug used to search for existing similar keys and gaps |

**Returns:**

```json
{"suggestions": [
  {"key": "architecture/auth-model", "score": 0.85, "source": "existing"},
  {"key": "decision/retry-strategy", "score": 0.72, "source": "gap"}
]}
```

**Errors:** `-32602` missing `title`.

---

## mem_relate

Create or update a relationship between two graph endpoints. Each endpoint
string is resolved in order: (1) memory UUID full or 8+ hex prefix, (2) memory
`topic_key` (only when the corresponding `*_kind` is omitted or `concept`),
(3) entity name (created with `kind` when missing). When resolution lands on a
memory, a proxy entity is auto-created and linked via `memory_entities` so the
relation is reachable from `mem_explore`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `source` | string | yes | Source endpoint: memory UUID, UUID prefix (8+ hex), topic_key, or entity name |
| `target` | string | yes | Target endpoint: same resolution semantics as `source` |
| `relation` | string | yes | `depends_on`, `implements`, `supersedes`, `related_to`, `part_of`, `uses`, `conflicts_with`, `references` |
| `source_kind` | string | no | `module`, `service`, `library`, `concept`, `person`, `pattern`, `file`. Default: `concept` (enables topic_key resolution) |
| `target_kind` | string | no | Same enum. Default: `concept` |
| `project` | string | no | Project slug. Default: auto-detected |
| `weight` | number | no | Override the default weight for this relation type (0.0-1.0). Type defaults: `depends_on` 0.9, `part_of` 0.85, `implements` 0.8, `uses`/`conflicts_with` 0.7, `supersedes` 0.6, `related_to` 0.5, `references` 0.4 |

**Returns:**

```json
{"relation_id": "019df133-...", "source_id": "019df115-...", "target_id": "019df116-...", "created": true, "weight": 0.9}
```

`created` is `true` for new relations, `false` when an identical relation
already existed (idempotent).

**Errors:** `-32602` invalid relation type, entity kind, or weight; `-32603`
cross-scope relation rejected (global/org → project memory).

---

## mem_timeline

Get memories around a specific point in time, ordered chronologically.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `around` | string | yes | A memory UUID or ISO 8601 timestamp to use as the centre of the window |
| `project` | string | no | Project slug. Default: auto-detected |
| `window` | string | no | Time range (e.g. `7d`, `24h`, `30d`). Default: `7d` |
| `limit` | integer | no | Max results (1-100). Default: 20 |

**Returns:** `{"memories": [ ... ], "center": "2026-04-25T12:00:00Z", "window": "7d"}`

---

## mem_stats

Return aggregate statistics about the project memory store.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `project` | string | no | Project slug. Default: auto-detected. Pass empty string for global stats |

**Returns:**

```json
{
  "project": "wirvii/mneme", "total_memories": 142, "active": 130, "superseded": 8, "forgotten": 4,
  "by_type": {"decision": 25, "discovery": 40}, "by_scope": {"project": 120, "global": 22},
  "embeddings_count": 130, "knowledge_gaps": {"total": 5, "top": [...]},
  "db_size_bytes": 524288, "oldest_memory": "2026-04-01T00:00:00Z",
  "newest_memory": "2026-04-30T12:00:00Z", "avg_importance": 0.72
}
```

---

## mem_checkpoint

Save a checkpoint of the current work state. Call periodically during long
tasks to prevent knowledge loss on context compaction. Overwrites the previous
checkpoint automatically.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `summary` | string | yes | Brief summary of current work state and progress |
| `decisions` | string | no | Decisions made since last checkpoint or session start |
| `next_steps` | string | no | What needs to happen next if the context is lost |
| `project` | string | no | Project slug. Default: auto-detected |

**Returns:** `{"id": "019de100-...", "status": "saved", "action": "created"}`

**Errors:** `-32602` missing `summary`.

---

## mem_forget

Mark a memory for accelerated decay. Sets its decay rate to 1.0 so importance
drops to near zero on the next scoring pass.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | UUIDv7 of the memory to forget |
| `reason` | string | no | Optional reason why the memory should be forgotten |

**Returns:** `{"id": "019de100-...", "status": "marked_for_decay"}`

**Errors:** `-32000` not found, `-32602` missing `id`.

---

## mem_promote

Mark a memory as team-curated (`shared=2`) and persist it in the database.
Materializes it to the shared git vault immediately when
[team-memory](../team-memory.md) is active for the current repository.
Idempotent — promoting an already-promoted memory is a no-op beyond
re-confirming `shared=2`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | UUIDv7 of the memory to promote |

**Returns:** `{"id": "019de100-...", "shared": 2, "author": "Jane Dev <jane@example.com>", "status": "promoted"}`

**Errors:** `-32602` missing `id` or unknown `id` (unlike other tools, an
unresolved ID here is reported as invalid params, not `-32000` — the caller
supplied a bad argument rather than a query that legitimately found nothing).

**Example:** `mneme promote 019de100-abcd-7fff-8000-000000000001`

---

## mem_explore

Explore the knowledge graph starting from a seed memory. Performs a
prioritised BFS traversal following strong relations, returning connected
memories with their distance and accumulated path weight.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `seed` | string | yes | Full UUID, short UUID prefix (8+ hex chars), or topic_key |
| `depth` | integer | no | Maximum hops from seed (0-5). Default: 2 |
| `budget` | integer | no | Maximum token budget for returned memories. Default: 4000 |
| `threshold` | number | no | Minimum relation weight to follow (0.0-1.0). Default: 0.3 |
| `project` | string | no | Project slug. Default: auto-detected |

**Returns:**

```json
{
  "seed_id": "019de100-...", "seed_title": "Architecture: Auth model",
  "nodes": [
    {"memory_id": "019de101-...", "parent_memory_id": "019de100-...", "title": "JWT token rotation",
     "topic_key": "security/jwt-rotation", "type": "decision", "distance": 1,
     "accumulated_weight": 0.9, "relation_type": "depends_on", "token_estimate": 245}
  ],
  "total_nodes": 5, "tokens_used": 1111, "max_depth_reached": 2
}
```

**Errors:** `-32602` missing `seed`, ambiguous seed (matches multiple), invalid depth. `-32000` seed not found.

---

## mem_gaps

List knowledge gaps — unresolved `[[wikilink]]` references. Shows topic_keys
that are mentioned in memories but don't have a corresponding memory yet.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `scope` | string | no | Query scope: `project` (default), `global`, `all` |
| `limit` | integer | no | Max gaps (1-100). Default: 20 |
| `min_mentions` | integer | no | Minimum `total_mentions` to include a gap. Default: 1 |
| `include_samples` | boolean | no | Include sample source memories for each gap. Default: `true` |
| `project` | string | no | Project slug. Default: auto-detected |

**Returns:**

```json
{
  "gaps": [
    {"target_topic_key": "decision/retry-strategy", "total_mentions": 5, "source_count": 3,
     "first_seen_at": "2026-04-20T10:00:00Z", "last_seen_at": "2026-04-30T14:00:00Z",
     "urgency_score": 0.8, "sample_sources": [...]}
  ],
  "total": 12
}
```

---

## Error codes

Shared across all MCP tool families (see [docs/API.md](../API.md) for the
full JSON-RPC transport reference):

| Code | Name | Triggered when |
|------|------|----------------|
| `-32600` | Invalid Request | Malformed JSON-RPC envelope |
| `-32601` | Method not found | Unknown MCP method |
| `-32602` | Invalid params | Missing required params, type mismatch, domain validation failure |
| `-32603` | Internal error | DB error, unexpected failure, dependent service unavailable |
| `-32000` | Not found | Unknown memory/entity/relation/backlog/spec/skill ID |

## See also

- [docs/GRAPH.md](../GRAPH.md) — relation weights, Hebbian learning, edge decay, `mem_explore` internals
- [docs/RULES.md](../RULES.md) — the `rule` memory type, `applies_to` syntax, severity, `pre-tool-use` hook
- [docs/api/sdd.md](sdd.md) — backlog/spec/lane/init tools
