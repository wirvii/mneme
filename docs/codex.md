# mneme + OpenAI Codex CLI

`mneme install codex` configures the [OpenAI Codex CLI](https://developers.openai.com/codex)
to use mneme as its persistent memory and SDD engine.

## What it installs

| Artefact | Location | Purpose |
|---|---|---|
| MCP server | `~/.codex/config.toml` → `[mcp_servers.mneme]` | Exposes all mneme tools to Codex |
| Session hooks | `~/.codex/hooks.json` → `SessionStart` | Auto-inject context at session start |
| Operating manual | `~/.codex/AGENTS.md` (managed block) | Single-agent memory + SDD instructions |
| CLAUDE.md fallback | `project_doc_fallback_filenames` in `config.toml` | Codex reads existing CLAUDE.md files |
| Workflow templates | `~/.mneme/templates/` | Shared with Claude (agent-agnostic) |
| Skills | `$HOME/.agents/skills/` | Codex discovery path (S4) |

## Installation

```bash
mneme install codex
# or dry-run to preview:
mneme install codex --dry-run
```

The install is **non-destructive and idempotent** — running it multiple times produces
the same result. All existing keys in `config.toml` and `hooks.json` are preserved.

## Single-agent model

Unlike the Claude Code integration, **Codex uses a single-agent setup**:

- There is no orchestrator/implementer separation.
- There is no delegation hook or edit-blocking hook.
- One agent reads memory, follows SDD, and implements changes directly.

This reflects the Codex philosophy and the owner's explicit decision (see memory
`codex/design-single-agent`). Self-discipline replaces role enforcement: follow
SDD, save memories, do not skip steps.

## Session hooks and trust (D3b)

Codex hooks in `~/.codex/hooks.json` require explicit trust before they run:

1. Start Codex: `codex`
2. In the TUI, run `/hooks`
3. Review and trust the mneme hooks

Until the hooks are trusted — and even after, for session end — the
session-lifecycle instructions in `~/.codex/AGENTS.md §5` (Memory & conflicts)
cover the discipline manually: call `mem_context` on first message,
`mem_search` before responding, `mem_save` after tasks, `mem_session_end`
before ending. There is no hook that reminds you of the last step: `mneme
hook session-end` is a retired no-op (SPEC-106) — see "Retired: the `Stop`
session hook" below.

### Retired: the `Stop` session hook (SPEC-106)

Earlier versions of `mneme install codex` also registered `Stop` →
`mneme hook session-end` in `hooks.json`. That registration never worked:
Codex's `Stop` contract requires JSON on stdout when it exits 0, and the
hook printed plain text, which produced a visible `Stop hook (failed)` error
on every session close. `mneme install codex` no longer registers `Stop`, and
it actively removes a pre-existing registration on every run.

If you previously worked around this by pointing `Stop` at your own script
(e.g. one that wraps the output in valid JSON), that registration is **not**
touched — mneme identifies its own hooks by executable + subcommand
(SPEC-107), never by matching an unrelated command. Note the purge is
therefore **not** limited to the literal string mneme originally wrote: if
you had instead customised mneme's own `Stop` registration (for example to
an absolute `mneme` path, or with extra flags), that variant is recognised
as the same registration and purged too, flags and all — see the "Hook registration identity" section in [docs/HOOKS.md](HOOKS.md) for
the full identity rules. You are free to remove any surviving foreign script
manually from `~/.codex/hooks.json` once you no longer need it.

## CLAUDE.md fallback behaviour (S1)

Codex reads `CLAUDE.md` as a **per-directory fallback**: in each directory it
probes `AGENTS.override.md` → `AGENTS.md` → fallback filenames in order.
`CLAUDE.md` is read **only in directories that do not have their own `AGENTS.md`**.

This means existing repos with `CLAUDE.md` (no `AGENTS.md`) work transparently.
If a repo adds its own `AGENTS.md`, its `CLAUDE.md` is no longer read in that
directory — this is the expected Codex behaviour.

## Skills note (R3)

Bundled mneme skills are installed to `$HOME/.agents/skills/` for Codex to
discover. However, the MCP tools `skills_*` manage `~/.claude/skills/` (hardcoded
in the mneme server), not the Codex path. This means:

- Codex can **use** the installed skills via the `/skills` command or implicit invocation.
- Managing skills via `mneme skills install|pin|unpin|remove` operates on `~/.claude/skills/`,
  not `$HOME/.agents/skills/`.

This desalignment is documented as R3 in SPEC-049 and will be addressed in a
future spec when the skills service is made agent-aware.

## Flags

| Flag | Applies to codex? | Notes |
|---|---|---|
| `--dry-run` | Yes | Preview steps without writing |
| `--personal` | No (silently ignored) | No personal ecosystem for codex in v1 |
| `--reinstall-hooks` | No (silently ignored) | No delegation hook for codex |
| `--force` | Partial | Affects skill pin-bypass only |

## Differences from `mneme install claude-code`

| Feature | Claude Code | Codex |
|---|---|---|
| MCP config format | JSON (`~/.claude.json`) | TOML (`~/.codex/config.toml`) |
| Session hooks file | `~/.claude/settings.json` | `~/.codex/hooks.json` |
| Hook trust | Automatic | Requires `/hooks` in Codex TUI |
| Operating manual | `~/.claude/CLAUDE.md` | `~/.codex/AGENTS.md` |
| Agent profiles | None global (per-project via `mneme-init` grill; SPEC-073) | None (single-agent) |
| Delegation hook | Yes (enforces roles) | None |
| Slash commands | `/mneme-init` (thin wrapper invoking the `mneme-init` skill) | None (deprecated in Codex) |
| Skills path | `~/.claude/skills/` | `$HOME/.agents/skills/` |
| Model assignments | Per-project at grill time (`subagent_compose`) | Skipped (no profiles) |
| Role model | Multi-agent (orchestrator + implementers) | Single-agent |
