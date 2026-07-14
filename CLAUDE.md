# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build, test, run

mneme is a pure-Go build — no CGO, no C compiler, no build tags. SQLite (with FTS5 compiled in by default) is provided by `modernc.org/sqlite`, a pure-Go transpilation of the SQLite engine. `go install github.com/wirvii/mneme/cmd/mneme@latest` works standalone; the `Makefile` is the canonical entrypoint for local development:

```bash
make build         # go build -o mneme ./cmd/mneme
make test          # go test ./...
make test-race     # go test -race ./...
make install       # build + sudo cp to /usr/local/bin/
make setup         # install + `mneme install claude-code` (configures MCP, hooks, manual, skills)
make release-local # ldflags-stripped build with Version=local
```

mneme also runs on Windows (EPIC-windows, SPEC-074/075-080): `go install` is
the only supported install *and* upgrade path there (`mneme upgrade` shells
out to it internally, SPEC-076) — see the `Windows` section in `README.md`.
OS-specific branches use `runtime.GOOS` checked inline (e.g.
`internal/enforcement.PathContext.GOOS`, the `goos` parameter of
`internal/cli/upgrade.go:performUpgrade`) rather than `_windows.go` files or
build tags — the same pure-Go, no-build-tags posture as the rest of the
build.

Run a single package or test:

```bash
go test ./internal/store/...
go test -run TestSaveMemory ./internal/store
```

Linting must pass with **zero warnings**: `golangci-lint run`. `gofmt` and `goimports` are enforced. A `pre-push` hook lives at `.githooks/pre-push`.

Module path is `github.com/wirvii/mneme`; entrypoint is `cmd/mneme`.

## Architecture

mneme is a single-binary persistent memory system for AI agents. The same service layer is exposed through three frontends — keep them in sync when adding capabilities.

### Layered packages (`internal/`)

```
cli/  mcp/  http/        ← three frontends (Cobra, JSON-RPC stdio, REST)
       │
       ▼
   service/              ← business logic orchestration (the only layer the frontends call)
       │
       ▼
   store/                ← repository pattern: CRUD, FTS5 search, entities, stats
       │
       ▼
   db/                   ← SQLite + embedded migrations
       │
       ▼
   model/                ← domain types — zero external imports (the leaf)
```

**Dependency rule:** imports flow inward only. `model` has no external deps. Adapters (`store`, `mcp`, `http`, `cli`) sit at the edges and are swappable. Don't let frontends call `store` or `db` directly — go through `service`.

Supporting packages: `scoring/` (decay, BM25 re-ranking, RRF fusion), `consolidation/` (background decay/dedup/budget sweeps + edge decay), `graph/` (Hebbian auto-strengthening: AccessTracker ring buffer + HebbianWorkerPool async worker), `rules/` (applies_to pattern matching engine for pre-tool-use hook), `enforcement/` (leaf: pure orchestrator-guard decision logic for `mneme hook enforce-delegation` — stdlib + `internal/shell` only, no internal deps, SPEC-069), `embed/` (TF-IDF baseline), `sync/` (JSONL.gz git-shareable export/import), `project/` (git-remote slug detection), `config/` (TOML + env overrides), `install/` (agent installer: MCP/hooks/manual/commands/skills — no global agent profiles since SPEC-073), `skill/` (leaf: SKILL.md parser, structural linter, validate runner — no internal deps), `conflicts/` (leaf: deterministic FTS5 candidate extraction + LLM judgment via claude CLI subprocess — no internal deps), `tui/` (Bubble Tea), `upgrade/`, `export/`.

### The three frontends

