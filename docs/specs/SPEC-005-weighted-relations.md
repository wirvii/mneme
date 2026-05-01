# SPEC-005 — Migracion 007: Weighted Relations

| Campo         | Valor                                                          |
|---------------|----------------------------------------------------------------|
| **ID**        | SPEC-005                                                       |
| **Epic**      | EPIC-2 — Grafo con peso + 1-hop expansion                     |
| **Backlog**   | BL-005                                                         |
| **Estado**    | speccing -> specced                                            |
| **Owner**     | architect                                                      |
| **Fecha**     | 2026-04-30                                                     |
| **Memorias**  | `roadmap/v2-master-plan`, `architecture/memory-model`, `architecture/interfaces`, `spec/SPEC-001-rule-type-design`, `spec/SPEC-001-implementation-notes` |

---

## 1. Contexto y motivacion

SPEC-005 es la primera spec de EPIC-2 (Grafo con peso + 1-hop expansion). Su proposito es transformar el knowledge graph de mneme de un grafo con aristas uniformes a un **grafo con pesos normalizados** en el rango [0.0, 1.0], con defaults diferenciados por tipo de relacion y tracking temporal de traversals.

### Por que ahora

EPIC-1 (rules) ya esta completo (SPEC-001 a SPEC-004). El grafo actual (`002_knowledge_graph.sql`) tiene una columna `weight REAL NOT NULL DEFAULT 1.0` en la tabla `relations`, pero se usa de forma uniforme: `store.CreateRelation()` (`internal/store/entity.go:181`) hardcodea `r.Weight = 1.0` cuando es 0, y `service.Relate()` (`internal/service/graph.go:80`) siempre crea con `Weight: 1.0`. No hay forma de:

1. Diferenciar la fuerza de una relacion `depends_on` (critica) de una `related_to` (debil).
2. Fortalecer o debilitar relaciones en base al uso (Hebbian learning, SPEC-G2).
3. Saber cuando fue la ultima vez que una relacion fue traversada (para decay futuro, SPEC-P1).

Esta spec redefine la semantica de `weight` de "unbounded, siempre 1.0" a "normalizado [0.0, 1.0] con defaults por tipo", agrega `last_traversed_at` para tracking temporal, introduce el tipo `references` (preparacion para SPEC-W1 wikilinks), y expone `UpdateRelationWeight` como operacion atomica.

### Que prepara

- **SPEC-G2 (Hebbian auto-strengthening):** Necesita `UpdateRelationWeight` y `last_traversed_at` para reforzar/debilitar aristas en base al co-acceso.
- **SPEC-G3 (1-hop expansion + RRF):** Necesita pesos para ponderar la expansion.
- **SPEC-P1 (PPR):** Necesita pesos normalizados para la matriz de adyacencia y `last_traversed_at` para teleportation bias.
- **SPEC-C1 (Louvain):** Necesita pesos para modularidad ponderada.
- **SPEC-W1 (Wikilinks):** Necesita el tipo `references` para crear relaciones desde `[[topic_key]]`.

---

## 2. Decisiones de diseno

### D1. Redefinicion de `weight` de [0, inf) a [0.0, 1.0] — backfill con CASE UPDATE

**Decision:** No se agrega una columna nueva. La columna `weight REAL NOT NULL DEFAULT 1.0` ya existe en `002_knowledge_graph.sql:23`. La migracion 007 cambia su semantica mediante:
1. Cambiar el `DEFAULT` a 0.5 (via recreacion de la columna, ya que SQLite no soporta `ALTER COLUMN ... DEFAULT` -- pero como `weight` es NOT NULL y ya tiene datos, el DEFAULT solo afecta inserts futuros sin valor explicito, lo cual no ocurre porque `store.CreateRelation` siempre pasa un valor explicito).
2. Backfill con `UPDATE` usando un `CASE` por tipo de relacion.

**Rationale:**
- La alternativa seria agregar una columna nueva `normalized_weight` y deprecar `weight`. Esto duplicaria el esquema y obligaria a cambiar todos los SELECT/scan de relaciones. Dado que `weight` nunca fue expuesto al usuario con semantica documentada y siempre vale 1.0 en la practica, redefinirlo in-place es mas limpio.
- El `UPDATE ... CASE` es O(n) sobre las relaciones existentes, pero el knowledge graph es pequeño (tipicamente <1000 relaciones por proyecto).

**Nota sobre el DEFAULT:** SQLite no tiene `ALTER TABLE ... ALTER COLUMN ... DEFAULT`. El DEFAULT 1.0 queda en el schema original. En la practica esto no importa porque `store.CreateRelation()` siempre proporciona el valor de `weight` explicitamente (linea `internal/store/entity.go:189`). El DEFAULT solo se usaria si alguien insertara directamente via SQL sin incluir `weight`, lo cual no ocurre en el codebase. Para evitar confusion futura, la spec documenta que el DEFAULT del schema es 1.0 pero el valor real lo determina `DefaultRelationWeights` en Go.

### D2. Tipo `references` anadido al enum

**Decision:** Agregar `RelReferences RelationType = "references"` a `validRelationTypes` en `internal/model/entity.go:78-86`.

