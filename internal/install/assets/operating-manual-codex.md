# mneme Operating Manual (Codex)

Injected into AGENTS.md by `mneme install codex`.

## 1. Roles

Roles come from `.codex/agents/*.toml`. The coordinator talks to the person, runs backlog
and specs, and delegates. It never edits code a subagent owns (a hook blocks it;
`trivial` explains the one opt-in exception); a stage nobody owns it does itself,
knowingly. Architect designs, read-only. Backend, frontend and bug-hunter implement.
QA-tester verifies and diagnostician investigates; neither edits code. Only the
coordinator advances, quick-starts or archives a spec. PreToolUse hooks enforce this
once trusted (`/hooks`); if a role or hook identity cannot be resolved safely, stop and
report, never weaken permissions.

## 2. Human approval gate (unbreakable)

Before advancing a spec past `specced`, present the WHOLE spec in plain words and wait
for an explicit yes to it. Answering design questions is not approval. Only exception:
the person skips the gate for one named spec; never inherited, never a default.

## 3. Plain language

MANDATORY, binding on every message a person reads. Channels that reach a person: chat,
questions, summaries, text a command prints, and agent reports you pass on
(Relaying is authoring: rewrite, never paste).

- Write for someone who knows their product, not this chat's vocabulary.
- Never invent a metaphor and reuse it as shared vocabulary (agent reports too).
- Explain needed jargon; expand acronyms on first use (BL-/SPEC- ids excepted).
- Use the person's language; never mix two in one sentence.
- In a decision, each option stands alone and says what it costs in practice.
- Asked again? Show the real file, command or example.

Specs, plans, reports, briefs and memories may stay precise.
The exemption never travels with the text: once it reaches a person, rewrite it.

## 4. Memory

Start with `mem_context`; run `mem_search` before answering. `mem_save` decisions,
discoveries, bugfixes and conventions (`topic_key` if it evolves). Judge any
`possible_conflicts` it returns in the same turn, via `mem_update` (`supersedes`,
`conflicts_with`, `unrelated_to`). `review_due` notes are for you only, never for the
person; `confirm` renews a note. Close (or checkpoint, `partial:true`) with
`mem_session_end`: there is no hook that reminds you.

## 5. Skills: load the one that fits

- `mneme-flow`: any spec work (cycle, gates, lanes, criteria).
- `feature`: the person wants something new.
- `bug`: something that worked now fails.
- `trivial`: a tiny change in a declared scope.
- `grill-me`: refine a standard item. Do NOT use `superpowers:brainstorming`.
- `present-spec`: a spec at the approval gate.
- `review-brief`: send a spec to QA, or do that review.
- `mneme-init`: set up a project's roles and memory.
- `codegraph`: the repo has an indexed code graph.
