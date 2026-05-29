# SPEC-041 — Implementation Report

**Process/Architecture Separation + Idempotent `init` (Finale)**
**Release:** v1.10.0 · **PR:** #11 (merge `3a4369f`) · **Base:** `fb6adc3` (v1.9.1)
**Lane:** standard · **Status:** done
**Date:** 2026-05-29

> El capstone del roadmap mneme-as-engine (SPEC-034..041). Este informe está orientado a **discusión de diseño**: documenta no solo lo que se construyó, sino *por qué*, qué supuestos de la spec se rompieron contra el código vivo, las dos decisiones del founder que reorientaron el diseño, los trade-offs aceptados y los puntos abiertos.

---

## 1. Resumen ejecutivo

SPEC-041 resuelve dos problemas estructurales sobre **dónde vive el conocimiento**:

1. **El conocimiento de proceso estaba enredado en cada `CLAUDE.md` per-repo** (cómo manejar mneme, política de lanes, delegación, modelos). Sin única fuente de verdad → derivaba (síntoma observado: rutas divergentes entre `wirvii360r` y `migratio`).
2. **No había forma limpia e idempotente de configurar un repo** separando arquitectura (per-repo, humano) de proceso (global, mneme-managed).

La solución es un **split limpio** apoyado en un primitive nuevo de *managed-block* determinista:

- **Proceso → global, mneme-managed:** un manual operativo conciso instalado en `~/.claude/CLAUDE.md` dentro de un bloque delimitado y versionado.
- **Arquitectura → per-repo, humano + skills:** `mneme init` gestiona un bloque mínimo en el `CLAUDE.md` del repo (puntero al manual global) y **reporta drift** sin tocar prosa humana.

Se incluyó además el **agente `diagnostician`** (ops/diagnóstico read-only), que viaja sobre la misma maquinaria de install/assets.

**Métricas:** 8 commits · 27 paquetes test ✅ · 0 races · 0 lint issues · tools MCP 56 → 57 · agentes bundled 5 → 6 · sin migración DB (schema v13).

---

## 2. La reconciliación rompió dos supuestos de la spec

El paso de reconciliación obligatorio (§4) no fue una formalidad: descubrió que **dos supuestos centrales de la spec congelada eran falsos contra el código vivo**. Esto es precisamente lo que la directiva "code wins over this spec" (§4) anticipaba.

### Supuesto roto #1 — "mneme no escribe a `~/.claude/CLAUDE.md`"

La spec §4.3 decía: *"SPEC-035 report said there is NO orchestrator-prompt asset; the root CLAUDE.md updated by specs is mneme's repo file, not the user-global one."*

**Realidad del código:** el step `"Protocol"` (`InjectProtocol` + `mergeProtocol`, `install.go:725-795`) **ya inyectaba** `protocol()` — un mini-manual de proceso (session lifecycle, save rules, SDD engine) — en `~/.claude/CLAUDE.md`, con markers `<!-- mneme:protocol:start/end -->`.

El operating manual de SPEC-041 (7 secciones) es un **superset** que solapa ese contenido. No reconocer esto habría producido **dos bloques mneme compitiendo** por el mismo concepto (proceso SDD), justo lo contrario del objetivo "single source of truth, no drift".

### Supuesto roto #2 — semántica de flags de `init`

La spec §5.4 especifica que `mneme init` **aplica por defecto** (managed blocks + drift report) y `--check` es el modo report.

**Realidad del código:** el `mneme init` existente es un **migrador legacy→SDD destructivo** (`DetectLegacy` → `Classify` → `Apply` con `rm -rf` de `.workflow/`, `.claude/specs/`, reescritura de `CLAUDE.local.md`). Su UX por seguridad es **dry-run por defecto, `--apply` para ejecutar**. La spec pedía la **inversión** de esa semántica — aplicar por defecto un comando que borra archivos sería peligroso.

### Por qué se escaló al founder en vez de adivinar

