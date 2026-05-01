# SPEC-009 — mneme graph rebuild: Backfill del grafo de memorias preexistentes

| Campo         | Valor                                                          |
|---------------|----------------------------------------------------------------|
| **ID**        | SPEC-009                                                       |
| **Epic**      | EPIC-2 — Grafo con peso + 1-hop expansion                     |
| **Backlog**   | BL-029                                                         |
| **Estado**    | speccing -> specced                                            |
| **Owner**     | architect                                                      |
| **Fecha**     | 2026-05-01                                                     |
| **Deps**      | SPEC-005 (completada), SPEC-006 (completada), SPEC-007 (completada), SPEC-008 (completada) — weighted relations, Hebbian, GetStrongRelations, 1-hop expansion, mem_explore |
| **Memorias**  | `roadmap/v2-master-plan`, `architecture/memory-model`, `spec/SPEC-005-weighted-relations-design`, `spec/SPEC-006-hebbian-design`, `spec/SPEC-007-graph-search-design`, `spec/SPEC-008-mem-explore-design` |

---

## 1. Contexto y motivacion

### El problema

Las memorias creadas antes de EPIC-2 (y las creadas despues sin `mem_relate` explicito) no tienen entradas en `memory_entities` ni `relations`. El grafo de conocimiento solo se puebla de dos formas:

1. **Explicita:** Un agente llama `mem_relate` (service/graph.go:27-111) para crear una relacion tipada entre entidades nombradas.
2. **Hebbian:** La AccessTracker (graph/tracker.go:76-152) genera pares co_accessed cuando memorias con entities linkados se acceden en la misma ventana.

Ninguna de las dos opera retroactivamente. Una base de datos con 500 memorias pero sin `mem_relate` historico tiene un grafo vacio. `mem_search --graph`, `mem_explore`, y todo el EPIC-2 producen cero resultados de expansion.

### Que resuelve SPEC-009

`mneme graph rebuild` es un comando CLI que:

1. **Extrae entities** del contenido de cada memoria usando heuristicas (topic_key, file paths, code symbols, wikilinks).
2. **Crea links** `memory_entities` para cada entidad extraida.
3. **Genera relations** `related_to` entre memorias que comparten >= K entities, con peso proporcional al overlap.

Es la pieza de bootstrap que hace que el grafo exista para proyectos existentes. Es la ultima spec de EPIC-2 antes de docs + release.

### Diferencia con `embed backfill`

`embed backfill` (cli/embed.go:29-86) genera embeddings faltantes para search vectorial. `graph rebuild` genera entities + relations faltantes para graph search. Ambos son idempotentes, ambos operan sobre memorias activas, y ambos siguen el mismo patron CLI. La diferencia: embed backfill tiene un algoritmo trivial (TF-IDF), graph rebuild tiene extraccion heuristica + join SQL para relations.

---

## 2. Decisiones de diseno

### D1. Extraccion de entities: 4 heuristicas concretas

**Decision:** Extraer entities del contenido de cada memoria usando 4 heuristicas en orden de prioridad:

#### H1. topic_key como entity (siempre)

Si la memoria tiene `topic_key` no vacio, se crea un entity con `name=topic_key`, `kind=KindConcept`. Esto es el link mas fuerte: cada memoria con topic_key tiene al menos 1 entity.

#### H2. File paths

Regex: `` `([a-zA-Z0-9_\-./]+\.[a-z]{1,10})` `` (backtick-enclosed, o sin backticks si matchea patron de path como `internal/store/entity.go`).

Regex completo:

