# SPEC-002 — mem_context inyeccion obligatoria de rules

| Campo         | Valor                                                          |
|---------------|----------------------------------------------------------------|
| **ID**        | SPEC-002                                                       |
| **Epic**      | EPIC-1 — Rules como ciudadanos de primera clase                |
| **Backlog**   | BL-002                                                         |
| **Estado**    | speccing -> specced                                            |
| **Owner**     | architect                                                      |
| **Fecha**     | 2026-04-30                                                     |
| **Deps**      | SPEC-001 (completada)                                          |
| **Memorias**  | `roadmap/v2-master-plan`, `spec/SPEC-001-rule-type-design`, `spec/SPEC-001-implementation-notes`, `spec/SPEC-001-qa-result`, `architecture/memory-model`, `architecture/interfaces`, `architecture/scoring-formulas`, `config/runtime-config` |

---

## 1. Contexto y motivacion

El dolor principal: el agente no consulta rules porque `mem_context` las trata como memorias comunes. El scoring por BM25 + vector + effective importance puede o no traerlas dependiendo del `focus` query y de cuantas memorias compitan por el budget. Una rule con titulo "SQL en archivos .sql unicamente" puede quedarse fuera del context window si el agente pregunta por "authentication" como focus.

SPEC-001 creo el tipo `rule` con `applies_to`, `severity`, importance=0.95 y decay_rate=0. Pero **no garantizo** que las rules aparezcan en el context. Hoy `mem_context` (`internal/service/context.go:22-214`) arma un bundle priorizando por effective importance con boost de architecture (x1.5) y focus (+0.3), empaquetando en el token budget general. Las rules compiten con todas las demas memorias por ese budget.

SPEC-002 resuelve la primera mitad del problema: **inyeccion obligatoria** de rules en `mem_context` con un budget dedicado y separado. La segunda mitad (enforcement activo via hooks) sera SPEC-R3 (BL-003).

---

## 2. Decisiones de diseno

### D1. Budget separado para rules vs. aumentar el default_budget

**Decision:** Nuevo campo `RulesBudget int` en `ContextConfig` con default 1500 tokens. El rules budget es independiente del `DefaultBudget` (4000) y se suma al total.

**Rationale:**
- Si simplemente aumentamos `DefaultBudget` de 4000 a 5500, las rules seguirian compitiendo con las demas memorias en el scoring. Un proyecto con 50 memorias de architecture (importance 0.9, boost x1.5 = eff 1.35) desplazaria facilmente rules (importance 0.95, sin boost especial = eff 0.95) del ranking.
- Un budget dedicado garantiza que las rules **siempre** tienen espacio reservado, independientemente del volumen de memorias generales. Es un contrato no negociable: si una rule existe y esta activa, aparece.
- El costo adicional en tokens es predecible (1500 tokens fijos max) y pequeno comparado con el context window tipico de los LLMs (128k-200k tokens).
- Cuando `RulesBudget=0`, la seccion de rules se omite completamente (toggle implicito sin necesidad de un flag booleano).
- El patron de "budget dedicado por seccion" ya existe conceptualmente en `GetContext`: `LastSession` es exempt del budget general y se deduce por separado (`context.go:176-183`).

### D2. Orden de sort para truncar rules: severity desc, effective_importance desc, updated_at desc

**Decision:** Las rules se ordenan por `severityOrder(severity) DESC, effectiveImportance DESC, updated_at DESC` al empaquetar. Cuando el rules budget se agota, las rules de menor severidad y menor importancia son las primeras en truncarse.

**Rationale:**
- `block` rules son restricciones duras: si el agente no las ve, puede ejecutar una accion prohibida. Deben tener prioridad absoluta.
- `warn` rules son el caso comun: alertas explicitas pero no bloqueantes.
- `info` rules son advisory: utiles pero prescindibles si el budget es ajustado.
- Dentro de la misma severidad, `effectiveImportance` (que para rules con decay_rate=0 es siempre la importance original) ordena por la prioridad asignada por el usuario.
- `updated_at DESC` como tiebreaker final: reglas actualizadas mas recientemente tienen prioridad.
- No usamos el scoring general (BM25/vector/focus boost) para rules porque las rules **no dependen del focus**: son restricciones que aplican independientemente de lo que el agente este haciendo. El focus bias es para la seccion de memorias generales.

### D3. Dedup post-scoring vs. pre-scoring

**Decision:** La deduplicacion de rules que tambien aparecen en el scoring general se hace **despues** de que ambas secciones estan empaquetadas. Es decir: primero se empaquetan las rules (budget separado), luego se empaquetan las memorias generales (budget general), y finalmente se eliminan de las memorias generales cualquier memory cuyo ID ya aparezca en la seccion de rules.

