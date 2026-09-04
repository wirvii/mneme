---
name: grill-me
description: Interview the user relentlessly about a plan or design until reaching shared understanding, resolving each branch of the decision tree. Use when user wants to stress-test a plan, get grilled on their design, or mentions "grill me".
---
<!-- mneme:command:start v=1 -->
Invoke the **grill-me** skill now.

Do not reimplement its steps here: the skill (`~/.claude/skills/grill-me/`) is
the single source of truth for the interview practice — one question at a
time, always with a recommended answer, poured into `backlog_refine` before
`backlog_promote`.

If the skill is not available, tell the user to run `mneme install claude-code`
to install it, then retry.
<!-- mneme:command:end -->
