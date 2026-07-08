# mneme

Persistent memory for AI coding agents -- with a spec-driven workflow engine, semantic code graph, and role enforcement built on top. One binary, zero runtime dependencies.

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8.svg)](https://go.dev)
[![Release](https://img.shields.io/github/v/release/wirvii/mneme?label=release)](https://github.com/wirvii/mneme/releases)
[![MCP Tools](https://img.shields.io/badge/MCP%20tools-57-green.svg)](#mcp-tools)

---

## Table of Contents

- [What is mneme?](#what-is-mneme)
- [Why mneme?](#why-mneme)
- [The 4 Layers](#the-4-layers)
- [Quickstart](#quickstart)
- [Features](#features)
- [Commands](#commands)
- [MCP Tools](#mcp-tools)
- [Comparison](#comparison)
- [Documentation](#documentation)
- [Architecture](#architecture)
- [Status & Roadmap](#status--roadmap)
- [Contributing](#contributing)
- [License](#license)

---

## What is mneme?

mneme gives AI coding agents a brain that survives between sessions. It stores structured knowledge -- decisions, patterns, rules, conventions, architecture -- in a local SQLite database with full-text search, a weighted knowledge graph, and automatic consolidation. Any MCP-compatible agent (Claude Code, Cursor, Windsurf, OpenCode, Gemini CLI) can save and retrieve persistent memory through 57 tools over JSON-RPC stdio.

## Why mneme?

- **Agents forget everything between sessions.** Patterns discovered, bugs resolved, architecture decisions -- all lost when the conversation ends.
- **CLAUDE.md files become battlegrounds.** Config, conventions, rules, and project knowledge collide in a single unstructured file. Teams clash when multiple contributors edit it.
- **Knowledge stays trapped in silos.** What you learn in one project is invisible in another. Reusable patterns, personal preferences, and team conventions never transfer.

## The 4 Layers

```
┌──────────────────────────────────────────────────┐
│ Layer 4 — Synthesis                              │
│   Community detection (Louvain) + auto-summaries │
├──────────────────────────────────────────────────┤
│ Layer 3 — Retrieval                              │
│   RRF(BM25 + vector + graph) + rule injection    │
├──────────────────────────────────────────────────┤
│ Layer 2 — Graph                                  │
│   Weighted edges, Hebbian learning, wikilinks,   │
│   edge decay, PPR, communities                   │
├──────────────────────────────────────────────────┤
│ Layer 1 — Storage                                │
│   SQLite + FTS5, multi-scope (global/project),   │
│   vault mirror (Markdown + Obsidian)             │
└──────────────────────────────────────────────────┘
```

**Storage** keeps memories in scoped SQLite databases with FTS5 full-text search and UUIDv7 identifiers. The vault mirror exports them as Markdown files readable by Obsidian or any editor.

**Graph** connects memories through weighted, directed relations with 8 relation types. Hebbian auto-strengthening reinforces edges between co-accessed memories. Wikilinks (`[[topic_key]]`) auto-create relations on save.

**Retrieval** fuses three channels via Reciprocal Rank Fusion: BM25 text matching, vector similarity, and 1-hop graph expansion. Rules are always injected first in context with a separate token budget.

**Synthesis** detects communities of related memories using the Louvain algorithm and generates summary memories that compress clusters into digestible context.

---

## Quickstart

```bash
# 1. Install
git clone https://github.com/wirvii/mneme.git && cd mneme
make install

# 2. Wire into your agent
mneme install claude-code
# or, for OpenAI Codex CLI (single-agent, no delegation enforcement):
mneme install codex

# 3. Start using it
mneme save --type decision --title "Auth uses JWT RS256" \
  --content "Switched to RS256 for asymmetric key verification."
mneme search "authentication"
mneme status
```

`mneme install claude-code` registers MCP tools, session hooks, and the rules enforcement hook in `~/.claude/settings.json`, plus the seven bundled subagent profiles. After install, the agent automatically loads context at session start, saves session summaries at end, and evaluates rules before every file edit. `mneme install codex` wires the same MCP server and memory protocol into OpenAI Codex CLI's single-agent model -- see [docs/codex.md](docs/codex.md) for the differences.

---

## Features

### Rules System

Rules are memories with enforcement. Each rule has `applies_to` patterns and a `severity` level (`info`, `warn`, `block`). They are immune to decay and always injected in context.

```bash
mneme rule add --title "No vendor edits" \
  --applies-to "vendor/**" --severity block \
  --content "Never edit vendor/ files."

mneme rule test --tool Edit --path vendor/foo.go
# -> BLOCKED
```

The `pre-tool-use` hook evaluates rules in under 50ms before every `Edit`, `Write`, and `MultiEdit`. Block-severity rules reject the tool call with exit code 2.

Pattern syntax: `**` (wildcard), `tool:Edit` (tool selector), `internal/**/*.go` (path glob), `tool:Edit+internal/**` (AND), `!docs/**` (negation).

### Knowledge Graph

Entities (modules, services, patterns, files) connected by 8 weighted relation types (`depends_on` 0.9, `part_of` 0.85, `implements` 0.8, `uses` 0.7, `conflicts_with` 0.7, `supersedes` 0.6, `related_to` 0.5, `references` 0.4).

- **Hebbian auto-strengthening** -- memories accessed together have their edges reinforced automatically via an async ring-buffer worker.
- **Edge decay** -- relations not traversed for 30 days decay exponentially (0.02/day). Active edges stay strong.
- **Graph rebuild** -- `mneme graph rebuild` backfills the graph from existing memories using 4 heuristics (topic_key, file paths, code symbols, wikilinks).

### 3-Channel RRF Retrieval

```
Query ──┬──> FTS5 BM25 ──────────> weight 1.0
        ├──> Vector similarity ───> weight 0.8
        ├──> 1-hop graph expand ──> weight 0.6
        └──> RRF Fusion (k=60) ──> Final ranking
```

### Wikilinks & Knowledge Gaps

`[[topic_key]]` references in memory content are parsed on save and create `references` relations automatically. Unresolved wikilinks (target does not exist yet) are tracked in `unresolved_references` and auto-resolved when the target memory is created later. `mem_gaps` exposes the gap list.

### Community Detection

Louvain algorithm groups related memories into communities. Synthesis memories summarize each community. `mem_context` packs context by community for coherent, compressed output.

### Vault & Obsidian Integration

Export memories as a directory of Markdown files with YAML frontmatter. Bidirectional: edit in Obsidian, import back to SQLite.

```bash
mneme vault export                    # SQLite -> Markdown
mneme vault import                    # Markdown -> SQLite
mneme vault import --strategy overwrite --dry-run
```

The vault is a valid Obsidian vault out of the box. See [docs/OBSIDIAN.md](docs/OBSIDIAN.md) for Dataview queries, recommended plugins, and workflows.

### Spec-Driven Development (SDD)

Built-in lifecycle engine for spec-driven development:

```
backlog_add -> backlog_refine -> backlog_promote -> spec_new
  -> spec_advance (draft -> speccing -> specced -> implementing -> qa -> done)
```

Seven pre-configured subagent profiles (architect, backend, frontend, qa-tester, bug-hunter, diagnostician, orchestrator) ship with `mneme install claude-code` and integrate with the SDD lifecycle. See [Enforcement](#enforcement) for the role/capability model.

### Memory Manifest

Portable interchange format (`.manifest.tar.gz`) defined by JSON Schema 2020-12. Exports memories, entities, relations, and sessions for full-fidelity transfer between machines or third-party tools.

```bash
mneme sync export --format manifest   # full-fidelity archive
mneme sync export                     # legacy JSONL.gz (memories only)
mneme sync import <file>              # auto-detects format
```

---

## Commands

33 top-level commands (`mneme --help` is the source of truth for flags; full flag reference in [docs/api/cli.md](docs/api/cli.md)):

| Command | Description |
|---------|-------------|
| `mneme save` | Save a memory |
| `mneme search` | Search memories (BM25 + vector + graph) |
| `mneme get` | Retrieve a memory by ID |
| `mneme update` | Update an existing memory |
| `mneme forget` | Mark a memory for accelerated decay |
| `mneme stats` | Show memory store statistics |
| `mneme status` | Show mneme status and project dashboard |
| `mneme consolidate` | Run the memory consolidation pipeline |
| `mneme rule` | Manage rules (`add`, `list`, `test`) |
| `mneme explore` | Explore the knowledge graph from a seed memory |
| `mneme graph` | Manage the knowledge graph (`rebuild`, `cleanup-orphan-relations`) |
| `mneme gaps` | List knowledge gaps (unresolved `[[wikilinks]]`) |
| `mneme mcp` | Start the MCP server over stdio |
| `mneme serve` | Start the HTTP API server |
| `mneme install` | Configure an AI coding agent to use mneme (`claude-code`, `codex`) |
| `mneme init` | Initialise a project with mneme managed blocks and show drift report |
| `mneme backlog` | Manage the project backlog (`add`, `list`, `refine`, `promote`, `archive`) |
| `mneme spec` | Manage specs in the SDD lifecycle (`new`, `advance`, `pushback`, `resolve`, `quick`, `reject`, `list`, `status`, `history`) |
| `mneme lane` | Manage trivial-lane SDD classification and auditing (`audit`, `reclassify`, `override`, `status`, `stats`) |
| `mneme codegraph` | Semantic code graph -- index and query code structure (`index`, `search`, `node`, `callers`, `callees`, `impact`, `trace`, `files`, `status`, `hooks`) |
| `mneme skills` | Manage mneme skills in `~/.claude/skills/` (`list`, `install`, `pin`, `unpin`, `remove`, `lint`, `validate`) |
| `mneme model` | Manage per-agent model assignments (`list`, `set`, `reset`) |
| `mneme conflicts` | Detect and manage memory conflict relations (`candidates`, `scan`, `link`, `unlink`, `list`) |
| `mneme sync` | Sync memories via git (`export`, `import`, `status`) |
| `mneme vault` | Manage the filesystem vault mirror (`export`, `import`) |
| `mneme embed` | Manage memory embeddings for semantic search (`backfill`) |
| `mneme export` | Export memories in various formats (`markdown`) |
| `mneme config` | Inspect resolved configuration (`show`) |
| `mneme hook` | Run a mneme hook handler (invoked by agent hooks) |
| `mneme tui` | Launch interactive terminal UI |
| `mneme upgrade` | Upgrade mneme to the latest release |
| `mneme version` | Print the mneme version |
| `mneme completion` | Generate the autocompletion script for the specified shell |

---

## MCP Tools

The MCP server (`mneme mcp`) exposes 24 tools over JSON-RPC 2.0 stdio:

### Memory Tools (14)

| Tool | Description |
|------|-------------|
| `mem_save` | Save a memory (supports `rule` type with `applies_to` and `severity`) |
| `mem_search` | Hybrid search with optional graph expansion (`include_graph` flag) |
| `mem_get` | Retrieve full content by ID |
| `mem_context` | Curated context bundle with rules always injected first |
| `mem_update` | Partial update of an existing memory |
| `mem_session_end` | End session and save summary |
| `mem_suggest_topic_key` | Suggest a topic_key for dedup |
| `mem_relate` | Create/update entity relations (with explicit weight override) |
| `mem_timeline` | Chronological neighborhood around a point in time |
| `mem_stats` | Aggregate statistics |
| `mem_checkpoint` | Save work-in-progress checkpoint |
| `mem_forget` | Mark a memory for accelerated decay |
| `mem_explore` | BFS graph traversal from a seed (depth/budget/threshold) |
| `mem_gaps` | List unresolved wikilink references (knowledge gaps) |

### Backlog Tools (4)

| Tool | Description |
|------|-------------|
| `backlog_add` | Add item to backlog |
| `backlog_list` | List backlog items |
| `backlog_refine` | Refine a raw backlog item |
| `backlog_promote` | Promote refined item to spec |

### Spec Tools (6)

| Tool | Description |
|------|-------------|
| `spec_new` | Create a new spec in draft status |
| `spec_status` | Get full spec status with history and pushbacks |
| `spec_advance` | Advance spec to next lifecycle state |
| `spec_pushback` | Register blocking questions on a spec |
| `spec_resolve` | Resolve a pushback |
| `spec_list` | List specs (filterable by status) |

---

## Comparison

| Feature | mneme | CLAUDE.md | RAG (vector DB) | Obsidian |
|---------|-------|-----------|-----------------|----------|
| Persists across sessions | ✅ | ✅ (manual) | ✅ | ✅ |
| Structured types | ✅ 11 types | ❌ Free-form | ❌ Chunks | ❌ Free-form |
| Cross-project memory | ✅ Scopes | ❌ | Depends | ❌ Per vault |
| Knowledge graph | ✅ Weighted + Hebbian | ❌ | ❌ | ✅ Backlinks |
| Auto-decay | ✅ Per-type rates | ❌ Manual | ❌ | ❌ |
| Rules enforcement | ✅ JIT hook + severity | ❌ Static text | ❌ | ❌ |
| Wikilinks + gap tracking | ✅ Auto-resolve | ❌ | ❌ | ✅ Links only |
| Community detection | ✅ Louvain | ❌ | ❌ | ❌ |
| Semantic code graph | ✅ Symbols, callers, impact | ❌ | ❌ | ❌ |
| Skills package manager | ✅ install/pin/lint/validate | ❌ | ❌ | ❌ |
| Role enforcement | ✅ Capability + hook, 2 layers | ❌ | ❌ | ❌ |
| Per-agent model assignment | ✅ `mneme model` | ❌ | N/A | N/A |
| Agent-agnostic | ✅ MCP + HTTP + CLI | Claude only | Varies | Manual |
| Local-only / no cloud | ✅ SQLite | ✅ File | Usually cloud | ✅ Files |
| SDD lifecycle | ✅ Built-in, with lanes | ❌ | ❌ | ❌ |
| Obsidian integration | ✅ Vault mirror | ❌ | ❌ | N/A |

---

## Documentation

| Document | Contents |
|----------|----------|
| [User Guide](docs/GUIDE.md) | End-to-end guide for humans and agents -- concepts, examples, cheatsheets |
| [Architecture](docs/ARCHITECTURE.md) | Layered design, graph layer, rules system, retrieval pipeline |
| [Rules System](docs/RULES.md) | applies_to syntax, severity, hooks, examples by stack |
| [Knowledge Graph](docs/GRAPH.md) | Weights, Hebbian learning, decay, wikilinks, rebuild |
| [Hooks Integration](docs/HOOKS.md) | Session hooks, pre-tool-use, migration from legacy |
| [Vault](docs/VAULT.md) | Filesystem mirror, export/import, frontmatter spec |
| [Obsidian Integration](docs/OBSIDIAN.md) | Using Obsidian as a front-end for mneme |
| [Configuration](docs/CONFIG.md) | All config sections, env overrides, tuning recipes |
| [Memory Manifest](docs/MEMORY-MANIFEST.md) | Portable interchange format (JSON Schema 2020-12) |
| [Technical Spec](docs/SPEC.md) | Original v0.1 specification |

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                       mneme binary                          │
│                                                             │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐      │
│  │   CLI   │  │   MCP   │  │  HTTP   │  │  Hooks  │      │
│  │ (cobra) │  │ (stdio) │  │ (:7437) │  │ (agent) │      │
│  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘      │
│       └─────────────┼───────────┼─────────────┘            │
│                     ▼                                       │
│            ┌──────────────┐                                 │
│            │   Service    │  business logic, scoring,       │
│            │    Layer     │  Hebbian tracking, rules        │
│            └──────┬───────┘                                 │
│                   │                                         │
│       ┌───────────┼───────────┐                             │
│       ▼           ▼           ▼                             │
│  ┌────────┐ ┌──────────┐ ┌────────┐                        │
│  │ Store  │ │ Scoring  │ │ Graph  │                        │
│  │ (CRUD, │ │ (BM25,   │ │ (ring  │                        │
│  │  FTS5) │ │ RRF,     │ │ buffer,│                        │
│  │        │ │ decay)   │ │ worker)│                        │
│  └───┬────┘ └──────────┘ └────────┘                        │
│      ▼                                                      │
│  ┌────────┐                                                 │
│  │ SQLite │  global.db + projects/<slug>.db                 │
│  │ + FTS5 │  scopes never leak between projects             │
│  └────────┘                                                 │
└─────────────────────────────────────────────────────────────┘
```

**Dependency rule:** imports flow inward only. `model` (zero external deps) is the leaf. Frontends (`cli`, `mcp`, `http`) call `service`, which orchestrates `store`, `scoring`, `graph`, and `rules`. No frontend calls `store` or `db` directly.

**Persistence:** two SQLite databases per host -- `~/.mneme/global.db` (global + org scope) and `~/.mneme/projects/<slug>.db` (project scope, slug from git remote). Schema v13 with 13 embedded migrations.

**Three frontends:** MCP (primary, 57 tools over stdio), HTTP (REST API at `:7437`, 10 endpoints under `/v1/` -- no SDD/codegraph/skills/model/conflicts parity yet), and CLI (Cobra, 33 commands).

---

## Status & Roadmap

**Current: v0.8.0** -- 6 EPICs delivered across 198 commits, schema v10, 24 MCP tools.

| EPIC | Theme | Status |
|------|-------|--------|
| EPIC-1 | Rules as first-class citizens | Done |
| EPIC-2 | Weighted graph + 1-hop expansion | Done |
| EPIC-3 | Wikilinks + knowledge gaps | Done |
| EPIC-4 | Personalized PageRank | Done |
| EPIC-5 | Community detection (Louvain) | Done |
| EPIC-6 | Vault mirror + Memory Manifest | Done |
| EPIC-7 | Documentation & discoverability | In progress |

**Follow-ups:** fsnotify vault watcher, MCP vault tools, query DSL, plugin model for subagents, HTTP SDD parity.

---

## Configuration

Config file at `~/.mneme/config.toml` (optional -- all settings have sensible defaults). Environment variable overrides with `MNEME_*` prefix always win.

```bash
mneme config show           # all sections with provenance
mneme config show graph     # just the graph section
```

See [docs/CONFIG.md](docs/CONFIG.md) for the full reference.

## Requirements

- Go 1.24+
- CGO-enabled C compiler (for SQLite FTS5)
- Build tag: `-tags fts5`

```bash
make build         # CGO_ENABLED=1 go build -tags fts5 -o mneme ./cmd/mneme
make test          # go test -tags fts5 ./...
make install       # build + sudo cp to /usr/local/bin/
make setup         # install + mneme install claude-code
```

## Contributing

1. Fork the repo
2. Create a feature branch (`type/short-description`)
3. Follow [Conventional Commits](https://www.conventionalcommits.org/) for commit messages
4. Run `make test-race` and `golangci-lint run` (zero warnings required)
5. Open a PR

## License

[Apache License 2.0](LICENSE)