**Rationale:**
- Pre-scoring (excluir rules de los candidates generales antes del scoring) es tentador pero romperia la semantica del campo `TotalAvailable` y cambiaria el comportamiento cuando una rule tiene alta relevancia por BM25 para el focus actual. Con dedup post, si una rule matchea el focus, igualmente esta en la seccion de rules (por inyeccion obligatoria), y su slot en el budget general se libera para otra memoria. El agente no pierde informacion.
- El costo de un dedup por ID set es O(n) con n = |packed_memories|, negligible.
- Post-scoring preserva backward compat: si `RulesBudget=0`, ninguna rule se inyecta, y las rules compiten normalmente en el scoring general (comportamiento pre-SPEC-002).

### D4. Incluir rules globales del usuario en cada proyecto

**Decision:** `LoadActiveRules` carga rules de **ambos** stores (project + global) cuando `IncludeGlobal` es true (default). Las rules globales pasan el mismo filtro que las memorias globales: `importance >= GlobalMinImportance` (0.7 por default). Dado que `DefaultImportance[TypeRule] = 0.95 > 0.7`, todas las rules globales pasan por default.

**Rationale:**
- Un usuario puede tener rules globales como "nunca uses `time.Now()` directo" o "prefiere `context.Context` como primer parametro". Estas aplican a todos sus proyectos.
- La infra multi-DB ya esta en `GetContext` (`context.go:46-59`): lee de `svc.globalStore` y filtra por `GlobalMinImportance`. Seguimos el mismo patron.
- No hay ambiguedad de scope: las rules globales vienen de `~/.mneme/global.db` y las de proyecto de `~/.mneme/projects/<slug>.db`. No se mezclan DBs.

### D5. Formato de renderizado de rules en el bundle (markdown)

**Decision:** Las rules se renderizan como una seccion markdown dedicada `## Active Rules` antes de `## Loaded Memories`, con este formato por regla:

```
## Active Rules (N rules, ~T tokens)

### [SEVERITY] Title
Content...
_Applies to: pattern1, pattern2_

---
```

**Rationale:**
- La seccion `## Active Rules` al inicio del bundle (despues de `## Last Session` si existe) asegura que el agente las vea **primero**. Los LLMs atienden mas a lo que esta al principio del contexto.
- El tag `[SEVERITY]` (ej: `[BLOCK]`, `[WARN]`, `[INFO]`) en el heading es visual y parseable. El agente puede grep por `[BLOCK]` para las restricciones duras.
- La linea `_Applies to:_` da contexto de scope sin ocupar mucho espacio. Es italica para distinguirla visualmente del content.
- El separador `---` entre rules es necesario para que el agente no confunda el content de una rule con el titulo de la siguiente.

### D6. Manejo cuando rules_budget=0

**Decision:** Cuando `RulesBudget=0`:
- `LoadActiveRules` no se ejecuta.
- No se renderiza la seccion `## Active Rules`.
- Las rules participan en el scoring general como cualquier otra memoria (comportamiento pre-SPEC-002).
- El response no incluye los campos `rules_count`, `rules_tokens`, `rules_truncated` (o los incluye con valor 0).

**Rationale:** `RulesBudget=0` es el toggle off implicito. No requiere un flag separado `IncludeRules bool`. Es coherente con el patron de `DefaultBudget`: un budget de 0 significa "no uses esta seccion". Esto permite rollback a comportamiento pre-SPEC-002 cambiando una sola linea de config.

---

## 3. Modelo de datos

### 3.1. No hay cambios al schema SQL

SPEC-001 ya creo las columnas `applies_to` y `severity`, y el indice parcial `idx_memories_rules` (`internal/db/migrations/006_rule_fields.sql`). SPEC-002 no requiere migracion.

### 3.2. Cambios en `config.ContextConfig` (`internal/config/config.go:142-157`)

```go
// ContextConfig controls how mneme assembles the context window injection
// that is sent back to the agent before each session.
type ContextConfig struct {
    // DefaultBudget is the maximum number of tokens allocated for injected
    // memories when the caller does not supply an explicit budget.
    DefaultBudget int `toml:"default_budget"`

    // RulesBudget is the maximum number of tokens reserved for rule-type
    // memories in the context bundle. Rules are packed before general
    // memories and use a dedicated budget so they are always present.
    // Set to 0 to disable rule injection (rules compete in general scoring).
    // Default: 1500.
    RulesBudget int `toml:"rules_budget"`

    // IncludeGlobal determines whether global-scope memories are mixed into
    // project-scoped context injections.
    IncludeGlobal bool `toml:"include_global"`

    // GlobalMinImportance is the minimum importance score a global memory
    // must have to be included in project context injections.
    // Only evaluated when IncludeGlobal is true.
    GlobalMinImportance float64 `toml:"global_min_importance"`
}
```

