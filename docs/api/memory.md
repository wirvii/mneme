# API Reference — Memory Tools (`mem_*`)

6 MCP tools over JSON-RPC 2.0 stdio (`mneme mcp`). Concept guide:
[docs/GRAPH.md](../GRAPH.md) (graph/relations), [docs/RULES.md](../RULES.md)
(rule type, `applies_to`, severity), [docs/team-memory.md](../team-memory.md)
(`mneme mem promote`, git-native shared vault). Index: [docs/API.md](../API.md).

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

When `topic_key` is omitted (and `type` is not `session_summary`/`synthesis`)
the response also carries `suggested_topic_key` (SPEC-164): an advisory
proposal built from the title, existing keys and unresolved knowledge gaps.
mneme never assigns it; reuse it on the next save of the same topic. This
replaces the retired `mem_suggest_topic_key` tool.

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
| `partial` | boolean | no | `true` saves a checkpoint (`checkpoint/latest`, one per project, overwritten each time) and leaves the session open. Use it periodically on long tasks to survive context compaction |
| `decisions` | string | no | Only read when `partial` is true |
| `next_steps` | string | no | Only read when `partial` is true |

**Returns:** `{"id": "019de100-...", "session_id": "sess-abc", "status": "saved"}`. With `partial: true`:
`{"session_id": "...", "summary_memory_id": "<checkpoint id>", "partial": true}` (no `memories_created`
or `session_duration`, no `sessions` row, no `session/<id>` summary).

**Errors:** `-32602` missing `summary`.

---

## Where the other nine tools went (SPEC-164)

| Retired MCP tool | Now |
|------------------|-----|
| `mem_suggest_topic_key` | `suggested_topic_key` in the `mem_save` response |
| `mem_checkpoint` | `mem_session_end` with `partial: true` |
| `mem_stats` | `mneme mem stats` (alias `mneme stats`) |
| `mem_forget` | `mneme mem forget` (alias `mneme forget`) |
| `mem_promote` | `mneme mem promote` (alias `mneme promote`) |
| `mem_gaps` | `mneme mem gaps` (alias `mneme gaps`) |
| `mem_explore` | `mneme mem explore` (alias `mneme explore`) |
| `mem_timeline` | `mneme mem timeline` |
| `mem_relate` | `mneme mem relate` |

Calling a retired name over MCP returns `-32601` (`unknown tool: <name>`).
The service methods are unchanged, and HTTP keeps its endpoints
(`/v1/entities/relate`, `/v1/stats`, `/v1/gaps`, `/v1/memories/{id}/explore`).
See [docs/api/cli.md](cli.md#mneme-mem) for the CLI parameters.

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