**Rationale:** SPEC-W1 (wikilinks) creara relaciones de tipo `references` cuando detecte `[[topic_key]]` en el contenido de una memoria. Agregar el tipo ahora (aunque ningun row lo use aun) evita que SPEC-W1 necesite una migracion adicional. El patron de agregar un valor al enum Go antes de usarlo ya se establecio con `TypeRule` en SPEC-001 (`internal/model/memory.go`).

No se agrega un CHECK constraint en SQL (ver D3).

### D3. NO usar CHECK constraint en SQL para relation_type ni weight

**Decision:** Validacion de `weight` en rango [0.0, 1.0] y de `type` como valor conocido se hace **exclusivamente en Go** (service layer).

**Rationale:**
- **Consistencia con el patron existente:** La tabla `relations` no tiene CHECK constraints sobre `type` ni `weight` (`002_knowledge_graph.sql:16-25`). La tabla `entities` tampoco tiene CHECK sobre `kind`. La unica tabla con CHECK en el codebase es `memories` para `severity` (`006_rule_fields.sql:14-15`), y esa decision fue especifica de SPEC-001 porque `severity` tiene solo 3+1 valores discretos y el campo podia venir de multiples frontends.
- **Extensibilidad:** Si en el futuro se agregan mas tipos de relacion (SPEC-W1 ya necesitara `references`), no hay que migrar el CHECK. Los tipos de relacion son mas volatiles que los valores de severity.
- **Peso como float:** Un CHECK `weight BETWEEN 0.0 AND 1.0` en SQLite funciona pero no protege contra `NaN` ni `Inf` (SQLite trata NaN como NULL en comparaciones, lo que hace que el CHECK pase). La validacion robusta requiere Go.

### D4. Atomicidad de `UpdateRelationWeight` — clamping en SQL

**Decision:** La funcion `store.UpdateRelationWeight(ctx, relationID, delta)` usa una sola sentencia SQL:

```sql
UPDATE relations
SET weight = MAX(0.0, MIN(1.0, weight + ?)),
    last_traversed_at = ?
WHERE id = ?
```

**Rationale:**
- El clamping `MAX(0, MIN(1, weight + delta))` se ejecuta **atomicamente en SQL**, eliminando la race condition de read-modify-write. Dos goroutines ejecutando `UpdateRelationWeight(id, +0.1)` simultaneamente produciran resultados correctos porque cada UPDATE lee y escribe en un solo statement.
- SQLite serializa writes, asi que no hay posibilidad de lectura inconsistente dentro de un statement.
- Se actualiza `last_traversed_at` en el mismo statement para registrar el momento del traversal.

### D5. Indices nuevos: `idx_relations_weight` e `idx_relations_last_traversed`

**Decision:** Crear dos indices:
1. `idx_relations_weight ON relations(weight)` — para SPEC-G3 (1-hop expansion ordenada por peso).
2. `idx_relations_last_traversed ON relations(last_traversed_at)` — para SPEC-G2 (decay de relaciones no traversadas) y SPEC-P1 (teleportation bias).

**Rationale:** Los dos queries mas comunes sobre el grafo en los epics futuros seran:
- "Dame las relaciones mas fuertes de esta entidad" (ORDER BY weight DESC) -- SPEC-G3, SPEC-P1.
- "Dame las relaciones no traversadas recientemente" (ORDER BY last_traversed_at ASC WHERE last_traversed_at < ?) -- SPEC-G2 decay.

Ambos indices son sobre la tabla `relations` que es pequeña (tipicamente <1000 rows por DB). El overhead de escritura es despreciable. Sin indices, los queries harian table scans que son aceptables hoy pero se degradaran si el grafo crece con wikilinks automaticos (SPEC-W1).

### D6. Defaults por tipo de relacion

**Decision:** Mapping completo de `DefaultRelationWeights`:

| RelationType | Default Weight | Rationale |
|-------------|---------------|-----------|
| `depends_on` | 0.9 | Dependencia directa, critica para integridad del grafo. Un modulo que depende de otro es una relacion fuerte. |
| `implements` | 0.8 | Relacion contractual fuerte pero no tan critica como dependencia. |
| `part_of` | 0.85 | Relacion composicional fuerte. Un componente que es parte de un sistema es mas fuerte que una implementacion abstracta. |
| `uses` | 0.7 | Uso directo, importante pero mas debil que dependencia/composicion. |
| `supersedes` | 0.6 | Relacion temporal — el target pierde relevancia. Peso medio porque el source aun necesita contexto del target. |
| `related_to` | 0.5 | Relacion generica, sin semantica fuerte. Es el "no se que tipo exacto es". |
| `conflicts_with` | 0.7 | Conflictos son importantes para detectar pero no definen la estructura del grafo. |
| `references` | 0.4 | Referencia debil (wikilink). Muchas referencias no implican fuerte acoplamiento. Peso bajo para evitar que el grafo se sature con links superficiales de SPEC-W1. |

