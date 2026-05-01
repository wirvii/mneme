# SPEC-004 — CLI `mneme rule add/list/test`

| Campo         | Valor                                                          |
|---------------|----------------------------------------------------------------|
| **ID**        | SPEC-004                                                       |
| **Epic**      | EPIC-1 — Rules como ciudadanos de primera clase                |
| **Backlog**   | BL-004                                                         |
| **Estado**    | speccing -> specced                                            |
| **Owner**     | architect                                                      |
| **Fecha**     | 2026-04-30                                                     |
| **Deps**      | SPEC-001 (completada), SPEC-002 (completada), SPEC-003 (completada) |
| **Memorias**  | `roadmap/v2-master-plan`, `spec/SPEC-001-implementation-notes`, `spec/SPEC-001-rule-type-design`, `spec/SPEC-002-implementation-notes`, `spec/SPEC-003-hook-pre-tool-use-design`, `spec/SPEC-003-implementation-notes`, `architecture/cli-surface`, `architecture/interfaces` |

---

## 1. Contexto y motivacion

### El problema

Hoy la unica forma de crear una rule es via `mneme save --type rule --applies-to "pattern" --severity warn --title "..." --content "..."`. Esto tiene tres problemas de UX:

1. **Verbose y propenso a errores:** El usuario debe recordar 5+ flags, incluir `--type rule` explicitamente, y saber que `--applies-to` es obligatorio para rules. Un olvido produce un error generico.
2. **Sin validacion de patterns:** `--applies-to "tool:Edit+internal/**"` se persiste tal cual sin feedback sobre si el pattern va a matchear algo. El usuario no puede verificar que su rule funciona hasta que la encuentre un hook en runtime.
3. **Sin listado especializado:** `mneme search --type rule` devuelve rules pero con el formato de memorias genericas (ID truncado, tipo, titulo, score). No muestra severity, applies_to, ni distingue visualmente block/warn/info.

### La solucion

Un subcomando `mneme rule` con tres subcomandos:

- `mneme rule add` — wizard guiado para crear rules con validacion inline.
- `mneme rule list` — listado tabular de rules con colores por severity y truncado inteligente de patterns.
- `mneme rule test` — evalua rules contra un tool+path simulado, mostrando cuales matchearian y por que.

### Cierre de EPIC-1

SPEC-004 es la ultima spec de EPIC-1 (Rules como ciudadanos de primera clase):
- SPEC-001: Modelo de datos (tipo `rule`, `applies_to`, `severity`). Completada.
- SPEC-002: Inyeccion obligatoria en `mem_context`. Completada.
- SPEC-003: Hook `pre-tool-use` con matching engine. Completada.
- **SPEC-004: CLI ergonomica para operacion de rules.** Esta spec.

---

## 2. Decisiones de diseno

### D1. Subcomando `mneme rule` con subcomandos vs. flags en `mneme save`

**Decision:** Nuevo subcomando `mneme rule` con `add`, `list`, `test` como subcomandos Cobra. Registrado en `root.go` como `root.AddCommand(newRuleCmd())`.

**Rationale:**
- `mneme save --type rule` sigue funcionando (backward compat). `mneme rule add` es un shortcut ergonomico que delega internamente al mismo `service.Save()`.
- El patron de subcomandos anidados ya existe en el codebase: `mneme sync export|import|status` (`internal/cli/sync.go`), `mneme hook session-start|session-end|pre-tool-use` (`internal/cli/hook.go`).
- Agrupar `add`, `list` y `test` bajo `rule` es coherente con el namespace: las tres operaciones son sobre rules, no sobre memorias genericas.

### D2. NO usar bubbletea para prompts interactivos

**Decision:** `mneme rule add` usa **solo flags** (como `mneme save`). No prompts interactivos con bubbletea.

**Rationale:**
- El usuario principal de `mneme rule add` es un agente (via MCP o CLI), no un humano. Los prompts interactivos requieren un TTY y bloquean la automatizacion.
- El patron del codebase es consistente: ninguno de los 22 comandos existentes usa bubbletea para input. `internal/tui/` se usa solo para el comando `tui` (lista navegable), no para recoleccion de input en otros comandos.
- `mneme save` (`internal/cli/save.go:17-113`) usa flags + `--stdin` para content multilinea. `mneme rule add` sigue exactamente este patron.
- Si en el futuro se quiere un wizard interactivo, se puede agregar como `mneme rule add --interactive` sin romper el flujo por defecto.

### D3. `$EDITOR` para content multilinea vs. flag solo

**Decision:** `mneme rule add` soporta tres formas de proveer content:

1. `--content "texto inline"` — para reglas cortas.
2. `--stdin` — lee content de stdin (`echo "..." | mneme rule add --title ...`).
3. `$EDITOR` — **NO se implementa en SPEC-004**. Se deja para una iteracion futura.

