# Lanes Reference

Lanes introduce two SDD workflow paths — **trivial** and **standard** — so that genuinely small changes can move through the system quickly without bypassing process controls.

## When to Read This

Read this document when:
- You are creating a backlog item or spec and need to choose a lane.
- A spec is stuck in `audit` status and you need to respond to breaches.
- You want to understand what the deterministic auditor checks (SPEC-184: risk, never size).

## Critical Rules

1. **Lane is required at creation.** `backlog_add` and `spec_new` (MCP and CLI) both require `lane`. There is no default. Omitting it is an error.
2. **`scope` is required for trivial items.** Declare a glob pattern (e.g. `internal/store/*.go`) that describes which files the change is allowed to touch. The auditor uses this to verify compliance. Since SPEC-184 the scope must name at least one real path segment and may not use braces: `**`, `*`, `**/*.go` or `{internal,cmd}/**` are refused by `backlog_add`, `spec_new` and `spec_quick` (`scope matches everything or uses braces`).
3. **Classification is explicit — never inferred.** No keyword heuristic, no LLM. The author declares the lane; the auditor validates the claim after the fact.
4. **Standard lane is unchanged.** All existing SDD flows for standard items remain exactly as before. This release adds a path alongside, not a replacement.
5. **Lane is immutable after `implementing`, with one exception.** Once implementation begins the lane cannot be changed — except from `audit` after a failed `lane audit` (SPEC-184), where `lane reclassify` to standard is the way out the auditor names. Before implementation starts, `lane reclassify` is always available.
6. **Only trivial→standard reclassification is allowed.** You cannot upgrade a standard spec to trivial.
7. **The auditor is deterministic.** Same diff + same declaration always produce the same result. No LLM, no subagent, pure Go reading the diff. It never counts files or lines to decide (SPEC-184).
8. **Override is only for a false positive.** `lane override` advances a failed audit to done, for example over a generated file, and requires a documented reason that is persisted as a discovery memory. A real risk item means reclassify.
9. **Only the orchestrator reclassifies or overrides.** `lane_reclassify` and `lane_override` are denied to subagents by MCP, by the Codex caller policy and by terminal (`mneme lane reclassify`, `mneme lane override`); a subagent returns the auditor's result instead (SPEC-175, SPEC-184).
10. **Archiving a spec's backlog item freezes the spec permanently (SPEC-125).** If the backlog item that originated a spec is archived while the spec is still open, the spec can never move again — not even `lane audit`, `lane override`, or `lane reclassify`. This is not a bug: archiving is a deliberate decision to discard the work, and a spec that cannot close is exactly what "discarded" means. There is no unarchive; the way back is to create a NEW backlog item that references the archived one. See "A trivial spec frozen by an archived backlog item" below.

## Trivial vs Standard: Definitions

Since SPEC-184 the lane follows risk, never size. A change qualifies as **trivial** only if all three hold (the agent-facing text is `mneme-flow/references/depth.md`, the only source):

| Criterion | What it means |
|-----------|---------------|
| One batch of reads | After one short round of reading you can say exactly what changes |
| No risk item | It touches none of the six items of `mneme-flow/references/risk.md`: (1) irreversible data or migrations, (2) security and permissions, (3) contracts others consume, (4) concurrency, (5) delivery or environment, (6) no test would catch the regression |
| Resumption test | Someone who was not here could finish it with only the original request and `git diff` |

The number of files or lines never decides, in either direction: a long mechanical change can be trivial, and a one-line change to a migration is not. The orchestrator proposes the lane and the person confirms it. The `spec_quick` rationale ends with two lines, `Resumption: <why>` and `Risk: none`; `spec_quick` refuses a `Risk:` line that names an item (that change is standard) or that does not parse.

Everything else is **standard**.

## Trivial SDD Flow

```
draft ──spec_quick──► rationale ──spec_advance──► implementing ──spec_advance──► audit ──lane_audit──► done
                                                        │
                                                   (pushback)
                                                        │
                                                   needs_grill ──spec_resolve──► rationale
```

`spec_quick` collapses the first two transitions into one step with a rationale string.

**The orchestrator edits inline, on by default (SPEC-171, SPEC-184).** While a
trivial spec is in `implementing`, the orchestrator may use
`Edit`/`Write`/`MultiEdit` on files inside that spec's declared scope. This
default was chosen by the orchestrator on the owner's behalf and is reversible:
turn it off in `~/.mneme/config.toml` with `[delegation]
orchestrator_trivial_inline = false` (or per project under
`[delegation.projects."<slug>"]`, or `MNEME_DELEGATION_TRIVIAL_INLINE=0`).
`Bash` stays blocked, the paths of risk items 1, 2 and 5 are never granted, a
scope that matches everything or uses braces never grants anything, and
`lane_audit` still decides. See `docs/enforcement-model.md`.

