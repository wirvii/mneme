---
name: mneme-init
description: Bootstrap or refresh this project with mneme. Thin wrapper that invokes the mneme-init skill (repo analysis + memory seeding + managed CLAUDE.md blocks, plus opt-in subagents / codegraph / team-memory). Use when the user says "mneme-init", "initialize mneme", or "onboard this repo".
---

Invoke the **mneme-init** skill now.

Do not reimplement its steps here: the skill (`~/.claude/skills/mneme-init/`) is
the single source of truth for the project-init workflow — core repo analysis and
memory seeding, mneme's managed CLAUDE.md blocks (which replace the native
`/init`), and the three independent opt-in steps (per-project subagents,
codegraph indexing, shared team memory).

If the skill is not available, tell the user to run `mneme install claude-code`
to install it, then retry.
