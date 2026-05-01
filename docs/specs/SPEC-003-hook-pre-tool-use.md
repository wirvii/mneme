# SPEC-003 — Hook pre-tool-use que emite/bloquea por rules

| Campo         | Valor                                                          |
|---------------|----------------------------------------------------------------|
| **ID**        | SPEC-003                                                       |
| **Epic**      | EPIC-1 — Rules como ciudadanos de primera clase                |
| **Backlog**   | BL-003                                                         |
| **Estado**    | speccing                                                       |
| **Owner**     | architect                                                      |
| **Fecha**     | 2026-04-30                                                     |
| **Deps**      | SPEC-001 (completada), SPEC-002 (completada)                   |
| **Memorias**  | `roadmap/v2-master-plan`, `spec/SPEC-001-rule-type-design`, `spec/SPEC-001-implementation-notes`, `spec/SPEC-002-context-injection-design`, `spec/SPEC-002-implementation-notes`, `architecture/claude-hooks-integration`, `architecture/delegation-hook`, `architecture/agent-ecosystem`, `architecture/interfaces` |

---

## 1. Contexto y motivacion

### El problema

SPEC-002 resolvio la primera mitad del enforcement: las rules siempre aparecen en `mem_context` al inicio de sesion. Sin embargo, el agente puede ignorarlas durante la sesion (una vez que el context fue compactado, o simplemente porque decidio no respetar la constraint). No hay un mecanismo **activo** que intercepte cada accion del agente y valide las rules en tiempo real.

### La solucion: JIT rule delivery

Un hook `PreToolUse` que se ejecuta antes de cada uso de herramientas (`Edit`, `Write`, `MultiEdit`) y:

1. **Carga las rules activas** desde la DB de mneme (project + global).
2. **Matchea** cada rule contra la herramienta y el path del archivo que el agente intenta modificar.
3. **Emite o bloquea** segun la severity:
   - `info`: emite un reminder en stdout (el agente lo ve pero no se le impide actuar).
   - `warn`: emite un warning explicito en stdout (igual que info, pero mas visible y con tono de advertencia).
   - `block`: imprime mensaje de bloqueo en stdout y sale con exit code 2 (Claude Code interpreta exit 2 como rechazo de la accion).

### Relacion con SPEC-002 (complementarios, no redundantes)

| Aspecto | SPEC-002 (`mem_context`) | SPEC-003 (hook `pre-tool-use`) |
|---------|--------------------------|-------------------------------|
| Cuando | Una vez, al inicio de sesion | Cada uso de Edit/Write/MultiEdit |
| Tipo | Observacional (no bloquea) | Activo (puede bloquear) |
| Scope | Todas las rules, sin filtrar por tool/path | Solo rules que matcheen el tool+path concreto |
| Funcion | Contexto amplio para que el agente "sepa" | Enforcement puntual para que el agente "obedezca" |
| Sobrevive compaction | No (se pierde con el context) | Si (se ejecuta cada vez) |

Ambos son necesarios: SPEC-002 da contexto "bulk" al inicio; SPEC-003 da enforcement "JIT" durante el trabajo.

### Relacion con `enforce-delegation` (evolucion, no reemplazo)

El hook actual `mneme hook enforce-delegation` (`internal/cli/hook.go:159-231`) bloquea ediciones a paths protegidos usando listas estaticas en `config.toml` (`DelegationConfig.ProtectedPaths`, `DelegationConfig.AllowedPaths`). SPEC-003 **subsume** esta funcionalidad:

- Las `ProtectedPaths` se expresan como rules con `severity=block` y `applies_to=["tool:Edit+<prefix>**", "tool:Write+<prefix>**", "tool:MultiEdit+<prefix>**"]`.
- Las `AllowedPaths` se expresan como negaciones en `applies_to` (ej: `"!docs/**"`).
- El nuevo hook `mneme hook pre-tool-use` implementa el matching engine completo.
- El antiguo `mneme hook enforce-delegation` se mantiene como alias que imprime un deprecation warning a stderr y delega al nuevo.

---

## 2. Decisiones de diseno

### D1. Paquete nuevo `internal/rules/` vs. agregar a `internal/service/`

**Decision:** Nuevo paquete `internal/rules/`.

**Rationale:**
- El matching engine es logica pura sin dependencias de DB, config, o I/O. Es un matcher de patrones: recibe una rule, un tool name, y un path, y retorna bool.
- `internal/service/` tiene como responsabilidad la orquestacion de operaciones CRUD, scoring, y context building. El matching de reglas es una operacion de dominio independiente que puede ser testeada y reutilizada sin instanciar un service completo.
- El roadmap (`roadmap/v2-master-plan`) ya lista `internal/rules/` como paquete planificado. Seguimos el plan.
- El paquete tiene zero external deps (solo standard library + `github.com/bmatcuk/doublestar/v4` para glob matching). Sigue la regla de que imports fluyen inward: `cli/ -> rules/ -> model/`.

### D2. El hook abre la DB en read-only en lugar de pasar por el service layer

**Decision:** El hook abre las DBs con `mode=ro` directamente, no usa `initService()`.

