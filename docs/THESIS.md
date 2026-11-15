# What mneme is for

mneme is persistent memory for coding agents plus a spec-driven workflow with a human approval gate and per-role permissions. This page says what mneme guarantees and through which mechanism, what it leaves to the agent harness (Claude Code, Codex), what it will not do, and the rule for retiring a mechanism. A design that contradicts this page changes the page first, in its own spec.

## What mneme guarantees

| Guarantee | Mechanism | Enforced in |
|---|---|---|
| Memory per project: what is saved in one project never shows up in another. | Two SQLite databases per host (`global.db`, `projects/<slug>.db`), slug from the git remote, scope filter on every query. | `internal/project`, `internal/store` |
| A standard spec has a human gate: it does not move past `specced` unless the orchestrator advances it after the person's explicit yes, recorded in the advance reason. In the trivial lane the person's decision is the lane itself, declared and never inferred, and a deterministic auditor checks the result; `spec_quick` records a rationale, not a yes. mneme records the yes; it cannot prove a human typed it. | Spec state machine per lane; lifecycle verbs denied to subagents over MCP and over Bash (SPEC-087, SPEC-175); the gate text in the operating manual and `present-spec`. | `internal/service`, `internal/cli/hook.go`, `internal/enforcement` |
| Roles with permissions: designers, reviewers and testers cannot edit code, implementers edit only their areas, and no agent writes `.mneme/sdd/` by hand. | Capability lists (`PermissionTable`), the `enforce-delegation` hook, containment, the SPEC-179 guard. | `internal/subagents`, `internal/enforcement` |
| Criteria contract and bounded rejection: a standard spec declares what it promises before the gate, and a rejection must name a declared criterion. | `criteria.toml` required at `speccing -> specced`; `spec_reject` findings must cite declared ids (SPEC-156). | `internal/service/sdd_contract.go` |
| Review frontier: each QA pass looks only at what was delivered since the last one. | `review_range` computed from `spec_history.reviewed_sha` (SPEC-157). | `internal/service/sdd_frontier.go` |
| Fresh-context review: a standard spec is read by reviewers who never saw the conversation, before the gate and before implementation. mneme requires the report; it cannot prove the review happened. | `revision-spec.toml` / `revision-plan.toml` required at the transitions (SPEC-181). | `internal/specreview`, `internal/service` |
| Delivery contract and stop hooks: an implementer's delivery carries a state, evidence per command and a risk line, and an implementer cannot stop without it. | Validated `entrega` section (SPEC-182); `Stop`/`SubagentStop` blocking decision (SPEC-183). | `internal/delivery`, `internal/service` (SPEC-182); arrives with SPEC-183 |
| Lane by risk: every item declares its lane, never inferred; trivial work is checked by a deterministic auditor. | Lane required at creation; `laneaudit`; the lane rule lives only in mneme-flow/references/depth.md; the risk-based rule arrives with SPEC-184 | `internal/laneaudit`, `internal/service` |

## What mneme delegates to the agent harness

- **Native permissions.** mneme writes the capability lists and registers the hooks; Claude Code and Codex enforce them, and a hook only runs when the harness calls it. A session started with `--dangerously-skip-permissions` switches role enforcement off and mneme cannot notice.
- **Running commands.** Builds, tests, lint and every command a criterion names run in the agent's shell. mneme never runs them.
- **The model.** mneme stores a model alias per role (`mneme model`); which model answers, how well and at what cost belongs to the harness.

## What mneme will not do

