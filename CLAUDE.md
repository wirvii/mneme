# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build, test, run

All builds and tests **require CGO and the `fts5` build tag** (SQLite FTS5 full-text search is not optional). The `Makefile` is the canonical entrypoint:

```bash
make build         # CGO_ENABLED=1 go build -tags fts5 -o mneme ./cmd/mneme
make test          # go test -tags fts5 ./...
make test-race     # go test -tags fts5 -race ./...
make install       # build + sudo cp to /usr/local/bin/
make setup         # install + `mneme install claude-code` (configures agent profiles)
make release-local # ldflags-stripped build with Version=local
```

Run a single package or test:

```bash
CGO_ENABLED=1 go test -tags fts5 ./internal/store/...
CGO_ENABLED=1 go test -tags fts5 -run TestSaveMemory ./internal/store
```

Linting must pass with **zero warnings**: `golangci-lint run`. `gofmt` and `goimports` are enforced. A `pre-push` hook lives at `.githooks/pre-push`.

Module path is `github.com/juanftp/mneme`; entrypoint is `cmd/mneme`.

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

Supporting packages: `scoring/` (decay, BM25 re-ranking, RRF fusion), `consolidation/` (background decay/dedup/budget sweeps + edge decay), `graph/` (Hebbian auto-strengthening: AccessTracker ring buffer + HebbianWorkerPool async worker), `rules/` (applies_to pattern matching engine for pre-tool-use hook), `embed/` (TF-IDF baseline), `sync/` (JSONL.gz git-shareable export/import), `project/` (git-remote slug detection), `config/` (TOML + env overrides), `install/` (agent profile installer + skills embed), `skill/` (leaf: SKILL.md parser, structural linter, validate runner — no internal deps), `tui/` (Bubble Tea), `upgrade/`, `export/`.

### The three frontends

- **MCP** (`internal/mcp`, primary) — JSON-RPC 2.0 over stdio, ProtocolVersion `2024-11-05`. Surface: 51 tools (14 `mem_*`, 4 `backlog_*`, 8 `spec_*`, 5 `lane_*`, 10 `codegraph_*`, 7 `skills_*`, 3 `model_*`). `spec_*`: spec_new, spec_status, spec_advance, spec_pushback, spec_resolve, spec_list, spec_quick, spec_reject. `lane_*`: lane_audit, lane_reclassify, lane_override, lane_status, lane_stats. `skills_*`: skills_list, skills_install, skills_pin, skills_unpin, skills_remove, skills_lint, skills_validate. `model_*`: model_list, model_set, model_reset. `handleMessage()` is exposed separately from `Run()` so unit tests can drive it without I/O loops.
- **HTTP** (`internal/http`, `mneme serve --addr :7437`) — stdlib `net/http`, graceful shutdown 10s, 8 endpoints under `/v1/`. Currently lacks SDD endpoints and a few mem tools (`mem_checkpoint`, `mem_timeline`, `mem_suggest_topic_key`); when adding service capabilities, decide explicitly whether HTTP gets parity.
- **CLI** (`internal/cli`, Cobra) — 31 top-level commands. Notable: `sync export|import|status` is the backup/restore path (no dedicated `restore` command); `mneme init` migrates legacy projects to the SDD engine; `mneme install <agent>` writes agent profiles; `mneme skills` manages skills in `~/.claude/skills/`; `mneme model` manages per-agent model assignments.

### Persistence

Two SQLite databases per host:
- `~/.mneme/global.db` — global + org scope memories
- `~/.mneme/projects/<slug>.db` — project-scoped memories (slug derived from git remote)

Scopes (`global` / `org` / `project`) never leak between projects. Migrations are embedded via `embed.FS`.

### Pre-tool-use hook (important when editing source)

`mneme hook pre-tool-use` is the active Claude Code `PreToolUse` hook. It evaluates **rules** from the mneme database against every `Edit`/`Write`/`MultiEdit` call. Rules carry `applies_to` patterns (path globs, tool selectors, negations) and a `severity` level (`info`/`warn`/`block`). When a `block`-severity rule matches, the hook exits with code 2 and Claude Code rejects the tool call. Source: `internal/cli/hook.go:runHookPreToolUse` + `internal/rules/match.go`. If a code edit gets blocked, that's a rule — check `mneme rule list` and delegate or adjust accordingly.

The legacy `mneme hook enforce-delegation` (config-based static paths) is deprecated but still works. Migrate with `mneme install claude-code --reinstall-hooks`. See `docs/HOOKS.md` for details.

### Enforcement Model

mneme enforces role boundaries at two layers:

1. **Capability (primary)**: every subagent declares an explicit `tools:`
   allowlist in its YAML frontmatter (`internal/install/assets/agents/*.md`).
   Read-only agents (`architect`, `qa-tester`) physically cannot edit code
   because they lack `Edit`, `Write`, `MultiEdit`, `NotebookEdit`, and `Bash`.
   Implementer agents (`backend`, `frontend`, `bug-hunter`) have the full
   edit+execution toolset.

2. **Hook (defense in depth)**: `enforce_delegation.sh` (a bash `PreToolUse`
   hook) detects the orchestrator by the absence of `agent_id` in the hook
   payload. Orchestrator edit attempts against protected paths are blocked with
   exit code 2 and logged as `discovery` memories.

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
- **Go version**: 1.24+ (go.mod currently declares 1.25.8).

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

mneme assigns a model alias to each bundled agent at install time (SPEC-038).
Assignments are stored in `~/.mneme/config.toml` under `[models.overrides]` and
applied to `~/.claude/agents/<agent>.md` on every `mneme install claude-code`.
Config overrides are NOT assets — they survive upgrades.

**Defaults (cost/quality rationale):**
- `architect` → `opus` (its output is the spec; errors propagate to all agents)
- `backend`, `frontend`, `qa-tester`, `bug-hunter` → `sonnet`

**Key commands:**
```bash
mneme model list                    # show effective model + origin for each agent
mneme model set bug-hunter opus     # override one agent
mneme model reset bug-hunter        # remove override, restore default
mneme model reset                   # remove all overrides
mneme install claude-code           # apply current model assignments
```

**Rules:**
- Any non-empty string is accepted as a model string (open-ended).
- Unknown aliases produce a WARNING, never an error (`model set backend banana` warns).
- Empty model string → `ErrInvalidModel`.
- Unknown agent name → `ErrUnknownAgent` (rejected with CodeInvalidParams in MCP).
- Override in config survives `mneme install` (Install() never rewrites config.toml).
- The assign step runs automatically after WriteAgents in every install.

Full reference: `docs/models.md`.
