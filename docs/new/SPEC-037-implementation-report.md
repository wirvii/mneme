# SPEC-037 — Skills Framework · Informe de implementación

> **Para:** agente de discusión de diseño.
> **Estado:** ✅ Implementado, mergeado (PR #7 → `main`, `c713eae`) y liberado como **v1.7.0** (4 assets, workflow limpio).
> **Origen:** `docs/new/mneme-v1.7-spec-skills-framework.md` (reconciliada) + `docs/new/SPEC-037-design.md` (D1-D9) + `docs/mneme-internals-map.md` (entregable §4).
> **Predecesores:** SPEC-034..036. **Fecha:** 2026-05-29.

---

## 1. Qué es (y qué NO es)

Es el **framework** de skills: mneme como **package manager + linter** de skills de Claude Code. **No** reimplementa el runtime loading / progressive disclosure — eso lo hace Claude Code nativamente. mneme: embebe skills, los instala a `~/.claude/skills/`, los gestiona (list/install/pin/unpin/remove) y hace cumplir que estén bien formados (`lint`) y, si traen casos, verificados (`validate`).

**Cero contenido arquitectónico real.** El release trae UN solo skill: `example-skill`, marcado explícitamente como fixture de tests, no guía. El contenido real (formularios Conform+Zod, usecases Go, etc.) se autorea después, derivado de errores reales — fuera de scope aquí por diseño.

## 2. Reconciliación (entregable §4: `docs/mneme-internals-map.md`)

La spec exigía reconciliación-primero porque toca el install machinery (el subsistema menos entendido). Se produjo `docs/mneme-internals-map.md` (reusable por SPEC-038/039/040) + memoria `spec/SPEC-037-reconciliation`. Hallazgos clave que moldearon el diseño:

- **`//go:embed assets/skills`** (patrón de directorio) embebe el árbol recursivo completo (excepto `.`/`_`); NO requiere prefijo `all:`.
- **`filesFromEmbed` es PLANO** (salta directorios) → no sirve para skills con subdirs; se usó `fs.WalkDir` sobre el embed.FS.
- **No hay dependencia yaml** (decisión recurrente desde SPEC-023). El parser de frontmatter de vault importa `model`; como `internal/skill` debe ser **leaf**, se replicó el patrón manual (`---` delimitado, switch por key) sin dep nueva.
- **No hay helper exec-con-timeout** → `validate.go` crea su propio `context.WithTimeout(120s)` + `exec.CommandContext` inline.
- **Bit ejecutable** se setea al escribir (`os.WriteFile(.., 0o755)`); embed.FS no preserva perms, así que se detecta por extensión `.sh` / path `scripts/`|`validation/`.
- Sin forks de founder.

## 3. Diseño implementado

### 3.1 Contrato y schema
Skill = directorio con `SKILL.md` (req) + opcionales `scripts/`, `references/`, `validation/run.sh`. Frontmatter: `name` (==dir, kebab-case), `description`, `version` (semver), `pinned` (default false), `license`. Body: 5 secciones H2 obligatorias — `When to Use`, `Critical Rules`, `Automated Checks` (tabla 3-col exactos: Check | What it verifies | How to fix), `Verification`, `Workflow`.

### 3.2 `internal/skill/` (paquete leaf, sin import de model, sin yaml)
- `parse.go` — frontmatter + secciones; `WriteFrontmatter`/`RewritePinned` round-trip-safe.
- `lint.go` — chequeo **determinista Go puro** (sin LLM, sin ejecutar scripts, sin red): campos req, name==dir, semver anclado, las 5 secciones, headers exactos de la tabla. Errores (block) / warnings / infos.
- `validate.go` — corre `validation/run.sh` con timeout 120s; `ErrNoValidation` si ausente.

### 3.3 Install (espejo de agents, pin-aware)
`WriteSkills` agrupa entries por skill, **no sobrescribe un skill instalado `pinned:true`** salvo `--force` (skip logueado), preserva subdirs y bit ejecutable. Wired en `install.Install()` **y** en el CLI `mneme install claude-code` (este último fue justo el bug C1 que QA atrapó — ver §4).

### 3.4 Superficie (MCP + CLI paridad)
7 tools nuevos: `skills_list/install/pin/unpin/remove/lint/validate` + grupo CLI `mneme skills`. Tools MCP: 41 → **48**. `skills_lint`/`skills_validate` en fallo devuelven result estructurado con `IsError:true` (patrón SPEC-035, no error de protocolo); skill no encontrado → `mapServiceError`. `SkillsService` es **filesystem-only** (sin SQLite, sin store) — un patrón nuevo en mneme.

### 3.5 `pinned` — alcance deliberadamente modesto
Solo protege contra overwrite (en reinstall) y remove. NO se acopla a `enforce_delegation.sh` ni a la capa de capacidad de SPEC-034. Como mneme no tiene loop de auto-curación, no hay nada más contra qué proteger.

## 4. Proceso SDD (con rebote de QA real y valioso)

| Fase | Resultado |
|---|---|
| Reconciliación | internals map + memoria; sin forks de founder |
| Architect | D1-D9, sin pushback (no pudo guardar la memoria por sí mismo — read-only; la guardó el orquestador) |
| Backend r1 | 10 commits, verdes |
| **QA r1** | **REJECTED** — 2 bloqueantes + 2 menores |
| Backend r2 | 3 commits de fix |
| Re-QA | **APPROVED** (4/4, sin regresión) |
| Verificación propia | build OK · test 26/26 · race 26/26 · golangci-lint 0 issues |
| Release | PR #7 → main (`c713eae`), tag v1.7.0, 4 assets |

### Los bloqueantes que QA atrapó (valiosos para la discusión)
- **C1 (crítico):** el backend wired `WriteSkills` en `install.Install()` (la función-librería que usa `mneme upgrade`) pero **NO** en el comando CLI `mneme install claude-code`, que secuencia los pasos manualmente. Resultado: el path de instalación primario no desplegaba skills. QA lo detectó por grep (cero referencias a WriteSkills en `cli/install.go`). Esto expone una **deuda estructural**: hay dos rutas de instalación (la función `Install()` y el CLI que re-secuencia a mano) que pueden divergir. Ver §6.
- **I1 (importante):** `RewritePinned` corrompía la `description` en cada ciclo: `WriteFrontmatter` usaba `strconv.Quote` pero `parseFrontmatter` no des-comillaba → comillas acumulándose. El test de round-trip original solo verificaba `Pinned` y el conteo de secciones, así que el bug estaba oculto. Fix: escribir verbatim + des-comillar en parse, y reforzar el test con descripción con caracteres especiales × 3 ciclos.

## 5. Commits (13) y release
Branch `feat/spec-037-skills-framework`: `1bdb1f1` model errors · `79ee8f2` embed+fixture · `25f4fb1` skill pkg · `c2c4285` installSkills · `b019710` service · `d017cc5` mcp · `a612248` cli · `665ba0d` tests · `9779d2e` docs · `50b9380` lint fix · `19de0ce` fix C1 · `205ef4b` fix I1 · `76ba081` fix M1+M2.
PR #7 → `c713eae`. Tag v1.7.0 → `mneme-1.7.0-{darwin-arm64,linux-amd64}.tar.gz` + `.sha256`.

## 6. Puntos abiertos para la discusión de diseño

1. **Dos rutas de instalación divergentes (deuda estructural).** `install.Install()` (librería, usada por `upgrade`) re-implementa la secuencia de pasos, y `mneme install claude-code` (CLI) la re-secuencia a mano. C1 ocurrió justo porque un paso nuevo se añadió a una y no a la otra. **Recomendación fuerte para una spec futura:** que el CLI `install` delegue en `install.Install()` (o que ambos compartan una única lista de pasos), eliminando la posibilidad de divergencia. Esto NO se arregló aquí (fuera de scope) — solo se parchó añadiendo el paso a ambas.

2. **`SkillsService` filesystem-only rompe el patrón de capas.** Todos los servicios previos (Memory, SDD) van sobre un store/DB. Skills opera directo sobre `~/.claude/skills/`. Es correcto (los skills son archivos que Claude Code lee, no estado de mneme), pero significa que `mneme skills list` no tiene noción de proyecto ni de versionado histórico — es un reflejo del filesystem. ¿Suficiente, o el catálogo de skills debería persistirse?

3. **`lint` valida estructura, no contenido.** Por diseño (determinista, sin LLM). Un SKILL.md con las 5 secciones presentes pero vacías o sin sentido pasa el lint. La calidad del *contenido* del skill no es verificable deterministamente — queda en manos del autor y del `validate` (si trae casos). Es el límite intencional del enfoque, igual que el auditor de lanes (SPEC-035) no juzga semántica.

4. **`pinned` se lee del SKILL.md *instalado*.** Si un usuario edita a mano el SKILL.md instalado y rompe el frontmatter, el chequeo de pin podría fallar al parsear. Hoy se maneja con tolerancia (parse error → tratado como no-pinned), pero un SKILL.md instalado corrupto es un estado posible que la UX podría señalar mejor.

5. **El validate corre `sh validation/run.sh` con la confianza del autor.** No hay sandbox; el script corre con los permisos del usuario. Para skills bundled/locales es aceptable (el founder los autorea), pero si alguna vez se añade sourcing externo (explícitamente diferido), `validate` se vuelve un vector de ejecución de código arbitrario. A tener en cuenta antes de abrir esa puerta.

6. **El architect no pudo guardar su propia memoria de reconciliación** (sus tools MCP no estaban disponibles en esa invocación, pese a tener `mcp__mneme__*` en el allowlist). El orquestador la guardó. Vale revisar por qué los subagentes architect/qa no siempre ven los tools mneme — afecta el patrón "el architect guarda decisiones".

## 7. Archivos (referencia)
```
docs/mneme-internals-map.md (NEW, reusable), docs/skills.md (NEW), docs/new/SPEC-037-{design,implementation-report}.md
internal/install/assets/skills/example-skill/{SKILL.md,validation/run.sh} (NEW fixture)
internal/install/{assets,install,skills}.go (embed árbol, WriteSkills pin-aware) + skills_test.go
internal/skill/{parse,lint,validate}.go (NEW leaf) + *_test.go
internal/service/skills.go (NEW, filesystem-only) + test
internal/mcp/{tools,handlers,server}.go (7 tools, IsError-payload) + handlers_skills_test.go
internal/cli/{skills.go(NEW),install.go,mcp.go,root.go}
internal/model/errors.go (4 sentinels), CLAUDE.md (48 tools, 30 commands, Skills), CHANGELOG [v1.7.0]
```

> **Operativo:** `mneme upgrade` a v1.7.0 + **reiniciar Claude Code** (7 tools MCP nuevos + comando `mneme skills`). Tras el upgrade, `~/.claude/skills/example-skill/` debería desplegarse; verifícalo con `mneme skills list`.
