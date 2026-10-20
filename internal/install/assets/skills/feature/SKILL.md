---
name: feature
description: "Use when the person asks for something new: a capability, command, screen or behaviour that does not exist yet. Takes it from idea to a done spec through mneme's flow, stopping at the human approval gate."
version: 1.0.0
pinned: false
---

# feature

Read `mneme-flow` first for the cycle and the roles. This skill is the path for something new.

## Steps

1. `mem_search` for earlier decisions, conventions and related bugs in the area.
2. `backlog_add` with a proposed lane. Wait for the person to confirm the lane; never decide it alone. If the lane is unclear, open `mneme-flow/references/depth.md`.
3. If the lane is trivial, switch to the `trivial` skill and stop reading here.
4. Standard: run `grill-me` with the person, then pour the result into `backlog_refine`.
5. `backlog_promote`, then `spec_advance` to speccing and delegate to the architect for the design and `criteria.toml`.
6. Use the `present-spec` skill. Do not move on without an explicit yes to the whole spec.
7. After the yes: `spec_advance` through planning and planned to implementing. Delegate to the implementer who owns the area; if none exists, do it yourself and say so.
8. `spec_advance` to qa and use the `review-brief` skill.
9. Show the QA report to the person in plain words. They decide whether the spec is done.
10. `mem_save` what was decided and learned.

## Do not

- Do not skip the gate because the person sounded enthusiastic. A comment on the idea is not approval.
- Do not let findings outside the criteria bounce the spec; they become new `backlog_add` items.
