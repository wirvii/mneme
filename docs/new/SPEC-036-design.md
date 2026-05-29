# SPEC-036 — Diseño técnico del architect (D1-D9)

> Diseño autoritativo para el backend. Acompaña `docs/new/mneme-v1.6-spec-sdd-integrity.md` y la memoria `spec/SPEC-036-reconciliation`. Sin forks de founder. PUSHBACK resuelto: base-SHA se captura en 2 sitios (onAdvanceSideEffects para SpecAdvance + explícito en SpecQuick), con comentarios cruzados.

## D1. Migración 012 — `internal/db/migrations/012_add_spec_base_sha_and_audits.sql` (NEW)
```sql
-- 012_add_spec_base_sha_and_audits.sql: base-SHA binding + structured lane audit records (SPEC-036).
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
Test `internal/db/migration_012_test.go` (patrón migration_011_test.go, helpers openRawMemory/applyUpToVersion de migration_005_test.go): EmptyDB (schema_version=12, lane_audits insert+select roundtrip, specs.base_sha), ExistingData (specs viejos base_sha=''), LaneAuditsShape (2 filas mismo spec_id, ORDER BY created_at DESC LIMIT 1 da la última). Helper loadMigration012.

## D2. Modelo — `internal/model/sdd.go` (EDIT)
- `Spec` (tras Scope): `BaseSHA string \`json:"base_sha,omitempty"\``.
- `SpecRejectRequest{ID, Reason, By string}` (tras LaneOverrideRequest).
- `LaneAuditRecord{ID int64; SpecID string; Passed bool; FileCount,LinesChanged int; Breaches,BaseSHA string; CreatedAt time.Time}` (tras AuditSummary).
- `LaneStatusResponse`: añadir `RejectionCount int \`json:"rejection_count"\``.
- `LaneStatsResponse{TrivialCount,AuditFailCount int; AuditFailRate float64; OverrideCount,ReclassifyCount int}` (nuevo).
- Sin sentinels nuevos: `ErrReasonRequired` ya existe (model/errors.go:146) y ya está en mapServiceError (handlers.go:508).

## D3. git.go — `internal/lane/git.go` (EDIT) + git_test.go
```go
// HeadSHA returns the full SHA of the current HEAD commit.
func (g *GitDiffer) HeadSHA() (string, error) {
    cmd := exec.Command("git", "rev-parse", "HEAD")
    cmd.Dir = g.RepoDir
    out, err := cmd.Output()
    if err != nil { return "", fmt.Errorf("lane: git rev-parse HEAD: %w", err) }
    return strings.TrimSpace(string(out)), nil
}
```
Test: t.TempDir()+git init+commit → HeadSHA no vacío 40-hex; dir no-git → error.

## D4. Store — `internal/store/sdd.go` (EDIT) + sdd_test.go
- **base_sha en spec CRUD** (4 sitios): CreateSpec (INSERT col+val spec.BaseSHA), GetSpec/ListSpecs/RecentlyCompletedSpecs (SELECT `COALESCE(base_sha,'')`), scanSpec + collectSpecs (Scan `&spec.BaseSHA` tras `&spec.Scope`). Orden de columnas: id,title,status,project,backlog_id,lane,scope,base_sha,assigned_agents,files_changed,created_at,updated_at.
- `UpdateSpecBaseSHA(ctx, specID, sha string) error` (espejo UpdateSpecLaneScope; UPDATE base_sha+updated_at; n==0 → ErrSpecNotFound).
- `InsertLaneAudit(ctx, rec *model.LaneAuditRecord) error` (INSERT 7 cols; passed bool→0/1; created_at now RFC3339Nano).
- `LatestLaneAudit(ctx, specID string) (*model.LaneAuditRecord, error)` (SELECT ... ORDER BY created_at DESC LIMIT 1; sql.ErrNoRows → nil,nil; parseTime para created_at).
- Stats: SIN método store de agregación; se hace en servicio sobre ListSpecs+GetSpecHistory+LatestLaneAudit.
- Tests: UpdateSpecBaseSHA (ok + not-found), InsertLaneAudit+LatestLaneAudit (2 inserts → última; sin filas → nil), GetSpec/ListSpecs devuelven BaseSHA.

