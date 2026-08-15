# mneme + OpenAI Codex CLI

`mneme install codex` configures the [OpenAI Codex CLI](https://developers.openai.com/codex)
to use mneme as its persistent memory and SDD engine.

## What it installs

| Artefact | Location | Purpose |
|---|---|---|
| MCP server | `~/.codex/config.toml` → `[mcp_servers.mneme]` | Exposes the coordinator's mneme tools to Codex |
| Session hooks | `~/.codex/hooks.json` → `SessionStart` | Auto-inject context at session start |
| Operating manual | `$CODEX_HOME/AGENTS.md` (managed block) | Memory, SDD and role instructions |
| CLAUDE.md fallback | `project_doc_fallback_filenames` in `config.toml` | Codex reads existing CLAUDE.md files |
| Workflow templates | `~/.mneme/templates/` | Shared with Claude (agent-agnostic) |
| Skills | `$HOME/.agents/skills/` | Codex discovery path (S4) |
| Role enforcement | `$CODEX_HOME/hooks.json` → `PreToolUse` | Enforces ownership and reserved tools |

`CODEX_HOME` is honored when set; otherwise it defaults to `~/.codex`.

## Installation

```bash
mneme install codex
# or dry-run to preview:
mneme install codex --dry-run
```

The install is **non-destructive and idempotent** — running it multiple times produces
the same result. All existing keys in `config.toml` and `hooks.json` are preserved.

## Supported versions

v1.40.1 can be installed with Codex CLI 0.147.0 or newer and requires Claude
Code 2.1.232 or newer. Codex 0.147.0 supports mneme's MCP, memory, skills,
project-role assets, and coordinator mode, but does not propagate child hooks
or identity. Therefore `mneme install codex` warns—without blocking—that
native multi-agent delegation and containment are not fully verified on that
stable release. Codex 0.148.0-alpha.19 is the first build verified to emit
identity-bearing `SubagentStart` and child `PreToolUse` payloads; a future
0.148.0 stable release also satisfies that capability floor by SemVer.

mneme never recommends switching to an alpha channel. `mneme install` refuses
only Codex versions older than 0.147.0. If a CLI is absent,
mneme can still prepare and statically validate project assets, but reports
that the real runtime verification was not run.

## Shared role model

`mneme init` generates project roles for both runtimes from one canonical
contract:

- Claude Code profiles live under `.claude/agents/*.md`.
- Codex profiles live under `.codex/agents/*.toml`.
- Both share responsibilities, ownership areas, memory and SDD state.
- Identity-bearing PreToolUse hooks enforce ownership and coordinator
  protection in both runtimes.
- Every Codex role starts a role-bound mneme MCP server. Its tool list and
  call-time authorization independently keep lifecycle transitions,
  `quality_ack`, `quality_sign`, and architect-only documents fail-closed.
  Allowed calls are pre-approved for that local server because Codex child
  sessions cannot answer MCP approval prompts; the role-bound filter is
  applied before this approval setting.

The formats differ because each runtime uses its native configuration. A
project initialized from either runtime is ready for both.

At SessionStart, mneme compares the manifest's recorded checksums with both
native projections. A legacy manifest, missing projection, or checksum drift
produces one actionable `mneme-init` nudge and never rewrites the repo from the
hook. Once reconciled, the nudge disappears.

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

Trust is also a security prerequisite for delegated work. mneme does not
claim role containment until Codex activates its `SubagentStart` and
`PreToolUse` hooks; the version gate rejects releases that predate the child
identity payload, and the role-bound MCP remains a second independent gate
for reserved mneme operations.

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

## Skills

Bundled mneme skills are installed to `$HOME/.agents/skills/` for Codex.
The CLI and MCP skill-management operations mirror installation, pinning,
unpinning and removal across Claude and Codex discovery directories.

## Flags

| Flag | Applies to codex? | Notes |
|---|---|---|
| `--dry-run` | Yes | Preview steps without writing |
| `--personal` | No (silently ignored) | No personal ecosystem for codex in v1 |
| `--reinstall-hooks` | No | Codex hooks are reconciled on every install |
| `--force` | Partial | Affects skill pin-bypass only |

## Differences from `mneme install claude-code`

| Feature | Claude Code | Codex |
|---|---|---|
| MCP config format | JSON (`~/.claude.json`) | TOML (`~/.codex/config.toml`) |
| Session hooks file | `~/.claude/settings.json` | `~/.codex/hooks.json` |
| Hook trust | Automatic | Requires `/hooks` in Codex TUI |
| Operating manual | `~/.claude/CLAUDE.md` | `~/.codex/AGENTS.md` |
| Agent profiles | Per-project `.claude/agents/*.md` | Per-project `.codex/agents/*.toml` |
| Delegation hook | Yes | Yes |
| Slash commands | `/mneme-init` (thin wrapper invoking the `mneme-init` skill) | None (deprecated in Codex) |
| Skills path | `~/.claude/skills/` | `$HOME/.agents/skills/` |
| Model assignments | Per-project contract | Per-project contract rendered to native TOML |
| Role model | Coordinator + project roles | Coordinator + project roles |