**Rationale general:** Los pesos reflejan la importancia estructural de la relacion para la navegacion del grafo. Las relaciones que definen la topologia del sistema (`depends_on`, `part_of`, `implements`) tienen peso alto. Las relaciones informativas (`related_to`, `references`) tienen peso bajo. El Hebbian auto-strengthening (SPEC-G2) ajustara estos pesos en base al uso real.

---

## 3. Modelo de datos

### 3.1. Cambios en `model.Relation` (`internal/model/entity.go:122-144`)

```go
// Relation is a directed edge between two entities in the knowledge graph.
// It records a typed, weighted relationship from a source entity to a target entity.
// Weight is normalised to [0.0, 1.0] where higher values indicate stronger
// relationships. LastTraversedAt records when the relation was last used
// for graph navigation, enabling temporal decay of unused edges.
type Relation struct {
	// ID is a UUIDv7 — time-sortable and globally unique.
	ID string `json:"id"`

	// SourceID is the UUIDv7 of the entity the edge originates from.
	SourceID string `json:"source_id"`

	// TargetID is the UUIDv7 of the entity the edge points to.
	TargetID string `json:"target_id"`

	// Type is the kind of relationship.
	Type RelationType `json:"type"`

	// Weight is the normalised strength of the relationship in [0.0, 1.0].
	// Higher values indicate stronger, more important relationships.
	// Defaults are type-specific (see DefaultRelationWeights).
	Weight float64 `json:"weight"`

	// Metadata is an optional JSON blob for storing additional edge attributes.
	Metadata string `json:"metadata,omitempty"`

	// CreatedAt is the wall-clock time when the relation was first created.
	CreatedAt time.Time `json:"created_at"`

	// LastTraversedAt records the most recent time this relation was used
	// for graph navigation (e.g. 1-hop expansion, PPR, mem_explore).
	// Zero value means the relation has never been traversed since tracking began.
	LastTraversedAt time.Time `json:"last_traversed_at,omitempty"`
}
```

### 3.2. Nuevo tipo `RelReferences` (`internal/model/entity.go`)

Agregar a la lista de constantes (despues de `RelConflictsWith`, linea 74):

```go
// RelReferences indicates the source entity references the target entity,
// typically through a [[wikilink]] in memory content. This is a weak
// relationship created by the wikilink parser (SPEC-W1).
RelReferences RelationType = "references"
```

Agregar a `validRelationTypes` (linea 85):

```go
RelReferences: {},
```

### 3.3. Helpers de defaults (`internal/model/entity.go`)

```go
// DefaultRelationWeights maps each RelationType to its default weight.
// These are used when creating new relations without an explicit weight.
// Values are normalised to [0.0, 1.0] and reflect the structural importance
// of each relation type for graph navigation.
var DefaultRelationWeights = map[RelationType]float64{
	RelDependsOn:     0.9,
	RelImplements:    0.8,
	RelSupersedes:    0.6,
	RelRelatedTo:     0.5,
	RelPartOf:        0.85,
	RelUses:          0.7,
	RelConflictsWith: 0.7,
	RelReferences:    0.4,
}

// DefaultWeight returns the default weight for the given RelationType.
// Returns 0.5 for unknown relation types as a safe fallback.
func DefaultWeight(rt RelationType) float64 {
	if w, ok := DefaultRelationWeights[rt]; ok {
		return w
	}
	return 0.5
}
```

### 3.4. Nuevo error sentinela (`internal/model/errors.go`)

```go
// ErrInvalidWeight is returned when a weight value is not in the valid range [0.0, 1.0]
// or is NaN/Inf.
var ErrInvalidWeight = errors.New("weight must be a finite number in [0.0, 1.0]")
```

### 3.5. Cambios en `model.RelateRequest` (`internal/model/entity.go:148-168`)

Agregar campo:

```go
// Weight overrides the default weight for this relation type.
// When zero, DefaultWeight(Relation) is used instead.
// Must be in [0.0, 1.0] when specified explicitly.
Weight float64 `json:"weight,omitempty"`
```

### 3.6. Cambios en `model.RelateResponse` (`internal/model/entity.go:171-183`)

Agregar campo:

```go
// Weight is the weight of the created or existing relation.
Weight float64 `json:"weight"`
```

---

## 4. Migracion 007

### `internal/db/migrations/007_weighted_relations.sql`

