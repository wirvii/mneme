# SPEC-035 — Graduated Lanes for SDD

**Status:** Ready for implementation
**Target release:** v1.5.0
**Predecessor:** SPEC-034 (Permission Enforcement by Capability, v1.4.0)
**Authoring context:** Design session, May 2026

> **NOTA DE RECONCILIACIÓN (orquestador, 2026-05-29):** Esta spec fue escrita contra rutas/arquitectura idealizadas que divergen de mneme real. Las decisiones de reconciliación del founder están en la memoria `spec/SPEC-035-reconciliation`. Resumen de divergencias resueltas:
> - Rutas reales: modelo `internal/model/sdd.go`, servicio `internal/service/sdd.go`, store `internal/store/`, CLI `internal/cli/`, migraciones `internal/db/migrations/` (próxima `011_`). NO existe `internal/sdd/` ni `internal/storage/`.
> - **Q1 (founder):** el auditor obtiene el diff vía un **git-exec adapter** (`git diff --numstat` + `git diff` contra base ref, default merge-base con la rama por defecto, flag `--base`). Lógica de umbrales = Go puro determinista. mneme no tenía integración de git previa.
> - **Q2 (founder):** superficie = **MCP tools + CLI en paridad** (no solo CLI). Nuevos MCP tools `spec_quick`, `lane_audit`, `lane_reclassify`, `lane_override`, `lane_status` + params `lane`/`scope` en `backlog_add`/`spec_new`.
> - **Q3 (founder):** lane+scope viven en **backlog_items Y specs**, declarado en backlog_add, propagado en promote, settable en spec_new.
> - No existen `mneme plan`/`mneme qa` (solo `mneme spec advance`); el gating trivial/standard va en `spec advance` + comandos lane.
> - Estados reales incluyen `needs_grill` y `planning` (la spec los omite); el flujo estándar se preserva intacto.

-----

## 1. Context & Motivation

After SPEC-034 the orchestrator is physically blocked from editing code outside the doc/config whitelist: `enforce_delegation.sh` exits 2 on every `Edit`/`Write` attempt from the principal (detected by absence of `agent_id`), and the attempt is logged as a `discovery` memory. This closed the **capability** gap.

It did not close the **process** gap.

Empirically, the orchestrator's main historical reason to bypass delegation was the framing *"this change is trivial, doesn't warrant a full SDD cycle"*. The SDD flow today is uniform regardless of change size. A one-line typo correction must traverse the same path as a feature spanning three modules. That uniformity is *the* incentive to defect.

This release introduces **two lanes** with deterministic classification at backlog-creation time, and a post-facto auditor that catches abuse of the trivial lane without relying on LLM judgment.

## 2. Goal

Make trivial changes legitimately fast through SDD, eliminate the orchestrator's incentive to bypass the flow, and detect lane abuse deterministically.

- Every backlog item / spec carries a `lane` field: `trivial` or `standard`.
- `trivial` skips `speccing`, `specced`, `planning`, `planned` — goes directly to `implementing` after a one-line rationale.
- Classification is deterministic, declared at creation, confirmable/changeable by explicit command — never inferred at runtime.
- A post-implementation deterministic auditor checks the actual diff against the lane's scope budget; violations create a `discovery` memory and block transition to `done`.

## 3. Non-goals

- Skills framework — future SPEC.
- Memory conflict surfacing — future SPEC.
- Model-per-phase routing — future SPEC.
- LLM-based classification of triviality — explicitly forbidden (§5.2).
- Auto-classification from a description with no lane flag — explicit declaration required.
- A third lane. Two lanes are enough.
- Backward-fill of lane on existing items past `speccing` (those auto-set `standard`, §5.7).
- Tokenizer fixes for `enforce_delegation.sh` (the `2>&1` issue).
- Resolving the "logging only captures the principal" gap (SPEC-034 §8.1).
- Changes to `tools:` allowlists of any agent.
- Changes to `enforce_delegation.sh` or any hook.

## 4. Background (read before coding) — mneme real layout

- SDD state machine: `internal/model/sdd.go` (`validTransitions` map, `CanTransitionTo`); `internal/service/sdd.go` (`nextForwardStatus`, `SpecAdvance`). States: `draft → speccing → (needs_grill ↔ speccing) → specced → planning → planned → implementing → (qa ↔ implementing/needs_grill) → done`.
- Backlog model: `BacklogItem` (status raw/refined/promoted/archived). Specs and backlog are separate tables; `specs.backlog_id` links them.
- Memory CLI: `mneme save` (top-level), flags `--title --content --type --topic-key --scope --file --importance --stdin --applies-to --severity`. One `content` markdown field. No `--tag`/`--what`/`--why`/`--learned`.
- Orchestrator cannot Edit/Write outside `.claude/**`, `~/.claude/**`, `CLAUDE.md`, `**/docs/*.md`, `.claudeignore`.
- Two hooks coexist; **this release touches neither**.

