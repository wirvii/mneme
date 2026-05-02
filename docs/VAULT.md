# Vault — Filesystem Mirror

mneme can export its SQLite memory database as a directory tree of human-readable Markdown files. This is the **vault** — a one-way mirror from SQLite to the filesystem.

## Why

- **Browse without mneme.** Open the vault in any file manager or Obsidian.
- **Review in git.** `git init` inside the vault and track memory changes over time.
- **No lock-in.** If mneme disappears, the vault remains as plain `.md` files.

## Layout

```
~/.mneme/vaults/
  wirvii-mneme/               # project vault root (slug from git remote)
    .mneme-vault               # JSON marker file
    notes/
      architecture/
        tech-stack.md          # topic_key: architecture/tech-stack
        memory-model.md        # topic_key: architecture/memory-model
      spec/
        SPEC-001-design.md     # topic_key: spec/SPEC-001-design
      _no-topic/
        019ddc45.md            # memory with no topic_key (8-char ID prefix)
  _global/                     # global scope vault
    .mneme-vault
    notes/
      ...
```

`topic_key` segments map directly to directory segments. Unsafe filesystem characters (`: * ? < > | \ "`) are replaced with `_`; spaces become `-`. Each segment is capped at 200 characters.

## Frontmatter

Each `.md` file has YAML frontmatter between `---` delimiters:

```yaml
---
id: 019ddc45-a39b-76da-9ab7-c4546f962418
type: architecture
scope: project
title: "Memory model — types and scopes"
topic_key: architecture/memory-model
project: wirvii/mneme
importance: 0.90
confidence: 0.80
decay_rate: 0.005
created_at: 2026-04-30T02:44:04Z
updated_at: 2026-04-30T20:41:14Z
revision_count: 1
created_by: claude-code
files:
  - internal/model/memory.go
  - README.md
---

<Memory content verbatim>
```

**Always present:** `id`, `type`, `scope`, `title`, `importance`, `confidence`, `decay_rate`, `created_at`, `updated_at`, `revision_count`.

**Present when non-empty:** `topic_key`, `project`, `created_by`, `files`, `superseded_by`.

**Rule memories only:** `applies_to`, `severity`.

**Never exported:** `access_count`, `last_accessed`, `deleted_at`, `session_id` (operational or ephemeral).

## Import — Vault to SQLite

`mneme vault import` reads `.md` files from the vault's `notes/` directory and
upserts their memories back into the SQLite store. This enables:

- **Human editing.** Edit vault files in Obsidian or any text editor, then
  import changes back to the DB.
- **Manual memory creation.** Create a new `.md` file with valid frontmatter (or
  no `id` field) and import it as a new memory.
- **Team sharing.** A vault committed to git can be imported by team members
  into their local databases.

### Import strategies

| Strategy    | Behaviour |
|-------------|-----------|
| `merge`     | `file.updated_at > DB.updated_at` → update; otherwise skip. Default. |
| `overwrite` | File always wins. Use for "restore from vault" scenarios. |

Both strategies are idempotent: running import twice produces the same result.

### Import CLI usage

```bash
# Import project-scoped memories (default)
mneme vault import

# Preview without writing to DB
mneme vault import --dry-run

# Force-overwrite DB with file content
mneme vault import --strategy overwrite

# Import from a custom directory
mneme vault import --input ~/obsidian-vault/mneme

# Import global memories
mneme vault import --scope global --input ~/.mneme/vaults/_global
```

**Output (normal):**

```
Vault import: 12 created, 5 updated, 83 skipped, 0 errors
Vault root: /Users/you/.mneme/vaults/wirvii-mneme
```

**Output (dry-run):**

```
Dry run — no changes will be written to the database.

Vault import (dry run): 12 would create, 5 would update, 83 would skip, 0 errors
Paths (first 20):
  notes/architecture/tech-stack.md (would update)
  notes/spec/my-new-note.md (would create)
  ...
Vault root: /Users/you/.mneme/vaults/wirvii-mneme
```

### Import rules

- Only `.md` files under `notes/` are processed. Files in the vault root
  (e.g. `.mneme-vault`) and non-`.md` files are silently ignored.
- The `.mneme-vault` marker file is **required**. Running `vault import` on a
  random directory of `.md` files without a marker aborts with an error.
- If the marker's project does not match the current project, import aborts
  with a clear error message.
- Files with invalid frontmatter (e.g. missing `---` delimiters) are logged and
  skipped; remaining files continue processing.
