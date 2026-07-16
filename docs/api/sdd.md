# API Reference — SDD Tools (`backlog_*`, `spec_*`, `lane_*`, `init`)

19 MCP tools over JSON-RPC 2.0 stdio (`mneme mcp`): `backlog_*` (4), `spec_*`
(9), `lane_*` (5), `init` (1). Concept guide: [docs/lanes.md](../lanes.md)
(trivial/standard lanes, auditor thresholds), [docs/init.md](../init.md)
(managed blocks, drift, legacy migration). Index: [docs/API.md](../API.md).

State machines:

```
Standard lane: draft -> speccing -> specced -> planning -> planned -> implementing -> qa -> done
Trivial lane:  draft -> rationale -> implementing -> audit -> done
```

`spec_pushback`/`spec_resolve` model ambiguity (any status -> `needs_grill` ->
back to `speccing`/`rationale`). `spec_reject` models a failed review (`qa`,
`audit`, or — since SPEC-087 D6 — `done` -> `implementing`), distinct from
pushback. `done -> implementing` is the ONLY way out of `done`: `spec_advance`
still rejects any attempt to advance past `done`. See error codes at the
bottom.

---

## Backlog Tools

### backlog_add

Add a new item to the project backlog. `lane` is required; `scope` is
required when `lane=trivial`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `title` | string | yes | Short description of the idea |
| `lane` | string | yes | `trivial` (≤3 files, ≤20 lines, no public API change, no SQL/cmd) or `standard` |
| `description` | string | no | Detailed explanation of the idea |
| `priority` | string | no | `critical`, `high`, `medium`, `low`. Default: `medium` |
| `project` | string | no | Project slug. Default: auto-detected |
| `scope` | string | no | Glob pattern for files this item may touch (e.g. `internal/store/**`). Required when `lane=trivial` |

**Returns:** `{"id": "BL-001", "title": "Push notifications", "status": "raw", "priority": "medium", "lane": "standard", "project": "wirvii/mneme", "created_at": "2026-04-30T12:00:00Z"}`

**Errors:** `-32602` missing `title`/`lane`, missing `scope` for trivial lane.

### backlog_list

List backlog items for the current project.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `status` | string | no | `raw`, `refined`, `promoted`, `archived` |
| `project` | string | no | Project slug. Default: auto-detected |

**Returns:** Array of backlog items.

### backlog_refine

Refine a raw backlog item with additional details.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Backlog item ID (e.g. `BL-001`) |
| `refinement` | string | yes | Refinement content to add to the item |

**Returns:** Updated backlog item with `status: "refined"`.

**Errors:** `-32602` missing `id`/`refinement`. `-32000` not found.

### backlog_promote

Promote a refined backlog item to a spec. The item must have status `refined`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Backlog item ID to promote (e.g. `BL-001`) |

**Returns:** New spec object with `status: "draft"` and the item's `lane`/`scope` carried over.

**Errors:** `-32602` item not refined, missing `id`. `-32000` not found.

---

## Spec Tools

### spec_new

Create a new spec in draft status. `lane` is required; `scope` is required
when `lane=trivial`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `title` | string | yes | Title of the spec |
| `lane` | string | yes | `trivial` or `standard` |
| `backlog_id` | string | no | Originating backlog item ID, if any |
| `project` | string | no | Project slug. Default: auto-detected |
| `scope` | string | no | Glob pattern for files this spec may touch. Required when `lane=trivial` |

**Returns:** Spec object with `status: "draft"`.

**Errors:** `-32602` missing `title`/`lane`, missing `scope` for trivial lane.

### spec_status

Get the full status of a spec including history and pushbacks.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID (e.g. `SPEC-001`) |

**Returns:**

```json
{
  "spec": {"id": "SPEC-001", "title": "Push notifications", "status": "implementing",
    "lane": "standard", "project": "wirvii/mneme", "backlog_id": "BL-001",
    "created_at": "2026-04-30T12:00:00Z", "updated_at": "2026-04-30T14:00:00Z"},
  "history": [{"from_status": "draft", "to_status": "speccing", "by": "orchestrator",
    "reason": "Ready for architect", "at": "2026-04-30T12:30:00Z"}],
  "pushbacks": [{"from_agent": "backend", "questions": ["API contract with auth?"],
    "resolution": "Use service accounts", "resolved": true, "created_at": "2026-04-30T13:00:00Z"}]
}
```

