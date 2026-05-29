# SPEC-038 — Diseño técnico (D1-D10)

> Autoritativo para el backend. Síntesis del diseño del architect (D1-D4, D6-D9) + **D5 reescrito a CONSOLIDACIÓN TOTAL por decisión del founder** (AskUserQuestion: "Consolidación total ahora en SPEC-038"). Acompaña `docs/new/mneme-v1.8-spec-per-agent-model.md`, `docs/mneme-internals-map.md`, memoria `spec/SPEC-038-reconciliation`.

## D1. Defaults + knownAliases — `internal/install/defaults.go` (NEW)
```go
var defaultAgentModels = map[string]string{
    "architect":  "opus",   // diseño/juicio: nunca degradar
    "backend":    "sonnet", "frontend": "sonnet",
    "qa-tester":  "sonnet", "bug-hunter": "sonnet",
}
var knownAliases = map[string]bool{"opus":true,"sonnet":true,"haiku":true,"inherit":true}
func BundledAgentNames() ([]string, error)  // lee builtinAgents "assets/agents", trim .md — ÚNICA fuente de agentes conocidos
func DefaultModelFor(agent string) string
func IsKnownAlias(alias string) bool
func ResolveEffectiveModels(overrides map[string]string) map[string]string // override no-vacío gana, si no default
```

## D2. Config `[models]` + write-back — `internal/config/config.go` (EDIT) + `internal/config/write.go` (NEW)
- Struct: `Models ModelsConfig` con `ModelsConfig{ Overrides map[string]string \`toml:"overrides"\` }`. Default() → Overrides vacío. Sin validación (strings libres).
- TOML: `[models.overrides]\nbug-hunter = "opus"`.
- `SetModelsOverrides(path string, overrides map[string]string) error`: lee config existente (o vacío), setea Models.Overrides, marshal full vía go-toml/v2 (ya en go.mod), write atómico (.tmp + rename). Normaliza formato (aceptable). 

## D3. Editor quirúrgico frontmatter — `internal/install/frontmatter.go` (NEW)
```go
// SetModelInFrontmatter reemplaza la línea `model: <x>` por `model: <newModel>`,
// preservando TODO lo demás byte-a-byte. Si no hay línea model:, la inserta tras description:.
// Error si no hay delimitadores de frontmatter.
func SetModelInFrontmatter(content []byte, newModel string) ([]byte, error)
```
Algoritmo: split líneas; "---" en línea 0; buscar "---" de cierre; dentro del rango, hallar línea que empieza con "model:" → reemplazar; si no, insertar tras "description:". SOLO 1 línea cambia/inserta. NO parsea ni re-serializa otros campos (imposible I1). Preserva description, tools, comentarios YAML, body.

## D4. Apply-on-install — `internal/install/install.go`
```go
func ApplyAgentModels(agentsDir string, overrides map[string]string) error
```
Resuelve efectivo (ResolveEffectiveModels) y por cada agente: read ~/.claude/agents/<agent>.md → SetModelInFrontmatter → write 0o644. Colecta errores (install parcial vale). Corre DESPUÉS de WriteAgents. known-agents de BundledAgentNames (única fuente; test enforce que defaultAgentModels keys == bundled agents).

## D5. CONSOLIDACIÓN TOTAL de rutas de install (decisión founder)
Unificar Install() (librería, usado por upgrade) y el CLI `mneme install claude-code` en UNA lista de pasos compartida.

### Tipos
```go
// InstallOptions parametriza qué pasos corren y su modo.
type InstallOptions struct {
    Force          bool // --force
    ReinstallHooks bool // --reinstall-hooks
    Personal       bool // --personal
}

// installStep es un paso ordenado del install. Run devuelve un detalle humano
// (p.ej. "example-skill") para el output del CLI, y un error.
type installStep struct {
    Name  string                 // etiqueta estable (p.ej. "MCP server", "Agent profiles", "Agent models")
    Run   func() (detail string, err error)
}
```

### Builder único (la ÚNICA fuente de la secuencia)
```go
// installSteps construye la lista ordenada de pasos para el agente, según opts.
// Es el único lugar donde se enumeran los pasos. Tanto Install() como el CLI lo consumen.
func (a *Agent) installSteps(opts InstallOptions) []installStep
```
Pasos (en orden), cada uno envuelve la lógica existente (WriteMCPConfig, PatchHooks, InjectProtocol, WriteCommands, WriteAgents, **ApplyAgentModels** [nuevo, tras WriteAgents], WriteTemplates, WriteSkills(force=opts.Force||opts.ReinstallHooks), DelegationHook [si opts.ReinstallHooks → ReinstallHooks, si no → PatchDelegationHook+WriteDelegationHook], CreateWorkflowDirs, MigrateWorkflowDir, [si opts.Personal → InstallPersonal]). ApplyAgentModels carga config.Load(DefaultPath()).Models.Overrides.

### Runner único (colecta-todo)
```go
// runInstallSteps ejecuta los pasos en orden, invocando progress por cada uno
// (nil = silencioso), colectando errores. Nunca para en el primer error.
func runInstallSteps(steps []installStep, progress func(name, detail string, err error)) []error
```

