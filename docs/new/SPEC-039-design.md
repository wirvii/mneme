# SPEC-039 — Diseño técnico del architect (D1-D8)

> Autoritativo para el backend. Acompaña `docs/new/mneme-v1.9-spec-conflict-surfacing.md`, `docs/mneme-internals-map.md`, memoria `spec/SPEC-039-reconciliation`. Sin pushback (re-entrancy de conflicts_scan = seguro). Orden de commits al final.

## Reconciliación confirmada
- `memories_fts` FTS5 (porter unicode61, title/content/type/topic_key). `store/search.go`: buildFTS5Query, FTS5Search (filtra `m.superseded_by IS NULL` si !IncludeSuperseded). `SetSupersededBy(loser,winner)` en store/consolidation.go:81.
- **supersedes → reusar superseded_by** (exclusión retrieval ya funciona). **conflicts_with+unrelated → tabla nueva memory_relations** (relations es entity-a-entity, no sirve).
- Claude CLI: `claude -p "<prompt>" --output-format json` → `{"type":"result","result":"<text>"}` → parsear result → JSON {relation, rationale}. LookPath falla → ErrCLIUnavailable, sin fallback. Timeout 60s.
- Re-entrancy MCP: SEGURO (proceso independiente, SQLite WAL). conflicts_scan queda como MCP tool con limit (default 5, max 10).

## D1. Migración 013 — `internal/db/migrations/013_memory_relations.sql`
```sql
CREATE TABLE IF NOT EXISTS memory_relations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id TEXT NOT NULL, to_id TEXT NOT NULL,
    relation TEXT NOT NULL CHECK (relation IN ('conflicts_with','unrelated')),
    judged_by TEXT NOT NULL DEFAULT 'manual',
    rationale TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    UNIQUE(from_id, to_id)
);
CREATE INDEX IF NOT EXISTS idx_memory_relations_from ON memory_relations(from_id);
CREATE INDEX IF NOT EXISTS idx_memory_relations_to ON memory_relations(to_id);
CREATE INDEX IF NOT EXISTS idx_memory_relations_relation ON memory_relations(relation);
INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (13, datetime('now'));
```
Sin FK (sobreviven soft-delete). conflicts_with simétrica → service normaliza par lexicográfico antes de insertar. INSERT OR REPLACE (manual gana sobre cli). Test migration_013 (patrón existente).

## D2. Store — `internal/store/conflicts.go` (NEW)
`MemoryRelation{ID int; FromID,ToID,Relation,JudgedBy,Rationale string; CreatedAt time.Time}` + `MemoryRelationListOptions{Project,Relation string; Limit int}` (en store, NO model — internal/conflicts es leaf).
Métodos en MemoryStore: `CreateMemoryRelation(ctx, from,to,relation,judgedBy,rationale)` (normaliza par para conflicts_with, INSERT OR REPLACE), `DeleteMemoryRelation(ctx, from,to)` (normaliza; ErrNotFound si 0), `ListMemoryRelations(ctx, opts)` (JOIN memories para project filter), `GetMemoryConflicts(ctx, memoryID) []string` (conflicts_with del id), `IsJudged(ctx, a,b) bool` (negative cache), `FTS5Candidates(ctx, sourceID, limit) []string` (FTS5 sobre términos salientes de title+content del source, scoped project+global, excluye self/deleted/superseded/pares ya juzgados [NOT EXISTS contra memory_relations], ORDER BY bm25 LIMIT). Helper `normalizePair(a,b)`. Reusar SetSupersededBy para supersedes.

## D3. `internal/conflicts/` (leaf, solo stdlib, sin import model/store)
- detect.go: `ExtractSalientTerms(title, content string, maxTerms int) []string` (strip stopwords, dedup, sort por longitud desc), `BuildCandidateQuery(terms) string` (FTS5 quote + OR). Determinista.
- judge.go: `Verdict{Relation string; WinnerID,LoserID,Rationale string}` (Relation: supersedes_a_over_b/supersedes_b_over_a/conflicts_with/unrelated), `JudgeResult{...}`, `ErrCLIUnavailable`, `JudgeConfig{CLIPath string; Timeout time.Duration}`, `NewJudgeConfig() (*JudgeConfig,error)` (exec.LookPath claude → ErrCLIUnavailable), `JudgePair(ctx, cfg, aID,aTitle,aContent, bID,bTitle,bContent) (*Verdict,error)` (`claude -p "<prompt>" --output-format json`, parse result→JSON interno, valida relation ∈ 4 valores). Prompt acotado (ver spec §5.3). Patrón exec+timeout de validate.go.

