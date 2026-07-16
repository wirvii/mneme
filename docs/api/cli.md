# API Reference — CLI Commands

36 top-level commands (`./mneme --help` and `./mneme <cmd> --help` are the
source of truth; this reference mirrors them). Global flags apply to every
subcommand:

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--project` | `-p` | auto-detect | Project slug override |
| `--data-dir` | | `~/.mneme` | Data directory override |
| `--log-level` | | (config) | `debug`, `info`, `warn`, `error` |

Index: [docs/API.md](../API.md).

---

## Memory

### mneme save

Save a structured observation to persistent memory (project DB, or global
when no project is detected). `--topic-key` upserts.

```bash
mneme save --title "Auth uses JWT RS256" --content "..." --type decision
echo "content" | mneme save --title "My note" --stdin
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--title` | `-t` | required | Memory title |
| `--content` | `-c` | | Content (or use `--stdin`) |
| `--type` | `-T` | `discovery` | Memory type |
| `--scope` | `-s` | `project` | Scope |
| `--topic-key` | `-k` | | Topic key for upserts |
| `--file` | `-f` | | Referenced files (repeatable) |
| `--importance` | `-i` | | Importance override (0.0-1.0) |
| `--stdin` | | false | Read content from stdin |
| `--applies-to` | `-a` | | Rule patterns (repeatable, required for `--type rule`) |
| `--severity` | | `warn` | Rule severity: `info`, `warn`, `block` |

### mneme search

Full-text search ranked by BM25 + importance + recency, with optional 1-hop
graph expansion.

```bash
mneme search "JWT RS256 auth"
mneme search "N+1 query" --type bugfix --full
mneme search "patterns" --json
mneme search "patterns" --no-graph
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--scope` | `-s` | `all` | `global`, `org`, `project`, or `all` |
| `--type` | `-T` | | Type filter |
| `--limit` | `-n` | 10 | Max results |
| `--full` | | false | Show full content below each result |
| `--json` | | false | JSON output |
| `--graph` | | true | Enable 1-hop graph expansion |
| `--no-graph` | | false | Disable graph expansion (overrides `--graph`) |

### mneme get

Retrieve a memory by ID (increments its access counter).

```bash
mneme get 019530a1-7e2f-7000-8000-abcdef123456 --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

### mneme update

Partial update — only provided flags change; at least one of
`--title`/`--content`/`--type`/`--importance`/`--stdin` is required.

```bash
mneme update <id> --title "New title"
echo "updated" | mneme update <id> --stdin
mneme update <id> --type decision --importance 0.9
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--title` | `-t` | | New title |
| `--content` | `-c` | | New content |
| `--type` | `-T` | | New type |
| `--importance` | `-i` | | New importance (0.0-1.0) |
| `--stdin` | | false | Read new content from stdin |
| `--json` | | false | JSON output |

### mneme forget

Mark a memory for accelerated decay (decay rate → 1.0; not an immediate delete).

```bash
mneme forget <id> --reason "API changed in v2"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--reason` | | Reason (informational) |

### mneme promote

Mark a memory as team-curated (`shared=2`, SPEC-053 D8) regardless of its
type. When [team-memory](../team-memory.md) is active for the current
repository, also materializes it to the shared vault immediately. Idempotent.

```bash
mneme promote 019de100-abcd-7fff-8000-000000000001
```

No flags besides the required positional `<id>`.

### mneme status

Project dashboard: detected project, DB path, memory counts, backlog/spec
pipeline state. Falls back to basic stats if the SDD engine isn't initialised.

```bash
mneme status --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

### mneme stats

Aggregate memory store statistics (counts by type/scope, DB size, avg importance).

```bash
mneme stats --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

### mneme consolidate

Run sweep → purge → dedup → evict manually (also runs automatically in the background when enabled).

```bash
mneme consolidate
```

No flags.

---

## Rules & Graph

### mneme rule add

Create a rule (memory with type `rule`).

```bash
mneme rule add -t "No vendor edits" -c "Never edit vendor/ files." -a "vendor/**" -s block
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--title` | `-t` | required | Rule title |
| `--content` | `-c` | | Rule content/instruction |
| `--applies-to` | `-a` | required | Pattern (repeatable) |
| `--severity` | `-s` | `warn` | `info`, `warn`, `block` |
| `--scope` | | `project` | `project` or `global` |
| `--topic-key` | `-k` | auto | Topic key for upserts (auto-generated from title) |
| `--importance` | `-i` | 0.95 | Importance override |
| `--stdin` | | false | Read content from stdin |