Ambos conflictos cambian el diseño central y tienen implicaciones de producto (afectan todos los repos del founder y la seguridad del migrador). La regla del proyecto — *"Bloqueado por ambigüedad → pushback, NUNCA adivines"* — aplica directamente. Se consultó vía `AskUserQuestion`.

---

## 3. Las dos decisiones del founder

### D-1: Protocol block → **reemplazar / subsumir**

El operating manual **absorbe** el protocol block. Un solo bloque mneme-owned, **versionado** (`<!-- mneme:managed:start v=N -->`), en `~/.claude/CLAUDE.md`. El contenido del viejo `protocol()` (lifecycle, save rules, SDD engine) se integra dentro de las 7 secciones.

**Consecuencias de diseño:**
- El primitive nuevo `upsertManagedBlock` **reemplaza** a `InjectProtocol`/`mergeProtocol`/`protocol()` y al step "Protocol".
- El step debe **migrar one-time**: si detecta el viejo bloque `mneme:protocol`, lo elimina e instala el `mneme:managed`.
- Esto también resolvió el pushback Q4 del architect ("¿extraer un `injectBlock` común o duplicar?"): no hay duplicación porque el primitive nuevo subsume al viejo.

**Alternativa descartada (coexistencia):** dejar `mneme:protocol` intacto y añadir un segundo bloque. Diff más pequeño, pero duplica contenido SDD y el propio drift detector se dispararía contra el solapamiento. Rechazada por contradecir el objetivo del split.

### D-2: `init` flags → **separar concerns**

Las operaciones **nuevas** de SPEC-041 (ensure manual, repo block, greenfield scaffold, drift report) son idempotentes/no-destructivas → **se aplican por defecto**, con `--check` para modo report. La migración legacy **destructiva** (`rm -rf`) **sigue exigiendo `--apply`**.

Un solo `mneme init`:
- **default:** aplica los managed blocks + imprime drift + muestra el plan de migración legacy en **dry-run**.
- **`--apply`:** ejecuta además la migración destructiva legacy (exit 2 en migración parcial — comportamiento preservado).
- **`--check`:** TODO en report, no escribe ni los blocks.

**Trade-off aceptado:** cumple SPEC-041 (aplicar por defecto lo seguro) sin volver el migrador default-destructivo. El costo es que un solo comando tiene dos "personalidades" (idempotente vs destructiva-bajo-flag), documentado en `docs/init.md`.

---

## 4. Arquitectura de la solución

### 4.1 El primitive: `upsertManagedBlock` (`internal/install/managedblock.go`)

El núcleo. Determinista, idempotente, genérico (usado para el manual global **y** el repo block).

```
upsertManagedBlock(filePath, content) error
readManagedBlock(filePath) (content, version, present, err)
```

- Markers versionados `<!-- mneme:managed:start v=N -->` … `<!-- mneme:managed:end -->`.
- **Presentes:** reemplaza todo entre markers (inclusive), refresca la versión. Byte-preserva todo lo de afuera.
- **Ausentes:** append (precedido de blank line) o crea archivo solo-bloque si no existe.
- **Legacy:** detecta y elimina `mneme:protocol` antes del upsert (migración one-time).
- **Idempotente:** correrlo 2× = archivo byte-idéntico.

**Por qué vive en `internal/install/`:** la inyección reutiliza el patrón probado de `mergeProtocol` y es conceptualmente un install step. No necesita `service` (no toca DB).

### 4.2 El manual operativo (`internal/install/assets/operating-manual.md`)

Embebido como asset, expuesto por `operatingManual()`. **Lean** por diseño (se carga en *cada* sesión): 7 secciones que **referencian** los docs detallados en vez de copiarlos.

> **Decisión clave anti-auto-drift:** el manual referencia los docs (`mneme skills lint`, `docs/lanes.md`, etc.) en vez de copiar sus headings literales. Si copiara los headings, el drift detector se dispararía contra el propio manual. Verificado por `TestDetectDrift_SkipsManagedBlock` (el detector además salta el rango del managed block).

Las 7 secciones: (1) How to launch (`--permission-mode acceptEdits`), (2) Roles (incl. diagnostician), (3) Delegation triggers (tabla gentle-ai), (4) SDD + lanes, (5) Skills, (6) Models, (7) Memory & conflicts.