### 3.3. Default en `config.Default()` (`internal/config/config.go:226-229`)

```go
Context: ContextConfig{
    DefaultBudget:       4000,
    RulesBudget:         1500,
    IncludeGlobal:       true,
    GlobalMinImportance: 0.7,
},
```

### 3.4. Env override en `config.Load()` (`internal/config/config.go:300-310`)

Agregar:

```go
if v := os.Getenv("MNEME_RULES_BUDGET"); v != "" {
    n, err := strconv.Atoi(v)
    if err == nil {
        cfg.Context.RulesBudget = n
    }
}
```

Import `strconv` si no esta presente (ya se usa en `http/server.go` pero no en `config.go`).

### 3.5. Validacion en `config.Validate()` (`internal/config/config.go:347-375`)

Agregar:

```go
if c.Context.RulesBudget < 0 {
    return errors.New("context.rules_budget must be >= 0")
}
```

### 3.6. Cambios en `model.ContextRequest` (`internal/model/search.go:69-80`)

No requiere cambios. El `RulesBudget` es un parametro de config del servidor, no del request del agente. El agente controla `budget` (tokens generales) y `focus` (sesgo de retrieval). Las rules se inyectan siempre segun la config, no segun el request.

**Rationale:** Las rules son restricciones del sistema, no decisiones del agente. Permitir que el agente pida `rules_budget=0` en su request seria un bypass de seguridad.

### 3.7. Cambios en `model.ContextResponse` (`internal/model/search.go:82-104`)

```go
// ContextResponse is the curated memory bundle returned by mem_context.
// It is designed to be injected directly into an agent's context window.
type ContextResponse struct {
    // Project is the project slug for which context was built.
    Project string `json:"project"`

    // Memories is the ordered set of memories selected within Budget.
    Memories []Memory `json:"memories"`

    // TokenEstimate is the estimated token count of all returned content
    // (rules + memories + last session).
    TokenEstimate int `json:"token_estimate"`

    // TotalAvailable is the total number of active memories for this project,
    // before budget filtering. Tells the agent how much was left out.
    TotalAvailable int `json:"total_available"`

    // Included is the count of memories (non-rule) actually returned.
    Included int `json:"included"`

    // LastSession is the most recent session summary for this project, nil if none.
    LastSession *SessionSummary `json:"last_session,omitempty"`

    // RulesCount is the number of rule-type memories included in the bundle.
    // Zero when no rules exist or rules_budget is 0.
    RulesCount int `json:"rules_count"`

    // RulesTokens is the estimated token count consumed by the rules section.
    RulesTokens int `json:"rules_tokens"`

    // RulesTruncated is the number of rules that could not fit in the rules budget.
    RulesTruncated int `json:"rules_truncated"`

    // Rules is the ordered set of rule memories included in the bundle.
    // Separated from Memories to allow callers to render them distinctly.
    Rules []Memory `json:"rules,omitempty"`
}
```

**Nota:** `Rules` es un campo separado de `Memories` para que los tres frontends (MCP, HTTP, CLI) puedan renderizarlos de forma distinta (la seccion `## Active Rules` vs `## Loaded Memories`). No mezclar rules en `Memories` evita que el consumidor tenga que filtrar por type.

---

## 4. Algoritmo de packing extendido

### 4.1. Pseudocodigo del nuevo flujo de `GetContext` (`internal/service/context.go`)