### Entrypoints delgados
- `Install(agent *Agent) error` (o firma actual): `steps := agent.installSteps(InstallOptions{})`; `errs := runInstallSteps(steps, nil)`; agrega errs → error. (Comportamiento de upgrade: silencioso, colecta-todo — sin cambio.)
- CLI `RunE`: construye `InstallOptions` desde flags; `steps := agent.installSteps(opts)`; `runInstallSteps(steps, func(name,detail,err){ if err nil → print "[ok] <name>[: detail]" else print "[fail] <name>: err" })`; al final, si hubo errores, return error agregado. **CAMBIO documentado:** el CLI pasa de fail-fast a colecta-todo (consistente con upgrade; mejora). El flag --personal y --reinstall-hooks ahora son parte de opts, no branches manuales.
- DryRun(): puede derivar de installSteps (listar Name de cada paso) o mantenerse, pero debe incluir el paso "Agent models".

### Parity test
Assert que ambos entrypoints derivan de `installSteps`: test que `installSteps(InstallOptions{})` devuelve la secuencia esperada de Names (incluido "Agent models" tras "Agent profiles"), y que es la única definición (no hay segunda lista hardcoded en el CLI). Verificar que con opts variados (ReinstallHooks, Personal, Force) la lista cambia coherentemente.

## D6. Servicio — `internal/service/models.go` (NEW)
`ModelsService{configPath string}` + NewModelsService(configPath). Tipos: ModelInfo{Agent,Model,Origin}, ModelListResponse{Agents []ModelInfo}, ModelSetRequest{Agent,Model}, ModelSetResponse{Agent,Model,Warning,Hint}, ModelResetRequest{Agent}, ModelResetResponse{Reset []string,Hint}.
- `List(ctx)`: BundledAgentNames + config.Load + ResolveEffectiveModels → ModelInfo por agente (Origin "override" si en overrides, si no "default").
- `Set(ctx, req)`: valida agente ∈ BundledAgentNames (ErrUnknownAgent); model no vacío (ErrInvalidModel); si !IsKnownAlias → Warning; load config, overrides[agent]=model, SetModelsOverrides; Hint="run mneme install claude-code to apply".
- `Reset(ctx, req)`: si agent no vacío valida conocido; borra de overrides (uno o todos); write-back; devuelve reset[] + hint.
- Sentinels en internal/model/errors.go: ErrUnknownAgent, ErrInvalidModel.

## D7. MCP + CLI (48→51)
- tools.go: model_list (sin req), model_set (req agent,model), model_reset (opt agent). handlers.go: campo modelsSvc, 3 handlers (patrón estándar unmarshal→service→resultFromAny), dispatch, mapServiceError (ErrUnknownAgent/ErrInvalidModel → CodeInvalidParams). server.go: campo modelsSvc + NewServer param. cli/mcp.go: construye ModelsService.
- cli/model.go (NEW): grupo `mneme model` con list [--json]/set <agent> <model>/reset [<agent>], filesystem-only (no initService). root.go: registrar newModelCmd().

## D8. Docs
docs/models.md (defaults+rationale: architect fuerte porque propaga; bug-hunter/qa primeros candidatos a subir; tuning con `mneme model set`; formato [models.overrides]; alias warn no block; nota cross-provider DIFERIDA; config.toml sobrevive upgrade). CLAUDE.md (51 tools "+3 model_*", sección Models, 31 top-level commands). CHANGELOG [v1.8.0] (incluir el cambio de comportamiento del CLI install: colecta-todo).

## D9. Tests
defaults_test (defaultAgentModels keys == BundledAgentNames; ResolveEffectiveModels override/default/empty), config write_test (SetModelsOverrides write+reload survive; merge), frontmatter_test (round-trip I1-hardened: 3 ciclos, description con comillas/colons/unicode, SOLO cambia model:; casos model presente/ausente/comentario YAML/permissionMode preservados), install parity test (installSteps única fuente; ApplyAgentModels presente en la secuencia; Install() aplica modelos en tmp dir; opts variados), service models_test (List origins; Set unknown agent→ErrUnknownAgent, empty→ErrInvalidModel, unknown alias→warning, known→sin warning; Reset single/all), mcp handlers (model_list/set/reset; mapServiceError ErrUnknownAgent→InvalidParams).

## D10. Assets
internal/install/assets/agents/*.md: cambiar model: de IDs pinneados a aliases (architect→opus; backend/frontend/qa-tester/bug-hunter→sonnet). qa-tester opus→sonnet. (install sobrescribe igual, pero alinea el asset.)

## Orden de commits
1. feat(model): ErrUnknownAgent + ErrInvalidModel
2. feat(install): defaults.go (defaultAgentModels, knownAliases, BundledAgentNames, ResolveEffectiveModels) + test
3. feat(install): SetModelInFrontmatter quirúrgico + test I1-hardened
4. feat(config): ModelsConfig + SetModelsOverrides write-back + test
5. feat(install): ApplyAgentModels + CONSOLIDACIÓN installSteps/runInstallSteps (Install() + CLI consumen el builder) + parity test
6. feat(install): assets agents a aliases (qa-tester opus→sonnet)
7. feat(service): ModelsService List/Set/Reset + test
8. feat(mcp): model_list/set/reset (48→51) + handlers + test
9. feat(cli): grupo mneme model + root.go
10. docs(models): docs/models.md + CLAUDE.md + CHANGELOG v1.8.0

## Anti-scope
NO cross-provider/proxy/base-url. NO allowlist cerrada (alias desconocido warn). NO switching runtime per-task. NO tercer frontmatter writer (editor quirúrgico), I1 no reintroducido. Config override NO es asset, sobrevive upgrade. NO tocar allowlists/hooks/SDD/lane/skills/memory schema.
