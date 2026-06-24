# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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
  - **TS/JS**: supports namespace imports (`"ns.member"`), named/default
    imports (`"member"`), and relative module specifiers; probes `.ts`, `.tsx`,
    `.js`, `.jsx`, and `index.*` extensions; bare npm imports (external
    packages) are silently skipped — no node in repo → stays unresolved.
  - **Candidato-único-o-nada**: links only when `len(candidates) == 1`.
    Zero candidates → unresolved; two or more → unresolved (no false positives).
  - **Provenance**: import-guided edges carry `Provenance="import"` (distinct
    from `"ast"` and `"resolver"`) for future confidence scoring (BL-044).

  **Schema change** (`internal/codegraph/schema.sql` + `db.go`):
  New nullable column `import_alias` on the `nodes` table records the local
  binding name for import nodes (alias, last segment, `.`, or `_`).
  `OpenDB` runs an idempotent `ALTER TABLE` via `applyAlterIdempotent` (uses
  `PRAGMA table_info` to check existence before issuing the statement) so
  existing codegraph databases are upgraded transparently on the next binary
  run — no re-index required for the schema migration itself.

  **Extractor change** (`internal/codegraph/extractor_go.go`):
  `extractImportSpec` now captures the local binding in `Node.ImportAlias`:
  explicit alias → verbatim; no alias → last path segment; dot/blank imports
  stored as `"."` / `"_"` and skipped by the resolver as non-resolvable.

  **New store methods** (`internal/codegraph/store.go`):
  `ListImportNodes()`, `FindNodesByNameInDir(name, dirSuffix)`,
  `FindNodesByNameInFile(name, filePath)`.

  **Tests** (AC4–AC8): `TestResolver_ImportGuidedGoUnique`,
  `TestResolver_ImportGuidedGoAmbiguous`, `TestResolver_ImportGuidedGoAlias`,
  `TestResolver_ImportGuidedTSNamespace`, `TestResolver_ImportGuidedTSBareDoesNotBreak`,
  `TestStore_ImportAliasPersistence`, `TestStore_OpenDBIdempotent`.

  **Limitations (out of scope):** method calls on variables (`x.Foo()` where
  `x` is a local variable), `tsconfig` `paths` aliases, re-exports, and
  external npm packages. Re-indexing is required to populate `import_alias`
  and generate new `provenance="import"` edges in existing databases.

  Commits: 80e316e, dfa611e, ddf9330, 9b3b5fb, 7469b1e, a419ac6, 1397136.

  **Recall measurement on mneme itself (AC1):**
  - Baseline (no resolver at all): ~43% (1209/2841 per spec; this machine had empty codegraph DB)
  - After implementation: **82.4%** (2358/2863) — calls edges from T2(import)+T3+T4.
  - Import-guided edges (T2, provenance="import"): **569** edges, verified **100% correct** on 20-edge spot-check.
  - Remaining unresolved: 17,014 — primarily stdlib/external packages (fmt, os, cobra) and variable-receiver calls (x.Method() where x is a local variable — out of scope per spec).

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

[v1.4.0]: https://github.com/juanftp/mneme/compare/v1.3.0...v1.4.0
[v1.3.0]: https://github.com/juanftp/mneme/compare/v1.2.0...v1.3.0
[v1.2.0]: https://github.com/juanftp/mneme/releases/tag/v1.2.0
