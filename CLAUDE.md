# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Project Overview

**mneme** is a persistent memory system for AI coding agents. Single Go binary, zero external dependencies at runtime. Provides hybrid retrieval (BM25 + vector + graph), automatic consolidation, multi-scope memory, and agent-agnostic access via MCP.

## Build & Run

```bash
# Build (requires CGO for SQLite + FTS5 extension)
CGO_ENABLED=1 go build -tags fts5 -o mneme ./cmd/mneme

# Run MCP server (stdio, for agent integration)
./mneme mcp

# CLI usage
./mneme save --title "..." --type decision --scope project
./mneme search "query"
./mneme status

# Version is injected via ldflags at release time; dev builds show "dev"
```

## Testing

```bash
# All tests (fts5 tag required for SQLite FTS5 support)
CGO_ENABLED=1 go test -tags fts5 ./...

# Single package
CGO_ENABLED=1 go test -tags fts5 -v ./internal/store/...

# With race detection
CGO_ENABLED=1 go test -tags fts5 -race ./...

# Coverage
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out
```

## Quality Standards

This project adheres to the highest engineering standards:

- **Clean Code**: Every function has a single responsibility. Names are intention-revealing. No dead code, no commented-out code, no magic numbers.
- **Clean Architecture**: Strict dependency inversion. Domain types have zero external dependencies. Infrastructure adapters are pluggable and testable.
- **Documentation**: Every exported type, function, and package has a godoc comment explaining *why*, not just *what*. Internal packages document their design rationale.
- **Testing**: Every public API has unit tests. Integration tests cover the full pipeline. Table-driven tests are preferred. Test helpers are well-documented. Target: >85% coverage on core packages.
- **Error Handling**: Errors are wrapped with context (`fmt.Errorf("store: save memory: %w", err)`). Never swallow errors silently. Sentinel errors for expected conditions.
- **Design Patterns**: Repository pattern for storage. Strategy pattern for retrieval backends. Observer pattern for hooks. Command pattern for CLI. Builder pattern where constructors are complex.

## Architecture

The binary entrypoint is `cmd/mneme/main.go`.

Key packages:
- **`internal/domain`** — Pure domain types: Memory, Entity, Relation, Scope, MemoryType. Zero external dependencies.
- **`internal/store`** — SQLite storage layer: FTS5, vector index, knowledge graph. Implements domain repository interfaces.
- **`internal/retrieval`** — Hybrid retrieval pipeline: BM25, vector search, graph traversal, RRF fusion.
- **`internal/consolidation`** — Background memory consolidation: scoring, decay, dedup, eviction.
- **`internal/mcp`** — MCP server (stdio): tool handlers, protocol implementation.
- **`internal/cli`** — CLI commands via cobra.
- **`internal/config`** — Configuration loading and defaults.
- **`internal/project`** — Auto-detection of project context from git remote.

## Conventions

- **Commits**: [Conventional Commits](https://www.conventionalcommits.org/) — `type(scope): description`
- **Branches**: `type/short-description` (lowercase, hyphens)
- **Go version**: 1.24+
- **Linting**: `golangci-lint run` must pass with zero warnings
- **Formatting**: `gofmt` and `goimports` enforced
