# SPEC-007 -- 1-hop Graph Expansion en mem_search con RRF de 3 canales

| Campo         | Valor                                                          |
|---------------|----------------------------------------------------------------|
| **ID**        | SPEC-007                                                       |
| **Epic**      | EPIC-2 -- Grafo con peso + 1-hop expansion                    |
| **Backlog**   | BL-007                                                         |
| **Estado**    | speccing -> specced                                            |
| **Owner**     | architect                                                      |
| **Fecha**     | 2026-04-30                                                     |
| **Deps**      | SPEC-005 (completada), SPEC-006 (completada) -- weighted relations, Hebbian, idx_relations_weight, FindRelationBidirectional, TouchRelation, HebbianWorkerPool, AccessTracker |
| **Memorias**  | `roadmap/v2-master-plan`, `architecture/scoring-formulas`, `spec/SPEC-005-weighted-relations-design`, `spec/SPEC-005-implementation-notes`, `spec/SPEC-006-hebbian-design`, `spec/SPEC-006-implementation-notes`, `architecture/memory-model` |

---

## 1. Contexto y motivacion

### El problema

`mem_search` hoy combina dos senales: FTS5 BM25 (exact token match) y vector similarity (TF-IDF cosine). Esto pierde una tercera dimension critica: **la topologia del knowledge graph**. Dos memorias que estan directamente conectadas en el grafo (via entidades compartidas o relaciones explicitas) deberian co-aparecer en resultados de busqueda incluso cuando no comparten tokens ni embeddings similares.

Ejemplo concreto: el agente busca "JWT RS256" y obtiene una memoria sobre el auth service. Esa memoria esta conectada via relacion `depends_on` (weight=0.9) a una memoria sobre el key rotation schedule. Sin expansion de grafo, la segunda memoria solo aparece si contiene "JWT" o "RS256" textualmente.

### Que habilita

- **SPEC-G4 (mem_explore):** El tool de exploracin navegara el grafo expandido. Esta spec provee la primitiva de expansion que `mem_explore` reutilizara.
- **SPEC-P1 (PPR):** Personalized PageRank reemplazara la expansion 1-hop con un random walk completo, pero la integracion con RRF (3 canales) que esta spec establece sera reutilizada directamente.
- **Mejor recall sin sacrificar precision:** RRF con k=60 garantiza que la senal de grafo no domina el ranking; solo eleva memorias que ya tienen alguna relevancia por proximidad topologica.

### Que prepararon SPEC-005 y SPEC-006

- **SPEC-005:** `idx_relations_weight` para ORDER BY weight DESC (`internal/db/migrations/007_weighted_relations.sql:35`), `TouchRelation` para marcar traversals (`internal/store/entity.go:244-258`), weights normalizados [0.0, 1.0].
- **SPEC-006:** Pesos que reflejan uso real via Hebbian auto-strengthening, no solo defaults estaticos. `FindRelationBidirectional` (`internal/store/entity.go:355-375`) para busqueda en ambas direcciones. `internal/graph/` package con AccessTracker y HebbianWorkerPool.

---

## 2. Decisiones de diseno

### D1. RRF generalizado -- NO refactorizar la signature de `RRFScore`

**Problema:** El backlog propone refactorizar `RRFScore` a `FuseRRF([]RankedList, k int)` donde `RankedList = {Weight float64, IDs []string}`.

**Decision:** NO refactorizar. La signature actual de `scoring.RRFScore(ranks []RankedResult, k float64) []FusedResult` **ya es N-lista por diseno**. Cada `RankedResult` lleva su propio `Weight`, asi que agregar un tercer canal es simplemente agregar mas `RankedResult` entries al slice.

**Evidencia:** `internal/scoring/rrf.go:50` -- `RRFScore` itera sobre un flat `[]RankedResult` y acumula `scores[r.ID] += r.Weight / (k + float64(r.Rank))`. No hay concepto de "lista" separada; la lista se infiere del Weight. Agregar graph results es:

```go
graphRanks := make([]scoring.RankedResult, len(graphResults))
for i, gr := range graphResults {
    graphRanks[i] = scoring.RankedResult{
        ID:     gr.MemoryID,
        Rank:   i + 1,
        Weight: weightGraph,
    }
}
all := append(append(ftsRanks, vecRanks...), graphRanks...)
fused := scoring.RRFScore(all, scoring.DefaultRRFK)
```

**Backward compatibility:** Zero cambios en `scoring.RRFScore`. Zero cambios en `service.Context()` (`internal/service/context.go`) que NO usa RRF (usa su propio scoring por effective importance). El unico caller afectado es `fuseAndRank` en `internal/service/search.go:200-319`.