```
(?:^|\s|`)(((?:internal|cmd|pkg|apps|lib|src|docs)/)?[a-zA-Z0-9_\-]+(?:/[a-zA-Z0-9_\-]+)*\.[a-z]{1,10})(?:`|\s|$|[,;:)])
```

Crear entity con `name=path`, `kind=KindFile`. Solo paths que luzcan como codigo fuente (extensiones: go, ts, tsx, js, jsx, py, rs, sql, md, yaml, yml, toml, json, sh).

#### H3. Code symbols en code blocks

Regex para contenido entre triple backticks:

```
(?:func|type|struct|interface|const|var|package|class|def|fn)\s+([A-Z][a-zA-Z0-9_]+|[a-z][a-zA-Z0-9_]+)
```

Crear entity con `name=symbol`, `kind=KindModule`. Solo nombres de 3+ caracteres que empiecen con letra. Dedup por nombre.

#### H4. Wikilinks `[[topic_key]]`

Regex: `\[\[([^\]]+)\]\]`

Crear entity con `name=topic_key_referenciado`, `kind=KindConcept`, `role=mention`. La **resolucion** del wikilink (buscar si la memoria referenciada existe y crear relacion explicita) es SPEC-W1. Aqui solo se extrae la mention como entity para que el grafo pueda expandir.

**Justificacion:**

- H1 es trivial y siempre correcta. Cada topic_key es un concepto unico.
- H2 y H3 son las heuristicas que producen entities de mayor valor: file paths conectan memorias que hablan del mismo archivo, y code symbols conectan memorias que hablan del mismo tipo/funcion.
- H4 prepara el terreno para SPEC-W1 sin implementar resolucion.
- No se incluyen heuristicas de NLP/NER — son caras, fragiles, y no aportan valor marginal suficiente sobre paths+symbols.

### D2. Threshold K=2 entities compartidas para generar relacion

**Decision:** Dos memorias generan una relacion `related_to` si comparten >= 2 entities.

**Justificacion:**

- K=1 es demasiado permisivo: dos memorias que ambas mencionan `internal/model/memory.go` estan probablemente relacionadas, pero una unica entity compartida puede ser coincidencia (e.g. ambas mencionan `config.go` pero hablan de temas distintos).
- K=2 exige que dos conceptos se superpongan, lo cual es una senal fuerte de relacion tematica.
- K=3 seria demasiado restrictivo para memorias cortas que tipicamente mencionan 2-3 entities.
- **Configurable** via `--min-shared` flag (CLI) y `GraphConfig.RebuildMinShared` (config). Default 2.

### D3. Weight formula: `min(0.5, K * 0.1)`

**Decision:** El peso de la relacion generada es `min(0.5, sharedCount * 0.1)`.

**Justificacion:**

- Con K=2 (threshold minimo): weight = 0.2. Es mayor que HebbianInitialWeight (0.1, config.go:54), lo cual es correcto: un overlap de 2 entities es evidencia mas fuerte que un co-acceso casual.
- Con K=5 (overlap fuerte): weight = 0.5 = `DefaultRelationWeights[RelRelatedTo]` (model/entity.go:14). Es el maximo para `related_to` — no queremos que backfill cree edges mas fuertes que los explicitos.
- El cap de 0.5 previene que memorias con muchas entities compartidas (e.g. dos decision memories sobre el mismo modulo con 10 paths en comun) tengan weight > que una relacion explicita `depends_on` (0.9).
- La formula es lineal y simple. No necesita lookup tables ni calibracion.

### D4. Cap 50 relations por memoria

**Decision:** Cada memoria genera como maximo 50 relations. Cuando el numero de pares con overlap >= K supera 50, se eligen los top 50 por overlap count descendente (desempate por weight descendente del par).

**Justificacion:**

- 50 es consistente con `ExpansionFanOutCap` (config.go:87) — el mismo cap que usa 1-hop expansion en search.
- Una memoria con >50 vecinos en el grafo es un hub. Los hubs degradan la calidad del BFS (SPEC-008 D4) y del PPR futuro (SPEC-P1). Limitar a 50 mantiene la densidad controlada.
- Seleccionar los de mayor overlap garantiza que las relaciones mas significativas se preservan.

### D5. Idempotencia sin --force

**Decision:** Sin `--force`, el rebuild es no-op para pares que ya tienen relacion `related_to`:

1. **Entities:** `FindOrCreateEntity` (store/entity.go:91-112) ya es idempotent por PK (name, project).
2. **Memory-entity links:** `LinkMemoryEntity` (store/entity.go:308-323) usa `INSERT OR IGNORE` por PK (memory_id, entity_id).
3. **Relations:** Antes de crear, `FindRelationBidirectional(src, tgt, RelRelatedTo)` (store/entity.go:480-494). Si existe, skip.

**Justificacion:**

- El patron idempotente ya existe en todo el store layer. Reusarlo es la opcion natural.
- No se necesita un campo especial `source=rebuild` en metadata — la idempotencia se logra por la unicidad natural de (source, target, type).

### D6. --force semantica: SOLO relations `related_to`

**Decision:** `--force` borra SOLO relaciones de tipo `related_to` y las recrea. Lista taxativa de lo que --force NO toca:

- `depends_on` — Explicita, creada via `mem_relate`.
- `implements` — Explicita.
- `supersedes` — Explicita.
- `part_of` — Explicita.
- `uses` — Explicita.
- `conflicts_with` — Explicita.
- `references` — SPEC-W1 (wikilinks).

`--force` ejecuta:

```sql
DELETE FROM relations WHERE type = 'related_to'
  AND source_id IN (SELECT id FROM entities WHERE project IS ?)
```

Luego re-ejecuta el rebuild completo. El delete se limita a entities del proyecto actual (o sin proyecto para global).

**Justificacion:**

- Las relaciones explicitas fueron creadas intencionalmente por el agente via `mem_relate`. Borrarlas seria destructivo.
- `related_to` es el unico tipo que Hebbian y graph rebuild producen automaticamente. Es seguro regenerarlas.
- Limitar el DELETE al proyecto evita afectar relaciones de otro scope.

### D7. Performance plan: SQL JOIN sobre memory_entities

**Decision:** Las relations se generan via un SQL JOIN directo sobre `memory_entities` en lugar de iterar en Go memoria por memoria.

**Pseudocodigo SQL (generacion de pares candidatos):**

```sql
-- Paso 1: Para cada par de memorias que comparten >= K entities,
-- calcular el count de entities compartidas.
SELECT me1.memory_id AS mem1,
       me2.memory_id AS mem2,
       COUNT(*)      AS shared_count
FROM memory_entities me1
JOIN memory_entities me2
  ON me1.entity_id = me2.entity_id
 AND me1.memory_id < me2.memory_id   -- evitar duplicados y self-join
WHERE me1.memory_id IN (SELECT id FROM memories WHERE deleted_at IS NULL AND project IS ?)
GROUP BY me1.memory_id, me2.memory_id
HAVING COUNT(*) >= ?                  -- K threshold
ORDER BY shared_count DESC
```

Este query usa `idx_memory_entities_entity` (migration 008) para el JOIN y el PK `(memory_id, entity_id)` para el GROUP BY. Estimacion: ~200ms para 5K memorias con 20K memory_entities rows.

**Justificacion:**

- Un JOIN SQL es O(E * log E) donde E = memory_entities rows. Es dramaticamente mas rapido que O(M^2) en Go (iterar todas las memorias y comparar entities).
- `idx_memory_entities_entity` (migration 008, SPEC-007) ya existe y hace el JOIN eficiente.
- El HAVING clause filtra en SQL, evitando materializar pares con overlap < K en Go.

### D8. Scope handling: project, global, all

**Decision:** El rebuild opera sobre un scope a la vez:

| Flag | Store | Project filter | Descripcion |
|------|-------|---------------|-------------|
| `--scope project` (default) | projectStore | `project IS ?` (detected slug) | Rebuild solo memorias del proyecto actual |
| `--scope global` | globalStore | `project IS NULL` | Rebuild solo memorias globales |
| `--scope all` | projectStore + globalStore | Ambos queries | Rebuild ambos scopes secuencialmente |

**Justificacion:**

- Consistente con `embed backfill` que procesa project y global secuencialmente (service/memory.go:632-660).
- Cross-scope relations son invalidas (SPEC-006 D1: "Cross-scope relations ignored"). Cada scope se procesa independientemente.

### D9. Atomic batch insert: transaction con rollback

**Decision:** Las inserciones de entities, memory_entities links, y relations de un batch se envuelven en una transaccion SQLite. Si falla mid-way, rollback.

**Implementacion:** El rebuild procesa memorias en batches de N (default 500, configurable con `--batch-size`). Cada batch:

1. BEGIN TRANSACTION
2. Para cada memoria en el batch: extract entities -> FindOrCreateEntity -> LinkMemoryEntity
3. COMMIT
4. Generar pares candidatos via SQL JOIN (sobre todo lo acumulado hasta ahora)
5. BEGIN TRANSACTION
6. Para cada par candidato: FindRelationBidirectional -> CreateRelation si no existe
7. COMMIT

Si alguna transaccion falla, se hace rollback del batch actual. Los batches anteriores ya estan committed y no se pierden. El proximo re-run del comando retomara donde quedo (idempotencia).

**Justificacion:**

- SQLite tiene un limite practico de ~10K operations por transaccion antes de que el WAL journal crezca significativamente. 500 memorias * ~5 entities = ~2500 inserts por transaccion, bien dentro del limite.
- El rollback per-batch (no per-memoria) balancea atomicidad con performance.

### D10. Dry-run output: mismas stats sin escribir

**Decision:** `--dry-run` ejecuta toda la logica de extraccion y pair generation pero NO escribe a la base de datos. Imprime un resumen identico al modo normal:

```
Dry run — no changes written.

Scan results:
  Memories scanned:      500
  Entities extracted:    1240
  New entities:           380
  Existing entities:      860
  Memory-entity links:   1240
  New links:              620
  Existing links:         620
  Relation candidates:    890
  New relations:          450
  Existing relations:     440
  Skipped (cap 50):        12
```

**Implementacion:** Un flag booleano `dryRun` en la funcion de rebuild. Cuando true:
- `FindOrCreateEntity` se reemplaza por `GetEntityByName` (read-only): si existe, count as "existing"; si no, count as "new". No crea.
- `LinkMemoryEntity` se reemplaza por un SELECT check: count existing vs new.
- `CreateRelation` se reemplaza por `FindRelationBidirectional` check: count existing vs new.

**Justificacion:**

- Patron identico a `git status --dry-run`, `rsync --dry-run`. El usuario puede evaluar el impacto antes de ejecutar.
- Es particularmente importante para `--force` donde el rebuild borra relaciones existentes.

---

## 3. Algoritmo de entity extraction

### extractEntities(m *model.Memory) []extractedEntity

```go
type extractedEntity struct {
    Name string
    Kind model.EntityKind
    Role string // "subject" for topic_key, "mention" for others
}

func extractEntities(m *model.Memory) []extractedEntity {
    seen := map[string]bool{}
    var result []extractedEntity

    add := func(name string, kind model.EntityKind, role string) {
        if seen[name] || len(name) < 3 {
            return
        }
        seen[name] = true
        result = append(result, extractedEntity{Name: name, Kind: kind, Role: role})
    }

    // H1: topic_key
    if m.TopicKey != "" {
        add(m.TopicKey, model.KindConcept, "subject")
    }

    text := m.Title + "\n" + m.Content

    // H2: file paths
    for _, path := range extractFilePaths(text) {
        add(path, model.KindFile, "mention")
    }

    // H3: code symbols in code blocks
    for _, sym := range extractCodeSymbols(text) {
        add(sym, model.KindModule, "mention")
    }

    // H4: wikilinks [[topic_key]]
    for _, ref := range extractWikilinks(text) {
        add(ref, model.KindConcept, "mention")
    }

    return result
}
```

### Regex definitions

```go
var (
    // H2: File paths — matches paths like internal/store/entity.go
    reFilePath = regexp.MustCompile(
        `(?:^|[\s` + "`" + `])` +
        `((?:internal|cmd|pkg|apps|lib|src|docs|config|test|tests|scripts)/` +
        `[a-zA-Z0-9_\-]+(?:/[a-zA-Z0-9_\-]+)*` +
        `\.(?:go|ts|tsx|js|jsx|py|rs|sql|md|yaml|yml|toml|json|sh))` +
        `(?:[\s` + "`" + `,:;)\]]|$)`,
    )

    // H3: Code symbols — function/type/struct/class declarations
    reCodeSymbol = regexp.MustCompile(
        `(?:func|type|struct|interface|const|var|package|class|def|fn)\s+` +
        `([A-Za-z][A-Za-z0-9_]{2,})`,
    )

    // H4: Wikilinks — [[topic_key]]
    reWikilink = regexp.MustCompile(`\[\[([^\]\[]{3,})\]\]`)
)
```

### Entity dedup

Entities are deduplicated by `name` within each memory (the `seen` map). Across memories, `FindOrCreateEntity` (store/entity.go:91-112) ensures uniqueness by the `idx_entities_name_project` UNIQUE index (002_knowledge_graph.sql:13).

---

## 4. Contratos

### 4.1. Model types

New type in `internal/model/rebuild.go` (NEW file):

```go
// RebuildRequest specifies parameters for the graph rebuild operation.
type RebuildRequest struct {
    // Project is the project slug to rebuild. Empty uses the service's default.
    Project string

    // Scope restricts which stores to process: "project" (default), "global", "all".
    Scope string

    // MinShared is the minimum number of shared entities required to create a
    // relation between two memories. Default: 2.
    MinShared int

    // MaxRelationsPerMemory caps the number of relations created per memory.
    // Default: 50.
    MaxRelationsPerMemory int

    // BatchSize is the number of memories processed per transaction.
    // Default: 500.
    BatchSize int

    // Force deletes existing related_to relations before rebuild.
    Force bool

    // DryRun performs extraction and pair generation without writing.
    DryRun bool

    // ProgressFn is called after each batch with current progress.
    ProgressFn func(phase string, current, total int)
}

// RebuildResult summarises the outcome of a graph rebuild.
type RebuildResult struct {
    // MemoriesScanned is the total number of memories processed.
    MemoriesScanned int

    // EntitiesExtracted is the total number of entity extractions attempted.
    EntitiesExtracted int

    // EntitiesCreated is the number of new entities inserted.
    EntitiesCreated int

    // EntitiesExisting is the number of entities that already existed.
    EntitiesExisting int

    // LinksCreated is the number of new memory_entity rows inserted.
    LinksCreated int

    // LinksExisting is the number of memory_entity links that already existed.
    LinksExisting int

    // RelationsCreated is the number of new relations inserted.
    RelationsCreated int

    // RelationsExisting is the number of relations that already existed (skipped).
    RelationsExisting int

    // RelationsDeleted is the number of relations deleted by --force (0 without --force).
    RelationsDeleted int

    // RelationsSkippedCap is the number of relations skipped due to per-memory cap.
    RelationsSkippedCap int
}
```

### 4.2. Service layer — `internal/service/rebuild.go` (NEW file)

```go
// RebuildGraph extracts entities from existing memories, links them, and creates
// co-occurrence relations between memories sharing >= MinShared entities.
//
// The rebuild is idempotent: existing entities and links are skipped, existing
// relations are not duplicated. With Force=true, only related_to relations are
// deleted and regenerated; explicit relations are preserved.
//
// DryRun=true performs the full analysis without writing to the database.
func (svc *MemoryService) RebuildGraph(ctx context.Context, req model.RebuildRequest) (*model.RebuildResult, error)
```

### 4.3. Store methods needed (existing)

All store methods required by SPEC-009 already exist:

| Method | File:Line | Purpose |
|--------|-----------|---------|
| `FindOrCreateEntity` | `store/entity.go:91` | Idempotent entity creation |
| `LinkMemoryEntity` | `store/entity.go:308` | INSERT OR IGNORE memory-entity link |
| `FindRelationBidirectional` | `store/entity.go:480` | Check if relation exists in either direction |
| `CreateRelation` | `store/entity.go:176` | Insert new relation |
| `List` | `store/memory.go:373` | List memories with filters |

### New store method needed

```go
// ListMemoriesWithoutEntities returns active memories that have no entry in
// memory_entities. Used by graph rebuild to find memories needing entity extraction.
func (s *MemoryStore) ListMemoriesWithoutEntities(ctx context.Context, project string, limit int) ([]*model.Memory, error)
```

Query pattern identical to `ListMemoriesWithoutEmbedding` (store/embedding.go:227-276):

```sql
SELECT m.id, m.type, m.scope, m.title, m.content, ...
FROM memories m
LEFT JOIN memory_entities me ON me.memory_id = m.id
WHERE m.deleted_at IS NULL
  AND m.superseded_by IS NULL
  AND me.memory_id IS NULL
  AND m.project IS ?
ORDER BY m.created_at ASC
```

With `--force`, this method is not used — all active memories are processed regardless of existing entities.

### New store method: DeleteRelatedToRelations

```go
// DeleteRelatedToRelations deletes all relations of type 'related_to' scoped to
// entities belonging to the given project. Returns the count of deleted rows.
func (s *MemoryStore) DeleteRelatedToRelations(ctx context.Context, project string) (int, error)
```

```sql
DELETE FROM relations
WHERE type = 'related_to'
  AND source_id IN (SELECT id FROM entities WHERE project IS ?)
```

### New store method: FindCandidatePairs

```go
// FindCandidatePairs returns pairs of memory IDs that share at least minShared
// entities, ordered by shared count descending. Each pair appears only once
// (mem1 < mem2 lexicographically).
func (s *MemoryStore) FindCandidatePairs(ctx context.Context, project string, minShared int) ([]CandidatePair, error)

// CandidatePair represents two memories with shared entity overlap.
type CandidatePair struct {
    MemoryID1   string
    MemoryID2   string
    SharedCount int
}
```

### 4.4. CLI -- `mneme graph rebuild`

New subcommand under `mneme graph` parent:

```bash
mneme graph rebuild [flags]

# Examples:
mneme graph rebuild                           # project scope, K=2, no force
mneme graph rebuild --force                   # delete+rebuild related_to
mneme graph rebuild --dry-run                 # preview without writing
mneme graph rebuild --scope all               # project + global
mneme graph rebuild --min-shared 3            # require 3+ shared entities
mneme graph rebuild --batch-size 1000         # process 1000 memories per tx
```

**Flags:**

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--scope` | `-s` | string | `"project"` | Scope: project, global, all |
| `--min-shared` | `-k` | int | 2 | Minimum shared entities for relation |
| `--max-relations` | | int | 50 | Max relations per memory |
| `--batch-size` | `-b` | int | 500 | Memories per transaction |
| `--force` | `-f` | bool | false | Delete existing related_to and rebuild |
| `--dry-run` | `-n` | bool | false | Preview without writing |

**Registration:** New `newGraphCmd()` parent in `internal/cli/root.go` with `newGraphRebuildCmd()` as subcommand:

```go
root.AddCommand(newGraphCmd())
// inside newGraphCmd:
cmd.AddCommand(newGraphRebuildCmd())
```

**Output:**

```
Starting graph rebuild for project "wirvii/mneme"...
  Scope:       project
  Min shared:  2
  Force:       false
  Batch size:  500

Phase 1: Entity extraction
  [100%] (500/500) Processing memories...

Phase 2: Relation generation
  Candidate pairs found: 890

Phase 3: Creating relations
  [100%] (450/450) Creating relations...

Rebuild complete in 1.2s:
  Memories scanned:      500
  Entities extracted:    1240
  New entities:           380
  Memory-entity links:   1240
  New links:              620
  Relations created:      450
  Relations skipped:      440 (existing)
  Relations skipped:       12 (cap)
```

### 4.5. No MCP/HTTP frontend for v1

**Decision:** `graph rebuild` is a CLI-only command. It is an administrative maintenance operation, not an agent tool.

**Justificacion:**

- `embed backfill` is CLI-only (cli/embed.go). Same pattern.
- MCP agents should not trigger a full graph rebuild — it is a heavyweight operation.
- If needed, HTTP parity can be added later (like the missing SDD endpoints noted in architecture/interfaces memory).

---

## 5. Edge cases

### 5.1. Memory with no entities extracted

All 4 heuristics return empty (no topic_key, no file paths, no code symbols, no wikilinks). This memory gets 0 entries in `memory_entities`. It will not participate in any relations. Not an error — some memories (e.g. short session summaries) may legitimately have no extractable entities.

### 5.2. New project with zero memories

`List()` returns empty slice. Rebuild prints "Nothing to do" and exits 0. Not an error.

### 5.3. --scope all on mneme with no global memories

globalStore.List returns empty. Only projectStore is processed. Output includes both phases with global showing "0 memories".

### 5.4. --force with weights already high from Hebbian

`--force` deletes ALL `related_to` relations regardless of weight. If Hebbian has strengthened a `related_to` edge to 0.8 through 14 co-accesses, that weight is lost. The rebuild recreates it with weight based on entity overlap (max 0.5). The Hebbian system will re-strengthen it over time.

**Mitigation:** The rebuild prints `Relations deleted: N` so the user sees the impact. The --dry-run flag exists for preview.

### 5.5. Large dataset >10K memories: chunked processing

With 10K memories and ~5 entities each = 50K memory_entities rows. The SQL JOIN produces up to 50K * 50K / 2 candidate pairs (but HAVING >= 2 filters most).

**Chunking strategy:**

- Phase 1 (entity extraction): Processed in batches of `--batch-size` (default 500). Each batch is a separate transaction.
- Phase 2 (pair generation): Single SQL query with `idx_memory_entities_entity`. For 50K rows, estimated ~2s.
- Phase 3 (relation creation): Processed in batches of 500 pairs per transaction.

**Memory pressure mitigation:** The candidate pairs query returns `(mem1, mem2, shared_count)` — 3 strings + 1 int per row. With 100K pairs, this is ~5MB in Go memory. Acceptable.

If the dataset exceeds 100K memory_entities, the pair query can be paginated with `LIMIT ? OFFSET ?` batches. Not implemented in v1 — measure first.

### 5.6. Memories with many entities: e.g. a memory listing 100 file paths

Entity extraction produces 100 entities. All 100 get `memory_entities` entries. During pair generation, this memory will have high overlap with many others. The per-memory cap (D4, default 50) limits the number of relations created.

### 5.7. Concurrent access during rebuild

SQLite WAL mode allows concurrent reads during rebuild. The Hebbian worker pool may create `related_to` relations concurrently. `FindRelationBidirectional` prevents duplicates. No special handling needed.

---

## 6. Apps/Modulos afectados

| Modulo | Archivo | Tipo de cambio |
|--------|---------|---------------|
| `internal/model` | `rebuild.go` (NEW) | RebuildRequest, RebuildResult |
| `internal/store` | `entity.go` | DeleteRelatedToRelations, FindCandidatePairs, CandidatePair, ListMemoriesWithoutEntities |
| `internal/store` | `entity_test.go` | Tests for new methods |
| `internal/service` | `rebuild.go` (NEW) | RebuildGraph, extractEntities, extractFilePaths, extractCodeSymbols, extractWikilinks, regex vars |
| `internal/service` | `rebuild_test.go` (NEW) | Integration tests |
| `internal/config` | `config.go` | 2 new fields in GraphConfig (RebuildMinShared, RebuildMaxRelations) |
| `internal/config` | `config_test.go` | Tests for new defaults and validation |
| `internal/cli` | `graph.go` (NEW) | newGraphCmd, newGraphRebuildCmd |
| `internal/cli` | `graph_test.go` (NEW) | CLI tests |
| `internal/cli` | `root.go` | Register newGraphCmd |

### Fuera de scope

- MCP/HTTP frontends for graph rebuild — CLI only (like `embed backfill`).
- Entity extraction during `mem_save` (automatic extraction on save is SPEC-W1 territory).
- Wikilink resolution (SPEC-W1).
- PPR / community detection (SPEC-P1, SPEC-C1).
- Changes to `mem_search`, `mem_explore`, or `graphExpand` — they consume the graph; this spec produces it.

---

## 7. Config

### New fields in `GraphConfig` (`internal/config/config.go`)

```go
// RebuildMinShared is the minimum number of shared entities required to create
// a co-occurrence relation between two memories during graph rebuild.
// Default: 2.
RebuildMinShared int `toml:"rebuild_min_shared"`

// RebuildMaxRelations is the maximum number of co-occurrence relations created
// per memory during graph rebuild. Relations with the highest overlap are kept.
// Default: 50.
RebuildMaxRelations int `toml:"rebuild_max_relations"`
```

**Defaults** in `Default()`:

```go
RebuildMinShared:    2,
RebuildMaxRelations: 50,
```

**Validation** in `Validate()`:

```go
if c.Graph.RebuildMinShared < 1 {
    return errors.New("graph.rebuild_min_shared must be >= 1")
}
if c.Graph.RebuildMaxRelations < 1 {
    return errors.New("graph.rebuild_max_relations must be >= 1")
}
```

---

## 8. Plan de implementacion atomico

6 commits, siguiendo el patron SPEC-005/006/007/008 (model -> config -> store -> service -> cli):

| # | Commit | Archivos | Descripcion |
|---|--------|----------|-------------|
| 1 | `feat(model): add RebuildRequest and RebuildResult` | `internal/model/rebuild.go` (NEW) | New file with rebuild types. |
| 2 | `feat(config): add graph rebuild params to GraphConfig` | `internal/config/config.go`, `internal/config/config_test.go` | RebuildMinShared, RebuildMaxRelations. Defaults + validation + tests. |
| 3 | `feat(store): add DeleteRelatedToRelations, FindCandidatePairs, ListMemoriesWithoutEntities` | `internal/store/entity.go`, `internal/store/entity_test.go` | Three new methods with integration tests. |
| 4 | `feat(service): add RebuildGraph with entity extraction and relation generation` | `internal/service/rebuild.go` (NEW), `internal/service/rebuild_test.go` (NEW) | RebuildGraph method, 4 extraction heuristics (H1-H4), regex vars, pair generation via SQL JOIN, batch processing. Integration tests. |
| 5 | `feat(cli): add mneme graph rebuild command` | `internal/cli/graph.go` (NEW), `internal/cli/graph_test.go` (NEW), `internal/cli/root.go` | newGraphCmd parent + newGraphRebuildCmd subcommand, flags, progress output, dry-run. Tests. Registration in root. |
| 6 | `test(service): add benchmark for RebuildGraph on 5K memories` | `internal/service/bench_test.go` | BenchmarkRebuildGraph_5K. |

---

## 9. Tests requeridos

### config (unit)

1. `TestGraphConfig_RebuildDefaults` — RebuildMinShared=2, RebuildMaxRelations=50.
2. `TestGraphConfig_RebuildValidation` — MinShared<1 error, MaxRelations<1 error. Valid values pass.

### store (integration, SQLite in-memory)

3. `TestStore_DeleteRelatedToRelations_OnlyRelatedTo` — creates 3 relation types, deletes only `related_to`, others survive.
4. `TestStore_DeleteRelatedToRelations_ProjectScoped` — relations for different projects are not deleted.
5. `TestStore_FindCandidatePairs_Basic` — 3 memories, 2 sharing 2 entities -> 1 pair returned.
6. `TestStore_FindCandidatePairs_BelowThreshold` — 2 memories sharing 1 entity with K=2 -> 0 pairs.
7. `TestStore_FindCandidatePairs_NoDuplicates` — pair (A,B) appears once, not twice.
8. `TestStore_ListMemoriesWithoutEntities_Basic` — 3 memories, 1 with entity, 2 without -> returns 2.

### service (integration, SQLite in-memory)

9. `TestRebuildGraph_Basic` — 5 memories with overlapping file paths, verify entities created, links created, relations created with correct weights.
10. `TestRebuildGraph_Idempotent` — Run twice, second run creates 0 new entities/links/relations.
11. `TestRebuildGraph_Force` — Run once, run with Force, verify related_to deleted and recreated. Explicit depends_on survives.
12. `TestRebuildGraph_DryRun` — Run with DryRun, verify 0 entities/links/relations in DB, but result counts are non-zero.
13. `TestRebuildGraph_NoMemories` — Empty project, verify 0 everything, no error.
14. `TestRebuildGraph_MemoryWithNoEntities` — Memory with content "hello world" (no paths, no symbols), verify 0 entities for that memory.
15. `TestRebuildGraph_MaxRelationsCap` — Memory connected to >50 others, verify cap enforced.
16. `TestRebuildGraph_WeightFormula` — 2 memories sharing 3 entities, verify weight = min(0.5, 3*0.1) = 0.3.

### extraction (unit, in service/rebuild_test.go)

17. `TestExtractEntities_TopicKey` — Memory with topic_key, verify entity with kind=concept, role=subject.
18. `TestExtractEntities_FilePaths` — Memory with `internal/store/entity.go` in content, verify entity with kind=file.
19. `TestExtractEntities_CodeSymbols` — Memory with `func RebuildGraph(...)` in code block, verify entity with kind=module.
20. `TestExtractEntities_Wikilinks` — Memory with `[[architecture/auth-model]]` in content, verify entity with kind=concept, role=mention.
21. `TestExtractEntities_Dedup` — Same path mentioned 3 times, verify 1 entity.
22. `TestExtractEntities_MinLength` — Symbol "fn" (2 chars) excluded, "Get" (3 chars) included.

### cli

23. `TestGraphRebuildCmd_Flags` — Verify --scope, --min-shared, --force, --dry-run, --batch-size parsed correctly.
24. `TestGraphRebuildCmd_DryRunOutput` — Verify "Dry run" prefix in output.

---

## 10. Criterios de aceptacion

1. **Entity extraction functional:** `mneme graph rebuild` on a project with 10 memories containing file paths and topic_keys produces >= 1 entity per memory (for memories with topic_key) and creates memory_entities links.

2. **Relation generation correct:** Two memories sharing 3 file paths produce a `related_to` relation with weight = 0.3 (`min(0.5, 3 * 0.1)`). Two memories sharing only 1 entity produce no relation (K=2 threshold).

3. **Idempotent without --force:** Running `mneme graph rebuild` twice produces `RelationsCreated: 0` and `EntitiesCreated: 0` on the second run.

4. **--force preserves explicit relations:** After `mneme graph rebuild --force`, relations of type `depends_on`, `implements`, `uses`, etc. are intact. Only `related_to` relations were deleted and recreated.

5. **--dry-run does not write:** `mneme graph rebuild --dry-run` produces non-zero counts in output but the database has 0 new entities, 0 new links, 0 new relations.

6. **Performance < 5s** for full rebuild of 5K memories / 20K memory_entities. Verified with BenchmarkRebuildGraph_5K.

---

## 11. Performance budget

**Target:** < 5s for 5K memories with ~5 entities/memory = 25K memory_entities rows.

**Estimated breakdown:**

| Phase | Operation | Estimate |
|-------|-----------|----------|
| 1 | List all memories (10 batches of 500) | 200ms |
| 1 | Entity extraction (regex on 5K memories) | 500ms |
| 1 | FindOrCreateEntity (25K calls) | 1500ms |
| 1 | LinkMemoryEntity (25K calls) | 1000ms |
| 2 | SQL JOIN for candidate pairs | 500ms |
| 3 | FindRelationBidirectional + CreateRelation (~5K pairs) | 1000ms |
| **Total** | | **~4.7s** |

**Mitigations if over budget:**

1. Batch `FindOrCreateEntity` with `INSERT OR IGNORE` + `SELECT` pattern instead of one-by-one.
2. Batch `LinkMemoryEntity` with multi-row INSERT.
3. Use prepared statements for repeated queries.

---

## 12. Dependencias

- **Hacia atras:** SPEC-005 (weighted relations, DefaultRelationWeights), SPEC-006 (Hebbian, HebbianInitialWeight, FindRelationBidirectional), SPEC-007 (idx_memory_entities_entity for JOIN performance), SPEC-008 (mem_explore consumes the graph produced here).
- **Hacia adelante:** SPEC-W1 (wikilinks) will add automatic entity extraction during `mem_save`. SPEC-009's extraction heuristics can be reused by SPEC-W1. EPIC-7 (docs) will document `mneme graph rebuild` in the user guide.
- **No requiere migracion SQL.** All tables and indices exist from migrations 002, 007, 008.
