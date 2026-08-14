<p align="center"><img src="../assets/logo.svg" width="56" alt="mneme"></p>

# mneme -- End-to-End User Guide

A practical guide for humans and AI agents working with mneme. Each section explains a concept, shows when to use it, and provides copy-pasteable examples with expected output.

For deep dives, follow the links to the reference docs ([ARCHITECTURE.md](ARCHITECTURE.md), [RULES.md](RULES.md), [GRAPH.md](GRAPH.md), [HOOKS.md](HOOKS.md), [VAULT.md](VAULT.md), [OBSIDIAN.md](OBSIDIAN.md), [CONFIG.md](CONFIG.md), [MEMORY-MANIFEST.md](MEMORY-MANIFEST.md)).

---

## Table of Contents

1. [Core Concepts](#1-core-concepts)
2. [Saving Memories Well](#2-saving-memories-well)
3. [Searching and Retrieving](#3-searching-and-retrieving)
4. [Rules](#4-rules)
5. [Wikilinks](#5-wikilinks)
6. [The Knowledge Graph](#6-the-knowledge-graph)
7. [Communities and Synthesis](#7-communities-and-synthesis)
8. [Sessions](#8-sessions)
9. [Vault and Obsidian](#9-vault-and-obsidian)
10. [Sync and Backup](#10-sync-and-backup)
11. [Agent Ecosystem](#11-agent-ecosystem)
12. [CLI Cheatsheet](#12-cli-cheatsheet)
13. [MCP Tools Cheatsheet](#13-mcp-tools-cheatsheet)
14. [Troubleshooting](#14-troubleshooting)
15. [FAQ](#15-faq)

---

## 1. Core Concepts

### Memory types (11)

Every memory has a `type` that controls its default importance and decay rate.

| Type | Decay | Purpose | Example |
|------|-------|---------|---------|
| `decision` | 0.005 | Architectural or technical choices | "Auth uses JWT RS256" |
| `discovery` | 0.02 | Learned facts about code, APIs, tools | "FTS5 requires CGO" |
| `bugfix` | 0.02 | Bug and its resolution | "Off-by-one in pagination fixed" |
| `pattern` | 0.01 | Recurring design/implementation pattern | "Repository pattern for all stores" |
| `preference` | 0.005 | Personal or team style preferences | "Always use named returns in Go" |
| `convention` | 0.005 | Naming, formatting, structural conventions | "Conventional Commits for all repos" |
| `architecture` | 0.005 | High-level structure and components | "Layered: CLI > service > store > db" |
| `config` | 0.01 | Configuration values, endpoints, env settings | "Prod DB on port 5433" |
| `session_summary` | 0.05 | Synthetic session-end summary (fast decay) | Auto-generated at session end |
| `rule` | **0.0** | Binding constraint with `applies_to` + `severity` | "Never edit vendor/" |
| `synthesis` | **0.0** | Auto-generated community summary | Created by consolidation |

**When to choose:** If it is a choice you made, use `decision`. If you found out how something works, use `discovery`. If it defines how things *should* be done, use `convention`. If you want it enforced by hooks, use `rule`.

### Scopes (3)

| Scope | Database | Visibility |
|-------|----------|------------|
| `global` | `~/.mneme/global.db` | Every project on this machine |
| `org` | `~/.mneme/global.db` | Shared across org projects |
| `project` | `~/.mneme/projects/<slug>.db` | This project only |

**When to use each:**
- `global` -- personal preferences, reusable patterns, cross-project rules.
- `project` (default) -- architecture decisions, conventions, discoveries specific to this codebase.
- `org` -- team conventions shared across multiple repositories.

Scopes never leak. A project-scoped search never returns global memories (and vice versa), unless `mem_context` explicitly includes global memories above an importance threshold.

### topic_key

A stable, slash-delimited key that enables **idempotent upserts**. If you save a memory with a `topic_key` that already exists, the existing memory is updated instead of creating a duplicate.

```
architecture/auth-model
convention/error-handling
config/database-urls
spec/SPEC-001-rules-design
```

**Why it matters:** Without a `topic_key`, each `mem_save` creates a new record. With one, knowledge evolves in place. If a decision changes, save with the same `topic_key` and the old content is replaced.

### importance and confidence

- **importance** (0.0--1.0): How relevant this memory is for context retrieval. Higher values are packed first into `mem_context`.
- **confidence** (0.0--1.0): How reliable this knowledge is. Use lower values for hypotheses or unverified information.

Both decay over time (per the type's `decay_rate`) unless the memory is accessed, which resets the decay clock.

---

## 2. Saving Memories Well

### Good vs bad topic_keys

| Good | Bad | Why |
|------|-----|-----|
| `architecture/auth-model` | `auth` | Too vague -- clashes with other auth memories |
| `convention/error-handling` | `errors` | No category prefix -- ambiguous |
| `bugfix/pagination-off-by-one` | `fix1` | Not descriptive -- useless for search |
| `config/database-urls` | `config` | Category alone -- will collide immediately |
| `decision/use-jwt-rs256` | `jwt decision made today` | Spaces and temporal reference -- fragile |

**Pattern:** `category/specific-description` using lowercase and hyphens.

### CLI example

```bash
mneme save \
  --type decision \
  --title "Auth uses JWT RS256" \
  --content "Switched from HS256 to RS256 for asymmetric key verification. \
Public key distributed to API gateway, private key stays in auth service." \
  --topic-key "decision/auth-jwt-rs256" \
  --importance 0.9

# Expected output:
# Saved memory 019de100-abcd-7fff-8000-000000000001 (created)
```

### MCP example (for agents)

```json
{
  "method": "tools/call",
  "params": {
    "name": "mem_save",
    "arguments": {
      "title": "Auth uses JWT RS256",
      "content": "Switched from HS256 to RS256...",
      "type": "decision",
      "topic_key": "decision/auth-jwt-rs256",
      "importance": 0.9,
      "files": ["internal/auth/jwt.go"]
    }
  }
}
```

### When NOT to save

- Trivial observations that have no reuse value.
- Information that will change within minutes (use `mem_checkpoint` instead).
- Exact code snippets -- save the *decision* or *pattern*, not the code itself.

### Using `mem_suggest_topic_key`

If you are unsure what key to use, ask mneme:

```json
mem_suggest_topic_key({ "title": "Error handling strategy for API routes" })
```

It returns scored suggestions from existing keys (Jaccard similarity) and from knowledge gaps (unresolved wikilinks). A gap match means the project already expects this key.

---

## 3. Searching and Retrieving

mneme has three retrieval tools. Use this decision tree:

```
What do I need?
  |
  |-- "Start a session / load project context"
  |     -> mem_context
  |
  |-- "Find specific knowledge by keywords"
  |     -> mem_search
  |
  |-- "Explore connections from a known memory"
  |     -> mem_explore
  |
  |-- "See what happened around a point in time"
        -> mem_timeline
```

### mem_context -- Session bootstrap

Use at the start of every session. Returns a curated bundle of the most relevant memories, with rules always injected first.

```bash
mneme context --budget 4000 --focus "authentication"

# Expected output:
# <!-- mneme:context:start -->
# # mneme -- Session Context
#
# ## Active Rules (2 rules, ~320 tokens)
# ### [BLOCK] No vendor edits
# ...
# ## Loaded Memories (12 of 142)
# ...
# <!-- mneme:context:end -->
```

**MCP:**

```json
mem_context({ "budget": 4000, "focus": "database migrations" })
```

The `focus` parameter biases selection toward memories matching that topic (via FTS5 + vector + graph expansion). Without focus, memories are ranked by effective importance.

**When to use:** At session start (automatic via hooks), after context compaction, or when switching topics mid-session.

### mem_search -- Keyword search

Hybrid search that fuses three channels via RRF: BM25 text matching, vector similarity, and 1-hop graph expansion.

```bash
mneme search "database connection pooling"

# Expected output:
# 1. [architecture] Database connection setup (0.92)
#    Relevance: 12.4 | Topic: architecture/db-connection
#    Preview: PostgreSQL connection pool configured with max 20...
#
# 2. [decision] Use pgxpool for connection management (0.85)
#    ...
```

**MCP:**

```json
mem_search({
  "query": "authentication middleware",
  "type": "decision",
  "limit": 5,
  "include_graph": true
})
```

**When to use:** Looking for specific knowledge. The `include_graph` flag (default true) adds topologically related memories even if they do not contain the search terms.

**When NOT to use:** To load broad context at session start (use `mem_context`) or to trace connections from a specific memory (use `mem_explore`).

### mem_explore -- Graph traversal

Starting from a seed memory, performs a prioritized BFS following strong graph relations.

```bash
mneme explore "architecture/auth-model" --depth 2

# Expected output:
# architecture/auth-model [seed]
# |-- JWT token rotation (depends_on, w=0.90, 245 tok)
# |   |-- Key management policy (uses, w=0.63, 180 tok)
# |   \-- Session invalidation flow (related_to, w=0.45, 320 tok)
# |-- OAuth2 provider config (implements, w=0.80, 156 tok)
# \-- Auth middleware setup (part_of, w=0.85, 210 tok)
#
# Total: 5 memories | 1111 tokens | 2 levels
```

**Seed resolution:** Full UUID, hex prefix (8+ chars), or `topic_key`.

**MCP:**

```json
mem_explore({
  "seed": "architecture/auth-model",
  "depth": 3,
  "budget": 8000,
  "threshold": 0.2
})
```

**When to use:** Understanding what is connected to a specific piece of knowledge. Debugging the graph. Building deep context for a specific module.

### mem_timeline -- Temporal neighborhood

```json
mem_timeline({ "around": "2026-04-28T12:00:00Z", "window": "3d", "limit": 10 })
```

Returns memories created around a specific point in time. Useful for reconstructing "what happened last Tuesday."

---

## 4. Rules

Rules are memories that are **actively enforced**. They have `applies_to` patterns and a `severity` level. See [RULES.md](RULES.md) for the full reference.

### Creating a rule

```bash
# Block severity -- rejects tool calls with exit code 2
mneme rule add \
  --title "No vendor edits" \
  --content "Never edit vendor/ files. They are managed by dependency tools." \
  --applies-to "vendor/**" \
  --severity block

# Expected output:
# Rule saved: 019de100-... "No vendor edits" [BLOCK]
```

```bash
# Warn severity -- advisory, agent proceeds but sees the warning
mneme rule add \
  --title "Always wrap errors" \
  --content "Use fmt.Errorf(\"context: %w\", err). Never swallow errors." \
  --applies-to "**/*.go" \
  --severity warn \
  --scope global
```

### Testing a rule

```bash
mneme rule test --tool Edit --path vendor/foo/bar.go

# Expected output:
# Testing: tool=Edit    path=vendor/foo/bar.go
#
# Evaluated: 3 rules
# Matched:   1 rules
#
#   [BLOCK] No vendor edits
#          Never edit vendor/ files. They are managed by dependency tools.
#          Matched by: vendor/**
#
# Effective severity: block
# Result: BLOCKED
```

### Pattern syntax quick reference

| Pattern | Matches |
|---------|---------|
| `**` | Everything (global wildcard) |
| `tool:Edit` | Specific tool (case-sensitive) |
| `internal/**/*.go` | Path glob with doublestar |
| `tool:Edit+internal/**` | AND: tool AND path must match |
| `!docs/**` | Negation: vetoes the rule when matched |
| `["**", "!docs/**"]` | Everything except docs/ |

### Severity guide

| Severity | Hook behavior | When to use |
|----------|---------------|-------------|
| `info` | Exit 0, shows reminder | Gentle guidance, tips |
| `warn` | Exit 0, shows warning | Conventions the agent should consider |
| `block` | **Exit 2**, tool call rejected | Absolute prohibitions |

### Examples by stack

**Go:**
```bash
mneme rule add -t "Protect generated code" \
  -c "Files with _gen.go suffix are auto-generated. Do not edit." \
  -a "**/*_gen.go" -s block
```

**TypeScript/Next.js:**
```bash
mneme rule add -t "Components must use API routes" \
  -c "React components must never call Prisma or DB directly." \
  -a "tool:Edit+src/components/**" -s warn
```

**Python:**
```bash
mneme rule add -t "Do not edit migrations manually" \
  -c "Use alembic to generate migrations." \
  -a "alembic/versions/**" -s block
```

---

## 5. Wikilinks

`[[topic_key]]` references in memory content are parsed on save and automatically create graph relations. This is the lowest-friction way to build the knowledge graph.

### Syntax

| Form | Example |
|------|---------|
| `[[topic_key]]` | `[[architecture/auth-model]]` |
| `[[topic_key#anchor]]` | `[[architecture/auth-model#jwt-section]]` |
| `[[topic_key\|alias]]` | `[[architecture/auth-model\|the auth design]]` |

### Example

```json
mem_save({
  "title": "Auth middleware setup",
  "content": "Implements the flow defined in [[architecture/auth-model]]. Uses [[convention/error-codes]] for error responses.",
  "topic_key": "impl/auth-middleware",
  "type": "decision"
})
```

After saving, `mem_explore("impl/auth-middleware")` returns both referenced memories at distance 1 with weight 0.6.

### Unresolved references (knowledge gaps)

If a wikilink target does not exist yet, it is tracked in the `unresolved_references` table. When a memory with that `topic_key` is saved later, the link is auto-resolved.

Use `mem_gaps` to see what is missing:

```json
mem_gaps({ "limit": 10, "min_mentions": 2 })
```

**When to use wikilinks:**
- When referencing another memory you know exists (or should exist).
- To create implicit "see also" connections without calling `mem_relate`.

**When NOT to use:**
- In code blocks (they are automatically skipped by the parser).
- For references to external URLs or files (wikilinks resolve against `topic_key` only).

---

## 6. The Knowledge Graph

The graph connects entities (nodes) via weighted directed relations (edges). Three mechanisms build it automatically. See [GRAPH.md](GRAPH.md) for the full reference.

### How edges are created

1. **Explicit:** `mem_relate` creates a specific relation with a chosen type and weight.
2. **Wikilinks:** `[[topic_key]]` in content creates `references` relations (weight 0.6).
3. **Hebbian:** Memories accessed together in the same session window have their entities' relations auto-strengthened.
4. **Graph rebuild:** `mneme graph rebuild` backfills the graph from existing memories.

### Relation types and weights

| Type | Weight | When to use |
|------|--------|-------------|
| `depends_on` | 0.9 | A depends on B (critical structural link) |
| `part_of` | 0.85 | A is a component of B |
| `implements` | 0.8 | A implements B (interface/contract) |
| `uses` | 0.7 | A uses/calls B |
| `conflicts_with` | 0.7 | A conflicts with B |
| `supersedes` | 0.6 | A replaces B |
| `related_to` | 0.5 | Generic association |
| `references` | 0.4 | Weak reference (auto-created by wikilinks at 0.6) |

### Creating a relation manually

```json
mem_relate({
  "source": "auth-service",
  "target": "jwt-library",
  "relation": "depends_on",
  "source_kind": "service",
  "target_kind": "library",
  "weight": 0.95
})
```

### Bootstrapping the graph for an existing project

```bash
# Preview what would be created
mneme graph rebuild --dry-run

# Run the rebuild
mneme graph rebuild

# Expected output:
# Rebuild complete in 1.234s:
#   Memories scanned:        142
#   Entities extracted:       89
#   Relations created:        45
```

### Edge decay

Relations not traversed for 30 days begin to decay exponentially (0.02/day). Active edges stay strong. This prevents the graph from filling up with stale connections.

---

## 7. Communities and Synthesis

The Louvain algorithm groups densely connected entities into communities. Synthesis memories summarize each community automatically. No LLM required -- synthesis is deterministic.

### How it works

1. **Detection:** Louvain runs during each consolidation cycle (every 6h by default). Communities with fewer than 3 members are filtered out.
2. **Synthesis:** One `synthesis` memory per community, containing an overview, top-3 members in detail, and a full member table.
3. **Context packing:** `mem_context` uses communities for coherent, compressed output -- packing cluster overviews first, then deep-diving the most relevant cluster.

### Viewing communities

Communities surface through `mem_context` (in the "Cluster Overviews" section) and through `mem_search` (synthesis memories appear in results). You can also see them in the vault if exported.

### Configuration

```toml
[graph]
# Community detection runs during consolidation
# Disable by setting community_detection_enabled = false in consolidation config

[context]
# context_packing_mode = "auto"  # auto | communities | flat
# cluster_overviews_budget = 1500
# top_cluster_max_members = 10
```

**When to use:** Communities are automatic. You benefit from them when your project has 50+ memories with a well-connected graph. For small projects, the graph may not have enough structure for meaningful communities.

---

## 8. Sessions

Sessions track the lifecycle of an agent's working period.

### Session start (automatic)

When wired via hooks, `mneme hook session-start` fires at the beginning of each agent session and injects context (rules, last session summary, top memories).

### Session end

At the end of a session, save a summary:

```json
mem_session_end({
  "summary": "Implemented JWT RS256 authentication. Created auth middleware, key rotation endpoint, and session invalidation flow. Decisions: chose RS256 over HS256 for asymmetric verification."
})
```

Or via CLI:

```bash
mneme session-end "Implemented JWT RS256 authentication..."
```

### Checkpoints (long tasks)

During long tasks, save periodic checkpoints to prevent knowledge loss on context compaction:

```json
mem_checkpoint({
  "summary": "Halfway through database migration. Completed schema changes for users and orders tables.",
  "decisions": "Using pgx instead of database/sql for better PostgreSQL support.",
  "next_steps": "Migrate the products table, then update all repository methods."
})
```

Checkpoints are overwritten on each call (same `topic_key`), so they always reflect the latest state.

### Session lifecycle in agents

Agents using mneme follow this pattern:

```
Session start
  -> mem_context (load project knowledge + rules)
  -> mem_search (find relevant memories for the task)

During work
  -> mem_save (decisions, discoveries, patterns)
  -> mem_checkpoint (periodic state snapshots)
  -> mem_search (as needed)

Session end
  -> mem_session_end (save summary for next session)
```

---

## 9. Vault and Obsidian

The vault exports memories as Markdown files with YAML frontmatter. It is a filesystem mirror of the SQLite database. See [VAULT.md](VAULT.md) for format details and [OBSIDIAN.md](OBSIDIAN.md) for the full Obsidian integration guide.

### Export

```bash
mneme vault export

# Expected output:
# Vault export: 142 written, 58 skipped, 0 errors
# Vault root: /Users/you/.mneme/vaults/wirvii-mneme
```

### Import (after editing in Obsidian)

```bash
# Preview first
mneme vault import --dry-run

# Import changes
mneme vault import

# Re-export to sync timestamps
mneme vault export
```

### Roundtrip workflow

```bash
mneme vault export          # 1. SQLite -> Markdown
# ... edit in Obsidian ...  # 2. Browse, edit, create notes
mneme vault import --dry-run # 3. Preview changes
mneme vault import           # 4. Markdown -> SQLite
mneme vault export           # 5. Re-sync timestamps
```

### Using Obsidian

```bash
# Open the vault
open -a Obsidian ~/.mneme/vaults/<slug>
```

Install the **Dataview** plugin for querying memories by type, importance, or date. Example query:

````
```dataview
TABLE importance, type, created_at
FROM "notes"
WHERE type = "architecture"
SORT importance DESC
```
````

**When to use the vault:** For visual exploration of your knowledge base, manual memory curation, PR-reviewable memory changes, or offline backup.

**When NOT to use:** As the primary interface for agents (use MCP). As real-time sync (there is no filesystem watcher yet).

---

## 10. Sync and Backup

Two export formats serve different needs.

### Memory Manifest (full-fidelity)

Exports memories, entities, relations, and sessions as a single `.manifest.tar.gz` archive. See [MEMORY-MANIFEST.md](MEMORY-MANIFEST.md).

```bash
# Export
mneme sync export --format manifest

# Expected output:
# Exported 142 memories, 89 entities, 45 relations, 3 sessions
# File: .mneme/sync/wirvii-mneme.manifest.tar.gz
```

### Legacy JSONL.gz (memories only)

```bash
# Export (default format)
mneme sync export

# Check status
mneme sync status

# Expected output:
# Project: wirvii/mneme
# Last export: 2026-04-30T20:41:14Z
# File: .mneme/sync/wirvii-mneme.jsonl.gz (142 memories)
```

### Import (auto-detects format)

```bash
mneme sync import .mneme/sync/wirvii-mneme.manifest.tar.gz
mneme sync import .mneme/sync/wirvii-mneme.jsonl.gz

# Expected output:
# Imported: 142 created, 0 updated, 0 skipped
```

### When to use which

| Scenario | Format |
|----------|--------|
| Full backup/restore | `--format manifest` |
| Share memories via git | JSONL.gz (lighter, memories only) |
| Transfer to another machine | `--format manifest` |
| Interop with third-party tools | `--format manifest` (JSON Schema validated) |

### Deduplication on import

- Memories with `topic_key`: merged by `(topic_key, project, scope)`.
- Memories without `topic_key`: skipped if ID already exists.
- Entities: skipped if `(name, project)` already exists.
- Relations: skipped if `(source, target, type)` already exists.

---

## 11. Agent Ecosystem

`mneme install claude-code` installs slash commands, hooks, the operating
manual, and skills — but **no longer ships global subagent profiles**. As of
SPEC-073 (completing the agnostic-agents EPIC, SPEC-052 SS-7), the six global
profiles that earlier releases wrote to `~/.claude/agents/` are retired:
install no longer writes them and actively removes any it installed in the past
(only the ones still byte-identical to what mneme shipped — a customised profile
is left untouched). Subagents are now generated **per-project** by the
`mneme-init` skill's grill, so a project's agents match its own stack rather than
a fixed Go/hexagonal/sqlc template — see
[docs/enforcement-model.md](enforcement-model.md#project-scoped-opt-in-registration-epic-agnostic-agents-ss-6)
for the model and the `subagent_*` MCP tools /
[docs/api/subagents.md](api/subagents.md) for the generation API.

### Built-in role archetypes

These six roles are the canonical archetypes mneme ships as assets
(`internal/install/assets/agents/*.md`), consumed by per-project subagent
generation. Since SPEC-073 they are **no longer installed globally** to
`~/.claude/agents/` — the table below documents the roles, not files on disk.

| Agent | Model | Role | Writes |
|-------|-------|------|--------|
| **architect** | opus | Design specs, never implement | `spec.md`, `decisions.md` |
| **backend** | sonnet | Backend implementation (Go, sqlc, ports/adapters) | code, `api-contracts.md` |
| **frontend** | sonnet | Frontend (Next.js, Server Components, Zod, i18n) | code |
| **qa-tester** | sonnet | End-to-end testing, "default state is REQUIRES CHANGES" | `qa-report.md` |
| **bug-hunter** | sonnet | READ-ONLY investigation, never modifies code | `diagnosis.md` |
| **diagnostician** | sonnet | Reads logs/infra, triages, proposes; `Bash` for reading only, no Edit/Write | `diagnosis` notes |

All agents integrate with mneme memory at start (search + spec_status) and end (save discoveries + spec_advance). Model per role is chosen when the `mneme-init` grill composes a per-project subagent (`subagent_compose`); `mneme model set/list/reset` still manages the `[models.overrides]` config (see [docs/models.md](models.md)).

### Per-project subagents (mneme-init grill)

Instead of the fixed global six, a project can run the `mneme-init` skill and
walk its grill to generate subagents tailored to its own stack: one profile
per role (`backend`, `frontend`, ...), each covering every app/area that role
owns, projected to `<repo>/.claude/agents/<role>.md` and
`<repo>/.codex/agents/<role>.toml`. Permissions (the
`tools:` allowlist) are always inherited from a fixed Go-authored archetype —
never LLM-generated — so a generated `backend` subagent has exactly the same
capability boundary as the global one, just a project-specific prompt. See
[docs/api/subagents.md](api/subagents.md) for the six `subagent_*` tools and
`mneme subagents` for their CLI counterpart.

### Team memory (git-native, opt-in)

A project can also opt into sharing durable knowledge (decisions,
conventions, architecture, patterns, bugfixes, rules) between teammates
through the repository itself — no server, no account, no network call. See
[docs/team-memory.md](team-memory.md) for the full model; the short version:

```bash
mneme team-memory enable      # activate: marker + bake/export + import hooks
mneme promote <id>            # explicitly share one memory regardless of type
```

### Slash commands

| Command | What it does |
|---------|--------------|
| `/mneme-init` | Thin wrapper that invokes the `mneme-init` skill — the skill (not this command) is the single source of truth for the project-init workflow: scan the project, seed mneme with foundational knowledge from CLAUDE.md, package.json, go.mod, etc., and offer the per-project subagent + team-memory grills. |
| `/grill-me` | Interview you relentlessly about a design, walking down every branch of the decision tree. |
| `/hunt-bug` | Orchestrate the bug-hunter subagent against a bug report. |
| `/bug-to-issue` | Convert a bug diagnosis into a spec via the architect. |

### Spec-Driven Development (SDD) lifecycle

```
backlog_add -> backlog_refine -> backlog_promote
  -> spec_new (draft)
  -> spec_advance: draft -> speccing -> specced -> implementing -> qa -> done
  -> spec_pushback (if blocked by ambiguity)
  -> spec_resolve (after resolving pushback)
```

```json
// Add an idea
backlog_add({ "title": "Add rate limiting to HTTP API", "priority": "high" })

// Refine it (can be called again later — refinements accumulate as rows,
// never concatenated into description — SPEC-110)
backlog_refine({ "id": "BL-042", "refinement": "Use token bucket, 100 req/min per IP..." })

// Promote to spec
backlog_promote({ "id": "BL-042" })

// Check spec status
spec_status({ "id": "SPEC-042" })

// Advance through states
spec_advance({ "id": "SPEC-042", "by": "architect" })
```

---

## 12. CLI Cheatsheet

### Memory operations

| Command | Description |
|---------|-------------|
| `mneme save --type decision -t "Title" -c "Content"` | Save a memory |
| `mneme search "query"` | Hybrid search (BM25 + vector + graph) |
| `mneme get <id>` | Retrieve full memory by ID |
| `mneme context --budget 4000 --focus "topic"` | Curated context bundle |
| `mneme update <id> --title "New title"` | Update a memory |
| `mneme forget <id>` | Mark for accelerated decay |
| `mneme status` | Project and memory status |
| `mneme stats` | Detailed statistics |

### Rules

| Command | Description |
|---------|-------------|
| `mneme rule add -t "Title" -a "pattern" -s block` | Create a rule |
| `mneme rule list` | List all active rules |
| `mneme rule list --severity block` | Filter by severity |
| `mneme rule test --tool Edit --path file.go` | Test against simulated invocation |

### Graph

| Command | Description |
|---------|-------------|
| `mneme explore "topic_key" --depth 2` | BFS graph traversal |
| `mneme graph rebuild` | Backfill graph from existing memories |
| `mneme graph rebuild --dry-run` | Preview without writing |

### Vault

| Command | Description |
|---------|-------------|
| `mneme vault export` | SQLite -> Markdown files |
| `mneme vault import` | Markdown -> SQLite |
| `mneme vault import --dry-run` | Preview import changes |
| `mneme vault import --strategy overwrite` | File always wins |

### Sync

| Command | Description |
|---------|-------------|
| `mneme sync export` | JSONL.gz export (memories only) |
| `mneme sync export --format manifest` | Full-fidelity archive |
| `mneme sync import <file>` | Import (auto-detects format) |
| `mneme sync status` | Show last export info |

### System

| Command | Description |
|---------|-------------|
| `mneme mcp` | Start MCP server (stdio) |
| `mneme serve --addr :7437` | Start HTTP API |
| `mneme install claude-code` | Configure MCP, hooks, manual + skills (no global agent profiles) |
| `mneme init` | Migrate legacy projects to SDD engine |
| `mneme consolidate` | Run consolidation pipeline manually |
| `mneme embed backfill` | Generate embeddings for memories without one |
| `mneme config show` | Show resolved config with provenance |
| `mneme config show graph` | Show specific config section |
| `mneme tui` | Interactive terminal UI |
| `mneme upgrade` | Check for and install updates |
| `mneme version` | Print version |

### Per-project subagents & team memory

| Command | Description |
|---------|-------------|
| `mneme subagents fingerprint` | Detect project root, apps, stack markers |
| `mneme subagents compose --role <r> --archetype <a> ...` | Preview a composed subagent profile |
| `mneme subagents write --role <r> --archetype <a> ...` | Write both runtime projections for the role |
| `mneme delegation-hook enable` | Register the project-scoped opt-in delegation hook |
| `mneme team-memory enable` | Activate git-native shared memory for this repo |
| `mneme promote <id>` | Mark one memory as team-curated (`shared=2`) |

---

## 13. MCP Tools Cheatsheet

The MCP server (`mneme mcp`) exposes 64 tools over JSON-RPC 2.0 stdio. This
cheatsheet covers the original memory-tool surface; for the complete set
(SDD, lane, codegraph, skills, model, conflicts, subagents) see [docs/api/](api/).

### Memory tools (15)

| Tool | Required params | Key optional params | Purpose |
|------|-----------------|---------------------|---------|
| `mem_save` | `title`, `content` | `type`, `topic_key`, `scope`, `importance`, `applies_to`, `severity`, `files` | Save a memory |
| `mem_search` | `query` | `type`, `scope`, `limit`, `include_graph` | Hybrid search |
| `mem_get` | `id` | -- | Retrieve by ID |
| `mem_context` | -- | `budget`, `focus`, `project`, `include_graph` | Curated context |
| `mem_update` | `id` | `title`, `content`, `type`, `importance`, `confidence`, `files` | Partial update |
| `mem_session_end` | `summary` | `session_id`, `project` | End session + save summary |
| `mem_suggest_topic_key` | `title` | `project` | Suggest topic_key for dedup |
| `mem_relate` | `source`, `target`, `relation` | `source_kind`, `target_kind`, `weight`, `project` | Create/update graph relation |
| `mem_timeline` | `around` | `window`, `limit`, `project` | Temporal neighborhood |
| `mem_stats` | -- | `project` | Aggregate statistics |
| `mem_checkpoint` | `summary` | `decisions`, `next_steps`, `project` | Save work-in-progress state |
| `mem_forget` | `id` | `reason` | Mark for accelerated decay |
| `mem_promote` | `id` | -- | Mark a memory as team-curated (`shared=2`) |
| `mem_explore` | `seed` | `depth`, `budget`, `threshold`, `project` | BFS graph traversal |
| `mem_gaps` | -- | `scope`, `limit`, `min_mentions`, `include_samples` | List unresolved wikilinks |

### Backlog tools (4)

| Tool | Required params | Key optional params | Purpose |
|------|-----------------|---------------------|---------|
| `backlog_add` | `title` | `description`, `priority`, `project` | Add to backlog |
| `backlog_list` | -- | `status`, `project` | List items |
| `backlog_refine` | `id`, `refinement` | `by` | Append a refinement (raw or refined item, callable N times — SPEC-110) |
| `backlog_promote` | `id` | -- | Promote to spec |

### Spec tools (8)

| Tool | Required params | Key optional params | Purpose |
|------|-----------------|---------------------|---------|
| `spec_new` | `title` | `backlog_id`, `project` | Create draft spec |
| `spec_status` | `id` | -- | Full status + history + pushbacks |
| `spec_advance` | `id`, `by` | `reason` | Advance to next state |
| `spec_pushback` | `id`, `from_agent`, `questions` | -- | Register blocking questions |
| `spec_resolve` | `id`, `resolution` | -- | Resolve a pushback |
| `spec_list` | -- | `status`, `project` | List specs |
| `spec_quick` | `id`, `rationale`, `by` | -- | Trivial lane: draft -> implementing in one step |
| `spec_reject` | `id`, `reason`, `by` | -- | Reject from qa/audit back to implementing |

---

## 14. Troubleshooting

### "mneme: command not found"

```bash
# Verify installation
which mneme
# If missing, rebuild and install:
cd /path/to/mneme && make install
```

### Build fails with "undefined: sqlite3"

CGO must be enabled and a C compiler must be available:

```bash
CGO_ENABLED=1 go build -tags fts5 -o mneme ./cmd/mneme
```

On macOS, install Xcode Command Line Tools: `xcode-select --install`.

### MCP server not connecting

Verify the MCP server config in `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "mneme": {
      "command": "mneme",
      "args": ["mcp"]
    }
  }
}
```

Run `mneme mcp` directly in a terminal to check for startup errors.

### Rules not firing

1. Check rules exist: `mneme rule list`
2. Test against the specific tool+path: `mneme rule test --tool Edit --path <path>`
3. Verify hooks are registered: check `~/.claude/settings.json` for `PreToolUse` entries.
4. Reinstall hooks: `mneme install claude-code --reinstall-hooks`

### Search returns no results

- Check memory exists: `mneme status` to see memory count.
- Try broader search terms.
- Try `include_graph: false` to rule out graph expansion issues.
- Run `mneme embed backfill` if you imported memories without embeddings.

### Graph is empty after import

`mneme sync import` (JSONL.gz) does not import entities/relations. Run:

```bash
mneme graph rebuild
```

For full-fidelity import including graph data, use the manifest format:

```bash
mneme sync export --format manifest
mneme sync import file.manifest.tar.gz
```

### Consolidation not running

Check the config:

```bash
mneme config show consolidation
```

Consolidation runs automatically when the MCP server starts and every 6h. To trigger manually:

```bash
mneme consolidate
```

### Vault import aborts with "project mismatch"

The `.mneme-vault` marker file's project must match the current project. You cannot import a vault from project A into project B.

### `mneme upgrade` fails on Windows with a file-in-use error

`go install` (the only Windows upgrade path) cannot replace an `mneme.exe`
that another process still holds a handle on -- commonly an antivirus
scanner, or this very binary running as the Claude Code MCP server. Close
Claude Code (which releases the MCP `mneme` process) and re-run
`mneme upgrade`.

### `mneme skills validate` always reports the script as skipped on Windows

`validation/run.sh` scripts need a POSIX `sh` on `PATH`; Windows only has
one when [Git for Windows](https://gitforwindows.org/) is installed.
Without it, `Validate` returns the `ErrNoShell` sentinel -- non-fatal, the
script is skipped rather than treated as a failure.

---

## 15. FAQ

**Q: Do I need a config file to get started?**
No. All settings have sensible defaults. mneme works without `~/.mneme/config.toml`. Create one only when you need to tune behavior.

**Q: How do I use mneme with agents other than Claude Code?**
mneme supports any MCP-compatible agent. Run `mneme mcp` as a stdio server. For non-MCP agents, use the HTTP API (`mneme serve --addr :7437`). See [ARCHITECTURE.md](ARCHITECTURE.md) for endpoint details.

**Q: What happens when I run out of memory budget?**
Consolidation enforces the budget. The lowest-scored memories are soft-deleted when the count exceeds `storage.project_budget` (default 1000) or `storage.global_budget` (default 200). Important memories survive; ephemeral ones decay.

**Q: Can two agents share the same mneme database?**
Yes, as long as they run on the same machine. SQLite handles concurrent reads. Writes are serialized (single writer). The MCP server uses WAL mode for this.

**Q: How do I delete a memory permanently?**
`mem_forget` sets `decay_rate=1.0` (soft delete). The memory is hard-deleted during the next consolidation cycle after the retention period (default 30 days). There is no instant hard-delete command.

**Q: Can I use mneme for non-code projects?**
Yes. Memory types like `decision`, `discovery`, and `convention` are domain-agnostic. Use project scope for project-specific knowledge and global scope for personal knowledge.

**Q: How do I reset mneme for a project?**
Delete the project database file:
```bash
rm ~/.mneme/projects/<slug>.db
```
The slug is derived from your git remote. Run `mneme status` to see the current slug.

**Q: What is the difference between `convention` and `rule`?**
A `convention` is passive -- it appears in search results and context if relevant. A `rule` is active -- it is always injected in context (with a dedicated token budget) and enforced by the pre-tool-use hook. Rules never decay.

**Q: How do I see what config value is active and where it came from?**
```bash
mneme config show --json | jq '.graph.graph_mode'
# Shows: value, source (default/file/env), and env_var name
```

**Q: Is my data sent to any cloud service?**
No. mneme is 100% local. All data stays in SQLite files under `~/.mneme/`. There is no telemetry, no cloud sync, no network calls.

**Q: Does mneme run on Windows?**
Yes. `go install github.com/wirvii/mneme/cmd/mneme@latest` is the only supported install path there, and it doubles as the upgrade path -- `mneme upgrade` shells out to `go install ...@<latest-tag>` internally when it detects a Windows host. Git for Windows is required for the codegraph/team-memory git hooks and for `mneme skills validate` (both shell out to `sh`); without it, `skills validate` returns a non-fatal "skipped" result rather than failing. See the [Windows](../README.md#windows) section of the README.

---

*Originally written 2026-04-30, covering mneme after EPIC-1 through EPIC-6. See [CHANGELOG.md](../CHANGELOG.md) for the full version history and [docs/api/](api/) for the complete, current tool/command/endpoint reference; current release is v1.17.0.*