**Rationale:**
- `mneme save` ya soporta `--content` y `--stdin` pero no `$EDITOR` (`save.go:50-57`). Agregar `$EDITOR` en `rule add` sin agregarlo en `save` seria inconsistente.
- Las rules tienden a ser concisas (1-3 oraciones). Content largo sugiere que deberia ser una convention o architecture, no una rule.
- `--stdin` cubre el caso de content largo cuando es necesario.

### D4. Validacion de applies_to en CLI: re-aplicar engine de SPEC-003

**Decision:** `mneme rule add` valida cada pattern de `--applies-to` **sintacticamente** en el CLI antes de llamar al service:

1. Cada entry no vacia (ya validado por service vía `ErrEmptyPattern`).
2. Si contiene `+`, verificar que tiene exactamente dos partes y no hay `tool:X+tool:Y`.
3. Si es glob de path, verificar que `doublestar.Match(pattern, "")` no retorna error de sintaxis (glob invalido).
4. Si empieza con `tool:`, verificar que el nombre del tool no esta vacio.

La validacion **semantica** (si el pattern va a matchear algo en el proyecto actual) es responsabilidad de `mneme rule test`, no de `mneme rule add`.

**Rationale:**
- La validacion rapida en CLI da feedback inmediato antes de persistir. No queremos rules con patterns sintacticamente invalidos en la DB.
- `internal/rules/match.go` ya implementa el parser. Se puede extraer una funcion `ValidatePattern(entry string) error` al mismo paquete sin romper la dependency rule (`cli -> rules -> model`).

### D5. Topic_key autogenerado: algoritmo de slugify

**Decision:** Cuando `--topic-key` no se provee, `mneme rule add` autogenera uno con el formato `rule/<slug-del-titulo>`. Algoritmo:

1. Convertir titulo a lowercase.
2. Reemplazar todo lo que no sea `[a-z0-9]` con `-` (incluyendo espacios, emojis, unicode).
3. Colapsar guiones multiples en uno solo.
4. Trim guiones al inicio y final.
5. Truncar a 60 caracteres (sin cortar a mitad de palabra si es posible).
6. Prefijo `rule/`.

Ejemplo: `"Never use time.Now() directly!"` -> `rule/never-use-time-now-directly`
Ejemplo: `"SQL in .sql files only"` -> `rule/sql-in-sql-files-only`
Ejemplo: `"Emoji titulo con caracteres unicode"` -> `rule/emoji-titulo-con-caracteres-unicode`

**Rationale:**
- El topic_key es critico para upserts. Sin el, cada `rule add` crea un duplicado. Autogenerarlo elimina friccion.
- El prefijo `rule/` es coherente con el patron existente de topic_keys en el codebase: `architecture/overview`, `config/runtime-config`, `spec/SPEC-001`.
- El algoritmo es determinista: el mismo titulo siempre produce el mismo topic_key, lo que habilita upserts idempotentes.
- El truncado a 60 caracteres es un safety cap. Los topic_keys existentes mas largos tienen ~40 caracteres.

### D6. Tabla output: columnas, anchos, truncado, color por severity

**Decision:** `mneme rule list` renderiza una tabla con estas columnas:

| Col | Ancho | Contenido |
|-----|-------|-----------|
| SEV | 5 | Tag de severity con color: `BLOCK` (rojo), `WARN` (amarillo), `INFO` (cyan) |
| ID | 8 | Primeros 8 chars del UUID (patron de `search.go:79-81`) |
| TITLE | 30 | Titulo truncado a 27 + `...` si excede |
| APPLIES_TO | 30 | Patterns unidos por `, `, truncados a 27 + `...` |
| SCOPE | 7 | `project` o `global` |

**Color por severity (lipgloss.AdaptiveColor):**
- `block` -> rojo: `{Light: "#b91c1c", Dark: "#f38ba8"}` — reutiliza `typeColors[TypeRule]` de `tui/style.go:36`.
- `warn` -> amarillo: `{Light: "#d97706", Dark: "#fab387"}` — reutiliza `typeColors[TypePreference]` de `tui/style.go:30`.
- `info` -> cyan: `{Light: "#0891b2", Dark: "#89dceb"}` — reutiliza `typeColors[TypeDiscovery]` de `tui/style.go:28`.

**Rationale:**
- El formato sigue el patron de `mneme search` (`search.go:72-101`): header con columnas fijas, ID truncado, padding con `%-Xs`.
- lipgloss ya esta disponible como indirect dependency (`go.mod:19`). Se usa directamente en el CLI package (no via `tui/`) para renderizar color — esto es aceptable porque `charmbracelet/lipgloss` es un paquete de estilo, no de TUI completa.
- La tabla no incluye CONTENT porque las rules se asumen concisas y el content se ve con `mneme get <id>`.

