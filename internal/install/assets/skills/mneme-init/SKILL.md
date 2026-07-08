---
name: mneme-init
description: "Use once to bootstrap a project with mneme, or to refresh/onboard it later. Single project-level entry point: seeds foundational repo knowledge into memory, applies mneme's managed CLAUDE.md blocks (replacing the native /init), and offers three independent opt-in steps — per-project subagent generation via a 5-phase grill, codegraph indexing, and shared team memory. Trigger keywords: mneme-init, initialize mneme, onboard this repo, generate subagents, set up codegraph."
version: 1.0.0
pinned: false
---

<!-- mneme-init: project-level orchestrator, SPEC-058 / EPIC agnostic-agents SS-5. -->

## When to Use

Use this skill when:
- The user explicitly asks to run `mneme-init`, "initialize mneme for this project", or "onboard this repo".
- A session starts and `mem_context`/`mem_search` return no memories for this project (cold-start).
- The user asks to refresh mneme's knowledge after significant repo changes (new apps, new stack, reorganised modules).
- The user asks specifically for one of the opt-in steps: "generate subagents", "set up per-project agents", "index the codegraph", or "wire shared team memory".

## Critical Rules

1. The CORE step (repo analysis + memory seeding + managed CLAUDE.md blocks) always runs and REPLACES the native `/init` command — never run both.
2. The three remaining steps — subagent grill, codegraph, shared memory — are INDEPENDENT and OPT-IN. Ask about each one separately; declining one does not skip the others.
3. Never invent MCP tool names, CLI flags, or schemas. Use only the tools and commands listed in this file.
4. The subagent grill (Phases 0-4) is conducted BY YOU using the deterministic `subagent_*` MCP tools as data sources/writers — permissions (`tools:`, `permissionMode:`) are ALWAYS Go-authored via the `archetype` parameter; never invent them.
5. `subagent_compose` returns a PREVIEW only. Nothing is written to disk until the user confirms and you call `subagent_write`.
6. Every `role` passed to `subagent_compose`/`subagent_write` must match `^[a-z][a-z0-9-]*$`. `archetype` must be one of `architect, backend, frontend, qa-tester, bug-hunter, diagnostician`.
7. The codegraph step invokes the EXISTING `mneme codegraph index` / `mneme codegraph hooks install` commands — never reimplement indexing or hook logic yourself.
8. The shared-memory step is a placeholder until SPEC-053 ships. Say so explicitly; never fabricate a command that does not exist yet.
9. ONE fact per `mem_save` call during Phase 0-2 seeding — never dump an entire file as a single memory. Always set `topic_key` so re-runs upsert instead of duplicating.
10. Never rewrite the user's CLAUDE.md prose directly — only the `init` MCP tool's managed block. Report drift; do not silently edit user content.

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| Core step calls `init` tool | Managed CLAUDE.md blocks + drift report came from the `init` MCP tool, not a hand-edit | Call `init` with `repo_root` (add `check:true` first if unsure) before touching CLAUDE.md |
| One fact per mem_save | Each `mem_save` call during seeding contains a single fact with a `topic_key` | Split multi-fact dumps into separate `mem_save` calls |
| Role slug validity | Every `role` passed to `subagent_compose`/`subagent_write` matches `^[a-z][a-z0-9-]*$` | Rename the role to a lowercase slug before composing |
| Preview before write | `subagent_compose` was reviewed by the user before the matching `subagent_write` call | Always compose (preview), get confirmation, then write |
| Opt-in steps confirmed | Subagents / codegraph / shared-memory steps were each explicitly offered and answered, not silently run or silently skipped | Ask the user yes/no for each opt-in step before acting on it |

## Verification

- Run `mneme skills validate mneme-init` to execute the deterministic validation script (checks that this file still documents the `subagent_*` tools it depends on).
- Run `mneme skills lint mneme-init` to confirm the structural format (5 sections, 3-column Automated Checks table, semver, name==directory).
- After the core step: `mneme init --check` (or the `init` MCP tool with `check:true`) should report the managed block present and list any drift findings.
- After the subagent grill: `subagent_manifest_list` should list the newly written roles with their `engine`/`areas`/`checksum`.
- After the codegraph opt-in: `mneme codegraph status` should report a non-zero file/symbol count for the repo.
- Before reporting completion, confirm with the user that each opt-in step's outcome (done / skipped / deferred) matches what they asked for.

## Workflow

