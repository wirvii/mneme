---
name: hunt-bug
description: Start a bug investigation using mneme's SDD flow — confirm minimums, search memory, create a ticket with a confirmed lane, and record a diagnosis. Use when the user reports a bug or asks to investigate a failure.
---
<!-- mneme:command:start v=1 -->
Invoke the **hunt-bug** skill now, with $ARGUMENTS as the bug report (it may be empty).

Do not reimplement its steps here: the skill (`~/.claude/skills/hunt-bug/`) is
the single source of truth for the investigation workflow.

If the skill is not available, tell the user to run `mneme install claude-code`
to install it, then retry.
<!-- mneme:command:end -->