### D7. JSON schema versionado para `rule list --json`

**Decision:** `mneme rule list --json` emite un JSON con wrapper:

```json
{
  "version": "1",
  "rules": [
    {
      "id": "019de100-...",
      "title": "Never use time.Now()",
      "severity": "warn",
      "applies_to": ["internal/**/*.go"],
      "scope": "project",
      "topic_key": "rule/never-use-time-now",
      "importance": 0.95,
      "created_at": "2026-04-30T20:00:00Z",
      "updated_at": "2026-04-30T20:00:00Z"
    }
  ]
}
```

**Rationale:**
- El wrapper con `version: "1"` permite evolucion futura del schema sin romper consumidores. Si se agregan campos o se cambia la estructura, se incrementa la version.
- `mneme search --json` no tiene wrapper (emite el SearchResponse directo, `search.go:63-65`). La diferencia es que `rule list` es un endpoint nuevo y podemos empezar con buen pie.
- Los campos del wrapper son un subconjunto de `model.Memory` — los relevantes para rules. No se expone `content` en el listado JSON para mantener payloads compactos; para content completo, usar `mneme get <id> --json`.

### D8. Headless detection: `mattn/go-isatty`

**Decision:** Usar `github.com/mattn/go-isatty` para detectar si stdout es un TTY. Cuando no es TTY (piped, CI, subagente), desactivar colores lipgloss y usar formato plain.

**Rationale:**
- `mattn/go-isatty` ya esta en `go.sum` como dependencia indirecta (traida por `charmbracelet/lipgloss`, `go.mod:29`). Promoverla a import directo no agrega ninguna dependencia nueva al binario.
- lipgloss detecta el color profile automaticamente via `termenv`, pero la logica de "usar formato tabla vs. raw" la debemos controlar nosotros para el output de `mneme rule list`.
- La deteccion headless es simple: si `!isatty.IsTerminal(os.Stdout.Fd())`, skip lipgloss styling.
- En la practica, `lipgloss` y `termenv` ya detectan "no color" cuando no es TTY. Pero necesitamos la comprobacion explicitamente para decidir si usar `fmt.Fprintf` con format strings fijos (headless) vs. lipgloss styles (TTY). La manera mas limpia: usar `lipgloss.HasDarkBackground()` que ya hace deteccion interna, y dejar que lipgloss degrace a plain cuando no hay TTY. **Simplificacion:** dado que lipgloss ya stripea ANSI en non-TTY automaticamente, no necesitamos `go-isatty` explicitamente. Solo construir los estilos lipgloss y dejar que la libreria haga lo correcto.

**Decision final simplificada:** No importar `go-isatty` directamente. Usar lipgloss para todo el styling de la tabla. lipgloss/termenv ya detectan automaticamente si stdout es TTY y strippean ANSI codes cuando no lo es. Esto es el patron correcto y ya funciona asi en `internal/tui/`.

### D9. `mneme rule rm` — out of scope explicitamente

**Decision:** `mneme rule rm` NO se incluye en SPEC-004.

**Rationale:**
- `mneme forget <id>` ya soft-deletes cualquier memoria, incluyendo rules. No hay necesidad de un alias dedicado.
- El roadmap no lo incluye como parte de EPIC-1.
- Si en el futuro se quiere `mneme rule rm --title "..."` (delete by title/topic_key en vez de ID), es un enhancement separado.

### D10. `mneme rule test` — integracion con matching engine

**Decision:** `mneme rule test` recibe `--tool <name>` y `--path <file-path>`, carga rules de la DB (project + global, mismo patron que `loadRulesForHook` en `hook.go:271-303`), y llama `rules.Match()` (`internal/rules/match.go:53`). El output muestra:

1. Cuantas rules evaluadas vs. cuantas matchearon.
2. Para cada rule matcheada: severity, titulo, entries que matchearon, y contenido.
3. Severity efectiva resultante (max de todas).
4. Accion hipotetica: "Would BLOCK", "Would ALLOW with warnings", "Would ALLOW".

**Rationale:**
- El engine ya esta implementado y testeado (SPEC-003). `rule test` es un frontend de diagnostico puro.
- `--path` acepta tanto paths absolutos como relativos (el engine los normaliza via `normalisePath`, `match.go:82-98`).
- `--tool` defaultea a `Edit` (el tool mas comun para reglas de path).

---

## 3. Comandos detallados

### 3.1. `mneme rule add`

**Uso:**
```
mneme rule add --title "Titulo" --content "Contenido" --applies-to "pattern" [--applies-to "..."] [--severity info|warn|block] [--scope project|global] [--topic-key "key"] [--importance 0.95] [--stdin]
```

**Flags:**

