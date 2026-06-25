# CodeGraph — Semantic code knowledge graph

mneme embeds a code-graph subsystem that indexes Go and TypeScript/JavaScript
source files into a SQLite database and exposes symbol relationships via 10 MCP
tools and 10 CLI subcommands. This document covers the indexing workflow and
the two automatic-adoption mechanisms added in v1.13.0 (SPEC-044).

## Quick start

```bash
mneme codegraph index           # index cwd (incremental by default)
mneme codegraph status          # node/edge/file counts
mneme codegraph search <sym>    # find a symbol by name
mneme codegraph hooks install   # auto-reindex after every commit/checkout
```

## Storage

Each project gets its own codegraph database at
`~/.mneme/projects/<slug>-codegraph.db`, alongside the memory database. The
`<slug>` is the project identifier derived from the git remote URL (same logic
as the memory database). The codegraph database is separate so it can be wiped
and rebuilt from scratch without touching memories.

## Indexing

`mneme codegraph index [path]` walks a directory tree, extracts code symbols,
and writes them to the project database.

- **Incremental:** files are skipped when their content hash matches the
  stored record. Use `--force` to re-index all files regardless.
- **Resolution pass:** after indexing, a best-effort second pass resolves
  cross-file symbol references using a four-tier strategy (see below).
- **Languages:** Go (via `go/ast`) and TypeScript/JavaScript (via a Node.js
  subprocess that uses the TypeScript compiler API).

## Symbol resolution strategy (v1.15.0+)

Cross-file references are resolved in four tiers, in order. The first match wins.

| Tier | Name | Description |
|------|------|-------------|
| T1 | Exact | `qualified_name == referenceName` — same-file and fully qualified cross-file calls. |
| T2 | Import-guided (C3) | Uses the file's import declarations to locate the target package/file, then finds all candidates with that symbol name. Only links when **exactly one** candidate exists (candidato-único-o-nada). |
| T3 | Suffix | `qualified_name LIKE '%.' + referenceName` — partial qualification. |
| T4 | Short-name | `name == lastComponent(referenceName)` — unqualified fallback. |

**T2 — Import-guided detail (SPEC-047):**

- **Go**: `"pkg.Func"` → splits into qualifier (`"pkg"`) and symbol (`"Func"`).
  Looks up qualifier in the file's import bindings (the `import_alias` column)
  to get the import path (e.g. `"internal/store"`). Finds all nodes with
  `name="Func"` whose `file_path` directory suffix-matches the import path.
- **TS/JS**: namespace imports (`"utils.formatDate"` from `import * as utils from './lib/utils'`)
  and named/default imports (`"formatDate"` from `import { formatDate } from './lib/utils'`)
  are resolved by mapping the binding to the module source, then probing
  `.ts`, `.tsx`, `.js`, `.jsx`, and `index.*` file extensions.
