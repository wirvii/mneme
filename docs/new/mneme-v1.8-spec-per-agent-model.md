# SPEC-038 — Per-Agent Model Assignment

**Status:** Ready for implementation · **Target release:** v1.8.0 · **Predecessors:** SPEC-034..037

> **NOTA DE RECONCILIACIÓN (orquestador, 2026-05-29):** sondeo previo. Detalle en `spec/SPEC-038-reconciliation` (la completa el architect). Hallazgos:
> - **Los agents YA tienen `model:`** en su frontmatter (`internal/install/assets/agents/*.md`): architect=`claude-opus-4-6`, backend/frontend/bug-hunter=`claude-sonnet-4-6`, **qa-tester=`claude-opus-4-6`**. Valores = IDs completos pinneados (no aliases). La spec §5.2 quiere qa-tester en el modelo barato → este release cambia qa de opus→sonnet (deliberado). Defaults preferidos: aliases (`opus`/`sonnet`) future-proof, no IDs pinneados (confirmar alias válidos en reconciliación: opus/sonnet/haiku/inherit).
> - **Config:** `internal/config/config.go` = TOML en `~/.mneme/config.toml` (user-config, NO asset → sobrevive upgrade). Reusar añadiendo sección `[models]` (map agent→model). NO inventar config paralela.
> - **Frontmatter de agents** tiene schema DISTINTO a skills (name/description/model/color/tools/permissionMode + comentario YAML). El `WriteFrontmatter` de skill es schema-específico → NO reusable tal cual. Preferencia fuerte: **edición quirúrgica de la línea `model:`** (reemplazar si existe — existe en todos — o insertar), preservando TODO lo demás byte-a-byte. Esto satisface "no tercer serializador completo" + es IMPOSIBLE que reintroduzca I1 (no re-serializa). El architect decide y documenta.
> - **2 rutas de install** (install.Install() lib usado por upgrade; CLI mneme install claude-code que re-secuencia a mano) — §5.4 las consolida. El architect EVALÚA el tamaño: si es extracción focalizada, hacerlo; si es más grande, PUSHBACK para escalar.

## 1. Objetivo
Modelo por subagente (≈ por fase SDD) como palanca de costo. Tras el release: cada agente tiene `model:` escrito en su frontmatter instalado en install-time; viene de config que sobrevive upgrade sobre defaults de código; `mneme model list/set/reset` (paridad MCP); string abierto (alias desconocido warn, nunca block); las 2 rutas de install consolidadas para que el model-apply no reproduzca C1.

## 2. Defaults (§5.2 — palanca de costo, documentada, tuneable)
```
architect   → opus    // diseño/juicio: NUNCA degradar (su output es la spec)
backend     → sonnet  // implementación: sigue spec ajustada
frontend    → sonnet
qa-tester   → sonnet  // verificación contra criterios fijos
bug-hunter  → sonnet  // investigación en scope definido
```
docs/models.md: el architect se mantiene fuerte porque sus errores propagan; bug-hunter y qa-tester son los primeros candidatos a subir si baja la calidad (`mneme model set bug-hunter opus`). Usar los alias que Claude Code acepte (confirmar).

## 3. Diseño

### 3.1 Config (3 capas, precedencia baja→alta)
1. **Defaults de código** — map Go `defaultAgentModels` (§2). Piso, siempre presente.
2. **Config overrides** — sección `[models]` en `~/.mneme/config.toml` (agent→model). Opcional; keys ausentes caen a default. **NO es asset; upgrade nunca la sobrescribe.**
3. (sin capa runtime — asignación estática).
Modelo efectivo = override si existe, si no default.

### 3.2 Apply-on-install
En el paso de install de agents, por cada agente: resolver modelo efectivo → escribir `model: <efectivo>` en `~/.claude/agents/<agent>.md` vía la edición quirúrgica round-trip-safe. El asset bundled puede o no traer `model:`; install es autoritativo y lo sobrescribe. La reescritura preserva verbatim name/description/tools/permissionMode/comentarios/body. Test (extendiendo el round-trip endurecido de I1): aplicar un modelo cambia SOLO la línea `model:`, 3 ciclos, con caracteres especiales en description, sin corrupción.

### 3.3 Consolidación de rutas (§5.4 — landmine C1, in-scope por necesidad)
Extraer los pasos de install a UNA secuencia compartida (`[]installStep` o equivalente) que ejecuten tanto `install.Install()` como el CLI `mneme install claude-code` (el CLI NO re-secuencia a mano; delega o comparte la lista). Test de paridad: ambos entrypoints corren el set ordenado idéntico. **Mínimo:** "una lista, dos entrypoints delgados", no rediseño. Si la reconciliación revela que es más grande que una extracción focalizada → PARAR y escalar (posible spec aparte).