| Flag | Short | Type | Required | Default | Descripcion |
|------|-------|------|----------|---------|-------------|
| `--title` | `-t` | string | si | — | Titulo corto de la rule |
| `--content` | `-c` | string | no* | — | Contenido/instruccion de la rule |
| `--applies-to` | `-a` | stringArray | si | — | Pattern(s) de aplicacion (repetible) |
| `--severity` | `-s` | string | no | `warn` | Severidad: `info`, `warn`, `block` |
| `--scope` | — | string | no | `project` | Scope: `project`, `global` |
| `--topic-key` | `-k` | string | no | auto | Topic key (autogenerado si omitido) |
| `--importance` | `-i` | float64 | no | `0.95` | Override de importancia |
| `--stdin` | — | bool | no | `false` | Leer content de stdin |

*`--content` o `--stdin` es requerido. Si ambos se omiten, error.

**Orden de ejecucion interno:**

1. Validar flags requeridos (`--title`, `--applies-to`, `--content` o `--stdin`).
2. Si `--stdin`, leer content de stdin (patron de `save.go:50-57`).
3. Validar severity (si no es `info|warn|block` -> error).
4. Validar cada pattern de `--applies-to` sintacticamente (ver D4).
5. Si `--topic-key` no provisto, autogenerar con slugify (ver D5).
6. Construir `model.SaveRequest` con `Type: model.TypeRule`.
7. Llamar `initService()` + `svc.Save()` (patron de `save.go:63-93`).
8. Imprimir resultado.

**Validaciones:**

| Condicion | Error message | Exit code |
|-----------|--------------|-----------|
| `--title` omitido | `--title is required` | 1 |
| `--content` y `--stdin` ambos omitidos | `--content is required (or use --stdin)` | 1 |
| `--applies-to` omitido | `--applies-to is required (at least one pattern)` | 1 |
| `--severity` valor invalido | `invalid severity "X": must be info, warn, or block` | 1 |
| Pattern con `tool:X+tool:Y` | `invalid pattern "tool:X+tool:Y": combined entries cannot have two tool selectors` | 1 |
| Pattern con glob invalido | `invalid pattern "[[bad": <doublestar error>` | 1 |
| Pattern vacio | `applies_to patterns must not be empty` | 1 (via service) |

**Output exitoso:**
```
Rule saved: 019de100-abcd-7fff (created) — Never use time.Now() directly
  Severity:   warn
  Applies to: internal/**/*.go, !internal/**/*_test.go
  Topic key:  rule/never-use-time-now-directly
  Scope:      project
```

### 3.2. `mneme rule list`

**Uso:**
```
mneme rule list [--scope project|global|all] [--severity info|warn|block] [--json] [--limit N]
```

**Flags:**

| Flag | Short | Type | Required | Default | Descripcion |
|------|-------|------|----------|---------|-------------|
| `--scope` | `-s` | string | no | `all` | Filtrar por scope |
| `--severity` | — | string | no | — | Filtrar por severity |
| `--json` | — | bool | no | `false` | Output JSON |
| `--limit` | `-n` | int | no | `50` | Maximo de resultados |

**Fuente de datos:** `initService()` -> `svc.ListRules(ctx, opts)`. ListRules es un metodo nuevo en service que wrappea `store.List()` con `Type: model.TypeRule`, combinando project y global stores. Si `--scope=all` (default), carga de ambos. Si `--scope=project`, solo project store. Si `--scope=global`, solo global store.

**Service method nuevo:**

```go
// ListRules returns all active rules, optionally filtered by scope and severity.
// When scope is empty or "all", rules from both project and global stores are returned.
func (svc *MemoryService) ListRules(ctx context.Context, opts ListRulesOptions) ([]*model.Memory, error)
```

```go
// ListRulesOptions parameterises a ListRules call.
type ListRulesOptions struct {
    Scope    model.Scope    // filter by scope ("" or "all" = both stores)
    Severity model.Severity // filter by severity ("" = all severities)
    Limit    int            // max results per store (default 50)
}
```

**Implementacion:** Internamente llama `store.List()` con `Type: model.TypeRule` + los filtros opcionales. Cuando scope es "all", hace dos calls (project + global) y merge por severity desc, importance desc (mismo sort que SPEC-002 D2).

**Output tabla (TTY):**

```
SEV    ID        TITLE                           APPLIES_TO                      SCOPE
─────  ────────  ──────────────────────────────  ──────────────────────────────  ───────
BLOCK  019de100  Never edit vendor/              vendor/**                       project
WARN   019de101  SQL in .sql files only          **/*.go, !**/*_test.go          project
WARN   019de102  No time.Now() directly          internal/**/*.go                project
INFO   019de103  Prefer Server Components        tool:Edit+**/*.tsx              global
INFO   019de104  Use context.Context first       **/*.go                         global
```

**Colores (solo en TTY, lipgloss strip automatico en non-TTY):**
- `BLOCK` -> rojo
- `WARN` -> amarillo
- `INFO` -> cyan
- Headers -> bold

