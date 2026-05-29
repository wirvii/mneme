# SPEC-035 — Diseño técnico del architect (D1-D8)

> Diseño autoritativo para el backend. Acompaña a `docs/new/mneme-v1.5-spec-graduated-lanes.md` y la memoria `spec/SPEC-035-reconciliation`. Decisiones founder: git-exec adapter, MCP+CLI paridad, lane+scope en backlog_items Y specs (propagado).

## D1. Migración 011 — `internal/db/migrations/011_add_lane.sql`

```sql
-- 011_add_lane.sql: Add lane and scope columns for graduated lanes (SPEC-035).
ALTER TABLE backlog_items ADD COLUMN lane  TEXT NOT NULL DEFAULT 'standard' CHECK (lane IN ('trivial','standard'));
ALTER TABLE backlog_items ADD COLUMN scope TEXT NOT NULL DEFAULT '';
ALTER TABLE specs ADD COLUMN lane  TEXT NOT NULL DEFAULT 'standard' CHECK (lane IN ('trivial','standard'));
ALTER TABLE specs ADD COLUMN scope TEXT NOT NULL DEFAULT '';
INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (11, datetime('now'));
```
`ALTER ... ADD COLUMN ... DEFAULT` backfilla filas existentes atómicamente. CHECK valida lane. Sin pre-flight. Test: `internal/db/migration_011_test.go` (patrón de migration_005_test.go): EmptyDB (schema_version=11 + INSERT lane='trivial'), ExistingData (backfill standard+''), CheckConstraint (INSERT lane='invalid' → error).

## D2. Modelo — `internal/model/sdd.go`

### D2.1 Tipo Lane
```go
type Lane string
const ( LaneTrivial Lane = "trivial"; LaneStandard Lane = "standard" )
var validLanes = map[Lane]struct{}{LaneTrivial: {}, LaneStandard: {}}
func (l Lane) Valid() bool { _, ok := validLanes[l]; return ok }
```

### D2.2 Nuevos SpecStatus (añadir al bloque de constantes + a validSpecStatuses)
```go
SpecStatusRationale SpecStatus = "rationale" // trivial: equivalente a speccing (justificación 1-3 frases via spec_quick)
SpecStatusAudit     SpecStatus = "audit"     // trivial: equivalente a qa (chequeo determinista)
```

### D2.3 Campos en BacklogItem (tras Position) y Spec (tras BacklogID)
```go
Lane  Lane   `json:"lane"`
Scope string `json:"scope,omitempty"`
```

### D2.4 Request structs
Añadir `Lane Lane json:"lane"` + `Scope string json:"scope,omitempty"` a `BacklogAddRequest` y `SpecNewRequest`. Nuevos:
```go
type SpecQuickRequest struct { ID string `json:"id"`; Rationale string `json:"rationale"`; By string `json:"by"` }
type LaneAuditRequest struct { ID string `json:"id"`; BaseRef string `json:"base_ref,omitempty"` }
type LaneReclassifyRequest struct { ID string `json:"id"`; Lane Lane `json:"lane"`; Scope string `json:"scope,omitempty"`; By string `json:"by"` }
type LaneOverrideRequest struct { ID string `json:"id"`; Reason string `json:"reason"`; By string `json:"by"` }
```

