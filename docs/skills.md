# mneme Skills — Authoring Guide

mneme acts as a **package manager** for Claude Code skills. It embeds a set of
bundled skills and installs them to `~/.claude/skills/`. This document explains
how to author a conformant skill.

> **Note:** mneme does NOT implement the Claude Code skill runtime. It manages
> installation, linting, and validation only.

---

## Skill Directory Contract

```
<skill-name>/
├── SKILL.md            # REQUIRED
├── scripts/            # OPTIONAL — helper executables
├── references/         # OPTIONAL — on-demand reference docs
└── validation/
    └── run.sh          # OPTIONAL — exit 0 = pass; deterministic
```

Only `SKILL.md` is required. The directory is the unit of install/pin/remove.
The directory name is the canonical skill identifier.

### Naming rules

- Directory name: kebab-case, `[a-z0-9-]+`.
- Must not conflict with existing bundled or installed skills.

---

## SKILL.md Schema

### Frontmatter

```yaml
---
name: my-skill                    # REQUIRED — must equal directory name
description: "1-3 focused sentences describing when to use this skill. Include explicit trigger keywords so the agent can recognise when to apply it."
version: 1.0.0                    # REQUIRED — semver X.Y.Z
pinned: false                     # OPTIONAL — set true to protect from overwrite/removal
license: MIT                      # OPTIONAL — SPDX identifier
---
```

**Field rules:**

| Field | Required | Format | Lint check |
|---|---|---|---|
| name | yes | kebab-case string | Must equal directory name |
| description | yes | 1-3 sentences | warn if <20 or >500 chars |
| version | yes | semver X.Y.Z | Regex `^\d+\.\d+\.\d+` |
| pinned | no | boolean, default false | — |
| license | no | SPDX string | — |

Unknown frontmatter keys are preserved for forward compatibility (INFO finding).

### Body — 5 Required H2 Sections

The body must contain all five of the following H2 headings (case-insensitive,
order is recommended but not enforced by the linter):

```markdown
## When to Use
## Critical Rules
## Automated Checks
## Verification
## Workflow
```

#### `## When to Use`

List explicit trigger conditions. Use concrete keywords and scenarios.

```markdown
## When to Use

Use this skill when:
- You need to implement a new REST endpoint.
- A reviewer asks for "REST API conventions".
- The task description mentions "HTTP handler" or "route".
```

#### `## Critical Rules`

Numbered list of hard requirements the agent must follow.

```markdown
## Critical Rules

1. Every handler must accept a `context.Context` as the first argument.
2. Never embed SQL strings in Go source files — use `.sql` files with sqlc.
3. Always wrap errors with context: `fmt.Errorf("handler: %w", err)`.
```

#### `## Automated Checks`

A markdown table with **exactly three columns**: `Check`, `What it verifies`,
`How to fix`. The linter validates column header names (case-insensitive).

```markdown
## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| No inline SQL | Absence of raw SQL strings in .go files | Move SQL to a .sql file and regenerate with sqlc |
| Error wrapping | All returned errors use fmt.Errorf with %w | Wrap the error: fmt.Errorf("context: %w", err) |
```

#### `## Verification`

Describe how the agent can confirm it executed the skill correctly.
Reference `validation/run.sh` if one exists.

```markdown
## Verification

Run `mneme skills validate my-skill` to execute the validation script.
Expected output: `my-skill: validation passed`.
```

#### `## Workflow`

A numbered procedure the agent should follow.

```markdown
## Workflow

1. Read the existing handler in the target package.
2. Define the domain entity in `internal/model/`.
3. Write the SQL query in `internal/store/queries/`.
4. Run `make generate` to regenerate sqlc code.
5. Implement the service method.
6. Wire the handler in the router.
```

---

## Validation Script

`validation/run.sh` is optional but recommended. Requirements:

- Must be **deterministic and idempotent** — running it twice produces the same
  result.
- Must **not depend on external services** (no network, no LLM calls).
- Exit code **0** = pass; any other code = fail.
- All output goes to stdout/stderr and is captured by `mneme skills validate`.
- A timeout of 120 seconds is applied.
- Requires a POSIX `sh` on `PATH`. Always present on Unix; on Windows it is
  only there once [Git for Windows](https://gitforwindows.org/) is
  installed. Without it, `Validate` returns the non-fatal `ErrNoShell`
  sentinel — the script is skipped, not treated as a failure.

```sh
#!/bin/sh
# my-skill/validation/run.sh
set -e

# Example: verify that required files exist.
test -f internal/store/queries/my_query.sql && echo "my-skill: query file found"
echo "my-skill: validation passed"
exit 0
```

---

## Pinning

Setting `pinned: true` in the installed `SKILL.md` protects the skill from:

- Being overwritten by `mneme install claude-code` or `mneme skills install`.
- Being removed by `mneme skills remove` (without `--force`).

This lets you maintain a locally customised version of a bundled skill without
future upgrades clobbering your edits.

```sh
mneme skills pin my-skill      # set pinned:true
mneme skills unpin my-skill    # set pinned:false
mneme skills remove my-skill --force  # remove even if pinned
```

---

## Skill Lifecycle

```
bundled (embedded in mneme binary)
  │
  ├─ mneme skills install <name>
  │
  ▼
installed (~/.claude/skills/<name>/)
  │
  ├─ mneme skills pin <name>    → protected from overwrite/remove
  ├─ mneme skills lint <name>   → structural check
  ├─ mneme skills validate <name> → run validation/run.sh
  └─ mneme skills remove <name>  → delete directory
```

---

## CLI Reference

```
mneme skills list                  List bundled and installed skills
mneme skills install <name>        Install a bundled skill
mneme skills install <name> --force  Force install even if pinned
mneme skills pin <name>            Protect skill from overwrite/removal
mneme skills unpin <name>          Remove pin protection
mneme skills remove <name>         Remove installed skill
mneme skills remove <name> --force Remove even if pinned
mneme skills lint [<name>]         Lint one or all installed skills
mneme skills lint --json           JSON output
mneme skills validate <name>       Run validation/run.sh
mneme skills validate --json       JSON output
```

---

## MCP Tools Reference

| Tool | Description |
|---|---|
| `skills_list` | List bundled + installed skills |
| `skills_install` | Install a bundled skill (opt: force) |
| `skills_pin` | Set pinned:true on installed skill |
| `skills_unpin` | Set pinned:false on installed skill |
| `skills_remove` | Remove installed skill (opt: force) |
| `skills_lint` | Lint one or all installed skills (IsError on failure) |
| `skills_validate` | Run validation/run.sh (IsError on failure) |

`skills_lint` and `skills_validate` return `IsError:true` in the result payload
when checks fail — this allows the calling agent to inspect the full finding list
or output without triggering a JSON-RPC protocol error.

---

## example-skill

The `example-skill` bundled with mneme is a **structural fixture only**. It
demonstrates the required SKILL.md format and is used in automated tests. It
contains no real architectural guidance. Do not cite its content as a project
convention or decision.

See `internal/install/assets/skills/example-skill/` for the source.

---

## API reference

Full contract (params, returns, errors, examples) for the 7 `skills_*` MCP
tools: [docs/api/skills.md](api/skills.md) →
