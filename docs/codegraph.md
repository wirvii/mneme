# CodeGraph — Semantic code knowledge graph

mneme embeds a code-graph subsystem that indexes Go and TypeScript/JavaScript
source files into a SQLite database and exposes symbol relationships through 4
MCP tools and the `mneme codegraph` CLI. The graph is **optional**: since
SPEC-165 nothing reminds or obliges an agent to use it, the bundled `codegraph`
skill explains when it pays off, and the procedure in "Medir el ahorro de
tokens" below measures whether it does. This document covers the indexing
workflow, the usage telemetry and the auto-reindex git hooks.

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

### Full scan respects `.gitignore` — SPEC-102

A full scan (`mneme codegraph index` with no prior `last_sha`, or the
auto-reindex hook's fallback when the recorded SHA is missing/stale) no
longer walks the raw filesystem tree with only a hardcoded `ignoredDirs`
name-list. When the target directory is inside a git worktree, the CLI first
resolves the eligible file set via a single `git ls-files -z --cached
--others --exclude-standard` call — tracked files **plus** untracked files
that are not gitignored — and hands that list to the indexer instead of
walking. git prunes ignored directories internally while resolving this, so
a large gitignored directory (build caches, a `tmp/` sandbox, vendored
dependencies not meant to be tracked, …) is never even stat'd, not just
skipped after being visited.

This is deliberately **not** "only indexed files" (`git ls-files --cached`
alone): a work-in-progress file you just created and haven't committed yet
is still indexed, which matters for an agent exploring code it is actively
writing. The only thing excluded is what git itself would ignore.

**Non-git fallback:** when the target directory is not inside a git
worktree (or `git` is not on `PATH`), indexing falls back to the previous
behaviour unchanged — `filepath.WalkDir` with the hidden-directory skip and
the `ignoredDirs` name-list described above. Nothing regresses for
non-git-managed source trees.

**Self-healing:** unlike the SPEC-101 scoped (commit-diff) reindex, a
full scan still runs the existing prune pass, so any node previously
indexed from a path that is no longer in the (now git-filtered) candidate
list — e.g. the entire contents of a gitignored directory picked up by an
older mneme version — is purged automatically on the next
`mneme codegraph index` or `mneme codegraph index --force`. No manual
cleanup step is required.

### TypeScript toolchain compatibility — SPEC-088 (v1.27.1)

The TS/JS extractor depends on the resolved `typescript` npm package (global
install or `node_modules/typescript`) exposing the **classic synchronous
Compiler API** — `ts.createSourceFile`, `ts.forEachChild`,
`ts.ScriptTarget`/`ts.SyntaxKind`/`ts.NodeFlags`, and friends. **Supported
majors: typescript 5.x and 6.x.** `typescript@7` is a from-scratch rewrite
whose `require('typescript')` export surface is nearly empty
(`version`/`versionMajorMinor` only) — every one of those symbols is
`undefined` on it, and no major-version allowlist protects against this: the
extractor checks for the actual symbols it uses (capability, not a version
number), so a hypothetical future major that keeps the classic API would
still work here without a code change.

**What happens with an incompatible typescript (updated by SPEC-142 — see
"Declared degradation" below):**

| Toolchain state | Result |
|---|---|
| `node` not on `PATH` | TS/JS extraction is skipped for that run; Go files still index normally. |
| `typescript` not installed/resolvable | Same as above — degrades gracefully. |
| `typescript` installed but API-incompatible (e.g. 7.x) | Every OTHER language still indexes normally. TypeScript/JavaScript are recorded as a **degraded language** (see below) and `mneme codegraph index` still exits **0** — it no longer aborts. |

**Escape hatches**, in order of preference:

1. **Downgrade** the resolved `typescript` to a 5.x or 6.x release
   (`npm install -g typescript@6`).
2. **Pin via `NODE_PATH`**: point `NODE_PATH` at a directory containing a
   compatible `typescript` install — an explicitly-set `NODE_PATH` now takes
   precedence over the global npm root (`NODE_PATH=/path/to/ts6/node_modules
   mneme codegraph index`).
3. **Do nothing.** mneme indexes every other language and marks the graph as
   incomplete for typescript/javascript — every code-graph query says so
   until a compatible typescript is resolvable again (see below). There is
   no need to uninstall anything.

**Mechanism, for contributors:** `js/extract.js` checks the required API
symbols right after `require('typescript')` and exits **20** (not one of
Node's own reserved 1–12 exit codes) when they're missing, writing a
structured stderr message with the found version. `TSExtractor.Extract` in
`extractor_ts.go` classifies that specific exit code as
`ErrExtractorIncompatible` (wrapping the subprocess's stderr). This sentinel
is the SIGNAL, not the policy (SPEC-142 D8): `Indexer.Index`/`indexList`/
`indexScoped` register the language as degraded and CONTINUE (no longer
abort, superseding SPEC-088 D4) — but keep the existing per-file
`FilesErrored` counting for every other kind of extraction error, so one
broken `.ts` file still doesn't take down the whole index. `budget.go`'s own
delta computation (SPEC-118) still treats the sentinel as fatal for its own
purpose — see "Declared degradation" below for why the same sentinel gets
two different, both correct, responses.

### Declared degradation — SPEC-142

**The problem this closes:** before SPEC-142, ONE file whose language's
toolchain was broken (the typescript@7 case above is the concrete, measured
example — 19 of 21 projects on the owner's own host, 6,402 TS/JS files, and
with them 10,230 UNRELATED Go files) made the indexer abort the ENTIRE run.
That was deliberate (it avoided a silently empty-but-"successful" index),
but it meant a project with a single broken TS/JS toolchain got NO code
graph at all, for ANY language.

**What changed:** an extractor whose toolchain is present but unusable no
longer aborts the index. Every OTHER language still indexes normally, and
the fact that one language could not be indexed is **declared**, not
swallowed — the guiding rule is that **a partial graph must never be
readable as a complete one**. Degrading silently would have reintroduced
exactly the false confidence the original abort behaviour was designed to
avoid — a query returning zero results looks identical whether the symbol
truly doesn't exist or its language was simply never indexed, and that
silence is the most expensive way to be wrong.

**Where the mark lives.** A `degraded_languages` key in `project_metadata`
— the same per-project settings table that already holds the last-indexed
git SHA — records, per language: the cause (today always
`toolchain-incompatible`, or `unreadable-mark` if the stored record itself
somehow can't be parsed), a bounded diagnostic reason, when it was first and
last observed, and how many files were skipped in the *most recent*
indexing pass (never a repository-wide total — a partial pass only ever
sees its own delta). It lives in the very same database as the nodes it
describes, so it resets the instant that database does (rebuild,
corruption, `--force` from empty).

**Who clears it.** Only a genuine FULL SCAN can clear the mark — one that
re-examines every eligible file of every language and can truthfully assert
none of it is degraded anymore. A scoped, incremental pass (the kind every
git-hook-driven re-index runs) only ever ADDS or UPDATES the mark; it never
clears it, because it only ever saw its own small delta. This is also how
recovery happens automatically: `reindexOnce` (the function behind
`mneme codegraph hooks install`'s auto-reindex, see below) always forces a
full scan while any language is marked degraded — the first commit after a
broken toolchain gets fixed heals the graph on its own, with no manual
command needed.

**Where you see it.**
- Every one of the four `codegraph_*` MCP tools prepends a one-line banner —
  `[mneme:graph-incomplete] <languages> NOT indexed (<cause>) — a symbol
  missing from this answer may still exist. Details: mneme codegraph
  status` — to a successful result, an EMPTY result, and an error message
  alike. A bare `symbol "X" not found` is exactly the sentence that makes an
  agent conclude a symbol doesn't exist; on a degraded graph, that
  conclusion may simply be wrong.
- `mem_context`'s `codegraph_hint` carries the same banner instead of an
  unqualified "Code Graph (indexed): N symbols".
- `mneme codegraph status` and `mneme codegraph index` print a per-language
  detail block: cause, reason, first/last seen, and files skipped **in the
  last run only**, labelled as such.
- `mneme codegraph search ...` (and every other CLI subcommand) prints the
  one-line banner to **stderr** before running — never stdout, so
  `mneme codegraph adoption --json`'s machine-readable output stays clean.

**What the mark does NOT mean.** It does not mean "the graph is wrong" or
"something failed". It means "this graph is missing this one language, and
until it is fixed, absence of a result for that language proves nothing".
Every other language in the same graph is exactly as trustworthy as
always. A project with no TypeScript/JavaScript at all never sees any of
this — the mark simply never gets set.

## Symbol resolution strategy (v1.15.0+)

Cross-file references are resolved in four tiers, in order. The first match wins.

| Tier | Name | Description |
|------|------|-------------|
| T1 | Exact | `qualified_name == referenceName` — same-file and fully qualified cross-file calls. |
| T2 | Import-guided (C3) | Uses the file's import declarations to locate the target package/file, then finds all candidates with that symbol name. Only links when **exactly one** candidate exists (single-candidate-or-nothing). |
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
   etc.) and resolved with single-candidate-or-nothing.

**Requirements:**
- Node.js must be available on `PATH`.
- `typescript` (major **5.x or 6.x** — see "TypeScript toolchain
  compatibility" above) must be installed in the project (local
  `node_modules/typescript` or a globally resolvable package).

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
node is a dead end: the callers, callees and impact queries all
returned empty results for cross-file TS calls.

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

## Adoption querylog (C6) — SPEC-083

### What it is

To know whether the code graph is actually used (SPEC-165 retired the reminder
that used to push agents toward it, so this is now the only adoption signal),
mneme records a **local, privacy-preserving adoption querylog**: an append-only
JSONL file that answers one question — "when an agent could have used the code
graph, did it?"

Two event kinds are recorded:

- **`use`** — the agent called a `codegraph_*` MCP tool. Logged authoritatively
  from the MCP dispatch (`internal/mcp/handlers.go`), so it always fires when the
  tool runs and **excludes human CLI use** (`mneme codegraph search` goes through
  the service, not the MCP handler). Consequently an agent that reaches the
  graph through the CLI (`mneme codegraph callees ...` via Bash) is not counted:
  the ratio below slightly understates real use, which is why "Medir el ahorro
  de tokens" is the source for deciding, not this report.
- **`opportunity`** — the agent explored code with `Read`/`Grep`/`Glob` or a
  Bash code-search command on a project that HAS an indexed graph, i.e. it could
  have queried the graph but chose not to. Logged by the pre-tool-use hook on
  **every** qualified call (not once per session). A Bash command qualifies when
  the first word of any pipeline/logical segment is a code-search executable —
  `grep`, `egrep`, `fgrep`, `rg`, `ag`, `ack`, `find`, `fd`, `cat`, `head` or
  `tail` — so `grep -r foo internal/` and `git diff | rg foo` count and
  `go test ./...` does not. Reads of mneme's own files under `~/.mneme/` never
  count (anti-loop guard). The hook itself prints nothing.

The file lives next to the graph DB at
`~/.mneme/projects/<slug>-codegraph-querylog.jsonl`.

### Privacy (D-owner-2)

The querylog is 100% local, costs nothing, and never leaves the machine. It
stores **only tool names** — never a file path, a shell command, or a search
query. For Bash the executable head is normalised to `bash:<cmd>` (e.g.
`bash:rg`). The `session_id` is Claude Code's opaque token; it identifies neither
the machine nor the user. The file is written `0o600` and is never transmitted.

```json
{"ts":"2026-07-14T22:00:01Z","session":"019f...","project":"wirvii/mneme","kind":"opportunity","tool":"Grep","source":"hook"}
{"ts":"2026-07-14T22:00:05Z","project":"wirvii/mneme","kind":"use","tool":"codegraph_search","source":"mcp"}
```

The file rotates to `<path>.1` (one backup) once it exceeds 5 MiB.

### Off-switch (default on)

```toml
# ~/.mneme/config.toml
[codegraph]
querylog_enabled = true   # default
```

```bash
MNEME_CODEGRAPH_QUERYLOG=false mneme hook pre-tool-use   # env wins over TOML
```

Because config is never rewritten by `mneme install`, an opt-out survives
`mneme upgrade`. A `config.toml` that still carries the retired
`hook_nudge_enabled` key keeps loading: the key is ignored.

### The report — `mneme codegraph adoption`

```bash
mneme codegraph adoption [--since 7d] [--json]
```

- `--since` accepts `24h`, `7d`, `30d` (default `7d`).
- `--json` emits the machine-readable report.

```
Code graph adoption (last 7d) — wirvii/mneme
  Adoption ratio:  0.34  (uses 52 / opportunities 100)
  Top graph tools:  codegraph_search 30, codegraph_context 15, codegraph_callers 7
  Top missed (Read/Grep/Bash instead of the graph):  Grep 40, Read 35, bash:rg 15, Glob 10
```

The **adoption ratio** is `uses / (uses + opportunities)` — the fraction of
qualified explorations that went through `codegraph_*`. A ratio rising release
over release means agents reach for the graph more; `Top missed` shows which
generic tool they used instead. No data in the window prints a clear message and exits 0.

The report is **CLI-only** by design (a human diagnostic; the codegraph has no
HTTP surface; an agent does not read its own adoption metric at runtime).

### Implementation

- Leaf package: `internal/querylog` (stdlib only — `Event`, `Append`, `Read`,
  `Aggregate`; no model/store/config imports).
- Path helper: `internal/codegraph/db.go` — `QuerylogPath(projectsDir, slug)`.
- `use` hook: `internal/mcp/handlers.go` — `logCodegraphUse`.
- `opportunity` hook: `internal/cli/hook.go` — `logOpportunity`, called from
  `recordCodegraphOpportunity` after the graph-existence probe.
- All writes are best-effort/fail-open: a telemetry failure never affects a tool
  call.

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

> **Windows:** these hooks are `#!/bin/sh` scripts that resolve the binary
> via `command -v mneme` -- they run correctly on Windows once [Git for
> Windows](https://gitforwindows.org/) is installed, since its bundled `sh`
> is what git invokes to run hooks. Without Git for Windows there is no `sh`
> to run them, so the auto-reindex silently never fires -- the hook file
> still exists and git itself is never blocked.

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

## Optional policy in the bundled agents — SPEC-165

Each of the 6 bundled agent assets (`internal/install/assets/agents/`) embeds
one identical, **optional** `## Grafo de codigo (opcional)` section, delimited by
markers:

```
<!-- mneme:codegraph-policy:start -->
## Grafo de codigo (opcional)
...
<!-- mneme:codegraph-policy:end -->
```

It says that, when the project has an indexed graph, `codegraph_search`,
`codegraph_context`, `codegraph_callers` and `codegraph_impact` can save reads on
structure questions, that the remaining queries go through the CLI
(`mneme codegraph callees|trace|node|explore|files|status`) for agents with
Bash, warns that `codegraph_impact` can be incomplete, and points at the
`codegraph` skill for when the graph is and is not worth using. Before SPEC-165
the block was mandatory ("consult the graph FIRST") and came in three variants
by toolset; there is now a single text for all six agents.

`TestAgentsCodegraphPolicy` (`internal/install/assets_test.go`) checks all 6
assets carry the canonical block and fail fast on drift. `WriteAgents`
(`internal/install/install.go`) rewrites each file from the embed on every
`mneme install claude-code`; after a `mneme upgrade`, run that command and
restart Claude Code for the change to take effect.

### The `codegraph` skill

`internal/install/assets/skills/codegraph/` ships with mneme like any bundled
skill (mirrored to `~/.claude/skills/` and `$HOME/.agents/skills/` by
`mneme install`). It names the 4 MCP tools, lists the CLI commands for the rest,
and states the rule that matters most: the graph never replaces reading the code
you will change, and a refactor's impact list must be cross-checked with Grep.

---

## MCP tools

The following 4 MCP tools expose the code graph to agents:

| Tool | Description |
|---|---|
| `codegraph_search` | Find symbols by name (FTS5 prefix match) |
| `codegraph_context` | Full node details + callers + callees |
| `codegraph_callers` | Nodes that call a given symbol |
| `codegraph_impact` | Transitive blast radius of changing a symbol |

The `codegraph_callees`, `codegraph_node`, `codegraph_explore`, `codegraph_trace`, `codegraph_status` and `codegraph_files` tools were removed from MCP by SPEC-165 (a
`tools/call` for them answers `unknown tool: <name>`); the same queries remain
available through the CLI below.

> **Recall limitation — `codegraph_impact` / `callees`:** these queries
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
mneme codegraph explore <sym> [<sym>...] [--budget N]  # node detail of up to 10 symbols
mneme codegraph trace <from> <to>     # shortest call path
mneme codegraph files [pattern]       # list indexed files
mneme codegraph adoption [--since 7d] # usage telemetry report
mneme codegraph hooks install         # install auto-reindex git hooks
mneme codegraph hooks remove          # remove auto-reindex git hooks
```

`mneme codegraph explore` prints, for each symbol, the same detail as
`mneme codegraph node` (definition and source). A symbol that cannot be found
prints `## <symbol>` and `Error: ...` and the command continues with the next
one; at most 10 symbols are processed and the output is cut at `--budget` bytes
(default 30000). It is the CLI home of what used to be `codegraph_explore`.

`mneme codegraph hooks run-reindex` is a hidden subcommand invoked by the
installed git hooks. It is not shown in `--help`.

---

## Config reference

```toml
[codegraph]
# Records the local adoption telemetry (see "Adoption querylog"). Default: true.
querylog_enabled = true
```

Environment override: `MNEME_CODEGRAPH_QUERYLOG=false`.

The former `hook_nudge_enabled` key and `MNEME_CODEGRAPH_HOOK_NUDGE` variable
were removed together with the reminder they controlled (SPEC-165). A
`config.toml` that still has the key loads without error and the value is
ignored. A leftover `~/.mneme/codegraph-nudge-state.json` is inert and can be
deleted.

---

## Medir el ahorro de tokens

The graph earns its place only if it costs fewer tokens than reading. This
procedure measures that with the same questions asked twice, with and without the
graph. Running it spends subscription usage, so it is a **manual step** for the
owner; nothing here runs in CI.

**Tasks.** `scripts/codegraph-bench/tasks.tsv`, columns
`id<TAB>prompt<TAB>expected` where `expected` is a substring (case-insensitive)
a correct answer must contain. It holds 5 structure questions about this
repository (who calls `loadMutableSpec`, where `ResolveStageExecutor` lives,
...), with `expected` verified by hand against the code on the day they were
written; re-check them if the code moves. Another project can bring its own file
with `--tasks`.

**Arms.** Same model, same directory, graph freshly indexed
(`mneme codegraph index`) before starting.

- A, with the graph:
  `claude -p "<prompt>" --output-format json --allowedTools "Read Grep Glob mcp__mneme__codegraph_search mcp__mneme__codegraph_context mcp__mneme__codegraph_callers mcp__mneme__codegraph_impact"`
- B, without the graph:
  `claude -p "<prompt>" --output-format json --allowedTools "Read Grep Glob" --disallowedTools "mcp__mneme__codegraph_search mcp__mneme__codegraph_context mcp__mneme__codegraph_callers mcp__mneme__codegraph_impact Bash(mneme codegraph:*)"`

**Metrics per run**, read from the JSON `claude -p` prints:
`tokens = usage.input_tokens + usage.cache_creation_input_tokens + usage.cache_read_input_tokens + usage.output_tokens`,
`total_cost_usd`, `num_turns`, and `correct` (the answer contains `expected`).

**Repetitions.** `--runs N` (default 3). The report gives, per task and arm, the
median tokens/cost/turns and the correct answers as `k/N`, then the total and the
relative saving `1 - median(A) / median(B)`. It prints TSV on stdout and keeps
every raw JSON under `--out DIR` (default: a new temp directory the script
prints).

**Running it.**

```bash
mneme codegraph index
scripts/codegraph-token-bench.sh --dry-run --runs 1   # print the 2 x tasks x N commands, run nothing
scripts/codegraph-token-bench.sh --runs 3 [--model M] [--out DIR] [--tasks F]
```

`--dry-run` needs neither `claude` nor `jq`; a real run without either exits 2
with a message naming it.

**Suggested decision rule (the owner decides).** Total saving of at least 20%
with no fewer correct answers: keep the graph in mneme. Below 20%, or with fewer
correct answers: candidate to split out or retire (open a new backlog item).
Record the outcome as a `decision` memory with `topic_key`
`codegraph/token-savings`.

---

## API reference

Full contract (params, returns, errors, examples) for the 4 `codegraph_*`
MCP tools: [docs/api/codegraph.md](api/codegraph.md) →
