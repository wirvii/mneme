# SPEC-039 — Memory Conflict Surfacing

**Status:** Ready for implementation · **Target release:** v1.9.0 · **Predecessors:** SPEC-034..038

> **NOTA DE RECONCILIACIÓN (orquestador, 2026-05-29):** sondeo previo; detalle en `spec/SPEC-039-reconciliation` (la completa el architect). Hallazgos que reconfiguran el alcance:
> - **`relations` (grafo) es ENTITY-a-ENTITY** (`source_id`/`target_id` → entities(id)), NO memoria-a-memoria. Ya tiene tipos `supersedes`/`conflicts_with` pero operan sobre entities → NO sirve directo para edges memoria-memoria.
> - **`memories.superseded_by` YA existe** (model/memory.go) + `store.SetSupersededBy(loserID, winnerID)` (lo usa consolidation dedup) + `mem_search`/`SearchRequest.IncludeSuperseded` (default false) **YA excluye superseded**. → **el `supersedes` está en gran parte construido**: reusar superseded_by + la exclusión existente; NO duplicar.
> - `conflicts_with` (memoria-memoria) y `unrelated` (negative cache) son nuevos → tabla `memory_relations` (el grafo entity-based no los cubre).
> - Subprocess precedentes: `internal/lane/git.go` (git, exec+timeout), `internal/skill/validate.go` (sh, exec+timeout+fake binary en tests). Reusar el patrón. Confirmar el flag headless de la Claude CLI (`claude -p`/`--print`) — NO adivinar.
> - **Re-entrancy de conflicts_scan via MCP:** spawnar la Claude CLI desde un tool MCP que corre bajo Claude Code. El architect evalúa; si es problemático → conflicts_scan CLI-only (PUSHBACK al founder).

## 1. Objetivo
Cerrar "el agente siguió una decisión que ya cambiamos". Patrón 2 pasos: detección FTS5 determinista (candidatos) + juicio LLM via subprocess a la CLI local (costo $0). Retrieval usa las relaciones (superseded demoted/anotado; conflicts_with flaggeado). Todo opt-in/no-bloqueante.

### 1.1 Excepción consciente al invariante de determinismo
SPEC-035 (lane auditor) y SPEC-037 (linter) son deterministas porque sus preguntas son ESTRUCTURALES. Juzgar si dos decisiones se contradicen es SEMÁNTICO — no hay test determinista. Por eso SOLO el paso de JUICIO usa LLM; la DETECCIÓN sigue determinista (FTS5). La distinción debe preservarse en el código (§5.2 detección vs §5.3 juicio).

## 2. Anti-scope
NO auto-delete/auto-edit de memorias (registrar + demote en retrieval, nunca destruir historia). NO juicio automático en cada save (detección sí; juicio = `scan` explícito). NO embeddings/vector. NO cross-provider/API-key/metered. Solo subprocess a la CLI local; ausente → sin juicio. NO merge cross-project, passive capture, prompt storage. NO tocar allowlists/hooks/SDD/lane/skills/models.

## 5. Diseño

### 5.1 Modelo de relación
Relación dirigida memoria-memoria: `supersedes` (A supersedes B; A actual, B obsoleta), `conflicts_with` (A↔B simétrica, tensión sin claro ganador), `unrelated` (negative cache para no re-juzgar).
**Storage (decisión de reconciliación, el architect confirma):**
- `supersedes` → REUSAR `memories.superseded_by` (set B.superseded_by=A vía SetSupersededBy). Retrieval ya lo excluye. NO duplicar.
- `conflicts_with` + `unrelated` → tabla nueva `memory_relations` (migración siguiente; test patrón migration_0NN_test):
```sql
CREATE TABLE memory_relations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id TEXT NOT NULL, to_id TEXT NOT NULL,
    relation TEXT NOT NULL CHECK (relation IN ('conflicts_with','unrelated')),
    judged_by TEXT NOT NULL,  -- 'cli' | 'manual'
    rationale TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    UNIQUE(from_id, to_id)
);
```
(Si el architect prefiere unificar las 3 en memory_relations en vez de reusar superseded_by, debe justificar por qué no duplica la lógica de exclusión de retrieval que YA funciona. Preferencia: reusar superseded_by.)

### 5.2 Detección de candidatos (determinista, en save)
`mneme conflicts candidates <id>` (y hook en save): FTS5 sobre memorias existentes con términos salientes (title+content) de la nueva, scoped a project+global; top-N (≈5), excluyendo self y pares ya juzgados (negative cache). Determinista, sin LLM/subprocess. Solo ENCUENTRA, no juzga. En save: NO-BLOQUEANTE (falla → stderr, nunca falla el save; disciplina SPEC-034/036); no escribe relaciones; puede emitir hint.

