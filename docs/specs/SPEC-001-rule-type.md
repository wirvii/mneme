# SPEC-001 — Nuevo tipo `rule` con `applies_to` y `severity`

| Campo         | Valor                                                          |
|---------------|----------------------------------------------------------------|
| **ID**        | SPEC-001                                                       |
| **Epic**      | EPIC-1 — Rules como ciudadanos de primera clase                |
| **Backlog**   | BL-001                                                         |
| **Estado**    | speccing -> specced                                            |
| **Owner**     | architect                                                      |
| **Fecha**     | 2026-04-30                                                     |
| **Memorias**  | `roadmap/v2-master-plan`, `architecture/memory-model`, `architecture/interfaces`, `architecture/mcp-error-codes`, `research/obsidian-lessons` |

---

## 1. Contexto y motivacion

El dolor mas frecuente reportado por el usuario: el agente rompe reglas que existen en memoria porque no las consulto. Las "reglas" hoy se guardan como `convention` o `architecture`, pero tienen el mismo peso que cualquier otra memoria -- el retrieval (BM25 + vector) puede o no traerlas segun la query. No existe forma de expresar que una memoria es una **restriccion obligatoria** (no un consejo) ni indicar sobre que herramientas o paths aplica.

SPEC-001 introduce el tipo `rule` como ciudadano de primera clase en el modelo de datos, con campos especificos para el matching (`applies_to`) y la severidad (`severity`). Esta spec cubre **solo** el modelado, la migracion y la persistencia. La inyeccion obligatoria en `mem_context` sera SPEC-R2 (BL-002), el hook pre-tool-use sera SPEC-R3 (BL-003), y la CLI `mneme rule add/list/test` sera SPEC-R4 (BL-004).

---

## 2. Decisiones de diseno

### D1. `rule` como tipo nuevo vs. flag booleano en cualquier memoria

**Decision:** Nuevo valor `rule` en el enum `MemoryType`.

**Rationale:** El tipo determina el comportamiento de scoring y decay (ver `model.DefaultImportance`, `model.DefaultDecayRate` en `internal/model/scoring.go:8-35`). Una rule necesita importance=0.95, decay_rate=0, y validacion especifica (`applies_to` requerido). Agregar un flag `is_rule bool` a cualquier tipo romperia la invariante de que el tipo gobierna el comportamiento de scoring, crearia una segunda dimension ortogonal, y complicaria las queries (hay que filtrar por type Y por flag). Un tipo dedicado es coherente con los 9 tipos existentes que ya modelan categorias de conocimiento con semantica distinta.

### D2. JSON-array en columna TEXT vs. tabla normalizada `rule_applies_to`

**Decision:** Columna `applies_to TEXT NOT NULL DEFAULT '[]'` con JSON array serializado.

**Rationale:**
- El uso de `applies_to` es siempre atomico: se lee completo, se valida completo, se escribe completo. No hay queries SQL tipo "dame todas las rules que aplican a `internal/store/*.go`" a nivel de store -- ese matching sera responsabilidad de un engine en Go (SPEC-R3, paquete `internal/rules/`).
- Los patrones son globs con wildcards, no valores normalizables. Una tabla join no aporta capacidad de query y agrega complejidad (JOINs en el scan path critico de `scanMemoryRow` que hoy escanea 19 columnas).
- El patron JSON-en-TEXT ya se usa en el codebase: `specs.assigned_agents TEXT NOT NULL DEFAULT '[]'` y `specs.files_changed TEXT NOT NULL DEFAULT '[]'` (ver `005_spec_pk_by_project.sql:23-24`).
- El sync export/import serializa `model.Memory` via `encoding/json`; un campo `[]string` viaja naturalmente. Una tabla join requeriria extension del formato JSONL.

### D3. Severity `info|warn|block` vs. escala numerica

**Decision:** Enum string de 3 valores.

**Rationale:** La severidad no es un gradiente continuo -- tiene semantica discreta:
- `info`: el agente ve la rule, la considera, pero no se le impide actuar. Ejemplo: "prefiere Server Components sobre Client Components".
- `warn`: el agente ve un warning explicito antes de actuar. Ejemplo: "no uses time.Now() directo, usa el clock inyectable".
- `block`: el hook pre-tool-use (SPEC-R3) rechaza la accion con exit code 2. Ejemplo: "nunca edites archivos en `vendor/`".

