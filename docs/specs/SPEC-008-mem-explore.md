# SPEC-008 — mem_explore: BFS Graph Traversal Tool

| Campo         | Valor                                                          |
|---------------|----------------------------------------------------------------|
| **ID**        | SPEC-008                                                       |
| **Epic**      | EPIC-2 — Grafo con peso + 1-hop expansion                     |
| **Backlog**   | BL-008                                                         |
| **Estado**    | speccing -> specced                                            |
| **Owner**     | architect                                                      |
| **Fecha**     | 2026-04-30                                                     |
| **Deps**      | SPEC-005 (completada), SPEC-006 (completada), SPEC-007 (completada) — weighted relations, Hebbian, GetStrongRelations, GetEntityMemoryIDs, BatchTouchRelations, graphExpand |
| **Memorias**  | `roadmap/v2-master-plan`, `architecture/scoring-formulas`, `architecture/mcp-error-codes`, `spec/SPEC-005-weighted-relations-design`, `spec/SPEC-006-hebbian-design`, `spec/SPEC-007-graph-search-design`, `spec/SPEC-007-implementation-notes` |

---

## 1. Contexto y motivacion

### El problema

`mem_search` expande el grafo implicitamente (1-hop via RRF, SPEC-007) pero el agente no tiene forma de explorar el grafo **explicitamente**. Cuando un agente quiere entender "que esta conectado a X y cuanto de lejos", necesita una herramienta de traversal dedicada.

`mem_explore` es esa herramienta: dado un seed memory, navega el grafo via BFS prioritario hasta un depth y budget configurables, y retorna las memorias descubiertas con su distancia y peso acumulado. Es la cuarta y ultima spec de EPIC-2.

### Diferencia con graphExpand (SPEC-007)

`graphExpand` (`internal/service/search.go:398-492`) es una primitiva interna de 1-hop que opera sobre un set de seeds rankeados por RRF, y produce `graphResult` structs que se integran en la fusion RRF. No es accesible como tool. `mem_explore` es un tool independiente que:

1. Acepta un **unico seed** (no un set de seeds de fusion).
2. Navega **multiples hops** (depth configurable, no solo 1).
3. Retorna un **resultado estructurado** con distance y accumulated_weight por nodo.
4. Es accesible via MCP, HTTP, y CLI.
5. Aplica token budget para no sobrecargar la ventana de contexto del agente.

### Que habilita

- **Exploracion interactiva:** El agente puede preguntar "que sabe mneme alrededor de auth-service" y navegar el contexto circundante.
- **Diagnostico de grafo:** Un agente puede verificar que relaciones existen alrededor de un concepto y cuales son fuertes/debiles.
- **Preparacion para PPR (SPEC-P1):** El traversal BFS con priority queue sienta las bases para la implementacion de Personalized PageRank en EPIC-4.

---

## 2. Decisiones de diseno

### D1. BFS con priority queue por accumulated_weight (no DFS, no Dijkstra)

**Decision:** Usar BFS prioritizado donde la priority queue ordena por `accumulated_weight` descendente. Esto garantiza que los nodos mas fuertemente conectados se exploran primero.

**Alternativas descartadas:**

- **DFS:** Explora en profundidad antes que en anchura. Si un camino tiene un nodo hub con 100 vecinos, DFS exploraria todos antes de visitar los vecinos directos del seed. No es util para "explorar el vecindario" de una memoria.
- **Dijkstra clasico:** Optimiza shortest-path entre dos puntos. No aplica: no buscamos un destino, sino explorar un vecindario.
- **PPR completo:** Requiere la matriz de adyacencia completa y power iteration. Es SPEC-P1 (EPIC-4). El BFS prioritizado es la version lightweight que funciona hoy sin infraestructura adicional.

**Justificacion del BFS prioritizado:** Es equivalente a un "best-first search" donde la calidad del camino se mide por el producto multiplicativo de pesos. Explora primero los caminos de mayor calidad, respeta el depth limit, y se detiene cuando el budget se agota. Es O(V + E) en el peor caso, donde V es el numero de nodos visitados (capped a 200) y E son las aristas consultadas.

### D2. accumulated_weight = path product (multiplicativo)

**Decision:** El peso acumulado de un nodo es el **producto** de los pesos de las aristas en el camino desde el seed hasta ese nodo.

```
accumulated_weight(node) = max over all paths P from seed to node:
    product(rel.Weight for rel in P)
```

**Ejemplo:** seed --[0.9]--> A --[0.7]--> B tiene `accumulated_weight(B) = 0.9 * 0.7 = 0.63`.

**Justificacion:**

- El modelo multiplicativo refleja "decay con distancia" naturalmente: cada hop adicional reduce el peso acumulado proporcionalmente a la fuerza de la arista. Un camino de 3 hops con aristas fuertes (0.9 x 0.9 x 0.9 = 0.729) es mejor que un camino de 2 hops con una arista debil (0.9 x 0.3 = 0.27).
- La alternativa aditiva (sum) no penaliza la distancia: un camino de 10 hops con aristas de 0.1 cada una tendria score 1.0, igual que una arista directa de 1.0. Absurdo.
- El modelo multiplicativo es consistente con probabilidades de transicion en random walks, que es exactamente lo que PPR (SPEC-P1) usara despues.
- Los pesos estan normalizados a [0.0, 1.0] (SPEC-005), asi que el producto siempre decrece o se mantiene. No hay overflow.

### D3. Seed resolution: UUID full -> UUID short prefix -> topic_key

**Decision:** El parametro `seed` acepta tres formatos, con la siguiente precedencia de resolucion:

1. **UUID full (36 chars):** `looksLikeUUID(seed)` (`internal/service/graph.go:218-237`) -> `getFromEitherStore(ctx, seed)` (`internal/service/memory.go:248-266`). Busca en projectStore primero, luego globalStore.
2. **UUID short prefix (8+ chars, hex-only, no hyphens, no slashes):** `SELECT ... FROM memories WHERE id LIKE ? AND deleted_at IS NULL` con `prefix%`. Si hay exactamente 1 match, se usa. Si hay 0 matches, (nil, nil). Si hay >1, `ErrAmbiguousSeed`.
3. **topic_key (contiene `/` o `.` o does not match hex pattern):** `SELECT ... FROM memories WHERE topic_key = ? AND project IS ? AND deleted_at IS NULL`. Busca en projectStore primero; si no encuentra, intenta globalStore.

