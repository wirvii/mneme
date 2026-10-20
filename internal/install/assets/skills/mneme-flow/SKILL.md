---
name: mneme-flow
description: "Use when doing any work that goes through mneme's backlog and specs: the lifecycle and who may move it, the human approval gate, lanes, the criteria contract, rejections, pushbacks and the review range. The other flow skills (feature, bug, trivial, present-spec, review-brief) build on this one."
version: 1.0.0
pinned: false
---

# mneme flow

The common base of the flow skills. The operating manual is the authority on the approval gate and on who may move a spec; this skill explains the cycle around them.

## The cycle

1. `backlog_add`: the lane is proposed to the person and confirmed by them, never inferred.
2. Refine: `grill-me` in standard lane, then `backlog_refine`.
3. `backlog_promote` creates the spec.
4. `spec_advance` draft to speccing: the architect writes the design and `criteria.toml`.
5. specced, then the **human approval gate** (skill `present-spec`).
6. planning, planned, implementing: the implementer of the area delivers.
7. qa (skill `review-brief`), then done.
8. Trivial lane: `spec_quick`, implementing, audit (`lane_audit`), done. See the `trivial` skill.

## Who moves what

| Role | Lifecycle tools it may call |
|---|---|
| orchestrator | all of them |
| subagents | `spec_pushback`, `spec_reject` (qa-tester), `spec_doc_write` |

A subagent never calls `spec_advance`, `spec_quick` or `backlog_archive`: the lifecycle is the orchestrator's.

## The gate

Before a spec leaves `specced`, the whole spec is presented in plain words and the person answers with an explicit yes. The operating manual is the authority on this gate; this skill never relaxes it.

## Criteria

A standard-lane spec needs `criteria.toml` before speccing to specced. It is written only with `spec_doc_write` kind `criteria`. Open `references/criteria.md` when you write or review criteria.

## Rejection and scope

`spec_reject` needs findings that name declared criterion ids. Anything outside the criteria becomes a new `backlog_add`; the person closes the spec after reading the QA report.

## Review range

`spec_advance` returns `review_range` when a spec enters qa. Copy its notice as it is; never recompute it. See `review-brief`.

## Pushback

`spec_pushback` moves the spec to needs_grill. The orchestrator asks the person, then `spec_resolve`.

## One document per spec

`tarea.md` holds the work: `spec_doc_write` kind `task` plus `section` (que, diseno, plan, entrega, revision). The architect writes que, diseno and plan; implementers write entrega; qa-tester writes revision. Older specs keep their own document kinds.

## Archiving

Archiving a backlog item freezes its spec for good. The way back is a new backlog item.

## Lane and depth

Open `references/depth.md` before choosing a lane or deciding whether a stage can be skipped. Open `references/lifecycle.md` for the full transition table.
