# SPEC-040 — Cleanup: Three Residual Patches

**Status:** Ready for implementation · **Target release:** v1.9.1 (patch) · **Predecessors:** SPEC-034..039

> **NOTA DE RECONCILIACIÓN (orquestador, 2026-05-29):** sondeo previo confirma los 3 puntos. Detalle en `spec/SPEC-040-reconciliation` (la completa el architect). **3 issues independientes, 3 commits separados, sin código compartido.** Si algún parche resulta mayor que un fix quirúrgico → PARAR y escalar.

## P1 — tokenizer `2>&1` (`internal/shell/tokenize.go`)
- `tokensFromRedirect` (L250) arma opStr = N(fd) + Op.String() y luego emite `r.Word` como `TypeRedirectTarget`. Para `2>&1` el Op es duplicación de fd (`>&` = `syntax.DplOut`; `<&` = `syntax.DplIn`) y el word ("1") es un **file descriptor**, NO un path. El consumidor (`enforce_delegation.sh` via `mneme hook tokenize`) trata todo `redirect_target` como ruta → **falso positivo** ("Redirect a ruta protegida: '1'"), observado en vivo con `golangci-lint run 2>&1`.
- **Fix:** en `tokensFromRedirect`, detectar ops de duplicación de fd (DplOut/DplIn, y revisar &>/>&/1>&2 etc.) y NO emitir el fd como `TypeRedirectTarget` (o emitirlo como un tipo distinto que el consumidor ignore). Acotado SOLO a tokenización de redirects. **NO reescribir el tokenizer.**
- Tests table-driven en tokenize_test.go: `2>&1`, `&>file`, `>&file`, `2>file`, `1>&2`, redirect en medio de pipeline, comando plano (regresión) — asertan el token stream exacto. + al menos un test a nivel consumidor (el comando con `2>&1` ya no produce redirect_target spurio).

## P2 — link/unlink al store resuelto (`internal/service/conflicts.go`)
- `getFromEitherStore` devuelve `(m, targetStore, err)` pero ConflictLink (L283), ConflictUnlink (L327) y persistVerdict de ConflictScan (L263/267) escriben a `svc.projectStore` (descartan el store resuelto). Global-global falla con ErrNotFound.
- **Fix:** usar el store devuelto por getFromEitherStore. **Same-store** (ambos project o ambos global): escribir ahí (arregla global-global). **Cross-store** (uno project, uno global): devolver `ErrCrossStoreRelation` ("cannot relate a global and a project memory; they live in separate stores") — sin write al store equivocado, sin ErrNotFound confuso. NO intentar M2 (detección cross-store / unificación).
- Migraciones se aplican a TODAS las DBs (global.db incluida) → memory_relations YA existe en global. **Confirmar; sin migración nueva salvo que el architect pruebe lo contrario.**
- Tests: project-project (regresión), global-global (el fix), cross-store (ErrCrossStoreRelation).

## P3 — dry-run desde `installSteps()` (`internal/install/install.go`)
- `installStep` YA tiene campo `Name` (L233). `DryRun` (L966) mantiene una lista propia → "mini-C1" latente.
- **Fix:** DryRun enumera `installSteps(opts)` e imprime el `.Name` de cada paso (más "would run"), sin lista separada. Sin cambiar qué hace install.
- Test de paridad: dry-run lista exactamente los pasos de `installSteps(opts)` para opts dados (cierra la clase mini-C1 junto al route-parity test de SPEC-038).

## Anti-scope (§3, §10)
3 commits independientes (ninguno sangra en otro). SIN tools/comandos nuevos (queda 56). NO unificación de stores / cross-DB FTS5 (M2 diferido). NO finale. NO §6.6 (MCP visibility). NO §7.5 (supersedes excluye vs anota — aceptado). NO cosméticos salvo que P2 toque ese exacto código zero-risk. NO refactors "ya que estoy". NO tocar allowlists/SDD/lane/skills/models/conflict detection-judgment/memory schema (salvo migración estrictamente requerida por P2, si el architect la prueba necesaria). Si un parche es mayor que quirúrgico → escalar.

## Criterios de aceptación
P1: reconciliación identifica consumidor + inputs mis-tokenizados; 2>&1 y formas relacionadas tokenizan bien (tests exactos + regresión plana); test a nivel consumidor; tokenizer parcheado no reescrito.
P2: Link/Unlink escriben al store resuelto; global-global OK; project-project OK (regresión); cross-store → ErrCrossStoreRelation claro; M2 NO intentado.
P3: DryRun enumera installSteps(opts), sin lista hardcoded; parity test; ejecución de install sin cambios.
Cross-cutting: 3 commits independientes; sin scope creep; make test+race; golangci-lint limpio (orchestrator-verified); CHANGELOG [v1.9.1] lista exactamente estos 3 fixes; spec/SPEC-040-reconciliation registra hallazgos reales.
