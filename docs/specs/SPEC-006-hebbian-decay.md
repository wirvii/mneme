# SPEC-006 — Hebbian Auto-Strengthening de Aristas + Edge Decay

| Campo         | Valor                                                          |
|---------------|----------------------------------------------------------------|
| **ID**        | SPEC-006                                                       |
| **Epic**      | EPIC-2 — Grafo con peso + 1-hop expansion                     |
| **Backlog**   | BL-006                                                         |
| **Estado**    | speccing -> specced                                            |
| **Owner**     | architect                                                      |
| **Fecha**     | 2026-04-30                                                     |
| **Deps**      | SPEC-005 (completada) — weighted relations, UpdateRelationWeight, TouchRelation, last_traversed_at |
| **Memorias**  | `roadmap/v2-master-plan`, `spec/SPEC-005-weighted-relations-design`, `spec/SPEC-005-implementation-notes`, `spec/SPEC-002-context-injection-design`, `architecture/memory-model`, `architecture/scoring-formulas`, `architecture/project-structure` |

---

## 1. Contexto y motivacion

### El modelo de Hebb

"Neurons that fire together, wire together." En mneme, las memorias accedidas juntas en una sesion deberian tener aristas mas fuertes entre sus entidades en el knowledge graph. Hoy, los pesos de relacion son estaticos: se asignan por tipo al crear la relacion (e.g. `depends_on=0.9`, `related_to=0.5`) y solo cambian si alguien llama explicitamente a `UpdateRelationWeight`. Ningun componente de mneme observa los patrones de acceso para reforzar relaciones.

### El complemento: decay

Inversamente, las relaciones que no se traversan deberian debilitarse con el tiempo. Sin decay, el grafo acumula aristas historicas con pesos altos que ya no reflejan la relevancia actual. El decay de aristas complementa el decay de memorias (que ya existe en consolidation) aplicando la misma filosofia al grafo.

### Que habilita

- **SPEC-G3 (1-hop expansion + RRF):** La expansion ponderada por `weight` solo tiene sentido si los pesos reflejan el uso real, no solo defaults estaticos.
- **SPEC-P1 (PPR):** Personalized PageRank sobre un grafo con pesos informados por uso real produce rankings significativamente mejores.
- **SPEC-C1 (Louvain):** La deteccion de comunidades requiere pesos que reflejen la cohesion real, no solo la topologia.

### Que preparo SPEC-005

SPEC-005 entrego la infraestructura necesaria:
- `store.UpdateRelationWeight(ctx, relationID, delta, now)` — update atomico con clamping SQL (`internal/store/entity.go:217-237`).
- `store.TouchRelation(ctx, relationID, now)` — timestamp-only (`internal/store/entity.go:243-258`).
- `last_traversed_at` en tabla `relations` (`internal/db/migrations/007_weighted_relations.sql:271`).
- Indice `idx_relations_last_traversed` para queries de decay.
- Weight normalizado [0.0, 1.0] con defaults por tipo.

---

## 2. Decisiones de diseno

### D1. Scope-cross relations — aristas entre memoria-project y memoria-global

**Problema:** mneme tiene dos DBs separadas: `~/.mneme/projects/<slug>.db` (project scope) y `~/.mneme/global.db` (global/org scope). Las tablas `entities` y `relations` existen **en cada DB** (migration 002 corre en ambas). Si un agente accede una memoria de project y luego una de global en la misma sesion, el tracker genera un par que involucra IDs de entidades en DBs distintas.

**Analisis del codigo actual:**
- `service.Get()` (`internal/service/memory.go:184-199`) busca en projectStore primero, luego globalStore. Retorna la memoria pero no indica de que store provino (salvo indirectamente via `foundIn`).
- `service.Relate()` (`internal/service/graph.go:27-111`) **siempre opera sobre projectStore** (`svc.projectStore.FindOrCreateEntity`, lineas 59-67). No hay un `Relate` cross-store.
- `service.Search()` (`internal/service/search.go:36-97`) fusiona resultados de ambos stores en una sola lista. El agente ve memorias de ambos scopes mezcladas.

**Decision:** El tracker Hebbian **ignora pares cross-scope**. Cuando el tracker genera un par (memA, memB) y las dos memorias provienen de stores distintos (una de projectStore, otra de globalStore), el par se descarta silenciosamente.

**Justificacion:**
1. Las relaciones viven dentro de cada DB. Crear una relacion en projectStore entre una entidad local y un ID de globalStore romperia la FK constraint (`relations.source_id REFERENCES entities(id)`).
2. El valor de una relacion cross-scope es dudoso: una memoria global como "prefiero tabs sobre spaces" y una memoria de project como "auth-service bug fix" no tienen una relacion semantica significativa por co-acceso.
3. Implementar relaciones cross-DB requeriria o (a) una tabla de relaciones externa a ambas DBs, o (b) una tabla bridge que rompe la arquitectura de "scopes never leak". Ambas opciones son complejas y no se justifican para la v1 de Hebbian.
4. El tracker necesita saber de que store proviene cada memoria. Esto se resuelve anotando el scope en el evento de acceso (ver seccion 3).

**Alternativa para el futuro:** Si SPEC-P1 (PPR) necesita un grafo unificado, se puede construir una vista in-memory al vuelo fusionando ambos grafos. Pero eso es PPR's problem, no Hebbian's.

### D2. Async vs sync write — worker pool con buffered channel

**Decision:** El strengthening se ejecuta en un worker pool con un buffered channel de capacidad 1000. Si el channel esta lleno, el evento se descarta (drop). El tracker nunca bloquea el read path.

**Justificacion:**
- `service.Get()` es la operacion mas sensible a latencia (el agente espera el resultado). Agregar una escritura sincrona a la DB dentro de Get degradaria la experiencia.
- El worker pool acepta `StrengtheningEvent` structs (par de memory IDs + delta) y aplica `store.UpdateRelationWeight` o `store.CreateRelation` segun corresponda.
- Drop en channel full es la politica correcta porque: (a) el strengthening es una senal debil (un co-acceso individual no es critico), (b) si el channel esta full el sistema esta bajo carga y agregar mas writes empeoraria todo, (c) la metrica `slog` `event=hebbian_dropped` permite detectar si esto ocurre frecuentemente y ajustar el buffer.