### 3.3. `mneme rule test`

**Uso:**
```
mneme rule test [--tool Edit|Write|MultiEdit] [--path <file-path>] [--json]
```

**Flags:**

| Flag | Short | Type | Required | Default | Descripcion |
|------|-------|------|----------|---------|-------------|
| `--tool` | `-T` | string | no | `Edit` | Nombre del tool a simular |
| `--path` | `-p` | string | no | — | Path del archivo a simular |
| `--json` | — | bool | no | `false` | Output JSON |

**Fuente de datos:** Misma funcion `loadRulesForHook(cwd, errW)` de `hook.go:271-303` (reutilizarla o extraer a un helper compartido). Luego `rules.Match(allRules, toolName, filePath, cwd)`.

**`--path` absoluto vs. relativo:**
- Si `--path` es relativo, `rules.Match` lo normaliza internamente (`match.go:82-98`).
- Si `--path` es absoluto, `rules.Match` lo relativiza contra CWD.
- Si `--path` omitido, se evaluan solo tool selectors y wildcards (`**`). El usuario ve un warning: `No --path specified; only tool selectors and wildcards will match.`

**Output con matches (TTY):**

```
Testing rules for: tool=Edit, path=internal/store/memory.go

Evaluated 5 rules, 2 matched.

  [BLOCK] Never edit vendor/
          Matched entries: vendor/**
          This rule would NOT match (vendor/** does not match internal/store/memory.go).

Wait — re-reading. Output for the 2 that DID match:

  [WARN] SQL in .sql files only
         Content: No inline SQL strings in Go code. Use sqlc-generated queries.
         Matched entries: **/*.go
         (negation !**/*_test.go did not veto)

  [WARN] No time.Now() directly
         Content: Use the injected clock from the service constructor.
         Matched entries: internal/**/*.go

Result: Would ALLOW with 2 warnings.
```

Correccion del format — output limpio:

```
Testing: tool=Edit  path=internal/store/memory.go

Evaluated: 5 rules
Matched:   2 rules

  [WARN] SQL in .sql files only
         No inline SQL strings in Go code. Use sqlc-generated queries.
         Matched by: **/*.go
         Negation !**/*_test.go: did not veto

  [WARN] No time.Now() directly
         Use the injected clock from the service constructor.
         Matched by: internal/**/*.go

Effective severity: warn
Result: ALLOWED (with 2 warnings)
```

**Output sin matches:**

```
Testing: tool=Edit  path=docs/README.md

Evaluated: 5 rules
Matched:   0 rules

Result: ALLOWED (no rules matched)
```

---

## 4. Output formats (mockups completos)

### 4.1. `rule add` exito

```
Rule saved: 019de100-abcd-7fff (created) — Never use time.Now() directly
  Severity:   warn
  Applies to: internal/**/*.go, !internal/**/*_test.go
  Topic key:  rule/never-use-time-now-directly
  Scope:      project
```

### 4.2. `rule add` error de validacion

```
Error: invalid pattern "tool:Edit+tool:Write": combined entries cannot have two tool selectors
```

### 4.3. `rule list` tabla con 5 rules de severity mixta

```
SEV    ID        TITLE                           APPLIES_TO                      SCOPE
─────  ────────  ──────────────────────────────  ──────────────────────────────  ───────
BLOCK  019de100  Never edit vendor/              vendor/**                       project
WARN   019de101  SQL in .sql files only          **/*.go, !**/*_test.go          project
WARN   019de102  No time.Now() directly          internal/**/*.go                project
INFO   019de103  Prefer Server Components        tool:Edit+**/*.tsx              global
INFO   019de104  Use context.Context first       **/*.go                         global

5 rules (1 block, 2 warn, 2 info)
```

### 4.4. `rule list --json`

```json
{
  "version": "1",
  "rules": [
    {
      "id": "019de100-abcd-7fff-8000-000000000001",
      "title": "Never edit vendor/",
      "severity": "block",
      "applies_to": ["vendor/**"],
      "scope": "project",
      "topic_key": "rule/never-edit-vendor",
      "importance": 0.95,
      "created_at": "2026-04-30T20:00:00Z",
      "updated_at": "2026-04-30T20:00:00Z"
    },
    {
      "id": "019de101-abcd-7fff-8000-000000000002",
      "title": "SQL in .sql files only",
      "severity": "warn",
      "applies_to": ["**/*.go", "!**/*_test.go"],
      "scope": "project",
      "topic_key": "rule/sql-in-sql-files-only",
      "importance": 0.95,
      "created_at": "2026-04-30T20:05:00Z",
      "updated_at": "2026-04-30T20:05:00Z"
    }
  ]
}
```

### 4.5. `rule test` con 2 rules matching