**Rationale:**
- **Performance critica:** El hook se ejecuta en cada uso de Edit/Write/MultiEdit. Abrir la DB en read-only es mas rapido que `initService()` que crea stores, embedders, detecta proyecto, y configura el service completo (`internal/cli/root.go:88-170`). Solo necesitamos una query `SELECT` contra la tabla `memories WHERE type='rule'`.
- **Sin write lock:** `mode=ro` (ya usado en `db.SchemaVersion`, `internal/db/db.go:118`) abre sin WAL writer, evitando contention con el MCP server que puede tener write lock. SQLite WAL permite lectores concurrentes sin conflicto.
- **Sin migraciones:** `db.Open()` ejecuta `migrate()` automaticamente (`db.go:61`). Un hook que se ejecuta N veces por sesion no debe intentar migrar. La apertura read-only omite migraciones.
- **Sin embedder:** No se necesita TF-IDF ni vector search para listar rules.
- **Patron existente:** `db.SchemaVersion()` (`db.go:113-135`) ya abre con `mode=ro` y cierra inmediatamente. El hook sigue este patron.

**Implementacion:** Nueva funcion `db.OpenReadOnly(path string) (*DB, error)` que abre con `mode=ro&_foreign_keys=ON&_busy_timeout=1000` (timeout mas corto para no bloquear el hook). NO ejecuta `migrate()`. Retorna error si el archivo no existe (a diferencia de `Open` que crea).

### D3. Sintaxis de matching

El formato de `applies_to` fue definido en SPEC-001 (D5). SPEC-003 define la **semantica** del matching:

| Pattern | Match logic | Ejemplo |
|---------|------------|---------|
| `**` | Matchea todo — tool y path | Rule aplica universalmente |
| `tool:Edit` | Solo el tool name (case-sensitive) | Rule aplica a cualquier Edit, sin importar path |
| `internal/**/*.go` | Solo el path (glob doublestar) | Rule aplica cuando el path matchea, sin importar tool |
| `tool:Edit+internal/**/*.go` | Tool AND path (ambos deben matchear) | Rule aplica solo a Edit de archivos en internal/ |
| `!docs/**` | Negacion de path | Excluye archivos en docs/ del matching |
| `tool:Edit+!internal/**` | Tool AND negacion de path | Rule aplica a Edit excepto en internal/ |

**Ambiguedades resueltas:**

1. **`tool:Foo+tool:Bar`** (dos tool selectors combinados con `+`): **Error de validacion** en `mem_save`. Un entry con `+` tiene exactamente un tool selector y un path glob. Dos tools no son combinables porque la semantica seria "la tool es simultaneamente Foo y Bar", lo cual es imposible. Si se quiere expresar "aplica a Edit y Write", se usan dos entries separados: `["tool:Edit+path", "tool:Write+path"]`.

2. **Path absoluto fuera del CWD** (ej: `/etc/hosts`): El matcher opera sobre paths relativos al CWD. Si el agente pasa un path absoluto, el hook lo convierte a relativo con `filepath.Rel(cwd, filePath)`. Si el resultado contiene `..` (es decir, esta fuera del tree), **solo los tool selectors matchean** (no los path globs). Esto significa que una rule con `applies_to=["tool:Edit"]` bloquearia una edicion a `/etc/hosts`, pero una rule con `applies_to=["internal/**"]` no.

3. **Symlinks:** **No se resuelven.** El matcher opera sobre el path literal que Claude Code pasa en `tool_input.file_path`. Resolver symlinks seria un syscall adicional por invocacion y complicaria la semantica. Si un symlink apunta a un path protegido, la proteccion depende de si el path literal del symlink matchea, no el target.

### D4. Stdout para info/warn, exit 2 para block

**Decision:** Todos los severities emiten a stdout. `block` ademas sale con exit code 2.

**Rationale:**
- Claude Code lee stdout del hook PreToolUse como "system reminder" que se inyecta en el contexto del agente. Esto es lo que permite que el agente "vea" las rules relevantes justo antes de actuar.
- Exit code 0 = permitir la accion. El agente ve el reminder pero puede proceder.
- Exit code 2 = rechazar la accion. Claude Code muestra el mensaje de rechazo y no ejecuta la herramienta. Esto es semantica documentada de Claude Code hooks (`internal/cli/hook.go:168-169`).
- Exit code 1 = error del hook (Claude Code lo trata como fallo del sistema, no como rechazo). NO se usa para block.
- stderr se usa solo para logging/diagnostics que el agente no debe ver como instrucciones.

### D5. Severity efectiva = max cuando multiples rules matchean

**Decision:** Cuando varias rules matchean el mismo tool+path, la severity efectiva es el maximo de todas las severities matcheadas.

**Rationale:**
- Si una rule dice `severity=info, applies_to=["**"]` y otra dice `severity=block, applies_to=["internal/**"]`, un Edit a `internal/foo.go` debe bloquearse (no solo informar).
- El "max" es trivial de computar: `severityOrder(block)=3 > severityOrder(warn)=2 > severityOrder(info)=1`. Se reutiliza `severityOrder()` de `internal/service/context.go:351-362`.
- Todas las rules matcheadas se incluyen en el stdout (no solo la de mayor severity), agrupadas por severity descendente. Asi el agente ve todo el contexto relevante.

### D6. No auto-crear "AllowedPaths legacy" como rule global

**Decision:** NO se auto-crean rules a partir de las `AllowedPaths` del `DelegationConfig` actual.

