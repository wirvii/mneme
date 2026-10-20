# API Reference — Models (CLI only since v2.0)

The three `model_*` MCP tools (SPEC-038) left the MCP server in v2.0
(SPEC-163). The capability is unchanged and lives in `mneme model`
([docs/api/cli.md](cli.md#models)). Calling a former tool name over MCP now
answers `-32601` `unknown tool: <name>`. Concept guide:
[docs/models.md](../models.md) (defaults, apply-on-install flow,
cross-provider scope). Index: [docs/API.md](../API.md).

Assignments are stored in `~/.mneme/config.toml` under `[models.overrides]`.
Global install does not create role files. Per-project generation applies
these assignments to both `.claude/agents/*.md` and `.codex/agents/*.toml`;
mneme uses an explicit verified alias map and rejects unsafe mappings.

## Retired tool → CLI command

| Retired MCP tool | CLI command | Notes |
|---|---|---|
| `model_list` | `mneme model list [--json]` | One entry per bundled agent, alphabetical: `{agent, model, origin}` with `origin` either `default` or `override` |
| `model_set` | `mneme model set <agent> <model>` | Any non-empty alias is accepted; an alias outside `opus`/`sonnet`/`haiku`/`inherit` only produces a warning. Empty model → `ErrInvalidModel`, unknown agent → `ErrUnknownAgent` |
| `model_reset` | `mneme model reset [<agent>]` | Omit the agent to remove every override |

## Defaults

`architect` → `opus` (its output is the spec; errors propagate to all other
agents). `backend`, `frontend`, `qa-tester`, `bug-hunter`, `diagnostician`,
`orchestrator` → `sonnet`.

## See also

- [docs/models.md](../models.md) — apply-on-install mechanics, cross-provider scope (deferred)
- [docs/api/cli.md](cli.md#models) — `mneme model list/set/reset`