### 5.3 Juicio (LLM via subprocess, explícito)
`mneme conflicts scan [--project] [--limit] [--apply]`: reúne pares no juzgados; por par invoca la Claude CLI headless (flag confirmado en reconciliación) con prompt acotado (title+content de ambas, pide clasificación supersedes A>B / supersedes B>A / conflicts_with / unrelated + rationale 1 línea); parsea. Con `--apply`: persiste (supersedes→SetSupersededBy; conflicts_with/unrelated→memory_relations). Sin `--apply`: dry-run (imprime, no persiste) — DEFAULT. Costo $0 (subscription CLI). CLI ausente/error → reporta, salta, no persiste, NUNCA API metered. Idempotente (negative cache + UNIQUE; ya-juzgados no se re-juzgan). Cada relación persiste judged_by + rationale. Timeout por juicio (reusar exec-con-timeout); timeout → skip, no fail-hard.

### 5.4 Override manual
`mneme conflicts link <from> <to> <supersedes|conflicts_with|unrelated> [--rationale]` (judged_by='manual', manual gana sobre cli). `unlink <from> <to>`. `list [--project]` (relaciones + origen + rationale).

### 5.5 Retrieval
- `supersedes(A,B)`: B demoted/anotado (ya lo hace la exclusión por superseded_by; el agente recibe A, no B como actual). Evaluar añadir anotación "superseded by <A.title>" cuando include_superseded=true.
- `conflicts_with`: flaggear (ambas surfaced, marcadas) — nuevo, post-ranking pass.
- `unrelated`: sin efecto (solo negative cache).
- Integrar SIN reescribir scoring: post-ranking pass que re-pondera/anota según edges. Confirmar punto de hook en reconciliación.

### 5.6 Surface (CLI + MCP paridad)
conflicts_candidates, conflicts_scan ([--apply]), conflicts_link, conflicts_unlink, conflicts_list. Sentinels: ErrMemoryNotFound (reusar si existe), ErrCLIUnavailable, ErrInvalidRelation. MCP: conflicts_scan con CLI ausente → result IsError:true con mensaje (patrón SPEC-035), no error de protocolo; bad requests → mapServiceError. **Re-entrancy:** si el architect halla que conflicts_scan via MCP es problemático (subprocess CLI dentro de tool bajo Claude Code), puede ser CLI-only con el tool MCP devolviendo "córrelo desde la CLI" — ESCALAR como decisión, no forzar.

### 5.7 Conteo y docs
Tools 51 + 5 = **56** (o 55 si scan queda CLI-only). docs/conflicts.md (NEW): modelo 2 pasos, por qué el juicio es LLM (§1.1), subprocess costo-cero, retrieval, override manual.

## 6. File map (confirmar)
migración memory_relations + test; internal/store (relation CRUD + FTS5 candidate query + negative-cache lookup; reusar SetSupersededBy); internal/conflicts/ (NEW leaf: detect.go FTS5 determinista, judge.go subprocess+parse; sin import model); internal/service/conflicts.go (orquesta detect/judge/link/list; hook no-bloqueante en save; retrieval post-pass); save path (hint no-bloqueante); retrieval/scoring path (post-ranking demote+flag); internal/model/errors.go (sentinels); internal/mcp/{tools,handlers}.go (conflicts_* + IsError-payload scan); internal/cli/conflicts.go + root.go; docs/conflicts.md, CLAUDE.md, CHANGELOG [v1.9.0].

## 7. Criterios de aceptación
Reconciliación: spec/SPEC-039-reconciliation (schema/FTS5, reuse superseded_by + memory_relations decision, retrieval hook, flag CLI headless confirmado, subprocess precedent). Detección determinista (FTS5, scoped, excluye self+juzgados, no-bloqueante en save). Juicio (CLI headless, parsea clasif+rationale; dry-run default; --apply persiste; CLI ausente reporta+salta NUNCA API; idempotente; judged_by+rationale). Manual link/unlink/list (manual gana; list muestra origen+rationale). Retrieval (supersedes→B demoted; conflicts_with→flag; unrelated→sin efecto; post-ranking, scoring no reescrito). Scope (sin auto-delete/embeddings/metered; juicio explícito; grafo no duplicado; determinismo detección vs juicio preservado en código). Calidad (make test+race; golangci-lint orchestrator-verified; subprocess testeado con fake claude en PATH; docs+conteo).

## 10. Anti-scope checklist
Internals map + reconciliación PRIMERO. Detección determinista FTS5; solo juicio usa LLM. Juicio = subprocess CLI local; CERO fallback metered. scan explícito, dry-run default, nunca implícito en save. NO auto-delete/edit. NO embeddings. Reusar grafo/superseded_by (no duplicar). Retrieval = post-ranking pass (no reescribir scoring). NO tocar allowlists/hooks/SDD/lane/skills/models. Memorias `mneme save --type --title --content` sin flags nuevos.