Una escala numerica (1-10) seria ambigua -- que significa severidad 6? Los consumidores de la rule (mem_context, hook) necesitan branching discreto, no interpolacion. Tres niveles cubren "informational", "advisory" y "enforcement" sin sobrecarga cognitiva.

### D4. Importance default 0.95 y decay_rate 0

**Decision:** `DefaultImportance[TypeRule] = 0.95`, `DefaultDecayRate[TypeRule] = 0.0`.

**Rationale:** Las rules son restricciones permanentes del proyecto. No pierden valor con el tiempo (a diferencia de bugfixes o session_summaries). Una regla de "no usar force push a main" es valida indefinidamente hasta que alguien la revoque explicitamente (via `mem_forget` o `mem_update`). Importance 0.95 las coloca justo debajo de 1.0 (reservado para overrides del usuario) y por encima de architecture (0.9).

**Conflicto detectado:** El test `TestDefaultDecayRateCoverage` (`internal/model/memory_test.go:117-134`) assertea `val > 0.0` con el comentario "zero means no decay". Esto fallara cuando se agregue `TypeRule` con decay_rate=0.

**Resolucion:** Modificar el test para excluir `TypeRule` de la asercion `> 0`, o cambiar la asercion a `val >= 0.0` con un comentario explicando que `TypeRule` tiene decay_rate=0 intencionalmente porque las rules son permanentes. Se recomienda la segunda opcion (mas robusta, menos fragil si se agregan otros tipos inmortales en el futuro).

### D5. Sintaxis de `applies_to`

**Decision:** Cada entrada del array es un string con uno de estos formatos:

| Formato | Ejemplo | Significado |
|---------|---------|-------------|
| Glob de path | `internal/store/**/*.go` | Aplica a archivos que matcheen el glob |
| Tool selector | `tool:Edit` | Aplica cuando se usa la tool `Edit` |
| Tool + path | `tool:Edit+internal/**/*.go` | Aplica a `Edit` solo dentro del path |
| Negacion | `!docs/**` | Excluye paths que matcheen |
| Wildcard global | `**` | Aplica a todo |

**Rationale:**
- Los globs de path usan doublestar (`**`) porque es el estandar en `.gitignore`, Go `filepath.Match` extendido, y la mayoria de herramientas de CI. No inventamos una sintaxis nueva.
- El prefijo `tool:` con nombre exacto (case-sensitive) es inequivoco y extensible. Los tools actuales de Claude Code que mutan archivos son `Edit`, `Write`, `MultiEdit`, `Bash` -- pero no hardcodeamos la lista aqui, el matching engine (SPEC-R3) la resolvera.
- El combinador `+` entre tool y path permite expresar "esta rule aplica cuando se usa Edit en archivos de internal/" sin duplicar entradas.
- Las negaciones con `!` son el mecanismo para excepciones: `["internal/**", "!internal/model/test_helpers.go"]` significa "aplica en internal/ excepto test_helpers".

**Validacion en esta spec (SPEC-001):** Solo se valida que el array no este vacio para type=rule y que cada entrada sea un string no vacio. La interpretacion semantica del pattern (doublestar matching, negaciones) es responsabilidad de SPEC-R3 (`internal/rules/`). Aqui solo almacenamos y validamos estructura.

---

## 3. Modelo de datos

### 3.1. Cambios en `model.Memory` (`internal/model/memory.go`)

```go
// TypeRule marks a memory as a project or global rule — a binding constraint
// that agents must respect. Rules differ from conventions in two ways:
// (1) they carry applies_to patterns and a severity level, and (2) they
// are immune to decay (decay_rate=0) so they remain active until
// explicitly revoked via mem_forget or mem_update.
TypeRule MemoryType = "rule"
```

Agregar a `validMemoryTypes` y a `AllMemoryTypes()`.

Nuevos campos en `Memory` struct (despues de `Files`):

