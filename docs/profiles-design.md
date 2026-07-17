# mneme Profiles — Documento de diseño

> Estado: **diseño aprobado** (grill 2026-07-16). Aún no implementado.
> Descomposición y ejecución viven en el SDD de mneme (ver EPIC `profiles`).
> Este documento es la referencia legible del diseño; la fuente de verdad de
> ejecución son las specs.

## 1. Motivación

mneme se volvió la herramienta central del equipo de Chatea Pro. Queremos darle
ventajas al equipo —lineamientos, arquitectura de referencia, roles de
subagentes, scaffolding estandarizado, estilo de código, convenciones de
commits/PRs/ramas, templates de specs— **sin dejar de ser open source**.

La tensión de fondo: cómo empaquetar "cómo trabaja un equipo" de forma portable
y activable por proyecto o global, dando ventaja competitiva a Chatea Pro, sin
que el contenido privado del equipo tenga que vivir en el repo OSS.

**La respuesta:** el motor de profiles es OSS; el *contenido* del profile de
Chatea Pro vive en un repo git privado. La ventaja de equipo es **contenido**,
no código cerrado.

## 2. Concepto

Un **profile** es la *metodología de trabajo de un equipo* empaquetada como
**repo git portable**, activable con semántica **nvm** (Node Version Manager):
un store instalado una sola vez + punteros con precedencia.

mneme **compone** primitivas que ya tiene (agents, skills, rules, managed
blocks, models, templates); no inventa formatos nuevos.

### El mapeo con nvm

| nvm | mneme profiles |
|-----|----------------|
| `~/.nvm/versions/node/*` — cada versión instalada 1 vez, compartida | `~/.mneme/profiles/<name>/` — cada profile clonado 1 vez, compartido por todos los proyectos |
| `nvm alias default <v>` — default global | `mneme profile default <name>` — default global en `~/.mneme/config.toml` |
| `.nvmrc` en el proyecto | `.mneme-profile` en la raíz del repo (commiteado) |
| shell hook en `cd` auto-aplica | el SessionStart hook (ya existe) lee `.mneme-profile` y activa |
| `nvm use <v>` — solo el shell actual | `mneme profile use <name>` — solo este repo/sesión |
| `npm install` — baja `node_modules` (derivado, gitignored) | materialización en SessionStart — `.claude/agents/*` (derivado, gitignored) |

## 3. Anatomía de un profile (repo git autocontenido)

El repo del profile **es la fuente de verdad** y carga todo adentro. mneme solo
lo lee y materializa.

```
mi-profile/
  mneme-profile.toml        # manifiesto: name, version, description, compat mneme, extends (reservado)
  agents/                   # capa-1 estándar del rol: <role>.md (rol, tools, enforcement, modelo)
  skills/<name>/            # skills completas (incluida /new-project y /new-app)
  rules.jsonl               # rules como datos (commits/PRs/ramas/convenciones)
  blocks/*.md               # managed CLAUDE.md blocks del orquestador
  models.toml               # asignación de modelo por agente
  templates/                # entregables de spec_doc_write
    spec.md
    plan.md
    qa-report.md
  scaffolds/                # arquetipos de proyecto (ver §15)
    _blueprints/            # blueprints de app (solo los consume layout=monorepo)
      go-core-srv/  next-web-ui/  expo-mobile/  hono-renderer/  packages-go/
    saas-multitenant/       # layout = monorepo (componible, soporta /new-app)
      scaffold.toml         # layout + shell + blueprints ofrecidos + defaults + sustituciones
      shell/                # raíz del monorepo (turbo.json, pnpm-workspace.yaml...)
    go-microservice/        # layout = single (skeleton plano, sin /new-app)
      scaffold.toml
      skeleton/             # esqueleto parametrizable copy+substitute
  policy.toml               # enforcement (subagent_containment) + política de lanes/SDD
```

## 4. El pin (`.mneme-profile`)

