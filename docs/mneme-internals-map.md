# mneme Internals Map

> Mapa de subsistemas internos de mneme, producido en SPEC-037 (§4) como base de reconciliación. Reusable por SPEC-038 (model-per-phase), SPEC-039 (memory conflict), SPEC-040 (delegation triggers + tokenizer). Las referencias archivo:línea son aproximadas y deben confirmarse contra el código vivo antes de editar.

## 1. Install machinery (`internal/install/`)

**Embeds** (`assets.go`):
- Tres `embed.FS` para `assets/agents/*.md`, `assets/commands/*.md`, `assets/templates/*.md` (glob = solo hijos directos).
- `delegationHookScript` embebido como `[]byte` (archivo único, no FS).
- `filesFromEmbed(fs embed.FS, subdir, destDir string) ([]CommandFile, error)`: lee hijos directos de `subdir`, **salta directorios**, mapea a `CommandFile{Path, Content}`. Usa `path.Join` (embed.FS siempre usa forward slashes). **Limitación:** es PLANO, no recursa subdirectorios. Para árboles (skills) usar `fs.WalkDir` sobre el embed.FS.
- `//go:embed assets/skills` (patrón de directorio) embebe el árbol recursivo completo, excepto archivos que empiezan con `.`/`_`. No requiere prefijo `all:`.

**`CommandFile`** (`install.go`): `{Path string, Content []byte}`. Transporte de contenido+destino por el pipeline de install.

**`Install()` flow** (`install.go`): ~9 pasos secuenciales, colectando errores en `[]string` (cada paso independiente; instalación parcial aporta valor): WriteMCPConfig, PatchHooks, InjectProtocol, WriteCommands, WriteAgents, WriteTemplates, PatchDelegationHook+WriteDelegationHook, CreateWorkflowDirs, MigrateWorkflowDir. Para añadir un subtree nuevo: insertar un paso (p.ej. entre Templates y DelegationHook) con el patrón de nil-guard `if agent.X != nil { ... }`.

**Idempotencia:**
- Agents (`WriteAgents`): siempre sobrescritos (builtin autoritativo).
- Templates (`WriteTemplates`): nunca sobrescritos (preserva customizaciones; `os.Stat`).
- Hooks (`WriteDelegationHook`, `hooks.go`): por checksum SHA256; idéntico+no-force → skip; distinto → backup `.bak-YYYYMMDD-HHMMSS` + write.

**Bit ejecutable:** `os.WriteFile(destPath, content, 0o755)` al crear (`hooks.go`). Patrón para `scripts/*.sh` y `validation/*.sh`.

**`DryRun()`** (`install.go`): descripción legible de lo que haría Install; extender con líneas nuevas.

**`ClaudeCode()`** (`install.go`): devuelve `*Agent` con closures por paso. Un subtree nuevo añade un campo func al struct Agent o se llama directo.

## 2. Frontmatter parsing

- **Vault** (`internal/vault/reader.go` `parseFrontmatter`): parser línea-por-línea entre `---`; maneja `key: value`, `key:`, `  - item`; switch por key; keys desconocidas ignoradas (forward-compat). Importa `internal/model`. La lógica de parsing es string puro y replicable sin importar model.
- **Serialización** (`internal/vault/frontmatter.go` `WriteTo`): `fmt.Fprintf` con orden de campos fijo (determinista). Patrón para reescribir frontmatter (p.ej. pin/unpin de skills).
- **Agents** (`assets/agents/*.md`): frontmatter YAML (`name/description/model/color/tools`) parseado por **Claude Code**, no por mneme. (Relevante para SPEC-038 `model:`.)
- No hay dependencia `gopkg.in/yaml.v3` en go.mod (decisión recurrente: parser manual).

## 3. Memory store (`internal/db`, `internal/store`, `internal/service`)