```go
// AppliesTo holds the list of patterns that determine when this rule is
// relevant. Patterns can be file path globs (e.g. "internal/**/*.go"),
// tool selectors (e.g. "tool:Edit"), combined selectors
// ("tool:Edit+internal/**/*.go"), negations ("!docs/**"), or the global
// wildcard "**". Only meaningful when Type is TypeRule; for all other
// types this slice must be nil or empty.
// Stored in SQLite as a JSON-encoded TEXT column.
AppliesTo []string `json:"applies_to,omitempty"`

// Severity indicates how strictly this rule should be enforced.
// Valid values are "info" (advisory), "warn" (explicit warning), and
// "block" (reject the action). Only meaningful when Type is TypeRule.
// Stored as a TEXT column in SQLite with a CHECK constraint.
Severity Severity `json:"severity,omitempty"`
```

### 3.2. Nuevo tipo `Severity` (`internal/model/memory.go`)

```go
// Severity controls how strictly a rule is enforced. It is stored as a
// string in SQLite and validated before persistence.
type Severity string

const (
    // SeverityInfo means the rule is advisory — the agent should consider
    // it but is not prevented from acting.
    SeverityInfo Severity = "info"

    // SeverityWarn means the agent receives an explicit warning before
    // the action proceeds. The hook emits the warning but does not block.
    SeverityWarn Severity = "warn"

    // SeverityBlock means the hook rejects the action entirely. The agent
    // must find an alternative approach or request an exception.
    SeverityBlock Severity = "block"
)

var validSeverities = map[Severity]struct{}{
    SeverityInfo:  {},
    SeverityWarn:  {},
    SeverityBlock: {},
}

// Valid reports whether the Severity is one of the recognised constants.
func (s Severity) Valid() bool {
    _, ok := validSeverities[s]
    return ok
}
```

### 3.3. Cambios en `model.SaveRequest`

```go
// AppliesTo specifies the patterns this rule applies to. Required when
// Type is TypeRule; ignored for all other types.
AppliesTo []string `json:"applies_to,omitempty"`

// Severity specifies the enforcement level. Required when Type is
// TypeRule; ignored for all other types. Defaults to SeverityWarn
// when omitted for a rule.
Severity Severity `json:"severity,omitempty"`
```

### 3.4. Cambios en `model.UpdateRequest`

```go
// AppliesTo replaces the applies_to list when non-nil.
// Only valid when the memory is of TypeRule.
AppliesTo *[]string `json:"applies_to,omitempty"`

// Severity replaces the severity when non-nil.
// Only valid when the memory is of TypeRule.
Severity *Severity `json:"severity,omitempty"`
```

### 3.5. Cambios en `model.DefaultImportance` y `model.DefaultDecayRate` (`internal/model/scoring.go`)

```go
// En DefaultImportance:
TypeRule: 0.95, // Rules are near-permanent constraints, ranked above architecture.

// En DefaultDecayRate:
TypeRule: 0.0,  // Rules do not decay — they remain active until explicitly revoked.
```

### 3.6. Nuevos errores sentinela (`internal/model/errors.go`)

```go
// ErrAppliesToRequired is returned when saving a rule without any applies_to patterns.
var ErrAppliesToRequired = errors.New("applies_to is required for rules")

// ErrAppliesToForbidden is returned when a non-rule memory specifies applies_to.
var ErrAppliesToForbidden = errors.New("applies_to is only valid for rules")

// ErrInvalidSeverity is returned when a severity value is not recognised.
var ErrInvalidSeverity = errors.New("invalid severity")

// ErrEmptyPattern is returned when an applies_to entry is an empty string.
var ErrEmptyPattern = errors.New("applies_to patterns must not be empty")
```

---

## 4. Migracion 006

### `internal/db/migrations/006_rule_fields.sql`

```sql
-- 006_rule_fields.sql: Add applies_to and severity columns to memories
-- for the new "rule" memory type (SPEC-001, EPIC-1).
--
-- applies_to: JSON array of pattern strings (globs, tool selectors, negations).
-- severity: enforcement level for rules (info, warn, block).
--
-- Both columns have defaults that make them transparent to existing
-- non-rule memories. The CHECK constraint on severity fires only when
-- a value is explicitly set (the default 'warn' passes validation).

-- UP

ALTER TABLE memories ADD COLUMN applies_to TEXT NOT NULL DEFAULT '[]';
ALTER TABLE memories ADD COLUMN severity   TEXT NOT NULL DEFAULT ''
    CHECK (severity IN ('', 'info', 'warn', 'block'));

-- Index to efficiently list all active rules for a given project.
-- Used by mem_context (SPEC-R2) and the matching engine (SPEC-R3).
CREATE INDEX IF NOT EXISTS idx_memories_rules
    ON memories(project, type)
    WHERE type = 'rule' AND deleted_at IS NULL;

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (6, datetime('now'));
```