- **MCP** (`internal/mcp`, primary) — JSON-RPC 2.0 over stdio, ProtocolVersion `2024-11-05`. Surface: 64 tools (15 `mem_*`, 4 `backlog_*`, 8 `spec_*`, 5 `lane_*`, 10 `codegraph_*`, 7 `skills_*`, 3 `model_*`, 5 `conflicts_*`, 6 `subagent_*`, 1 `init`). `mem_*`: mem_save, mem_search, mem_get, mem_context, mem_update, mem_session_end, mem_suggest_topic_key, mem_relate, mem_timeline, mem_stats, mem_checkpoint, mem_forget, mem_promote, mem_gaps, mem_explore. `spec_*`: spec_new, spec_status, spec_advance (returns `{spec, executor}` — SPEC-068 advisory `ResolveStageExecutor` recommendation for the stage just entered), spec_pushback, spec_resolve, spec_list, spec_quick, spec_reject. `lane_*`: lane_audit, lane_reclassify, lane_override, lane_status, lane_stats. `skills_*`: skills_list, skills_install, skills_pin, skills_unpin, skills_remove, skills_lint, skills_validate. `model_*`: model_list, model_set, model_reset. `conflicts_*`: conflicts_candidates, conflicts_scan, conflicts_link, conflicts_unlink, conflicts_list. `subagent_*`: subagent_fingerprint, subagent_profile_get, subagent_profile_save, subagent_compose, subagent_write, subagent_manifest_list. `init`: applies managed blocks + drift report (see `docs/init.md`). `handleMessage()` is exposed separately from `Run()` so unit tests can drive it without I/O loops.
- **HTTP** (`internal/http`, `mneme serve --addr :7437`) — stdlib `net/http`, graceful shutdown 10s, 8 endpoints under `/v1/`. Currently lacks SDD endpoints and a few mem tools (`mem_checkpoint`, `mem_timeline`, `mem_suggest_topic_key`); when adding service capabilities, decide explicitly whether HTTP gets parity.
- **CLI** (`internal/cli`, Cobra) — 36 top-level commands. Notable: `sync export|import|status` is the backup/restore path (no dedicated `restore` command); `mneme init` sets up managed blocks, reports drift, and (with `--apply`) migrates legacy projects to the SDD engine (see `docs/init.md`); `mneme install <agent>` configures MCP config, hooks, the operating manual, slash commands and skills (no global agent profiles since SPEC-073) — supported agents: `claude-code` (multi-agent, full delegation) and `codex` (single-agent, no delegation; see `docs/codex.md`); `mneme skills` manages skills in `~/.claude/skills/`; `mneme model` manages per-agent model assignments; `mneme conflicts` detects and manages memory conflict relations; `mneme subagents` composes/writes per-project subagent profiles; `mneme delegation-hook` toggles the project-scoped opt-in enforcement hook; `mneme codegraph hooks install|remove` installs/removes git hooks that auto-reindex the code graph after commits and checkouts (see `docs/codegraph.md`); `mneme team-memory enable` activates the git-native shared-knowledge vault (marker + bake/export existing durables + import hooks) and `mneme promote <id>` explicitly shares one memory regardless of type (see `docs/team-memory.md`).

### Persistence

Two SQLite databases per host:
- `~/.mneme/global.db` — global + org scope memories
- `~/.mneme/projects/<slug>.db` — project-scoped memories (slug derived from git remote)

Scopes (`global` / `org` / `project`) never leak between projects. Migrations are embedded via `embed.FS`.

### Pre-tool-use hook (important when editing source)

`mneme hook pre-tool-use` is the active Claude Code `PreToolUse` hook. It evaluates **rules** from the mneme database against every `Edit`/`Write`/`MultiEdit` call. Rules carry `applies_to` patterns (path globs, tool selectors, negations) and a `severity` level (`info`/`warn`/`block`). When a `block`-severity rule matches, the hook exits with code 2 and Claude Code rejects the tool call. Source: `internal/cli/hook.go:runHookPreToolUse` + `internal/rules/match.go`. If a code edit gets blocked, that's a rule — check `mneme rule list` and delegate or adjust accordingly.

`mneme hook enforce-delegation` (SPEC-069) is the orchestrator-guard (Layer 2): a portable Go subcommand (registered with no path to the home directory) that blocks the orchestrator from editing, or running Bash against, a path outside the static whitelist and owned by an implementer subagent (or legacy deny-by-default when no manifest exists). It replaced the embedded ~640-line `enforce_delegation.sh` bash script — the decision logic now lives in the leaf package `internal/enforcement` (stdlib + `internal/shell` only: `IsWhitelisted`, `EvaluateFileTool`, `EvaluateBash`), wired up by `internal/cli/hook.go:runHookEnforceDelegation`, which injects an in-process `OwnershipFunc` closure over `resolvePathOwnership` — no subprocess spawn. The embedded `enforce_delegation.sh` asset is now a ~6-line compat shim (`exec mneme hook enforce-delegation`), kept only so a pre-existing absolute-path registration keeps working until re-registered. See `docs/HOOKS.md` for details.

