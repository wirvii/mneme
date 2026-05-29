# SPEC-034 — Permission Enforcement by Capability · Informe de implementación

> **Para:** agente de discusión de diseño.
> **Estado:** ✅ Implementado, mergeado (PR #4 → `main`, commit `ed34a81`) y liberado como **v1.4.0**.
> **Origen:** `docs/new/mneme-v0.9-spec-permission-enforcement.md` (la spec se tituló "v0.9" por naming de un roadmap viejo; el repo ya iba en v1.3.0, así que la release real es v1.4.0).
> **Fecha:** 2026-05-29.

---

## 1. Qué problema resuelve

En mneme los límites de rol entre subagentes estaban declarados en **prosa** dentro de cada `*.md` de agente, pero no se hacían cumplir a nivel de **capacidad**:

- `architect` y `qa-tester` decían "NUNCA implementar código" pero su frontmatter no tenía `tools:` → heredaban TODAS las herramientas del hilo principal (`Edit/Write/MultiEdit/Bash`). La regla era una petición, no una restricción.
- El orquestador (hilo principal) ya estaba contenido por un hook `PreToolUse`, pero **los bloqueos eran silenciosos**: no había telemetría de quién intenta violar reglas, contra qué archivos, cuántas veces.

SPEC-034 cierra ambos gaps.

---

## 2. Contexto crítico: cómo era mneme ANTES (post SPEC-032/033)

Esto importa para la discusión porque la spec original asumía un modelo distinto al real.

- **Hook bloqueador = bash**, no Go. `internal/install/assets/hooks/enforce_delegation.sh` (embebido en el binario vía `//go:embed` en SPEC-032, con tokenizer Go `mneme hook tokenize` de SPEC-033). Se instala con `mneme install claude-code`.
- **Mecanismo de distinción orquestador↔subagente:** el hook lee el JSON de stdin del `PreToolUse`. Si el campo **`agent_id`** está presente → es un subagente → `exit 0` (permitido). Si está ausente → es el orquestador → se evalúa la whitelist.
- **`mneme hook pre-tool-use` (Go)** es un hook SEPARADO, basado en reglas de la DB (warn/info, SPEC-003). NO es el bloqueador. No confundir.
- **CLI de memoria:** el comando es `mneme save` (top-level), con flags `--title --content --type --scope --topic-key --file --importance --stdin --applies-to --severity`. **No existe** `mneme mem save` ni `--what/--why/--learned/--tag`. El modelo de memoria mneme usa un único campo `content` (markdown) + `topic_key`.

---

## 3. Las 3 divergencias spec ↔ realidad y cómo se reconciliaron

La spec original venía calcada de un proyecto de referencia (engram) y describía un modelo idealizado. Tres divergencias se resolvieron con **decisiones explícitas del founder** antes de implementar:

| # | Lo que pedía la spec | Realidad mneme | Decisión tomada |
|---|----------------------|----------------|-----------------|
| **A** | Hook lee `agent_type` y bloquea por rol (`IMPLEMENTER_AGENT_TYPES = {backend, frontend, bug-hunter}`); architect/qa-tester subagentes también bloqueados en el hook. | Claude Code **solo inyecta `agent_id`** (un UUID de sesión de subagente), **no el rol**. El hook puede saber "¿es subagente?" pero no "qué rol". | **Allowlist + hook orquestador.** El allowlist `tools:` (capa 1) es la barrera real de capacidad. El hook bash (capa 2) sigue bloqueando SOLO al orquestador, como defense-in-depth, y NO intenta discriminar rol. |
| **B** | Tests unitarios Go del core del hook en `internal/hooks/pretool/`. | El bloqueador es bash; su "core Go" es solo el tokenizer (`internal/shell/`). | Tests adaptados: verificación marker-based del asset embebido en `internal/install/install_test.go` (mismo estilo que los tests de SPEC-032) + smoke tests bash. |
| **C** | Memoria con campos `what/why/learned/tags` vía `mneme mem save --tag`; query con `mneme mem search --tag enforcement`. | mneme usa `content` único + `topic_key`, sin tags libres. | **Modelo nativo mneme.** El logging usa `mneme save --type discovery` con What/Why/Learned embebidos en el markdown de `--content`. Queryable por título via `mneme search "Blocked edit"`. Sin flags ni tags nuevos. |

**Implicación clave para la discusión:** como el hook bash solo bloquea al orquestador (los subagentes salen `exit 0` antes de llegar a `block()`), **el logging solo captura intentos del `principal`**. El bloqueo de architect/qa-tester ocurre en la capa de allowlist de Claude Code, que **no pasa por el hook ni por mneme** → esos intentos no son logeables hoy. Esto es honesto dado el modelo, pero es un punto abierto (ver §8).

---

## 4. El modelo de enforcement resultante (2 capas)

```
┌─────────────────────────────────────────────────────────────────┐
│ CAPA 1 — Capacidad (primaria)                                     │
│ Cada subagente declara un allowlist `tools:` en su frontmatter.   │
│ architect/qa-tester NO tienen Edit/Write/MultiEdit/NotebookEdit/  │
│ Bash → físicamente incapaces de editar. Lo hace Claude Code, no   │
│ pasa por mneme.                                                   │
├─────────────────────────────────────────────────────────────────┤
│ CAPA 2 — Hook (defensa en profundidad)                            │
│ enforce_delegation.sh detecta al orquestador por AUSENCIA de      │
│ agent_id y bloquea (exit 2) sus ediciones fuera de la whitelist.  │
│ Cada bloqueo se registra como memoria `discovery` en mneme.       │
└─────────────────────────────────────────────────────────────────┘
```

- **¿Quién contiene a quién?**
  - Orquestador (principal): capa 2 (hook). También capa 1 implícita: su única whitelist es `.claude/**`, `~/.claude/**`, `CLAUDE.md`, `**/docs/*.md`, `.claudeignore`.
  - architect / qa-tester (subagentes read-only): capa 1 (allowlist). El hook los deja pasar.
  - backend / frontend / bug-hunter (subagentes implementers): sin contención — son los escritores legítimos.

---

## 5. Cambios concretos

### 5.1 Allowlists `tools:` en los 5 agentes (`internal/install/assets/agents/*.md`)

Solo se tocó el frontmatter YAML; los cuerpos de prompt quedaron intactos (anti-scope).

**Read-only (`architect.md`, `qa-tester.md`)** — se eliminó `permissionMode: bypassPermissions`, se agregó:
```yaml
tools: Read, Grep, Glob, NotebookRead, BashOutput, mcp__mneme__*
```
Sin `Edit`, `Write`, `MultiEdit`, `NotebookEdit`, `Bash`. `mcp__mneme__*` conserva el acceso a las herramientas de memoria.

**Implementers (`backend.md`, `frontend.md`, `bug-hunter.md`)** — se conservó `bypassPermissions` (autonomous runs) con comentario justificándolo, y se agregó el toolset completo:
```yaml
permissionMode: bypassPermissions
# bypassPermissions: implementer en autonomous runs (sin prompts de permiso); la barrera de rol es el allowlist tools: de abajo
tools: Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit, Write, MultiEdit, Bash, mcp__mneme__*
```

> **Convención verificada:** el servidor MCP se registra como `mneme`, así que `mcp__mneme__*` es el patrón wildcard correcto de Claude Code.

### 5.2 Logging de bloqueos en `enforce_delegation.sh`

Se agregó un global `TARGET_PATH` (default `"unknown"`) que se asigna **antes** de cada uno de los 16 call sites de `block()`, y dentro de `block()` se registra la memoria justo antes de `exit 2`:

```bash
block() {
  local reason="$1"
  # ... printfs de bloqueo a stderr (sin cambios) ...
  if command -v mneme >/dev/null 2>&1; then
    local _basename="${TARGET_PATH##*/}"
    local _tool="${TOOL_NAME:-unknown}"
    mneme save \
      --type discovery \
      --title "Blocked edit: principal -> ${_tool} -> ${_basename}" \
      --content "$(printf '## Blocked edit attempt\n\n**What:** ... \n\n**Why:** Capability rule fired: principal is not in implementer allowlist [backend, frontend, bug-hunter] ...\n\n**Learned:** Pattern to watch: orchestrator attempted to edit directly instead of delegating ...\n\n**Reason (hook):** %s' ...)" \
      >/dev/null 2>&1 || printf '[enforce_delegation] WARNING: mneme save failed (block still enforced)\n' >&2
  fi
  exit 2
}
```

Garantías de diseño:
- **El `exit 2` es incondicional.** Guard `command -v mneme` + `>/dev/null 2>&1 || true`-pattern: un fallo de `mneme save` (o su ausencia en PATH) escribe un WARNING a stderr pero **nunca** altera el código de salida del bloqueo.
- **Sin dedup** (§5.2.5 de la spec): cada intento es un evento único; reintentos generan memorias duplicadas a propósito (señal cruda). Por eso NO se usa `topic_key`.
- **Foreground** (no background): el `mneme save` corre síncrono. Coste estimado ~80ms (fork del binario Go) sobre un target de ~50ms del hook — aceptable y no bloqueante; ver §8.
- Se documentó `IMPLEMENTER_AGENT_TYPES={backend,frontend,bug-hunter}` como **comentario** (punto de extensión futuro si Claude Code algún día expone `agent_type`).

### 5.3 Documentación

- **`docs/enforcement-model.md`** (NUEVO, 127 líneas): patrón "Critical Rules + Automated Checks (tabla) + How to Fix", adaptado al modelo real (`agent_id`, `mneme search` sin `--tag`). Incluye: cuándo leerlo, reglas críticas, tabla de checks automáticos, cómo añadir un subagente, cómo debuggear un bloqueo, cómo decide el hook.
- **`CLAUDE.md`** (raíz): sección "Enforcement Model" bajo Architecture, describiendo las 2 capas reales y `mneme search "Blocked edit"` como vía de consulta.
- **`CHANGELOG.md`** (NUEVO): formato Keep a Changelog, entrada `[v1.4.0] — 2026-05-29`.

### 5.4 Tests (`internal/install/install_test.go`, +121 líneas)

3 tests nuevos, marker-based sobre el asset embebido:
- `TestDelegationHookContent_LogsBlockedAttempts` — el asset contiene `mneme save`, `--type discovery`, `Blocked edit: principal`, `command -v mneme`, el WARNING de fallo, y conserva `exit 2`.
- `TestAgentAssets_ReadOnlyAllowlists` — architect/qa-tester tienen el allowlist read-only y NO tienen `bypassPermissions` ni tools de edición.
- `TestAgentAssets_ImplementerAllowlists` — backend/frontend/bug-hunter tienen el toolset completo + `mcp__mneme__*`.

---

## 6. Verificación

| Check | Resultado |
|---|---|
| `make build` | OK |
| `make test` | 24/24 paquetes OK |
| `make test-race` | 24/24 paquetes OK |
| `golangci-lint run` | **0 issues** (verificado por el orquestador; está en `GOPATH/bin`) |
| `bash -n enforce_delegation.sh` | sintaxis válida |
| QA (qa-tester) | **APPROVED** — 25/25 criterios + 4 smoke tests |

**Smoke tests del hook (aislado, vía stdin):**

| Caso | Input | exit esperado | observado |
|---|---|---|---|
| Orquestador Edit fuera de whitelist | `{"tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}` | 2 | ✅ 2 |
| Subagente Edit (con `agent_id`) | `{"agent_id":"x","tool_name":"Edit",...}` | 0 | ✅ 0 |
| Orquestador Edit `CLAUDE.md` (whitelist) | `{"tool_name":"Edit","tool_input":{"file_path":"CLAUDE.md"}}` | 0 | ✅ 0 |
| Orquestador Write `docs/*.md` (whitelist) | `{"tool_name":"Write",...}` | 0 | ✅ 0 |

---

## 7. Commits y release

Branch `feat/spec-034-permission-enforcement`, 5 commits atómicos (Conventional Commits):

| Hash | Commit |
|---|---|
| `208c111` | feat(agents): read-only tool allowlists for architect and qa-tester |
| `f1b230f` | feat(agents): explicit full toolset for implementer agents |
| `71a4757` | feat(install): log blocked orchestrator edits as discovery memory |
| `722b9e9` | test(install): verify hook logs blocked attempts + agents have allowlists |
| `33ea614` | docs(enforcement): add enforcement-model.md, CLAUDE.md section, CHANGELOG |

- **PR #4** mergeado a `main` (fast-forward, merge commit `ed34a81`), branch eliminado.
- **Tag `v1.4.0`** pusheado → workflow `release.yml` generó la release limpia con 4 assets: `mneme-1.4.0-{darwin-arm64,linux-amd64}.tar.gz` + `.sha256`.
- SDD: `SPEC-034` recorrió draft → speccing → specced → planned → implementing → qa → **done**.

---

## 8. Puntos abiertos para la discusión de diseño

Estos son los temas que vale la pena debatir con el otro agente:

1. **El logging solo captura al `principal`.** Por el modelo `agent_id`-only, los intentos de edición de architect/qa-tester los corta Claude Code en la capa de allowlist, **fuera del hook**, así que no generan memoria. ¿Es suficiente la telemetría del orquestador, o se necesita capturar también los intentos de subagentes read-only? Opciones: (a) aceptar el gap; (b) un `PreToolUse` adicional que corra dentro de subagentes y logee sin bloquear; (c) esperar a que Claude Code exponga `agent_type` y mover la discriminación al hook.

2. **`agent_type` vs `agent_id`.** Toda la divergencia A depende de que Claude Code no expone el rol. Si en una versión futura lo expusiera, el comentario `IMPLEMENTER_AGENT_TYPES` ya marca dónde reintroducir la discriminación por rol en el hook (capa 2 redundante con capa 1). ¿Vale la pena la redundancia?

3. **Performance del hook.** `mneme save` en foreground añade ~80ms por bloqueo. Solo ocurre en el camino de bloqueo (no en el feliz), pero si se vuelve molesto se podría: hacer el save en background (`&`, con riesgo de perder el log si el proceso muere), o portar el hook a Go (contradice la decisión SPEC-032 de "el mismo script bash en el repo").

4. **Sin dedup (intencional).** Reintentos en loop generan memorias duplicadas. Es señal cruda a propósito para v1.4. Una versión futura podría agregar dedup por ventana de tiempo. ¿Cuándo conviene?

5. **`bypassPermissions` en implementers.** Se conservó para autonomous runs. ¿Debería eliminarse para forzar prompts de permiso interactivos en sesiones no autónomas? Trade-off autonomía vs. control.

6. **Doble hook `PreToolUse`.** Conviven `mneme hook pre-tool-use` (Go, rules-based, warn/info) y `enforce_delegation.sh` (bash, block del orquestador). Son ortogonales pero ambos corren en cada Edit/Write. ¿Consolidar en algún momento?

7. **Falso positivo observado en el parser del hook live.** Durante esta misma sesión, el hook live (versión SPEC-033) bloqueó un `golangci-lint run 2>&1` interpretando `2>&1` como redirect al archivo `'1'`. El parser nuevo de SPEC-033 debía cubrir esto; aparece un caso residual con `2>&1` cuando el target es un fd y no un archivo. No es de SPEC-034, pero es un bug del tokenizer/parser que conviene anotar.

---

## 9. Archivos tocados (referencia rápida)

```
internal/install/assets/agents/architect.md      (frontmatter)
internal/install/assets/agents/qa-tester.md       (frontmatter)
internal/install/assets/agents/backend.md         (frontmatter)
internal/install/assets/agents/frontend.md        (frontmatter)
internal/install/assets/agents/bug-hunter.md      (frontmatter)
internal/install/assets/hooks/enforce_delegation.sh  (+33 líneas: TARGET_PATH + logging en block())
internal/install/install_test.go                  (+121 líneas: 3 tests)
docs/enforcement-model.md                          (NUEVO, 127 líneas)
CLAUDE.md                                          (+25 líneas: sección Enforcement Model)
CHANGELOG.md                                       (NUEVO, 64 líneas, v1.4.0)
```

> **Nota operativa:** los cambios viven en el **asset embebido**. Para que el hook live (`~/.claude/hooks/enforce_delegation.sh`) y los frontmatter de `~/.claude/agents/*.md` reflejen esto, hay que reconstruir el binario e `mneme install claude-code` (o `mneme upgrade` a v1.4.0). Hasta entonces, la instalación local sigue corriendo la versión SPEC-033 sin logging.
