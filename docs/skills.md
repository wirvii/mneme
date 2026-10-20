# mneme Skills — Authoring Guide

mneme acts as a **cross-runtime package manager** for skills. It embeds a set of
bundled skills and mirrors them to `~/.claude/skills/` for Claude Code and
`$HOME/.agents/skills/` for Codex. This document explains
how to author a conformant skill.

> **Note:** mneme does NOT implement either agent's skill runtime. It manages
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

## SKILL.md Schema (SPEC-168)

The format is deliberately minimal: a front matter and a body. Keep `SKILL.md`
short and move detail into `references/` files the agent opens on demand
(progressive disclosure).

### Frontmatter

```yaml
---
name: my-skill                    # REQUIRED — must equal directory name
description: "Use when ... (say WHEN the skill applies, with trigger keywords)."
version: 1.0.0                    # OPTIONAL — semver X.Y.Z when present
pinned: false                     # OPTIONAL — set true to protect from overwrite/removal
license: MIT                      # OPTIONAL — SPDX identifier
---
```

| Field | Required | Format | Lint check |
|---|---|---|---|
| name | yes | kebab-case string | Must equal directory name |
| description | yes | 1-3 sentences | warn if <20 or >500 chars; warn if it lacks "when"/"cuando" |
| version | no | semver X.Y.Z | error if present and not semver; info if absent |
| pinned | no | boolean, default false | — |
| license | no | SPDX string | — |

Unknown frontmatter keys are preserved for forward compatibility (INFO finding).

### Body, size and `references/`

The body is free-form Markdown. Two limits keep the main file focused:

- `SKILL.md` larger than **4096 bytes** (`skill.MainFileSoftLimit`) gives a
  warning: move detail to `references/`.
- `SKILL.md` larger than **8192 bytes** (`skill.MainFileHardLimit`) is an error,
  except for skills in the legacy five-section format (below), where it stays a
  warning.

Cite a reference file in the body with its relative path, for example
`Open references/checklist.md when you reach the review step.` The linter
checks every `references/<path>` citation (code blocks included): the file must
exist inside the skill directory, and a path containing `..` or starting with
`/` is an error because it escapes the directory. Trailing `.` and `:` are not
part of the path. A bare `references/` with no file name is ignored.

### Lint rules

| ID | Rule | Severity |
|----|------|----------|
| L1 | `name` present | error |
| L2 | `name` equals the directory name | error |
| L3 | `description` present | error |
| L4 | `description` shorter than 20 or longer than 500 characters | warning |
| L5 | `description` contains neither `when` nor `cuando` | warning |
| L6 | `version` present but not semver | error |
| L7 | `version` absent | info |
| L8 | `SKILL.md` over 4096 and up to 8192 bytes | warning |
| L9 | `SKILL.md` over 8192 bytes | error (warning for legacy format) |
| L10 | a cited `references/<path>` does not exist | error |
| L11 | a cited reference escapes the directory (`..` or leading `/`) | error |
| L12 | `## Automated Checks` exists but its table lacks the 3 columns | warning |
| L13 | unknown front matter key | info |

L10 and L11 need access to the skill directory, so they run through
`skill.LintFS` (used by `mneme skills lint`, `mneme skills list` and the test
that lints every bundled skill). `skill.Lint` on an already-parsed skill applies
the rest.

### Legacy five-section format (still valid, no longer required)

A skill that has all five H2 sections — `## When to Use`, `## Critical Rules`,
`## Automated Checks`, `## Verification`, `## Workflow` — is in the legacy
format. It remains valid, and as a declared, transitional exemption its
`SKILL.md` may exceed 8192 bytes: L9 is then a warning that says
`exento por formato heredado de 5 secciones`. The warning always appears, so the
exemption is never silent. When `## Automated Checks` is present its table
should have the columns `Check`, `What it verifies`, `How to fix`.

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
installed in both runtime destinations
  │
  ├─ mneme skills pin <name>    → protected from overwrite/remove
  ├─ mneme skills lint <name>   → structural check
  ├─ mneme skills validate <name> → run validation/run.sh
  └─ mneme skills remove <name>  → delete directory