**Justificacion:** YAGNI. Una abstraccion `RankedList` agrega complejidad sin beneficio cuando el flat slice ya resuelve el problema. Si SPEC-P3 (RRF de 3 canales con PPR) necesita una API diferente, puede wrapper sobre la misma funcion.

### D2. Graph expansion algorithm -- expansion bidireccional con fan-out cap

**Decision:** Para cada seed memory (top-K de BM25+vector fusion), expandir 1 hop via relations con weight > threshold, con fan-out cap de 50 por seed.

**Pseudocodigo exacto:**

```
func graphExpand(ctx, store, seedIDs []string, threshold float64, fanOutCap int) []GraphResult:
    accumulated = map[memoryID]float64{}  // memoryID -> accumulated graph score
    touchIDs    = []string{}               // relation IDs to touch async

    for rank, seedID in enumerate(seedIDs):  // rank is 0-based
        seedWeight = 1.0 / float64(rank + 1)  // inverse rank of seed

        // Step 1: Get entities linked to this seed memory
        entityIDs = store.GetMemoryEntities(ctx, seedID)
        if len(entityIDs) == 0:
            continue

        // Step 2: For each entity, get relations above threshold
        neighborEntityIDs = set{}
        relationMap = map[entityID][]Relation{}

        for _, entityID in entityIDs:
            // Single query: both directions, weight > threshold, ORDER BY weight DESC, LIMIT fanOutCap
            rels = store.GetStrongRelations(ctx, entityID, threshold, fanOutCap)
            for _, rel in rels:
                neighborID = rel.otherEnd(entityID)  // source_id if entityID==target, vice versa
                neighborEntityIDs.add(neighborID)
                relationMap[neighborID] = append(relationMap[neighborID], rel)
                touchIDs = append(touchIDs, rel.ID)

        // Step 3: For each neighbor entity, find memories linked to it
        for _, neighborEntityID in neighborEntityIDs:
            memoryIDs = store.GetEntityMemories(ctx, neighborEntityID)
            for _, memID in memoryIDs:
                if memID == seedID:
                    continue  // don't re-score the seed itself

                // Score: max relation weight to this neighbor * seed inverse rank
                maxRelWeight = 0.0
                for _, rel in relationMap[neighborEntityID]:
                    if rel.Weight > maxRelWeight:
                        maxRelWeight = rel.Weight
                maxRelWeight = max(rels that connect to this neighborEntityID)

                score = maxRelWeight * seedWeight
                accumulated[memID] = max(accumulated[memID], score)  // max, not sum

    // Sort by accumulated score descending
    results = sorted(accumulated, by score desc)
    return results, touchIDs
```

**Scoring formula:** `graph_score(mem) = max over all paths: (rel_weight * (1 / seed_rank))`.

- `rel_weight` es el peso de la relacion mas fuerte al vecino.
- `1 / seed_rank` decae linealmente con el rank del seed. El seed #1 tiene influencia 1.0; el seed #10 tiene 0.1.
- Se usa `max` (no `sum`) cuando una memoria aparece desde multiples seeds. Motivo: `sum` inflaria memorias que son hub nodes (conectados a muchas seeds por ser genericos). `max` premia la conexion mas fuerte individual.

**Fan-out cap:** 50 relaciones por seed entity. Esto protege contra entity hubs (e.g., un entity "Go" conectado a 500 memorias). El cap se aplica en SQL con `ORDER BY weight DESC LIMIT ?`, asi que las relaciones mas fuertes sobreviven.

### D3. Touch de relaciones traversadas -- pool dedicado, NO reutilizar HebbianWorkerPool

**Decision:** Las relaciones traversadas durante la expansion se tocan (update `last_traversed_at`) via un batch SQL despues de la expansion, **NO** via el HebbianWorkerPool.

**Justificacion:**
1. **Semantica distinta:** Hebbian fortalece pesos (`UpdateRelationWeight` con delta). Touch solo actualiza timestamps (`TouchRelation`). Mezclar ambas en el mismo canal requeriria un event type discriminator que complica `StrengtheningEvent` sin beneficio.
2. **Timing distinto:** Hebbian es fire-and-forget async. Touch debe completarse antes de retornar (o al menos intentarlo) para que el `last_traversed_at` este actualizado para el siguiente decay sweep.
3. **Volumen acotado:** El fan-out cap de 50 * top-K seeds (tipicamente 10) = max 500 relaciones. Un batch `UPDATE relations SET last_traversed_at = ? WHERE id IN (?, ?, ...)` es O(1) para SQLite. No justifica un pool async.

**Implementacion:** Despues de `graphExpand()`, un unico batch SQL:

```sql
UPDATE relations SET last_traversed_at = ? WHERE id IN (?, ?, ..., ?)
```

Con parametros generados del slice `touchIDs`. Si falla, log + skip (best-effort, mismo patron que Hebbian).