**Rationale:**
- Las `AllowedPaths` actuales (`["docs/", "*.md", "CLAUDE.md", "CLAUDE.local.md"]`) son configuracion de un mecanismo que se esta reemplazando. Migrarlas automaticamente a rules seria crear estado magico sin consentimiento del usuario.
- El nuevo hook `pre-tool-use` consulta rules de la DB, no el `DelegationConfig`. Si el usuario quiere proteger paths, debe crear rules explicitamente via `mem_save` con `type=rule`.
- El alias `enforce-delegation` sigue usando `DelegationConfig` directamente (backward compat total). Cuando el usuario migra a `pre-tool-use`, crea sus propias rules.
- Se documentara en la seccion de migracion como crear rules equivalentes a su configuracion actual.

### D7. Performance: <50ms target y como se logra

**Decision:** El hook debe completar en <50ms en el p99 con hasta 200 rules.

**Rationale:**
- El hook se ejecuta en cada Edit/Write/MultiEdit. Un hook lento degrada la experiencia del agente notablemente.
- El bottleneck es la DB. Mitigaciones:
  1. **Read-only open** (`mode=ro`): sin WAL writer setup, sin checkpoint, sin fsync. Medido en `db.SchemaVersion`: <5ms para open+query+close.
  2. **Single query:** `SELECT id, title, content, applies_to, severity FROM memories WHERE type='rule' AND deleted_at IS NULL LIMIT 200`. Sin JOINs, sin FTS5, sin scoring. Hits the partial index `idx_memories_rules` creado en migration 006.
  3. **Matching in-memory:** El glob matching con doublestar es O(len(pattern)*len(path)) por regla. Con 200 rules y 3 patterns promedio por rule, son 600 comparaciones de string — microsegundos.
  4. **Lazy deserialization:** Solo se deserializa el `applies_to` JSON de cada rule. No se cargan embeddings, relations, ni metadata pesada.
  5. **Busy timeout 1000ms** (no 5000ms como en `Open`): si la DB esta locked (otro proceso escribiendo), el hook espera max 1s y luego falla gracefully (allow).
- Con 1000 rules el hook puede exceder 50ms por I/O de lectura. Mitigacion: cap en `LIMIT 200` en la query (consistente con `loadActiveRules` en `context.go:311`). Si un proyecto tiene >200 rules, las de menor importance se ignoran en el hook.

---

## 3. Modelo del matcher

### Pseudocodigo del engine (`internal/rules/match.go`)

```go
// MatchResult holds the outcome of matching a set of rules against a tool invocation.
type MatchResult struct {
    Matched  []MatchedRule // rules that matched, sorted by severity desc
    MaxSev   model.Severity
}

// MatchedRule pairs a rule with the specific entries that caused the match.
type MatchedRule struct {
    Rule     model.Memory
    Entries  []string // which applies_to entries matched
}

// Match evaluates all rules against the given tool invocation.
// cwd is the working directory used to relativize file_path.
// Returns only rules whose applies_to patterns match.
func Match(rules []model.Memory, toolName string, filePath string, cwd string) MatchResult {
    pathRel := ""
    outOfTree := false
    if filePath != "" {
        rel, err := filepath.Rel(cwd, filePath)
        if err != nil || strings.HasPrefix(rel, "..") {
            outOfTree = true
        } else {
            pathRel = filepath.ToSlash(rel) // normalize to forward slashes for matching
        }
    }

    var matched []MatchedRule
    maxSev := model.Severity("")

    for _, rule := range rules {
        if ok, entries := matchRule(rule.AppliesTo, toolName, pathRel, outOfTree); ok {
            matched = append(matched, MatchedRule{Rule: rule, Entries: entries})
            if severityOrder(rule.Severity) > severityOrder(maxSev) {
                maxSev = rule.Severity
            }
        }
    }

    // Sort matched rules by severity desc for output ordering
    sort.Slice(matched, func(i, j int) bool {
        return severityOrder(matched[i].Rule.Severity) > severityOrder(matched[j].Rule.Severity)
    })

    return MatchResult{Matched: matched, MaxSev: maxSev}
}

// matchRule checks whether a single rule's applies_to entries match the invocation.
// Returns true if at least one positive entry matches and no negative entry vetoes.
func matchRule(appliesTo []string, toolName string, pathRel string, outOfTree bool) (bool, []string) {
    positives, negatives := splitEntries(appliesTo)

    // Check negatives first: if any negative matches, the rule does not apply.
    for _, neg := range negatives {
        pattern := strings.TrimPrefix(neg, "!")
        if entryMatch(pattern, toolName, pathRel, outOfTree) {
            return false, nil
        }
    }

    // At least one positive must match.
    var matchedEntries []string
    for _, pos := range positives {
        if entryMatch(pos, toolName, pathRel, outOfTree) {
            matchedEntries = append(matchedEntries, pos)
        }
    }

    return len(matchedEntries) > 0, matchedEntries
}

// splitEntries separates applies_to into positive and negative entries.
func splitEntries(entries []string) (positives []string, negatives []string) {
    for _, e := range entries {
        if strings.HasPrefix(e, "!") {
            negatives = append(negatives, e)
        } else {
            positives = append(positives, e)
        }
    }
    return
}

// entryMatch checks a single (positive) entry against the tool invocation.
func entryMatch(entry string, toolName string, pathRel string, outOfTree bool) bool {
    if entry == "**" {
        return true
    }

    if strings.Contains(entry, "+") {
        // Combined selector: all parts must match independently.
        parts := strings.SplitN(entry, "+", 2)
        return entryMatch(parts[0], toolName, pathRel, outOfTree) &&
               entryMatch(parts[1], toolName, pathRel, outOfTree)
    }

    if strings.HasPrefix(entry, "tool:") {
        return toolName == strings.TrimPrefix(entry, "tool:")
    }

    // Path glob — cannot match if path is out of tree
    if outOfTree || pathRel == "" {
        return false
    }

    matched, _ := doublestar.Match(entry, pathRel)
    return matched
}
```