**Logica de deteccion:**

```go
func classifySeed(seed string) seedKind {
    if looksLikeUUID(seed) {        // 36 chars, hyphens at 8,13,18,23
        return seedUUIDFull
    }
    if len(seed) >= 8 && isAllHex(seed) {
        return seedUUIDPrefix
    }
    return seedTopicKey              // e.g. "architecture/auth-model"
}
```

**Justificacion:**

- Los agentes tipicamente usan topic_key (memorable) o UUID corto (del output de search). Soportar los tres formatos hace el tool accesible sin consultar IDs largos.
- La precedencia UUID full > short > topic_key evita ambiguedades: un string de 36 caracteres con guiones es siempre UUID, no topic_key.
- El fallback a globalStore para topic_key es necesario porque memorias globales (e.g. preferencias) pueden ser seeds validos para explorar.

**Edge case: UUID prefix con multiples matches:**

```
seed = "019de001"  ->  3 memories match
-> Error: "seed '019de001' matches 3 memories; use the full UUID"
```

Mapeado a: MCP -32602 (Invalid params), HTTP 400, CLI exit 1.

### D4. Cap interno de 200 nodos

**Decision:** El BFS se detiene despues de visitar 200 nodos, independientemente del depth o budget restante.

**Justificacion:**

- El cap protege contra grafos densos donde un depth=3 podria visitar miles de nodos (50 fan-out/entity ^ 3 = 125K combinaciones teoricas).
- 200 nodos es suficiente para una exploracion significativa: con fan-out tipico de 5-10, depth=3 produce ~100-200 nodos. Mas alla de eso, los nodos tienen accumulated_weight tan bajo que no aportan informacion util.
- El cap es **adicional** al budget de tokens. Un grafo con muchas memorias pequenas podria alcanzar 200 nodos antes de agotar el budget.
- El valor 200 es configurable via config (`ExploreMaxNodes`) para proyectos con necesidades especiales.

### D5. Token estimation: `runeCount / 3.0`

**Decision:** Usar la misma formula que `mem_context`: `estimateTokens(text) = int(float64(utf8.RuneCountInString(text)) / 3.0)` (`internal/service/context.go:368-370`).

**Justificacion:**

- Consistencia con el unico otro tool que gestiona token budget (`mem_context`).
- El budget controla cuanto contenido cabe en la ventana del agente. Usar una formula diferente crearia inconsistencias en la planificacion de tokens.
- `runeCount / 3.0` es conservador para ingles/espanol y markdown -- sobre-estima ligeramente, lo cual es preferible a sub-estimar (que causaria truncamiento inesperado).

### D6. Touch async via BatchTouchRelations (reutilizar patron SPEC-007)

**Decision:** Las relaciones traversadas durante la exploracion se tocan via `store.BatchTouchRelations` (`internal/store/entity.go:450-472`) al final, en un unico batch. Es fire-and-forget: si falla, log + skip.

**Justificacion:**

- Identico al patron de SPEC-007 (`internal/service/search.go:497-520`). No reinventar.
- El volumen es acotado: 200 nodos maximo * ~5 aristas por nodo = ~1000 relaciones. Un batch UPDATE es O(1) para SQLite.
- Marcar `last_traversed_at` es importante para el decay sweep (SPEC-006): si una relacion fue traversada por mem_explore, no deberia decaer.

### D7. BFS bidireccional: traversar relations en ambas direcciones

**Decision:** Reutilizar `store.GetStrongRelations(ctx, entityID, threshold, limit)` (`internal/store/entity.go:364-413`) que ya hace queries bidireccionales (source y target) y merge en Go.

**Justificacion:**

- `GetStrongRelations` fue disenado para esto en SPEC-007. Usa dos queries indexadas (idx_relations_source, idx_relations_target) con merge en Go -- mas rapido que una query OR sobre tablas grandes (D7, SPEC-007).
- El threshold de 0.3 (config `ExpansionThreshold`) filtra aristas debiles. Mismo umbral que la expansion en search.

### D8. Output: lista plana ordenada + tree rendering en CLI

**Decision:** El response JSON (MCP y HTTP) es una **lista plana** ordenada por `(distance ASC, accumulated_weight DESC)`. El CLI renderiza esa lista como un **tree visual** agrupado por parent_memory_id con indentacion y simbolos.

**Justificacion para lista plana en JSON:**

- Una estructura anidada (tree) complica la deserializacion y procesamiento por parte de agentes. Los agentes procesan JSON flat de forma natural.
- La lista incluye `distance`, `accumulated_weight`, y `parent_memory_id` como campos, asi que la estructura del tree es reconstruible.
- El orden `(distance ASC, accumulated_weight DESC)` muestra primero los vecinos directos (mas relevantes) y dentro de cada nivel, los mejor conectados.

**Justificacion para tree en CLI:**

- La visualizacion humana se beneficia de la indentacion. Un usuario ejecutando `mneme explore "auth-service"` espera ver la jerarquia de conexiones, no una tabla plana.

### D9. CLI tree rendering

**Mockup de output:**

```
Exploring from: Auth service JWT RS256 setup (019de100...)
Depth: 3 | Budget: 4000 tokens | Nodes: 6/200

Auth service JWT RS256 setup [seed]
|-- jwt-library (depends_on, w=0.90, 245 tok)
|   |-- rsa-key-rotation (uses, w=0.63, 180 tok)
|   \-- token-validation (implements, w=0.72, 310 tok)
|-- user-model (part_of, w=0.85, 420 tok)
|   \-- session-store (depends_on, w=0.77, 290 tok)
\-- rate-limiter (related_to, w=0.45, 150 tok)

Total: 6 memories | 1595 tokens | 3 levels
```

**Implementacion:** El tree se construye a partir de la lista plana usando `ParentMemoryID` para reconstruir la jerarquia. Cada nodo muestra:
- Nombre (title truncado a 40 chars) o topic_key si disponible
- Tipo de relacion que lo conecta al padre en el tree
- `w=` accumulated_weight con 2 decimales
- `N tok` estimacion de tokens de esta memoria

**Simbolos:** `|-- ` (hijo intermedio), `\-- ` (ultimo hijo), `|   ` (continuacion vertical). Implementacion con pure string formatting, no lipgloss. Simbolos ASCII son portables y legibles en cualquier terminal.

### D10. Error mapping: seed not found -> -32000