```
func (svc *MemoryService) Context(ctx, req) -> (*ContextResponse, error):

  // 0. Defaults
  project = req.Project || svc.project
  budget = req.Budget || svc.config.Context.DefaultBudget
  rulesBudget = svc.config.Context.RulesBudget

  // ── FASE 1: Rules (budget dedicado) ──────────────────────────

  var packedRules []Memory
  var ruleTokens, rulesTruncated int
  ruleIDs := set{}

  if rulesBudget > 0:
    // 1a. Cargar rules activas de ambos stores
    projectRules = svc.projectStore.List(ctx, ListOptions{
        Project: project,
        Type: TypeRule,
        Scope: ScopeProject,
        OrderBy: "importance DESC",
        Limit: 200,  // safety cap
    })

    allRules = append([], projectRules...)

    if svc.config.Context.IncludeGlobal:
      globalRules = svc.globalStore.List(ctx, ListOptions{
          Type: TypeRule,
          Scope: ScopeGlobal,
          OrderBy: "importance DESC",
          Limit: 200,
      })
      for _, r := range globalRules:
        if r.Importance >= svc.config.Context.GlobalMinImportance:
          allRules = append(allRules, r)

    // 1b. Sort rules por severity desc, effImp desc, updated_at desc
    sort(allRules, func(a, b):
      sa, sb = severityOrder(a.Severity), severityOrder(b.Severity)
      if sa != sb: return sa > sb
      ea = EffectiveImportance(a.Importance, a.DecayRate, a.lastAccessed)
      eb = EffectiveImportance(b.Importance, b.DecayRate, b.lastAccessed)
      if ea != eb: return ea > eb
      return a.UpdatedAt.After(b.UpdatedAt)
    )

    // 1c. Pack rules en el rules budget
    for _, rule := range allRules:
      cost = estimateTokens(rule.Title) + estimateTokens(rule.Content)
      if ruleTokens + cost > rulesBudget:
        rulesTruncated++
        continue  // seguir intentando rules mas chicas
      packedRules = append(packedRules, *rule)
      ruleTokens += cost
      ruleIDs.add(rule.ID)

  // ── FASE 2: Scoring general (budget estandar, sin cambios) ──

  // 2a. Collect candidates (igual que hoy, context.go:31-59)
  projectMemories = svc.projectStore.List(...)
  candidates = projectMemories
  if IncludeGlobal: candidates += globalMemories (filtered by GlobalMinImportance)

  totalAvailable = len(candidates)

  // 2b. Focus boost (igual que hoy, context.go:68-118)
  focusIDs = buildFocusSet(req.Focus)

  // 2c. Score candidates (igual que hoy, context.go:123-152)
  scoredCandidates = scoreAll(candidates, focusIDs)
  sort(scoredCandidates, by score DESC)

  // 2d. Last session (igual que hoy, context.go:155-183)
  lastSession = loadLastSession()
  tokenBudget = budget
  if lastSession != nil:
    tokenBudget -= estimateTokens(lastSession.Summary)

  // 2e. Pack general memories (con dedup)
  packed = []
  tokenUsed = 0
  for _, sc := range scoredCandidates:
    if sc.mem.Type == TypeSessionSummary: continue  // handled via LastSession
    if ruleIDs.contains(sc.mem.ID): continue         // ← DEDUP: ya incluida en rules
    cost = estimateTokens(sc.mem.Title) + estimateTokens(sc.mem.Content)
    if tokenUsed + cost > tokenBudget: break
    packed = append(packed, *sc.mem)
    tokenUsed += cost

  // ── FASE 3: Response ────────────────────────────────────────

  totalTokens = ruleTokens + tokenUsed
  if lastSession != nil:
    totalTokens += estimateTokens(lastSession.Summary)

  return &ContextResponse{
    Project:        project,
    Memories:       packed,
    Rules:          packedRules,
    TokenEstimate:  totalTokens,
    TotalAvailable: totalAvailable,
    Included:       len(packed),
    LastSession:    lastSession,
    RulesCount:     len(packedRules),
    RulesTokens:    ruleTokens,
    RulesTruncated: rulesTruncated,
  }
```

### 4.2. Funcion helper `severityOrder`

```go
// severityOrder maps a Severity to a numeric rank for sorting.
// Higher values have higher priority in the rules packing order.
func severityOrder(s model.Severity) int {
    switch s {
    case model.SeverityBlock:
        return 3
    case model.SeverityWarn:
        return 2
    case model.SeverityInfo:
        return 1
    default:
        return 0
    }
}
```

Ubicacion: `internal/service/context.go` (junto a `estimateTokens`).

### 4.3. Truncamiento de rules grandes

En el paso 1c, cuando una rule individual es mas grande que el `rulesBudget` restante, se usa `continue` (intenta la siguiente rule mas chica) en lugar de `break`. Esto asegura que una rule de 2000 tokens no bloquee la inclusion de 5 rules de 200 tokens.

**Una rule que excede el rulesBudget completo (ej: content de 5000 tokens con rulesBudget=1500):**
- Se cuenta como `rulesTruncated` y no se incluye.
- No se trunca parcialmente (no cortamos el content a la mitad). El content de una rule es una instruccion atomica; cortarla podria dar instrucciones incompletas o daninas.
- Si esto ocurre frecuentemente, el usuario debe aumentar `rules_budget` o hacer rules mas concisas.

---

## 5. Contratos de API

