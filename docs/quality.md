# Quality mechanism (SPEC-115 S1 + SPEC-116 S2, EPIC-calidad)

mneme's quality mechanism replaces an agent's self-reported "it works" with
a result mneme itself produced by **executing something**, bound to the
exact commit under review. If one more line is touched, the result no
longer applies — it has to be earned again.

This document covers the **cimiento** (S1): a repository's own declared
constitution, the certificate mneme emits by running it, and the block in
`spec_advance` that requires one — plus **S2** (SPEC-116): the first
comprehension that judges *the work* rather than the process — coverage of
the lines a spec actually added or modified — and the **ratchet**, which
keeps the repository's aggregate coverage from silently regressing. The
four specs that remain in the EPIC (`EPIC-calidad`) build executable
acceptance criteria, budget/graph checks (absorbing `lane audit`), mutation
testing, and visual verification **on top of this same registry** — none of
that is implemented here (see "What this does NOT do" below).

## The problem this solves

Today, the evidence that an implementation is correct is a sentence an
agent writes in its final report. An agent that did the work properly and
one that wrote a test incapable of ever failing produce the exact same
green report. There is nothing in that sentence a human can verify without
re-reading the diff themselves.

The fix: mneme runs the project's own declared build/test commands itself,
records the exit code, the output, and the exact commit, and persists that
as a **certificate**. `spec_advance` then only ever asks "does a green
certificate exist for this exact commit?" — a cheap database read, never a
re-execution.

## The constitution: `.mneme/quality.toml`

A repository opts in by committing `.mneme/quality.toml` — versioned,
revisable in PR, like every other governance file in this repo. `mneme init`
materializes a starter template (see "Materialization" below); the project
declares its own gates and, when ready, flips `enabled = true` in its own
commit.

```toml
schema_version = 2
enabled = false

[execution]
output_tail_bytes = 4096

[[gate]]
name = "build"
command = ["make", "build"]
timeout = "5m"
required = true

[[gate]]
name = "test"
command = ["make", "test"]
timeout = "20m"
required = true

[coverage]
enabled = false
format = "go-cover"
command = ["make", "coverage"]
profile_path = "tmp/coverage.out"
timeout = "20m"
min_diff_line_pct = 80.0
min_changed_lines = 5
exclude = []

[ratchet]
enabled = false
max_global_line_pct_drop = 0.0
max_baseline_staleness_pct = 1.0
```

- **`schema_version` accepts `{1, 2}`, never narrower.** A schema-1
  document (no `[coverage]`/`[ratchet]`) keeps parsing exactly as it always
  did — S2 is additive, not a breaking migration. Declaring `[coverage]` or
  `[ratchet]` under `schema_version = 1` is an explicit error naming the fix
  ("bump schema_version to 2"), never a silent tolerance. Every future spec
  in this EPIC widens the accepted set further; **none may narrow it** —
  narrowing turns every existing constitution in the world into an instant
  block.
- **`enabled`** is the interruptor. While `false` (materialized default),
  nothing in this file blocks anything — `mneme init` never turns a repo
  into a project with a live quality gate nobody asked for. `[coverage]`
  and `[ratchet]` have their OWN independent `enabled` switches — the
  aggregate/delta coverage mechanism can be on while gates are unconditional
  (they always run), and `[ratchet].enabled = true` REQUIRES
  `[coverage].enabled = true` (`Parse` rejects the inconsistent
  combination outright — the ratchet feeds off the same profile).
- **No defaults anywhere in the binary.** Every key above is required;
  `quality.Parse` rejects a missing key by NAME, and rejects any key it does
  not recognise (`DisallowUnknownFields`) — a typo must explode, not
  silently govern nothing. This applies identically to `[coverage]`/
  `[ratchet]`: a schema-2 document declaring either section must declare it
  **completely**.
- **`command` is an argv vector, never a shell string.** `["make", "test"]`
  is executed via `exec.CommandContext(argv[0], argv[1:]...)` directly — no
  `sh -c`, ever. Portable to Windows, no quoting ambiguity, nothing for a
  security scanner to flag. `command = ["make test"]` (one element with a
  space) is rejected with a message explaining the argv-per-element rule.
  The environment is inherited **verbatim** — mneme adds nothing, removes
  nothing; the project's own sandboxing (e.g. this repo's own HOME
  isolation, SPEC-085) is entirely its own business.
- **`output_tail_bytes`** (1–65536) bounds how much of a gate's combined
  stdout+stderr is *retained* in the certificate. The full stream is still
  **hashed in streaming** regardless of this bound — the fingerprint is
  honest even for gigabytes of output, and the process never runs out of
  memory holding it.
- **The file declares only what runs.** No coverage thresholds, no AC
  vocabulary, no budget — those belong to S2..S6, each adding its own
  section and bumping `schema_version` when it lands. A number in this file
  that nothing reads would be the same invisible doctrine D13 of the grill
  already ruled out, just inverted.

## What gets checked, and in what order

Every `mneme quality verify <SPEC-ID>` run assembles, in this order:

1. **Tree check** (`kind=tree`, `name=clean-worktree`) — `git status
   --porcelain --untracked-files=normal`. **Untracked files count as
   dirty.** An implementer who creates a new file and forgets `git add`
   must not get a certificate that claims to cover work the commit does not
   actually contain. Files ignored by `.gitignore` never appear in this
   output, so they never count.
2. **Three constitution checks** (`kind=constitution`):
   - `tracked` — is `.mneme/quality.toml` itself committed? If not: a
     `finding` (D9 of the grill: a constitution that is not versioned is
     not revisable in PR, which is half the point of putting it in the
     repo at all).
   - `unchanged-in-range` — did the constitution change between the spec's
     `base_sha` and HEAD? If yes: a `finding` with both hashes in `detail`.
     This covers modification **and deletion**.
   - `hash` — an informative `pass` check recording the constitution's
     sha256 in the certificate.
3. **Every declared gate**, sequentially, in declared order. The first
   `required` gate that fails **stops the run** — the rest are recorded
   `skipped` (present in the registry, never silently omitted) rather than
   executed for nothing.

The certificate's overall **verdict** is *derived*, never set directly:

```
any check status == fail      → verdict = fail
else any un-acked "finding"   → verdict = findings
else                          → verdict = pass
```

`acked` and `skipped` never degrade the verdict. A certificate with **zero**
checks is `fail` — a certificate that verified nothing is not a green
certificate, it is an absence of evidence, and treating absence as success
is exactly the dishonest report this whole mechanism exists to eliminate.

## S2: coverage of the diff, and the ratchet (SPEC-116)

S1's gates prove the SUITE passes. They do not prove the suite **looked
at** the new code — an agent that adds 400 lines with zero tests produces
the exact same green `make test` a careful implementer does. S2 closes
that concrete lie: after the gates run, seven MORE rows land in the SAME
`quality_checks` table, under two new `kind`s (`coverage`, `ratchet`) —
**no migration, no new column**. This is the first real proof the S1
registry can absorb a whole new spec's worth of checks without a schema
change.

### One execution, two measurements

The declared `[coverage].command` runs **once** per `mneme quality verify`.
Its output feeds BOTH halves at the same time:

- restricted to the lines the spec actually **added or modified** →
  `coverage/diff-lines` (the delta a human should read the diff for);
- aggregated over the WHOLE repository → the ratchet's `global-line-pct`
  and its staleness check, further down.

This is not an optimization — it is what makes the ratchet free to compute
and therefore viable at all (see "The ratchet" below).

### The two profile formats: LCOV and go-cover