### D2.5 State machine lane-aware (DOS mapas separados)
Reemplazar el `validTransitions` único por:
```go
var validTransitionsStandard = map[SpecStatus]map[SpecStatus]struct{}{
    SpecStatusDraft:        {SpecStatusSpeccing: {}},
    SpecStatusSpeccing:     {SpecStatusSpecced: {}, SpecStatusNeedsGrill: {}},
    SpecStatusNeedsGrill:   {SpecStatusSpeccing: {}},
    SpecStatusSpecced:      {SpecStatusPlanning: {}},
    SpecStatusPlanning:     {SpecStatusPlanned: {}},
    SpecStatusPlanned:      {SpecStatusImplementing: {}},
    SpecStatusImplementing: {SpecStatusQA: {}, SpecStatusNeedsGrill: {}},
    SpecStatusQA:           {SpecStatusDone: {}, SpecStatusImplementing: {}, SpecStatusNeedsGrill: {}},
}
var validTransitionsTrivial = map[SpecStatus]map[SpecStatus]struct{}{
    SpecStatusDraft:        {SpecStatusRationale: {}},
    SpecStatusRationale:    {SpecStatusImplementing: {}},
    SpecStatusImplementing: {SpecStatusAudit: {}, SpecStatusNeedsGrill: {}},
    SpecStatusAudit:        {SpecStatusDone: {}, SpecStatusImplementing: {}},
    SpecStatusNeedsGrill:   {SpecStatusRationale: {}},
}
func (s SpecStatus) CanTransitionTo(target SpecStatus, lane Lane) bool {
    transitions := validTransitionsStandard
    if lane == LaneTrivial { transitions = validTransitionsTrivial }
    targets, ok := transitions[s]; if !ok { return false }
    _, valid := targets[target]; return valid
}
```
**BREAKING:** `CanTransitionTo` cambia de `(target)` a `(target, lane)`. Hacer `grep -rn CanTransitionTo` y actualizar TODOS los call sites (servicio ~L264,~L379 + tests) en el MISMO commit.

### D2.6 Sentinel errors (internal/model/errors.go)
`ErrLaneRequired`, `ErrInvalidLane`, `ErrScopeRequired`, `ErrLaneImmutable`, `ErrLaneMismatch`, `ErrAuditFailed`, `ErrReasonRequired`.

## D3. Store — `internal/store/sdd.go`
- `CreateBacklogItem`/`UpdateBacklogItem`/`GetBacklogItem`/`ListBacklogItems`/`scanBacklogItem`/`collectBacklogItems`: añadir `lane, scope` a INSERT/UPDATE/SELECT y Scan (`(*string)(&item.Lane), &item.Scope`).
- `CreateSpec`/`UpdateSpecFields`/`GetSpec`/`ListSpecs`/`RecentlyCompletedSpecs`/`scanSpec`/`collectSpecs`: idem con `lane, scope` tras `backlog_id`.
- Nuevo: `func (s *SDDStore) UpdateSpecLaneScope(ctx, specID string, lane model.Lane, scope string) error` (solo lane+scope, no status).

## D4. Auditor — `internal/lane/` (NUEVO paquete leaf, solo stdlib + go/ast)
NO importa internal/model. El servicio traduce model↔tipos del auditor.

### D4.1 `internal/lane/git.go` — git-exec adapter
```go
type FileStat struct { Added, Removed int; Path string }
type DiffStats struct { Files []FileStat }
func (d DiffStats) TotalFiles() int
func (d DiffStats) TotalLines() int // sum added+removed
type GitDiffer struct { RepoDir string }
func (g *GitDiffer) DefaultBaseRef() (string, error) // git merge-base HEAD <default-branch>
func (g *GitDiffer) NumStat(baseRef string) (*DiffStats, error)   // git diff --numstat <baseRef>
func (g *GitDiffer) DiffContent(baseRef string, paths []string) (string, error) // git diff <baseRef> -- <paths>
func (g *GitDiffer) ShowFile(ref, path string) (string, error)    // git show <ref>:<path> (before)
```
Default branch: `git symbolic-ref refs/remotes/origin/HEAD` (strip `refs/remotes/origin/`), fallback `main`→`master`. Numstat: `<added>\t<removed>\t<path>`; binarios `-\t-\t<path>` (1 archivo, 0 líneas). Todos los exec con `RepoDir` como cwd.

