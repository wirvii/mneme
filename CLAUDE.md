# CLAUDE.md

This file guides Claude Code (claude.ai/code) when working in this repository. The user-facing overview is `README.md`; the index of every current document is `docs/README.md`.

## Build, test, run

mneme is a pure-Go build: no CGO, no C compiler, no build tags. SQLite (FTS5 compiled in) comes from `modernc.org/sqlite`. The `Makefile` is the canonical entrypoint:

```bash
make build         # go build -o mneme ./cmd/mneme
make test          # HOME/USERPROFILE-sandboxed go test ./... + scripts/testguard.sh
make test-race     # same sandbox, go test -race ./...
make install       # build + sudo cp to /usr/local/bin/
make setup         # install + `mneme install claude-code`
make release-local # ldflags-stripped build with Version=local
```

Single package or test: `go test ./internal/store/...`, `go test -run TestSaveMemory ./internal/store`.

Lint must pass with **zero warnings**: `golangci-lint run`. `gofmt` and `goimports` are enforced; a `pre-push` hook lives at `.githooks/pre-push`. Module path `github.com/wirvii/mneme`, entrypoint `cmd/mneme`, Go 1.25+. mneme also builds and runs on Windows (`GOOS=windows go build ./...` is a gate): OS-specific branches check `runtime.GOOS` inline, never `_windows.go` files or build tags.

## Architecture

One service layer, three frontends. Keep them in sync when adding a capability, and decide explicitly whether HTTP gets parity.

```
cli/  mcp/  http/        three frontends (Cobra, JSON-RPC stdio, REST)
       |
   service/              business logic (the only layer frontends call)
       |
   store/                repository: CRUD, FTS5 search, entities, stats
       |
   db/                   SQLite + embedded migrations
       |
   model/                domain types, zero external imports (the leaf)
```

**Dependency rule:** imports flow inward only. Never let a frontend call `store` or `db`; go through `service`.

Supporting packages: `scoring` (decay, BM25, RRF fusion), `consolidation` (decay/dedup/budget sweeps), `graph` (Hebbian strengthening), `rules` (`applies_to` matching for the pre-tool-use hook), `enforcement` (leaf: orchestrator-guard decisions), `embed` (TF-IDF), `sync` (JSONL.gz export), `vault` (team-memory files), `sddfile` (SDD Markdown format), `project` (git-remote slug), `config` (TOML + env), `install` (agent installer: MCP, hooks, manual, commands, skills), `subagents` (per-project role files: permissions, layer 1, composer), `codegraph`, `tui`, `upgrade`, `export`. Leaf packages that import no other `internal/*`: `skill` (SKILL.md parser, linter, usage log), `conflicts`, `testenv`, `gitrepo`, `laneaudit`, `acceptance`, `gitattrs`.

### The three frontends

- **MCP** (`internal/mcp`, primary): JSON-RPC 2.0 over stdio, ProtocolVersion `2024-11-05`. **31 tools** (pinned by `TestAllTools_Count31`): `mem_*` 6 (save, search, get, context, update, session_end), `backlog_*` 6, `spec_*` 9, `lane_*` 5, `codegraph_*` 4 (search, context, callers, impact), `init` 1. Anything administrative (skills, models, conflicts, subagents, SDD files, the rest of memory and graph tooling) is CLI only. `lifecycleTools` (`internal/cli/hook.go`) denies `spec_advance`, `spec_quick`, `backlog_archive` and `lane_override` to subagents. Memory responses carry `suggested_topic_key`, `possible_conflicts` and `review_due`/`review_note`; `mem_update` takes `supersedes`/`conflicts_with`/`unrelated_to` and `confirm`; `mem_session_end` takes `partial`.
- **HTTP** (`internal/http`, `mneme serve --addr :7437`): stdlib `net/http`, `/v1/` memory endpoints. No SDD endpoints.
- **CLI** (`internal/cli`, Cobra): 38 top-level commands registered by the single `root.AddCommand(...)` call in `internal/cli/root.go` (Cobra adds `help` and `completion`). Verify against the source, not `--help`. `mneme mem ...` carries the memory tools that left MCP.