### 5.1. MCP — `mem_context` response actualizado

No hay cambios al input schema de `mem_context` (los campos `project`, `budget`, `focus` siguen igual).

El response JSON dentro del `ToolCallResult.content[0].text` se extiende con los campos nuevos.

#### Ejemplo JSON-RPC completo (con rules)

Request:
```json
{
  "jsonrpc": "2.0",
  "id": 42,
  "method": "tools/call",
  "params": {
    "name": "mem_context",
    "arguments": {
      "focus": "authentication"
    }
  }
}
```

Response:
```json
{
  "jsonrpc": "2.0",
  "id": 42,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"project\":\"wirvii/mneme\",\"memories\":[{\"id\":\"019de200-...\",\"type\":\"architecture\",\"scope\":\"project\",\"title\":\"Auth model\",\"content\":\"JWT + refresh tokens...\",\"importance\":0.9,\"confidence\":0.8,\"access_count\":5,\"decay_rate\":0.005,\"revision_count\":2}],\"rules\":[{\"id\":\"019de100-...\",\"type\":\"rule\",\"scope\":\"project\",\"title\":\"Never store plain-text passwords\",\"content\":\"Always use bcrypt with cost >= 12...\",\"importance\":0.95,\"confidence\":0.8,\"access_count\":0,\"decay_rate\":0,\"revision_count\":0,\"applies_to\":[\"internal/**/*.go\"],\"severity\":\"block\"}],\"token_estimate\":1850,\"total_available\":35,\"included\":8,\"rules_count\":1,\"rules_tokens\":120,\"rules_truncated\":0,\"last_session\":{\"id\":\"019de050-...\",\"summary\":\"## Goal\\nImplement auth...\",\"ended_at\":\"2026-04-30T18:00:00Z\"}}"
      }
    ]
  }
}
```

Parsed content:
```json
{
  "project": "wirvii/mneme",
  "memories": [
    {
      "id": "019de200-...",
      "type": "architecture",
      "title": "Auth model",
      "content": "JWT + refresh tokens...",
      "importance": 0.9,
      ...
    }
  ],
  "rules": [
    {
      "id": "019de100-...",
      "type": "rule",
      "title": "Never store plain-text passwords",
      "content": "Always use bcrypt with cost >= 12...",
      "importance": 0.95,
      "applies_to": ["internal/**/*.go"],
      "severity": "block"
    }
  ],
  "token_estimate": 1850,
  "total_available": 35,
  "included": 8,
  "rules_count": 1,
  "rules_tokens": 120,
  "rules_truncated": 0,
  "last_session": {
    "id": "019de050-...",
    "summary": "## Goal\nImplement auth...",
    "ended_at": "2026-04-30T18:00:00Z"
  }
}
```

#### Ejemplo sin rules

Cuando no hay rules en la DB o `rules_budget=0`:
```json
{
  "project": "wirvii/mneme",
  "memories": [...],
  "token_estimate": 3200,
  "total_available": 35,
  "included": 12,
  "rules_count": 0,
  "rules_tokens": 0,
  "rules_truncated": 0
}
```

Nota: `rules` omitido (tag `omitempty` en `[]Memory`) cuando es nil/vacio.

### 5.2. HTTP — `GET /v1/memories/context` response actualizado

No hay cambios a los query params: `project`, `budget`, `focus`.

```bash
curl -s "http://localhost:7437/v1/memories/context?project=wirvii/mneme&focus=auth" | jq .
```

Response 200 OK:
```json
{
  "project": "wirvii/mneme",
  "memories": [
    {
      "id": "019de200-...",
      "type": "architecture",
      "title": "Auth model",
      "content": "JWT + refresh tokens..."
    }
  ],
  "rules": [
    {
      "id": "019de100-...",
      "type": "rule",
      "title": "Never store plain-text passwords",
      "content": "Always use bcrypt with cost >= 12...",
      "applies_to": ["internal/**/*.go"],
      "severity": "block"
    }
  ],
  "token_estimate": 1850,
  "total_available": 35,
  "included": 8,
  "rules_count": 1,
  "rules_tokens": 120,
  "rules_truncated": 0,
  "last_session": {
    "id": "019de050-...",
    "summary": "## Goal\nImplement auth...",
    "ended_at": "2026-04-30T18:00:00Z"
  }
}
```

No hay cambios a `handleContext` (`internal/http/server.go:368-395`) ni a `errorStatus` — el handler simplemente serializa el `*model.ContextResponse` que ya contiene los campos nuevos.

### 5.3. CLI — `mneme hook session-start` output actualizado

