# mneme -- Knowledge Graph

El grafo de conocimiento de mneme conecta memorias a traves de entidades y relaciones pesadas. Permite descubrir conexiones que la busqueda textual no encuentra: "que memorias estan relacionadas con el modulo de auth?" se resuelve siguiendo aristas del grafo, no buscando la palabra "auth".

Introducido en SPEC-005..009 (EPIC-2).

---

## Table of Contents

1. [Modelo del grafo](#modelo-del-grafo)
2. [Pesos por tipo](#pesos-por-tipo)
3. [Wikilinks](#wikilinks)
4. [Hebbian auto-strengthening](#hebbian-auto-strengthening)
5. [Edge decay](#edge-decay)
6. [Retrieval con grafo](#retrieval-con-grafo)
7. [Personalized PageRank (PPR)](#personalized-pagerank-ppr)
8. [Community detection](#community-detection)
9. [Synthesis (community summaries)](#synthesis-community-summaries)
10. [Context packing por comunidades](#context-packing-por-comunidades)
11. [mem_explore](#mem_explore)
12. [graph rebuild](#graph-rebuild)
13. [Comandos relacionados](#comandos-relacionados)
14. [Configuracion](#configuracion)

---

## Modelo del grafo

El grafo tiene dos tipos de nodos y un tipo de arista:

### Entities (nodos)

Conceptos nombrados que las memorias referencian. Cada entity es unica dentro de `(name, project)`.

| Kind | Descripcion | Ejemplo |
|------|-------------|---------|
| `module` | Paquete o modulo de codigo | `internal/store` |
| `service` | Servicio desplegado | `auth-service` |
| `library` | Dependencia externa | `mattn/go-sqlite3` |
| `concept` | Concepto o idea abstracta | `auth-model` |
| `person` | Persona o contributor | `juan` |
| `pattern` | Patron de diseno | `repository-pattern` |
| `file` | Path de archivo fuente | `internal/store/entity.go` |

### Relations (aristas)

Aristas dirigidas y pesadas entre entities. El peso esta normalizado a `[0.0, 1.0]`.

```
Entity A ──[type, weight]──> Entity B
```

Cada relacion tiene:
- `type`: uno de 8 tipos reconocidos
- `weight`: fuerza de la relacion en [0.0, 1.0]
- `last_traversed_at`: timestamp de ultima navegacion
- `metadata`: JSON opcional para atributos extra

### memory_entities (junction)

Tabla de union que conecta memorias con entidades:

```
Memory M ──[role: subject|object|mention]──> Entity E
```

Una memoria puede estar vinculada a multiples entidades, y una entidad a multiples memorias.

---

## Pesos por tipo

Cada `RelationType` tiene un peso default que refleja su importancia estructural para la navegacion del grafo:

| Tipo | Peso default | Cuando usarla |
|------|-------------|---------------|
| `depends_on` | **0.9** | A depende de B (dependencia fuerte, critica para entender el sistema) |
| `part_of` | **0.85** | A es un componente de B (relacion composicional) |
| `implements` | **0.8** | A implementa B (interface, contrato) |
| `uses` | **0.7** | A usa o llama a B (relacion de uso) |
| `conflicts_with` | **0.7** | A conflicta con B (incompatibilidad) |
| `supersedes` | **0.6** | A reemplaza a B (evolucion) |
| `related_to` | **0.5** | Relacion generica (co-mencion, tematica) |
| `references` | **0.4** | A referencia a B (wikilink, mencion debil) |

Se puede pasar un `weight` explicito al crear una relacion via `mem_relate`. Si no se pasa, se usa el default del tipo.

**Intuicion sobre los pesos:** Un path de 2 hops con pesos 0.9 * 0.9 = 0.81 es muy fuerte (dependencia transitiva). Un path de 2 hops con pesos 0.4 * 0.4 = 0.16 es debil (referencia indirecta). El producto penaliza naturalmente caminos largos con aristas debiles.

---

## Wikilinks

`[[topic_key]]` en el contenido de una memoria se parsean automaticamente al final de `mem_save` y `mem_update`. Cada wikilink resuelto crea una relacion `references` entre la memoria origen y la memoria target. Introducido en SPEC-011 (EPIC-3).

### Sintaxis soportada

| Forma | Topic | Anchor | Alias |
|-------|-------|--------|-------|
| `[[topic]]` | `topic` | - | - |
| `[[a/b/c]]` | `a/b/c` | - | - |
| `[[topic#section]]` | `topic` | `section` | - |
| `[[topic|Display]]` | `topic` | - | `Display` |
| `[[topic#sec|Lbl]]` | `topic` | `sec` | `Lbl` |

Solo el **topic** se usa para resolver la memoria target. El **anchor** se almacena en `relation.metadata` como `{"anchor": "section"}` para referencia futura. El **alias** es solo display, no se persiste.

### Comportamiento

- **Automatico y sincrono:** parser O(n) sobre lineas, procesamiento <25ms para 5 wikilinks tipicos.
- **Code blocks ignorados:** wikilinks dentro de bloques ` ``` ` o `~~~` no se parsean (CommonMark 4.5).
- **Inline code ignorado:** wikilinks dentro de backticks no se parsean.
- **Idempotente:** si la relacion ya existe, se llama `TouchRelation` (refresca `last_traversed_at`).
- **Self-loop guard:** `[[mismo-topic_key]]` de la memoria origen se ignora.
- **Append-only en updates:** wikilinks removidos en un `mem_update` NO borran relaciones existentes.
- **TypeSessionSummary excluido:** session summaries no activan el parser.

### Resolucion de scope

| Scope origen | Busca en | Fallback |
|--------------|----------|----------|
| `project` | projectStore (mismo proyecto) | globalStore (mismo proyecto) |
| `global` / `org` | globalStore (mismo proyecto) | ninguno |

Una memoria global NO puede crear relaciones hacia memorias project-scoped (invariante de cross-scope isolation, identico a Hebbian).

### Peso de la relacion

El peso de las relaciones creadas por wikilinks es `wikilink_relation_weight` (default **0.6**), superior al default de `references` (0.4 para rebuild inference) porque un wikilink explicito escrito por el agente es una senal mas fuerte que una inferencia heuristica.

### Configuracion

```toml
[graph]
wikilinks_enabled = true         # false = tratar [[...]] como texto plano
wikilink_relation_weight = 0.6   # [0.0, 1.0]
```

### Ejemplo

```
mem_save({
  "title": "Auth middleware setup",
  "content": "See [[architecture/auth-model]] for the design. Uses [[convention/error-codes]].",
  "topic_key": "impl/auth-middleware",
  "type": "decision"
})
```

Despues del save, `mem_explore("impl/auth-middleware")` retorna `architecture/auth-model` y `convention/error-codes` a distancia 1 con weight=0.6.

---

## Knowledge gaps: unresolved references

Cuando un wikilink `[[topic_key]]` no puede resolverse (la memoria target no existe aun), mneme persiste la referencia en la tabla `unresolved_references` en lugar de descartarla silenciosamente. Introducido en SPEC-012 (EPIC-3).

### Por que importa

El agente puede escribir `[[decision/retry-strategy]]` antes de que esa memoria exista. Sin tracking de gaps, este wikilink se perderia en silencio. Con `unresolved_references`, el grafo sabe que hay un gap y puede exponerlo via `mem_gaps` (SPEC-W3, proximo).

### Esquema

```
unresolved_references
├── id                  UUIDv7 PK
├── source_memory_id    FK → memories(id) ON DELETE CASCADE
├── target_topic_key    el topic_key que no pudo resolverse
├── project             slug del proyecto origen
├── mention_count       cuantas veces se ha visto este par (source, target)
├── first_seen_at       primera deteccion
└── last_seen_at        ultima deteccion
```

`mention_count` es el indicador de criticidad: un gap mencionado 10 veces es mas urgente de cerrar que uno mencionado 1 vez.

### Auto-resolve

Cuando se guarda una memoria nueva con `topic_key=X`, mneme busca automaticamente todos los `unresolved_references` cuyo `target_topic_key=X` y:

1. Carga la memoria origen de cada ref.
2. Aplica el cross-scope guard (global source → project target = skip).
3. Llama `createWikilinkRelation` (la misma logica que el resolve en vivo).
4. Elimina la fila de `unresolved_references`.

Es **best-effort**: si falla parcialmente, las refs no resueltas persisten y se intentan de nuevo la proxima vez que se guarde una memoria con el mismo topic_key. Es self-healing.

### Cascade cleanup

`ON DELETE CASCADE` en `source_memory_id`: si la memoria origen se **hard-delete** (expira de la consolidacion), sus gaps se limpian automaticamente. Un **soft-forget** no triggerea el cascade — la memoria sigue existiendo con decay_rate=1.0, y sus gaps siguen siendo validos.

### Comportamiento con updates

`mem_update` que cambia content puede registrar nuevos gaps (via `processWikilinks`). No triggerea auto-resolve porque `topic_key` no es parte de `UpdateRequest` — el auto-resolve solo ocurre cuando una nueva memoria con topic_key se guarda via `mem_save` / upsert.

---

## Hebbian auto-strengthening

"Cells that fire together, wire together."

Cuando el agente accede a memoria A y despues a memoria B en la misma ventana temporal, mneme refuerza automaticamente las aristas entre las entidades de A y las entidades de B.

### Como funciona

1. **`mem_get(A)`** o **`mem_search` top-3**: el servicio llama `recordHebbianAccess(A, entities_of_A)`
2. El **AccessTracker** mantiene un ring buffer de tamano `hebbian_window` (default: 5) con las memorias recientes
3. Para cada memoria previamente en el buffer, se generan **pares de co-acceso** entre entidades
4. Cada par se envia como `StrengtheningEvent` al **HebbianWorkerPool**
5. El worker (goroutine unica, async) aplica el cambio:
   - Si la relacion existe: `weight += hebbian_increment` (default: 0.05)
   - Si no existe: crea una nueva con `weight = hebbian_initial_weight` (default: 0.1)

### Ejemplo intuitivo

```
Sesion del agente:
  1. Busca "database connection"    → obtiene memoria sobre db.Open
  2. Busca "migration strategy"     → obtiene memoria sobre db/migrations
  3. Busca "FTS5 configuration"     → obtiene memoria sobre FTS5 setup

Resultado Hebbian:
  Entity(db.Open) ←→ Entity(migrations)     weight += 0.05
  Entity(db.Open) ←→ Entity(FTS5-setup)     weight += 0.05
  Entity(migrations) ←→ Entity(FTS5-setup)  weight += 0.05
```

Despues de varias sesiones donde estas 3 memorias se co-acceden, las aristas entre sus entidades se vuelven fuertes (>0.3, elegibles para expansion en busquedas).

### Guardas de seguridad

| Guardia | Que previene |
|---------|-------------|
| **D1: Cross-scope** | Pares entre project y global descartados (distintas DBs) |
| **D4: Self-loop** | Acceso consecutivo al mismo ID no genera pares |
| **D5: Noise types** | Tipos `rule` y `session_summary` excluidos |
| **Drop policy** | Si el buffer async esta lleno (1000 default), los eventos se dropean silenciosamente |
| **Same entity** | Pares donde source == target se ignoran (edge case 8.9) |

### El peso crece pero no sin limite

- Los pesos estan clamped a `[0.0, 1.0]`
- El increment de 0.05 por co-acceso es conservador: se necesitan ~6 co-accesos para ir de 0.1 (initial) a 0.4 (fuerte)
- El edge decay (0.02/dia despues de 30d sin traversar) previene que aristas antiguas se acumulen indefinidamente

---

## Edge decay

Las relaciones del grafo decaen si no se usan. Esto previene que el grafo se llene de aristas historicas irrelevantes.

### Mecanismo

Durante el ciclo de consolidacion (cada 6h por default), se evalua cada relacion:

```
excess_days = days_since_last_traversed - edge_decay_after_days
if excess_days > 0:
    new_weight = weight * exp(-edge_decay_rate * excess_days)
```

### Parametros

| Parametro | Default | Efecto |
|-----------|---------|--------|
| `edge_decay_rate` | 0.02/dia | Velocidad del decaimiento exponencial |
| `edge_decay_after_days` | 30 | Grace period antes de que empiece el decay |

### Ejemplo

Una relacion con weight=0.5 que no se traversa por 60 dias:
- Excess days: 60 - 30 = 30
- New weight: 0.5 * exp(-0.02 * 30) = 0.5 * 0.549 = 0.274
- Despues de 90 dias sin uso: 0.5 * exp(-0.02 * 60) = 0.151
- La relacion se vuelve demasiado debil para expansion (threshold 0.3) despues de ~45 dias de inactividad

### Notas

- Relaciones con `last_traversed_at = NULL` (nunca traversadas desde la migracion) son **excluidas** del decay
- Poner `edge_decay_rate = 0` en config desactiva el edge decay completamente
- Las relaciones creadas por Hebbian tienen `last_traversed_at` seteado al momento de creacion, por lo que si son elegibles para decay futuro

---

## Retrieval con grafo

### 1-hop expansion en mem_search (SPEC-007)

Cuando `expansion_enabled = true` (default) y `graph_mode = "1hop"`, `mem_search` fusiona 3 canales via RRF. (Para `graph_mode = "ppr"`, el tercer canal usa Personalized PageRank — ver [PPR](#personalized-pagerank-ppr).)

```
Query ──┬──> FTS5 BM25 ────────────> Channel A (weight 1.0)
        │
        ├──> Vector similarity ─────> Channel B (weight 0.8)
        │
        ├──> 1-hop graph expansion ─> Channel C (weight 0.6)
        │
        └──> RRF Fusion (k=60) ─────> Final ranking
```

**Proceso de expansion:**

1. Fusion preliminar 2-channel (FTS5 + vector) para identificar top-K seeds (default K=10)
2. Para cada seed:
   - Obtener entidades vinculadas
   - Obtener relaciones fuertes (`weight > expansion_threshold`, default 0.3)
   - Mapear entidades vecinas a memory IDs
   - Score: `max(rel_weight * 1/seed_rank)` -- max en lugar de sum para evitar inflacion de hub nodes
3. Los resultados del grafo entran como tercer canal RRF con peso 0.6

**Parametros de expansion:**

| Parametro | Default | Descripcion |
|-----------|---------|-------------|
| `expansion_enabled` | `true` | Activa/desactiva expansion |
| `expansion_threshold` | `0.3` | Peso minimo para seguir una relacion |
| `expansion_fan_out_cap` | `50` | Max relaciones por entidad |
| `expansion_seed_top_k` | `10` | Seeds para expansion |

**Per-request toggle:** El parametro `include_graph` en `mem_search` permite deshabilitar la expansion para una busqueda especifica:

```json
mem_search({
  "query": "auth middleware",
  "include_graph": false
})
```

### Hebbian tracking post-search

Los top-3 resultados de cada `mem_search` se registran en el AccessTracker para Hebbian auto-strengthening. Esto significa que memorias frecuentemente co-recuperadas por las mismas queries refuerzan sus conexiones automaticamente.

---

## Personalized PageRank (PPR)

Personalized PageRank es un algoritmo de ranking sobre grafos que propaga importancia desde un conjunto de "seed nodes" a traves de la topologia del grafo. mneme lo usa como tercer canal de retrieval (ademas de BM25 y vector similarity). Introducido en SPEC-017..018 (EPIC-4).

### Algoritmo

mneme implementa PPR via power iteration sobre la matriz de adyacencia del grafo:

1. Construir la matriz de adyacencia en memoria desde `entities` + `relations` (solo relaciones con `weight > threshold`)
2. Seed vector: los entity IDs correspondientes a los top-K resultados de la fusion BM25+vector
3. Iterar `max_iterations` veces (default: 20) con damping factor `alpha` (default: 0.85)
4. Convergir cuando `||v_new - v_old|| < epsilon` (default: 1e-6)
5. Mapear entity scores de vuelta a memory IDs via `memory_entities`

### Modos de grafo

El parametro `graph_mode` controla que algoritmo se usa para la expansion:

| Mode | Algoritmo | Cuando usarlo |
|------|-----------|---------------|
| `ppr` | Personalized PageRank | Default. Mejor ranking para grafos grandes (>100 entities) |
| `1hop` | 1-hop BFS expansion | Rapido, predecible. Mejor para grafos chicos |
| `off` | Sin expansion | Solo BM25 + vector. Para debugging o DBs sin grafo |

### RRF de 3 canales (con PPR)

```
Query ──┬──> FTS5 BM25 ────────────> Channel A (weight 1.0)
        │
        ├──> Vector similarity ─────> Channel B (weight 0.8)
        │
        ├──> PPR ranking ──────────> Channel C (weight 0.6)
        │
        └──> RRF Fusion (k=60) ─────> Final ranking
```

En modo `ppr`, Channel C usa PPR en lugar de 1-hop BFS. El RRF weight (0.6) es el mismo para ambos modos.

### Cache

La matriz de adyacencia se construye una vez por llamada a `mem_search`/`mem_context`. No se cachea entre llamadas porque el grafo puede cambiar entre invocaciones (Hebbian, nuevas relaciones). El costo tipico de construccion es <15ms para grafos de 5K entities.

---

## Community detection

mneme usa el algoritmo Louvain para detectar comunidades de memorias densamente conectadas en el grafo. Las comunidades agrupan memorias que comparten muchas entidades y relaciones fuertes, formando "clusters tematicos" naturales. Introducido en SPEC-019..020 (EPIC-5).

### Algoritmo Louvain

1. **Input:** Grafo de entidades conectadas por relaciones pesadas
2. **Fase 1 (local moves):** Cada entidad se mueve a la comunidad vecina que maximiza la ganancia de modularidad, iterando hasta convergencia
3. **Fase 2 (aggregation):** Las comunidades se colapsan en super-nodos y se repite Fase 1 sobre el grafo reducido
4. **Output:** Asignacion de cada entidad a una comunidad, con un hash de membership para deteccion de cambios

### Persistencia

Las comunidades se persisten en dos tablas (migracion 010):

```
communities
├── id              UUIDv7 PK
├── project         slug
├── label           titulo generado (actualizado por synthesis)
├── membership_hash SHA256 del sorted set de entity IDs
├── modularity      score de modularidad (0.0-1.0)
├── member_count    numero de entidades
├── created_at
├── updated_at
└── deleted_at      soft-delete

community_members
├── community_id    FK → communities(id)
├── entity_id       FK → entities(id) ON DELETE CASCADE
└── PRIMARY KEY (community_id, entity_id)
```

### Deteccion de cambios

Cada comunidad tiene un `membership_hash` que es el SHA256 de los entity IDs ordenados. En cada ciclo de consolidacion:

- **Hash igual:** Comunidad estable, no se modifica
- **Hash diferente:** Comunidad cambio, se actualiza con los nuevos miembros
- **Comunidad nueva:** Se crea con los nuevos miembros
- **Comunidad desaparecida:** Se soft-deleta

### Pipeline

La deteccion de comunidades corre como parte del pipeline de consolidacion, despues del edge decay y antes de la generacion de synthesis:

```
sweep → edgeDecay → detectCommunities → generateSyntheses → hardDelete → dedup → budget
```

### Output en CLI

```
Consolidation complete: 3 swept, 1 hard-deleted, 0 duplicates merged, 2 evicted,
5 edges decayed, 8 communities detected (2 new, 1 deleted),
synthesis: 2 created, 1 updated, 0 deleted, 5 skipped
```

---

## Synthesis (community summaries)

El tipo `synthesis` es un tipo especial de memoria que resume automaticamente el contenido de una comunidad. Cada comunidad activa tiene exactamente un synthesis, generado de forma deterministica (sin LLM). Introducido en SPEC-021 (EPIC-5).

### Generacion deterministica

El generador toma los miembros de una comunidad y produce:

1. **Titulo:** De los top-3 miembros por importancia, truncado a 80 chars
2. **Content (4 secciones):**
   - Overview: resumen cuantitativo (N memorias, tipos, importancia promedio)
   - Top members: las 3 memorias mas importantes con titulo y extracto
   - All members: tabla con ID, titulo, tipo, importancia (max 50 filas)
   - Aggregate metadata: estadisticas de tipos, archivos referenciados
3. **Wikilinks:** `[[topic_key]]` para cada miembro con topic_key, creando relaciones `references` automaticamente

### topic_key

Formato: `synthesis/community-{uuid7}` donde uuid7 es el ID de la comunidad. Esto permite upserts idempotentes.

### Lifecycle

| Situacion | Accion |
|-----------|--------|
| Comunidad nueva | Crear synthesis |
| Comunidad estable, mismo contenido | Skip (no-op) |
| Comunidad estable, contenido cambio | Update synthesis |
| Comunidad eliminada | Forget synthesis (decay_rate = 1.0) |

### Propiedades especiales

| Propiedad | Valor | Razon |
|-----------|-------|-------|
| `importance` | 0.85 | Alto para aparecer en context |
| `decay_rate` | 0.0 | Inmune a decay (como rules) |
| Hebbian | Excluido | Previene loops de auto-refuerzo |
| Seeds (Louvain) | Excluido | Previene synthesis-of-synthesis |
| Wikilinks | Procesado | Crea relaciones `references` a miembros |

---

## Context packing por comunidades

Cuando hay comunidades detectadas, `mem_context` puede organizar las memorias por clusters tematicos en lugar de un ranking plano. Esto produce contextos mas coherentes y navegables para el agente. Introducido en SPEC-022 (EPIC-5).

### Modos de packing

| Mode | Comportamiento |
|------|---------------|
| `auto` (default) | Detecta comunidades; si hay > 0, usa community packing; si no, flat |
| `communities` | Fuerza community packing (falla silenciosamente a flat si no hay comunidades) |
| `flat` | Ranking plano pre-SPEC-022 (backward compatible) |

### Algoritmo de 4 fases

```
Phase 1: Community ranking
  ├── Focus provided? → PPR seeded by focus entities → rank communities by PPR score
  └── No focus?       → rank by member_count DESC, modularity DESC

Phase 2: Cluster overviews (dedicated budget: 1500 tokens)
  └── Pack synthesis summaries of top communities

Phase 3: Top cluster deep-dive (max 10 members)
  └── Pack individual memories from the highest-ranked community by importance

Phase 4: Fill remaining budget
  └── Pack remaining memories from all communities using flat scoring
  └── Dedup: exclude memories already packed in Phases 2 and 3
```

### Sections en el output

| # | Section | Presente en |
|---|---------|-------------|
| 1 | Last Session | flat + community |
| 2 | Active Rules | flat + community |
| 3 | Cluster Overviews | community only |
| 4 | Top Cluster Detail | community only |
| 5 | Other Memories / Loaded Memories | both (renamed) |

### Configuracion

```toml
[context]
context_packing_mode = "auto"       # auto | communities | flat
cluster_overviews_budget = 1500     # tokens for Phase 2
top_cluster_max_members = 10        # max memories in Phase 3
```

### Fallback silencioso

Cualquier error durante el community packing (ListCommunities, PPR, etc.) se logea como warning y el sistema cae a flat mode. `mem_context` nunca falla por culpa del packing.

---

## mem_explore

`mem_explore` es una herramienta de exploracion interactiva del grafo. Desde una memoria seed, hace un BFS priorizado y retorna las memorias conectadas con sus distancias y pesos acumulados.

### Cuando usarlo

- **Debugging:** "que esta conectado a esta memoria?"
- **Discovery:** "que otros modulos dependen del servicio de auth?"
- **Context building:** "dame todo lo relacionado con el pipeline de consolidation"
- **Graph health:** "esta memoria tiene conexiones? El grafo esta bien formado?"

### Seed resolution

El seed puede ser:
- **UUID completo:** `019de100-abcd-7fff-8000-000000000001`
- **Prefijo hex (8+ chars):** `019de100`
- **topic_key:** `architecture/auth-model`

### Algoritmo BFS priorizado

1. Resolver seed -> cargar entidades vinculadas
2. Encolar vecinos a distancia 1 en un max-heap por `accumulatedWeight`
3. Loop:
   - Pop del max-heap
   - Si ya visitado con peso mayor, skip
   - Token budget check (skip si excede)
   - Registrar nodo
   - Si hay depth restante, expandir sus vecinos con `accumulatedWeight = parent_weight * edge_weight`
4. Ordenar resultado: `(distance ASC, accumulated_weight DESC)`

### Parametros

| Parametro | Default | Rango | Descripcion |
|-----------|---------|-------|-------------|
| `depth` | 2 | 0-5 | Hops maximos desde seed |
| `budget` | 4000 | >0 | Token budget para memorias retornadas |
| `threshold` | 0.3 | 0.0-1.0 | Peso minimo para seguir una relacion |

### CLI output: arbol ASCII

```bash
$ mneme explore "architecture/auth-model" --depth 2

architecture/auth-model [seed]
|-- JWT token rotation (depends_on, w=0.90, 245 tok)
|   |-- Key management policy (uses, w=0.63, 180 tok)
|   \-- Session invalidation flow (related_to, w=0.45, 320 tok)
|-- OAuth2 provider config (implements, w=0.80, 156 tok)
\-- Auth middleware setup (part_of, w=0.85, 210 tok)

Total: 5 memories | 1111 tokens | 2 levels
```

### JSON output

```bash
$ mneme explore "architecture/auth-model" --json
```

```json
{
  "seed_id": "019de100-...",
  "seed_title": "Architecture: Auth model",
  "nodes": [
    {
      "memory_id": "019de101-...",
      "parent_memory_id": "019de100-...",
      "title": "JWT token rotation",
      "topic_key": "security/jwt-rotation",
      "type": "decision",
      "distance": 1,
      "accumulated_weight": 0.9,
      "relation_type": "depends_on",
      "token_estimate": 245
    }
  ],
  "total_nodes": 5,
  "tokens_used": 1111,
  "max_depth_reached": 2
}
```

### Notas

- Las relaciones traversadas durante la exploracion actualizan `last_traversed_at` async (previene edge decay)
- El seed no se incluye en la lista de nodos retornados
- Si el seed no tiene entidades vinculadas, retorna una lista vacia
- El max-heap garantiza que los caminos mas fuertes se exploran primero, incluso con budget limitado

---

## graph rebuild

`mneme graph rebuild` es un comando de backfill que extrae entidades de memorias existentes y crea relaciones de co-mencion. Es el punto de entrada para proyectos legacy con muchas memorias pero sin grafo.

### Cuando correrlo

- **Proyecto nuevo con memorias existentes:** despues de migrar con `mneme init`
- **Despues de importar memorias:** `mneme sync import` trae memorias sin grafo
- **Periodicamente:** para incorporar nuevas memorias al grafo (es idempotente)
- **Debugging:** `--dry-run` para ver que se extraeria sin modificar nada

### 4 heuristicas de extraccion

| # | Heuristica | Que detecta | Entity kind |
|---|-----------|-------------|-------------|
| H1 | **topic_key** | Cada memoria con topic_key genera un concept entity | `concept` |
| H2 | **file paths** | Paths reconocidos en content (e.g. `internal/store/entity.go`) | `file` |
| H3 | **code symbols** | Declaraciones `func`/`type`/`struct` en code blocks | `concept` |
| H4 | **wikilinks** | `[[topic_key]]` references en content | `concept` |

### Generacion de relaciones

Memorias que comparten >= K entidades (default K=2) reciben una relacion `related_to` con:

```
weight = min(0.5, shared_count * 0.1)
```

Esto significa:
- 2 entidades compartidas: weight = 0.2
- 3 entidades: weight = 0.3
- 5+ entidades: weight = 0.5 (cap)

### Uso

```bash
# Preview (dry run)
mneme graph rebuild --dry-run

# Rebuild normal
mneme graph rebuild

# Force: borra related_to existentes y regenera
mneme graph rebuild --force

# Ajustar threshold
mneme graph rebuild --min-shared 3
```

### Flags

| Flag | Short | Default | Descripcion |
|------|-------|---------|-------------|
| `--scope` | `-s` | `project` | project, global, o all |
| `--min-shared` | `-k` | `2` | Minimo entidades compartidas para crear relacion |
| `--max-relations` | | `50` | Cap de relaciones por memoria |
| `--batch-size` | `-b` | `500` | Memorias por transaccion |
| `--force` | `-f` | false | Borrar related_to existentes antes de regenerar |
| `--dry-run` | `-n` | false | Preview sin escribir |

### Output

```
Starting graph rebuild for project "wirvii-mneme"...
  Scope:       project
  Min shared:  2
  Force:       false
  Batch size:  500

Phase 1: Entity extraction
  [100%] (142/142)

Phase 2: Relation generation
  [100%] (142/142)

Rebuild complete in 1.234s:
  Memories scanned:        142
  Entities extracted:       89
  New entities:             67
  Existing entities:        22
  Memory-entity links:     234
  Relations created:        45
```

### Idempotencia

- Entidades existentes se reutilizan (match por `(name, project)`)
- Links memory-entity existentes se saltan
- Relaciones `related_to` existentes se saltan (a menos que `--force`)
- Relaciones de otros tipos (`depends_on`, `implements`, etc.) **nunca** se tocan

---

## graph cleanup-orphan-relations

`mneme graph cleanup-orphan-relations` detecta y opcionalmente borra **relations huerfanas**: aquellas cuyas entities no tienen ningun row en `memory_entities` y por lo tanto no son alcanzables desde `mem_explore`. Introducido en SPEC-031.

### Por que existe

Antes de SPEC-031, `mem_relate` no resolvia `topic_key` a memoria y nunca llamaba `LinkMemoryEntity`. Resultado: relations creadas via `mem_relate` quedaban desconectadas del puente memory_entities y eran invisibles para `mem_explore`. El comando permite limpiar el residual.

### Flujo recomendado de recuperacion

Para un proyecto victima del bug (relations existentes pero `mem_explore` retorna 0 hops):

```bash
# 1) Ver que se borraria (dry-run, default)
mneme graph cleanup-orphan-relations

# 2) Borrar relations huerfanas
mneme graph cleanup-orphan-relations --apply --yes

# 3) Reconstruir grafo desde wikilinks/heuristicas
mneme graph rebuild --force

# 4) Verificar
mneme explore <topic_key>
```

### Resolucion de mem_relate post-fix

Despues de SPEC-031, `mem_relate` resuelve cada endpoint en este orden:

1. UUID full o prefix de 8+ hex de memoria existente → memoria
2. Si `*_kind` esta omitido (default `concept`): `topic_key` exacto en project store o global store → memoria
3. Entity con `name == string` en project (reusar)
4. Crear entity nueva con `kind` (default `concept`)

Cuando la resolucion termina en una memoria, se llama `LinkMemoryEntity(memory.ID, proxy_entity.ID, "relate")` automaticamente para que la relation sea alcanzable por BFS de `mem_explore`. Pasar un `*_kind` explicito distinto de `concept` (e.g. `"service"`, `"library"`) preserva la semantica legacy entity-only.

### Flags

| Flag | Short | Default | Descripcion |
|------|-------|---------|-------------|
| `--scope` | `-s` | `project` | project, global, o all |
| `--apply` | | false | Default es dry-run; usar `--apply` para borrar |
| `--also-delete-entities` | | false | Borra entities que quedan totalmente sin referencias |
| `--output` | `-o` | `text` | text o json |
| `--yes` | `-y` | false | Confirma borrado destructivo (requerido con `--apply`) |

### Output

```
Orphan relations found: 21
Relations deleted:      21
Entities deleted:       0

Examples:
  - architecture/backend-modular-hexagonal --[depends_on]--> architecture/event-system-detail
  - architecture/backend-modular-hexagonal --[references]--> architecture/bounded-contexts
  ...
```

### Idempotencia

Re-correr el comando despues de un `--apply` exitoso reporta 0 candidatos. Es seguro ejecutar repetidamente.

---

## Comandos relacionados

| Comando | Descripcion |
|---------|-------------|
| `mneme explore <seed>` | BFS desde seed (arbol ASCII o JSON) |
| `mneme graph rebuild` | Backfill grafo desde memorias existentes |
| `mneme graph cleanup-orphan-relations` | Limpiar relations huerfanas (SPEC-031) |
| `mneme gaps` | Listar knowledge gaps (wikilinks no resueltos) |
| `mneme search --no-graph` | Busqueda sin expansion de grafo |
| `mneme consolidate` | Run pipeline incluyendo community detection + synthesis |
| `mem_relate` (MCP) | Crear/actualizar relacion entre entidades |
| `mem_explore` (MCP) | Exploracion del grafo desde MCP |
| `mem_gaps` (MCP) | Listar knowledge gaps |

Referencia completa de todos los endpoints: [API.md](API.md).

---

## Configuracion

La referencia completa de todos los parametros del grafo con tipos, rangos y variables de entorno
esta en [CONFIG.md](CONFIG.md#graph).

Resumen rapido de los parametros disponibles en `~/.mneme/config.toml`:

```toml
[graph]
# Graph mode (MNEME_GRAPH_MODE)
graph_mode = "ppr"            # ppr | 1hop | off

# Hebbian auto-strengthening (MNEME_GRAPH_HEBBIAN_*)
hebbian_window = 5            # Ring buffer size (0 = disabled)
hebbian_increment = 0.05      # Weight delta per co-access
hebbian_initial_weight = 0.1  # Weight for new Hebbian relations
hebbian_buffer_size = 1000    # Async channel capacity

# Edge decay (consolidation) (MNEME_GRAPH_EDGE_*)
edge_decay_rate = 0.02        # Daily exponential decay rate [0.0, 1.0]
edge_decay_after_days = 30    # Grace period before decay starts

# Expansion in mem_search (MNEME_GRAPH_EXPANSION_*)
expansion_enabled = true      # Toggle graph expansion
expansion_threshold = 0.3     # Min weight to follow
expansion_fan_out_cap = 50    # Max relations per entity
expansion_seed_top_k = 10     # Seeds for expansion

# mem_explore defaults (MNEME_GRAPH_EXPLORE_*)
explore_max_nodes = 200       # Hard cap on BFS nodes
explore_default_depth = 2     # Default depth when not specified
explore_default_budget = 4000 # Default token budget

# graph rebuild (MNEME_GRAPH_REBUILD_*)
rebuild_min_shared = 2        # K: min shared entities for co-mention
rebuild_max_relations = 50    # Cap per memory

# wikilinks (SPEC-011) (MNEME_GRAPH_WIKILINKS_*)
wikilinks_enabled = true          # Parse [[topic_key]] in mem_save/mem_update
wikilink_relation_weight = 0.6    # Weight for wikilink-created relations [0.0, 1.0]

# Synthesis (SPEC-021) (MNEME_GRAPH_SYNTHESIS_*)
synthesis_enabled = true      # Generate community summaries during consolidation
synthesis_max_members = 50    # Max members in synthesis content table
synthesis_top_n = 3           # Top members for title generation

[context]
# Context packing (SPEC-022) (MNEME_CONTEXT_*)
context_packing_mode = "auto"       # auto | communities | flat
cluster_overviews_budget = 1500     # Tokens for cluster overview phase
top_cluster_max_members = 10        # Max memories in top cluster deep-dive
```

Para inspeccionar la configuracion activa con proveniencia (default/file/env):

```bash
mneme config show graph
mneme config show context
```
