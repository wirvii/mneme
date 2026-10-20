# API Reference — SDD Tools (`backlog_*`, `spec_*`, `lane_*`, `init`)

21 MCP tools over JSON-RPC 2.0 stdio (`mneme mcp`): `backlog_*` (6), `spec_*`
(9), `lane_*` (5), `init` (1). The `sdd_status`/`sdd_import` tools left the
MCP server in v2.0 (SPEC-163) and are CLI-only — see "SDD git-native
commands" below. Concept guide:
[docs/lanes.md](../lanes.md) (trivial/standard lanes, auditor thresholds),
[docs/init.md](../init.md) (managed blocks, drift, legacy migration).
The `quality_*` family (5 tools) existed from SPEC-115 through SPEC-137 and
was retired whole by SPEC-158, along with the certification mechanism it
served — `spec_advance` no longer checks for any certificate anywhere.
[docs/sdd-git-native.md](../sdd-git-native.md) (the SDD git-native
mechanism's file format, write path, and read path, SPEC-130/SPEC-131).
Index: [docs/API.md](../API.md).

**The SDD git-native mechanism has no MCP tools.** `sdd_enable`/`sdd_disable`/
`sdd_export` were never tools — a `--apply` an agent could call unattended
would defeat the human-confirmation gate that publishing a backlog to git
requires — and since v2.0 (SPEC-163) the read-only `sdd_status` and the importing `sdd_import` (SPEC-131 §2b) are gone from the server too. Run
`mneme sdd status [--json]` and `mneme sdd import [--dry-run]` from the
terminal, or from an agent through Bash.

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

**`unreadable`/`unreadable_total` (SPEC-133) name a row whose identity was
readable but whose content was not** — a pre-existing row with a timestamp
or JSON column no mneme write path can produce (a hand-edited database, an
old migration). `total` above still counts every such row: what is
incomplete is the LIST, never the count. Both fields are **absent — not
empty — on a healthy project**, and `unreadable` is truncated to 20 rows
(`unreadable_total` always reports the real, uncapped count):
`{"unreadable": [{"kind": "backlog", "id": "BL-901", "column": "created_at", "reason": "..."}], "unreadable_total": 1}`.

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
  "spec": {"id": "SPEC-001", "uuid": "0198f2c1-9e10-7c3d-4a7b-3f4a5b6c7d8e", "title": "Push notifications", "status": "qa",
    "lane": "standard", "project": "wirvii/mneme", "backlog_id": "BL-001",
    "created_at": "2026-04-30T12:00:00Z", "updated_at": "2026-04-30T14:00:00Z"},
  "history": [{"from_status": "draft", "to_status": "speccing", "by": "orchestrator",
    "reason": "Ready for architect", "at": "2026-04-30T12:30:00Z"},
    {"from_status": "implementing", "to_status": "qa", "by": "backend",
     "reason": "ready for review", "at": "2026-04-30T14:00:00Z",
     "reviewed_sha": "9a3c1234567890abcdef1234567890abcdef1234"}],
  "pushbacks": [{"from_agent": "backend", "questions": ["API contract with auth?"],
    "resolution": "Use service accounts", "resolved": true, "created_at": "2026-04-30T13:00:00Z"}],
  "frozen": {"state": "archived", "backlog_id": "BL-001", "reason": "Superseded by BL-207"},
  "review_range": {"available": true, "from": "abc123...", "from_kind": "base",
    "to": "9a3c1234...", "notice": "primera pasada: revisar desde el commit base abc12345 hasta el extremo entregado 9a3c1234."}
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

**`review_range` (SPEC-157) is present ONLY while `spec.status` is `qa`** —
absent (not `null`) in every other status, so a response outside `qa` stays
byte-identical to one from before this field existed. See "Review frontier
and review range" below `spec_advance` for its full shape and the four
states it can be in.

**Errors:** `-32000` not found, `-32602` missing `id`.

### spec_advance

Advance a spec to its next lifecycle state (standard-lane state machine above).

**`speccing -> specced` requires a declared `criteria.toml` for a
standard-lane spec (SPEC-156 D2).** The document is delivered via
`spec_doc_write` with `kind: "criteria"` — the architect's only write
channel for it. Missing it fails with `no criteria.toml found for
this spec`, naming the exact path and the remedy. This is the ONLY
transition the check applies to: trivial-lane specs, every other standard
transition (including `qa -> done`), and a project with the workflow
directory unconfigured all pass through unaffected — nothing here can ever
block a spec from closing.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID to advance |
| `by` | string | yes | Who triggers the advance (e.g. `orchestrator`, `architect`, `backend`) |
| `reason` | string | no | Optional reason for the transition |

**Returns:** `{spec, executor, review_range?}` (SPEC-068, SPEC-157) — NOT a
bare spec object. `executor` is an advisory `ExecutorResolution` for the
stage just entered (SPEC-068 D2/D5): delegate to a manifest subagent when
`executor.delegate` is `true`, or supply the stage yourself as a conscious
fallback when `executor.degraded` is `true`. `review_range` is present ONLY
when the stage just entered is `qa` (SPEC-157 D5) — see "Review frontier
and review range" right below.

**Errors:** `-32602` invalid transition, missing `id`/`by`, `no criteria.toml
found for this spec` (standard lane, `speccing -> specced`, no declared
criteria document).

### Review frontier and review range (SPEC-157)

Every QA pass looks at a bounded tramo of commits, not "the spec" over and
over: `spec_history` gains one additive column, `reviewed_sha`, filled by
exactly three transitions and meaning ONE of two things — which one is
decided ONLY by the row's own `(from_status, to_status)` pair, never by the
value alone:

| Transition | `reviewed_sha` means | Empty when |
|---|---|---|
| `implementing -> qa` | the **delivered end**: HEAD at the moment the spec entered review — how far the qa-tester is being asked to look | mneme could not read HEAD (`repoDir` unset, or git failed) |
| `qa -> done` / `qa -> implementing` | the **reviewed frontier**: COPIED — never recomputed from HEAD — from the delivered end of the entry that started this pass | the entry into `qa` left no delivered end to copy |
| every other transition | always empty | always |

Copying instead of recomputing is what makes a defect structurally
impossible: if the implementer commits again while QA is still reading, the
frontier still advances only to what was actually handed over — never to
whatever HEAD happens to be when the report is emitted. When that happens,
the persisted `spec_history` reason gets a note appended (see
`spec_reject` below for the exact composition order), and the commits that
landed after the delivered end simply enter the NEXT pass's range.

`review_range` — returned by `spec_advance` only when the stage just
entered is `qa`, and by `spec_status` only while `spec.status` is `qa` — is
the tramo computed from that same column:

```json
{
  "available": true,
  "from": "<sha>", "from_kind": "frontier",
  "to": "<sha>",
  "empty": false,
  "frontier_lost": false, "lost_frontier": "",
  "notice": "revisar desde la frontera anterior <abbr> hasta el extremo entregado <abbr>."
}
```

- `to` is always the CURRENT entry's delivered end, read from its row —
  never `HeadSHA` — so a commit landing after entry never moves it.
- `from`/`from_kind` is either the previous reviewed frontier
  (`from_kind: "frontier"`) or, on a first pass or when the stored frontier
  is no longer an ancestor of `to` (a rebase or squash rewrote it out from
  under it), `spec.base_sha` (`from_kind: "base"`) — the safe direction:
  covering MORE code, never less.
- `empty: true` means `from == to`: nothing changed since the last
  reviewed frontier. Informational only — it never blocks a pass from
  being requested, it only means nothing NEW would be found.
- `frontier_lost: true` names the dead SHA in `lost_frontier` when history
  was rewritten out from under the stored frontier.
- `available: false` replaces every field above with `unavailable`, one of
  three closed causes — never an error, and `spec_advance`/`spec_status`
  keep returning normally either way: no delivered end was recorded; git
  failed while checking whether the frontier is still an ancestor of `to`
  (the frontier is then treated as lost, never as valid); or there is no
  base_sha to fall back on either.
- `notice` is the ONE sentence every surface — CLI, this MCP response, the
  qa-tester's brief, the QA report — shows verbatim. The orchestrator
  copies it into the encargo; it never computes or rewrites the range
  itself.

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

Write a spec entregable (`spec`/`plan`/`qa-report`/`changes`/`criteria`/`task`) to
its workflow directory (SPEC-087 D3, `criteria` added by SPEC-156) — the
path a subagent uses instead of copying its report into the workflow
directory by hand. The destination directory and filename are never
caller-supplied: the directory is derived from the persisted spec record
(`spec.Project`, via `GetSpec`) and the filename comes from a closed,
Go-authored `kind -> filename` map (`spec` → `spec.md`, `plan` → `plan.md`,
`qa-report` → `qa-report.md`, `changes` → `changes.md`, `criteria` →
`criteria.toml`). 0644, parent directories created as needed, plain
overwrite-or-create — no append, no arbitrary read. A sixth kind, `budget`,
existed from SPEC-118 through SPEC-137 and was retired by SPEC-158 along
with the certification mechanism it served; `spec_doc_write` now rejects it
with `ErrUnknownSpecDocKind`, writing nothing.

**`kind = "criteria"` is validated BEFORE anything is written** (SPEC-156):
the content is parsed as a criteria document (`internal/acceptance`) — id
and text required per criterion, every legacy mode-specific key from the
retired evaluator (`mode`, `assert`, `command`, `timeout`,
`evidence_required`) accepted and discarded, unknown keys rejected. No
anchor is resolved against the working tree any more — the evaluator that
used to do that was retired with the rest of the certification mechanism.
Any failure returns without writing a single byte.

**`kind = "criteria"` is restricted to the `architect` role for a subagent
caller** (`internal/cli/hook.go`'s `roleScopedDocKinds`) — an implementer
writing it would be rewriting the exam it is judged by. The rule fails OPEN
when a subagent's role cannot be resolved: closing it would leave the
architect unable to write the document at all the moment `agent_type`
stopped arriving in the payload.

**`kind = "task"` writes ONE section of `tarea.md`** (SPEC-170) — a single
document per spec with five fixed sections, one per role, so no role can
overwrite another's. `section` is required with `task` and forbidden with
every other kind; the five legacy kinds keep writing their own files
unchanged. The ids are ASCII slugs; the headings carry the accents:

| `section` | Heading | Written by (subagent archetypes) |
|-----------|---------|----------------------------------|
| `que` | `## Qué y por qué` | `architect` |
| `diseno` | `## Diseño` | `architect` |
| `plan` | `## Plan` | `architect` |
| `entrega` | `## Entrega` | `backend`, `frontend`, `bug-hunter` |
| `revision` | `## Revisión` | `qa-tester` |

The orchestrator may write any section; `diagnostician` none. The table
lives in `model.TaskSectionAllows` and is enforced by the Claude Code hook
and by the Codex `CallerPolicy`.

Format: a `<!-- mneme:tarea v=1 -->` line, a `# SPEC-NNN — <title>` line, then
five regions fenced by `<!-- mneme:tarea:<section>:start -->` /
`<!-- mneme:tarea:<section>:end -->` marker lines, in the order above. A task
write replaces ONLY the lines between its section's markers; everything else
stays byte for byte. mneme writes the heading itself (a duplicate heading as
the first line of the content is dropped). Any occurrence of
`<!-- mneme:tarea` inside content is written as `&lt;!-- mneme:tarea` so it
can never be mistaken for a marker. The file is created on the first write and
replaced atomically (temp file + rename). If an existing `tarea.md` was
edited by hand and no longer has every marker pair exactly once, in order,
the call fails with `ErrTaskDocMalformed` and writes nothing — fix the file by
hand. `entrega` is replaced whole: when backend and frontend both deliver, the
last writer must include both. The qa-tester reads `tarea.md` and
`criteria.toml` with `Read` (the path is `SpecDocWriteResponse.Path`) and
writes its report with `section: revision`. The response gains
`section`; `created` is true when `tarea.md` did not exist. The lock that
serialises section writes is per mneme process; two processes writing in the
same instant can lose one write (the real flow is sequential).

Errors (all `-32602`): `ErrTaskSectionRequired`, `ErrUnknownTaskSection`,
`ErrSectionNotApplicable`, `ErrTaskDocMalformed`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID (e.g. `SPEC-001`) |
| `kind` | string | yes | `spec`, `plan`, `qa-report`, `changes`, or `criteria` |
| `content` | string | yes | Full document content, written verbatim |

**Returns:** `{"path": "/abs/path/to/qa-report.md", "bytes": 1234, "created": true}`

**Errors:** `-32602` unknown `kind`, missing fields, or (for `kind =
"criteria"`) an invalid document. `-32000` spec not found.

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

**`unreadable`/`unreadable_total` (SPEC-133)** work exactly as documented
under `backlog_list` above, naming a `specs` row instead: absent on a
healthy project, truncated to 20 rows, `unreadable_total` always the real
count. `total` is unaffected — it still counts every matching row.

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

**Findings are the frontier a rejection can refute (SPEC-156 D1/D4).** A
rejection can carry `findings`, each naming the `criterion_id` of a
declared `criteria.toml` entry it violates, plus a `detail` of what fails
about it and optional `evidence`. **When the spec is standard lane, in
`qa` status, and a `criteria.toml` exists and parses**, at least one
finding is required — each `criterion_id` must match a declared id
(`finding names a criterion id that is not declared in criteria.toml`,
naming both the offending id and the full declared set),
and every finding's `detail` must be non-empty. Outside those three
conditions — trivial lane rejecting from `audit`, a rejection from `done`,
or a spec whose `criteria.toml` is missing or does not parse — the
rejection is accepted with or without findings, exactly as it was before
this mechanism existed (the rule is enforced going forward, never
retroactively). A finding that falls outside the declared criteria is
never a legitimate rejection reason: open it as a new backlog item with
`backlog_add` instead — `spec_reject` is reserved for what the spec's own
contract promised.

The persisted `spec_history` reason composes the human-authored reason
with one block per finding:

```
rejected: <reason>

[<criterion_id>] <detail>
      evidencia: <evidence>
[<criterion_id>] <detail>
```

The `evidencia:` line only appears when `evidence` is non-empty. With zero
findings the persisted reason is exactly `"rejected: " + reason`, byte for
byte what it always was.

**SPEC-157's frontier note, when there is one, is appended AFTER all of
the above — never reordered, never rewritten.** `spec_reject` targeting
`qa -> implementing` is one of the three transitions that resolve the
review frontier (see "Review frontier and review range" under
`spec_advance`); when HEAD moved while QA was reviewing, the persisted
reason ends with one more block:

```
rejected: <reason>

[<criterion_id>] <detail>
      evidencia: <evidence>

frontera: la frontera avanza solo hasta el extremo entregado <abbr>; al emitir
el informe el HEAD era <abbr>. Lo que llego despues NO se reviso y entra en
el rango de la siguiente pasada.
```

Fixed order, always: human reason → findings (SPEC-156) → `frontera:` note
(SPEC-157). With HEAD unmoved, or with no delivered end to copy, no
`frontera:` block is appended at all — the reason stays exactly what it
would be without this note.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | yes | Spec ID to reject |
| `reason` | string | yes | Why the spec was rejected (persisted in history) |
| `by` | string | yes | Who triggers the rejection (e.g. `qa-agent`, `orchestrator`) |
| `findings` | array of `{criterion_id, detail, evidence?}` | conditionally | Required only when standard lane + `qa` status + a declared `criteria.toml` — see above |

**Returns:** Updated spec object with `status: "implementing"`.

**Errors:** `-32602` invalid transition (spec not in `qa`/`audit`/`done`),
missing fields, `spec reject requires at least one finding naming a
declared criterion...` (no findings, or a finding with an empty `detail`),
`finding names a criterion id that is not declared in criteria.toml` (an
unknown `criterion_id`). `-32000` not found.

**`mneme spec reject` accepts a repeatable `--finding <criterion-id>=<detail>`**
flag (split on the first `=`, so a detail may itself contain `=`).
Evidence is not settable from the CLI — it is the manual path, and a
detail is enough; a qa-tester subagent attaches evidence via this MCP tool
instead.

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

**`unreadable`/`unreadable_total` (SPEC-133)** work exactly as documented
under `backlog_list` above, naming a `specs` row the underlying listing
could not fully read: absent on a healthy project, truncated to 20 rows,
`unreadable_total` always the real count.

---

## SDD git-native commands (CLI only since v2.0, SPEC-163)

The reading half of the SDD git-native mechanism — bringing a repository's
committed `.mneme/sdd/` Markdown files into the local database — is reached
through the CLI. See [docs/sdd-git-native.md](../sdd-git-native.md) for the
file format and the write path (SPEC-130 §2a) these commands read back from.
Calling the former tool names over MCP now answers `-32601` `unknown tool`.

| Retired MCP tool | CLI command | Notes |
|---|---|---|
| `sdd_status` | `mneme sdd status [--json]` | Read-only. Same fields the tool returned (`RepoRoot`, `Enabled`, `Plan`, `PendingGit`, `Broken`, `Conflicted`, `Incomplete`, `Divergent`, `HooksInstalled`, `OnlyInBaseCount`, `FrozenBlocked`, …) |
| `sdd_import` | `mneme sdd import [--dry-run]` | Decides by anchor, never by correlative. A correlative claimed by two different anchors is skipped and reported by title. Without `--dry-run` it executes |

**Behaviour change to know about:** `sdd_import` used to be denied to
subagents by `lifecycleTools` (`internal/cli/hook.go`). As a CLI command it
is no longer covered by that map; a subagent with Bash can run it, exactly as
it can already run `mneme spec advance`. Denying lifecycle verbs spoken
through Bash is tracked as separate work (SPEC-163 R1).

---

## init

Initialise a project with mneme managed blocks and report drift. Applies the
global operating manual and a minimal repo block to `CLAUDE.md` files, then
runs the drift detector. This is the MCP-tool form of `mneme init`; the
destructive legacy-project migration (`--apply` DB writes + `rm-rf`) is
**CLI-only** and not exposed here. Since SPEC-158 retired the certification
mechanism, `init` never writes a quality constitution; if a repository still
carries a legacy `.mneme/quality.toml` or `.mneme/quality-baseline.toml`
(files the retired mechanism used to own), `init` names each one as an
advisory drift finding — pointing at `mneme upgrade` to remove them — but
never writes or deletes either file itself.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `check` | boolean | no | When `true`, report-only mode: drift is reported but no managed blocks are written. Default: `false` |
| `repo_root` | string | no | Absolute path to the repository root. Default: current working directory |

**Returns:**

```json
{
  "repo_root": "/path/to/repo", "check_mode": false, "manual_applied": true,
  "repo_block_applied": true,
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
