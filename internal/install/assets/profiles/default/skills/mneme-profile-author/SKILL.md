---
name: mneme-profile-author
description: "Use to CREATE or curate a mneme profile — a team's methodology (agents, skills, rules, blocks, models, templates) packaged as a portable git repo, activated with nvm-like semantics. Sibling of mneme-init: mneme-init ONBOARDS a repo to a profile; this skill AUTHORS the profile repo itself. Trigger keywords: create a profile, author a profile, new mneme profile, curate profile agents/rules."
version: 1.0.0
pinned: false
---

<!-- mneme-profile-author: profile-authoring grill, SPEC-095 / EPIC profiles §5. -->

## When to Use

Use this skill when:
- The user explicitly asks to "create a profile", "author a profile", "start a new mneme profile", or "package our team's methodology as a profile".
- The user has an existing profile repo checked out and wants to curate/extend its `agents/`, `skills/`, `rules.jsonl`, `blocks/`, `models.toml`, `policy.toml`, or `templates/`.
- The user asks specifically how to package their team's agents/rules/conventions for reuse across repos (nvm-like distribution).

This skill is the SIBLING of `mneme-init`, never a sub-phase of it: `mneme-init` onboards a REPO to consume a profile (detects the pin, offers the install gate, authors capa-2/3); this skill authors the PROFILE REPO itself, independent of any single consuming project.

## Critical Rules

