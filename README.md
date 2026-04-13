# mneme

Persistent memory system for AI coding agents. Single Go binary, zero runtime dependencies. Full-text search (BM25), knowledge graph, automatic consolidation, multi-scope memory, and agent-agnostic access via MCP.

## Why

AI coding agents forget everything between sessions. CLAUDE.md files become battlegrounds mixing config, conventions, and knowledge. What you learn in one project is invisible in another.

mneme fixes this. It stores structured observations (decisions, discoveries, patterns, conventions) in a local SQLite database and exposes them through MCP, HTTP API, and CLI. Any agent that speaks MCP can save and retrieve persistent knowledge.

## Quick Start

```bash
# Build
CGO_ENABLED=1 go build -tags fts5 -o mneme ./cmd/mneme

# Save a memory
mneme save -t "Auth uses JWT with RS256" -T decision -c "## What
Switched to RS256 for JWT signing.

## Why
Legal requires asymmetric key verification for compliance."

# Search memories
mneme search "authentication"

# Get project context (what an agent receives at session start)
mneme context --budget 4000

# Start MCP server (for agent integration)
mneme mcp

# Start HTTP API
mneme serve --addr :7437
```

## Features

### Memory Types
- **decision** — Architectural choices and their rationale
- **discovery** — Things learned during development
- **bugfix** — Bugs found and how they were resolved
- **pattern** — Reusable code patterns and approaches
- **convention** — Team or project conventions
- **architecture** — System design and structure
- **preference** — Personal workflow preferences
- **config** — Configuration knowledge
- **session_summary** — Auto-generated session recaps

### Multi-Scope Memory
- **global** — Your preferences and skills, available in every project
- **org** — Team conventions shared across projects
- **project** — Project-specific knowledge (architecture, decisions, bugs)

Global memories live in `~/.mneme/global.db`. Project memories live in `~/.mneme/projects/<slug>.db`. Scopes never leak between projects.

### Knowledge Graph
Entities (modules, services, patterns, files) connected by typed relations (depends_on, implements, uses, conflicts_with). Enables queries like "what depends on the auth service?"

### Automatic Consolidation
Background pipeline that keeps memory healthy:
- **Decay** — Old, unused memories fade based on configurable rates
- **Dedup** — Duplicate memories are detected and merged
- **Budget** — When memory exceeds limits, lowest-scored entries are evicted
- No manual curation needed.

### Git Sync
Export memories as compressed JSONL for version-controlled sharing:
```bash
mneme sync export          # → .mneme/sync/project.jsonl.gz
mneme sync import file.gz  # merge into local DB
```

## Interfaces

### MCP Server (primary — for AI agents)
```bash
mneme mcp --tools=agent
```
11 tools: `mem_save`, `mem_search`, `mem_get`, `mem_context`, `mem_update`, `mem_session_end`, `mem_suggest_topic_key`, `mem_relate`, `mem_timeline`, `mem_stats`, `mem_forget`

### HTTP API
```bash
mneme serve --addr :7437
```
RESTful endpoints at `/v1/memories/*`, `/v1/sessions/*`, `/v1/entities/*`, `/v1/stats`, `/v1/health`

### CLI
```
mneme save        Save a memory
mneme search      Search memories (BM25 full-text)
mneme get         Retrieve a memory by ID
mneme status      Show project and memory stats
mneme stats       Detailed statistics
mneme forget      Mark a memory for accelerated decay
mneme consolidate Run consolidation manually
mneme sync        Export/import memories for git sharing
mneme serve       Start HTTP API server
mneme mcp         Start MCP server
mneme version     Print version
```

## Configuration

Config file at `~/.mneme/config.toml` (optional — all settings have sensible defaults):

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
architecture = 0.005  # slow — stable knowledge
decision = 0.005
convention = 0.005
pattern = 0.01         # medium
bugfix = 0.02          # faster — specifics fade
session_summary = 0.05 # fast — recent sessions matter
```

Environment variable overrides: `MNEME_DATA_DIR`, `MNEME_PROJECT`, `MNEME_LOG_LEVEL`.

## Architecture

```
cmd/mneme/          → entrypoint
internal/
  model/            → domain types (zero external deps)
  project/          → git remote detection
  config/           → TOML config with defaults
  db/               → SQLite + FTS5 + migrations
  store/            → repository pattern (CRUD, search, entities, stats)
  scoring/          → importance, decay, relevance, RRF fusion
  service/          → business logic orchestration
  consolidation/    → background decay, dedup, budget enforcement
  mcp/              → MCP server (JSON-RPC 2.0 over stdio)
  http/             → REST API (stdlib net/http)
  sync/             → JSONL.gz export/import
  cli/              → cobra commands
```

Dependencies flow inward: `cli/mcp/http → service → store → db → model`. Model is the leaf with zero external imports.

## Requirements

- Go 1.24+
- CGO-enabled C compiler (for SQLite FTS5)
- Build tag: `-tags fts5`

## License

MIT
