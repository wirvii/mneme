# Lanes Reference (v1.6.0)

Lanes introduce two SDD workflow paths — **trivial** and **standard** — so that genuinely small changes can move through the system quickly without bypassing process controls.

## When to Read This

Read this document when:
- You are creating a backlog item or spec and need to choose a lane.
- A spec is stuck in `audit` status and you need to respond to breaches.
- You want to understand the thresholds the deterministic auditor enforces.

## Critical Rules

1. **Lane is required at creation.** `backlog_add` and `spec_new` (MCP and CLI) both require `lane`. There is no default. Omitting it is an error.
2. **`scope` is required for trivial items.** Declare a glob pattern (e.g. `internal/store/*.go`) that describes which files the change is allowed to touch. The auditor uses this to verify compliance.
3. **Classification is explicit — never inferred.** No keyword heuristic, no LLM. The author declares the lane; the auditor validates the claim after the fact.
4. **Standard lane is unchanged.** All existing SDD flows for standard items remain exactly as before. This release adds a path alongside, not a replacement.
5. **Lane is immutable after `implementing`.** Once implementation begins the lane cannot be changed. If the change grew beyond trivial, use `lane reclassify` before implementation starts.
6. **Only trivial→standard reclassification is allowed.** You cannot upgrade a standard spec to trivial.
7. **The auditor is deterministic.** Same diff + same item always produce the same result. No LLM, no subagent, pure Go reading the diff and comparing numbers.
8. **Override is the last resort.** `lane override` advances a failed audit to done but requires a documented reason that is persisted as a discovery memory.
9. **Archiving a spec's backlog item freezes the spec permanently (SPEC-125).** If the backlog item that originated a spec is archived while the spec is still open, the spec can never move again — not even `lane audit`, `lane override`, or `lane reclassify`. This is not a bug: archiving is a deliberate decision to discard the work, and a spec that cannot close is exactly what "discarded" means. There is no unarchive; the way back is to create a NEW backlog item that references the archived one. See "A trivial spec frozen by an archived backlog item" below.

## Trivial vs Standard: Definitions

A change qualifies as **trivial** only if it meets **all** of the following:

| Criterion | Limit |
|-----------|-------|
| Files touched | ≤ 3 |
| Lines changed (added + removed) | ≤ 20 |
| Public symbols | No additions, removals, or renames of exported Go functions/types/consts, MCP tools, CLI flags, or DB columns |
| Forbidden paths | None of: `**/*.sql`, `**/migrations/**`, `**/schema.*`, `cmd/**`, `internal/install/assets/**` |
| Scope compliance | All changed files match the declared `scope` glob |

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

## Standard SDD Flow

Standard flow is unchanged:

```
draft → speccing → (needs_grill ↔ speccing) → specced → planning → planned → implementing → qa → done
```

## Automated Checks

The auditor runs when `lane audit <id>` is called on a trivial spec in `audit` status.

| Check | Threshold | Breach Message |
|-------|-----------|----------------|
| File count | > 3 | `file count N exceeds trivial limit of 3` |
| Line count | > 20 | `line count N exceeds trivial limit of 20` |
| Forbidden: SQL | any `**/*.sql` | `forbidden path modified: <path>` |
| Forbidden: migrations | any `**/migrations/**` | `forbidden path modified: <path>` |
| Forbidden: schema | any `**/schema.*` | `forbidden path modified: <path>` |
| Forbidden: cmd | any `cmd/**` | `forbidden path modified: <path>` |
| Forbidden: install assets | any `internal/install/assets/**` | `forbidden path modified: <path>` |
| Scope | file outside declared scope glob | `out of scope: <path>` |
| Go public symbols | exported name added/removed | `public symbol changed: <name> in <path>` |
| TS/JS exports | `export ` added/removed | `public export changed in <path>` |

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

## The engine underneath (SPEC-118 P11), and the absorption (D12)

`internal/lane` — the package that used to implement this auditor — is
gone. `lane_audit` now runs on the SAME engine the standard lane's budget
mechanism uses: `internal/quality`'s own `Git` (file/symbol delta) and
`EvaluateTrivialBudget` (the verdict), reusing `internal/service`'s own
`symbolExtractorAdapter` for the (now real, cross-language) public-symbol
check instead of the old package's Go-only `go/ast` walk plus a regex
heuristic for TypeScript. The 3/20 limits and the five forbidden globs
are unchanged, migrated verbatim (`DefaultTrivialBudget`); every field of
`lane_audits`, the `audit` status, `lane_reclassify`/`lane_override`, and
the JSON shape `lane_audit` returns are all unchanged.