```sql
-- 007_weighted_relations.sql: Normalise relation weights and add traversal tracking.
-- Part of EPIC-2 (SPEC-005, SPEC-G1).
--
-- The relations table already has a weight column (002_knowledge_graph.sql)
-- with DEFAULT 1.0 and all existing rows at weight=1.0. This migration:
--   1. Backfills existing rows with type-appropriate default weights.
--   2. Adds last_traversed_at for temporal tracking of graph navigation.
--   3. Creates indices for weight-ordered and recency-ordered queries.
--
-- The weight column semantics change from "unbounded, always 1.0" to
-- "normalised [0.0, 1.0] with type-based defaults". No schema change
-- needed for weight itself since REAL already covers the new range.

-- UP

-- Backfill existing relations with type-appropriate default weights.
-- All existing rows have weight=1.0 from migration 002. Remap them to
-- the new [0.0, 1.0] scale based on relation type.
UPDATE relations SET weight = CASE type
    WHEN 'depends_on'     THEN 0.9
    WHEN 'implements'     THEN 0.8
    WHEN 'part_of'        THEN 0.85
    WHEN 'uses'           THEN 0.7
    WHEN 'supersedes'     THEN 0.6
    WHEN 'related_to'     THEN 0.5
    WHEN 'conflicts_with' THEN 0.7
    WHEN 'references'     THEN 0.4
    ELSE 0.5
END;

-- Add traversal tracking column. NULL means "never traversed since tracking
-- began". Using TEXT for ISO 8601 timestamps, consistent with other *_at
-- columns in the schema (memories.created_at, entities.created_at, etc.).
ALTER TABLE relations ADD COLUMN last_traversed_at TEXT;

-- Index for weight-ordered queries (1-hop expansion, PPR adjacency).
CREATE INDEX IF NOT EXISTS idx_relations_weight ON relations(weight);

-- Index for recency-ordered traversal queries (Hebbian decay, teleportation bias).
CREATE INDEX IF NOT EXISTS idx_relations_last_traversed ON relations(last_traversed_at);

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (7, datetime('now'));
```

### DOWN (rollback)

```sql
-- DOWN
-- Reverting to uniform weight 1.0 and removing traversal tracking.
-- This is a lossy rollback: type-specific weights are lost.

DROP INDEX IF EXISTS idx_relations_last_traversed;
DROP INDEX IF EXISTS idx_relations_weight;
ALTER TABLE relations DROP COLUMN last_traversed_at;
UPDATE relations SET weight = 1.0;

DELETE FROM schema_version WHERE version = 7;
```

### Notas de diseno de la migracion

1. **El `UPDATE ... CASE` es O(n):** A diferencia de `ALTER TABLE ADD COLUMN` (O(1) en SQLite), el UPDATE toca cada fila. Para un knowledge graph tipico (<1000 relations), esto tarda <10ms. En el peor caso extremo (100k relations), estimado <500ms. Aceptable para una migracion one-shot.
2. **ELSE 0.5 en el CASE:** Si alguna relacion tiene un `type` que no esta en el enum conocido (error de datos o tipo futuro no previsto), cae al fallback de 0.5 (el peso de `related_to`). Esto es mas seguro que fallar la migracion.
3. **`last_traversed_at` nullable:** A diferencia de `created_at` (NOT NULL), `last_traversed_at` admite NULL porque ninguna relacion existente ha sido "traversada" en el sentido del nuevo tracking. El zero value de `time.Time{}` en Go se mapea a cadena vacia o NULL en SQLite.
4. **ALTER TABLE ADD COLUMN es O(1):** Agregar `last_traversed_at` es instantaneo independientemente del numero de filas.
5. **Migracion sobre DB con 0 relations:** El UPDATE no toca nada, el ALTER TABLE y los CREATE INDEX se ejecutan sobre tabla vacia. No hay error.
6. **Atomicidad via applyMigration:** El migration runner (`internal/db/migrate.go:93-117`) ejecuta toda la migracion dentro de una transaccion. Si alguna sentencia falla, se hace rollback completo.

---

## 5. Contratos de API

### 5.1. MCP — `mem_relate` schema actualizado

Cambios en `allTools()` (`internal/mcp/tools.go`), entrada `mem_relate` (linea 244):

**Propiedad nueva en `properties`:**

```go
"weight": map[string]any{
    "type":        "number",
    "description": "Override the default weight for this relation type. Must be between 0.0 and 1.0. When omitted, a type-specific default is used (e.g. depends_on=0.9, related_to=0.5).",
},
```

**Agregar `"references"` al enum de `relation`** (linea 262):

```go
"enum": []string{
    "depends_on", "implements", "supersedes",
    "related_to", "part_of", "uses", "conflicts_with",
    "references",
},
```

#### Ejemplo request: crear relacion con peso explicito

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "mem_relate",
    "arguments": {
      "source": "auth-service",
      "target": "jwt-library",
      "relation": "depends_on",
      "source_kind": "service",
      "target_kind": "library",
      "weight": 0.95
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
        "text": "{\"relation_id\":\"019de200-abcd-7fff-8000-000000000001\",\"source_id\":\"019de100-...\",\"target_id\":\"019de100-...\",\"created\":true,\"weight\":0.95}"
      }
    ]
  }
}
```

#### Ejemplo request: relacion con peso por defecto (omitido)

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "mem_relate",
    "arguments": {
      "source": "store-package",
      "target": "model-package",
      "relation": "depends_on"
    }
  }
}
```

Response tendra `"weight":0.9` (default para `depends_on`).

#### Errores MCP

| Condicion | Code | Mensaje |
|-----------|------|---------|
| `weight` = 1.5 (fuera de rango) | -32602 (Invalid params) | `mcp: handle mem_relate: weight must be a finite number in [0.0, 1.0]` |
| `weight` = NaN | -32602 | `mcp: handle mem_relate: weight must be a finite number in [0.0, 1.0]` |
| `weight` = -0.1 (negativo) | -32602 | `mcp: handle mem_relate: weight must be a finite number in [0.0, 1.0]` |
| `relation` = `"references"` (nuevo, valido) | N/A | Exito con weight default 0.4 |

