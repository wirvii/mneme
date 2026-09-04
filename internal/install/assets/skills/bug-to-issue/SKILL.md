---
name: bug-to-issue
description: Turn a confirmed bug diagnosis already recorded in the ticket into an SDD spec — never promote a suspicion, always route lane-standard tickets through the architect and the human approval gate. Use when the user wants to convert a diagnosis into a fix, says "turn this into an issue", or asks to promote a bug ticket to a spec.
version: 1.0.0
pinned: false
---

<!-- bug-to-issue: diagnosis-to-spec entry point rewritten for mneme's SDD flow, SPEC-141 §3.6. -->

## When to Use

Use this skill when a bug's diagnosis is already recorded in its mneme ticket and the user wants to turn it into work: a fix, a spec, or a promoted backlog item.

## Critical Rules

1. **Start from the diagnosis already in the ticket (`backlog_get`), not from a memory summary.**
2. **If the root cause is not identified with confidence, do not promote it.** Say so, and go back to investigating. Promoting a suspicion turns a hypothesis into real work for several people.
3. **Write the fix proposal (scope, files, risk, lane) with `backlog_refine`.** There is no template on disk — the ticket is the medium.
4. **`backlog_promote` creates the spec in `draft`.**
5. **Lane standard always goes through design.** `spec_advance` moves the spec from draft to speccing, and the architect writes `spec.md` with `spec_doc_write`. This is the rule the previous version of this workflow stated as "ALWAYS go through the Architect, no exceptions" — still in force.
6. **Lane trivial: `spec_quick` with its justification**, staying inside the declared scope. This path did not exist when the previous workflow was written; this is the honest update of the same rule, not a relaxation of it.
7. **The human approval gate is unbreakable**: the complete spec is presented in plain language and explicit approval is awaited before advancing past `specced`. Answering design questions is not approval.
8. **The architect never advances the spec. The orchestrator advances it; a subagent, never.**

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| Root cause confidence declared before promoting | The confidence in the root cause (confirmed / highly likely / low) was stated before `backlog_promote` ran | State the confidence level, and keep investigating instead of promoting if it is low |
| Proposal written in the ticket | `backlog_get <id>` shows a refinement with the fix proposal before `backlog_promote` | Call `backlog_refine` with the proposal first |
| Lane standard passed through design | A standard-lane spec has a `spec.md` written by the architect via `spec_doc_write` | Advance through `speccing` and have the architect write the spec |
| Human gate recorded before advancing | Explicit approval was obtained before advancing past `specced` | Present the complete spec in plain language and wait for an explicit yes |

## Verification

Run `mneme skills validate bug-to-issue` to execute the deterministic validation script (checks that this file still documents `backlog_promote`/`spec_quick` and the human approval gate, and never names the path this rewrite abandoned).

Run `mneme skills lint bug-to-issue` to confirm the structural format.

Before promoting: confirm the root-cause confidence out loud. Before advancing past `specced`: confirm the human said yes, in words, to the spec as a whole.

## Workflow

1. `backlog_get <id>` to read the diagnosis.
2. Judge the confidence in the root cause; stop and keep investigating if it is low.
3. Write the fix proposal with `backlog_refine`.
4. `backlog_promote`.
5. Standard lane: advance to `speccing` and have the architect write the spec. Trivial lane: `spec_quick` with its justification, staying inside the declared scope.
6. Present the complete spec in plain language.
7. Wait for explicit approval.
8. Advance past `specced`.