### DOWN (rollback)

```sql
-- DOWN
-- SQLite does not support DROP COLUMN before 3.35.0. mneme requires
-- Go 1.25.8 which bundles a recent SQLite. If the embedded SQLite
-- supports it, use ALTER TABLE. Otherwise, use the table-rebuild pattern
-- used by migration 005.

DROP INDEX IF EXISTS idx_memories_rules;
ALTER TABLE memories DROP COLUMN severity;
ALTER TABLE memories DROP COLUMN applies_to;

DELETE FROM schema_version WHERE version = 6;
```

**Nota sobre la migracion DOWN:** SQLite soporta DROP COLUMN desde la version 3.35.0 (2021-03-12). La version embebida en `mattn/go-sqlite3` actual (3.46+) lo soporta. Si por algun motivo no se pudiera, usar la tecnica de rebuild con tabla temporal como se hizo en `005_spec_pk_by_project.sql`.

### Notas de diseno de la migracion

1. **severity default `''` (empty string) en lugar de `'warn'`:** Para memorias no-rule, severity no tiene semantica. Usar `''` permite distinguir "no es una rule" de "es una rule con severity warn". La CHECK admite `''` para backward compat.
2. **Rendimiento:** `ALTER TABLE ADD COLUMN` con un DEFAULT literal es O(1) en SQLite (no reescribe la tabla). En una DB con 100k memorias, la migracion tarda <1ms.
3. **Indice parcial `idx_memories_rules`:** Solo indexa rows donde `type='rule'`. Como las rules seran una fraccion minima del total de memorias, el indice es compacto y no impacta la escritura general.

---

## 5. Contratos de API

### 5.1. MCP — `mem_save` schema actualizado

Cambios en `allTools()` (`internal/mcp/tools.go`), entrada `mem_save`.properties:

```go
"applies_to": map[string]any{
    "type":        "array",
    "description": "Patterns this rule applies to. Required when type is 'rule'. Supports path globs (internal/**/*.go), tool selectors (tool:Edit), combined (tool:Edit+internal/**), negations (!docs/**), and global wildcard (**).",
    "items":       map[string]any{"type": "string"},
},
"severity": map[string]any{
    "type":        "string",
    "description": "Enforcement level for rules. Defaults to 'warn' when type is 'rule'. Ignored for non-rule types.",
    "enum":        []string{"info", "warn", "block"},
},
```

Agregar `"rule"` al enum de `type`:
```go
"enum": []string{
    "decision", "discovery", "bugfix", "pattern",
    "preference", "convention", "architecture", "config",
    "session_summary", "rule",
},
```

#### Ejemplo request: crear una rule

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "mem_save",
    "arguments": {
      "title": "Never use time.Now() directly",
      "content": "Use the injected clock from the service constructor...",
      "type": "rule",
      "scope": "project",
      "topic_key": "rule/no-time-now",
      "applies_to": ["internal/**/*.go", "!internal/**/*_test.go"],
      "severity": "warn",
      "importance": 0.95
    }
  }
}
```

#### Ejemplo response exitosa

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"id\":\"019de100-abcd-7fff-8000-000000000001\",\"action\":\"created\",\"revision_count\":1,\"title\":\"Never use time.Now() directly\",\"topic_key\":\"rule/no-time-now\"}"
      }
    ]
  }
}
```

#### Errores MCP

| Condicion | Code | Ejemplo message |
|-----------|------|-----------------|
| `type=rule` sin `applies_to` | -32602 (Invalid params) | `mcp: handle mem_save: applies_to is required for rules` |
| `type=rule` con `applies_to=[]` (array vacio) | -32602 | `mcp: handle mem_save: applies_to is required for rules` |
| `type=architecture` con `applies_to` no vacio | -32602 | `mcp: handle mem_save: applies_to is only valid for rules` |
| `severity` valor invalido (ej: `"critical"`) | -32602 | `mcp: handle mem_save: invalid severity` |
| `applies_to` contiene string vacio `["", "internal/**"]` | -32602 | `mcp: handle mem_save: applies_to patterns must not be empty` |