### 4.3 Drift detection (`internal/service/drift.go`)

Heurístico **determinista, report-only, sin LLM** (consistente con el invariante de determinismo de SPEC-035/036/037).

```
DetectDrift(...) []DriftFinding   // {File, Line, Message}
```

Escanea el `CLAUDE.md` del repo **fuera** del managed block. Reporta dos categorías:
- **(a) Contenido de proceso duplicado:** headings que matchean secciones canónicas del manual/docs → *"duplicates global manual section X; consider removing (now global)"*.
- **(b) Instrucciones que contradicen enforcement:** frases anti-enforcement (el orquestador puede editar código, saltarse SDD) → *"contradicts enforcement (orchestrator cannot edit code since v1.4.0)"*.

Match de phrase/heading (lista mantenida, documentada en `docs/init.md` para que sea auditable y extensible). Exit 0 siempre (advisory). **Nunca edita ni borra** — verificado por `TestDetectDrift_NeverEditsFile` (byte-identidad).

**Filosofía:** falsos positivos son aceptables porque la salida es solo consejo; el humano decide. Esto es deliberado — preferimos report-only sobre auto-edición de prosa humana.

### 4.4 `mneme init` extendido (`internal/service/init.go` + `internal/cli/init.go`)

`InitServiceOptions` + cuatro métodos nuevos componiendo con el pipeline legacy sin romperlo:
`EnsureGlobalManual` · `UpsertRepoBlock` · `EnsureGreenfieldScaffold` · `RunDrift`.

- **Greenfield:** repo sin `CLAUDE.md` → crea con managed block + skeleton arquitectura-only (`## Stack`, `## Conventions`, `## Module structure`). Cero process content.
- **Brownfield:** upserta el repo block (puntero mínimo al manual global), nunca toca prosa humana fuera de markers, reporta drift.

### 4.5 El agente `diagnostician` (`internal/install/assets/agents/diagnostician.md`)

- `tools: Read, Grep, Glob, Bash, mcp__mneme__*`. **Sin** Edit/Write/MultiEdit/NotebookEdit. Sin bypassPermissions.
- `defaultAgentModels["diagnostician"] = sonnet`. Auto-descubierto por `BundledAgentNames()`.
- Rol: lee logs (GCP/kubectl), triage, diagnostica y **propone**; nunca muta código ni infra.
- **No** está en `IMPLEMENTER_AGENT_TYPES` (es read-only por capability).

> **Sutileza de enforcement:** el diagnostician *sí* tiene `Bash` (para leer logs), que **no** está cubierto por `enforce_delegation.sh`. Su carácter read-only depende 100% de la ausencia de Edit/Write en el `tools:` allowlist (layer 1, capability). Por eso el prompt del agente **prohíbe explícitamente** mutar infra/código. Punto a discutir: si en el futuro se quisiera blindar el Bash del diagnostician (p.ej. impedir `kubectl delete`), haría falta un layer adicional — hoy es confianza-en-el-prompt + read-only-en-código.

---

## 5. Trade-offs y decisiones notables

| Decisión | Trade-off aceptado |
|----------|--------------------|
| Manual subsume protocol block | Migración destructiva del viejo bloque para los usuarios existentes; su contenido custom *fuera* de markers se preserva, pero el viejo `mneme:protocol` desaparece (one-time, intencional). |
| Drift report-only, falsos positivos OK | No hay auto-limpieza; el humano debe actuar. Elegido sobre auto-edición de prosa (riesgo de destruir contexto humano). |
| `init` con dos personalidades | Un comando, dos UX (idempotente default vs destructiva con `--apply`). Documentado; preserva seguridad del migrador. |
| Manual referencia docs, no los copia | El manual puede quedar "desincronizado" de los docs si estos cambian; pero evita auto-drift y mantiene el manual lean. |
| diagnostician con Bash | Read-only depende del allowlist + prompt, no de un hook sobre Bash. |

---

