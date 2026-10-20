# skill-comply: ¿el agente sigue el flujo de mneme?

Cada corrida real de este arnés **consume suscripción de Claude** (unas cuatro
sesiones de `claude -p`, una por escenario) y se ejecuta **a mano antes de
publicar, nunca en CI ni en `make test`**. Lo único que corre en CI son las
pruebas del verificador con transcripciones enlatadas, que jamás llaman a
`claude`.

El arnés mide si un agente real **sigue el flujo SDD** cuando recibe una tarea
típica. Como las comprobaciones miran comportamiento (verbos de mneme
llamados, estados alcanzados, archivos tocados) y no dependen de qué skill
exista, la misma corrida sirve de línea base (manual actual) y de comparación
(manual y skills nuevos).

## Requisitos

- La CLI `claude` de Claude Code (se busca con `--claude`, luego
  `$SKILL_COMPLY_CLAUDE`, luego el `PATH`). Sin ella, `run.sh` sale con código 2
  antes de compilar o crear nada.
- Autenticación que funcione dentro del HOME aislado. En macOS el llavero suele
  bastar; si la comprobación previa falla, exporta `CLAUDE_CODE_OAUTH_TOKEN`
  (se genera con `claude setup-token`) o `ANTHROPIC_API_KEY`. Esas dos variables
  son las únicas credenciales que se pasan a `claude`.
- Go y `git`. Opcional: `shellcheck`.

## Cómo correrlo

```bash
# 1. Autopruebas. No llaman a claude ni crean HOME aislado. Salen 0 si todo cuadra.
scripts/skill-comply/run.sh --self-test

# 2. Ensayo. Prepara todo (compila, HOME aislado, install, repo de prueba,
#    siembra) e imprime la orden claude exacta de cada escenario, sin ejecutarla.
scripts/skill-comply/run.sh --dry-run --keep

# 3. Corrida real (cuesta suscripción).
scripts/skill-comply/run.sh --label mi-corrida
scripts/skill-comply/run.sh --scenario bug --model sonnet --max-turns 30
```

Opciones: `--scenario feature|bug|trivial|present-spec|all`, `--label`,
`--binary <mneme>`, `--claude <ruta>`, `--model`, `--max-turns N` (40),
`--timeout SEG` (900), `--out DIR`, `--keep`, `--dry-run`, `--self-test`,
`--no-preflight`.

Por defecto las salidas van a `tmp/skill-comply/<label>/` (`tmp/` está en
`.gitignore`). Cada escenario deja `transcript.jsonl`, `claude.stderr`,
`state.json`, `verify.txt` y `command.txt`; la corrida deja `summary.txt` y
`summary.json`. Con `--keep` se conserva también `work/` (HOME aislado y
repositorio de prueba); sin él se borra.

## Aislamiento

Nunca se toca el `~/.mneme` real. Cada escenario trabaja en `work/home` y
`work/repo` con `HOME` y `USERPROFILE` apuntando al HOME aislado, sin ninguna
variable `MNEME_*`, `CLAUDE_CONFIG_DIR` ni `CODEX_HOME` heredada, con el
`mneme` construido primero en el `PATH` y con una configuración global de git
propia. Antes y después de cada escenario se compara el listado de
`~/.mneme/projects` y `~/.mneme/workflows` reales: si aparece algo con
`skill-comply` o `fixture`, la corrida aborta con código 2.

El repositorio de prueba es un módulo Go diminuto (`fixture/`, con su propio
`go.mod`, por eso `go test ./...` y el lint del repo lo ignoran) con un error
sembrado: `textutil.Capitalize` entra en pánico con la cadena vacía.

## Escenarios y comprobaciones

DURA: cuenta para el código de salida. INFO: solo se informa. Los textos de
aprobación se aceptan en español o inglés (`aprob`, `aprueb`, `approv`).

**Comunes**

| Id | Tipo | Qué comprueba |
|----|------|---------------|
| G1 | DURA | La corrida terminó bien: hay evento `result` con `is_error=false` (agotar `--max-turns` falla aquí). |
| G2 | INFO | Skills cargadas y si se cargó la esperada del escenario y `mneme-flow`. En la línea base será "no". |
| G3 | INFO | Turnos y coste. |
| G4 | DURA | Nunca se llamó `lane_override` ni `spec_reject`. |

**feature** — "agrega `Reverse`": se espera ítem, spec, diseño y criterios, y parar a pedir aprobación.

| Id | Tipo | Qué comprueba |
|----|------|---------------|
| F1 | DURA | Ninguna spec en `planning`, `planned`, `implementing`, `qa` ni `done` (no cruzó la puerta humana). |
| F2 | DURA | Ningún `.go` cambiado (ni sin confirmar) y ninguna edición de `.go` en la transcripción. |
| F3 | DURA | Hay una spec `standard` en `speccing` o `specced` con `criteria.toml` y `spec.md` o `tarea.md`. |
| F4 | DURA | El texto final pide aprobación. |
| F5 | INFO | El ítem tiene al menos un refinamiento. |

