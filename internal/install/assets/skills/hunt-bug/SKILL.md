---
name: hunt-bug
description: Start a bug investigation the way this project's SDD flow expects — minimums confirmed, memory searched, a ticket created with a confirmed lane, and a diagnosis (never a fix) recorded back to it. Use when the user reports a bug, asks to investigate a failure, or says "hunt this bug".
version: 1.0.0
pinned: false
---

<!-- hunt-bug: bug-investigation entry point rewritten for mneme's SDD flow, SPEC-141 §3.5. -->

## When to Use

Use this skill when the user reports a bug, describes something that used to work and no longer does, or asks to start investigating a failure.

## Critical Rules

1. **The investigation's state lives in the mneme ticket (`backlog_add`/`backlog_refine`). Never create folders or files by hand to hold it.**
2. **Minimums before delegating**: a symptom, expected vs. observed behavior, **at least one concrete fact** (an identifier, a date, a tenant, the command that was run), and how to reproduce it. If something is missing, ask for it **one question at a time** (this is `grill-me`'s own practice) before launching anyone.
3. **Search memory (`mem_search`) before investigating**: this repository keeps past failures in the same module and the decisions that explain them.
4. **The ticket is created with `backlog_add` and a lane that is proposed and confirmed, never inferred.**
5. **The investigation ends in a diagnosis, not a fix.** A fix goes through SDD.
6. **Delegate to the investigator role if the project has one; if it does not, the orchestrator does it, knowingly, as a stand-in — correction, not excellence.**
7. **The diagnosis goes back to the ticket with `backlog_refine`.** Never create or advance a spec from here — that is `bug-to-issue`'s job, gated by human approval.

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| Minimums present before delegating | Symptom, expected vs. observed, one concrete fact, and reproduction steps were all confirmed | Ask for the missing item, one question at a time |
| Memory consulted | `mem_search` ran before delegating the investigation | Run `mem_search` with the module/symptom keywords |
| Lane confirmed by the person | The lane on the new ticket was proposed and explicitly confirmed, not assumed | Ask the person to confirm trivial vs. standard |
| Diagnosis recorded in the ticket | `backlog_get <id>` shows a refinement with the diagnosis, not just chat history | Call `backlog_refine` with the diagnosis |

## Verification

Run `mneme skills validate hunt-bug` to execute the deterministic validation script (checks that this file still documents `backlog_add`/`backlog_refine`, still mentions lane, and never names the path this rewrite abandoned).

Run `mneme skills lint hunt-bug` to confirm the structural format.

After delegating: `backlog_get <id>` should return a refinement with the diagnosis. Before delegating: confirm all four minimums are present and the lane was confirmed by the person, not assumed.

## Workflow

1. Read the bug report.
2. Check the minimums (rule 2); ask for whatever is missing, one question at a time.
3. Run `mem_search` for prior failures in the same module.
4. Create the ticket with `backlog_add`, proposing a lane and waiting for confirmation.
5. Delegate the investigation to the investigator role, or take it on yourself as a declared stand-in.
6. Collect the diagnosis.
7. Record it with `backlog_refine`.
8. Save a `bugfix`/`discovery` memory (`mem_save`).
9. Stop and say what the next step is.