## 6. Verificación y QA

**Orquestador (independiente):** `make build` ✅ · `make test` 27 paquetes ✅ · `make test-race` 0 races ✅ · `golangci-lint` 0 issues ✅.

**QA: APPROVED** con 2 menores, ambos cerrados antes del merge:
- **Gap-1 (importante):** `--check` sin test de integración CLI. Cerrado con `TestInitCheckMode_NoCWDSideEffects` a nivel service-injectable (el `RunE` bootstrapea `~/.mneme` real, no inyectable vía Cobra; testear ahí sería frágil). Verifica `RunDrift`+`Plan` read-only y byte-identidad del cwd.
- **Minor-1:** `ErrNotARepo` dead code (definido y mapeado, nunca retornado). Eliminado. No se añadió validación de repo (habría sido scope creep — `init` opera en cwd).

---

## 7. Commits

| SHA | Commit |
|-----|--------|
| `2ae0a9b` | feat(install): managed-block primitive with legacy protocol migration |
| `9aab72d` | feat(install): operating manual asset + InjectManual step |
| `d9f4557` | feat(install): diagnostician agent + default model |
| `3684c07` | feat(service): drift detection for repo CLAUDE.md |
| `eb9b63c` | feat(init,mcp): extended init command + MCP tool |
| `1e3e297` | docs: process/arch split docs + CHANGELOG v1.10.0 |
| `c866026` | refactor(model): remove unused ErrNotARepo sentinel |
| `039076d` | test(cli): verify --check mode leaves cwd byte-identical |

---

## 8. Puntos abiertos (para discusión de diseño)

1. **Migración destructiva del protocol block:** los usuarios existentes pierden el viejo `mneme:protocol` al primer `install` con v1.10.0. `TestCopyClaudeMD_PreservesManagedBlock` documenta que el viejo bloque ya no se preserva (intencional). ¿Conviene un aviso explícito en el output del install la primera vez?
2. **`--check` a nivel CLI:** sigue sin test end-to-end real (cubierto a nivel service). Gap conocido y aceptado; el bloqueo es la no-inyectabilidad de `initSDDService()`/`initService()` vía Cobra. ¿Vale refactorizar el constructor para inyección de servicios?
3. **Bash del diagnostician sin hook:** read-only depende del allowlist + prompt. ¿Se quiere un layer que restrinja comandos Bash mutadores de infra?
4. **Manual vs docs desincronizables:** el manual referencia los docs en vez de copiarlos (anti-auto-drift). Si los docs cambian de heading, el manual no se entera. ¿Algún check de consistencia manual↔docs?
5. **`docs/operating-manual.md` no se creó como doc separado:** el asset embebido `internal/install/assets/operating-manual.md` es la fuente (la spec lo permitía). Decisión de no duplicar.
6. **Pattern list de drift es estática:** vive como lista mantenida en código + documentada en `docs/init.md`. Extensible pero manual; podría derivarse de los headings de los docs automáticamente en el futuro.

---

## 9. Validación manual pendiente (founder, §8 de la spec)

1. `mneme upgrade` a v1.10.0 + `mneme install claude-code` + **reiniciar Claude Code** (nuevo tool `init` + agente `diagnostician`).
2. Inspeccionar `~/.claude/CLAUDE.md`: el viejo `mneme:protocol` debe estar **reemplazado** por el operating manual `mneme:managed`; el contenido custom fuera de markers, **intacto**.
3. Idempotencia: `mneme install claude-code` de nuevo → bloque byte-idéntico.
4. Greenfield: `mneme init` en repo temporal sin `CLAUDE.md` → crea managed block + skeleton arquitectura.
5. Brownfield (el test real): `mneme init --check` en una **copia** de `wirvii360r` → reporta drift, no escribe.
6. `mneme model list` → `diagnostician = sonnet`; dispatch del diagnostician a leer un log funciona, a editar código se bloquea.

---

**Fin del roadmap engine (SPEC-034..041).** Lo que resta es **contenido**: las skills Wirvii, autoradas aparte, derivadas del uso real.
