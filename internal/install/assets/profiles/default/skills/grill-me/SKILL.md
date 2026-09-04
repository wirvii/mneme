---
name: grill-me
description: Interview the user one question at a time, always with a recommended answer, to refine a backlog item before promoting it to a spec. Use when the user says "grill me", wants to stress-test a plan, or is refining a lane-standard backlog item before backlog_promote.
version: 1.0.0
pinned: false
---

<!-- grill-me: one-question-at-a-time refinement, SPEC-141 §3.4. -->

## When to Use

Use this skill when:
- The user says "grill me" or asks to be interviewed about a plan or design.
- A **lane-standard** backlog item is being refined — grilling it is mandatory before `backlog_refine`/`backlog_promote`.
- A **lane-trivial** item turns out to be ambiguous — grilling it here is optional; the alternative is reclassifying it to standard.

## Critical Rules

1. **One question at a time. Never a list of questions.**
2. **Every question carries your recommended answer and what each option costs in practice.**
3. **What the code can answer, explore it — don't ask.** With an indexed code graph, consult the graph first.
4. **In lane standard, the result is poured into `backlog_refine` before `backlog_promote`.** An interview that stays only in the chat never happened.
5. **Never delegate to `superpowers:brainstorming` to refine a backlog item.** It writes its own design doc and invokes its own planning step, stepping on the architect's job and saving the spec to the wrong path — and it doesn't ship with mneme, so not every teammate has it installed.
6. **In lane trivial, the interview is optional**: if the item turns out ambiguous, grill it or reclassify it to standard.
7. **Every question is a channel that reaches a person**: no invented metaphor, no untranslated foreign term, every acronym expanded on first use.

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| One question per turn | No message of the interview contains two questions | Ask again, one question at a time |
| Every question recommends | Every question carries a recommended answer and its cost | Add the recommendation before asking |
| Result lives in the ticket | `backlog_get <id>` returns a refinement with the closed decisions | Call `backlog_refine` with the result |
| No brainstorming was used | The interview did not delegate to `superpowers:brainstorming` | Redo the refinement with this skill instead |

## Verification

Run `mneme skills validate grill-me` to execute the deterministic validation script (checks that this file still documents `backlog_refine`, still forbids `superpowers:brainstorming`, and still carries the "one question at a time" discipline).

Run `mneme skills lint grill-me` to confirm the structural format (5 sections, 3-column Automated Checks table, semver, name==directory).

After a refinement: `backlog_get <id>` should show a new refinement row with the closed decisions, not just chat history.

## Workflow

1. Read the ticket (`backlog_get <id>`) and related memories (`mem_search`).
2. Build the decision tree the ticket implies.
3. Walk it one branch at a time, resolving dependencies between decisions, always giving a recommended answer.
4. When the code can answer a question, explore it (codegraph first) instead of asking.
5. Close by pouring the result into `backlog_refine`.