### Persistence

Two SQLite databases per host: `~/.mneme/global.db` (global + org scope) and `~/.mneme/projects/<slug>.db` (project scope, slug from the git remote). Scopes never leak between projects. Migrations are embedded via `embed.FS`. Every `backlog_items`/`specs` row carries an immutable UUIDv7 anchor, so a `SPEC-125` mention in a memory resolves against the reading machine's own database.

## Roles, skills and the operating manual

The operating manual (`internal/install/assets/operating-manual*.md`, about 2.7 KB, one per runtime) holds only what must always be loaded: roles, the human approval gate, plain language, the memory loop and the skill index. Everything procedural lives in skills under `internal/install/assets/skills/` (embedded, mirrored to `~/.claude/skills` and `$HOME/.agents/skills`): `mneme-flow`, `feature`, `bug`, `trivial`, `review-brief`, `present-spec`, `grill-me`, `mneme-init`, `codegraph`. The lane thresholds exist only in `mneme-flow/references/depth.md`. `SKILL.md` is at most 4096 bytes, detail goes in `references/`, and `mneme skills lint|validate|usage` check and measure them (`docs/skills.md`). `TestOperatingManuals_*` pin the manuals' size and anchors.

Per-project role files (`.claude/agents/*.md`, `.codex/agents/*.toml`) are generated by `mneme subagents` from `internal/subagents`: `PermissionTable` (capability allowlists, every role carries `Skill`) plus layer 1 (`assets/agent-fixed.md`, at most 4096 bytes per role, `AgentFixedVersion`, now 6). Bump the version whenever layer 1 changes, and run `mneme subagents regen --all` afterwards; `regen` and `doctor` read the project's database, so never run them against the real `~/.mneme` while testing: use a HOME sandbox with a `sqlite3 .backup` copy.

## Hooks and enforcement

`mneme hook pre-tool-use` evaluates **rules** from the database against every `Edit`/`Write`/`MultiEdit`; a `block`-severity match exits 2 and Claude Code rejects the call (`mneme rule list`). `mneme hook enforce-delegation` blocks the orchestrator from editing, or running Bash against, a path owned by an implementer subagent (decision logic in the leaf `internal/enforcement`); a path with no delegate is allowed as a conscious fallback, and a project with no manifest keeps deny-by-default. `[delegation] orchestrator_trivial_inline` is the opt-in that lets the orchestrator edit inside a trivial spec's declared scope. The same hook rejects a subagent's Bash command that runs `mneme spec advance|quick`, `sdd import`, `backlog archive` or `lane override`, unless that very command names an isolated data directory (`--data-dir`, `HOME=`, `USERPROFILE=`, `MNEME_DATA_DIR=`; SPEC-175). No agent, orchestrator included, may write `.mneme/sdd/**` through a file tool, Bash or Codex's `apply_patch`: it is the first guard of the same hook and ignores identity, manifest and config (SPEC-179, `internal/enforcement/sddfiles*.go`). Subagents are contained to their certified areas (`subagent_containment`: off, warn, block). Capability allowlists are the primary layer, hooks the second. Reference: `docs/enforcement-model.md`, `docs/HOOKS.md`.

## Testing approach

- `internal/store` tests run against a **real in-memory SQLite**, no mocks. Table-driven by default. Target above 85% coverage on `model`, `store`, `service`, `scoring`.
- mneme dogfoods itself: this repo's own `~/.mneme/projects/wirvii-mneme.db` and `.mneme/shared/` vault are real. A test that resolves an environment-derived path can silently write into them (SPEC-085 found 7752 polluted rows). Four layers protect them; know all four:
  1. `NewMemoryService` never auto-detects team memory; it takes `...service.Option`, defaulting off. Only `internal/cli.initService` opts in. A test that needs the real detection opts in explicitly (`newRepoTestService`).
  2. Call `gitident.Reset()` in any helper that chdirs into a fixture git repo (`Author()` memoizes per process).
  3. A CLI test that runs a real cobra command must also chdir into an isolated, non-git temp dir, not only isolate `--data-dir` (see `runSubagentsCmd`).
  4. `internal/testenv.Isolate(m)` from `TestMain` in every package that touches environment-derived paths: `service`, `cli`, `mcp`, `http`, `install`, `upgrade`.