### mneme rule list

```bash
mneme rule list --scope global --severity block
mneme rule list --json | jq '.rules[].title'
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--scope` | `-s` | `all` | `project`, `global`, or `all` |
| `--severity` | | | `info`, `warn`, `block` |
| `--limit` | `-n` | 50 | Max rules to return |
| `--json` | | false | Versioned JSON output |

### mneme rule test

Evaluate active rules against a simulated tool/path invocation.

```bash
mneme rule test --tool Edit --path internal/store/memory.go
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--tool` | `-T` | `Edit` | Tool name to simulate |
| `--path` | | | File path to simulate |
| `--json` | | false | JSON output |

### mneme explore

BFS graph traversal from a seed memory (UUID, prefix, or topic_key). ASCII tree by default.

```bash
mneme explore "architecture/auth-model" --depth 3
mneme explore "ops/key-rotation" --budget 2000 --threshold 0.5
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--depth` | `-d` | 2 | Max hops (0-5) |
| `--budget` | `-b` | 4000 | Token budget |
| `--threshold` | `-t` | 0.3 | Min relation weight (0.0-1.0) |
| `--json` | | false | JSON instead of tree |

### mneme gaps

Unresolved `[[wikilink]]` references.

```bash
mneme gaps --scope all --limit 50
mneme gaps --json | jq '.gaps[].target_topic_key'
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--scope` | `-s` | `project` | `project`, `global`, `all` |
| `--limit` | `-n` | 20 | Max gaps |
| `--min-count` | | 1 | Min mentions |
| `--json` | | false | Versioned JSON output |

### mneme graph rebuild

Backfill the graph from existing memories using 4 heuristics (topic_key, file paths, code symbols, wikilinks). Idempotent.

```bash
mneme graph rebuild --dry-run
mneme graph rebuild --force --min-shared 3
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--scope` | `-s` | `project` | `project`, `global`, `all` |
| `--min-shared` | `-k` | 2 | Min shared entities for a relation |
| `--max-relations` | | 50 | Cap per memory |
| `--batch-size` | `-b` | 500 | Memories per transaction |
| `--force` | `-f` | false | Delete existing `related_to` and regenerate |
| `--dry-run` | `-n` | false | Preview without writing |

### mneme graph cleanup-orphan-relations

Remove relations whose endpoint entities are unlinked from any memory (unreachable from `mem_explore`; legacy `mem_relate` artifacts pre-SPEC-031).

```bash
mneme graph cleanup-orphan-relations
mneme graph cleanup-orphan-relations --apply --yes
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--scope` | `-s` | `project` | `project`, `global`, `all` |
| `--apply` | | false | Default is dry-run; pass to actually delete |
| `--also-delete-entities` | | false | Also delete entities that become fully unreferenced |
| `--output` | `-o` | `text` | `text` or `json` |
| `--yes` | `-y` | false | Required with `--apply` |

---

## SDD: Backlog, Spec, Lane

### mneme backlog add

`--lane` is required (`trivial` or `standard`); `--scope` is required when `--lane=trivial`.

```bash
mneme backlog add "Fix comment typo" --lane trivial --scope "internal/model/*.go"
mneme backlog add "Add push notifications" --lane standard --priority high
```

| Flag | Default | Description |
|------|---------|-------------|
| `--lane` | required | `trivial` or `standard` |
| `--scope` | | Glob pattern (required when `--lane=trivial`) |
| `--description` | | Detailed description |
| `--priority` | `medium` | `critical`, `high`, `medium`, `low` |

### mneme backlog list

```bash
mneme backlog list --status refined --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--status` | | `raw`, `refined`, `promoted`, `archived` |
| `--json` | false | JSON output |

### mneme backlog refine

```bash
mneme backlog refine BL-001 --refinement "Acceptance criteria..."
```

| Flag | Default | Description |
|------|---------|-------------|
| `--refinement` | required | Refinement content (appended) |

### mneme backlog promote

```bash
mneme backlog promote BL-001
```

No flags. Item must be `refined`; creates a spec in `draft`.

### mneme backlog archive

```bash
mneme backlog archive BL-002 --reason "Superseded by BL-007"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--reason` | required | Reason for archiving |

### mneme spec new

`--lane` is required; `--scope` is required when `--lane=trivial`.

