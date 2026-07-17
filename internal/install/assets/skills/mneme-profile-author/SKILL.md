---
name: mneme-profile-author
description: "Use to CREATE or curate a mneme profile — a team's methodology (agents, skills, rules, blocks, models, templates) AND its project scaffolds packaged as a portable git repo, activated with nvm-like semantics. Sibling of mneme-init: mneme-init ONBOARDS a repo to a profile; this skill AUTHORS the profile repo itself. Trigger keywords: create a profile, author a profile, new mneme profile, curate profile agents/rules, capture a scaffold, author project scaffolds."
version: 1.1.0
pinned: false
---

<!-- mneme-profile-author: profile-authoring grill, SPEC-095 §5 + SPEC-100 §7c / EPIC profiles. -->

## When to Use

Use this skill when:
- The user explicitly asks to "create a profile", "author a profile", "start a new mneme profile", or "package our team's methodology as a profile".
- The user has an existing profile repo checked out and wants to curate/extend its `agents/`, `skills/`, `rules.jsonl`, `blocks/`, `models.toml`, `policy.toml`, `templates/`, or `scaffolds/`.
- The user wants to author the project scaffolds a profile generates repos from — "capture a scaffold", "turn wirvii360r into a scaffold", "add a /new-project archetype to our profile".
- The user asks specifically how to package their team's agents/rules/conventions for reuse across repos (nvm-like distribution).

This skill is the SIBLING of `mneme-init`, never a sub-phase of it: `mneme-init` onboards a REPO to consume a profile (detects the pin, offers the install gate, authors capa-2/3); this skill authors the PROFILE REPO itself, independent of any single consuming project.

## Critical Rules

