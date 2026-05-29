# SPEC-036 — SDD Engine Integrity

**Status:** Ready for implementation · **Target release:** v1.6.0
**Predecessors:** SPEC-034 (v1.4.0), SPEC-035 (v1.5.0)

> **NOTA DE RECONCILIACIÓN (orquestador, 2026-05-29):** verificado contra el código real; sin forks de founder. Detalle completo en la memoria `spec/SPEC-036-reconciliation`. Puntos confirmados:
> - Edges backward YA existen (`internal/model/sdd.go`: `validTransitionsStandard[qa]→implementing`, `validTransitionsTrivial[audit]→implementing`). Reject solo expone un comando; NO toca mapas.
> - `SpecPushback` (service/sdd.go:417) va a `needs_grill` (preguntas) — semántica distinta del reject (defectos → implementing). → **añadir `SpecReject` nuevo**, no extender pushback.
> - `GitDiffer` (internal/lane/git.go) NO tiene `HeadSHA()` → añadir (`git rev-parse HEAD`). `svc.repoDir`/`WithRepoDir` ya existen.
> - LaneAudit hoy usa el hack `InsertSpecHistoryEntry(audit→audit,"audit failed:...")` + LaneStatus lo parsea → reemplazar por tabla `lane_audits`; eliminar ese write path.
> - lane stats: override/reclassify ya quedan en history reasons; sin contadores nuevos.
> - Surface: MCP+CLI paridad. 39 → 41 tools. Versión real v1.6.0.

## 1. Contexto y objetivo

Tres gaps de integridad del motor SDD (del informe de SPEC-035 §8.2/§8.3/§8.5). No son features: son lugares donde el estado registrado puede ser **incorrecto o no confiable**, lo que socava al state machine como fuente de verdad.

1. **(1.1 CRÍTICO)** El state machine no puede representar un rechazo de QA. `spec_advance` es forward-only; los edges backward existen pero ningún comando los camina. Un rebote de review es el evento más común y no se modela → se pierde la telemetría de cuántas veces rebota cada spec.
2. **(1.2 CORRECTNESS)** El auditor no está atado al spec: `lane_audit` corre `git diff` contra un base ref del cwd. Con 2 specs en la misma rama, el diff agregado se atribuye a ambos → los conteos mienten.
3. **(1.3 FRAGILIDAD)** `lane_status` parsea breaches del texto del `reason` de history. Un cambio de formato lo rompe en silencio. No hay registro estructurado de audits.

Objetivo: hacer el estado del motor confiable — reject de primera clase, audit atado a un base SHA capturado, tabla `lane_audits` estructurada, y una vista `lane stats` que haga visible el abuso del carril trivial.

## 2. Diseño

### 5.1 Reject (gap 1.1)
- Standard: `qa → implementing`; Trivial: `audit → implementing`; con reason registrado.
- Requiere reason no vacío (`ErrReasonRequired`, ya existe).
- Valida status exacto (`qa` standard / `audit` trivial) según lane; si no, error claro y sin cambio de estado.
- Usa `CanTransitionTo(target, lane)` (no bypassa el state machine). Registra history de la transición (cambio de estado real, NO el hack same-status).
- **Añadir `SpecReject(ctx, SpecRejectRequest)` nuevo** (espejo de LaneOverrideRequest: `{ID, Reason, By}`). NO duplicar pushback.
- `rejection_count` derivado de history (transiciones to=implementing desde qa|audit); SIN columna contador. Lo expone `lane_status`.

### 5.2 Base-SHA binding (gap 1.2)
- **Migración 012** (`012_add_spec_base_sha_and_audits.sql`):
```sql
ALTER TABLE specs ADD COLUMN base_sha TEXT NOT NULL DEFAULT '';
CREATE TABLE lane_audits (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    spec_id       TEXT    NOT NULL,
    passed        INTEGER NOT NULL,
    file_count    INTEGER NOT NULL,
    lines_changed INTEGER NOT NULL,
    breaches      TEXT    NOT NULL DEFAULT '',
    base_sha      TEXT    NOT NULL DEFAULT '',
    created_at    TEXT    NOT NULL
);
CREATE INDEX idx_lane_audits_spec ON lane_audits(spec_id, created_at);
INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (12, datetime('now'));
```
Test `migration_012_test.go` (patrón 011): EmptyDB (schema_version=12, lane_audits existe, insert+select roundtrip), ExistingData (specs viejos base_sha=''), shape.
- `GitDiffer.HeadSHA() (string,error)` = `git rev-parse HEAD` (RepoDir cwd).
- `UpdateSpecBaseSHA(ctx, specID, sha)` en store (espejo UpdateSpecLaneScope).
- Capturar en `onAdvanceSideEffects` en transición → `implementing` (todos los lanes). **No-bloqueante**: si git falla, base_sha="", warning, transición NO falla.
- LaneAudit precedencia baseRef: `spec.BaseSHA` → `req.BaseRef` → `DefaultBaseRef()`. **`internal/lane/audit.go` core NO cambia** (determinismo intacto); solo la selección del ref se mueve en el servicio.