**Errors:** `-32000` not found, `-32602` missing `id`.

### spec_advance

Advance a spec to its next lifecycle state (standard-lane state machine above).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID to advance |
| `by` | string | yes | Who triggers the advance (e.g. `orchestrator`, `architect`, `backend`) |
| `reason` | string | no | Optional reason for the transition |

**Returns:** Updated spec object.

**Errors:** `-32602` invalid transition, missing `id`/`by`.

### spec_pushback

Register a pushback from an agent, transitioning the spec to `needs_grill`.
Models ambiguity — distinct from `spec_reject` (which models a failed review).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID to push back on |
| `from_agent` | string | yes | Agent raising the pushback (e.g. `architect`, `backend`, `qa`) |
| `questions` | string[] | yes | Questions blocking progress (min 1) |

**Returns:** Updated spec object with `status: "needs_grill"`.

**Errors:** `-32602` missing fields. `-32000` not found.

### spec_resolve

Resolve the latest pushback on a spec, returning it to `speccing` (standard
lane) or `rationale` (trivial lane).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID whose pushback to resolve |
| `resolution` | string | yes | Answer to the pushback questions |

**Returns:** Updated spec object.

**Errors:** `-32602` missing fields. `-32000` pushback or spec not found.

### spec_doc_write

Write a spec entregable (`spec`/`plan`/`qa-report`/`changes`) to its
workflow directory (SPEC-087 D3) — the path a subagent uses instead of
copying its report into the workflow directory by hand. The destination
directory and filename are never caller-supplied: the directory is derived
from the persisted spec record (`spec.Project`, via `GetSpec`) and the
filename comes from a closed, Go-authored `kind -> filename` map
(`spec` → `spec.md`, `plan` → `plan.md`, `qa-report` → `qa-report.md`,
`changes` → `changes.md`). 0644, parent directories created as needed, plain
overwrite-or-create — no append, no arbitrary read.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID (e.g. `SPEC-001`) |
| `kind` | string | yes | `spec`, `plan`, `qa-report`, or `changes` |
| `content` | string | yes | Full document content, written verbatim |

**Returns:** `{"path": "/abs/path/to/qa-report.md", "bytes": 1234, "created": true}`

**Errors:** `-32602` unknown `kind`, missing fields. `-32000` spec not found.

### spec_list

List specs for the current project.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `status` | string | no | `draft`, `speccing`, `needs_grill`, `specced`, `planning`, `planned`, `implementing`, `qa`, `done`, `rationale`, `audit` |
| `project` | string | no | Project slug. Default: auto-detected |

**Returns:** Array of spec objects.

### spec_quick

Advance a trivial-lane spec from `draft` directly to `implementing` in one
step by recording a rationale. Rejected for standard-lane specs.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID (must be trivial lane, draft status) |
| `rationale` | string | yes | 1-3 sentence justification for the trivial classification |
| `by` | string | yes | Who triggers the advance (e.g. `orchestrator`) |

**Returns:** Updated spec object with `status: "implementing"`.

**Errors:** `-32602` spec is not trivial lane, spec is not in `draft`, missing fields.

**Example:** `mneme spec quick SPEC-007 "One-line fix to a comment typo in audit.go" --by orchestrator`

### spec_reject

Reject a spec from `qa` (standard lane), `audit` (trivial lane), or `done`
(either lane, SPEC-087 D6) back to `implementing`. Records the rejection
reason in history. Distinct from `spec_pushback`, which models ambiguity
rather than a failed review. `done -> implementing` is the only way a
`done` spec ever moves again — `spec_advance` remains impossible from
`done`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID to reject |
| `reason` | string | yes | Why the spec was rejected (persisted in history) |
| `by` | string | yes | Who triggers the rejection (e.g. `qa-agent`, `orchestrator`) |

**Returns:** Updated spec object with `status: "implementing"`.

**Errors:** `-32602` invalid transition (spec not in `qa`/`audit`/`done`), missing fields. `-32000` not found.

---

## Lane Tools

### lane_audit

