# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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
