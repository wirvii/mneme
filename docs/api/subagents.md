# API Reference — Subagents (CLI only since v1.49)

The six `subagent_*` MCP tools (SPEC-057) left the MCP server in v1.49
(SPEC-163). The capability is unchanged and lives entirely in
`mneme subagents` ([docs/api/cli.md](cli.md#subagents)); the `mneme-init`
skill's grill runs those commands through Bash. Calling a former tool name
over MCP now answers `-32601` `unknown tool: <name>`. Concept guide:
[docs/enforcement-model.md](../enforcement-model.md) (capability allowlists,
the two enforcement layers, project-scoped opt-in delegation-hook). Index:
[docs/API.md](../API.md).

**3-layer profile anatomy** these commands compose: layer 1 (Go-authored
frontmatter + permission envelope, selected via `--archetype` — never
LLM-generated), layer 2 (the saved project profile, repo/org knowledge
elicited once per project), layer 3 (role×area×stack content drafted during
the grill, treated as **untrusted** — wrapped and escaped before being
embedded in the composed system prompt).

## Retired tool → CLI command

| Retired MCP tool | CLI command | Notes |
|---|---|---|
| `subagent_fingerprint` | `mneme subagents fingerprint [path] [--json]` | Output includes `foreign_agents` (never `null`) |
| `subagent_profile_get` | `mneme subagents profile get` | Always JSON; empty object when none saved |
| `subagent_profile_save` | `mneme subagents profile save --file F \| --stdin [--json]` | `--json` prints `{action, id, …}` |
| `subagent_compose` | `mneme subagents compose --role R --archetype A [--model M] [--description D] [--profile-file F] (--areas-file F \| --areas-stdin) [--json]` | Reads the saved profile unless `--profile-file` is given. Rejects layer-1 leaks in the layer-3 input |
| `subagent_write` | `mneme subagents write --role R --archetype A (--composed-file F \| --composed-stdin) [--areas a,b] [--areas-complete] [--engine E] [--enforcement-hook] [--repo-root P] [--json]` | Scans the grill region, backs up foreign files, reports `backup_path` |
| `subagent_manifest_list` | `mneme subagents manifest-list [--json]` | `--json` adds `findings` per entry |

The MCP tools accepted their payload inline (`profile_json`,
`areas_layer3_md`, `composed_md`). The CLI takes large inputs by file or
stdin instead, so a draft never travels as a command-line argument.

## Guarantees the CLI carries

These were enforced by the MCP handlers and moved to the CLI so retiring the
tools did not weaken them (SPEC-163 G1–G5):

- **Role slug** must match `^[a-z][a-z0-9-]*$` (path traversal and
  frontmatter injection defence); the description may not contain newlines.
- **Layer 2/3 boundary (SPEC-090).** `compose` scans the raw layer-3 content,
  and `write` scans the composed file's grill region, for SDD lifecycle
  tokens (`spec_advance`, `spec_quick`, `spec_reject`) and capability keys
  (`tools:`, `permissionMode:`). A hit exits non-zero, writes nothing, and
  names each `"<token>" (line N)`. Scanning the grill region only — not the
  whole file — is deliberate: layer 1 legitimately says "never call
  `spec_advance`".
- **Permission envelope.** `write` re-validates the composed markdown
  against the archetype's Go-authored permission table, so a hand-crafted
  file can never grant a role more than its archetype allows.
- **Foreign file backup (SPEC-090 D9).** If `.claude/agents/<role>.md`
  exists and has no `agent-fixed` block, it is copied to
  `<path>.bak-<UTC 20060102T150405Z>` before being overwritten;
  `backup_path` is reported. Overwriting a mneme-generated file makes no
  backup, and `backup_path` is omitted.
- **Atomic write.** Both runtime files (`.claude/agents/<role>.md` and
  `.codex/agents/<role>.toml`) plus the manifest entry succeed together; a
  manifest failure rolls the files back to their exact prior state.

## Output shapes

`fingerprint --json`:

```json
{"root": "/path/to/repo", "apps": ["apps/core-srv"], "stack_markers": ["go.mod"],
 "seeded_memories": ["project-profile/agnostic-agents"],
 "foreign_agents": [".claude/agents/security-auditor.md"]}
```

`compose --json`:
`{"role": "backend", "archetype": "backend", "composed_md": "...", "valid": true, "errors": []}`

`write --json`: `{"path": "...", "checksum": "...", "version": 5,
"artifacts": [{runtime: "claude-code", ...}, {runtime: "codex", ...}],
"backup_path": "..."}` (`backup_path` omitted when no backup was made).

`manifest-list --json`: an array of manifest entries, each with the persisted
fields (`role`, `path`, `version`, `checksum`, `artifacts`, `areas`,
`areas_complete`, `archetype`, `engine`, `generated_at`, `enforcement_hook`)
plus `findings: [{kind, detail}]` — the same diagnostics
`mneme subagents doctor` reports for that entry (omitted when empty).

## See also

- [docs/api/cli.md](cli.md#subagents) — flags for every command above
- [docs/enforcement-model.md](../enforcement-model.md) — capability
  allowlists, the two enforcement layers, project-scoped opt-in
  delegation-hook (`mneme delegation-hook`)
- [docs/team-memory.md](../team-memory.md) — the parallel EPIC that shares
  the same `mneme-init` skill entry point
