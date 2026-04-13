# mneme — Architecture & Design Documentation

Documentación viva de la arquitectura de mneme. Explica qué se construyó, cómo funciona, por qué se tomaron las decisiones, y cómo encajan las piezas.

---

## Qué es mneme

mneme es un sistema de memoria persistente para agentes AI de coding. Un solo binario Go que expone un servidor MCP (Model Context Protocol) sobre stdio, permitiendo que cualquier agente AI compatible (Claude Code, OpenCode, Gemini CLI, Codex, Cursor, Windsurf) guarde y recupere conocimiento entre sesiones.

### El problema que resuelve

1. **CLAUDE.md como campo de batalla** — Los archivos de instrucciones mezclan configuración del agente con conocimiento del proyecto. Cuando un líder de equipo define reglas y el desarrollador define las suyas, colisionan.

2. **Amnesia entre sesiones** — Cada vez que se abre una nueva sesión, el agente no sabe nada. Patrones descubiertos, decisiones de arquitectura, bugs resueltos — todo se pierde.

3. **Islas de conocimiento** — Lo que se aprende en un proyecto no existe en otro. Soluciones reutilizables, patrones propios, librerías custom — no se comparten.

### La solución

Una base de datos SQLite local con búsqueda full-text (FTS5) expuesta via MCP. El agente llama herramientas como `mem_save`, `mem_search`, `mem_context` para guardar y recuperar conocimiento estructurado.

---

## Arquitectura de alto nivel

```
┌──────────────────────────────────────────────────┐
│                  mneme binary                     │
│                                                   │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐       │
│  │   CLI    │  │   MCP    │  │  HTTP    │       │
│  │ (cobra)  │  │ (stdio)  │  │ (futuro) │       │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘       │
│       │              │              │              │
│  ┌────▼──────────────▼──────────────▼────┐       │
│  │           Service Layer                │       │
│  │  MemoryService / SearchService         │       │
│  └────────────────┬──────────────────────┘       │
│                   │                               │
│  ┌────────────────▼──────────────────────┐       │
│  │           Storage Layer                │       │
│  │  MemoryStore / SearchStore (SQLite)    │       │
│  └────────────────┬──────────────────────┘       │
│                   │                               │
│  ┌────────────────▼──────────────────────┐       │
│  │         Infrastructure                 │       │
│  │  Config / ProjectDetector / DB         │       │
│  └───────────────────────────────────────┘       │
└──────────────────────────────────────────────────┘
```

### Principio de diseño: Clean Architecture

Las dependencias fluyen hacia adentro. El paquete `model` es el centro — no depende de nada externo. `store` depende de `model` y `db`. `service` depende de `store` y `model`. `mcp` y `cli` dependen de `service`.

```
cli, mcp → service → store → db
                  → model (leaf, zero deps)
                  → scoring → model
                  → project
```

Ningún paquete interno importa a otro que esté "arriba" en la cadena.

---

## Paquetes implementados

### `internal/model/` — Tipos de dominio
**Estado:** Completo | **Tests:** 35 subtests | **Deps:** Solo stdlib

El paquete hoja. Define todos los tipos de dominio: `Memory`, `MemoryType` (9 tipos), `Scope` (3 scopes), request/response structs para cada operación, scoring defaults, y 7 sentinel errors. Cero dependencias externas garantiza que el dominio es puro y portable.

### `internal/project/` — Detección de proyecto
**Estado:** Completo | **Tests:** 19 casos | **Deps:** Solo stdlib

Detecta el proyecto actual parseando el git remote origin. Soporta SSH (`git@github.com:org/repo.git`), HTTPS, SSH con puerto, y GitLab anidado. Fallback al nombre del directorio cuando no hay remote. `SlugToFilename` convierte slugs a nombres de archivo seguros.

### `internal/config/` — Configuración
**Estado:** Completo | **Tests:** 38 subtests (86.7% coverage) | **Deps:** go-toml/v2

Carga configuración TOML con tres niveles de precedencia: defaults → archivo TOML → env vars. Todos los valores tienen defaults sensatos — mneme funciona sin archivo de config. Expone helpers para paths de DB (`ProjectDBPath`, `GlobalDBPath`).

### `internal/db/` — Base de datos
**Estado:** Completo | **Tests:** 5 tests | **Deps:** go-sqlite3

Wrapper sobre `*sql.DB` que configura SQLite con WAL mode, foreign keys, busy timeout 5s, y synchronous NORMAL. Ejecuta migraciones embebidas (via `go:embed`) automáticamente al abrir. Requiere build tag `-tags fts5`.

### `internal/store/` — Acceso a datos
**Estado:** Completo | **Tests:** 20 tests | **Deps:** db, model, uuid

Repository pattern puro. CRUD completo de memorias con soporte para upsert via `topic_key`. FTS5 search con stop words removal, preview truncation, y BM25 scoring. Session tracking. Todos los tests contra SQLite in-memory real — sin mocks.

