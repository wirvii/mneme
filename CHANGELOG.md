# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [v1.28.0] — 2026-07-16 — The layer-2/3 boundary: no lifecycle/capabilities in grill content + merge pre-existing dev agents (SPEC-090)

### Added

- **`mneme-init` now recognises and merges a dev's pre-existing agents (SPEC-090, Part B).**
  When the grill runs, `subagent_fingerprint` reports `foreign_agents` (files in `.claude/agents/`
  without mneme's `agent-fixed` block). The grill analyses their responsibilities, offers to unify
  overlapping ones — **synthesising the dev's project knowledge into layer 2/3, never their
  lifecycle/capabilities** — and offers to adopt non-overlapping ones as custom roles mapped to the
  nearest archetype. A dev-authored file is backed up (`.bak-<timestamp>`) before it is overwritten.

### Fixed

- **The grill can no longer leak lifecycle or capability instructions into a subagent's layer-2/3
  body (SPEC-090, Part A — closes the BL-110 live bug).** The invariant is now enforced, not just
  documented: **layer 2/3 = what the agent knows about the project; layer 1 = who it is and what it
  can do.** A mechanical guard in `subagent_compose`/`subagent_write` scans **only the grill region**
  (never the whole file — layer 1 legitimately says "never call `spec_advance`") and rejects
  `spec_advance`/`spec_quick`/`spec_reject` mentions and `tools:`/`permissionMode:` lines. A new
  `subagents doctor` finding (`lifecycle_in_layer23`, CLI + MCP) surfaces already-materialised leaks
  in existing repos. The mneme-init SKILL documents the rule, coupled to the detector's token list by
  a test so the two cannot drift. Honest limit: the guard catches literal tokens, not paraphrased
  doctrine — that is covered by the SKILL prose and grill discipline (documented in the code).

## [v1.27.2] — 2026-07-16 — Fix: subagent manifest paths leaking across machines via the vault (SPEC-089)

### Fixed

- **The subagent manifest no longer corrupts across machines (SPEC-089).** `ManifestEntry.Path`
  was absolute and the manifest auto-shared through the team-memory vault, so a teammate's
  manifest — with their absolute paths — would clobber the local one on `git pull`. Observed in
  production: one repo's manifest pointed a role at a *different* repo, another had Windows paths
  from a teammate's machine. Three-part fix:
  - **Part 1 (defensive):** `subagents regen`/`doctor` now confine every manifest path to the
    project root and reject foreign paths (a path escaping the root, or absolute on a different OS)
    instead of reading/writing them — closing a hole where `regen` could write into the wrong repo.
    New `foreign_path` doctor finding (CLI + MCP).
  - **Part 2:** manifest `Path` is now stored relative to the repo root; legacy absolute paths
    migrate transparently on next `regen` (a path inside the repo relativises with zero behaviour
    change; a foreign one is rejected). The enforcement hook is unaffected — it never read `Path`.
  - **Part 3:** the manifest is excluded from share-by-default and from vault import (by topic key),
    on **both** import paths — the manual `vault import` and the `post-merge`/`post-checkout` git
    hook that caused the real incident. A teammate's `areas_complete` certification can no longer
    silently drive your enforcement hook.

## [v1.27.1] — 2026-07-16 — Hotfix: typescript@7 broke TS/JS code-graph extraction IN SILENCE (SPEC-088)

### Fixed

- **`mneme codegraph index` no longer silently produces an empty TS/JS
  index when the resolved `typescript` package is API-incompatible (e.g.
  `typescript@7.x`).** v1.27.0 shipped with CI red and, worse, the same
  failure reproduced on any real machine with an unpinned global
  `typescript@7`: zero nodes/edges extracted, `errors[]` swallowed, exit 0.
  The silence had three layers, all fixed together (fixing only one leaves
  the others swallowing the signal):
  1. `js/extract.js` now checks the actual Compiler API symbols it uses
     right after `require('typescript')` — capability, not a version
     number — and exits **20** (not 3, which Node reserves) with a
     structured stderr message naming the found version and three escape
     hatches, instead of letting every call throw into the existing
     per-file `catch` and produce a valid-but-empty result.
  2. `TSExtractor.Extract` (`extractor_ts.go`) classifies exit 20 as the
     new sentinel `ErrExtractorIncompatible`, wrapping the subprocess's own
     stderr into the Go error. The classification is sticky (a `fatal`
     field) so a dead subprocess is never written to twice, and
     `cmd.Wait()` is now called through a `sync.Once`-guarded
     `waitProcess()` shared between the death-detection path and `Close()`
     to avoid a second, panicking `Wait()` call.
  3. `Indexer.Index` now aborts the walk on `errors.Is(err,
     ErrExtractorIncompatible)` instead of folding it into the ordinary
     per-file `FilesErrored` counter (`indexer.go:211-216`, the decisive
     swallow — a systemic "this toolchain can't process ANY file" failure
     was being treated the same as one broken `.ts` file). Every other
     per-file extraction error keeps the existing degrade-and-continue
     behaviour: a repo without `node`, or without `typescript` installed at
     all, still indexes its `.go` files and exits 0 exactly as before.
  - `NODE_PATH` precedence inverted (`extractor_ts.go`): an explicitly-set
    `NODE_PATH` now wins over the global npm root, both because it's the
    correct semantics for an explicit env var and because it is the escape
    hatch the new error message points users at
    (`NODE_PATH=/path/to/ts6/node_modules mneme codegraph index`).
    **Behaviour change:** anyone with a stale `NODE_PATH` already set now
    has it take priority over the global install where it previously
    didn't.
  - `docs/codegraph.md` documents the supported majors (5.x/6.x), the
    degrade-vs-abort table, and the three escape hatches; the old
    "typescript must be installed" line named no major.
  - CI (`ci.yml`, ubuntu job) now pins `typescript@6.0.3` explicitly before
    `go test`, and exports `MNEME_TEST_REQUIRE_TS=1` so the TS extractor
    tests are a hard failure — never a silent skip — if that pin ever
    breaks. `internal/codegraph/extractor_ts_test.go`'s
    `TestTSExtractor_ExtractsClass` also had a latent
    `index out of range` panic (asserting `len(classes) != 1 ||
    classes[0].Name != ...` in one `||` expression) fixed to fail cleanly
    instead.
  - See `bug/codegraph-ts-extractor-typescript-v7-runner-drift` for the
    full root-cause diagnosis and reproduction.

## [v1.27.0] — 2026-07-16 — Enforcement that actually enforces + test isolation + subagents that can work (SPEC-084/085/086/087)

### Added

- **Subagents can now do their own work end-to-end (SPEC-087):**
  - `qa-tester` gains `Bash` + `permissionMode: bypassPermissions` so it can
    run its own gates (`go test`, lint, build) unattended, instead of the
    orchestrator running them and pasting back the output. It still carries
    no `Edit`/`Write`/`MultiEdit`/`NotebookEdit` — `IsImplementer` now reads
    the actual toolset rather than `permissionMode` (D1), since the two are
    no longer the same three roles. `architect`, `qa-tester`, `backend`,
    `frontend`, and `bug-hunter` also gain `WebSearch`/`WebFetch`; the
    `diagnostician` envelope is unchanged.
  - New MCP tool `spec_doc_write(id, kind, content)` writes a spec's
    entregable (`spec`/`plan`/`qa-report`/`changes`) to its workflow
    directory — the directory and filename are never caller-supplied (a
    closed, Go-authored `kind -> filename` map plus the persisted spec
    record), closing the "reports copied by hand" gap.
  - `ComposeInput` splits `Role` (identity: frontmatter `name:`, `{{ROLE}}`)
    from `Archetype` (capability: permission envelope) — a custom role no
    longer needs a post-hoc frontmatter patch to fix its name.
  - The agent-fixed layer-1 block no longer instructs subagents to call
    `spec_advance`; it explicitly prohibits it and points to
    `spec_doc_write` instead (`AgentFixedVersion` 1 → 2).
  - `mneme hook enforce-delegation` now unconditionally denies
    `spec_advance`/`spec_quick` to any subagent (exact-match, no mode) —
    the lifecycle stays the orchestrator's.
  - `spec_reject` can now send a `done` spec back to `implementing`
    (the only way out of `done`; `spec_advance` still can't move past it).
  - `mneme subagents regen [--role R] [--all] [--dry-run]` mechanically
    upgrades already-materialised profiles in place, preserving
    hand-authored area sections byte-for-byte; `mneme subagents doctor`
    gains the `stale_agent_fixed` finding (CLI and MCP).
  - MCP surface: 64 → 65 tools.

## [v1.26.0] — 2026-07-14 — Code graph adoption: querylog + mandatory nudge/prompts (SPEC-083)

### Added

- **Code graph adoption loop — querylog + mandatory nudge/prompts (SPEC-083):**
  - **Adoption querylog (W1):** a new leaf package `internal/querylog` records a
    local, privacy-preserving JSONL telemetry file
    (`~/.mneme/projects/<slug>-codegraph-querylog.jsonl`) capturing when an agent
    used a `codegraph_*` tool (`use`, logged authoritatively from the MCP
    dispatch — excludes human CLI use) vs. explored code with Read/Grep/Glob/Bash
    on an indexed project (`opportunity`, logged by the pre-tool-use hook on every
    qualified call). Tool names only — never paths, commands, or queries. On by
    default with an off-switch: `[codegraph] querylog_enabled` (env
    `MNEME_CODEGRAPH_QUERYLOG`). New report `mneme codegraph adoption [--since
    7d] [--json]` prints the adoption ratio `uses/(uses+opportunities)` plus
    top-use / top-missed tools. CLI-only (no new MCP tool, no HTTP).
  - **Nudge extended to Bash-search + mandatory tone (W2):** the pre-tool-use
    codegraph nudge now also fires for Bash code-search commands
    (`grep/egrep/fgrep/rg/ag/ack/find/fd/cat/head/tail`, detected via the shell
    tokenizer) and its copy is rewritten to mandatory, non-blocking tone
    ("consult the code graph FIRST … applies to subagents too"). Still
    context-only, exit 0, once per session (shared across tools), fail-open.
  - **Prompts reinforced (W3):** a "Code graph: consult it FIRST" section was
    added to the operating manual (orchestrator + Codex), and the subagent policy
    (`agent-fixed.md` + the 6 `agents/*.md`) was raised to mandatory tone
    (OBLIGATORIO). A coherence test guards shared vocabulary across nudge,
    manuals, and subagent policy.

## [v1.25.1] — 2026-07-14 — CI green on Windows + Ubuntu (SPEC-081)

### Fixed

- **First CI run (SPEC-077) surfaced three pre-existing fragilities; all fixed
  so the `ci.yml` workflow is green on both runners:**
  - `internal/upgrade` tests `TestUpgrader_Upgrade` and
    `TestDetectInstalledAgents` are Unix-specific (executable bit / `$HOME`
    semantics) and now skip on `runtime.GOOS == "windows"` — `internal/upgrade`
    is the Unix upgrade path; on Windows upgrade goes via `go install`.
  - `TestLaneAudit_FailedAuditReturnsBreaches` (internal/mcp) no longer assumes
    the working tree diverges from `main`; it builds its own ephemeral git repo
    so the breach is deterministic on any checkout (clean CI or feature branch).
  - The lint job builds `golangci-lint` from source (`go install`) so it is
    compiled with the runner's go1.25 toolchain, fixing the
    "built with go1.24 < targeted go1.25.8" load error; two pre-existing
    dead-code findings (`repoSlugFilename`, `Resolver.findNode`) were removed.

## [v1.25.0] — 2026-07-14 — Windows support: go install install/upgrade + path-aware enforcement (EPIC-windows, SPEC-074)

### Added

- **Windows support (EPIC-windows, SPEC-074; sub-specs SPEC-075–SPEC-080)** —
  mneme now runs on Windows end to end, with `go install` as the only
  supported install *and* upgrade path there:
  - **Path-aware orchestrator-guard enforcement (SPEC-075)** —
    `internal/enforcement`'s `IsWhitelisted`/`EvaluateFileTool`/
    `EvaluateBash` now take an explicit `PathContext{Home, TempDir, GOOS}`
    instead of a bare Unix `home` string. In windows-mode they normalize
    backslashes, recognize drive-absolute (`C:\...`) and UNC
    (`\\server\share\...`) paths, and case-fold a fixed set of ASCII
    literals (`CLAUDE.md`, `.claude`, `.mneme`, `docs`) — Unix behavior is
    byte-for-byte unchanged.
  - **Self-upgrade via `go install` (SPEC-076)** — on Windows, `mneme
    upgrade` shells out to `go install github.com/wirvii/mneme/cmd/mneme@<tag>`
    instead of the Unix binary-replace path (`internal/upgrade` is
    untouched). The worst case — a locked `.exe`, e.g. an antivirus scanner
    or the MCP `mneme` process itself — is documented: close Claude Code and
    retry.
  - **Cross-platform CI (SPEC-077)** — new `.github/workflows/ci.yml`, the
    repository's first push/PR test workflow, with a `windows-latest` job
    (build + portable-package tests: `enforcement`, `upgrade`, `skill`,
    `model`, `scoring`, `embed`) alongside a full `ubuntu-latest` job
    (all tests + `golangci-lint`); `release.yml` is unmodified.
  - **`skills validate` cross-platform (SPEC-078)** — `internal/skill.Validate`
    resolves `sh` via `exec.LookPath` and returns the sentinel `ErrNoShell`
    (non-fatal — validation is skipped, not failed) when no shell is found
    on `PATH`, which is expected on Windows without Git for Windows.
  - **`filepath.Join` cleanup (SPEC-079)** — cosmetic path-concatenation
    fixes in `internal/mcp/handlers.go` and `internal/cli/init.go`.
  - **Docs (SPEC-080)** — Windows install/upgrade documented in `README.md`
    and `docs/GUIDE.md`, plus the Git-for-Windows dependency of the
    codegraph/team-memory git hooks (`docs/codegraph.md`,
    `docs/team-memory.md`) and `mneme skills validate` (`docs/skills.md`).

## [v1.24.0] — 2026-07-11 — install no longer ships global agent profiles (SPEC-073)

### Changed

- **`mneme install claude-code` no longer installs global subagent profiles
  (SPEC-073)** — the per-project subagent model (SPEC-068 / the `mneme-init`
  grill) supersedes the six built-in profiles (`architect`, `backend`,
  `frontend`, `qa-tester`, `bug-hunter`, `diagnostician`) that used to be
  written unconditionally to `~/.claude/agents/*.md`, along with the "Agent
  models" step that patched their `model:` frontmatter. A new "Remove legacy
  global agents" install step cleans up profiles left by earlier installs:
  it removes a file only when it is an unmodified copy of the built-in asset
  (the `model:` line is normalised before comparing, so a `mneme model set`
  override does not count as a customisation) — anything hand-edited is left
  intact. `internal/install/assets/agents/*.md` remain embedded unchanged;
  they are still the canonical source `internal/subagents` composes
  per-project profiles from. `mneme install codex` is unaffected — it never
  installed global agent profiles.

## [v1.23.0] — 2026-07-11 — share-by-default team memory + unbreakable human-approval gate (SPEC-071, SPEC-072)

### Changed

- **Team-memory auto-share policy inverted from allow-list to deny-list
  (SPEC-071)** — project-scoped memories now share to the git-native vault
  by default. Previously only 6 types (decision, convention, architecture,
  pattern, bugfix, rule) auto-shared; now all 9 human-authored types do
  (adding discovery, config, preference). Only `synthesis` (auto-generated
  cluster overviews) and `session_summary` (ephemeral) are excluded. Running
  `mneme team-memory enable` again on an already-enabled repo retroactively
  bakes and exports any pre-existing discovery/config/preference memories
  that predate this change. No API/schema changes — `SaveRequest.Shared`
  overrides continue to work in both directions (opt-out an auto-shared
  type, opt-in an excluded one).
- **Operating manuals now declare the unbreakable human-approval gate**
  (SPEC-072) — both `operating-manual.md` (§4 SDD + lanes) and
  `operating-manual-codex.md` (§3 SDD + lanes) spell out that a spec must
  never advance past `specced` into `planning`/`implementing` without
  explicit human approval of the complete spec. The `architect` never calls
  `spec_advance` past `speccing`; answering design questions is not
  approval; any authorization to skip the gate is one-time and never
  inherited. This is a process gate enforced by orchestrator/agent
  discipline, not a hook — the equivalent DB rule (`019f5229`) was removed
  because a file-write hook cannot enforce a process-level gate.

## [v1.22.0] — 2026-07-09 — `go install` support: pure-Go driver + module path aligned (SPEC-070)

### Changed

- **`go install github.com/wirvii/mneme/cmd/mneme@latest` now works** — no
  clone, no C toolchain, no flags. Two independent blockers are fixed
  together (SPEC-070):
  - **SQLite driver: `mattn/go-sqlite3` → `modernc.org/sqlite`** — a pure-Go
    transpilation of the SQLite engine with FTS5 compiled in by default.
    `internal/db`, `internal/codegraph/db.go` and
    `internal/codegraph/probe.go` register the `"sqlite"` driver name and use
    the modernc pragma DSN dialect (`_pragma=<name>(<val>)`); `mode=ro` is
    unchanged. No data migration: the on-disk SQLite file format is identical
    between drivers.
  - **Module path aligned**: `go.mod` declared a `module` path that 404s on
    GitHub and the Go proxy (a stale `juanftp` org reference) while the code
    and releases live at `github.com/wirvii/mneme`. `go install` requires
    them to match; the `module` directive and all internal imports now use
    the real path.
  - **Version resolution without ldflags**: `go install …@vX.Y.Z` does not
    pass `-ldflags`, so `mneme version`/`mneme upgrade` now fall back to
    `runtime/debug.ReadBuildInfo()` when `Version` is still the literal
    `"dev"` default, resolving the real tag. Release builds keep injecting
    the version via `-ldflags -X`, which always takes priority.
- **CGO and `-tags fts5` dropped from build and CI** — `Makefile`,
  `.github/workflows/release.yml`, and `.githooks/pre-push` no longer set
  `CGO_ENABLED=1` or pass `-tags fts5`; none of it is needed with the
  pure-Go driver.
- `install.sh` (pre-built binary download) is unchanged and remains a
  documented alternative to `go install`.

## [v1.21.0] — 2026-07-09 — Portable Go delegation hook (SPEC-069)

### Changed

- **Portable delegation guard: `mneme hook enforce-delegation` (SPEC-069)**:
  the orchestrator-guard (Layer 2) that used to be an embedded ~640-line bash
  script (`~/.claude/hooks/enforce_delegation.sh`, registered with an
  absolute path to the home directory — not portable, not self-contained) is
  now an in-process Go subcommand. A new leaf package, `internal/enforcement`
  (stdlib + `internal/shell` only), implements the pure decision logic
  (`IsWhitelisted`, `EvaluateFileTool`, `EvaluateBash`); `internal/cli/hook.go`
  does the I/O wiring and injects an in-process `OwnershipFunc` closure over
  `resolvePathOwnership` (SPEC-068) — no subprocess spawn to `mneme hook
  tokenize` or `mneme hook path-owned` remains. `ClaudeCode().DelegationHook`
  and `ProjectDelegationHookPatches` now register the portable
  `mneme hook enforce-delegation` command string instead of the absolute
  script path; `mneme install claude-code` and `mneme delegation-hook enable`
  both strip any pre-existing legacy `enforce_delegation.sh` entry before
  adding the new one, so upgrading or re-enabling migrates cleanly with no
  duplicates. The embedded `enforce_delegation.sh` asset is now a ~6-line
  compat shim (`exec mneme hook enforce-delegation`) kept only so a
  pre-existing absolute-path registration keeps working until it is
  re-registered — it is never written by new installs. See
  `docs/HOOKS.md` and `docs/enforcement-model.md`.

## [v1.20.0] — 2026-07-09 — SDD executor resolution + manifest-aware delegation hook (SPEC-068)

### Added

- **SDD executor resolution + manifest-aware delegation hook (SPEC-068)**:
  `spec_advance` now returns `{spec, executor}` — `executor` is an advisory
  `ResolveStageExecutor` recommendation (delegate to a manifest subagent, or
  let the orchestrator supply the stage as a conscious fallback) for the
  stage the spec just entered. `mneme hook path-owned <path>` is a new
  subcommand `enforce_delegation.sh` invokes instead of blocking directly for
  every non-whitelisted write target: it blocks only when an implementer
  subagent's manifest `areas` own the path, allows an unowned path through as
  a legitimate fallback, and keeps today's deny-by-default behaviour when no
  manifest exists yet (protects projects mid-migration to per-project
  subagents). This closes the gap SS-7 (retiring the six global subagents)
  would otherwise open for projects that never ran the `mneme-init` grill.
  See `docs/enforcement-model.md` and `docs/HOOKS.md`.

## [v1.19.0] — 2026-07-09 — Fix: `mneme upgrade` provisions incoming-version assets (SPEC-067)

### Fixed

- **`mneme upgrade` now provisions agents with the INCOMING version's assets**
  (SPEC-067): `postUpgradeHooks` re-executes the newly-written binary as a
  subprocess (`mneme install <slug>`) instead of calling `install.Install`
  in-process. Previously the in-process call ran with the code and embedded
  assets of the OUTGOING binary (the running process can't reload code from
  the file it just overwrote) — so an upgrade to v1.18.0 kept reinstalling
  the old `/mneme-init` slash command and never installed the new
  `mneme-init` skill. Non-fatal: a subprocess failure warns and suggests a
  manual `mneme install <slug>`, timeout 120s; `mneme upgrade` always
  finishes with "Done." regardless of the outcome.

### Added / Changed

- **`/mneme-init` restored as a thin slash-command wrapper** around the
  `mneme-init` skill (Claude Code): invoking it now simply instructs the
  agent to run the skill, instead of duplicating the old 5-phase logic.
  Codex continues to use the skill directly (no slash command).

### Upgrade note (IMPORTANT)

This fix only protects upgrades **from v1.19.0 onward**. An installation
already affected by the v1.18.0 bug must be repaired manually, once:
`mneme install claude-code` + `rm ~/.claude/commands/mneme-init.md` +
restart Claude Code.

## [v1.18.0] — 2026-07-08 — Per-project subagents + git-native team memory

### Added

- **Per-project subagents (EPIC SPEC-052, SS-1..SS-6)**: subagent profiles are
  now generated **per project** through a grill run by the `mneme-init` skill,
  instead of being 6 global profiles hard-wired to a single stack. The 6
  global profiles installed by `mneme install claude-code` **remain in place
  during this release as a transition path** — retiring them is a follow-up
  (SS-7, not part of this release).

  - **`internal/managedblock` + `internal/frontmatter` leaves (SS-1 /
    SPEC-054)**: the marker-fenced idempotent block-upsert primitive and the
    agent-frontmatter editor, previously private to `internal/install`, are
    now standalone leaf packages so the new subagent-generation code can
    reuse them without a dependency on `install`.
  - **`internal/subagents` leaf (SS-2 / SPEC-055)**: `StackFingerprinter`
    (project-root/apps/stack detection), a Go-authored `PermissionTable`
    (role → tool allowlist — never LLM-generated), `ProfileComposer` +
    `Validator` + `ParseGenerated`, and a `GenerationEngine` interface with
    two implementations — `PassthroughEngine` (the skill's own LLM drafts the
    profile body, no subprocess) and `CLIEngine` (subprocess `claude -p` /
    `codex exec`, with an anti-injection prompt envelope).
  - **`SubagentService` + persistence (SS-3 / SPEC-056)**: orchestrates
    `internal/subagents` on top of typed memories — no new store or
    migration. A project's subagent profile and its manifest of generated
    files are persisted as regular memories (`project-profile/*`,
    `subagents/manifest`), so they're queryable and self-healing like any
    other mneme memory.
  - **6 `subagent_*` MCP tools (SS-4 / SPEC-057)**: `subagent_fingerprint`,
    `subagent_profile_get`, `subagent_profile_save`, `subagent_compose`,
    `subagent_write`, `subagent_manifest_list`. `subagent_write` validates the
    composed profile's `tools:`/`permissionMode` against the Go-authored
    `PermissionTable` for the declared archetype **before** writing anything
    to disk — a profile can never be written with more permissions than its
    role allows, even from adversarial input (hardened against path
    traversal and frontmatter injection during QA).
  - **`mneme-init` becomes an opt-in orchestrator skill (SS-5 / SPEC-058,
    SS-5b / SPEC-059)**: the former `/mneme-init` slash command is retired in
    favour of a skill that runs the core managed-blocks + memory-seeding step
    always, then offers three independent opt-in steps: the subagent grill,
    codegraph indexing, and shared team memory.
  - **`mneme install` split + project-scoped delegation hook (SS-6 /
    SPEC-060)**: `mneme subagents fingerprint|profile|compose|write|
    manifest-list` CLI commands (mirroring the MCP tools, with `--engine
    claude|codex` for `CLIEngine`); the delegation-enforcement hook is now
    **opt-in per project** via `mneme delegation-hook enable|disable|status`,
    tied to whether the project actually has implementer subagents — a
    project with no generated subagents runs single-agent (no hook, no
    edit-blocking), matching the existing Codex precedent.

  Surface: +6 `subagent_*` MCP tools; new CLI commands `mneme subagents`
  (`fingerprint`, `profile get/save`, `compose`, `write`, `manifest-list`) and
  `mneme delegation-hook` (`enable`/`disable`/`status`).

- **Team Memory — git-native shared knowledge (EPIC SPEC-053, SS-A..SS-F)**:
  durable memories (decisions, conventions, architecture, patterns, bugfixes,
  rules) can now flow between teammates through the repository itself, with
  no server, no account, and no network call.

  - **Model + migration (SS-A / SPEC-061)**: `shared` (0/1/2) and `author`
    columns on `memories` (migration 014), layered on `scope=project` rather
    than introducing a new scope. Inert by default — nothing changes for an
    existing project until team-memory is explicitly enabled.
  - **Write-through materialization (SS-B / SPEC-062)**: `service.Save`/
    `Update` synchronously materialize shared memories to
    `.mneme/shared/notes/<uuid>.md` when the current repository has opted
    in — best-effort, never fails the save. Corrects an earlier assumption
    that a filesystem watcher existed between the vault and SQLite; it does
    not, so write-through lives directly in the save/update path.
  - **`mneme promote <id>` / `mem_promote` (SS-C / SPEC-063)**: explicitly
    marks any memory as team-curated (`shared=2`) regardless of type and
    materializes it immediately when team-memory is active.
  - **Import hooks + conflict detection (SS-D / SPEC-064)**: `mneme
    team-memory hooks install|remove|run-import` — idempotent
    `post-merge`/`post-checkout` git hooks (mirroring the codegraph-hooks
    pattern) that import `.mneme/shared/` in the background after every
    pull/checkout, merge-by-`updated_at`, and report deterministic FTS5
    conflict-candidate counts (judgment remains a separate, manual `mneme
    conflicts scan` step).
  - **`mneme team-memory enable` + docs (SS-F / SPEC-065)**: the single
    opt-in activation command — creates `.mneme/shared/` + its marker,
    bakes/exports pre-existing durable memories, installs the import hooks,
    and always prints an explicit privacy notice (team-memory is
    offline/git-native, so remote visibility can never be determined without
    a network call mneme deliberately never makes). The `mneme-init` skill's
    shared-memory step now invokes this command instead of the SPEC-053
    placeholder. See `docs/team-memory.md`.

  MCP surface grows to 64 tools (`mem_promote` joins `mem_*`; `subagent_*`
  from the parallel agnostic-agents EPIC also now counted). CLI grows to 36
  top-level commands (`mneme team-memory`, `mneme promote`).

## [v1.17.0] — 2026-06-25 — Codex CLI support: `mneme install codex` (SPEC-049)

### Added

- **`mneme install codex` — OpenAI Codex CLI support (single-agent model)**
  (SPEC-049): configura Codex CLI para usar mneme como memoria persistente y
  motor SDD. A diferencia de `claude-code` (multi-agente con delegación), Codex
  usa un modelo single-agent: un solo agente lee memoria, sigue el ciclo SDD e
  implementa — sin subagentes, sin hook de delegación, sin bloqueo de edición.

  Artefactos que instala:
  - `~/.codex/config.toml`: `[mcp_servers.mneme]` (`command` + `args=["mcp","--tools=agent"]`) y `"CLAUDE.md"` en `project_doc_fallback_filenames` (merge TOML no destructivo, idempotente).
  - `~/.codex/hooks.json`: `SessionStart`→`mneme hook session-start`, `Stop`→`mneme hook session-end` (schema Codex, idempotente).
  - `~/.codex/AGENTS.md`: operating manual single-agent como managed block (< 32 KiB).
  - `$HOME/.agents/skills`: skills bundled para descubrimiento por la CLI.

  Reusa el runner de install agnóstico (`installSteps`/`DryRun`/`upsertManagedBlock`/`WriteSkills`); el path de `claude-code` no cambia de comportamiento. Las MCP tools `skills_*` siguen gobernando `~/.claude/skills`. Ver `docs/codex.md`.

## [v1.16.0] — 2026-06-25 — CodeGraph C11: TS cross-file call resolution

### Fixed

- **CodeGraph C11: TS extractor now emits cross-file refs for imported-symbol
  calls** (SPEC-048): `internal/codegraph/js/extract.js` — fixes a root-cause
  bug where calls to imported bindings (named, default, namespace) resolved to
  the local **import node** instead of emitting an `unresolved_ref` for the
  import-guided resolver (`resolveTSImport`, SPEC-047). The result was that
  ~88% of TS `calls` edges pointed to dead-end import nodes, making
  `codegraph_callers`, `codegraph_callees`, and `codegraph_impact` return empty
  results for cross-file TypeScript calls.

  **Root cause:** import bindings were registered in the same `topLevel` map as
  local declarations. `walkCalls` found them there and emitted
  `calls → import_node` instead of leaving them for the resolver.

  **Fix:** a pre-scan of `ImportDeclaration` nodes (before `visit()` runs)
  builds `importedBindings` — a `Set` of every local binding name — covering the
  hoisting case (import declared after its use in a class method body).
  `walkCalls` now checks the call head against this set: if matched, it emits an
  `unresolved_ref` with `reference_name = callName` (`"member"` for
  named/default, `"ns.member"` for namespace). Import nodes and their
  `file → import` edges are preserved — the resolver needs them.

  **Measured recall improvement** (re-index with `--force` to apply):

  | Repo | Useful TS call edges (target = function/method) |
  |------|------------------------------------------------|
  | quantium (before) | 11 / 198 = **5.6%** |
  | quantium (after) | 69 / 198 = **34.8%** |
  | site (before) | 0 / 35 = **0%** |
  | site (after) | 19 / 35 = **54.3%** |

  Precision spot-check: 30/30 cross-file TS edges verified correct (100%).

  **Known ceiling (out of scope):** method-calls on typed variables
  (`payload.find(...)`, `res.docs.map(...)`) require type inference; re-exports
  and barrel files (`export { X } from './y'`) are not followed; bare npm
  imports (`react`, `next`) stay unresolved without error. These are tracked as
  future backlog items.

## [v1.15.0] — 2026-06-24 — CodeGraph C3: import-guided cross-package resolution (Go) + tsconfig paths infrastructure

### Added

- **CodeGraph C3: import-guided cross-package resolver** (SPEC-047):
  `internal/codegraph/resolver.go` — adds a new Tier 2 (T2) to the four-tier
  resolution strategy that uses each file's import declarations to resolve
  `pkg.Func()` cross-package calls to the correct node.

  **Design highlights:**
  - `Resolver.buildFileImports()` reads all `kind=import` nodes once per
    `Resolve()` call and builds a `fileImports` index
    (`filePath → (binding → importPath/moduleSource)`), eliminating per-ref
    DB queries.
  - **Go**: splits `"pkg.Func"` into qualifier + symbol; looks up qualifier in
    `fileImports[ref.FilePath]` to get the import path; finds candidate nodes
    via `FindNodesByNameInDir` (directory suffix-match against the import path).
  - **TS/JS**: supports namespace imports (`"ns.member"`), named/default imports
    (`"member"`), and relative module specifiers; probes `.ts`, `.tsx`, `.js`,
    `.jsx`, and `index.*` extensions; bare npm imports are silently skipped.
  - **Candidato-único-o-nada**: links only when `len(candidates) == 1`.
    Zero or two-or-more candidates → left unresolved (no false positives).
  - **Provenance**: import-guided edges carry `Provenance="import"` (distinct
    from `"ast"` and `"resolver"`) for future confidence scoring.

  **Schema change** (`internal/codegraph/schema.sql` + `db.go`):
  New nullable column `import_alias` on the `nodes` table records the local
  binding name for import nodes (alias, last segment, `.`, or `_`).
  `OpenDB` runs an idempotent `ALTER TABLE` via `applyAlterIdempotent` (uses
  `PRAGMA table_info`) so existing databases are upgraded transparently on the
  next binary run — no re-index required for the schema migration itself.

  **Extractor change** (`internal/codegraph/extractor_go.go`):
  `extractImportSpec` now captures the local binding in `Node.ImportAlias`:
  explicit alias → verbatim; no alias → last path segment; dot/blank imports
  stored as `"."` / `"_"` and skipped by the resolver.

  **New store methods** (`internal/codegraph/store.go`):
  `ListImportNodes()`, `FindNodesByNameInDir(name, dirSuffix)`,
  `FindNodesByNameInFile(name, filePath)`, `FindNodesByName(name)`,
  `FindNodesBySuffix(suffix)`, `HasNodesForLanguage(language)`.

  **Tests** (AC4–AC8): `TestResolver_ImportGuidedGoUnique`,
  `TestResolver_ImportGuidedGoAmbiguous`, `TestResolver_ImportGuidedGoAlias`,
  `TestResolver_ImportGuidedTSNamespace`, `TestResolver_ImportGuidedTSBareDoesNotBreak`,
  `TestStore_ImportAliasPersistence`, `TestStore_OpenDBIdempotent`.

  **Limitations (out of scope):** method calls on variables (`x.Foo()` where
  `x` is a local variable), re-exports, and external npm packages.
  Re-indexing is required to populate `import_alias` and generate
  `provenance="import"` edges in existing databases.

- **TypeScript tsconfig `paths` alias resolution infrastructure** (SPEC-047 ampliación):
  Implements the plumbing for resolving `@/*` and similar `compilerOptions.paths`
  aliases in TS import specifiers. This is infrastructure for future TS recall
  improvement — **the improvement in useful TS recall requires a separate rework
  of the TS extractor** (tracked in BL-052: the extractor currently links direct
  function calls to the import node in the same file rather than to the definition
  in the target file, so `unresolved_refs` does not contain the entries that
  tsconfig expansion would resolve). The infrastructure is correct and tested.

  **Implementation:**
  - `internal/codegraph/js/extract.js` — new `parseTsconfigs(rootDir)` function;
    subprocess discriminates on `msg.op === "tsconfig"` vs file-extraction
    messages (back-compat: `{path,content}` protocol unchanged). Uses
    `ts.parseJsonConfigFileContent`; skips only category-0 (fatal) errors —
    category-1 warnings ("No inputs found") do not block a valid tsconfig.
  - `internal/codegraph/extractor_ts.go` — `LoadTSConfigAliases(rootDir string)`
    method on `*TSExtractor` sends the control message and deserialises the result.
  - `internal/codegraph/tsconfig.go` — `TSAliasMap` type, `LoadTSAliases()`,
    `ResolveAlias(moduleSource, refFilePath string)`. In a monorepo with multiple
    tsconfigs, the entry whose `TsconfigDir` is the longest-path-match ancestor
    of the importing file is selected.
  - `internal/codegraph/resolver.go` — `Resolve(rootDir string)` (signature
    change from `Resolve()`); loads the alias map when `rootDir` is non-empty
    and TS nodes are present; `resolveTSImport` tries alias expansion before the
    bare-import gate. Fail-open: empty alias map → behaviour identical to before.

  **Tests:** `TestTSAliasMap_ResolveAlias_MonorepoPicksClosestTSConfig` (AC-T5,
  temp monorepo fixture, 2 tsconfigs, verifies ancestor selection per app) and
  `TestResolver_TSAliasEmptyMap_NoBreak` (AC-T6, empty rootDir, no panic).

### Fixed

- **T3 (suffix) and T4 (short-name) hardened to candidato-único-o-nada**
  (SPEC-047 precision fix): `FindNodesByName` and `FindNodesBySuffix` (plural,
  no LIMIT) added to the store; `resolveRef` updated so both tiers bind only
  when `len(candidates) == 1`. Previously LIMIT 1 caused ~1212 false-positive
  edges (e.g. `writeMemory→CodeGraphDB.Close`, `TestMemoryTypeValid→NodeKind.Valid`).
  Four new tests: `TestResolver_ShortNameAmbiguousStaysUnresolved`,
  `TestResolver_ShortNameUniqueResolves`, `TestResolver_SuffixAmbiguousStaysUnresolved`,
  `TestResolver_SuffixUniqueResolves`.

- **Indexer now persists unresolved refs** (SPEC-047 recall fix): `indexFile` in
  `internal/codegraph/indexer.go` was calling `BatchUpsertEdges` but discarding
  `extraction.UnresolvedRefs`. Added `BatchUpsertUnresolvedRefs` call so T2–T4
  actually run during the resolution pass.

**Recall measurement on mneme itself (Go, post all fixes):**
- Baseline (no resolver): ~43%
- After SPEC-047 (all tiers candidato-único-o-nada): **81.2%** (2335/2877 callables)
- Import-guided edges (T2, `provenance="import"`): **553** — spot-checked 15/15 correct
- False-positive edges eliminated: ~1212 (T3/T4 LIMIT 1) → 0 from that source
- Remaining unresolved: ~18 000 — stdlib/external packages and variable-receiver calls

## [v1.14.0] — 2026-06-24

### Added

- **Codegraph-first exploration policy in agent system prompts** (SPEC-045, C8):
  `internal/install/assets/agents/{architect,backend,frontend,qa-tester,bug-hunter,diagnostician}.md`
  — each of the 6 bundled agent assets now embeds a permanent
  `## Exploracion de codigo: grafo primero` section delimited by
  `<!-- mneme:codegraph-policy:start/end -->` markers. The section instructs
  agents to use `codegraph_*` tools as the first line for code exploration and
  to fall back to Read/Grep only when the graph does not cover the question,
  is stale, or the repo is not indexed.

  Two canonical variants:

  - **Without Bash clause** (`architect`, `qa-tester`): neither agent has `Bash`
    in its tool allowlist; the section states the codegraph-first policy and
    fallback rules without Bash-specific guidance.

  - **With Bash clause** (`backend`, `frontend`, `bug-hunter`): adds an explicit
    prohibition on using `Bash` (grep/cat/find/rg/head/tail) for code navigation
    — that need is covered by codegraph tools and native Read/Grep. Bash is
    reserved for build, test, git, and operational tasks.

  - **Diagnostician variant**: same Bash prohibition for code navigation, plus
    an explicit sentence preserving Bash for reading logs, infra, and operational
    diagnostics (consistent with the agent's own `## Permisos de Bash` section).

  Commits: c697d77, 61fc434, 6331d28.

  The policy takes effect after `mneme install claude-code` (no flags needed —
  `WriteAgents` always rewrites agent files from the embed) and a Claude Code
  restart. Relationship with the runtime nudge (C1/SPEC-044): these are
  complementary layers — the prompt policy is always present and imperative;
  the nudge fires once per session with real-time graph freshness data.

- **`TestAgentsCodegraphPolicy`** anti-drift test
  (`internal/install/assets_test.go`): verifies that all 6 agent assets contain
  the exact canonical policy block for their variant. Uses three authoritative
  constants (`codegraphPolicyNoBash`, `codegraphPolicyWithBash`,
  `codegraphPolicyDiagnostician`) as the source of truth. Follows the pattern of
  `TestAgentsMnemeAware`. Fails immediately on any textual drift.

### Fixed

- **Codegraph indexer no longer indexes generated/hidden directories** (SPEC-046, C10):
  `internal/codegraph/indexer.go` — the `WalkDir` branch for directories now skips
  any directory whose name starts with `"."` (hidden directories), in addition to
  the existing `ignoredDirs` map lookup. This fixes the root cause that allowed
  `.next`, `.turbo`, `.svelte-kit`, `.nuxt`, `.cache`, `.angular`, and similar
  toolchain directories to be recursed into and indexed, producing thousands of
  "errored" file records from transpiled/minified bundles. New explicit entries
  added to `ignoredDirs` for documentation and defense-in-depth: `.next`,
  `.turbo`, `.svelte-kit`, `.nuxt`, `.cache`, `coverage`. `dist` and `build`
  were already present. Obsolete nodes (e.g. from a previously indexed `.next`
  directory) are pruned from the store on the **next** `mneme codegraph index`
  run via `pruneDeleted` — no `--force` flag needed. Test coverage extended in
  `TestIndexer_RespectsIgnoreDirs` to cover `.next`, `.turbo`, `coverage`, and
  an arbitrary hidden directory (`.foo`).

- **`pruneDeleted` now removes orphan nodes from now-ignored directories** (SPEC-046, bug fix):
  `pruneDeleted` previously only iterated the `files` table, so nodes whose
  `file_path` had no corresponding `files`-table entry were never cleaned up.
  This occurred when (1) a directory was added to `ignoredDirs` after an initial
  index — a prior `pruneDeleted` pass had removed the `files` entry but the
  associated nodes were left behind as permanent orphans — or (2) `indexFile`
  wrote nodes successfully but `UpsertFile` failed, leaving nodes with no anchor.
  A new second pass iterates the distinct `file_path` values in the `nodes` table
  (via `Store.ListDistinctNodeFilePaths`) and deletes nodes for any path absent
  from both `onDisk` and the `files` table. New test:
  `TestIndexer_PrunesNodesFromNowIgnoredDir`. Smoke on migratio: `.next` and
  `.claude/worktrees` orphan nodes dropped from 79 762 and 38 557 to **0** on
  the next `mneme codegraph index` run.

### Changed

- **Agent codegraph policy now warns that `codegraph_impact` / `codegraph_callees`
  may be incomplete** (SPEC-046, C10): all 6 agent assets and the 3 canonical
  test constants in `internal/install/assets_test.go` include a new
  "Aviso de cobertura" paragraph (identical across variants). The notice explains
  that the call-edge graph captures ~43% of callable relationships in practice —
  method-calls (`x.Foo()`) and cross-package/stdlib calls are often not recorded.
  `codegraph_search`, `codegraph_context`, and `codegraph_callers` remain
  reliable for symbol location. The imperative to prefer codegraph tools over
  Read/Grep for code exploration is unchanged. `docs/codegraph.md` updated with a
  matching recall-limitation note near the MCP tools table.

### Docs

- `docs/codegraph.md`: new "Adoption by prompt (C8) — SPEC-045" section
  documenting the policy block, two canonical variants, the anti-drift test,
  deployment procedure, and the complementary relationship with the C1 runtime
  nudge. Recall-limitation note added for `codegraph_impact`/`codegraph_callees`
  (SPEC-046).

## [v1.13.0] — 2026-06-12

### Added

- **Adoption nudge** (SPEC-044, C1): `internal/cli/hook.go` — `maybeEmitCodegraphNudge()`
  is called inside `runHookPreToolUse` before the mutating-tools early-return, so it
  fires for `Read`, `Grep`, and `Glob` invocations. When the project has an indexed code
  graph with at least one node, the hook writes a context-only markdown block
  (`<!-- mneme:codegraph-nudge:start/end -->`) to stdout recommending the
  `codegraph_*` MCP tools over token-heavy re-reads. The nudge is **never blocking**
  (exit 0 always, no new non-zero exit path).

  Suppression and anti-noise:

  - **Per-session deduplication** via `session_id`: the statefile key `sid:<session_id>`
    is written to `~/.mneme/codegraph-nudge-state.json` after the first injection;
    subsequent invocations in the same session skip the nudge without any I/O to the
    code graph DB.
  - **Project-keyed TTL fallback** (`proj:<slug>`, 4 h TTL via `nudgeTTL4h`) when no
    `session_id` is present in the hook payload.
  - **Empty-graph guard**: `codegraph.ProbeGraph` returns `hasNodes=false` → no nudge.
  - **Staleness notice**: when `MAX(nodes.updated_at)` is more than 24 h ago
    (`nudgeStalenessThreshold`), a one-line note is appended to the nudge block
    recommending `mneme codegraph index` to refresh, with the approximate elapsed hours.
  - **Opt-out**: set `hook_nudge_enabled = false` under `[codegraph]` in `config.toml`,
    or set the environment variable `MNEME_CODEGRAPH_HOOK_NUDGE=false` (or `0`); env
    wins over TOML. Default is `true`.
  - **Anti-loop**: if the path being read is inside `cfg.Storage.DataDir` (mneme's own
    data directory) the nudge is skipped, preventing feedback loops when the agent
    reads mneme's own statefiles or DB files.

- **`mneme codegraph hooks install|remove`** (SPEC-044, C2):
  `internal/cli/codegraph_hooks.go` — new `codegraph hooks` subcommand with three
  children.

  - **`install`**: appends the mneme-managed re-index block (delimited by
    `# >>> mneme codegraph (SPEC-044) >>>` / `# <<< mneme codegraph (SPEC-044) <<<`
    markers) to the `post-commit` and `post-checkout` git hooks of the current
    repository. Creates the hook file with a `#!/bin/sh` shebang and `0755`
    permissions when absent. Idempotent: running `install` a second time is a no-op
    (the block is not duplicated). Hook location is resolved via
    `git rev-parse --git-path hooks` so `core.hooksPath` overrides and worktrees are
    respected. Any pre-existing hook content is preserved.

  - **`remove`**: strips only the mneme-managed block (from begin-marker to
    end-marker inclusive) from `post-commit` and `post-checkout`. All other hook
    content is untouched. Exits successfully when no block is present.

  - **`run-reindex`** (hidden): invoked by the installed git hooks; runs an
    incremental re-index (`Force=false`) in the foreground but is always launched
    detached with `&` from the hook script, so the triggering git operation is never
    blocked. Always exits 0 — index errors are appended to
    `~/.mneme/codegraph-hooks.log` with a timestamp and repo path, and are not
    propagated. The command skips the re-index when any of the following git
    in-progress state files are detected inside the git directory: `rebase-merge`,
    `rebase-apply`, `MERGE_HEAD`, `CHERRY_PICK_HEAD` (interactive/non-interactive
    rebase, merge, and cherry-pick), preventing storms of redundant runs.

- **`codegraph.ProbeGraph`** (SPEC-044): `internal/codegraph/probe.go` — read-only
  probe that opens the code graph SQLite DB at a given path, executes
  `SELECT 1 FROM nodes LIMIT 1` and `SELECT MAX(updated_at) FROM nodes`, and returns
  `(hasNodes bool, lastUpdatedUnixMs int64, err error)`. Opens the file with
  `mode=ro` and closes it immediately. Returns `hasNodes=false, err=nil` when the
  file is absent (fail-open). Used by the nudge hook to avoid emitting a reminder
  when the graph is empty.

### Docs

- `docs/codegraph.md` (new): full reference for the code graph subsystem. Covers
  quick-start, storage layout, indexing, the adoption nudge (C1) — what it is, when
  it fires, once-per-session anti-noise, empty/stale graph behaviour, opt-out, and
  anti-loop. Auto-reindex git hooks (C2) — install/remove, block format with markers,
  skip-during-rebase/merge/cherry-pick, failure logging, idempotence. MCP tools
  table, CLI subcommands table, config reference.
- `CLAUDE.md`: `mneme codegraph hooks install|remove` added to the CLI description;
  `[codegraph]` config section and pointer to `docs/codegraph.md` added under the
  pre-tool-use hook section.

### Upgrade path

Run `mneme codegraph hooks install` inside each repository where you want the code
graph to stay fresh automatically. To enable the adoption nudge, ensure
`hook_nudge_enabled` is `true` (the default) in `~/.mneme/config.toml` and that the
project has been indexed at least once (`mneme codegraph index`).

## [v1.12.0] — 2026-06-12

### Added

- **Role-aware enforcement** (SPEC-043): `internal/rules/match.go` — the Go match
  engine now receives a typed `Invocation` struct with a `Caller Caller` field
  (type `Caller`, constants `CallerOrchestrator` / `CallerSubagent`) derived from
  the hook payload.

  - **`agent:` selector** in `applies_to` patterns: rules can now target
    `agent:orchestrator`, `agent:subagent`, or `agent:*` (wildcard). Selectors
    are combinable with `+` in multi-part entries alongside tool selectors and
    path globs (e.g. `tool:Edit+agent:orchestrator+internal/**`).

  - **Block→warn degradation for subagents**: a `block`-severity rule without an
    `agent:` selector continues to block the orchestrator but is degraded to
    `warn` for subagents. The degradation is visible: the rendered output tags the
    rule as `[WARN — degraded from BLOCK for subagent]` and appends an
    informational note so the subagent has full context; legitimate implementer
    work is not blocked.

  - **Multi-key `agent_id` resolution** (5 routes): `internal/cli/hook.go` resolves
    caller identity from `.agent_id`, `.session.agent_id`, `.subagent.agent_id`,
    `.context.agent_id`, and `.metadata.agent_id`. Empty string and `null` are
    treated as orchestrator. Aligns `hook.go` resolution with the bash-layer logic
    introduced in v1.11.0.

  - **`NotebookEdit` support**: added to `mutatingTools` in `hook.go` with
    `file_path` as the primary path key and a `notebook_path` fallback, consistent
    with how `Edit`/`Write` extract their target paths.

  - **Validation** (`internal/rules/validate.go`): `agent:` selector values are
    validated at rule-save time. Empty agent names (`agent:`) and multi-part
    entries with duplicate selector kinds (two `tool:` parts or two `agent:`
    parts) are rejected with a descriptive error. Unknown agent names such as
    `agent:backend` are accepted without error for forward-compatibility — the
    matching engine returns no-match for unrecognised agent types.

### Fixed

- **Asset sync** (SPEC-043): `internal/install/assets/hooks/enforce_delegation.sh`
  is now byte-identical to the copy installed at `~/.claude/hooks/`. Four fixes
  backported:

  - **Strip redirect operators** from path candidates: the legacy bash path
    extractor (`command_mentions_protected_path`) now strips leading redirect
    operator prefixes (`>`, `>>`, `2>`, `&>`, etc.) from each candidate before
    whitelist matching, eliminating false positives for python/node inline
    commands whose redirects (e.g. `2>/dev/null`) were not being recognised as
    `/dev/` paths because the operator prefix remained attached.

  - **Tilde expansion**: `~/` prefixes are expanded to `$HOME/` before whitelist
    matching so `~/.mneme/**` and `~/.claude/**` resolve correctly regardless of
    the shell context.

  - **`/tmp` and `/private/tmp` scratch space**: both paths are added to the
    whitelist as legitimate scratch space, unblocking common tool patterns that
    write temporary files (e.g. `mktemp`, `go test -coverprofile`).

  - **Process substitution exclusion**: `<(...)` and `>(...)` command-substitution
    tokens are excluded from path candidate extraction, preventing false positives
    from process-substitution syntax.

  Regression battery extended from 17 to 34 cases
  (`internal/install/install_test.go`, `enforce_delegation_test.sh`).

### Docs

- `docs/RULES.md`: documents `agent:` selector syntax, multi-part combinability,
  and degradation semantics.
- `docs/HOOKS.md`: documents `tool:Bash` as context-only (reads are allowed;
  write detection is heuristic), `/tmp` as legitimate scratch, and the
  block→warn degradation behaviour for subagents.
- `docs/enforcement-model.md`: updated allowlist table and agent identity
  resolution to reflect the five `agent_id` lookup keys and the new
  `agent:` selector capability.

### Upgrade path

Run `mneme install claude-code` (or `--reinstall-hooks`) and restart Claude Code
to deploy the updated `enforce_delegation.sh` and pick up the role-aware match
engine.

## [v1.11.0] — 2026-05-30

### Changed

- **Hook hardening** (SPEC-042): `enforce_delegation.sh` — three targeted improvements
  to the orchestrator-guard bash hook.

  - **D1 — Robust subagent detection**: multi-key `agent_id` resolution replaces
    the single `.agent_id` read. The hook now checks
    `[.agent_id, .session.agent_id, .subagent.agent_id, .context.agent_id,
    .metadata.agent_id]` and treats empty string and `null` as orchestrator
    (previously `""` was incorrectly accepted as a subagent, creating a bypass).

  - **D2 — Noisy jq guard**: when `jq` is absent from PATH the hook now emits a
    visible WARNING to stderr before exiting 0 (fail-open). Previously the
    fail-open was completely silent, hiding the fact that enforcement was disabled.

  - **D3 — Hardened python/node detection**: new `command_mentions_protected_path`
    helper blocks inline python/node commands that reference any path outside the
    whitelist. This closes `shutil.copy`, `subprocess.run(['cp',...])`,
    `os.rename`/`os.replace`, `node child_process`, and other indirect write APIs
    that previously bypassed the API-name heuristic.
    `python -c 'print(2+2)'` (no paths) continues to be allowed.

  - **D4 — Layer-2 scope documented**: script header, `docs/enforcement-model.md`,
    and `docs/HOOKS.md` now explicitly describe inherent limits (base64/eval
    indirection, arbitrary binaries, unlisted interpreters) as out-of-scope by
    design. Primary defense for subagent boundaries remains Layer 1 (capability
    `tools:` allowlist in `agents/*.md`).

### Tests

- `TestDelegationHookContent_ValidBash` extended with D1/D2/D3 marker assertions
  (`session.agent_id`, `command -v jq`, `command_mentions_protected_path`).
- New `TestDelegationHook_SmokeTests`: table-driven Go harness that writes the
  embedded hook to a temp file and invokes it via `exec.Command("bash", ...)`.
  Covers AC1 (5 B-cases), AC2 (3 A-bypass cases), AC3 (6 non-regression cases).
  Skips gracefully when `bash` or `jq` are absent from PATH.
- New `internal/install/assets/hooks/enforce_delegation_test.sh`: standalone bash
  smoke runner for local manual verification.

### Upgrade path

Run `mneme install claude-code` (or `--reinstall-hooks`) and restart Claude Code
to deploy the updated hook to `~/.claude/hooks/enforce_delegation.sh`.

## [v1.10.0] — 2026-05-29

### Added

- **Process/Architecture split** (SPEC-041): mneme now separates process instructions
  (owned globally by mneme) from project architecture docs (owned by each project).

  - **Managed-block primitive** (`internal/install/managedblock.go`): `upsertManagedBlock`
    and `readManagedBlock` replace the old `InjectProtocol`/`mergeProtocol` pair. Single
    versioned block `<!-- mneme:managed:start v=N -->` … `<!-- mneme:managed:end -->`.
    Idempotent by construction; detects and removes legacy `mneme:protocol` markers as a
    one-time migration.

  - **Operating manual** (`internal/install/assets/operating-manual.md`): lean 7-section
    global manual (roles, delegation triggers, SDD+lanes, skills, models, memory) embedded
    at build time. Replaces the old `protocol()` function. `mneme install claude-code` now
    runs an "Operating manual" step instead of "Protocol".

  - **`diagnostician` agent** (`internal/install/assets/agents/diagnostician.md`): new
    bundled agent for ops/diagnostics. Tools: Read, Grep, Glob, BashOutput, Bash,
    mcp__mneme__* — Bash for log reading; no Edit/Write/MultiEdit. Model: sonnet.
    6 bundled agents total (was 5).

  - **Drift detection** (`internal/service/drift.go`): `DetectDrift` scans a project's
    `CLAUDE.md` (outside the managed block) and reports two advisory categories:
    (a) headings duplicating global manual sections; (b) phrases contradicting the
    enforcement model. Deterministic, no LLM, exit 0 always.

  - **Extended `mneme init`**: default mode now applies managed blocks (global manual +
    repo block) + prints drift report + shows legacy migration plan in dry-run. New
    `--check` flag for report-only mode. `--apply` remains the gate for the destructive
    legacy migration.

  - **MCP `init` tool** (57th tool): applies managed blocks and runs drift detection.
    `check=true` for report-only. Destructive migration remains CLI-only.

  - **`model.ErrNotARepo`** sentinel for init operations outside a git repo.

### Changed

- `Agent.Protocol` field replaced by `Agent.Manual` in `internal/install`. All callers
  updated. `InjectProtocol`/`mergeProtocol` removed; use `InjectManual`/`upsertManagedBlock`.
- `service.NewInitService` now accepts `InitServiceOptions` for injectable UpsertBlock and
  ManualContent dependencies (no breaking change for zero-value callers).
- `docs/enforcement-model.md`: diagnostician added to the agent allowlist table.

## [v1.9.1] — 2026-05-29

### Fixed

- **P1 — tokenizer fd-dup false positive** (`internal/shell/tokenize.go`): `2>&1`
  and `1>&2` (fd-dup redirects) no longer emit a `TypeRedirectTarget` token for the
  numeric file-descriptor word. Enforcement hooks (`mneme hook pre-tool-use`,
  `enforce_delegation.sh`) were treating the digit `"1"` as a protected path,
  producing a spurious "Redirect a ruta protegida: '1'" block for commands like
  `golangci-lint run 2>&1`. Non-numeric targets (e.g. `>&file`) still emit
  `TypeRedirectTarget` correctly. Adds helper `isAllDigits` and table-driven tests
  with exact token-stream assertions.

- **P2 — conflict link/unlink write to resolved store** (`internal/service/conflicts.go`,
  `internal/model/errors.go`): `ConflictLink`, `ConflictUnlink`, and `persistVerdict`
  previously discarded the store returned by `getFromEitherStore` and always wrote to
  `projectStore`. Global-global relation operations therefore silently failed with
  `ErrNotFound`. Fix: writes go to the resolved store. Cross-store pairs (one project,
  one global) now return the new sentinel `model.ErrCrossStoreRelation` before any
  write is attempted. No new migration needed — migration 013 already applies to every
  database, including `global.db`.

- **P3 — DryRun derived from installSteps** (`internal/install/install.go`,
  `internal/cli/install.go`): `DryRun` previously maintained a hardcoded list of
  steps that could silently diverge from `installSteps`. Signature changed to
  `DryRun(agent *Agent, opts InstallOptions)` and the implementation now enumerates
  `agent.installSteps(opts)`, printing `[would run] <step.Name>` for each step.
  CLI builds `opts` before the dry-run branch so both paths share the same value.

## [v1.9.0] — 2026-05-29

### Added

- **Memory Conflict Surfacing** (SPEC-039): two-phase workflow for detecting and
  managing conflicting memories. Closes "the agent followed a decision we already
  changed."

  - **Migration 013** (`internal/db/migrations/013_memory_relations.sql`): new
    `memory_relations` table for `conflicts_with` and `unrelated` edges with
    CHECK constraint, UNIQUE (from_id, to_id), and three indices. `supersedes`
    reuses the existing `memories.superseded_by` column.
  - **`internal/conflicts/`** — new leaf package (stdlib only, no internal deps):
    - `detect.go`: `ExtractSalientTerms` + `BuildCandidateQuery` — purely
      deterministic FTS5 term extraction.
    - `judge.go`: `JudgePair` — invokes `claude -p --output-format json` as a
      subprocess; parses outer/inner JSON; validates relation ∈
      {supersedes_a_over_b, supersedes_b_over_a, conflicts_with, unrelated};
      60s timeout. `NewJudgeConfig` → `ErrCLIUnavailable` when CLI absent.
  - **`internal/store/conflicts.go`**: `MemoryRelation`, `MemoryRelationListOptions`,
    `CreateMemoryRelation` (normalised pair, INSERT OR REPLACE),
    `DeleteMemoryRelation`, `ListMemoryRelations`, `GetMemoryConflicts` (symmetric),
    `IsJudged` (negative cache), `FTS5Candidates` (scoped, excludes self/judged).
    `ClearSupersededBy` added to `consolidation.go`.
  - **`internal/service/conflicts.go`**: `ConflictCandidates` (deterministic),
    `ConflictScan` (judge config guard, dedup pairs, `judgeOnePair`, dry-run
    default, `persistVerdict`), `ConflictLink` (ErrInvalidRelation/ErrNotFound),
    `ConflictUnlink` (clears superseded_by), `ConflictList`. Non-blocking
    `logConflictHint` goroutine added to `memory.go` Save().
  - **`model/search.go`**: `SearchResult.ConflictsWith []string` field. Post-ranking
    `annotateConflicts` pass in `service/search.go`.
  - **`model/errors.go`**: `ErrCLIUnavailable`, `ErrInvalidRelation`.
  - **MCP tools** (51→56): `conflicts_candidates`, `conflicts_scan`,
    `conflicts_link`, `conflicts_unlink`, `conflicts_list`. `conflicts_scan` CLI
    absent → `IsError:true` with `{error, suggestion}` payload.
  - **CLI**: `mneme conflicts` command group with candidates/scan/link/unlink/list
    subcommands. `mneme conflicts scan` with `--apply` flag.
  - **`docs/conflicts.md`**: full reference including architecture notes,
    relation model table, anti-scope.

## [v1.8.0] — 2026-05-29

### Added

- **Per-Agent Model Assignment** (SPEC-038): each bundled agent now has its model
  alias written into `~/.claude/agents/<agent>.md` at install time, driven by
  config overrides with built-in defaults.

  - **`internal/install/defaults.go`** — `defaultAgentModels` map, `knownAliases`
    set, and helpers `BundledAgentNames`, `DefaultModelFor`, `IsKnownAlias`,
    `ResolveEffectiveModels`. BundledAgentNames is the canonical source for agent
    names across service validation and MCP tools.
  - **`internal/install/frontmatter.go`** — `SetModelInFrontmatter`: surgical
    line-scanner editor that replaces exactly the `model:` line in YAML frontmatter
    without re-serializing any other field. I1-hardened (3-cycle round-trip, special
    chars in description, YAML comments, permissionMode all preserved verbatim).
  - **`ApplyAgentModels(agentsDir, overrides)`** in `internal/install/install.go`:
    resolves effective models and writes them to each installed agent file; skips
    agents not yet installed (graceful).
  - **`[models]` config section**: `ModelsConfig{Overrides map[string]string}` added
    to `internal/config/config.go`; `SetModelsOverrides` atomic write-back in
    `internal/config/write.go`. Config survives upgrade (not an asset).
  - **`internal/service/models.go`** — `ModelsService` with `List`, `Set`, `Reset`.
    `Set` rejects unknown agents (`ErrUnknownAgent`) and empty model (`ErrInvalidModel`);
    warns (does not error) on unknown alias.
  - **3 MCP tools** (48 → 51): `model_list`, `model_set`, `model_reset`. Mapped by
    `mapServiceError`: `ErrUnknownAgent` and `ErrInvalidModel` → `CodeInvalidParams`.
  - **`mneme model` CLI command group** (31 top-level commands): `list [--json]`,
    `set <agent> <model>`, `reset [<agent>]`. Filesystem-only (no DB connection).
  - **`docs/models.md`** — defaults rationale, config format, alias table.
  - **2 model sentinel errors**: `ErrUnknownAgent`, `ErrInvalidModel`.

### Changed

- **Install consolidation** (D5): `install.go` now exposes a single `installSteps(opts
  InstallOptions)` builder and `runInstallSteps` runner. Both `Install()` (upgrade
  path) and the CLI `mneme install claude-code` (RunE) consume the same step list.
  The step "Agent models" runs immediately after "Agent profiles" in every install.
- **CLI install behavior change**: `mneme install claude-code` now uses collect-all
  error semantics (was fail-fast). All steps are attempted; errors are printed as
  `[fail]` lines and returned combined. This is consistent with the upgrade path
  and improves partial installs.
- **Agent assets**: all bundled `*.md` files now use model aliases instead of pinned
  IDs (`opus`/`sonnet` instead of `claude-opus-4-6`/`claude-sonnet-4-6`). `qa-tester`
  changed from `opus` to `sonnet` (deliberate per §2 cost rationale).
- `NewServer` signature adds `modelsSvc *service.ModelsService` parameter (nil-safe).
- MCP tool count: 48 → 51.
- CLI top-level command count: 30 → 31 (added `model`).

## [v1.7.0] — 2026-05-29

### Added

- **Skills Framework** (SPEC-037): mneme is now the package manager for Claude
  Code skills. Skills live at `~/.claude/skills/<name>/` and are installed by
  `mneme install claude-code` alongside agents and hooks.

  - **`internal/skill/`** — leaf package (no internal deps): deterministic
    SKILL.md parser (`parse.go`), structural linter (`lint.go`), and validation
    runner (`validate.go`). No yaml dependency; manual frontmatter scanner.
  - **`example-skill` fixture** — the only bundled skill; structural fixture for
    testing the framework. Not architectural guidance.
  - **7 MCP tools** (41 → 48 total): `skills_list`, `skills_install`,
    `skills_pin`, `skills_unpin`, `skills_remove`, `skills_lint`,
    `skills_validate`. `skills_lint` and `skills_validate` use the
    `IsError:true`+payload pattern (mirrors `lane_audit`) so callers receive
    full finding lists on failure.
  - **`mneme skills` CLI command group** (7 subcommands) with `--json` and
    `--force` flags; exit 1 on lint/validate failures.
  - **Install machinery**: `WriteSkills` with pin-aware idempotency — bundled
    skills are authoritative for non-pinned installs; `pinned:true` in the
    installed `SKILL.md` blocks overwrite/remove without `--force`.
  - **4 model sentinel errors**: `ErrSkillNotFound`, `ErrSkillMalformed`,
    `ErrSkillPinned`, `ErrSkillNoValidation`. Mapped by `mapServiceError`.
  - **`docs/skills.md`** — authoring guide (skill directory contract, SKILL.md
    schema, validation script conventions, pinning, lifecycle).

### Changed

- MCP tool count: 41 → 48.
- CLI top-level command count: 23 → 25 (added `skills`, `codegraph` was
  already counted).
- `NewServer` signature adds `skillsSvc *service.SkillsService` parameter
  (nil-safe; existing callers pass nil).

## [v1.6.0] — 2026-05-29

### Added

- **`spec_reject`** / `mneme spec reject` (SPEC-036): rejects a spec from `qa`
  (standard lane) or `audit` (trivial lane) back to `implementing` with a
  documented reason. Models QA review that found defects. Distinct from
  `spec_pushback` (ambiguity → `needs_grill`). MCP tool count: 39 → 41.
- **`lane_stats`** / `mneme lane stats` (SPEC-036): reports trivial-lane compliance
  metrics — trivial count, audit-fail count and rate, override count, reclassify
  count. Scoped to the current project.
- **Base-SHA binding** (SPEC-036): when a spec enters `implementing` status, mneme
  captures the current HEAD commit SHA as `spec.base_sha`. `lane_audit` uses it
  as the diff base (precedence: explicit `--base` → `spec.base_sha` → merge-base).
  Prevents cross-spec diff contamination on multi-spec branches.
- **Structured lane audit records** (SPEC-036): every `lane_audit` run inserts a
  row in the new `lane_audits` table (migration 012). `lane_status` reads the
  latest row instead of parsing `spec_history` text.
- **`rejection_count`** in `lane_status` response (SPEC-036): derived from
  `spec_history` transitions `qa/audit → implementing`; no additional column.
- Migration 012 (`012_add_spec_base_sha_and_audits.sql`): adds `base_sha TEXT`
  column to `specs` and creates the `lane_audits` table.

### Changed

- `lane_status` now reads audit outcome from `lane_audits` table rather than
  parsing `spec_history` reason strings. Existing specs without audit rows
  report `latest_audit: null` (no crash).
- `lane_audit` base-ref precedence updated: `req.base_ref` → `spec.base_sha` →
  auditor's default `merge-base` logic.

### Removed

- The `spec_history` hack of writing same-status `audit → audit` entries to
  record audit failures has been removed. `lane_audits` table is now canonical.

## [v1.5.0] — 2026-05-28

### Added

- **Graduated lanes for SDD** (SPEC-035): every backlog item and spec now
  carries a `lane` field (`trivial` or `standard`) declared at creation time.
  - **Trivial lane**: shortened path `draft → rationale → implementing → audit → done`.
    Skips speccing, planning, and QA. Requires a `scope` glob declaring which
    files the change is allowed to touch.
  - **Standard lane**: full SDD flow unchanged. All existing items and flows
    continue to work as before with no required changes.
  - **Deterministic post-implementation auditor**: `lane_audit` / `mneme lane audit`
    checks the actual git diff against thresholds (≤3 files, ≤20 lines, no
    forbidden paths, scope compliance, no public Go/TS symbol changes). Pure Go,
    no LLM. Violations are saved as discovery memories.
  - **`spec_quick`** / `mneme spec quick`: advances a trivial spec from draft to
    implementing in one step with a rationale string.
  - **`lane_reclassify`** / `mneme lane reclassify`: reclassifies trivial→standard,
    moves spec to speccing.
  - **`lane_override`** / `mneme lane override`: bypasses a failed audit (requires
    documented reason, persisted as discovery memory).
  - **`lane_status`** / `mneme lane status`: shows lane, scope, and latest audit
    summary.
  - Migration `011_add_lane.sql`: adds `lane` and `scope` columns to
    `backlog_items` and `specs` with CHECK constraint and DEFAULT backfill.
  - `internal/lane/`: new leaf package with git-exec adapter (`GitDiffer`) and
    auditor (`Audit`) — no internal/model imports.
  - `docs/lanes.md`: full lanes reference (Critical Rules, Automated Checks,
    How to Fix).
  - `CLAUDE.md` Lanes section with orchestrator instructions.

## [v1.4.0] — 2026-05-29

### Added

- **Permission enforcement by capability** (SPEC-034): role boundaries are now
  enforced at the capability level for all five subagents, not only the
  orchestrator.
  - `architect.md` and `qa-tester.md`: replaced `permissionMode: bypassPermissions`
    with an explicit read-only `tools:` allowlist (`Read, Grep, Glob,
    NotebookRead, BashOutput, mcp__mneme__*`). These agents can no longer
    invoke `Edit`, `Write`, `MultiEdit`, `Bash`, or `NotebookEdit`.
  - `backend.md`, `frontend.md`, `bug-hunter.md`: added explicit
    `tools: Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit,
    Write, MultiEdit, Bash, mcp__mneme__*`. Retained
    `permissionMode: bypassPermissions` for autonomous runs with a comment
    documenting the intent.
- **Blocked-edit discovery logging** in `enforce_delegation.sh`: the bash
  `PreToolUse` hook now calls `mneme save --type discovery` on every
  orchestrator block, storing a structured markdown record with what/why/
  learned sections. The call is guarded by `command -v mneme` and uses
  `|| true` so failures never affect the exit-2 guarantee. Blocked attempts
  are queryable via `mneme search "Blocked edit"`.
- `docs/enforcement-model.md`: full reference for the two-layer enforcement
  model (capability allowlist + bash hook), including the allowlist table,
  automated checks, how to add a new subagent, and a debugging guide.
- `CLAUDE.md` (repo root): new "Enforcement Model" subsection under
  Architecture summarising the two layers and linking to
  `docs/enforcement-model.md`.

### Changed

- `internal/install/install_test.go`: three new marker-based tests
  (`TestDelegationHookContent_LogsBlockedAttempts`,
  `TestAgentAssets_ReadOnlyAllowlists`,
  `TestAgentAssets_ImplementerAllowlists`) covering the new enforcement
  constraints.

## [v1.3.0] — 2026-05-28

### Added

- Shell tokenizer (`mneme hook tokenize`) using `mvdan.cc/sh/v3/syntax` for
  robust `PreToolUse` hook parsing. Handles pipelines, compound commands,
  heredocs, and `bash -c` recursion (SPEC-033).

## [v1.2.0] — 2026-05-22

### Added

- `enforce_delegation.sh` embedded in the mneme binary and installed via
  `mneme install claude-code`. SHA-256 checksum verification with automatic
  backup on update (SPEC-032).

[v1.4.0]: https://github.com/wirvii/mneme/compare/v1.3.0...v1.4.0
[v1.3.0]: https://github.com/wirvii/mneme/compare/v1.2.0...v1.3.0
[v1.2.0]: https://github.com/wirvii/mneme/releases/tag/v1.2.0
