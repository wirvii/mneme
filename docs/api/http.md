# API Reference — HTTP API

**Start:** `mneme serve --addr :7437` · **Base URL:** `http://localhost:7437/v1`
**Content-Type:** `application/json` (all requests and responses) · **Auth:** none (local, single-user)

10 route registrations in `internal/http/server.go` (`registerRoutes`), one of
which (`/v1/memories/{id}`) also serves the `/explore` suffix. Concept guide:
none dedicated — this is the transport reference; see [docs/api/memory.md](memory.md)
for the underlying `mem_*` semantics each route wraps. Index: [docs/API.md](../API.md).

### Error envelope

```json
{"error": {"code": "not_found", "message": "memory with id ... not found"}}
```

Error codes: `not_found` (404), `invalid_request` (400), `invalid_json` (400), `method_not_allowed` (405), `internal_error` (500).

---

## GET /v1/health

Health check. Returns 200 unconditionally.

```bash
curl http://localhost:7437/v1/health
```

```json
{"status": "ok"}
```

---

## POST /v1/memories

Save a new memory (or upsert by `topic_key`). Request body: same schema as `mem_save` ([docs/api/memory.md](memory.md#mem_save)).

```bash
curl -X POST http://localhost:7437/v1/memories \
  -H 'Content-Type: application/json' \
  -d '{"title": "Auth uses JWT RS256", "content": "RS256 with 2048-bit keys...", "type": "decision", "topic_key": "architecture/auth-model"}'
```

**Response:** `201 Created`

```json
{"id": "019de100-...", "title": "Auth uses JWT RS256", "action": "created", "topic_key": "architecture/auth-model"}
```

## GET /v1/memories/{id}

```bash
curl http://localhost:7437/v1/memories/019de100-abcd-7fff-8000-000000000001
```

**Response:** `200 OK` -- full Memory object. **Errors:** `404` not found.

## PATCH /v1/memories/{id}

Partial update. Request body: same schema as `mem_update` (minus `id`).

```bash
curl -X PATCH http://localhost:7437/v1/memories/019de100-... \
  -H 'Content-Type: application/json' -d '{"title": "Auth uses JWT RS256 (updated)"}'
```

**Response:** `200 OK` -- updated Memory object.

## DELETE /v1/memories/{id}

Mark a memory for accelerated decay. Body is optional.

```bash
curl -X DELETE http://localhost:7437/v1/memories/019de100-... \
  -H 'Content-Type: application/json' -d '{"reason": "Outdated after v2 migration"}'
```

**Response:** `200 OK` -- `{"status": "forgotten", "id": "019de100-..."}`

---

## GET /v1/memories/search

Full-text search with optional graph expansion. Same shape as `mem_search`.

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

## GET /v1/memories/context

Contextual memories with token budgeting. Same shape as `mem_context`.

| Query param | Type | Required | Description |
|-------------|------|----------|-------------|
| `project` | string | no | Project slug |
| `budget` | integer | no | Token budget |
| `focus` | string | no | Focus topic |
| `include_graph` | boolean | no | `true`/`false`/`1`/`0` |

```bash
curl 'http://localhost:7437/v1/memories/context?focus=auth&budget=4000'
```

## GET /v1/memories/{id}/explore

Explore the knowledge graph from a seed memory. Same shape as `mem_explore`. Served by the same handler as the `/v1/memories/{id}` catch-all.

| Query param | Type | Required | Description |
|-------------|------|----------|-------------|
| `depth` | integer | no | Max hops (0-5). Default: 2 |
| `budget` | integer | no | Token budget. Default: 4000 |
| `threshold` | float | no | Min weight (0.0-1.0). Default: 0.3 |

```bash
curl 'http://localhost:7437/v1/memories/019de100-.../explore?depth=3'
```

---

## POST /v1/sessions/end

End the current session and save a summary. Request body: same schema as `mem_session_end`.

```bash
curl -X POST http://localhost:7437/v1/sessions/end \
  -H 'Content-Type: application/json' -d '{"summary": "Implemented auth middleware"}'
```

**Response:** `200 OK`

## POST /v1/entities/relate

Create or update a relationship between entities. Request body: same schema as `mem_relate`.

```bash
curl -X POST http://localhost:7437/v1/entities/relate \
  -H 'Content-Type: application/json' \
  -d '{"source": "internal/store", "target": "internal/db", "relation": "depends_on"}'
```

**Response:** `201 Created` (new) or `200 OK` (existing).

## GET /v1/stats

Aggregate statistics. Same shape as `mem_stats`.

| Query param | Type | Required | Description |
|-------------|------|----------|-------------|
| `project` | string | no | Project slug. Empty for global |

```bash
curl http://localhost:7437/v1/stats
```

## GET /v1/gaps

Knowledge gaps (unresolved wikilinks). Same shape as `mem_gaps`.

| Query param | Type | Required | Description |
|-------------|------|----------|-------------|
| `project` | string | no | Project slug |
| `scope` | string | no | `project`, `global`, `all` |
| `limit` | integer | no | Max gaps |
| `min_mentions` | integer | no | Min mentions threshold |

```bash
curl 'http://localhost:7437/v1/gaps?scope=all&limit=10'
```

## POST /v1/consolidate

Run the consolidation pipeline synchronously.

```bash
curl -X POST http://localhost:7437/v1/consolidate
```

```json
{"swept": 3, "hard_deleted": 1, "duplicates": 0, "evicted": 2, "edge_decayed": 5,
 "communities_detected": 8, "communities_new": 2, "communities_deleted": 1,
 "synthesis_created": 2, "synthesis_updated": 1, "synthesis_deleted": 0, "synthesis_skipped": 5}
```

---

## HTTP parity gaps

The HTTP API is intentionally **behind** MCP and CLI — this is a documented
gap, not an oversight (tracked in the README Roadmap). The following tool
families have **no HTTP route at all**:

| Family | MCP tools | HTTP route | Status |
|--------|-----------|------------|--------|
| `mem_checkpoint` | 1 | -- | Not implemented |
| `mem_timeline` | 1 | -- | Not implemented |
| `mem_suggest_topic_key` | 1 | -- | Not implemented |
| `backlog_*` | 4 | -- | Not implemented |
| `spec_*` | 8 | -- | Not implemented |
| `lane_*` | 5 | -- | Not implemented |
| `codegraph_*` | 10 | -- | Not implemented |
| `skills_*` | 7 | -- | Not implemented |
| `model_*` | 3 | -- | Not implemented |
| `conflicts_*` | 5 | -- | Not implemented |
| `init` | 1 | -- | Not implemented |

Before adding a new service capability, decide explicitly whether HTTP gets
parity -- do not assume it should.

## See also

- [docs/api/memory.md](memory.md) -- underlying `mem_*` tool semantics for routes above
- [docs/API.md](../API.md) -- index of all reference pages