### 3.4 Comandos (CLI + MCP paridad)
| CLI | MCP | Behavior |
|---|---|---|
| `mneme model list` | `model_list` | cada agente, modelo efectivo, origen (default/override) |
| `mneme model set <agent> <model>` | `model_set` | escribe override en config; valida agente conocido (ErrUnknownAgent); warn (no error) si alias desconocido; rechaza modelo vacío (ErrInvalidModel). Decide y documenta: ¿solo escribe config + hint a reinstalar, O aplica también al archivo instalado? (elegir una) |
| `mneme model reset [<agent>]` | `model_reset` | quita override de uno, o todos si se omite |
- mapServiceError: ErrUnknownAgent → invalid-params. Alias desconocido = warning en result exitoso, NO error.
- Set de agentes conocidos derivado de los assets bundled (única fuente, no hardcode en 2 lados).

### 3.5 Model string
Cualquier no-vacío se acepta y escribe. `knownAliases` (opus/sonnet/haiku/inherit + lo que confirme reconciliación) solo dispara WARNING en set, nunca block. Vacío → ErrInvalidModel.

## 4. File map (confirmar en reconciliación)
internal/install/assets/agents/*.md (EDIT opcional: default model o quitarlo, install sobrescribe), internal/install/install.go (secuencia `[]installStep` compartida + paso model-apply), internal/cli/install.go (delega/comparte, quita re-secuenciado), internal/install/agentmodel.go (NEW: resolver efectivo + aplicar al frontmatter vía editor quirúrgico), internal/install/install_test.go (parity test + model-apply round-trip I1-hardened), internal/frontmatter/ leaf o reuso (editor quirúrgico, sin tercer serializador, sin I1), internal/config (sección [models], read/write, sobrevive upgrade), internal/model/errors.go (ErrUnknownAgent, ErrInvalidModel), internal/service/models.go (NEW: list/set/reset; known-agents de assets) + test, internal/mcp/{tools,handlers}.go (model_list/set/reset + mapServiceError), internal/cli/model.go (NEW) + root.go, docs/models.md (NEW), CLAUDE.md (sección Models, conteo 48→51), CHANGELOG [v1.8.0].
Tool count: 48 + 3 = **51**.

## 5. Criterios de aceptación
Reconciliación: spec/SPEC-038-reconciliation (frontmatter actual, writer elegido [reusado no re-implementado], ubicación config, mapeo 2 rutas, alias conocidos).
Model assignment: defaultAgentModels con §2; config (no-asset, sobrevive upgrade) overridea por agente; install escribe `model: <efectivo>` en cada ~/.claude/agents/<agent>.md; la reescritura cambia SOLO `model:` y preserva todo verbatim (test 3-ciclos special-char, sin I1); override sobrevive upgrade simulado.
Consolidación: ambas rutas ejecutan una lista de pasos compartida; parity test; (si fue más grande que extracción focalizada, se escaló, no se expandió en silencio).
Comandos: list (agente→efectivo+origen) CLI+MCP; set escribe override, rechaza agente desconocido (ErrUnknownAgent), warn (no error) alias desconocido, rechaza modelo vacío; reset uno/todos; known-agents de assets (única fuente); paridad MCP/CLI los 3.
Scope: NO cross-provider/proxy; NO allowlist cerrada; NO switching runtime per-task; NO tercer frontmatter writer; I1 no reintroducido; allowlists/hooks/SDD/lane/skills/memory schema intactos.
Calidad: make test + test-race; golangci-lint limpio (verificado por orquestador); docs+CLAUDE.md+CHANGELOG; conteo tools correcto.

## 10. Anti-scope
Internals map + reconciliación PRIMERO. NO cross-provider/proxy/base-url. NO allowlist cerrada (alias desconocido warn). NO switching runtime per-task. NO tercer frontmatter writer, NO reintroducir I1. Consolidar 2 rutas en una lista compartida (o escalar si es mayor). Config override NO es asset y sobrevive upgrade. NO tocar allowlists/hooks/SDD/lane auditor/skills/memory schema. Solo: model por agente (defaults+config), apply-on-install vía editor fijo, consolidar rutas, grupo `mneme model` con paridad MCP + docs/tests.