### Invariantes del matcher

1. Una rule con `applies_to=["**"]` matchea **toda** invocacion (cualquier tool, cualquier path).
2. Una rule con `applies_to=["tool:Edit"]` matchea **todo** Edit, sin importar path.
3. Una rule con `applies_to=["internal/**"]` matchea cuando el path esta en internal/, sin importar tool.
4. Negaciones excluyen: `["**", "!docs/**"]` matchea todo excepto paths en docs/.
5. Negaciones sin positivos: `["!docs/**"]` nunca matchea (no hay positivos que satisfacer).
6. Combined entry con `+`: es AND de sus partes. `["tool:Edit+internal/**"]` = Edit AND path en internal/.
7. Paths out-of-tree (fuera del CWD): solo matchean por tool selector, nunca por path glob.

---

## 4. Contratos

### 4.1. CLI: `mneme hook pre-tool-use`

**Invocacion:**

```
mneme hook pre-tool-use
```

No recibe argumentos. Toda la informacion viene por stdin.

**Stdin format (JSON, una linea):**

Claude Code pasa el siguiente JSON en stdin al hook PreToolUse:

```json
{
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "/absolute/path/to/file.go",
    "old_string": "...",
    "new_string": "..."
  }
}
```

Campos relevantes para el hook:
- `tool_name` (string): nombre de la herramienta. El hook solo procesa `Edit`, `Write`, `MultiEdit`. Otros tools pasan sin check (exit 0).
- `tool_input.file_path` (string): path absoluto del archivo objetivo.

Campos ignorados: `old_string`, `new_string`, `content`, `command`, etc.

**Stdout format (markdown, cuando hay rules matcheadas):**

```markdown
<!-- mneme:rules:start -->
## mneme — Rules for this action

**Tool:** Edit | **File:** internal/store/memory.go

### [BLOCK] Never store plain passwords
Always use bcrypt with cost >= 12.
_Applies to: tool:Edit+internal/**/*.go_

---

### [WARN] SQL in .sql files only
No inline SQL in Go code.
_Applies to: **/*.go, !**/*_test.go_

---

**Action: BLOCKED** — 1 block rule matched. The agent must find an alternative approach.
<!-- mneme:rules:end -->
```

Cuando no hay rules matcheadas: stdout vacio.

Cuando hay rules matcheadas pero ninguna es block: stdout con el markdown pero sin la linea "Action: BLOCKED" y exit 0.

Cuando hay reglas info/warn matcheadas:
```markdown
<!-- mneme:rules:start -->
## mneme — Rules for this action

**Tool:** Edit | **File:** internal/store/memory.go

### [WARN] SQL in .sql files only
No inline SQL in Go code.
_Applies to: **/*.go, !**/*_test.go_

---

**Action: ALLOWED** — 1 rule matched (review above).
<!-- mneme:rules:end -->
```

**Exit codes:**

| Code | Significado | Cuando |
|------|-------------|--------|
| 0 | Permitido | Sin rules matcheadas, o rules info/warn matcheadas |
| 2 | Bloqueado | Al menos una rule con severity=block matchea |

Exit code 1 se reserva para errores inesperados del hook (crash, panic). En la practica, el hook NUNCA retorna exit 1 — todos los errores (DB no existe, JSON malformado, etc.) resultan en exit 0 (fail open).

### 4.2. Alias: `mneme hook enforce-delegation`

Se mantiene por backward compatibility. Comportamiento:

1. Imprime un deprecation warning a stderr:
   ```
   [mneme] WARNING: "mneme hook enforce-delegation" is deprecated. Use "mneme hook pre-tool-use" instead.
   [mneme] Run "mneme install claude-code --reinstall-hooks" to update your settings.json.
   ```
2. Ejecuta la logica legacy actual (config-based, `DelegationConfig`). NO delega al nuevo hook para evitar doble procesamiento y porque la semantica es distinta (el legacy usa config.toml, el nuevo usa rules de la DB).
3. Exit codes sin cambios (0=allow, 2=block).

**Razon:** Redirigir al nuevo hook romperia a usuarios que no tienen rules en su DB pero si tienen `DelegationConfig` en config.toml. El alias legacy y el nuevo hook coexisten como entries separadas en settings.json. Cuando el usuario migra, borra el legacy y queda solo el nuevo.

### 4.3. Install: cambios al settings.json template

**DelegationHook actualizado en `internal/install/install.go`:**

El campo `DelegationHook` en `ClaudeCode()` (`install.go:171-184`) se actualiza para registrar **ambos** hooks:

```go
DelegationHook: func() (string, []HookPatch, error) {
    // ...
    patches := []HookPatch{
        {
            Event:   "PreToolUse",
            Command: "mneme hook pre-tool-use",
        },
    }
    return path, patches, nil
},
```

**Decision sobre overwrite vs. append:**

Se usa `PatchHooks` que ya implementa append-if-not-present (`install.go:259-327`, `hookCommandExists`). Si el usuario ya tiene `mneme hook enforce-delegation` en PreToolUse, el installer **no lo borra** — agrega `mneme hook pre-tool-use` como un segundo entry. Ambos coexisten: el legacy hace su check config-based, el nuevo hace su check rule-based. No hay conflicto porque ambos salen con exit 0 cuando permiten o exit 2 cuando bloquean — Claude Code trata cada hook independientemente.