Vive en la **raíz del repo del proyecto** y se **commitea** (como `package.json`).
Es **auto-describible** para que un dev fresco pueda auto-bootstrapear:

```toml
# .mneme-profile
name     = "chatea-pro"
source   = "git@github.com:chateapro/mneme-profile.git"   # de dónde clonar (privado)
ref      = "v3"                                            # tag/commit pinneado
scaffold = "saas-multitenant"                             # opcional: arquetipo con que nació (ver §15)
```

Sin `source` → mneme resuelve el **default profile** de sus assets internos (el
caso OSS, sin URL). El acceso al repo privado lo resuelven las credenciales git
que el dev ya tiene. El campo `scaffold` solo existe si el repo nació de
`/new-project`; un repo onboardeado con `mneme-init` no lo tiene.

## 5. Precedencia (nvm puro, reemplazo total)

```
pin del repo (.mneme-profile)  >  default global (config.toml)  >  mneme vanilla
```

- Un solo profile activo a la vez. El pin del proyecto **reemplaza 100%** al
  global; nunca hay merge.
- **Sin herencia en v1.** El manifiesto reserva `extends` para no cerrarnos la
  puerta, pero no se implementa hasta que el dolor de duplicación lo justifique
  (YAGNI). Si un proyecto necesita casi-lo-mismo-con-un-cambio, clona el profile.

## 6. Los dos verbos (columna vertebral de la consistencia)

Copiados literal de nvm, que nunca los confunde:

| Verbo | Semántica | Análogo nvm |
|-------|-----------|-------------|
| `mneme profile use <B>` | "para ESTE repo/sesión" — escribe el pin `.mneme-profile` | `nvm use` |
| `mneme profile default <B>` | "default global para sesiones FUTURAS" | `nvm alias default` |

**Regla de oro:** el default global se lee **una sola vez, en SessionStart** — no
es live. Cambiar el default desde otra sesión es, por definición, "para la
próxima". Una sesión ya abierta nunca se le cambia el profile por debajo.

## 7. Activación híbrida + lockfile

Claude Code exige archivos reales (no tiene la capa de indirección de `PATH`),
así que activar es **híbrido**:

- **Repunta** lo que mneme resuelve en runtime: rules → DB, modelos, policy,
  templates.
- **Materializa** lo file-based: `agents/*` → `.claude/agents/`, skills →
  `~/.claude/skills/`, blocks → `CLAUDE.md`.
- Escribe **`.mneme/profile.lock`** (gitignored) con: profile + git ref +
  timestamp + lista EXACTA de artefactos materializados (rutas de archivos + IDs
  de memorias/rules insertadas, con su marca de proveniencia).

**Switch A → B:** leer el lock, remover *solo* lo que registró A, materializar B,
reescribir el lock. Lo **hand-authored** (fuera del lock) nunca se toca.

### Materialización de un agent = capa-1 + capa-2/3

El archivo final `.claude/agents/<role>.md` es **derivado de dos fuentes**:

- **Capa-1** (estándar del equipo): del profile store.
- **Capa-2/3** (áreas del repo, app→role, conocimiento específico): del
  `subagent_profile` del repo (ya existe: `subagent_profile_get/save`), que se
  comparte como hand-authored por el vault de team-memory.

El profile aporta el estándar; `mneme-init` aporta lo del repo; el archivo final
es gitignored y regenerable.

## 8. Qué se commitea vs qué es derivado

Analogía exacta: `package.json` (commiteado) + `node_modules` (gitignored).

| Artefacto | Git |
|-----------|-----|
| `.mneme-profile` (el pin) | **commiteado** (fuente de verdad de qué profile usa el repo) |
| `.claude/agents/*`, managed blocks | **gitignored** (derivados, se regeneran en SessionStart) |
| `.mneme/profile.lock` | **gitignored** (estado de materialización local, específico de máquina — lección de SPEC-089: no commitear estado local) |