## 5. Detailed Design

### 5.1 The `lane` and `scope` fields

Migration `011_add_lane.sql` adds to BOTH `backlog_items` and `specs`:

```sql
ALTER TABLE backlog_items ADD COLUMN lane  TEXT NOT NULL DEFAULT 'standard' CHECK (lane IN ('trivial','standard'));
ALTER TABLE backlog_items ADD COLUMN scope TEXT NOT NULL DEFAULT '';
ALTER TABLE specs          ADD COLUMN lane  TEXT NOT NULL DEFAULT 'standard' CHECK (lane IN ('trivial','standard'));
ALTER TABLE specs          ADD COLUMN scope TEXT NOT NULL DEFAULT '';
```

Lane is immutable after `implementing` begins. Enforcement is in Go at transition time, not in SQL.

### 5.2 Classification at creation: explicit, never inferred

No LLM classifier, no keyword heuristic. Declared at creation via explicit flag/param.

- `lane` is **required** at `backlog add` / `spec new` (MCP param + CLI flag). Omitting → error explaining trivial vs standard.
- `scope` is **required when lane=trivial**; optional for standard.
- No global default.

### 5.3 Lane definitions

#### 5.3.1 `trivial` — meets ALL of:
- Touches ≤ **3 files**.
- Touches ≤ **20 lines** total (added + removed).
- Does not add/remove/rename any public symbol (Go exported function/type/const/method; route; CLI flag; DB column; MCP tool).
- Does not modify any file matching `**/*.sql`, `**/migrations/**`, `**/schema.*`, `cmd/**`, or `internal/install/assets/**`.
- Does not change test behavior (assertion string updates only).

#### 5.3.2 `standard` — everything else. Full SDD flow unchanged.

### 5.4 Modified SDD flow for `trivial`

```
draft → rationale → implementing → audit → done
```

`speccing`, `specced`, `planning`, `planned` are skipped, replaced by `rationale` (a 1–3 sentence justification captured by `spec_quick` / `mneme spec quick`). `audit` (NEW) replaces `qa` for trivial items (§5.5).

For `standard`, the flow is unchanged.

### 5.5 Deterministic post-facto auditor

`lane_audit <id>` / `mneme lane audit <id>`:
1. Reads declared `scope` from the spec.
2. Inspects the actual diff (git-exec adapter: `git diff --numstat` + `git diff` against base ref; default merge-base with default branch; `--base` override).
3. Computes: files touched, lines added+removed, out-of-scope paths, forbidden paths (SQL/migrations/install assets/`cmd/**`), public-symbol changes (Go via `go/parser`+`go/ast`; TS heuristic on `export`).
4. Compares against §5.3.1 thresholds.

Outcomes:
- **All pass** → audit OK, advance to `done`, no memory.
- **Any fail** → audit FAILED, stays in `audit`, creates `discovery` memory `Lane audit failed: <id>` listing breaches. User must `lane reclassify <id> standard` (→ moves to `speccing`) OR `lane override <id> --reason "..."` (→ advances to `done`, persists override as another `discovery` memory).

**Deterministic. No LLM. No subagent.** Pure Go reading the diff and comparing numbers.

### 5.6 Memory shape (SPEC-034 conventions verbatim)

`mneme save --type discovery --title "Lane audit failed: SPEC-XXX" --content "<markdown blob>"` with breaches listed. Override: `--title "Lane override applied: SPEC-XXX"`. No `--tag`, no `--what/--why/--learned`.

### 5.7 Migration for existing items
- Items past `speccing` → `lane='standard'` (DEFAULT handles it). Proceed as before.
- Items in `draft` → `lane='standard'`; user can reclassify before advancing.
No data lost, no forced reclassification.

### 5.8 New surface (MCP tools + CLI parity — founder Q2)

MCP tools: `spec_quick`, `lane_audit`, `lane_reclassify`, `lane_override`, `lane_status`; `lane`/`scope` params added to `backlog_add` and `spec_new`.

CLI:
```
mneme backlog add --lane <trivial|standard> [--scope <glob>] "<title>"
mneme spec new --lane <trivial|standard> [--scope <glob>] "<title>"
mneme spec quick "<1-3 sentence rationale>"     # trivial only; rejects standard
mneme lane audit <id>
mneme lane reclassify <id> <trivial|standard> [--scope <glob>]
mneme lane override <id> --reason "<text>"
mneme lane status <id>
```

`mneme spec advance` becomes lane-aware. For trivial items, the standard speccing/planning transitions error with a message pointing to `spec quick` / `lane audit`. Standard items: `spec advance` unchanged.

