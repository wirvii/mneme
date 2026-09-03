---
name: mneme-init
description: "Use once to bootstrap or reconcile a project with mneme for both Claude Code and Codex. Seeds repo knowledge, applies managed instructions, detects a team profile, generates both native agent projections, and offers codegraph and shared-memory setup. Trigger: mneme-init, initialize mneme, onboard this repo, generate subagents, reconcile agent profiles."
version: 1.11.0
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
8. The shared-memory step invokes the EXISTING `mneme team-memory enable` command (SPEC-053/SPEC-065) — never reimplement vault export or hook-install logic yourself, and always relay its privacy notice to the user verbatim (never paraphrase or suppress it).
9. ONE fact per `mem_save` call during Phase 0-2 seeding — never dump an entire file as a single memory. Always set `topic_key` so re-runs upsert instead of duplicating.
10. Never rewrite the user's CLAUDE.md prose directly — only the `init` MCP tool's managed block. Report drift; do not silently edit user content.
11. **The layer 2/3 boundary (SPEC-090).** `areas_layer3_md` (Phase 3, and any content you later synthesize into it) is project KNOWLEDGE ONLY — stack, architecture, commands, best practices, domain. It must NEVER contain lifecycle instructions (`spec_advance`, `spec_quick`, `spec_reject` — layer 1's `agent-fixed` block already governs the SDD lifecycle and explicitly forbids the subagent from calling `spec_advance` itself), capability declarations (`tools:`/`permissionMode:` — those are ALWAYS Go-authored via `archetype`, see rule 4), or role doctrine. `subagent_compose`/`subagent_write` mechanically reject any of this if it slips into `areas_layer3_md`/the composed grill region — but do not rely on the reject as your only defense: draft Phase 3 content that never contains it in the first place.
12. **Profile detection precedes the subagent grill (SPEC-095 §5).** Before Step 1, call `profile_status` to read this repo's pin state and branch: `PinInstalled` → run Step 1 in **profile-active mode** (below); `PinMissing` → offer the install **gate** (`profile_add` + `profile_use`) and only proceed if the user says yes — **never clone without explicit OK**, mirroring the SessionStart gate's own contract; `PinDefault`/`PinAbsent` → run Step 1 in **vanilla mode**, IDENTICAL to today's behavior (zero regression for repos with no profile). In **profile-active mode**, the grill authors and persists capa-2/3 (`subagent_profile_save`, including a `profile_json.areas` entry per role — the capa-3 doctrine draft) but does **NOT** call `subagent_write` (that would bake a second, duplicate capa-1 from the archetype on top of the profile's own); instead it materializes by calling `profile_use <name>`, which fuses the profile's capa-1 with this repo's capa-2/3. In **vanilla mode**, nothing changes: `subagent_compose` → `subagent_write` as always.
13. **Fusing pre-existing agents (SPEC-090, Phase 0.5).** `subagent_fingerprint`'s `foreign_agents` list is the ONLY detection source — never `Read`/`Glob` `.claude/agents/` yourself to look for more. Whether a foreign agent OVERLAPS a proposed role is a judgment call YOU make by reading the file and reasoning about it in conversation — there is no naming heuristic to lean on (a name like `security-auditor` tells you nothing reliable about scope). When you DO fuse one in, you must EXTRACT its project knowledge and draft fresh `areas_layer3_md` prose from it — NEVER concatenate/paste its raw body into `areas_layer3_md`; that is exactly the BL-110 mechanism rule 11's guard exists to catch, and a mechanical reject is a worse outcome for the user than simply never having pasted it. A foreign agent that does not overlap ANY proposed role is offered as a new CUSTOM role (`subagent_compose`/`subagent_write` with `role` set to its own name and `archetype` mapped to the closest of the six built-ins) — it brings ONLY its extracted knowledge, never its own capabilities; `archetype`'s Go-authored `PermissionTable` entry is what governs `tools:`/`permissionMode:`, exactly as for any other role.
14. **Plain language with the person (operating manual §9).** Every question this grill asks, every option it offers, the areas-completeness question of step 7b and the Step 6 final report are **Channels that reach a person**: no metaphor you invented, no foreign term left untranslated, every acronym expanded on first use, and every option must say what it costs the person in practice — not what it is internally. If they ask you to explain something again, change level (show the real file, the real command) instead of rephrasing it. The project knowledge you draft into `areas_layer3_md` is agent-to-agent and stays precise, but **The exemption never travels with the text**: anything you read back to the person, you rewrite in plain language first.
15. **Dual-runtime projection (SPEC-123).** Every confirmed `subagent_write` and every `profile_use` materializes both `.claude/agents/<role>.md` and `.codex/agents/<role>.toml` from one canonical role contract. Never run a second initialization for Codex, never create separate memory or SDD state, and never hand-author one projection from the other. If either projection fails validation, report that role as failed; do not claim partial success.
16. **The SDD opt-in (Step 5, SPEC-130 §2a) invokes the EXISTING `mneme sdd enable` command** — never reimplement the file format, the write-through, or the marker yourself, and always relay its preview output (plan, remote, and the four warnings) verbatim, never paraphrased or suppressed, before asking for confirmation to apply.
17. **A committed marker means someone already decided to publish (SPEC-140 D3/D14, Step 0.6).** When `.mneme/sdd/.mneme-sdd` or `.mneme/shared/.mneme-vault` is already committed, activation is never re-offered for that mechanism: complete only what this machine is missing (hooks, then import — Step 0.6) and report what was done, instead of running Step 3/5's "do you want to enable this?" question again.

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| Core step calls `init` tool | Managed CLAUDE.md blocks + drift report came from the `init` MCP tool, not a hand-edit | Call `init` with `repo_root` (add `check:true` first if unsure) before touching CLAUDE.md |
| One fact per mem_save | Each `mem_save` call during seeding contains a single fact with a `topic_key` | Split multi-fact dumps into separate `mem_save` calls |
| Role slug validity | Every `role` passed to `subagent_compose`/`subagent_write` matches `^[a-z][a-z0-9-]*$` | Rename the role to a lowercase slug before composing |
| Preview before write | `subagent_compose` was reviewed by the user before the matching `subagent_write` call | Always compose (preview), get confirmation, then write |
| Opt-in steps confirmed | Subagents / codegraph / shared-memory steps were each explicitly offered and answered, not silently run or silently skipped | Ask the user yes/no for each opt-in step before acting on it |
| Layer 2/3 stays project knowledge only | `areas_layer3_md` never contains `spec_advance`, `spec_quick`, `spec_reject`, or a `tools:`/`permissionMode:` line — those belong exclusively to layer 1 | Strip the lifecycle/capability content from the draft before calling `subagent_compose`; if it was rejected, re-synthesize using only the token/line the error names |
| Fusion never concatenates | A foreign agent's body was EXTRACTED and re-drafted into fresh `areas_layer3_md` prose, never pasted verbatim (rule 13) | Re-read the foreign file, pull out only the project-knowledge facts, and write new prose in your own words |
| Profile detection before the grill | `profile_status` was called before Step 1 in every run — the mode (vanilla/profile-active/gate) was decided, not assumed | Call `profile_status` first; branch on its `state` before proposing any role |
| No `subagent_write` in profile-active mode | When the repo is `PinInstalled`, Step 1 never calls `subagent_write` — only `subagent_profile_save` + `profile_use` | Replace the `subagent_write` call with `profile_use <name>` to materialize the fusion instead |
| Plain language in every question | Each question asked and each option offered is free of invented metaphors and untranslated foreign terms, expands every acronym on first use, and states what the option costs the person (rule 14) | Rewrite the question before asking it; if the person asks again, change level — show the real file or the real command instead of rephrasing |
| Both runtime projections exist | Every confirmed role has a Claude markdown artifact and a Codex TOML artifact in the shared manifest | Re-run the single compose/write or profile activation; never initialize a second mneme project |
| SDD preview relayed before apply | `mneme sdd enable`'s preview output (plan + the four warnings) was shown to the user, and explicit confirmation was obtained, before `mneme sdd enable --apply` ran | Re-run the preview, relay it verbatim, and wait for confirmation before applying |
| Diagnose-before-offer ordering (Step 0.6, rule 17) | Whenever a mechanism's marker was already present, its hooks-install then its import ran BEFORE `subagent_fingerprint` was called | Re-run Step 0.6 in the documented order (0.6.1 → 0.6.2/0.6.3 → 0.6.4); never call `subagent_fingerprint` first |
| Never re-offers an already-committed mechanism | When a marker (SDD or shared-vault) was already present, Step 0.6 completed what was missing instead of re-running Step 3's or Step 5's activation question | Check the marker first (Step 0.6.1's status calls); only ask the activation question when that mechanism's own marker is absent |

## Verification

- Run `mneme skills validate mneme-init` to execute the deterministic validation script (checks that this file still documents the `subagent_*` tools it depends on, AND that it still documents every `spec_advance`/`spec_quick`/`spec_reject` layer 2/3-forbidden token from rule 11, AND that it still carries the three plain-language anchors of rule 14).
- Run `mneme skills lint mneme-init` to confirm the structural format (5 sections, 3-column Automated Checks table, semver, name==directory).
- After the core step: `mneme init --check` (or the `init` MCP tool with `check:true`) should report the managed block present and list any drift findings.
- After the subagent grill: `subagent_manifest_list` should list each role with two `artifacts`, one for `claude-code` and one for `codex`, plus its `engine`/`areas`. Any foreign agent that was fused now maps to a manifest entry too; any that the user declined remains untouched.
- After fusing a foreign agent (rule 12): its `subagent_write` response's `backup_path` (SPEC-090 D9) should be non-empty — confirms the original file was preserved before being overwritten.
- After enabling the delegation hook: confirm both `<repo>/.claude/settings.json` and `<repo>/.codex/hooks.json` contain the two managed PreToolUse registrations.
- After the codegraph opt-in: `mneme codegraph status` should report a non-zero file/symbol count for the repo.
- After the shared-memory opt-in: `<repo>/.mneme/shared/.mneme-vault` should exist and `mneme team-memory enable`'s output should report the hooks it installed; re-running the command should report "already enabled" instead of erroring.
- After the SDD opt-in: `<repo>/.mneme/sdd/.mneme-sdd` should exist, and `mneme sdd status` should report the mechanism enabled with a non-zero backlog/spec count.
- Before reporting completion, confirm with the user that each opt-in step's outcome (done / skipped / deferred) matches what they asked for.

## Workflow

### Step 0 — Core (always runs, no opt-in)

1. Call `mem_context` and `mem_search` (keywords from the repo name/stack) to see what mneme already knows — avoid re-seeding duplicates.
2. Call the `init` MCP tool (`repo_root` = project root; pass `check:true` first if unsure) to apply the shared project instructions and get a drift report. This replaces the native `/init` command and must not create a repository `AGENTS.md`, because that would suppress Codex's configured `CLAUDE.md` fallback.
3. Read CLAUDE.md and, if present, package.json / go.mod / Cargo.toml / tsconfig.json / docker-compose.yml / .env.example / a 2-level directory listing. Save ONE memory per fact (types: architecture / config / convention, with topic_keys like `architecture/overview`, `config/commands`, `convention/commits`).
4. Classify each CLAUDE.md section as a **behavior instruction** (keep in CLAUDE.md) or **project knowledge** (migrate to mneme). Do not delete anything from CLAUDE.md — the user cleans it up later if they want.
5. Report to the user what was seeded and the drift findings from step 2.
6. Ask, ONE AT A TIME, whether to proceed with each of the three opt-in steps below.

### Step 0.6 — Diagnose before offering anything (always runs, no opt-in; SPEC-140 D3/D14)

Runs right after Step 0, BEFORE Step 0.5 and before offering any opt-in step. A repository whose SDD or shared-memory marker is already committed was already activated by someone on the team — the question this step answers is never "should we turn this on?" (that question is Step 3's/Step 5's, and only fires when a marker is absent), it is "what does THIS machine still need?" Numbered as its own sequence because skipping straight to Phase 0's `subagent_fingerprint` before this step gets the diagnosis wrong in 100% of clones — the subagent manifest is a MEMORY, not a file, so a database with nothing imported yet always reads as "no agents", even in a repository that already has several committed.

0.6.1. Run `mneme sdd status` and `mneme team-memory status` (both read-only) and relay their output to the user.
0.6.2. If `mneme sdd status` reports the mechanism enabled: run, WITHOUT asking, `mneme sdd hooks install` (only if its output says this machine's hooks are missing) and then `mneme sdd import`. NEVER run `mneme sdd enable --apply` here — that is Step 5's own gate, reserved for a repository whose marker is still absent.
0.6.3. If `mneme team-memory status` reports the vault present: run, WITHOUT asking, `mneme team-memory hooks install` (only if missing) and then `mneme team-memory import`.
0.6.4. ONLY AFTER 0.6.2 and 0.6.3 have run, call `subagent_fingerprint` (Phase 0, below). Calling it any earlier answers "no agents" in every clone, because the manifest a fused/adopted agent lives in is imported by 0.6.2/0.6.3, not read from disk.
0.6.5. For each mechanism whose marker is ABSENT, this step does nothing: continue exactly with Step 3 (shared memory) or Step 5 (SDD) as written below, with their warnings verbatim and their explicit confirmation gate untouched.

### Step 0.5 — Profile detection (always runs, no opt-in; SPEC-095 §5)

Runs right after Step 0, BEFORE offering the subagent grill — its outcome decides which MODE Step 1 runs in.

7. Call `profile_status` (optionally `{project_root}`) to read this repo's pin resolution and branch on `state`:
   - **`installed`** → this repo already pins a profile that IS in the host-level store. Tell the user: "this repo uses profile `<name>@<ref>` — the subagent grill's capa-1 comes from the profile; it will only author this repo's capa-2/3." Proceed to Step 1 in **profile-active mode**.
   - **`missing`** → the pin names a profile with a `source` that is NOT installed yet. Offer the **gate**, exactly like the SessionStart gate: "This repo uses `<name>@<ref>` (source `<source>`), not installed. Install it now?" On yes: call `profile_add` with that source/ref, then `profile_use <name>` (which writes nothing further and materializes immediately). On no, or no answer: proceed to Step 1 in **vanilla mode** for this session — **never clone without explicit confirmation**.
   - **`default`** (pinned to mneme's internal default profile, no external `source`) or **`absent`** (no pin at all) → proceed to Step 1 in **vanilla mode** — identical to today's behavior, zero regression.

### Step 1 — Opt-in: generate per-project subagents (5-phase grill)

Only if the user opts in. Uses the `subagent_*` MCP tools exclusively. Runs in **vanilla mode** or **profile-active mode**, decided by Step 0.5 above:

| Phase | Purpose | Tools |
|---|---|---|
| 0 | Deterministic fingerprint: project root, apps/packages, stack markers, what typed-memory already exists, AND `foreign_agents` — pre-existing `.claude/agents/*.md` files mneme did not generate (SPEC-090 D5) | `subagent_fingerprint`, `mem_context`, `mem_search` |
| 0.5 | **Fusion (SPEC-090, only when `foreign_agents` is non-empty).** For EACH foreign agent: read the file, then reason (in conversation, no naming heuristic — rule 12) about whether it overlaps a role you are about to propose. Overlaps → offer to EXTRACT its project knowledge into that role's `areas_layer3_md` draft (never concatenate the raw body). No overlap → offer to adopt it as a new CUSTOM role, mapped to the closest archetype. Declines to either → leave the file untouched, do not mention it again this session | `Read` (the foreign file only — never `Glob`/`Read` to re-discover more); `subagent_compose`/`subagent_write` for the fused or adopted result |
| 1 | Elicit repo/org knowledge ONCE (commit convention, language, layout, cross-cutting rules) | `subagent_profile_get`, `subagent_profile_save` (writes `profile_json.repo` + `profile_json.org`) |
| 2 | Propose roles + map apps to roles (one subagent per role, covering ALL its apps) — user adjusts the suggestion | `subagent_profile_save` (writes `profile_json.mapping`) |
| 3 | Per role x area detail: stack, architecture, commands, best practices. Brownfield: pre-seed from Phase 0 + `mem_search` + any Phase 0.5 extraction. Evergreen (no code yet): ask the DESIRED stack from scratch. **Project knowledge only — never lifecycle (`spec_advance`/`spec_quick`/`spec_reject`), capabilities (`tools:`/`permissionMode:`), or role doctrine (rule 11)** | conversation with the user; draft `areas_layer3_md` per role |
| 4 | **Vanilla mode:** compose a preview, then write only on confirmation. **Profile-active mode:** persist capa-2/3 (repo/org/mapping + this role's `areas_layer3_md` draft as a `ProjectProfileArea`), do NOT write a full agent file — materialize by activating the profile instead | Vanilla: `subagent_compose` (preview) → user confirms → `subagent_write`. Profile-active: `subagent_profile_save` (with `profile_json.areas` including this role) → user confirms → `profile_use <name>` |

6a. If `foreign_agents` (from Phase 0) is non-empty, run Phase 0.5 BEFORE proposing roles in Phase 2: for each foreign path, `Read` it, judge overlap by reasoning about its content against the roles you are about to propose (never by matching names), and tell the user what you found. Offer exactly one of: (a) fuse — its knowledge feeds that role's Phase 3 draft, extracted and re-written, never pasted; (b) adopt as a custom role — proceeds through the normal compose/write flow with `role` = the foreign agent's own name; (c) leave it alone — skip it entirely, do not raise it again. The user decides for each one; never assume. (This foreign-agent fusion path applies to **vanilla mode** — a profile-active repo's capa-1 always comes from the profile, never from a foreign local file.)
7. **Vanilla mode:** for each role, call `subagent_compose` with `role`, `archetype`, `areas_layer3_md`, and `profile_json` (from Phases 1-2, plus any Phase 0.5 extraction), then show the user the preview. If it is rejected for a layer 2/3 leak (rule 11), the error names the exact token and line — remove it from the Phase 3 draft and re-compose; never work around the rejection by hiding the token outside the tool's plain-text argument. **Profile-active mode:** skip `subagent_compose` for this role entirely — its capa-1 already exists in the profile's own `agents/<role>.md`.
7b. For each role, ask the **areas-completeness question** (SPEC-086 D11) BEFORE writing — this is what feeds `areas_complete`, the flag that turns the delegation hook's subagent containment (`mneme delegation-hook`) from purely informational into something that can actually block:
    > "`<role>` hoy declara `<apps from Phase 2>`. **Además de las apps**, ¿en qué otras rutas puede escribir — `packages/*-go`, migraciones, config de raíz? Estas van a ser las **únicas** rutas donde podrá escribir."
    Set `areas_complete: true` ONLY when the user explicitly confirms the resulting area list is exhaustive. **NEVER default it to `true`, NEVER infer it from Phase 2's app→role mapping alone** — Phase 2 maps apps, not every path a role may touch (`packages/*-go`, root-level config, migrations are easy to miss), and `areas_complete: true` on an incomplete list will make that role start losing writes to legitimate paths the day the project promotes to `block` mode (`mneme delegation-hook promote`). When in doubt, or the user is unsure, leave it `false` (or omit it) — an unverified role is never contained, only observed (`would_block` telemetry, never a real block). This question is asked in BOTH modes.
8. **Vanilla mode:** on confirmation, call `subagent_write` once with the same `role`/`archetype` and the composed content, plus `areas`, `areas_complete` and `engine`. The response must contain both runtime artifacts. When this overwrites a foreign Claude profile, mention its `backup_path`. **Profile-active mode:** do NOT call `subagent_write`; persist project knowledge with `subagent_profile_save`, then call `profile_use <name>` once to materialize both projections from the profile's capa-1 and the saved project layers.
9. AFTER at least one implementer role has been written or activated, OFFER strict role enforcement for this project. If yes, run `mneme delegation-hook enable`; it registers both managed PreToolUse hooks in the Claude and Codex project hook files. Record the answer through `enforcement_hook` in vanilla mode. `status` requires both runtimes to be configured and `disable` removes both.
10. Call `subagent_manifest_list` at the end and confirm both artifacts per role. In profile-active mode also re-read `subagent_profile_get` and point to both native files.

### Step 2 — Opt-in: codegraph indexing

Only if the user opts in. Do not reimplement indexing — invoke the existing commands:

11. Run `mneme codegraph index` to build the initial semantic index of the repo.
12. Run `mneme codegraph hooks install` to install the git hooks that auto-reindex after commit/checkout.
13. If either command fails or is unsupported (e.g. not a git repository for the hooks step), report the failure and skip — do not attempt a manual workaround.

### Step 3 — Opt-in: shared team memory

Only if the user opts in. This wires the git-native team-memory vault (SPEC-053): a `.mneme/shared/` directory committed to the repo that project-scoped memories materialize into automatically once enabled — share-by-default (SPEC-071): every human-authored persistent memory type shares, excluding only auto-generated `synthesis` (cluster overviews) and ephemeral `session_summary` — plus git hooks that import teammates' shared knowledge after every pull/checkout. Do not reimplement any of this yourself — invoke the existing command:

14. Before running anything, tell the user: enabling this commits `.mneme/shared/` to the repository — if the remote is (or might become) public, that knowledge becomes publicly visible, including full commit history once pushed. Confirm they still want to proceed.
15. Run `mneme team-memory enable`. It is idempotent (safe to re-run): it creates `.mneme/shared/` + its marker if absent, bakes/exports existing auto-shared memories, and installs the `post-merge`/`post-checkout` import hooks.
16. Relay the command's own output verbatim, including its privacy notice — never paraphrase, summarize away, or suppress it.
17. If the command fails (e.g. not a git repository), report the failure and skip — do not attempt a manual workaround or fabricate a fallback command.

### Step 5 — Opt-in: SDD backlog and specs as versioned files (SPEC-130 §2a)

Only if the user opts in. This wires the SDD git-native mechanism: the SAME backlog items and specs already in mneme's local database, ALSO written as reviewable Markdown files under `.mneme/sdd/` in this repository — so they can be reviewed in a pull request. Do not reimplement any of this yourself — invoke the existing command (rule 16):

18. Run `mneme sdd enable` (the preview, without `--apply`) and relay its FULL output verbatim — the plan (how many backlog items and specs would be exported), the remote git reports locally, and the four honest warnings (publishing to git is not undone; mneme cannot tell whether the remote is public without a network call it deliberately never makes; mneme has not scanned the content for sensitive data; and these files sync into a pull request today, AND into a teammate's own database on every pull/checkout once that teammate's machine has the git hooks installed). Never paraphrase, summarize away, or suppress any of these.
19. Explicitly tell the user, in your own words, what this step buys and its one remaining limit: enabling installs THIS machine's own git hooks, so future pulls import automatically; a teammate who later clones this repository only needs to run `mneme sdd hooks install` once to start receiving the same imports. What still does not happen automatically: if two people create the SAME correlative (e.g. two "BL-050") at the same time, mneme detects and reports that collision but does not yet resolve it — that reconciliation is a separate, later part of this project.
20. Wait for the user's explicit confirmation before applying — the same "read the preview, then confirm" gate Step 3 already uses for shared memory.
21. On confirmation, run `mneme sdd enable --apply`. It exports every backlog item and spec (including archived items and already-completed specs), writes the enable marker (committed to the repository — this turns the mechanism on for every teammate who later clones it, the same "enabling is a team decision" posture as shared memory), updates `.mneme/.gitignore`, and installs this machine's own git hooks.
22. If the command fails — not a git repository, or the repository already carries SDD records this database does not recognize (an unreadable file, or one anchored to a different machine's item) — relay the failure message verbatim (it already names the affected files and says to run `mneme sdd import` first) and skip. Do not attempt a manual workaround, and do not attempt to read or merge those foreign files yourself.

### Step 6 — Final report

23. Summarize what ran, using this format (core always runs; each opt-in step is done / skipped / not requested / deferred):

```
mneme-init complete for {project}

Core: memories seeded (N), managed blocks applied, drift findings: N
Subagents: <done (roles: ...) | skipped | not requested>
Codegraph: <done | skipped | not requested>
Shared memory: <done (vault + hooks installed) | skipped | not requested>
SDD (backlog/specs as files): <done (N backlog items, N specs exported) | skipped | not requested>
```