### 5.3 Tabla lane_audits (gap 1.3)
- `InsertLaneAudit(ctx, LaneAuditRecord)` + `LatestLaneAudit(ctx, specID)` en store.
- LaneAudit inserta una fila por corrida (pass y fail): spec_id, passed, file_count, lines_changed, breaches (newline-joined; vacío si pasa), base_sha (el ref usado), created_at.
- **ELIMINAR** el write path `InsertSpecHistoryEntry(audit→audit, "audit failed: ...")`. Mantener las transiciones de history legítimas.
- LaneStatus lee `LatestLaneAudit`; `AuditSummary{Passed, Breaches, At}` desde la fila. Sin parseo de strings.
- Backward-compat: sin fila → "no audit recorded yet", no crashea.

### 5.4 lane stats (bonus, §8.4)
- `lane_stats` (MCP) + `mneme lane stats` (CLI), scoped al proyecto: count de specs trivial; count+rate de trivial con último audit failed; count de overrides (history reason "lane override:"); count de reclassify trivial→standard (history reason "reclassified..."). Solo counts/rates, sin series temporales.

### 5.5 Surface (MCP + CLI paridad)
- MCP: `spec_reject` (req id, reason, by), `lane_stats` (sin req; project opcional). 39 → 41 tools.
- CLI: `mneme spec reject <id> --reason --by`, `mneme lane stats`.
- mapServiceError: registrar ErrReasonRequired en invalid-params (ya debería estar).
- **NO regresar el patrón MCP de SPEC-035:** lane_audit fallido sigue IsError=true con payload. spec_reject usa mapServiceError normal (no tiene payload-on-failure).

### 5.6 Memorias
Sin cambios: `mneme save --type discovery --title --content`, sin tags. spec_reject NO escribe memoria (history es el registro). Se mantienen override y audit-fail memories.

## 3. File map (rutas reales confirmadas)
| File | Action |
|---|---|
| `internal/db/migrations/012_add_spec_base_sha_and_audits.sql` | NEW |
| `internal/db/migration_012_test.go` | NEW |
| `internal/model/sdd.go` | EDIT — `Spec.BaseSHA`; `SpecRejectRequest`; `LaneAuditRecord`; `LaneStatusResponse`+`rejection_count`; `LaneStatsResponse` |
| `internal/model/errors.go` | EDIT — reusar `ErrReasonRequired`; sentinel de reject si hace falta |
| `internal/lane/git.go` (+git_test.go) | EDIT — `HeadSHA()` |
| `internal/store/sdd.go` (+sdd_test.go) | EDIT — `UpdateSpecBaseSHA`, `InsertLaneAudit`, `LatestLaneAudit`, base_sha en spec CRUD; quitar hack |
| `internal/service/sdd.go` (+sdd_test.go) | EDIT — `SpecReject`, captura base-SHA en onAdvanceSideEffects, precedencia baseRef en LaneAudit, write lane_audits, LaneStatus lee tabla, rejection_count, `LaneStats` |
| `internal/mcp/tools.go`,`handlers.go` (+handlers_test.go) | EDIT — spec_reject, lane_stats + dispatch + mapServiceError |
| `internal/cli/spec.go` | EDIT — `spec reject` |
| `internal/cli/lane.go` | EDIT — `lane stats` |
| `docs/lanes.md`, `CLAUDE.md`, `CHANGELOG.md` | EDIT — reject, base-sha, audit records, stats; `[v1.6.0]` |

## 4. Criterios de aceptación
Reject: standard qa→implementing con reason+history; trivial audit→implementing; reason vacío → ErrReasonRequired; otro status → error sin cambio; lane_status muestra rejection_count derivado de history; no se duplicó pushback (documentado en reconciliación).
Base SHA: migración 012 + test; entrar a implementing captura git rev-parse HEAD (todos los lanes); git ausente → base_sha='', warning, transición OK; lane_audit usa spec.base_sha → --base → merge-base; audit.go core sin cambios.
Audit records: cada corrida inserta fila lane_audits (pass y fail); lane_status lee de la tabla; hack audit→audit removido; sin fila → "no audit recorded" sin crash.
Stats: lane stats reporta trivial count, audit-fail count+rate, override count, reclassify count, scoped.
Calidad: MCP+CLI paridad; sin regresión del patrón IsError de lane_audit; reconciliación memory existe; make test + test-race + golangci-lint limpios (verificado por orquestador); docs actualizados.

## 5. Anti-scope
Reconciliación PRIMERO. NO LLM en audit. NO check de trivialidad semántica. NO tocar enforce_delegation.sh / tools: allowlists / edges del flujo estándar. NO inventar flags de memoria. NO duplicar pushback/resolve. NO modificar el core de internal/lane/audit.go (solo la selección del base ref se mueve, en el servicio). Solo: reject, base-SHA binding, tabla lane_audits, lane stats + tests y docs.
