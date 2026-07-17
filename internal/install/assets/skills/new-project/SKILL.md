---
name: new-project
description: "Use to CREATE a brand-new project repository from a scaffold of the ACTIVE mneme profile (pin > host default > vanilla). The grill half of /new-project: lists the profile's scaffolds, elicits the choice + variables, defers the assembly to project_new, then chains mneme-init over the fresh repo. Sibling of mneme-init, which onboards an EXISTING repo. Trigger keywords: new project, scaffold a repo, /new-project, start a project from our profile."
version: 1.0.0
pinned: false
---

<!-- new-project: project-scaffolding grill, SPEC-098 / EPIC profiles §7a. -->

## When to Use

Use this skill when:
- The user asks to "start a new project", "scaffold a new repo", "create a project from our profile/stack", or types `/new-project`.
- The user wants a fresh repository that already carries the team's methodology (agents, rules, conventions) via the active profile — born wired, not onboarded after the fact.

This skill is the SIBLING of `mneme-init`, and the two are the opposite ends of one lifecycle (docs/profiles-design.md §15.7): `mneme-init` cables an EXISTING repo to a profile; this skill BIRTHS a new repo from a profile's scaffold, then chains `mneme-init` over it so both converge on the same wired end state.

Do NOT use this skill to add an app to an existing monorepo (that is `/new-app`, a later spec) or to onboard an existing repository (that is `mneme-init`).

## Critical Rules

1. Never assemble the repo yourself: the deterministic assembly (copy skeleton, substitute variables, `git init`) is ALWAYS performed by the `project_new` tool (or the `mneme project new` CLI). This skill only decides WHAT — it never copies files, edits templates, or shells out a generator by hand.
2. Never pass or invent an unpinned (floating) generator version. The scaffold's `scaffold.toml` owns the exact pinned bootstrap version; determinism is enforced by `project_new` itself (a scaffold with an unpinned bootstrap is rejected). Do not attempt to override it.
3. Always list the active profile's scaffolds first (via `profile_status`/`project_new`'s catalog) and let the user CHOOSE — never assume a scaffold name. If the active profile has no scaffolds, say so plainly (clean "nothing to scaffold" degradation) rather than inventing one.
4. Only elicit the variables the chosen scaffold declares in its `[vars]`; pass them through `project_new`'s `vars` map. Do not invent variables the scaffold does not declare.
5. The destination directory must be empty or absent — confirm the path with the user before calling `project_new`; a non-empty destination is rejected.
6. After `project_new` succeeds, ALWAYS chain `mneme-init` over the freshly created repo (the pin is already written, so `mneme-init` runs in profile-active mode: it materializes capa-1 + capa-2/3 agents and seeds memory). The repo is not "done" until `mneme-init` has run.
7. Never commit, tag, set a remote, or push on the user's behalf — `project_new` only runs `git init`. The user makes the first commit with their own credentials.

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| Defers assembly to the command | This SKILL.md invokes `project_new` (or `mneme project new`) rather than describing a hand-rolled copy/generator flow | Route all assembly through `project_new`; remove any manual copy/exec instructions |
| No unpinned bootstrap | The skill never instructs passing a floating/unpinned generator version | Remove any floating version reference; the exact pinned version lives in the scaffold's `scaffold.toml` |
| Lists scaffolds before choosing | The skill lists the active profile's scaffolds and lets the user pick, never assuming a name | Add the list-then-choose step before calling `project_new` |
| Chains mneme-init | The skill runs `mneme-init` over the fresh repo after assembly | Add the mneme-init chaining step as the closing action |
| No unauthorized git ops | The skill never runs `git commit`/`git tag`/`git push`/`git remote` on the user's behalf | Tell the user the commands to run themselves instead |

## Verification

- Run `mneme skills validate new-project` to execute the deterministic validation script (confirms this file still defers to `project_new`, forbids a floating bootstrap version, and chains `mneme-init`).
- Run `mneme skills lint new-project` to confirm the structural format (5 sections, 3-column Automated Checks table, semver, `name==directory`).
- After `project_new`: the destination directory exists, contains the scaffold's files with variables substituted, has a `.git` directory (no commits), and a `.mneme-profile` pin recording `scaffold=<name>`.
- After chaining `mneme-init`: the fresh repo has materialized agents under `.claude/agents/` and seeded memory — confirm before telling the user the project is ready.

## Workflow

### Step 1 — Resolve the active profile and list scaffolds

1. Determine the active profile (pin > host default > vanilla) with `profile_status`. Tell the user which profile will supply the catalog.
2. List the profile's scaffolds. If there are none, stop and tell the user the active profile has no scaffolds (nothing to scaffold from) — offer to author one via `mneme-profile-author` instead.

### Step 2 — Choose a scaffold and destination

3. Present the available scaffolds and let the user choose ONE. Note its layout (`single` today; `monorepo` arrives in a later mneme).
4. Agree on the destination directory with the user. It must be empty or absent.

### Step 3 — Elicit variables

5. For each variable the chosen scaffold declares in `[vars]`, ask the user (offering the declared default). Collect them into a `key=value` map.

### Step 4 — Assemble (deterministic)

6. Call `project_new` (`{scaffold, dir, vars?}`) — or `mneme project new <scaffold> --dir <path> --var k=v ...`. This copies the skeleton with `{{var}}` substitution, runs `git init` (no commit, no remote), and writes the `.mneme-profile` pin with `scaffold=<name>` plus the active profile's identity. Never assemble by hand.

### Step 5 — Chain mneme-init

7. Run the `mneme-init` skill over the freshly created repo. Because the pin is already written, `mneme-init` runs in profile-active mode: it materializes capa-1 (profile) + capa-2/3 (repo) agents and seeds foundational memory. The repo is now born wired.

### Step 6 — Close out

8. Remind the user to make their first commit themselves (their own credentials — never on their behalf): `git add` / `git commit` / optionally set a remote and push. Do not run these.
