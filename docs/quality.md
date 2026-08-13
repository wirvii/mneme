# Quality mechanism (SPEC-115, EPIC-calidad S1)

mneme's quality mechanism replaces an agent's self-reported "it works" with
a result mneme itself produced by **executing something**, bound to the
exact commit under review. If one more line is touched, the result no
longer applies — it has to be earned again.

This document covers the **cimiento** (S1): a repository's own declared
constitution, the certificate mneme emits by running it, and the block in
`spec_advance` that requires one. The five specs that follow in the
EPIC (`EPIC-calidad`) build coverage-of-the-diff, executable acceptance
criteria, budget/graph checks (absorbing `lane audit`), mutation testing,
and visual verification **on top of this same registry** — none of that is
implemented here (see "What this does NOT do" below).

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
schema_version = 1
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
```

- **`enabled`** is the interruptor. While `false` (materialized default),
  nothing in this file blocks anything — `mneme init` never turns a repo
  into a project with a live quality gate nobody asked for.
- **No defaults anywhere in the binary.** Every key above is required;
  `quality.Parse` rejects a missing key by NAME, and rejects any key it does
  not recognise (`DisallowUnknownFields`) — a typo must explode, not
  silently govern nothing.
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

`kind` is an **open vocabulary** — S2 (coverage), S3 (criteria), S4
(budget, absorbing `lane_audit` as `kind=lane-scope`), S5 (mutation), and S6
(visual) all add new `kind` values to this same `quality_checks` table
without any schema change. The verdict derivation and the `ack` mechanism
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
key present, `enabled = false`, two example gates commented out (the
project declares its own toolchain; mneme does not guess it). If the file
already exists, it is **never touched**, regardless of content. If its
`schema_version` is older than the one this mneme understands, an advisory
drift finding is added to the same drift channel `mneme init` already
reports through for `CLAUDE.md` — never written, never blocking.
`--check` never writes anything, in either frontend (CLI or MCP).

## Enforcement: `quality_ack` denied to subagents

`mcp__mneme__quality_ack` is in `lifecycleTools`
(`internal/cli/hook.go`) alongside `spec_advance`/`spec_quick` — but for a
**different reason**. `spec_advance`/`spec_quick` are denied because the SDD
lifecycle belongs to the orchestrator (SPEC-087 D5). `quality_ack` is
denied because **the author of a change does not get to absolve their own
finding** — the same "the constitution cannot quietly weaken itself"
principle the tamper checks above establish. A human's approval, channelled
through the orchestrator, is the only path.

## Surface: CLI, MCP — HTTP excluded on purpose

- **CLI**: `mneme quality verify|status|ack` (`internal/cli/quality.go`).
- **MCP**: `quality_verify`, `quality_status`, `quality_ack` — surface
  79 → 82 tools.
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

Explicitly out of scope for S1 — each is its own spec in the EPIC, building
on this exact registry without a schema change:

- **S2 — Coverage of the diff.** Line coverage of NEW code via LCOV, plus a
  ratchet so aggregate metrics can never silently regress.
- **S3 — Executable acceptance criteria.** A closed vocabulary of automated
  checks per AC, with an escape hatch for the rest, and a rule that an AC
  that already passed at the spec's base commit is vacuous and does not
  count.
- **S4 — Budget and graph, absorbing `lane audit`.** The architect's
  declared symbol/impact budget, checked against the real code-graph diff;
  `lane_audit`'s trivial-lane mechanism folds into this same registry.
- **S5 — Mutation testing.** A survived mutant fails the certificate
  outright, with a counted, justified "equivalent mutant" escape hatch.
- **S6 — Visual verification.** Project-declared routes/states/themes
  rendered and checked; an optional, project-declared screenshot-diff tier.

## See also

- [docs/api/sdd.md](api/sdd.md) — full parameter/response contract for
  `quality_verify`/`quality_status`/`quality_ack`, and the `spec_advance`
  block's error table
- [docs/api/http.md](api/http.md) — the HTTP exclusion, in full
- [docs/init.md](init.md) — the materialization step inside `mneme init`
- [docs/lanes.md](lanes.md) — the trivial-lane auditor this mechanism does
  not yet absorb (S4 will)
