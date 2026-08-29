# API Reference — SDD Tools (`backlog_*`, `spec_*`, `lane_*`, `quality_*`, `sdd_*`, `init`)

28 MCP tools over JSON-RPC 2.0 stdio (`mneme mcp`): `backlog_*` (6), `spec_*`
(9), `lane_*` (5), `quality_*` (5), `sdd_*` (2), `init` (1). Concept guide:
[docs/lanes.md](../lanes.md) (trivial/standard lanes, auditor thresholds),
[docs/init.md](../init.md) (managed blocks, drift, legacy migration),
[docs/quality.md](../quality.md) (the quality constitution, certificates,
and the `spec_advance` block, SPEC-115),
[docs/sdd-git-native.md](../sdd-git-native.md) (the SDD git-native
mechanism's file format, write path, and read path, SPEC-130/SPEC-131).
Index: [docs/API.md](../API.md).

**`sdd_enable`/`sdd_disable`/`sdd_export` are still never planned to become
tools at all** — a `--apply` an agent could call unattended would defeat
the human-confirmation gate that publishing a backlog to git requires.
`sdd_status`/`sdd_import` (SPEC-131 §2b, the project-wide MCP surface's
86th/87th tools, taking it from 85 to 87) ARE tools: `sdd_status` is
read-only, and `sdd_import` — the automated equivalent of `mneme sdd
import` — is deliberately safe to call from an agent because it only ever
brings COMMITTED, already-reviewed files into the local database; see
their entries below.

**Archiving a backlog item can freeze its spec (SPEC-125):** `backlog_archive`
requires a reason and is refused when the item is already archived, or when
its linked spec already reached `done`. When the linked spec is still alive,
archiving the item ALSO freezes that spec: none of the eight verbs that
change a spec's status — `spec_advance`, `spec_pushback`, `spec_reject`,
`spec_resolve`, `spec_quick`, `lane_audit`, `lane_reclassify`, `lane_override`
— can move it again, though it stays fully readable (`spec_status`,
`spec_list`, `spec_doc_write`, `lane_status` keep working). This cannot be
undone: there is no unarchive. The agreed way back is to create a NEW
backlog item that references the archived one — never to resurrect the old
one. **The freeze itself is now visible (SPEC-126):** `spec_status` and
`spec_list` both gain a `frozen` field naming why a spec can no longer
change status — see their own entries below for its exact shape.

`backlog_list`/`spec_list` share one acotado convention (SPEC-109): a `limit`
param (integer, min 1, max 50, default 20 when omitted) and a `total` field
reporting the number of matches *before* `limit` was applied — a `limit`
above 50 is silently capped, which is safe only because `total` always tells
the truth about how many exist. `backlog_list` additionally replaces each
item's `description` with a 200-rune `excerpt` + `truncated` flag, since
backlog descriptions are grill ledgers that can run to tens of KB; call
`backlog_get` for the full text.

**Refinement is iterative (SPEC-110):** a backlog item accepts N
refinements, each stored as its own row (`backlog_refinements`) rather than
concatenated into `description` — the old code accepted exactly one
refinement (`raw -> refined`), which is why a real grill ledger merge was
lost when a second refinement was attempted. `raw` and `refined` items both
accept `backlog_refine`; `promoted`/`archived` do not (`-32602`,
`ErrBacklogNotRefinable`). `description` is write-once, set only by
`backlog_add`. `backlog_list` gains a `refinements` counter per item (no
`omitempty` — present even at 0); `backlog_get` now returns
`{item, refinements}` with ALL refinements in full, no limit — a
**breaking change** to its previous shape (the raw item at the top level).

**Every backlog item and spec now carries a `uuid` (SPEC-128):** an
identifier of its own, assigned once, that never changes — distinct from
`id` (`BL-001`/`SPEC-001`), which is a per-machine correlative and can name
different work on two different machines. `uuid` is what lets a memory that
mentions `SPEC-125` resolve correctly even on a machine whose own
`SPEC-125` is unrelated work (see `mem_get`'s `sdd_refs`, and `mneme get`'s
one-line warning when a reference is `foreign`). It is present in every
JSON response that returns a backlog item or spec, and it is NEVER shown in
any readable table, list, or status line — there is no command that prints
it for a human to read.

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

**Returns:** `{"id": "BL-001", "uuid": "0198f2c1-4a7b-7c3d-9e10-3f4a5b6c7d8e", "title": "Push notifications", "status": "raw", "priority": "medium", "lane": "standard", "project": "wirvii/mneme", "created_at": "2026-04-30T12:00:00Z"}`

**Errors:** `-32602` missing `title`/`lane`, missing `scope` for trivial lane.

### backlog_list

List backlog items for the current project. Descriptions are returned as a
200-character `excerpt` with a `truncated` flag — call `backlog_get` for the
full description. `total` is the number of matches before `limit` was
applied. Items beyond `limit` (max 50) are not reachable by listing: narrow
with `status`, or fetch by ID with `backlog_get` (SPEC-109 D19 — a known,
documented limitation, not paging). Each item also carries `refinements`
(SPEC-110 D4): how many refinements it has. An empty `excerpt` with
`refinements` > 0 does **not** mean an empty item — the detail lives in the
refinements; call `backlog_get`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `status` | string | no | `raw`, `refined`, `promoted`, `archived` |
| `project` | string | no | Project slug. Default: auto-detected |
| `limit` | integer | no | Max items returned. Min 1, max 50, default 20 |

**Returns:**

```json
{
  "items": [
    {"id": "BL-001", "uuid": "0198f2c1-4a7b-7c3d-9e10-3f4a5b6c7d8e", "title": "Push notifications", "excerpt": "...", "truncated": true,
     "status": "raw", "priority": "medium", "project": "wirvii/mneme", "refinements": 2,
     "created_at": "2026-04-30T12:00:00Z", "updated_at": "2026-04-30T12:00:00Z"}
  ],
  "total": 25
}
```

### backlog_get

Get one backlog item by ID with its FULL description, plus **all** of its
refinements — no excerpt, no limit (SPEC-110 D6/D7). This is the only way to
read a grill ledger over MCP: `spec_status` does not include the backlog
item and the `specs` table has no description column. The SPEC-109 `limit`
convention does **not** apply here — there is no escape hatch for
refinements the way `backlog_get` itself is one for `backlog_list`, so a
default window would leave rows permanently unreachable.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Backlog item ID (e.g. `BL-001`) |

**Returns (breaking change from SPEC-109's shape):** `{item, refinements}` —
the full backlog item (`description` included in full, plus
`refinement_count`) and the complete, ordered (`seq` ascending) list of
refinements: `{"item": {...}, "refinements": [{"item_id": "BL-001", "seq": 1,
"body": "...", "by": "architect", "at": "..."}]}`. No `total` field: with no
window, `len(refinements)` and `item.refinement_count` cannot disagree.

**Errors:** `-32602` missing `id`. `-32000` not found (`model.ErrBacklogNotFound`).

### backlog_refine

Append a refinement to a `raw` or `refined` backlog item (SPEC-110). An item
accepts **N** refinements: each is stored as its own row and the item's
`description` never grows — `raw` becomes `refined` on the first call and
**stays** `refined` on every subsequent one. `promoted`/`archived` items
reject refinement (`-32602`, not writable — nobody reads a promoted item's
description).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Backlog item ID (e.g. `BL-001`) |
| `refinement` | string | yes | Refinement content to add to the item |
| `by` | string | no | Who appends the refinement (e.g. `orchestrator`, `architect`). Omitted means unattributed. |

**Returns:** Updated backlog item, re-read from the database — `status`
(`refined`) and `refinement_count` reflect the just-appended row.

**Errors:** `-32602` missing `id`/`refinement`, empty/whitespace-only
`refinement` (`ErrContentRequired`), item is `promoted`/`archived`
(`ErrBacklogNotRefinable`). `-32000` not found.

### backlog_promote

Promote a refined backlog item to a spec. The item must have status `refined`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Backlog item ID to promote (e.g. `BL-001`) |

**Returns:** New spec object with `status: "draft"` and the item's `lane`/`scope` carried over.

**Errors:** `-32602` item not refined, missing `id`. `-32000` not found.

### backlog_archive

Archive a backlog item with a mandatory reason (SPEC-125). Refused when the
item is already archived, or when its linked spec is already `done`. When
the linked spec is still alive, the item IS archived and the spec is
FROZEN — see the note at the top of this page for what freezing means and
the way back.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Backlog item ID to archive (e.g. `BL-001`) |
| `reason` | string | yes | Why the item is being discarded. Required and never blank |

**Returns:** `{item, frozen_spec}` — `item` is the archived backlog item.
`frozen_spec` is present (`{id, title, status}`, the spec's status at the
moment of archiving) only when a live spec was frozen; it is ABSENT
(not `null`) when the item had no spec or the spec was already done.

**Errors:** `-32602` missing `reason` (empty or whitespace-only), item
already archived, linked spec already `done`. `-32000` item not found, or
(fail-closed) the linked spec does not exist.

**Denied to subagents:** `backlog_archive` is a lifecycle tool — like
`spec_advance`/`spec_quick` — and is unconditionally denied to every
subagent regardless of role. Discarding work and freezing its record
irreversibly is the owner's call, channelled by the orchestrator.

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
  "spec": {"id": "SPEC-001", "uuid": "0198f2c1-9e10-7c3d-4a7b-3f4a5b6c7d8e", "title": "Push notifications", "status": "implementing",
    "lane": "standard", "project": "wirvii/mneme", "backlog_id": "BL-001",
    "created_at": "2026-04-30T12:00:00Z", "updated_at": "2026-04-30T14:00:00Z"},
  "history": [{"from_status": "draft", "to_status": "speccing", "by": "orchestrator",
    "reason": "Ready for architect", "at": "2026-04-30T12:30:00Z"}],
  "pushbacks": [{"from_agent": "backend", "questions": ["API contract with auth?"],
    "resolution": "Use service accounts", "resolved": true, "created_at": "2026-04-30T13:00:00Z"}],
  "frozen": {"state": "archived", "backlog_id": "BL-001", "reason": "Superseded by BL-207"}
}
```

**`frozen` (SPEC-126) is present ONLY when this spec can no longer change
status**, and absent — not `null` — otherwise. `state` is `"archived"` (the
originating backlog item was read and is archived; `reason` carries its
recorded archive reason, possibly empty) or `"missing"` (the item named by
`backlog_id` is not in this database at all — a different problem, since it
was never actually read, so `reason` is absent). Either state means every
`spec_*`/`lane_*` verb that changes status will fail — `spec_status` itself
never does, so a frozen spec stays fully readable.

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

Write a spec entregable (`spec`/`plan`/`qa-report`/`changes`/`criteria`/
`budget`) to its workflow directory (SPEC-087 D3, `criteria` added by
SPEC-117 S3 D1, `budget` added by SPEC-118 S4 D2) — the path a subagent
uses instead of copying its report into the workflow directory by hand.
The destination directory and filename are never caller-supplied: the
directory is derived from the persisted spec record (`spec.Project`, via
`GetSpec`) and the filename comes from a closed, Go-authored `kind ->
filename` map (`spec` → `spec.md`, `plan` → `plan.md`, `qa-report` →
`qa-report.md`, `changes` → `changes.md`, `criteria` → `criteria.toml`,
`budget` → `budget.toml`). 0644, parent directories created as needed,
plain overwrite-or-create — no append, no arbitrary read.

**`kind = "criteria"` and `kind = "budget"` are both validated BEFORE
anything is written** (D7/D11): the content is parsed strictly and, when
the server's `repoDir` is configured, every anchor is resolved against the
real working tree — `criteria.toml`'s `new = false` assertions, or
`budget.toml`'s `[[modify]]` symbols and `[[quota]]` directories. Any
failure returns without writing a single byte — see
[docs/quality.md](../quality.md#s3-executable-acceptance-criteria-spec-117)
and [docs/quality.md](../quality.md#s4-the-budget-against-the-graph-and-the-absorption-of-lane-audit-spec-118)
for the full vocabulary each validates against.

**`kind = "budget"` and `kind = "criteria"` are BOTH restricted to the
`architect` role for a subagent caller** (SPEC-118 D10,
`internal/cli/hook.go`'s `roleScopedDocKinds`) — an implementer writing
either would be examining itself. The rule fails OPEN when a subagent's
role cannot be resolved, the opposite of `quality_sign`'s fail-closed
posture (see docs/quality.md's own note on why).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID (e.g. `SPEC-001`) |
| `kind` | string | yes | `spec`, `plan`, `qa-report`, `changes`, `criteria`, or `budget` |
| `content` | string | yes | Full document content, written verbatim |

**Returns:** `{"path": "/abs/path/to/qa-report.md", "bytes": 1234, "created": true}`

**Errors:** `-32602` unknown `kind`, missing fields, or (for `kind =
"criteria"`/`"budget"`) an invalid document / an unresolvable anchor.
`-32000` spec not found.

### spec_list

List specs for the current project. `total` is the number of matches before
`limit` was applied. No excerpt field: `model.Spec` has no `description`
column to truncate.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `status` | string | no | `draft`, `speccing`, `needs_grill`, `specced`, `planning`, `planned`, `implementing`, `qa`, `done`, `rationale`, `audit` |
| `project` | string | no | Project slug. Default: auto-detected |
| `limit` | integer | no | Max specs returned. Min 1, max 50, default 20 |

**Returns:** `{"specs": [ {...spec object...} ], "total": 12, "frozen": {"SPEC-007": {"state": "archived", "backlog_id": "BL-001", "reason": "Superseded by BL-207"}}}`

**`frozen` (SPEC-126) is an object keyed by spec ID**, holding one entry
for every spec in `specs` that can no longer change status — same shape as
`spec_status`'s own `frozen` field above. **Absent entirely — not an empty
object — when none of the returned specs is frozen.** A spec ID missing from
this object can still change status normally.

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

**Errors:** `-32602` spec is not trivial lane / not in audit status; spec is
frozen (its backlog item was archived, SPEC-125) — checked before the audit
runs, so a frozen spec never executes git or writes a `lane_audits` row.

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

**Errors:** `-32602` target lane is not `standard`, spec already standard,
spec is frozen (its backlog item was archived, SPEC-125).

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

**Errors:** `-32602` missing fields, spec not trivial lane / not in audit,
spec is frozen (its backlog item was archived, SPEC-125) — the reason
check for `reason` runs before the freeze check, so an empty reason still
surfaces first.

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

## SDD Tools (SPEC-131 §2b)

The reading half of the SDD git-native mechanism: bringing a repository's
committed `.mneme/sdd/` Markdown files into the local database. See
[docs/sdd-git-native.md](../sdd-git-native.md) for the file format and the
write path (SPEC-130 §2a) these two tools read back from. Both take no
arguments — they always operate on the current repository (`h.sdd.
RepoDir()`), the same root every other `mneme sdd` command resolves from.
`sdd_import`'s response uses the same snake_case convention as every other
tool on this page; `sdd_status`'s does not — its Go struct carries no
`json` tags at all, so its fields serialize under their Go names
(`RepoRoot`, `Enabled`, …), a deliberate exception worth knowing before
parsing it.

### sdd_status

Report the mechanism's state for this repository: on/off, pending git
changes, broken/conflicted/incomplete/divergent files, whether THIS
machine's own git hooks are installed, and correlatives that exist only in
the local database on this branch. Read-only — never writes anything. Same
response `mneme sdd status --json` prints.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| _(none)_  |      |          |              |

**Returns** (abbreviated — every field beyond `RepoRoot`/`Enabled`/`Plan`/
`PendingGit` is derived at the moment of the call, never read from a
dedicated state file):

```json
{
  "RepoRoot": "/path/to/repo",
  "Enabled": true,
  "Plan": { "...": "..." },
  "PendingGit": "",
  "Broken": [],
  "ForeignPaths": [],
  "Conflicted": [],
  "Incomplete": [],
  "Divergent": [],
  "HooksInstalled": true,
  "OnlyInBaseCount": 0,
  "FrozenBlocked": []
}
```

**Errors:** `-32000` when the SDD engine is unavailable.

### sdd_import

Import this repository's SDD backlog/specs (`.mneme/sdd/`) into the local
database — the same read path the installed git hooks run automatically
after every pull/checkout. Decides by anchor, never by correlative: a
record already known here by its own UUIDv7 anchor is created or updated;
a correlative already claimed by a DIFFERENT anchor is skipped and
reported by title, never by anchor (no field on this response ever
carries one, matching SPEC-128 D9). Executes unconditionally — there is no
dry-run parameter over MCP (the CLI's own `--dry-run` flag has no
equivalent here, since nothing this mechanism writes is ever destructive:
D13 already guarantees no file under `.mneme/sdd/` is ever deleted).
**Denied to subagents** (`lifecycleTools` in `internal/cli/hook.go`): an
implementer editing its own record file and then calling this tool would
be authorizing its own status change, the same family `spec_advance`/
`spec_quick`/`quality_ack`/`backlog_archive` already belong to.

| Parameter | Type | Required | Description |
|-----------|------|----------|--------------|
| _(none)_  |      |          |              |

**Returns:**

```json
{
  "created": ["BL-050 (backlog/BL-050.md)"],
  "updated": ["SPEC-130: implementing -> qa"],
  "completed": [{"path": "backlog/BL-003.md", "id": "BL-003", "fields": ["priority"]}],
  "skipped": [{"path": "specs/SPEC-131/record.md", "id": "SPEC-131", "reason": "correlativo-reclamado-por-dos-elementos"}],
  "only_in_base": ["BL-012"],
  "only_in_base_total": 1,
  "no_op_reason": ""
}
```

`no_op_reason` is the ONLY field populated (`"mecanismo apagado"` when the
marker exists but `.mneme/sdd.off` is present, `"no hay directorio
.mneme/sdd"` when the mechanism was never enabled) when the import had
nothing to do — every other field is left empty rather than reporting a
false zero-work success.

**Errors:** `-32000` when the SDD engine is unavailable, or on a foreign
project marker (a `.mneme/sdd/.mneme-sdd` committed by a different mneme
project).

---

## Quality Tools (SPEC-115 EPIC-calidad S1, extended by SPEC-116 S2 and SPEC-117 S3)

The quality mechanism replaces an agent's self-reported "it works" with a
result mneme produced by executing something, bound to the exact commit
(see [docs/quality.md](../quality.md) for the full design). `quality_verify`
EMITS a certificate; `spec_advance` only ever COMPARES an already-emitted
one — the two verbs are deliberately separate (D5): running a project's
full build+test suite inside a synchronous MCP call would risk a client
timeout, and `spec_advance` is denied to subagents (see below), so an
implementer could never verify its own work if verification only happened
there.

**SPEC-116 (S2) added no new tool.** `quality_verify` also emits up to
seven more `quality_checks` rows (`kind=coverage`/`ratchet`) when the
constitution declares them; `quality_status`'s response gains the
registered baseline's fields (below). `quality_ack` and its request/
response shape are unchanged.

**SPEC-117 (S3) adds two tools**, `quality_sign` and `quality_report` (82 →
84): `quality_verify` also emits a `criteria/*`/`criterion*` set of rows
when `[criteria]` is declared and enabled (see
[docs/quality.md](../quality.md#s3-executable-acceptance-criteria-spec-117)),
and `spec_doc_write`'s `kind` enum gains `"criteria"` (above).

**SPEC-118 (S4) adds NO new tool.** `quality_verify` also emits up to 12
(standard lane) or 13 (trivial lane) more `quality_checks` rows
(`kind=budget`/`detection`) when `[budget]` is declared and enabled (see
[docs/quality.md](../quality.md#s4-the-budget-against-the-graph-and-the-absorption-of-lane-audit-spec-118));
`quality_status`'s response gains a `budget` field (path, disk hash,
certificate hash, margin, budgeted/delivered/overrun); `spec_doc_write`'s
`kind` enum gains `"budget"` (above); and `lane_audit`'s response type
changes from `lane.AuditResult` to `model.LaneAuditResult` — same field
names, same (absent) JSON tags, byte-identical wire shape.

**SPEC-119 (S5) adds NO new tool.** `quality_verify` also emits `6 + N`
more `quality_checks` rows (`kind=mutation`/`kind=mutant`) when
`[mutation]` is declared and enabled (see
[docs/quality.md](../quality.md#s5-mutation-over-the-diff-and-the-equivalent-mutant-escape-hatch-spec-119));
`quality_status`'s response gains a `mutation` field (declared
format/`report_path`/cupo, plus the latest certificate's signed-equivalent
count, survivor count, and full per-status recount); and `quality_sign`'s
domain GENERALIZES to also accept a `mutant` survivor row (below) —
`quality_ack`'s domain narrows in exact lockstep, since the two are now
decided by one predicate, negated. No change to either tool's request or
response SHAPE.

### quality_verify

Run the gates declared in `.mneme/quality.toml` for a spec and emit (or
deny) a certificate bound to the current commit. Valid only while the spec
is `implementing` or `qa` (`qa` admits recertification when HEAD moved
during QA).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID to verify (must be `implementing` or `qa` status) |

**Returns:** the persisted `QualityCertificate` — `id`, `verdict`
(`pass`/`fail`/`findings`), `head_sha`, `constitution_hash`, `dirty`,
`started_at`/`finished_at`/`duration_ms`.

**Errors:** `-32602` spec not in `implementing`/`qa`
(`ErrInvalidTransition`), constitution missing or unparseable
(`ErrInvalidConstitution`). `-32000` spec not found.

### quality_status

Report the constitution's state (path, hash, `enabled`, declared gates)
and, when a spec ID is given, its latest certificate and checks. Never
executes anything — this is the read-only half of the mechanism.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | no | Spec ID to report the latest certificate for. Omit to report only the constitution's own state |

**Returns:**

```json
{
  "enabled": true, "exists": true, "path": ".mneme/quality.toml",
  "constitution_hash": "…", "gate_names": ["build", "test"],
  "note": "mecanismo encendido",
  "latest_certificate": {"id": "…", "verdict": "pass", "head_sha": "…"},
  "checks": [{"seq": 1, "kind": "tree", "name": "clean-worktree", "status": "pass"}],
  "baseline": {
    "path": ".mneme/quality-baseline.toml",
    "measured_at_sha": "9f3c…",
    "measured_at": "2026-08-13T11:04:22Z",
    "global_line_pct": 70.5,
    "staleness_known": true,
    "staleness_pct": 0.3,
    "stale": false
  },
  "mutation": {
    "format": "gremlins", "report_path": "tmp/mutants.json",
    "max_equivalent": 2, "signed_equivalent": 0, "survivor_count": 0,
    "by_status": {"killed": 24, "not_covered": 2, "timed_out": 36}
  }
}
```

`mutation` (SPEC-119) is omitted entirely when the constitution does not
declare `[mutation]` (schema < 5). `signed_equivalent`/`survivor_count`/
`by_status` are populated only when `id` was supplied and a certificate
exists; `by_status` is the LATEST certificate's `mutation/score` recount,
verbatim.

`baseline` (SPEC-116) is the ratchet's registered baseline — omitted
entirely when `.mneme/quality-baseline.toml` does not exist (the normal
state before the repository's first `mneme quality baseline update`,
which is CLI-only and never exposed over MCP, see docs/quality.md).
`staleness_known`/`staleness_pct`/`stale` are populated only when `id` was
supplied AND that spec's latest certificate has a usable
`coverage/profile` row to compare against; otherwise `staleness_known` is
`false` and the other two fields are absent. Reading and reporting only —
`quality_status` never executes anything to compute this.

A repo with no constitution at all (the common case) returns
`{"enabled": false, "exists": false, "note": "mecanismo apagado: no existe .mneme/quality.toml"}`
— never an error.

### quality_ack

Record a human's justified approval of a quality finding, converting it from
`finding` to `acked` without re-running anything. The certificate's verdict
is recalculated in the same operation. **Denied to subagents**
(`lifecycleTools`, see below) — the author of a change never absolves
themselves of its own findings.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `cert_id` | string | yes | Certificate ID the finding belongs to |
| `seq` | integer | yes | Seq of the finding within the certificate's checks |
| `by` | string | yes | Who is acknowledging the finding — never the author of the change under review |
| `justification` | string | yes | Why the finding is acceptable (non-empty) |

**Returns:** `{"acked": true, "cert_id": "…", "seq": 1}`

**Errors:** `-32602` missing `by`/`justification` (`ErrReasonRequired`), or
the targeted row requires a signature — a criterion, or (SPEC-119 S5) a
`mutant` survivor row (`ErrRequiresSign`, alias `ErrCriterionRequiresSign`
— use `quality_sign` instead). `-32000` no such certificate/finding at that
seq (`ErrCertificateNotFound`).

### quality_sign

Record an ATTESTATION that a row genuinely holds, converting it from
`finding` to `acked`. Distinct from `quality_ack` (an ABSOLUTION): `ack`
says "I approve this despite it being a problem", `sign` says "I verified
this and it holds" — mixing the two verbs would make a count confuse "we
forgave N findings" with "we verified M manuals". Accepts a row iff
`quality.RequiresSignature(kind)` — a criterion row (SPEC-117 S3 D11), OR
(SPEC-119 S5 D8) a `mutant` survivor row, the equivalent-mutant escape
hatch. **Restricted to the `qa-tester` role** for a subagent caller, and
**fails CLOSED** when that role cannot be resolved — the first rule in the
repo to do so (see docs/quality.md).

**SPEC-119 D9: signing a `mutant` row additionally enforces the
certificate's own `max_equivalent` cupo** — read from that SAME
certificate's `mutation/score` row, never from `.mneme/quality.toml` on
disk. Past the cupo, `Sign` refuses (`ErrEquivalentQuotaExceeded`) and the
row stays `finding`; a certificate with no `mutation/score` row at all
refuses ANY `mutant` signature (fails closed, never "unlimited").

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `cert_id` | string | yes | Certificate ID the attested row belongs to |
| `seq` | integer | yes | Seq of the row within the certificate's checks |
| `by` | string | yes | Who is signing — the qa-tester |
| `evidence` | string | yes | What was verified and how (non-empty) |

**Returns:** `{"signed": true, "cert_id": "…", "seq": 4}`

**Errors:** `-32602` missing `by`/`evidence` (`ErrReasonRequired`), or the
targeted row is not one `RequiresSignature` accepts (`ErrNotSignable`,
alias `ErrNotACriterion`). `-32602` the certificate's `max_equivalent` cupo
is already reached, for a `mutant` row (`ErrEquivalentQuotaExceeded`,
SPEC-119). `-32000` no such certificate/row at that seq
(`ErrCertificateNotFound`).

### quality_report

Generate the QA report from the spec's latest certificate and write it
via `spec_doc_write`'s `qa-report` kind (SPEC-117 S3 D12). Renders from
the certificate's own persisted rows — **never** from `criteria.toml`, so
editing that document after certifying cannot change what the report
says. Deterministic: the same certificate always produces byte-identical
output.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID to generate the report for |
| `force` | boolean | no | Overwrite an existing `qa-report.md` even if it lacks mneme's generation marker |

**Returns:** `{"path": "/abs/path/to/qa-report.md", "bytes": 4096, "certificate_id": "…"}`

**Errors:** `-32602` an existing `qa-report.md` lacks mneme's generation
marker and `force` was not set (`ErrReportNotGenerated`). `-32000` spec or
certificate not found.

### `mneme quality baseline update|show` (SPEC-116, CLI-only)

Not exposed over MCP, by design (docs/quality.md). `mneme quality baseline
update <spec-id>` writes `.mneme/quality-baseline.toml` from that spec's
latest **`pass`** certificate — refusing outright (no write) if the latest
certificate is not `pass`, or none exists. `mneme quality baseline show`
prints the registered baseline, or a note that none is registered yet;
never an error either way.

### The `spec_advance` block (D12)

When the mechanism is enabled, `implementing → qa` and `qa → done` of a
**standard**-lane spec require a usable certificate — `verdict == pass`,
`head_sha` matches HEAD, `constitution_hash` matches the current file, and
the worktree is clean. Each broken conjunction has its own error, always
naming the exact remedy command:

| Error | Cause | Remedy |
|-------|-------|--------|
| `ErrCertificateMissing` | No certificate was ever recorded | `mneme quality verify <SPEC-ID>` |
| `ErrCertificateStale` | HEAD moved since the certificate was issued | re-verify |
| `ErrCertificateNotGreen` | Verdict is `fail` or `findings` | fix the gates or `quality_ack` the finding, re-verify |
| `ErrConstitutionChanged` | `.mneme/quality.toml` changed since certification | re-verify |
| `ErrWorktreeDirty` | Uncommitted changes right now | commit or discard, re-verify |
| `ErrInvalidConstitution` | The constitution does not parse | fix the file |
| `ErrConstitutionAblated` | Enabled at the spec's base commit, off now | do not disable the mechanism mid-spec |

**Lane trivial is entirely unaffected** — `implementing → audit → done`
still runs exactly as before (`lane_audit`); this mechanism does not gate it
in S1 (see docs/quality.md).

---

## init

Initialise a project with mneme managed blocks and report drift. Applies the
global operating manual and a minimal repo block to `CLAUDE.md` files,
materializes `.mneme/quality.toml` when absent (SPEC-115 D15 — always
`enabled=false`, never overwrites an existing file), then runs the drift
detector. This is the MCP-tool form of `mneme init`; the destructive
legacy-project migration (`--apply` DB writes + `rm-rf`) is **CLI-only** and
not exposed here.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `check` | boolean | no | When `true`, report-only mode: drift is reported but no managed blocks (nor the quality constitution) are written. Default: `false` |
| `repo_root` | string | no | Absolute path to the repository root. Default: current working directory |

**Returns:**

```json
{
  "repo_root": "/path/to/repo", "check_mode": false, "manual_applied": true,
  "repo_block_applied": true, "quality_constitution_applied": true,
  "drift_findings": ["CLAUDE.md: no drift detected"]
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