- Files without an `id` field are always inserted as new memories. If the file
  has a `topic_key`, the service performs upsert dedup — a matching memory is
  updated rather than duplicated.
- `decay_rate` from the file is **not** applied to existing memories on update
  (it is an operational parameter, not user-editable content).
- The `created_at` from the file is **not** preserved for new memories; the DB
  record uses the time it entered this database.

### Roundtrip workflow

```bash
# 1. Export your project memories to the vault
mneme vault export

# 2. Open the vault in Obsidian (or your editor) and make changes.
#    Edit titles, add content, create new .md files with frontmatter.

# 3. Preview the impact of importing
mneme vault import --dry-run

# 4. Import the changes back to SQLite
mneme vault import

# 5. Re-export to sync any DB-side changes (e.g. new memories from agents)
mneme vault export
```

After step 5, running `vault import` again produces `0 created, 0 updated, N skipped`
because every file's `updated_at` matches the DB (idempotency is guaranteed by
the RFC3339Nano precision fix from SPEC-023).

---

## Export CLI Usage

```bash
# Export project-scoped memories (default)
mneme vault export

# Export all scopes (project + global)
mneme vault export --scope all

# Preview without writing
mneme vault export --dry-run

# Custom output directory
mneme vault export --output ~/obsidian-vault/mneme

# Export only decision-type memories
mneme vault export --type decision

# Include superseded memories
mneme vault export --include-superseded
```

**Output example:**

```
Vault export: 142 written, 58 skipped, 0 errors
Vault root: /Users/you/.mneme/vaults/wirvii-mneme
```

**Dry-run output:**

```
Dry run — no files will be written.

Vault export (dry run): 142 would write, 58 would skip
Paths (first 20):
  notes/architecture/tech-stack.md
  notes/architecture/memory-model.md
  notes/spec/SPEC-001-rule-type-design.md
  ...
Vault root: /Users/you/.mneme/vaults/wirvii-mneme
```

## Idempotency

Export is safe to run repeatedly. Before writing a file, the vault reads the first 512 bytes of the existing file and compares `updated_at` from its frontmatter to the database value. If the on-disk version is current, the file is skipped. A full re-export with no DB changes produces 0 writes.

## Atomic Writes

Files are written to a hidden `.name.md.tmp` file in the same directory, then renamed atomically via `os.Rename`. No partial files appear on disk. Stale `.tmp` files from interrupted exports are cleaned at the start of the next export.

## Marker File

The `.mneme-vault` JSON file at the vault root tracks metadata:

```json
{
  "vault_version": 1,
  "project": "wirvii/mneme",
  "scope": "project",
  "created_at": "2026-04-30T02:43:00Z",
  "last_export_at": "2026-04-30T20:41:14Z",
  "memory_count": 142
}
```

If you run `vault export` targeting a directory whose marker belongs to a different project, the export aborts with an error. Use `--output` to point to a separate directory.

## Obsidian Integration

The vault is a valid Obsidian vault. Open it directly:

```bash
open -a Obsidian ~/.mneme/vaults/wirvii-mneme
```

No `.obsidian/` configuration is generated -- Obsidian will create it on first open. The YAML frontmatter is compatible with Obsidian's Dataview plugin for querying memories by type, importance, or date.

For a complete guide on using Obsidian with mneme -- including setup, recommended plugins, Dataview queries, and workflows -- see **[docs/OBSIDIAN.md](OBSIDIAN.md)**.

## Scope Rules

| `--scope`   | Store queried    | Vault root                         |
|-------------|------------------|------------------------------------|
| `project`   | project store    | `~/.mneme/vaults/<slug>/`          |
| `global`    | global store     | `~/.mneme/vaults/_global/`         |
| `all`       | both stores      | project + `_global/` separately    |

Soft-deleted memories are never exported. Superseded memories are excluded by default; use `--include-superseded` to override.

## Future Work

- **SPEC-M2b:** fsnotify watcher — continuous filesystem watching that auto-imports
  on file change. Will call `VaultImport` in a loop.
- **SPEC-M3:** ~~Obsidian integration documentation~~ -- delivered in [docs/OBSIDIAN.md](OBSIDIAN.md).
- **`vault gc`:** Remove orphan vault files for deleted/superseded memories.
- **MCP tools:** `mem_vault_export` and `mem_vault_import` for agent-driven access.
- **Original ID preservation:** Extend `store.Create()` to accept caller-supplied
  IDs for faithful ID-preserving import across databases.
