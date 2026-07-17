---
name: new-app
description: "Use to ADD a composable app to an existing MONOREPO grown from the active mneme profile's scaffold. The grill half of /new-app: reads the monorepo's pin to learn its scaffold, offers that archetype's blueprints, elicits the app name + variables, then defers the deterministic copy + auto-wiring to app_add. Sibling of /new-project, which BIRTHS a repo. Trigger keywords: new app, add an app, scaffold an app, /new-app, add a service to the monorepo."
version: 1.0.0
pinned: false
---

<!-- new-app: monorepo app-scaffolding grill, SPEC-099 / EPIC profiles §7b. -->

## When To Use

Use this skill when:
- The user asks to "add an app", "add a service", "scaffold a new app in the monorepo", or types `/new-app`.
- The repository is a MONOREPO that was born from a profile scaffold (its `.mneme-profile` pin records `scaffold=<name>`), and the user wants a new app grown from one of that archetype's blueprints, already wired into the workspace and toolchain.

This skill is the SIBLING of `/new-project`: `/new-project` BIRTHS a fresh repository from a scaffold; this skill ADDS an app to an existing monorepo already grown from one. Do NOT use it to create a new repository (that is `/new-project`) or to onboard an existing repo (that is `mneme-init`).

If the project is a `single`-layout scaffold, there are no composable apps to add — say so plainly instead of forcing an app.

## Critical Rules

1. Never copy the blueprint or edit workspace/toolchain files yourself: the deterministic copy (blueprint into the apps directory, `{{var}}` substitution) and the auto-wiring (`pnpm-workspace.yaml`, `turbo.json`, or the scaffold's declared `[wiring]`) are ALWAYS performed by the `app_add` tool (or the `mneme app add` CLI). This skill only decides WHAT — it never copies files or edits root config by hand.
2. Always read the monorepo's scaffold from its pin first — `app_add` resolves `scaffold` from the `.mneme-profile` pin and offers only the blueprints THAT archetype declares. Never invent a blueprint the scaffold does not offer.
3. Never pass or invent an unpinned (floating) generator version. App wiring never bootstraps a generator, but if you reference the originating scaffold's bootstrap, it is already pinned to an exact version in `scaffold.toml`; do not override it with a floating one.
4. Only elicit the variables the scaffold declares in its `[vars]`; pass them through `app_add`'s `vars` map. Do not invent variables the scaffold does not declare.
5. The target app directory (apps-dir/`<name>`) must be empty or absent — confirm the app `name` with the user before calling `app_add`; a non-empty target is rejected.
6. Never commit, tag, set a remote, or push on the user's behalf — `app_add` only copies and wires files (no `git init`, the monorepo already has its `.git`). The user makes the commit and runs `pnpm install` themselves.
7. If the layout is `single`, stop and tell the user app add does not apply — never fabricate an apps directory in a flat project.

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| Defers assembly to the command | This SKILL.md invokes `app_add` (or `mneme app add`) rather than describing a hand-rolled copy/wiring flow | Route all copy + wiring through `app_add`; remove any manual copy/edit instructions |
| Reads the scaffold from the pin | The skill reads the monorepo's `scaffold` from its `.mneme-profile` pin and offers that archetype's blueprints, never assuming one | Add the read-pin-then-offer step before calling `app_add` |
| No unpinned bootstrap | The skill never instructs passing a floating/unpinned generator version | Remove any floating version reference; the exact pinned version lives in the scaffold's `scaffold.toml` |
| Single layout degrades cleanly | The skill states that app add does not apply to a single-layout project | Add the "single layout → does not apply" guard |
| No unauthorized git ops | The skill never runs `git commit`/`git tag`/`git push`/`git remote` on the user's behalf | Tell the user the commands to run themselves instead |

## Verification

- Run `mneme skills validate new-app` to execute the deterministic validation script (confirms this file still defers to `app_add`, reads the scaffold from the pin, and forbids a floating bootstrap version).
- Run `mneme skills lint new-app` to confirm the structural format (5 sections, 3-column Automated Checks table, semver, `name==directory`).
- After `app_add`: the apps directory contains the new app with variables substituted, `pnpm-workspace.yaml` covers it (directly or via an existing glob), and any custom `[wiring]` edits are applied — confirm before telling the user the app is ready.

## Workflow

### Step 1 — Read the monorepo's scaffold

1. Read the monorepo's `.mneme-profile` pin (via `profile_status`) to learn which `scaffold` generated it. If the pin records no scaffold, tell the user this repo was not born from a scaffold and ask them to pass an explicit `scaffold` (or run `/new-project` for a fresh one).
2. If the scaffold's layout is `single`, stop: app add does not apply to a flat project.

### Step 2 — Offer the archetype's blueprints

3. Present the blueprints the scaffold declares and let the user choose ONE. Note the toolchain (`turborepo` built-in wiring, or `custom` declared `[wiring]`).

### Step 3 — Name the app and elicit variables

4. Agree on the app `name` with the user (a safe-slug). The target apps-dir/`<name>` must be empty or absent.
5. For each variable the scaffold declares in `[vars]`, ask the user (offering the declared default). Collect them into a `key=value` map.

### Step 4 — Add and wire (deterministic)

6. Call `app_add` (`{blueprint, name, dir?, vars?}`) — or `mneme app add <blueprint> --name <app> --var k=v ...`. This copies the blueprint into the apps directory with `{{var}}` substitution and auto-wires it (Turborepo adapter updates `pnpm-workspace.yaml` — often a no-op when a glob already covers `apps/*` — or the custom `[wiring]` actions run). Never wire by hand.

### Step 5 — Close out

7. Remind the user to run `pnpm install` to link the new workspace, then make their own commit (their own credentials — never on their behalf). Do not run these.
