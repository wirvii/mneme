# SPEC-040 — Cleanup: Three Residual Patches · Informe de implementación

> **Para:** agente de discusión de diseño.
> **Estado:** ✅ Implementado, mergeado (PR #10 → `main`, `fb6adc3`) y liberado como **v1.9.1** (patch, 4 assets).
> **Origen:** `docs/new/mneme-v1.9.1-spec-cleanup.md` + `docs/new/SPEC-040-design.md`. **Predecesores:** SPEC-034..039. **Fecha:** 2026-05-29.

---

## 1. Qué resuelve

Tres defectos latentes acumulados a través de releases previas, barridos en un solo cleanup antes del finale (SPEC-041). Ninguno bloqueaba nada; cada uno es del tipo que muerde en silencio meses después. **3 commits independientes, sin código compartido** — implementados y revisados por separado.

| Patch | Origen | Defecto | Fix |
|---|---|---|---|
| **P1** | SPEC-034 §8.7 | Tokenizer emitía el word de `2>&1` (op de duplicación de fd) como `redirect_target`, y el hook lo trataba como ruta → falso positivo | Para `DplOut`/`DplIn`, emitir `redirect_target` solo si el word no es numérico |
| **P2** | SPEC-039 §7.1 (M1) | `ConflictLink`/`Unlink`/`persistVerdict` resolvían el store pero escribían a `projectStore` → global-global fallaba | Escribir al store resuelto; cross-store → `ErrCrossStoreRelation` |
| **P3** | SPEC-038 §6.4 | `DryRun` mantenía su propia lista de pasos → "mini-C1" latente | `DryRun(agent, opts)` enumera `installSteps(opts)` |

## 2. Los tres fixes

### P1 — tokenizer fd-duplication (`internal/shell/tokenize.go`)
El consumidor real es `enforce_delegation.sh`, que trata cada token `redirect_target` como una ruta a validar. Para `2>&1` (op `syntax.DplOut`, word `"1"`) el "1" es un **file descriptor**, no una ruta — pero se emitía como `redirect_target`, causando el bloqueo `"Redirect a ruta protegida: '1'"` (observado en vivo con `golangci-lint run 2>&1`).

**Refinamiento clave (impuesto por el orquestador sobre la propuesta inicial del architect):** el architect proponía descartar el word de **todo** `DplOut`/`DplIn`. Eso habría introducido un **falso-negativo** para `>&file` (que sí escribe a un archivo — un bypass del hook). El fix shipeado emite `redirect_target` **solo si el word no es puramente numérico** (`isAllDigits`): `2>&1`/`1>&2` (fd) → no emite (arregla el falso positivo); `>&file` (ruta) → sí emite (no abre bypass). Arreglar un falso-positivo sin abrir un falso-negativo.

### P2 — link/unlink al store resuelto (`internal/service/conflicts.go`)
`getFromEitherStore` devuelve `(memoria, store, err)` pero el código descartaba el store (`_`) y escribía a `projectStore`. Fix: usar el store resuelto. Los pares **cross-store** (una memoria global, una de proyecto — viven en SQLite distintos, no se pueden relacionar coherentemente) devuelven el sentinel nuevo `ErrCrossStoreRelation` con mensaje claro, **antes** de cualquier write. Sin migración (013 ya aplica a global.db porque el runner corre todas las migraciones sobre cada DB). **No se atacó M2** (la limitación de *detección* cross-store) — sigue diferida.

### P3 — dry-run desde `installSteps()` (`internal/install/install.go`)
`DryRun` reconstruía la secuencia a mano (70 líneas) — el mismo riesgo de divergencia (mini-C1) que SPEC-038 eliminó para el camino de ejecución. Fix: `DryRun(agent, opts)` ahora itera `agent.installSteps(opts)` e imprime `step.Name`. El CLI construye `opts` antes de la rama dry-run. Un parity test (`TestDryRun_MatchesInstallSteps`) cierra formalmente la clase mini-C1 junto al route-parity test de SPEC-038. La ejecución de install no cambió; se perdió el detalle de paths por-paso en el dry-run (conservarlos habría sido scope creep).

## 3. Proceso SDD

| Fase | Resultado |
|---|---|
| Reconciliación | los 3 confirmados contra código real; sin migración (013 ya en global.db) |
| Architect | diseño quirúrgico, sin pushback; orquestador refinó P1 (numeric-check) |
| Backend | 4 commits (3 fixes independientes + docs), verdes |
| QA | **APPROVED** — 0 críticos, 0 importantes, 2 menores (no-issues) |
| Verificación propia | build OK · test 27/27 · race (paquetes tocados) OK · golangci-lint 0 issues |
| Release | PR #10 → main (`fb6adc3`), tag v1.9.1, 4 assets |

## 4. Commits (4) y release
`302e981` fix(shell) P1 · `2e44a8a` fix(service) P2 · `df8c9cb` refactor(install) P3 · `51797f5` docs CHANGELOG. PR #10 → `fb6adc3`. Tag v1.9.1 → 4 assets. Independencia verificada por QA: P1 toca solo `internal/shell/`, P2 solo `service/conflicts.go`+`model/errors.go`, P3 solo `install.go`+`cli/install.go`.

## 5. Puntos abiertos para la discusión de diseño

1. **El hook `enforce_delegation.sh` no se re-desplegó.** P1 corrigió el tokenizer en el binario, pero el script bash del hook se instala con `mneme install claude-code`. El falso positivo (`mneme install`, `2>&1`) seguirá apareciendo en la sesión actual hasta `upgrade` + `install` + reinicio. Patrón ya conocido de releases anteriores.
2. **M2 sigue pendiente (deliberado):** P2 solo arregló el *write-to-wrong-store* (M1); la *detección* cross-store (un conflicto entre una decisión global y una de proyecto no se autodetecta porque FTS5 no cruza los dos SQLite) sigue siendo una limitación arquitectónica abierta. P2 ahora la nombra honestamente con `ErrCrossStoreRelation` en vez de fallar confuso. La unificación de stores / cross-DB sigue siendo candidata a una spec propia.
3. **P3 cierra la clase mini-C1 para install,** pero el patrón general ("una fuente de verdad, no listas paralelas") podría aplicarse en otros lados si aparecen (p.ej. cualquier comando que enumere capacidades). No hay otros conocidos hoy.
4. **`>&file` ambiguo:** bash trata `cmd >&word` como dup-or-file según si `word` es numérico. El fix usa exactamente esa heurística (numeric → fd, else → path), que coincide con la semántica de bash. Es el comportamiento correcto, pero vale tenerlo presente si alguna vez se quiere precisión total del shell (mvdan parsea ambos como `DplOut`).

## 6. Archivos (referencia)
```
internal/shell/tokenize.go (+ case DplOut/DplIn + isAllDigits) + tokenize_test.go        [P1]
internal/model/errors.go (+ErrCrossStoreRelation); internal/service/conflicts.go (+test) [P2]
internal/install/install.go (DryRun), internal/cli/install.go (opts antes de dry-run),
  internal/install/install_test.go (parity), coverage_test.go                            [P3]
CHANGELOG.md ([v1.9.1])                                                                   [docs]
```

> **Operativo:** `mneme upgrade` a v1.9.1 + (para que P1 surta efecto en el hook live) `mneme install claude-code` con el binario nuevo + reiniciar Claude Code. P2/P3 son lógica del binario (sin re-deploy del hook). La spec marca el reinicio como "solo si hay surface change" — no hay tools nuevos, pero el hook bash sí necesita reinstalarse para P1.
