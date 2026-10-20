# API Reference — CodeGraph Tools (`codegraph_*`)

4 MCP tools over JSON-RPC 2.0 stdio (`mneme mcp`). Concept guide:
[docs/codegraph.md](../codegraph.md) (indexing model, coverage caveats, git
hooks). Index: [docs/API.md](../API.md).

Unlike the other tool families, `codegraph_*` tools return **plain,
human-readable text** (not JSON) in the single `text` content block — they are
designed to be read directly by an agent, not machine-parsed. Coverage
caveat: `codegraph_impact` is **best-effort**. The
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
containing file, in one call.

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

## codegraph_impact

Analyze the impact radius of changing a symbol — what transitively depends on
it, by following incoming `calls`/`imports`/`extends`/`implements` edges.
**Coverage caveat:** method-calls and cross-package/stdlib calls are not reliably captured — complement with `Grep`/`Read`
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

## Removed tools and CLI parity

SPEC-165 reduced MCP from 10 to 4 tools. A `tools/call` for any of the six
removed tools fails with `-32601` (`unknown tool: <name>`). Their queries live
on in the CLI:

| Removed MCP tool | CLI replacement |
|---|---|
| `codegraph_callees` | `mneme codegraph callees <symbol> [-d] [-n]` |
| `codegraph_node` | `mneme codegraph node <symbol>` |
| `codegraph_explore` | `mneme codegraph explore <symbol> [<symbol>...] [--budget N]` |
| `codegraph_trace` | `mneme codegraph trace <from> <to>` |
| `codegraph_status` | `mneme codegraph status` |
| `codegraph_files` | `mneme codegraph files [pattern]` |

The 3 tools `codegraph_search`, `codegraph_callers` and `codegraph_impact` map
1:1 to `mneme codegraph search|callers|impact`; `codegraph_context` is MCP-only.
See [docs/api/cli.md](cli.md). `mneme codegraph index` and
`mneme codegraph hooks install|remove` are CLI-only (indexing and git hook
management are not MCP tools — an agent triggers indexing via `Bash` or relies
on the auto-reindex git hooks).

## Error codes

| Code | Name | Triggered when |
|------|------|----------------|
| `-32602` | Invalid params | Missing required symbol/query/from/to |
| `-32603` | Internal error | Codegraph service unavailable, symbol not found, query/traversal failure |

## See also

- [docs/codegraph.md](../codegraph.md) — indexing model, extractor coverage by language, auto-reindex git hooks, usage telemetry, token-savings measurement
- [docs/api/cli.md](cli.md) — `mneme codegraph index/search/node/callers/callees/impact/trace/files/status/hooks`
