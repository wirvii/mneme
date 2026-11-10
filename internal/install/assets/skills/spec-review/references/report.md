# The review report

One file per moment, written by the architect with `spec_doc_write` (kind `review-spec` writes `revision-spec.toml`, kind `review-plan` writes `revision-plan.toml`). mneme parses it before writing; an invalid report is rejected and nothing reaches the disk. It is a complete overwrite, with no `section`.

## Format

```toml
schema_version = 1
re_revision = false            # true when the moment's single re-review was used

[[revisor]]
papel = "comprension"          # moment spec: comprension | diseno | hechos; moment plan: preparacion | hechos
estado = "completo"            # completo | no_corrio (describes the ORIGINAL round)
nota = ""                      # required with no_corrio; optional with completo
parafrasis_propuesta = "..."   # comprension only: what the spec proposes, in the reviewer's words
parafrasis_hoy = "..."         # comprension only: how it works today, per the document

[[hallazgo]]
id = "H1"                      # unique in the whole report, also across rounds
revisor = "hechos"             # a declared role that completed
severidad = "blocker"          # blocker | major | nit
texto = "..."
salida = "corregir"            # corregir | refutar | escalar | descartar | riesgo_aceptado
evidencia = ""                 # required with refutar
resolucion = ""                # required with corregir
```

Unknown keys are rejected. Zero findings is valid. A report where every role is `no_corrio` (with its `nota`) is valid: it is the way out when no reviewer is available, and it stays visible at the gate.

## The five outcomes

Always these five values, in this order.

1. `corregir`: you fixed the document. `resolucion` says what changed.
2. `refutar`: the finding is wrong. `evidencia` is something the reviewer could have read (a file and line, a test, a command output). "It was on purpose" is not evidence.
3. `escalar`: the decision belongs to the person. It reaches them at the gate (moment 1) or before implementing (moment 2).
4. `descartar`: only for a `nit`.
5. `riesgo_aceptado`: not a resolution path. It is the state of what is still pending after the single re-review, and it is only valid with `re_revision = true`.

Never change a severity: copy the reviewer's text and severity.

## The paraphrase finding

If the `comprension` paraphrase does not match the intent, record a finding with `revisor = "comprension"`, `severidad = "major"` and a `texto` that starts with "Paráfrasis que no coincide:". It is the only finding you write yourself. mneme cannot check that you copied the others faithfully; that is discipline.

## Using `nota` on a completed role

Besides a failed role, `nota` on a `completo` role records that the re-review did not run for it, or what changed after its review (see "Changes after the report" in `SKILL.md`).

## Valid example, moment 1

```toml
schema_version = 1
re_revision = false

[[revisor]]
papel = "comprension"
estado = "completo"
parafrasis_propuesta = "Adds a review of the spec by readers without the conversation, before the gate."
parafrasis_hoy = "Today nobody reviews a spec before it is presented to the person."

[[revisor]]
papel = "diseno"
estado = "completo"

[[revisor]]
papel = "hechos"
estado = "completo"

[[hallazgo]]
id = "H1"
revisor = "hechos"
severidad = "blocker"
texto = "The design cites internal/service/sdd_contract.go:45, which does not hold loadSpecCriteria."
salida = "corregir"
resolucion = "Corrected the path and line in the design."

[[hallazgo]]
id = "H2"
revisor = "diseno"
severidad = "nit"
texto = "Several small naming nits grouped in one finding."
salida = "descartar"
```

## Valid example, moment 2

```toml
schema_version = 1
re_revision = true

[[revisor]]
papel = "preparacion"
estado = "completo"
nota = "The re-review did not run for this role."

[[revisor]]
papel = "hechos"
estado = "no_corrio"
nota = "The reviewer failed twice."

[[hallazgo]]
id = "H1"
revisor = "preparacion"
severidad = "major"
texto = "Phase 3 does not say which file receives the new constant."
salida = "riesgo_aceptado"
```