**Decision:** Cuando el seed no se encuentra en ninguna store, retornar:
- MCP: code `-32000` (CodeMemoryNotFound), message `"mcp: handle mem_explore: service: explore: memory not found"`.
- HTTP: status `404 Not Found`.
- CLI: exit 1 con mensaje a stderr.

**Justificacion:** Reutilizar `model.ErrNotFound` y el mapeo existente en `mapServiceError` (`internal/mcp/handlers.go:363-406`). `ErrNotFound` ya se mapea a `-32000` (linea 364). No se necesita un error code nuevo.

### D11. HTTP endpoint: `GET /v1/memories/{id}/explore`

**Decision:** Usar `GET /v1/memories/{id}/explore` con query params para depth, budget, y threshold.

**Alternativa descartada:** `GET /v1/explore?seed=...` -- no es RESTful. El explore es una operacion sobre un recurso (memory) identificado por ID. El patron `/{id}/explore` sigue la convencion REST de sub-resource actions.

**Implementacion:** Go's `http.ServeMux` usa `handleMemoryByID` como catch-all para `/v1/memories/` (`internal/http/server.go:101`). Detectar el sufijo `/explore` dentro del handler existente:

```go
func (s *Server) handleMemoryByID(w http.ResponseWriter, r *http.Request) {
    path := strings.TrimPrefix(r.URL.Path, "/v1/memories/")
    if strings.HasSuffix(path, "/explore") {
        id := strings.TrimSuffix(path, "/explore")
        s.handleExplore(w, r, id)
        return
    }
    // existing GET/PATCH/DELETE logic...
}
```

**Nota:** El HTTP endpoint solo acepta **full UUID** como `{id}` en el path (consistente con el patron existente de `handleMemoryByID`). Topic key y short prefix resolution son features del MCP/CLI frontends donde el seed es un parametro de texto libre.

---

## 3. Modelo del BFS prioritizado

### Pseudocodigo

```
func explore(ctx, store, seedMemory, maxDepth, tokenBudget, threshold, fanOutCap, maxNodes):
    // Priority queue: max-heap by accumulated_weight
    pq = max-heap by accumulated_weight
    visited = map[memoryID]ExploreNode{}
    touchIDs = []string{}
    tokensUsed = 0

    // Resolve seed memory entities
    seedEntities = store.GetMemoryEntities(ctx, seedMemory.ID)
    if len(seedEntities) == 0:
        return {nodes: [], total_nodes: 0, tokens_used: seedTokens}

    // Seed token cost
    seedTokens = estimateTokens(seedMemory.Title + seedMemory.Content)
    tokensUsed = seedTokens

    // Mark seed as visited (not included in output nodes)
    visited[seedMemory.ID] = ExploreNode{MemoryID: seedMemory.ID, Distance: 0, AccumulatedWeight: 1.0}

    // For each entity of the seed, enqueue neighbors
    for _, entity in seedEntities:
        rels = store.GetStrongRelations(ctx, entity.ID, threshold, fanOutCap)
        for _, rel in rels:
            neighborEntityID = otherEnd(rel, entity.ID)
            memIDs = store.GetEntityMemoryIDs(ctx, neighborEntityID)
            for _, memID in memIDs:
                if memID == seedMemory.ID: continue
                weight = 1.0 * rel.Weight
                pq.push({memID, weight, distance: 1, relType: rel.Type, relID: rel.ID, parentID: seedMemory.ID})
            touchIDs = append(touchIDs, rel.ID)

    // BFS loop
    while pq.notEmpty() && len(visited) < maxNodes:
        item = pq.pop()

        if item.memID in visited:
            // Update weight if better path found (but don't re-expand)
            if item.weight > visited[item.memID].AccumulatedWeight:
                visited[item.memID].AccumulatedWeight = item.weight
                visited[item.memID].RelationType = item.relType
                visited[item.memID].ParentMemoryID = item.parentID
            continue

        if item.distance > maxDepth:
            continue

        // Load memory metadata (lightweight — no full content)
        meta = store.GetMemoryMetadata(ctx, item.memID)
        if meta == nil: continue

        tokenCost = (len(meta.Title_runes) + meta.ContentLen) / 3
        if tokensUsed + tokenCost > tokenBudget:
            continue  // skip, try next in queue

        tokensUsed += tokenCost
        node = ExploreNode{
            MemoryID:          meta.ID,
            ParentMemoryID:    item.parentID,
            Title:             meta.Title,
            TopicKey:          meta.TopicKey,
            Type:              meta.Type,
            Distance:          item.distance,
            AccumulatedWeight: item.weight,
            RelationType:      item.relType,
            TokenEstimate:     tokenCost,
        }
        visited[meta.ID] = node

        // Expand if within depth limit
        if item.distance < maxDepth:
            entities = store.GetMemoryEntities(ctx, meta.ID)
            for _, entity in entities:
                rels = store.GetStrongRelations(ctx, entity.ID, threshold, fanOutCap)
                for _, rel in rels:
                    neighborEntityID = otherEnd(rel, entity.ID)
                    neighborMemIDs = store.GetEntityMemoryIDs(ctx, neighborEntityID)
                    for _, nMemID in neighborMemIDs:
                        if nMemID in visited: continue
                        newWeight = item.weight * rel.Weight
                        pq.push({nMemID, newWeight, item.distance + 1, rel.Type, rel.ID, meta.ID})
                    touchIDs = append(touchIDs, rel.ID)

    // Build result: exclude seed, sort by (distance ASC, accumulated_weight DESC)
    nodes = [v for k, v in visited if k != seedMemory.ID]
    sort(nodes, by: distance ASC, then accumulated_weight DESC)
    return ExploreResponse{
        SeedID: seedMemory.ID,
        SeedTitle: seedMemory.Title,
        Nodes: nodes,
        TotalNodes: len(nodes),
        TokensUsed: tokensUsed,
        MaxDepthReached: max(node.Distance for node in nodes) or 0,
    }
```

### Priority queue implementation

Use Go's `container/heap` with a max-heap ordered by `accumulated_weight` descending:

```go
type exploreItem struct {
    memoryID          string
    accumulatedWeight float64
    distance          int
    relationType      model.RelationType
    relationID        string  // for touch
    parentMemoryID    string
}

type explorePQ []*exploreItem

func (pq explorePQ) Len() int            { return len(pq) }
func (pq explorePQ) Less(i, j int) bool  { return pq[i].accumulatedWeight > pq[j].accumulatedWeight }
func (pq explorePQ) Swap(i, j int)       { pq[i], pq[j] = pq[j], pq[i] }
func (pq *explorePQ) Push(x any)         { *pq = append(*pq, x.(*exploreItem)) }
func (pq *explorePQ) Pop() any           { old := *pq; n := len(old); item := old[n-1]; *pq = old[:n-1]; return item }
```

