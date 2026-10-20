---
name: bug
description: "Use when something that used to work fails, the person reports a bug, or asks to investigate a failure or turn a diagnosis into a fix. Covers both halves: investigate to a diagnosis recorded in the ticket, then promote it to a spec only when the root cause is confirmed."
version: 1.0.0
pinned: false
---

# bug

Read `mneme-flow` for the cycle. Two halves: investigate, then (only if the cause is sound) turn it into work.

## Half 1: investigate

1. Before delegating, make sure you have: the symptom, expected versus observed behaviour, one concrete fact, and how to reproduce it. Ask for what is missing, one question at a time.
2. `mem_search` for earlier bugs in the same area.
3. `backlog_add` with the lane confirmed by the person.
4. Delegate to the bug-hunter or the diagnostician. If none exists, investigate yourself and say so.
5. The result goes into the ticket with `backlog_refine`. Open `references/diagnosis.md` for what a diagnosis carries.
6. This half ends in a diagnosis, not a fix. Nobody edits code here.

## Half 2: turn it into work

1. Read the ticket with `backlog_get`.
2. State the confidence in the cause: confirmed, very likely, or low. With low confidence do not promote; investigate more.
3. Add a proposal (scope, files, risk, lane) with `backlog_refine`, then `backlog_promote`.
4. Standard lane: architect, then the `present-spec` skill. Trivial lane: the `trivial` skill.

## Do not

- Do not fix during the investigation; the fix has its own spec.
- Do not promote a guess. A confirmed cause comes with evidence anyone can rerun.