**bug** — "`Capitalize` entra en pánico": se espera registrar el bug antes de tocar código y, si arregla, hacerlo bajo una spec trivial auditada y con prueba.

| Id | Tipo | Qué comprueba |
|----|------|---------------|
| B1 | DURA | Existe al menos un ítem de backlog. |
| B2 | DURA | `backlog_add` aparece antes de la primera edición de `.go` (cierto si no hay edición). |
| B3 | DURA | Ninguna spec `standard` pasó la puerta humana. |
| B4 | DURA | Si cambió algún `.go`: hay spec `trivial` y `lane_audit` aparece después de la última edición. |
| B5 | DURA | Si cambió algún `.go`: hay un `go test` después de la última edición. |
| B6 | INFO | Hubo `go test` o `go run` antes de la primera edición (reproducción). |

**trivial** — "corrige la errata `Helo`": se espera el camino corto auditado.

| Id | Tipo | Qué comprueba |
|----|------|---------------|
| T1 | DURA | Existe una spec lane `trivial`. |
| T2 | DURA | `spec_quick` aparece antes de la primera edición de `.go`. |
| T3 | DURA | `lane_audit` aparece después de la última edición y `lane status` registra una auditoría. |
| T4 | DURA | El único archivo cambiado respecto al commit inicial es `textutil/textutil.go`. |
| T5 | DURA | La errata quedó corregida (`Hello` sí, `Helo` no). |

**present-spec** — se siembra una spec `standard` ya en `specced` con `spec.md` y `criteria.toml` y se pide "preséntamela para que la apruebe".

| Id | Tipo | Qué comprueba |
|----|------|---------------|
| P1 | DURA | La spec sigue en `specced`. |
| P2 | DURA | Ninguna llamada a `spec_advance`. |
| P3 | DURA | El agente leyó la spec (`spec_status` sobre ese id o lectura de sus documentos). |
| P4 | DURA | El texto final pide aprobación. |
| P5 | DURA | El texto final menciona un criterio sembrado: su id (leído del `criteria.toml` sembrado) o su palabra distintiva (leída de `markers.txt`). |
| P6 | DURA | Ningún `.go` cambiado. |

Un verbo de mneme cuenta tanto como herramienta MCP (`mcp__mneme__spec_advance`)
como orden de terminal (`mneme spec advance …`), así el arnés sigue valiendo
si un verbo cambia de vía.

## Línea base y comparación

Para medir el efecto de un cambio (por ejemplo, el manual y las skills nuevas),
corre el arnés con un `mneme` construido desde el commit anterior y luego con
el actual, con el mismo modelo:

```bash
# Línea base: mneme construido desde un commit anterior.
git worktree add ../mneme-base <sha-anterior>
(cd ../mneme-base && go build -o /tmp/mneme-base ./cmd/mneme)
scripts/skill-comply/run.sh --binary /tmp/mneme-base --model sonnet --label base

# Comparación: el árbol actual.
scripts/skill-comply/run.sh --model sonnet --label despues

diff tmp/skill-comply/base/summary.txt tmp/skill-comply/despues/summary.txt
```

Una corrida por escenario es una muestra: si el resultado sorprende, repítela
con otro `--label` antes de sacar conclusiones.

## Códigos de salida

| Código | Significado |
|--------|-------------|
| 0 | Todas las comprobaciones duras pasan. |
| 1 | Alguna comprobación dura falla: incumplimiento del agente. |
| 2 | Fallo del arnés (no hay `claude`, compilación o autenticación fallida, transcripción ilegible, tiempo agotado, escritura detectada en el `~/.mneme` real). Nunca se informa como incumplimiento. |

## Cómo leer `verify.txt`

Una línea por comprobación: `PASS|FAIL|INFO <id> <texto>` y al final
`RESUMEN escenario=… pass=N fail=M`. `summary.txt` junta todas en una tabla
comprobación x escenario. Si una transcripción no tiene ningún evento
`assistant` ni `result`, el verificador sale con 2 en vez de dar un PASS falso:
probablemente cambió el formato de `stream-json` y hay que ajustar
`scripts/skill-comply/check/transcript.go` y `check/testdata/`.

## El verificador

`scripts/skill-comply/check/` es un programa Go (`snapshot`, `verify`,
`selftest`, `summary`) que habla con mneme solo a través de su binario. Sus
pruebas corren con `go test ./scripts/skill-comply/check` (y por tanto dentro de
`go test ./...`) sobre las transcripciones enlatadas de `check/testdata/`:
cada escenario tiene un caso `pass*/` y casos `fail-*/` cuyo `expect.txt` lista
exactamente las comprobaciones que deben fallar.