This is a standard Go `container/heap` pattern. The heap lives in `internal/service/explore.go` (unexported types).

### Cycle detection

The `visited` map prevents re-expanding nodes. When a node is popped and already in `visited`, only its weight may be updated (if the new path is better). This naturally handles cycles without tracking a separate "in-progress" set.

---

## 4. Modelo del tree CLI

### Tree construction from flat list

With `ParentMemoryID` tracked in each `ExploreNode`, the CLI builds a proper tree:

```go
type treeNode struct {
    node     model.ExploreNode
    children []*treeNode
}

func buildTree(seedID string, nodes []model.ExploreNode) *treeNode {
    root := &treeNode{node: model.ExploreNode{MemoryID: seedID}}
    byID := map[string]*treeNode{seedID: root}

    // Nodes are already sorted by (distance ASC, weight DESC).
    // Processing in order ensures parents are registered before children.
    for i := range nodes {
        tn := &treeNode{node: nodes[i]}
        byID[nodes[i].MemoryID] = tn
        parent, ok := byID[nodes[i].ParentMemoryID]
        if !ok {
            parent = root  // fallback: attach to root
        }
        parent.children = append(parent.children, tn)
    }
    return root
}
```

### renderTree function

```go
func renderTree(w io.Writer, root *treeNode, seedTitle string) {
    fmt.Fprintf(w, "%s [seed]\n", truncate(seedTitle, 50))
    for i, child := range root.children {
        isLast := i == len(root.children)-1
        printNode(w, child, "", isLast)
    }
}

func printNode(w io.Writer, tn *treeNode, prefix string, isLast bool) {
    connector := "|-- "
    if isLast {
        connector = "\\-- "
    }
    name := truncate(tn.node.Title, 40)
    if tn.node.TopicKey != "" {
        name = tn.node.TopicKey
    }
    fmt.Fprintf(w, "%s%s%s (%s, w=%.2f, %d tok)\n",
        prefix, connector, name,
        tn.node.RelationType,
        tn.node.AccumulatedWeight,
        tn.node.TokenEstimate,
    )

    childPrefix := prefix + "|   "
    if isLast {
        childPrefix = prefix + "    "
    }
    for i, child := range tn.children {
        printNode(w, child, childPrefix, i == len(tn.children)-1)
    }
}
```

---

## 5. Contratos

### 5.1. Model types (`internal/model/explore.go` -- NEW file)

```go
// ExploreRequest specifies the parameters for a graph exploration starting
// from a seed memory.
type ExploreRequest struct {
    // Seed identifies the starting memory. Accepts a full UUID (36 chars),
    // a short UUID prefix (8+ hex chars), or a topic_key.
    Seed string `json:"seed"`

    // Depth is the maximum number of hops from the seed. Defaults to 2.
    Depth int `json:"depth,omitempty"`

    // Budget is the maximum token estimate for returned memories. Defaults to 4000.
    Budget int `json:"budget,omitempty"`

    // Threshold is the minimum relation weight to follow. Defaults to config.
    Threshold float64 `json:"threshold,omitempty"`

    // Project restricts seed resolution and exploration to this project.
    Project string `json:"project,omitempty"`
}

// ExploreResponse is the result of a graph exploration.
type ExploreResponse struct {
    SeedID          string        `json:"seed_id"`
    SeedTitle       string        `json:"seed_title"`
    Nodes           []ExploreNode `json:"nodes"`
    TotalNodes      int           `json:"total_nodes"`
    TokensUsed      int           `json:"tokens_used"`
    MaxDepthReached int           `json:"max_depth_reached"`
}

// ExploreNode represents a memory discovered during graph exploration.
type ExploreNode struct {
    MemoryID          string       `json:"memory_id"`
    ParentMemoryID    string       `json:"parent_memory_id,omitempty"`
    Title             string       `json:"title"`
    TopicKey          string       `json:"topic_key,omitempty"`
    Type              MemoryType   `json:"type"`
    Distance          int          `json:"distance"`
    AccumulatedWeight float64      `json:"accumulated_weight"`
    RelationType      RelationType `json:"relation_type"`
    TokenEstimate     int          `json:"token_estimate"`
}
```

New sentinel error in `internal/model/errors.go`:

```go
// ErrAmbiguousSeed is returned when a short UUID prefix matches multiple memories.
var ErrAmbiguousSeed = errors.New("seed matches multiple memories; use the full UUID")
```

### 5.2. MCP -- `mem_explore` tool

New entry in `allTools()` (`internal/mcp/tools.go`):

```go
{
    Name:        "mem_explore",
    Description: "Explore the knowledge graph starting from a seed memory. Performs a prioritised BFS traversal following strong relations, returning connected memories with their distance and path weight.",
    InputSchema: map[string]any{
        "type":     "object",
        "required": []string{"seed"},
        "properties": map[string]any{
            "seed": map[string]any{
                "type":        "string",
                "description": "Starting memory: full UUID, short UUID prefix (8+ hex chars), or topic_key (e.g. 'architecture/auth-model').",
            },
            "depth": map[string]any{
                "type":        "integer",
                "description": "Maximum hops from seed. Default: 2. Range: 0-5.",
                "minimum":     0,
                "maximum":     5,
            },
            "budget": map[string]any{
                "type":        "integer",
                "description": "Maximum token budget for returned memories. Default: 4000.",
                "minimum":     1,
            },
            "threshold": map[string]any{
                "type":        "number",
                "description": "Minimum relation weight to follow. Default: 0.3. Range: 0.0-1.0.",
                "minimum":     0.0,
                "maximum":     1.0,
            },
            "project": map[string]any{
                "type":        "string",
                "description": "Project slug. Defaults to the detected project.",
            },
        },
    },
},
```

#### Handler dispatch

Add to `handleToolCall` switch in `internal/mcp/handlers.go:32`:

```go
case "mem_explore":
    return h.handleMemExplore(ctx, params.Arguments)
```

#### Handler implementation