```bash
mneme spec new "SDD Engine" --lane standard
mneme spec new "Fix typo" --lane trivial --scope "internal/model/*.go"
mneme spec new "Push notifications" --lane standard --from-backlog BL-003
```

| Flag | Default | Description |
|------|---------|-------------|
| `--lane` | required | `trivial` or `standard` |
| `--scope` | | Glob pattern (required when `--lane=trivial`) |
| `--from-backlog` | | Link to backlog item ID |

### mneme spec list

```bash
mneme spec list --status implementing --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--status` | | `draft`, `speccing`, `needs_grill`, `specced`, `planning`, `planned`, `implementing`, `qa`, `done` (also `rationale`, `audit` for trivial lane) |
| `--json` | false | JSON output |

### mneme spec status

```bash
mneme spec status SPEC-001 --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

### mneme spec advance

Exactly one forward path per current state.

```bash
mneme spec advance SPEC-001 --by orchestrator
mneme spec advance SPEC-001 --by architect --reason "All quality gates passed"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--by` | required | Who triggers the advance |
| `--reason` | | Reason for transition |

### mneme spec pushback

```bash
mneme spec pushback SPEC-001 --from backend --questions "API contract?" "Missing dependency?"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--from` | required | Agent raising pushback |
| `--questions` | required | Questions (repeatable, min 1) |

### mneme spec resolve

Resolves the oldest unresolved pushback; spec must be in `needs_grill`.

```bash
mneme spec resolve SPEC-001 --resolution "Use service accounts"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--resolution` | required | Resolution text |

### mneme spec history

```bash
mneme spec history SPEC-001 --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

### mneme spec quick

Advance a trivial-lane spec from `draft` straight to `implementing`.

```bash
mneme spec quick SPEC-007 "One-line fix to a comment typo in audit.go" --by orchestrator
```

| Flag | Default | Description |
|------|---------|-------------|
| `--by` | required | Who triggers the advance |

### mneme spec reject

Reject from `qa` (standard) or `audit` (trivial) back to `implementing`.

```bash
mneme spec reject SPEC-012 --reason "edge case in payment flow" --by qa-agent
```

| Flag | Default | Description |
|------|---------|-------------|
| `--reason` | required | Rejection reason |
| `--by` | required | Who triggers the rejection |

### mneme lane audit

Deterministic auditor for a trivial-lane spec in `audit` status: file count ≤3, line count ≤20, no forbidden paths (`*.sql`, `migrations/**`, `cmd/**`, `internal/install/assets/**`), scope compliance, no exported symbol changes. Advances to `done` on pass.

```bash
mneme lane audit SPEC-007
mneme lane audit SPEC-007 --base HEAD~2
```

| Flag | Default | Description |
|------|---------|-------------|
| `--base` | merge-base with default branch | Git base ref to diff against |

### mneme lane reclassify

Only `trivial→standard`; moves the spec to `speccing`.

```bash
mneme lane reclassify SPEC-007 standard --by orchestrator
```

| Flag | Default | Description |
|------|---------|-------------|
| `--by` | required | Who triggers the reclassification |
| `--scope` | | Updated scope glob (optional) |

### mneme lane override

Bypasses a failed audit; requires a documented reason (persisted as a discovery memory).

```bash
mneme lane override SPEC-007 --reason "Build tooling file is autogenerated; not a real change" --by orchestrator
```

| Flag | Default | Description |
|------|---------|-------------|
| `--by` | required | Who triggers the override |
| `--reason` | required | Reason for bypassing the audit |

### mneme lane status

```bash
mneme lane status SPEC-007 --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

### mneme lane stats

Project-wide lane compliance: `trivial_count`, `audit_fail_count`, `audit_fail_rate`, `override_count`, `reclassify_count`.

```bash
mneme lane stats --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

---

## CodeGraph

### mneme codegraph index

Walk a directory and extract symbols/relations into the codegraph DB. Incremental (content-hash skip); `[path]` defaults to cwd.

```bash
mneme codegraph index
mneme codegraph index --force
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--dry-run` | `-n` | false | Report without writing |
| `--force` | `-f` | false | Re-index all files regardless of content hash |
| `--language` | `-l` | auto | Force language detection (e.g. `go`, `typescript`) |

### mneme codegraph search

```bash
mneme codegraph search "MemoryService" --kind function --limit 10
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--kind` | `-k` | | Filter by node kind |
| `--language` | `-l` | | Filter by language |
| `--limit` | `-n` | 20 | Max results |