- **Schema** (`migrations/001_initial.sql`): tabla `memories` (~18 cols, PK UUIDv7, soft-delete `deleted_at`, índices parciales WHERE deleted_at IS NULL, unique upsert `(topic_key, project, scope)`). `memories_fts` (FTS5 sobre title/content/type/topic_key, tokenizer `porter unicode61`, triggers de sync). `memory_files` (junction). `sessions`.
- **Store** (`store/memory.go`): `MemoryStore` envuelve `*db.DB` (tipo concreto, repository pattern). `Create()` genera UUIDv7, inserta en memories+memory_files. Search FTS5 `MATCH ?` con ranking BM25.
- **Service** (`service/memory.go`): `MemoryService` (stores project+global, config, embedder, Hebbian pool, access tracker). `Save()` valida, resuelve scope/project, maneja upsert por topic_key, crea memoria, genera embedding.
- Migraciones: `internal/db/migrate.go` runner; embed `migrations/*.sql`; `schema_version` table; pre-flight checks en Go cuando hace falta. Última aplicada: 012 (schema v12). Próxima: 013.

## 4. Hooks / tokenizer

- **Hook CLI** (`internal/cli/hook.go`): `mneme hook <event>` → session-start, session-end, pre-tool-use, enforce-delegation, tokenize, path-owned.
- **pre-tool-use**: `runHookPreToolUse` lee JSON de stdin, evalúa reglas (`internal/rules/match.go`), emite markdown a stdout, exit 2 para bloquear.
- **enforce-delegation** (SPEC-069): `runHookEnforceDelegation` — orchestrator-guard in-process (Go), reemplaza el bash `enforce_delegation.sh` (ahora un shim de ~6 líneas). Lógica pura portada a `internal/enforcement` (leaf: stdlib + `internal/shell`); wiring de I/O + cierre `OwnershipFunc` sobre `resolvePathOwnership` (SPEC-068) sin subprocess.
- **tokenize**: `runHookTokenize` parsea comandos shell de stdin → JSON estructurado. Superficie general/retro-compat; `enforce-delegation` ya no lo invoca como subprocess (llama `shell.Tokenize` in-process vía `internal/enforcement`).
- **Shell pkg** (`internal/shell/`): tokenizer (mvdan.cc/sh/v3/syntax). (Relevante para SPEC-040.)
- **Rules** (`internal/rules/match.go`): matching sobre `applies_to` (path globs, tool selectors, negaciones); severidades info/warn/block.
- **Settings registration** (`install.go` `PatchHooks`): hooks en `~/.claude/settings.json` bajo `hooks.{Event}` como matcher-groups; append idempotente (chequea comando existente).

## 5. CLI + MCP registration

- **CLI** (`internal/cli/root.go`): `root.AddCommand(newXxxCmd())`. Command groups (lane, backlog, spec, vault, codegraph) = parent `cobra.Command` + `AddCommand()` de subcomandos. Ej: `newLaneCmd()` en `internal/cli/lane.go`.
- **Init**: `initService()` (MemoryService), `initSDDService()` (SDDService). Operaciones filesystem-only pueden no usar ninguno.
- **MCP tools** (`internal/mcp/tools.go`): `allTools()` devuelve `[]ToolDefinition` (struct literals con Name/Description/InputSchema map[string]any). Conteo actual: 41 (14 mem_*, 4 backlog_*, 8 spec_*, 5 lane_*, 10 codegraph_*).
- **MCP dispatch** (`internal/mcp/handlers.go`): `handleToolCall()` switch sobre `params.Name` → `h.handleXxx()`. Patrones: estándar (unmarshal→service→`resultFromAny`); guard `if h.sdd == nil { return h.sddUnavailable(...) }`; **IsError+payload** (lane_audit): cuando el servicio devuelve error de dominio PERO también un result, marshal el result y devolver `ToolCallResult{IsError:true, Content:[text]}` (no error de protocolo).
- **mapServiceError** (`handlers.go`): mapea sentinels → códigos JSON-RPC. NotFound → CodeMemoryNotFound; validación → CodeInvalidParams; otros → CodeInternalError.
- **Server** (`internal/mcp/server.go`): struct con MemoryService, SDDService (opcional), tools, handlers, logger, version. Un servicio nuevo se añade como campo opcional (como `sdd`).
