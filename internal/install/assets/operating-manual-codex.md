# mneme Operating Manual (Codex)

Injected globally into ~/.codex/AGENTS.md by `mneme install codex`. Always active.
Full reference: `docs/` in the mneme repo and `mneme help`.

**§7 is binding on every message a person reads.** Read it before you write anything
to the operator.

<!-- Note: this file must stay well below 32 KiB (Codex project_doc_max_bytes
     default). Current size is intentionally kept near ~3 KiB. -->

## §1 How to launch

Run Codex as: `codex`

Codex uses mneme's project roles from `.codex/agents/*.toml`. The coordinator
owns the SDD lifecycle and delegates design, implementation and QA when the
project manifest provides the corresponding role. `mneme init` generates both
Codex and Claude Code projections from one project contract.

## §2 Role model

Claude Code and Codex use the same semantic roles and guarantees. Their file
formats differ, but responsibilities, ownership areas and reserved lifecycle
transitions do not.

As coordinator you:
1. Read relevant memories with `mem_context` + `mem_search`.
2. Manage the backlog and spec lifecycle; subagents never advance it.
3. Delegate to the project role named by `spec_advance` when available.
4. Save discoveries and decisions before ending.

PreToolUse hooks enforce project ownership. Each generated role also starts a
role-bound mneme MCP server whose filtered tool list and call-time checks keep
reserved lifecycle and quality operations unavailable to the wrong role.
Do not treat the role's declared sandbox as an independent boundary: Codex can
inherit the coordinator's workspace permissions. These guarantees require
Codex 0.148.0-alpha.19 or newer and trusted hooks (`/hooks`). If a
required role or hook identity cannot be resolved safely, stop and report the
gap instead of weakening its permissions.

## §3 SDD + lanes

Every change flows through SDD — no ad-hoc edits.

**Lane declaration is required at creation:**
- `trivial` — ≤3 files, ≤20 lines, no SQL/migrations, no public API change.
- `standard` — everything else.

State machine: `backlog_add` → refine → `backlog_promote` → `spec_advance` × N → `qa` → `done`.
`spec_reject` bounces a failed QA back to implementing.
`spec_pushback` pauses a spec at `needs_grill` until ambiguity is resolved.

**Human approval gate (unbreakable).** You MUST present the complete spec to the human and wait for EXPLICIT
approval before advancing a spec past `specced` into planning/implementation.
Answering design questions is NOT approval. The only exception is an explicit,
one-time authorization from the human to skip the gate for that specific spec; it is
never inherited and never a default.

The coordinator traverses the full cycle: backlog → spec → implement → qa → done.

**Refinamiento: grill-me, no brainstorming.** For a **standard**-lane item,
refine it with `grill-me` (one question at a time, recommending an answer at
each step) before `backlog_refine`. `grill-me` is a bundled skill `mneme
install` delivers; `mneme skills list` shows it once installed.
**Do NOT use `superpowers:brainstorming` to refine it** — it clashes with the
SDD flow (writes its own design doc and plan, stepping on the spec you are
about to write) and doesn't ship with mneme. For **trivial** items the grill
is optional — grill it or reclassify to standard if it turns out ambiguous.

## §4 Skills

When the `UserPromptSubmit` hook injects a `<mneme:speech>` block, resolve that
turn exactly once with `speech_emit`: speak only the concise useful result,
decision, explanation, question, or blocker, or use `disposition=skip`. Never
send raw tool output, code, progress chatter, or a copy of the full visual
answer. The user can ask to enable or disable speech; use `speech_control`.
Speech is local, opt-in, and disabled by default.

Bundled skills are installed to `$HOME/.agents/skills` for Codex to discover.

Check available skills: `mneme skills list`.
Validate before relying on: `mneme skills lint [name]` / `mneme skills validate <name>`.

The CLI and MCP `skills_*` operations keep Claude's and Codex's discovery
directories synchronized. Pinning, removal and installation apply to both.

## §5 Memory & conflicts

Save decisions, discoveries, bugfixes, conventions to mneme — never rely on session
history alone.