**Pseudocodigo del worker pool:**

```go
// HebbianWorkerPool processes relation-strengthening events asynchronously.
// It owns a buffered channel and a configurable number of workers that drain
// events by calling store.UpdateRelationWeight / store.CreateRelation.
type HebbianWorkerPool struct {
    ch     chan StrengtheningEvent
    store  *store.MemoryStore
    wg     sync.WaitGroup
    logger *slog.Logger
    config GraphConfig
}

// StrengtheningEvent represents a co-access pair to be strengthened.
type StrengtheningEvent struct {
    SourceEntityID string
    TargetEntityID string
    RelationType   model.RelationType
    Delta          float64
}

func NewHebbianWorkerPool(s *store.MemoryStore, cfg GraphConfig, logger *slog.Logger) *HebbianWorkerPool {
    return &HebbianWorkerPool{
        ch:     make(chan StrengtheningEvent, cfg.HebbianBufferSize), // default 1000
        store:  s,
        logger: logger,
        config: cfg,
    }
}

func (p *HebbianWorkerPool) Start(ctx context.Context) {
    numWorkers := 1 // see D2a
    for i := 0; i < numWorkers; i++ {
        p.wg.Add(1)
        go func() {
            defer p.wg.Done()
            for {
                select {
                case <-ctx.Done():
                    return
                case evt, ok := <-p.ch:
                    if !ok {
                        return
                    }
                    p.applyStrengthening(ctx, evt)
                }
            }
        }()
    }
}

// Enqueue sends an event to the worker pool. Returns false if the channel
// is full (event dropped).
func (p *HebbianWorkerPool) Enqueue(evt StrengtheningEvent) bool {
    select {
    case p.ch <- evt:
        return true
    default:
        p.logger.Warn("hebbian_dropped",
            "event", "hebbian_dropped",
            "source", evt.SourceEntityID,
            "target", evt.TargetEntityID,
        )
        return false
    }
}

// Drain waits for all pending events to be processed, with a timeout.
// Used during shutdown of short-lived CLI processes.
func (p *HebbianWorkerPool) Drain(timeout time.Duration) {
    close(p.ch)
    done := make(chan struct{})
    go func() {
        p.wg.Wait()
        close(done)
    }()
    select {
    case <-done:
    case <-time.After(timeout):
        p.logger.Warn("hebbian_drain_timeout", "timeout", timeout)
    }
}

func (p *HebbianWorkerPool) applyStrengthening(ctx context.Context, evt StrengtheningEvent) {
    // Try to find existing relation between the two entities.
    existing, err := p.store.FindRelation(ctx, evt.SourceEntityID, evt.TargetEntityID, evt.RelationType)
    if err != nil {
        p.logger.Error("hebbian_find_relation_error",
            "event", "hebbian_error",
            "source", evt.SourceEntityID,
            "target", evt.TargetEntityID,
            "error", err,
        )
        return // log + skip, never retry
    }

    if existing != nil {
        // Strengthen existing relation.
        _, err = p.store.UpdateRelationWeight(ctx, existing.ID, evt.Delta, time.Now().UTC())
        if err != nil {
            p.logger.Error("hebbian_update_error",
                "event", "hebbian_error",
                "relation_id", existing.ID,
                "error", err,
            )
            return
        }
        p.logger.Debug("hebbian_strengthened",
            "event", "hebbian_strengthened",
            "relation_id", existing.ID,
            "delta", evt.Delta,
        )
        return
    }

    // No relation exists — create one with HebbianInitialWeight.
    rel := &model.Relation{
        SourceID: evt.SourceEntityID,
        TargetID: evt.TargetEntityID,
        Type:     evt.RelationType,
        Weight:   p.config.HebbianInitialWeight,
    }
    created, err := p.store.CreateRelation(ctx, rel)
    if err != nil {
        p.logger.Error("hebbian_create_error",
            "event", "hebbian_error",
            "source", evt.SourceEntityID,
            "target", evt.TargetEntityID,
            "error", err,
        )
        return
    }
    p.logger.Debug("hebbian_created",
        "event", "hebbian_created",
        "relation_id", created.ID,
        "weight", p.config.HebbianInitialWeight,
    )
}
```

#### D2a. numWorkers = 1

**Decision:** El worker pool usa un solo worker goroutine.

**Justificacion:** SQLite serializa todos los writes via WAL. Multiples workers no obtienen paralelismo real — solo contention en el busy_timeout. Un solo worker mantiene el orden causal y simplifica el razonamiento. Si en el futuro se migra a un backend con write concurrency, se puede subir `numWorkers` sin cambiar la interfaz.

#### D2b. Comportamiento ante SQL failure

**Decision:** Log + skip. No retry.

**Justificacion:** Un strengthening fallido no corrompe datos. El peor caso es que una relacion no se fortalece. Retry en SQLite con WAL es innecesario porque `busy_timeout=5000` ya maneja la contention. Si el error es de otro tipo (disk full, corruption), reintentar empeora todo.

#### D2c. Cierre limpio en shutdown

**Decision:** `Drain(timeout)` cierra el channel y espera a que el worker procese todos los eventos pendientes, con un timeout.

**Justificacion:** En el MCP server long-running, ctx cancellation cierra el worker via `<-ctx.Done()`. En CLI commands cortos, `Drain(200*time.Millisecond)` da tiempo para procesar los ultimos eventos sin bloquear el proceso indefinidamente. 200ms es suficiente para ~100 strengthening events a ~2ms cada uno.

### D3. Window dedup — set, no multiset

**Problema:** Si el sliding window contiene [A, B, A, B, A], el acceso a A genera pares (A,B), (A,A), (A,B), (A,A). Esto produce duplicados que sobrefortalecen la relacion A<->B.

**Decision:** Antes de generar pares, el tracker extrae los IDs unicos del window como un **set**. Si window=[A,B,A,B,A], el set es {A,B}. Pares generados: solo (A,B). Cada par se genera una sola vez por evento de acceso.