`mneme hook path-owned <path>` (SPEC-068) is the standalone subcommand exposing the same manifest-aware ownership check `resolvePathOwnership` implements (general-purpose surface / backward compatibility — `enforce-delegation` calls the function in-process rather than spawning this subcommand): exit 2 (block, prints the owning role or `legacy`) when the path is owned by an implementer subagent's manifest `areas`, or when no manifest exists yet (deny-by-default, protects projects mid-migration); exit 0 (allow) when the path has no delegate, or on any hard failure (fail-open). This is the orchestrator-fallback mechanism — see "Enforcement Model" below and `docs/enforcement-model.md`.

The hook also emits a context-only reminder (exit 0, never blocking) when an agent calls `Read`/`Grep`/`Glob` on a project with an indexed code graph — see `docs/codegraph.md` for the adoption nudge (C1) and auto-reindex git hooks (C2). The nudge is controlled by:

```toml
[codegraph]
hook_nudge_enabled = true   # MNEME_CODEGRAPH_HOOK_NUDGE env var overrides
```

### Enforcement Model

mneme enforces role boundaries at two layers:

1. **Capability (primary)**: every subagent declares an explicit `tools:`
   allowlist in its YAML frontmatter (`internal/install/assets/agents/*.md`).
   Read-only agents (`architect`, `qa-tester`) physically cannot edit code
   because they lack `Edit`, `Write`, `MultiEdit`, `NotebookEdit`, and `Bash`.
   Implementer agents (`backend`, `frontend`, `bug-hunter`) have the full
   edit+execution toolset. The `diagnostician` agent has `Bash` for log reading
   but lacks Edit/Write/MultiEdit — it reads infra, never mutates code.

2. **Hook (defense in depth)**: `mneme hook enforce-delegation` (a Go
   `PreToolUse` subcommand, SPEC-069) detects the orchestrator by the absence
   of `agent_id` in the hook payload. Orchestrator edit attempts against protected paths are blocked with
   exit code 2 and logged as `discovery` memories — except a path with no
   implementer subagent to own it (per `mneme hook path-owned`, SPEC-068),
   which is allowed through as a conscious fallback: the orchestrator supplies
   stages/areas that have no delegate rather than the flow breaking. This is a
   documented trade-off (correction, not excellence) — materialize the missing
   subagent via the `mneme-init` grill when quality/isolation matter.

Every blocked attempt is queryable via:

```bash
mneme search "Blocked edit"
```

For the full reference — adding subagents, debugging blocks, allowlist tables —
see `docs/enforcement-model.md`.

## Testing approach

- `internal/store` tests run against a **real in-memory SQLite** — no mocks (per `docs/ARCHITECTURE.md`). Treat the DB as part of the unit under test.
- Table-driven tests are the default.
- Target >85% coverage on core packages (`model`, `store`, `service`, `scoring`).

## Quality standards

- **Clean Code**: single-responsibility functions, intention-revealing names, no dead code, no commented-out code, no magic numbers.
- **Clean Architecture**: strict dependency inversion as described above. Adapters are pluggable.
- **Documentation**: every exported type/function/package has a godoc comment explaining *why*, not just *what*.
- **Error handling**: wrap with context — `fmt.Errorf("store: save memory: %w", err)`. Never swallow. Use sentinel errors for expected conditions. `.golangci.yml` excludes a curated set of unactionable error returns (deferred `Close`, `fmt.Fprint*`, `os.Remove`); don't expand that list casually.
- **Design patterns in use**: Repository (storage), Strategy (retrieval backends), Observer (hooks), Command (CLI), Builder (complex constructors).

## Conventions

