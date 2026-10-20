<p align="center"><img src="../assets/logo.svg" width="56" alt="mneme"></p>

# mneme -- API Reference

mneme exposes the same service layer through three frontends: **MCP** (82
tools over JSON-RPC 2.0 stdio, primary), **HTTP** (10 REST endpoints under
`/v1/`), and **CLI** (42 mneme-registered top-level commands, Cobra). This page is an index --
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
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"mneme","version":"1.33.0"}}}

// Notification (no response expected)
{"jsonrpc":"2.0","method":"notifications/initialized"}
```

`serverInfo.version` reflects the running binary's build version (ldflags),
not a fixed string -- expect it to match `mneme version`.

---

## MCP tool families (59 tools on this branch)

Rule: every MCP tool appears in exactly **one** of these files.

| Family | Count | Reference | Concept guide |
|--------|-------|-----------|---------------|
| `mem_*` (incl. `mem_promote`) | 15 | [docs/api/memory.md](api/memory.md) | [docs/GRAPH.md](GRAPH.md), [docs/RULES.md](RULES.md), [docs/team-memory.md](team-memory.md) |
| `backlog_*` + `spec_*` + `lane_*` + `init` | 6+9+5+1=21 | [docs/api/sdd.md](api/sdd.md) | [docs/lanes.md](lanes.md), [docs/init.md](init.md) |
| `codegraph_*` | 10 | [docs/api/codegraph.md](api/codegraph.md) | [docs/codegraph.md](codegraph.md) |
| `profile_*` + `project_*` + `app_*` + `scaffold_*` | 8+1+1+1=11 | [docs/profiles.md](profiles.md) (no dedicated `docs/api/` split yet) | [docs/profiles.md](profiles.md) |
| `speech_*` | 2 | [docs/speech.md](speech.md) | [docs/speech.md](speech.md) |

15 + 21 + 10 + 11 + 2 = **59** (SPEC-163 took 23 tools out of the 82 the server exposed before it; sibling v2.0 specs subtract their own, so the integrated count is lower still). The two host-local
`speech_*` tools intentionally have no HTTP counterpart because they control
the current user's audio device. The `quality_*` family (5 tools) existed
from SPEC-115 through SPEC-137 and was retired whole by SPEC-158, along
with the certification mechanism it served.

**CLI-only since v2.0 (SPEC-163):** `skills_*` (7), `model_*` (3), `conflicts_*` (5), `subagent_*` (6), `sdd_status` and `sdd_import` left the MCP server; each keeps its full capability as a CLI command. References with the old-tool to command table: [docs/api/skills.md](api/skills.md), [docs/api/models.md](api/models.md), [docs/api/conflicts.md](api/conflicts.md), [docs/api/subagents.md](api/subagents.md), [docs/api/sdd.md](api/sdd.md#sdd-git-native-commands-cli-only-since-v20-spec-163).

## Transport references

| Reference | Contents |
|-----------|----------|
| [docs/api/cli.md](api/cli.md) | CLI commands and flags verified against `./mneme <cmd> --help` |
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