### D4. Integracion con `service.Context()` -- NO tocar

**Decision:** `internal/service/context.go` NO se modifica. El contexto service NO usa RRF -- usa su propio scoring por `EffectiveImportance` con boosts (architecture x1.5, focus +0.3). El refactor de RRF a 3 canales esta confinado a `internal/service/search.go`.

**Verificacion:** `internal/service/context.go` no importa `scoring.RRFScore`. La fusion en context es manual (scored candidates sorted by effective importance). La unica conexion es que ambos usan `scoring.EffectiveImportance` para decay, pero eso no cambia.

**Impacto SPEC-P3 (futuro):** SPEC-P3 en EPIC-4 esta definida como "RRF de 3 canales (BM25 + vector + PPR)" y listada como spec separada del roadmap. Esa spec podra decidir si context tambien adopta RRF. Esta spec no le cierra la puerta.

### D5. Param naming -- `include_graph` en todos los frontends

**Decision:** Usar `include_graph` (boolean, default `true`) consistente en los tres frontends:

| Frontend | Param | Default |
|----------|-------|---------|
| MCP      | `include_graph` (boolean) | `true` |
| HTTP     | `include_graph` query param (`true`/`false`) | `true` |
| CLI      | `--graph` / `--no-graph` | `--graph` (on by default) |

**Justificacion:**
- El naming `include_graph` es descriptivo y autoexplicativo. Un agente que lee el schema entiende que controla la expansion de grafo.
- Default `true` porque la expansion mejora recall sin degradar precision (gracias a RRF). El agente no deberia tener que opt-in.
- CLI usa el patron `--flag/--no-flag` de cobra, consistente con `--full`/`--json` en search.
- No existe precedente de flags `include_*` en los frontends actuales, pero el patron es estandar en APIs de busqueda.

### D6. Nuevos defaults en `[graph]` config

**Decision:** Agregar 3 nuevos campos a `GraphConfig` en `internal/config/config.go`:

```go
// ExpansionEnabled controls whether 1-hop graph expansion is active
// during mem_search. Set to false to disable graph-augmented retrieval
// without affecting Hebbian strengthening or edge decay.
// Default: true.
ExpansionEnabled bool `toml:"expansion_enabled"`

// ExpansionThreshold is the minimum relation weight required for a
// relation to be followed during 1-hop expansion. Relations below this
// threshold are ignored. This filters out weak/noise edges.
// Default: 0.3.
ExpansionThreshold float64 `toml:"expansion_threshold"`

// ExpansionFanOutCap is the maximum number of relations to follow per
// entity during 1-hop expansion. Relations are sorted by weight DESC
// and only the top N are followed.
// Default: 50.
ExpansionFanOutCap int `toml:"expansion_fan_out_cap"`

// ExpansionSeedTopK is the number of top-ranked seeds (from BM25+vector
// fusion) to expand via the graph. Limits the cost of expansion by only
// expanding the most relevant results.
// Default: 10.
ExpansionSeedTopK int `toml:"expansion_seed_top_k"`
```

**Valores:**
- `expansion_threshold=0.3` (no 0.5 como propuso el backlog). Justificacion: Hebbian initial weight es 0.1 (`internal/config/config.go:322`), y despues de 5 co-accesos sube a 0.35. Un threshold de 0.5 excluiria la mayoria de relaciones Hebbianas tempranas. 0.3 permite que relaciones con 4+ co-accesos participen.
- `expansion_fan_out_cap=50` -- suficiente para cubrir entidades con muchas relaciones sin explotar en hubs.
- `expansion_seed_top_k=10` -- los top 10 de la fusion FTS5+vector. Mas alla de 10, la relevancia del seed cae tanto que expandir no aporta.

**Channel weight:** `weightGraph = 0.6` como constante en `internal/service/search.go` (junto a `weightFTS5 = 1.0` y `weightVector = 0.8`). Menor que ambos porque la senal de grafo es indirecta (topologia, no contenido). Suficiente para elevar memorias relevantes sin dominar el ranking.

### D7. Performance budget -- <50ms overhead en proyecto tipico

**Objetivo:** La expansion de grafo debe agregar **<50ms** al tiempo de busqueda en un proyecto con 5K memorias, 10K relaciones, y 20K memory_entities.

**Plan SQL:**

El cuello de botella es la expansion: para cada seed, buscar entidades, luego relaciones, luego memorias vecinas. La query critica es:

```sql
-- GetStrongRelations: relaciones bidireccionales above threshold, weight-ordered
SELECT id, source_id, target_id, type, weight, metadata, created_at, last_traversed_at
FROM relations
WHERE (source_id = ? OR target_id = ?) AND weight > ?
ORDER BY weight DESC
LIMIT ?
```

