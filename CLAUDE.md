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

Supporting packages: `scoring/` (decay, BM25 re-ranking, RRF fusion), `consolidation/` (background decay/dedup/budget sweeps), `embed/` (TF-IDF baseline), `sync/` (JSONL.gz git-shareable export/import), `project/` (git-remote slug detection), `config/` (TOML + env overrides), `install/` (agent profile installer), `tui/` (Bubble Tea), `upgrade/`, `export/`.

### The three frontends

- **MCP** (`internal/mcp`, primary) — JSON-RPC 2.0 over stdio, ProtocolVersion `2024-11-05`. Surface: 23 tools (13 `mem_*`, 4 `backlog_*`, 6 `spec_*`). `handleMessage()` is exposed separately from `Run()` so unit tests can drive it without I/O loops.
- **HTTP** (`internal/http`, `mneme serve --addr :7437`) — stdlib `net/http`, graceful shutdown 10s, 8 endpoints under `/v1/`. Currently lacks SDD endpoints and a few mem tools (`mem_checkpoint`, `mem_timeline`, `mem_suggest_topic_key`); when adding service capabilities, decide explicitly whether HTTP gets parity.
- **CLI** (`internal/cli`, Cobra) — 23 top-level commands. Notable: `sync export|import|status` is the backup/restore path (no dedicated `restore` command); `mneme init` migrates legacy projects to the SDD engine; `mneme install <agent>` writes agent profiles.

### Persistence

Two SQLite databases per host:
- `~/.mneme/global.db` — global + org scope memories
- `~/.mneme/projects/<slug>.db` — project-scoped memories (slug derived from git remote)

Scopes (`global` / `org` / `project`) never leak between projects. Migrations are embedded via `embed.FS`.

### Delegation hook (important when editing source)

`mneme hook enforce-delegation` is a Claude Code `PreToolUse` hook that **blocks `Edit`/`Write`/`MultiEdit` calls** against protected paths (default: `cmd/`, `internal/`, `src/`, `apps/`, `packages/`, `lib/`) and exits with code 2 telling the principal agent to delegate to the `backend`/`frontend`/`architect` subagents. `AllowedPaths` (default: `docs/`, `*.md`, `CLAUDE.md`) bypass it. Source: `internal/cli/hook.go:runHookEnforceDelegation`. If a code edit gets blocked, that's the hook — delegate, don't bypass.

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