### mneme codegraph node

Print the full record for a symbol: kind, qualified name, location, signature, docstring, source.

```bash
mneme codegraph node SaveMemory
```

No flags beyond the positional `<symbol>`.

### mneme codegraph callers / callees

```bash
mneme codegraph callers SaveMemory --depth 2
mneme codegraph callees SaveMemory --depth 1 --limit 10
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--depth` | `-d` | 1 | Traversal depth (hops) |
| `--limit` | `-n` | 20 | Max results |

### mneme codegraph impact

Blast radius via incoming `calls`/`imports`/`extends`/`implements` edges.

```bash
mneme codegraph impact Memory --limit 50
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--depth` | `-d` | 3 | Traversal depth |
| `--limit` | `-n` | 50 | Max results |

### mneme codegraph trace

Shortest call path between two symbols (BFS on outgoing `calls`).

```bash
mneme codegraph trace Handler ServiceCall --max-depth 8
```

| Flag | Default | Description |
|------|---------|-------------|
| `--max-depth` | 5 | Maximum traversal depth |

### mneme codegraph files

```bash
mneme codegraph files "internal/store/*.go" --language go
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--language` | `-l` | | Filter by language |

### mneme codegraph status

```bash
mneme codegraph status
```

No flags. Prints node/edge/file counts by kind and language.

### mneme codegraph hooks install / remove

Auto re-index git hooks (`post-commit`, `post-checkout`), appended non-destructively; idempotent.

```bash
mneme codegraph hooks install
mneme codegraph hooks remove
```

No flags on either subcommand.

---

## Skills

### mneme skills list

```bash
mneme skills list --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

### mneme skills install

```bash
mneme skills install example-skill --force
```

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | false | Overwrite even if the installed skill is pinned |

### mneme skills pin / unpin

```bash
mneme skills pin example-skill
mneme skills unpin example-skill
```

No flags.

### mneme skills remove

```bash
mneme skills remove example-skill --force
```

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | false | Remove even if the skill is pinned |

### mneme skills lint

```bash
mneme skills lint example-skill
mneme skills lint --json   # lint all installed skills
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

### mneme skills validate

Runs `validation/run.sh` (120s timeout).

```bash
mneme skills validate example-skill --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

---

## Subagents

CLI counterpart to the `subagent_*` MCP tools (SPEC-057) — non-interactive
generation/persistence for per-project subagent profiles (EPIC
agnostic-agents, see [docs/enforcement-model.md](../enforcement-model.md)
for the delegation-hook enablement these profiles can opt into).

### mneme subagents fingerprint

Read-only, deterministic; never calls an LLM.

```bash
mneme subagents fingerprint
mneme subagents fingerprint /path/to/repo --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

### mneme subagents profile get / save

```bash
mneme subagents profile get
mneme subagents profile save --file profile.json
cat profile.json | mneme subagents profile save --stdin
```

| Flag | Default | Description |
|------|---------|-------------|
| `--file` | | Path to a project-profile JSON file (`save` only) |
| `--stdin` | false | Read the project-profile JSON from stdin (`save` only) |

### mneme subagents compose

Never writes to `.claude/agents/` — prints a preview (or writes it to
`--out`). Layer-3 content comes from exactly one of `--areas-file`/
`--areas-stdin` (already-drafted) or `--areas-prompt` + `--engine`
(drafted non-interactively via a `CLIEngine` subprocess: `claude --print -p`
or `codex exec`).

```bash
mneme subagents compose --role backend --archetype backend \
  --description "Implements server-side logic" --areas-file areas.md
mneme subagents compose --role backend --archetype backend \
  --areas-prompt "Summarize apps/core-srv's stack" --engine claude
```

| Flag | Default | Description |
|------|---------|-------------|
| `--role` | | Subagent role / frontmatter name (required) |
| `--archetype` | | Built-in archetype to inherit the permission envelope from (required) |
| `--model` | `sonnet` | Frontmatter model value |
| `--description` | | Frontmatter description (no newlines) |
| `--profile-file` | saved profile | Project-profile JSON path override |
| `--areas-file` | | Already-drafted layer-3 markdown path |
| `--areas-stdin` | false | Read already-drafted layer-3 markdown from stdin |
| `--areas-prompt` | | Prompt to draft layer-3 content via a CLIEngine subprocess |
| `--engine` | `claude` | CLIEngine for `--areas-prompt`: `claude` or `codex` |
| `--out` | stdout | Write the composed preview to this path instead |
| `--json` | false | JSON output (`composed_md`, `valid`, `errors`) |

