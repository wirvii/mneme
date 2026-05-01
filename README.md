# mneme

Persistent memory system for AI coding agents. Single Go binary, zero runtime dependencies.

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8.svg)](https://go.dev)
[![CGO](https://img.shields.io/badge/requires-CGO%20%2B%20fts5-orange.svg)](#requirements)

---

## What is mneme?

mneme stores structured knowledge (decisions, discoveries, patterns, conventions, rules) in a local SQLite database and exposes it through MCP, HTTP, and CLI. Any AI agent that speaks MCP can save and retrieve persistent, cross-session memory. It also provides a weighted knowledge graph, rules engine, and a spec-driven development (SDD) lifecycle.

## Why

AI coding agents forget everything between sessions. CLAUDE.md files become battlegrounds mixing config, conventions, and knowledge. What you learn in one project stays invisible in another.

mneme fixes this with four layers:

| Layer | What it does |
|-------|-------------|
| **Storage** | SQLite multi-scope (global/project), FTS5 full-text search, UUIDv7 IDs |
| **Graph** | Weighted knowledge graph with Hebbian auto-strengthening, edge decay, and co-mention backfill |
| **Retrieval** | RRF fusion of BM25 + vector + graph; rules always injected in context |
| **SDD Engine** | backlog --> spec --> architect --> backend --> qa lifecycle |

## Quick Start

```bash
# Install from source
git clone https://github.com/wirvii/mneme.git && cd mneme
make install

# Or use the install script
curl -sSL https://raw.githubusercontent.com/wirvii/mneme/main/install.sh | sh

# Configure your agent (adds hooks to ~/.claude/settings.json)
mneme install claude-code

# Save your first memory
mneme save --type architecture --title "Auth uses JWT with RS256" \
  --content "## What\nSwitched to RS256 for JWT signing.\n\n## Why\nLegal requires asymmetric key verification."

# Search memories
mneme search "authentication"

# Get project context (what the agent receives at session start)
mneme context --budget 4000
```

## How It Works

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

## Features

### Memory Types

10 built-in types with per-type decay rates and importance defaults:

| Type | Decay | Purpose |
|------|-------|---------|
| `architecture` | 0.005/d | System design and structure |
| `decision` | 0.005/d | Architectural choices and rationale |
| `convention` | 0.005/d | Team or project conventions |
| `pattern` | 0.01/d | Reusable code patterns |
| `preference` | 0.01/d | Personal workflow preferences |
| `bugfix` | 0.02/d | Bugs found and how they were resolved |
| `discovery` | 0.02/d | Things learned during development |
| `config` | 0.02/d | Configuration knowledge |
| `session_summary` | 0.05/d | Auto-generated session recaps |
| `rule` | 0 (immune) | Binding constraints with `applies_to` and `severity` |

### Multi-Scope Memory

- **global** -- Your preferences and skills, available in every project (`~/.mneme/global.db`)
- **org** -- Team conventions shared across projects
- **project** -- Project-specific knowledge (`~/.mneme/projects/<slug>.db`)

Scopes never leak between projects.

### Knowledge Graph (SPEC-005..009)

Entities (modules, services, patterns, files) connected by weighted, directed relations. 8 relation types with default weights:

| Relation | Default Weight |
|----------|---------------|
| `depends_on` | 0.9 |
| `part_of` | 0.85 |
| `implements` | 0.8 |
| `uses` | 0.7 |
| `conflicts_with` | 0.7 |
| `supersedes` | 0.6 |
| `related_to` | 0.5 |
| `references` | 0.4 |

**Hebbian auto-strengthening:** when two memories are accessed together, the edge between them is reinforced automatically (ring buffer + async worker pool).

**Edge decay:** relations not traversed for 30 days decay at 0.02/day. Actively used edges stay strong.

**Graph rebuild:** `mneme graph rebuild` backfills the graph from existing memories using 4 heuristics (topic_key, file paths, code symbols, wikilinks).

### Retrieval: 3-Channel RRF Fusion (SPEC-007)

```
Query ──┬──> FTS5 BM25 ──────────> Rank A (weight 1.0)
        │
        ├──> Vector similarity ───> Rank B (weight 0.8)
        │
        ├──> 1-hop graph expand ──> Rank C (weight 0.6)
        │
        └──> RRF Fusion ──> Final ranking ──> Return
```

### Rules System (SPEC-001..004)

Rules are memories of type `rule` with `applies_to` patterns and a `severity` level:

- **info** -- advisory, agent should consider
- **warn** -- explicit warning, action proceeds
- **block** -- action rejected (hook exits with code 2)

Rules are always injected in `mem_context` output (separate token budget). The `pre-tool-use` hook evaluates rules JIT before every Edit/Write/MultiEdit.

```bash
# Create a rule
mneme rule add --title "No vendor edits" \
  --content "Never edit vendor/ files." \
  --applies-to "vendor/**" \
  --severity block

# Test which rules would fire
mneme rule test --tool Edit --path vendor/foo/bar.go
```

### Automatic Consolidation

Background pipeline that keeps memory healthy:
- **Decay** -- old, unused memories fade based on configurable rates
- **Dedup** -- duplicate memories detected and merged
- **Budget** -- when memory exceeds limits, lowest-scored entries are evicted
- **Edge decay** -- graph relations not traversed in 30d lose weight

### SDD Engine

Spec-driven development lifecycle powered by mneme:

```
backlog_add --> backlog_refine --> backlog_promote --> spec_new
  --> spec_advance (draft --> speccing --> specced --> implementing --> qa --> done)
```

### Git Sync

```bash
mneme sync export          # --> .mneme/sync/project.jsonl.gz
mneme sync import file.gz  # merge into local DB
```

## CLI Commands

### Core Memory

```
mneme save          Save a memory
mneme search        Search memories (BM25 + vector + graph)
mneme get           Retrieve a memory by ID
mneme context       Get project context (agent session start)
mneme update        Update an existing memory
mneme forget        Mark a memory for accelerated decay
mneme stats         Detailed statistics
mneme status        Show project and memory status
mneme consolidate   Run consolidation manually
```

### Rules (SPEC-001..004)

```
mneme rule add      Create a rule with applies_to + severity
mneme rule list     Display all active rules (colour-coded table)
mneme rule test     Evaluate rules against a simulated invocation
```

### Graph (SPEC-005..009)

```
mneme explore       BFS traversal from a seed memory (ASCII tree or JSON)
mneme graph rebuild Backfill graph from existing memories
```

### Hooks

```
mneme hook session-start     Load context at session start
mneme hook session-end       Remind agent to save session summary
mneme hook pre-tool-use      Evaluate rules JIT before file edits
mneme hook enforce-delegation  Legacy delegation (deprecated)
```

### Integration

```
mneme mcp           Start MCP server (stdio, JSON-RPC 2.0)
mneme serve         Start HTTP API server (:7437)
mneme install       Configure agent profiles (claude-code)
```

### SDD Lifecycle

```
mneme backlog       Manage backlog items (add/list)
mneme spec          Manage specs (status/advance/list)
mneme init          Migrate legacy projects to SDD engine
```

### Utilities

```
mneme sync          Export/import memories (JSONL.gz)
mneme upgrade       Check for and install updates
mneme export        Export memories to markdown
mneme version       Print version
```

## MCP Tools (23 tools)

The MCP server (`mneme mcp`) exposes 23 tools over JSON-RPC 2.0 stdio:

### Memory Tools (13)

| Tool | Description |
|------|-------------|
| `mem_save` | Save a memory (supports type `rule` with `applies_to` and `severity`) |
| `mem_search` | Hybrid search with optional graph expansion (`include_graph` flag) |
| `mem_get` | Retrieve full content by ID |
| `mem_context` | Curated context bundle with rules always injected |
| `mem_update` | Partial update of an existing memory |
| `mem_session_end` | End session and save summary |
| `mem_suggest_topic_key` | Suggest a topic_key for dedup |
| `mem_relate` | Create/update entity relations (with explicit weight override) |
| `mem_timeline` | Chronological neighborhood around a point in time |
| `mem_stats` | Aggregate statistics |
| `mem_checkpoint` | Save work-in-progress checkpoint |
| `mem_forget` | Mark for accelerated decay |
| `mem_explore` | BFS graph traversal from a seed (depth/budget/threshold) |

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
| `spec_status` | Get full spec status with history |
| `spec_advance` | Advance spec to next lifecycle state |
| `spec_pushback` | Register blocking questions |
| `spec_resolve` | Resolve a pushback |
| `spec_list` | List specs (filterable by status) |

## Documentation

- [Architecture](docs/ARCHITECTURE.md) -- layered design, graph layer, rules system
- [Rules System](docs/RULES.md) -- applies_to syntax, severity, hooks, examples
- [Knowledge Graph](docs/GRAPH.md) -- weights, Hebbian learning, decay, rebuild
- [Hooks Integration](docs/HOOKS.md) -- session hooks, pre-tool-use, migration
- [Technical Spec](docs/SPEC.md) -- v0.1 original specification

## Comparison

| Feature | mneme | CLAUDE.md | RAG on vector DB |
|---------|-------|-----------|-----------------|
| Persists across sessions | Yes | Yes (manual) | Yes |
| Structured types | 10 types + rules | Free-form text | Chunks |
| Cross-project memory | Yes (scopes) | No | Depends |
| Knowledge graph | Weighted + Hebbian | No | No |
| Auto-decay | Per-type rates | Manual cleanup | No |
| Rules enforcement | JIT hook + severity | Static text | No |
| Agent-agnostic | MCP + HTTP + CLI | Claude only | Varies |
| Local-only | SQLite, no cloud | File | Usually cloud |
| SDD lifecycle | Built-in | No | No |

## Status & Roadmap

**Current:** v0.2.0 -- EPIC-1 (Rules) + EPIC-2 (Graph) complete.

Planned EPICs:
- **EPIC-3:** Wikilinks -- `[[topic_key]]` references in memory content
- **EPIC-4:** Personalized PageRank -- global importance scoring via PPR
- **EPIC-5:** Community detection -- automatic memory clustering
- **EPIC-6:** Vault mirror -- bidirectional sync with Obsidian
- **EPIC-7:** Deep documentation -- comprehensive docs and guides

## Configuration

Config file at `~/.mneme/config.toml` (optional -- all settings have sensible defaults):

```toml
[storage]
data_dir = "~/.mneme"
project_budget = 1000
global_budget = 200

[search]
default_limit = 10
preview_length = 300

[context]
default_budget = 4000
include_global = true
global_min_importance = 0.7

[consolidation]
enabled = true
interval = "6h"
retention_days = 30

[decay]
architecture = 0.005
decision = 0.005
convention = 0.005
pattern = 0.01
bugfix = 0.02
session_summary = 0.05

[graph]
hebbian_window = 5
hebbian_increment = 0.05
hebbian_initial_weight = 0.1
edge_decay_rate = 0.02
edge_decay_after_days = 30
expansion_enabled = true
expansion_threshold = 0.3
expansion_fan_out_cap = 50
expansion_seed_top_k = 10
```

Environment variable overrides: `MNEME_DATA_DIR`, `MNEME_PROJECT`, `MNEME_LOG_LEVEL`.

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

MIT
