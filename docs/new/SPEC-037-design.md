# SPEC-037 — Diseño técnico del architect (D1-D9)

> Autoritativo para el backend. Acompaña `docs/new/mneme-v1.7-spec-skills-framework.md`, `docs/mneme-internals-map.md` y la memoria `spec/SPEC-037-reconciliation`. Sin pushback. Orden de commits al final.

## D1. Asset subtree + embed + example-skill fixture
- `internal/install/assets.go`: añadir `//go:embed assets/skills` → `var builtinSkills embed.FS` (incluye árbol recursivo; sin `all:`). Imports `io/fs`, `strings`.
- Helpers nuevos en assets.go:
  - `type SkillEntry struct { RelPath string; Content []byte; IsExecutable bool }`
  - `BundledSkillEntries() ([]SkillEntry, error)`: `fs.WalkDir(builtinSkills, "assets/skills", ...)`, salta dirs, RelPath = `filepath.Rel("assets/skills", p)`, IsExecutable = `.sh` || path contiene `/scripts/` || `/validation/`.
  - `BundledSkillNames() ([]string, error)`: `builtinSkills.ReadDir("assets/skills")`, dirs.
- `internal/install/assets/skills/example-skill/SKILL.md`: fixture conformante (frontmatter name=example-skill, description con cue, version 0.0.1, pinned false; 5 secciones H2: When to Use, Critical Rules, Automated Checks [tabla 3-col Check|What it verifies|How to fix], Verification, Workflow; comentario "fixture, NO guía arquitectónica").
- `internal/install/assets/skills/example-skill/validation/run.sh`: `#!/bin/sh\necho "example-skill: validation passed"\nexit 0`.

## D2. `internal/skill/` (leaf, sin import internal/model)
- `parse.go`: `Metadata{Name,Description,Version string; Pinned bool; License string; Extra map[string]string}`, `Section{Heading,Content string}`, `Skill{Metadata; Sections []Section; RawBody string}`. `ParseFile(path)`, `Parse(data)`, `parseFrontmatter(lines)` (patrón vault, `---` delimitado, switch por key, pinned via strconv.ParseBool, desconocidas→Extra), `parseSections(body)` (H2). `WriteFrontmatter(m Metadata) []byte` (orden fijo determinista) + `RewritePinned(data []byte, pinned bool) ([]byte, error)` (preserva body).
- `lint.go`: `Severity` (Error/Warning/Info), `Finding{Severity,Message}`, `LintResult{Name string; Errors,Warnings,Infos []Finding; Passed bool}`. `requiredSections=[When to Use, Critical Rules, Automated Checks, Verification, Workflow]`, `automatedChecksHeaders=[Check, What it verifies, How to fix]`. `Lint(s *Skill, dirName string) LintResult`, `LintFile(path, dirName)`. Errores: name/description/version vacíos; version no semver (`^\d+\.\d+\.\d+`); name≠dirName; falta sección (match case-insensitive); tabla Automated Checks ausente o headers ≠ los 3 (case-insensitive, trim). Warning: description <20 o >500. Info: keys desconocidas. Determinista, sin ejecución, sin LLM.
- `validate.go`: `ValidateResult{Passed bool; Output string; ExitCode int}`, `Validate(ctx, skillDir) (*ValidateResult, error)`: si no hay validation/run.sh → `ErrNoValidation` (sentinel del pkg, NO de model). `context.WithTimeout(ctx,120s)` + `exec.CommandContext(ctx,"sh","validation/run.sh")` con `cmd.Dir=skillDir`, `CombinedOutput()`, exit code de `*exec.ExitError`.

## D3. `internal/model/errors.go` — 4 sentinels
`ErrSkillNotFound`, `ErrSkillMalformed`, `ErrSkillPinned`, `ErrSkillNoValidation`.

## D4. Install: installSkills pin-aware
- `Agent` struct (install.go): campo `Skills func() ([]SkillEntry, error)`. En `ClaudeCode()`: `Skills: BundledSkillEntries`.
- `WriteSkills(agent *Agent, force bool) (*SkillsResult, error)` + `SkillsResult{Installed,Skipped []string}`: agrupa entries por nombre top-level (primer componente de RelPath); si `~/.claude/skills/<name>/SKILL.md` existe y `pinned:true` (parse) y !force → skip+log; si no, escribe cada archivo (`os.MkdirAll` parent, `os.WriteFile` 0o644 o 0o755 si IsExecutable).
- Wire en `Install()` (tras WriteTemplates, antes de DelegationHook), `DryRun()`, y `internal/cli/install.go` (imprime [ok]/[skip]).