El hook `printContextHook` (`internal/cli/hook.go:91-118`) se extiende para renderizar `## Active Rules` antes de `## Loaded Memories`.

#### Mockup del output

```
<!-- mneme:context:start -->
# mneme — Session Context

**Project:** wirvii/mneme

## Last Session

_Ended: Wed, 30 Apr 2026 18:00:00 UTC_

## Goal
Implement authentication module...

## Active Rules (2 rules, ~250 tokens)

### [BLOCK] Never store plain-text passwords
Always use bcrypt with cost >= 12. Never SHA256 or MD5 for password hashing.
_Applies to: internal/**/*.go_

---

### [WARN] SQL in .sql files only
No inline SQL strings in Go code. Use sqlc-generated queries.
_Applies to: **/*.go, !**/*_test.go_

---

## Loaded Memories (8 of 35)

### [architecture] Auth model

JWT + refresh tokens...

### [decision] Use bcrypt cost 12

...

<!-- mneme:context:end -->
```

#### Cambios en `printContextHook`

Actualizar `printContextHook` para recibir `*model.ContextResponse` (que ya tiene `resp.Rules`) y renderizar la seccion. Pseudocodigo:

```go
// After Last Session, before Loaded Memories:
if len(resp.Rules) > 0 {
    fmt.Fprintf(w, "## Active Rules (%d rules, ~%d tokens)\n\n",
        resp.RulesCount, resp.RulesTokens)
    for _, r := range resp.Rules {
        fmt.Fprintf(w, "### [%s] %s\n", strings.ToUpper(string(r.Severity)), r.Title)
        fmt.Fprintf(w, "%s\n", r.Content)
        if len(r.AppliesTo) > 0 {
            fmt.Fprintf(w, "_Applies to: %s_\n", strings.Join(r.AppliesTo, ", "))
        }
        fmt.Fprintf(w, "\n---\n\n")
    }
    if resp.RulesTruncated > 0 {
        fmt.Fprintf(w, "_(%d rules truncated — increase rules_budget in config)_\n\n",
            resp.RulesTruncated)
    }
}
```

---

## 6. Configuracion

### 6.1. TOML defaults exactos

```toml
[context]
default_budget = 4000       # tokens para memorias generales
rules_budget = 1500          # tokens reservados para rules (0 = disabled)
include_global = true
global_min_importance = 0.7
```

### 6.2. Env override

| Variable | Maps to | Validation |
|----------|---------|------------|
| `MNEME_RULES_BUDGET` | `config.Context.RulesBudget` | Parse int, must be >= 0 |

### 6.3. Validacion

En `config.Validate()`:

```go
if c.Context.RulesBudget < 0 {
    return errors.New("context.rules_budget must be >= 0")
}
```

`RulesBudget=0` es valido (toggle off).

---

## 7. Edge cases

### 7.1. Sin rules en DB

Output identico al pre-SPEC-002. El response incluye `rules_count: 0`, `rules_tokens: 0`, `rules_truncated: 0` y `rules` esta ausente (omitempty). La seccion `## Active Rules` no se renderiza en la CLI. No hay regresion.

### 7.2. rules_budget=0

Toggle off: `LoadActiveRules` no se ejecuta, no se renderiza la seccion. Las rules participan en el scoring general como cualquier otra memoria (si su effective importance es alta, pueden aparecer en `Memories`). Este es el comportamiento pre-SPEC-002.

### 7.3. Una rule pesa mas que rules_budget completo

**Decision:** Se omite (no se trunca parcialmente). Se cuenta en `rules_truncated`. El agente ve `rules_truncated > 0` en el response y puede alertar al usuario.

**Justificacion:** Truncar el content de una rule es peligroso. "Always use bcrypt with cost >= 12. Never SHA" es peor que no mostrar la rule. Las rules deben ser concisas por diseno. Si un usuario tiene una rule de 5000 tokens, debe refactorizarla en multiples rules mas especificas o aumentar `rules_budget`.

**Nota sobre el algoritmo:** El packing usa `continue` (no `break`) al encontrar una rule demasiado grande, por lo que rules mas pequenas que si caben se siguen incluyendo.

### 7.4. Misma rule matchea por BM25 (focus) Y esta en rules section

**Dedup por ID:** En la fase 2e del packing general, antes de agregar una memoria al `packed`, se chequea `ruleIDs.contains(sc.mem.ID)`. Si la rule ya fue incluida en la seccion dedicada, se salta. El token budget general no se desperdicia en la duplicacion.

### 7.5. Rule con scope=global de usuario distinto