**Justificacion:**
- El multiset generaria pares proporcionales a la frecuencia, lo cual sobrefortaleceria relaciones entre memorias accedidas muchas veces. Pero el hecho de que A aparezca 3 veces en el window no significa que la relacion A<->B sea 3x mas fuerte — solo que A fue accedida frecuentemente.
- El set approach trata cada co-presencia como una senal binaria: "A y B coexistieron en la ventana reciente, reforzar una vez."
- Adicionalmente, un set `seen` por evento previene que el mismo par se encole multiples veces dentro de un solo `Record()` call.

### D4. Self-loop guard

**Decision:** En `tracker.Record(memoryID)`, si `memoryID == lastID` (el ultimo ID registrado), no encolar al ring buffer ni generar pares.

**Justificacion:** Un agente que llama `mem_get` dos veces seguidas al mismo ID (e.g. por retry o re-read) no debe generar un self-loop en el grafo. Ademas, un par (A,A) no tiene significado en un grafo de relaciones.

### D5. Exclusion de rules y synthesis del tracking

**Decision:** El tracker ignora memorias de tipo `rule` y `session_summary`. La exclusion se aplica en `tracker.Record()` consultando el tipo de la memoria.

**Mecanismo:** `tracker.Record(memoryID, memoryType)` recibe el tipo como segundo argumento. Si `memoryType == model.TypeRule || memoryType == model.TypeSessionSummary`, retorna inmediatamente sin registrar.

**Justificacion:**
- **Rules** son inyectadas en cada sesion via `loadActiveRules()` (`internal/service/context.go:309-345`). Cada `mem_context` accede todas las rules activas. Si las trackearamos, cada rule tendria aristas fuertes con todas las demas memorias accedidas en cualquier sesion — ruido puro.
- **Session summaries** son memorias sinteticas de cierre de sesion. Son accedidas por `GetLastSession()` (`internal/service/context.go:231-245`) en cada `mem_context`. Mismo problema que rules.
- **No excluimos otros tipos** (discovery, decision, architecture, etc.) porque su co-acceso genuinamente refleja relaciones semanticas.
- El tipo `synthesis` (SPEC-C3, futuro) debera agregarse a la exclusion cuando se implemente.

**Nota de implementacion:** El tipo se pasa como argumento para evitar una llamada adicional a `store.Get()` dentro del tracker. Los callers (`service.Get`, `service.Search`) ya tienen el tipo disponible.

### D6. Edge decay sobre `last_traversed_at IS NULL`

**Decision:** Las relaciones con `last_traversed_at IS NULL` NO reciben decay.

**Justificacion:**
- `last_traversed_at IS NULL` significa que la relacion fue creada explicitamente por el agente (via `mem_relate`) pero nunca ha sido atravesada por el sistema de recuperacion. Estas son relaciones intencionales, no emergentes del uso.
- Aplicar decay a relaciones explicitamente creadas penalizaria decisiones del agente. Si el agente creo una relacion `auth-service -> jwt-library (depends_on, 0.9)`, esa relacion tiene significado estructural independientemente de cuantas veces se travese.
- El decay se aplica solo a relaciones que **alguna vez fueron traversadas** (`last_traversed_at IS NOT NULL`) y cuyo ultimo traversal fue hace mas de `EdgeDecayAfterDays` dias.
- Las relaciones creadas por Hebbian auto-strengthening siempre tendran `last_traversed_at` set (porque el create les pone un timestamp), asi que siempre son elegibles para decay futuro.

### D7. No eliminar relaciones con weight=0

**Decision:** Las relaciones cuyo weight llega a 0.0 por decay no se eliminan. Se mantienen en la tabla como aristas de peso cero.

**Justificacion:**
- Un weight de 0.0 indica que la relacion ha perdido toda relevancia temporal, pero la relacion topologica sigue existiendo. Si SPEC-P1 (PPR) necesita la topologia completa para calcular random walk probabilities, las aristas de peso 0 son utiles como caminos potenciales.
- Si se eliminaran, un ciclo de decay -> re-acceso -> Hebbian create -> decay -> re-create generaria churn de UUIDs y metadata perdida.
- El costo de almacenamiento es minimo: una relacion con weight=0.0 ocupa ~200 bytes en SQLite.
- **Opcion futura:** Un flag de configuracion `EdgePruneEnabled` podria habilitar la eliminacion de relaciones con weight=0 despues de N dias consecutivos en 0. No se implementa en SPEC-006.

### D8. Default values

**Confirmacion de defaults:**

| Config key | Value | Justificacion |
|-----------|-------|---------------|
| `HebbianWindow` | 5 | Ventana de 5 memorias es suficiente para capturar co-acceso en un task tipico. Ventanas mas grandes generan demasiados pares (5 produce max 10 pares, 10 produce max 45). |
| `HebbianIncrement` | 0.05 | Con weight en [0.0, 1.0], un increment de 0.05 requiere ~20 co-accesos para llevar una relacion de default 0.5 a 1.0. Suficientemente gradual para evitar sobrefortalecimiento. |
| `HebbianInitialWeight` | 0.1 | Relaciones emergentes del co-acceso empiezan debiles. Solo si el co-acceso se repite, suben a niveles significativos. 0.1 esta por debajo de cualquier default por tipo (el minimo es `references=0.4`), diferenciando relaciones Hebbian de relaciones explicitas. |
| `HebbianBufferSize` | 1000 | En una sesion tipica, un agente accede ~50-200 memorias. 1000 da un margen de 5-20x para picos sin drops. |
| `EdgeDecayRate` | 0.02 | Con `effectiveWeight = weight * e^(-0.02 * days)`, una relacion sin traversar pierde ~2% de peso diario. En 30 dias pierde ~45% (weight 0.5 -> 0.27). Suficientemente agresivo para degradar relaciones obsoletas sin destruir relaciones moderadamente usadas. |
| `EdgeDecayAfterDays` | 30 | 30 dias de gracia antes de empezar el decay. Relaciones traversadas dentro del ultimo mes no sufren decay. Alinea con `Consolidation.RetentionDays` (default 30, `internal/config/config.go:241`). |

---

## 3. Modelo del Tracker — ring buffer + thread-safe + sliding window dedup

### Estructura

