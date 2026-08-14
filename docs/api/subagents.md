# API Reference — Subagent Tools (`subagent_*`)

6 MCP tools over JSON-RPC 2.0 stdio (`mneme mcp`). These are the building
blocks the `mneme-init` skill's grill uses to generate **per-project**
subagent profiles (EPIC agnostic-agents, SPEC-052/057) — the CLI counterpart
is `mneme subagents` ([docs/api/cli.md](cli.md#subagents)). Concept guide:
[docs/enforcement-model.md](../enforcement-model.md) (capability allowlists,
the two enforcement layers, project-scoped opt-in delegation-hook). Index:
[docs/API.md](../API.md).

**3-layer profile anatomy** these tools compose: layer 1 (Go-authored
frontmatter + permission envelope, selected via `archetype` — never
LLM-generated), layer 2 (`profile_json`, repo/org knowledge elicited once per
project), layer 3 (`areas_layer3_md`, role×area×stack content drafted during
the grill, treated as **untrusted** — wrapped and escaped before being
embedded in the composed system prompt).

---

## subagent_fingerprint

Deterministic, read-only detection of a project's root, apps/packages, and
stack markers, plus which subagent typed-memory records (project-profile,
manifest) already exist. Phase 0 of the subagents grill. Never calls an LLM.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `repo_root` | string | no | Absolute path to start the project-root search from. Default: current working directory |

**Returns:** `{"root": "/path/to/repo", "apps": ["apps/core-srv"], "stack_markers": ["go.mod"], "seeded_memories": ["subagents/project-profile"]}`
(`seeded_memories` lists which of the two typed-memory records —
project-profile, manifest — already exist for this project, so the grill can
offer to reuse them.)

**Errors:** `-32602` invalid `repo_root`.

**Example:** `mneme subagents fingerprint --json`

---

## subagent_profile_get

Read the project-profile (repo/org knowledge + app→role mapping) elicited by
the subagents grill (layer 2). Returns an empty profile when none has been
saved yet.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `project` | string | no | Project slug. Default: auto-detected |

**Returns:** `{"schema_version": 1, "repo": {"commits": "...", "lang": "...", "layout": "...", "cross_rules": [...]}, "org": {...}, "mapping": [{"app": "apps/core-srv", "role": "backend"}]}`

**Example:** `mneme subagents profile get`

---

## subagent_profile_save

Upsert the project-profile (repo/org knowledge + app→role mapping) elicited
by the subagents grill.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `profile_json` | object | yes | The project profile payload: `{schema_version, repo:{commits,lang,layout,cross_rules[]}, org, mapping:[{app,role}]}` |
| `project` | string | no | Project slug. Default: auto-detected |

**Returns:** the saved profile, echoed back.

**Errors:** `-32602` missing `profile_json`, invalid schema.

**Example:** `mneme subagents profile save --file profile.json`

---

## subagent_compose

Assemble a subagent profile preview: Go-authored frontmatter + permissions
(selected via `archetype`) and the layer-1 managed block, plus layer-2
(`profile_json`) and layer-3 (`areas_layer3_md`) content. `areas_layer3_md`
is treated as untrusted grill-provided data — it is wrapped and escaped
against prompt injection before being embedded, since the composed file
becomes the subagent's own system prompt. Validates the result and returns
it **without writing to disk**.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `role` | string | yes | Subagent role name used for the frontmatter `name:` and destination filename (may differ from `archetype` for custom roles). Must match `^[a-z][a-z0-9-]*$` |
| `archetype` | string | yes | Built-in role whose Go-authored permission envelope and agent-fixed sections this profile inherits: `architect`, `backend`, `frontend`, `qa-tester`, `bug-hunter`, `diagnostician`. An LLM never generates permissions directly — custom roles must map to one of these |
| `areas_layer3_md` | string | yes | Layer-3 (role×area×stack) markdown content drafted during the grill. Treated as untrusted data — wrapped and escaped before embedding |
| `model` | string | no | Frontmatter `model:` alias (e.g. `sonnet`, `opus`). Default: `sonnet` |
| `description` | string | no | Frontmatter `description:` value (no newlines). Default: a generic one-liner |
| `profile_json` | object | no | The project profile (layer-2 repo/org knowledge) to render into the profile body |

**Returns:** `{"role": "backend", "archetype": "backend", "composed_md": "...", "valid": true, "errors": []}`

**Errors:** `-32602` missing required params, invalid `role` slug, unknown `archetype`.

**Example:**

```bash
mneme subagents compose --role backend --archetype backend \
  --description "Implements server-side logic" --areas-file areas.md
```

---

## subagent_write

Atomically write `.claude/agents/<role>.md` and `.codex/agents/<role>.toml`
from the same role contract and update the manifest. Rolls back file writes if
the manifest update fails. `role` must be a safe slug (lowercase letters/digits/hyphens) —
rejects path traversal. `composed_md` is **re-validated** against
`archetype`'s Go-authored permission envelope before anything is written, so
a hand-crafted `composed_md` can never grant a role more capability than its
archetype allows.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `role` | string | yes | Subagent role name; determines both runtime destination filenames. Must match `^[a-z][a-z0-9-]*$` |
| `archetype` | string | yes | Built-in role whose Go-authored permission envelope `composed_md` is validated against before writing |
| `composed_md` | string | yes | Full composed profile content, as returned by `subagent_compose`'s preview |
| `enforcement_hook` | boolean | no | Whether the project's delegation-enforcement hook is enabled. Recorded in the manifest only — use `mneme delegation-hook enable` to actually register it |
| `project` | string | no | Project slug. Default: auto-detected |
| `repo_root` | string | no | Absolute repo root the profile is written under. Default: current working directory |
| `engine` | string | no | GenerationEngine identifier used to draft layer-3 content (e.g. `passthrough`, `cli-claude`). Default: `passthrough` |
| `areas` | string[] | no | App/package paths this profile's role/area sections cover |
| `areas_complete` | boolean | no | Certifies `areas` as an exhaustive list of every path this role may write to — what activates role containment (SPEC-086 D4/D5/D11). Set `true` only as the direct answer to the `mneme-init` grill's explicit completeness question, reviewed by a human. Never infer, default, or backfill it: an uncertified role is reported by `mneme subagents doctor` as `not_verified`, which is the correct and safe state until certified (SPEC-113) |

**Returns:** the legacy Claude `path`/`checksum` fields plus an `artifacts`
array containing the Claude Code and Codex paths and checksums (`version` is
the layer-1 managed-block version parsed back out of the
written file.)

**Errors:** `-32602` missing required params, invalid `role` slug (including
attempted path traversal), `composed_md` fails re-validation against
`archetype`'s permission envelope. `-32603` filesystem/manifest failure — the
file write is rolled back to its exact pre-call state before the error is
returned.

---

## subagent_manifest_list

List the current subagent manifest (generated profile files and their
metadata) for drift/status reporting.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `project` | string | no | Project slug. Default: auto-detected |

**Returns:** a JSON array of manifest entries (empty array `[]` when none
exist yet): `[{"role": "backend", "path": ".claude/agents/backend.md", "version": 1, "checksum": "sha256-hex...", "areas": ["apps/core-srv"], "engine": "cli-claude", "generated_at": "2026-07-08T22:00:00Z", "enforcement_hook": true}]`

**Example:** `mneme subagents manifest-list --json`

---

## Error codes

Shared across all MCP tool families (see [docs/API.md](../API.md) for the
full JSON-RPC transport reference):

| Code | Name | Triggered when |
|------|------|----------------|
| `-32600` | Invalid Request | Malformed JSON-RPC envelope |
| `-32601` | Method not found | Unknown MCP method |
| `-32602` | Invalid params | Missing required params, type mismatch, domain validation failure |
| `-32603` | Internal error | Unexpected failure, filesystem error |

## See also

- [docs/enforcement-model.md](../enforcement-model.md) — capability
  allowlists, the two enforcement layers, project-scoped opt-in
  delegation-hook (`mneme delegation-hook`)
- [docs/api/cli.md](cli.md#subagents) — `mneme subagents` CLI reference
- [docs/team-memory.md](../team-memory.md) — the parallel EPIC that shares
  the same `mneme-init` skill entry point
