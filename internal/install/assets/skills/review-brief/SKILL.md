---
name: review-brief
description: "Use when a spec enters qa: the orchestrator uses it to write the QA brief, and the qa-tester uses it to run that review and write the report. Keeps every pass inside the declared criteria and the review range mneme computed."
version: 1.0.0
pinned: false
---

# review-brief

Read `mneme-flow` for the cycle. The review is bounded twice: by the criteria in `criteria.toml` (what may be claimed) and by the review range (which code is looked at).

## Orchestrator: writing the brief

1. `spec_advance` to qa returns `review_range`. Copy its notice verbatim into the brief. Never recompute it, never reword it.
2. If the notice says the earlier frontier was lost, or that the range could not be computed, say so in the brief in the same words mneme used.
3. Include the criterion ids from `criteria.toml` and the delivered end (the commit where the spec entered qa).
4. Ask for the report through `spec_doc_write` (section revision of `tarea.md`, or the kind the spec already uses).
5. When the report arrives, check that its declared delivered end equals the one in your brief. If it differs, stop and report: the review looked at other code than you asked.
6. Tell the person the result in plain words: what passed, what failed, what was left out of the contract.

## qa-tester: running the review

1. Review only the range in the brief. Earlier code was already reviewed.
2. Declare the delivered end you received at the top of the report. If it does not match what you actually checked out, stop and report.
3. Reject with `spec_reject` only for a declared criterion, and give findings that name its id, with detail and evidence. Nothing else authorizes a rejection.
4. Anything outside the criteria, however serious it looks: `backlog_add` right then, with the evidence. There is no "out of contract but blocking".
5. Write the report with the two sections in `references/report.md`. Open it before you write.
6. Never call `spec_advance`. Open the real screen when the change is visible; say plainly when no browser was available.
