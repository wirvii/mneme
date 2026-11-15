# Lane and depth

This file is the only place in mneme's agent-facing text that states what makes a change trivial. Every other skill, the manuals and the subagent layer point here. When the rule changes, change it here. The six risk items it names are defined in `risk.md`, next to this file.

## Trivial lane

A change is trivial only if all three hold:

1. **Understood in one batch of reads.** After one short round of reading you can say exactly what changes; nothing needs exploring.
2. **No risk item.** It touches none of the six items in `risk.md`. Before you write `Risk: none`, check whether the code you change is shared by another feature: if it is, that is item 3.
3. **It passes the resumption test.** Someone who was not here could finish it with only the original request and `git diff`.

The number of files or lines never decides the lane, in either direction: a long mechanical change can be trivial, and a one-line change to a migration is not.

Anything else is standard. The orchestrator proposes the lane and the person confirms it; it is never inferred.

## Declared scope

A trivial item carries a `scope` glob naming the files it may touch. It must name at least one real path segment (`internal/store/**`, `docs/*.md`) and may not use braces (`{a,b}`): a glob that matches everything (`**`, `*`, `**/*.go`) or alternates paths is refused. That is a standard change, or a scope you have not thought about yet. The auditor checks every changed file against the scope. If the scope turns out too small, stop and report; do not widen it silently.

## What the trivial rationale carries

`spec_quick` takes one to three sentences, plus two lines the orchestrator writes:

    Resumption: <why the request plus git diff is enough to finish it>
    Risk: none

`spec_quick` checks only the `Risk:` line: it refuses one that names an item (that change is standard) or that does not parse. A missing `Risk:` line is accepted; the delivery may declare it later. The `Resumption:` line is for the person to confirm. The delivery (section `entrega` of `tarea.md`) may restate the risk; the delivery wins over the rationale. A `Risk:` line inside a fenced code block is never read as a declaration.

## What the auditor checks

`lane_audit` (Go, `internal/laneaudit`) is deterministic and checks only what the diff shows:

- every changed file is inside the declared `scope` glob;
- no changed path matches the patterns of items 1, 2 or 5 (listed in `risk.md`);
- no exported symbol is added, removed or renamed (the part of item 3 a diff shows);
- a `Risk:` statement exists, says `none`, and agrees with the three checks above.

Items 4 and 6, the batch of reads and the resumption test are judgments: the orchestrator states them and the person confirms them. The auditor still records how many files and lines changed; that never fails an audit.

## When the change grows

`lane_reclassify` to standard is the only direction allowed, and only the orchestrator runs it, in every case.

- Before implementing starts: `lane_reclassify` to standard.
- After `lane_audit` fails: the failure names the risk item. `lane_reclassify` to standard is the way out; it is allowed from `audit` once an audit has failed. The spec then starts the standard path in `speccing` with its commits already in the branch: fresh-context review, criteria, approval gate, plan, QA. `lane_override` with a written reason is only for a false positive, such as a generated file, and only the orchestrator decides it, after asking the person.

## Orchestrator editing inline (on by default)

The orchestrator may edit with Edit, Write and MultiEdit inside the declared scope of a trivial spec in implementing, never on a path of items 1, 2 or 5. This default was chosen by the orchestrator on the owner's behalf and is reversible: turn it off with `[delegation] orchestrator_trivial_inline = false` (or per project under `[delegation.projects."<slug>"]`, or `MNEME_DELEGATION_TRIVIAL_INLINE=0`). Bash is not relaxed.

## Skipping stages

- Trivial lane skips the architect, the criteria document and the approval gate; the auditor replaces QA.
- Standard lane skips nothing. A stage nobody owns is supplied by the orchestrator, knowingly.
- Depth follows risk, not size.
