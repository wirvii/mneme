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
- Every one of the ten `codegraph_*` MCP tools prepends a one-line banner —
  `[mneme:graph-incomplete] <languages> NOT indexed (<cause>) — a symbol
  missing from this answer may still exist. Details: mneme codegraph
  status` — to a successful result, an EMPTY result, and an error message
  alike. A bare `symbol "X" not found` is exactly the sentence that makes an
  agent conclude a symbol doesn't exist; on a degraded graph, that
  conclusion may simply be wrong.
- `mem_context`'s `codegraph_hint` carries the same banner instead of an
  unqualified "Code Graph (indexed): N symbols".
- The pre-tool-use "consult the code graph FIRST" nudge carries the banner
  too — pushing an agent toward a graph without telling it a language is
  missing would be the same mistake.
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

When an agent calls `Read`, `Grep`, `Glob`, or a Bash code-search command (see
below) on a project that has an indexed code graph with at least one node, the
`mneme hook pre-tool-use` hook appends a short markdown reminder to its stdout
output:

```
<!-- mneme:codegraph-nudge:start -->
## mneme — consult the code graph FIRST

MANDATORY: this project has an indexed code graph. BEFORE reading or grepping
source to understand its structure, you MUST consult the code graph tools first
(far fewer tokens): `codegraph_search` (locate a symbol), `codegraph_context` /
`codegraph_callers` / `codegraph_callees` (relationships), `codegraph_impact`
(blast radius). This applies to subagents too.
Use Read/Grep/Bash only for the exact text the graph can't provide, or if the
graph is stale or the repo is not indexed.
<!-- mneme:codegraph-nudge:end -->
```

The tone is **mandatory but non-blocking** (SPEC-083 D-owner-1): Claude Code
injects this block into the agent's context window as a system-reminder, and the
hook always exits 0. It never blocks a tool call — the same mandatory vocabulary
appears in the operating manual and the subagent policy so all three surfaces
say the same thing.

If the graph is stale (last indexed more than 24 hours ago), one additional
line is appended before the closing tag:

```
Note: the graph may be stale (last indexed <N>h ago). Run `mneme codegraph index` to refresh.
```

### When the nudge fires

All five conditions must be true for the nudge to emit:

1. The tool being invoked is `Read`, `Grep`, `Glob`, or a `Bash` command whose
   first word in any pipeline/logical segment is a code-search executable —
   `grep`, `egrep`, `fgrep`, `rg`, `ag`, `ack`, `find`, `fd`, `cat`, `head`, or
   `tail` (SPEC-083 W2). So `grep -r foo internal/` and `git diff | rg foo`
   qualify; `go test ./...` does not. Bash commands are tokenized with the
   shell parser; a tokenizer error fails open (no nudge).
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

## Adoption querylog (C6) — SPEC-083

### What it is

To know whether the nudge and the codegraph-first prompt policy actually work,
mneme records a **local, privacy-preserving adoption querylog**: an append-only
JSONL file that answers one question — "when an agent could have used the code
graph, did it?"

Two event kinds are recorded:

- **`use`** — the agent called a `codegraph_*` MCP tool. Logged authoritatively
  from the MCP dispatch (`internal/mcp/handlers.go`), so it always fires when the
  tool runs and **excludes human CLI use** (`mneme codegraph search` goes through
  the service, not the MCP handler).
- **`opportunity`** — the agent explored code with `Read`/`Grep`/`Glob` or a
  Bash code-search command on a project that HAS an indexed graph, i.e. it could
  have queried the graph but chose not to. Logged by the pre-tool-use hook on
  **every** qualified call (not once per session).

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

The two `[codegraph]` flags are independent: turning the nudge off does not turn
telemetry off, and vice versa. Because config is never rewritten by
`mneme install`, an opt-out survives `mneme upgrade`.

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
over release validates the nudge and prompt policy; `Top missed` points at the
next pattern to target. No data in the window prints a clear message and exits 0.

The report is **CLI-only** by design (a human diagnostic; the codegraph has no
HTTP surface; an agent does not read its own adoption metric at runtime).

### Implementation

- Leaf package: `internal/querylog` (stdlib only — `Event`, `Append`, `Read`,
  `Aggregate`; no model/store/config imports).
- Path helper: `internal/codegraph/db.go` — `QuerylogPath(projectsDir, slug)`.
- `use` hook: `internal/mcp/handlers.go` — `logCodegraphUse`.
- `opportunity` hook: `internal/cli/hook.go` — `logOpportunity`, called from
  `maybeEmitCodegraphNudge` after the graph-existence probe.
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

---

## API reference

Full contract (params, returns, errors, examples) for the 10 `codegraph_*`
MCP tools: [docs/api/codegraph.md](api/codegraph.md) →