- `make test` additionally sandboxes HOME to `tmp/testhome` and `scripts/testguard.sh` fails if a `projects/*.db` or `global.db` appears there. `GOCACHE`/`GOMODCACHE` are captured with `$(shell go env ...)` at parse time, never inline after the sandboxed `HOME=`.
- `scripts/cleanup-test-pollution.sh` is the one-off, dry-run-by-default purge of the pre-SPEC-085 pollution; it has not been applied to the real database.

## Quality standards and conventions

- Clean Code and Clean Architecture as above; no dead code, no commented-out code, no magic numbers.
- Godoc on every exported type, function and package, explaining why.
- Wrap errors with context (`fmt.Errorf("store: save memory: %w", err)`), never swallow, sentinel errors for expected conditions. `.golangci.yml` excludes a curated set of unactionable returns; do not expand it casually.
- Patterns: Repository, Strategy (retrieval), Observer (hooks), Command (CLI), Builder.
- Commits: Conventional Commits, `type(scope): description`. Branches `type/short-description`. No AI signatures in commits or PRs.

## Lanes

Every backlog item and spec carries a lane, declared at creation and never inferred. **Trivial** is the small, low-risk path (quick-start with `spec_quick`, deterministic `lane_audit`); everything else is **standard**. The thresholds live in `internal/install/assets/skills/mneme-flow/references/depth.md`, and the auditor's numbers in `internal/laneaudit`. Archiving a spec's backlog item freezes the spec for good (there is no unarchive; open a new item). Reference: `docs/lanes.md`.

## SDD git-native (opt-in per repository)

`mneme sdd enable` also writes the database's backlog items and specs as reviewable Markdown under `.mneme/sdd/` (marker `.mneme/sdd/.mneme-sdd`); `mneme sdd import` and the hooks from `mneme sdd hooks install` read teammates' files back, by UUID anchor and never by correlative. The format lives in `internal/sddfile` (named by correlative so collisions show up in git), the write path in `internal/service/sdd_export.go`, the read path in the neighbouring import file of `internal/service`. Two machines claiming the same correlative is reported, not resolved. Only the mneme binary writes `.mneme/sdd/`; the `enforce-delegation` hook rejects any agent write there, so a hand edit is a human act in an editor (SPEC-179). Reference: `docs/sdd-git-native.md`.

## Criteria and the review frontier

A standard spec's `criteria.toml` (id + text; written only by `spec_doc_write` kind `criteria`) is required before `speccing -> specced` and is the only thing that authorizes `spec_reject`: findings must name declared criterion ids (`ensureCriteriaDeclared`, `ensureRejectFindings` in `internal/service/sdd_contract.go`). Anything outside the contract becomes a new `backlog_add`. `spec_advance` returns the `review_range` when a spec enters `qa`, computed by mneme from the review frontier stored in `spec_history.reviewed_sha` (`internal/service/sdd_frontier.go`); the QA brief copies its notice verbatim. A spec's work document is `tarea.md` (`spec_doc_write` kind `task` plus `section`). References: `docs/lanes.md`, `docs/api/sdd.md`.

## Team memory

Opt-in per repository by the presence of `.mneme/shared/.mneme-vault`. Durable project memories (every type except `session_summary` and `synthesis`) are written through to one Markdown file each and imported by `post-merge`/`post-checkout` hooks; `mneme promote <id>` shares one explicitly. No server, no network. Reference: `docs/team-memory.md`.

## Models and conflicts

`mneme model set|reset|list` keeps per-role model aliases in `~/.mneme/config.toml` (defaults: architect opus, others sonnet). Conflict detection is deterministic FTS5 candidates at save time (`possible_conflicts`); judgment is the agent's (`mem_update`), and `mneme conflicts scan` can ask the Claude CLI. Nothing is auto-deleted. References: `docs/models.md`, `docs/conflicts.md`.