Two behaviour changes came with the engine swap, both deliberate:

- **`DefaultBaseRef`'s removal**, above.
- **The public-symbol detector got MORE accurate**, not less: it now uses
  the real code-graph extractor's `IsExported` for both Go and
  TypeScript, instead of a `^\+export ` regex heuristic for the latter.

The mechanism's absorption itself (SPEC-118 D12) means the trivial lane's
own certificate requirement is now conditioned on the SAME `[budget]`
switch the standard lane's budget checks use — **not** on `.mneme/quality.toml`'s
top-level `enabled`, which keeps governing gates/coverage/criteria for
standard work exactly as before:

| `.mneme/quality.toml` | `mneme lane audit` |
|---|---|
| absent · schema < 4 · `[budget].enabled = false` | **Direct route.** Computes the delta in-process, evaluates the trivial form, writes `lane_audits`, advances `audit → done`. No certificate, no gates, no graph. This is TODAY's behaviour, on the new engine. |
| schema 4 with `[budget].enabled = true` | **Absorbed route.** Requires a usable certificate for HEAD (`quality.CertificateUsable`) before advancing; without one, `ErrCertificateMissing` naming `mneme quality verify <ID>` as the remedy. |

**Turning `[budget]` on activates the certificate requirement for the
trivial lane too** — `ensureCertified`'s own gate at `implementing → audit`
now checks `[budget].enabled` specifically for trivial specs (leaving the
standard lane's own top-level `enabled` gate untouched). This is why the
project constitution ships with `[budget].enabled = false` by default:
turning it on is a cost decision (a trivial spec now inherits the full
gate/coverage/criteria/graph suite, R3 of the SPEC-118 design), not a free
upgrade.

**SPEC-137 softens what that certificate can fail on, without touching the
audit's own scope/size checks.** The certificate the absorbed route
requires can now only turn red for tree/constitution/gate/criteria
reasons — the budget mechanism's own rows (`budget/*`, `detection/*`,
including the six graph detections) became measure-only in SPEC-137 and no
longer block the verdict. `mneme lane audit` itself is **not** weakened by
this: its own verdict comes from `EvaluateTrivialBudget`, a completely
separate code path from the certificate's `DeriveVerdict`, so a real
scope/size/forbidden-path breach still fails the audit exactly as before,
independent of whether the certificate covering the same commit is green.
What changed is only how EASY it is to obtain that green certificate in
the first place.

## Reject (v1.6.0)

When a QA review finds defects, send the spec backward to `implementing` without changing the spec document:

```bash
mneme spec reject SPEC-012 --reason "payment edge case fails" --by qa-agent
# MCP: spec_reject {id, reason, by}
```

- Standard lane: `qa → implementing`
- Trivial lane: `audit → implementing`

The rejection reason is persisted in `spec_history`. `lane status` reports the total `rejection_count` derived from history (no extra column). Distinct from `spec_pushback` which models ambiguity → `needs_grill`.

## Structured Audit Records (v1.6.0)

Every `lane_audit` run inserts a row in the `lane_audits` table (migration 012) — both passes and failures. `lane status` reads the latest row instead of parsing `spec_history` text, making audit outcomes deterministic and format-independent.

Fields stored: `spec_id`, `passed`, `file_count`, `lines_changed`, `breaches` (newline-joined), `base_sha` (ref used for diffing), `created_at`.

## Lane Stats (v1.6.0)

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
| `reclassify_count` | Specs reclassified trivial → standard |

## How to Fix a Failed Audit

### Option A: Reclassify to standard (preferred)

If the change turned out to be larger than trivial, reclassify it so the full SDD workflow applies:

```bash
mneme lane reclassify SPEC-007 standard --by orchestrator
```

The spec moves to `speccing`. Work through the standard flow from there.

### Option B: Override (last resort)

If the breach is a false positive (e.g. an autogenerated file that does not represent real complexity), document the justification and override:

```bash
mneme lane override SPEC-007 --reason "schema.generated.go is autogenerated; not a real change" --by orchestrator
```

A discovery memory is saved with the reason. The spec advances to `done`.

### Prevention

- Declare a tight `scope` at creation time. If you need to touch more files than the scope allows, the spec should be standard.
- Keep trivial items genuinely small. If the implementation expands, use `lane reclassify` before advancing to `implementing`.
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
