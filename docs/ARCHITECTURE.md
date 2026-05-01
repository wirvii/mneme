# mneme -- Architecture & Design Documentation

Documentacion viva de la arquitectura de mneme. Explica que se construyo, como funciona, por que se tomaron las decisiones, y como encajan las piezas.

---

## Table of Contents

1. [Que es mneme](#que-es-mneme)
2. [Arquitectura de alto nivel](#arquitectura-de-alto-nivel)
3. [Paquetes implementados](#paquetes-implementados)
4. [Graph Layer (SPEC-005..009)](#graph-layer-spec-005009)
5. [Rules System (SPEC-001..004)](#rules-system-spec-001004)
6. [Retrieval Pipeline](#retrieval-pipeline)
7. [Consolidation Pipeline](#consolidation-pipeline)
8. [Decisiones de diseno](#decisiones-de-diseno)

---

## Que es mneme

mneme es un sistema de memoria persistente para agentes AI de coding. Un solo binario Go que expone un servidor MCP (Model Context Protocol) sobre stdio, permitiendo que cualquier agente AI compatible (Claude Code, OpenCode, Gemini CLI, Codex, Cursor, Windsurf) guarde y recupere conocimiento entre sesiones.

### El problema que resuelve

1. **CLAUDE.md como campo de batalla** -- Los archivos de instrucciones mezclan configuracion del agente con conocimiento del proyecto. Cuando un lider de equipo define reglas y el desarrollador define las suyas, colisionan.

2. **Amnesia entre sesiones** -- Cada vez que se abre una nueva sesion, el agente no sabe nada. Patrones descubiertos, decisiones de arquitectura, bugs resueltos -- todo se pierde.

3. **Islas de conocimiento** -- Lo que se aprende en un proyecto no existe en otro. Soluciones reutilizables, patrones propios, librerias custom -- no se comparten.

### La solucion

Una base de datos SQLite local con busqueda full-text (FTS5) y un grafo de conocimiento pesado, expuestos via MCP. El agente llama herramientas como `mem_save`, `mem_search`, `mem_context`, `mem_explore` para guardar y recuperar conocimiento estructurado. Las reglas se inyectan automaticamente en el contexto y se aplican JIT via hooks.

---

## Arquitectura de alto nivel

```
┌─────────────────────────────────────────────────────────────┐
│                       mneme binary                          │
│                                                             │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐      │
│  │   CLI   │  │   MCP   │  │  HTTP   │  │  Hooks  │      │
│  │ (cobra) │  │ (stdio) │  │ (:7437) │  │ (agent) │      │
│  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘      │
│       └─────────────┼───────────┼─────────────┘            │
│                     ▼                                       │
│            ┌──────────────┐                                 │
│            │   Service    │  business logic, scoring,       │
│            │    Layer     │  Hebbian tracking, rules        │
│            └──────┬───────┘                                 │
│                   │                                         │
│       ┌───────────┼───────────┐                             │
│       ▼           ▼           ▼                             │
│  ┌────────┐ ┌──────────┐ ┌────────┐                        │
│  │ Store  │ │ Scoring  │ │ Graph  │                        │
│  │ (CRUD, │ │ (BM25,   │ │ (ring  │                        │
│  │  FTS5) │ │ RRF,     │ │ buffer,│                        │
│  │        │ │ decay)   │ │ worker)│                        │
│  └───┬────┘ └──────────┘ └────────┘                        │
│      ▼                                                      │
│  ┌────────┐                                                 │
│  │ SQLite │  global.db + projects/<slug>.db                 │
│  │ + FTS5 │  scopes never leak between projects             │
│  └────────┘                                                 │
└─────────────────────────────────────────────────────────────┘
```

### Principio de diseno: Clean Architecture

Las dependencias fluyen hacia adentro. El paquete `model` es el centro -- no depende de nada externo. `store` depende de `model` y `db`. `service` depende de `store` y `model`. `mcp` y `cli` dependen de `service`.

```
cli, mcp, http, hooks --> service --> store --> db
                                  --> model (leaf, zero deps)
                                  --> scoring --> model
                                  --> graph --> store, model, config
                                  --> rules --> model
                                  --> project
```

Ningun paquete interno importa a otro que este "arriba" en la cadena.

### Las cuatro frontends

- **MCP** (`internal/mcp`, primary) -- JSON-RPC 2.0 sobre stdio, ProtocolVersion `2024-11-05`. Superficie: 23 tools (13 `mem_*`, 4 `backlog_*`, 6 `spec_*`). `handleMessage()` se expone separado de `Run()` para testing sin I/O loops.
- **HTTP** (`internal/http`, `mneme serve --addr :7437`) -- stdlib `net/http`, graceful shutdown 10s, 8 endpoints bajo `/v1/`. Actualmente le faltan endpoints SDD y algunos mem tools (`mem_checkpoint`, `mem_timeline`, `mem_suggest_topic_key`, `mem_explore`).
- **CLI** (`internal/cli`, Cobra) -- 23+ comandos top-level. Notable: `sync export|import|status` para backup/restore; `mneme init` migra proyectos legacy al SDD engine; `mneme install <agent>` escribe agent profiles; `mneme rule add|list|test` para gestion de reglas; `mneme explore` para exploracion del grafo; `mneme graph rebuild` para backfill.
- **Hooks** (`internal/cli/hook.go`) -- 4 hook handlers (`session-start`, `session-end`, `pre-tool-use`, `enforce-delegation`). Los hooks no son un frontend separado; son subcommands de CLI invocados por el sistema de hooks del agente.

### Persistencia

Dos bases de datos SQLite por host:
- `~/.mneme/global.db` -- memorias global + org scope
- `~/.mneme/projects/<slug>.db` -- memorias project-scoped (slug derivado del git remote)

Scopes (`global` / `org` / `project`) nunca filtran entre proyectos. Migraciones embebidas via `embed.FS`.

---

## Paquetes implementados

### `internal/model/` -- Tipos de dominio
**Deps:** Solo stdlib

El paquete hoja. Define todos los tipos de dominio: `Memory`, `MemoryType` (10 tipos incluyendo `rule`), `Scope` (3 scopes), `Severity` (info/warn/block), `Entity`, `Relation`, `RelationType` (8 tipos), `EntityKind` (7 tipos), `DefaultRelationWeights`, request/response structs (SaveRequest, ExploreRequest, RebuildRequest, etc.), scoring defaults, y sentinel errors. Cero dependencias externas.

### `internal/project/` -- Deteccion de proyecto
**Deps:** Solo stdlib

Detecta el proyecto actual parseando el git remote origin. Soporta SSH, HTTPS, SSH con puerto, y GitLab anidado. Fallback al nombre del directorio cuando no hay remote.

### `internal/config/` -- Configuracion
**Deps:** go-toml/v2

Carga configuracion TOML con tres niveles de precedencia: defaults -> archivo TOML -> env vars. Incluye `GraphConfig` con todos los knobs de Hebbian, edge decay, expansion, y rebuild.

### `internal/db/` -- Base de datos
**Deps:** go-sqlite3

Wrapper sobre `*sql.DB` que configura SQLite con WAL mode, foreign keys, busy timeout 5s. Ejecuta migraciones embebidas automaticamente. Incluye `OpenReadOnly` para hooks de alta performance.

### `internal/store/` -- Acceso a datos
**Deps:** db, model

Repository pattern. CRUD completo de memorias con soporte para upsert via `topic_key`. FTS5 search. Entity CRUD, relation CRUD con `FindRelationBidirectional`, `GetStrongRelations`, `BatchTouchRelations`, `GetMemoryEntities`, `GetEntityMemoryIDs`, `GetMemoryMetadata`, `GetByIDPrefix`, `GetByTopicKey`. Vector search (cosine similarity).

### `internal/scoring/` -- Scoring de importancia
**Deps:** model

Importancia inicial (type-based defaults con override), decay exponencial (Ebbinghaus-inspired), relevancia final (BM25 x importance x recency), y RRF fusion con constante k=60.

### `internal/graph/` -- Hebbian subsystem (SPEC-006)
**Deps:** store, model, config

`AccessTracker`: ring buffer de window size configurable que genera pares de co-acceso. Excluye reglas y session_summaries (D5), cross-scope (D1), self-loops (D4). `HebbianWorkerPool`: single-worker goroutine que procesa `StrengtheningEvent`s asynchronamente. Aplica delta a relaciones existentes o crea nuevas con `HebbianInitialWeight`. Drop policy cuando el buffer esta lleno.

### `internal/rules/` -- Matching engine (SPEC-003)
**Deps:** model

`Match()` evalua una lista de reglas contra un tool name + file path. Soporta path globs, tool selectors, combined selectors (`tool:Edit+internal/**`), negations (`!docs/**`), y wildcard (`**`). `ValidatePattern()` para validacion sintatica.

### `internal/service/` -- Logica de negocio
**Deps:** store, model, scoring, config, graph, rules, embed, sync

Orquesta operaciones: validacion, scoring, upsert, access tracking, context assembly con token budgeting y rules injection (SPEC-002), search con RRF 3-channel (SPEC-007), `Explore()` BFS priorizado (SPEC-008), `RebuildGraph()` (SPEC-009), `ListRules()`, Hebbian tracking en `recordHebbianAccess()`, `UpdateRelationWeight()`.

### `internal/mcp/` -- Servidor MCP
**Deps:** service, model

Servidor JSON-RPC 2.0 sobre stdio. 23 tools con JSON schemas completos. `handleMessage()` permite testing sin I/O loop.

### `internal/cli/` -- Comandos CLI
**Deps:** service, config, db, store, project, mcp, rules, cobra

23+ comandos cobra incluyendo `rule add|list|test`, `explore`, `graph rebuild`, `hook pre-tool-use|session-start|session-end|enforce-delegation`, `backlog`, `spec`, `init`, `install`, `sync`, `serve`, `mcp`.

### Supporting packages

- `embed/` -- TF-IDF baseline embedder
- `consolidation/` -- background decay/dedup/budget sweeps + edge decay
- `sync/` -- JSONL.gz git-shareable export/import
- `install/` -- agent profile installer (writes settings.json)
- `tui/` -- Bubble Tea interface
- `upgrade/` -- release update checker
- `export/` -- markdown export

---

## Graph Layer (SPEC-005..009)

### Modelo de datos

El grafo de conocimiento conecta **entities** (nodos) con **relations** (aristas dirigidas y pesadas).

**Entities** (`internal/model/entity.go`):
- 7 kinds: `module`, `service`, `library`, `concept`, `person`, `pattern`, `file`
- Unicas dentro de `(name, project)`
- Se crean automaticamente al llamar `mem_relate` o durante `graph rebuild`

**Relations** (`internal/model/entity.go`):
- 8 tipos con pesos default diferenciados (SPEC-005):

| Tipo | Peso default | Semantica |
|------|-------------|-----------|
| `depends_on` | 0.9 | A depende de B |
| `part_of` | 0.85 | A es componente de B |
| `implements` | 0.8 | A implementa B |
| `uses` | 0.7 | A usa/llama a B |
| `conflicts_with` | 0.7 | A conflicta con B |
| `supersedes` | 0.6 | A reemplaza a B |
| `related_to` | 0.5 | Relacion generica (bidireccional) |
| `references` | 0.4 | A referencia a B (wikilinks) |

- Rango de peso: `[0.0, 1.0]`
- `last_traversed_at`: timestamp de ultima navegacion (para decay)
- Se puede pasar `weight` explicitamente al crear via `mem_relate`

### Hebbian auto-strengthening (SPEC-006)

"Cells that fire together, wire together" -- cuando el agente accede a memoria A y luego a memoria B en la misma ventana de sesion, la arista entre sus entidades se refuerza automaticamente.

**Mecanismo:**

```
mem_get(A)  -->  AccessTracker.Record(A, entities_A)
                   |
                   +--> Para cada memoria en el ring buffer:
                          Para cada par (entity_A_i, entity_prev_j):
                            Enqueue StrengtheningEvent
                                |
                                v
mem_search(B) -->  HebbianWorkerPool (goroutine unica)
                   |
                   +--> Existe relacion? -> UpdateRelationWeight(+delta)
                   +--> No existe?       -> CreateRelation(initial_weight)
```

**Parametros (config.toml `[graph]`):**

| Parametro | Default | Funcion |
|-----------|---------|---------|
| `hebbian_window` | 5 | Tamano del ring buffer (slots de memoria reciente) |
| `hebbian_increment` | 0.05 | Delta aplicado a relaciones existentes |
| `hebbian_initial_weight` | 0.1 | Peso al crear una relacion nueva por co-acceso |
| `hebbian_buffer_size` | 1000 | Capacidad del canal async. Eventos dropeados cuando lleno |

**Guardas de seguridad:**
- D1: Pares cross-scope (project vs global) descartados
- D4: Self-loop guard -- acceso consecutivo al mismo ID no genera pares
- D5: Tipos `rule` y `session_summary` excluidos (generan ruido)

### Edge decay (SPEC-006)

Relaciones no traversadas decaen durante el ciclo de consolidacion:

```
if days_since_last_traversed > edge_decay_after_days:
    weight *= exp(-edge_decay_rate * excess_days)
```

| Parametro | Default |
|-----------|---------|
| `edge_decay_rate` | 0.02/dia |
| `edge_decay_after_days` | 30 |

Relaciones con `last_traversed_at = NULL` (nunca traversadas desde la migracion) son excluidas del decay para no penalizar aristas historicas.

### Retrieval con grafo: 1-hop expansion (SPEC-007)

El `mem_search` fusiona 3 canales via RRF (ver [Retrieval Pipeline](#retrieval-pipeline)):

1. FTS5 BM25 (peso 1.0)
2. Vector similarity (peso 0.8)
3. Graph expansion (peso 0.6)

La expansion funciona asi:
1. Fusion preliminar 2-channel (FTS5 + vector) para identificar top-K seeds
2. Para cada seed: obtener entidades -> obtener relaciones fuertes (weight > threshold) -> mapear a memory IDs
3. Score: `max(rel_weight * 1/seed_rank)` -- max para evitar inflacion de hub nodes
4. RRF fusion final 3-channel

**Parametros:**

| Parametro | Default |
|-----------|---------|
| `expansion_enabled` | true |
| `expansion_threshold` | 0.3 |
| `expansion_fan_out_cap` | 50 |
| `expansion_seed_top_k` | 10 |

El flag `include_graph` en `mem_search` permite deshabilitar la expansion por request.

### mem_explore: BFS prioritizado (SPEC-008)

`mem_explore` realiza un BFS priorizado (priority queue por `accumulatedWeight` descendente) desde una memoria seed.

**Seed resolution:** UUID completo, prefijo hex (8+ chars), o `topic_key`.

**Algoritmo:**
1. Resolver seed -> obtener entidades -> encolar vecinos a distancia 1
2. BFS loop: pop del max-heap, token budget check, expandir vecinos si hay depth restante
3. `accumulatedWeight = parent_weight * relation_weight` (producto a lo largo del path)
4. Nodos ordenados por `(distance ASC, accumulated_weight DESC)`
5. Relaciones traversadas se tocan asynchronamente (`BatchTouchRelations`)

**Parametros:** `depth` (0-5, default 2), `budget` (tokens, default 4000), `threshold` (min weight, default 0.3).

**Output CLI:** arbol ASCII con tipo de relacion, peso acumulado, y tokens estimados.

### graph rebuild: backfill (SPEC-009)

`mneme graph rebuild` extrae entidades de memorias existentes y crea relaciones de co-mencion.

**4 heuristicas de extraccion:**

| Heuristica | Que detecta | Ejemplo |
|------------|-------------|---------|
| H1: topic_key | Cada topic_key -> entidad concept | `architecture/auth-model` |
| H2: file paths | Paths reconocidos en content | `internal/store/entity.go` |
| H3: code symbols | func/type/struct en code blocks | `func NewMemoryStore` |
| H4: wikilinks | `[[topic_key]]` references | `[[architecture/auth-model]]` |

**Co-mencion:** memorias que comparten >= K entidades (default K=2) reciben una relacion `related_to` con `weight = min(0.5, shared_count * 0.1)`.

**Idempotente:** seguro re-ejecutar. `--force` borra relaciones `related_to` existentes antes de regenerar; otros tipos (`depends_on`, `implements`, etc.) nunca se tocan.

**Flags:** `--min-shared` (K), `--max-relations` (cap por memoria), `--batch-size`, `--force`, `--dry-run`.

---

## Rules System (SPEC-001..004)

### Modelo

Las reglas son memorias de tipo `rule` con dos campos adicionales (SPEC-001):

- **`applies_to`**: lista de patrones que determinan cuando aplica la regla
- **`severity`**: nivel de enforcement (`info` / `warn` / `block`)

Las reglas son inmunes al decay (`decay_rate = 0`) -- permanecen activas hasta que se revocan via `mem_forget` o `mem_update`.

### Sintaxis applies_to

| Patron | Match |
|--------|-------|
| `**` | Cualquier tool, cualquier path (wildcard global) |
| `tool:Edit` | Tool especifico (case-sensitive) |
| `internal/**/*.go` | Path glob con doublestar |
| `tool:Edit+internal/**` | AND: tool Y path deben matchear |
| `!docs/**` | Negacion: veta la regla cuando este patron matchea |
| `["**", "!docs/**"]` | Combinacion: todo excepto docs/ |

### Inyeccion en contexto (SPEC-002)

`mem_context` siempre inyecta las reglas del scope activo con un presupuesto de tokens separado. Las reglas aparecen en la seccion "Active Rules" del output, ANTES de las memorias generales, para que el LLM las encuentre primero.

```
<!-- mneme:context:start -->
# mneme -- Session Context

## Active Rules (N rules, ~M tokens)
### [BLOCK] No vendor edits
Never edit vendor/ files.
_Applies to: vendor/**_
---

## Loaded Memories (X of Y)
...
<!-- mneme:context:end -->
```

### Hook pre-tool-use (SPEC-003)

El hook `mneme hook pre-tool-use` se ejecuta antes de cada `Edit`, `Write`, y `MultiEdit`:

1. Lee JSON de stdin (tool_name + tool_input.file_path)
2. Abre las DBs en read-only mode (no migraciones, no WAL writer)
3. Matchea reglas contra tool + path
4. Emite markdown a stdout con reglas que aplican
5. Exit 2 si alguna regla tiene severity `block`

**Performance target:** <50ms. Query unica con `LIMIT 200`, matching in-memory.

**Fail open:** cualquier error interno resulta en exit 0 (allow).

### CLI de gestion (SPEC-004)

```bash
# Crear regla
mneme rule add --title "No vendor edits" --content "..." \
  --applies-to "vendor/**" --severity block

# Listar reglas (tabla coloreada por severity)
mneme rule list
mneme rule list --severity block --scope global

# Testear contra invocacion simulada
mneme rule test --tool Edit --path internal/store/memory.go
```

---

## Retrieval Pipeline

### 3-Channel RRF (v0.2.0, SPEC-007)

```
Query ──┬──> FTS5 BM25 ──────────> Rank A (w=1.0)
        │
        ├──> Vector similarity ───> Rank B (w=0.8)
        │
        ├──> 1-hop graph expand ──> Rank C (w=0.6)
        │
        └──> RRF Fusion (k=60) ──> Re-rank (decay + importance) ──> Return
```

**RRF formula:**

```
RRF_score(d) = SUM over all rank lists R:
    weight_R / (k + rank_R(d))

k = 60 (standard RRF constant)
```

Los pesos reflejan que FTS5 es la senal mas confiable (term matches exactos), vectors son fuertes para similitud semantica, y graph expansion agrega contexto topologico pero es mas ruidoso.

**Hebbian tracking post-search:** los top-3 resultados de cada busqueda se registran para Hebbian auto-strengthening, reforzando las relaciones entre memorias frecuentemente co-recuperadas.

### Context assembly (`mem_context`)

1. Cargar ultimo session summary del proyecto
2. Cargar todas las reglas activas (presupuesto separado, inyectadas primero)
3. Cargar memorias activas ordenadas por `importance * recency_factor`
4. Incluir memorias globales con importance > 0.7
5. Si `focus` provisto: boost FTS5 a memorias que matcheen
6. Empacar en token budget, highest scored first
7. Architecture memories reciben 1.5x weight (priorizadas)

---

## Consolidation Pipeline

Corre automaticamente cuando `mneme mcp` arranca y cada 6h (configurable):

1. **SWEEP:** Calcular effective_importance, soft-delete si < 0.05
2. **HARD DELETE:** Borrar memorias soft-deleted > 30 dias
3. **DEDUP:** Detectar duplicados por titulo + BM25 overlap, merge
4. **BUDGET:** Si total > budget, evict las de menor score
5. **EDGE DECAY** (nuevo, SPEC-006): Aplicar decay a relaciones no traversadas en > 30 dias

---

## Decisiones de diseno

### D001: SQLite con CGO obligatorio
**Decision:** Usar `mattn/go-sqlite3` con CGO en lugar de alternativas pure-Go.
**Razon:** FTS5 (full-text search) es critico para mneme.
**Consecuencia:** El build requiere `CGO_ENABLED=1` y un compilador C.

### D002: UUIDv7 para identificadores
**Decision:** Usar UUIDv7 (RFC 9562) para todos los IDs.
**Razon:** Time-sortable, globalmente unicos sin coordinacion.

### D003: No ORM
**Decision:** SQL crudo con `database/sql`.

### D004: Tests contra SQLite real
**Decision:** No mockear la base de datos. Tests usan SQLite in-memory (`:memory:`).

### D005: internal/model sin dependencias externas
**Decision:** El paquete `model` no importa nada fuera de la stdlib.

### D006: Build tag -tags fts5
**Decision:** Todos los builds y tests requieren `-tags fts5`.

### D007: handleMessage() para testing del MCP server
**Decision:** El server MCP expone `handleMessage([]byte)` para tests unitarios directos.

### D008: Fire-and-forget en access tracking
**Decision:** `service.Get()` incrementa `access_count` despues de retornar.

### D009: SetMaxOpenConns(1) en tests
**Decision:** Los tests de store usan max 1 conexion para evitar deadlock con `:memory:`.

### D010: Rows cerrados antes de queries secundarias
**Decision:** Las rows del query principal se cierran antes de queries de files.

### D011: Pesos diferenciados por tipo de relacion (SPEC-005)
**Decision:** Cada RelationType tiene un peso default en `[0.0, 1.0]` que refleja su importancia estructural. `depends_on=0.9` porque las dependencias son criticas; `references=0.4` porque son senales debiles.
**Consecuencia:** La expansion y el BFS siguen caminos mas fuertes primero.

### D012: Hebbian single-worker goroutine (SPEC-006)
**Decision:** Un solo worker procesa StrengtheningEvents. Canal buffered con drop policy.
**Razon:** Simplicidad sobre throughput. A la escala de mneme (~1000 memorias/proyecto), un worker es suficiente. El path de lectura nunca se bloquea.

### D013: Graph expansion como tercer canal RRF (SPEC-007)
**Decision:** Graph expansion es un canal RRF con peso 0.6, no un filter ni un boost post-hoc.
**Razon:** RRF permite que memorias topologicamente conectadas aparezcan incluso cuando FTS5 y vector no las encuentran, mientras que el peso bajo (0.6 vs 1.0 para FTS5) evita que el grafo domine los resultados.

### D014: BFS priorizado con producto de pesos (SPEC-008)
**Decision:** `mem_explore` usa un max-heap por `accumulatedWeight = parent * edge`, no suma.
**Razon:** El producto penaliza naturalmente caminos largos con aristas debiles. Un path de 3 hops con pesos 0.9*0.8*0.7=0.504 es significativo; con 0.3*0.3*0.3=0.027 no.

### D015: Backfill idempotente con 4 heuristicas (SPEC-009)
**Decision:** `graph rebuild` extrae entidades heuristicamente y crea relaciones `related_to` de co-mencion.
**Razon:** Proyectos legacy con muchas memorias preexistentes no tienen grafo. El rebuild bootstraps el grafo sin requerir que el usuario cree relaciones manualmente. `--force` solo toca `related_to`; las relaciones explicitas son inmutables.

### D016: Rules inmunes a decay (SPEC-001)
**Decision:** Memorias de tipo `rule` tienen `decay_rate = 0`.
**Razon:** Las reglas son constraints activos. Si decayeran, el agente las ignoraria gradualmente -- el opuesto de lo deseado. Se revocan explicitamente con `mem_forget`.

### D017: Pre-tool-use hook fail open (SPEC-003)
**Decision:** Cualquier error en el hook `pre-tool-use` resulta en exit 0 (allow).
**Razon:** Un hook roto nunca debe bloquear al agente de trabajar. Es preferible que pase una accion sin evaluar reglas a que el agente quede paralizado.