```
Testing: tool=Edit  path=internal/store/memory.go

Evaluated: 5 rules
Matched:   2 rules

  [WARN] SQL in .sql files only
         No inline SQL strings in Go code.
         Matched by: **/*.go
         Negation !**/*_test.go: did not veto

  [WARN] No time.Now() directly
         Use the injected clock from the service constructor.
         Matched by: internal/**/*.go

Effective severity: warn
Result: ALLOWED (with 2 warnings)
```

### 4.6. `rule test` con 0 rules matching

```
Testing: tool=Edit  path=docs/README.md

Evaluated: 5 rules
Matched:   0 rules

Result: ALLOWED (no rules matched)
```

---

## 5. Edge cases

### 5.1. `$EDITOR` no definido y stdin no es TTY

No aplica — SPEC-004 no implementa `$EDITOR` (ver D3). Si `--content` no se provee y `--stdin` no se usa, se retorna error: `--content is required (or use --stdin)`. Si `--stdin` se usa pero stdin esta vacio, content sera `""` y el service retornara `ErrContentRequired`.

### 5.2. `applies_to` con espacios (whitespace handling)

Los patterns se toman tal cual de los flags, sin trim de whitespace. En la shell, `--applies-to " internal/** "` incluiria los espacios. Esto es consistente con como Go y doublestar manejan globs. La validacion sintactica via `doublestar.Match(pattern, "")` aceptaria patterns con leading/trailing spaces (son globs validos que simplemente nunca matchearian un path real). No hacemos trim automatico para no sorprender al usuario.

**Recomendacion en docs:** Documentar que los patterns no deben tener whitespace extra.

### 5.3. Title con caracteres unicode (emojis) -> slugify

El algoritmo de slugify (D5) reemplaza todo lo que no es `[a-z0-9]` con `-`. Emojis y caracteres unicode multibyte producen guiones que se colapsan.

Ejemplo: `"No push directo al main"` -> `rule/no-push-directo-al-main`
Ejemplo: `"Rule con emojis y tildes"` -> `rule/rule-con-emojis-y-tildes`

El topic_key resultante es siempre ASCII, lowercase, con guiones. Determinista.

### 5.4. Rule sin scope explicito -> default

Default es `project` (consistente con `mneme save` que defaultea a `ScopeProject`, `service/memory.go:92`). Si el usuario quiere una rule global, debe pasar `--scope global`.

### 5.5. `rule list` con cero rules

```
No rules found.

Create one with: mneme rule add --title "..." --content "..." --applies-to "pattern"
```

### 5.6. `rule test` con path absoluto vs relativo

- **Absoluto:** `--path /Users/x/project/internal/store/memory.go` — el engine relativiza contra CWD via `normalisePath` (`match.go:82-98`). Si el path esta fuera del arbol del proyecto, solo matchean tool selectors y `**`.
- **Relativo:** `--path internal/store/memory.go` — el engine lo convierte a absoluto con `filepath.Join(cwd, path)` y luego relativiza (produciendo el mismo path relativo). Funciona correctamente.

### 5.7. `rule test` sin `--path`

Solo se evaluan tool selectors (`tool:X`) y wildcards (`**`). Path globs no matchean path vacio (`match.go:171`). Se muestra un warning:

```
Note: No --path specified. Only tool selectors and ** wildcards will match.
```

### 5.8. `rule list` con scope=all y muchas rules

Cuando `--scope=all` (default), se cargan rules de project + global stores. El merge se hace post-query con sort por `severityOrder DESC, importance DESC, updated_at DESC`. El `--limit` se aplica al resultado mergeado (no por store). Internamente se consulta cada store con `LIMIT=limit` (safety cap) y luego se trunca el merge.

### 5.9. `rule add` con topic_key que ya existe (upsert)

Se comporta como upsert via `store.Upsert()` (patron existente). Si ya existe una rule con el mismo `(topic_key, project, scope)`, se actualiza. El output muestra:

```
Rule saved: 019de100-abcd-7fff (updated) — Never use time.Now() directly
  Severity:   block
  Applies to: internal/**/*.go
  Topic key:  rule/never-use-time-now-directly
  Scope:      project
```

---

## 6. Paquetes y archivos afectados

| Paquete | Archivo | Tipo de cambio |
|---------|---------|---------------|
| `internal/cli/` | `rule.go` | **nuevo** — `newRuleCmd()`, `newRuleAddCmd()`, `newRuleListCmd()`, `newRuleTestCmd()`, helper `slugifyTitle()` |
| `internal/cli/` | `rule_test.go` | **nuevo** — tests para los 3 subcomandos |
| `internal/cli/` | `root.go` | modificacion — agregar `newRuleCmd()` a `root.AddCommand()` |
| `internal/rules/` | `validate.go` | **nuevo** — `ValidatePattern(entry string) error` |
| `internal/rules/` | `validate_test.go` | **nuevo** — tests de validacion de patterns |
| `internal/service/` | `memory.go` | modificacion — agregar `ListRules()` method |
| `internal/service/` | `memory_test.go` | modificacion — tests para `ListRules()` |