**Indice:** `idx_relations_source` y `idx_relations_target` (ya creados en migration 002, `internal/db/migrations/002_knowledge_graph.sql:26-27`). SQLite puede usar uno de los dos indices (no un merge). Para la query OR, SQLite tipicamente hace un full scan de `relations` filtrado por weight, que en 10K rows es ~1ms.

**Alternativa mas eficiente:** Dos queries separadas (source + target) unidas en Go:

```sql
SELECT ... FROM relations WHERE source_id = ? AND weight > ? ORDER BY weight DESC LIMIT ?
UNION ALL
SELECT ... FROM relations WHERE target_id = ? AND weight > ? ORDER BY weight DESC LIMIT ?
```

Esto permite que SQLite use `idx_relations_source` para la primera y `idx_relations_target` para la segunda. Con LIMIT aplicado antes del UNION, el cost es O(log n) por query.

**Nuevo metodo en store:** `GetStrongRelations(ctx, entityID, threshold, limit)` que internamente hace las dos queries y las merge en Go. No se crea indice compuesto nuevo porque `idx_relations_source` y `idx_relations_target` ya cubren el caso.

**GetEntityMemories:** Query inversa de `GetMemoryEntities`:

```sql
SELECT me.memory_id
FROM memory_entities me
WHERE me.entity_id = ?
```

Usa el PK `(memory_id, entity_id)` de `memory_entities` -- pero no hay indice por `entity_id`. **Necesitamos un nuevo indice:**

```sql
CREATE INDEX IF NOT EXISTS idx_memory_entities_entity ON memory_entities(entity_id);
```

Sin este indice, la query haria full scan de `memory_entities` para cada neighbor entity. Con 20K rows esto es ~5ms por scan, y con 500 neighbor entities seria catastrofico.

**Migration 008:** Un nuevo migration file para este indice:

```sql
-- 008_graph_expansion.sql
CREATE INDEX IF NOT EXISTS idx_memory_entities_entity ON memory_entities(entity_id);
INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (8, datetime('now'));
```

**Budget breakdown** (10 seeds, 5K memorias, 10K relaciones, 20K memory_entities):
- GetMemoryEntities per seed: 10 x ~0.1ms = 1ms (PK index)
- GetStrongRelations per entity (assume 2 entities/seed avg): 20 x ~0.5ms = 10ms (indexed)
- GetEntityMemories per neighbor: ~200 unique neighbors x ~0.1ms = 20ms (con nuevo indice)
- TouchRelation batch: 1 x ~2ms = 2ms (batch UPDATE)
- In-memory scoring + dedup: ~1ms
- **Total: ~34ms** (within 50ms budget)

**Nota sobre `memory_entities` cardinality:** En un proyecto con 5K memorias y un promedio de 4 entidades por memoria, la tabla tiene 20K rows. Sin indice en `entity_id`, cada lookup es un scan. Con el indice, es O(log n).

---

## 3. Modelo del expansion algorithm

### Data flow completo

```
                         mem_search("JWT RS256")
                                  |
                     +------------+------------+
                     |                         |
              Signal 1: FTS5              Signal 2: Vector
              (BM25 ranked)               (TF-IDF cosine)
                     |                         |
                     +----------+  +-----------+
                                |  |
                     Fusion FTS5+Vector (RRF, k=60)
                                |
                        Top-K seeds (10)
                                |
                        Signal 3: Graph Expansion
                                |
                     +----------+-----------+
                     |                      |
              For each seed:          Accumulate:
              1. GetMemoryEntities    graph_score(mem) =
              2. GetStrongRelations     max(rel_weight * 1/seed_rank)
              3. GetEntityMemories
                     |                      |
                     +----------+-----------+
                                |
                     3-way RRF Fusion
                     (FTS5=1.0, Vector=0.8, Graph=0.6)
                                |
                     Re-rank by FinalScore + RRF
                                |
                         Final results
                                |
                     Touch traversed relations (batch)
                     Hebbian tracking (top-3, existing)
```

### Integracion en `fuseAndRank` (`internal/service/search.go`)

La funcion actual `fuseAndRank(ctx, ftsResults, vectorResults, limit)` se refactoriza a:

