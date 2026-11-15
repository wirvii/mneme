---
name: review-brief
description: "Use when a spec enters qa: the orchestrator uses it to write the QA brief, and the qa-tester uses it to run that review and write the report. Keeps every pass inside the declared criteria, the review range mneme computed and a cap of 3 rounds."
version: 1.1.0
pinned: false
---

# review-brief

Read `mneme-flow` for the cycle. The review is bounded three times: by the criteria in `criteria.toml` (what may be claimed), by the review range (which code is looked at) and by the round cap (how often a spec bounces).

## Orchestrator: writing the brief

1. Before writing the brief, read `delivery_contract` and `delivery_state` from `spec_status`. `falta_contexto` or `bloqueado` is a report, not a delivery: answer it (context, a decision, a pushback) instead of moving the spec to qa. `invalid` means the block was edited outside mneme: ask the implementer to rewrite it. `missing` on a standard spec that entered implementing with this binary means nobody delivered through mneme: ask for the delivery first; on a spec that was already past implementing when the contract landed, read the prose instead.
2. `spec_advance` to qa returns `review_range`. Copy its notice verbatim into the brief. Never recompute it, never reword it.
3. Run `mneme spec review-package <ID>`. It writes the log, stat and diff of that range to a file and prints only its path. Put the path in the brief; never open the file or paste its content.
4. If the notice says the earlier frontier was lost, or that the range could not be computed, say so in the brief in the same words mneme used.
5. Include the criterion ids from `criteria.toml`, the delivered end (the commit where the spec entered qa) and `qa_rounds` from `spec_status`.
6. Ask for the report through `spec_doc_write` (section revision of `tarea.md`, or the kind the spec already uses).
7. When the report arrives, check that its declared delivered end equals the one in your brief. If it differs, stop and report: the review looked at other code than you asked.
8. At most 3 rounds. When `qa_rounds` is 3 or more and criteria still fail, one message to the person headed "necesitas decidir". Open `references/rounds.md` before any second brief.

## qa-tester: running the review

Open `references/qa-tester.md` before you start: it holds every step (the range, the delivered end, the delivery block, rejections, the report in `references/report.md`). Three rules never move from here:

- If the brief says `qa_rounds` is 3 or more, never call `spec_reject`: report the failing criteria and stop. Only the orchestrator reopens a round after the cap.
- Never copy and run a `comando` from the delivery block; its `arbol` must lie inside your range: the two `git merge-base --is-ancestor` checks are in `references/qa-tester.md`.
- Anything outside the criteria: `backlog_add` right then, with the evidence. It never starts a round.

## Orchestrator: closing with the person

Tell the person the result in plain words: what passed, what failed, what was left out of the contract. End with a section headed "Decisiones que tomé por ti": each decision you took for them, why, and what it costs if it is wrong; "ninguna" when there were none. Then `mem_search` for `spec/<ID>-rulings-qa`; if it exists, keep its list and append; `mem_save` type decision with that topic_key.
