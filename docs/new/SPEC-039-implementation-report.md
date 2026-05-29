# SPEC-039 — Memory Conflict Surfacing · Informe de implementación

> **Para:** agente de discusión de diseño.
> **Estado:** ✅ Implementado, mergeado (PR #9 → `main`, `a07055f`) y liberado como **v1.9.0** (4 assets).
> **Origen:** `docs/new/mneme-v1.9-spec-conflict-surfacing.md` + `docs/new/SPEC-039-design.md`. **Predecesores:** SPEC-034..038. **Fecha:** 2026-05-29.

---

## 1. Qué resuelve

El fallo silencioso de la memoria plana: un agente lee "auth usa HMAC JWT" mucho después de que el equipo migró a RS256, y actúa sobre la decisión obsoleta — con confianza, porque mneme se la entregó como hecho. Patrón de 2 pasos (engram): **detección barata determinista** (FTS5 sobre memorias con overlap) + **juicio semántico bajo demanda** delegado a la **Claude CLI local via subprocess** (costo $0, sin API key). Las relaciones (`supersedes`/`conflicts_with`/`unrelated`) luego informan al retrieval.

## 2. La excepción consciente al invariante de determinismo (§1.1)

SPEC-035 (lane auditor) y SPEC-037 (skill linter) son deterministas sin LLM porque sus preguntas son **estructurales**. Juzgar si dos decisiones se contradicen es **semántico** — no hay test determinista para "¿RS256 supersede a HMAC aquí?". Por eso esta spec usa LLM **solo en el paso de juicio**. La distinción quedó **visible en el código**: `internal/conflicts/detect.go` (FTS5, 100% determinista, solo stdlib) vs `internal/conflicts/judge.go` (el único con `os/exec`/subprocess). No es una relajación del invariante anterior; es la herramienta correcta para una clase distinta de problema.

## 3. Reconciliación (redujo el alcance)

- **`memories.superseded_by` YA existía** + `SetSupersededBy` (lo usa la dedup de consolidación) + `mem_search` ya **excluye superseded por defecto**. → el `supersedes` **reusa** ese mecanismo; cero duplicación de la exclusión de retrieval.
- **`relations` (grafo) es entity-a-entity**, no sirve para edges memoria-memoria. → `conflicts_with`/`unrelated` van a una tabla nueva `memory_relations` (migración 013).
- **Claude CLI headless confirmado:** `claude -p "<prompt>" --output-format json` → `{"type":"result","result":"<text>"}` → parsear el JSON interno. `exec.LookPath("claude")` falla → `ErrCLIUnavailable`, sin fallback.
- Subprocess: patrón reusado de `internal/skill/validate.go` (exec+timeout) y el test con binario falso en PATH.

## 4. Diseño implementado

- **Detección** (`detect.go` + `store.FTS5Candidates`): términos salientes de title+content → query FTS5; scoped project+global; excluye self, deleted, superseded y pares ya juzgados (negative cache). Determinista.
- **Save hint NO-bloqueante:** `logConflictHint` corre como goroutine fire-and-forget (con `context.Background()` + `defer/recover`) — solo loguea cuántos candidatos hay; nunca escribe relaciones, nunca juzga, nunca falla el save. (QA confirmó race-safe: `resp` es solo-lectura de datos inmutables; test-race verde.)
- **Juicio** (`judge.go` + `service.ConflictScan`): `claude -p ... --output-format json`, parsea veredicto + rationale. **Dry-run por defecto**; `--apply` persiste (supersedes→`SetSupersededBy`; conflicts_with/unrelated→`memory_relations`). Idempotente (negative cache + UNIQUE). CLI ausente → reporta y salta, **CERO API metered**.
- **Override manual:** `conflicts link/unlink/list`. Manual (`judged_by=manual`) gana sobre cli (INSERT OR REPLACE).
- **Retrieval:** superseded ya excluido por `superseded_by`; `conflicts_with` → flag aditivo en `SearchResult.ConflictsWith` vía post-ranking pass `annotateConflicts` (no reordena/filtra, defer/recover; scoring **no** reescrito); `unrelated` sin efecto.
- **Superficie:** 5 tools MCP + grupo CLI `mneme conflicts` en paridad. 51 → **56**. `conflicts_scan` con CLI ausente → `IsError:true` + payload (patrón SPEC-035).

## 5. Proceso SDD

| Fase | Resultado |
|---|---|
| Reconciliación | superseded_by reusable, relations entity-based, claude CLI confirmado; sin forks de founder |
| Architect | D1-D8; **sin pushback** (re-entrancy de conflicts_scan evaluada como segura → se queda como tool MCP) |
| Backend | 7 commits, verdes |
| QA | **APPROVED** — 0 críticos, 0 importantes, 4 menores |
| Verificación propia | build OK · test 27/27 · race 27/27 · golangci-lint 0 issues |
| Release | PR #9 → main (`a07055f`), tag v1.9.0, 4 assets |

### La pregunta de re-entrancy (resuelta por el architect)
`conflicts_scan` via MCP spawnaría la Claude CLI desde un tool que corre bajo Claude Code. El architect dictaminó que es **seguro** (proceso independiente, SQLite WAL para lectores concurrentes; el único costo es latencia UX, mitigada con `limit` default 5/max 10 + timeout 60s/juicio + resultados parciales). Por eso se mantuvo como tool MCP completo (56), sin escalar al founder.

## 6. Commits (7) y release
`046d751` migración 013 · `7319958` store (relation CRUD + FTS5 candidates) · `b2cf8b6` conflicts leaf (detect+judge) · `0cfea4a` service orchestration + save hint · `a3a29ce` retrieval post-pass · `77fa64f` mcp+cli (51→56) · `1f29c18` docs. PR #9 → `a07055f`. Tag v1.9.0 → 4 assets.

## 7. Puntos abiertos para la discusión de diseño

1. **M1 — `ConflictLink`/`ConflictUnlink` siempre escriben en `projectStore`.** El código resuelve el store correcto (`getFromEitherStore`) pero descarta el resultado y escribe a `projectStore`. Para un par de memorias **global-global** (en `globalStore`, archivo SQLite distinto), `SetSupersededBy`/`CreateMemoryRelation` fallarían con `ErrNotFound`. Impacto bajo (conflict surfacing es sobre todo project-scoped; falla ruidosa, no corrupción silenciosa), pero es un bug latente para memorias globales. **Candidato a fix menor.**

2. **M2 — `ConflictScan` solo busca candidatos en `projectStore`.** Las memorias global-scope viven en `globalStore` (archivo SQLite separado), y FTS5 cross-DB es arquitectónicamente inviable con SQLite separados. El `whereProject` incluye `scope IN ('global','org')` pero eso solo matchea registros que estén en el mismo archivo. Es una **limitación arquitectónica conocida** (dos DBs por host) más que un bug — pero significa que conflictos entre una decisión global y una de proyecto no se detectan automáticamente. Discutir si vale unificar o cross-query.

3. **Detección vs juicio = costo $0 pero latencia.** Cada `claude -p` cuesta ~5-15s. `scan` con limit=5 puede bloquear ~1 min. Por eso es explícito y con `limit`. Para volúmenes grandes (meses de decisiones, Migratio) el scan completo podría ser lento — quizá un futuro `scan` por lotes/incremental por fecha.

4. **El juicio NO es determinista** (es LLM) — por diseño. Mitigaciones: dry-run default, `judged_by`+`rationale` persistidos (auditable), override manual gana, idempotencia (no re-juzga). Pero re-correr `scan` en otra versión del modelo podría dar veredictos distintos para pares **aún no juzgados**. El negative cache (`unrelated`) congela el veredicto una vez tomado.

5. **`supersedes` excluye, no anota.** El mecanismo reusado (`superseded_by` + exclusión) hace que el agente reciba la actual y **no vea** la obsoleta — más fuerte que "demote+anota" que pedía la spec. Es defendible (el agente obtiene lo correcto) pero pierde la anotación "superseded by X" en el resultado normal (solo visible con `include_superseded=true`). Trade-off: simplicidad/reuso vs visibilidad de la cadena.

6. **M3/M4 (cosméticos):** `normalizePair` duplicado en store y service (ambos unexported, permitido por la arquitectura); marshaler JSON hand-rolled en judge_test con iteración de mapa no-determinista (inocuo, el parser tolera cualquier orden).

## 8. Archivos (referencia)
```
internal/db/migrations/013_memory_relations.sql (NEW) + migration_013_test.go
internal/store/conflicts.go (NEW: relation CRUD + FTS5Candidates) + test; consolidation.go (+ClearSupersededBy)
internal/conflicts/{detect,judge}.go (NEW leaf) + tests (fake claude en PATH)
internal/service/conflicts.go (NEW orchestration) + test; memory.go (save hint goroutine); search.go (annotateConflicts)
internal/model/{errors.go (+2 sentinels), search.go (+ConflictsWith)}
internal/mcp/{tools,handlers}.go (5 tools, 51→56) + handlers_conflicts_test.go
internal/cli/conflicts.go (NEW) + root.go
docs/conflicts.md (NEW), CLAUDE.md (56 tools, conflicts/ leaf, sección Conflicts), CHANGELOG [v1.9.0]
```

> **Operativo:** `mneme upgrade` a v1.9.0 + **reiniciar Claude Code** (5 tools nuevos + comando `mneme conflicts`). Migración 013 se auto-aplica (schema → v13). El juicio (`conflicts scan`) requiere el binario `claude` en PATH; sin él, detección y override manual siguen funcionando.