## Standard SDD Flow

```
draft → speccing → (needs_grill ↔ speccing) → specced → planning → planned → implementing → qa → done
```

**`speccing → specced` requires a declared `criteria.toml` (SPEC-156 D2).**
A standard-lane spec cannot cross the human approval gate without its
acceptance-criteria document on file — the owner approves the spec's prose
and its measurable boundary in the same act. The architect delivers it via
`spec_doc_write` with `kind: "criteria"`, its only write channel for that
file. Missing it fails `spec_advance` with `no criteria.toml found for
this spec`, naming the exact path. This is the ONLY transition
the requirement applies to — trivial-lane specs are unaffected, and
`qa → done` gains no check at all: nothing here can ever block a spec from
closing.

**A `spec_reject` from `qa`, standard lane, with a declared `criteria.toml`,
must name the criterion each finding violates (SPEC-156 D4/D5).** Findings
outside that declared contract are not a valid rejection reason — open
them as a new backlog item with `backlog_add` instead, with the evidence
still fresh. Three cases are excluded from this requirement, matching the
same forward-only posture as the criteria gate above: a trivial-lane spec
rejected from `audit` (it has no architect and no `criteria.toml` by
design), a rejection from `done` (a post-hoc defect, not a QA pass over a
contract), and a spec whose `criteria.toml` is missing or unparsable (the
rule does not apply retroactively). See
[docs/api/sdd.md](api/sdd.md#spec_reject) for the exact error shapes and
the persisted reason format.

## Revisión sin contexto (SPEC-181)

A standard spec whose design starts after SPEC-181 is **marked** (`specs.review_required = 1`, local to this machine, never exported by SDD git-native): the mark is set when it enters `speccing` from `draft`, and when a trivial spec is reclassified to standard. Specs already in `speccing` or beyond when the binary was installed stay unmarked and are untouched; trivial specs are out of scope; a spec imported from another machine is created unmarked.

For a marked spec `spec_advance` requires a valid and current fresh-review report at three checkpoints:

| Transition | Report | Must not predate |
|---|---|---|
| `speccing → specced` | `revision-spec.toml` (kind `review-spec`, moment 1) | last entry to `speccing`, `tarea.md`, `criteria.toml` |
| `specced → planning` | `revision-spec.toml` again | last entry to `speccing`, `tarea.md`, `criteria.toml` |
| `planned → implementing` | `revision-plan.toml` (kind `review-plan`, moment 2) | last entry to `planning`, `tarea.md`, `criteria.toml` |

The reports live in the spec's workflow directory next to `tarea.md` and are written by the architect with `spec_doc_write`; mneme parses them before writing. **Currency:** timestamps are truncated to the second and a tie counts as current, so any edit of `tarea.md` or `criteria.toml` after the report makes it stale, and so does a return to `speccing` through `needs_grill`. There is no override and no way back from `specced` or `planned`: the way out is to rewrite the report (a report whose every reviewer is `no_corrio` with its `nota` is valid). mneme records the review; it cannot prove it happened.

Each finding leaves by one of five outcomes: `corregir`, `refutar` (with evidence), `escalar` (to the person), `descartar` (nits only) and `riesgo_aceptado` (what is pending after the single re-review). Procedure for the orchestrator, the read-only `spec-reviewer` and the architect: skill `spec-review`.

### The P1 measurement and its decision rule

The review is on trial for the next 5 reviewed standard specs. `mneme spec review-stats --limit 0` (experimental, CLI only) dumps one row per standard spec and computes no rule; the owner applies this one by hand once the fifth reviewed spec has 21 days in `done`:

- **Group R:** the first 5 standard specs with `revisada = sí` that reach `done` (by `done_at`).
- **Group B:** the 5 most recent standard specs with `review_required = 0` whose `done_at` is earlier than the first of R.
- `a` = sum of `corregir` in R (both moments); `d_R` and `d_B` = QA rejections plus reopenings in each group.
- **Keep** if `a ≥ 5` and `d_R < d_B`. **Remove** if `a < 5` and `d_R ≥ d_B`. **Adjust** otherwise.
- Follow-up backlog items citing the spec id (P1-c) are reported apart and stay out of the rule: the duty to cite the id starts with this change, so group B would read systematically low.

## Automated Checks

The auditor runs when `lane audit <id>` is called on a trivial spec in `audit` status.

Since SPEC-184 it checks only what the diff shows, in this fixed order:

| Check | Fails when | Breach Message |
|-------|------------|----------------|
| Scope | a changed file is outside the declared scope glob | `out of scope: <path>` |
| Risk paths (items 1, 2, 5) | a changed path matches a pattern of `mneme-flow/references/risk.md` (plus `[lane.risk_globs]`) | `risk item <n>: path <path>` |
| Exported symbols (item 3) | an exported Go or TS/JS symbol is added or removed | `risk item 3: public symbol changed: <name> in <path>` |
| Declaration missing | no `Risk:` line in `tarea.md` section `entrega` nor in the `spec_quick` rationale | `risk not declared: ...` |
| Declaration malformed | the `Risk:` line does not parse | `risk declaration malformed: <line>` |
| Item declared | the `Risk:` line names an item | `risk declared: item <n>; the trivial lane admits only Risk: none` |
| Contradiction | `Risk: none` but the diff showed items | `risk declaration contradicts diff: Risk: none declared, items <n, m> detected` |

The declaration comes first from the delivery — the `riesgo` field of the SPEC-182 contract block in `tarea.md` section `entrega`, then a `Risk:` line in that section's prose (outside fenced code blocks and outside the block) — and last from the `spec_quick` rationale. File and line counts are recorded in `lane_audits` but never fail an audit. A failed audit returns a `Remedy` that names the risk item and the way out: the orchestrator runs `lane_reclassify` (MCP) / `mneme lane reclassify` (CLI); `lane_override` only for a false positive.

**Base-SHA binding (v1.6.0):** When a spec enters `implementing`, mneme captures the current HEAD SHA as `base_sha` on the spec. On subsequent `lane_audit` runs the base ref is resolved in this order:
1. Explicit `--base <ref>` / `base_ref` argument (caller override).
2. `spec.base_sha` (captured at implementing time — recommended for multi-spec branches).

**Behaviour change (SPEC-118 P11): there is no third step anymore.** The
old auditor used to guess a default (`git merge-base HEAD <default-branch>`,
falling back to `HEAD~1`) when neither of the two above resolved. That
guess is gone, not replaced: with neither a `base_sha` nor an explicit
override, `lane_audit` now returns a clear error instead of silently
picking a base the caller never asked for. Adivinar una base y llamarlo
veredicto is exactly the failure mode this whole EPIC exists to close —
S3 already made the same call for the standard lane's own criteria
(`base-unknown`, never a guess).

## The engine underneath, and why nothing can block it any more (SPEC-158)

`internal/lane` — the original package that implemented this auditor — is
long gone. Its successor, the EPIC-calidad certification mechanism's own
`internal/quality`, is gone too: SPEC-158 retired that whole mechanism, and
with it the "absorption" this section used to describe (a `[budget].enabled`
switch that could require a certificate before a trivial spec's own
`implementing → audit` transition). `lane_audit` now runs on two small,
dedicated leaf packages: `internal/gitrepo` (the git primitives — `NumStat`,
`ChangedFilesInRange`, `ChangedLines`, `FileAtRef`) and `internal/laneaudit`
(`CollectSymbols`/`DiffSymbols` for the file/symbol delta, `Evaluate` for the
verdict since SPEC-184, reading the risk path table of the leaf package
`internal/risk`); every field of `lane_audits`, the `audit` status,
`lane_reclassify`/`lane_override`, and the JSON shape `lane_audit` returns
are all unchanged.

**`lane_audit` now ALWAYS runs direct — no certificate of any kind is ever
involved, regardless of what a repository's `.mneme/quality.toml` (a file
from the retired mechanism, now completely inert wherever it still exists)
declares.** Before SPEC-158, a constitution with `[budget].enabled = true`
could require a usable certificate before this transition; that requirement
is gone along with the mechanism that enforced it. Computing the delta
in-process, evaluating the trivial form, writing `lane_audits`, and
advancing `audit → done` is now the ONLY route, unconditionally.