```go
func (svc *MemoryService) fuseAndRank(
    ctx context.Context,
    ftsResults []model.SearchResult,
    vectorResults []store.VectorResult,
    limit int,
    includeGraph bool,    // new param from SearchRequest
) []model.SearchResult {
    // ... existing FTS5 + vector RankedResult generation (lines 202-219) ...

    // === Signal 3: Graph expansion (new) ===
    var graphRanks []scoring.RankedResult
    if includeGraph && svc.config.Graph.ExpansionEnabled {
        // Build seed IDs from the initial FTS5+vector fusion
        preliminary := scoring.RRFScore(append(ftsRanks, vecRanks...), scoring.DefaultRRFK)
        topK := svc.config.Graph.ExpansionSeedTopK
        if topK > len(preliminary) {
            topK = len(preliminary)
        }
        seedIDs := make([]string, topK)
        for i := 0; i < topK; i++ {
            seedIDs[i] = preliminary[i].ID
        }

        graphResults, touchIDs := svc.graphExpand(ctx, seedIDs)
        graphRanks = make([]scoring.RankedResult, len(graphResults))
        for i, gr := range graphResults {
            graphRanks[i] = scoring.RankedResult{
                ID:     gr.MemoryID,
                Rank:   i + 1,
                Weight: weightGraph,
            }
        }

        // Async touch traversed relations
        svc.batchTouchRelations(ctx, touchIDs)
    }

    all := append(append(ftsRanks, vecRanks...), graphRanks...)
    fused := scoring.RRFScore(all, scoring.DefaultRRFK)

    // ... rest of fuseAndRank (building results, loading vector-only memories, etc.) ...
}
```

### New types

```go
// GraphResult holds a memory discovered via 1-hop graph expansion.
// Used internally; not exposed to frontends.
type GraphResult struct {
    MemoryID   string
    GraphScore float64  // max(rel_weight * 1/seed_rank)
}
```

This type lives in `internal/service/search.go` (unexported), not in `model/`.

---

## 4. Modelo del touch flow

### Batch touch implementation

```go
// batchTouchRelations updates last_traversed_at for a list of relation IDs.
// Best-effort: failures are logged but do not affect search results.
func (svc *MemoryService) batchTouchRelations(ctx context.Context, relationIDs []string) {
    if len(relationIDs) == 0 {
        return
    }

    // Dedup relation IDs
    seen := make(map[string]bool, len(relationIDs))
    unique := make([]string, 0, len(relationIDs))
    for _, id := range relationIDs {
        if !seen[id] {
            seen[id] = true
            unique = append(unique, id)
        }
    }

    // Delegate to store batch method
    if err := svc.projectStore.BatchTouchRelations(ctx, unique, time.Now().UTC()); err != nil {
        slog.Warn("graph expansion: batch touch failed",
            "event", "graph_touch_error",
            "count", len(unique),
            "error", err,
        )
    }
}
```

### New store method

```go
// BatchTouchRelations updates last_traversed_at for multiple relations in a
// single statement. This is the bulk version of TouchRelation, used after
// graph expansion to mark traversed edges for future decay eligibility.
func (s *MemoryStore) BatchTouchRelations(ctx context.Context, ids []string, now time.Time) error {
    if len(ids) == 0 {
        return nil
    }

    // Build parameterised query: UPDATE ... WHERE id IN (?, ?, ...)
    placeholders := make([]string, len(ids))
    args := make([]any, 0, len(ids)+1)
    args = append(args, now.UTC().Format(time.RFC3339Nano))
    for i, id := range ids {
        placeholders[i] = "?"
        args = append(args, id)
    }

    q := fmt.Sprintf(
        "UPDATE relations SET last_traversed_at = ? WHERE id IN (%s)",
        strings.Join(placeholders, ","),
    )

    _, err := s.db.ExecContext(ctx, q, args...)
    if err != nil {
        return fmt.Errorf("store: batch touch relations: %w", err)
    }
    return nil
}
```

---

## 5. Scope

### Paquetes/archivos afectados

| Paquete | Archivo | Tipo de cambio |
|---------|---------|---------------|
| `internal/config` | `config.go` | Modificacion: agregar 4 campos a GraphConfig + defaults + validation |
| `internal/config` | `config_test.go` | Modificacion: tests para nuevos campos |
| `internal/db/migrations` | `008_graph_expansion.sql` | Nuevo: indice `idx_memory_entities_entity` |
| `internal/db` | `migrate_test.go`, `schema_version_test.go` | Modificacion: update version assertions a 8 |
| `internal/store` | `entity.go` | Modificacion: `GetStrongRelations`, `GetEntityMemories`, `BatchTouchRelations` |
| `internal/store` | `entity_test.go` | Modificacion: tests para nuevos metodos |
| `internal/model` | `search.go` | Modificacion: `IncludeGraph` campo en SearchRequest |
| `internal/service` | `search.go` | Modificacion: graph expansion, 3-way fusion, touch batch |
| `internal/service` | `search_test.go` | Nuevo/Modificacion: tests para expansion + fusion |
| `internal/mcp` | `tools.go` | Modificacion: `include_graph` param en `mem_search` schema |
| `internal/http` | `server.go` | Modificacion: `include_graph` query param en handleSearch |
| `internal/cli` | `search.go` | Modificacion: `--graph`/`--no-graph` flags |