1. Always start with `profile_new` (or confirm an existing profile repo root) — never hand-create the directory tree or `mneme-profile.toml` yourself; the scaffolder is the only source of truth for the tree's shape.
2. The capa-1 envelope (frontmatter `tools:`/`permissionMode:` + the `agent-fixed` managed block) written to `<profile-repo>/agents/<role>.md` is ALWAYS Go-authored via `subagent_compose(archetype=...)` — NEVER invent or hand-write `tools:`/`permissionMode:` yourself, regardless of how confident you are about a role's capabilities.
3. Every `role` passed to `subagent_compose` must match `^[a-z][a-z0-9-]*$`; `archetype` must be one of `architect, backend, frontend, qa-tester, bug-hunter, diagnostician`.
4. `subagent_compose` returns a PREVIEW only — nothing is written to disk by it. Use `Write` yourself to place the composed result at `<profile-repo>/agents/<role>.md` (the profile repo is the author's own content, never the protected `.claude/agents/` of the CURRENT project).
5. The `areas_layer3_md`/`profile_json` content fed into `subagent_compose` here must be the TEAM's stack-agnostic doctrine and best practices — never a single repo's concrete apps/paths (that per-repo capa-2/3 is authored later, by `mneme-init`, when a specific project activates this profile).
6. `scaffolds/_blueprints/` is left EMPTY by `profile_new` and stays that way in this skill — project scaffolding (`/new-project`, `/new-app`) is authored by a separate, later spec (§7 of the EPIC). Never populate it here.
7. `rules.jsonl` holds one JSON object per line — `{title, content, applies_to, severity}` — never a JSON array wrapping the whole file.
8. Never commit, tag, or push on the author's behalf — `profile_new` only runs `git init`; the author decides when to `git add`/`commit`/`tag`/`push` with their own credentials.

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| Scaffold first | `profile_new` (or an equivalent confirmed existing profile repo) ran before any curation step | Call `profile_new` before writing agents/rules/blocks |
| Capa-1 always Go-authored | Every `agents/<role>.md` written to the profile repo came from a `subagent_compose` preview, never hand-written `tools:`/`permissionMode:` | Re-derive the file via `subagent_compose(archetype=...)` and `Write` its `composed_md` verbatim |
| Role/archetype validity | Every `role` matches `^[a-z][a-z0-9-]*$` and every `archetype` is one of the six built-ins | Rename the role or pick a valid archetype before composing |
| scaffolds/ deferred | `scaffolds/_blueprints/` remains empty; no project-scaffolding content authored here | Remove any content added to `scaffolds/`; defer to the project-scaffolding spec (§7) |
| rules.jsonl shape | Each non-blank line of `rules.jsonl` is a single JSON object (title/content/applies_to/severity), not a JSON array | Split a wrapping array into one object per line |
| No unauthorized git ops | This skill never runs `git commit`/`git push`/`git tag` on the author's behalf | Tell the author the commands to run themselves instead of running them |

## Verification

- Run `mneme skills validate mneme-profile-author` to execute the deterministic validation script (confirms this file still documents `profile_new`/`subagent_compose`, the Go-authored capa-1 invariant, and the `scaffolds/`-deferred-to-§7 note).
- Run `mneme skills lint mneme-profile-author` to confirm the structural format (5 sections, 3-column Automated Checks table, semver, `name==directory`).
- After scaffolding: `<dest>/mneme-profile.toml` exists and parses, `<dest>/.git` exists, and `<dest>/scaffolds/_blueprints/` is present but empty.
- After composing an agent: the `subagent_compose` response's `valid` field is `true` before you `Write` it — never write a preview flagged invalid.
- Before telling the author they're done: confirm every curated piece (`agents/`, `skills/`, `rules.jsonl`, `blocks/`, `models.toml`, `policy.toml`, `templates/`) was either filled in or explicitly left for later, and remind them of the exact `git add`/`commit`/`tag`/`push` sequence plus how a team consumes the result (`mneme profile add <url> --ref <tag>` → `mneme profile use <name>`).

## Workflow

### Step 1 — Scaffold

1. Determine the profile's `name` (safe-slug, `^[a-z0-9][a-z0-9-]*$`) and destination directory with the author.
2. Call `profile_new` (`{name, dir?}`) to create the skeleton — the tree, a stub `mneme-profile.toml`, `README.md`, `rules.jsonl`, `models.toml`, `policy.toml`, and `git init` (no commit). If the author already has a partially-curated profile repo, confirm its root instead of re-scaffolding over it.

### Step 2 — Identity

3. Elicit `version`, `description`, and (optionally) a `[compat].mneme` constraint, and write them into `<dest>/mneme-profile.toml` with `Write`/`Edit`.

### Step 3 — Agents capa-1 (the team's standard roles)

4. For each role the team standardizes on: call `subagent_compose(role, archetype, areas_layer3_md=<team doctrine/best-practices, stack-agnostic>, profile_json=<team-wide facts, NO concrete repo apps>)` to get the Go-authored envelope (frontmatter `tools:`/`permissionMode:` + `agent-fixed` block) plus the doctrine.
5. Show the author the preview (`composed_md`) and, on confirmation, `Write` it to `<dest>/agents/<role>.md`.

### Step 4 — Rules

6. Curate `<dest>/rules.jsonl`: one JSON `RuleSpec` object per line (`title`, `content`, `applies_to`, `severity` ∈ `info|warn|block`, optional `topic_key`) — commit conventions, branch naming, cross-cutting policies the team always wants enforced.

### Step 5 — Blocks / models / policy / templates

7. Curate `<dest>/blocks/*.md` (managed CLAUDE.md blocks the orchestrator applies on activation), `<dest>/models.toml` (per-agent model assignment), `<dest>/policy.toml` (`subagent_containment` + lanes/SDD policy), and `<dest>/templates/{spec,plan,qa-report}.md` (the entregables `spec_doc_write` renders).

### Step 6 — Skills

8. Curate `<dest>/skills/<name>/` for any full team skill the profile should carry (authored fresh or copied in). Note: skills for project scaffolding (`/new-project`, `/new-app`) live under `skills/` too, but authoring THEM is a separate, later spec (§7) — do not draft their content here.

### Step 7 — scaffolds/ (explicitly deferred)

9. Tell the author `<dest>/scaffolds/_blueprints/` stays empty — project-scaffolding blueprints are wired by a separate spec (§7 of the EPIC); this skill never populates it.

### Step 8 — Close out

10. Guide the author through `git add` / `git commit` / `git tag v0.1.0` / `git push` themselves (their own credentials — never run these on their behalf), and remind them how a team consumes the result:

```
mneme profile add <this-repo-url> --ref v0.1.0
mneme profile use <name>
```