### Fuera de scope

- `mneme rule rm` (ver D9) — usar `mneme forget <id>`.
- `mneme rule edit` — usar `mneme update <id>` existente.
- `$EDITOR` para content (ver D3).
- Prompts interactivos bubbletea (ver D2).
- Cambios a MCP o HTTP (SPEC-004 es solo CLI).
- Cambios a `mneme save` (sigue funcionando tal cual).

---

## 7. Contratos del service layer

### 7.1. `ListRules` — nuevo metodo

**Firma:**
```go
// ListRules returns active rules from the project and/or global stores,
// sorted by severity descending then importance descending.
func (svc *MemoryService) ListRules(ctx context.Context, opts ListRulesOptions) ([]*model.Memory, error)
```

**ListRulesOptions:**
```go
// ListRulesOptions parameterises a ListRules query.
type ListRulesOptions struct {
    // Scope restricts results. Empty or "all" queries both stores.
    Scope    string
    // Severity filters by severity. Empty means all severities.
    Severity model.Severity
    // Limit caps results after merge. Default 50.
    Limit    int
}
```

**Algoritmo interno:**
1. Si scope == "" || scope == "all": query project store + global store con `Type: model.TypeRule`.
2. Si scope == "project": solo project store.
3. Si scope == "global": solo global store.
4. Merge results.
5. Si severity != "": filtrar por severity.
6. Sort por `severityOrder DESC, importance DESC, updated_at DESC` (reutilizar patron de `context.go`).
7. Truncar a limit.

### 7.2. `loadRulesForHook` — extraer a helper compartido

Actualmente en `hook.go:271-303`. Para que `rule test` pueda reutilizarlo, extraer la logica de "abrir DBs read-only, query rules, merge" a un helper:

**Opcion A:** Mover `loadRulesForHook` a un helper interno del package `cli` (ya esta ahi). `rule test` lo llama directamente.

**Opcion B:** `rule test` usa `initService()` + `svc.ListRules()` en vez de read-only DB.

**Decision:** **Opcion B**. `rule test` no tiene las mismas constraints de performance que el hook (<50ms). Puede usar `initService()` que es el patron estandar de todos los CLI commands. Esto simplifica la implementacion y evita duplicar logica de acceso a DB.

---

## 8. Plan de implementacion

Pasos atomicos, cada uno commit-able y con tests verdes:

| # | Commit message | Archivos | Descripcion |
|---|----------------|----------|-------------|
| 1 | `feat(rules): add ValidatePattern for syntax checking` | `internal/rules/validate.go`, `internal/rules/validate_test.go` | Nueva funcion `ValidatePattern(entry string) error`. Table-driven tests. |
| 2 | `feat(service): add ListRules method` | `internal/service/memory.go`, `internal/service/memory_test.go` | `ListRules(ctx, opts)` con merge multi-store, severity sort. Integration tests con SQLite in-memory. |
| 3 | `feat(cli): add mneme rule add/list/test commands` | `internal/cli/rule.go`, `internal/cli/rule_test.go`, `internal/cli/root.go` | `newRuleCmd()` con 3 subcomandos. Helper `slugifyTitle()`. Registro en root. Tests para output y validacion. |
| 4 | `docs: add SPEC-004 CLI rule specification` | `docs/specs/SPEC-004-cli-rule.md` | Copia del spec para tracking en el repo. |

---

## 9. Tests requeridos

### 9.1. `internal/rules/validate_test.go` — validacion de patterns

| Test | Input | Expected |
|------|-------|----------|
| ValidPattern_Wildcard | `**` | nil |
| ValidPattern_ToolSelector | `tool:Edit` | nil |
| ValidPattern_PathGlob | `internal/**/*.go` | nil |
| ValidPattern_Combined | `tool:Edit+internal/**` | nil |
| ValidPattern_Negation | `!docs/**` | nil |
| InvalidPattern_EmptyString | `""` | error |
| InvalidPattern_TwoToolSelectors | `tool:Edit+tool:Write` | error |
| InvalidPattern_EmptyToolName | `tool:` | error |
| InvalidPattern_BadGlob | `[[bad` | error |
| InvalidPattern_EmptyAfterPlus | `tool:Edit+` | error |

### 9.2. `internal/service/memory_test.go` — ListRules

