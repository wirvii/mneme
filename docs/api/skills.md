# API Reference — Skills Tools (`skills_*`)

7 MCP tools over JSON-RPC 2.0 stdio (`mneme mcp`). Concept guide:
[docs/skills.md](../skills.md) (SKILL.md format, pin semantics, bundled
skills). Index: [docs/API.md](../API.md).

mneme is the **cross-runtime package manager** for skills. It mirrors managed
content to `~/.claude/skills/` and `$HOME/.agents/skills/`; it does not
implement either runtime's skill loader.

---

## skills_list

List all available skills (bundled and installed), showing name, version,
installed status, pinned status, and lint result.

No parameters.

**Returns:** Array of `SkillInfo`: `{"Name": "example-skill", "Version": "1.0.0", "Installed": true, "Pinned": false, "Bundled": true, "LintOK": true}` per skill.

**Errors:** `-32603` skills service unavailable, bundle read failure.

**Example:** `mneme skills list --json`

---

## skills_install

Install a bundled skill to both runtime destinations. Respects pin protection
unless `force` is true.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | string | yes | Skill name to install (must be in the bundled set) |
| `force` | boolean | no | When `true`, overwrite even if the installed skill is pinned. Default: `false` |

**Returns:** `{"skill": "example-skill", "status": "installed"}`

**Errors:** `-32602` missing `name`, skill pinned without `force`, unknown skill. `-32000` not found.

**Example:** `mneme skills install example-skill --force`

---

## skills_pin

Set `pinned: true` in the installed `SKILL.md` to protect it from overwrite or
removal.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | string | yes | Skill name to pin |

**Returns:** `{"skill": "example-skill", "status": "pinned"}`

**Errors:** `-32602` missing `name`. `-32000` skill not installed.

---

## skills_unpin

Set `pinned: false` in the installed `SKILL.md`, allowing future installs to
overwrite it.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | string | yes | Skill name to unpin |

**Returns:** `{"skill": "example-skill", "status": "unpinned"}`

**Errors:** `-32602` missing `name`. `-32000` skill not installed.

---

## skills_remove

Remove an installed skill directory. Refuses if the skill is pinned unless
`force` is true.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | string | yes | Skill name to remove |
| `force` | boolean | no | When `true`, remove even if the skill is pinned. Default: `false` |

**Returns:** `{"skill": "example-skill", "status": "removed"}`

**Errors:** `-32602` missing `name`, skill pinned without `force`. `-32000` skill not installed.

---

## skills_lint

Run the deterministic structural linter on a skill or all installed skills.
Checks: required frontmatter fields (`name`, `description`, `version`), `name`
matches directory name, semver `version`, all 5 required H2 sections present,
and a 3-column Automated Checks table with the correct headers. No scripts are
executed.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | string | no | Skill name to lint. When omitted, all installed skills are linted |

**Returns:** Array of `LintResult`: `{"Name": "example-skill", "Errors": [], "Warnings": [], "Infos": [], "Passed": true}` per skill (each `Finding` has `Severity` and `Message`).

**On lint failure:** returned with `IsError: true` in the tool result payload
(not a protocol error) so the caller still receives the full finding list.

**Errors:** `-32603` skills service unavailable.

---

## skills_validate

Run the `validation/run.sh` script for a skill. 120-second timeout.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | string | yes | Skill name to validate |

**Returns:** `ValidateResult`: `{"Passed": true, "Output": "...", "ExitCode": 0}`.

**On failure (non-zero exit or missing `validation/run.sh`):** returned with
`IsError: true` so the caller receives the full output. Missing script returns
an informational message rather than a hard error.

**Errors:** `-32602` missing `name`. `-32000` skill not found.

---

## Error codes

| Code | Name | Triggered when |
|------|------|----------------|
| `-32602` | Invalid params | Missing `name`, skill pinned without `force` |
| `-32603` | Internal error | Skills service unavailable |
| `-32000` | Not found | Unknown skill name |

## See also

- [docs/skills.md](../skills.md) — SKILL.md format, required sections, `example-skill` fixture caveat
- [docs/api/cli.md](cli.md) — `mneme skills list/install/pin/unpin/remove/lint/validate`
