# API Reference — Conflicts Tools (`conflicts_*`)

5 MCP tools over JSON-RPC 2.0 stdio (`mneme mcp`). Concept guide:
[docs/conflicts.md](../conflicts.md) (two-phase workflow, relation types,
anti-scope). Index: [docs/API.md](../API.md).

Two-phase workflow: (1) deterministic FTS5 candidate detection — no LLM, $0
cost, always safe to run; (2) optional LLM judgment via a `claude` CLI
subprocess ($0 cost on subscription, but requires the CLI on `PATH`). Judgment
is **never automatic** — `conflicts_scan` is dry-run by default.

---

## conflicts_candidates

Find candidate memories that may conflict with the given memory using
deterministic FTS5 term matching. No LLM involved. Use `conflicts_scan` to
judge candidates.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Memory ID to find conflict candidates for |
| `limit` | integer | no | Maximum number of candidates to return. Default: 5 |

**Returns:** `{"id": "<memory-id>", "candidates": ["<id1>", "<id2>"], "count": 2}`

**Errors:** `-32602` missing `id`. `-32000` memory not found.

**Example:** `mneme conflicts candidates 01910000-0000-7000-8000-000000000001 --limit 10`

---

## conflicts_scan

Scan memories for conflicts using the local Claude CLI as judge (subprocess,
$0 cost). Dry-run by default — set `apply: true` to persist judgments. Already
judged pairs (in `memory_relations` or `superseded_by`) are skipped.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `project` | string | no | Project slug to scan. Default: auto-detected |
| `limit` | integer | no | Maximum number of candidate pairs to judge. Default: 5, max 10 |
| `apply` | boolean | no | When `true`, persist judged relations. Default: `false` (dry-run) |

**Returns:** `ConflictScanResponse`: `{"pairs": [{"memory_a": "...", "memory_b": "...", "title_a": "...", "title_b": "...", "relation": "conflicts_with", "rationale": "...", "skipped": false}], "applied": false, "total": 3, "errors": 0}`

**When the Claude CLI is unavailable:** returns `IsError: true` with a
structured payload (`{"error": "...", "suggestion": "Install the Claude CLI ..."}`)
rather than a protocol error — no metered API call is made.

**Errors:** `-32603` scan failure unrelated to CLI absence.

**Example:** `mneme conflicts scan --apply --limit 10`

---

## conflicts_link

Manually create a relation between two memories. Relation must be one of:
`supersedes`, `conflicts_with`, `unrelated`. Manual links always win over
CLI-judged links.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `from_id` | string | yes | Source memory ID |
| `to_id` | string | yes | Target memory ID |
| `relation` | string | yes | `supersedes` (from supersedes to), `conflicts_with`, or `unrelated` |
| `rationale` | string | no | Optional one-line explanation for the relation |

**Returns:** `{"from_id": "...", "to_id": "...", "relation": "supersedes", "status": "linked"}`

**Errors:** `-32602` missing `from_id`/`to_id`/`relation`, invalid relation value. `-32000` memory not found.

**Example:** `mneme conflicts link mem-abc mem-def supersedes --rationale "Updated auth design"`

---

## conflicts_unlink

Remove a memory relation between two memories (in either direction). Also
clears `superseded_by` when applicable.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `from_id` | string | yes | First memory ID of the pair |
| `to_id` | string | yes | Second memory ID of the pair |

**Returns:** `{"from_id": "...", "to_id": "...", "status": "unlinked"}`

**Errors:** `-32602` missing `from_id`/`to_id`. `-32000` relation not found.

---

## conflicts_list

List all memory conflict relations (`conflicts_with` and `unrelated` edges)
for the given project. `supersedes` relations are stored as
`memories.superseded_by` and are **not** listed here — use `mem_search` with
`include_superseded: true` to see those.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `project` | string | no | Project slug to filter results. Default: auto-detected |

**Returns:** `{"relations": [{"from_id": "...", "to_id": "...", "relation": "conflicts_with", "judged_by": "claude-cli", "rationale": "...", "created_at": "..."}], "count": 1}`

**Errors:** `-32603` query failure.

---

## Anti-scope (deliberate non-goals)

No auto-delete or auto-edit of memories. No automatic judgment on save (only a
hint/log). No embeddings/vector similarity used for detection (FTS5 only). No
metered API calls — `conflicts_scan` reports and skips when the CLI is
missing. No changes to allowlists, hooks, SDD, lane, skills, or models.

## Error codes

| Code | Name | Triggered when |
|------|------|----------------|
| `-32602` | Invalid params | Missing required IDs, invalid relation value |
| `-32603` | Internal error | Query/persist failure unrelated to CLI availability |
| `-32000` | Not found | Unknown memory ID or relation |

## See also

- [docs/conflicts.md](../conflicts.md) — detection algorithm, judgment prompt, `memory_relations` schema (migration 013)
- [docs/api/cli.md](cli.md) — `mneme conflicts candidates/scan/link/unlink/list`
