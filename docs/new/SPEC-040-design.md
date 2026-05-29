# SPEC-040 — Diseño técnico (3 parches independientes)

> Autoritativo para el backend. Acompaña `docs/new/mneme-v1.9.1-spec-cleanup.md` y memoria `spec/SPEC-040-reconciliation`. Sin pushback. **3 commits independientes, sin código compartido.**

## D-P1 — tokenizer fd-dup (`internal/shell/tokenize.go`)
En `tokensFromRedirect`, los ops `syntax.DplOut` (`>&`) y `syntax.DplIn` (`<&`) tienen `r.Word` = file descriptor en `2>&1`/`1>&2`, pero = path en `>&file`. El default actual emite todo como `TypeRedirectTarget` → el consumidor (enforce_delegation.sh) trata "1" como path → falso positivo.

**Fix (REFINADO):** para DplOut/DplIn, emitir `TypeRedirectTarget` SOLO si el word NO es puramente numérico:
```go
case syntax.DplOut, syntax.DplIn:
    if r.Word != nil {
        target := extractWordLiteral(r.Word)
        // `2>&1`/`1>&2`: word es un fd (numérico) → NO es path, no emitir.
        // `>&file`: word es un path → emitir (cmd >&file escribe a file).
        if !isAllDigits(target) {
            tokens = append(tokens, Token{Value: target, Type: TypeRedirectTarget})
        }
    }
```
+ helper `isAllDigits(s string) bool` (true si s no vacío y todos los runes son dígitos). El operator token (`2>&` etc.) se sigue emitiendo siempre. NO reescribir el resto del tokenizer.

**Tests (tokenize_test.go, token streams exactos):** `git log 2>&1` → sin redirect_target (FIX); `cmd 1>&2` → sin redirect_target; `echo hi >&out.txt` → CON redirect_target "out.txt" (no-numérico, no abre bypass); `make build &>/dev/null` → CON redirect_target (RdrAll, regresión); `rmdir x 2>/dev/null` → CON redirect_target (RdrOut N=2, regresión); `golangci-lint run 2>&1 | tee log` (pipeline) → sin redirect_target spurio; `echo foo > internal/x.go` (plano, regresión) → CON redirect_target. + test consumidor: `Tokenize("golangci-lint run 2>&1")` no produce NINGÚN TypeRedirectTarget.

**Commit 1:** `fix(shell): only emit redirect_target for fd-dup when word is a path, not a descriptor (SPEC-040)`

## D-P2 — link/unlink store resuelto (`internal/service/conflicts.go` + `internal/model/errors.go`)
Nuevo sentinel: `ErrCrossStoreRelation = errors.New("cannot relate a global and a project memory; they live in separate stores")`.
- **ConflictLink:** `mFrom, storeFrom, err := getFromEitherStore(from)`; `mTo, storeTo, err := getFromEitherStore(to)` (nil/err → ErrNotFound como ya). Si `storeFrom != storeTo` → `ErrCrossStoreRelation` (antes de cualquier write). `targetStore := storeFrom`; supersedes → `targetStore.SetSupersededBy(ctx, to, from)`; otra → `targetStore.CreateMemoryRelation(ctx, from, to, relation, "manual", rationale)`.
- **ConflictUnlink:** resolver ambos; si ambos resueltos y `storeFrom != storeTo` → ErrCrossStoreRelation; `targetStore` = storeFrom||storeTo||projectStore (fallback si ambas borradas); `targetStore.DeleteMemoryRelation` (best-effort, ErrNotFound tolerado); ClearSupersededBy sobre targetStore para from/to.
- **persistVerdict:** resolver storeA/storeB por ID; si storeA!=storeB → ErrCrossStoreRelation (judgeOnePair lo convierte en Skipped); `ts := storeA`; SetSupersededBy/CreateMemoryRelation sobre ts. (NO cambia de dónde vienen los candidatos = M2 fuera de scope.)
- **SIN migración** (013 ya está en global.db; migrate corre sobre cada DB).

**Tests (conflicts_test.go, dual store in-memory):** project-project supersedes/conflicts_with (regresión); global-global (el fix, antes ErrNotFound); project↔global → errors.Is ErrCrossStoreRelation sin write; ConflictUnlink global-global (fix) y project↔global (error).

**Commit 2:** `fix(service): conflict link/unlink write to resolved store; ErrCrossStoreRelation (SPEC-040)`

## D-P3 — DryRun desde installSteps (`internal/install/install.go` + `internal/cli/install.go`)
```go
func DryRun(agent *Agent, opts InstallOptions) (string, error) {
    var lines []string
    lines = append(lines, fmt.Sprintf("Agent: %s (%s)", agent.Name, agent.Slug))
    lines = append(lines, "")
    for _, step := range agent.installSteps(opts) {
        lines = append(lines, fmt.Sprintf("  [would run]  %s", step.Name))
    }
    return strings.Join(lines, "\n"), nil
}
```
Elimina el bloque hardcodeado L972-1042. **Firma cambia** binaryPath→opts: en `internal/cli/install.go`, mover la construcción de `opts` (incl. resolución de personalSource) ANTES de la rama `if flagDryRun`, y llamar `install.DryRun(agent, opts)`. El bloque DryRunPersonal se mantiene. Solo se listan Names (conservar paths = scope creep).

**Test (install_test.go):** `TestDryRun_MatchesInstallSteps` — parsea los Names del output de DryRun(agent, opts) y asserta que == [step.Name for step in agent.installSteps(opts)] exacto (orden+cantidad), para opts default y {Personal:true, ReinstallHooks:true}.

**Commit 3:** `refactor(install): derive DryRun from installSteps(opts); parity test (SPEC-040)`

## Cierre
CHANGELOG `[v1.9.1]` con exactamente los 3 fixes (commit docs adjunto al último o separado). make test+test-race + golangci-lint limpios. Sin tools nuevos (56). Sin schema changes.

## Anti-scope
3 commits independientes. NO reescribir tokenizer (P1 solo el case DplOut/DplIn). NO M2/unificación de stores (P2 solo wrong-store write + cross-store error). NO cambiar qué hace Install (P3 solo el listado de DryRun). Sin tools/comandos. Sin tocar allowlists/SDD/lane/skills/models/conflict detection-judgment/memory schema.