### Fuera de scope

- **`service.Context()`:** No se modifica. El context service tiene su propio scoring que no usa RRF (D4).
- **PPR / Personalized PageRank:** SPEC-P1 (EPIC-4). Esta spec establece la infraestructura de RRF 3 canales que PPR reutilizara.
- **`mem_explore` tool:** SPEC-G4 (BL-008). Exploracion interactiva del grafo es un tool aparte.
- **Cross-scope graph expansion:** La expansion opera solo dentro del store de la scope seleccionada. Expansion cross-DB es out of scope por las mismas razones que SPEC-006 D1 (FK constraints, scopes never leak).
- **SearchResponse cambios:** No se agregan campos `GraphScore` al response publico. La senal de grafo se refleja en `RelevanceScore` via RRF. Transparencia total del graph channel es SPEC-G4.

---

## 6. Contratos

### 6.1 MCP — `mem_search`

**Cambio:** Agregar `include_graph` al InputSchema.

```json
{
  "name": "mem_search",
  "inputSchema": {
    "type": "object",
    "required": ["query"],
    "properties": {
      "query": { "type": "string" },
      "project": { "type": "string" },
      "scope": { "type": "string", "enum": ["global", "org", "project"] },
      "type": { "type": "string" },
      "limit": { "type": "integer", "minimum": 1, "maximum": 50 },
      "include_superseded": { "type": "boolean" },
      "include_graph": {
        "type": "boolean",
        "description": "Enable 1-hop graph expansion. Augments BM25+vector results with topologically related memories from the knowledge graph. Default: true."
      }
    }
  }
}
```

**Response:** Sin cambios. `SearchResponse` mantiene su formato actual `{ results: [...], total: N, query: "..." }`. La senal de grafo se absorbe en `relevance_score`.

### 6.2 HTTP — `GET /v1/memories/search`

**Cambio:** Agregar query param `include_graph`.

```
GET /v1/memories/search?q=JWT+RS256&include_graph=true
GET /v1/memories/search?q=patterns&include_graph=false
```

**Formato de response:** Sin cambios. Mismo `SearchResponse` JSON.

**Ejemplo response (no changes):**
```json
{
  "results": [
    {
      "id": "019dde01-...",
      "type": "architecture",
      "title": "Auth service JWT RS256 setup",
      "preview": "...JWT RS256...",
      "relevance_score": 12.5,
      "bm25_score": -14.05,
      "vector_score": 0.43
    }
  ],
  "total": 5,
  "query": "JWT RS256"
}
```

### 6.3 CLI — `mneme search`

**Cambio:** Agregar `--graph`/`--no-graph` flags.

```bash
mneme search "JWT RS256"              # graph expansion ON (default)
mneme search "JWT RS256" --no-graph   # graph expansion OFF
mneme search "JWT RS256" --graph      # explicit ON
```

**Implementacion:** Cobra `BoolVar` con default `true`:

```go
var flagGraph bool
cmd.Flags().BoolVar(&flagGraph, "graph", true, "Enable graph expansion (use --no-graph to disable)")
```

---

## 7. Edge cases

### 7.1 Cold start -- sin relaciones en el grafo

**Comportamiento:** `graphExpand` retorna un slice vacio. La fusion RRF procede con solo 2 canales (FTS5 + vector), identico al comportamiento actual. No hay degradacion.

### 7.2 Fan-out explosivo -- entity hub con 200+ relaciones

**Proteccion:** `GetStrongRelations` usa `ORDER BY weight DESC LIMIT ?` con `ExpansionFanOutCap=50`. Solo las 50 relaciones mas fuertes se siguen. El hub no causa explosion combinatoria.

### 7.3 Dedup BM25 <-> graph -- misma memoria en ambos canales

**Manejo:** RRF maneja esto nativamente. Si una memoria aparece en FTS5 (rank 3, weight 1.0) y en graph (rank 7, weight 0.6), su RRF score es `1.0/(60+3) + 0.6/(60+7) = 0.0159 + 0.00896 = 0.0249`. No hay doble-counting; RRF suma contribuciones de cada canal independientemente.

### 7.4 `include_graph=false` -- bypass explicito

**Comportamiento:** Skip `graphExpand()`. `fuseAndRank` procede exactamente como hoy (2 canales). Zero overhead.

### 7.5 `ExpansionEnabled=false` en config -- global disable

**Comportamiento:** Mismo efecto que `include_graph=false` per-request, pero a nivel de toda la instancia. Util para debugging o si el grafo no tiene relaciones utiles aun. La config tiene prioridad: si `ExpansionEnabled=false`, `include_graph=true` en el request es ignorado.

### 7.6 Scope-restricted expansion

