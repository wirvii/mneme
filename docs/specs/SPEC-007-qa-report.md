# SPEC-007 QA Report -- 1-hop Graph Expansion en mem_search

| Campo     | Valor |
|-----------|-------|
| Spec      | SPEC-007 |
| Veredicto | **REQUIRES CHANGES** |
| Fecha     | 2026-04-30 |
| Tester    | qa-tester (claude-opus-4-6) |

---

## Issues

### IMPORTANTES (bloquean)

**IMP-1: `--no-graph` CLI flag does not work**

The spec (section 6.3) defines the CLI contract as `--graph / --no-graph`. The help text explicitly says `(use --no-graph to disable)`. However, running `mneme search 'query' --no-graph` produces `Error: unknown flag: --no-graph`. pflag/Cobra do not support `--no-<flag>` syntax natively for `BoolVar` flags.

- **File**: `internal/cli/search.go:115`
- **Impact**: Users cannot disable graph expansion via the documented CLI syntax
- **Workaround**: `--graph=false` works
- **Fix**: Register a separate `no-graph` bool flag that sets `flagGraph = false`, or document `--graph=false` as the correct syntax and remove `--no-graph` from the help text. Alternative: use pflag's `BoolVarP` and add `--no-graph` as an explicit separate flag with `MarkHidden`.

**IMP-2: Missing frontend tests required by spec**

The spec (section 9) mandates 4 frontend tests:
- `TestMemSearch_IncludeGraph_Default`
- `TestMemSearch_IncludeGraph_False`
- `TestHandleSearch_IncludeGraph_QueryParam`
- `TestSearchCmd_GraphFlag`

None of these exist. The `include_graph` parameter parsing in MCP (via `json.Unmarshal`), HTTP (via `r.URL.Query().Get`), and CLI (via `BoolVar`) are all untested.

- **Impact**: No automated verification that frontends correctly pass `include_graph` to the service layer
- **Files**: `internal/mcp/*_test.go`, `internal/http/*_test.go`, `internal/cli/*_test.go`

### MENORES (deben corregirse)

**MIN-1: `handleSearch` godoc missing `include_graph` param**

The godoc comment on `handleSearch` (line 310-318 of `internal/http/server.go`) lists all query parameters but omits `include_graph`.

**MIN-2: HTTP `include_graph` parsing is case-sensitive**

Line 353 of `internal/http/server.go`: `b := igStr == "true" || igStr == "1"`. Passing `?include_graph=TRUE` or `?include_graph=True` would silently be treated as `false`. Using `strconv.ParseBool` would be more robust and consistent with how `limit` is parsed with `strconv.Atoi`.

**MIN-3: Comment in `fuseAndRank` is outdated**

Line 316 of `internal/service/search.go`: `// Memory found only by vector search` -- this comment was written before graph expansion. Now this path is also hit by graph-only results. Should say "Memory not in FTS5 results (vector-only or graph-only)".

**MIN-4: `slog.InfoContext` in hot path of `graphExpand`**

Two INFO-level log lines fire on every graph-expanded search (lines 402-405 and 484-488 in search.go). At high query volume these will be noisy. Consider `slog.DebugContext` or gating behind a verbose config flag. The spec does say "slog.Info with event=graph_expansion" so this is per-spec, but worth reconsidering for production.

---

## Pruebas funcionales

| Test | Resultado |
|------|-----------|
| `make test` (20 packages) | PASS |
| `make test-race` (20 packages) | PASS |
| `golangci-lint run` | N/A (not installed on this machine) |
| `make build` | PASS |
| Store tests: `GetStrongRelations/GetEntityMemoryIDs/BatchTouchRelations` | 9/9 PASS |
| Service tests: `Search.*Graph` (5 tests) | 5/5 PASS |
| Config tests: `Expansion*` (4 test groups) | 4/4 PASS |
| DB migration 008 tests (4 tests) | 4/4 PASS (via make test) |
| Benchmark: `BenchmarkSearch_GraphExpansion_5K` | with_graph=55.5ms, without_graph=52.2ms, overhead=3.3ms (<50ms budget) |

## E2E CLI validation