```go
// AccessTracker maintains a sliding window of recently accessed memories and
// generates co-access pairs for Hebbian strengthening. It is thread-safe.
//
// The tracker uses a fixed-size ring buffer to store the last N accessed
// memory IDs (N = HebbianWindow). When a new memory is recorded, it
// generates pairs with every unique ID in the current window, deduplicates
// them, and sends StrengtheningEvents to the worker pool.
type AccessTracker struct {
    mu       sync.Mutex
    ring     []trackedAccess    // ring buffer of size HebbianWindow
    pos      int                // next write position in ring
    count    int                // number of valid entries (0..HebbianWindow)
    lastID   string             // last recorded memory ID (for self-loop guard)
    pool     *HebbianWorkerPool
    config   GraphConfig
    logger   *slog.Logger
}

// trackedAccess records a memory access in the ring buffer, including the
// entity IDs linked to that memory so that strengthening events can reference
// graph nodes rather than memory IDs.
type trackedAccess struct {
    memoryID  string
    entityIDs []string // entity IDs linked to this memory via memory_entities
    scope     model.Scope
}

func NewAccessTracker(pool *HebbianWorkerPool, cfg GraphConfig, logger *slog.Logger) *AccessTracker {
    windowSize := cfg.HebbianWindow
    if windowSize <= 0 {
        windowSize = 5
    }
    return &AccessTracker{
        ring:   make([]trackedAccess, windowSize),
        pool:   pool,
        config: cfg,
        logger: logger,
    }
}

// Record registers a memory access. It generates co-access pairs with every
// unique memory in the current window and enqueues strengthening events.
//
// Excluded types (rule, session_summary) return immediately without
// registering. Self-loops (same ID as last recorded) are also skipped.
//
// entityIDs are the entity IDs linked to this memory via memory_entities.
// When empty, no strengthening events are generated (the memory has no
// graph presence).
func (t *AccessTracker) Record(memoryID string, memoryType model.MemoryType, memoryScope model.Scope, entityIDs []string) {
    // Exclusion guard: rules and session summaries generate noise.
    if memoryType == model.TypeRule || memoryType == model.TypeSessionSummary {
        return
    }

    // HebbianWindow=0 means tracking is disabled.
    if t.config.HebbianWindow <= 0 {
        return
    }

    // No graph presence — nothing to strengthen.
    if len(entityIDs) == 0 {
        return
    }

    t.mu.Lock()
    defer t.mu.Unlock()

    // Self-loop guard.
    if memoryID == t.lastID {
        return
    }
    t.lastID = memoryID

    // Collect unique (entityID-pair, scope-compatible) entries from the window
    // as a set to deduplicate.
    type pairKey struct{ src, tgt string }
    seen := make(map[pairKey]bool)

    for i := 0; i < t.count; i++ {
        idx := (t.pos - 1 - i + len(t.ring)) % len(t.ring)
        prev := t.ring[idx]

        // Cross-scope guard: skip if scopes differ (D1).
        if prev.scope != memoryScope {
            continue
        }

        // Generate pairs: each entity of the new memory with each entity of
        // the previous memory.
        for _, newEnt := range entityIDs {
            for _, prevEnt := range prev.entityIDs {
                if newEnt == prevEnt {
                    continue // same entity, skip
                }
                // Canonical order to avoid (A,B) and (B,A) as separate pairs.
                src, tgt := newEnt, prevEnt
                if src > tgt {
                    src, tgt = tgt, src
                }
                key := pairKey{src, tgt}
                if seen[key] {
                    continue
                }
                seen[key] = true

                t.pool.Enqueue(StrengtheningEvent{
                    SourceEntityID: src,
                    TargetEntityID: tgt,
                    RelationType:   model.RelRelatedTo,
                    Delta:          t.config.HebbianIncrement,
                })
            }
        }
    }

    // Write to ring buffer.
    t.ring[t.pos] = trackedAccess{
        memoryID:  memoryID,
        entityIDs: entityIDs,
        scope:     memoryScope,
    }
    t.pos = (t.pos + 1) % len(t.ring)
    if t.count < len(t.ring) {
        t.count++
    }
}
```

### Notas de diseno del ring buffer

1. **Fixed size:** El ring buffer tiene exactamente `HebbianWindow` slots. Cuando se llena, el slot mas viejo se sobrescribe. No hay allocacion despues de la inicializacion.
2. **Canonical pair ordering:** `src, tgt = min(a,b), max(a,b)` evita que (A,B) y (B,A) se traten como pares distintos. Solo se fortalece la relacion una vez.
3. **Entity-level, no memory-level:** El tracker opera sobre entity IDs, no memory IDs. Esto es porque las relaciones del grafo conectan entidades, no memorias. Una memoria puede estar vinculada a multiples entidades via `memory_entities`. El tracker obtiene los entity IDs de la memoria via `store.GetMemoryEntities()`.
4. **Scope anotada:** Cada entrada del ring buffer incluye el scope de la memoria para filtrar cross-scope en D1.

---

## 4. Modelo del worker pool

(Ver pseudocodigo completo en D2.)

### Resumen de comportamiento

| Aspecto | Valor |
|---------|-------|
| `numWorkers` | 1 (D2a) |
| Buffer size | `HebbianBufferSize` (default 1000) |
| Channel full | Drop + slog warn (D2) |
| SQL failure | Log error + skip (D2b) |
| Relation not found | Create with `HebbianInitialWeight` |
| Relation exists | `UpdateRelationWeight(id, delta)` |
| Shutdown (MCP) | ctx cancellation, worker drains remaining |
| Shutdown (CLI) | `Drain(200ms)` timeout |
| RelationType for Hebbian | `related_to` (tipo generico para co-acceso emergente) |

### Porque `related_to` como tipo de relacion

Las relaciones creadas por Hebbian son emergentes del co-acceso, no tienen un tipo semantico fuerte (no son `depends_on` ni `implements`). `related_to` es el tipo generico con default weight 0.5, pero Hebbian les asigna `HebbianInitialWeight=0.1` al crear, diferenciandolas. El tipo puede refinarse en el futuro si se identifica una taxonomia mas rica.

---

## 5. Modelo del decay sweep

### Cuando corre

El edge decay corre como un nuevo paso dentro del pipeline de consolidation existente (`internal/consolidation/consolidation.go`). Se ejecuta **despues del sweep de memorias y antes de dedup**, porque el decay de aristas puede hacer que las relaciones reflejen mejor el estado real antes de que dedup busque duplicados.