## D4. Servicio — `internal/service/conflicts.go` (NEW)
Tipos en service: ConflictScanRequest{Project string; Limit int; Apply bool}, ConflictScanResponse{Pairs []ConflictPairResult; Applied bool; Total,Errors int}, ConflictPairResult{MemoryA,MemoryB,TitleA,TitleB,Relation,Rationale,Error string; Skipped bool}, ConflictRelation{FromID,ToID,Relation,JudgedBy,Rationale,CreatedAt string}.
Métodos en MemoryService:
- `ConflictCandidates(ctx, memoryID, limit) []string` (FTS5Candidates, determinista).
- `ConflictScan(ctx, req)`: NewJudgeConfig (ErrCLIUnavailable si no hay claude); List memorias del project (limit 200); por cada una FTS5Candidates(5); dedup pares (normalizar); cap a req.Limit; por par carga ambas + JudgePair; si Apply: supersedes→SetSupersededBy(loser,winner), conflicts_with/unrelated→CreateMemoryRelation; errores → Skipped=true, continúa.
- `ConflictLink(ctx, from,to,relation,rationale)`: valida relation ∈ {supersedes,conflicts_with,unrelated} (ErrInvalidRelation); ambas memorias existen (ErrNotFound); supersedes→SetSupersededBy(to,from); otras→CreateMemoryRelation judged_by=manual.
- `ConflictUnlink(ctx, from,to)`: borra de memory_relations; y si to.superseded_by==from o viceversa, limpia superseded_by (UPDATE ... SET superseded_by=NULL).
- `ConflictList(ctx, project) []ConflictRelation`.
- **Save hint NO-BLOQUEANTE:** en service/memory.go Save() tras los hooks existentes, `svc.logConflictHint(ctx, result)` → FTS5Candidates(3), si hay loguea slog.Info; NO escribe relaciones, NO juzga, NUNCA propaga error (defer/recover).
Sentinels en model/errors.go: ErrCLIUnavailable, ErrInvalidRelation.

## D5. Retrieval post-pass
model/search.go: añadir `ConflictsWith []string \`json:"conflicts_with,omitempty"\`` a SearchResult. service/search.go Search(): tras sort+truncation final, `svc.annotateConflicts(ctx, results)` → por cada result GetMemoryConflicts, si el conflicto está en el result set añade a ConflictsWith de ambos (simétrico); project+global stores; defer/recover. NO reordena/filtra. supersedes ya excluido por superseded_by. unrelated sin efecto.

## D6. MCP + CLI (51→56)
tools.go: conflicts_candidates {id, limit?}, conflicts_scan {project?,limit?,apply?}, conflicts_link {from_id,to_id,relation,rationale?}, conflicts_unlink {from_id,to_id}, conflicts_list {project?}. handlers.go: 5 handlers + dispatch. handleConflictsScan: si error envuelve ErrCLIUnavailable → IsError:true con payload {error, suggestion} (NO protocolo). mapServiceError: ErrInvalidRelation→CodeInvalidParams. cli/conflicts.go: grupo `mneme conflicts` (candidates <id> [--limit]/scan [--project][--limit][--apply]/link <from> <to> <rel> [--rationale]/unlink <from> <to>/list [--project]) + root.go.

## D7. Docs
docs/conflicts.md (2 pasos, §1.1 por qué LLM en juicio, subprocess costo-cero, retrieval, override manual). CLAUDE.md (56 tools "+5 conflicts_*", conflicts/ en leaf packages, sección "Conflicts (v1.9.0)"). CHANGELOG [v1.9.0].

## D8. Tests
conflicts/detect_test (table-driven determinista), conflicts/judge_test (FAKE claude en PATH via t.Setenv: supersedes a>b/b>a/conflicts_with/unrelated/malformed→error/timeout/CLI-ausente→ErrCLIUnavailable), store/conflicts_test (CRUD, UNIQUE idempotente, normalización par, FTS5Candidates excluye self/juzgados, IsJudged, GetMemoryConflicts simétrico), service/conflicts_test (Candidates, Link supersedes→superseded_by, Link conflicts_with→row, Unlink ambos, List, Scan CLI-ausente→ErrCLIUnavailable, Link relation inválida→ErrInvalidRelation, memoria inexistente→ErrNotFound), search_test (annotateConflicts puebla ConflictsWith), mcp handlers (scan CLI-ausente→IsError:true; link válido; relation inválida→InvalidParams), migración 013.

## Orden de commits
1. feat(db): migración 013 memory_relations + test
2. feat(store): memory relation CRUD + FTS5 candidate query + tests
3. feat(conflicts): paquete leaf detect+judge + tests (fake claude)
4. feat(service): orquestación conflicts + save hint + tests
5. feat(service): retrieval post-pass conflicts_with (SearchResult.ConflictsWith + annotateConflicts)
6. feat(mcp,cli): 5 conflicts tools + grupo CLI + tests
7. docs(conflicts): conflicts.md + CLAUDE.md + CHANGELOG v1.9.0
8. test(conflicts): tests de integración restantes (o plegar en los anteriores)

## Anti-scope
Detección determinista FTS5; SOLO juicio usa LLM (detect.go vs judge.go). Subprocess SOLO CLI local; CERO API metered. scan dry-run default, --apply persiste, nunca implícito en save. NO auto-delete/edit. NO embeddings. Reusar superseded_by (no duplicar). Retrieval post-pass (no reescribir scoring). NO tocar allowlists/hooks/SDD/lane/skills/models.