### 5.2. HTTP — `POST /v1/entities/relate` schema actualizado

El request body (`model.RelateRequest`) ahora acepta `weight`:

```bash
curl -X POST http://localhost:7437/v1/entities/relate \
  -H "Content-Type: application/json" \
  -d '{
    "source": "auth-service",
    "target": "jwt-library",
    "relation": "depends_on",
    "source_kind": "service",
    "target_kind": "library",
    "weight": 0.95
  }'
```

#### Response 201 Created

```json
{
  "relation_id": "019de200-abcd-7fff-8000-000000000001",
  "source_id": "019de100-...",
  "target_id": "019de100-...",
  "created": true,
  "weight": 0.95
}
```

#### Response 200 OK (relacion ya existente)

```json
{
  "relation_id": "019de200-abcd-7fff-8000-000000000001",
  "source_id": "019de100-...",
  "target_id": "019de100-...",
  "created": false,
  "weight": 0.9
}
```

#### Errores HTTP

| Condicion | HTTP Status | Body |
|-----------|-------------|------|
| `weight` fuera de [0.0, 1.0] | 400 Bad Request | `{"error":{"code":"invalid_request","message":"weight must be a finite number in [0.0, 1.0]"}}` |
| `weight` = NaN / Inf | 400 Bad Request | `{"error":{"code":"invalid_request","message":"weight must be a finite number in [0.0, 1.0]"}}` |

El mapeo en `server.go:errorStatus()` debe agregar `model.ErrInvalidWeight` a la rama que retorna `http.StatusBadRequest`.

### 5.3. CLI

No existe un comando CLI directo para `mem_relate`. El comando `relate` no esta en el Cobra tree (`internal/cli/`). No se agrega en esta spec -- el acceso es via MCP y HTTP.

---

## 6. Store layer — Persistencia

### 6.1. `store.CreateRelation()` (`internal/store/entity.go:171-199`)

Cambios:
- En lugar de hardcodear `r.Weight = 1.0` cuando `r.Weight == 0` (linea 181), usar `model.DefaultWeight(r.Type)`.
- Agregar `last_traversed_at` al INSERT (como NULL cuando es zero value de `time.Time`).
- Actualizar la query:

```go
if r.Weight == 0 {
    r.Weight = model.DefaultWeight(r.Type)
}

const q = `
    INSERT INTO relations (id, source_id, target_id, type, weight, metadata, created_at, last_traversed_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
```

### 6.2. `scanRelationRow()` (`internal/store/entity.go:437-460`)

Agregar scan de `last_traversed_at`:

```go
var (
    r              model.Relation
    metadata       sql.NullString
    createdAt      string
    lastTraversed  sql.NullString  // nuevo
)

err := row.Scan(
    &r.ID, &r.SourceID, &r.TargetID,
    &r.Type, &r.Weight,
    &metadata, &createdAt,
    &lastTraversed,  // nuevo
)
```

### 6.3. Todos los SELECT de relations deben ampliarse

Las queries en `GetRelationsFrom` (linea 204-208), `GetRelationsTo` (linea 216-220), y `FindRelation` (linea 281-284) deben agregar `last_traversed_at` al SELECT:

```sql
SELECT id, source_id, target_id, type, weight, metadata, created_at, last_traversed_at
FROM relations
```

### 6.4. Nuevo metodo `store.UpdateRelationWeight()`

```go
// UpdateRelationWeight atomically adjusts the weight of a relation by delta,
// clamping the result to [0.0, 1.0]. The last_traversed_at timestamp is
// updated to now. Returns the relation after the update, or ErrRelationNotFound
// if the ID does not exist.
func (s *MemoryStore) UpdateRelationWeight(ctx context.Context, relationID string, delta float64, now time.Time) (*model.Relation, error) {
    const q = `
        UPDATE relations
        SET weight = MAX(0.0, MIN(1.0, weight + ?)),
            last_traversed_at = ?
        WHERE id = ?`

    result, err := s.db.ExecContext(ctx, q, delta, now.UTC().Format(time.RFC3339Nano), relationID)
    if err != nil {
        return nil, fmt.Errorf("store: update relation weight: %w", err)
    }
    rows, _ := result.RowsAffected()
    if rows == 0 {
        return nil, fmt.Errorf("store: update relation weight: %w", model.ErrRelationNotFound)
    }

    // Re-read to return the updated state.
    // ... (usar query similar a FindRelation con WHERE id = ?)
}
```

### 6.5. Nuevo metodo `store.TouchRelation()`

```go
// TouchRelation updates only the last_traversed_at timestamp without changing
// the weight. Used when a relation is traversed during graph navigation
// (e.g. 1-hop expansion) without intent to strengthen/weaken it.
func (s *MemoryStore) TouchRelation(ctx context.Context, relationID string, now time.Time) error {
    const q = `UPDATE relations SET last_traversed_at = ? WHERE id = ?`
    // ...
}
```

---

## 7. Service layer — Logica de negocio

### 7.1. `service.Relate()` (`internal/service/graph.go:26-93`)

Cambios:

1. **Validacion de weight** (despues de la validacion de `Relation`, linea 37):

```go
if req.Weight != 0 {
    if math.IsNaN(req.Weight) || math.IsInf(req.Weight, 0) || req.Weight < 0 || req.Weight > 1 {
        return nil, fmt.Errorf("service: relate: %w", model.ErrInvalidWeight)
    }
}
```

2. **Usar weight del request o default** (linea 76-81):

```go
weight := req.Weight
if weight == 0 {
    weight = model.DefaultWeight(req.Relation)
}
rel := &model.Relation{
    SourceID: srcEntity.ID,
    TargetID: tgtEntity.ID,
    Type:     req.Relation,
    Weight:   weight,
}
```

3. **Incluir weight en RelateResponse** (lineas 67-72 y 87-92):

En el caso de relacion existente:
```go
return &model.RelateResponse{
    RelationID: existing.ID,
    SourceID:   srcEntity.ID,
    TargetID:   tgtEntity.ID,
    Created:    false,
    Weight:     existing.Weight,
}, nil
```

En el caso de relacion nueva:
```go
return &model.RelateResponse{
    RelationID: created.ID,
    SourceID:   srcEntity.ID,
    TargetID:   tgtEntity.ID,
    Created:    true,
    Weight:     created.Weight,
}, nil
```

### 7.2. Nuevo metodo `service.UpdateRelationWeight()`

```go
// UpdateRelationWeight adjusts the weight of an existing relation by delta,
// clamping to [0.0, 1.0]. Returns the updated relation. This is the primary
// API for Hebbian auto-strengthening (SPEC-G2).
func (svc *MemoryService) UpdateRelationWeight(ctx context.Context, relationID string, delta float64) (*model.Relation, error) {
    if math.IsNaN(delta) || math.IsInf(delta, 0) {
        return nil, fmt.Errorf("service: update relation weight: delta must be a finite number")
    }
    return svc.projectStore.UpdateRelationWeight(ctx, relationID, delta, time.Now().UTC())
}
```

---

## 8. Edge cases

### 8.1. Migracion sobre DB con 0 relations

El `UPDATE ... CASE` no toca nada (0 rows affected). `ALTER TABLE ADD COLUMN` y `CREATE INDEX` operan sobre tabla vacia. La migracion completa sin error y registra schema version 7.

### 8.2. Migracion sobre DB con relations cuyo `type` es un valor desconocido

El `ELSE 0.5` en el CASE maneja valores no reconocidos. Si alguna relacion tiene `type = 'invented_type'` (error de datos), recibira weight=0.5. Esto es mejor que fallar la migracion.

### 8.3. Insert con weight=NaN

`math.IsNaN(req.Weight)` en `service.Relate()` retorna `model.ErrInvalidWeight` antes de tocar la DB. El error se mapea a:
- MCP: code -32602, message `"mcp: handle mem_relate: weight must be a finite number in [0.0, 1.0]"`.
- HTTP: status 400, code `"invalid_request"`.

### 8.4. Insert con weight=Inf (positivo o negativo)

`math.IsInf(req.Weight, 0)` cubre ambos signos. Mismo tratamiento que NaN.

### 8.5. UpdateRelationWeight con delta negativo extremo (-100)

El SQL `MAX(0.0, MIN(1.0, weight + (-100)))` evalua a `MAX(0.0, MIN(1.0, -99.5))` = `MAX(0.0, -99.5)` = `0.0`. El weight se clampea correctamente a 0.0. No hay error.

### 8.6. UpdateRelationWeight con delta=NaN

El service valida `math.IsNaN(delta)` antes de llamar al store. Retorna error `"delta must be a finite number"` sin tocar la DB.

### 8.7. UpdateRelationWeight con delta=Inf

Misma validacion que NaN. `math.IsInf(delta, 0)` captura ambos signos.

### 8.8. Concurrencia: 100 updates simultaneos al mismo relation

SQLite serializa todos los writes via WAL. Cada `UPDATE ... SET weight = MAX(0.0, MIN(1.0, weight + ?))` es atomico: lee el weight actual y escribe el nuevo en un solo statement. 100 updates concurrentes se secuencian y cada uno ve el resultado del anterior. El resultado final es determinista (la suma de todos los deltas, clampeada). No hay data loss ni race condition.

El `busy_timeout=5000` (configurado en `db.Open()`, linea 47) evita `SQLITE_BUSY` en la mayoria de escenarios de contention.

### 8.9. Relacion existente via mem_relate con weight diferente

Cuando `FindRelation` encuentra una relacion existente (linea 66), se retorna `Created: false` con el **weight actual** de la relacion existente. **No se actualiza el weight.** Este es el comportamiento idempoente esperado: `mem_relate` crea O confirma, no modifica. Para modificar el weight, usar `UpdateRelationWeight`.

### 8.10. scanRelationRow con last_traversed_at NULL

Relaciones creadas antes de la migracion 007 tendran `last_traversed_at = NULL`. `sql.NullString` con `Valid=false` se mapea a `time.Time{}` (zero value). El campo JSON `last_traversed_at` con `omitempty` se omite del output para estos casos. Correcto.

### 8.11. Weight 0.0 explicito vs. weight omitido

`Weight float64` con `json:"weight,omitempty"` en `RelateRequest`: el zero value de float64 es 0.0 y `omitempty` lo omite del JSON. Esto significa que un cliente **no puede** pasar `weight: 0.0` explicitamente -- se tratara como "omitido" y recibira el default por tipo.

**Decision:** Esto es aceptable. Un weight de 0.0 significa "relacion sin fuerza", que es equivalente a "no existe". Si un caller quiere debilitar una relacion a 0.0, debe usar `UpdateRelationWeight` con un delta negativo suficiente. Crear una relacion con weight=0.0 no tiene sentido semantico.

---

## 9. Plan de implementacion

Pasos ordenados, cada uno commit-able independientemente. Sigue el patron de SPEC-001 (model -> db -> store -> service -> mcp -> http).

| # | Commit | Archivos | Descripcion |
|---|--------|----------|-------------|
| 1 | `feat(model): add RelReferences, DefaultRelationWeights, LastTraversedAt, ErrInvalidWeight` | `internal/model/entity.go`, `internal/model/errors.go` | Nuevo RelationType `references`, `DefaultRelationWeights` map, `DefaultWeight()` helper, campo `LastTraversedAt` en Relation, campo `Weight` en RelateRequest y RelateResponse, error sentinela `ErrInvalidWeight`. |
| 2 | `feat(db): add migration 007 for weighted relations` | `internal/db/migrations/007_weighted_relations.sql`, `internal/db/migrate_test.go`, `internal/db/schema_version_test.go` | DDL up con UPDATE CASE backfill + ALTER TABLE + indices. Schema version 7. Migration tests. |
| 3 | `feat(store): persist and load LastTraversedAt, add UpdateRelationWeight and TouchRelation` | `internal/store/entity.go`, `internal/store/entity_test.go` | Actualizar scanRelationRow (8 cols), CreateRelation con DefaultWeight, todos los SELECT de relations ampliados, nuevos metodos UpdateRelationWeight y TouchRelation. Tests roundtrip. |
| 4 | `feat(service): validate weight in Relate, add UpdateRelationWeight` | `internal/service/graph.go`, `internal/service/graph_test.go` | Validacion NaN/Inf/rango en Relate, usar DefaultWeight, incluir weight en response, nuevo metodo UpdateRelationWeight. Tests. |
| 5 | `feat(mcp): add weight prop to mem_relate schema, add references to enum` | `internal/mcp/tools.go`, `internal/mcp/handlers.go`, `internal/mcp/handlers_test.go` | Schema JSON actualizado, `references` en enum, error mapping de ErrInvalidWeight. |
| 6 | `feat(http): add ErrInvalidWeight mapping to POST /v1/entities/relate` | `internal/http/server.go`, `internal/http/server_test.go` | Error mapping actualizado para ErrInvalidWeight -> 400. |

---

## 10. Tests requeridos

### model (unit)

- `TestRelationTypeValid` -- actualizar para incluir `RelReferences` como caso valido y verificar que el total de tipos validos es 8 (era 7).
- `TestDefaultRelationWeights_Coverage` -- verificar que cada RelationType tiene entrada en DefaultRelationWeights.
- `TestDefaultWeight_Known` -- table-driven: verificar que `DefaultWeight(RelDependsOn)` == 0.9, etc.
- `TestDefaultWeight_Unknown` -- `DefaultWeight("unknown_type")` retorna 0.5.

### db (migration)

- `TestMigration007_Up` -- aplicar migration, verificar que `last_traversed_at` existe, insertar relacion, verificar roundtrip.
- `TestMigration007_BackfillWeights` -- insertar relaciones con weight=1.0 en schema version 6, aplicar migration 007, verificar que weight cambio al valor por tipo.
- `TestMigration007_BackfillUnknownType` -- insertar relacion con type='invented', aplicar migration, verificar weight=0.5.
- `TestMigration007_EmptyTable` -- aplicar migration sobre tabla relations vacia, verificar exito.
- `TestMigration007_Indices` -- verificar que idx_relations_weight e idx_relations_last_traversed existen despues de la migracion.

### store (integration, SQLite in-memory)

- `TestStore_CreateRelation_DefaultWeight` -- crear relacion sin weight explicito, verificar que usa DefaultWeight por tipo.
- `TestStore_CreateRelation_ExplicitWeight` -- crear relacion con weight=0.75, verificar que se persiste.
- `TestStore_CreateRelation_LastTraversedAtNull` -- crear relacion, verificar que last_traversed_at es zero value.
- `TestStore_UpdateRelationWeight_ClampHigh` -- update con delta=+10, verificar weight=1.0.
- `TestStore_UpdateRelationWeight_ClampLow` -- update con delta=-10, verificar weight=0.0.
- `TestStore_UpdateRelationWeight_Normal` -- update con delta=+0.1 sobre weight=0.5, verificar weight=0.6.
- `TestStore_UpdateRelationWeight_NotFound` -- update con ID inexistente, verificar ErrRelationNotFound.
- `TestStore_UpdateRelationWeight_SetsLastTraversed` -- verificar que last_traversed_at se actualiza.
- `TestStore_TouchRelation_OnlyTimestamp` -- touch sin cambiar weight, verificar last_traversed_at actualizado y weight intacto.
- `TestStore_ScanRelation_WithLastTraversed` -- crear, touch, re-read, verificar scan correcto.

### service

- `TestService_Relate_DefaultWeight` -- relate sin weight explicito, verificar response.Weight == DefaultWeight(tipo).
- `TestService_Relate_ExplicitWeight` -- relate con weight=0.75, verificar response.Weight.
- `TestService_Relate_InvalidWeightNaN` -- verificar ErrInvalidWeight.
- `TestService_Relate_InvalidWeightInf` -- verificar ErrInvalidWeight.
- `TestService_Relate_InvalidWeightNegative` -- weight=-0.1, verificar ErrInvalidWeight.
- `TestService_Relate_InvalidWeightAboveOne` -- weight=1.5, verificar ErrInvalidWeight.
- `TestService_Relate_ExistingReturnsCurrentWeight` -- relate duplicado retorna weight existente, no default.
- `TestService_Relate_ReferencesType` -- verificar que relation="references" funciona.
- `TestService_UpdateRelationWeight_DeltaNaN` -- verificar error.
- `TestService_UpdateRelationWeight_Normal` -- verificar round-trip.

### mcp

- `TestMCP_MemRelate_WithWeight` -- JSON-RPC request con weight, verificar response incluye weight.
- `TestMCP_MemRelate_InvalidWeight` -- verificar error code -32602.
- `TestMCP_MemRelate_ReferencesType` -- verificar que `references` es aceptado.
- `TestMCP_ToolSchema_IncludesWeight` -- verificar que allTools() incluye weight prop en mem_relate.

### http

- `TestHTTP_PostRelate_WithWeight` -- POST con weight, verificar 201 y weight en response.
- `TestHTTP_PostRelate_InvalidWeight` -- verificar 400.

---

## 11. Criterios de aceptacion

1. `SELECT weight, last_traversed_at FROM relations` muestra pesos diferenciados por tipo despues de migration 007 (no todos 1.0).
2. `mem_relate` con `weight: 0.95` crea la relacion con ese peso exacto; omitir `weight` usa el default por tipo (e.g. `depends_on` -> 0.9).
3. `mem_relate` con `weight: 1.5` retorna error -32602 con mensaje claro.
4. `UpdateRelationWeight(id, -100.0)` clampea a 0.0 sin error; `UpdateRelationWeight(id, +100.0)` clampea a 1.0.
5. `make test` pasa con todos los tests nuevos; `golangci-lint run` con cero warnings.
6. `mem_relate` con `relation: "references"` funciona correctamente con peso default 0.4.

**Performance budgets:**
- Migration 007 up en DB con 1000 relations: < 100ms.
- `UpdateRelationWeight` single call: < 5ms.

---

## 12. Open questions / pushbacks

### Q1. Weight 0.0 via JSON (ambiguedad con omitempty)

El campo `Weight float64` con `json:"weight,omitempty"` en `RelateRequest` no distingue entre "weight omitido" (usar default) y "weight = 0.0 explicito" (crear relacion con fuerza cero). La decision actual es que weight=0.0 no tiene sentido semantico y se trata como omitido.

**Alternativa no adoptada:** Usar `*float64` en lugar de `float64`. Esto permitiria distinguir nil (omitido) de 0.0, pero complica la API y el patron de Go idiomatico para RPCs sin beneficio claro (una relacion con weight=0.0 es inutil).

### Q2. Actualizacion de weight en mem_relate duplicado

Cuando `mem_relate` encuentra una relacion existente, actualmente retorna `Created: false` sin modificar el weight. Una alternativa seria: si el caller pasa un weight diferente, actualizar el weight de la relacion existente (upsert semantics).

**Decision actual:** No actualizar. `mem_relate` es idempotente (create-or-get). Cambiar weight es responsabilidad de `UpdateRelationWeight`. Esto mantiene la separacion de concerns y evita efectos secundarios sorpresivos.

Si el usuario necesita upsert-with-weight-update, se puede considerar como feature futuro. No bloquea SPEC-005.

---

## Scope explicitamente fuera

- Hebbian auto-strengthening -> SPEC-G2 (BL-006)
- 1-hop expansion + RRF -> SPEC-G3 (BL-007)
- mem_explore tool -> SPEC-G4 (BL-008)
- Wikilink parser que crea relaciones `references` -> SPEC-W1 (BL-009)
- PPR power iteration sobre weighted graph -> SPEC-P1 (BL-013)
- docs/GRAPH.md -> SPEC-D4 (BL-028)

---

## Dependencias

- **Hacia atras:** Ninguna. SPEC-005 no depende de EPIC-1 (rules) a nivel de codigo. Sin embargo, requiere que el schema version sea 6 (SPEC-001 ya fue implementado).
- **Hacia adelante:** SPEC-G2, G3, G4, W1, P1, C1 dependen de SPEC-005.