El mapeo en `handlers.go:mapServiceError` debe agregar los nuevos errores sentinela al bloque que retorna `CodeInvalidParams`.

### 5.2. HTTP — `POST /v1/memories` schema actualizado

El request body acepta los nuevos campos:

```bash
curl -X POST http://localhost:7437/v1/memories \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Never use time.Now() directly",
    "content": "Use the injected clock...",
    "type": "rule",
    "topic_key": "rule/no-time-now",
    "applies_to": ["internal/**/*.go"],
    "severity": "block"
  }'
```

#### Response 201 Created

```json
{
  "id": "019de100-abcd-7fff-8000-000000000001",
  "action": "created",
  "revision_count": 1,
  "title": "Never use time.Now() directly",
  "topic_key": "rule/no-time-now"
}
```

#### Errores HTTP

| Condicion | HTTP Status | Body |
|-----------|-------------|------|
| `type=rule` sin `applies_to` | 400 Bad Request | `{"error":{"code":"invalid_request","message":"applies_to is required for rules"}}` |
| `type=architecture` con `applies_to` | 400 Bad Request | `{"error":{"code":"invalid_request","message":"applies_to is only valid for rules"}}` |
| `severity` invalida | 400 Bad Request | `{"error":{"code":"invalid_request","message":"invalid severity"}}` |

El mapeo en `server.go:errorStatus` debe agregar los nuevos errores sentinela a la rama que retorna `http.StatusBadRequest`.

### 5.3. CLI — `mneme save` flags nuevos

Nuevos flags en `newSaveCmd()` (`internal/cli/save.go`):

```
--applies-to, -a   stringArray   Patterns this rule applies to (repeatable)
--severity          string        Rule severity: info, warn, block (default "warn")
```

#### Ejemplos

```bash
# Crear una rule de bloqueo
mneme save \
  --title "Never edit vendor/" \
  --content "All vendor changes must go through go mod vendor" \
  --type rule \
  --applies-to "vendor/**" \
  --severity block \
  --topic-key "rule/no-vendor-edits"

# Rule con multiples patterns
mneme save \
  --title "SQL in .sql files only" \
  --content "No inline SQL strings in Go code" \
  --type rule \
  --applies-to "**/*.go" \
  --applies-to "!**/*_test.go" \
  --severity warn
```

#### Exit codes

| Condicion | Exit code |
|-----------|-----------|
| Exito | 0 |
| `--type rule` sin `--applies-to` | 1 (cobra RunE error) |
| `--applies-to` con `--type` distinto de `rule` | 1 |
| `--severity` con valor invalido | 1 |

#### Output exitoso

```
Saved: 019de100-abcd-... (created) — Never edit vendor/
```

(Mismo formato que la linea 88 actual de `save.go`.)

---

## 6. Validacion

La validacion se implementa en el service layer (`service.Save()`) antes de llamar a `store.Upsert()`, siguiendo el patron existente (`internal/service/memory.go:80-101`).

### Truth table

| type | applies_to | severity | Resultado |
|------|-----------|----------|-----------|
| `rule` | `["internal/**"]` | `warn` | OK |
| `rule` | `["internal/**"]` | `""` (omitido) | OK -- default a `warn` |
| `rule` | `["internal/**"]` | `block` | OK |
| `rule` | `["internal/**"]` | `info` | OK |
| `rule` | `["internal/**"]` | `"critical"` | Error: `ErrInvalidSeverity` |
| `rule` | `[]` (vacio) | `warn` | Error: `ErrAppliesToRequired` |
| `rule` | `nil` (omitido) | `warn` | Error: `ErrAppliesToRequired` |
| `rule` | `["", "x"]` | `warn` | Error: `ErrEmptyPattern` |
| `architecture` | `nil` | `""` | OK (caso habitual) |
| `architecture` | `["internal/**"]` | `""` | Error: `ErrAppliesToForbidden` |
| `discovery` | `nil` | `warn` | OK -- severity ignorada para no-rules |
| `decision` | `["x"]` | `""` | Error: `ErrAppliesToForbidden` |

### Pseudocodigo de validacion (en `service.Save()`)

