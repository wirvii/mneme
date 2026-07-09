# mneme -- API Reference

mneme exposes the same service layer through three frontends: **MCP** (57
tools over JSON-RPC 2.0 stdio, primary), **HTTP** (10 REST endpoints under
`/v1/`), and **CLI** (35 top-level commands, Cobra). This page is an index --
the full contract for every tool, endpoint, and command lives in
[docs/api/](api/).

**Protocol:** JSON-RPC 2.0 over stdio (line-delimited) · **ProtocolVersion:** `2024-11-05` · **Start:** `mneme mcp` (or `mneme mcp --tools agent`)

The server responds to three methods: `initialize`, `tools/list`, and
`tools/call`. All tool results are returned as a single `text` content block
containing a JSON string (the `codegraph_*` family returns plain text instead
of JSON -- see [docs/api/codegraph.md](api/codegraph.md)).

```json
// Request
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"my-agent","version":"1.0"}}}

// Response
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"mneme","version":"1.17.0"}}}

// Notification (no response expected)
{"jsonrpc":"2.0","method":"notifications/initialized"}
```

`serverInfo.version` reflects the running binary's build version (ldflags),
not a fixed string -- expect it to match `mneme version`.

---

## MCP tool families (57 tools)

Rule: every MCP tool appears in exactly **one** of these files.

| Family | Count | Reference | Concept guide |
|--------|-------|-----------|---------------|
| `mem_*` | 14 | [docs/api/memory.md](api/memory.md) | [docs/GRAPH.md](GRAPH.md), [docs/RULES.md](RULES.md) |
| `backlog_*` + `spec_*` + `lane_*` + `init` | 4+8+5+1=18 | [docs/api/sdd.md](api/sdd.md) | [docs/lanes.md](lanes.md), [docs/init.md](init.md) |
| `codegraph_*` | 10 | [docs/api/codegraph.md](api/codegraph.md) | [docs/codegraph.md](codegraph.md) |
| `skills_*` | 7 | [docs/api/skills.md](api/skills.md) | [docs/skills.md](skills.md) |
| `model_*` | 3 | [docs/api/models.md](api/models.md) | [docs/models.md](models.md) |
| `conflicts_*` | 5 | [docs/api/conflicts.md](api/conflicts.md) | [docs/conflicts.md](conflicts.md) |

14 + 18 + 10 + 7 + 3 + 5 = **57**.

## Transport references

| Reference | Contents |
|-----------|----------|
| [docs/api/cli.md](api/cli.md) | All 33 top-level CLI commands, flags verified against `./mneme <cmd> --help` |
| [docs/api/http.md](api/http.md) | All 10 HTTP routes under `/v1/`, plus the `/explore` suffix and the "HTTP parity gaps" table |

## MCP error codes

Shared across all tool families:

| Code | Name | Triggered when |
|------|------|----------------|
| `-32600` | Invalid Request | Malformed JSON-RPC envelope |
| `-32601` | Method not found | Unknown MCP method |
| `-32602` | Invalid params | Missing required params, type mismatch, schema/domain validation |
| `-32603` | Internal error | DB error, unexpected failure, dependent service unavailable |
| `-32000` | Not found | Unknown memory/entity/relation/backlog/spec/skill ID |

## See also

- [README.md](../README.md) -- project overview, quickstart, feature tour
- [docs/ARCHITECTURE.md](ARCHITECTURE.md) -- layered design and package boundaries
- [docs/GUIDE.md](GUIDE.md) -- end-to-end user guide for humans and agents