**Nuevo install vs. reinstall:**

- **Nuevo install** (`mneme install claude-code`): registra solo `mneme hook pre-tool-use` (no el legacy).
- **Reinstall** (`mneme install claude-code --reinstall-hooks`, nuevo flag): borra todas las entries PreToolUse existentes y registra solo `mneme hook pre-tool-use`.

El flag `--reinstall-hooks` es nuevo. Requiere agregar logica en `PatchHooks` para "replace-all" en un event dado.

**settings.json resultado (nuevo install):**

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "mneme hook session-start"}]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "mneme hook session-end"}]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "mneme hook pre-tool-use"}]
      }
    ]
  }
}
```

---

## 5. Paquetes y archivos afectados

| Paquete | Archivo | Tipo de cambio |
|---------|---------|---------------|
| `internal/rules/` | `match.go` | **nuevo** — matching engine |
| `internal/rules/` | `match_test.go` | **nuevo** — table-driven tests |
| `internal/db/` | `db.go` | modificacion — agregar `OpenReadOnly()` |
| `internal/cli/` | `hook.go` | modificacion — agregar `runHookPreToolUse()`, deprecation en `enforce-delegation` |
| `internal/cli/` | `hook_test.go` | modificacion — tests para `pre-tool-use` |
| `internal/install/` | `install.go` | modificacion — actualizar `DelegationHook`, agregar `--reinstall-hooks` |
| `internal/install/` | `install_test.go` | modificacion — tests para nuevo hook registration |
| `docs/` | `HOOKS.md` | modificacion — documentar `pre-tool-use`, deprecation, migracion |
| `go.mod` | | modificacion — agregar `github.com/bmatcuk/doublestar/v4` |

### Fuera de scope

- SPEC-R4 (`mneme rule add/list/test`) — CLI para crear rules. Esta spec asume que las rules ya existen en la DB (creadas via `mem_save` con `type=rule`).
- Matching de rules en MCP tools (solo el hook CLI esta en scope).
- Cache de rules entre invocaciones del hook (cada invocacion es un proceso nuevo — no hay cache inter-proceso sin un daemon).
- HTTP frontend para rules (fuera del scope de EPIC-1).

---

## 6. Edge cases

### 6.1. Stdin vacio

**Comportamiento:** `json.NewDecoder(os.Stdin).Decode()` retorna `io.EOF`. El hook imprime nada a stdout y sale con exit 0 (allow). No imprime error a stderr.

**Rationale:** Un stdin vacio puede ocurrir si el hook se invoca manualmente sin pipe. Fail open.

### 6.2. Stdin con JSON malformado

**Comportamiento:** `json.Decode()` retorna `*json.SyntaxError`. El hook sale con exit 0 (allow). Imprime warning a stderr: `[mneme] pre-tool-use hook: invalid stdin JSON: <error>`.

**Rationale:** Fail open. El hook no debe bloquear acciones por un error de parseo.

### 6.3. tool_name desconocido (ej: "Bash", "Read", "Grep")

**Comportamiento:** El hook solo procesa `Edit`, `Write`, `MultiEdit`. Para cualquier otro tool_name, sale con exit 0 inmediatamente sin consultar la DB.

**Rationale:** Solo tools que mutan archivos tienen file_path y son relevantes para rules basadas en paths. Futuro: si se quieren rules sobre Bash, seria un mecanismo distinto.

### 6.4. file_path absoluto fuera del CWD

**Ejemplo:** `file_path="/etc/hosts"`, CWD=`/Users/x/project`.

**Comportamiento:** `filepath.Rel` produce `../../etc/hosts` (con `..`). El flag `outOfTree=true` se activa. Solo los tool selectors matchean (ej: `tool:Edit`). Los path globs no matchean porque el path no esta dentro del arbol del proyecto.

**Rationale:** Las rules con path globs son relativas al proyecto. Un path fuera del arbol no tiene relacion con los patterns del proyecto. Pero un tool selector (`tool:Edit`) o un wildcard (`**`) si matchean porque no dependen del path.

### 6.5. file_path con `..` que escapa CWD

**Ejemplo:** `file_path="../other-project/secret.go"`.

**Comportamiento:** Igual que 6.4. `filepath.Rel(cwd, abs)` produce un path con `..` prefix. `outOfTree=true`.

### 6.6. Symlinks

**Comportamiento:** No se resuelven. El matcher opera sobre el path literal de `tool_input.file_path`. Un symlink `link.go -> internal/store/memory.go` se matchea como `link.go`, no como `internal/store/memory.go`.

**Rationale:** Resolver symlinks requiere `os.EvalSymlinks()` — un syscall adicional. Los symlinks en codebases Go son raros. Si un usuario necesita proteger el target, debe agregar el path del symlink a sus rules.

### 6.7. DB lock (otro proceso tiene write lock)

**Comportamiento:** La apertura read-only con `mode=ro` y `_busy_timeout=1000` espera hasta 1s. Si el lock persiste, `sql.Open` o la query retornan error. El hook imprime warning a stderr y sale con exit 0 (allow).

**Rationale:** Fail open. Un lock de escritura (ej: consolidacion en progreso) no debe bloquear al agente de editar archivos. Las rules se re-evaluaran en la proxima invocacion.

### 6.8. DB no existe (proyecto nuevo sin mneme inicializado)

**Comportamiento:** `os.Stat(dbPath)` retorna `os.ErrNotExist`. El hook sale con exit 0 sin query. No imprime nada a stdout ni stderr.

**Rationale:** Un proyecto sin mneme no tiene rules. Fail silencioso.

### 6.9. Rule con `applies_to=["+"]` (entry vacio despues del split)

**Comportamiento:** `strings.SplitN("+", "+", 2)` produce `["", ""]`. `entryMatch("", ...)` retorna false (string vacio no matchea ningun tool ni path). La rule efectivamente nunca matchea.

**Rationale:** Esto deberia ser prevenido por la validacion en `mem_save` (SPEC-001 ya valida `no empty strings` en applies_to). Si llega a la DB de alguna manera, el matcher lo ignora silenciosamente.

### 6.10. Rule con `applies_to=["tool:Foo+tool:Bar"]` (dos tool selectors)

**Comportamiento:** `SplitN("tool:Foo+tool:Bar", "+", 2)` produce `["tool:Foo", "tool:Bar"]`. `entryMatch("tool:Foo")` check `toolName == "Foo"`, `entryMatch("tool:Bar")` check `toolName == "Bar"`. Dado que un tool solo tiene un nombre, ambos no pueden ser true simultaneamente. La entry nunca matchea.

**Validacion:** SPEC-001 no valida la semantica de las entries. Se recomienda agregar validacion en `mem_save` para detectar `tool:X+tool:Y` como error (push a SPEC-004 o fix directo), pero el matcher de SPEC-003 maneja el caso gracefully (simplemente no matchea).

### 6.11. Rule con `applies_to=["tool:Edit+!internal/**"]`

**Comportamiento:** `SplitN("tool:Edit+!internal/**", "+", 2)` produce `["tool:Edit", "!internal/**"]`. El segundo part empieza con `!`, lo que significa negacion. Pero dentro de `entryMatch` (que solo maneja positivos), se evalua como un path glob literal `!internal/**` — que no matcheara ningun path real.

**Resolucion de diseno:** La negacion `!` solo tiene semantica a nivel de entry en `applies_to`, no dentro de un combined `+` expression. Para expresar "Edit en todo excepto internal/", se usan entries separadas: `["tool:Edit", "!internal/**"]` (ambas como entries del array, no combinadas con `+`).

**Documentacion:** Se documenta esta limitacion en docs/HOOKS.md.

### 6.12. Performance bajo carga (1000 rules)

**Comportamiento:** La query tiene `LIMIT 200` (consistente con `loadActiveRules` en `context.go:311`). Solo las 200 rules de mayor importance se evaluan. Con 200 rules y ~3 entries promedio por rule, el matching toma <1ms (pura CPU, sin I/O).

**Mitigacion si 200 no es suficiente:** El LIMIT es configurable via `config.toml` en una futura iteracion. Para SPEC-003, 200 es un hardcoded cap.

---

## 7. Flujo de datos del hook

```
Claude Code
  |
  | PreToolUse event fires
  | stdin: {"tool_name":"Edit","tool_input":{"file_path":"/abs/path"}}
  |
  v
mneme hook pre-tool-use
  |
  +-- Parse stdin JSON
  +-- Check tool_name in {Edit, Write, MultiEdit}? No -> exit 0
  +-- Detect project (project.NewDetector(cwd))
  +-- Compute DB paths (config.ProjectDBPath, config.GlobalDBPath)
  +-- Open project DB (read-only) + global DB (read-only)
  +-- Query: SELECT id,title,content,applies_to,severity
  |         FROM memories WHERE type='rule' AND deleted_at IS NULL
  |         ORDER BY importance DESC LIMIT 200
  +-- Close DBs immediately
  +-- rules.Match(allRules, toolName, filePath, cwd) -> MatchResult
  +-- If no matches: exit 0 (empty stdout)
  +-- Render matched rules as markdown to stdout
  +-- If MatchResult.MaxSev == block: exit 2
  +-- Else: exit 0
```

---

## 8. Plan de implementacion

Pasos atomicos, cada uno commit-able y con tests verdes:

### Paso 1: `internal/rules/` — matching engine (modelo puro)

- Crear `internal/rules/match.go` con `Match()`, `matchRule()`, `splitEntries()`, `entryMatch()`, `severityOrder()`.
- Agregar `github.com/bmatcuk/doublestar/v4` a go.mod.
- Crear `internal/rules/match_test.go` con table-driven tests exhaustivos (ver seccion 9).
- **No tocar** ningun otro paquete. El engine es standalone.

### Paso 2: `internal/db/` — `OpenReadOnly()`

- Agregar `func OpenReadOnly(path string) (*DB, error)` a `db.go`. DSN: `file:%s?mode=ro&_foreign_keys=ON&_busy_timeout=1000`. No ejecuta `migrate()`. Retorna error si file not exists.
- Agregar test en `db_test.go` o nuevo `db_readonly_test.go`.

### Paso 3: `internal/cli/hook.go` — `runHookPreToolUse()`

- Agregar case `"pre-tool-use"` al switch en `newHookCmd()` (`hook.go:42`).
- Implementar `runHookPreToolUse()`:
  1. Parse stdin JSON (reuse struct de `runHookEnforceDelegation` pero extender si necesario).
  2. Check tool_name in allowed set.
  3. Detect project, compute DB paths.
  4. Open DBs read-only, query rules, close.
  5. Call `rules.Match()`.
  6. Render output, set exit code.
- Agregar deprecation warning a `runHookEnforceDelegation()`.
- Tests en `hook_test.go`.

### Paso 4: `internal/install/install.go` — actualizar hook registration

- Cambiar `DelegationHook` en `ClaudeCode()` para registrar `mneme hook pre-tool-use`.
- Agregar logica de `--reinstall-hooks` flag al CLI command `install`.
- Tests en `install_test.go`.

### Paso 5: `docs/HOOKS.md` — documentacion

- Documentar `pre-tool-use`: setup, formato, exit codes.
- Documentar deprecation de `enforce-delegation`.
- Agregar seccion de migracion (tabla antes/despues, comando de migracion).
- Agregar FAQ.

### Paso 6: Integration test end-to-end

- Test que crea una in-memory DB, guarda rules, invoca el matcher, y verifica output completo.

---

## 9. Tests requeridos

### 9.1. `internal/rules/match_test.go` — matching engine (table-driven)

| Test | Input | Expected |
|------|-------|----------|
| WildcardMatchesAll | `applies_to=["**"]`, tool=Edit, path=any | match=true |
| ToolSelectorOnly | `applies_to=["tool:Edit"]`, tool=Edit | match=true |
| ToolSelectorMismatch | `applies_to=["tool:Write"]`, tool=Edit | match=false |
| PathGlobMatch | `applies_to=["internal/**/*.go"]`, path=`internal/store/foo.go` | match=true |
| PathGlobMismatch | `applies_to=["internal/**/*.go"]`, path=`docs/README.md` | match=false |
| CombinedToolAndPath | `applies_to=["tool:Edit+internal/**"]`, tool=Edit, path=`internal/x.go` | match=true |
| CombinedToolMismatch | `applies_to=["tool:Edit+internal/**"]`, tool=Write, path=`internal/x.go` | match=false |
| CombinedPathMismatch | `applies_to=["tool:Edit+internal/**"]`, tool=Edit, path=`docs/x.md` | match=false |
| NegationExcludes | `applies_to=["**", "!docs/**"]`, path=`docs/README.md` | match=false |
| NegationAllowsOther | `applies_to=["**", "!docs/**"]`, path=`internal/x.go` | match=true |
| NegationOnlyNeverMatches | `applies_to=["!docs/**"]` | match=false |
| OutOfTreeToolSelector | path=`/etc/hosts` (out of tree), `applies_to=["tool:Edit"]` | match=true |
| OutOfTreePathGlob | path=`/etc/hosts` (out of tree), `applies_to=["internal/**"]` | match=false |
| OutOfTreeWildcard | path=`/etc/hosts`, `applies_to=["**"]` | match=true |
| EmptyPathToolOnly | path=`""`, `applies_to=["tool:Edit"]` | match=true |
| EmptyPathPathGlob | path=`""`, `applies_to=["internal/**"]` | match=false |
| MultiplePosAnyMatches | `applies_to=["internal/**", "cmd/**"]`, path=`cmd/main.go` | match=true |
| MaxSeverityBlock | rules: [info on **, block on internal/**], path=internal/x.go | MaxSev=block |
| MaxSeverityWarn | rules: [info on **, warn on internal/**], path=internal/x.go | MaxSev=warn |
| NoRules | rules=[] | Matched=[], MaxSev="" |
| InvalidEntryPlus | `applies_to=["+"]` | match=false |
| TwoToolSelectors | `applies_to=["tool:Foo+tool:Bar"]` | match=false |
| CombinedWithNegation | `applies_to=["tool:Edit+!internal/**"]` | match depends on entryMatch semantics (literal `!internal/**` path never matches) |
| DotDotEscape | path=`../secret/file.go` | outOfTree=true, only tool selectors match |
| CaseSensitiveTool | `applies_to=["tool:edit"]`, tool=Edit | match=false |
| NestedDoublestar | `applies_to=["**/test/**"]`, path=`a/b/test/c/d.go` | match=true |

### 9.2. `internal/db/db_test.go` — OpenReadOnly

| Test | Scenario | Expected |
|------|----------|----------|
| ReadOnlySuccess | Existing migrated DB | Opens, query works, close succeeds |
| ReadOnlyFileNotExist | Path does not exist | Returns error (not creates file) |
| ReadOnlyNoWrite | Try INSERT | Returns error (readonly) |

### 9.3. `internal/cli/hook_test.go` — pre-tool-use hook output

| Test | Scenario | Expected |
|------|----------|----------|
| PreToolUse_BlockRule | block rule matches | stdout contains `[BLOCK]`, `Action: BLOCKED` |
| PreToolUse_WarnRule | warn rule matches | stdout contains `[WARN]`, `Action: ALLOWED` |
| PreToolUse_InfoRule | info rule matches | stdout contains `[INFO]`, `Action: ALLOWED` |
| PreToolUse_NoMatch | no rules match | stdout empty |
| PreToolUse_NonMutatingTool | tool=Read | stdout empty, exit 0 |
| PreToolUse_MultipleSeverities | block+warn+info match | MaxSev=block, all rendered, block first |
| PreToolUse_MalformedJSON | stdin not valid JSON | stdout empty, exit 0 |
| PreToolUse_EmptyStdin | stdin empty | stdout empty, exit 0 |

### 9.4. `internal/cli/hook_test.go` — enforce-delegation deprecation

| Test | Scenario | Expected |
|------|----------|----------|
| EnforceDelegation_DeprecationWarning | Normal invocation | stderr contains "deprecated" |
| EnforceDelegation_StillWorks | Protected path | exit 2 (legacy behavior) |

### 9.5. `internal/install/install_test.go` — hook registration

| Test | Scenario | Expected |
|------|----------|----------|
| NewInstall_RegistersPreToolUse | Fresh install | settings.json has `pre-tool-use` in PreToolUse |
| ReinstallHooks_ReplacesLegacy | Existing `enforce-delegation` | settings.json has only `pre-tool-use` |

---

## 10. Criterios de aceptacion

1. **`mneme hook pre-tool-use` con rule severity=block matcheando sale con exit 2** y stdout contiene `[BLOCK]` tag y `Action: BLOCKED` mensaje.

2. **`mneme hook pre-tool-use` con rule severity=warn/info matcheando sale con exit 0** y stdout contiene markdown con `[WARN]`/`[INFO]` tags y `Action: ALLOWED`.

3. **El matching engine resuelve correctamente**: wildcards (`**`), tool selectors (`tool:Edit`), combined (`tool:Edit+internal/**`), negaciones (`!docs/**`), y paths out-of-tree (solo tool selectors matchean).

4. **Performance: el hook completa en <50ms** (p99) con 200 rules en una DB SQLite de proyecto tipico (medido con benchmark test).

5. **`mneme hook enforce-delegation` sigue funcionando** con la logica legacy (config-based) y emite deprecation warning a stderr.

6. **`mneme install claude-code` registra `mneme hook pre-tool-use`** en PreToolUse de `~/.claude/settings.json`.

---

## 11. Migracion del usuario

### Tabla antes vs. despues

| Aspecto | Antes (enforce-delegation) | Despues (pre-tool-use) |
|---------|---------------------------|------------------------|
| Fuente de reglas | `config.toml` [delegation] | DB rules (type=rule via mem_save) |
| Matching | Prefix string + filepath.Match | Doublestar globs + tool selectors + negaciones |
| Granularidad | "todo path con este prefix" | Per-tool, per-path-pattern, con excepciones |
| Severity | Solo block | info, warn, block |
| Contexto al agente | Solo "BLOCKED: ..." | Markdown completo con titulo, contenido, patterns |
| settings.json hook | `mneme hook enforce-delegation` | `mneme hook pre-tool-use` |

### Comando de migracion

```bash
mneme install claude-code --reinstall-hooks
```

Este comando:
1. Borra todas las entries PreToolUse existentes en `~/.claude/settings.json`.
2. Agrega `mneme hook pre-tool-use` como unica entry PreToolUse.
3. Imprime instrucciones para crear rules equivalentes a su DelegationConfig actual:

```
Migration complete. Your hooks have been updated.

To recreate your protected paths as rules, run:

  mneme save --type rule --severity block \
    --applies-to "tool:Edit+cmd/**" --applies-to "tool:Write+cmd/**" --applies-to "tool:MultiEdit+cmd/**" \
    --applies-to "tool:Edit+internal/**" --applies-to "tool:Write+internal/**" --applies-to "tool:MultiEdit+internal/**" \
    --title "Delegation: protect source paths" \
    "Delegate code edits in protected paths to the appropriate subagent (backend, frontend, etc.)."

Your old config.toml [delegation] section is still active for the legacy hook.
Once you've created rules and verified they work, you can set delegation.enabled=false in config.toml.
```

### FAQ (para docs/HOOKS.md)

**Q: Puedo usar ambos hooks simultaneamente?**
A: Si. `enforce-delegation` usa config.toml y `pre-tool-use` usa rules de la DB. Ambos se ejecutan independientemente en PreToolUse. Si alguno sale con exit 2, la accion se bloquea.

**Q: Tengo enforce-delegation configurado. Necesito migrar ahora?**
A: No. El hook legacy sigue funcionando. Migracion es opcional hasta que se anuncie remocion (no antes de v3).

**Q: Que pasa si no tengo rules en la DB?**
A: `pre-tool-use` sale con exit 0 (permite todo). El hook no hace nada si no hay rules.

**Q: Como creo una rule?**
A: Via MCP: `mem_save({type:"rule", severity:"block", applies_to:["tool:Edit+internal/**"], title:"...", content:"..."})`. Via CLI: `mneme save --type rule --severity block --applies-to "tool:Edit+internal/**" --title "..." "content"`. (SPEC-004 agregara `mneme rule add` como shortcut.)

**Q: El hook es lento?**
A: Target <50ms. Abre la DB en read-only, hace una query simple, y cierra. Sin scoring, sin embeddings, sin migraciones.

---

## 12. Open questions

Ninguna cuestion bloqueante identificada. Todas las ambiguedades fueron resueltas durante el diseno con decisiones explicitas y rationale documentado.

Cuestiones menores que podrian refinarse post-implementacion:
1. El cap de 200 rules en el hook es arbitrario — si un proyecto tiene mas, deberia ser configurable. Se deja para una iteracion futura.
2. La validacion de `tool:X+tool:Y` como error en `mem_save` es una mejora ortogonal. Se recomienda como fix independiente o parte de SPEC-004.

---

## Dependencias

- **SPEC-001** (completada): Tipo `rule` con `applies_to` y `severity` en el modelo y la DB.
- **SPEC-002** (completada): `loadActiveRules` pattern en service, `printContextHook` rendering.
- **`github.com/bmatcuk/doublestar/v4`**: Dependencia nueva para glob matching con `**`. No esta en go.mod actual.
