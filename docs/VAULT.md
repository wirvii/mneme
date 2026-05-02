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

## CLI Usage

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

No `.obsidian/` configuration is generated — Obsidian will create it on first open. The YAML frontmatter is compatible with Obsidian's Dataview plugin for querying memories by type, importance, or date.

## Scope Rules

| `--scope`   | Store queried    | Vault root                         |
|-------------|------------------|------------------------------------|
| `project`   | project store    | `~/.mneme/vaults/<slug>/`          |
| `global`    | global store     | `~/.mneme/vaults/_global/`         |
| `all`       | both stores      | project + `_global/` separately    |

Soft-deleted memories are never exported. Superseded memories are excluded by default; use `--include-superseded` to override.

## Future Work

- **SPEC-M2:** Bidirectional watcher — filesystem edits propagate back to SQLite.
- **SPEC-M3:** Obsidian integration documentation and `.obsidian/` config generation.
- **`vault gc`:** Remove orphan vault files for deleted/superseded memories.
- **MCP tool:** `mem_vault_export` for agent-driven export.
