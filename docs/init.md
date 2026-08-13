# mneme init — Process/Architecture Split

`mneme init` has two concerns:

1. **Managed blocks** (idempotent, non-destructive): ensures the global operating
   manual is present in `~/.claude/CLAUDE.md` and injects a minimal pointer block
   into the project's `CLAUDE.md`. Safe to run at any time.

2. **Legacy migration** (destructive, explicit): migrates pre-SDD workflow artifacts
   (`.workflow/`, `.claude/specs/`, legacy backlog files) into the mneme SDD engine.
   Requires `--apply` and a confirmation prompt (or `--yes` for scripted use).

## Managed block contract

The global operating manual is a single versioned block delimited by:

```
<!-- mneme:managed:start v=N -->
...content...
<!-- mneme:managed:end -->
```

Rules:
- Content outside the block is never modified.
- Running the operation twice produces a byte-identical file (idempotent).
- If a legacy `<!-- mneme:protocol:start -->` block exists, it is removed automatically
  as a one-time migration when the managed block is first installed.
- The version in the start marker (`v=N`) is updated whenever the content changes,
  allowing future tooling to detect stale blocks.

## What `mneme init` does

### Default mode (no flags)

1. **Greenfield scaffold**: if `<repo>/CLAUDE.md` does not exist, create it with
   architecture-only H2 headings (`## Stack`, `## Conventions`, `## Module structure`).
   No process content is added — that lives in the global manual.
2. **Global manual**: upsert the managed block in `~/.claude/CLAUDE.md` (safety net;
   `mneme install claude-code` is the primary path for this).
3. **Repo block**: upsert a minimal managed block in `<repo>/CLAUDE.md` pointing to
   the global manual. User prose outside the block is preserved.
4. **Quality constitution** (SPEC-115): materialize `<repo>/.mneme/quality.toml`
   when absent — every key present, commented gate examples, always
   `enabled = false` (materializing the mechanism must never itself start
   blocking `spec_advance` in a repo that never asked for it). If the file
   already exists it is **never touched**, no matter what it contains; if its
   `schema_version` is older than the one this mneme understands, an
   advisory drift finding is added to step 5 below (never written). See
   [docs/quality.md](quality.md).
5. **Drift report**: scan `<repo>/CLAUDE.md` outside the managed block, plus
   the quality constitution's schema_version, and print advisory findings
   (no file is modified by this step).
6. **Legacy plan**: show the legacy migration plan in dry-run mode.

### `--check` mode

Report-only: runs drift detection and shows the legacy plan without writing
anything — including the quality constitution, which is materialized only
outside `--check`.

### `--apply` mode

Executes the destructive legacy migration in addition to the default steps:
- Migrates active legacy artifacts to the SDD backlog/specs.
- Migrates historical artifacts to memory.
- Cleans up legacy directories (`.workflow/`, `.claude/specs/`, etc.).
- Rewrites `CLAUDE.local.md` with the canonical SDD template.

## Drift detection

`DetectDrift` (in `internal/service/drift.go`) scans the file for two advisory
categories:

### (a) Duplicated section

A heading whose text matches one of the canonical sections owned by the global
operating manual. Example finding:

```
CLAUDE.md:42 — duplicates global manual section "session lifecycle"; consider removing (now global)
```

The canonical section list is maintained in `var canonicalSections` in `drift.go`.
It is a whitelist of lowercase heading strings — deterministic, no LLM, no regex.
False positives are acceptable (advisory only).

### (b) Enforcement contradiction

A line containing a phrase that contradicts the enforcement model. Example finding:

```
CLAUDE.md:17 — contradicts enforcement (orchestrator cannot edit code since v1.4.0); remove or rephrase
```

The contradiction phrase list is maintained in `var enforcementContradictions` in
`drift.go`. Matches are case-insensitive substring checks.

**Both categories are advisory.** Exit code is always 0. No file is modified.

## Greenfield scaffold

When `<repo>/CLAUDE.md` does not exist, `mneme init` creates it with:

```markdown
# CLAUDE.md

## Stack
<!-- TODO: describe language, frameworks, major dependencies -->

## Conventions
<!-- TODO: naming, formatting, commit style -->

## Module structure
<!-- TODO: describe packages/directories and their responsibilities -->
```

The file intentionally contains zero process content. A subsequent call to
`UpsertRepoBlock` adds the managed block with the pointer to the global manual.

## MCP tool

The `init` MCP tool exposes the same idempotent managed-block steps.
It accepts `repo_root` (defaults to cwd) and `check` (boolean). The destructive
legacy migration is NOT exposed via MCP — it remains CLI-only behind `--apply`.

---

## API reference

Full contract for `init` (alongside `backlog_*`, `spec_*`, and `lane_*`):
[docs/api/sdd.md](api/sdd.md) →
