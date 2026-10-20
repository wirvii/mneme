---
name: trivial
description: "Use for a tiny, low-risk change that fits mneme's trivial lane (see mneme-flow's references/depth.md): quick-start the spec, stay strictly inside the declared scope, and let the lane auditor decide."
version: 1.0.0
pinned: false
---

# trivial

Read `mneme-flow` first. The thresholds that make a change trivial live only in `mneme-flow/references/depth.md`; check them there before you start. If any one fails, the item is standard: use `feature` or `bug`.

## Steps

1. `backlog_add` with lane trivial and a `scope` glob naming the files the change may touch. The person confirms the lane.
2. Create the spec: `backlog_promote`, or `spec_new` with the same lane and scope.
3. `spec_quick` with a one to three sentence rationale. It moves the spec straight to implementing.
4. Implement, one of two ways:
   - Delegate to the implementer of the area with this text, unchanged: "This is a trivial-lane item. Stay strictly within the declared scope `<scope>`. Do not refactor adjacent code. Do not add tests beyond updating existing assertions. If the scope is insufficient, stop and report — do not expand."
   - Only if the project turned on inline editing for the orchestrator (see mneme-flow/references/depth.md), edit it yourself, inside the scope.
5. `spec_advance` to audit, then `lane_audit`. It checks files, lines, forbidden paths, scope and public symbols against what was declared. A pass closes the spec.
6. If the audit fails: `lane_reclassify` to standard and continue with the full flow. `lane_override` with a written reason is the last resort.

## Rules

- Stay strictly within the declared scope. If it is not enough, stop and report; do not widen it.
- No refactors of nearby code, no new tests beyond updating existing assertions.
- No public symbol is added, removed or renamed in a trivial item.
- A frozen spec (its backlog item was archived) cannot be audited or overridden.
