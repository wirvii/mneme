# API Reference — Conflicts (CLI only since v2.0)

The five `conflicts_*` MCP tools (SPEC-039) left the MCP server in v2.0
(SPEC-163). The capability is unchanged and lives in `mneme conflicts`
([docs/api/cli.md](cli.md#conflicts)). Calling a former tool name over MCP
now answers `-32601` `unknown tool: <name>`. Concept guide:
[docs/conflicts.md](../conflicts.md) (two-phase workflow, relation types,
anti-scope). Index: [docs/API.md](../API.md).

Two-phase workflow: (1) deterministic FTS5 candidate detection — no LLM, $0
cost, always safe to run; (2) optional LLM judgment via a `claude` CLI
subprocess ($0 cost on subscription, but requires the CLI on `PATH`). Judgment
is **never automatic** — `mneme conflicts scan` is dry-run by default.

## Retired tool → CLI command

| Retired MCP tool | CLI command | Notes |
|---|---|---|
| `conflicts_candidates` | `mneme conflicts candidates <id> [--limit N]` | FTS5 candidate IDs for one memory; deterministic |
| `conflicts_scan` | `mneme conflicts scan [--apply] [--limit N] [--project P]` | Dry-run unless `--apply`; already-judged pairs are skipped; when the `claude` CLI is missing it reports and skips (no metered API call) |
| `conflicts_link` | `mneme conflicts link <from> <to> <relation> [--rationale R]` | `relation` is `supersedes`, `conflicts_with` or `unrelated`; manual links always win over judged ones |
| `conflicts_unlink` | `mneme conflicts unlink <from> <to>` | Removes the relation in either direction; clears `superseded_by` when applicable |
| `conflicts_list` | `mneme conflicts list [--project P] [--json]` | Lists every `conflicts_with`/`unrelated` edge. There is no `limit`: the CLI lists them all, a superset of the old capped tool. `supersedes` lives in `memories.superseded_by` and is not listed — use `mem_search` with `include_superseded: true` |

## Anti-scope (deliberate non-goals)

No auto-delete or auto-edit of memories. No automatic judgment on save (only a
hint/log). No embeddings/vector similarity used for detection (FTS5 only). No
metered API calls — `mneme conflicts scan` reports and skips when the CLI is
missing.