Two behaviour changes survive from the original engine swap, both
deliberate:

- **`DefaultBaseRef`'s removal**: the old auditor used to guess a default
  base ref when neither `--base`/`base_ref` nor `spec.base_sha` resolved.
  That guess is gone, not replaced — see above.
- **The public-symbol detector got MORE accurate**, not less: it uses the
  real code-graph extractor's `IsExported` for both Go and TypeScript,
  instead of a `^\+export ` regex heuristic for the latter.

## Reject

When a QA review finds defects, send the spec backward to `implementing` without changing the spec document:

```bash
mneme spec reject SPEC-012 --reason "payment edge case fails" --by qa-agent
# MCP: spec_reject {id, reason, by}
```

- Standard lane: `qa → implementing`
- Trivial lane: `audit → implementing`

The rejection reason is persisted in `spec_history`. `lane status` reports the total `rejection_count` derived from history (no extra column). `spec_status.qa_rounds` (SPEC-188) counts only `qa -> implementing` rejections of the current stay, since the last reopening from `done`; the two numbers differ after a reopening. Distinct from `spec_pushback` which models ambiguity → `needs_grill`.

## Structured Audit Records

Every `lane_audit` run inserts a row in the `lane_audits` table (migration 012) — both passes and failures. `lane status` reads the latest row instead of parsing `spec_history` text, making audit outcomes deterministic and format-independent.