1. Always start with `profile_new` (or confirm an existing profile repo root) — never hand-create the directory tree or `mneme-profile.toml` yourself; the scaffolder is the only source of truth for the tree's shape.
2. The capa-1 envelope (frontmatter `tools:`/`permissionMode:` + the `agent-fixed` managed block) written to `<profile-repo>/agents/<role>.md` is ALWAYS Go-authored via `subagent_compose(archetype=...)` — NEVER invent or hand-write `tools:`/`permissionMode:` yourself, regardless of how confident you are about a role's capabilities.
3. Every `role` passed to `subagent_compose` must match `^[a-z][a-z0-9-]*$`; `archetype` must be one of `architect, backend, frontend, qa-tester, bug-hunter, diagnostician`.
4. `subagent_compose` returns a PREVIEW only — nothing is written to disk by it. Use `Write` yourself to place the composed result at `<profile-repo>/agents/<role>.md` (the profile repo is the author's own content, never the protected `.claude/agents/` of the CURRENT project).
5. The `areas_layer3_md`/`profile_json` content fed into `subagent_compose` here must be the TEAM's stack-agnostic doctrine and best practices — never a single repo's concrete apps/paths (that per-repo capa-2/3 is authored later, by `mneme-init`, when a specific project activates this profile).
6. Author project scaffolds via CAPTURE + CURATION, never by hand: run `scaffold_capture` (MCP) / `mneme scaffold capture <exemplar-repo>` to emit a DRAFT `scaffolds/<name>/scaffold.toml` + captured trees (`shell/`/`overlay/` or `skeleton/`, and each app under `_blueprints/`) with the exemplar's identity parametrized — then curate the draft. NEVER hand-write a `scaffold.toml` or invent a `bootstrap` version; the deterministic command is the only source of truth for a scaffold's shape.
7. `rules.jsonl` holds one JSON object per line — `{title, content, applies_to, severity}` — never a JSON array wrapping the whole file.
8. Never commit, tag, or push on the author's behalf — `profile_new` only runs `git init`; the author decides when to `git add`/`commit`/`tag`/`push` with their own credentials.

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| Scaffold first | `profile_new` (or an equivalent confirmed existing profile repo) ran before any curation step | Call `profile_new` before writing agents/rules/blocks |
| Capa-1 always Go-authored | Every `agents/<role>.md` written to the profile repo came from a `subagent_compose` preview, never hand-written `tools:`/`permissionMode:` | Re-derive the file via `subagent_compose(archetype=...)` and `Write` its `composed_md` verbatim |
| Role/archetype validity | Every `role` matches `^[a-z][a-z0-9-]*$` and every `archetype` is one of the six built-ins | Rename the role or pick a valid archetype before composing |
| Scaffolds captured, not hand-written | Every `scaffolds/<name>/scaffold.toml` originated from `scaffold_capture` and was curated, never authored from scratch | Re-run `scaffold_capture <exemplar>` and curate its draft instead of hand-writing the TOML |
| rules.jsonl shape | Each non-blank line of `rules.jsonl` is a single JSON object (title/content/applies_to/severity), not a JSON array | Split a wrapping array into one object per line |
| No unauthorized git ops | This skill never runs `git commit`/`git push`/`git tag` on the author's behalf | Tell the author the commands to run themselves instead of running them |

## Verification

- Run `mneme skills validate mneme-profile-author` to execute the deterministic validation script (confirms this file still documents `profile_new`/`subagent_compose`/`scaffold_capture`, the Go-authored capa-1 invariant, and the capture-based scaffold authoring flow).
- Run `mneme skills lint mneme-profile-author` to confirm the structural format (5 sections, 3-column Automated Checks table, semver, `name==directory`).
- After scaffolding: `<dest>/mneme-profile.toml` exists and parses, `<dest>/.git` exists, and `<dest>/scaffolds/_blueprints/` is present (empty until you capture a scaffold).
- After composing an agent: the `subagent_compose` response's `valid` field is `true` before you `Write` it — never write a preview flagged invalid.
- After capturing a scaffold: `<dest>/scaffolds/<name>/scaffold.toml` exists and `mneme profile ...` never rejects it — the command guarantees a draft that `ParseScaffold` accepts; your curation must keep it valid.
- Before telling the author they're done: confirm every curated piece (`agents/`, `skills/`, `rules.jsonl`, `blocks/`, `models.toml`, `policy.toml`, `templates/`, `scaffolds/`) was either filled in or explicitly left for later, and remind them of the exact `git add`/`commit`/`tag`/`push` sequence plus how a team consumes the result (`mneme profile add <url> --ref <tag>` → `mneme profile use <name>`).

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

8. Curate `<dest>/skills/<name>/` for any full team skill the profile should carry (authored fresh or copied in). Note: the project-scaffolding grill skills (`/new-project`, `/new-app`) are BUNDLED, generic, and version-locked in mneme itself — the profile supplies only the scaffold DATA (`scaffolds/`, `_blueprints/`), never re-authored grill wrappers. Do not draft `/new-project`/`/new-app` content here.

### Step 7 — Scaffolds (capture + curation)

9. Author each project scaffold the profile should generate repos from, following §15.6 (capture + curation, branching by toolchain):
   - **Capture:** with the author, pick an exemplar repository (e.g. an existing team monorepo). Run `scaffold_capture` (`{repo, name, into=<dest>}`) / `mneme scaffold capture <exemplar> --name <name> --into <dest>`. It auto-detects `apps/`/`packages/`/`turbo.json`/`pnpm-workspace.yaml` to infer the layout (`single`|`monorepo`) and toolchain (`turborepo`|`custom`), reads `go.mod`/`package.json` for the identity, and writes a DRAFT `scaffolds/<name>/scaffold.toml` + captured trees, rewriting the exemplar's project name / Go module path to `{{PROJECT_NAME}}`/`{{MODULE_PATH}}` placeholders.
   - **Curate — template vs legacy:** prune from the captured `shell/`/`overlay/`/`skeleton/` and `_blueprints/` anything that is historical cruft rather than a reusable template (dead configs, one-off apps, stale docs). Capture strips only the unambiguous noise (`.git`, `node_modules`); the template-vs-legacy judgment is yours.
   - **Curate — variables:** refine the drafted `[vars]` (prompts/defaults) and add any further `{{placeholders}}` the generated project should fill (org name, ports, package prefixes).
   - **Curate — wiring (custom toolchain only):** for a `custom` monorepo, elicit and refine the `[wiring]` block — where apps live (`apps_dir`) and which root files to touch when one is added (`on_add`, from the closed vocabulary `workspace:`/`json-merge:`/`copy:`). A `turborepo` toolchain wires built-in; a `single` layout needs no wiring.
   - **Curate — bootstrap (optional):** a captured `turborepo` shell ships as `shell/` with no bootstrap. If the team prefers reproducible generation from the official generator, replace `shell/` with a PINNED `bootstrap = "create-turbo@<exact-version>"` — never `@latest` (the draft's determinism invariant).

### Step 8 — Close out

10. Guide the author through `git add` / `git commit` / `git tag v0.1.0` / `git push` themselves (their own credentials — never run these on their behalf), and remind them how a team consumes the result:

```
mneme profile add <this-repo-url> --ref v0.1.0
mneme profile use <name>
```
