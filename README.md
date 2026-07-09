# mneme

Persistent memory for AI coding agents -- with a spec-driven workflow engine, semantic code graph, and role enforcement built on top. One binary, zero runtime dependencies.

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8.svg)](https://go.dev)
[![Release](https://img.shields.io/github/v/release/wirvii/mneme?label=release)](https://github.com/wirvii/mneme/releases)
[![MCP Tools](https://img.shields.io/badge/MCP%20tools-64-green.svg)](#mcp-tools)

---

## Table of Contents

- [What is mneme?](#what-is-mneme)
- [Why mneme?](#why-mneme)
- [The 4 Layers](#the-4-layers)
- [Quickstart](#quickstart)
- [Features](#features)
- [CodeGraph](#codegraph)
- [Skills](#skills)
- [Subagents](#subagents)
- [Team Memory](#team-memory)
- [Enforcement](#enforcement)
- [Models & Conflicts](#models--conflicts)
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

mneme gives AI coding agents a brain that survives between sessions. It stores structured knowledge -- decisions, patterns, rules, conventions, architecture -- in a local SQLite database with full-text search, a weighted knowledge graph, and automatic consolidation. Any MCP-compatible agent (Claude Code, Cursor, Windsurf, OpenCode, Gemini CLI) can save and retrieve persistent memory through 64 tools over JSON-RPC stdio.

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
# 1. Install (recommended — requires Go 1.24+, no C compiler needed)
go install github.com/wirvii/mneme/cmd/mneme@latest

# Alternative: download a pre-built binary (no Go toolchain required)
curl -sSL https://raw.githubusercontent.com/wirvii/mneme/main/install.sh | sh

# Alternative: build from source
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

`mneme install claude-code` registers MCP tools, session hooks, and the rules enforcement hook in `~/.claude/settings.json`, plus six bundled subagent profiles (a transitional global set -- see [Subagents](#subagents) below for the per-project model that is replacing them). After install, the agent automatically loads context at session start, saves session summaries at end, and evaluates rules before every file edit. `mneme install codex` wires the same MCP server and memory protocol into OpenAI Codex CLI's single-agent model -- see [docs/codex.md](docs/codex.md) for the differences.

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

Six pre-configured subagent profiles (architect, backend, frontend, qa-tester, bug-hunter, diagnostician) ship globally with `mneme install claude-code` and integrate with the SDD lifecycle; the orchestrator role is the principal, not an installed profile. See [Subagents](#subagents) for the newer per-project generation model and [Enforcement](#enforcement) for the role/capability model.

### Memory Manifest

Portable interchange format (`.manifest.tar.gz`) defined by JSON Schema 2020-12. Exports memories, entities, relations, and sessions for full-fidelity transfer between machines or third-party tools.

```bash
mneme sync export --format manifest   # full-fidelity archive
mneme sync export                     # legacy JSONL.gz (memories only)
mneme sync import <file>              # auto-detects format
```

---

## CodeGraph

A semantic code graph, separate from the memory graph, that indexes source files
into symbols (functions, types, methods) and relations (`calls`, `imports`,
`extends`, `implements`). It answers questions memory alone cannot: *who calls
this function*, *what breaks if I change this type*, *what's the shortest path
between these two symbols*.

```bash
mneme codegraph index                          # walk cwd, extract symbols (incremental)
mneme codegraph search "MemoryService"         # find symbols by name
mneme codegraph callers SaveMemory --depth 2   # who calls this?
mneme codegraph impact Memory --limit 50       # blast radius of a change
mneme codegraph trace Handler ServiceCall      # shortest call path
mneme codegraph hooks install                  # auto re-index on commit/checkout
```

10 MCP tools (`codegraph_search`, `codegraph_context`, `codegraph_callers`,
`codegraph_callees`, `codegraph_impact`, `codegraph_trace`, `codegraph_explore`,
`codegraph_files`, `codegraph_node`, `codegraph_status`) let agents explore code
structure before falling back to `Read`/`Grep`. A `PreToolUse` nudge reminds
agents to prefer the graph when one is indexed. See
[docs/codegraph.md](docs/codegraph.md) (concepts, coverage caveats, git hooks)
and [docs/api/codegraph.md](docs/api/codegraph.md) (full tool contracts).

---

## Skills

mneme is the **package manager for Claude Code skills** -- it embeds a bundle of
skills and installs them to `~/.claude/skills/`. It does not implement the skill
runtime itself.

```bash
mneme skills list                       # bundled + installed, with lint status
mneme skills install example-skill      # copy to ~/.claude/skills/
mneme skills pin example-skill          # protect from overwrite/removal
mneme skills lint example-skill         # deterministic structural check
mneme skills validate example-skill     # run validation/run.sh (120s timeout)
```

See [docs/skills.md](docs/skills.md) (SKILL.md format, pin semantics) and
[docs/api/skills.md](docs/api/skills.md) (the 7 `skills_*` MCP tools).

---

## Subagents

Subagents used to be global and fixed: six profiles (architect, backend,
frontend, qa-tester, bug-hunter, diagnostician), the same Go/hexagonal/sqlc
template regardless of the project. As of the agnostic-agents EPIC
(SPEC-052), the `mneme-init` skill can instead **generate subagents
per-project**, tailored to that project's own stack, through a conversational
grill:

```bash
# Invoke the mneme-init skill (not a slash command), then walk its grill:
#   0. fingerprint the repo (apps, stack markers) -- deterministic, no LLM
#   1. elicit repo/org knowledge once (commit style, cross-cutting rules)
#   2. propose roles + app<->role mapping, let the user adjust
#   3. draft role x area x stack content for each role
#   4. compose, validate, preview, and write .claude/agents/<role>.md
```

Every generated profile still inherits its `tools:` permission envelope from
a fixed, Go-authored archetype (never LLM-generated) -- a generated `backend`
subagent has exactly the capability boundary of the global one, just a
project-specific system prompt. Projects that generate implementer subagents
can additionally opt into the delegation-enforcement hook at **project
scope** (`mneme delegation-hook enable`), independent of the global
installation.

The global six keep shipping unconditionally with every `mneme install
claude-code` during this transition (a future release, SS-7, retires that
default). See [docs/enforcement-model.md](docs/enforcement-model.md) for the
two-layer detail and [docs/api/subagents.md](docs/api/subagents.md) for the
6 `subagent_*` MCP tools (`mneme subagents` is the CLI counterpart).

---

## Team Memory

Git-native, opt-in knowledge sharing between teammates -- no server, no
account, no network call. Durable memories (decisions, conventions,
architecture, patterns, bugfixes, rules) flow through the repository itself:

```bash
mneme team-memory enable      # creates .mneme/shared/, bakes+exports durable
                               # memories, installs import hooks, prints a
                               # privacy notice (always -- mneme cannot know
                               # offline whether your remote is public)
mneme promote <id>             # explicitly share any single memory (shared=2)
```

Writes are synchronous write-through (`mem_save`/`mem_update` materialize to
`.mneme/shared/notes/<uuid>.md` immediately when active); reads happen via
idempotent `post-merge`/`post-checkout` git hooks that import teammates'
notes in the background. Semantic conflicts get a deterministic FTS5 count
on import; judging them stays the same manual `mneme conflicts scan` step.
See [docs/team-memory.md](docs/team-memory.md) for the full model
(what's shared, conflicts, privacy) and [docs/api/memory.md](docs/api/memory.md)
for `mem_promote`.

---

## Enforcement

Two independent layers keep agent roles honest:

1. **Capability allowlist (primary).** Every subagent declares an explicit
   `tools:` allowlist in its YAML frontmatter. Read-only agents physically
   cannot call `Edit`/`Write`/`MultiEdit`/`NotebookEdit` -- Claude Code enforces
   this natively, independent of what the prompt says.
2. **`PreToolUse` hook (defense in depth).** `mneme hook pre-tool-use` evaluates
   role-aware rules against every mutating tool call; a bash hook additionally
   blocks the orchestrator from editing protected paths directly, by detecting
   the absence of an `agent_id` in the hook payload.

| Role | Edits code? | Notes |
|------|-------------|-------|
| `orchestrator` | ❌ Never | Routes work, manages the SDD lifecycle; blocked by the bash hook even if a rule doesn't cover the path |
| `architect` | ❌ Read-only | Designs, writes specs |
| `backend` | ✅ Full toolset | Implements server-side logic |
| `frontend` | ✅ Full toolset | Implements client-side logic |
| `bug-hunter` | ✅ Full toolset | Investigates and fixes bugs |
| `qa-tester` | ❌ Read-only | Verifies implementation against the spec |
| `diagnostician` | ⚠️ Bash only, no Edit/Write | Reads logs/infra, triages, proposes -- never mutates code |

See [docs/enforcement-model.md](docs/enforcement-model.md) for the full
allowlist table, the role-aware rules engine, and how to add a new subagent.

---

## Models & Conflicts

**Models** -- each bundled subagent gets a model alias (`architect` defaults to
`opus`; the rest default to `sonnet`), stored in `~/.mneme/config.toml` and
applied to agent files on every `mneme install claude-code`. Override with
`mneme model set <agent> <alias>`. See [docs/models.md](docs/models.md) and
[docs/api/models.md](docs/api/models.md).

**Conflicts** -- a two-phase workflow surfaces contradictions between memories:
deterministic FTS5 candidate detection (`mneme conflicts candidates`, no LLM),
then optional LLM judgment via a `claude` CLI subprocess ($0 cost on
subscription, dry-run by default -- `mneme conflicts scan --apply` to persist).
See [docs/conflicts.md](docs/conflicts.md) and
[docs/api/conflicts.md](docs/api/conflicts.md).

---

## Commands

36 top-level commands (`mneme --help` is the source of truth for flags; full
flag reference in [docs/api/cli.md](docs/api/cli.md)). Cobra's
auto-generated `completion` is listed below for reference but not counted in
that figure:

| Command | Description |
|---------|-------------|
| `mneme save` | Save a memory |
| `mneme search` | Search memories (BM25 + vector + graph) |
| `mneme get` | Retrieve a memory by ID |
| `mneme update` | Update an existing memory |
| `mneme forget` | Mark a memory for accelerated decay |
| `mneme promote` | Mark a memory as team-curated (`shared=2`) |
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
| `mneme subagents` | Generate and manage per-project subagent profiles (`fingerprint`, `profile`, `compose`, `write`, `manifest-list`) |
| `mneme delegation-hook` | Register/inspect the project-scoped opt-in delegation-enforcement hook (`enable`, `disable`, `status`) |
| `mneme team-memory` | Manage git-native team memory sharing (`enable`, `hooks install\|remove`) |
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

The MCP server (`mneme mcp`) exposes **64 tools** over JSON-RPC 2.0 stdio,
grouped by family. Each family has a full contract reference (params, returns,
errors, examples) under [docs/api/](docs/api/):

| Family | Count | What it covers | Reference |
|--------|-------|-----------------|-----------|
| `mem_*` | 15 | Save, search, update, relate, explore, promote, and time-travel through memories | [docs/api/memory.md](docs/api/memory.md) |
| `backlog_*` | 4 | Raw idea → refined → promoted-to-spec lifecycle | [docs/api/sdd.md](docs/api/sdd.md) |
| `spec_*` | 8 | Spec lifecycle: draft → speccing → ... → done, plus `quick`/`reject` shortcuts | [docs/api/sdd.md](docs/api/sdd.md) |
| `lane_*` | 5 | Trivial-lane audit, reclassify, override, status, stats | [docs/api/sdd.md](docs/api/sdd.md) |
| `codegraph_*` | 10 | Symbol search, callers/callees, impact analysis, call tracing | [docs/api/codegraph.md](docs/api/codegraph.md) |
| `skills_*` | 7 | Install/pin/lint/validate skills in `~/.claude/skills/` | [docs/api/skills.md](docs/api/skills.md) |
| `model_*` | 3 | Per-agent model alias assignment | [docs/api/models.md](docs/api/models.md) |
| `conflicts_*` | 5 | Detect and manage memory conflict relations | [docs/api/conflicts.md](docs/api/conflicts.md) |
| `init` | 1 | Apply managed blocks + drift report (project bootstrap) | [docs/api/sdd.md](docs/api/sdd.md) |
| `subagent_*` | 6 | Fingerprint, compose, validate, and write per-project subagent profiles | [docs/api/subagents.md](docs/api/subagents.md) |

15 + 4 + 8 + 5 + 10 + 7 + 3 + 5 + 1 + 6 = **64**.

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
| Per-project subagent generation | ✅ `mneme-init` grill | ❌ | N/A | N/A |
| Team memory (git-native sharing) | ✅ Opt-in, no server | ❌ Manual merge | ❌ | ❌ Not designed for teams |
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
| [Team Memory](docs/team-memory.md) | Git-native shared knowledge: what's shared, write-through, import hooks, conflicts, privacy |
| [Enforcement Model](docs/enforcement-model.md) | Two-layer role enforcement, per-project subagent generation, opt-in delegation-hook |
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

**Persistence:** two SQLite databases per host -- `~/.mneme/global.db` (global + org scope) and `~/.mneme/projects/<slug>.db` (project scope, slug from git remote). Schema v14 with 14 embedded migrations.

**Three frontends:** MCP (primary, 64 tools over stdio), HTTP (REST API at `:7437`, 10 endpoints under `/v1/` -- no SDD/codegraph/skills/model/conflicts/subagent parity yet), and CLI (Cobra, 36 commands).

---

## Status & Roadmap

**Current (main): schema v14, 64 MCP tools, 36 CLI commands, 10 HTTP endpoints.**
Last tagged release: **v1.17.0**. Full history in [CHANGELOG.md](CHANGELOG.md).

**Shipped:**

| Milestone | Theme | Status |
|-----------|-------|--------|
| EPIC-1..6 (v0.2.0-v0.8.0) | Rules, weighted graph, wikilinks, PPR, communities, vault mirror | Done |
| SDD engine + lanes (v0.9.0-v1.5.0) | Backlog → spec lifecycle, trivial/standard lanes, deterministic audit | Done |
| CodeGraph C1-C11 (v1.10.0-v1.16.0) | Semantic code graph: Go + TypeScript/JS extraction, cross-file resolution, hook nudges, auto-reindex hooks | Done |
| Skills package manager (v1.7.0) | Bundled skill install/pin/lint/validate | Done |
| Models (v1.8.0) | Per-agent model alias assignment | Done |
| Conflicts (v1.9.0) | FTS5 candidate detection + LLM judgment via `claude` CLI | Done |
| `mneme install codex` (v1.17.0) | Single-agent OpenAI Codex CLI support | Done |
| Agnostic agents (EPIC SPEC-052, SS-1..SS-6) | Per-project subagent generation via `mneme-init` grill, `subagent_*` tools, `mneme subagents`, project-scoped opt-in delegation-hook | Done |
| Team Memory (EPIC SPEC-053, SS-A..SS-F) | Git-native shared knowledge: `shared`/`author` columns (schema v14), write-through, `mneme promote`, import hooks, `mneme team-memory enable` | Done |

**Roadmap (honest gaps):**

- **SS-7 -- retire the global subagent installation.** The six global subagent profiles still ship unconditionally with every `mneme install claude-code` during the agnostic-agents transition. A future release drops that default once per-project generation is the only supported path.
- **CodeGraph C4-C7** -- remaining backlog items (further language coverage, deeper cross-package resolution beyond the current best-effort pass).
- **HTTP parity** -- the REST API has no SDD/codegraph/skills/model/conflicts/subagent routes; MCP and CLI are ahead. Tracked as a deliberate gap, not an oversight (see [docs/api/http.md](docs/api/http.md) "HTTP parity gaps").
- **TypeScript/JS type-inference ceiling** -- the CodeGraph JS/TS extractor resolves calls and imports but does not do full type inference; some dynamically-typed or heavily-generic call sites are not tracked. `codegraph_impact`/`codegraph_callees` are documented as best-effort, not exhaustive.

---

## Configuration

Config file at `~/.mneme/config.toml` (optional -- all settings have sensible defaults). Environment variable overrides with `MNEME_*` prefix always win.

```bash
mneme config show           # all sections with provenance
mneme config show graph     # just the graph section
```

See [docs/CONFIG.md](docs/CONFIG.md) for the full reference.

## Requirements

- Go 1.24+ (`go install github.com/wirvii/mneme/cmd/mneme@latest` is the
  recommended install path — see [Quickstart](#quickstart))
- No C compiler, no CGO, no build tags: SQLite (with FTS5) is provided by the
  pure-Go `modernc.org/sqlite` driver.

```bash
make build         # go build -o mneme ./cmd/mneme
make test          # go test ./...
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
