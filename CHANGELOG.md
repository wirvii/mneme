# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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