### D4.2 `internal/lane/audit.go`
```go
type AuditInput struct { Scope, BaseRef, RepoDir string }
type AuditResult struct {
    FileCount int; LinesChanged int
    OutOfScopeFiles, ForbiddenPaths, PublicSymbolChanges, Breaches []string
    Passed bool // len(Breaches)==0
}
func Audit(input AuditInput) (*AuditResult, error)
```
Checks (Go puro determinista):
1. files>3 → "file count %d exceeds trivial limit of 3"
2. lines>20 → "line count %d exceeds trivial limit of 20"
3. forbidden globs: `**/*.sql`, `**/migrations/**`, `**/schema.*`, `cmd/**`, `internal/install/assets/**` → "forbidden path modified: %s"
4. scope (si no vacío): archivos fuera del glob → "out of scope: %s"
5. símbolos públicos Go: por cada .go, parsear before (`git show base:path`) y after (FS) con go/parser+go/ast, comparar nombres exportados (mayúscula inicial) → "public symbol changed: %s in %s"
6. TS/JS (.ts/.tsx/.js/.jsx): heurística regex sobre el diff (`+export `/`-export `) → "public export changed in %s"
Extraer la lógica de comparación a funciones que aceptan `DiffStats` para testear sin repo real. Glob `**` con expansión manual (filepath.Match no soporta `**`).