Fields stored: `spec_id`, `passed`, `file_count`, `lines_changed`, `breaches` (newline-joined), `base_sha` (ref used for diffing), `created_at`. Since SPEC-184 `file_count` and `lines_changed` only inform; they never decide.

## Lane Stats

```bash
mneme lane stats [--json]
# MCP: lane_stats {project?}
```

Reports trivial-lane compliance for the current project:

| Field | Description |
|-------|-------------|
| `trivial_count` | Total trivial specs |
| `audit_fail_count` | Trivial specs whose latest audit failed |
| `audit_fail_rate` | `audit_fail_count / trivial_count` |
| `override_count` | Specs completed via `lane_override` |
| `reclassify_count` | Specs, of any current lane, reclassified trivial → standard (no longer always 0 since SPEC-184) |
| `risk_breach_count` | Specs whose latest audit failed on a risk item (SPEC-184; absent from the CLI's `--json` when 0, always present over MCP) |

Risk breaches counts every spec whose latest lane audit failed on a risk item, whatever its current lane, so it can exceed audit failures (which only count specs still trivial). Old breach texts `forbidden path modified:` and `public symbol changed:` count as risk too; a missing or malformed declaration does not.

## How to Fix a Failed Audit

### Option A: Reclassify to standard (the way out)

If the audit named a risk item, the change is standard. The orchestrator reclassifies it, from `audit`, so the full SDD workflow applies (SPEC-184):

```bash
mneme lane reclassify SPEC-007 standard --by orchestrator
```

The spec moves from `audit` to `speccing` with its commits already in the branch: fresh-context review, criteria, approval gate, plan and QA follow. Its `base_sha` is kept, so the review range of its QA still covers the trivial commits. If the only breach is a missing `Risk:` line, write `Risk: none` in `tarea.md` section `entrega` (`spec_doc_write` kind `task` section `entrega`) and run `lane audit` again.

### Option B: Override (false positives only)

If the breach is a false positive (e.g. an autogenerated file that does not represent real complexity), document the justification and override:

```bash
mneme lane override SPEC-007 --reason "schema.generated.go is autogenerated; not a real change" --by orchestrator
```

A discovery memory is saved with the reason. The spec advances to `done`.

### Prevention

- Declare a tight `scope` at creation time. If you need to touch more files than the scope allows, the spec should be standard.
- Keep trivial items free of risk items. If the implementation turns out to touch one, use `lane reclassify` before advancing to `implementing`, or from `audit` after the failed audit.
- Never add, remove, or rename exported Go symbols in a trivial item. Even renaming a constant is a public API change.

### A trivial spec frozen by an archived backlog item (SPEC-125)

Neither Option A nor Option B above is available once the spec's originating
backlog item has been archived (`backlog_archive`). `lane reclassify` and
`lane override` — like every other verb that changes a spec's status — are
refused with the same error a frozen spec gives anywhere: the spec's status
can never change again.

This is deliberate, not a gap: archiving requires a reason and cannot be
repeated, so if it happened, someone decided this work was abandoned. A
frozen spec stays fully readable — `lane status`, `spec status`, and
`spec doc write` keep working — it just cannot close, be reclassified, or be
forced through.

**You do not have to attempt a move to find this out (SPEC-126).** `spec
status <id>` prints a `Frozen:` block naming the archived item and its
reason before you ever call `lane override`; `spec list` marks the row with
`— frozen`. Check either one first instead of learning it from a rejected
`lane override`.

**There is no unarchive.** If the work needs to be picked back up, the
agreed way back is to create a NEW backlog item that references the
archived one (e.g. "supersedes BL-190") and start a fresh spec from it —
never to try to reopen the old one.

---

## API reference

Full contract (params, returns, errors, examples) for the 5 `lane_*` MCP
tools: [docs/api/sdd.md](api/sdd.md) →