- **Commits**: [Conventional Commits](https://www.conventionalcommits.org/) — `type(scope): description`.
- **Branches**: `type/short-description` (lowercase, hyphens).
- **Go version**: 1.25+ (go.mod currently declares 1.25.8).

## Lanes (v1.5.0)

Every backlog item and spec carries a **lane** (`trivial` or `standard`) declared at creation. Lane is never inferred automatically.

**When adding an item without a lane, ask:** "Is this trivial (≤3 files, ≤20 lines, no SQL/migrations, no public API change) or standard?"

**Orchestrator rules:**
- May propose a lane based on the description but must never assign without confirmation.
- For trivial items: dispatch to implementers with `spec quick` and include in the task payload: *"This is a trivial-lane item. Stay strictly within the declared scope `<scope>`. Do not refactor adjacent code. Do not add tests beyond updating existing assertions. If the scope is insufficient, stop and report — do not expand."*
- Never edits code itself regardless of lane.

**Key commands:**
```bash
mneme backlog add "Fix typo" --lane trivial --scope "internal/model/*.go"
mneme spec quick SPEC-007 "One-line comment fix" --by orchestrator
mneme lane audit SPEC-007
mneme lane reclassify SPEC-007 standard --by orchestrator  # if audit fails
mneme lane override SPEC-007 --reason "autogenerated file" --by orchestrator  # last resort
mneme lane status SPEC-007
```

Full reference: `docs/lanes.md`.

## Skills (v1.7.0)

mneme is the **package manager** for Claude Code skills. It embeds skills under
`internal/install/assets/skills/` and installs them to `~/.claude/skills/`.
mneme does NOT implement the Claude Code skill runtime.

**Bundled skills (SPEC-037):** only `example-skill` (structural fixture — NOT architectural guidance).

**Key commands:**
```bash
mneme skills list
mneme skills install example-skill
mneme skills pin example-skill        # protect from overwrite/remove
mneme skills unpin example-skill
mneme skills lint [<name>]            # deterministic structural check
mneme skills validate <name>          # run validation/run.sh
mneme skills remove <name> [--force]
```

**Rules:**
- `pinned: true` in an installed SKILL.md = only protection from overwrite/remove. No hook or capability coupling.
- `lint` is pure Go, deterministic, no LLM, no script execution.
- `validate` runs `validation/run.sh` with a 120s timeout; ErrNoValidation if absent.
- `internal/skill` is a leaf package — no imports of `internal/model` or other internal packages.
- SKILL.md requires 5 H2 sections and a 3-col Automated Checks table (see `docs/skills.md`).

Full reference: `docs/skills.md`.

## Models (v1.8.0)

mneme tracks a model alias per bundled agent (SPEC-038). Assignments are stored
in `~/.mneme/config.toml` under `[models.overrides]`. Config overrides are NOT
assets — they survive upgrades. Since SPEC-073, `mneme install claude-code` no
longer installs global agent profiles, so these overrides no longer rewrite any
`~/.claude/agents/<agent>.md`; per-project subagents pick their model at grill
time (`subagent_compose`). The apply-on-install machinery
(`ApplyAgentModels`) remains as dormant capacity for any future per-profile agent.

**Defaults (cost/quality rationale):**
- `architect` → `opus` (its output is the spec; errors propagate to all agents)
- `backend`, `frontend`, `qa-tester`, `bug-hunter` → `sonnet`

**Key commands:**
```bash
mneme model list                    # show effective model + origin for each agent
mneme model set bug-hunter opus     # override one agent
mneme model reset bug-hunter        # remove override, restore default
mneme model reset                   # remove all overrides
# (since SPEC-073 install no longer writes global agents, so overrides no
#  longer rewrite ~/.claude/agents; per-project subagents pick model at grill time)
```

**Rules:**
- Any non-empty string is accepted as a model string (open-ended).
- Unknown aliases produce a WARNING, never an error (`model set backend banana` warns).
- Empty model string → `ErrInvalidModel`.
- Unknown agent name → `ErrUnknownAgent` (rejected with CodeInvalidParams in MCP).
- Override in config survives `mneme install` (Install() never rewrites config.toml).
- The assign step runs automatically after WriteAgents in every install.

Full reference: `docs/models.md`.

## Conflicts (v1.9.0)

mneme surfaces memory conflicts via a two-phase workflow (SPEC-039):

1. **Detection (deterministic, FTS5):** `internal/conflicts/detect.go` extracts salient
   terms and builds a candidate FTS5 query. No LLM.
2. **Judgment (LLM via subprocess, explicit):** `internal/conflicts/judge.go` invokes
   `claude -p --output-format json` for each pair. $0 cost on subscription. Never automatic.

**Relation types:**
- `supersedes` → reuses `memories.superseded_by` + `SetSupersededBy`. Already excluded by retrieval.
- `conflicts_with` → `memory_relations` table (migration 013). Post-ranking `annotateConflicts` pass.
- `unrelated` → `memory_relations` table. Negative cache only; no retrieval effect.

**Key commands:**
```bash
mneme conflicts candidates <id>            # FTS5 candidates (deterministic)
mneme conflicts scan                       # dry-run judgment (needs claude CLI)
mneme conflicts scan --apply               # persist judgments
mneme conflicts link <from> <to> supersedes --rationale "..."
mneme conflicts link <from> <to> conflicts_with
mneme conflicts unlink <from> <to>
mneme conflicts list
```

**Rules:**
- `internal/conflicts/` is a leaf package — stdlib only, no model/store imports.
- CLI absent → `ErrCLIUnavailable`; MCP returns `IsError:true` with structured payload.
- Scan is dry-run by default; `--apply` persists. NEVER auto-judges on save.
- No auto-delete/edit of memories. No embeddings. No metered API.

Full reference: `docs/conflicts.md`.

## Team Memory (SPEC-053)

mneme lets a repository's durable knowledge flow between teammates entirely
through git — no server, no account, no network call.

**Activation is opt-in per repository:** the presence of
`<repo>/.mneme/shared/.mneme-vault` is the only flag; there is no other
config. `mneme team-memory enable` creates it, bakes/exports pre-existing
durable memories, and installs the import hooks in one idempotent step.

**Sharing levels** (`shared` column, layered on `scope=project`, not a new
scope): `0` local-only (default) · `1` auto-shared — **share-by-default**
(SPEC-071): every project-scoped type auto-shares *except* the two
auto-generated/ephemeral ones (`session_summary`, `synthesis`); i.e. decision,
discovery, config, preference, convention, architecture, pattern, bugfix, rule
all auto-share · `2` team-curated (`mneme promote <id>` / `mem_promote`, any
type).

**Key commands:**
```bash
mneme team-memory enable          # activate: marker + bake/export + hooks
mneme team-memory hooks install   # install only the import hooks
mneme team-memory hooks remove    # remove only the mneme-managed hook block
mneme promote <id>                # explicitly share one memory (shared=2)
```

**Rules:**
- WRITE is synchronous write-through inside `service.Save`/`Update` — no
  filesystem watcher exists between the vault and SQLite (see
  `docs/VAULT.md`). Best-effort: a materialization failure is logged, never
  fails the save.
- READ is a `post-merge`/`post-checkout` git hook (`mneme team-memory hooks
  run-import`, exit-0-always) that imports `.mneme/shared/` in the
  background after every pull/checkout.
- Every shared memory is its own file (`notes/<uuid>.md`) — concurrent
  creations by different teammates never collide at the git level.
- Conflict detection after import is the same deterministic FTS5 candidate
  count `mneme conflicts` uses — judgment is always a separate, manual
  `mneme conflicts scan` step, never automatic.
- `mneme team-memory enable` always prints a privacy notice: this feature is
  offline/git-native, so mneme can never determine whether a remote is
  public without a network call it deliberately never makes.

Full reference: `docs/team-memory.md`.

<!-- mneme:managed:start v=1 -->
Process and operating instructions are managed globally via mneme.
See the mneme operating manual in your global ~/.claude/CLAUDE.md.

Project scope: see this file's sections below for stack, conventions, and module structure.
<!-- mneme:managed:end -->
