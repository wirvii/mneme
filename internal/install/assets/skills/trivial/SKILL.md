---
name: trivial
description: "Use when making a tiny, low-risk change that fits mneme's trivial lane (see mneme-flow's references/depth.md): quick-start the spec, stay strictly inside the declared scope, and let the lane auditor decide."
version: 1.1.0
pinned: false
---

# trivial

Read `mneme-flow` first. The criterion that makes a change trivial lives only in `mneme-flow/references/depth.md`, and the six risk items it names in `mneme-flow/references/risk.md`; check both before you start. If any part fails, the item is standard: use `feature` or `bug`.

## Steps

1. `backlog_add` with lane trivial and a `scope` glob naming the files the change may touch. The scope must name at least one real path segment and may not use braces: `**`, `*` or `{a,b}` are refused. The person confirms the lane.
2. Create the spec: `backlog_promote`, or `spec_new` with the same lane and scope.
3. `spec_quick` with a one to three sentence rationale that ends with two lines: `Resumption: <why the request plus git diff is enough to finish it>` and `Risk: none`. It moves the spec straight to implementing. A `Risk:` line that names an item is refused: that change is standard.
4. Implement, one of two ways:
   - Edit it yourself, inside the scope and never on a path of risk items 1, 2 or 5. This is the default unless the project turned it off (see `mneme-flow/references/depth.md`).
   - If it is turned off, delegate to the implementer of the area with this text, unchanged: "This is a trivial-lane item. Stay strictly within the declared scope `<scope>`. Do not refactor adjacent code. Do not add tests beyond updating existing assertions. If the scope is insufficient, stop and report — do not expand."
5. `spec_advance` to audit, then `lane_audit`. It checks the declared scope, the paths of risk items 1, 2 and 5, exported symbols, and that the `Risk:` line says none and agrees with the diff. File and line counts never fail it. A pass closes the spec.
6. If the audit fails, the failure names the risk item. The way out is `lane_reclassify` to standard, also from audit, and only the orchestrator runs it; then the full flow continues. `lane_override` with a written reason is only for a false positive, such as a generated file, and only the orchestrator decides it, after asking the person; a subagent returns the auditor's failure instead.

## Rules

- Stay strictly within the declared scope. If it is not enough, stop and report; do not widen it.
- No refactors of nearby code, no new tests beyond updating existing assertions.
- No public symbol is added, removed or renamed in a trivial item.
- A frozen spec (its backlog item was archived) cannot be audited or overridden.