## D5. `internal/service/skills.go`
`SkillsService{skillsDir string}` (filesystem-only, sin store). `NewSkillsService(skillsDir)`. `SkillInfo{Name,Version string; Installed,Pinned,Bundled,LintOK bool}`. Métodos: `List() ([]SkillInfo,error)` (bundled de embed + installed de ~/.claude/skills, merge), `Install(name string, force bool) error`, `Pin(name)`, `Unpin(name)`, `Remove(name, force)`, `Lint(name string) ([]skill.LintResult,error)` (vacío=todos installed), `Validate(ctx, name) (*skill.ValidateResult,error)`. Mapeo de errores: `skill.ErrNoValidation`→`model.ErrSkillNoValidation`; dir inexistente→`model.ErrSkillNotFound`; pinned sin force→`model.ErrSkillPinned`. Pin/Unpin: lee SKILL.md, `skill.RewritePinned`, escribe.

## D6. MCP: 7 tools + handlers
- tools.go: bloque SKILLS TOOLS: skills_list (sin req), skills_install (req name; opt force), skills_pin/unpin/remove (req name; remove opt force), skills_lint (opt name), skills_validate (req name). Descripciones notan IsError en fallo para lint/validate.
- handlers.go: campo `skillsSvc *service.SkillsService` en struct handlers + `newHandlers`. 7 cases en handleToolCall. handleSkillsLint/Validate: si hay errores/`!Passed` → marshal result + `ToolCallResult{IsError:true}`; si no → resultFromAny. Resto patrón estándar. `skillsUnavailable` helper si skillsSvc nil. mapServiceError: ErrSkillNotFound→CodeMemoryNotFound; ErrSkillMalformed/Pinned/NoValidation→CodeInvalidParams.
- server.go: campo skillsSvc + parámetro en NewServer + pasar a newHandlers.
- Conteo 41→48.

## D7. CLI: `internal/cli/skills.go`
`newSkillsCmd()` (parent `skills`) + 7 subcomandos (patrón internal/cli/lane.go), cada uno construye `service.NewSkillsService(filepath.Join(home,".claude","skills"))` inline. Flags: --json (list/lint/validate), --force (install/remove). lint/validate exit 1 si error/fail. Registrar `newSkillsCmd()` en root.go.

## D8. Docs
- `docs/skills.md` (NEW): guía de autoría (patrón gentle-ai), contrato de directorio, schema SKILL.md con ejemplos, validation scripts, pinning, lifecycle.
- CLAUDE.md: línea MCP "48 tools (14 mem_*, 4 backlog_*, 8 spec_*, 5 lane_*, 10 codegraph_*, 7 skills_*)"; sección Skills tras Lanes; CLI "25 top-level commands".
- CHANGELOG: `[v1.7.0]`.

## D9. Tests
internal/skill: parse_test (conformante, campos faltantes, sin delimitadores, RewritePinned round-trip), lint_test (fixture pasa + cada violación + combinado), validate_test (run.sh pass exit0, fail exit1, sin validation→ErrNoValidation, timeout). internal/install/skills_test (WriteSkills: subdirs+exec bits, idempotente no-pinned, pin-skip, force). internal/service/skills_test (List, Install ok/not-found/pinned, Pin/Unpin, Remove ok/pinned/force, Lint, Validate). internal/mcp handlers (7 tools, lint/validate pass+fail con IsError, not-found→mapServiceError). Filesystem real (temp dirs), sin mocks.

## Orden de commits (cada uno compila)
1. feat(model): skill sentinel errors (D3)
2. feat(install): embed skills asset subtree + example-skill fixture (D1)
3. feat(skill): parse/lint/validate package leaf (D2)
4. feat(install): installSkills pin-aware (D4)
5. feat(service): SkillsService (D5)
6. feat(mcp): 7 skills_* tools + IsError-payload (D6)
7. feat(cli): mneme skills command group (D7)
8. test(skill): table-driven tests parse/lint/validate/install/mcp (D9)
9. docs: skills.md + CLAUDE.md + CHANGELOG v1.7.0 (D8)

## Anti-scope
Solo framework + example-skill fixture. NO contenido real. NO auto-curación. NO reimplementar loading de Claude Code. NO sourcing externo. pinned = solo overwrite/remove. lint Go determinista sin LLM ni ejecución. internal/skill leaf sin import model. Sin dep yaml. NO tocar enforce_delegation.sh/allowlists/SDD/lane auditor/memory schema.
