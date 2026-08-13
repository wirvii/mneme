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
`kind=lane-scope`), S5 (mutation), and S6 (visual) add further `kind`
values the exact same way. The verdict derivation and the `ack` mechanism
already work for whatever `kind` a future spec introduces.

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
  `quality` command group; the top-level command count stays 42.
- **MCP**: `quality_verify`, `quality_status`, `quality_ack`,
  `quality_sign`, `quality_report` — surface is now 84 tools (79 → 82 in
  S2, zero new; 82 → 84 in S3). `quality_status`'s response gains the
  baseline's path/SHA/date/percentage/staleness fields; `spec_doc_write`'s
  `kind` enum gains `"criteria"`.
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

## What this does NOT do (yet)

Explicitly out of scope for S1+S2+S3+S4 — each remaining spec in the EPIC
builds on this exact registry without a schema change narrowing what came
before:

- **S5 — Mutation testing.** A survived mutant fails the certificate
  outright, with a counted, justified "equivalent mutant" escape hatch.
- **S6 — Visual verification.** Project-declared routes/states/themes
  rendered and checked; an optional, project-declared screenshot-diff tier.

## See also

- [docs/api/sdd.md](api/sdd.md) — full parameter/response contract for
  `quality_verify`/`quality_status`/`quality_ack`/`quality_sign`/
  `quality_report`, and the `spec_advance` block's error table
- [docs/api/http.md](api/http.md) — the HTTP exclusion, in full
- [docs/init.md](init.md) — the materialization step inside `mneme init`
- [docs/lanes.md](lanes.md) — the trivial-lane auditor this mechanism now
  absorbs (SPEC-118 S4), and the one behaviour change that absorption
  brought