### D4.3 Tests `internal/lane/audit_test.go` + `git_test.go`
Table-driven: happy (2 files,15 líneas,in-scope,sin públicos→Passed), cada breach individual (5 files / 47 líneas / *.sql / cmd/** / install/assets / out-of-scope / func exportada Go / export TS), combinado (5 files+forbidden→2 breaches). git_test: NumStat parsing + DefaultBaseRef con repo temporal.

## D5. Servicio — `internal/service/sdd.go`
- **BacklogAdd/SpecNew:** validar `req.Lane==""`→ErrLaneRequired; `!Valid()`→ErrInvalidLane; `trivial && Scope==""`→ErrScopeRequired. Setear Lane/Scope.
- **BacklogPromote:** propagar `Lane: item.Lane, Scope: item.Scope` al SpecNewRequest.
- **SpecAdvance:** usar `nextForwardStatus(spec.Status, spec.Lane)` (mapa trivial vs standard) y `CanTransitionTo(next, spec.Lane)`. `onAdvanceSideEffects`: createSpecDirectory en `SpecStatusSpeccing` Y `SpecStatusRationale`; saveCompletionMemory en `SpecStatusDone`.
- **SpecPushback:** pasar `spec.Lane` a CanTransitionTo.
- **SpecResolve:** target = speccing (standard) o rationale (trivial).
- **nextForwardStatus(current, lane)**: dos mapas (ver D2.5 equivalente). trivial: draft→rationale→implementing→audit→done. standard: igual que hoy.
- **Nuevos métodos** (firmas):
  - `SpecQuick(ctx, SpecQuickRequest) (*model.Spec, error)`: valida Lane==trivial (ErrLaneMismatch), Status==draft. Avanza draft→rationale (reason=Rationale) y luego rationale→implementing. Devuelve en implementing.
  - `LaneAudit(ctx, LaneAuditRequest) (*lane.AuditResult, error)`: valida Lane==trivial, Status==audit. Resuelve base ref. `lane.Audit(...)`. Si Passed: advance audit→done + saveCompletionMemory. Si no: memoria discovery con breaches + return ErrAuditFailed (devolviendo result). repoDir: nuevo campo `repoDir` en SDDService (cwd para CLI / project root para MCP).
  - `LaneReclassify(ctx, LaneReclassifyRequest)`: solo trivial→standard (standard→trivial NO permitido). Mueve status a speccing. Registra history. Tras implementing igual solo trivial→standard.
  - `LaneOverride(ctx, LaneOverrideRequest)`: valida trivial, Status==audit, Reason!="" (ErrReasonRequired). advance audit→done. Memoria discovery "Lane override applied: <id>" + saveCompletionMemory.
  - `LaneStatus(ctx, id) (*model.LaneStatusResponse, error)`: lane, scope, state, último audit. Guardar resultado de audit en el `reason` del history entry; LaneStatus lee el history más reciente con to_status audit/done.
- Memoria discovery vía memorySvc (como saveCompletionMemory): `Title: "Lane audit failed: <id>"`, `Type: TypeDiscovery`, `Scope: ScopeProject`, `Content: <breaches markdown>`, `Project: spec.Project`.
- Nuevos tipos model: `LaneStatusResponse{Spec, Lane, Scope, LatestAudit *AuditSummary}`, `AuditSummary{Passed bool, Breaches []string, At time.Time}`.

## D6. MCP — `internal/mcp/tools.go` + `handlers.go`
- `backlog_add` y `spec_new`: añadir properties `lane` (enum trivial/standard) y `scope`; añadir `lane` a required.
- `spec_list`: añadir `rationale`, `audit` al enum de status.
- 5 tools nuevos: `spec_quick` (req id,rationale,by), `lane_audit` (req id; opt base_ref), `lane_reclassify` (req id,lane[enum standard],by; opt scope), `lane_override` (req id,reason,by), `lane_status` (req id).
- Handlers: patrón existente (check h.sdd==nil, unmarshal, call service, mapServiceError, resultFromAny). Dispatch en handleToolCall.
- `mapServiceError`: añadir los 7 sentinels nuevos a la rama invalid params (CodeInvalidParams), salvo not-found que ya está.

## D7. CLI — `internal/cli/`
- `backlog.go` newBacklogAddCmd: flags `--lane`,`--scope`; poblar req; output incluye `lane:%s`.
- `spec.go` newSpecNewCmd: flags `--lane`,`--scope`. Nuevo `newSpecQuickCmd()` (`spec quick <id> <rationale> --by`, ExactArgs(2)).
- NUEVO `internal/cli/lane.go`: `newLaneCmd()` con subcomandos `audit <id> [--base]`, `reclassify <id> <lane> [--scope] --by`, `override <id> --reason --by`, `status <id>`. audit imprime breaches a stderr y exit !=0 si ErrAuditFailed.
- `root.go`: registrar `newLaneCmd()`.
- Usar el helper `initSDDService()` existente (root.go ~L188).

## D8. Docs
- `docs/lanes.md` (NUEVO): When to read · Critical Rules (8) · Automated Checks (tabla con thresholds y breach msg) · How to Fix (reclassify/override/prevención). Patrón de docs/enforcement-model.md.
- `CLAUDE.md` raíz: sección "## Lanes (v1.5.0)" tras Conventions.
- `CHANGELOG.md`: entrada `[v1.5.0]`.
- **NO existe** asset embebido de prompt de orquestador en internal/install/assets/ (verificado: solo agents/*.md, templates, commands, enforce_delegation.sh). §5.9 del asset NO aplica; solo CLAUDE.md raíz.

## Orden de commits atómicos (cada uno compila y testea)
1. feat(db): migración 011 + test
2. feat(model): Lane, estados trivial, state machine lane-aware + errors (actualizar TODOS los call sites de CanTransitionTo)
3. feat(store): lane+scope en backlog_items y specs + tests
4. feat(lane): auditor determinista + git-exec adapter + tests
5. feat(service): métodos lane-aware + tests
6. feat(mcp): lane tools + params
7. feat(cli): comandos lane + flags
8. docs: lanes.md, CLAUDE.md, CHANGELOG v1.5.0

## Tests
migration_011_test, model sdd_test (Lane.Valid, CanTransitionTo ambos lanes), store sdd_test (CRUD lane+scope, UpdateSpecLaneScope), lane audit_test+git_test, service sdd_test (rechazo lane faltante, scope trivial, SpecQuick rechaza standard, SpecAdvance flujo por lane, reclassify trivial→standard→speccing, override requiere reason, promote propaga), mcp handlers_test (backlog_add/spec_new rechazan lane faltante; dispatch de los 5 tools). make test + test-race + golangci-lint limpios.