- **tsconfig `paths` aliases** (`@/*`, `~/*`, etc.): when the module source is
  a non-relative bare specifier, the resolver first tries to expand it using
  path alias mappings loaded from `tsconfig.json` files in the project (see
  [tsconfig paths](#tsconfig-paths-aliases--spec-047) below). Requires Node.js +
  TypeScript in the project.
- **Bare/external imports** (npm packages, Go stdlib): no node in the repo →
  silently left unresolved (no error, no false edge).
- **Provenance**: edges created by T2 carry `provenance="import"` (distinct
  from `"ast"` for same-file edges and `"resolver"` for T3/T4 edges).

**Limitations:**
- Method calls on variables (`x.Foo()` where `x` is a typed local variable)
  require type inference — out of scope. Tracked as BL future item.
- Re-exports (`export { X } from './y'`) are not resolved — the target of the
  re-export is not followed.
- Go: package names that differ from the last path segment
  (e.g. `gopkg.in/yaml.v3` → package name `yaml`) require an explicit import
  alias to resolve (e.g. `import yaml "gopkg.in/yaml.v3"`). Without an alias
  the heuristic uses the last path segment (`v3`), which may not match.

**Re-indexing required:** existing databases must be re-indexed to populate
`import_alias` values and generate `provenance="import"` edges. The
`import_alias` schema column is added automatically on the next binary open
(no manual migration needed); re-index is needed only for the new edge data.

### tsconfig paths aliases — SPEC-047

TypeScript projects often use `tsconfig.json` `compilerOptions.paths` to define
module aliases such as `@/*` pointing to `src/*`. Without resolving these, any
import like `import { getDB } from "@/lib/db"` leaves an unresolved ref.

**How it works:**

1. After indexing, when TS nodes are present and a `rootDir` is known, the
   resolver calls the existing Node.js subprocess with a one-time control
   message `{op:"tsconfig",root:"<rootDir>"}`.
2. The subprocess walks all directories under `rootDir` (respecting
   `node_modules`, `.git`, etc.), parses each `tsconfig.json` found using
   `ts.parseJsonConfigFileContent`, and returns the resolved `baseUrl` +
   `paths` mapping for each config file.
3. The Go side builds a `TSAliasMap` — a mapping from alias prefix (e.g.
   `"@/"`) to a list of `TSAliasEntry` values carrying the tsconfig directory
   and the candidate base directories.
4. For each non-relative TS import ref, `ResolveAlias` picks the `TSAliasEntry`
   whose `TsconfigDir` is the **closest ancestor** of the importing file (longest
   path match). This ensures that in a monorepo with multiple tsconfigs defining
   the same alias, each file uses its own app's mapping.
5. The expanded candidates are fed to `tsCandidatePaths` (probing `.ts`, `.tsx`,
   etc.) and resolved with candidato-único-o-nada.

**Requirements:**
- Node.js must be available on `PATH`.
- `typescript` must be installed in the project (local `node_modules/typescript`
  or a globally resolvable package).

**Fail-open:** if Node.js is absent, TypeScript is unavailable, or no tsconfigs
define `paths`, the alias map is empty and the resolver skips alias expansion
silently — no error, no broken edges.

**The `{op:"tsconfig"}` message is back-compat:** the existing file-extraction
protocol (`{path,content}` → JSONL result) is unchanged. The subprocess
discriminates on the presence of `op` to distinguish control messages from file
extraction requests.

### TS cross-file call recall fix — SPEC-048 (v1.16.0)

Before v1.16.0, the TS/JS extractor registered all import bindings in the
same `topLevel` map as local declarations. This meant that a call like
`getPayloadClient()` — where `getPayloadClient` was imported — resolved to
the local **import node** instead of emitting an `unresolved_ref`. The import
node is a dead end: `codegraph_callers`, `codegraph_callees`, and
`codegraph_impact` all returned empty results for cross-file TS calls.

**Root cause and fix (`internal/codegraph/js/extract.js`):**

A pre-scan now walks the top-level `ImportDeclaration` nodes before the main
`visit()` pass and builds `importedBindings` — a `Set` of every local binding
name. When `walkCalls` encounters a call whose head (part before the first `.`)
is in `importedBindings`, it emits an `unresolved_ref` with `reference_name`
set to the call name (`"member"` for named/default, `"ns.member"` for
namespace). The import node and its `imports` edge are preserved (the
import-guided resolver uses them).

The pre-scan runs before `visit()` so that imports declared *after* their use
(valid in TS, which hoists `import` declarations) are correctly identified.

**Impact on recall (quantium, post v1.15.0 baseline):**

| Metric | Before (v1.15.0) | After (v1.16.0) |
|--------|-----------------|-----------------|
| Useful TS calls (target = function/method) | ~5.6% (11/198) | significantly higher |
| TS calls to import nodes | ~88% | ~0% |

Re-index with `mneme codegraph index --force` to see the improvement.

---

## Adoption nudge (C1) — SPEC-044

### What it is

When an agent calls `Read`, `Grep`, or `Glob` on a project that has an indexed
code graph with at least one node, the `mneme hook pre-tool-use` hook appends
a short markdown reminder to its stdout output:

```
<!-- mneme:codegraph-nudge:start -->
## mneme — code graph available

This project has an indexed code graph. Before reading or grepping source to
understand structure, prefer the codegraph tools (far fewer tokens):
`codegraph_search` (find a symbol), `codegraph_context` / `codegraph_callers` /
`codegraph_callees` (relationships), `codegraph_impact` (blast radius).
Use Read/Grep only for exact source text the graph can't provide.
<!-- mneme:codegraph-nudge:end -->
```

Claude Code injects this block into the agent's context window as a
system-reminder. The hook always exits 0 (context-only, never blocking).

If the graph is stale (last indexed more than 24 hours ago), one additional
line is appended before the closing tag:

```
Note: the graph may be stale (last indexed <N>h ago). Run `mneme codegraph index` to refresh.
```

### When the nudge fires

All five conditions must be true for the nudge to emit:

1. The tool being invoked is `Read`, `Grep`, or `Glob`.
2. `[codegraph] hook_nudge_enabled` is `true` (default) and the env variable
   `MNEME_CODEGRAPH_HOOK_NUDGE` is not `false`/`0`.
3. The path being read (if any) is not inside `~/.mneme/` (anti-loop guard:
   prevents the nudge firing when the agent reads mneme's own database files
   or workflow outputs).
4. A `<slug>-codegraph.db` file exists and contains at least one node.
   An empty graph (0 nodes) never triggers a nudge.
5. The nudge has not already been emitted for this session/project within the
   TTL window (see anti-noise section below).

### Anti-noise: once per session

The nudge fires at most once per Claude Code session. State is persisted in
`~/.mneme/codegraph-nudge-state.json`:

```json
{ "sid:abc123": 1739312000000, "proj:wirvii/mneme": 1739311000000 }
```

- When the hook payload contains a `session_id` (the normal case), the key is
  `sid:<session_id>`. The nudge fires once for the lifetime of that session —
  no TTL.
- When no `session_id` is present (fallback), the key is `proj:<slug>` with a
  4-hour TTL. Re-injection happens after the TTL expires.

The statefile is pruned on every write: entries older than 24 hours are
removed automatically so the file stays small.

Concurrent writes are best-effort (no file lock). The worst case is an extra
nudge in a race, which is harmless.

### Empty and stale graph behaviour

| Graph state | Behaviour |
|---|---|
| No DB file | No nudge |
| DB exists, 0 nodes | No nudge (graph not useful yet) |
| DB has nodes, last indexed ≤24h | Nudge without stale warning |
| DB has nodes, last indexed >24h | Nudge **with** stale-refresh line |

### Opt-out

**Per project (config file):**

```toml
# ~/.mneme/config.toml
[codegraph]
hook_nudge_enabled = false
```

**Per invocation (environment variable):**

```bash
MNEME_CODEGRAPH_HOOK_NUDGE=false mneme hook pre-tool-use
```

The environment variable takes precedence over the config file.

### Anti-loop

The nudge is suppressed when the file being read is located inside
`cfg.Storage.DataDir` (default `~/.mneme/`). This covers the case where an
agent reads the codegraph DB itself, the nudge statefile, or any other mneme
output — recursive nudging is prevented.

### Implementation

- Source: `internal/cli/hook.go` — `maybeEmitCodegraphNudge` and helpers.
- Probe helper: `internal/codegraph/probe.go` — `ProbeGraph(path)` opens the
  DB read-only, executes `SELECT 1 FROM nodes LIMIT 1` and
  `SELECT MAX(updated_at) FROM nodes`, and returns immediately. Called only
  when the statefile confirms the nudge has not been emitted yet for the
  current session.
- The hook does not modify `internal/install` or `settings.json` — the
  existing match-all PreToolUse hook registration already delivers
  Read/Grep/Glob payloads.

---

## Auto-reindex git hooks (C2) — SPEC-044

### What it is

`mneme codegraph hooks install` appends a one-line background invocation of
`mneme codegraph hooks run-reindex` to the `post-commit` and `post-checkout`
hooks of the current git repository. After installation, every commit and
branch checkout triggers an incremental re-index automatically.

The git operation is never delayed: the re-index runs detached (`&`) and the
hook always exits 0.

### Install

```bash
cd /path/to/your/repo
mneme codegraph hooks install
```

The command must be run inside a git repository. It resolves the hooks directory
via `git rev-parse --git-path hooks`, so `core.hooksPath` overrides and git
worktrees are handled correctly.

For each of `post-commit` and `post-checkout`:

- If the hook file does not exist, it is created with a `#!/bin/sh` shebang
  and `0755` permissions.
- If the hook file already exists, its current content is preserved and the
  mneme block is appended.
- Running `install` twice is idempotent: the block is not duplicated.

The appended block looks like this:

```sh
# >>> mneme codegraph (SPEC-044) >>>
# Auto-reindex the code graph after this git event. Managed by `mneme codegraph hooks`.
# Skipped during rebase/merge/cherry-pick to avoid storms.
"$(command -v mneme || echo mneme)" codegraph hooks run-reindex >/dev/null 2>&1 &
# <<< mneme codegraph (SPEC-044) <<<
```

`command -v mneme || echo mneme` degrades gracefully when `mneme` is not on
PATH, avoiding a syntax error that could break other hook logic.

### Remove

```bash
mneme codegraph hooks remove
```

Strips only the block between the `>>> mneme codegraph (SPEC-044) >>>` and
`<<< mneme codegraph (SPEC-044) <<<` markers from each hook file. All other
content in the hook is left untouched. The hook file is never deleted, even if
it becomes empty. Running `remove` when no block is present is a no-op.

### Skip during rebase / merge / cherry-pick

`run-reindex` (the hidden subcommand invoked by the git hooks) checks for the
following sentinel files inside the git directory before indexing:

| File | Meaning |
|---|---|
| `rebase-merge` | Interactive rebase in progress |
| `rebase-apply` | Non-interactive rebase or `git am` in progress |
| `MERGE_HEAD` | Merge in progress |
| `CHERRY_PICK_HEAD` | Cherry-pick in progress |

If any sentinel file is present, `run-reindex` exits 0 immediately without
indexing. This prevents a storm of redundant re-index runs during an
interactive rebase that fires many consecutive `post-checkout` hooks.

### Failure logging

Any error during re-indexing is appended to `~/.mneme/codegraph-hooks.log`:

```
[2026-06-12T21:00:00Z] repo=/path/to/repo error=<message>
```

The log file is created on first use with `0600` permissions. Log failures are
silently ignored. `run-reindex` always exits 0 so the git operation is never
affected.

### Uninstalling

```bash
mneme codegraph hooks remove
```

To completely uninstall, also delete the hook files if they contain no other
content (`post-commit`, `post-checkout` in `$(git rev-parse --git-path hooks)`).

---

## Adoption by prompt (C8) — SPEC-045

### What it is

In addition to the runtime nudge (C1, above), each of the 6 bundled agent assets
embeds a permanent `## Exploracion de codigo: grafo primero` section in its system
prompt. Unlike the nudge — which fires once per session and only when the graph is
indexed — this block is always present and states the policy imperatively.

The block is delimited by markers:

```
<!-- mneme:codegraph-policy:start -->
## Exploracion de codigo: grafo primero
...
<!-- mneme:codegraph-policy:end -->
```

The markers exist so `TestAgentsCodegraphPolicy` (`internal/install/assets_test.go`)
can verify all 6 assets carry the canonical block and fail fast on any drift.

### Two canonical variants

**Without Bash clause** (`architect`, `qa-tester`):
These agents do not have `Bash` in their tool allowlist, so no Bash-specific
guidance is needed.

**With Bash clause** (`backend`, `frontend`, `bug-hunter`):
Adds an explicit prohibition on using `Bash` (grep/cat/find/rg/head/tail) for
code navigation, since codegraph tools and native Read/Grep cover that need.
Bash is reserved for build, test, git, and operational tasks.

**Diagnostician variant** (`diagnostician`):
Same Bash prohibition for code navigation, but adds a sentence that explicitly
preserves Bash for reading logs, infra, and operational diagnostics
(so it does not contradict the agent's own `## Permisos de Bash` section).

### How it is deployed

The policy is embedded directly in the agent `.md` assets under
`internal/install/assets/agents/`. `WriteAgents` (`internal/install/install.go`)
rewrites each file entirely from the embed on every `mneme install claude-code` —
no flags required. After a `mneme upgrade`, run `mneme install claude-code` and
restart Claude Code for the policy to take effect.

### Relationship with the runtime nudge (C1)

The two mechanisms are complementary, not redundant:

| | C8 (prompt, this section) | C1 (runtime nudge, SPEC-044) |
|---|---|---|
| Trigger | Always — static system prompt | Once per session when Read/Grep/Glob fires |
| Content | Imperativ policy + tools list + fallback rules | Short reminder with graph freshness info |
| Bash gap | Yes — explicit prohibition for agents with Bash | No |
| Staleness info | No | Yes — warns when graph is >24h stale |

---

## MCP tools

The following 10 MCP tools expose the code graph to agents:

| Tool | Description |
|---|---|
| `codegraph_search` | Find symbols by name (FTS5 prefix match) |
| `codegraph_context` | Full node details + callers + callees |
| `codegraph_callers` | Nodes that call a given symbol |
| `codegraph_callees` | Nodes called by a given symbol |
| `codegraph_impact` | Transitive blast radius of changing a symbol |
| `codegraph_node` | Raw node fields + source lines |
| `codegraph_explore` | Multi-symbol batch exploration |
| `codegraph_trace` | Shortest call path between two symbols |
| `codegraph_status` | Aggregate node/edge/file counts |
| `codegraph_files` | List indexed files (with optional glob/language filter) |

> **Recall limitation — `codegraph_impact` / `codegraph_callees`:** these tools
> rely on the call-edge graph, which currently captures ~43% of callable
> relationships in a typical Go codebase. Method-calls (`x.Foo()`),
> cross-package calls, and stdlib calls are often not recorded as edges.
> `codegraph_search`, `codegraph_context`, and `codegraph_callers` are reliable
> for locating symbols. For an **exhaustive** impact analysis before a refactor,
> complement with `Grep`/`Read` — do not assume "nobody calls X" just because
> the graph does not show it. Stale noise from generated directories (`.next`,
> etc.) can be purged by running `mneme codegraph index` after upgrading to a
> build that includes the SPEC-046 indexer fix.

---

## CLI subcommands

```
mneme codegraph index [path]          # index source files
mneme codegraph status                # show statistics
mneme codegraph search <query>        # search by symbol name
mneme codegraph callers <symbol>      # find callers
mneme codegraph callees <symbol>      # find callees
mneme codegraph impact <symbol>       # blast radius
mneme codegraph node <symbol>         # full node detail
mneme codegraph trace <from> <to>     # shortest call path
mneme codegraph files [pattern]       # list indexed files
mneme codegraph hooks install         # install auto-reindex git hooks
mneme codegraph hooks remove          # remove auto-reindex git hooks
```

`mneme codegraph hooks run-reindex` is a hidden subcommand invoked by the
installed git hooks. It is not shown in `--help`.

---

## Config reference

```toml
[codegraph]
# Controls whether the pre-tool-use hook emits a reminder to use
# codegraph_* tools when an agent calls Read/Grep/Glob on a project
# that has an indexed code graph. Default: true.
hook_nudge_enabled = true
```

Environment override: `MNEME_CODEGRAPH_HOOK_NUDGE=false` disables the nudge
regardless of the config file value.