No aplica en la arquitectura actual. mneme es single-user: `~/.mneme/global.db` pertenece al usuario que corre el binario. No hay concepto de "usuario distinto" en el global store. Las rules globales son del usuario actual, punto.

Si en el futuro se soporta multi-user (ej: via sync import de rules de otro usuario), el scope `org` seria el mecanismo adecuado. Pero eso esta fuera de scope de SPEC-002.

### 7.6. Performance: muchas rules (>50)

**Target:** `LoadActiveRules` (project + global) < 5ms en proyecto tipico (< 20 rules). Para un caso extremo de 200 rules:
- La query usa el indice parcial `idx_memories_rules` (`WHERE type = 'rule' AND deleted_at IS NULL`) creado en SPEC-001 migration 006. Lookup O(log n).
- El sort de 200 reglas por 3 criterios es O(n log n) con n=200, ~microsegundos.
- El packing loop es O(n) con n=200.
- Estimacion total para 200 rules: < 10ms.

**Render < 2ms:** La serializacion de los campos nuevos en JSON es constante por regla (~1us). Para 50 rules empaquetadas: < 1ms.

### 7.7. ContextRequest con budget explicito muy pequeno

Si el agente pasa `budget: 500` (menor que el token cost de las rules section), las rules **no** se ven afectadas. El `RulesBudget` es de la config del servidor, no del request. El `budget` del request solo controla las memorias generales. Esto es por diseno (ver decision D1 rationale).

### 7.8. Backward compatibility del response JSON

Los campos nuevos (`rules_count`, `rules_tokens`, `rules_truncated`, `rules`) son todos aditivos. Ningun campo existente cambia de tipo o semantica:
- `token_estimate` ahora incluye los tokens de rules, pero esto es correcto: el valor siempre fue "tokens totales del bundle".
- `memories` sigue sin contener rules (gracias al dedup).
- `included` sigue contando solo memorias no-rule.
- Clientes que deserialicen `ContextResponse` sin los campos nuevos simplemente los ignoran (`encoding/json` Go, JSON.parse JS).

---

## 8. Plan de implementacion

Pasos atomicos, cada uno commit-able:

| # | Commit message | Archivos | Descripcion |
|---|----------------|----------|-------------|
| 1 | `feat(config): add rules_budget to ContextConfig` | `internal/config/config.go`, `internal/config/config_test.go` (si existe) | Nuevo campo `RulesBudget`, default 1500, env override `MNEME_RULES_BUDGET`, validacion >= 0. |
| 2 | `feat(model): extend ContextResponse with rules fields` | `internal/model/search.go` | Campos `RulesCount`, `RulesTokens`, `RulesTruncated`, `Rules []Memory` en `ContextResponse`. |
| 3 | `feat(service): inject rules in GetContext with dedicated budget` | `internal/service/context.go`, `internal/service/context_test.go` (nuevo o extender) | Fase 1 (load + sort + pack rules), Fase 2 (dedup), helper `severityOrder`. |
| 4 | `feat(cli): render Active Rules section in hook output` | `internal/cli/hook.go` | Actualizar `printContextHook` para renderizar `## Active Rules`. |
| 5 | `test(service): context rule injection integration tests` | `internal/service/context_test.go` | Tests de integracion contra SQLite in-memory con rules. |
| 6 | `docs: add SPEC-002 rules injection specification` | `docs/specs/SPEC-002-rules-injection.md` | Copia del spec para tracking en el repo. |

**Nota:** Los commits 3 y 4 cubren los tres frontends (MCP, HTTP, CLI):
- **MCP** y **HTTP** no necesitan cambios en sus handlers — ambos serializan `*model.ContextResponse` directamente, que ahora tiene los campos nuevos. `handleMemContext` (`handlers.go:149-164`) llama `resultFromAny(resp)` y `handleContext` (`server.go:368-395`) llama `writeJSON(w, 200, resp)`. Ambos se benefician automaticamente.
- **CLI** necesita cambio solo en `printContextHook` para renderizar la seccion markdown.

---

## 9. Tests requeridos

### config (unit)
- `TestContextConfig_RulesBudgetDefault` — `Default()` tiene `RulesBudget: 1500`.
- `TestContextConfig_RulesBudgetEnvOverride` — `MNEME_RULES_BUDGET=2000` overrides.
- `TestContextConfig_RulesBudgetValidation` — `RulesBudget: -1` falla validacion.
- `TestContextConfig_RulesBudgetZero` — `RulesBudget: 0` es valido.

### model (unit)
- `TestContextResponse_JSONFields` — verificar que `rules`, `rules_count`, `rules_tokens`, `rules_truncated` se serializan/deserializan correctamente. Verificar que `rules` se omite con `omitempty` cuando es nil.

