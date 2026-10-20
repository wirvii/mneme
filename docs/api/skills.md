# API Reference — Skills (CLI only since v1.49)

The seven `skills_*` MCP tools (SPEC-037) left the MCP server in v1.49
(SPEC-163). The capability is unchanged and lives in `mneme skills`
([docs/api/cli.md](cli.md#skills)). Calling a former tool name over MCP now
answers `-32601` `unknown tool: <name>`. Concept guide:
[docs/skills.md](../skills.md) (SKILL.md format, pin semantics, bundled
skills). Index: [docs/API.md](../API.md).

mneme is the **cross-runtime package manager** for skills. It mirrors managed
content to `~/.claude/skills/` and `$HOME/.agents/skills/`; it does not
implement either runtime's skill loader.

## Retired tool → CLI command

| Retired MCP tool | CLI command | Notes |
|---|---|---|
| `skills_list` | `mneme skills list [--json]` | Bundled and installed skills with version, installed/pinned state and lint result |
| `skills_install` | `mneme skills install <name> [--force]` | Respects pin protection unless `--force`; mirrors to both runtime destinations |
| `skills_pin` | `mneme skills pin <name>` | Sets `pinned: true` in the installed `SKILL.md` |
| `skills_unpin` | `mneme skills unpin <name>` | Sets `pinned: false` |
| `skills_remove` | `mneme skills remove <name> [--force]` | Refuses a pinned skill unless `--force` |
| `skills_lint` | `mneme skills lint [<name>] [--json]` | Deterministic structural check (frontmatter, semver, size limits of 4096/8192 bytes, `references/` citations that exist); no script is executed. Exits non-zero on findings |
| `skills_validate` | `mneme skills validate <name> [--json]` | Runs `validation/run.sh` with a 120-second timeout; exits non-zero on failure |

The old tools returned lint and validate failures as `IsError: true` payloads
so an agent could read the full finding list; from the CLI the same detail is
in the command's output (use `--json` to parse it) and the exit code carries
pass/fail.

## See also

- [docs/skills.md](../skills.md) — SKILL.md format, pin semantics
- [docs/api/cli.md](cli.md#skills) — flags for every command above