| Test | Scenario | Expected |
|------|----------|----------|
| ListRules_AllScopes | 2 project + 1 global rule | Returns 3, sorted by severity |
| ListRules_ProjectOnly | scope=project | Returns only project rules |
| ListRules_GlobalOnly | scope=global | Returns only global rules |
| ListRules_FilterBySeverity | 3 rules mixtas, severity=block | Returns only block rules |
| ListRules_SortOrder | block, info, warn | Returns in order: block, warn, info |
| ListRules_EmptyDB | No rules | Returns empty slice, no error |
| ListRules_Limit | 10 rules, limit=3 | Returns 3 |
| ListRules_Performance | 1000 rules | Completes in <1s |

### 9.3. `internal/cli/rule_test.go`

| Test | Scenario | Expected |
|------|----------|----------|
| SlugifyTitle_Simple | "Never use time.Now()" | "rule/never-use-time-now" |
| SlugifyTitle_Unicode | "Emoji titulo" | "rule/emoji-titulo" (only ascii) |
| SlugifyTitle_Long | 100 char title | Truncated to rule/ + 60 chars |
| SlugifyTitle_OnlySpecialChars | "!!!" | "rule/---" collapsed to "rule/" -> empty guard needed |
| RuleAdd_Success | Valid flags | "Rule saved: ... (created)" in stdout |
| RuleAdd_MissingTitle | No --title | Error exit 1 |
| RuleAdd_MissingAppliesTo | No --applies-to | Error exit 1 |
| RuleAdd_MissingContent | No --content, no --stdin | Error exit 1 |
| RuleAdd_InvalidSeverity | --severity critical | Error exit 1 |
| RuleAdd_InvalidPattern | --applies-to "tool:A+tool:B" | Error exit 1 |
| RuleAdd_AutoTopicKey | No --topic-key | Response contains auto-generated key |
| RuleList_Table | 3 rules in DB | Table output with 3 rows + header |
| RuleList_Empty | No rules | "No rules found." message |
| RuleList_JSON | 2 rules, --json | JSON with version:"1" wrapper |
| RuleList_FilterScope | --scope global | Only global rules shown |
| RuleList_FilterSeverity | --severity block | Only block rules shown |
| RuleTest_Match | block rule on Edit+internal/** | "Would BLOCK" in output |
| RuleTest_NoMatch | No rules matching | "no rules matched" |
| RuleTest_NoPath | No --path | Warning about tool selectors only |
| RuleTest_JSON | --json flag | JSON output of MatchResult |

---

## 10. Criterios de aceptacion

1. **`mneme rule add` crea una rule con auto-topic-key:** `mneme rule add -t "No vendor edits" -c "..." -a "vendor/**" -s block` persiste la rule, genera topic_key `rule/no-vendor-edits`, y devuelve `Rule saved: ... (created)`.

2. **`mneme rule list` muestra tabla con colores:** Con 3 rules de severity mixta (block, warn, info), la tabla muestra severity tags coloreados, IDs truncados, titulos, patterns truncados, y scope. Summary line al final.

3. **`mneme rule test` evalua matching correctamente:** `mneme rule test --tool Edit --path internal/store/memory.go` con una rule `applies_to=["internal/**/*.go"]` muestra la rule como matched y explica que entry la causo.

4. **Validacion de patterns en CLI:** `mneme rule add --applies-to "tool:Edit+tool:Write" ...` falla con error descriptivo antes de llamar al service.

5. **JSON output versionado:** `mneme rule list --json` produce `{"version":"1","rules":[...]}`.

6. **Performance:** `mneme rule list` con 1000 rules completa en < 1s (incluyendo DB open, query, sort, render).

---

## 11. Open questions / pushbacks

Ninguna cuestion bloqueante. El diseno sigue los patrones exactos del codebase:

1. El patron de subcomandos anidados ya existe en `sync.go` y `hook.go`.
2. El patron de flags + `--stdin` ya existe en `save.go`.
3. El patron de tabla + `--json` ya existe en `search.go`.
4. El matching engine de SPEC-003 ya esta testeado con 40+ cases.
5. lipgloss y go-isatty ya son dependencias indirectas.

**Punto de atencion menor:**
- `slugifyTitle` podria producir un topic_key vacio si el titulo es solo caracteres especiales (ej: `"!!!"` -> `rule/`). El implementador debe agregar un fallback: si el slug queda vacio despues del trim, usar un UUID corto como fallback (`rule/unnamed-<uuid[:8]>`).

---

## Scope explicitamente fuera

- `mneme rule rm` — usar `mneme forget <id>`
- `mneme rule edit` — usar `mneme update <id>`
- `$EDITOR` para content multilinea
- Prompts interactivos bubbletea
- Cambios a MCP/HTTP (solo CLI)
- Documentacion docs/RULES.md -> SPEC-D4

---

## Dependencias

- **SPEC-001** (completada): Tipo `rule`, campos `applies_to`/`severity`, migration 006.
- **SPEC-002** (completada): `loadActiveRules` pattern, `severityOrder()`.
- **SPEC-003** (completada): `internal/rules/match.go` matching engine, `loadRulesForHook`.
