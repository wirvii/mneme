# SPEC-037 — Skills Framework

**Status:** Ready for implementation · **Target release:** v1.7.0
**Predecessors:** SPEC-034 (v1.4.0), SPEC-035 (v1.5.0), SPEC-036 (v1.6.0)

> **NOTA DE RECONCILIACIÓN (orquestador, 2026-05-29):** sondeo previo del install machinery confirma las suposiciones de la spec. Detalle en memoria `spec/SPEC-037-reconciliation` (la produce el architect). Hallazgos:
> - `internal/install/assets.go` embebe `assets/{agents,commands,templates}/*.md` como `embed.FS` + `assets/hooks/enforce_delegation.sh`. Helper `filesFromEmbed(fs, subdir, destDir) []CommandFile` (salta directorios). → skills: `//go:embed assets/skills` (árbol con subdirs); `installSkills()` espejo de la instalación de agents; manejar subdirectorios (scripts/, references/, validation/) y bit ejecutable.
> - **No hay dependencia yaml** (go.mod). SPEC-023/024 y `internal/install/commands.go` usan parser de frontmatter MANUAL. → `internal/skill` (leaf, sin import de internal/model) hace parser manual de frontmatter+secciones; sin dep nueva. pin/unpin reescribe el frontmatter del SKILL.md instalado.
> - CLI command-group (`internal/cli/root.go` registra) + MCP tools (`internal/mcp/tools.go`+`handlers.go`) + mapServiceError + patrón `IsError:true`-con-payload (SPEC-035) bien establecidos.
> - Sin forks de founder; decisiones restantes son de ingeniería (architect).

## 1. Objetivo
Entregar el FRAMEWORK de skills (cero contenido arquitectónico real). mneme = package manager + linter de skills de Claude Code (NO reimplementa runtime loading). Tras el release:
- mneme embebe skills y los instala a `~/.claude/skills/` (paralelo a agents/hooks).
- Un skill = directorio con `SKILL.md` (schema §5.2) + opcionales `scripts/`, `references/`, `validation/`.
- `mneme skills`: list/install/pin/unpin/remove/lint/validate, con paridad MCP.
- `lint` = chequeo estructural determinista. `validate` = corre `validation/run.sh`. `pinned` = protección overwrite/remove.
- `mneme install claude-code` despliega todos los skills embebidos (idempotente).
- CERO contenido Wirvii. Solo UN skill fixture: `example-skill`.

## 2. Anti-scope (§3, §10)
NO contenido arquitectónico real (solo example-skill fixture). NO auto-creación/auto-curación de skills (runtime loop Hermes rechazado). NO reimplementar el loading/progressive-disclosure de Claude Code. NO sourcing externo (GitHub/marketplace). NO publicar al Plugin Marketplace. NO tocar enforce_delegation.sh / allowlists / SDD state machine / lane auditor / memory schema. NO LLM en lint (chequeo determinista). `pinned` = SOLO protección overwrite/remove (sin hook integration, sin acople a la capa de capacidad SPEC-034).

## 4. Reconciliación-primero (OBLIGATORIO antes de código)
Primer entregable: `docs/mneme-internals-map.md` (architect, read-only → devuelve contenido, el orquestador lo escribe) cubriendo: (1) install machinery [primario], (2) frontmatter parsing [para SPEC-038 model:], (3) memory store [SPEC-039], (4) hooks/tokenizer [SPEC-040], (5) CLI+MCP registration. Reusable por 038/039/040. Más la memoria `spec/SPEC-037-reconciliation`.

## 5. Diseño

### 5.1 Contrato de directorio del skill
```
<skill-name>/
├── SKILL.md            # REQUIRED
├── scripts/            # OPTIONAL — ejecutables
├── references/         # OPTIONAL — docs on-demand
└── validation/run.sh   # OPTIONAL — exit 0 = pass; determinista
```
Solo SKILL.md es obligatorio. El directorio es la unidad de install/pin/remove.

### 5.2 Schema de SKILL.md
**Frontmatter YAML:** `name` (req; == nombre del dir; kebab-case `[a-z0-9-]+`), `description` (req; 1-3 frases con cues de *cuándo usar*; linter warn si >500 o <20 chars), `version` (req; semver), `pinned` (opt, default false), `license` (opt). Keys desconocidas permitidas (info).
**Body (markdown) — 5 secciones H2 obligatorias** (match case-insensitive por texto del heading, orden recomendado no forzado):
1. `## When to Use` — triggers explícitos.
2. `## Critical Rules` — reglas duras numeradas.
3. `## Automated Checks` — tabla markdown de EXACTAMENTE 3 columnas: **Check**, **What it verifies**, **How to fix**.
4. `## Verification` — (Hermes, obligatoria) cómo saber que ejecutó bien; referencia validation/run.sh si existe.
5. `## Workflow` — procedimiento numerado.
`## References` y `## Examples` opcionales. Faltar una sección obligatoria = lint ERROR; tabla Automated Checks malformada = lint ERROR.

### 5.3 Install machinery (espejo de agents)
- Subtree embebido `internal/install/assets/skills/<name>/...` vía `//go:embed`.
- `installSkills()` paralelo a la instalación de agents: copia cada dir de skill a `~/.claude/skills/<name>/`, preservando subdirs.
- `mneme install claude-code` llama installSkills junto con agents/hooks/commands.
- **Idempotencia + pin:** al reinstalar, un skill destino con `pinned: true` (según el SKILL.md INSTALADO) NO se sobrescribe salvo `--force`; el skip se loguea. Un skill no-pinned se sobrescribe (el bundled es autoritativo). Permite al founder pinear un skill editado localmente sin que `mneme upgrade` lo pise.
- `scripts/*.sh` y `validation/*.sh` con bit ejecutable (espejo de cómo hooks setean perms).