### Step 0 — Core (always runs, no opt-in)

1. Call `mem_context` and `mem_search` (keywords from the repo name/stack) to see what mneme already knows — avoid re-seeding duplicates.
2. Call the `init` MCP tool (`repo_root` = project root; pass `check:true` first for a dry-run if unsure) to apply the global operating manual and the minimal repo managed block to CLAUDE.md and get a drift report. This REPLACES the native `/init` command.
3. Read CLAUDE.md and, if present, package.json / go.mod / Cargo.toml / tsconfig.json / docker-compose.yml / .env.example / a 2-level directory listing. Save ONE memory per fact (types: architecture / config / convention, with topic_keys like `architecture/overview`, `config/commands`, `convention/commits`).
4. Classify each CLAUDE.md section as a **behavior instruction** (keep in CLAUDE.md) or **project knowledge** (migrate to mneme). Do not delete anything from CLAUDE.md — the user cleans it up later if they want.
5. Report to the user what was seeded and the drift findings from step 2.
6. Ask, ONE AT A TIME, whether to proceed with each of the three opt-in steps below.

### Step 1 — Opt-in: generate per-project subagents (5-phase grill)

Only if the user opts in. Uses the `subagent_*` MCP tools exclusively:

| Phase | Purpose | Tools |
|---|---|---|
| 0 | Deterministic fingerprint: project root, apps/packages, stack markers, what typed-memory already exists | `subagent_fingerprint`, `mem_context`, `mem_search` |
| 1 | Elicit repo/org knowledge ONCE (commit convention, language, layout, cross-cutting rules) | `subagent_profile_get`, `subagent_profile_save` (writes `profile_json.repo` + `profile_json.org`) |
| 2 | Propose roles + map apps to roles (one subagent per role, covering ALL its apps) — user adjusts the suggestion | `subagent_profile_save` (writes `profile_json.mapping`) |
| 3 | Per role x area detail: stack, architecture, commands, best practices. Brownfield: pre-seed from Phase 0 + `mem_search`. Evergreen (no code yet): ask the DESIRED stack from scratch | conversation with the user; draft `areas_layer3_md` per role |
| 4 | Compose a preview, then write only on confirmation | `subagent_compose` (preview, no disk writes) → user confirms → `subagent_write` (atomic write + manifest update) |

7. For each role, call `subagent_compose` with `role`, `archetype`, `areas_layer3_md`, and `profile_json` (from Phases 1-2), then show the user the preview.
8. On confirmation, call `subagent_write` with the same `role`/`archetype` and the (possibly user-edited) `composed_md`, plus `areas` and `engine` (defaults to `passthrough`).
9. AFTER at least one implementer role (`backend`, `frontend`, or `bug-hunter` archetype) has been written, OFFER the delegation-enforcement hook: ask the user if they want strict orchestrator/subagent separation for this project. Record the answer via the `enforcement_hook` parameter on `subagent_write` (persisted in the manifest). The two-layer enforcement itself currently ships from the GLOBAL `mneme install claude-code` (transitional, per the agnostic-agents design) — if the user says yes and hasn't run that yet, tell them to.
10. Call `subagent_manifest_list` at the end to confirm what was written.

### Step 2 — Opt-in: codegraph indexing

Only if the user opts in. Do not reimplement indexing — invoke the existing commands:

11. Run `mneme codegraph index` to build the initial semantic index of the repo.
12. Run `mneme codegraph hooks install` to install the git hooks that auto-reindex after commit/checkout.
13. If either command fails or is unsupported (e.g. not a git repository for the hooks step), report the failure and skip — do not attempt a manual workaround.

### Step 3 — Opt-in: shared team memory

Only if the user opts in. This step is a PLACEHOLDER — the vault/sync mechanism it delegates to (SPEC-053, team-memory) has not shipped yet.

14. Tell the user: "Shared team memory (git-synced vault) isn't available yet — it ships in SPEC-053. I'll skip this for now; re-run `mneme-init` once it's released to wire it up."
15. Do not fabricate a command or write any files for this step.

### Step 4 — Final report

16. Summarize what ran, using this format (core always runs; each opt-in step is done / skipped / not requested / deferred):

```
mneme-init complete for {project}

Core: memories seeded (N), managed blocks applied, drift findings: N
Subagents: <done (roles: ...) | skipped | not requested>
Codegraph: <done | skipped | not requested>
Shared memory: <deferred (SPEC-053) | not requested>
```