| Scenario | Result |
|----------|--------|
| Migration 008: index created | PASS - `idx_memory_entities_entity` exists |
| Migration 008: schema version 8 | PASS |
| Search `--graph`: surfaces neighbor via strong relation (w=0.7 > 0.3 threshold) | PASS - "secrets" appears at boosted relevance |
| Search `--graph=false`: neighbor only via vector, not graph-boosted | PASS |
| Threshold respected: relation w=0.2 < 0.3 not traversed | PASS - "circuit breaker" not boosted, deploy-circuit not touched |
| Touch verification: `last_traversed_at` updated for traversed relation | PASS - only rel-deploy-secrets touched |
| Backwards compat: fresh DB, no relations | PASS - no crash |
| `--no-graph` flag | **FAIL** - `Error: unknown flag: --no-graph` |

## Flujos trazados end-to-end

### Flujo 1: Graph expansion (happy path)
```
CLI --graph=true
  -> model.SearchRequest{IncludeGraph: &true}
    -> service.Search()
      -> fts5SearchAll() -> FTS5 results
      -> vectorSearchAll() -> vector results (NopEmbedder: empty)
      -> includeGraph = true (config.Graph.ExpansionEnabled && *req.IncludeGraph)
      -> fuseAndRank(ctx, fts, vec, limit, includeGraph=true)
        -> preliminary RRF(fts+vec) -> top-K seeds
        -> graphExpand(ctx, seedIDs)
          -> projectStore.GetMemoryEntities(seedID) -> entities
          -> projectStore.GetStrongRelations(entityID, threshold, cap) -> strong rels
          -> projectStore.GetEntityMemoryIDs(neighborEntityID) -> memory IDs
          -> score = max(rel_weight * 1/seed_rank)
        -> batchTouchRelations(ctx, touchIDs)
          -> projectStore.BatchTouchRelations(unique, now) -> UPDATE SQL
        -> 3-way RRF(fts + vec + graph, k=60)
      -> build SearchResult with RelevanceScore
    -> model.SearchResponse
```
Verified: complete, no broken links.

### Flujo 2: Graph disabled
```
CLI --graph=false
  -> model.SearchRequest{IncludeGraph: &false}
    -> service.Search()
      -> includeGraph = false (*req.IncludeGraph overrides config)
      -> len(vectorResults) > 0 || includeGraph => evaluates based on vectors
      -> if NopEmbedder: reRankFTS5 (legacy path)
      -> if real embedder: fuseAndRank with includeGraph=false (2-channel)
```
Verified: correct bypass.

### Flujo 3: MCP frontend
```
JSON-RPC: {"method":"tools/call","params":{"name":"mem_search","arguments":{"query":"x","include_graph":true}}}
  -> handlers.handleMemSearch()
    -> json.Unmarshal(raw, &req) // SearchRequest.IncludeGraph populated via json tag
    -> svc.Search(ctx, req)
```
Verified: json tag on *bool field correctly deserializes.

### Flujo 4: HTTP frontend
```
GET /v1/memories/search?q=x&include_graph=true
  -> handleSearch()
    -> r.URL.Query().Get("include_graph") -> "true"
    -> b := "true" == "true" -> true
    -> req.IncludeGraph = &b
    -> svc.Search(ctx, req)
```
Verified: correct, but case-sensitive (MIN-2).

## Validacion de spec

| Criterio de aceptacion | Estado |
|------------------------|--------|
| 1. Graph expansion surfaces topologically connected memories | PASS |
| 2. 3-way RRF fusion correcta | PASS |
| 3. Performance <50ms overhead | PASS (3.3ms) |
| 4. include_graph=false preserves current behavior | PASS |
| 5. Touch relations update last_traversed_at | PASS |
| 6. Three frontends consistent | PARTIAL FAIL (CLI --no-graph broken) |

## Lineamientos

| Check | Status |
|-------|--------|
| Imports inward only | PASS |
| golangci-lint zero warnings | SKIPPED (not installed) |
| make test green | PASS (20 packages) |
| make test-race green | PASS (20 packages) |
| Godoc on new exports | PASS |
| Conventional commits | PASS (7 commits) |
| Migration 008 idempotent | PASS |

---

## VEREDICTO: REQUIRES CHANGES

2 IMPORTANT issues block approval. 4 MINOR issues should be corrected.