`[coverage].format` is a **declared**, closed-set key (`"lcov"` |
`"go-cover"`) — never sniffed from the file's bytes. Guessing wrong from
content is exactly the kind of failure that produces zero matched files,
which reads as a 100% percentage on an empty denominator — the single
riskiest failure mode in this entire spec (see "The empty-denominator
trap" below).

- **LCOV** is the lingua franca most non-Go ecosystems already produce.
  mneme reads `SF:`/`DA:<line>,<hits>`/`end_of_record`; every other
  directive (`FN:`, `FNDA:`, `BRDA:`, `LF:`, `LH:`, `TN:`, `BRF:`, …) is
  ignored **without error** — a real-world LCOV file mixes tool-specific
  dialects, and rejecting them would be fragile against a producer mneme
  does not control. The producer's OWN summary counters (`LF:`/`LH:`) are
  **never read** — they can lie or drift, so the per-line `DA:` records are
  recomputed independently every time.
- **go-cover** is Go's own native `go test -coverprofile` format — an
  explicit exception to "stay agnostic of the ecosystem", approved because
  Go is mneme's own implementation language and dogfooding its own coverage
  needs to read its own toolchain's output directly. Its first line
  (`mode: set|count|atomic`) is **mandatory**; its absence is exactly the
  signal that catches an LCOV file mistakenly declared `go-cover`.
  **Approximation, stated plainly:** Go's coverage unit is a BLOCK spanning
  a line range, not a single line — every line in the block is marked with
  its count, matching the ecosystem's standard converters. Blank lines and
  comments inside a covered block therefore count as covered. This is
  accepted so that **the same repository produces the same percentage
  whether measured via LCOV conversion or this native profile** — the
  alternative (marking only the block's first line) would drastically
  undervalue any multi-line function body.
- **The shared rule, declared once, applied to both:** a line is
  *instrumented* if any record mentions it; it is *covered* if **any**
  record that mentions it declares `hits > 0` — never a sum, never "the
  last record wins". This single rule is what makes merged LCOV tracefiles
  (several `DA:` for the same line) and overlapping go-cover blocks behave
  correctly without special-casing either format.
- Adding a THIRD format is additive: a type implementing `ProfileParser`
  plus one entry in `internal/quality`'s registry (the same registry shape
  `internal/codegraph/extractor.go` already uses for language extractors).
  `Formats()` is the single source of truth `Parse` validates the `format`
  key against — never a second, parallel literal list that could drift.

### Extracting the diff: merge-base, not the raw base commit

`Git.ChangedLines(fromSHA, toRef)` runs `git diff --unified=0` with every
flag that a repository's or a user's own `.gitconfig` could otherwise
change the parsed text of, fixed explicitly on the command line:
`core.quotePath=false` (a non-ASCII filename is never quoted/escaped),
`--src-prefix=a/ --dst-prefix=b/` (defeats `diff.noprefix`), `--no-ext-diff`
(defeats `diff.external`), and `-M` (rename detection stays ON regardless
of `diff.renames` — without it a pure rename looks like a full delete plus
a full add, demanding fresh coverage of a file nobody actually touched).

The range is computed from **`MergeBase(spec.BaseSHA, HEAD)`**, not from
`spec.BaseSHA` directly. In the common, linear case this is a no-op — the
merge-base of an ancestor and its descendant IS that ancestor, so nothing
changes. It matters when `BaseSHA` is not (or is no longer) a true ancestor
of HEAD: a raw two-dot diff against a non-ancestor commit compares two
trees directly, with no regard for shared history, and can attribute
completely unrelated content to a spec that never touched it. Anchoring on
the merge-base is never worse than the raw commit, and is sometimes the
only correct answer. (`Git.PathChangedInRange`, the constitution's own
tamper check from S1, still uses a raw two-dot range — that primitive is
intentionally untouched here; unifying it is a separate item, BL-172.)

### Reconciling paths: `NormalizeSourcePath`

A coverage profile's own paths rarely match git's repo-relative form
exactly — go-cover's native output carries the full module import path
(`github.com/wirvii/mneme/internal/quality/git.go`), and some producers
emit absolute paths. `NormalizeSourcePath` reconciles a raw profile path
against the set of files git actually reports changed, by progressively
shrinking the raw path from the front and requiring an EXACT match (or a
`/`-bounded suffix match) at each length — the first length with **exactly
one** match wins.

**A sufix that matches more than one file does NOT match at all.** Picking
"the first" of an ambiguous match is exactly how a mechanism starts
silently misattributing coverage to the wrong file — a wrong answer is
worse than no answer here.

### The empty-denominator trap (`coverage/changed-files-in-profile`)

If path reconciliation is broken — the producer emits paths in a form
mneme cannot map at all — **no changed file appears in the profile**, the
diff-coverage denominator becomes 0, and a 0/0 ratio reads as **100%**: a
green certificate with the mechanism entirely dead. This is the same shape
of scar as SPEC-087 AC12, and it is treated with the same seriousness.

`coverage/changed-files-in-profile` catches it deterministically: if at
least one non-excluded changed file exists AND the profile has at least
one file AND their intersection is empty → a `finding`. Not a `skipped`
(that would BE the silent green) and not a `fail` (mneme cannot tell "this
spec only touched docs/SQL/markdown" from "the path mapping is broken"
apart — that would require ecosystem knowledge mneme deliberately does not
have). A `finding` puts the ambiguity in front of a human, who can.

### The seven new rows

| # | `kind` | `name` | Asserts |
|---|---|---|---|
| 1 | `coverage` | `profile` | the declared command ran and produced a parseable, non-empty profile in the declared format |
| 2 | `coverage` | `changed-files-in-profile` | at least one changed file appears in the profile |
| 3 | `coverage` | `diff-lines` | coverage of the added/modified lines meets `min_diff_line_pct` |
| 4 | `ratchet` | `baseline-integrity` | the registered baseline was not weakened or deleted within this spec's commit range |
| 5 | `ratchet` | `baseline-comparable` | the baseline was measured on an ancestor of HEAD, under the SAME measurement scope |
| 6 | `ratchet` | `global-line-pct` | the repository's aggregate coverage has not dropped past the declared tolerance |
| 7 | `ratchet` | `baseline-stale` | the registered mark still describes the repository, within a declared margin |

**One row per ASSERTION, never per topic** — `AckCheck` signs exactly one
row. If the three baseline assertions shared a row, acking "yes, I moved
the mark on purpose" would also silently ack "yes, I measured it on the
right branch" and "yes, I know it's stale" — three different remedies
sharing one signature is exactly how a signature stops meaning anything.

### `coverage/profile`: the destructive step

`[coverage].profile_path` is treated as an OUTPUT of `[coverage].command`,
never an input mneme trusts blindly. Before running the command, mneme
**deletes any pre-existing file at that path** — the first destructive
effect this mechanism has ever had on a working tree, and it is guarded
accordingly:

1. The path is already validated relative, `..`-free, by `Parse`.
2. mneme refuses to delete a **directory**.
3. mneme refuses to delete a path **tracked by git** — checked with `git
   ls-files --error-unmatch` BEFORE any deletion is attempted. A tracked
   profile means the constitution itself is misconfigured (the profile
   belongs in `.gitignore`, being a command's OUTPUT); the check fails
   with that exact message, and the file is left untouched.
4. Only after all three hold does the delete happen, and its error IS
   handled explicitly — `.golangci.yml` excludes `os.Remove` from
   `errcheck`, so the linter will not catch a dropped error here; this was
   handled by hand, not delegated to tooling.

Without this delete, a stale profile left over from a previous run or
branch could produce a certificate that looks green for a commit it was
never measured against — a false pass indistinguishable from a real one
without this guarantee.

### The ratchet: where the baseline comes from, and what it does NOT guarantee

Comparing "did coverage drop" requires the metric at the commit being
compared against — and nobody has it. Three tempting alternatives were
considered and rejected:

1. **Re-run the suite against the base tree.** Doubles the most expensive
   part of an already-minutes-long operation, may not even build (a
   different toolchain state, generated code, migrations), and would
   require mneme to create and clean up a `git worktree` — a class of
   filesystem side effect this mechanism does not otherwise have.
2. **Require a certificate at the base commit.** Certificates live in a
   HOST-level database (`~/.mneme/projects/<slug>.db`) — a teammate, a CI
   runner, or a clean machine has none. The same commit would produce
   different ratchet verdicts depending on WHERE it runs, destroying the
   determinism this whole mechanism exists for. It would also make the
   first spec in any repository's history impossible to satisfy.
3. **Derive it from the current profile alone, without re-running
   anything.** Wrong, not just expensive: if a spec deletes a test file,
   HEAD's profile shows the tested code newly uncovered, but the diff
   against that file is EMPTY (nothing in it changed) — the derivation is
   blind exactly where the ratchet exists to look.

**What mneme does instead:** a REGISTERED baseline, `.mneme/quality-baseline.toml`
— versioned, like the constitution itself — written ONLY by `mneme quality
baseline update <spec-id>`, and only from the `coverage/profile` row's
figures of the spec's LATEST **`pass`** certificate. Nobody types a number
into this file, ever.

```toml
schema_version  = 1
measured_at_sha = "9f3c…"
measured_at     = "2026-08-13T11:04:22Z"
certificate_id  = "0193…"
lines_total     = 41230
lines_covered   = 29066
global_line_pct = 70.50
scope_hash      = "b21f…"
```

**What this guarantees:** the repository's aggregate coverage cannot fall
below a mark mneme itself measured, on an ancestor commit, without someone
signing off in writing; and the mark cannot silently go stale while the
codebase improves around it (see "Staleness" below). **What it does NOT
guarantee:** that a baseline someone else registered came from a
certificate mneme's OWN database can verify — certificates are host-local
(above), so a teammate's baseline is not independently checkable against
this machine's certificate history. The real guarantee is structural, not
cryptographic: every change to the baseline file is visible in a normal
`git diff`, and weakening it costs a signed acknowledgment — the mark is
never silently editable, even though its provenance cannot be re-verified
byte for byte.

### Baseline integrity is DIRECTIONAL (`ratchet/baseline-integrity`)

| Before (at merge-base) | After (at HEAD) | Result | Why |
|---|---|---|---|
| absent | absent | `pass` | nothing to compare |
| absent | present | `pass` | no mark existed to weaken — an honest bootstrap |
| present | **absent** | **`finding`** | deleting it returns the ratchet to `skipped` — the obvious leak |
| present | present, pct **≥** | `pass` | raising the mark only hardens the ratchet — must be free |
| present | present, pct **<** | **`finding`** | weakening the mark mid-spec is the attack |

The asymmetry is the entire point: raising the mark is monotonically safe
and costs nothing; lowering or deleting it costs a signature with a name
and a date. A guardian that flags EVERY change (never letting an
improvement pass for free) is exactly as useless as one that flags NO
change (never catching a real weakening) — both are vacuous in their own
direction, which is why this table's two middle rows are load-bearing, not
decorative.

### `ratchet/baseline-comparable`: is the mark even meaningful right now?

Two independent causes, each a `finding` on its own:

1. **`measured_at_sha` is not an ancestor of HEAD.** A mark measured on a
   sibling branch does not describe this commit's history — comparing
   against it means nothing.
2. **`scope_hash` mismatch.** `ScopeHash(format, exclude)` — sha256 of the
   format plus the sorted, deduplicated exclude patterns. If a PAST spec
   widened `exclude` or switched `format` (which changes what gets
   instrumented at all), the repository's aggregate coverage changed
   without a single test being written, that spec's OWN ratchet check
   passed easily (a wider exclude list only ever raises the visible
   percentage), and the registered mark is now stale **forever** unless
   this check catches it. Widening `exclude` is otherwise a silent,
   permanent way to loosen the ratchet.

### Staleness (`ratchet/baseline-stale`, D17): the margin that closes the loophole

Baseline integrity alone leaves a gap: if coverage improves and nobody
updates the mark, the repository could regress right back down to the OLD
mark without anything firing — a number that does not actually obligate
anyone to anything. `baseline-stale` closes it: when the CURRENT
measurement exceeds the registered mark by more than
`max_baseline_staleness_pct`, it is a `finding`.

- **The margin defaults to `1.0` point.** Below one point is noise (a test
  entering/leaving, a file moving); above it is a real improvement that
  deserves to be registered. `0.0` is deliberately rejected by `Parse` — a
  single covered line in a 40,000-line repo moves the aggregate by
  thousandths of a point, and treating that as a finding would train a
  team to sign without reading.
- **Why a `finding`, never a `fail`:** the remedy is `baseline update`,
  which is a COMMIT — it moves HEAD and invalidates the certificate that
  produced it, forcing a full re-verify (the same minutes-long operation
  S1 already documented as expensive). A `fail` here would impose a
  mandatory double `verify` on every single spec that improves coverage —
  exactly the kind of friction that ends with someone raising the margin
  to 100 just to make the nuisance stop.
- **Why signing it repeatedly does NOT disable it, by construction:**
  1. **No signature covers a future certificate.** Each `verify` recomputes
     the finding fresh against the current measurement; an `ack` is scoped
     to the exact certificate row it was recorded against.
  2. **The number grows.** Each successive finding's `summary` carries a
     bigger gap and an older mark, side by side in the certificate
     history — a repeated finding does not disappear, it visibly
     accumulates.
  3. **The author cannot sign it.** `quality_ack` is denied to subagents
     (same mechanism as every other finding in this document) — a human
     has to do it, by name, every single time.

### `make coverage`, and this repository's own constitution

This repository's `.mneme/quality.toml` is now `schema_version = 2`, with
`[coverage]`/`[ratchet]` fully declared and **both `enabled = false`** —
the same posture S1 shipped: materialize the doctrine so it is read and
revised, never turn on a live block nobody asked for. `format = "go-cover"`,
`command = ["make", "coverage"]`, `profile_path = "tmp/coverage.out"` are
real and already functional; enabling the mechanism is a decision for the
owner, outside this spec's scope. **No `.mneme/quality-baseline.toml` is
committed** — there is no measurement yet, and a file with invented figures
would be the worst possible start for a mechanism whose entire point is
that nobody types a number into it.

```make
coverage:
	@mkdir -p "$(TEST_HOME)"
	$(TEST_ENV) go test -coverprofile=tmp/coverage.out -covermode=count ./...
```

This target **must inherit `$(TEST_ENV)`**, exactly like `test`/`test-race`
— the coverage command IS the entire test suite with instrumentation
turned on, so without the HOME/USERPROFILE sandbox it would write into the
REAL `~/.mneme/projects/*.db` and the real team-memory vault, the SPEC-085
disaster, this time triggered by mneme's own declared command.
`tmp/coverage.out` is ignored three separate times over
(`*.out`/`coverage.*`/`tmp/`) — the requirement behind the destructive
delete above.

### `mneme quality baseline update|show`: CLI-only, deliberately not on MCP

Writing the baseline is an act of governance over a versioned file — the
same class of act as hand-editing `.mneme/quality.toml`, which also has no
MCP tool. Offering `baseline update` to an agent would make "just update
the mark" the path of least resistance the very first time the ratchet is
inconvenient — precisely the failure this whole mechanism is designed to
resist (no bad faith required: a stuck agent "fixing" its own blocker
produces the identical effect). `baseline-integrity` would still surface it
eventually, but not offering the tool at all keeps it from happening in the
first place. **Reading** the baseline has full parity: `quality_status`
reports its path, SHA, date, percentage, and staleness over MCP — an agent
can see the problem and hand it to a human, which is its job.

## S3: executable acceptance criteria (SPEC-117)

The cheapest lie in the whole SDD flow: "the criterion is met" is a
sentence in `spec.md`, and the evidence that it is met is another sentence
in `qa-report.md`, written by an agent. An agent that did the work and one
that did not produce the exact same pair of sentences. S3 replaces that
pair with **a criterion declared in a closed vocabulary mneme itself
understands, evaluated on the tree at HEAD and at the spec's base
commit**, with a report generated from those evaluations instead of
written by hand.

### `criteria.toml`: where it lives, and the honest limit of that choice

The architect writes a spec's criteria in `criteria.toml`, in the spec's
**workflow directory** (`<workflow-dir>/<project>/specs/<ID>/criteria.toml`)
— alongside `spec.md`/`plan.md` — via a fifth `spec_doc_write` kind,
`"criteria"`. This is the architect's **only** write channel: it has no
`Edit`/`Write`/`MultiEdit` (`internal/subagents/permissions.go`), so the
criteria document cannot live in a repo file the architect would need to
create directly, nor can the orchestrator or an implementer write it on
the architect's behalf — that would be the exam graded by the examinee.

**Said plainly, not hidden:** `criteria.toml` is **not versioned**. It does
not appear in the PR's diff; a teammate or a CI checking out the branch
does not have it. This is not a new gap this spec opens — the spec.md and
the certificate itself are already host-side facts (the same limit S1's
own design documents) — but it is bounded by three properties this spec
does guarantee:

1. **The certificate stores the declaration verbatim.** Every criterion
   row's `detail` (JSON) carries its `mode`, `text`, and every assertion
   key exactly as declared — the certificate never just says "14 criteria,
   all green", it says which ones.
2. **The report is generated from the certificate, never from the file**
   (see below) — what a human reads always comes from the registry.
3. **`quality_report` prints the criteria hash the certificate recorded**
   alongside the constitution hash, so a caller who wants to double-check
   can compare it against the current file's hash — a divergence after
   certification is visible, not hidden (see "What this does NOT do (yet)"
   below for the one gap this does not close).

### The closed vocabulary: four verbs, all pure functions of a tree at a ref

Every verb is evaluated against the **git object database directly** —
`git ls-tree` for a file listing, `git grep` for a literal search — never
a checkout, never a `git worktree`, never a build. This is what makes
evaluating a criterion against the spec's base commit as cheap as
evaluating it against HEAD: it is the exact same code, with a different
ref.

| Verb | Keys | Holds when |
|---|---|---|
| `file_exists` | `path`, `new` | `path` exists in the ref's tree |
| `pattern_count` | `contains`, `in`, `word`, `comparator`, `count`, `new` | the number of **lines** containing `contains`, across the files in the ref matching `in`, satisfies `comparator count` |
| `symbol_defined` | `symbol`, `in`, `new` | `symbol` appears as a **whole word** in at least one file matching `in` |
| `symbol_referenced` | `symbol`, `defined_in`, `ignore`, `new` | at least one file **not** matching `defined_in` or `ignore` contains `symbol` as a whole word |

Three deliberate limitations, documented so nobody is surprised by them:

- **`-F`, never a regex.** `contains`/`symbol` are matched as **literal
  strings**. `git grep`'s own regex dialect is not Go's; a misread
  metacharacter would silently produce zero matches, and a criterion
  asserting `comparator == "=="` with `count = 0` reads that as **green**
  — the same family of trap as S2's empty-denominator bug. Expressiveness
  is traded for a result that never depends on which git binary is
  installed.
- **`-w` is the only notion of "identifier".** `symbol_defined`/
  `symbol_referenced` check that the name appears as a whole word in the
  declared scope — a symbol named only in a comment counts. This is
  **not** "the symbol is declared in the language's syntax", and
  `symbol_referenced` is a **file-level** check, not a call-graph edge —
  "the graph says this symbol's only callers are `_test.go`" is precise,
  graph-backed, and belongs to S4.
- **`-c` counts matching LINES, not occurrences.** Two hits on the same
  line count once. `-c` is chosen over `-o` because it exists in every git
  version that matters and because a declared, stable semantics is worth
  more than a precision that depends on the installed binary.

`in`/`defined_in`/`ignore` are **doublestar** globs (the repo's canonical
glob engine, already used by `coverage.exclude`) — git is **never** handed
a pathspec (BL-173): `ls-tree`/`grep` return the whole tree/all matches,
and the glob filter runs in Go, identically for HEAD and for base.

### The rule that gives it teeth: fail in base, or it proves nothing

Borrowed from TDD: for every `mode = "assert"` criterion, mneme evaluates
the same conjunction of assertions **twice** — once at HEAD, once at the
spec's base commit (the **merge-base** of `spec.BaseSHA` and HEAD, never
`BaseSHA` alone — a branch that merged `main` along the way must not
attribute someone else's work to this spec).

| Holds at HEAD | Holds at base | Row | Why |
|---|---|---|---|
| no | — | `fail` | the criterion is not met. Done. |
| yes | **yes** | `finding` `vacuous` | it already held before the work — proves nothing |
| yes | no | `pass` | the criterion distinguishes before from after |

**Vacuous is a firmable finding, not a fail — on purpose.** A legitimate,
common category exists: the **regression guardian**. "The trivial lane
does not change" or "`ensureCertified` still requires pass" are real,
valuable criteria, and vacuous by construction — their entire value is
that they keep holding. Forcing `fail` here would force deleting
non-regression criteria, which makes the repository worse. With
`finding`, the author either rewrites the criterion so it distinguishes
(the normal case), or **signs** that it is a deliberate regression
guardian (see "Sign" below) — and signing does not silence it: it is
recalculated on every `verify`, scoped to one certificate, and the author
cannot sign their own.

**When vacuity cannot be determined** (no `spec.BaseSHA`, or an
unreachable merge-base — a shallow clone) the row is `finding`
`base-unknown`. **Never `pass`**: "I could not check" and "I checked and
it's fine" must never share a status.

### `new`: the declare-time promise, and what breaks it

Every assertion carries `new` (bool, required): a claim about its
**anchor** (`file_exists`'s `path`, or the glob(s) in `in`/`defined_in`) —
never about the searched content. It means "this anchor does not exist in
the base commit yet".

Checked at **both** ends:

1. **At declare time** (`spec_doc_write` kind `criteria`): if `new =
   false`, the anchor must resolve **today**, against the real working
   tree — a `path` that exists, a glob matching at least one file.
   Otherwise the write is refused, naming the anchor — this is the
   SPEC-087 AC12 scar (`docs/api/mcp.md` named a file that never existed)
   caught at the moment it would be written, not after. `new = true`
   requires nothing: it is a promise to create it.
2. **At `verify` time**: if `new = true`, mneme checks the BASE tree —
   if the anchor already existed there, the row is `finding`
   `anchor-not-new` (a more precise diagnosis than a generic `vacuous`:
   the author declared one thing and the repository says another). And
   the case that escapes (1) — a `new = true` path that is never actually
   created — fails loudly at `verify`: `file_exists` simply does not hold
   at HEAD.

### The `command` escape hatch: always improbable, on purpose

For the rest — anything the four verbs cannot express — a criterion may
declare `mode = "command"`: an argv vector (validated by the exact same
shared validator a gate's own `command` uses, so the rejection message is
byte-identical) run **once, against HEAD only**, through the injected
`Runner` — the same seam gates and `coverage.command` already use, never
a second execution path.

**In base it is never executed.** Running a project's build/test against
an arbitrary older tree would require materializing it (`git worktree add`
+ install + build); the tree might not even build with today's toolchain,
and a build failure there would be indistinguishable from a real
regression. So: **a `command` criterion that passes at HEAD is ALWAYS**
`finding` `vacuity-unprovable` — costing a signature every time. That cost
is deliberate: it is what keeps the closed vocabulary the path of least
resistance instead of the hatch swallowing it within a week.

### The quotas: manual and command, both `fail`, never firmable

`.mneme/quality.toml`'s `[criteria]` table declares two quotas, both a
percentage of the **total declared** criteria (not of the ones that ran):

- `max_manual_pct` — a cap on `mode = "manual"` criteria.
- `max_command_pct` — a cap on `mode = "command"` criteria, a decision
  beyond the original grill (which only names manuals): the escape hatch
  has the exact same degenerate failure mode (14 criteria, 11 `command`,
  a green certificate that never exercised the closed vocabulary at all).

Exceeding either **fails the certificate outright** — never a firmable
finding. The remedy is not a signature: it is rewriting the criteria, and
a firmable quota would be no quota at all. The comparison is strict
(`>`, not `>=`): `max_manual_pct = 25.0` with 4 total criteria admits
**exactly** one manual. Deliberately no floor (no
`min_changed_lines`-style exemption for a small N): a floor would be the
door to "declare 3 criteria and make all 3 manual", the exact degenerate
case the quota exists to prevent. A team that legitimately needs a manual
in a small spec raises the number in its own constitution, in a revisable
commit — the visible act this mechanism wants.

### `sign` and `ack`: disjoint domains, and a rule that fails closed

`mneme quality sign <cert-id> --check <seq> --by <who> --evidence "..."` /
`quality_sign` reuses `store.AckCheck`'s mechanism **verbatim** (the same
three columns, the same in-transaction verdict recalculation) but
**never its verb**. The reason has a real consequence for what a
`COUNT(*)` means:

> `ack` says *"I approve this despite it being a problem"* — an
> **absolution**. `sign` says *"I verified this and it holds"* — an
> **attestation**. Mixing the two into one verb would make a count
> confuse "we forgave 3 findings" with "we verified 4 manuals" — exactly
> the harm S2's own "one row per fact" rule prevents on the other axis.

The domains are disjoint, checked in **both** directions: `Sign` only
accepts rows whose `kind` starts with `"criterion"` (`ErrNotACriterion`
otherwise); `Ack` refuses any row whose `kind` DOES start with
`"criterion"` (`ErrCriterionRequiresSign`, naming `mneme quality sign`).

> **SPEC-119 S5 generalizes this to a single predicate.**
> `quality.RequiresSignature(kind)` now decides both domains, negated —
> a `mutant` survivor row (S5's own equivalent-mutant escotilla) is an
> ATTESTATION exactly like a criterion, and the two verbs' domains can no
> longer independently drift apart. `ErrNotACriterion`/
> `ErrCriterionRequiresSign` are kept as **aliases** of the newly-generic
> `ErrNotSignable`/`ErrRequiresSign`, so every check written against the
> old names still holds. See "S5: mutation over the diff" below for the
> full reasoning.

**The role rule, and how far it really goes.** `internal/cli/hook.go`
gains `roleScopedTools`, a sibling of `lifecycleTools`:
`mcp__mneme__quality_sign` is restricted to the `qa-tester` role for a
subagent caller. Three precisions:

1. **It fails CLOSED when the role cannot be resolved** — the first rule
   in the repo that does. This deliberately breaks SPEC-086's fail-open
   posture for this one tool: a signature whose signer cannot be
   identified is worse than no signature. If Claude Code ever stops
   sending `agent_type`, the qa-tester loses the ability to sign over
   MCP until noticed — the escape hatch is a human, using the CLI (which
   never passes through this hook).
2. **The orchestrator can always sign** — it is not a subagent, and it is
   the human's own channel, the same posture `quality_ack` has had since
   S1.
3. **`--by` is free text, not authenticated.** What is enforceable is
   *who may invoke the tool*, not what they type. What IS deterministic,
   and comes free from S1's own design: **a signature dies with the
   commit** — it lives on a certificate bound to `head_sha`; once HEAD
   moves the certificate goes stale, and re-verifying means re-signing.
   A signature never inherits across commits.

### The generated report

`mneme quality report <SPEC-ID>` / `quality_report` renders the QA report
from the spec's **latest certificate** and writes it via `spec_doc_write`
kind `qa-report` — the same blinded channel, the same registry-derived
path.

- The renderer (`quality.RenderReport`) is **pure**, lives in the leaf,
  and takes its own flat `ReportInput` — never a filesystem path, never
  `criteria.toml`. Changing the file after certifying cannot change what
  the report says (closes D1's second property).
- Deterministic: the same certificate produces byte-identical output —
  ordered by `seq`, no map iteration, no timestamp of "now" anywhere in
  the body (the only date printed is the certificate's own creation
  time).
- **Refuses to overwrite a report it did not generate.** Every rendered
  report carries a literal marker; if `qa-report.md` already exists
  without it, `report` fails (`ErrReportNotGenerated`) unless `--force` —
  `spec_doc_write` overwrites the whole file, so the mechanism's first
  effect must never be silently destroying someone's hand-written report.
- The qa-tester's judgment enters the report **through signatures**, not
  prose: the evidence typed while signing a manual criterion is exactly
  what the report prints — "generated, not authored" made operational.

### The one window this spec leaves open, on purpose

`CertificateUsable` (the four-way conjunction `spec_advance` checks —
verdict, `head_sha`, constitution hash, clean tree) does **not** compare a
hash of `criteria.toml`. So a window exists: certify green, edit
`criteria.toml` (which lives on the host and touches neither the tree nor
HEAD), advance with a certificate that attests different criteria than
the ones now on disk.

Not closed by widening that conjunction — deliberately: it is S1's own
design surface, and S4 revisits it anyway while absorbing `lane_audit`.
The defense that DOES work against the actual attack — weak criteria
rewritten to pass trivially — is already here: a criterion rewritten to
be trivially true at HEAD (`file_exists("README.md")`) is also true at
base, which makes it **vacuous**, which costs a finding and a signature.
That defense works identically before and after certification.

## S4: the budget against the graph, and the absorption of `lane audit` (SPEC-118)

The owner's own original contribution to this EPIC: it converts "escribió
de más" — a feeling a reviewer gets reading a diff — into a number. The
architect declares, in `budget.toml`, how much a change is allowed to
cost against the code graph: a per-directory quota for NEW symbols, a
nominal list of EXISTING symbols the spec may modify, a radius of paths
the change may touch, and a single margin of slack shared by both halves.
`mneme quality verify` computes the REAL delta between the spec's base
commit and HEAD and compares it against that declaration.

### `budget.toml`: where it lives, and what each half declares

Same channel as `criteria.toml` (S3), for the same reason: the architect
is read-only on the repository, so `spec_doc_write` (kind `"budget"`) is
its only write channel. It lives next to `spec.md`/`plan.md`/
`criteria.toml` in the spec's own workflow directory, is **not** versioned
in the repository (the same honest limit S3's own `criteria.toml`
accepted, R9), and its content is written **verbatim** into the
certificate's `budget/declared` row — so a generous or careless budget is
visible in the artifact a human reads, never hidden behind a bare
pass/fail.

```toml
schema_version = 1
margin = 2
radius = ["internal/quality/**", "internal/service/**"]

[[quota]]
dir             = "internal/quality"
max_new_symbols = 18

[[modify]]
file   = "internal/service/quality.go"
symbol = "runAllChecks"

[revision]
by        = "architect"
at        = "2026-08-14T09:12:00Z"
rationale = "the graph-facts wiring needed more symbols than planned"
margin    = 2
  [[revision.quota]]
  dir             = "internal/service"
  max_new_symbols = 14
```

`[[quota]]` is the asymmetric half of D4 of the EPIC grill: what does not
exist yet can only be BOUNDED (a directory, a count), never named.
`[[modify]]` is the other half: what already exists CAN be named exactly,
because the architect read the graph while designing.

### The three layers, in increasing order of what they require

1. **File delta** (`git diff --name-status`/`--numstat`, always available
   with just git) feeds the trivial form's own thresholds and the radius
   check.
2. **Symbol delta** — for every file the layer above touched, `git show
   <ref>:<path>` (no checkout, no worktree, no build) followed by the SAME
   in-process extractor the code graph indexer uses. Extracting a file's
   symbols is a pure function of its bytes, and a symbol's identity is a
   pure function of `(file, qualified name)` — so diffing the two sets by
   key is EXACT, never a heuristic, and requires only the extractor, not
   the indexed graph.
3. **Graph facts** — who calls a symbol, whether a test reaches it,
   whether another symbol shares its name and signature. This is the ONE
   layer that needs the indexed graph (`~/.mneme/projects/<slug>-codegraph.db`),
   and it is the only one degraded when that graph is stale or absent.

Each layer works even when the next one does not: a spec with no indexed
graph still gets its file/symbol-delta arithmetic (`fail`, never silently
skipped); only the six graph-dependent detections degrade to `finding`
`skipped`, naming the remedy.

### Freshness measured by content, never by a timestamp

`mneme codegraph index` (the full-scan command) does **not** stamp
`last_indexed_sha` — only the git-hook-driven incremental reindex does.
A mechanism that judged freshness by that stamp would print a remedy
(`mneme codegraph index`) that does not actually fix the state it just
complained about — the exact scar `docs/HOOKS.md` and SPEC-087 AC12
already describe in a different shape. Instead, for every file the
delta touched and the graph considers indexable, `budget/graph-index`
compares the graph's own recorded `ContentHash` against the sha256 of
that file's blob at HEAD (already in hand from step 2 above) — an exact
comparison, not an inference. A mismatch, an absence, or no graph at all
is a `finding` naming `mneme codegraph index` as the remedy — and that
remedy genuinely fixes it, which is the whole point of not trusting the
stamp.

This freshness check is **only proven for the files the delta touched**
(R4): a stale, unrelated file elsewhere in the graph is not detected, and
the bias this leaves is toward `finding`, never toward a silent pass.

### The eight detections

Two are pure git/budget arithmetic — `fail`, no signature required,
because mneme calculated them directly:

- **unbudgeted** — the single margin pool (below) is exceeded.
- **out-of-radius** — a changed file falls outside the declared `radius`
  (or, for the trivial lane, `scope`). Deliberately **separate** from the
  margin pool: a file outside radius is a design miss, not a quantity to
  forgive, however much slack remains.

Six lean on edges the graph **inferred** — `finding`, always firmable,
never blocking outright (D7 of the EPIC grill), because an inference can
be wrong in ways arithmetic cannot:

| Detection | Condition | Known false positive |
|---|---|---|
| `orphan` | a created, non-test symbol with zero incoming edges | legitimate public API; an interface satisfied structurally (Go never produces an edge for that); a handler registered in a table/switch; a Cobra `RunE` closure |
| `test-only` | a symbol (created or modified) whose only callers all live in files matching `test_globs` | none known — the cleanest of the six |
| `dead` | a preexisting symbol with zero incoming edges at HEAD, whose name WAS referenced in the BASE version of a changed file and no longer is | the same as `orphan`'s, plus a caller the resolver failed to resolve |
| `single-use-indirection` | a created, non-exported symbol with EXACTLY one incoming call | a legitimate extraction for readability |
| `reinvention` | a created function/struct/interface/type_alias (never a `method`) sharing name AND normalised signature with a preexisting symbol in ANOTHER directory | none beyond the excluded case — excluding `method` is what prevents two interface implementations from being flagged against each other |
| `untested-reach` | a created, non-test symbol with no caller within `test_reach_depth` hops living in a test file | a resolver gap; an integration test that reaches it by reflection or HTTP |

`untested-reach` overlaps with, but does not duplicate, S2's diff
coverage: coverage says "these lines executed"; `untested-reach` says
"which test reaches here, and by what path" — and can fire even when
coverage is off, or when the covering path exists but the resolver never
closed it.

Every detection is **one row per detection kind**, never one row per
subject — the full subject list lives in the row's `detail`, the count in
its `summary`. Six orphans cost one signature, not six identical ones.

### The margin: one pool, and why radius never joins it

A created symbol is covered if its directory has a `[[quota]]` with
remaining capacity (imputed deterministically, symbols sorted by key); a
modified symbol is covered if its `(file, symbol)` pair is declared in
`[[modify]]`. What is NOT covered is the excess. `excess <= margin` passes
(with the deviation recorded); `excess > margin` fails the certificate
outright. One pool for both halves, because a spec that overruns its
quota in one package AND touches three undeclared functions overran
TWICE, and two independent margins would only ever say it once.

### The revision: two locks, and why neither is the author's own signature

`[revision]` widens the contract — the architect's signed, after-the-fact
"this excess is justified." Its presence is **always** a firmable
`finding` (never silently absorbed), guarded by two independent locks:

1. **Structural.** `quality_ack` (the only verb that clears a finding) is
   in `lifecycleTools` — denied to every subagent unconditionally. Even an
   implementer who wrote its own `[revision]` cannot make it effective.
2. **Role.** `spec_doc_write` with `kind = "budget"` is restricted to the
   `architect` role (`roleScopedDocKinds`, `internal/cli/hook.go`) — an
   implementer cannot write the ORIGINAL budget either, closing the gap
   lock 1 alone leaves: examining oneself by writing an unrevised,
   generous budget with nothing to sign.

Unlike S3's `quality_sign` (fail-closed when a subagent's role cannot be
resolved), this rule fails **open**: closing it would leave the architect
unable to write any budget at all if `agent_type` ever stopped arriving in
the payload, breaking the common path for every spec, while the document
that slipped through stays subject to lock 1 downstream.

### A renaming limit, stated plainly (R8)

A file **rename** is resolved exactly (`git diff -M`), and a symbol whose
file moved without a body change is `moved`, never created+deleted. A
symbol renamed **within the same file** is indistinguishable from one
deleted and a different one created — this mechanism does not attempt
name-similarity heuristics for that case. It costs one quota slot and one
nominal entry; it is a stated limit, not a hidden defect.

### The absorption of `lane audit`

The trivial lane's own auditor (`mneme lane audit`) now runs on the exact
same engine: `quality.Git` for the delta, `quality.EvaluateTrivialBudget`
for the verdict, replacing the deleted `internal/lane` package's own
duplicated git wrapper and its own (`**`-only) glob matcher. See
[docs/lanes.md](lanes.md) for the two routes (off/absorbed) and the one
deliberate behaviour change (`DefaultBaseRef`'s removal).

### The window this spec leaves open too (D16)

Same shape as S3's own R3/H2: `CertificateUsable` compares the
constitution's hash, but not `budget.toml`'s. The defense that matters is
already in place — the declaration is verbatim in the certificate's
`detail`, and `quality status` reports the disk hash next to the one the
certificate recorded, so a swap after certification is visible, not
silent. A `document-hash` check (one more row, not a new argument to
`CertificateUsable`) is registered as a follow-up (BL), the same shape S3
proposed for its own criteria window.

## S5: mutation over the diff, and the equivalent-mutant escape hatch (SPEC-119)

S1 proves the suite passes. S2 proves the suite **executes** the new lines.
Neither proves the suite **checks** anything: a test that runs a hundred
lines and asserts nothing produces the identical green in both. S5 closes
that with the only evidence that closes it: **change the behaviour of the
new code and see whether a test notices.**

### The decision that matters most: distinguishing a real red from a build failure

A mutation tool that cannot tell "a test's own assertion caught this" apart
from "this simply failed to compile" produces exactly the fabricated
evidence this EPIC exists to eliminate — and the failure mode is not
imprecision, it is **a perfect green**: if every mutation breaks
compilation, there are no survivors, the certificate comes back `pass`, and
the mechanism is dead and green at the same time. This is closed with
**four independent legs**, not one mechanism — each sufficient on its own
for the absence of the others to be noticed:

1. **The premise: the unmutated tree is proven green in THIS SAME
   certificate.** The mutation stage does not evaluate while ANY `gate` row
   is `fail` — not just when a `required` one stops the cascade
   (`gatesStopped`, the condition every earlier stage shares). A project can
   declare its test gate `required = false`; if it is red, every subsequent
   red is unattributable, so mutation's own premise is **stricter**: *zero
   gates in `fail`*, full stop.
2. **The report format itself must be able to EXPRESS "this never
   compiled" — a requirement on the format's VOCABULARY, not a guarantee
   the tool actually EMITS it faithfully at runtime.**
   `MutantReportParser`'s registry contract (`TestMutantFormats_RegistryContract`,
   G5) mechanically refuses to admit a format whose fixture cannot produce
   at least one `not_viable`, one `killed`, one `lived` mutant — a mutator
   whose vocabulary lacks that state ENTIRELY can never be registered. What
   this does **not** guarantee, verified during this spec's own
   implementation and not as a hypothesis: `gremlins` declares `NOT VIABLE`
   in its vocabulary (satisfies the letter of this leg) but, on Go 1.26+,
   never actually emits it — see "A real, verified limitation" below and
   R2 in `spec.md`. The registry contract still closes the worst case (a
   format whose vocabulary could NEVER express non-viability); it does not
   close the case of a format that declares the right vocabulary but whose
   implementation doesn't exercise it faithfully.
3. **The arithmetic never counts `not_viable` as a death.** `quality.Tally`
   never adds a `not_viable` mutant to `Survivors`, and its `ByStatus` count
   is reported, never silently dropped (`mutation/score`'s recount is
   verbatim).
4. **The viability quota (`mutation/viability`) is the guardian that closes
   the loophole the other three legs leave open — PROVIDED the tool
   reports `not_viable` faithfully.** An informe where EVERY in-diff
   mutant is genuinely `not_viable` has **zero survivors** — and would read
   as an unqualified pass without this row. Above `max_not_viable_pct`,
   `mutation/viability` is a `finding`: the certificate cannot be `pass`
   even though nothing "survived". This is the single most important
   guardian in the spec (`TestRunMutationChecks_Viability`'s all-not-viable
   case, verified with the mutation applied and reverted by hand: forcing
   the row to always `pass` makes the all-not-viable certificate come back
   `pass` — exactly the fabricated green this design exists to prevent).
   **This leg only fires when the tool's `not_viable` signal is honest** —
   with `gremlins` on Go 1.26+, verified, it is not: a compile failure
   counts as `killed`, so the measured `not_viable` proportion trends
   toward 0% even while the mutator is silently failing to compile. This
   specific case is NOT caught by this leg with this format on this
   toolchain — see below.

### The mutant vocabulary (`internal/quality/mutants.go`)

A **closed, six-value** set every registered format's raw status is mapped
into: `killed` (the only one that counts as a death), `lived` (a survivor —
its own `mutant/<id>` row), `not_viable` (never a death, never a
survivor — leg 3 above), `not_covered` (informative only), `timed_out`
(neither death nor survival — the suite hung, nobody asserted anything),
`skipped` (counted, nothing else). The registry
(`ParseMutantReport`/`MutantFormats`) is the literal mold of S2's own
`ParseProfile`/`Formats` — a format is a `MutantReportParser`
implementation plus one map entry, nothing else in the package changes.

Two formats ship:

- **`mutants-v1`** — the lingua franca, the role LCOV plays for coverage: a
  minimal JSON any tool in any language can produce with a small adapter,
  strict in both directions (`DisallowUnknownFields`, an exact `schema`
  string, the six-value vocabulary enumerated in every rejection message).
- **`gremlins`** (`go-gremlins/gremlins`) — the native Go entry, chosen
  because its own vocabulary DECLARES `NOT VIABLE` as a first-class state,
  distinct from `KILLED`/`LIVED` — satisfying the LETTER of leg 2 above,
  without mneme having to interpret anything. **Verified during
  implementation that declaring the vocabulary is not the same as
  emitting it faithfully** — see "A real, verified limitation" below.
  `mutate4go` (the tool the doctrine's own anecdote references) does not
  exist as a verifiable public tool; the classic Go mutation testers
  (`go-mutesting` and its forks) do not distinguish non-viability as a
  status of its own AT ALL, which would industrialize exactly the trap
  leg 2 closes — `gremlins` remains the best available choice even with
  its own verified gap, because no evaluated alternative does better.

**The gremlins parser is written against a REAL captured report, never
against the tool's documentation** (`internal/quality/testdata/`, see the
provenance note in `testdata/README.md`): if the tool's real schema ever
disagrees with what its own docs describe, the file wins.

### A real, verified limitation of `gremlins` this dogfooding surfaced — and what it actually costs (BL-178)

**`gremlins` v0.6.0 cannot reliably produce `NOT VIABLE` on a modern Go
toolchain.** Its own source maps the `go test <pkg>` subprocess's exit code
to a status (`1 → KILLED`, `2 → NOT VIABLE`) — but on Go 1.26, `go test`
returns exit status **1** for every failure this project's own testing
observed, including a genuine compile error (a string-concatenation
mutation turned into an invalid `-` operation) and even a raw, unrecovered
panic at package-init time (which, run as a bare compiled test binary
rather than through the `go` command, DOES exit 2 — `go test` itself
normalizes that back down to 1 before returning). The practical
consequence: on this toolchain, a mutation that breaks compilation is
reported by gremlins as **`KILLED`**, not `NOT VIABLE`. The captured
`testdata/gremlins-v0.6.0.json` fixture's one `NOT VIABLE` entry is
hand-forced for exactly this reason — documented, never silently invented
as if it were captured. This is a real weakness of the CHOSEN TOOL, not of
mneme's own design: mneme's four legs (above) defend against a mutator
whose *vocabulary* cannot express non-viability; they do not — and cannot
— correct a mutator that *mislabels* one real state as another inside its
own vocabulary. A project depending on gremlins for the viability signal on
this class of failure should know this limitation exists.

**Said without softening it, because this is the number someone needs to
decide how much to trust: the effect is an INFLATED mutation score.**
Every mutant that failed to compile counts as a death nobody's test
actually proved, so a weak suite reads as stronger than it is. **This is
not a false red — it is FALSE CONFIDENCE**, which is exactly what this
EPIC exists to close. The registry contract (leg 2 above) still closes the
worst case — a format whose vocabulary could never express non-viability
at all — but it does not close this one: a format that declares the right
vocabulary and simply isn't exercised faithfully by its own tool on this
toolchain. **BL-178** records the four possible ways to close this
remaining gap in a future spec; this spec does not close it, and its own
deliverable should not be read as if it had.

**`gremlins`'s own `--diff <ref>` flag was found unusable in this
dogfooding.** Invoked with `--diff`, every candidate mutation in a package
this spec unambiguously changed came back `SKIPPED` — none evaluated —
even though the equivalent `git diff --merge-base <ref>` run by hand, from
the same working directory, produced a correct and substantial diff. This
is exactly why D3 below treats a mutator's own `--diff` as an
**optimisation, never the boundary of correctness**: mneme re-derives the
in-diff set from its own git primitives regardless of what the mutator's
flag does or does not do, so this tool-specific breakage costs nothing but
a slower run (mutating more than strictly necessary) — it does not corrupt
the certificate.

### The acotado: mneme re-derives the diff, the mutator's own `--diff` is only an optimisation

Same doctrine as S2's diff coverage, extended to a mutation report:

1. `base = MergeBase(spec.BaseSHA, HEAD)` — never `BaseSHA` alone (D8 of S2,
   D5 of S3: a merged-in `main` would misattribute someone else's lines).
2. `changed = ChangedLines(base, HEAD)`.
3. `repoFiles = ListFilesAtRef(HEAD)`, and every mutant's path is
   reconciled through `NormalizeSourcePath` (S2's own function, reused
   **verbatim** — rule 5 still applies: an ambiguous suffix match never
   counts, it is unresolved).
4. **In-diff ⇔ the path normalizes AND the mutant's line is in the changed
   set.** Everything else is counted separately (`outside`/`unresolved`)
   and never judged.

**Zero new git primitives.** The bridge to the mutator is a single
substitution token, `{{BASE_SHA}}` (`ExpandCommand`), replaced with the
merge-base mneme just computed — optional (a command that never mentions
it is passed through unchanged), and the ONLY token accepted: `Parse`
rejects any other `{{...}}` sequence at constitution-parse time, naming it,
so a typo can never travel as an inert literal to the mutator (the same
scar SPEC-087 AC12 left on a shell flag, here on a substitution token).

### The `6 + N` rows (D5), and their severities

| # | `kind`/`name` | What it asserts |
|---|---|---|
| 1 | `mutation`/`report` | the declared command ran, left the report at `report_path`, and it parses in the declared format |
| 2 | `mutation`/`scope` | the base is known **and** at least one mutant lands on a line this spec changed |
| 3 | `mutation`/`viability` | the proportion of in-diff `not_viable` mutants is within the declared quota |
| 4 | `mutation`/`timeouts` | no in-diff mutant timed out |
| 5 | `mutation`/`not-covered` | informative recount of in-diff mutants no test executes at all (D10) |
| 6 | `mutation`/`score` | the full per-status recount, verbatim, plus `max_equivalent` — the row `sign` reads the cupo from |
| 7..6+N | `mutant`/`<file>:<line>:<column>:<mutator>` | one survivor, `finding`, capped at `MaxSurvivorRows` (50) |

Row 1 is a `fail` for a stale/tracked/undeleteable report path (the SAME
`prepareDeclaredOutput` helper S2's own `coverage/profile` uses — extracted
into its own function in its own commit, guarded by S2's existing tests
passing unmodified, with byte-identical wording for coverage's own call
site), for a non-zero exit, or for an unparseable report — and a `finding`
`budget-exceeded`, distinct from a plain non-zero exit, when the declared
`timeout` is exhausted (D12): a timeout is not the observation of a
survivor, the suite simply hung.

Row 2 is `finding` `base-unknown` — never `pass`, never a silent `skipped`
— when `spec.BaseSHA` is empty or its merge-base with HEAD is unreachable;
`finding` `no-mutants-in-diff` when the report has mutants but none land
in the changed set (mutation's own version of S2's empty-denominator trap).

**Not-covered is informative, timed-out is a finding — and the asymmetry
is deliberate (D10).** Every uncovered mutable line reads as a moral
survivor at first glance — but S2 already judges test coverage of the diff
with a number the constitution declares (`min_diff_line_pct`); turning
every one into a finding here would impose a silent, binary-authored 100%
on top of it. A timed-out mutant, by contrast, genuinely changed behaviour
observably (it hung) and nobody asserted anything — neither a death nor a
survival, but not nothing either.

### `MaxSurvivorRows`: a storage cap on the registry, not a quality threshold

Past 50 survivors in one certificate, mneme emits the first 50 (in the
deterministic order below) and fails `mutation/score` outright, naming the
real total. **A certificate with 97 survivors is not signed — it is
rewritten.** The cap lives in the binary, not the constitution, for the
same reason S4's `BudgetedKinds` does: it bounds how much of one row-shape
the registry holds, not a number a project calibrates.

**Survivor order is a contract, not cosmetics** — ascending by
`(file, line, column, mutator)` — because it is what makes the certificate
reproducible across two runs of the SAME report, and what makes the
50-row cap deterministic rather than "whichever 50 happened first".

### The escotilla: equivalent mutants, signed by the `qa-tester`, with a hard cupo

A survivor is a `finding`, never a `fail` — `AckCheck` only ever converts a
`finding` row (V3 of the design), so "a survivor blocks the certificate"
and "the only way out is a signed equivalence" cannot both be expressed
with `status='fail'` without redefining what `fail` has meant across four
specs. The dureza D12 of the grill asks for is delivered on four axes
instead:

| Axis | A graph finding (S4) | A mutant survivor (S5) |
|---|---|---|
| Granularity | one row per **detection** — six orphans, one signature | one row per **mutant** — six survivors, six signatures |
| Who signs | `quality ack` — the orchestrator, on the human's behalf | **`quality sign` — the `qa-tester`**, the role that cannot edit code |
| What is claimed | "I approve this despite it being a problem" | "I read the mutant and **demonstrate** it changes nothing observable" |
| Limit | none | **`max_equivalent`**: past the cupo, `Sign` **refuses** |

**`Sign`'s domain generalizes from S3's own, via one predicate, negated
(D8).** `quality.RequiresSignature(kind)` reports whether a row is an
ATTESTATION (a criterion, or now a `mutant` survivor) rather than an
ABSOLUTION — `Sign` accepts iff it is true, `Ack` accepts iff it is false.
Before this predicate existed, the two verbs each carried their own,
independently-written condition (`Sign` required a `"criterion"` prefix;
`Ack` required its absence) — two assertions that happened to agree and
that nothing forced to keep agreeing. `model.ErrNotACriterion` and
`model.ErrCriterionRequiresSign` are now **aliases** of the newly-generic
`model.ErrNotSignable`/`model.ErrRequiresSign` — every S3 test that checks
`errors.Is(err, model.ErrNotACriterion)` still passes, unmodified, because
the two names now resolve to the exact same value.

**The cupo (`max_equivalent`) is absolute, never a percentage** — the same
arithmetic-with-small-N lesson S3 already learned for its own manual quota
— and it is enforced **at signing time**, reading `mutation/score`'s
`detail` from the SAME certificate being signed, never from
`.mneme/quality.toml` on disk: editing the file between certifying and
signing buys not one extra signature. A certificate with no
`mutation/score` row at all refuses ANY `mutant` signature outright — the
absence of a recorded cupo is never read as "unlimited".

### Cost, and why `[mutation]` is the last stage

A mutator runs the covering test(s) **once per mutant** — the most
expensive check in the EPIC by a wide margin. It is bounded three ways:
mutating only the diff (above), evaluating **once per spec** (`verify`
emits, `spec_advance` only ever compares — the same two-verb split every
earlier stage shares), and the declared `timeout`. When `budget-exceeded`
becomes routine, the fix documented here explicitly is **not** raising the
number — it is narrowing the mutator's scope, speeding up the suite, or
accepting the spec was too large.

### The limits, said before QA finds them (D18)

1. **mneme never attributes a kill to a specific test.** The certificate's
   claim is exactly this, no more: *"behaviour changed on this line, and
   some test in the project went from green to red running it — not that
   it did not compile, not that it timed out."* Every survivor's `detail`
   keeps the mutant verbatim (file/line/column/mutator) so a human can
   audit any of them.
2. **Mutant equivalence is undecidable in general** — the escotilla exists
   because of this, with a signature, evidence, and a hard cupo.
3. **mneme does not implement mutators** (a standing rule of this EPIC). A
   language without one declares `enabled = false`; a mutator whose
   vocabulary cannot separate non-viability is not enchufable at all.
4. **The acotado depends on `spec.BaseSHA`.** Without it, `mutation/scope`
   is `finding` `base-unknown` — never a `pass`.

## S6: declarative visual verification (SPEC-120)

The last check this EPIC closes: a gate says the build passed and the
suite passed, coverage says the new lines executed, mutation says
something noticed when they changed — and **none of the three ever looks
at the screen.** A component can compile, be covered, and kill mutants,
and still throw an uncaught exception in dark mode at 360px. `[visual]`
adds that missing check as a **sixth (fifth, if S4/S5 have not landed yet
on a given branch — the order between stages never changes an
assertion, D16) tramo** of the same `runAllChecks` pipeline S1 built.

### The one decision that matters most: mneme does not supervise a server

A "visual harness" usually means a dev server that starts, a browser that
drives it, and a shutdown at the end — a **long-lived process**, not a
command that terminates like every gate the `Runner` (`internal/quality/
runner.go`, untouched by this spec) has ever executed. The obvious design
— `serve_command` + `ready_url` + polling + a supervised shutdown — is
**not what this spec does**, and the reason is structural, not a
preference:

`exec.CommandContext` (the primitive `ExecRunner.Run` already uses) kills
**the direct child only**. Killing a whole process tree portably requires
`syscall.SysProcAttr.Setpgid` on Unix or Job Objects on Windows — which
means per-OS files or build tags, exactly what this repository's posture
forbids (`CLAUDE.md`: OS branches are `runtime.GOOS` checked inline, never
`_windows.go` files). `GOOS=windows go build ./...` staying green is the
standing proof this premise holds.

So `[visual].command` is executed **exactly like a gate**: the same
argv-no-shell `Runner`, the same `timeout`, the same exit code. **The
entire server lifecycle — start, wait until ready, drive the browser,
shut down — belongs to the declared command.** mneme only waits for it to
terminate. The honest residue: if the timeout is hit, mneme kills the
process it launched, not its descendants, and a dev server can be left
holding a port. Mitigated three ways: the report and every declared
capture are deleted **before** the command runs (so a zombie can never
produce a stale, green-looking artifact), the timeout is calibrated by the
project, and this paragraph is the disclosure — not a hidden limitation.

### Why `visual-v1` is the ONLY registered format, unlike S2 and S5

S2 registered `lcov`/`go-cover` and S5 registered `gremlins` for the same
reason: without a native format, **this repository itself** could never
exercise the coverage/mutation chain at all. That argument does not apply
here. The gap S6 cannot close is not a format — it is that **mneme itself
has no graphical interface** (it has a TUI, Bubble Tea). Adding a native
browser-test-runner parser would not fix that, and a native runner's own
report is a *test-result* report anyway: it does not carry console output
or accessibility violations as first-class data, so any harness ends up
emitting `visual-v1` through a small reporter regardless. `visual-v1` is
therefore the lingua franca and the registry's only member
(`internal/quality/visual.go`'s `visualRegistry`) — adding a second format
the day a project needs one is additive, never a redesign.

### The `visual-v1` schema, and a minimal reporter

```json
{
  "schema": "visual-v1",
  "harness": "playwright-toy",
  "harness_version": "0.1.0",
  "targets": [
    {
      "id": "home-light-360",
      "rendered": true,
      "error": "",
      "page_errors": [],
      "console": {"error": 0, "warning": 1, "info": 3},
      "a11y": {
        "engine": "axe-core",
        "engine_version": "4.9.1",
        "violations": [
          {"rule": "color-contrast", "impact": "serious", "nodes": 2}
        ]
      }
    }
  ]
}
```

- `id` is an **opaque** string mneme never interprets — a route, a UI
  state, a theme, a width are all doctrine of the *project* (a skill like
  `wirvii-ui-premium` can require its own four states; mneme never does).
- `rendered`/`error` are complementary in **both** directions: `false`
  requires a non-empty `error`; `true` forbids one.
- `page_errors` (uncaught exceptions / unhandled promise rejections) is a
  **fact**: it fails `visual/console` unconditionally.
- `console` counts are always recorded; `console.error` only degrades the
  verdict when the project declares `fail_on_console_error = true`.
- `a11y` is **optional per target** — its absence (`Reported == false` in
  the parsed model) is a distinct, meaningful state from "present with zero
  violations", because D6's own "declared and not measured is `fail`" rule
  depends on telling the two apart.

A ~20-line Node reporter (the shape any Playwright/Cypress/Puppeteer
harness writes at the end of its own run) is enough to emit this:

```js
const fs = require('fs');
const report = { schema: 'visual-v1', harness: 'toy', harness_version: '0.1.0', targets: [] };
for (const t of collectedTargets) {           // whatever the harness already tracked
  report.targets.push({
    id: t.id, rendered: t.rendered, error: t.error ?? '',
    page_errors: t.pageErrors, console: t.consoleCounts,
    ...(t.a11y ? { a11y: t.a11y } : {}),
  });
}
fs.writeFileSync(process.env.VISUAL_REPORT_PATH, JSON.stringify(report));
```

### The seven rows, plus one per failing target

| # | `kind` | `name` | Asserts |
|---|---|---|---|
| 1 | `visual` | `report` | the declared command ran, left the report at `report_path`, and it parses in the declared format |
| 2 | `visual` | `scope` | every declared target is covered (and none extra, undeclared) |
| 3 | `visual` | `render` | every reported target rendered |
| 4 | `visual` | `console` | no uncaught exception anywhere (and, if declared, no `console.error`) |
| 5 | `visual` | `a11y` | no violation of a declared impact, and no target left unmeasured when impacts are declared |
| 6 | `visual` | `compare` | every captured screenshot matches its reference within `max_diff_pct` |
| 7 | `visual` | `reference-drift` | no reference image changed within the spec's own commit range |
| 8..7+N | `visual-target` | `<id>` | one failing target, with **every** reason it failed |

Nothing here touches `DeriveVerdict`/`CertificateUsable`
(`internal/quality/verdict.go`) or `ensureCertified`
(`internal/service/sdd.go`) — the block is the same one every prior
mechanism already gets for free: any `fail` row wins, any un-acked
`finding` degrades to `findings`.

### The empty-denominator trap, closed twice (D3)

A declared-but-unverified target is the same "green report that proves
nothing" this whole EPIC exists to close, so it is closed in **two**
independent places: `Parse` rejects `enabled = true` with `targets = []`
outright (naming the key); at verify time, `visual/scope` is `fail` when a
declared target is **missing** from the report, and `finding`
`target-drift` when the report has one **extra** — never silently
tolerated either way.

### Console: the fact, and the opinion (D5)

"Zero console messages" cannot be required on a real app without false
positives — framework dev-mode notices, a missing favicon, third-party
deprecations. A gate that is red on day one gets turned off, or signed
without reading (the pattern this EPIC's grill rejected three times). So
this splits, deliberately:

- **An uncaught exception or unhandled promise rejection is a FACT.**
  `visual/console` fails, always, with no knob.
- **`console.error` is an OPINION**, gated by the REQUIRED
  `fail_on_console_error` key — no binary default. mneme keeps **no**
  exclusion list; a project that needs to filter third-party noise does it
  in its own harness, which is reviewable code, not a list buried in the
  binary.

### Accessibility: measured always, blocking only when declared (D6)

`a11y_fail_impacts` is a REQUIRED key whose value may legitimately be
empty — "measured and recorded, never blocking" is an explicit choice, not
an omission. Engine and version are always recorded in `visual/a11y`'s
`detail`, because the day an automatic tool update silently changes a
verdict, that is the one fact that explains why. And the rule that closes
the other half of the trap: **a target with no `a11y` block at all, when
`a11y_fail_impacts` is non-empty, is `fail`** — a check that was never run
must never look like one that passed.

### `visual.compare`: the optional pixel-comparison tier (D7)

The capture is the harness's job; **the comparison is mneme's**, in pure
Go, stdlib only (`image/png`) — `internal/quality/pixel.go`'s `ComparePNG`.
Dimension mismatch is `fail` with **no** invented percentage.
`png.DecodeConfig` (header only) runs before `png.Decode`, bounded by
`MaxComparePixels`, so a hostile or merely corrupt PNG declaring absurd
dimensions cannot make `quality verify` allocate gigabytes of pixel
buffer. The threshold comparison is **strict** (`>`): with the tolerance
declared exactly at the measured difference, it passes.

### References: who approves the first one, and how (D8)

**mneme never writes a reference.** The first capture is approved by a
human with `cp <capture> <reference_dir>/<id>.png && git add && git
commit` — a normal, reviewable action that shows up in the PR as a new
binary someone looks at. A missing reference is a **grouped** `finding`
`reference-missing` (one row, not one per target) — it costs a signature
and is not a permanent block. A verb like `quality visual accept` was
considered and rejected: it would add surface (a command that writes
versioned files) to automate a `cp` + `git add`, erasing exactly the human
review moment the reference needs.

**A reference changed inside the spec's own commit range is a `finding`
`reference-changed-in-range`**, never silently accepted — the same
doctrine S1 already applies to its own constitution (a change that must be
visible, not a change that is forbidden). The primitive behind it,
`Git.ChangedFilePathsInRange` (`internal/quality/git.go`), exists for one
verifiable reason: `ChangedLines`/`ParseUnifiedDiff` only index a file when
they see a `+++ b/<path>` line followed by an `@@` hunk — a **modified
binary file produces neither** (git instead prints `Binary files … differ`),
so a changed PNG can never appear in `ChangedLines`' result. Computing
reference drift from `ChangedLines` would be the empty-denominator trap in
image form: a finding that can structurally never fire. The primitive is
anchored on `MergeBase(spec.BaseSHA, "HEAD")`, never the raw `base_sha` —
the same correction S2/S3/S4 already established for their own range
comparisons.

*(Naming note for anyone diffing this repository's history against the
design: `internal/quality/git.go` already had a function named
`ChangedFilesInRange` — added by S4/SPEC-118, with rename-detection ON and
a richer `[]FileChange` result, for the unrelated budget mechanism. This
spec's own primitive needed the OPPOSITE semantics — rename detection OFF,
so a renamed reference lists both its old and new path as drift — and a
plain `[]string` result, so it is named `ChangedFilePathsInRange` instead.
Same substance, different name, discovered as a real collision during
implementation rather than assumed at design time.)*

### `MaxVisualTargetRows`: the second cap of this shape

`MaxVisualTargetRows = 50` (`internal/quality/visualscope.go`) bounds how
many `visual-target/<id>` rows one certificate emits — a storage cap on
the *registry*, never a quality threshold a project tunes, the same
distinction `MaxSurvivorRows` (S5) already draws. Past the cap, the first
50 (ascending by id, deterministic) are emitted and `visual/render`'s own
summary names the real total — regardless of whether the overflow actually
came from rendering, console, accessibility, or pixel comparison, since
that row is where D10 puts the note.

### The two firmable findings are `ack`, never `sign`

`reference-missing` and `reference-changed-in-range` are governance calls —
"I accept there is no reference yet" / "I approve this reference update" —
not technical re-verifications a `qa-tester` attests to by reading code.
`RequiresSignature("visual")` and `RequiresSignature("visual-target")` are
both `false`; both kinds are firmed with `quality ack`, never `quality
sign`.

### Dogfooding: honestly impossible here, and what was done instead (D15)

**This repository has no graphical interface** — a TUI (`internal/tui`,
Bubble Tea), not a web UI. Unlike S2 (where the missing native format was
the gap, closed by registering one), there is no equivalent fix here: the
gap is the absence of a screen, and no product decision closes that. A TUI
screenshot harness was considered and rejected — it would be a subproduct
built inside mneme, with a font-rasterisation dependency the module does
not have today, purely to satisfy an acceptance criterion. That is exactly
the "fake work" this EPIC exists to eliminate.

What *was* exercised, honestly:

1. **This repository's own `.mneme/quality.toml` declares `[visual]` and
   `[visual.compare]` complete and switched OFF, with `targets = []`** — a
   declared, deliberate "off", distinct from never having been written at
   all, and exactly the shape D3's own value-conditional validation makes
   possible.
2. **The half that IS mneme's own code runs against real data**: pixel
   comparison runs on real PNGs encoded with `image/png` inside the test
   (exact match, under/over/exactly-at tolerance, mismatched dimensions, a
   corrupt file, a header declaring absurd dimensions); reference drift
   runs on a real git repository with a real binary committed and modified
   — the same fixture that proves `ChangedLines`' blindness from the other
   side.
3. **A full walkthrough with a TOY harness** that emits `visual-v1` over a
   throwaway git repository — the same principle S5 used for its own
   mutation walkthrough (a toy mutator, not `gremlins`, so the walkthrough
   proves the *mechanism*, not a third-party tool). See the changes
   document for the exact commands and output.
4. **A reality check with a real browser harness**, reported with real
   numbers — or an explicit statement that the environment could not
   install one, never silently substituted by (3). See the changes
   document.

The honest limit, stated once more: the first real project that turns this
on will hit friction this repository cannot surface — a declared target
list that needs maintaining, a `command` that has to actually start and
stop a server, a first reference someone has to approve by eye.

## Findings and `ack`: the constitution cannot be quietly weakened

An implementer has write access to the repository and could, without any
bad faith at all (an agent that gets stuck and "fixes the config" produces
the exact same effect), lower a threshold to make its own work pass. The
mitigation is not a lock — it is making the attempt **impossible to hide**:
the constitution's tamper checks above surface as `finding` rows, with the
hash of before and after, in a registry a human reviews before approving.

`mneme quality ack <cert-id> --check <seq> --by <who> --justification "..."`
converts a `finding` into `acked`, recording who approved it, when, and why.
It does **not** re-run anything — it is purely the record of a human's
justified approval. **`quality_ack` is denied to subagents** (see
"Enforcement" below) for the same reason: the author of a change does not
get to absolve their own finding.

## The certificate registry

Two tables, migration 018, in the **same project database** as `specs` and
`lane_audits` — this is deliberately the same family, not a new subsystem:

- `quality_certificates` — one row per `mneme quality verify` run (pass
  **and** fail; nothing is thrown away), keyed by `(project, spec_id)`,
  carrying `head_sha`, `base_sha`, `constitution_hash`, `schema_version`,
  the derived `verdict`, whether the tree was `dirty`, timing, and the
  mneme version that produced it.
- `quality_checks` — every check that went into that certificate: the tree
  check, the three constitution checks, and each gate, each its own row
  with `kind`/`name`/`status`, and for gates the exit code, duration, output
  hash, and a bounded output tail.

`kind` is an **open vocabulary** — **S2 (`coverage`, `ratchet`) already
proves this**: seven new rows landed with zero migrations, zero new
columns, all of their structured data in the existing `detail` (JSON) and
`summary` fields. S3 (criteria), S4 (budget, absorbing `lane_audit` as
`kind=lane-scope`), and S5 (mutation, `kind=mutation`/`kind=mutant`) all
add further `kind` values the exact same way — S5 is the fourth
confirmation in a row that this point of extension holds, and D16 of S1
predicted it by name ("S5 mutación: filas `kind='mutant'`… y el recuento
es un `COUNT(*)`"). S6 (visual) will be the fifth. The verdict derivation
and the `ack`/`sign` mechanism already work for whatever `kind` a future
spec introduces.

## The two verbs, and why they are separate

- **`mneme quality verify <SPEC-ID>`** / `quality_verify` — the expensive
  operation (minutes, since it runs a project's real build+test). **Emits**
  a certificate.
- **`spec_advance`** — never executes anything. A database read plus three
  cheap comparisons (below). **Only ever compares** an already-emitted
  certificate.

Three concrete reasons this is two verbs and not one:

1. `make test` in a repo this size takes minutes — a synchronous MCP call
   of that duration risks the calling client timing out.
2. If the gates fail, an operation that both executes and advances leaves
   the caller with nothing durable to inspect; a persisted red certificate
   can be read and fixed.
3. `spec_advance` is **denied to subagents** (SPEC-087 D5) — an
   implementer could never verify its own work before handing it off if
   verification only ever happened inside `spec_advance`.

`quality_verify` and `quality_status` are **not** denied to subagents —
anyone can ask for the truth; since mneme itself is the one executing,
there is nothing to falsify by asking.

## The `spec_advance` block

When the mechanism is **enabled**, `implementing → qa` and `qa → done` of a
**standard**-lane spec require:

```
usable  ⇔  verdict == "pass"
        ∧  certificate.head_sha == current HEAD
        ∧  certificate.constitution_hash == current constitution hash
        ∧  the worktree is clean right now
```

Every broken conjunction has its own error, because each has a different
fix:

| State | Mechanism | `spec_advance` |
|---|---|---|
| `.mneme/quality.toml` absent | off | passes; informative note |
| Present, `enabled = false` | off | passes; informative note |
| Present, `enabled = true` | **on** | requires a usable certificate |
| Present, **unparseable** | **fails closed** | **blocks** — `ErrInvalidConstitution` |
| Off/absent NOW, but `enabled = true` at the spec's `base_sha` | **ablation** | **blocks** — `ErrConstitutionAblated` |

| Error | Remedy |
|---|---|
| `ErrCertificateMissing` | `mneme quality verify <SPEC-ID>` |
| `ErrCertificateStale` (HEAD moved) | re-verify |
| `ErrCertificateNotGreen` (red gate or un-acked finding) | fix the gate, or `quality_ack` the finding, then re-verify |
| `ErrConstitutionChanged` | re-verify |
| `ErrWorktreeDirty` | commit or discard changes, then re-verify |
| `ErrInvalidConstitution` | fix the file |
| `ErrConstitutionAblated` | the constitution must not be turned off mid-spec |

**Lane trivial is out of scope for S1.** `implementing → audit → done` runs
exactly as it did before this spec — `lane_audit` is untouched. This is a
scoped, temporary gap: S4 (budget) explicitly absorbs `lane_audit` into this
same registry (`kind=lane-scope`); merging the two mechanisms now, with the
registry not yet proven in production, was judged the larger risk.

### A recertification note (R1)

Because the certificate is bound to the EXACT commit, a documentation or
CHANGELOG commit made during QA re-opens `ErrCertificateStale` — the price
of "if one more line moved, it does not count" being taken literally. The
practical mitigation is **process**, not loosening the binding: commit docs
*before* verifying, not after. If this proves genuinely painful in
practice, the fix is to declare fewer or faster gates — never to relax the
SHA binding, which is the entire point of the mechanism.

## Materialization (`mneme init`)

`mneme init` writes `.mneme/quality.toml` when it does not exist — every
key present, `enabled = false` throughout (including `[coverage]` and
`[ratchet]`, both now mandatory sections under `schema_version = 2`), two
example gates commented out (the project declares its own toolchain; mneme
does not guess it) — `[coverage]`/`[ratchet]`, by contrast, cannot be left
commented out (the schema requires them complete), so their values are a
generic, harmless illustration, never executed while disabled. If the file
already exists, it is **never touched**, regardless of content. If its
`schema_version` is older than the one this mneme understands, an advisory
drift finding is added to the same drift channel `mneme init` already
reports through for `CLAUDE.md` — never written, never blocking.
`--check` never writes anything, in either frontend (CLI or MCP). No
`.mneme/quality-baseline.toml` is ever materialized by `init` — there is
nothing to measure yet.

## Enforcement: `quality_ack` denied to subagents; `quality_sign` scoped to qa-tester

`mcp__mneme__quality_ack` is in `lifecycleTools`
(`internal/cli/hook.go`) alongside `spec_advance`/`spec_quick` — but for a
**different reason**. `spec_advance`/`spec_quick` are denied because the SDD
lifecycle belongs to the orchestrator (SPEC-087 D5). `quality_ack` is
denied because **the author of a change does not get to absolve their own
finding** — the same "the constitution cannot quietly weaken itself"
principle the tamper checks above establish. A human's approval, channelled
through the orchestrator, is the only path.

`mcp__mneme__quality_sign` (SPEC-117 S3) is a **different rule**, in its
own `roleScopedTools` map: it is restricted to the `qa-tester` role, and
fails **CLOSED** when a subagent's role cannot be resolved — see "`sign`
and `ack`: disjoint domains, and a rule that fails closed" above for the
full reasoning.

## Surface: CLI, MCP — HTTP excluded on purpose

- **CLI**: `mneme quality verify|status|ack|sign|report|baseline`
  (`internal/cli/quality.go`) — every subcommand hangs off the SAME
  `quality` command group; the top-level command count stays **42**
  through S2/S3/S4/S5/S6. `quality status` gains a `visual:` summary line
  (format, declared target count, whether nivel-2 comparison is on,
  verified/failed target counts, missing-reference count) in S6, the same
  shape the `mutation:` line already established — no new flag, no new
  subcommand.
- **MCP**: `quality_verify`, `quality_status`, `quality_ack`,
  `quality_sign`, `quality_report` — **zero new tools in S5 or S6**; the
  surface stays at **84 tools**. `quality_status`'s response gains a
  `visual` object (S6) mirroring `mutation`'s own shape;
  `quality_sign`/`quality_ack`'s error mapping gains three sentinels
  (`ErrNotSignable`, `ErrRequiresSign`, `ErrEquivalentQuotaExceeded`) in
  `mapServiceError` — the only line `internal/mcp/handlers.go` gains. S6
  adds **zero** new sentinels: a broken visual report is a `fail` row you
  can query, never a `Verify` call that errors. `internal/mcp/tools.go` is
  untouched by either S5 or S6.
  **`quality_baseline_update` is deliberately NOT an MCP tool** — see
  "`mneme quality baseline update|show`" above.
- **HTTP: excluded, and not as a "gap to close later".** `quality_verify`
  executes commands declared in a repository file; publishing that on a
  network surface — even `localhost` — turns the constitution into a remote
  code-execution vector for anything that can reach the port. See
  [docs/api/http.md](api/http.md) for the full reasoning.

## A note on `[spec.quality_gates]` (BL-170)

`~/.mneme/config.toml` already has a `[spec.quality_gates]` section
(`min_acceptance_criteria`, `require_out_of_scope`, `require_dependencies`,
`max_ambiguous_terms`) documented in `docs/CONFIG.md`. **It is inert** — no
code outside `internal/config` reads any of those four fields; it is
configuration that governs nothing. It is a HOST-level config (one file per
machine, shared by every project on it); this spec's constitution is a
REPO-level file (`.mneme/quality.toml`, one per project, versioned in that
project's own history). Two almost-identically-named things, one alive and
one dead, is exactly the ambiguity an agent resolves wrong at 3am. This spec
does not touch, rename, or remove `[spec.quality_gates]` — deciding whether
to implement it, delete it, or document it as inert is a separate item
(BL-170).

## What this does NOT do

S1 through S6 — gates, coverage/ratchet, executable criteria, the budget
against the graph, mutation over the diff, and now declarative visual
verification — complete the EPIC's original plan (BL-163). None of the six
narrowed the schema range any prior one already parsed. What none of them
do, and what remains explicitly out of scope:

- **mneme does not know what "correct" looks like.** Every mechanism
  measures a project-declared property (a threshold, a criterion, a
  budget, a mutation score, a rendered screen) — it never invents doctrine
  about what a correct program is.
- **mneme does not supervise long-lived processes** (S6 D1) — a project's
  own harness owns starting and stopping a server; mneme only runs a
  command that terminates.
- **mneme does not judge whether a screen is USABLE**, only whether it
  rendered without an uncaught exception, whether declared accessibility
  impacts are absent, and whether a capture matches its reference within a
  declared tolerance (S6 D18).

## See also

- [docs/api/sdd.md](api/sdd.md) — full parameter/response contract for
  `quality_verify`/`quality_status`/`quality_ack`/`quality_sign`/
  `quality_report`, and the `spec_advance` block's error table
- [docs/api/http.md](api/http.md) — the HTTP exclusion, in full
- [docs/init.md](init.md) — the materialization step inside `mneme init`
- [docs/lanes.md](lanes.md) — the trivial-lane auditor this mechanism now
  absorbs (SPEC-118 S4), and the one behaviour change that absorption
  brought
