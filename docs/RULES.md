# mneme -- Rules System

Las reglas son memorias de tipo `rule` que establecen constraints vinculantes para agentes AI. A diferencia de conventions (que son guias), las reglas se **ejecutan activamente** via hooks y se inyectan obligatoriamente en el contexto.

Introducidas en SPEC-001..004 (EPIC-1).

---

## Table of Contents

1. [Modelo mental](#modelo-mental)
2. [Sintaxis applies_to](#sintaxis-applies_to)
3. [Severity tradeoffs](#severity-tradeoffs)
4. [CLI: mneme rule](#cli-mneme-rule)
5. [Como funciona el hook](#como-funciona-el-hook)
6. [Inyeccion en contexto](#inyeccion-en-contexto)
7. [Ejemplos por stack](#ejemplos-por-stack)
8. [FAQ](#faq)

---

## Modelo mental

```
Regla = Memoria + applies_to + severity
```

| Aspecto | Convention | Rule |
|---------|-----------|------|
| Tipo | `convention` | `rule` |
| Enforcement | Pasivo (el agente la lee si la encuentra) | Activo (inyectada en contexto + hook) |
| Decay | Normal (0.005/dia) | Inmune (decay_rate = 0) |
| applies_to | No tiene | Obligatorio |
| severity | No tiene | info / warn / block |
| Hook integration | No | Si (pre-tool-use) |

Una regla existe hasta que se revoca explicitamente con `mem_forget` o se modifica con `mem_update`.

---

## Sintaxis applies_to

El campo `applies_to` es un array de patrones que determinan cuando aplica la regla. Cada patron puede ser:

### Path globs

Matchean contra el file path relativo al directorio del proyecto.

```
internal/**/*.go     Cualquier .go bajo internal/ (recursivo)
*.test.ts            Archivos de test TypeScript en la raiz
src/api/**           Todo bajo src/api/
vendor/**            Todo bajo vendor/
```

Usa doublestar (`**`) para recursion y single star (`*`) para un nivel.

### Tool selectors

Matchean contra el nombre del tool (case-sensitive).

```
tool:Edit            Cualquier llamada a Edit
tool:Write           Cualquier llamada a Write
tool:MultiEdit       Cualquier llamada a MultiEdit
```

### Combined selectors (AND)

Tool + path separados por `+`. Ambos deben matchear.

```
tool:Edit+internal/**        Edit Y path dentro de internal/
tool:Write+**/*.sql          Write Y archivos .sql
tool:MultiEdit+cmd/**/*.go   MultiEdit Y archivos Go en cmd/
```

### Negaciones

Prefijo `!` veta la regla cuando el patron matchea. Util para excepciones.

```
!docs/**             No aplica si el path esta en docs/
!*.md                No aplica para archivos markdown
!test/**             No aplica para archivos de test
```

### Wildcard global

```
**                   Aplica a todo: cualquier tool, cualquier path
```

### Combinaciones

El array `applies_to` evalua como OR entre entries positivas, con veto por entries negativas:

```json
["internal/**/*.go", "cmd/**/*.go", "!*_test.go"]
```

Esto significa: "aplica a Go files en internal/ O cmd/, EXCEPTO test files".

### Notas importantes

- Tool selectors son **case-sensitive**: `tool:Edit` no es `tool:edit`
- La negacion (`!`) solo funciona como entry de top-level, no dentro de un `+` combined
- Paths son relativos al directorio de trabajo del proyecto
- Paths fuera del arbol del proyecto solo matchean tool selectors y `**`
- Los symlinks no se resuelven; el matching usa el path literal

---

## Severity tradeoffs

| Severity | Efecto en hook | Efecto en contexto | Cuando usarla |
|----------|---------------|-------------------|---------------|
| `info` | Exit 0, stdout con reminder | Inyectada con tag `[INFO]` | Guias suaves, buenas practicas, recordatorios |
| `warn` | Exit 0, stdout con warning | Inyectada con tag `[WARN]` | Reglas que el agente debe considerar pero puede overridear |
| `block` | **Exit 2** (tool call rechazado) | Inyectada con tag `[BLOCK]` | Prohibiciones absolutas, delegation enforcement |

### Cuando usar block

- Proteger paths de source code para delegation
- Prevenir edicion de archivos generados
- Prohibir patrones de codigo peligrosos
- Enforcement de compliance (passwords, secrets, etc.)

### Cuando usar warn

- Recordar convenciones de estilo
- Sugerir patrones preferidos
- Alertar sobre areas de alto riesgo

### Cuando usar info

- Documentar contexto que el agente debe conocer
- Recordar dependencias entre modulos
- Tips de performance o seguridad

---

## CLI: mneme rule

### `mneme rule add`

Crea una regla. Auto-genera `topic_key` desde el titulo para upserts idempotentes.

```bash
mneme rule add \
  --title "No vendor edits" \
  --content "Never edit files under vendor/. They are managed by dependency tools." \
  --applies-to "vendor/**" \
  --severity block

# Multiple patterns
mneme rule add \
  --title "Protect internal package" \
  --content "Delegate edits to the backend subagent." \
  --applies-to "tool:Edit+internal/**" \
  --applies-to "tool:Write+internal/**" \
  --applies-to "tool:MultiEdit+internal/**" \
  --severity block

# Global scope (applies to all projects)
mneme rule add \
  --title "Always use error wrapping" \
  --content "Wrap errors with fmt.Errorf and %w, never swallow." \
  --applies-to "**/*.go" \
  --severity warn \
  --scope global

# Read content from stdin
echo "Detailed instruction..." | mneme rule add \
  --title "My rule" \
  --applies-to "**" \
  --stdin
```

**Flags:**

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--title` | `-t` | required | Rule title |
| `--content` | `-c` | required | Rule content/instruction |
| `--applies-to` | `-a` | required | Pattern(s), repeatable |
| `--severity` | `-s` | `warn` | info, warn, or block |
| `--scope` | | `project` | project or global |
| `--topic-key` | `-k` | auto-generated | Topic key for upserts |
| `--importance` | `-i` | `0.95` | Importance override |
| `--stdin` | | false | Read content from stdin |

### `mneme rule list`

Muestra todas las reglas activas en una tabla coloreada por severity.

```bash
mneme rule list
mneme rule list --scope global
mneme rule list --severity block
mneme rule list --json | jq '.rules[].title'
```

Output ejemplo:
```
SEV    ID        TITLE                           APPLIES_TO                      SCOPE
-----  --------  ------------------------------  ------------------------------  -------
BLOCK  019de100  No vendor edits                 vendor/**                       project
WARN   019de101  Always use error wrapping        **/*.go                         global
INFO   019de102  Auth module documentation        internal/auth/**                project

3 rules (1 block, 1 warn, 1 info)
```

### `mneme rule test`

Evalua reglas contra una invocacion simulada, sin ejecutar nada.

```bash
mneme rule test --tool Edit --path vendor/foo/bar.go
mneme rule test --tool Write --path internal/store/memory.go
mneme rule test --tool Edit  # sin path, solo matchean tool selectors y **
mneme rule test --tool Edit --path docs/README.md --json
```

Output ejemplo:
```
Testing: tool=Edit          path=vendor/foo/bar.go

Evaluated: 3 rules
Matched:   1 rules

  [BLOCK] No vendor edits
         Never edit files under vendor/. They are managed by dependency tools.
         Matched by: vendor/**

Effective severity: block
Result: BLOCKED
```

---

## Como funciona el hook

El hook `mneme hook pre-tool-use` se registra como `PreToolUse` hook en Claude Code via `mneme install claude-code`.

### Flujo

```
Claude Code quiere Edit(file)
        |
        v
Invoca hook: mneme hook pre-tool-use
        |
        v
Hook lee stdin JSON: {"tool_name":"Edit","tool_input":{"file_path":"..."}}
        |
        v
Abre project + global DB en read-only mode
        |
        v
SELECT rules WHERE type='rule' AND deleted_at IS NULL (LIMIT 200)
        |
        v
Match rules contra tool + path (in-memory, <50ms)
        |
        v
  +-----------+     +-----------+     +-----------+
  | No match  |     | info/warn |     |   block   |
  |           |     |           |     |           |
  | stdout:   |     | stdout:   |     | stdout:   |
  | (vacio)   |     | markdown  |     | markdown  |
  |           |     | reminder  |     | + action  |
  | exit 0    |     | exit 0    |     | exit 2    |
  +-----------+     +-----------+     +-----------+
```

### Formato de salida (cuando hay match)

```markdown
<!-- mneme:rules:start -->
## mneme -- Rules for this action

**Tool:** Edit | **File:** internal/store/memory.go

### [BLOCK] Never store plain passwords
Always use bcrypt with cost >= 12.
_Applies to: tool:Edit+internal/**/*.go_

---

**Action: BLOCKED** -- 1 block rule matched. The agent must find an alternative approach.
<!-- mneme:rules:end -->
```

### Exit codes

| Code | Significado | Cuando |
|------|-------------|--------|
| 0 | Allow | No rules matched, o solo info/warn |
| 2 | Block | Al menos una regla block matched |

El hook **nunca** sale con code 1 -- todos los errores internos resultan en exit 0 (fail open).

### Performance

Target: <50ms. Mecanismos:
- DB abierta en read-only mode (`mode=ro`) -- no migraciones, no WAL writer
- Single `SELECT` con `LIMIT 200` contra indice parcial
- Matching in-memory despues del query
- Busy timeout de 1s; si la DB esta lockeada, el hook permite

---

## Inyeccion en contexto

`mem_context` (SPEC-002) siempre inyecta las reglas del scope activo ANTES de las memorias generales. Las reglas tienen un presupuesto de tokens separado del presupuesto general.

Esto garantiza que el LLM ve las constraints (especialmente `block`) antes de cualquier otro contenido, maximizando la probabilidad de que las respete.

El orden en el output es:

1. Last Session (si hay)
2. **Active Rules** (siempre primero, presupuesto separado)
3. Loaded Memories

---

## Ejemplos por stack

### Go

```bash
# Enforce error wrapping
mneme rule add -t "Always wrap errors" \
  -c "Use fmt.Errorf(\"context: %w\", err). Never swallow errors." \
  -a "**/*.go" -s warn

# Protect generated code
mneme rule add -t "Do not edit generated files" \
  -c "Files with //go:generate or _gen.go suffix are auto-generated." \
  -a "**/*_gen.go" -a "**/*_generated.go" -s block

# Architecture: no store from CLI
mneme rule add -t "CLI must not import store" \
  -c "CLI commands go through the service layer. Never import store directly." \
  -a "tool:Edit+internal/cli/**" -s warn
```

### Next.js / TypeScript

```bash
# No direct DB calls from components
mneme rule add -t "Components must use API routes" \
  -c "React components must never call Prisma or DB directly. Use API routes." \
  -a "tool:Edit+src/components/**" -a "tool:Write+src/components/**" -s warn

# Protect generated types
mneme rule add -t "Do not edit Prisma types" \
  -c "Types in node_modules/.prisma are generated. Run prisma generate instead." \
  -a "node_modules/.prisma/**" -s block
```

### Python

```bash
# Enforce type hints
mneme rule add -t "Use type hints" \
  -c "All function signatures must have type annotations." \
  -a "**/*.py" -a "!tests/**" -s info

# Protect migrations
mneme rule add -t "Do not edit migrations manually" \
  -c "Use alembic to generate migrations. Never edit migration files directly." \
  -a "alembic/versions/**" -s block
```

### Delegation (multi-agent)

```bash
# Protect source code from orchestrator
mneme rule add -t "Delegation: protect source paths" \
  -c "Delegate code edits in protected paths to the appropriate subagent." \
  -a "tool:Edit+cmd/**" -a "tool:Write+cmd/**" -a "tool:MultiEdit+cmd/**" \
  -a "tool:Edit+internal/**" -a "tool:Write+internal/**" -a "tool:MultiEdit+internal/**" \
  -s block
```

---

## FAQ

**Q: Se inyectan las reglas en cada turno?**
A: Las reglas se inyectan en `mem_context` al inicio de la sesion (via `session-start` hook) y se evaluan en cada llamada a Edit/Write/MultiEdit (via `pre-tool-use` hook). No se inyectan en cada turno de conversacion -- eso lo hace el hook cuando aplica.

**Q: Son overrideables las reglas?**
A: Las reglas `info` y `warn` son advisory -- el agente las recibe pero puede proceder. Las reglas `block` son absolutas: el hook rechaza el tool call con exit code 2 y Claude Code cancela la accion. No hay override en runtime; para deshabilitar una regla, usa `mem_forget` o `mem_update`.

**Q: Bloquean al architect/backend subagent?**
A: Si. El hook evalua reglas sin importar quien ejecuta. Para permitir que un subagent edite paths protegidos, crea las reglas con negaciones que lo permitan, o usa `severity: warn` en lugar de `block` para esos paths.

**Q: Que pasa si no hay reglas en la DB?**
A: `pre-tool-use` sale con code 0 (allow) -- no hace nada. `mem_context` omite la seccion "Active Rules".

**Q: Como desactivo el hook temporalmente?**
A: Remueve o comenta el entry `PreToolUse` en `~/.claude/settings.json`. Alternativamente, usa `mem_forget` en reglas individuales.

**Q: El hook es lento -- que puedo hacer?**
A: Target es <50ms. Si es mas lento, verifica que la DB del proyecto no sea inusualmente grande y que ningun otro proceso tenga un write lock largo. El busy timeout es 1s.

**Q: Puedo tener reglas globales y de proyecto?**
A: Si. Las reglas globales (`--scope global`) se almacenan en `global.db` y aplican a todos los proyectos. Las reglas de proyecto (`--scope project`, default) aplican solo al proyecto actual. El hook evalua ambas.

**Q: Que diferencia hay con el hook legacy `enforce-delegation`?**
A: `enforce-delegation` usa paths estaticos definidos en `config.toml`. El nuevo `pre-tool-use` usa reglas dinamicas almacenadas en la DB, con patrones mas expresivos (globs, negaciones, tool selectors) y 3 niveles de severity. Se recomienda migrar con `mneme install claude-code --reinstall-hooks`.

---

## See also

- [API.md](API.md) -- Full API reference for `mem_save` (type `rule`), `mem_context` (rule injection), CLI `mneme rule` commands
- [HOOKS.md](HOOKS.md) -- Hook system details (`session-start`, `session-end`, `pre-tool-use`)
- [CONFIG.md](CONFIG.md) -- Configuration reference including rule-related settings