```
si req.Type == "rule":
    si req.AppliesTo es nil o len == 0:
        return ErrAppliesToRequired
    para cada pattern en req.AppliesTo:
        si pattern == "":
            return ErrEmptyPattern
    si req.Severity == "":
        req.Severity = "warn"  // default
    si !req.Severity.Valid():
        return ErrInvalidSeverity
sino:
    si len(req.AppliesTo) > 0:
        return ErrAppliesToForbidden
    // severity se ignora silenciosamente para no-rules
    req.Severity = ""  // normalizar a vacio
```

---

## 7. Store layer — Persistencia

### 7.1. `store.Create()` (`internal/store/memory.go`)

Actualizar el INSERT de `Create()` (linea 48-59) para incluir `applies_to` y `severity`:

- `applies_to` se serializa con `json.Marshal(m.AppliesTo)` si no es nil, o `"[]"` si es nil/vacio.
- `severity` se guarda como string (`string(m.Severity)`).

### 7.2. `scanMemoryRow()` (`internal/store/memory.go:537-587`)

Agregar dos columnas al SELECT y al Scan:
- `applies_to TEXT` -> `json.Unmarshal` en `m.AppliesTo`.
- `severity TEXT` -> `m.Severity = Severity(severityStr)`.

### 7.3. `store.Upsert()` (`internal/store/memory.go:189-248`)

El UPDATE en upsert (linea 221-224) debe incluir `applies_to` y `severity` en los campos actualizados.

### 7.4. `store.Update()` (`internal/store/memory.go:131-182`)

Agregar handling para `req.AppliesTo` y `req.Severity` como campos opcionales (patron nil-check existente).

### 7.5. Todos los SELECT deben ampliarse

Cada query que hace `SELECT id, type, scope, ...` (lineas 102-108, 376-381, etc.) debe incluir las dos columnas nuevas. Esto afecta: `Get`, `List`, `Search`, y cualquier query que use `scanMemoryRow`.

---

## 8. Edge cases

### 8.1. Migracion sobre una DB con miles de memorias existentes

`ALTER TABLE ADD COLUMN` con `DEFAULT` literal es O(1) en SQLite -- no reescribe la tabla. La migracion solo modifica el schema, no toca datos existentes. Las memorias existentes leidas despues de la migracion tendran `applies_to='[]'` y `severity=''`. El `scanMemoryRow` debe manejar `severity=''` como "no es una rule" (ya cubierto por el default).

**Performance budget:** Migration up < 10ms en DB con 100k memorias (es O(1), no depende del tamano).

### 8.2. Rules con applies_to que solo tiene tool selectors (sin path)

Valido. Ejemplo: `["tool:Bash"]` -- la rule aplica a toda invocacion de Bash, sin importar el path. La validacion de SPEC-001 solo verifica que el array no este vacio y que las entradas no sean strings vacios. La interpretacion semantica del pattern es de SPEC-R3.

### 8.3. Rules con applies_to vacio

Invalido. `ErrAppliesToRequired` se retorna. Una rule sin scope de aplicacion no tiene sentido semantico -- el usuario debe especificar al menos `["**"]` para una rule global.

### 8.4. Sync export/import de una rule (forward + backward compat)

**Forward compat (mneme con SPEC-001 exporta, mneme sin SPEC-001 importa):**
- El JSONL incluira `"applies_to":["internal/**"],"severity":"warn"`. El importador viejo (sin las columnas) hara `json.Unmarshal` en `model.Memory` -- los campos extra se ignoran silenciosamente por `encoding/json`. El INSERT fallara si las columnas no existen en la DB destino. 
- **Mitigacion:** El importador ya no hace schema version check (discovery `sync-gaps`). Se recomienda documentar que importar entre versiones distintas puede fallar y que ambos lados deben correr la misma version. Este es un problema pre-existente, no introducido por SPEC-001.

**Backward compat (mneme sin SPEC-001 exporta, mneme con SPEC-001 importa):**
- El JSONL no tendra los campos `applies_to` y `severity`. `json.Unmarshal` los dejara como zero values (`nil` y `""`). El INSERT usara los defaults de la columna (`'[]'` y `''`). Funciona sin cambios.

### 8.5. Rule con topic_key duplicado vs upsert