### mneme subagents write

Writes composed markdown to `<repo-root>/.claude/agents/<role>.md` and
updates the manifest. Validates `composed_md` against `--archetype`'s
Go-authored permission table before writing anything (tools/permissionMode
can never be widened). Atomic: a manifest-save failure after the file write
rolls the file back.

```bash
mneme subagents compose --role backend --archetype backend ... | \
  mneme subagents write --role backend --archetype backend --composed-stdin
```

| Flag | Default | Description |
|------|---------|-------------|
| `--role` | | Subagent role / destination filename (required) |
| `--archetype` | | Built-in archetype to validate against (required) |
| `--composed-file` | | Path to the composed markdown |
| `--composed-stdin` | false | Read the composed markdown from stdin |
| `--enforcement-hook` | false | Record the delegation hook as enabled in the manifest (metadata only — use `mneme delegation-hook enable` to actually register it) |
| `--repo-root` | cwd | Repository root |
| `--engine` | `passthrough` | Generation engine label recorded in the manifest |
| `--areas` | | App/package paths this profile covers (repeatable) |
| `--json` | false | JSON output |

### mneme subagents manifest-list

```bash
mneme subagents manifest-list --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

### mneme subagents doctor

Diagnoses the current project's manifest (report-only by default):
`degenerate_areas`, `archetype_missing`, `not_verified`
(`areas_complete` absent/false), `orphan_path`, `drift`, `unknown_role`,
`bare_dir_ok` (informational, never actionable), and, since SPEC-087 D7,
`stale_agent_fixed` (a manifest entry's persisted `Version` behind
`subagents.AgentFixedVersion`). `--fix` ONLY backfills the `archetype`
field for entries whose `Role` is one of the six built-in archetypes — it
never touches `areas_complete` or `Version`; regenerating a stale profile's
FILE is a different blast radius, handled by `mneme subagents regen`
below.

```bash
mneme subagents doctor
mneme subagents doctor --fix
mneme subagents doctor --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--fix` | false | Backfill missing `archetype` fields only |
| `--json` | false | JSON output |

### mneme subagents regen

Rewrites a materialised profile's frontmatter and agent-fixed managed block
against the current `PermissionTable`/layer-1 asset
(`internal/subagents.Regenerate`), preserving any hand-authored capa-2/3
body byte-for-byte, then updates the manifest entry's `Version`/`Checksum`/
`GeneratedAt`. This is the mechanical upgrade path for a project whose
profiles were generated before a layer-1 change landed (e.g. SPEC-087 D4's
removal of the `spec_advance` instruction from the agent-fixed block) —
bumping `AgentFixedVersion` alone changes nothing already written to disk.

Refuses (per-entry, does not abort the batch) any manifest entry whose file
has no frontmatter or no agent-fixed managed block — not a mneme-generated
profile, never overwritten. Exactly one of `--role`/`--all` is required.

```bash
mneme subagents regen --all
mneme subagents regen --role backend
mneme subagents regen --all --dry-run
```

| Flag | Default | Description |
|------|---------|-------------|
| `--role` | | Regenerate only this role's profile |
| `--all` | false | Regenerate every profile in the manifest |
| `--dry-run` | false | Report what would change without writing anything |
| `--json` | false | JSON output (per-entry `role`, `path`, `old_version`, `new_version`, `changed`, `error`) |

---

## Team Memory

Git-native shared knowledge (SPEC-053, EPIC team-memory) — see
[docs/team-memory.md](../team-memory.md) for the full model (what gets
shared, write-through, import hooks, conflicts, privacy).

### mneme team-memory enable

Single opt-in activation command: creates `.mneme/shared/` with its marker if
absent, bakes `shared=1` onto pre-existing durable memories and exports them,
installs the import hooks (same as `hooks install`), and always prints a
privacy notice. Idempotent.

```bash
mneme team-memory enable
```

No flags.

### mneme team-memory hooks install / remove

Installs or removes the `post-merge`/`post-checkout` git hooks that import
`.mneme/shared/` into the local database in the background after every
pull/checkout. Appends/strips only the mneme-managed block; other hook
content is preserved. Idempotent.

```bash
mneme team-memory hooks install
mneme team-memory hooks remove
```

No flags.

---

## Models

### mneme model list

```bash
mneme model list --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