```go
func (h *handlers) handleMemExplore(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
    var req model.ExploreRequest
    if err := json.Unmarshal(raw, &req); err != nil {
        return nil, &JSONRPCError{
            Code:    CodeInvalidParams,
            Message: fmt.Sprintf("mcp: handle mem_explore: invalid arguments: %s", err),
        }
    }
    if req.Seed == "" {
        return nil, &JSONRPCError{
            Code:    CodeInvalidParams,
            Message: "mcp: handle mem_explore: seed is required",
        }
    }
    resp, err := h.svc.Explore(ctx, req)
    if err != nil {
        return nil, h.mapServiceError("mem_explore", err)
    }
    return resultFromAny(resp)
}
```

#### Error mapping update

Add `model.ErrAmbiguousSeed` to the invalid-params branch in `mapServiceError` (`internal/mcp/handlers.go:376-394`):

```go
errors.Is(err, model.ErrAmbiguousSeed) ||
```

#### Ejemplo request

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "mem_explore",
    "arguments": {
      "seed": "architecture/auth-model",
      "depth": 3,
      "budget": 4000
    }
  }
}
```

#### Ejemplo response

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"seed_id\":\"019de100-abcd-7fff-8000-000000000001\",\"seed_title\":\"Auth service JWT RS256 setup\",\"nodes\":[{\"memory_id\":\"019de200-efgh-...\",\"parent_memory_id\":\"019de100-abcd-7fff-8000-000000000001\",\"title\":\"JWT key rotation schedule\",\"topic_key\":\"ops/key-rotation\",\"type\":\"decision\",\"distance\":1,\"accumulated_weight\":0.9,\"relation_type\":\"depends_on\",\"token_estimate\":245},{\"memory_id\":\"019de300-ijkl-...\",\"parent_memory_id\":\"019de200-efgh-...\",\"title\":\"RSA key storage in Vault\",\"type\":\"architecture\",\"distance\":2,\"accumulated_weight\":0.63,\"relation_type\":\"uses\",\"token_estimate\":180}],\"total_nodes\":2,\"tokens_used\":425,\"max_depth_reached\":2}"
      }
    ]
  }
}
```

#### Errores MCP

| Condicion | Code | Mensaje |
|-----------|------|---------|
| seed empty | -32602 (Invalid params) | `mcp: handle mem_explore: seed is required` |
| seed not found | -32000 (Memory not found) | `mcp: handle mem_explore: service: explore: memory not found` |
| seed prefix ambiguous | -32602 (Invalid params) | `mcp: handle mem_explore: service: explore: resolve seed: seed matches multiple memories; use the full UUID` |
| depth > 5 | -32602 | `mcp: handle mem_explore: service: explore: depth must be between 0 and 5` |
| threshold > 1.0 | -32602 | `mcp: handle mem_explore: service: explore: threshold must be between 0.0 and 1.0` |

### 5.3. HTTP -- `GET /v1/memories/{id}/explore`

**Request:**

```
GET /v1/memories/019de100-abcd-7fff-8000-000000000001/explore?depth=3&budget=4000&threshold=0.3
```

Query params (all optional):
- `depth` (int, default 2, range 0-5)
- `budget` (int, default 4000)
- `threshold` (float, default 0.3, range 0.0-1.0)

**Response 200 OK:**

```json
{
  "seed_id": "019de100-abcd-7fff-8000-000000000001",
  "seed_title": "Auth service JWT RS256 setup",
  "nodes": [
    {
      "memory_id": "019de200-efgh-...",
      "parent_memory_id": "019de100-abcd-7fff-8000-000000000001",
      "title": "JWT key rotation schedule",
      "topic_key": "ops/key-rotation",
      "type": "decision",
      "distance": 1,
      "accumulated_weight": 0.9,
      "relation_type": "depends_on",
      "token_estimate": 245
    }
  ],
  "total_nodes": 1,
  "tokens_used": 245,
  "max_depth_reached": 1
}
```

**Errores HTTP:**

| Condicion | Status | Body code |
|-----------|--------|-----------|
| ID not found | 404 | `not_found` |
| depth > 5 or non-numeric | 400 | `invalid_request` |
| threshold > 1.0 or non-numeric | 400 | `invalid_request` |

**Nota:** El HTTP endpoint solo acepta **full UUID** como `{id}` en el path (consistente con el patron existente de `handleMemoryByID`). Topic key y short prefix resolution son features del MCP/CLI frontends.

### 5.4. CLI -- `mneme explore`

**Nuevo subcomando** (`internal/cli/explore.go` -- NEW file):

```bash
mneme explore <seed> [flags]

# Examples:
mneme explore "architecture/auth-model" --depth 3 --budget 4000
mneme explore 019de100 --depth 2
mneme explore "019de100-abcd-7fff-8000-000000000001" --json
```

**Flags:**

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--depth` | `-d` | int | 2 | Maximum hops from seed |
| `--budget` | `-b` | int | 4000 | Token budget |
| `--threshold` | `-t` | float | 0.3 | Minimum relation weight |
| `--json` | | bool | false | Output as JSON instead of tree |

**Registration:** Add `rootCmd.AddCommand(newExploreCmd())` in `internal/cli/root.go`.

**Output (default -- tree):** See D9 mockup.

**Output (--json):** Same as `ExploreResponse` JSON, printed via `printJSON(os.Stdout, resp)`.

---

## 6. Service layer

### New file: `internal/service/explore.go`

Contains:
- `Explore(ctx, ExploreRequest) (*ExploreResponse, error)` -- main method
- `resolveSeed(ctx, seed, project string) (*model.Memory, *store.MemoryStore, error)` -- returns memory and its store
- `classifySeed(seed string) seedKind` -- unexported enum
- `isAllHex(s string) bool` -- helper
- `exploreItem`, `explorePQ` -- priority queue types (unexported)

### New store methods

In `internal/store/memory.go`:

```go
// GetByIDPrefix returns the single active memory whose ID starts with prefix.
// Returns (*Memory, nil) on exact-one match, (nil, nil) on zero matches, or
// (nil, ErrAmbiguousSeed) when multiple memories share the prefix.
func (s *MemoryStore) GetByIDPrefix(ctx context.Context, prefix string) (*model.Memory, error) {
    const countQ = `
        SELECT COUNT(*) FROM memories
        WHERE id LIKE ? AND deleted_at IS NULL`
    var count int
    if err := s.db.QueryRowContext(ctx, countQ, prefix+"%").Scan(&count); err != nil {
        return nil, fmt.Errorf("store: get by id prefix: count: %w", err)
    }
    if count == 0 {
        return nil, nil
    }
    if count > 1 {
        return nil, fmt.Errorf("store: get by id prefix: %w", model.ErrAmbiguousSeed)
    }
    // Exactly one match — load it.
    const q = `
        SELECT id, type, scope, title, content, topic_key, project,
               session_id, created_by, created_at, updated_at,
               importance, confidence, access_count, last_accessed,
               decay_rate, revision_count, superseded_by, deleted_at,
               applies_to, severity
        FROM memories
        WHERE id LIKE ? AND deleted_at IS NULL
        LIMIT 1`
    row := s.db.QueryRowContext(ctx, q, prefix+"%")
    m, err := scanMemory(row)
    if err != nil {
        return nil, fmt.Errorf("store: get by id prefix: %w", err)
    }
    if err := s.loadFiles(ctx, m); err != nil {
        return nil, err
    }
    return m, nil
}

