---
name: bug-to-issue
description: Turn a confirmed bug diagnosis into an SDD spec, routed through the architect and the human approval gate. Use when the user wants to convert a diagnosis into a fix or promote a bug ticket to a spec.
---
<!-- mneme:command:start v=1 -->
Invoke the **bug-to-issue** skill now, with $ARGUMENTS as the diagnosis (it may be empty).

Do not reimplement its steps here: the skill (`~/.claude/skills/bug-to-issue/`) is
the single source of truth for turning a diagnosis into a spec.

If the skill is not available, tell the user to run `mneme install claude-code`
to install it, then retry.
<!-- mneme:command:end -->
