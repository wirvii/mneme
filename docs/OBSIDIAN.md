# Using Obsidian as a Front-End for mneme

mneme stores memories in SQLite databases optimized for programmatic access by AI agents. But sometimes **you** want to browse, edit, and connect your project's knowledge visually. Obsidian turns mneme's vault export into an interactive knowledge base -- with graph views, backlinks, search, and community plugins -- all without changing how mneme works under the hood.

```
                 ┌─────────────────────────────────┐
                 │          mneme (SQLite)          │
                 │   global.db + projects/<slug>.db │
                 └──────────┬──────────────────┬────┘
                            │                  │
                  vault export              vault import
                            │                  │
                            ▼                  │
                 ┌─────────────────────────────────┐
                 │       Vault (Markdown files)     │
                 │  ~/.mneme/vaults/<slug>/notes/   │
                 │  YAML frontmatter + content      │
                 └──────────┬──────────────────┬────┘
                            │                  │
                       Open as vault      Edit & save
                            │                  │
                            ▼                  ▼
                 ┌─────────────────────────────────┐
                 │           Obsidian              │
                 │  Graph view, Dataview, search,  │
                 │  backlinks, templates            │
                 └─────────────────────────────────┘
```

**SQLite remains the source of truth.** The vault is a filesystem mirror that Obsidian reads (and you can edit). Changes flow back to SQLite via `mneme vault import`.

---

## Table of Contents

