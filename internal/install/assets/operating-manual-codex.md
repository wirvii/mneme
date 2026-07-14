# mneme Operating Manual (Codex)

Injected globally into ~/.codex/AGENTS.md by `mneme install codex`. Always active.
Full reference: `docs/` in the mneme repo and `mneme help`.

<!-- Note: this file must stay well below 32 KiB (Codex project_doc_max_bytes
     default). Current size is intentionally kept near ~3 KiB. -->

## §1 How to launch

Run Codex as: `codex`

You are the **single agent**: there are no subagents, no orchestrator, and no
read-only roles. One agent reads memory, follows SDD, and implements changes.
Do not expect role separation — it does not exist in this setup.

## §2 Single-agent model

In the Claude Code integration, mneme separates an orchestrator (read-only, delegates)
from implementer subagents. That model does NOT apply here.

In Codex you:
1. Read relevant memories with `mem_context` + `mem_search`.
2. Manage the backlog and spec lifecycle yourself (no handoffs).
3. Implement, test, commit — all in the same session.
4. Save discoveries and decisions before ending.

There is no hook blocking edits. There are no role boundaries enforced by the
toolset. Self-discipline is the only gate: follow SDD, save memories, do not
skip steps.

## §3 SDD + lanes

Every change flows through SDD — no ad-hoc edits.

**Lane declaration is required at creation:**
- `trivial` — ≤3 files, ≤20 lines, no SQL/migrations, no public API change.
- `standard` — everything else.

State machine: `backlog_add` → refine → `backlog_promote` → `spec_advance` × N → `qa` → `done`.
`spec_reject` bounces a failed QA back to implementing.
`spec_pushback` pauses a spec at `needs_grill` until ambiguity is resolved.

**Human approval gate (unbreakable).** Even as the single agent that both designs and
implements, you MUST present the complete spec to the human and wait for EXPLICIT
approval before advancing a spec past `specced` into planning/implementation.
Answering design questions is NOT approval. The only exception is an explicit,
one-time authorization from the human to skip the gate for that specific spec; it is
never inherited and never a default.

As the single agent you traverse the full cycle: backlog → spec → implement → qa → done.

## §4 Skills

Bundled skills are installed to `$HOME/.agents/skills` for Codex to discover.

Check available skills: `mneme skills list`.
Validate before relying on: `mneme skills lint [name]` / `mneme skills validate <name>`.

**Note:** the MCP tools `skills_*` manage `~/.claude/skills` (hardcoded in the
mneme server). Skills copied to `$HOME/.agents/skills` at install time are
available to Codex but not managed by the tools in this session.

## §5 Memory & conflicts

Save decisions, discoveries, bugfixes, conventions to mneme — never rely on session
history alone.

Session lifecycle:
- FIRST MESSAGE: `mem_context`, then `mem_search` with keywords. `spec_list` to see active specs.
- EVERY user message: `mem_search` before responding.
- AFTER completed task: `mem_save` (decision/discovery/bugfix/convention). Use `topic_key` for evolving knowledge.
- BEFORE session end: `mem_session_end` with summary.
- LONG tasks: `mem_checkpoint` periodically.
- POST-COMPACTION: `mem_context` to recover context.

Save rules: `scope:global` for user preferences, `scope:project` for everything else.
`topic_key` for knowledge that evolves (overwrites). Omit for unique events. Save liberally.

Conflict hygiene: `mneme conflicts scan` periodically to surface superseded memories.

**This section is the safety net:** if the session hooks (SessionStart/Stop) are
not trusted yet (`/hooks` in Codex TUI), the memory discipline described here
keeps your project knowledge intact without automation.

## §6 Code graph: consult it FIRST

MANDATORY: when this project has an indexed code graph (`mneme codegraph`), you
MUST consult the graph BEFORE reading or grepping source to locate a symbol,
find its callers, or assess the blast radius of a change: `codegraph_search` /
`codegraph_context` / `codegraph_callers` / `codegraph_callees` /
`codegraph_impact`. Fall back to Read/Grep only for the literal text the graph
can't provide, or when it is stale or the repo is not indexed. Measure adoption
with `mneme codegraph adoption`.