- **Run the verifications for the agent.** Since SPEC-158 mneme evaluates no criterion: the QA tester runs what each criterion names. mneme may check the shape of the evidence (SPEC-182); it never re-runs it.
- **Keep a spec from closing without a person's decision.** `qa -> done` has no automatic check (SPEC-156, SPEC-158), and a failed trivial audit is overridden with a recorded reason (`lane_override`).
- **Infer the lane.** It is declared at creation; a trivial spec can be reclassified to standard, never the reverse.
- **Delete your records on its own judgment.** Backlog items, specs and their history are never deleted: archiving an item freezes its spec. Memories are removed when you ask (`mneme mem forget`) or by consolidation, under rules you can read and switch off (`[consolidation]`: `enabled`, `retention_days`, `dedup_threshold`; `[storage]`: `project_budget`, `global_budget`), and conflict detection never deletes anything. No migration drops user data without a release note that names it; migration 023 (SPEC-158) is the one that did.

## The retirement rule

No mechanism is retired whole unless, before the retirement is decided:

1. a measurement period was declared in advance, in the backlog item or spec that introduces or questions the mechanism: what is measured, with which command or query, for how long, and the value that decides; and
2. the period ran, and the spec that retires the mechanism quotes the datum it produced.

"Whole" means a package, an MCP tool family, a CLI command, a hook event or a skill. Fixing, narrowing or renaming part of a mechanism is not a retirement. A mechanism that causes harm may be switched off at once (a configuration value, or out of the default install) while its measurement runs: switching off is not retiring.

Who reads it: the person, at the approval gate of the spec that retires; and the `diseno` role of the [`spec-review` skill](skills.md), which reports a major finding when a spec retires a mechanism whole without the declared period and its datum.

Done right: SPEC-181 shipped the fresh-context review with its measurement declared in [docs/lanes.md](lanes.md): the first 5 reviewed standard specs, read with `mneme spec review-stats --limit 0` once the fifth has 21 days in `done`; keep if `a >= 5` and `d_R < d_B`, remove if `a < 5` and `d_R >= d_B`, adjust otherwise.

## Historical: what was retired whole, and why this rule exists

Measured on 2026-11-15 with `git log --numstat --format='' 2ba81d8 -- cmd internal | awk '{a+=$1; d+=$2} END {print a, d}'`: 275,043 lines added and 82,005 deleted since 2026-04-13, so 30 % of what was written was deleted. Five mechanisms went whole, and none of them after a declared measurement.

| Mechanism | Built | Retired | By | What decided it | Measurement declared beforehand |
|---|---|---|---|---|---|
| Certification (EPIC-calidad: the `quality_*` tools, `mneme quality`, `internal/quality`) | v1.40.0, 2026-08-15 (SPEC-115..SPEC-137) | 2026-10-19, v1.48.0 | SPEC-158 | The decision that nothing in mneme may block a spec from closing on account of a certificate: the only authority is the human reading the QA report (CHANGELOG v1.48.0) | none |
| delivery-v2 (the `work_*` tools) | September 2026, branch `feat/delivery-v2`, 102 commits, betas v1.47.0-beta.1 and beta.2 | betas withdrawn 2026-09-23, never merged | BL-239 | Five defects found closing real work in another project: a manual criterion could never be signed and `work_verify` was unreachable from any reachable state | none |
| Spoken responses (`speech_*`, `mneme speech`, the managed Kokoro engine) | local speech 2026-08-06 (SPEC-111), managed engine v1.34.0 2026-08-07 | engine 2026-08-11 (v1.35.0, SPEC-114); whole 2026-10-20 (v1.49.0) | SPEC-161 | The v1.49 surface reduction | none |
| Team profiles and project templates (`profile_*`, `mneme profile`, `project_new`, `app_add`, `scaffold_capture`) | v1.29.0, 2026-07-17 | 2026-10-20, v1.49.0 | SPEC-162 | The v1.49 surface reduction | none |
| `Stop` hook (session-end reminder) | before v1.30 | 2026-08-04, v1.30.3 | SPEC-106 | Its plain-text stdout never reached the agent | none: the JSON blocking decision was never evaluated, and SPEC-183 brings `Stop` back with it |

The last row is the clearest case: the event was retired because one way of using it failed, and three months later the other way turned out to be the one this project needed.
