# The QA report

Every report has the delivered end first and then exactly two sections. An empty section says "ninguno" explicitly; it is never left out.

```markdown
Delivered end reviewed: <commit>
Range: <the notice, verbatim from the brief>

## Dentro del contrato

- C1: PASS. `go test ./pkg -run TestX -v` printed PASS (3 subtests).
- C2: FAIL. Expected exit code 0, got 1. Output: `...`.

## Fuera del contrato — items abiertos

- BL-123 (new): the export command ignores `--out`. Found while running C2. Not a criterion of this spec.
```

Rules:

- One line per criterion id declared in `criteria.toml`, each with what was run and what happened.
- A FAIL in the first section is the only thing that can justify `spec_reject`.
- Each item in the second section is already a `backlog_add`; cite its id. If there is none, write "ninguno".
- Do not mix the two: presenting an out-of-contract finding as severe does not move it into the first section.
- Write for the person who reads it after you: plain words first, ids and commands after.