**Secuencia actualizada de `Pipeline.Run()`:**

```
sweep (memories) -> edgeDecay (relations) -> hardDelete -> dedup -> budget
```

### SQL del decay

```sql
UPDATE relations
SET weight = MAX(0.0, weight * EXP(-? * (julianday('now') - julianday(last_traversed_at))))
WHERE last_traversed_at IS NOT NULL
  AND julianday('now') - julianday(last_traversed_at) > ?
```

Parametros:
1. `EdgeDecayRate` (default 0.02)
2. `EdgeDecayAfterDays` (default 30)

### Explicacion

- `weight * EXP(-rate * days_since_last_traversal)` aplica decay exponencial, identico al modelo usado para memorias (`scoring.EffectiveImportanceAt`).
- `WHERE last_traversed_at IS NOT NULL` excluye relaciones explicitas no traversadas (D6).
- `julianday('now') - julianday(last_traversed_at) > EdgeDecayAfterDays` aplica el grace period.
- `MAX(0.0, ...)` previene weights negativos (aunque EXP nunca produce negativos, es una guardia defensiva).

### Batch size

No se necesita batching. La query actualiza todas las relaciones elegibles en un solo UPDATE. Para 100K relaciones, SQLite ejecuta este UPDATE en <500ms (constraint del criterio de aceptacion). El indice `idx_relations_last_traversed` permite filtrar eficientemente.

### Resultado del sweep

El metodo retorna el numero de relaciones actualizadas. Se agrega un campo `EdgeDecayed int` a `ConsolidationResult`.

### Implementacion en Go

```go
// edgeDecay applies exponential weight decay to relations that have not been
// traversed within the configured grace period. Relations with
// last_traversed_at IS NULL (explicit, never traversed) are excluded.
func (p *Pipeline) edgeDecay(ctx context.Context) (int, error) {
    decayRate := p.config.Graph.EdgeDecayRate
    graceDays := p.config.Graph.EdgeDecayAfterDays

    if decayRate <= 0 {
        return 0, nil // decay disabled
    }

    const q = `
        UPDATE relations
        SET weight = MAX(0.0, weight * EXP(- ? * (julianday('now') - julianday(last_traversed_at))))
        WHERE last_traversed_at IS NOT NULL
          AND julianday('now') - julianday(last_traversed_at) > ?`

    result, err := p.store.ExecContext(ctx, q, decayRate, graceDays)
    if err != nil {
        return 0, fmt.Errorf("consolidation: edge decay: %w", err)
    }
    rows, _ := result.RowsAffected()

    if rows > 0 {
        p.logger.Info("consolidation: edge decay applied",
            "event", "edge_decay",
            "relations_decayed", rows,
            "decay_rate", decayRate,
            "grace_days", graceDays,
        )
    }

    return int(rows), nil
}
```

**Nota sobre `store.ExecContext`:** El pipeline actualmente opera via `store.MemoryStore` methods. El edge decay query es raw SQL que no tiene un store method dedicated. La opcion es: (a) agregar un `store.DecayRelationWeights(ctx, rate, graceDays)` method, o (b) exponer `ExecContext` en el store. Se prefiere (a) por consistencia con el patron repository — la raw SQL no debe escapar del store.

---

## 6. Configuracion nueva — `[graph]` section

### En `config.go`:

```go
// GraphConfig controls the knowledge graph's Hebbian auto-strengthening
// and edge decay behaviour. This section is evaluated by the access tracker,
// worker pool, and consolidation pipeline.
type GraphConfig struct {
    // HebbianWindow is the number of recently accessed memories tracked for
    // co-access pair generation. Set to 0 to disable Hebbian strengthening.
    // Default: 5.
    HebbianWindow int `toml:"hebbian_window"`

    // HebbianIncrement is the weight delta applied to a relation when two
    // memories co-occur in the access window. Default: 0.05.
    HebbianIncrement float64 `toml:"hebbian_increment"`

    // HebbianInitialWeight is the weight assigned when Hebbian creates a new
    // relation that didn't exist before. Default: 0.1.
    HebbianInitialWeight float64 `toml:"hebbian_initial_weight"`

    // HebbianBufferSize is the capacity of the async strengthening channel.
    // Events are dropped when the buffer is full. Default: 1000.
    HebbianBufferSize int `toml:"hebbian_buffer_size"`

    // EdgeDecayRate is the daily exponential decay rate applied to relation
    // weights during consolidation. Set to 0 to disable edge decay.
    // Default: 0.02.
    EdgeDecayRate float64 `toml:"edge_decay_rate"`

    // EdgeDecayAfterDays is the number of days after last_traversed_at before
    // edge decay begins. Relations traversed more recently are not decayed.
    // Default: 30.
    EdgeDecayAfterDays int `toml:"edge_decay_after_days"`
}
```

### Defaults (en `config.Default()`):

```go
Graph: GraphConfig{
    HebbianWindow:        5,
    HebbianIncrement:     0.05,
    HebbianInitialWeight: 0.1,
    HebbianBufferSize:    1000,
    EdgeDecayRate:        0.02,
    EdgeDecayAfterDays:   30,
},
```

### Validation:

```go
if c.Graph.HebbianWindow < 0 {
    return errors.New("graph.hebbian_window must be >= 0")
}
if c.Graph.HebbianIncrement < 0 || c.Graph.HebbianIncrement > 1 {
    return errors.New("graph.hebbian_increment must be in [0.0, 1.0]")
}
if c.Graph.HebbianInitialWeight < 0 || c.Graph.HebbianInitialWeight > 1 {
    return errors.New("graph.hebbian_initial_weight must be in [0.0, 1.0]")
}
if c.Graph.HebbianBufferSize < 0 {
    return errors.New("graph.hebbian_buffer_size must be >= 0")
}
if c.Graph.EdgeDecayRate < 0 {
    return errors.New("graph.edge_decay_rate must be >= 0")
}
if c.Graph.EdgeDecayAfterDays < 0 {
    return errors.New("graph.edge_decay_after_days must be >= 0")
}
```

### `config.toml` example:

