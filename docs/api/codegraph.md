# API Reference — CodeGraph Tools (`codegraph_*`)

10 MCP tools over JSON-RPC 2.0 stdio (`mneme mcp`). Concept guide:
[docs/codegraph.md](../codegraph.md) (indexing model, coverage caveats, git
hooks). Index: [docs/API.md](../API.md).

Unlike the other tool families, `codegraph_*` tools return **plain,
human-readable text** (not JSON) in the single `text` content block — they are
designed to be read directly by an agent, not machine-parsed. Coverage
caveat: `codegraph_impact` and `codegraph_callees` are **best-effort**. The
graph does not reliably capture method-calls (`x.Foo()`) or cross-package/
stdlib calls — do not assume "nobody calls X" purely from empty results; a
project must be indexed (`mneme codegraph index`) before any of these tools
return data.

---

## codegraph_search

Search code symbols by name using full-text search. Returns functions,
methods, structs, etc. matching the query.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | yes | Search query for symbol names |
| `kind` | string[] | no | Filter by node kind (`function`, `struct`, `interface`, `method`, etc.) |
| `language` | string[] | no | Filter by language (`go`, `typescript`, `javascript`) |
| `limit` | integer | no | Max results. Default: 20 |

**Returns:** Text list of matching symbols with kind and `file:line` location, or `"No results found."`.

**Errors:** `-32602` missing `query`. `-32603` codegraph service unavailable or query failure.

**Example:** `mneme codegraph search "MemoryService"`

---

## codegraph_context

Get the full context of a code symbol: definition, callers, callees, and
containing file. Combines `codegraph_node` + `codegraph_callers` +
`codegraph_callees` into one call.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `symbol` | string | yes | Symbol name (short name, qualified name, or partial match) |
| `depth` | integer | no | How many hops of callers/callees to include. Default: 1 |

**Returns:** Text block: node detail (kind, qualified name, file:line, signature, docstring, source), followed by "Callers" and "Callees" sections when non-empty.

**Errors:** `-32602` missing `symbol`. `-32603` symbol not found or lookup failure.

---

## codegraph_callers

Find all functions/methods that call a given symbol.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `symbol` | string | yes | Symbol name to find callers of |
| `depth` | integer | no | Traversal depth (default 1, max 5) |
| `limit` | integer | no | Max results. Default: 20 |

**Returns:** Text list titled `"Callers of <symbol>"`.

**Errors:** `-32602` missing `symbol`. `-32603` traversal failure.

**Example:** `mneme codegraph callers SaveMemory --depth 2`

---

## codegraph_callees

Find all functions/methods that a given symbol calls. **Coverage caveat:**
method-calls and cross-package/stdlib calls are not reliably captured — treat
empty results as inconclusive, not proof of "no callees".

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `symbol` | string | yes | Symbol name to find callees of |
| `depth` | integer | no | Traversal depth (default 1, max 5) |
| `limit` | integer | no | Max results. Default: 20 |

**Returns:** Text list titled `"Callees of <symbol>"`.

**Errors:** `-32602` missing `symbol`. `-32603` traversal failure.

---

## codegraph_impact

Analyze the impact radius of changing a symbol — what transitively depends on
it, by following incoming `calls`/`imports`/`extends`/`implements` edges.
**Coverage caveat:** same as `codegraph_callees` — complement with `Grep`/`Read`
for an exhaustive pre-refactor analysis; do not assume completeness.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `symbol` | string | yes | Symbol to analyze impact for |
| `depth` | integer | no | Traversal depth (default 3, max 10) |
| `limit` | integer | no | Max results. Default: 50 |

**Returns:** Text list titled `"Impact of <symbol>"`.

**Errors:** `-32602` missing `symbol`. `-32603` traversal failure.

**Example:** `mneme codegraph impact Memory --limit 50`

---

## codegraph_node

Get detailed information about a specific code symbol including its source
code.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `symbol` | string | yes | Symbol name to look up |

**Returns:** Text block: kind, qualified name, file location, signature, docstring, and full source code.

**Errors:** `-32602` missing `symbol`. `-32603` symbol not found.

---

## codegraph_explore

Explore multiple symbols at once: get their source code and relationships in
one call. Useful for loading a small cluster of related symbols before making
a change.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `symbols` | string[] | yes | List of symbol names to explore (max 10; truncated silently beyond that) |
| `budget` | integer | no | Maximum output length in characters. Default: 30000 |

**Returns:** Concatenated text blocks (one `codegraph_node`-style detail per symbol), truncated to `budget` characters. Symbols that fail lookup are reported inline as `"## <symbol>\nError: ..."` without failing the whole call.

**Errors:** `-32602` missing/empty `symbols`.

---

## codegraph_files

List indexed files, optionally filtered by language or glob pattern.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `pattern` | string | no | Glob pattern to filter file paths (`filepath.Match` semantics) |
| `language` | string | no | Filter by language (`go`, `typescript`, `javascript`) |

**Returns:** Text list: `"Indexed files (N):"` followed by `path  [language] N nodes` per file.

**Errors:** `-32603` query failure.

---

## codegraph_status

Show the status of the code graph index: counts, languages, last update.

No parameters.

**Returns:** Text block with node/edge/file counts broken down by kind and language (see `mneme codegraph status` for the exact format).

**Errors:** `-32603` query failure.

---

## codegraph_trace

Find the call path between two symbols via BFS on outgoing `calls` edges.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `from` | string | yes | Source symbol name |
| `to` | string | yes | Target symbol name |
| `max_depth` | integer | no | Maximum path length. Default: 5 |

**Returns:** Text: each step in the path as `"symbol  →  symbol"`, or a not-found message when no path exists within `max_depth`.

**Errors:** `-32602` missing `from`/`to`. `-32603` traversal failure.

---

## CLI parity note

`codegraph_explore` has **no CLI equivalent** — it is an MCP-only batched
convenience tool. All other 9 tools map 1:1 to `mneme codegraph <subcommand>`
(see [docs/api/cli.md](cli.md)), plus `mneme codegraph index` and
`mneme codegraph hooks install|remove`, which are CLI-only (indexing and git
hook management are not exposed as MCP tools — an agent triggers indexing via
`Bash` or relies on the auto-reindex git hooks).

## Error codes

| Code | Name | Triggered when |
|------|------|----------------|
| `-32602` | Invalid params | Missing required symbol/query/from/to |
| `-32603` | Internal error | Codegraph service unavailable, symbol not found, query/traversal failure |

## See also

- [docs/codegraph.md](../codegraph.md) — indexing model, extractor coverage by language, auto-reindex git hooks, `hook_nudge_enabled` config
- [docs/api/cli.md](cli.md) — `mneme codegraph index/search/node/callers/callees/impact/trace/files/status/hooks`