**Ganancias:** cero churn en git al switchear/actualizar; una sola fuente de
verdad (el repo del profile); update trivial para todo el equipo
(`mneme profile update` + reiniciar sesión, sin merge conflicts en `.claude/`).

**Costo aceptado:** todo dev necesita mneme + el profile instalado (en Chatea
Pro es un hecho). Un dev sin mneme no tiene los agents (el default profile OSS
cubre el caso genérico). El review de cambios al rol se hace en el **repo del
profile** (un PR cambia el estándar para los 13 repos, en vez de 13 PRs).

## 9. Consistencia entre sesiones

- **Sesiones en repos distintos:** aisladas por diseño (materialización
  per-proyecto).
- **Dos sesiones en el MISMO repo, una switchea:** el `profile.lock` graba el
  ref; la otra sesión detecta staleness en su siguiente operación mneme y
  **avisa** ("el profile de este workspace cambió a B; reinicia para
  sincronizar"). Nunca re-materializa a ciegas.

## 10. SessionStart: gate accionable

El SessionStart hook es no-interactivo, pero no es un callejón sin salida:

1. Lee el pin → consulta el store.
2. ¿Profile en el store? → **materializa** y listo.
3. ¿No está? → emite un **nudge accionable** → el agente en sesión ofrece un
   **gate**: *"Este repo usa `chatea-pro@v3`, no instalado. ¿Lo instalo ahora?
   (clona `git@...`, ref `v3`)"*. Confirmas → `mneme profile add` + materializa.
   Declinas → vanilla y el nudge queda.

Nunca clona sin OK explícito (los prompts de auth git ocurren con el dev mirando,
en sesión), pero sin obligar a cambiar de contexto a otra terminal.

## 11. Profile vs team-memory: dos canales ortogonales

**Colisión latente detectada en el grill:** con share-by-default (SPEC-071), las
rules son un tipo project-scoped que auto-comparte al vault (`shared=1`). Sin
cuidado, las rules del profile se auto-compartirían al vault → se commitearían →
contradicen el modelo derivado, duplican la fuente de verdad, y crean **rules
zombie** (al switchear, `Forget` borra de la DB pero el vault re-importa en el
siguiente `git pull`).

**Resolución — proveniencia:** cada rule/memoria que inyecta el profile lleva la
marca `source=profile:<name>`. Es la MISMA marca que usa el lockfile. Un solo
concepto sirve para tres cosas:

1. **Switch limpio:** `Forget` solo lo marcado `profile:*`.
2. **Proteger hand-authored:** sin marca = del dev = intocable.
3. **Excluir del vault:** cualquier memoria con proveniencia `profile:*` se
   fuerza a `shared=0`.

| Canal | Qué lleva | Cómo se comparte |
|-------|-----------|------------------|
| **Repo del profile** | metodología/estándar: rules, agents, skills, templates | git del profile |
| **Vault team-memory** | conocimiento acumulado de ESTE repo: decisiones, discoveries, bugfixes | git del proyecto (`.mneme/shared/`) |

Una rule del profile vive solo en la DB local (derivada, regenerable). Una rule
que el dev crea a mano (`mneme rule add`) sí auto-comparte al vault como hoy.

## 12. Rules: reutilización del borrado existente

Una rule no es una tabla aparte: es una **memoria de tipo `rule`**
(`internal/model/memory.go`, `TypeRule = "rule"`; no decae). `mneme rule` solo
tiene `add/list/test` — **no** hay `rule remove`.

> **Corrección SPEC-092 + decisión del owner (2026-07-16):** el diseño original
> decía "el switch reusa `Forget()`". Es **mecánicamente falso**:
> `MemoryService.Forget` fija `decay_rate=1.0` (NO borra), y `loadActiveRules`
> (`context.go`) carga rules vía `store.List` filtrando `deleted_at IS NULL`
> **ignorando la importancia** → una rule "forgotten" se sigue inyectando. El
> @architect lo movió a `SoftDelete`; el **owner pidió ir más lejos: un delete
> real, no decay ni tumba.** Decisión final: la desactivación de rules del
> profile es un **hard delete por proveniencia** (borra la fila
> `WHERE source='profile:<name>'`), seguro porque las rules del profile son
> **derivadas/regenerables** desde el store (re-materializables si vuelves al
> profile). `Forget`/soft-delete queda **solo** para memorias hand-authored
> (recuperables). Hay test de regresión que demuestra la insuficiencia de
> `Forget` y verifica el hard delete.

El switch de profile hace **hard delete por proveniencia** de las rules del
profile; `Forget` (soft/decay) NO se usa para esto. Las rules del profile se
insertan **project-scoped** en la DB del repo activo (no global), para que un
switch en un repo no altere a otro.

## 13. Perímetro

**DENTRO del profile:** agents capa-1 · skills · rules (commits/PRs/ramas) ·
managed blocks · modelos · skill de scaffolding · config de enforcement
(`subagent_containment`) · política de lanes/SDD · templates spec/plan/qa.

**FUERA (infra per-repo/host):** codegraph · team-memory · config MCP · hooks
base. *(Son plomería que depende del host/lenguaje, no del estilo del equipo.)*

## 14. Entregable open source

- **OSS (mneme):** el motor completo (comandos `profile *`, activación, lockfile,
  precedencia, proveniencia) + **1 "default profile"** = los assets actuales de
  mneme migrados a formato profile. El OSS sigue funcionando igual que hoy.
- **Privado (Chatea Pro):** un repo git con SUS agents/skills/rules/templates
  premium. mneme nunca los embebe.

## 15. Scaffolding de proyectos

El scaffolding es una pieza del perímetro del profile (§13). Sigue el mismo
patrón de dos mitades que la creación de profiles.

### 15.1 Dos mitades

- **Skills `/new-project` y `/new-app`** (grill) → conversan, elicitan las
  decisiones del proyecto/app nuevo.
- **Comandos `mneme` deterministas** (`mneme project new`, `mneme app add`) →
  copian el esqueleto, sustituyen variables, corren `git init` / `pnpm install`.

El skill decide *qué*; el comando ejecuta *cómo*. Lo determinista no es LLM.

### 15.2 Vive en el profile

El template + los skills de scaffolding viajan **dentro** del repo del profile.
`/new-project` produce el stack estándar de ESE equipo. El profile de Chatea Pro
carga el esqueleto monorepo Turborepo/pnpm/Go-Fiber-sqlc/Next-16; actualizar el
stack = bump del profile → el próximo `/new-project` usa el stack nuevo.

### 15.3 Un profile define VARIOS scaffolds (arquetipos)

`scaffolds/<name>/`, con blueprints compartidos en `_blueprints/`. Cada scaffold
declara su **layout** en `scaffold.toml`.

**Dos modos de ensamblaje (no cuatro):**

| `layout` | Ensamblaje | `/new-app` | Cubre |
|----------|-----------|-----------|-------|
| `monorepo` | shell + apps componibles desde `_blueprints/` | **Sí** | wirvii360r, migratio, pagos |
| `single` | un skeleton plano, copy+substitute | No | library (whatsapp-client), single-app (site), single-module (mneme) |

`library`, `single-app` y `single-module` son scaffolds nombrados de modo
`single` — mismo motor, distinto contenido. La composición shell+blueprints es
una propiedad del layout `monorepo`, no de scaffolding en general.

### 15.4 Segundo eje: `toolchain` (solo monorepo) y el bootstrap por capas

Un monorepo puede armarse con una **convención conocida** o de **diseño libre**.
La diferencia es *de dónde saca mneme el conocimiento de wiring para `/new-app`*:

| `toolchain` | Wiring de `/new-app` | Bootstrap del shell |
|-------------|----------------------|---------------------|
| `turborepo` | adapter **built-in** de mneme (sabe `apps/`, `turbo.json`, `pnpm-workspace.yaml`) | generador oficial `create-turbo` |
| `custom` (diseño libre) | reglas **declaradas por el autor** en `scaffold.toml` | shell capturado del repo ejemplar |

`single` es siempre diseño libre trivial (sin composición, sin wiring).

**El shell se arma por capas** (reconcilia "generador oficial" con
"template-in-profile" — no es either/or, es apilado):

1. **Bootstrap opcional, pinneado:** un generador oficial a versión fija
   (nunca `@latest` — es no-determinista y rompe en silencio, cf. SPEC-088).
   Declarado en `scaffold.toml`: `bootstrap = "create-turbo@2.3.1"`. Bumpear la
   versión = editar el profile (deliberado, versionado). Diseño libre → sin
   bootstrap (shell capturado puro).
2. **Overlay del profile** (siempre, encima): contenido del equipo —
   `packages/*-go`, tokens de Tailwind, tweaks de config, convenciones.
3. **Blueprints del profile:** `_blueprints/`, que el generador no conoce.

```toml
# scaffolds/saas-multitenant/scaffold.toml
layout    = "monorepo"
toolchain = "turborepo"
bootstrap = "create-turbo@2.3.1"        # pinneado; omitido si toolchain=custom
blueprints = ["go-core-srv", "next-web-ui", "hono-renderer", "packages-go"]

# solo toolchain=custom: el autor declara el wiring que Turborepo trae built-in
# [wiring]
# apps_dir = "services/"
# on_add   = ["update:workspace.yaml", "update:build.config.js"]
```

### 15.5 Flujo

- **`/new-project`** → lista **todos** los scaffolds del profile activo → eliges
  uno → el grill pregunta cuáles de sus apps incluir (solo si `monorepo`) →
  ensambla → **corre `mneme-init`** sobre el repo fresco → graba `scaffold` en el
  pin. El repo **nace cableado** (pin + agents materializados + memoria sembrada).
- **`/new-app`** → lee `scaffold` del pin → si el layout es `monorepo`, carga el
  catálogo de blueprints de ESE arquetipo → ofrece los apps compatibles → suelta
  el blueprint y **auto-cablea**: con `toolchain=turborepo` vía el adapter
  built-in (`turbo.json`, `pnpm-workspace.yaml`); con `toolchain=custom` vía las
  reglas `[wiring]` declaradas por el autor. Si el layout es `single` → "no
  aplica".

### 15.6 Autoría de scaffolds (durante la creación del profile)

**Híbrido: captura + curación**, y bifurca según el toolchain:

| Autoría | Qué hace el grill |
|---------|-------------------|
| Monorepo + Turborepo | Captura asistida por el adapter (auto-detecta `apps/`, `packages/`, `turbo.json`); wiring built-in → el autor casi solo cura blueprints. |
| Monorepo + diseño libre | Captura + curación **+ elicitar el wiring** (dónde viven los apps, qué archivos tocar al agregar uno) → se declara en `[wiring]`. |
| Single | Captura el skeleton + parametriza variables. Sin wiring. |

Parte de un repo ejemplar existente (ej: wirvii360r) sin arrastrar su legacy a
ciegas: el autor cura qué es template vs basura histórica.

### 15.7 Scaffold e init = extremos del ciclo de vida

- `/new-project` (scaffold) → crea un repo **desde cero** desde el template del
  profile, y termina en `mneme-init`.
- `mneme-init` (onboarding) → cablea un repo **existente** al profile.

Ambos convergen en el mismo estado final: repo con el pin puesto, agents
materializados (capa-1 del profile + capa-2/3 del repo) y memoria sembrada.

## 16. Descomposición en specs (EPIC-sized)

Cruza `model / db / store / service / cli / mcp / install`.

1. **Manifiesto + store + resolución del pin** — formato `mneme-profile.toml` +
   `.mneme-profile` self-describing; `profile add/update/list`; store en
   `~/.mneme/profiles/`.
2. **Activación + lockfile + proveniencia** — materialización híbrida, switch
   limpio, `Forget` de rules marcadas, marca `source=profile:<name>`.
3. **Precedencia + dos verbos + SessionStart** — `use`/`default`, gate
   accionable, integración con el SessionStart hook existente.
4. **Integración team-memory** — exclusión por proveniencia, guard anti-zombie.
5. **Creación asistida + integración `mneme-init`** — scaffolder
   (`profile new`) + skill de grill; `mneme-init` detecta el pin y fusiona
   capa-1 + capa-2/3.
6. **Migración del default profile OSS** — assets actuales → formato profile.
7. **Scaffolding** — skills `/new-project` + `/new-app`, comandos deterministas
   `mneme project new` / `mneme app add`, `scaffolds/` con `_blueprints/`, los
   dos layouts (`monorepo`/`single`), autoría híbrida captura+curación, cierre en
   `mneme-init`. Depende de la spec 5 (creación asistida). Probablemente se
   sub-divide.

## 17. Ledger de decisiones del grill

| # | Decisión |
|---|----------|
| 1 | Profile = contenedor unificador de piezas existentes. |
| 2 | Distribución = repo git; `mneme profile add <git-url>` → store. |
| 3 | Creación = scaffolder + skill de grill asistido (hermano de `mneme-init`). |
| 4 | Activación híbrida: repunta runtime + materializa file-based. |
| 5 | Precedencia nvm, reemplazo puro; `extends` reservado, no implementado. |
| 6 | Dos verbos: `use` (repo/sesión) vs `default` (futuras); default no live. |
| 7 | Switch limpio vía `.mneme/profile.lock` + proveniencia. |
| 8 | Race mismo-repo: lock detecta staleness + avisa. |
| 9 | Materializado = derivado + gitignored; solo el pin se commitea. |
| 10 | Pin auto-describible: name + source + ref. |
| 11 | SessionStart: materializa si está; si no, nudge + gate accionable para instalar. |
| 12 | Proveniencia `profile:<name>` = switch limpio + protege hand-authored + excluye del vault. |
| 13 | Rules = memorias tipo `rule`. **Corregido (SPEC-092) + decisión owner:** el switch NO usa `Forget` (decay_rate=1.0, la rule se sigue inyectando) NI soft-delete; hace **hard delete por proveniencia** (`WHERE source='profile:<name>'`) — seguro porque las rules del profile son derivadas/regenerables. `Forget`/soft queda solo para hand-authored. Proveniencia en columna `source` (migración 015). |
| 14 | Perímetro IN/OUT definido (§13). |
| 15 | Scaffolding = dos mitades (skills `/new-project`+`/new-app` + comandos deterministas), vive en el profile, template-in-profile. |
| 16 | Un profile define VARIOS scaffolds; layout `monorepo` (componible, `/new-app`) vs `single` (plano). single-app/single-module/library colapsan en `single`. |
| 17 | `/new-project` lista scaffolds; el pin graba `scaffold`; `/new-app` lee ese campo para saber qué blueprints ofrecer. |
| 18 | Autoría de scaffolds = híbrido captura-de-repo-ejemplar + curación. Scaffold cierra en `mneme-init` → el repo nace cableado. |
| 19 | Segundo eje `toolchain` (solo monorepo): `turborepo` (adapter built-in) vs `custom`/diseño-libre (wiring declarado por el autor). `single` siempre libre. |
| 20 | `/new-app` en diseño libre auto-cablea con reglas `[wiring]` declaradas en `scaffold.toml`. |
| 21 | Shell por capas: bootstrap generador oficial **pinneado** (`create-turbo@2.3.1`, nunca `@latest`) + overlay del profile + blueprints. Bumpear = editar el profile. |