```toml
[graph]
hebbian_window = 5
hebbian_increment = 0.05
hebbian_initial_weight = 0.1
hebbian_buffer_size = 1000
edge_decay_rate = 0.02
edge_decay_after_days = 30
```

### Environment variable overrides:

No env overrides for graph config in v1. The values are not frequently changed per-deployment. If needed, they can be added following the pattern in `config.Load()` (`internal/config/config.go:310-328`).

---

## 7. Contratos

### 7.1. Sin cambios en MCP/HTTP API publicos

El Hebbian auto-strengthening y el edge decay son **comportamiento interno**. No se agregan tools, endpoints, ni parametros de request/response.

- `mem_get` sigue retornando el mismo response JSON.
- `mem_search` sigue retornando el mismo response JSON.
- `mem_context` sigue retornando el mismo response JSON.
- Los pesos de las relaciones cambian implicitamente con el uso, lo cual se refleja en futuras queries de `mem_relate` (que retorna `weight`) y en SPEC-G3 (1-hop expansion que usa weight para ranking).

### 7.2. Configuracion nueva

Seccion `[graph]` en `config.toml` con 6 parametros (ver seccion 6). Todos tienen defaults sensatos en `config.Default()`, por lo que no se requiere migracion de configuracion.

### 7.3. Cambios en `ConsolidationResult`

Nuevo campo:

```go
// EdgeDecayed is the number of relations whose weight was reduced by the
// edge decay sweep. Only counted when EdgeDecayRate > 0.
EdgeDecayed int `json:"edge_decayed"`
```

Afecta el output de:
- `mneme consolidate` CLI (agrega `edge_decayed: N` al output).
- `POST /v1/consolidate` HTTP (campo nuevo en JSON response).
- `slog` consolidation cycle log (campo nuevo).

### 7.4. Telemetria slog

| Event | Level | Campos | Cuando |
|-------|-------|--------|--------|
| `hebbian_strengthened` | Debug | `event`, `relation_id`, `delta` | Worker fortalece una relacion existente |
| `hebbian_created` | Debug | `event`, `relation_id`, `weight` | Worker crea una relacion nueva |
| `hebbian_dropped` | Warn | `event`, `source`, `target` | Channel full, evento descartado |
| `hebbian_error` | Error | `event`, `relation_id` o `source`+`target`, `error` | SQL failure en worker |
| `hebbian_drain_timeout` | Warn | `timeout` | Drain excede timeout |
| `edge_decay` | Info | `event`, `relations_decayed`, `decay_rate`, `grace_days` | Sweep de consolidation completo |
| `tracker_started` | Info | `event`, `window`, `buffer_size`, `increment` | Tracker inicializado |

---

## 8. Edge cases

### 8.1. Tracker en CLI corto (e.g. `mneme search`)

**Situacion:** Un comando CLI como `mneme search "auth"` inicia el servicio, ejecuta Search, y termina.

**Decision:** SI se procesan los strengthenings. El flujo es:
1. `initService()` crea el `MemoryService`.
2. `MemoryService` crea el `AccessTracker` y `HebbianWorkerPool`.
3. El pool se inicia con `Start(ctx)` en `service.Start(ctx)`.
4. Search llama `tracker.Record()` para cada resultado accedido.
5. Antes de `cleanup()`, se llama `pool.Drain(200*time.Millisecond)`.
6. Si los events no se procesaron en 200ms, se loguea warning y se descarta.

**Justificacion:** 200ms es un budget aceptable para un CLI command. Permite procesar ~100 strengthening events. Si el usuario ejecuta `mneme search` repetidamente, el fortalecimiento gradual se acumula entre invocaciones.

### 8.2. Tracker en MCP server long-running

Comportamiento normal. El pool corre mientras ctx este activo. `<-ctx.Done()` cierra el worker limpiamente cuando el agente desconecta o el proceso recibe SIGTERM.

### 8.3. DB locked durante UpdateRelationWeight async

**Decision:** Log error + skip (D2b). `busy_timeout=5000` (`internal/db/db.go` linea ~47) ya maneja la contention normal de SQLite. Si despues de 5 segundos la DB sigue locked, es un problema mayor (otro proceso con exclusive lock). Loguear y continuar es la unica opcion razonable.

### 8.4. Race entre Touch y Decay sweep

**Situacion:** Una relacion se traversa (TouchRelation actualiza `last_traversed_at` a ahora) justo antes de que el decay sweep ejecute su UPDATE.

**Resolucion natural:** El decay SQL tiene `WHERE julianday('now') - julianday(last_traversed_at) > ?`. Si `last_traversed_at` fue actualizado a ahora, `julianday('now') - julianday(now) = 0`, que es `< 30`. La relacion no recibe decay. El SQL lo cubre naturalmente sin necesidad de locking adicional.

### 8.5. HebbianWindow=0

Toggle off. `tracker.Record()` retorna inmediatamente sin registrar nada. El pool puede iniciarse (es un no-op si nadie encola). La seccion `[graph]` con `hebbian_window = 0` desactiva todo el tracking.

### 8.6. HebbianWindow=1

Una ventana de 1 elemento no produce pares. Cuando se registra un acceso, el window contiene solo ese elemento. No hay "previos" con los cuales generar pares. Efecto: tracking esta activo pero nunca genera strengthenings. Documentar como "no-op implicito" en config validation, sin retornar error.

### 8.7. EdgeDecayRate=0

Toggle off. `edgeDecay()` retorna inmediatamente con `return 0, nil`. Las relaciones mantienen sus pesos indefinidamente.

### 8.8. Memoria sin entidades vinculadas

Si una memoria no tiene entidades vinculadas (e.g. fue creada sin que `mem_relate` haya vinculado su contenido al grafo), `entityIDs` sera vacio y `tracker.Record()` retorna sin generar eventos. Esto es correcto: no hay nodos en el grafo que fortalecer.

### 8.9. Dos memorias vinculadas a la misma entidad

Si memoria A y memoria B estan ambas vinculadas a entidad X, el par (X, X) se descarta por la guard `if newEnt == prevEnt { continue }`. No se genera self-loop ni strengthening.

### 8.10. HebbianIncrement acumulativo supera 1.0

Si una relacion recibe suficientes strengthening events para que su weight supere 1.0, el SQL clamping `MIN(1.0, weight + delta)` lo previene. El weight nunca supera 1.0. Ya probado en SPEC-005.