Sigue el mecanismo estandar de upsert (`store.Upsert`, lineas 189-248). Si existe un memory con el mismo `(topic_key, project, scope)`, se actualiza. Esto permite que el agente actualice rules existentes (ej: cambiar severity de warn a block) sin duplicar.

El upsert actualiza `title`, `content`, `importance`, `type` (linea 221-224). **Debe extenderse para tambien actualizar `applies_to` y `severity`** en el caso de upsert.

### 8.6. mem_update sobre una rule -- cambiar solo severity

`UpdateRequest` con `Severity: &SeverityBlock` y demas campos nil. El store aplica partial update (patron existente en `store.Update()`, lineas 131-182). Solo se escribe la columna severity. Funciona con el mismo patron de nil-check.

### 8.7. mem_get devolviendo una rule

El response de `mem_get` devuelve el `model.Memory` completo serializado como JSON. Los campos `applies_to` y `severity` aparecen en el JSON solo cuando son no-vacios (tag `omitempty`). Para una rule, siempre apareceran. Para non-rules, no.

### 8.8. FTS5 sync triggers

Los triggers FTS5 (migration 001, lineas 47-62) sincronizan `title`, `content`, `type`, `topic_key`. No necesitan cambio -- `applies_to` y `severity` no son campos buscables por texto (las rules se buscan por type, no por pattern).

---

## 9. Plan de implementacion

Pasos ordenados, cada uno commit-able independientemente:

| # | Commit | Archivos | Descripcion |
|---|--------|----------|-------------|
| 1 | `feat(model): add TypeRule, Severity, and rule-specific fields` | `internal/model/memory.go`, `internal/model/scoring.go`, `internal/model/errors.go`, `internal/model/memory_test.go` | Nuevo tipo `rule`, tipo `Severity`, campos `AppliesTo`/`Severity` en `Memory` y `SaveRequest`/`UpdateRequest`, defaults de importance/decay, errores sentinela, tests unitarios. |
| 2 | `feat(db): add migration 006 for rule fields` | `internal/db/migrations/006_rule_fields.sql`, `internal/db/migrate.go` (si requiere pre-flight) | DDL up con ALTER TABLE, indice parcial. Schema version 6. |
| 3 | `feat(store): persist and load applies_to and severity` | `internal/store/memory.go`, `internal/store/memory_test.go` | Actualizar CREATE/INSERT, scanMemoryRow, Upsert update, Update parcial. Tests roundtrip con SQLite in-memory. |
| 4 | `feat(service): validate rule fields in Save` | `internal/service/memory.go`, `internal/service/memory_test.go` (si existe) | Logica de validacion en `Save()`, defaults de severity, mapeo de nuevos campos al `model.Memory`. |
| 5 | `feat(mcp): add applies_to and severity to mem_save schema` | `internal/mcp/tools.go`, `internal/mcp/handlers.go`, `internal/mcp/handlers_test.go` (si existe) | Schema JSON actualizado, error mapping de nuevos sentinels. |
| 6 | `feat(http): add rule fields to POST /v1/memories` | `internal/http/server.go`, `internal/http/server_test.go` (si existe) | Error mapping actualizado. El request body ya se deserializa en `model.SaveRequest`, que ahora tiene los campos nuevos. |
| 7 | `feat(cli): add --applies-to and --severity flags to save` | `internal/cli/save.go` | Nuevos flags, mapeo a `SaveRequest`. |

---

## 10. Tests requeridos

### model (unit)
- `TestMemoryTypeValid` -- actualizar para incluir `TypeRule` como caso valido.
- `TestAllMemoryTypes` -- actualizar `wantLen` de 9 a 10.
- `TestDefaultImportanceCoverage` -- pasa automaticamente si se agrega la entrada.
- `TestDefaultDecayRateCoverage` -- **modificar** la asercion para aceptar `val >= 0.0` (ver D4).
- `TestSeverityValid` -- table-driven: `info`=true, `warn`=true, `block`=true, `""`=false, `"critical"`=false.
- `TestSaveRequest_RuleValidation` (nuevo, en service o model):
  - `type=rule` + applies_to valido -> OK
  - `type=rule` + applies_to nil -> error
  - `type=rule` + applies_to `[]` -> error
  - `type=rule` + applies_to `[""]` -> error
  - `type=rule` + severity omitida -> default warn
  - `type=rule` + severity invalida -> error
  - `type=architecture` + applies_to no vacio -> error