## D5. Servicio — `internal/service/sdd.go` (EDIT) + sdd_test.go
- **SpecReject** (tras SpecResolve): reason=="" → ErrReasonRequired; GetSpec; `!CanTransitionTo(implementing, lane)` → ErrInvalidTransition; UpdateSpecStatus(spec.Status→implementing, By, "rejected: "+reason); reload. (camina edges existentes qa→impl / audit→impl; NO especializa por lane).
- **captureBaseSHA(ctx, spec)** helper (tras createSpecDirectory): repoDir = svc.repoDir||Getwd; `lane.GitDiffer{RepoDir}.HeadSHA()`; err → log warning + return (NO bloquea); UpdateSpecBaseSHA (err → log). 
- **onAdvanceSideEffects**: añadir `case model.SpecStatusImplementing: svc.captureBaseSHA(ctx, spec)`.
- **SpecQuick**: tras la 2ª transición (rationale→implementing) y reload, añadir `svc.captureBaseSHA(ctx, updated)` (comentario cruzando con onAdvanceSideEffects).
- **LaneAudit**: baseRef precedence = req.BaseRef → spec.BaseSHA → (vacío=DefaultBaseRef interno del auditor). [Nota: el auditor con BaseRef vacío resuelve DefaultBaseRef; respeta eso]. Tras lane.Audit: construir LaneAuditRecord (Passed, FileCount, LinesChanged, Breaches=join("\n") solo si !Passed, BaseSHA=el ref usado o "(default)") e `InsertLaneAudit` (pass Y fail; err → log no-bloqueante). **ELIMINAR** el `InsertSpecHistoryEntry(audit,audit,"audit failed: ...")`. Mantener saveAuditFailureMemory en fallo y advance→done+saveCompletionMemory en pass. return result, ErrAuditFailed en fallo.
- **LaneStatus**: leer `LatestLaneAudit` → AuditSummary{Passed, Breaches=split("\n"), At=CreatedAt} (nil → sin LatestAudit). RejectionCount = count de GetSpecHistory con ToStatus==implementing && FromStatus in {qa,audit}.
- **LaneStats(ctx, project string) (*model.LaneStatsResponse, error)** (tras LaneStatus): project=""→svc.project; ListSpecs; por cada spec trivial: TrivialCount++, LatestLaneAudit !Passed → AuditFailCount++, GetSpecHistory reason prefix "lane override:" → OverrideCount++, "reclassified from trivial to standard" → ReclassifyCount++; AuditFailRate=fail/trivial si >0.
- **Tests:** SpecReject (standard qa→impl, trivial audit→impl, reason vacío→ErrReasonRequired, status inválido draft→ErrInvalidTransition), base-SHA capturado en implementing (SpecQuick trivial + SpecAdvance standard, con git dir), base-SHA git ausente no bloquea (repoDir no-git → BaseSHA="" + transición OK), LaneAudit precedencia (set BaseSHA via store, audita contra él), LaneAudit escribe lane_audits (pass y fail), LaneStatus lee tabla, LaneStatus RejectionCount (2 rejects → 2), LaneStats counts. **ACTUALIZAR** `TestLaneStatus_AfterFailedAudit` (hoy usa InsertSpecHistoryEntry hack → usar InsertLaneAudit; breaches ahora newline). `TestLaneStatus_NoAuditRun` sigue pasando (LatestLaneAudit nil).

## D6. MCP — `internal/mcp/tools.go` + `handlers.go` (EDIT) + handlers_test.go
- tools.go: `spec_reject` (req id,reason,by) tras spec_list; `lane_stats` (opt project) tras lane_status.
- handlers.go: dispatch `spec_reject`→handleSpecReject, `lane_stats`→handleLaneStats. handleSpecReject (unmarshal SpecRejectRequest, SpecReject, mapServiceError, resultFromAny). handleLaneStats (unmarshal {project}, LaneStats, resultFromAny). mapServiceError SIN cambios (ErrReasonRequired ya está). spec_reject usa patrón normal (NO IsError-con-payload).
- Conteo tools 39→41. Actualizar el test de conteo (server_test.go) si existe.
- Tests handlers: spec_reject happy (devuelve spec), lane_stats (devuelve response), status inválido → error.

## D7. CLI — `internal/cli/spec.go` + `lane.go` (EDIT)
- spec.go: `newSpecRejectCmd()` (`reject <id> --reason --by`, ambos required, ExactArgs(1)) registrado en newSpecCmd.
- lane.go: `newLaneStatsCmd()` (`stats [--json]`) registrado en newLaneCmd. Usa initSDDService(), imprime counts o JSON.

## D8. Docs
- docs/lanes.md: secciones Reject, Base-SHA Binding, Structured Audit Records (lane_audits reemplaza el hack), Lane Stats.
- CLAUDE.md: conteo 39→41, añadir spec_reject + lane_stats al inventario (4 lane_* → spec_* pasa a 8: spec_new/status/advance/pushback/resolve/list/quick/reject; lane_* pasa a 5: audit/reclassify/override/status/stats). Verifica el desglose real.
- CHANGELOG.md: `[v1.6.0]` (Added spec_reject, lane_stats, base-sha binding, lane_audits, rejection_count, migración 012; Changed LaneStatus lee tabla + precedencia baseRef; Removed hack audit→audit; Fixed determinismo per-spec).

## D9. Orden de commits atómicos (cada uno compila y testea)
1. feat(db): migración 012 + test
2. feat(model): BaseSHA, SpecRejectRequest, LaneAuditRecord, LaneStatsResponse, RejectionCount
3. feat(lane): HeadSHA en GitDiffer + test
4. feat(store): base_sha en spec CRUD + InsertLaneAudit/LatestLaneAudit/UpdateSpecBaseSHA + tests
5. feat(service): SpecReject + captura base-SHA + LaneAudit rewrite (tabla, quita hack) + LaneStatus tabla+rejection_count + LaneStats + tests (ACTUALIZAR TestLaneStatus_AfterFailedAudit)
6. feat(mcp): spec_reject + lane_stats (39→41) + tests
7. feat(cli): spec reject + lane stats
8. docs: lanes.md, CLAUDE.md, CHANGELOG v1.6.0

## Anti-scope
NO tocar enforce_delegation.sh/allowlists/edges estándar; NO LLM; NO trivialidad semántica; NO modificar internal/lane/audit.go core (solo la SELECCIÓN del baseRef se mueve, en servicio); NO duplicar pushback/resolve; reusar ErrReasonRequired.
