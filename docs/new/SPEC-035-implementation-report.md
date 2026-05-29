# SPEC-035 — Graduated Lanes for SDD · Informe de implementación

> **Para:** agente de discusión de diseño.
> **Estado:** ✅ Implementado, mergeado (PR #5 → `main`, commit `b7ddcf1`) y liberado como **v1.5.0** (4 assets, workflow limpio).
> **Origen:** `docs/new/mneme-v1.5-spec-graduated-lanes.md` (reconciliada) + `docs/new/SPEC-035-design.md` (diseño D1-D8).
> **Predecesor:** SPEC-034 (v1.4.0).
> **Fecha:** 2026-05-29.

---

## 1. Qué resuelve

SPEC-034 cerró el gap de **capacidad** (el orquestador no puede editar código). SPEC-035 cierra el gap de **proceso**: el flujo SDD era uniforme sin importar el tamaño del cambio, así que una corrección de una línea recorría el mismo camino que una feature de 3 módulos — *ese* era el incentivo a defeccionar ("esto es trivial, no amerita SDD completo").

Solución: **dos carriles** con clasificación determinista declarada en creación, y un **auditor post-facto determinista** (sin LLM) que detecta el abuso del carril trivial inspeccionando el diff real.

## 2. Los dos carriles

| | trivial | standard |
|---|---|---|
| Flujo | `draft → rationale → implementing → audit → done` | `draft → speccing → (needs_grill) → specced → planning → planned → implementing → qa → done` (**idéntico al actual**) |
| Artefacto inicial | `spec_quick` (justificación 1-3 frases) | spec completa + plan |
| Cierre | `lane_audit` determinista | `qa` |
| Requisitos | ≤3 archivos, ≤20 líneas, sin SQL/migrations/cmd/install-assets, sin cambios de API pública | cualquier cosa |

La clasificación es **explícita y obligatoria** en `backlog_add`/`spec_new`; nunca inferida por un LLM ni por heurística de keywords (eso es justo lo que rompía el comportamiento previo).

## 3. Contexto: realidad de mneme vs. lo que asumía la spec

La spec fue escrita contra una arquitectura idealizada que divergía fuerte. Reconciliación (memoria `spec/SPEC-035-reconciliation`):

| Spec asumía | Realidad mneme | Resolución |
|---|---|---|
| `internal/storage/`, `internal/sdd/`, `cmd/mneme/*.go` | modelo `internal/model/sdd.go`, servicio `internal/service/sdd.go`, store `internal/store/`, CLI `internal/cli/`, migraciones `internal/db/migrations/` | rutas reales |
| Hook lee `agent_type` (heredado de SPEC-034) | — | n/a aquí |
| SDD CLI-only | SDD se conduce sobre todo por **MCP tools** | **(Q2 founder)** MCP tools + CLI en paridad |
| Auditor inspecciona "el diff que aterrizó" | mneme **no tenía NINGUNA integración de git** | **(Q1 founder)** nuevo adapter git-exec mínimo |
| `lane` en `backlog_items` declarado en backlog-add | backlog y spec son tablas separadas; la máquina de estados es del Spec | **(Q3 founder)** lane+scope en **ambas** tablas, propagado en promote |
| Estados `draft→speccing→specced→planned→...` | el real incluye `needs_grill` y `planning` | flujo estándar preservado intacto |
| `mneme plan`/`mneme qa` | no existen (solo `mneme spec advance` genérico) | gating va en `spec advance` lane-aware + comandos `lane` |
| Versión v1.5.0 | mneme iba en v1.4.0 | v1.5.0 ✓ |

## 4. Diseño implementado

### 4.1 Esquema (migración 011)
```sql
ALTER TABLE backlog_items ADD COLUMN lane  TEXT NOT NULL DEFAULT 'standard' CHECK (lane IN ('trivial','standard'));
ALTER TABLE backlog_items ADD COLUMN scope TEXT NOT NULL DEFAULT '';
ALTER TABLE specs          ADD COLUMN lane  TEXT NOT NULL DEFAULT 'standard' CHECK (lane IN ('trivial','standard'));
ALTER TABLE specs          ADD COLUMN scope TEXT NOT NULL DEFAULT '';
```
El `DEFAULT 'standard'` backfilla las filas existentes — toda spec previa queda como standard (sin reclasificación forzada). `lane` es inmutable tras `implementing` (enforced en Go, no en SQL).

### 4.2 Máquina de estados lane-aware
Dos mapas de transición separados (decisión del architect: más legibles que un mapa parametrizado). `validTransitionsStandard` es **bit a bit idéntico** al original (regresión cero verificada por QA). El trivial:
```go
var validTransitionsTrivial = map[SpecStatus]map[SpecStatus]struct{}{
    SpecStatusDraft:        {SpecStatusRationale: {}},
    SpecStatusRationale:    {SpecStatusImplementing: {}},
    SpecStatusImplementing: {SpecStatusAudit: {}, SpecStatusNeedsGrill: {}},
    SpecStatusAudit:        {SpecStatusDone: {}, SpecStatusImplementing: {}},
    SpecStatusNeedsGrill:   {SpecStatusRationale: {}},
}
```
`CanTransitionTo` pasó de `(target)` a `(target, lane)` — cambio breaking interno actualizado atómicamente en todos los call sites.

### 4.3 Auditor determinista (`internal/lane/`, paquete leaf)
- **Go puro, sin LLM, sin subagente, sin imports de `internal/model`** (el servicio traduce model↔`lane.AuditInput`).
- `git.go` — adapter git-exec: `git diff --numstat` (conteos), `git diff <base> -- <paths>` y `git show <base>:<path>` (para el "antes" del AST). Base ref por defecto = `git merge-base HEAD <default-branch>` (detectado via `git symbolic-ref refs/remotes/origin/HEAD`, fallback `main`→`master`); override con `--base`.
- `audit.go` — checks deterministas: files>3, líneas>20, paths prohibidos (`**/*.sql`, `**/migrations/**`, `**/schema.*`, `cmd/**`, `internal/install/assets/**`), fuera de scope, símbolos públicos Go (compara nombres exportados con `go/parser`+`go/ast` antes/después) y heurística `export` para TS/JS.
- `AuditResult{FileCount, LinesChanged, OutOfScopeFiles, ForbiddenPaths, PublicSymbolChanges, Breaches, Passed}`.

### 4.4 Outcomes del audit
- **Pasa** → `audit → done`, sin memoria.
- **Falla** → queda en `audit`, crea memoria `discovery` "Lane audit failed: <id>" con los breaches, y persiste un history entry `audit→audit reason="audit failed: ..."` (para que `lane_status` reporte los breaches reales). El usuario decide: `lane_reclassify <id> standard` (→ `speccing`, flujo completo) o `lane_override <id> --reason "..."` (→ `done`, override persistido como otra memoria discovery). **Nunca reclasifica solo.**

### 4.5 Superficie (MCP + CLI en paridad)
- **MCP** (5 tools nuevos): `spec_quick`, `lane_audit`, `lane_reclassify`, `lane_override`, `lane_status`; params `lane`/`scope` añadidos a `backlog_add` y `spec_new` (lane requerido). Total de tools: 34 → **39**.
- **CLI**: `mneme spec quick`, `mneme lane audit/reclassify/override/status`, flags `--lane`/`--scope` en `backlog add` y `spec new`.

## 5. Proceso SDD (incluye un rebote real de QA)

| Fase | Resultado |
|---|---|
| Reconciliación | 3 decisiones del founder (git-exec / MCP+CLI / lane en ambas tablas) |
| Architect | D1-D8, sin pushback |
| Backend (ronda 1) | 9 commits, build/test/lint verdes |
| **QA (ronda 1)** | **REJECTED** — 1 crítico + 1 importante + 3 menores |
| Backend (ronda 2) | 4 commits de fix |
| **Re-QA** | **APPROVED** — 5/5 resueltos, sin regresión |
| Release | PR #5 → main, tag v1.5.0, workflow con 4 assets |

### El crítico que QA atrapó (vale para la discusión de diseño)
`handleLaneAudit` (MCP) descartaba el `AuditResult` cuando el audit fallaba: hacía `if err != nil { return mapServiceError(err) }`, perdiendo los breaches. Como MCP es la superficie principal y el agente necesita los breaches para decidir reclassify vs override, era bloqueante. Fix (patrón idiomático MCP de tool-error):
```go
if errors.Is(err, model.ErrAuditFailed) && result != nil {
    b, _ := json.Marshal(result)
    return &ToolCallResult{Content: []ContentBlock{{Type:"text", Text: string(b)}}, IsError: true}, nil
}
return nil, h.mapServiceError("lane_audit", err)
```
Es decir: un fallo de audit **no** es un error de protocolo JSON-RPC; es un resultado de tool exitoso con `IsError: true` que transporta el payload. La CLI ya lo hacía bien (imprime breaches a stderr, exit≠0); el MCP no.

El importante: `LaneStatus` confundía la transición `implementing→audit` con un resultado de audit. Fix: solo reconoce `audit→done reason="lane audit passed"` (pass) y `audit→audit reason="audit failed: ..."` (fail), persistiendo este último vía un nuevo `InsertSpecHistoryEntry` (sin optimistic-lock, permite anotación same-status).

## 6. Verificación
- `make build` OK · `make test` 25/25 · `make test-race` 25/25 · `golangci-lint run` **0 issues** (verificado por el orquestador, no solo por subagentes).
- Re-QA APPROVED con evidencia archivo:línea por cada uno de los 5 bugs.
- Determinismo del auditor confirmado (mismo diff+item → mismo resultado; el core `lane/audit.go` no fue tocado por los fixes).

## 7. Commits (13) y release
Branch `feat/spec-035-graduated-lanes`:
`68f03a2` db/migración 011 · `054ca62` model lane-aware · `8485b7b` store · `e98b95b` auditor+git · `1924882` service · `ebfc21e` mcp · `bc527d4` cli · `6c8b99c` docs · `dd8ae04` lint · `8f4371e` fix(mcp) crítico · `6e8c14c` fix(service) importante · `a48ab0a` fix menores 3+4 · `5f66bf7` test menor 5.
PR #5 merge → `b7ddcf1`. Tag `v1.5.0` → workflow generó `mneme-1.5.0-{darwin-arm64,linux-amd64}.tar.gz` + `.sha256`.

## 8. Puntos abiertos para la discusión de diseño

1. **Determinismo del auditor vs. "el cambio pequeño pero semánticamente grande".** §5.3.1 de la spec admite que un diff puede ser ≤3 archivos/≤20 líneas y aun así NO ser trivial (p.ej. voltear un booleano default que cambia comportamiento en producción). El auditor determinista **no puede** detectar eso — por diseño no juzga semántica. Hoy lo cubre parcialmente la lista de paths prohibidos + cambios de símbolo público, pero el riesgo residual existe. ¿Aceptable? ¿O se necesita un check adicional determinista?

2. **El auditor depende del estado del repo, no del spec.** `lane_audit` corre `git diff` contra un base ref en el cwd. No hay binding fuerte entre "el diff que aterrizó" y "el spec". Si hay varios specs en vuelo en la misma rama, el diff agregado se le atribuye a todos. ¿Conviene registrar un base SHA por spec al entrar a `implementing`?

3. **`lane_status` lee breaches del history reason.** Funciona, pero acopla la presentación al texto del reason (`"audit failed: <breaches joined by ;>"`). Si el formato cambia, `lane_status` se rompe silenciosamente. ¿Mejor una tabla `lane_audits` dedicada?

4. **Gating del orquestador es por convención, no enforced.** §5.9 instruye al orquestador a preguntar "¿trivial o standard?" y nunca asignar sin confirmar — pero eso vive en `CLAUDE.md` (prosa), no hay enforcement de capacidad como en SPEC-034. El orquestador podría declarar `trivial` para saltarse el flujo; el auditor lo atrapa *post-facto*, no lo previene. ¿Suficiente con la detección post-facto + memoria?

5. **`spec_advance` es forward-only — no modela el rebote de QA.** Cuando QA rechazó (ronda 1), avancé la spec y el motor la mandó a `done` en vez de volver a `implementing` (la máquina permite `qa→implementing` pero no hay tool MCP de "reject"/backward). Es un hueco del motor SDD: no hay forma de representar un rebote de QA en el estado. Candidato a un futuro `spec_reject`/transición backward. (No bloqueó nada: no mergeé hasta el re-APPROVED, pero el bookkeeping del motor quedó impreciso durante la ronda de fix.)

6. **Heurística TS/JS frágil.** Para no-Go, los cambios de símbolo público se detectan con regex sobre `+export`/`-export`. mneme es repo Go puro, así que es casi irrelevante hoy, pero la heurística daría falsos positivos/negativos en un repo TS real. Documentado como heurística, no como análisis.

## 9. Archivos (referencia)
```
internal/db/migrations/011_add_lane.sql            (NEW)
internal/db/migration_011_test.go                  (NEW)
internal/model/sdd.go, errors.go, sdd_test.go      (lane type, estados, 2 mapas, 7 errors)
internal/store/sdd.go, sdd_test.go                 (lane+scope CRUD, InsertSpecHistoryEntry, UpdateSpecLaneScope)
internal/lane/{audit,git}.go + *_test.go           (NEW — auditor + git-exec adapter)
internal/service/sdd.go, sdd_test.go, init.go      (validación, SpecQuick/Lane*, propagación)
internal/mcp/{tools,handlers}.go + *_test.go        (5 tools, params, mapServiceError)
internal/cli/{backlog,spec,root}.go + lane.go(NEW)  (flags, spec quick, lane *)
docs/lanes.md (NEW), CLAUDE.md, CHANGELOG.md
```

> **Operativo:** para activar lo nuevo localmente: `mneme upgrade` (a v1.5.0) — y como esto añade tools MCP y un comando CLI, **reiniciar Claude Code** para que el servidor MCP reexponga los 39 tools. La migración 011 se aplica sola al abrir la DB con el binario nuevo.