- [Setup](#setup)
- [Recommended Obsidian Configuration](#recommended-obsidian-configuration)
- [Understanding the Vault Layout](#understanding-the-vault-layout)
- [Frontmatter Fields](#frontmatter-fields)
- [Wikilinks](#wikilinks)
- [Recommended Plugins](#recommended-plugins)
- [Workflow](#workflow)
- [Creating New Memories in Obsidian](#creating-new-memories-in-obsidian)
- [Limitations](#limitations)
- [Equivalence Table: mneme to Obsidian](#equivalence-table-mneme-to-obsidian)
- [FAQ](#faq)

---

## Setup

### 1. Export your vault

```bash
# Export project-scoped memories (most common)
mneme vault export

# Or export everything (project + global)
mneme vault export --scope all
```

This creates a directory tree at `~/.mneme/vaults/<slug>/` with one `.md` file per memory.

### 2. Open the vault in Obsidian

On macOS:

```bash
open -a Obsidian ~/.mneme/vaults/<slug>
```

On Linux, open Obsidian and choose "Open folder as vault", then navigate to `~/.mneme/vaults/<slug>/`.

On Windows, open Obsidian and point it to `%USERPROFILE%\.mneme\vaults\<slug>\`.

Obsidian will create its `.obsidian/` configuration folder inside the vault directory on first open. This folder is ignored by mneme.

### 3. Install recommended plugins

Open Obsidian Settings > Community plugins > Browse, and install:

- **Dataview** -- query your memories by type, importance, date, and more
- **Graph Analysis** (optional) -- enhanced graph view with metrics

### 4. Verify it works

Open the Graph View (Ctrl/Cmd + G). You should see nodes for each memory file, organized by directory structure matching your `topic_key` hierarchy.

### 5. Set up a re-export habit

After your AI agent saves new memories during a coding session, re-export to keep Obsidian in sync:

```bash
mneme vault export
```

This is idempotent -- only new or updated memories are written. Unchanged files are skipped.

---

## Recommended Obsidian Configuration

After opening the vault for the first time, these settings improve the experience:

### Core settings

- **Settings > Files and Links > Default location for new notes**: set to `notes/` so new files you create land in the right directory.
- **Settings > Files and Links > New link format**: set to "Relative path to file" for portable links.
- **Settings > Editor > Properties in document**: set to "Source" to see raw YAML frontmatter, or "Reading" to see it rendered as a table.

### Appearance

- Enable **Readable line length** for long memory content.
- Consider a monospace font for code-heavy memories.

### File recovery

- **Settings > Core plugins > File recovery**: enable. This gives you snapshots of edits before importing back to mneme.

---

## Understanding the Vault Layout

```
~/.mneme/vaults/
  wirvii-mneme/                        # project vault root
    .mneme-vault                       # JSON marker (do not delete)
    .obsidian/                         # Obsidian config (auto-created)
    notes/
      architecture/
        tech-stack.md                  # topic_key: architecture/tech-stack
        memory-model.md               # topic_key: architecture/memory-model
      spec/
        SPEC-001-rule-type-design.md   # topic_key: spec/SPEC-001-rule-type-design
      discovery/
        sync-gaps.md                   # topic_key: discovery/sync-gaps
      _no-topic/
        019ddc45.md                    # memory without topic_key (8-char ID)
  _global/                             # global scope vault
    .mneme-vault
    notes/
      ...
```

**Key points:**

- `topic_key` segments map directly to directory segments. `architecture/tech-stack` becomes `notes/architecture/tech-stack.md`.
- Memories without a `topic_key` go into `notes/_no-topic/` named by the first 8 characters of their UUID.
- The `.mneme-vault` marker file is required for import. Do not delete it.
- Obsidian's `.obsidian/` folder is created and managed by Obsidian. mneme ignores it.

---

## Frontmatter Fields

Every vault note has YAML frontmatter between `---` delimiters. Here is a full example:

```yaml
---
id: 019ddc45-a39b-76da-9ab7-c4546f962418
type: architecture
scope: project
title: "Memory model -- types and scopes"
topic_key: architecture/memory-model
project: wirvii/mneme
importance: 0.90
confidence: 0.80
decay_rate: 0.005
created_at: 2026-04-30T02:44:04.635089Z
updated_at: 2026-04-30T20:41:14.163036Z
revision_count: 1
created_by: claude-code
files:
  - internal/model/memory.go
  - README.md
---
```

| Field | Always present | Editable | Description |
|-------|:-:|:-:|-------------|
| `id` | Yes | **No** | UUIDv7 identifier. Changing it breaks the link to the DB record. |
| `type` | Yes | Yes | One of: `architecture`, `decision`, `convention`, `pattern`, `preference`, `bugfix`, `discovery`, `config`, `session_summary`, `rule`. |
| `scope` | Yes | **No** | `global`, `org`, or `project`. |
| `title` | Yes | Yes | Short searchable summary (double-quoted in YAML). |
| `topic_key` | When set | Yes | Dot/slash-delimited key for dedup and path mapping. |
| `project` | When set | **No** | Project slug (derived from git remote). |
| `importance` | Yes | Yes | 0.0--1.0. Higher = more relevant in context retrieval. |
| `confidence` | Yes | Yes | 0.0--1.0. How reliable this knowledge is. |
| `decay_rate` | Yes | **No** | Operational parameter. Ignored on import. |
| `created_at` | Yes | **No** | When the memory was first saved. |
| `updated_at` | Yes | **No** | When the memory was last modified. Used for merge strategy. |
| `revision_count` | Yes | **No** | Number of updates. |
| `created_by` | When set | Yes | Agent identifier (e.g. `claude-code`). |
| `files` | When set | Yes | Related source file paths. |
| `applies_to` | Rules only | Yes | Glob patterns the rule matches against. |
| `severity` | Rules only | Yes | `info`, `warn`, or `block`. |
| `superseded_by` | When set | **No** | ID of the memory that replaced this one. |

**Fields marked "No" for editable should not be modified in Obsidian.** Changing `id`, `scope`, `project`, `created_at`, `updated_at`, or `revision_count` may cause import failures, duplicates, or data loss.

---

## Wikilinks

If your mneme memories contain `[[topic_key]]` wikilink references (e.g., `[[architecture/tech-stack]]`), Obsidian resolves them as links to the corresponding `.md` file in the vault. This gives you clickable navigation between related memories.

mneme's wikilink parser (EPIC-3) also uses these references to build the knowledge graph automatically. When you add `[[some/topic-key]]` to a memory's content in Obsidian, then import it back, mneme will create or strengthen the graph edge between the two memories.

**Example:** In a memory about authentication, you might write:

```markdown
This service depends on the [[architecture/auth-model]] decision and
follows the [[convention/error-handling]] pattern.
```

In Obsidian, these become clickable links. After `vault import`, mneme processes them as graph relations.

---

## Recommended Plugins

### Dataview

[Dataview](https://github.com/blacksmithgu/obsidian-dataview) lets you query your memories using frontmatter fields. Install it from Community Plugins.

**List all architecture decisions sorted by importance:**

````markdown
```dataview
TABLE importance, created_at, created_by
FROM "notes"
WHERE type = "architecture"
SORT importance DESC
```
````

**Find high-importance memories that are decaying fast:**

````markdown
```dataview
TABLE title, importance, decay_rate, type
FROM "notes"
WHERE importance > 0.7 AND decay_rate > 0.01
SORT decay_rate DESC
```
````

**Show all rules with their severity and patterns:**

````markdown
```dataview
TABLE severity, applies_to, title
FROM "notes"
WHERE type = "rule"
SORT severity ASC
```
````

**Count memories by type:**

````markdown
```dataview
TABLE length(rows) AS "Count"
FROM "notes"
GROUP BY type
SORT length(rows) DESC
```
````

**Recent discoveries (last 7 days):**

````markdown
```dataview
TABLE title, created_by, importance
FROM "notes"
WHERE type = "discovery" AND date(updated_at) > date(now) - dur(7 days)
SORT updated_at DESC
```
````

**Memories related to a specific file:**

````markdown
```dataview
TABLE title, type, importance
FROM "notes"
WHERE contains(files, "internal/service/memory.go")
SORT importance DESC
```
````

### Graph View (built-in)

Obsidian's Graph View is built-in. Open it with Ctrl/Cmd + G. Tips:

- **Color nodes by folder** to see topic_key clusters (architecture, spec, discovery, etc.).
- **Filter by path** to focus on a specific area: type `path:notes/architecture` in the filter bar.
- **Increase depth** to see how memories connect through wikilinks.

### Templates (built-in)

Create a template for manually adding new memories. Save it as `_templates/new-memory.md` (outside `notes/` so it is not imported):

```markdown
---
type: discovery
scope: project
title: ""
topic_key: 
importance: 0.5
confidence: 0.8
files:
---

## What

## Why

## Details
```

Enable **Settings > Core plugins > Templates** and set the template folder to `_templates/`. Then use Ctrl/Cmd + T to insert the template when creating a new note.

**Important:** Do not include `id`, `created_at`, `updated_at`, `revision_count`, or `decay_rate` in templates. When you import a note without an `id` field, mneme creates a new memory with a fresh UUIDv7. If you include a `topic_key`, mneme's upsert logic will deduplicate against existing memories with the same key.

---

## Workflow

### Daily workflow: agent saves, you browse

```bash
# After a coding session where the agent saved new memories:
mneme vault export

# Open Obsidian and browse/search your memories
# Use Graph View to explore connections
# Use Dataview queries to surface important knowledge
```

### Editing memories in Obsidian

```bash
# 1. Export current state
mneme vault export

# 2. Open in Obsidian and edit:
#    - Fix a title
#    - Add content to a discovery
#    - Change importance from 0.5 to 0.9
#    - Add wikilinks to connect related memories

# 3. Preview changes before importing
mneme vault import --dry-run

# 4. Import edits back to SQLite
mneme vault import

# 5. Re-export to sync updated_at timestamps
mneme vault export
```

### Creating new memories in Obsidian

```bash
# 1. In Obsidian, create a new .md file under notes/
#    Include frontmatter with at least: type, scope, title
#    Do NOT include id, created_at, updated_at, revision_count

# 2. Import it
mneme vault import

# 3. Re-export to get the full frontmatter (id, timestamps, etc.)
mneme vault export
```

### When to use `vault export` vs `vault import`

| Scenario | Command |
|----------|---------|
| Agent saved new memories during a session | `vault export` |
| You edited a memory's content in Obsidian | `vault import` then `vault export` |
| You created a new `.md` file in Obsidian | `vault import` then `vault export` |
| You want to browse memories without changing anything | `vault export` (if not already exported) |
| You want to restore from a git-tracked vault | `vault import --strategy overwrite` |
| You changed `importance` on several memories in Obsidian | `vault import` then `vault export` |

---

## Creating New Memories in Obsidian

You can create memories directly in Obsidian by adding `.md` files under `notes/`. The minimum viable frontmatter is:

```yaml
---
type: discovery
scope: project
title: "My new finding"
---

The content of the memory goes here. This is standard Markdown.
```

**Optional fields you can add:**

```yaml
---
type: decision
scope: project
title: "Switch to PostgreSQL for analytics"
topic_key: decision/analytics-db
importance: 0.8
confidence: 0.9
created_by: human
files:
  - internal/analytics/db.go
  - docs/ADR-003.md
---
```

**After importing**, re-export to get the complete frontmatter with `id`, `created_at`, `updated_at`, `revision_count`, and `decay_rate` populated by mneme.

**Deduplication:** If your new note includes a `topic_key` that already exists in the database, mneme's upsert logic updates the existing memory rather than creating a duplicate.

---

## Limitations

1. **No real-time sync.** There is no filesystem watcher. You must run `vault export` and `vault import` manually. A future spec (SPEC-M2b) will add `fsnotify`-based continuous sync.

2. **Do not edit critical frontmatter fields.** The fields `id`, `scope`, `project`, `created_at`, `updated_at`, `revision_count`, and `decay_rate` are managed by mneme. Editing them in Obsidian can cause import failures or data inconsistencies.

3. **Synthesis memories are read-only.** Memories of type `synthesis` (community summaries from EPIC-5) are auto-generated. Editing them in Obsidian and importing back will work, but the next consolidation run may overwrite your changes.

4. **Merge strategy depends on timestamps.** The default `merge` import strategy uses `updated_at` to decide whether a file is newer than the DB. If you edit a vault file without changing `updated_at` (unlikely but possible with programmatic edits), the change will be skipped. Use `--strategy overwrite` to force all file changes into the DB.

5. **Deleted memories are not cleaned up.** If a memory is soft-deleted in the DB (via `mneme forget`), its vault `.md` file is not removed by `vault export`. A future `vault gc` command will handle cleanup.

6. **Global and project vaults are separate.** `--scope project` and `--scope global` produce separate vault directories. They cannot be merged into a single Obsidian vault without using `--output` to redirect both scopes into one directory.

7. **One-way path mapping.** The original `topic_key` is recoverable from the frontmatter field, not from the file path. Renaming a file in Obsidian's file manager does not change the `topic_key` -- you must edit the frontmatter field.

---

## Equivalence Table: mneme to Obsidian

| mneme concept | Obsidian equivalent | Notes |
|--------------|--------------------|----|
| Memory | Note (`.md` file) | One note per memory with YAML frontmatter |
| `topic_key` | File path under `notes/` | `architecture/tech-stack` becomes `notes/architecture/tech-stack.md` |
| Memory type (`architecture`, `decision`, ...) | Frontmatter `type` field | Queryable via Dataview |
| Rule (`type: rule`) | Note with `applies_to` and `severity` in frontmatter | Enforced by mneme's hook system, not by Obsidian |
| Synthesis (`type: synthesis`) | Auto-generated note | Read-only; overwritten by consolidation |
| `importance` | Frontmatter field | Sortable/filterable in Dataview |
| Knowledge graph (entities + relations) | Graph View + wikilinks | `[[topic_key]]` in content creates links |
| `scope` (global/project) | Separate vault directories | `~/.mneme/vaults/<slug>/` vs `~/.mneme/vaults/_global/` |
| `mem_save` (MCP tool) | Create a new `.md` file + `vault import` | Manual equivalent of what agents do |
| `mem_search` (FTS5 + vector + graph) | Obsidian search (Ctrl/Cmd + Shift + F) | Obsidian search is text-only; mneme search is hybrid |
| `mem_context` (curated context) | No equivalent | Agent-facing feature; not relevant in Obsidian |
| Consolidation (decay, dedup, budget) | No equivalent | Background process in mneme; transparent in Obsidian |
| `.mneme-vault` marker | No equivalent | Required for `vault import`; do not delete |

---

## FAQ

### 1. Can I use Obsidian as my only interface to mneme?

No. Obsidian is a **supplementary front-end** for browsing and editing. mneme's primary interfaces are MCP (for AI agents), CLI (for humans), and the HTTP API. The vault export/import cycle adds a manual step that agents do not use. Use Obsidian for visual exploration, knowledge curation, and ad-hoc memory creation.

### 2. Will my agent's new memories appear in Obsidian automatically?

No. You need to run `mneme vault export` after each agent session (or periodically) to update the vault files. A future fsnotify watcher will automate this.

### 3. What happens if I delete a `.md` file in Obsidian?

Nothing happens to the SQLite database. The memory still exists in mneme. The next `vault export` will recreate the file. To actually delete a memory, use `mneme forget <id>`.

### 4. Can I reorganize files/folders in the vault?

You can move files, but it has no effect on mneme. The `topic_key` is stored in the frontmatter, not derived from the file path. If you want to change how a memory is categorized, edit the `topic_key` field in the frontmatter, then run `vault import`.

### 5. How do I handle merge conflicts between Obsidian edits and agent updates?

The default `merge` strategy compares `updated_at` timestamps. If you edited a file in Obsidian (making the file newer) and the agent also updated the same memory in SQLite, the one with the later `updated_at` wins during import. To preview conflicts before importing:

```bash
mneme vault import --dry-run
```

For cases where you want your file edits to always win:

```bash
mneme vault import --strategy overwrite
```

### 6. Can I use Obsidian Sync or git to share my vault with teammates?

Yes. The vault is plain Markdown files. You can:
- **Git:** `git init` inside the vault directory, commit, and push. Team members can `git pull` and `vault import` into their local DBs.
- **Obsidian Sync:** works, but each team member needs their own mneme database. Sync the vault files, then each person runs `vault import`.

The `.mneme-vault` marker file should be included in version control -- it is required for import.

### 7. Do I need to export the global vault separately?

Yes. By default, `vault export` only exports project-scoped memories. To also export global memories:

```bash
mneme vault export --scope all
```

This writes project memories to `~/.mneme/vaults/<slug>/` and global memories to `~/.mneme/vaults/_global/`. You can open each as a separate Obsidian vault, or use `--output` to direct them to a shared directory.

### 8. What is the `.mneme-vault` file? Can I delete it?

It is a JSON marker file that tracks vault metadata (version, project, scope, last export timestamp, memory count). **Do not delete it.** It is required for `vault import` to work -- import aborts if the marker is missing or if the project in the marker does not match the current project.

### 9. Can I add Obsidian plugins that modify the vault files?

Be cautious. Plugins that modify frontmatter (like Linter or MetaEdit) may change formatting in ways that confuse mneme's parser. The parser is tolerant of unknown fields (they are silently ignored), but changes to the YAML structure (e.g., changing indentation of list items) could cause parse errors on import. Test with `vault import --dry-run` after enabling such plugins.

### 10. How large can my vault get before performance degrades?

`vault export` handles 5,000 memories in under 10 seconds. `vault import` handles 1,000 files in under 10 seconds. Obsidian itself performs well with tens of thousands of notes. For most projects, performance is not a concern.
