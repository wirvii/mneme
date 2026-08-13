# testdata/gremlins-v0.6.0.json provenance (SPEC-119 P2)

This fixture is a REAL `gremlins unleash -o report.json` run (gremlins
v0.6.0, `go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0`),
executed against a disposable 4-function scratch module (`Add`, `Max`,
`IsPositive`, `Concat`) with a hand-written test suite, on this machine's
Go toolchain (`go1.26.2 darwin/arm64`). Six of its seven mutations are
UNMODIFIED, byte-for-byte, from that real run's own `report.json`:

```
KILLED CONDITIONALS_NEGATION at calc.go:18:11
KILLED CONDITIONALS_BOUNDARY at calc.go:18:11
KILLED ARITHMETIC_BASE       at calc.go:5:11
KILLED ARITHMETIC_BASE       at calc.go:23:11
KILLED CONDITIONALS_NEGATION at calc.go:10:7
LIVED  CONDITIONALS_BOUNDARY at calc.go:10:7
```

## The one entry that is NOT real, and exactly why

`{"type":"INVERT_NEGATIVES","status":"NOT VIABLE","line":99,"column":1}`
is HAND-ADDED — line 99 does not exist in the 24-line `calc.go` this run
mutated. No real gremlins run against this fixture, or against several
follow-up scratch modules built specifically to try to trigger it
(including a package-level `var` initializer that panics at load time, and
an `ARITHMETIC_BASE` mutation on STRING CONCATENATION — `"a" + "b"` mutated
to the invalid `"a" - "b"`, which a Go compiler genuinely rejects), ever
produced a `NOT VIABLE` entry.

This is a real, verified fact about gremlins v0.6.0 on Go 1.26.2, not a
gap in how this fixture was captured:

- `gremlins`'s own source
  (`internal/engine/executor.go`'s `getTestFailedStatus`) maps the `go
  test <pkg>` subprocess's exit code to a mutant status: `1 → KILLED`,
  `2 → NOT VIABLE`, anything else → `LIVED`.
- On this Go toolchain, `go test <pkg>` — invoked exactly the way
  gremlins invokes it (`go test -timeout <d> -failfast <pkg>`) — returns
  exit status **1** for EVERY failure mode tried: a syntax error, a type
  error (`invalid operation: operator - not defined on a (variable of
  type string)`), and even an unrecovered `panic()` at package-init time
  (which, run as a bare compiled test binary rather than through the `go`
  command, DOES exit 2 — `go test` itself normalizes that back down to 1
  before returning).
- The practical consequence: a mutation that breaks compilation is
  reported by gremlins v0.6.0 on this toolchain as **KILLED**, not `NOT
  VIABLE` — verified directly by mutating `Concat`'s `a + b` to `a - b`
  and observing gremlins report `KILLED ARITHMETIC_BASE at calc.go:23:11`
  for what a `go build` of that exact file confirms is a compile error.

Per the design (D2) and the plan (P2 point 3 of `plan.md`): when a real
run cannot produce a required state, the fixture is forced and the fact
is documented here rather than silently invented as if it were captured.
The forced entry exists SOLELY so `TestMutantFormats_RegistryContract`
(AC6/G5) has something to parse for the `gremlins` format's contract row —
it is not evidence that gremlins reliably reports `NOT VIABLE` in
practice on this toolchain; the opposite is true, and is recorded as a
finding in this spec's changes document (`spec_doc_write kind=changes`)
and in `docs/quality.md`'s known-limitations section.