**Comportamiento:** La expansion opera solo sobre el store que corresponde al scope del search. Si `scope=project`, solo `projectStore` se expande. Si `scope=nil` (ambos stores), se expande solo `projectStore` (porque las relaciones cross-scope no existen, per SPEC-006 D1).

**Justificacion:** Las tablas `entities`, `relations`, y `memory_entities` viven dentro de cada DB. No hay forma de expandir cross-DB sin un merge layer.

### 7.7 Seed memory sin entidades

**Comportamiento:** `GetMemoryEntities` retorna slice vacio. Esa seed no genera expansion. Se continua con la siguiente seed. No es un error.

### 7.8 RRF backward compat -- scoring.RRFScore no cambia

**Verificacion:** `scoring.RRFScore` no se modifica. Los tests existentes (`internal/scoring/rrf_test.go`) siguen pasando sin cambios. El unico cambio es que los callers le pasan mas `RankedResult` entries.

---

## 8. Plan de implementacion atomico

6 commits, siguiendo el patron SPEC-005/006 (model -> db -> store -> service -> frontends):

### Commit 1: `feat(config): add graph expansion params to GraphConfig`
- `internal/config/config.go`: Agregar `ExpansionEnabled`, `ExpansionThreshold`, `ExpansionFanOutCap`, `ExpansionSeedTopK` a `GraphConfig`. Defaults en `Default()`. Validation en `Validate()`.
- `internal/config/config_test.go`: Tests para defaults, validation, TOML override, env override.

### Commit 2: `feat(db): add migration 008 for memory_entities entity index`
- `internal/db/migrations/008_graph_expansion.sql`: `CREATE INDEX idx_memory_entities_entity`.
- `internal/db/migration_008_test.go`: Test que indice existe post-migration.
- `internal/db/migrate_test.go`: Update version assertions 7 -> 8.
- `internal/db/schema_version_test.go`: Update wantVersion 7 -> 8.

### Commit 3: `feat(store): add GetStrongRelations, GetEntityMemories, BatchTouchRelations`
- `internal/store/entity.go`: Three new methods.
- `internal/store/entity_test.go`: Table-driven tests con real SQLite in-memory.

### Commit 4: `feat(model): add IncludeGraph field to SearchRequest`
- `internal/model/search.go`: `IncludeGraph *bool` field con `json:"include_graph,omitempty"`.
  - Pointer so `false` is distinguishable from omitted (omitted = use config default = true).

### Commit 5: `feat(service): graph expansion and 3-way RRF fusion in Search`
- `internal/service/search.go`: `graphExpand()`, `batchTouchRelations()`, `weightGraph` const, modify `fuseAndRank` to accept `includeGraph`, modify `Search` to pass `includeGraph`.
- `internal/service/search_test.go`: Integration tests.

### Commit 6: `feat(mcp,http,cli): expose include_graph param in all frontends`
- `internal/mcp/tools.go`: Add `include_graph` to `mem_search` InputSchema.
- `internal/http/server.go`: Parse `include_graph` query param in `handleSearch`.
- `internal/cli/search.go`: Add `--graph`/`--no-graph` flags, pass to `SearchRequest.IncludeGraph`.

---

## 9. Tests requeridos

### Config
- `TestGraphConfig_ExpansionDefaults`: Verifica que `ExpansionEnabled=true`, `ExpansionThreshold=0.3`, `ExpansionFanOutCap=50`, `ExpansionSeedTopK=10`.
- `TestGraphConfig_ExpansionValidation`: Threshold <0 o >1, FanOutCap <0, SeedTopK <0 -> error.

### Store
- `TestGetStrongRelations_AboveThreshold`: Crea 5 relaciones con pesos variados, threshold=0.3 retorna solo las que superan.
- `TestGetStrongRelations_Bidirectional`: Relacion source->target y target->source ambas retornadas.
- `TestGetStrongRelations_FanOutCap`: Crea 100 relaciones, cap=10 retorna las top 10 por weight.
- `TestGetEntityMemories_Basic`: Crea memory-entity links, retorna memory IDs correctos.
- `TestGetEntityMemories_NoLinks`: Retorna slice vacio, no error.
- `TestBatchTouchRelations_UpdatesTimestamp`: Verifica que last_traversed_at se actualiza.
- `TestBatchTouchRelations_EmptySlice`: No-op, no error.

### Service
- `TestSearch_GraphExpansion_SurfacesNeighbor`: Crea dos memorias conectadas via entidades + relacion. Buscar por la primera debe surfacear la segunda via graph.
- `TestSearch_GraphExpansion_Disabled`: `include_graph=false` no surfacea memorias que solo aparecen via grafo.
- `TestSearch_GraphExpansion_ConfigDisabled`: `ExpansionEnabled=false` ignora `include_graph=true`.
- `TestSearch_GraphExpansion_ColdStart`: Sin relaciones, resultados identicos a 2-channel fusion.
- `TestSearch_GraphExpansion_FanOutCap`: Verificar que fan-out respeta el cap.
- `TestSearch_GraphExpansion_TouchRelations`: Verificar que las relaciones traversadas tienen `last_traversed_at` actualizado.