`reclassify`: trivial→standard allowed up to `implementing`; after `implementing` only trivial→standard (no late upgrade to trivial). Trivial→standard moves the item to `speccing`.

### 5.9 Orchestrator instructions update

Repo-root `CLAUDE.md` (and the install-embedded orchestrator prompt if one exists) gets a "Lanes" section: when a user adds an item without a lane, the orchestrator must ask "trivial (≤3 files, ≤20 lines, no SQL, no public API change) or standard?"; may propose but never assign without confirmation; never edits code itself; dispatches trivial items to implementers with a `spec quick` rationale.

### 5.10 Implementer subagent updates (per-task, no frontmatter/prompt-body changes)

On a trivial item, the orchestrator injects in the task payload: *"This is a trivial-lane item. Stay strictly within the declared scope `<scope>`. Do not refactor adjacent code. Do not add tests beyond updating existing assertions if a string changed. If the scope is insufficient, stop and report — do not expand."*

## 6. File map (reconciled to mneme real layout)

| File / area | Action |
|---|---|
| `internal/db/migrations/011_add_lane.sql` | NEW — lane+scope columns + CHECK on backlog_items and specs |
| `internal/model/sdd.go` | EDIT — Lane/Scope fields on BacklogItem & Spec; lane-aware `validTransitions`/`nextForwardStatus`; new states `rationale`, `audit` |
| `internal/store/` (SDD store) | EDIT — CRUD reads/writes lane+scope; propagate on promote |
| `internal/service/sdd.go` | EDIT — lane logic in BacklogAdd/SpecNew/SpecAdvance/BacklogPromote; SpecQuick; lane methods (audit/reclassify/override/status) |
| `internal/lane/audit.go` | NEW — deterministic auditor (pure thresholds + Go AST) |
| `internal/lane/git.go` (or `internal/git/`) | NEW — minimal git-exec diff adapter |
| `internal/lane/audit_test.go` | NEW — table-driven tests for every threshold |
| `internal/cli/backlog.go`, `spec.go` | EDIT — `--lane`/`--scope` flags; `spec quick`; lane-aware advance |
| `internal/cli/lane.go` | NEW — `audit`/`reclassify`/`override`/`status` |
| `internal/mcp/tools.go`, `handlers.go` | EDIT — register new tools + params |
| `CLAUDE.md` (root) | EDIT — "Lanes" section |
| `docs/lanes.md` | NEW — full reference (Critical Rules + Automated Checks + How to Fix) |
| `CHANGELOG.md` | EDIT — `[v1.5.0]` entry |

## 7. Acceptance criteria
(See original; reconciled: surface = MCP+CLI; auditor uses git-exec adapter; lane+scope on both tables.)

- Migration adds lane (CHECK, default standard) + scope to backlog_items and specs; backfill is the DEFAULT.
- Models gain `Lane`/`Scope`; serialised by all read/write paths; propagated on promote.
- `backlog_add`/`spec_new` (MCP + CLI) error if lane missing; error if lane=trivial and scope missing.
- `spec_quick` works only for trivial; rejects standard.
- `lane_audit` reports all threshold checks (file count, line count, forbidden paths, public-symbol changes, scope match), deterministically.
- `lane_reclassify` trivial→standard up to implementing; only trivial→standard after; trivial→standard moves to speccing.
- `lane_override` requires reason, persists discovery memory.
- `lane_status` shows lane, scope, state, latest audit.
- State machine: trivial = `draft→rationale→implementing→audit→done`; standard preserved exactly; no skipping within a lane.
- Auditor deterministic (same diff+item → same result, no LLM); detects all breaches; passes a real one-typo change; fails a 5-file/47-line/install-assets change with all breaches listed.
- Audit/override memories use `mneme save --type discovery --title --content` only.
- Docs: `docs/lanes.md`, CLAUDE.md Lanes section, CHANGELOG v1.5.0.
- Tests: table-driven auditor tests; state-machine tests (lanes don't mix); CLI/MCP required-flag errors; marker asset test if prompt embedded; `make test`, `make test-race`, `golangci-lint run` clean.

## 8. Manual validation (post-merge)
Trivial happy path; trivial abuse path (auditor catches, memory created, reclassify); standard path unchanged; override path. (See original spec for exact commands.)

## 10. Anti-scope
NOT touching hooks/allowlists; NO LLM in classification/audit (pure Go); NO new memory flags; NO third lane; NO refactor of standard flow; NO 2>&1 fix; NO subagent-logging gap. ONLY: lane+scope field, two trivial states, deterministic auditor, the lane commands/tools, docs, tests.

-----
**End of spec (reconciled).**
