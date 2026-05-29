# SPEC-036 — SDD Engine Integrity · Informe de implementación

> **Para:** agente de discusión de diseño.
> **Estado:** ✅ Implementado, mergeado (PR #6 → `main`, `ab89c84`) y liberado como **v1.6.0** (4 assets, workflow limpio).
> **Origen:** `docs/new/mneme-v1.6-spec-sdd-integrity.md` (reconciliada) + `docs/new/SPEC-036-design.md` (D1-D9).
> **Predecesores:** SPEC-034 (v1.4.0), SPEC-035 (v1.5.0). Derivada de los puntos abiertos del informe de SPEC-035 (§8.2/§8.3/§8.5).
> **Fecha:** 2026-05-29.

---

## 1. Qué resuelve

Tres gaps de **integridad** del motor SDD (no features): lugares donde el estado registrado podía ser incorrecto o no confiable, lo que socava al state machine como fuente de verdad.

| Gap | Severidad | Problema | Solución |
|---|---|---|---|
| 1.1 | CRÍTICO | El state machine no podía representar un rechazo de QA. `spec_advance` es forward-only; los edges backward existían pero ningún comando los caminaba. Se perdía la telemetría de rebotes. | `spec_reject` que camina `qa→implementing` (standard) y `audit→implementing` (trivial) con reason. `rejection_count` derivado de history. |
| 1.2 | CORRECTNESS | `lane_audit` corría `git diff` contra un base ref del cwd, sin atar al spec. Con 2 specs en una rama, el diff agregado se atribuía a ambos. | Capturar `git HEAD SHA` al entrar a `implementing` y usarlo como base del audit. |
| 1.3 | FRAGILIDAD | `lane_status` parseaba breaches del texto del `reason` de history (un cambio de formato lo rompía en silencio). | Tabla estructurada `lane_audits`; `lane_status` lee de ahí. |
| bonus | — | El abuso del carril trivial era invisible. | `lane_stats` con counts/rates. |

## 2. Reconciliación (la spec, derivada de mi propio informe de SPEC-035, fue muy exacta)

Verificado contra el código real **antes** de implementar (memoria `spec/SPEC-036-reconciliation`). Hallazgos clave que evitaron duplicación:

- **Los edges backward YA existían** (`internal/model/sdd.go`: `validTransitionsStandard[qa]→implementing`, `validTransitionsTrivial[audit]→implementing`). El reject **solo expone un comando**; no toca los mapas.
- **`SpecPushback` NO sirve para reject:** transiciona a `needs_grill` y crea un registro de preguntas — semántica "re-grillar por ambigüedad", distinta de "QA encontró defectos → vuelve a implementing". → se añadió `SpecReject` nuevo, **sin duplicar** pushback. (Esto es exactamente lo que §4 de la spec pedía verificar.)
- **`GitDiffer` no tenía `HeadSHA()`** → se añadió (`git rev-parse HEAD`).
- El hack de SPEC-035 (`InsertSpecHistoryEntry(audit→audit, "audit failed: ...")`) se **eliminó**, reemplazado por la tabla.
- override/reclassify ya quedan en history reasons → `lane_stats` deriva counts sin contadores nuevos.

**No hubo forks de founder esta vez** — cada punto tenía resolución dictada por la spec/código.

## 3. Diseño implementado

### 3.1 Reject (gap 1.1)
`SpecReject(ctx, {ID, Reason, By})` — valida reason no vacío (`ErrReasonRequired`), usa `CanTransitionTo(implementing, lane)` (no bypassa el state machine), `UpdateSpecStatus(status→implementing, "rejected: <reason>")`. Como los edges `qa→implementing` y `audit→implementing` ya existen, **no necesita especializar por lane** — un solo camino los cubre. `rejection_count` = count de transiciones de history con `to=implementing` desde `qa|audit`.

### 3.2 Base-SHA binding (gap 1.2)
- Migración 012: `specs.base_sha TEXT DEFAULT ''`.
- `GitDiffer.HeadSHA()` nuevo.
- `captureBaseSHA()` se llama en **dos sitios** (con comentarios cruzados): `onAdvanceSideEffects` (caso `implementing`, para `SpecAdvance` standard) y explícitamente en `SpecQuick` tras `rationale→implementing` (el atajo trivial que no pasa por `SpecAdvance`). **No-bloqueante**: si git falla, `base_sha=""`, warning, la transición no falla (patrón SPEC-034 de side-effects).
- `LaneAudit` precedencia del base ref: `req.BaseRef` → `spec.BaseSHA` → `DefaultBaseRef()`. **El core `internal/lane/audit.go` no cambió** — solo la *selección* del ref se movió al servicio (determinismo preservado).

### 3.3 Tabla lane_audits (gap 1.3)
- Migración 012: `lane_audits(id, spec_id, passed, file_count, lines_changed, breaches, base_sha, created_at)` + índice `(spec_id, created_at)`.
- `InsertLaneAudit` (una fila por corrida, pass y fail) + `LatestLaneAudit`.
- `LaneStatus` lee `LatestLaneAudit` → `AuditSummary{Passed, Breaches, At}`. Sin parseo de strings. Sin fila → "no audit recorded" (no crash).

### 3.4 lane_stats (bonus)
`LaneStats(project)` deriva: trivial count, audit-fail count+rate (de `LatestLaneAudit`), override count (history reason `"lane override:"`), reclassify count (history reason `"reclassified from trivial to standard"`). Sin contadores nuevos.

### 3.5 Surface
MCP 39 → **41 tools** (`spec_reject`, `lane_stats`), CLI en paridad (`mneme spec reject`, `mneme lane stats`). `spec_reject` usa `mapServiceError` normal; el patrón `IsError=true`-con-payload de `lane_audit` (SPEC-035) se preservó sin regresión.

## 4. Proceso SDD

| Fase | Resultado |
|---|---|
| Reconciliación | Verificada contra código; sin forks de founder |
| Architect | D1-D9; 1 PUSHBACK de ingeniería (timing de captura base-SHA en SpecQuick) resuelto con la opción simple (2 sitios + comentarios cruzados) |
| Backend | 9 commits, build/test/lint verdes |
| QA | **APPROVED** (0 críticos), 2 menores |
| Fix menor post-QA | 1 commit (`115cc52`): mensaje `ErrReasonRequired` generalizado |
| Verificación propia | build OK · test 25/25 · race 25/25 · golangci-lint 0 issues |
| Release | PR #6 → main (`ab89c84`), tag v1.6.0, workflow con 4 assets |

### El punto de diseño que QA dirimió
La spec (§5.2.3 y AC §7) decía que la precedencia del base ref debía ser **`spec.base_sha` primero**, luego `--base`, luego legacy. El diseño D5 y la implementación la invirtieron: **`--base` explícito primero**, luego `spec.base_sha`. QA evaluó la divergencia y dictaminó que la implementación es la **correcta** (un override explícito del usuario siempre debe ganar) y que lo desactualizado es el texto de la spec, no el código. Coincidí. El doc de producto (`docs/lanes.md`) y el CHANGELOG documentan la precedencia real.

## 5. Commits (10) y release
Branch `feat/spec-036-sdd-integrity`:
`446b158` db/migración 012 · `00c5b38` model · `280460c` lane/HeadSHA · `360a9fe` store · `6f007ba` service (SpecReject+captureBaseSHA+LaneAudit rewrite+LaneStats) · `8d39612` mcp (39→41) · `8f75008` cli · `b949184` docs · `05cf910` fix(lint+test regression) · `115cc52` fix(model) mensaje genérico.
PR #6 → `ab89c84`. Tag `v1.6.0` → `mneme-1.6.0-{darwin-arm64,linux-amd64}.tar.gz` + `.sha256`.

## 6. Puntos abiertos para la discusión de diseño

1. **base_sha se captura una vez, al entrar a implementing.** Si un spec rebota (reject → implementing de nuevo) el base_sha NO se re-captura — sigue apuntando al commit de la primera entrada. Esto es deseable (el audit mide *todo* lo que el spec cambió desde su inicio), pero si entremedio se mergeó otro spec a la rama, ese trabajo ajeno entra en el diff. El binding es por-spec-por-rama, no por-spec-absoluto. ¿Suficiente para el caso de uso (1 spec activo por rama) o se necesita aislamiento por worktree?

2. **rejection_count vs telemetría de rebotes.** Se deriva de history on-read (transiciones to=implementing desde qa|audit). Es correcto pero no distingue "rechazo por defectos" (reject) de otros caminos hipotéticos a implementing. Hoy solo reject y el avance normal planned→implementing llegan ahí, y planned→implementing no cuenta (from=planned). OK por ahora.

3. **`lane_stats` deriva override/reclassify de prefijos de texto del `reason` de history** (`"lane override:"`, `"reclassified from trivial to standard"`). Es exactamente el tipo de acoplamiento frágil que esta misma spec eliminó para los audits (gap 1.3). Lo dejamos así porque eran datos secundarios de una vista de stats, pero es inconsistente: si se vuelve crítico, merecería su propia tabla o un tipo de evento estructurado. **Deuda reconocida.**

4. **Precedencia base-ref spec↔código.** Resuelto a favor del código (override gana), pero ilustra que las specs derivadas de informes pueden arrastrar afirmaciones que el diseño luego mejora. El QA como árbitro funcionó.

5. **base_sha = "(default)" como sentinela en lane_audits.** Cuando no hay base_sha ni --base, se guarda el string literal `"(default)"` en la columna `base_sha` del registro de audit (porque el ref real lo resuelve el auditor internamente y no se devuelve). Es legible pero no es un SHA; un consumidor que asuma que la columna siempre es un SHA se confundiría. Menor.

## 7. Archivos (referencia)
```
internal/db/migrations/012_add_spec_base_sha_and_audits.sql (NEW) + migration_012_test.go (NEW)
internal/model/sdd.go (BaseSHA, SpecRejectRequest, LaneAuditRecord, LaneStatsResponse, RejectionCount)
internal/model/errors.go (mensaje ErrReasonRequired generalizado)
internal/lane/git.go (HeadSHA) + git_test.go
internal/store/sdd.go (base_sha CRUD, UpdateSpecBaseSHA, InsertLaneAudit, LatestLaneAudit) + sdd_test.go
internal/service/sdd.go (SpecReject, captureBaseSHA, LaneAudit rewrite, LaneStatus tabla, LaneStats) + sdd_test.go
internal/mcp/{tools,handlers,server,handlers_test}.go (spec_reject, lane_stats, 41 tools)
internal/cli/{spec,lane}.go (spec reject, lane stats)
docs/lanes.md, CLAUDE.md (41 tools), CHANGELOG.md ([v1.6.0])
```

> **Operativo:** `mneme upgrade` a v1.6.0 + **reiniciar Claude Code** (2 tools MCP nuevos: spec_reject, lane_stats). La migración 012 se auto-aplica al abrir la DB con el binario nuevo (schema v11 → v12).