// GetByTopicKey returns the active memory with the given topic_key and project.
// Returns (nil, nil) when no match is found.
func (s *MemoryStore) GetByTopicKey(ctx context.Context, topicKey, project string) (*model.Memory, error) {
    const q = `
        SELECT id, type, scope, title, content, topic_key, project,
               session_id, created_by, created_at, updated_at,
               importance, confidence, access_count, last_accessed,
               decay_rate, revision_count, superseded_by, deleted_at,
               applies_to, severity
        FROM memories
        WHERE topic_key = ? AND project IS ? AND deleted_at IS NULL
        LIMIT 1`
    row := s.db.QueryRowContext(ctx, q, topicKey, toNullString(project))
    m, err := scanMemory(row)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, nil
        }
        return nil, fmt.Errorf("store: get by topic key: %w", err)
    }
    if err := s.loadFiles(ctx, m); err != nil {
        return nil, err
    }
    return m, nil
}

// GetMemoryMetadata returns lightweight metadata for a memory without loading
// full content. Used by mem_explore to estimate tokens cheaply.
func (s *MemoryStore) GetMemoryMetadata(ctx context.Context, id string) (*MemoryMetadata, error) {
    const q = `
        SELECT id, title, topic_key, type, length(content)
        FROM memories
        WHERE id = ? AND deleted_at IS NULL`
    var meta MemoryMetadata
    var topicKey sql.NullString
    err := s.db.QueryRowContext(ctx, q, id).Scan(
        &meta.ID, &meta.Title, &topicKey, &meta.Type, &meta.ContentLen,
    )
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, nil
        }
        return nil, fmt.Errorf("store: get memory metadata: %w", err)
    }
    meta.TopicKey = topicKey.String
    return &meta, nil
}

// MemoryMetadata is a lightweight projection of a memory used when full
// content loading is not needed (e.g. token estimation in graph traversal).
type MemoryMetadata struct {
    ID         string
    Title      string
    TopicKey   string
    Type       model.MemoryType
    ContentLen int  // SQLite length(content) in bytes; used for token estimation
}
```

**Nota sobre `ContentLen`:** SQLite's `length(content)` returns the number of characters for TEXT columns (not bytes), which is approximately the rune count. The token estimate is `(len(title_runes) + ContentLen) / 3`. This is a close enough approximation and avoids loading multi-KB content bodies.

---

## 7. Config

### New fields in `GraphConfig` (`internal/config/config.go`)

```go
// ExploreMaxNodes is the hard cap on nodes visited during mem_explore BFS.
// Default: 200.
ExploreMaxNodes int `toml:"explore_max_nodes"`

// ExploreDefaultDepth is the default depth for mem_explore. Default: 2.
ExploreDefaultDepth int `toml:"explore_default_depth"`