### mneme model set

Known aliases: `opus`, `sonnet`, `haiku`, `inherit` (unknown aliases accepted with a warning).

```bash
mneme model set bug-hunter opus
```

No flags beyond the positional `<agent> <model>`.

### mneme model reset

```bash
mneme model reset bug-hunter   # restore one agent
mneme model reset              # restore all agents
```

No flags beyond the optional positional `[agent]`.

---

## Conflicts

### mneme conflicts candidates

FTS5-only, no LLM.

```bash
mneme conflicts candidates 01910000-0000-7000-8000-000000000001 --limit 10
```

| Flag | Default | Description |
|------|---------|-------------|
| `--limit` | 5 | Max candidates to return |

### mneme conflicts scan

Dry-run by default; requires the `claude` CLI on `PATH`.

```bash
mneme conflicts scan
mneme conflicts scan --apply --limit 10
```

| Flag | Default | Description |
|------|---------|-------------|
| `--apply` | false | Persist judged relations |
| `--limit` | 5 | Max candidate pairs to judge (max 10) |
| `--project` | auto-detect | Project slug to scan |

### mneme conflicts link

```bash
mneme conflicts link mem-abc mem-def supersedes --rationale "Updated auth design"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--rationale` | | One-line explanation |

Positional: `<from-id> <to-id> <supersedes\|conflicts_with\|unrelated>`.

### mneme conflicts unlink

```bash
mneme conflicts unlink mem-abc mem-def
```

No flags.

### mneme conflicts list

```bash
mneme conflicts list --project my-project --json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--project` | auto-detect | Project slug |
| `--json` | false | JSON output |

---

## Sync, Vault, Export, Embed

### mneme sync export

```bash
mneme sync export
mneme sync export --format manifest
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | cwd | Output directory |
| `--format` | `jsonl` | `jsonl` or `manifest` |

### mneme sync import

Format auto-detected from extension. Idempotent (merges by `topic_key`/dedup keys).

```bash
mneme sync import .mneme/sync/my-project.manifest.tar.gz
```

No flags beyond the positional `<file>`.

### mneme sync status

```bash
mneme sync status --dir /path/to/repo
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | cwd | Directory containing the manifest |

### mneme vault export

Writes one `.md` file per memory with YAML frontmatter, mirroring `topic_key` structure. Idempotent.

```bash
mneme vault export --output /path/to/vault --dry-run
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output` | `-o` | `~/.mneme/vaults/<slug>` | Vault root directory |
| `--scope` | `-s` | `project` | Scope filter |
| `--type` | `-t` | | Type filter |
| `--dry-run` | `-n` | false | Preview changes |
| `--include-superseded` | | false | Include superseded memories |

### mneme vault import

Requires a `.mneme-vault` marker file. `merge` strategy compares `updated_at`; `overwrite` always applies the file.

```bash
mneme vault import --input /path/to/vault --strategy overwrite --dry-run
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--input` | `-i` | `~/.mneme/vaults/<slug>` | Vault root directory |
| `--scope` | `-s` | `project` | `project` or `global` |
| `--strategy` | | `merge` | `merge` or `overwrite` |
| `--dry-run` | `-n` | false | Preview without writing to DB |

### mneme embed backfill

```bash
mneme embed backfill --batch-size 100
```

| Flag | Default | Description |
|------|---------|-------------|
| `--batch-size` | 100 | Memories per batch |

### mneme export markdown

```bash
mneme export markdown -o memories.md
mneme export markdown --dir ./docs/memories
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output` | `-o` | stdout | Single output file (mutually exclusive with `--dir`) |
| `--dir` | | | One file per type |
| `--scope` | | | Scope filter |
| `--type` | `-t` | | Type filter |

---

## Servers, Install, Config, Misc

### mneme mcp

```bash
mneme mcp
mneme mcp --tools agent
```

| Flag | Default | Description |
|------|---------|-------------|
| `--tools` | config | `all` or `agent` |

### mneme serve

Graceful shutdown (10s) on SIGINT/SIGTERM.

```bash
mneme serve --addr :8080
```

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `:7437` | Listen address |

### mneme install

Configures an agent (`claude-code` or `codex`) to use mneme: MCP registration, session hooks, memory protocol, workflow templates, bundled skills. Since SPEC-073 it does **not** install global agent profiles (and removes any it installed in the past); subagents are generated per-project by the `mneme-init` grill. Idempotent.