---

## 9. Apps/Modulos afectados

| Modulo | Tipo de cambio |
|--------|---------------|
| `internal/config/config.go` | Nuevo: `GraphConfig` struct, defaults, validation |
| `internal/config/config_test.go` | Nuevo: tests para GraphConfig |
| `internal/graph/` | **Nuevo paquete**: `AccessTracker`, `HebbianWorkerPool`, `StrengtheningEvent` |
| `internal/graph/tracker.go` | **Nuevo**: ring buffer, Record, exclusion guards |
| `internal/graph/tracker_test.go` | **Nuevo**: tests del tracker |
| `internal/graph/worker.go` | **Nuevo**: worker pool, Enqueue, Drain, applyStrengthening |
| `internal/graph/worker_test.go` | **Nuevo**: tests del worker pool |
| `internal/service/memory.go` | Modificacion: `MemoryService` gana campo `tracker`, `Get` y `Search` llaman `tracker.Record` |
| `internal/service/consolidation.go` | Modificacion: `Start` inicia el worker pool |
| `internal/consolidation/consolidation.go` | Modificacion: nuevo paso `edgeDecay`, campo `EdgeDecayed` en result |
| `internal/consolidation/consolidation_test.go` | Modificacion: tests de edge decay |
| `internal/store/entity.go` | Modificacion: nuevo metodo `DecayRelationWeights` |
| `internal/store/entity_test.go` | Modificacion: tests de `DecayRelationWeights` |

### Fuera de scope

- Cambios en MCP tools / HTTP endpoints / CLI commands (es comportamiento interno).
- Cambios en el schema SQL (SPEC-005 ya creo los indices y columnas necesarias).
- Hebbian sobre relaciones cross-scope (ver D1, futuro).
- Poda de relaciones con weight=0 (ver D7, futuro).
- Tipo `synthesis` en la exclusion list (SPEC-C3, futuro).

---

## 10. Plan de implementacion atomico

Sigue el patron model -> config -> store -> graph -> service -> consolidation, 6 commits.

| # | Commit | Archivos | Descripcion |
|---|--------|----------|-------------|
| 1 | `feat(config): add [graph] section with Hebbian and edge decay settings` | `internal/config/config.go`, `internal/config/config_test.go` | Nuevo `GraphConfig` struct, defaults en `Default()`, validation en `Validate()`. Tests: defaults, validation errors, zero-value toggles. |
| 2 | `feat(store): add DecayRelationWeights method` | `internal/store/entity.go`, `internal/store/entity_test.go` | Nuevo metodo `DecayRelationWeights(ctx, rate, graceDays)` con SQL de decay exponencial. Tests: decay after grace period, no decay before grace, no decay on NULL, no decay when rate=0. |
| 3 | `feat(graph): add AccessTracker with ring buffer and sliding window dedup` | `internal/graph/tracker.go`, `internal/graph/tracker_test.go` | Nuevo paquete `internal/graph/`. AccessTracker con ring buffer, Record, exclusion guards (rule, session_summary), self-loop guard, cross-scope guard, window dedup. Tests: table-driven for all edge cases. |
| 4 | `feat(graph): add HebbianWorkerPool with async strengthening` | `internal/graph/worker.go`, `internal/graph/worker_test.go` | HebbianWorkerPool: Start, Enqueue, Drain, applyStrengthening. Tests: create new relation, strengthen existing, channel full drop, drain timeout. |
| 5 | `feat(service): wire AccessTracker into Get and Search, start worker pool` | `internal/service/memory.go`, `internal/service/consolidation.go` | MemoryService gains tracker field. Get/Search call tracker.Record. Start initializes worker pool. Drain on cleanup. |
| 6 | `feat(consolidation): add edge decay sweep step` | `internal/consolidation/consolidation.go`, `internal/consolidation/consolidation_test.go` | New `edgeDecay()` step in Pipeline.Run. EdgeDecayed field in ConsolidationResult. Tests: decay applied, grace period respected, NULL excluded, rate=0 skipped. |

---

## 11. Tests requeridos

### config (unit)

- `TestGraphConfig_Defaults` — verificar que `Default()` produce los valores de D8.
- `TestGraphConfig_Validation_HebbianWindowNegative` — error.
- `TestGraphConfig_Validation_HebbianIncrementOutOfRange` — error para <0 y >1.
- `TestGraphConfig_Validation_HebbianInitialWeightOutOfRange` — error.
- `TestGraphConfig_Validation_EdgeDecayRateNegative` — error.
- `TestGraphConfig_Validation_ZeroValues` — HebbianWindow=0, EdgeDecayRate=0 son validos (toggle off).

### store (integration, SQLite in-memory)

- `TestStore_DecayRelationWeights_AfterGracePeriod` — relacion con last_traversed_at 60 dias atras, rate=0.02: weight decreases.
- `TestStore_DecayRelationWeights_WithinGracePeriod` — relacion con last_traversed_at 10 dias atras: weight unchanged.
- `TestStore_DecayRelationWeights_NullLastTraversed` — relacion con last_traversed_at IS NULL: weight unchanged.
- `TestStore_DecayRelationWeights_RateZero` — rate=0: returns 0, no changes.
- `TestStore_DecayRelationWeights_MultipleRelations` — batch de relaciones con distintas fechas, verificar que solo las elegibles decaen.
- `TestStore_DecayRelationWeights_WeightFloor` — weight ya cercano a 0, verificar que no va negativo.

### graph/tracker (unit)

- `TestTracker_Record_GeneratesPairs` — window con [A, B], record C: pares (A,C) y (B,C).
- `TestTracker_Record_SelfLoopGuard` — record(A) dos veces consecutivas: sin pares.
- `TestTracker_Record_ExcludesRules` — record con TypeRule: no registra.
- `TestTracker_Record_ExcludesSessionSummary` — record con TypeSessionSummary: no registra.
- `TestTracker_Record_CrossScopeIgnored` — record con ScopeProject seguido de ScopeGlobal: no genera pares.
- `TestTracker_Record_WindowDedup` — window [A,B,A,B,A], record C: pares con set {A,B}, no multiset.
- `TestTracker_Record_HebbianWindowZero` — window=0: Record es no-op.
- `TestTracker_Record_HebbianWindowOne` — window=1: no genera pares (solo un elemento).
- `TestTracker_Record_EmptyEntityIDs` — no genera eventos.
- `TestTracker_Record_SameEntityGuard` — dos memorias vinculadas a la misma entidad X: par (X,X) descartado.
- `TestTracker_Record_RingBufferOverflow` — window=3, record 5 memorias: solo las ultimas 3 en el buffer.

