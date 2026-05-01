# mneme -- Knowledge Graph

El grafo de conocimiento de mneme conecta memorias a traves de entidades y relaciones pesadas. Permite descubrir conexiones que la busqueda textual no encuentra: "que memorias estan relacionadas con el modulo de auth?" se resuelve siguiendo aristas del grafo, no buscando la palabra "auth".

Introducido en SPEC-005..009 (EPIC-2).

---

## Table of Contents

1. [Modelo del grafo](#modelo-del-grafo)
2. [Pesos por tipo](#pesos-por-tipo)
3. [Hebbian auto-strengthening](#hebbian-auto-strengthening)
4. [Edge decay](#edge-decay)
5. [Retrieval con grafo](#retrieval-con-grafo)
6. [mem_explore](#mem_explore)
7. [graph rebuild](#graph-rebuild)
8. [Comandos relacionados](#comandos-relacionados)
9. [Configuracion](#configuracion)

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

Cuando `expansion_enabled = true` (default), `mem_search` fusiona 3 canales via RRF:

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

## Comandos relacionados

| Comando | Descripcion |
|---------|-------------|
| `mneme explore <seed>` | BFS desde seed (arbol ASCII o JSON) |
| `mneme graph rebuild` | Backfill grafo desde memorias existentes |
| `mneme search --include-graph=false` | Busqueda sin expansion de grafo |
| `mem_relate` (MCP) | Crear/actualizar relacion entre entidades |
| `mem_explore` (MCP) | Exploracion del grafo desde MCP |

---

## Configuracion

Todos los parametros del grafo viven en `[graph]` en `~/.mneme/config.toml`:

```toml
[graph]
# Hebbian auto-strengthening
hebbian_window = 5            # Ring buffer size (0 = disabled)
hebbian_increment = 0.05      # Weight delta per co-access
hebbian_initial_weight = 0.1  # Weight for new Hebbian relations
hebbian_buffer_size = 1000    # Async channel capacity

# Edge decay (consolidation)
edge_decay_rate = 0.02        # Daily exponential decay rate
edge_decay_after_days = 30    # Grace period before decay starts

# 1-hop expansion in mem_search
expansion_enabled = true      # Toggle graph expansion
expansion_threshold = 0.3     # Min weight to follow
expansion_fan_out_cap = 50    # Max relations per entity
expansion_seed_top_k = 10     # Seeds for expansion

# mem_explore defaults
explore_max_nodes = 200       # Hard cap on BFS nodes
explore_default_depth = 2     # Default depth when not specified
explore_default_budget = 4000 # Default token budget

# graph rebuild
rebuild_min_shared = 2        # K: min shared entities for co-mention
rebuild_max_relations = 50    # Cap per memory
```
