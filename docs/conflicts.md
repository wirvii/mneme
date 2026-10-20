# Memory Conflict Surfacing (v1.9.0)

> SPEC-039. Closes "the agent followed a decision we already changed."

## Overview

Memory conflict surfacing is a two-phase workflow:

1. **Detection (deterministic, FTS5):** find candidate memories that share
   salient terms with a given memory. No LLM. No network. Purely local.

2. **Judgment (LLM via subprocess, explicit):** classify each candidate pair
   using the local Claude CLI (`claude -p --output-format json`). Free on
   subscription plans. Must be run explicitly — never automatic on save.

### 1.1 Why the judgment step uses an LLM

SPEC-035 (lane auditor) and SPEC-037 (skills linter) are deterministic because
their questions are **structural**. Judging whether two pieces of knowledge
contradict each other is **semantic** — no deterministic algorithm can reliably
answer it. The distinction is preserved in code: `internal/conflicts/detect.go`
(deterministic) vs `internal/conflicts/judge.go` (subprocess LLM).

## Relation model

| Relation       | Storage                 | Effect on retrieval                     |
|----------------|-------------------------|-----------------------------------------|
| `supersedes`   | `memories.superseded_by`| B excluded from search by default       |
| `conflicts_with` | `memory_relations`    | Both surfaced; ConflictsWith annotation |
| `unrelated`    | `memory_relations`      | Negative cache; no retrieval effect     |

`supersedes` reuses the existing `superseded_by` column and the retrieval
exclusion that already works. `conflicts_with` and `unrelated` use the new
`memory_relations` table (migration 013).

## Usage

### Step 1: Find candidates (deterministic)

```bash
mneme conflicts candidates <memory-id>
mneme conflicts candidates <memory-id> --limit 10
```

Also available as MCP tool `conflicts_candidates`.

After every `mem_save`, a non-blocking background hint logs to `slog.Info`
when FTS5 candidates exist (`conflict_hint` event). The save itself never
blocks or fails.

### Step 2: Judge candidates (LLM subprocess)

```bash
# Dry-run (default): prints results, does not persist
mneme conflicts scan

# Apply: persists judged relations
mneme conflicts scan --apply
mneme conflicts scan --project my-project --limit 10 --apply
```

When the Claude CLI is not installed:
- CLI: prints an actionable message and exits 1. No API call.
- MCP: returns `IsError:true` with `{error, suggestion}` payload. No protocol error.

### Override manual links

```bash
# Create a relation manually (overrides CLI judgment)
mneme conflicts link <from-id> <to-id> supersedes --rationale "New auth design"
mneme conflicts link <from-id> <to-id> conflicts_with
mneme conflicts link <from-id> <to-id> unrelated

# Remove a relation
mneme conflicts unlink <from-id> <to-id>

# List all relations
mneme conflicts list
mneme conflicts list --project my-project --json
```

## Retrieval integration

`supersedes` relations are already handled: `memories.superseded_by IS NOT NULL`
memories are excluded from search by default (pass `include_superseded:true` to
override).

`conflicts_with` triggers the `annotateConflicts` post-ranking pass in
`service/search.go`. After the final sort and truncation, each result's
`ConflictsWith []string` field is populated with the IDs of conflict partners
that appear in the same result page. The results are not reordered or filtered.

`unrelated` has no retrieval effect; it only acts as a negative cache to prevent
re-judging the same pair.

## Architecture notes

- `internal/conflicts/` is a **leaf package**: no imports of `internal/model`
  or `internal/store`. Pure stdlib.
- `detect.go` (deterministic) vs `judge.go` (subprocess LLM) are intentionally
  separate files to preserve the invariant that only judgment uses an LLM.
- `model.ErrCLIUnavailable` and `model.ErrInvalidRelation` are the sentinel
  errors added in this feature.
- `store.ClearSupersededBy` is the inverse of `store.SetSupersededBy`, used by
  `ConflictUnlink`.

## MCP tools (51→56)

| Tool                  | Description                                      |
|-----------------------|--------------------------------------------------|
| `conflicts_candidates`| FTS5 candidate IDs for a memory (deterministic) |
| `conflicts_scan`      | Judge pairs via Claude CLI subprocess            |
| `conflicts_link`      | Manual relation creation                         |
| `conflicts_unlink`    | Remove a relation                                |
| `conflicts_list`      | List all edges for a project                     |

## Judging at save time (SPEC-166)

`mem_save` no longer only logs a hint. Right after the upsert it returns up to
three `possible_conflicts` (`id`, `title`, `type`, `updated_at`): existing
memories that share salient terms with the new one. The lookup is the same
deterministic FTS5 query `conflicts candidates` uses, with no LLM. It already
skips the memory itself, deleted and superseded ones, `session_summary` and
`synthesis`, and any pair judged before (so an `unrelated` verdict is never
proposed again). A failure in the lookup is logged and the save still succeeds.

The agent judges in the same turn with `mem_update` on the NEW memory (no new
tool):

| Field | Effect |
|-------|--------|
| `supersedes: [id]` | The older memory is hidden from search (`superseded_by = new id`), never deleted |
| `conflicts_with: [id]` | A `conflicts_with` row in `memory_relations`; both stay visible |
| `unrelated_to: [id]` | An `unrelated` row: stops being proposed |

Rules: every target must exist, differ from the memory itself, appear in only
one list, and live in the same store (project vs global); otherwise nothing is
written (`ErrInvalidRelation`, `ErrNotFound`, `ErrCrossStoreRelation`). A
request that carries only these lists does not count as a revision (no
`revision_count` bump, no `updated_at` change). The response lists what was
recorded in `relations_applied`. Rows are stamped `judged_by = "agent"`
(`manual` for `conflicts link`, `claude-cli` for `conflicts scan`).
Everything is reversible with `mneme conflicts unlink <a> <b>`.
`mneme update <id> --supersedes ID --conflicts-with ID --unrelated-to ID`
(each repeatable) does the same from the shell. `conflicts scan` remains for
older memories that were never judged at save time.

## Anti-scope

- No auto-delete or auto-edit of memories.
- No automatic judgment on save: `mem_save` only proposes candidates (SPEC-166); the saving agent judges.
- No embeddings/vector similarity.
- No metered API calls. CLI absent → report and skip.
- No changes to allowlists, hooks, SDD, lane, skills, or models.

---

## API reference

Full contract (params, returns, errors, examples) for the 5 `conflicts_*` MCP
tools: [docs/api/conflicts.md](api/conflicts.md) →