### MCP/HTTP/CLI
- `TestMemSearch_IncludeGraph_Default`: Llamar sin param, grafo activo.
- `TestMemSearch_IncludeGraph_False`: Param explicitamente `false`, grafo inactivo.
- `TestHandleSearch_IncludeGraph_QueryParam`: HTTP query param parsing.
- `TestSearchCmd_GraphFlag`: CLI `--graph` y `--no-graph` flags.

---

## 10. Criterios de aceptacion

1. **Graph expansion surfaces topologically connected memories:** `mem_search("JWT")` con una memoria "auth-service" (match FTS5) conectada via relacion `depends_on` (weight=0.9) a "key-rotation" (no match FTS5) retorna ambas memorias. "key-rotation" aparece en los resultados con `relevance_score > 0`.

2. **3-way RRF fusion correcta:** El `relevance_score` final de una memoria que aparece en los 3 canales (FTS5 rank 2, vector rank 5, graph rank 3) es mayor que una que aparece solo en 2. Verificable con test: `score_3channels > score_2channels` para la misma memoria.

3. **Performance <50ms overhead:** En un proyecto con 5K memorias, 10K relaciones, 20K memory_entities, la diferencia de latencia entre `include_graph=true` y `include_graph=false` es <50ms. Verificable con benchmark `BenchmarkSearch_GraphExpansion_5K`.

4. **include_graph=false preserva comportamiento actual:** Search con `include_graph=false` produce resultados identicos al search actual (2-channel RRF). Zero regression.

5. **Touch relations update last_traversed_at:** Despues de un search con graph expansion, las relaciones traversadas tienen `last_traversed_at` actualizado al momento de la query. Verificable con test que inspecciona la relacion post-search.

6. **Tres frontends consistentes:** MCP `include_graph:boolean`, HTTP `?include_graph=true`, CLI `--graph`/`--no-graph` producen el mismo efecto sobre el `SearchRequest.IncludeGraph` field.

---

## 11. Consideraciones

- **Permisos:** Sin cambios. Search es read-only (mas touch de timestamps que es best-effort).
- **Seguridad:** La expansion no escala scopes. Un search con `scope=project` solo expande relaciones dentro de projectStore.
- **Performance:** Ver D7. El indice `idx_memory_entities_entity` (migration 008) es critico. Sin el, la expansion es O(n) por neighbor en lugar de O(log n).
- **Migracion de datos:** Migration 008 solo agrega un indice. No modifica datos. Backward-compatible. Rollback: `DROP INDEX idx_memory_entities_entity`.
- **Monitoring:** `slog.Info` con `event=graph_expansion` al inicio de cada expansion, incluyendo `seeds_count`, `neighbors_found`, `relations_touched`. `slog.Warn` con `event=graph_touch_error` si el batch touch falla.

---

## 12. Dependencias

- **SPEC-005 (completada):** Weighted relations, `UpdateRelationWeight`, `TouchRelation`, `idx_relations_weight`, `last_traversed_at`.
- **SPEC-006 (completada):** Hebbian auto-strengthening (los pesos son dinamicos, no solo defaults). `FindRelationBidirectional`. `internal/graph/` package. `GraphConfig` en `internal/config/config.go`.
- **No requiere SPEC-P1 (PPR):** La expansion 1-hop es self-contained. PPR la reemplazara en el futuro pero no la bloquea.

---

## 13. Open questions

1. **Global store expansion:** Cuando `scope=nil` (search ambos stores), deberiamos expandir tambien el grafo de `globalStore`? La decision actual es "no" (D4/edge case 7.6) por consistencia con SPEC-006 D1. Pero si el usuario tiene muchas memorias globales con relaciones ricas, se pierde recall. Diferir a SPEC-P1.

2. **`IncludeGraph *bool` vs `bool` en SearchRequest:** Usar `*bool` permite distinguir "no enviado" (use default) de "false" (explicit disable). Pero agrega complejidad de nil-checking. La alternativa es `bool` con default `true` resuelto en el service layer (como `Limit` hoy). Decision: `*bool` es mas correcto pero si el equipo prefiere simplicidad, `bool` con default in service es aceptable. El implementador decide.

3. **Telemetria del graph channel en SearchResponse:** No exponer `graph_score` en SearchResult (fuera de scope). Pero podria agregarse un campo `graph_expansions: int` a SearchResponse para que el agente sepa cuantas memorias vinieron del grafo. Diferir a feedback post-implementacion.