### 5.4 Comandos (CLI + MCP paridad)
| CLI | MCP | Behavior |
|---|---|---|
| `mneme skills list` | `skills_list` | bundled+installed: name, version, installed?, pinned?, lint status |
| `mneme skills install <name>` | `skills_install` | copia 1 bundled a ~/.claude/skills/; respeta pin |
| `mneme skills pin <name>` | `skills_pin` | set pinned:true en el SKILL.md instalado |
| `mneme skills unpin <name>` | `skills_unpin` | set pinned:false |
| `mneme skills remove <name>` | `skills_remove` | borra; rehúsa si pinned salvo --force |
| `mneme skills lint [<name>]` | `skills_lint` | valida 1/todos vs §5.2; determinista |
| `mneme skills validate <name>` | `skills_validate` | corre validation/run.sh; pass/fail+output |
- Operan sobre `~/.claude/skills/`; list refleja también bundled del embed FS.
- Sentinels nuevos: `ErrSkillNotFound`, `ErrSkillMalformed`, `ErrSkillPinned`, `ErrSkillNoValidation` en mapServiceError (not-found / invalid-params).
- MCP error: `skills_lint`/`skills_validate` con fallos → result estructurado + `IsError:true` (patrón lane_audit SPEC-035), NO error de protocolo. Skill no encontrado → mapServiceError normal.

### 5.5 pinned
SOLO protege: `remove` de pinned → ErrSkillPinned salvo --force; install/reinstall no sobrescribe pinned salvo --force. NADA más (sin hook, sin capa de capacidad).

### 5.6 validate runner
Si hay `validation/run.sh`: ejecutar con el dir del skill como cwd, capturar stdout/stderr+exit. 0=pass, !=0=fail. Sin run.sh → `ErrSkillNoValidation` (info, no fallo duro). NO interpreta el script, no LLM. Timeout ~120s (reusar helper exec-con-timeout si existe).

### 5.7 lint (determinista)
Frontmatter (campos req+formato §5.2.1; name==dir; version semver; description longitud sana). Body (5 secciones H2). Automated Checks = tabla 3 col headers exactos `Check`,`What it verifies`,`How to fix` (case-insensitive, trim). Output por skill: errores (block) + warnings/info. Exit !=0 / IsError si hay error. Go puro, sin ejecución de scripts, sin red, sin LLM.

### 5.8 example-skill (único fixture)
`internal/install/assets/skills/example-skill/`: SKILL.md mínimo conformante (5 secciones, tabla válida, version 0.0.1, pinned false) sobre tarea trivial falsa ("Example: how to greet") + `validation/run.sh` que exit 0. Comentario explícito: NO es guía arquitectónica.

## 6. File map (confirmar en reconciliación)
docs/mneme-internals-map.md (NEW), docs/skills.md (NEW, guía de autoría patrón gentle-ai), internal/install/assets/skills/example-skill/{SKILL.md,validation/run.sh} (NEW), internal/install/assets.go (embed skills), internal/install/install.go (installSkills + pin-aware), internal/install/install_test.go (EDIT), internal/skill/{parse,lint,validate}.go (NEW leaf, sin import model) + tests, internal/model/errors.go (4 sentinels), internal/service/skills.go (NEW)+test, internal/mcp/{tools,handlers}.go (7 tools + IsError-payload) + handlers_test, internal/cli/skills.go (NEW) + root.go, CLAUDE.md (sección Skills + conteo 41→48), CHANGELOG [v1.7.0].
Tool count: 41 + 7 = **48** (confirmar y actualizar docs).

## 7. Criterios de aceptación
Reconciliación: docs/mneme-internals-map.md cubre los 5 puntos; memoria spec/SPEC-037-reconciliation.
Schema/parsing: internal/skill parsea frontmatter+secciones determinista sin import model; lint flaggea (campo req faltante, name≠dir, version no-semver, sección faltante, tabla malformada); lint pasa el fixture.
Install: install claude-code despliega skills a ~/.claude/skills/ con subdirs+bit ejecutable; reinstall idempotente para no-pinned (bundled sobrescribe); pinned NO se sobrescribe sin --force (skip logueado).
Management: list (bundled+installed, version/installed/pinned/lint) CLI+MCP; install/pin/unpin/remove (remove rehúsa pinned sin --force); lint determinista; validate corre run.sh, ErrSkillNoValidation si ausente; paridad MCP/CLI los 7; lint/validate fallos → IsError:true result (no protocolo); not-found → mapServiceError.
Scope: solo example-skill (marcado fixture); sin auto-curación/sourcing externo/reimplementación runtime; enforce_delegation.sh/allowlists/SDD/lane auditor/memory schema intactos.
Calidad: make test + test-race; golangci-lint limpio (verificado por orquestador); docs+CLAUDE.md+CHANGELOG; conteo de tools correcto.

## 10. Anti-scope checklist
Internals map + reconciliación PRIMERO. Solo framework (example-skill fixture). NO auto-creación. NO reimplementar loading. NO sourcing externo/marketplace. pinned = solo overwrite/remove. lint = Go determinista, sin LLM ni ejecución. NO tocar hooks/allowlists/SDD/lane auditor/memory schema. Memorias `mneme save --type --title --content`, sin flags nuevos.
