# SPEC-038 — Per-Agent Model Assignment · Informe de implementación

> **Para:** agente de discusión de diseño.
> **Estado:** ✅ Implementado, mergeado (PR #8 → `main`, `f3e5596`) y liberado como **v1.8.0** (4 assets).
> **Origen:** `docs/new/mneme-v1.8-spec-per-agent-model.md` + `docs/new/SPEC-038-design.md`. **Predecesores:** SPEC-034..037. **Fecha:** 2026-05-29.

---

## 1. Qué resuelve

La palanca de costo acordada: **enrutar trabajo de juicio a un modelo fuerte y trabajo de implementación a uno barato**, dentro del mismo proveedor (Anthropic; el cross-provider quedó diferido a post-Migratio). Los subagentes de mneme mapean limpio sobre fases SDD, así que "modelo por agente" = "modelo por fase".

Defaults: **architect=opus** (nunca degradar — su output es la spec, sus errores propagan al 100% del código downstream), **backend/frontend/qa-tester/bug-hunter=sonnet**. Enrutar ~80% de las llamadas (implementers+QA) a sonnet, estimado ~60-70% de ahorro mensual sin pérdida de calidad de diseño. `docs/models.md` marca a bug-hunter y qa-tester como los primeros candidatos a subir si baja la calidad (`mneme model set bug-hunter opus`).

## 2. Reconciliación

- **Los agents YA tenían `model:`** con IDs pinneados (architect/qa=`claude-opus-4-6`, resto=`claude-sonnet-4-6`). Este release los pasa a **aliases** (`opus`/`sonnet`, future-proof) y **baja qa-tester de opus→sonnet** (cambio deliberado de costo).
- **No existía write-back de config** (config.go solo leía TOML). Se añadió `SetModelsOverrides` (atómico, go-toml/v2 ya en deps).
- **El writer de frontmatter de skills (SPEC-037) NO era reusable** para agents (schema distinto). Se usó un **editor quirúrgico de la línea `model:`** — imposible reintroducir el bug I1 porque no re-serializa nada más.

## 3. Diseño implementado

### 3.1 Resolución en 3 capas
`defaultAgentModels` (código, piso) → `[models.overrides]` en `~/.mneme/config.toml` (override, **sobrevive upgrade** porque es user-config, no asset) → (sin capa runtime). Efectivo = override no-vacío, si no default. `BundledAgentNames()` (lee el embed de agents) es la única fuente de agentes conocidos.

### 3.2 Editor quirúrgico (`SetModelInFrontmatter`)
Reemplaza solo la línea `model:` (o la inserta tras `description:`), preservando name/description/tools/permissionMode/comentarios YAML/body byte-a-byte. Test I1-hardened: 3 ciclos con description con comillas/colons/unicode → cero corrupción. **No reusa** `skill.WriteFrontmatter` (decisión consciente: schema distinto, y un line-editor es estructuralmente incapaz de acumular comillas).

### 3.3 Apply-on-install
`ApplyAgentModels` corre como un paso del install, tras "Agent profiles": resuelve el efectivo y lo escribe en `~/.claude/agents/<agent>.md`. `model set` solo escribe la config + imprime un hint a reinstalar (la config es la fuente de verdad; install es el aplicador).

### 3.4 Consolidación TOTAL de rutas (decisión del founder)
Este era el punto de PUSHBACK del architect (ver §4). Resultado: **un único builder `installSteps(opts InstallOptions) []installStep`** + runner `runInstallSteps(steps, progress)` que consumen **ambos** entrypoints:
- `install.Install()` (usado por `upgrade`): `installSteps(InstallOptions{})` + progress `nil` → silencioso, colecta-todo (comportamiento de upgrade sin cambio).
- CLI `mneme install claude-code`: `installSteps(opts-de-flags)` + progress que imprime `[ok]`/`[fail]` → **cambió de fail-fast a colecta-todo** (documentado). Los flags `--force/--reinstall-hooks/--personal` ahora son `InstallOptions`, no branches manuales.
Esto **elimina de raíz la clase de bug C1 de SPEC-037** (donde un paso nuevo se añadía a una ruta y no a la otra). El paso "Agent models" está en la única lista, tras "Agent profiles". Parity test sobre `installSteps`.

### 3.5 Superficie
`mneme model list/set/reset` + 3 tools MCP (`model_list/set/reset`) en paridad. Tools: 48 → **51**. String abierto: alias desconocido → warning (no block); agente desconocido → `ErrUnknownAgent`; modelo vacío → `ErrInvalidModel`.

## 4. Proceso SDD — el PUSHBACK del architect y la decisión del founder

A diferencia de specs anteriores, aquí **el architect levantó un PUSHBACK legítimo** en §5.4: tras mapear ambas rutas de install, encontró que la consolidación total **no era una extracción focalizada** — las rutas divergían en manejo de errores (CLI fail-fast vs librería colecta), branches por flags, e interleaving de output (~280 líneas con riesgo en el path de upgrade). El architect recomendó el mínimo viable (añadir el paso a ambas + parity test) y diferir la consolidación total a su propia spec.

**Lo escalé al founder** (la spec explícitamente delegaba esta decisión), que eligió **hacer la consolidación total ahora**. Reescribí D5 con la forma `InstallOptions` + builder único + runner con progress callback, y el backend la implementó limpiamente. QA confirmó que el path de upgrade no se rompió y los flags siguen funcionando.

Este es un buen ejemplo del valor del PUSHBACK: el architect detectó que el alcance "contenido" de la spec era en realidad mayor, lo cuantificó, y la decisión de incurrir en ese costo quedó explícita en el founder en vez de tomarse en silencio.

| Fase | Resultado |
|---|---|
| Reconciliación | sin forks upfront; agents ya tenían model:, config sin write-back |
| Architect | D1-D9 + **PUSHBACK en §5.4** (consolidación mayor de lo esperado) |
| Founder | eligió **consolidación total** (AskUserQuestion) |
| Backend | 11 commits (incl. el refactor de consolidación), verdes |
| QA | **APPROVED** — 0 críticos/importantes, 2 menores pre-existentes |
| Verificación propia | build OK · test 26/26 · race 26/26 · golangci-lint 0 issues |
| Release | PR #8 → main (`f3e5596`), tag v1.8.0, 4 assets |

## 5. Commits (11) y release
`ca180aa` errors · `37ea029` defaults+aliases · `a26b150` editor quirúrgico · `b31882e` config write-back · `84ba39a` **consolidación rutas + ApplyAgentModels** · `9403b4b` assets a aliases · `1383dcb` ModelsService · `c0a693c` mcp (48→51) · `190fd64` cli model · `2c426ce` docs · `4f39d0f` lint. PR #8 → `f3e5596`. Tag v1.8.0 → 4 assets.

## 6. Puntos abiertos para la discusión de diseño

1. **El cambio de comportamiento del CLI install (fail-fast → colecta-todo)** es un efecto secundario de la consolidación. Ahora `mneme install claude-code` intenta TODOS los pasos aunque uno falle, reportando todos los fallos al final (antes paraba en el primero). Es consistente con `upgrade` y arguablemente mejor (instalación parcial aporta valor), pero es un cambio observable de UX. Documentado en CHANGELOG.

2. **`model set` no aplica al archivo del agente — solo escribe config + hint.** El usuario debe correr `mneme install claude-code` para que el cambio surta efecto en `~/.claude/agents/`. Es separación limpia (config = verdad, install = aplicador), pero significa que un `model set` "no hace nada visible" hasta reinstalar. ¿La UX debería ofrecer un `--apply` que reinstale en el acto? (No se hizo; candidato menor.)

3. **`SetModelsOverrides` re-serializa el config TOML completo**, normalizando orden de campos y descartando keys/comentarios no representados en el struct `Config`. Inocuo hoy (config.toml rara vez se edita a mano), pero un usuario con comentarios custom los perdería al hacer `model set`. Deuda documentada; un futuro write-back basado en AST de go-toml lo haría quirúrgico.

4. **DryRun no deriva de `installSteps`** (mantiene su propio listado, pre-existente a esta spec) — divergencia menor que podría re-aparecer como un mini-C1 para el output de dry-run. No es regresión, pero la consolidación quedó "casi total": el camino de ejecución está unificado, el de dry-run no. Candidato a cierre.

5. **Aliases vs IDs pinneados.** Se eligió `opus`/`sonnet` (tracking automático del modelo vigente) sobre IDs pinneados (`claude-opus-4-6`). Esto significa que mneme NO controla exactamente qué versión de opus/sonnet usa cada agente — sigue al alias de Claude Code. Para reproducibilidad estricta uno querría pinnear; para future-proofing (la spec) uno quiere aliases. Trade-off consciente; `mneme model set architect claude-opus-4-8` permite pinnear si se desea.

6. **base-SHA binding de SPEC-036 funcionó en vivo:** esta misma spec mostró `base_sha: c713eae` capturado al entrar a implementing. Validación orgánica de la feature anterior.

## 7. Archivos (referencia)
```
internal/install/defaults.go (NEW), frontmatter.go (NEW) + tests
internal/config/write.go (NEW) + test; config.go (ModelsConfig)
internal/install/install.go (consolidación: InstallOptions/installStep/installSteps/runInstallSteps/ApplyAgentModels) + parity tests
internal/cli/install.go (usa el builder; ya no re-secuencia), cli/model.go (NEW), cli/mcp.go, root.go
internal/service/models.go (NEW) + test; internal/model/errors.go (2 sentinels)
internal/mcp/{tools,handlers,server}.go (3 tools, 48→51)
internal/install/assets/agents/*.md (model: a aliases; qa opus→sonnet)
docs/models.md (NEW), CLAUDE.md (51 tools, sección Models, 31 commands), CHANGELOG [v1.8.0]
```

> **Operativo:** `mneme upgrade` a v1.8.0 + **reiniciar Claude Code** (3 tools nuevos + comando `mneme model`). Tras el upgrade, los agents en `~/.claude/agents/` tendrán `model:` con los defaults (architect=opus, resto=sonnet). Recordatorio del patrón observado: el paso de install que aplica los modelos lo ejecuta el binario que corre el install — si `mneme upgrade` no los aplica (porque el paso lo corre el binario viejo), basta un `mneme install claude-code` con el binario nuevo.