Run the deterministic post-implementation auditor for a trivial-lane spec in
`audit` status. Checks: file count ≤3, line count ≤20, no forbidden paths
(`*.sql`, `migrations/**`, `cmd/**`, `internal/install/assets/**`), files
within declared scope, no exported Go/TS symbol changes.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID to audit (must be trivial lane, audit status) |
| `base_ref` | string | no | Git ref to diff against. Default: merge-base with the default branch |

**Returns (pass):** the spec advances to `done`; returns the `AuditResult` (`file_count`, `lines_changed`, `breaches: []`, `passed: true`).

**Returns (fail):** `IsError: true` with the full `AuditResult` payload (`breaches` populated, `out_of_scope_files`, `forbidden_paths`, `public_symbol_changes`) so the caller can decide whether to reclassify or override — the spec stays in `audit`.

**Errors:** `-32602` spec is not trivial lane / not in audit status.

### lane_reclassify

Reclassify a spec's lane from `trivial` to `standard`. Only `trivial→standard`
is allowed. Moves the spec to `speccing` so the full SDD workflow can proceed.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID to reclassify |
| `lane` | string | yes | Target lane (only `standard` is accepted) |
| `by` | string | yes | Who triggers the reclassification |
| `scope` | string | no | Updated scope glob (optional when moving to standard) |

**Returns:** Updated spec object with `lane: "standard"`, `status: "speccing"`.

**Errors:** `-32602` target lane is not `standard`, spec already standard.

### lane_override

Override a failed lane audit and advance a trivial-lane spec from `audit` to
`done`. Requires a documented reason, persisted as a discovery memory. Use
sparingly — prefer `lane_reclassify` when possible.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID to override (must be trivial lane, audit status) |
| `reason` | string | yes | Justification for bypassing the audit |
| `by` | string | yes | Who triggers the override |

**Returns:** Updated spec object with `status: "done"`.

**Errors:** `-32602` missing fields, spec not trivial lane / not in audit.

### lane_status

Show the lane classification, scope, and latest audit summary for a spec.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID to inspect |

**Returns:**

```json
{
  "spec": { "...": "..." }, "lane": "trivial", "scope": "internal/store/*.go",
  "latest_audit": {"passed": true, "breaches": [], "at": "2026-06-01T10:00:00Z"},
  "rejection_count": 0
}
```

**Errors:** `-32000` not found, `-32602` missing `id`.

### lane_stats

Return lane compliance statistics for the project: trivial spec count,
audit-fail count and rate, override count, reclassify count.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `project` | string | no | Project slug. Default: auto-detected |

**Returns:**

```json
{"trivial_count": 12, "audit_fail_count": 2, "audit_fail_rate": 0.166, "override_count": 1, "reclassify_count": 1}
```

---

## init

Initialise a project with mneme managed blocks and report drift. Applies the
global operating manual and a minimal repo block to `CLAUDE.md` files, then
runs the drift detector. This is the MCP-tool form of `mneme init`; the
destructive legacy-project migration (`--apply` DB writes + `rm-rf`) is
**CLI-only** and not exposed here.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `check` | boolean | no | When `true`, report-only mode: drift is reported but no managed blocks are written. Default: `false` |
| `repo_root` | string | no | Absolute path to the repository root. Default: current working directory |

**Returns:**

```json
{
  "repo_root": "/path/to/repo", "check_mode": false, "manual_applied": true,
  "repo_block_applied": true, "drift_findings": ["CLAUDE.md: no drift detected"]
}
```

**Errors:** `-32603` SDD service unavailable, `getwd`/home dir resolution failure.

---

## Error codes

| Code | Name | Triggered when |
|------|------|----------------|
| `-32600` | Invalid Request | Malformed JSON-RPC envelope |
| `-32601` | Method not found | Unknown MCP method |
| `-32602` | Invalid params | Missing required params, invalid lane/state transition, domain validation |
| `-32603` | Internal error | DB error, unexpected failure, SDD/init service unavailable |
| `-32000` | Not found | Unknown backlog/spec ID |

## See also

- [docs/lanes.md](../lanes.md) — lane thresholds, auditor internals, reclassify vs. override decision guide
- [docs/init.md](../init.md) — managed blocks, drift detection, legacy migration (CLI-only)
- [docs/api/memory.md](memory.md) — `mem_*` tools
