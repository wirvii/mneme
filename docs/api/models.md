# API Reference — Models Tools (`model_*`)

3 MCP tools over JSON-RPC 2.0 stdio (`mneme mcp`). Concept guide:
[docs/models.md](../models.md) (defaults, apply-on-install flow,
cross-provider scope). Index: [docs/API.md](../API.md).

Assignments are stored in `~/.mneme/config.toml` under `[models.overrides]`
and applied to `~/.claude/agents/<agent>.md` on every
`mneme install claude-code`. Changes made through these tools require a
follow-up `mneme install claude-code` to take effect on agent files.

---

## model_list

List the effective model for each bundled agent, showing origin (default or
override).

No parameters.

**Returns:** `ModelListResponse`: `{"agents": [{"agent": "architect", "model": "opus", "origin": "default"}, {"agent": "backend", "model": "sonnet", "origin": "default"}, ...]}` (one entry per bundled agent, alphabetical).

**Errors:** `-32603` models service unavailable, config load failure.

**Example:** `mneme model list --json`

---

## model_set

Set the model alias for a specific agent. Writes an override to
`config.toml`. Validates that the agent is a bundled agent (error if not).
Accepts any non-empty string as the model alias; warns (does not error) when
the alias is not in the known-aliases list (`opus`/`sonnet`/`haiku`/`inherit`).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `agent` | string | yes | Agent name (e.g. `bug-hunter`, `architect`) |
| `model` | string | yes | Model alias to assign (e.g. `opus`, `sonnet`, `haiku`). Must not be empty |

**Returns:** `ModelSetResponse`: `{"agent": "bug-hunter", "model": "opus", "warning": "", "hint": "run \`mneme install claude-code\` to apply"}`. `warning` is populated (non-empty) when the alias is not a known alias.

**Errors:** `-32602` empty `model` (`ErrInvalidModel`), unknown agent name (`ErrUnknownAgent`).

**Example:** `mneme model set bug-hunter opus`

---

## model_reset

Remove the model override for a specific agent, or for all agents when
`agent` is omitted. Restores default models.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `agent` | string | no | Agent name to reset. Omit to reset all agents |

**Returns:** `ModelResetResponse`: `{"reset": ["bug-hunter"], "hint": "run \`mneme install claude-code\` to apply"}` (lists the agent names whose overrides were removed; all overrides when `agent` was omitted).

**Errors:** `-32602` unknown agent name (`ErrUnknownAgent`).

**Example:** `mneme model reset bug-hunter` or `mneme model reset` (all agents)

---

## Defaults

`architect` → `opus` (its output is the spec; errors propagate to all other
agents). `backend`, `frontend`, `qa-tester`, `bug-hunter`, `diagnostician`,
`orchestrator` → `sonnet`.

## Error codes

| Code | Name | Triggered when |
|------|------|----------------|
| `-32602` | Invalid params | Empty model string, unknown agent name |
| `-32603` | Internal error | Models service unavailable, config load/write failure |

## See also

- [docs/models.md](../models.md) — apply-on-install mechanics, cross-provider scope (deferred)
- [docs/api/cli.md](cli.md) — `mneme model list/set/reset`
