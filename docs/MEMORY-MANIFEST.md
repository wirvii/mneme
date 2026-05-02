# Memory Manifest Format (v1.0)

The Memory Manifest is a portable interchange format for exporting and importing
the complete knowledge state of a mneme project — memories, entities, relations,
and sessions — as a single `.manifest.tar.gz` archive.

It is the successor to the legacy JSONL.gz format for use cases that require
full-fidelity portability or interoperability with third-party tools.

---

## Motivation

The legacy `mneme sync export` format (`.jsonl.gz`) exports only `Memory` records.
The Memory Manifest (`.manifest.tar.gz`) addresses four gaps:

1. **Completeness** — entities, relations, and sessions travel with memories.
2. **Interoperability** — the format is defined by JSON Schema 2020-12, enabling
   any tool to produce or consume it without depending on mneme's Go code.
3. **Schema contract** — producers can validate output; consumers can validate
   input before processing.
4. **Discoverability** — bundled schema files allow offline validation.

---

## Quick Start

```bash
# Export a Memory Manifest (full-fidelity)
mneme sync export --format manifest

# Export legacy JSONL.gz (memories only, default)
mneme sync export

# Import either format — format is auto-detected
mneme sync import .mneme/sync/my-project.manifest.tar.gz
mneme sync import .mneme/sync/my-project.jsonl.gz
```

---

## Archive Layout

A `.manifest.tar.gz` file is a gzip-compressed tar archive. The entries are:

```
manifest.json              # root document — always the first tar entry
schemas/
  manifest.schema.json     # root schema (JSON Schema 2020-12)
  memory.schema.json       # Memory record schema
  entity.schema.json       # Entity (knowledge graph node) schema
  relation.schema.json     # Relation (directed weighted edge) schema
  session.schema.json      # Session (agent working session) schema
```

`manifest.json` **must** be the first entry in the tar stream. Consumers may
reject archives where this invariant is violated.

---

## manifest.json

```json
{
  "version": "1.0",
  "exported_at": "2026-05-01T12:00:00Z",
  "producer": {
    "name": "mneme",
    "version": "1.0.0"
  },
  "project": "wirvii/mneme",
  "scope": "project",
  "memories": [ ... ],
  "entities": [ ... ],
  "relations": [ ... ],
  "sessions": [ ... ],
  "stats": {
    "memory_count": 42,
    "entity_count": 12,
    "relation_count": 8,
    "session_count": 3
  }
}
```

### Required fields

| Field | Type | Description |
|-------|------|-------------|
| `version` | `"1.0"` | Specification version. Consumers MUST reject unknown versions. |
| `exported_at` | RFC 3339 | When the archive was produced. |
| `producer.name` | string | Tool name (e.g. `"mneme"`). |
| `producer.version` | string | Tool semantic version. |
| `project` | string | Project slug all records belong to. |
| `memories` | array | Memory records. May be empty (`[]`). |

### Optional fields

| Field | Type | Description |
|-------|------|-------------|
| `scope` | enum | `"project"`, `"global"`, or `"org"`. Defaults to `"project"`. |
| `entities` | array | Knowledge graph nodes. Defaults to `[]`. |
| `relations` | array | Knowledge graph edges. Defaults to `[]`. |
| `sessions` | array | Agent working sessions. Defaults to `[]`. |
| `stats` | object | Summary counts. Informational only. |

---

## Versioning

The `version` field is a string. mneme v1.x implements exactly `"1.0"`.

**Consumers MUST** check `version` before processing. If the version is unknown,
reject with a clear error — do not attempt partial parsing.

```
unsupported manifest version "2.0"; this tool supports "1.0"
```

Future minor versions (`"1.1"`) may add optional fields. Consumers that ignore
unknown fields are forward-compatible. Future major versions (`"2.0"`) require
an explicit consumer upgrade.

---

## Import Semantics

### Auto-detection

`mneme sync import <file>` detects format automatically:

| Extension | Format | What is imported |
|-----------|--------|-----------------|
| `.manifest.tar.gz` | Manifest | Memories + entities + relations + sessions |
| `.jsonl.gz` | JSONL | Memories only (legacy) |
| Unknown | Content sniff | First 512 bytes of decompressed content |

### Deduplication

| Type | Key | If exists |
|------|-----|-----------|
| Memory (with `topic_key`) | `(topic_key, project, scope)` | Update in place |
| Memory (no `topic_key`) | `id` | Skip |
| Entity | `(name, project)` | Skip |
| Relation | `(source_id, target_id, type)` bidirectional | Skip |
| Session | `id` | Skip |

### Edge Cases

- **Unknown version:** `ErrUnsupportedManifestVersion` returned, zero side effects.
- **Orphan relation** (entity not in DB): logged and skipped.
- **Duplicate IDs within archive:** first record wins.
- **Empty manifest:** valid, imports zero records.

---

## What Is Excluded from v1.0

| Item | Rationale |
|------|-----------|
| Vector embeddings | Large (~2KB/memory), model-specific, regenerable via `mneme embed backfill` |
| Communities | Derived state from Louvain detection, regenerable via consolidation |
| Unresolved references | Derived state from wikilinks, regenerable via `mneme graph rebuild` |
| SDD types (Backlog, Spec) | Workflow state, not knowledge. Cross-installation transfer creates semantic conflicts. |

---

## Open Specification

The formal JSON Schema 2020-12 definitions live in `internal/sync/schemas/`.
A standalone repository for the open specification (`wirvii/mneme-spec`) is
generated in `tmp/mneme-spec/` within the mneme repo (gitignored) and published
manually by the project maintainer. This design decouples specification versioning
from implementation versioning — the spec can evolve independently.

### Validation with ajv-cli

```bash
# Validate a manifest.json against the schema
npm install -g ajv-cli ajv-formats
ajv validate \
  -s internal/sync/schemas/manifest.schema.json \
  -r "internal/sync/schemas/*.schema.json" \
  -d path/to/manifest.json \
  --spec=draft2020
```