```bash
mneme install claude-code
mneme install claude-code --reinstall-hooks
mneme install codex --dry-run
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | false | Preview changes without writing |
| `--force` | false | Overwrite existing files (`settings.json` is always merged, never overwritten) |
| `--personal` | false | Install personal ecosystem (claude-code only; ignored for codex) |
| `--source` | config | Personal ecosystem source (git URL or local path) |
| `--reinstall-hooks` | false | Replace PreToolUse hook entries with `mneme hook pre-tool-use` |

### mneme delegation-hook enable / disable / status

Project-scoped, opt-in registration of the delegation-enforcement hook —
independent of the global registration `mneme install claude-code` performs.
See [docs/enforcement-model.md](../enforcement-model.md#project-scoped-opt-in-registration-epic-agnostic-agents-ss-6).

```bash
mneme delegation-hook enable /path/to/repo
mneme delegation-hook status /path/to/repo
mneme delegation-hook disable /path/to/repo
```

No flags beyond the optional positional `[path]` (default: cwd).

### mneme init

Applies managed blocks (global manual + repo block) and reports drift. `--apply` additionally runs the destructive legacy migration (DB writes + `rm-rf`); idempotent otherwise.

```bash
mneme init                  # apply blocks + drift report + dry-run legacy-migration plan
mneme init --check          # report-only, no writes
mneme init --apply --yes    # execute legacy migration without prompt
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--check` | | false | Report-only mode: no files written |
| `--apply` | | false | Also execute the legacy migration |
| `--yes` | `-y` | false | Skip confirmation prompt (only with `--apply`) |

### mneme config show

```bash
mneme config show graph
mneme config show suggestions --json
```

Valid sections: `storage`, `search`, `context`, `consolidation`, `decay`, `mcp`, `embedding`, `personal`, `workflow`, `delegation`, `spec`, `graph`, `suggestions`.

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | JSON output |

### mneme hook

Invoked by agent hook systems, not typically run directly.

| Subcommand | Event | Description |
|------------|-------|-------------|
| `session-start` | SessionStart | Load and print project context |
| `session-end` | SessionEnd | Print reminder to call `mem_session_end` |
| `pre-tool-use` | PreToolUse | Evaluate rules against the tool invocation |
| `enforce-delegation` | PreToolUse | Orchestrator-guard (Layer 2, SPEC-069): blocks the orchestrator from editing/running Bash against a path outside the static whitelist and owned by an implementer subagent |
| `tokenize` | -- | Parse a shell command from stdin into structured JSON tokens (general-purpose surface; `enforce-delegation` no longer spawns this) |
| `path-owned <path>` | -- | Manifest-aware ownership check (SPEC-068); general-purpose surface (`enforce-delegation` calls the same logic in-process instead of spawning this) |

### mneme tui

```bash
mneme tui
```

No flags. Interactive terminal UI (Bubble Tea).

### mneme version

```bash
mneme version
```

No flags. Prints `mneme v<version> (<os>/<arch>)` and `DB schema: v<N>`.

### mneme upgrade

Downloads the latest GitHub release, verifies SHA256, atomically replaces the binary, re-applies agent integrations.

```bash
mneme upgrade --check
```

| Flag | Default | Description |
|------|---------|-------------|
| `--check` | false | Only check for updates |

### mneme completion

Cobra-generated shell completion script (`bash`, `zsh`, `fish`, `powershell`).

```bash
mneme completion zsh > ~/.zsh/completions/_mneme
```

---

## See also

- [docs/HOOKS.md](../HOOKS.md) — `mneme hook` event details
- [docs/lanes.md](../lanes.md) — `mneme lane`/`mneme spec quick`/`reject` decision guide
- [docs/codegraph.md](../codegraph.md) — `mneme codegraph` indexing model
- [docs/skills.md](../skills.md) — `mneme skills` SKILL.md format
- [docs/models.md](../models.md) — `mneme model` apply-on-install
- [docs/conflicts.md](../conflicts.md) — `mneme conflicts` two-phase workflow
- [docs/enforcement-model.md](../enforcement-model.md) — `mneme subagents`/`mneme delegation-hook`, per-project subagent generation
- [docs/team-memory.md](../team-memory.md) — `mneme team-memory`/`mneme promote`, git-native shared vault
