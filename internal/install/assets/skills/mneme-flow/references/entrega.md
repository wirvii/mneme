# The delivery contract

The `entrega` section of `tarea.md` is the implementer's report. Besides free prose it carries one small TOML block, the delivery contract, so the orchestrator, the qa-tester and the stop hooks can tell at a glance how the turn ended, which commands were run, on which commit, and what risk is declared.

mneme checks the shape, never the truth: it does not run your commands, compare their output or look at the repository. The qa-tester checks the truth.

## Where it goes

The block sits between two lines, each alone on its own line (spaces around them are fine):

```
<!-- mneme:entrega:contrato:start -->
<!-- mneme:entrega:contrato:end -->
```

Everything before and after is prose. Each line must appear exactly once in the section. A delimiter line anywhere else, in your prose or inside a `salida` that printed it, counts as a duplicate and the write is refused: never quote them.

The TOML inside may be wrapped in a fence of three backticks (with or without `toml`) so it renders well; the closing line must then be exactly three backticks. A `~~~` fence is not recognised.

## Example

~~~markdown
Implemented the delivery package and wired it into service, mcp and cli.
Doubt: the layer-1 text may be short for bug-hunter.

<!-- mneme:entrega:contrato:start -->
```toml
schema_version = 1
estado = "hecho_con_dudas"
arbol = "2ba81d8c4e1f"
riesgo = { item = 3, razon = "spec_status and spec_doc_write gain fields other agents read" }

[[evidencia]]
comando = "go test -cover ./internal/delivery/"
exit_code = 0
salida = """
ok  	github.com/wirvii/mneme/internal/delivery	0.011s	coverage: 94.2% of statements
"""
criterio = "C1"

[[evidencia]]
comando = "golangci-lint run"
exit_code = 0
salida = "0 issues."
criterio = "C13"

[[prueba_en_rojo]]
nombre = "TestSubagentsCompose_MatchesMCPGolden"
accion = "editada"
motivo = "the golden pins layer 1 v=7; the design foresaw regenerating it at v=8"
```
<!-- mneme:entrega:contrato:end -->

Notes for QA: ...
~~~

Blocked, with the command that failed (evidence is optional here, but useful):

```toml
schema_version = 1
estado = "bloqueado"
riesgo = "none"

[[evidencia]]
comando = "make test"
exit_code = 2
salida = "FAIL: TestExplore_TouchRelations (known flaky, BL-219)"
```

The smallest valid block is three lines: `schema_version = 1`, an `estado` and `riesgo = "none"`.

## Keys

Closed list: any other key is refused (notes go in the prose).

| Where | Key | Value | Required |
|---|---|---|---|
| top | `schema_version` | always `1` | yes |
| top | `estado` | `hecho`, `hecho_con_dudas`, `falta_contexto` or `bloqueado` | yes |
| top | `arbol` | SHA of the commit that holds the work, 7 to 64 lowercase hex characters | with `hecho` and `hecho_con_dudas` |
| top | `riesgo` | `"none"`, or a table with `item` (1 to 6) and `razon` (not empty); inline `{ item = 3, razon = "..." }` or as a `[riesgo]` table | yes |
| `[[evidencia]]` | `comando` | the exact command you ran | yes, per entry |
| `[[evidencia]]` | `exit_code` | integer, the exit code you saw | yes, per entry |
| `[[evidencia]]` | `salida` | an excerpt of the output: at most 40 lines and 4096 bytes; may be empty | no |
| `[[evidencia]]` | `criterio` | the criterion id the command covers (`C1`) | no |
| `[[prueba_en_rojo]]` | `nombre` | the test name, unique in the block | yes, per entry |
| `[[prueba_en_rojo]]` | `accion` | `parada_y_reportada` or `editada` | yes, per entry |
| `[[prueba_en_rojo]]` | `motivo` | why | yes, per entry |

At least one `[[evidencia]]` with `hecho` and `hecho_con_dudas`; any number, zero included, with the other two. Any number of `[[prueba_en_rojo]]`.

## The four states

- `hecho`: everything asked for is done and the checks ran.
- `hecho_con_dudas`: done, but with doubts about correctness or scope; explain the doubts in the prose.
- `falta_contexto`: you cannot start or continue because you need information or a decision you do not have; the prose names what is missing. It does not mean "my context window ran out".
- `bloqueado`: you cannot complete, including partial work cut short by the end of a turn or of the context; the prose says what is done and what is left, and evidence is optional.

The last two are valid reports. Say them instead of inventing a finish.

## arbol: commit before you write the delivery

`arbol` is the SHA of the committed tree your checks ran on, not the HEAD of a dirty working tree. Commit before you write the delivery, run the checks on that commit, and give its SHA. Copy each `[[evidencia]]` from what you really ran.

## Red tests

An existing test that goes red is stopped and reported (`parada_y_reportada`); it is edited (`editada`) only when the spec's design foresaw that edit. Both go in `[[prueba_en_rojo]]` with their reason. If you stopped and reported, you did not finish: `hecho` is refused with a `parada_y_reportada` entry.

## riesgo

One item per delivery, or `"none"`. mneme only checks the shape: `item` between 1 and 6 and a non-empty `razon`. The six items and how to choose one: `references/risk.md`.

## What mneme checks, in order

The first rule that fails wins, and the error names the field (and the entry index in repeated tables).

1. Exactly one start line and one end line, start first. No delimiter at all is "missing"; anything else is invalid.
2. An opened fence is closed; the block is not empty.
3. `schema_version` is present and is 1; the TOML parses.
4. No unknown key and no wrong type (`exit_code = "0"` is refused).
5. `estado` is one of the four.
6. `arbol` is present with `hecho`/`hecho_con_dudas`, and has the SHA shape whenever it is given.
7. Evidence: at least one with `hecho`/`hecho_con_dudas`; each with a non-blank `comando`, an `exit_code`, a short `salida` and a well-formed `criterio`.
8. Red tests: `nombre` present and unique, `accion` one of the two, `motivo` present.
9. `riesgo` present: `"none"` or a table with exactly `item` (1 to 6) and `razon`.
10. A `parada_y_reportada` red test is incompatible with `hecho`.

An invalid block is always refused and `tarea.md` is left untouched. A missing block is refused only for a standard spec in implementing; anywhere else it is written and `spec_doc_write` answers `contract: missing`.

## The SubagentStop reminder

In Claude Code, a `SubagentStop` hook reminds an implementer once per spec when a standard spec in implementing has no current block; `falta_contexto` and `bloqueado` satisfy it. If the spec is not yours, finish without writing.

## What the orchestrator does with it

`spec_status` shows `delivery_contract` (valid, missing or invalid) and `delivery_state` from implementing on. `falta_contexto` or `bloqueado` is a report, not a delivery: the orchestrator answers it (context, a decision, a pushback) instead of moving the spec to qa. `invalid` means the file was edited outside mneme: the implementer rewrites the block. No transition checks the block; closing the spec stays a human decision.