Session lifecycle:
- FIRST MESSAGE: `mem_context`, then `mem_search` with keywords. `spec_list` to see active specs.
- EVERY user message: `mem_search` before responding.
- AFTER completed task: `mem_save` (decision/discovery/bugfix/convention). Use `topic_key` for evolving knowledge.
- BEFORE session end: `mem_session_end` with summary. There is no hook that reminds you of
  this — if you don't call it, no one will. `mneme hook session-end` is a retired no-op.
- LONG tasks: `mem_checkpoint` periodically.
- POST-COMPACTION: `mem_context` to recover context.

Save rules: `scope:global` for user preferences, `scope:project` for everything else.
`topic_key` for knowledge that evolves (overwrites). Omit for unique events. Save liberally.

Conflict hygiene: `mneme conflicts scan` periodically to surface superseded memories.

**This section is the safety net:** if the session hook (SessionStart) is
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

## §7 Plain language: everything a person reads

MANDATORY. A person must be able to understand what you wrote without asking you to
explain it again. This is not a style preference: a person who does not understand
your question is not deciding, only trusting you — and that empties the human
approval gate of §3 of its meaning.

### Channels that reach a person

The rule below is binding on every one of these. There are no other exceptions than
the ones listed further down.

1. Any message you write in the session — answers, explanations, progress notes,
   warnings, the reason something failed.
2. Any question that asks a person to decide, approve or choose, including grill
   questions and the presentation of a spec at the §3 approval gate.
3. Any summary you present — what you did, what changed, what a command printed, the
   closing report of a skill or of a session.
4. Spoken output (`speech_emit`, §4).
5. Text you write for a command to print to a person: console output, help text,
   error messages, prompts.
6. Any part of an agent-to-agent report that you pass on to a person.
   **Relaying is authoring:** you rewrite it in plain language; you never paste it
   through.

### The rule

1. Write for someone who knows their own product and their own code, but not the
   vocabulary of this conversation. This is not writing for a beginner.
2. **Never invent a metaphor and then reuse it as if it were shared vocabulary.** If
   a concept needs a name, describe it in ordinary words every time — six plain words
   beat one word only you understand. This clause binds every channel, including
   agent-to-agent reports.
3. Use a real technical term only when no ordinary word exists for it, and explain it
   in the same sentence the first time it appears. Never send the reader elsewhere
   for the meaning.
4. Write in the language the person is writing to you in. Never mix two languages in
   one sentence, and never leave a foreign word in when your language has one. This
   clause alone does not apply to code, identifiers, commit messages or product copy,
   which follow the repository's own conventions; every other clause still does.
5. Expand every acronym the first time it appears. The only exception is this flow's
   own identifiers (BL-123, SPEC-115) — proper names the operator uses daily.
6. **When you ask for a decision, the bar is higher:** each option must be
   understandable without having read anything before it, and must say what it costs
   the person in practice, not what it is internally.
7. If the person asks you to explain something again, do not rephrase it with the
   same words. Change level: show the real file, the real command, the concrete
   example.

### What is exempt, and why

Agent-to-agent reports are exempt from clauses 1 and 3-7 (clause 2 binds everywhere):

- the spec, plan, qa-report and changes documents written with `spec_doc_write`;
- memories saved with `mem_save`;
- code, identifiers, comments, test names and commit messages.

Precision beats simplicity when the reader is another agent, and this is measured, not
assumed: precise agent-to-agent reports are what caught real defects — an imprecise
description of which test was failing, a row count that did not add up, an uncovered
function that a global average was hiding. Plain language would have destroyed that.

The exemption covers writing internal documents, never showing them to the person.

**The exemption never travels with the text.** The moment any of it reaches a person,
channel 6 applies and you rewrite it. Presenting a spec at the §3 gate is not pasting
the document: it is telling the person, in plain words, what is being decided and what
it costs them.

### Examples, not a list to game

Two categories, with examples that are deliberately not exhaustive: metaphors invented
mid-conversation and then reused as if they were shared vocabulary; and foreign terms
left untranslated ("dogfooding", "opt-in", "roundtrip", "fixture", "flake",
"merge-base"). A word is banned because it belongs to one of these categories, never
because it appears in this list.