// ExploreDefaultBudget is the default token budget for mem_explore. Default: 4000.
ExploreDefaultBudget int `toml:"explore_default_budget"`
```

**Defaults** in `Default()`:

```go
ExploreMaxNodes:      200,
ExploreDefaultDepth:  2,
ExploreDefaultBudget: 4000,
```

**Validation** in `Validate()`:

```go
if c.Graph.ExploreMaxNodes < 0 {
    return errors.New("graph.explore_max_nodes must be >= 0")
}
if c.Graph.ExploreDefaultDepth < 0 || c.Graph.ExploreDefaultDepth > 5 {
    return errors.New("graph.explore_default_depth must be between 0 and 5")
}
if c.Graph.ExploreDefaultBudget < 0 {
    return errors.New("graph.explore_default_budget must be >= 0")
}
```

**Environment variable overrides:** Follow existing pattern if needed, but not required for v1.

---

## 8. Edge cases

### 8.1. Seed memory has no entity links

`GetMemoryEntities(ctx, seedID)` returns empty slice. The BFS produces zero neighbors. Response: `{nodes: [], total_nodes: 0, tokens_used: seedTokens, max_depth_reached: 0}`. Not an error -- the seed exists but has no graph presence.

### 8.2. Cycle in the graph (A -> B -> C -> A)

The `visited` map prevents re-expanding. When C tries to enqueue A, A is already visited, so only its accumulated_weight may be updated if the cyclic path is better. No infinite loop.

### 8.3. Seed prefix ambiguous (>1 match)

`store.GetByIDPrefix(ctx, "019de001")` finds 3 memories. Returns `ErrAmbiguousSeed`. MCP maps to -32602, HTTP 400, CLI exit 1 with message.

### 8.4. Budget = 0

`budget <= 0` falls back to config default (4000). Consistent with `mem_context` pattern (`internal/service/context.go:35`).

### 8.5. Depth = 0

Only the seed is visited. No neighbors are explored. Response: `{nodes: [], total_nodes: 0, max_depth_reached: 0}`. The seed is excluded from `nodes` (it is the starting point, not a discovery).

### 8.6. Disconnected graph

Seed's entities have no relations above threshold. Response: empty nodes. Not an error.

### 8.7. Fan-out > 200 edges from a single entity

`GetStrongRelations` caps at `ExpansionFanOutCap` (50). Additionally, the global `ExploreMaxNodes` (200) stops the BFS. Both caps protect against explosion.

### 8.8. Memory found in globalStore

`resolveSeed` returns the store the memory was found in. The BFS uses that store for all graph operations (GetMemoryEntities, GetStrongRelations, GetEntityMemoryIDs). Cross-store expansion is not supported (same constraint as SPEC-006 D1).

### 8.9. topic_key with no match

Returns `ErrNotFound` -> MCP -32000, HTTP 404, CLI exit 1.

### 8.10. Seed memory is deleted

`store.Get` / `GetByTopicKey` / `GetByIDPrefix` all filter `deleted_at IS NULL`. A deleted seed returns not-found.

---

## 9. Apps/Modulos afectados

| Modulo | Archivo | Tipo de cambio |
|--------|---------|---------------|
| `internal/model` | `explore.go` (NEW) | ExploreRequest, ExploreResponse, ExploreNode |
| `internal/model` | `errors.go` | ErrAmbiguousSeed sentinel |
| `internal/config` | `config.go` | 3 new fields in GraphConfig + defaults + validation |
| `internal/config` | `config_test.go` | Tests for new defaults and validation |
| `internal/store` | `memory.go` | GetByIDPrefix, GetByTopicKey, GetMemoryMetadata, MemoryMetadata |
| `internal/store` | `memory_test.go` | Tests for new methods |
| `internal/service` | `explore.go` (NEW) | Explore, resolveSeed, BFS, priority queue |
| `internal/service` | `explore_test.go` (NEW) | Integration tests |
| `internal/service` | `bench_test.go` | BenchmarkExplore_Depth3_5K |
| `internal/mcp` | `tools.go` | mem_explore in allTools() |
| `internal/mcp` | `handlers.go` | handleMemExplore, case in handleToolCall, ErrAmbiguousSeed in mapServiceError |
| `internal/mcp` | `handlers_test.go` | MCP tests |
| `internal/http` | `server.go` | handleExplore, /explore suffix in handleMemoryByID |
| `internal/http` | `server_test.go` | HTTP tests |
| `internal/cli` | `explore.go` (NEW) | newExploreCmd, tree rendering, buildTree, renderTree |
| `internal/cli` | `explore_test.go` (NEW) | CLI tests |
| `internal/cli` | `root.go` | Register newExploreCmd |

### Fuera de scope

- Changes to `service.Search()` or `graphExpand()` -- SPEC-007 domain.
- DB schema migration -- no new migration needed. All indices exist (002, 007, 008).
- PPR / Personalized PageRank -- SPEC-P1 (EPIC-4).
- Community detection -- SPEC-C1 (EPIC-5).
- TUI explore view -- future.

---

## 10. Plan de implementacion atomico

7 commits, siguiendo el patron SPEC-005/006/007 (model -> config -> store -> service -> frontends):

| # | Commit | Archivos | Descripcion |
|---|--------|----------|-------------|
| 1 | `feat(model): add ExploreRequest, ExploreResponse, ExploreNode, ErrAmbiguousSeed` | `internal/model/explore.go` (NEW), `internal/model/errors.go` | New file with explore types. New sentinel error. |
| 2 | `feat(config): add graph explore params to GraphConfig` | `internal/config/config.go`, `internal/config/config_test.go` | ExploreMaxNodes, ExploreDefaultDepth, ExploreDefaultBudget. Defaults + validation + tests. |
| 3 | `feat(store): add GetByIDPrefix, GetByTopicKey, GetMemoryMetadata` | `internal/store/memory.go`, `internal/store/memory_test.go` | Three new methods for seed resolution and lightweight metadata loading. Tests: prefix match, ambiguous prefix, topic_key, metadata. |
| 4 | `feat(service): add Explore with BFS priority queue and seed resolution` | `internal/service/explore.go` (NEW), `internal/service/explore_test.go` (NEW), `internal/service/bench_test.go` | Explore method, resolveSeed, BFS algorithm, priority queue, isAllHex. Integration tests + benchmark. |
| 5 | `feat(mcp): add mem_explore tool` | `internal/mcp/tools.go`, `internal/mcp/handlers.go`, `internal/mcp/handlers_test.go` | Tool definition, handleMemExplore handler, case in handleToolCall, ErrAmbiguousSeed in mapServiceError. Tests. |
| 6 | `feat(http): add GET /v1/memories/{id}/explore endpoint` | `internal/http/server.go`, `internal/http/server_test.go` | handleExplore handler, /explore suffix detection in handleMemoryByID, query param parsing. Tests. |
| 7 | `feat(cli): add mneme explore command with tree rendering` | `internal/cli/explore.go` (NEW), `internal/cli/explore_test.go` (NEW), `internal/cli/root.go` | newExploreCmd, buildTree, renderTree, --json flag, register in root. Tests. |

---

## 11. Tests requeridos

### model (unit)

- `TestExploreNode_JSONRoundtrip` -- marshal/unmarshal preserves all fields including omitempty.
- `TestErrAmbiguousSeed_Sentinel` -- `errors.Is(wrapped, model.ErrAmbiguousSeed)` works through fmt.Errorf wrapping.

### config (unit)

- `TestGraphConfig_ExploreDefaults` -- ExploreMaxNodes=200, ExploreDefaultDepth=2, ExploreDefaultBudget=4000.
- `TestGraphConfig_ExploreValidation` -- MaxNodes<0 error, DefaultDepth<0 error, DefaultDepth>5 error, DefaultBudget<0 error. Zero values valid (toggle off).

### store (integration, SQLite in-memory)

- `TestStore_GetByIDPrefix_ExactOneMatch` -- single memory with prefix, returns it.
- `TestStore_GetByIDPrefix_MultipleMatches` -- two memories sharing prefix, returns ErrAmbiguousSeed.
- `TestStore_GetByIDPrefix_NoMatch` -- returns (nil, nil).
- `TestStore_GetByIDPrefix_DeletedExcluded` -- deleted memory excluded from match.
- `TestStore_GetByTopicKey_Found` -- memory with topic_key in correct project.
- `TestStore_GetByTopicKey_NotFound` -- returns (nil, nil).
- `TestStore_GetByTopicKey_WrongProject` -- topic_key exists for different project, returns (nil, nil).
- `TestStore_GetMemoryMetadata_Found` -- verify title, topic_key, type, content_len correct.
- `TestStore_GetMemoryMetadata_NotFound` -- returns (nil, nil).
- `TestStore_GetMemoryMetadata_Deleted` -- excluded.

### service (integration, SQLite in-memory)

- `TestExplore_Basic_Depth1` -- seed with 3 neighbors, verify 3 nodes, distance=1, correct weights.
- `TestExplore_Depth2_Transitive` -- seed -> A -> B, verify B.distance=2, B.weight = w1*w2.
- `TestExplore_CycleDetection` -- A -> B -> C -> A, verify no infinite loop, each appears once.
- `TestExplore_BudgetLimit` -- verify nodes skipped when budget exhausted.
- `TestExplore_NodeCap` -- >200 reachable nodes, verify <=200 returned.
- `TestExplore_DepthZero` -- depth=0, zero nodes.
- `TestExplore_SeedNoEntities` -- empty nodes, no error.
- `TestExplore_SeedNotFound` -- verify ErrNotFound.
- `TestExplore_SeedByTopicKey` -- resolve via topic_key.
- `TestExplore_SeedByShortPrefix` -- resolve via 8-char prefix.
- `TestExplore_SeedPrefixAmbiguous` -- verify ErrAmbiguousSeed.
- `TestExplore_TouchRelations` -- verify last_traversed_at updated.
- `TestExplore_ThresholdFilter` -- relations below threshold not followed.
- `TestExplore_ParentMemoryID` -- verify parent tracking.
- `TestExplore_OrderByDistanceThenWeight` -- verify output order.

### mcp

- `TestMCP_MemExplore_Basic` -- JSON-RPC roundtrip.
- `TestMCP_MemExplore_SeedNotFound` -- error -32000.
- `TestMCP_MemExplore_SeedRequired` -- error -32602.
- `TestMCP_MemExplore_DepthExceeded` -- depth=6, error -32602.
- `TestMCP_MemExplore_SeedAmbiguous` -- error -32602.

### http

- `TestHTTP_GetExplore_200` -- valid ID, verify response shape.
- `TestHTTP_GetExplore_404` -- nonexistent ID.
- `TestHTTP_GetExplore_InvalidDepth` -- non-numeric depth, verify 400.
- `TestHTTP_GetExplore_DefaultParams` -- omit all params, verify defaults.

### cli

- `TestExploreCmd_TreeOutput` -- verify tree symbols (|-- , \-- ) and formatting.
- `TestExploreCmd_JSONOutput` -- --json flag produces valid JSON.
- `TestExploreCmd_SeedRequired` -- no args, verify usage error.
- `TestExploreCmd_Flags` -- --depth, --budget, --threshold parsed correctly.

---

## 12. Criterios de aceptacion

1. **BFS traversal functional:** `mem_explore` with seed having 2 direct neighbors and 1 transitive neighbor (depth=2) returns 3 nodes with distances [1, 1, 2] and accumulated_weights that are products of edge weights along the best path.

2. **Token budget respected:** `mem_explore` with budget=500 over a graph where total neighbor tokens exceed 500 returns a subset. Sum of returned `token_estimate` values <= budget.

3. **Cycle handling correct:** Graph with A -> B -> C -> A does not cause infinite loop. Each memory appears at most once in the result.

4. **Seed resolution for all 3 formats:** `mem_explore` accepts full UUID, 8-char hex prefix, and topic_key. One test per format verifies correct seed resolution.

5. **Three frontends consistent:** MCP `mem_explore`, HTTP `GET /v1/memories/{id}/explore`, and CLI `mneme explore <seed>` produce equivalent ExploreResponse data.

6. **Performance < 200ms** for depth=3 on 5K memories / 10K relations. Verified with `BenchmarkExplore_Depth3_5K`.

---

## 13. Performance budget

**Target:** < 200ms for depth=3 on 5K memories / 10K relations / 20K memory_entities.

**Key optimization: `GetMemoryMetadata`** avoids loading full content bodies. Uses `SELECT id, title, topic_key, type, length(content) FROM memories WHERE id = ?` -- single row, no large TEXT column read.

**Estimated breakdown (200 nodes, depth=3):**

| Operation | Cost/call | Max calls | Subtotal |
|-----------|-----------|-----------|----------|
| resolveSeed | 0.5ms | 1 | 0.5ms |
| GetMemoryEntities (seed) | 0.1ms | 1 | 0.1ms |
| GetStrongRelations (seed entities) | 0.5ms | ~5 | 2.5ms |
| GetEntityMemoryIDs (seed neighbors) | 0.1ms | ~50 | 5ms |
| PQ pop + GetMemoryMetadata | 0.1ms | 200 | 20ms |
| GetMemoryEntities (expanded nodes) | 0.1ms | 200 | 20ms |
| GetStrongRelations (expanded entities) | 0.5ms | ~400 | 200ms |
| GetEntityMemoryIDs (expanded neighbors) | 0.1ms | ~1000 | 100ms |
| BatchTouchRelations | 2ms | 1 | 2ms |

**Total worst-case:** ~350ms. **Over budget for worst case.**

**Practical scenario:** Most graphs are sparse. Average fan-out ~5 (not 50). With depth=3 and 5 entities/node average:
- Nodes visited: ~100 (not 200)
- GetStrongRelations calls: ~200 (not 400)
- GetEntityMemoryIDs calls: ~300 (not 1000)
- Estimated: ~140ms. **Within budget.**

**Further mitigation if needed:** Batch `GetStrongRelations` for multiple entity IDs in a single query. Not implemented in v1 — measure first, optimize if needed.

---

## 14. Open questions

None blocking. All critical decisions have been made. Q1 and Q2 below are deferred considerations.

### Q1. Include preview in ExploreNode?

A `preview` field (first 200 chars) would add ~67 tokens per node but give the agent enough context to decide whether to `mem_get` the full content. **Decision:** Add `Preview string` field to ExploreNode in a follow-up if agents request it. Not in v1 to keep the response compact.

### Q2. HTTP support for topic_key and short prefix?

Topic keys contain `/` which conflicts with URL path parsing. Short prefixes could be supported via `GET /v1/explore?seed=...` but this duplicates the MCP interface. **Decision:** Defer to feedback. HTTP is the least-used frontend for mneme.

---

## Dependencias

- **Hacia atras:** SPEC-005 (weighted relations), SPEC-006 (Hebbian, FindRelationBidirectional), SPEC-007 (GetStrongRelations, GetEntityMemoryIDs, BatchTouchRelations, graphExpand as reference, migration 008 idx_memory_entities_entity).
- **Hacia adelante:** SPEC-P1 (PPR) may reuse resolveSeed logic and ExploreResponse structure. The BFS priority queue can evolve into PPR random walk.
- **No requiere migracion SQL.** All indices exist from migrations 002, 007, 008.
