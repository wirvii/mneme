# mneme — Progress Log

Registro de avances de implementación. Cada entrada documenta qué se hizo, por qué, y qué decisiones se tomaron.

---

## Fase 1: MVP — Replace CLAUDE.md as source of truth

**Objetivo:** Sistema de memoria funcional donde un agente pueda guardar y buscar memorias via MCP. Suficiente para dejar de usar CLAUDE.md como base de conocimiento.

**Estado:** COMPLETO

### Entregables Phase 1:
- [x] Proyecto Go inicializado (go.mod, estructura de directorios)
- [x] `internal/model/` — Tipos de dominio (7 archivos, 35 subtests)
- [x] `internal/project/` — Auto-detección de proyecto desde git remote (19 test cases)
- [x] `internal/config/` — Configuración TOML (38 subtests, 86.7% coverage)
- [x] `internal/db/` — Conexión SQLite + FTS5 + migraciones (5 tests)
- [x] `internal/store/` — CRUD de memorias + FTS5 search + sessions (20 tests)
- [x] `internal/scoring/` — Scoring de importancia, decay, relevancia, RRF (43 subtests)
- [x] `internal/service/` — Lógica de negocio completa (25 tests)
- [x] `internal/mcp/` — Servidor MCP stdio JSON-RPC 2.0 (14 tests)
- [x] `internal/cli/` — 7 comandos CLI (save, search, get, status, mcp, version, help)
- [x] `cmd/mneme/main.go` — Entrypoint + build smoke test
- [x] Build exitoso y smoke test end-to-end
- [ ] Tests con >85% coverage en paquetes core (pendiente: medir coverage formal)

### Smoke test results:
- `mneme version` → `mneme vdev` ✓
- `mneme help` → lista 8 comandos ✓
- `mneme save -t "Test" -c "content" -T decision` → crea memoria con UUIDv7 ✓
- `mneme search "test"` → encuentra memoria via FTS5 ✓
- `mneme get <id>` → retorna contenido completo ✓
- `mneme status` → detecta proyecto `wirvii/mneme`, muestra conteo ✓
- MCP initialize → handshake correcto, protocolVersion 2024-11-05 ✓
- MCP tools/list → 7 herramientas con JSON schemas completos ✓
- MCP tools/call mem_search → retorna resultados via JSON-RPC ✓

---

### Log de avances

*(Las entradas se agregan cronológicamente, la más reciente arriba)*

#### 2026-04-13 — Fase 1 MVP completa

**Batch 5: MCP + CLI + Entrypoint**
- `internal/mcp/` — Servidor MCP completo: protocol.go (tipos JSON-RPC 2.0), tools.go (7 herramientas con JSON schemas), handlers.go (dispatch + 7 handlers), server.go (loop stdio line-delimited). 14 tests cubriendo handshake, todas las herramientas, validación, y errores.
- `internal/cli/` — 7 comandos cobra: save, search, get, status, mcp, version, help. initService() helper compartido que carga config, detecta proyecto, abre DB.
- `cmd/mneme/main.go` — Entrypoint minimal.
- Build exitoso: `CGO_ENABLED=1 go build -tags fts5 -o mneme ./cmd/mneme`
- Smoke test end-to-end verificado (CLI + MCP protocol)

**Batch 4: Service layer**
- `internal/service/` — 6 archivos: memory.go (Save, Get, Update, Forget), search.go (Search con re-ranking), context.go (Context assembly con token budgeting), session.go (SessionEnd), topic_key.go (SuggestTopicKey). 25 tests.
- Decisión: Get() incrementa access_count fire-and-forget (no bloquea respuesta)
- Decisión: SessionEnd crea memoria session_summary + session record atómicamente

**Batch 3: Store layer**
- `internal/store/` — Repository pattern completo: memory CRUD, FTS5 search, session tracking. 20 tests contra SQLite in-memory real.
- Decisión: SetMaxOpenConns(1) en tests para evitar deadlock con :memory:
- Decisión: Rows cerrados ANTES de queries de files para evitar connection starvation

**Batch 2: DB + Scoring**
- `internal/db/` — SQLite wrapper con WAL, foreign keys, busy timeout. Migración embebida con go:embed.
- Descubrimiento: mattn/go-sqlite3 requiere `-tags fts5` para compilar FTS5. CLAUDE.md actualizado.
- `internal/scoring/` — Importancia inicial, decay exponencial, relevancia final, RRF fusion. 43 subtests.

**Batch 1: Fundaciones**
- `go.mod` inicializado con 4 dependencias: go-sqlite3, uuid/v5, cobra, go-toml/v2
- `internal/model/` — Tipos de dominio puros (zero deps). 9 memory types, 3 scopes, 7 sentinel errors. 35 subtests.
- `internal/project/` — Detección git remote, normalización SSH/HTTPS/GitLab. 19 test cases.
- `internal/config/` — TOML config con defaults, env var overrides, expandHome. 38 subtests.

#### 2026-04-13 — Inicio del proyecto
- Creado repo, README, LICENSE, .gitignore
- Creado CLAUDE.md con estándares de calidad y convenciones
- Creado docs/SPEC.md — spec técnica completa (~1660 líneas)
- Creado CLAUDE.local.md — config del orquestador
- Inicializado ~/.workflows/mneme/ con estructura de workflow
- Eliminado Appendix D (compatibilidad con Engram) — mneme es proyecto independiente