### `internal/scoring/` — Scoring de importancia
**Estado:** Completo | **Tests:** 43 subtests | **Deps:** model

Cuatro archivos especializados: importancia inicial (type-based defaults con override), decay exponencial (Ebbinghaus-inspired), relevancia final (BM25 × importance × recency), y RRF fusion (preparado para Fase 2). Funciones `*At()` con `now` explícito para tests deterministas.

### `internal/service/` — Lógica de negocio
**Estado:** Completo | **Tests:** 25 tests | **Deps:** store, model, scoring, config

Orquesta las operaciones de negocio: validación de inputs, scoring automático, upsert logic, access tracking, context assembly con token budgeting, session lifecycle. El Context assembly prioriza session summaries y architecture memories, respeta budgets de tokens.

### `internal/mcp/` — Servidor MCP
**Estado:** Completo | **Tests:** 14 tests | **Deps:** service, model

Servidor JSON-RPC 2.0 sobre stdio. Lee requests línea por línea, las dispatcha al handler correspondiente, y escribe responses como JSON compacto + newline. 7 herramientas expuestas con JSON schemas completos. `handleMessage()` permite testing sin I/O loop.

### `internal/cli/` — Comandos CLI
**Estado:** Completo | **Deps:** service, config, db, store, project, mcp, cobra

7 comandos: save, search, get, status, mcp, version, help. `initService()` helper compartido que carga config, aplica flag overrides, detecta proyecto, y abre la DB correcta. Output humano-friendly por defecto, `--json` flag donde aplica.

---

## Decisiones de diseño

*(Se documentan conforme se toman)*

### D001: SQLite con CGO obligatorio
**Decisión:** Usar `mattn/go-sqlite3` con CGO en lugar de alternativas pure-Go.
**Razón:** FTS5 (full-text search) es crítico para mneme. Las alternativas pure-Go (modernc, zombiezen) no soportan FTS5 de forma fiable o tienen limitaciones de rendimiento.
**Consecuencia:** El build requiere `CGO_ENABLED=1` y un compilador C. Afecta cross-compilation y CI.

### D002: UUIDv7 para identificadores
**Decisión:** Usar UUIDv7 (RFC 9562) para todos los IDs.
**Razón:** Son time-sortable (el timestamp está en el prefijo), lo que permite range queries eficientes y orden cronológico natural. Globalmente únicos sin coordinación.

### D003: No ORM
**Decisión:** SQL crudo con `database/sql`.
**Razón:** ORMs en Go añaden complejidad sin beneficio proporcional. Las queries de mneme son lo suficientemente simples para SQL directo. Mantiene el control total sobre FTS5, triggers, y optimizaciones.

### D004: Tests contra SQLite real
**Decisión:** No mockear la base de datos. Tests usan SQLite in-memory (`:memory:`).
**Razón:** Los mocks de base de datos dan falsa confianza. SQLite in-memory es rápido, determinista, y testea el SQL real que correrá en producción.

### D005: internal/model sin dependencias externas
**Decisión:** El paquete `model` no importa nada fuera de la stdlib.
**Razón:** Es el paquete hoja. Todos dependen de él, él no depende de nadie. Esto garantiza que los tipos de dominio son puros y portables.

### D006: Build tag -tags fts5
**Decisión:** Todos los builds y tests requieren `-tags fts5`.
**Razón:** `mattn/go-sqlite3` no compila la extensión FTS5 por defecto. El build tag activa la compilación de FTS5 a nivel de C. Sin él, `CREATE VIRTUAL TABLE ... USING fts5(...)` falla en runtime.
**Consecuencia:** Todo comando de build/test debe incluir el tag. Documentado en CLAUDE.md.

### D007: handleMessage() para testing del MCP server
**Decisión:** El server MCP expone `handleMessage([]byte) (JSONRPCResponse, bool)` además del loop `Run()`.
**Razón:** Testear un loop de I/O es frágil y propenso a deadlocks. `handleMessage` permite tests unitarios directos sin buffers ni goroutines. `Run()` es el wrapper trivial que hace el loop.

### D008: Fire-and-forget en access tracking
**Decisión:** `service.Get()` incrementa `access_count` después de retornar la memoria, sin bloquear.
**Razón:** El tracking de acceso es un side-effect observacional, no parte del contrato de Get. Si falla, se loguea pero no se propaga el error. Prioriza latencia sobre exactitud del contador.

### D009: SetMaxOpenConns(1) en tests
**Decisión:** Los tests de store usan `SetMaxOpenConns(1)` con SQLite in-memory.
**Razón:** `file::memory:` crea una DB distinta por conexión. Con max 1, todas las queries comparten el mismo schema. En producción (file path) esto no es necesario.

### D010: Rows cerrados antes de queries secundarias
**Decisión:** En store, las rows del query principal se cierran completamente antes de ejecutar queries de files.
**Razón:** Con `MaxOpenConns(1)`, un segundo `QueryContext` mientras el primero aún está streaming causa deadlock. Esta es buena práctica incluso sin el constraint.