### graph/worker (unit + integration)

- `TestWorkerPool_EnqueueAndProcess` — evento encolado, worker lo procesa, relacion strengthened.
- `TestWorkerPool_CreateNewRelation` — evento entre entidades sin relacion: crea con HebbianInitialWeight.
- `TestWorkerPool_ChannelFull` — llenar el channel, verificar que Enqueue retorna false.
- `TestWorkerPool_DrainWithTimeout` — Drain con eventos pendientes completa en <200ms.
- `TestWorkerPool_DrainTimeout` — Drain con workers bloqueados supera timeout, loguea warning.
- `TestWorkerPool_SQLError` — simular error de DB, verificar que loguea y no paniquea.

### service (integration)

- `TestService_Get_TriggersTracker` — Get de una memoria llama tracker.Record.
- `TestService_Search_TriggersTracker` — Search result IDs encolados al tracker.
- `TestService_Start_InitializesWorkerPool` — Start crea y arranca el pool.
- `TestService_Get_RuleNotTracked` — Get de TypeRule no llama tracker.Record.

### consolidation (integration, SQLite in-memory)

- `TestPipeline_Run_IncludesEdgeDecay` — full cycle incluye edge decay step.
- `TestPipeline_EdgeDecay_Applied` — relacion vieja (60 dias) con rate=0.02: weight decayed.
- `TestPipeline_EdgeDecay_GracePeriodRespected` — relacion reciente (10 dias): not decayed.
- `TestPipeline_EdgeDecay_NullExcluded` — relacion sin last_traversed_at: not decayed.
- `TestPipeline_EdgeDecay_RateZeroDisabled` — rate=0: 0 decayed.
- `TestPipeline_EdgeDecay_100KRelations_Performance` — 100K relaciones: <500ms.

---

## 12. Criterios de aceptacion

1. **Hebbian strengthening funcional:** `service.Get(id_A)` seguido de `service.Get(id_B)` (ambas project scope, con entidades vinculadas) produce un `UpdateRelationWeight` o `CreateRelation` asincrono entre las entidades. Verificar con un sleep corto o mock del pool.
2. **Edge decay funcional:** `mneme consolidate` con relaciones cuyo `last_traversed_at` es > 30 dias atras y `EdgeDecayRate=0.02` reduce el weight. Verificar con `SELECT weight FROM relations WHERE ...`.
3. **Exclusion de rules/session_summary:** `service.Get` de una memoria tipo `rule` no genera eventos en el tracker. Verificar con un tracker instrumentado.
4. **Cross-scope guard:** Acceso a una memoria project + una memoria global no genera pares Hebbian. Verificar con test.
5. **make test pasa** con todos los tests nuevos; `golangci-lint run` con cero warnings.
6. **Toggle off:** `HebbianWindow=0` desactiva todo el tracking. `EdgeDecayRate=0` desactiva el decay. Verificar que ambos no causan errores ni side effects.

### Performance budgets

- Overhead del tracking en `service.Get`: **<1ms** por llamada (Record es O(window) con window=5, no-IO).
- Worker pool channel full: **no bloquea** el read path (select + default).
- Edge decay sweep de **100K relaciones**: <500ms en SQLite (single UPDATE).
- `Drain(200ms)`: no bloquea CLI mas de 200ms.

---

## 13. Open questions / pushbacks

### Q1. Tracker.Record necesita entity IDs — overhead de GetMemoryEntities

`tracker.Record` necesita los entity IDs vinculados a la memoria para generar pares a nivel de entidades. Esto requiere una llamada a `store.GetMemoryEntities()` dentro de `service.Get()`. Si la memoria no tiene entidades vinculadas (el caso comun hoy), la query retorna vacio rapidamente. Pero si el grafo crece con wikilinks (SPEC-W1), cada memoria podria tener 5-10 entidades vinculadas.

**Mitigacion:** La query `SELECT ... FROM entities JOIN memory_entities WHERE memory_id = ?` tiene un indice en `memory_entities(memory_id)` (PK), asi que es O(log n) + O(k) donde k es el numero de entidades vinculadas. Para k=10, <0.5ms.

**Decision:** Aceptar el overhead. El budget de <1ms por Get se mantiene con margenes.

### Q2. Search genera muchos Record calls

`service.Search` retorna hasta 50 resultados. Llamar `tracker.Record` 50 veces genera hasta 50*5=250 pares (con window=5). En la practica, los resultados de search no se "acceden" secuencialmente — el agente los ve todos al mismo tiempo.

**Decision:** Llamar `tracker.Record` **solo para los top-N resultados** (N=3 por default). Los primeros 3 resultados son los que el agente realmente leera. Esto limita el ruido y mantiene el budget de pares razonable.

**Alternativa no adoptada:** No trackear Search en absoluto. Descartada porque Search es la forma primaria de descubrir relaciones entre memorias.

### Q3. Relaciones bidireccionales

El tracker genera pares con orden canonico (min ID, max ID) y tipo `related_to`. Pero `FindRelation` busca con `source_id = ? AND target_id = ?` — es direccional. Si la relacion existente tiene `source_id=B, target_id=A` pero el tracker busca `source_id=A, target_id=B`, no la encontrara.

**Decision:** El worker intenta `FindRelation(src, tgt)` y si no encuentra, intenta `FindRelation(tgt, src)` antes de crear una nueva. Esto cubre ambas direcciones sin cambiar la estructura de relaciones.

---

## Dependencias

- **Hacia atras:** SPEC-005 (completada). Usa `UpdateRelationWeight`, `TouchRelation`, `last_traversed_at`, indices.
- **Hacia adelante:** SPEC-G3 (1-hop expansion) usara los pesos Hebbian-adjusted. SPEC-P1 (PPR) usara el grafo con pesos reales.