### service (integration, SQLite in-memory)
- `TestContext_RulesInjected` — crear 3 rules (block, warn, info) y 10 memorias generales, llamar `Context()`, verificar que las 3 rules estan en `resp.Rules` y no en `resp.Memories`.
- `TestContext_RulesSortedBySeverity` — crear rules con distintas severities, verificar orden: block primero, luego warn, luego info.
- `TestContext_RulesBudgetExhausted` — crear rules que excedan el rules_budget (ej: 5 rules de 500 tokens con budget=1500), verificar `rules_truncated >= 2`.
- `TestContext_RulesBudgetZero_NoInjection` — `RulesBudget=0`, crear rules, verificar `resp.Rules` vacio, `resp.RulesCount == 0`. Rules deben aparecer en `resp.Memories` si su score es alto.
- `TestContext_RulesDedup` — crear una rule que matchea el focus query, verificar que aparece en `resp.Rules` pero NO en `resp.Memories`.
- `TestContext_GlobalRulesIncluded` — crear rules en global store, verificar que aparecen en `resp.Rules` de un project context.
- `TestContext_GlobalRulesExcluded_IncludeGlobalFalse` — `IncludeGlobal=false`, verificar rules globales no aparecen.
- `TestContext_RuleLargerThanBudget_Skipped` — una rule de 5000 tokens con `RulesBudget=1500`, verificar omision + `rules_truncated == 1`, pero rules mas pequenas si se incluyen.
- `TestContext_NoRules_BackwardCompat` — sin rules en DB, verificar response identico al pre-SPEC-002 (fields nuevos en 0/nil).
- `TestContext_Performance_LoadActiveRules` — benchmark con 50 rules, verificar < 5ms.

### cli (integration)
- `TestPrintContextHook_WithRules` — verificar que el output contiene `## Active Rules`, `[BLOCK]`, `_Applies to:_`.
- `TestPrintContextHook_WithoutRules` — verificar que `## Active Rules` no aparece.
- `TestPrintContextHook_RulesTruncated` — verificar mensaje de truncamiento.

---

## 10. Criterios de aceptacion

1. **Rules inyectadas:** `mem_context` con 2 rules tipo `block` y `warn` en DB devuelve `rules_count: 2` y ambas en `resp.Rules`, independientemente del `focus`.
2. **Dedup activo:** Una rule que matchea focus no aparece duplicada en `resp.Memories`.
3. **Sort correcto:** Con rules de severidad mixta (block, info, warn), `resp.Rules[0].Severity == "block"`, `resp.Rules[1].Severity == "warn"`, `resp.Rules[2].Severity == "info"`.
4. **Budget toggle:** Con `RulesBudget=0`, `resp.Rules` esta vacio/nil y `resp.RulesCount == 0`.
5. **CLI render:** `mneme hook session-start` con rules en DB imprime `## Active Rules (N rules, ~T tokens)` con `[BLOCK]`/`[WARN]`/`[INFO]` tags.
6. **Performance:** `LoadActiveRules` completa en < 5ms con 20 rules. Render del bundle completo en < 2ms.

---

## 11. Open questions / pushbacks

Ninguno. El scope esta bien definido por el backlog refinado y las decisiones se derivan directamente de los patrones existentes en el codigo:

1. La separacion de budget sigue el patron de `LastSession` exempt del budget general (`context.go:176-183`).
2. El orden severity->importance->updated sigue patrones existentes de sort multi-criteria en `scoring/`.
3. Los nuevos campos en `ContextResponse` son aditivos (no rompen backward compat).
4. El indice parcial `idx_memories_rules` ya fue creado por SPEC-001 para este uso exacto.

El unico punto de atencion menor: el SPEC-001 QA report (`spec/SPEC-001-qa-result`) reporto que `mem_update` MCP schema falta el type `rule` en su enum y los campos `applies_to`/`severity`. Esto es un bug pendiente que no afecta SPEC-002 pero deberia corregirse.

---

## Scope explicitamente fuera

- Matching engine + hook pre-tool-use -> SPEC-R3 (BL-003)
- CLI `mneme rule add/list/test` -> SPEC-R4 (BL-004)
- `ContextRequest` con `rules_budget` override por request -> no planeado
- Documentacion docs/RULES.md -> SPEC-D4

---

## Dependencias

- **SPEC-001** (completada): Tipo `rule`, campos `applies_to`/`severity`, migration 006, indice parcial.
- SPEC-R3 y SPEC-R4 dependen de SPEC-002.