### db (migration)
- `TestMigration006_Up` -- aplicar migration, verificar columnas existen, insertar rule, verificar roundtrip.
- `TestMigration006_Down` -- aplicar up, insertar datos, aplicar down, verificar columnas removidas y datos originales intactos.
- `TestMigration006_CheckConstraint` -- insertar con severity invalida, verificar que SQLite rechaza.
- `TestMigration006_Performance` -- medir tiempo de migracion en DB con 1k memorias, verificar < 100ms.

### store (integration, SQLite in-memory)
- `TestStore_CreateRule_Roundtrip` -- crear rule, leer, verificar applies_to y severity.
- `TestStore_UpsertRule_UpdatesAppliesTo` -- upsert con topic_key, cambiar applies_to, verificar.
- `TestStore_UpdateRule_PartialSeverity` -- update solo severity via UpdateRequest.
- `TestStore_ListRules_FilterByType` -- crear rules y no-rules, listar con type=rule, verificar solo rules.
- `TestStore_AppliesTo_JSONMarshalling` -- guardar patterns complejos (`tool:Edit+internal/**`, `!docs/**`), leer, verificar igualdad.

### service
- `TestService_Save_RuleValidation` -- all cases from truth table.
- `TestService_Save_RuleDefaults` -- severity default, importance default, decay_rate default.

### mcp
- `TestMCP_MemSave_RuleCreated` -- JSON-RPC request con rule, verificar response.
- `TestMCP_MemSave_RuleMissingAppliesTo` -- verificar error code -32602.
- `TestMCP_MemSave_NonRuleWithAppliesTo` -- verificar error code -32602.
- `TestMCP_ToolSchema_IncludesRuleFields` -- verificar que allTools() incluye applies_to y severity en mem_save.

### http
- `TestHTTP_PostMemories_RuleCreated` -- POST con body rule, verificar 201.
- `TestHTTP_PostMemories_RuleMissingAppliesTo` -- verificar 400.

### cli
- `TestCLI_Save_RuleFlags` -- integration test con flags --applies-to y --severity.

---

## 11. Criterios de aceptacion

- [ ] `mneme save --type rule --applies-to 'internal/**/*.go' --severity block --title "test" --content "test"` persiste correctamente y devuelve `Saved: ... (created)`.
- [ ] `SELECT applies_to, severity FROM memories WHERE type='rule'` devuelve `'["internal/**/*.go"]'` y `'block'`.
- [ ] `mneme save --type rule --title "test" --content "test"` (sin --applies-to) falla con exit code 1 y mensaje claro.
- [ ] `mneme save --type architecture --applies-to 'x' --title "test" --content "test"` falla con exit code 1 y mensaje claro.
- [ ] `make test` pasa con coverage >85% en `internal/model` y `internal/store`.
- [ ] `golangci-lint run` con cero warnings.

**Performance budgets:**
- Validacion de un SaveRequest con type=rule: < 1ms (es pura logica in-memory).
- Migration 006 up en DB con 100k memorias: < 100ms (ALTER TABLE ADD COLUMN con DEFAULT es O(1)).

---

## 12. Open questions / pushbacks

Ninguno. El backlog refinado es suficientemente claro y las decisiones de diseno se pudieron tomar sin ambiguedad basandose en el codigo existente. Los unicos puntos de atencion son:

1. **D4 (decay_rate=0):** Requiere un cambio menor en un test existente (`TestDefaultDecayRateCoverage`). No es un bloqueo -- es un cambio esperado y justificado.
2. **Sync forward compat:** El problema de importar entre schema versions distintas es pre-existente (ver `discovery/sync-gaps`). SPEC-001 no lo empeora pero tampoco lo resuelve. Documentar en SPEC-D4.

---

## Scope explicitamente fuera

- Inyeccion obligatoria en mem_context -> SPEC-R2
- Matching engine + hook pre-tool-use -> SPEC-R3
- CLI `mneme rule add/list/test` -> SPEC-R4
- Documentacion docs/RULES.md -> SPEC-D4
- Schema version check en sync -> backlog futuro

---

## Dependencias

- Ninguna. SPEC-001 no depende de specs anteriores.
- SPEC-R2, R3, R4 dependen de SPEC-001.