```

---

## Usage log (SPEC-168)

mneme can tell you which skills actually get loaded. In Claude Code, `mneme
install claude-code` registers a `PostToolUse` hook with matcher `Skill`
(`mneme hook skill-usage`), so the process only runs when a skill is loaded.
Each load appends one line to `<data dir>/skill-usage.jsonl` (by default
`~/.mneme/skill-usage.jsonl`, file mode `0600`):

```json
{"v":1,"ts":"2026-10-20T03:04:05Z","skill":"grill-me","session_id":"0941f295-...","role":"orchestrator","runtime":"claude-code"}
```

- `v`: record version; a reader skips records with a higher version.
- `ts`: UTC, RFC 3339, seconds.
- `skill`: the name as the agent sent it (may carry a plugin namespace),
  truncated to 200 bytes. `session_id` is truncated to 128, `role` to 64.
- `role`: `orchestrator` when the payload has no `agent_id`; the `agent_type`
  when it does; `unresolved` when there is an `agent_id` without `agent_type`.
- `runtime`: `claude-code`.

Not recorded: skill arguments, file paths, or any content. There is no
rotation (about 150 bytes per load). The hook never blocks and always exits 0;
if the payload lacks a recognizable skill name it records nothing.

`mneme skills usage [--since 30d|12h|2026-10-01] [--json]` aggregates the file:
loads, distinct sessions, last use and roles per skill, plus the known skills
that were never loaded in the window. Unreadable lines are skipped and counted.

**Codex is not recorded.** Codex loads a skill by reading its file, not through
a tool with its own name, so there is no event that says "skill X was loaded";
`mneme install codex` therefore registers no usage hook.

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
mneme skills usage [--since 30d] [--json]  Which skills were loaded, and which never
```

---

## MCP tools (removed in v1.49)

The seven `skills_*` MCP tools (SPEC-037) were removed from the server in
SPEC-163. Every operation above is a `mneme skills` command, which an agent
runs through Bash; `lint` and `validate` exit non-zero when checks fail and
`--json` prints the full finding list or output.

---

## Bundled skills (v1.49)

mneme ships nine skills. Each one appears in an agent's context only by its
`description`, so the descriptions say when to use the skill. The operating
manual lists the same set.

| Skill | What it is for | Detail files |
|---|---|---|
| `mneme-flow` | The common base of the flow family: the lifecycle and who may move it, the approval gate, lanes, the criteria contract, rejections, pushbacks, the review range | `references/depth.md` (the only place with the lane thresholds), `lifecycle.md`, `criteria.md` |
| `feature` | Something new, from idea to a done spec, stopping at the approval gate | uses `mneme-flow` |
| `bug` | A failure: investigate to a diagnosis in the ticket, then promote it only when the root cause is confirmed (replaces the old hunt-and-convert pair) | `references/diagnosis.md` |
| `trivial` | A tiny, low-risk change in a declared scope, checked by the lane auditor | uses `mneme-flow` |
| `review-brief` | A spec in QA: the orchestrator's brief and the QA tester's report, bounded by the criteria and the review range | `references/report.md` |
| `present-spec` | The human approval gate: turn the spec into a plain-language decision and record the explicit answer | none |
| `grill-me` | Refine a standard backlog item one question at a time | none |
| `mneme-init` | Set up a project's roles and memory | none |
| `codegraph` | Use the code graph when the repository is indexed | none |

The two older bug skills (investigation and diagnosis-to-spec, now one `bug`
skill), the structural fixture skill, and the two slash commands that wrapped
the bug skills were retired in v1.49. `mneme install`
removes the copies mneme itself installed earlier from `~/.claude` and
`$HOME/.agents`; a pinned skill, a skill whose `SKILL.md` declares another
name, and a slash command you wrote yourself are left in place and reported.

Subagents load these skills with the `Skill` tool (every role has it); where a
runtime has no such tool they read `~/.claude/skills/<name>/SKILL.md` or
`$HOME/.agents/skills/<name>/SKILL.md`.

---

## API reference

Old-tool to CLI-command table: [docs/api/skills.md](api/skills.md) →
