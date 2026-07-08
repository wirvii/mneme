# mneme -- Knowledge Graph

mneme's knowledge graph connects memories through entities and weighted relations. It enables discovering connections that text search cannot find: "which memories are related to the auth module?" is resolved by following graph edges, not by searching for the word "auth".

Introduced in SPEC-005..009 (EPIC-2).

---

## Table of Contents

1. [Graph model](#graph-model)
2. [Weights by type](#weights-by-type)
3. [Wikilinks](#wikilinks)
4. [Hebbian auto-strengthening](#hebbian-auto-strengthening)
5. [Edge decay](#edge-decay)
6. [Retrieval with the graph](#retrieval-with-the-graph)
7. [Personalized PageRank (PPR)](#personalized-pagerank-ppr)
8. [Community detection](#community-detection)
9. [Synthesis (community summaries)](#synthesis-community-summaries)
10. [Context packing by community](#context-packing-by-community)
11. [mem_explore](#mem_explore)
12. [graph rebuild](#graph-rebuild)
13. [Related commands](#related-commands)
14. [Configuration](#configuration)

---

## Graph model

The graph has two node types and one edge type:

### Entities (nodes)

Named concepts that memories reference. Each entity is unique within `(name, project)`.

| Kind | Description | Example |
|------|-------------|---------|
| `module` | Code package or module | `internal/store` |
| `service` | Deployed service | `auth-service` |
| `library` | External dependency | `mattn/go-sqlite3` |
| `concept` | Abstract concept or idea | `auth-model` |
| `person` | Person or contributor | `juan` |
| `pattern` | Design pattern | `repository-pattern` |
| `file` | Source file path | `internal/store/entity.go` |

### Relations (edges)

Directed, weighted edges between entities. The weight is normalized to `[0.0, 1.0]`.

```
Entity A ──[type, weight]──> Entity B
```

Each relation has:
- `type`: one of 8 recognized types
- `weight`: relation strength in [0.0, 1.0]
- `last_traversed_at`: timestamp of last traversal
- `metadata`: optional JSON for extra attributes

### memory_entities (junction)

Junction table that connects memories to entities:

```
Memory M ──[role: subject|object|mention]──> Entity E
```

A memory can be linked to multiple entities, and an entity to multiple memories.

---

## Weights by type

Each `RelationType` has a default weight that reflects its structural importance for graph navigation:

| Type | Default weight | When to use it |
|------|-------------|---------------|
| `depends_on` | **0.9** | A depends on B (strong dependency, critical to understanding the system) |
| `part_of` | **0.85** | A is a component of B (compositional relation) |
| `implements` | **0.8** | A implements B (interface, contract) |
| `uses` | **0.7** | A uses or calls B (usage relation) |
| `conflicts_with` | **0.7** | A conflicts with B (incompatibility) |
| `supersedes` | **0.6** | A replaces B (evolution) |
| `related_to` | **0.5** | Generic relation (co-mention, thematic) |
| `references` | **0.4** | A references B (wikilink, weak mention) |

An explicit `weight` can be passed when creating a relation via `mem_relate`. If not passed, the type's default is used.

**Intuition about weights:** A 2-hop path with weights 0.9 * 0.9 = 0.81 is very strong (transitive dependency). A 2-hop path with weights 0.4 * 0.4 = 0.16 is weak (indirect reference). The product naturally penalizes long paths with weak edges.

---

## Wikilinks

`[[topic_key]]` in a memory's content is automatically parsed at the end of `mem_save` and `mem_update`. Each resolved wikilink creates a `references` relation between the source memory and the target memory. Introduced in SPEC-011 (EPIC-3).

### Supported syntax

| Form | Topic | Anchor | Alias |
|-------|-------|--------|-------|
| `[[topic]]` | `topic` | - | - |
| `[[a/b/c]]` | `a/b/c` | - | - |
| `[[topic#section]]` | `topic` | `section` | - |
| `[[topic|Display]]` | `topic` | - | `Display` |
| `[[topic#sec|Lbl]]` | `topic` | `sec` | `Lbl` |

Only the **topic** is used to resolve the target memory. The **anchor** is stored in `relation.metadata` as `{"anchor": "section"}` for future reference. The **alias** is display-only and is not persisted.

### Behavior

- **Automatic and synchronous:** O(n) parser over lines, processing <25ms for 5 typical wikilinks.
- **Code blocks ignored:** wikilinks inside ` ``` ` or `~~~` blocks are not parsed (CommonMark 4.5).
- **Inline code ignored:** wikilinks inside backticks are not parsed.
- **Idempotent:** if the relation already exists, `TouchRelation` is called (refreshes `last_traversed_at`).
- **Self-loop guard:** `[[same-topic_key]]` of the source memory is ignored.
- **Append-only on updates:** wikilinks removed in a `mem_update` do NOT delete existing relations.
- **TypeSessionSummary excluded:** session summaries do not trigger the parser.

### Scope resolution

| Source scope | Looks in | Fallback |
|--------------|----------|----------|
| `project` | projectStore (same project) | globalStore (same project) |
| `global` / `org` | globalStore (same project) | none |

A global memory CANNOT create relations to project-scoped memories (cross-scope isolation invariant, identical to Hebbian).

### Relation weight

The weight of relations created by wikilinks is `wikilink_relation_weight` (default **0.6**), higher than the `references` default (0.4 for rebuild inference) because an explicit wikilink written by the agent is a stronger signal than a heuristic inference.

### Configuration

```toml
[graph]
wikilinks_enabled = true         # false = treat [[...]] as plain text
wikilink_relation_weight = 0.6   # [0.0, 1.0]
```

### Example

```
mem_save({
  "title": "Auth middleware setup",
  "content": "See [[architecture/auth-model]] for the design. Uses [[convention/error-codes]].",
  "topic_key": "impl/auth-middleware",
  "type": "decision"
})
```

After the save, `mem_explore("impl/auth-middleware")` returns `architecture/auth-model` and `convention/error-codes` at distance 1 with weight=0.6.

---

## Knowledge gaps: unresolved references

When a wikilink `[[topic_key]]` cannot be resolved (the target memory doesn't exist yet), mneme persists the reference in the `unresolved_references` table instead of silently discarding it. Introduced in SPEC-012 (EPIC-3).

### Why it matters

The agent can write `[[decision/retry-strategy]]` before that memory exists. Without gap tracking, this wikilink would be silently lost. With `unresolved_references`, the graph knows there's a gap and can expose it via `mem_gaps` (SPEC-W3, upcoming).

### Schema

```
unresolved_references
├── id                  UUIDv7 PK
├── source_memory_id    FK → memories(id) ON DELETE CASCADE
├── target_topic_key    the topic_key that could not be resolved
├── project             source project slug
├── mention_count       how many times this (source, target) pair has been seen
├── first_seen_at       first detection
└── last_seen_at        last detection
```

`mention_count` is the criticality indicator: a gap mentioned 10 times is more urgent to close than one mentioned once.

### Auto-resolve

When a new memory is saved with `topic_key=X`, mneme automatically searches for all `unresolved_references` whose `target_topic_key=X` and:

1. Loads the source memory of each ref.
2. Applies the cross-scope guard (global source → project target = skip).
3. Calls `createWikilinkRelation` (the same logic as live resolve).
4. Deletes the row from `unresolved_references`.

It is **best-effort**: if it fails partially, unresolved refs persist and are retried the next time a memory with the same topic_key is saved. It is self-healing.

### Cascade cleanup

`ON DELETE CASCADE` on `source_memory_id`: if the source memory is **hard-deleted** (expires from consolidation), its gaps are cleaned up automatically. A **soft-forget** does not trigger the cascade — the memory still exists with decay_rate=1.0, and its gaps remain valid.

### Behavior with updates

A `mem_update` that changes content can register new gaps (via `processWikilinks`). It does not trigger auto-resolve because `topic_key` is not part of `UpdateRequest` — auto-resolve only happens when a new memory with a topic_key is saved via `mem_save` / upsert.

---

## Hebbian auto-strengthening

"Cells that fire together, wire together."

When the agent accesses memory A and then memory B within the same time window, mneme automatically strengthens the edges between A's entities and B's entities.

### How it works

1. **`mem_get(A)`** or **`mem_search` top-3**: the service calls `recordHebbianAccess(A, entities_of_A)`
2. The **AccessTracker** keeps a ring buffer of size `hebbian_window` (default: 5) with recent memories
3. For each memory previously in the buffer, **co-access pairs** between entities are generated
4. Each pair is sent as a `StrengtheningEvent` to the **HebbianWorkerPool**
5. The worker (single async goroutine) applies the change:
   - If the relation exists: `weight += hebbian_increment` (default: 0.05)
   - If it doesn't exist: creates a new one with `weight = hebbian_initial_weight` (default: 0.1)

### Intuitive example

```
Agent session:
  1. Searches "database connection"    → gets a memory about db.Open
  2. Searches "migration strategy"     → gets a memory about db/migrations
  3. Searches "FTS5 configuration"     → gets a memory about FTS5 setup

Hebbian result:
  Entity(db.Open) ←→ Entity(migrations)     weight += 0.05
  Entity(db.Open) ←→ Entity(FTS5-setup)     weight += 0.05
  Entity(migrations) ←→ Entity(FTS5-setup)  weight += 0.05
```

After several sessions where these 3 memories are co-accessed, the edges between their entities become strong (>0.3, eligible for expansion in searches).

### Safety guards

| Guard | What it prevents |
|---------|-------------|
| **D1: Cross-scope** | Pairs between project and global discarded (different DBs) |
| **D4: Self-loop** | Consecutive access to the same ID does not generate pairs |
| **D5: Noise types** | `rule` and `session_summary` types excluded |
| **Drop policy** | If the async buffer is full (1000 default), events are silently dropped |
| **Same entity** | Pairs where source == target are ignored (edge case 8.9) |

### Weight grows but not without limit

- Weights are clamped to `[0.0, 1.0]`
- The 0.05 increment per co-access is conservative: ~6 co-accesses are needed to go from 0.1 (initial) to 0.4 (strong)
- Edge decay (0.02/day after 30 days without traversal) prevents old edges from accumulating indefinitely

---

## Edge decay

Graph relations decay if not used. This prevents the graph from filling up with irrelevant historical edges.

### Mechanism

During the consolidation cycle (every 6h by default), each relation is evaluated:

```
excess_days = days_since_last_traversed - edge_decay_after_days
if excess_days > 0:
    new_weight = weight * exp(-edge_decay_rate * excess_days)
```

### Parameters

| Parameter | Default | Effect |
|-----------|---------|--------|
| `edge_decay_rate` | 0.02/day | Speed of exponential decay |
| `edge_decay_after_days` | 30 | Grace period before decay starts |

### Example

A relation with weight=0.5 that is not traversed for 60 days:
- Excess days: 60 - 30 = 30
- New weight: 0.5 * exp(-0.02 * 30) = 0.5 * 0.549 = 0.274
- After 90 days without use: 0.5 * exp(-0.02 * 60) = 0.151
- The relation becomes too weak for expansion (threshold 0.3) after ~45 days of inactivity

### Notes

- Relations with `last_traversed_at = NULL` (never traversed since the migration) are **excluded** from decay
- Setting `edge_decay_rate = 0` in config disables edge decay entirely
- Relations created by Hebbian have `last_traversed_at` set at creation time, so they ARE eligible for future decay

---

## Retrieval with the graph

### 1-hop expansion in mem_search (SPEC-007)

When `expansion_enabled = true` (default) and `graph_mode = "1hop"`, `mem_search` fuses 3 channels via RRF. (For `graph_mode = "ppr"`, the third channel uses Personalized PageRank — see [PPR](#personalized-pagerank-ppr).)

```
Query ──┬──> FTS5 BM25 ────────────> Channel A (weight 1.0)
        │
        ├──> Vector similarity ─────> Channel B (weight 0.8)
        │
        ├──> 1-hop graph expansion ─> Channel C (weight 0.6)
        │
        └──> RRF Fusion (k=60) ─────> Final ranking
```

**Expansion process:**

1. Preliminary 2-channel fusion (FTS5 + vector) to identify top-K seeds (default K=10)
2. For each seed:
   - Get linked entities
   - Get strong relations (`weight > expansion_threshold`, default 0.3)
   - Map neighboring entities to memory IDs
   - Score: `max(rel_weight * 1/seed_rank)` -- max instead of sum to avoid hub-node inflation
3. Graph results enter as the third RRF channel with weight 0.6

**Expansion parameters:**

| Parameter | Default | Description |
|-----------|---------|-------------|
| `expansion_enabled` | `true` | Toggles expansion |
| `expansion_threshold` | `0.3` | Minimum weight to follow a relation |
| `expansion_fan_out_cap` | `50` | Max relations per entity |
| `expansion_seed_top_k` | `10` | Seeds for expansion |

**Per-request toggle:** the `include_graph` parameter in `mem_search` allows disabling expansion for a specific search:

```json
mem_search({
  "query": "auth middleware",
  "include_graph": false
})
```

### Hebbian tracking post-search

The top-3 results of each `mem_search` are recorded in the AccessTracker for Hebbian auto-strengthening. This means memories frequently co-retrieved by the same queries automatically strengthen their connections.

---

## Personalized PageRank (PPR)

Personalized PageRank is a graph ranking algorithm that propagates importance from a set of "seed nodes" through the graph's topology. mneme uses it as the third retrieval channel (in addition to BM25 and vector similarity). Introduced in SPEC-017..018 (EPIC-4).

### Algorithm

mneme implements PPR via power iteration over the graph's adjacency matrix:

1. Build the adjacency matrix in memory from `entities` + `relations` (only relations with `weight > threshold`)
2. Seed vector: the entity IDs corresponding to the top-K results of the BM25+vector fusion
3. Iterate `max_iterations` times (default: 20) with damping factor `alpha` (default: 0.85)
4. Converge when `||v_new - v_old|| < epsilon` (default: 1e-6)
5. Map entity scores back to memory IDs via `memory_entities`

### Graph modes

The `graph_mode` parameter controls which algorithm is used for expansion:

| Mode | Algorithm | When to use it |
|------|-----------|---------------|
| `ppr` | Personalized PageRank | Default. Better ranking for large graphs (>100 entities) |
| `1hop` | 1-hop BFS expansion | Fast, predictable. Better for small graphs |
| `off` | No expansion | BM25 + vector only. For debugging or graph-less DBs |

### 3-channel RRF (with PPR)

```
Query ──┬──> FTS5 BM25 ────────────> Channel A (weight 1.0)
        │
        ├──> Vector similarity ─────> Channel B (weight 0.8)
        │
        ├──> PPR ranking ──────────> Channel C (weight 0.6)
        │
        └──> RRF Fusion (k=60) ─────> Final ranking
```

In `ppr` mode, Channel C uses PPR instead of 1-hop BFS. The RRF weight (0.6) is the same for both modes.

### Cache

The adjacency matrix is built once per call to `mem_search`/`mem_context`. It is not cached between calls because the graph can change between invocations (Hebbian, new relations). Typical build cost is <15ms for graphs of 5K entities.

---

## Community detection

mneme uses the Louvain algorithm to detect communities of densely connected memories in the graph. Communities group memories that share many entities and strong relations, forming natural "thematic clusters". Introduced in SPEC-019..020 (EPIC-5).

### Louvain algorithm

1. **Input:** Graph of entities connected by weighted relations
2. **Phase 1 (local moves):** Each entity moves to the neighboring community that maximizes modularity gain, iterating until convergence
3. **Phase 2 (aggregation):** Communities collapse into super-nodes and Phase 1 repeats over the reduced graph
4. **Output:** Assignment of each entity to a community, with a membership hash for change detection

### Persistence

Communities are persisted in two tables (migration 010):

```
communities
├── id              UUIDv7 PK
├── project         slug
├── label           generated title (updated by synthesis)
├── membership_hash SHA256 of the sorted set of entity IDs
├── modularity      modularity score (0.0-1.0)
├── member_count    number of entities
├── created_at
├── updated_at
└── deleted_at      soft-delete

community_members
├── community_id    FK → communities(id)
├── entity_id       FK → entities(id) ON DELETE CASCADE
└── PRIMARY KEY (community_id, entity_id)
```

### Change detection

Each community has a `membership_hash` that is the SHA256 of the sorted entity IDs. On each consolidation cycle:

- **Same hash:** Community stable, not modified
- **Different hash:** Community changed, updated with new members
- **New community:** Created with the new members
- **Vanished community:** Soft-deleted

### Pipeline

Community detection runs as part of the consolidation pipeline, after edge decay and before synthesis generation:

```
sweep → edgeDecay → detectCommunities → generateSyntheses → hardDelete → dedup → budget
```

### CLI output

```
Consolidation complete: 3 swept, 1 hard-deleted, 0 duplicates merged, 2 evicted,
5 edges decayed, 8 communities detected (2 new, 1 deleted),
synthesis: 2 created, 1 updated, 0 deleted, 5 skipped
```

---

## Synthesis (community summaries)

The `synthesis` type is a special memory type that automatically summarizes a community's content. Each active community has exactly one synthesis, generated deterministically (no LLM). Introduced in SPEC-021 (EPIC-5).

### Deterministic generation

The generator takes a community's members and produces:

1. **Title:** From the top-3 members by importance, truncated to 80 chars
2. **Content (4 sections):**
   - Overview: quantitative summary (N memories, types, average importance)
   - Top members: the 3 most important memories with title and excerpt
   - All members: table with ID, title, type, importance (max 50 rows)
   - Aggregate metadata: type statistics, referenced files
3. **Wikilinks:** `[[topic_key]]` for each member with a topic_key, automatically creating `references` relations

### topic_key

Format: `synthesis/community-{uuid7}` where uuid7 is the community's ID. This allows idempotent upserts.

### Lifecycle

| Situation | Action |
|-----------|--------|
| New community | Create synthesis |
| Stable community, same content | Skip (no-op) |
| Stable community, content changed | Update synthesis |
| Deleted community | Forget synthesis (decay_rate = 1.0) |

### Special properties

| Property | Value | Reason |
|-----------|-------|-------|
| `importance` | 0.85 | High so it appears in context |
| `decay_rate` | 0.0 | Immune to decay (like rules) |
| Hebbian | Excluded | Prevents auto-reinforcement loops |
| Seeds (Louvain) | Excluded | Prevents synthesis-of-synthesis |
| Wikilinks | Processed | Creates `references` relations to members |

---

## Context packing by community

When communities are detected, `mem_context` can organize memories by thematic clusters instead of a flat ranking. This produces more coherent and navigable contexts for the agent. Introduced in SPEC-022 (EPIC-5).

### Packing modes

| Mode | Behavior |
|------|---------------|
| `auto` (default) | Detects communities; if > 0, uses community packing; otherwise flat |
| `communities` | Forces community packing (silently falls back to flat if there are no communities) |
| `flat` | Flat ranking, pre-SPEC-022 (backward compatible) |

### 4-phase algorithm

```
Phase 1: Community ranking
  ├── Focus provided? → PPR seeded by focus entities → rank communities by PPR score
  └── No focus?       → rank by member_count DESC, modularity DESC

Phase 2: Cluster overviews (dedicated budget: 1500 tokens)
  └── Pack synthesis summaries of top communities

Phase 3: Top cluster deep-dive (max 10 members)
  └── Pack individual memories from the highest-ranked community by importance

Phase 4: Fill remaining budget
  └── Pack remaining memories from all communities using flat scoring
  └── Dedup: exclude memories already packed in Phases 2 and 3
```

### Sections in the output

| # | Section | Present in |
|---|---------|-------------|
| 1 | Last Session | flat + community |
| 2 | Active Rules | flat + community |
| 3 | Cluster Overviews | community only |
| 4 | Top Cluster Detail | community only |
| 5 | Other Memories / Loaded Memories | both (renamed) |

### Configuration

```toml
[context]
context_packing_mode = "auto"       # auto | communities | flat
cluster_overviews_budget = 1500     # tokens for Phase 2
top_cluster_max_members = 10        # max memories in Phase 3
```

### Silent fallback

Any error during community packing (ListCommunities, PPR, etc.) is logged as a warning and the system falls back to flat mode. `mem_context` never fails because of packing.

---

## mem_explore

`mem_explore` is an interactive graph exploration tool. Starting from a seed memory, it performs a prioritized BFS and returns connected memories with their distances and accumulated weights.

### When to use it

- **Debugging:** "what is connected to this memory?"
- **Discovery:** "what other modules depend on the auth service?"
- **Context building:** "give me everything related to the consolidation pipeline"
- **Graph health:** "does this memory have connections? Is the graph well-formed?"

### Seed resolution

The seed can be:
- **Full UUID:** `019de100-abcd-7fff-8000-000000000001`
- **Hex prefix (8+ chars):** `019de100`
- **topic_key:** `architecture/auth-model`

### Prioritized BFS algorithm

1. Resolve seed -> load linked entities
2. Enqueue distance-1 neighbors into a max-heap by `accumulatedWeight`
3. Loop:
   - Pop from the max-heap
   - If already visited with a higher weight, skip
   - Token budget check (skip if exceeded)
   - Register node
   - If depth remains, expand its neighbors with `accumulatedWeight = parent_weight * edge_weight`
4. Sort result: `(distance ASC, accumulated_weight DESC)`

### Parameters

| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `depth` | 2 | 0-5 | Maximum hops from seed |
| `budget` | 4000 | >0 | Token budget for returned memories |
| `threshold` | 0.3 | 0.0-1.0 | Minimum weight to follow a relation |

### CLI output: ASCII tree

```bash
$ mneme explore "architecture/auth-model" --depth 2

architecture/auth-model [seed]
|-- JWT token rotation (depends_on, w=0.90, 245 tok)
|   |-- Key management policy (uses, w=0.63, 180 tok)
|   \-- Session invalidation flow (related_to, w=0.45, 320 tok)
|-- OAuth2 provider config (implements, w=0.80, 156 tok)
\-- Auth middleware setup (part_of, w=0.85, 210 tok)

Total: 5 memories | 1111 tokens | 2 levels
```

### JSON output

```bash
$ mneme explore "architecture/auth-model" --json
```

```json
{
  "seed_id": "019de100-...",
  "seed_title": "Architecture: Auth model",
  "nodes": [
    {
      "memory_id": "019de101-...",
      "parent_memory_id": "019de100-...",
      "title": "JWT token rotation",
      "topic_key": "security/jwt-rotation",
      "type": "decision",
      "distance": 1,
      "accumulated_weight": 0.9,
      "relation_type": "depends_on",
      "token_estimate": 245
    }
  ],
  "total_nodes": 5,
  "tokens_used": 1111,
  "max_depth_reached": 2
}
```

### Notes

- Relations traversed during exploration update `last_traversed_at` asynchronously (prevents edge decay)
- The seed is not included in the list of returned nodes
- If the seed has no linked entities, an empty list is returned
- The max-heap guarantees the strongest paths are explored first, even with a limited budget

---

## graph rebuild

`mneme graph rebuild` is a backfill command that extracts entities from existing memories and creates co-mention relations. It is the entry point for legacy projects with many memories but no graph.

### When to run it

- **New project with existing memories:** after migrating with `mneme init`
- **After importing memories:** `mneme sync import` brings in memories without a graph
- **Periodically:** to incorporate new memories into the graph (it's idempotent)
- **Debugging:** `--dry-run` to see what would be extracted without modifying anything

### 4 extraction heuristics

| # | Heuristic | What it detects | Entity kind |
|---|-----------|-------------|-------------|
| H1 | **topic_key** | Each memory with a topic_key generates a concept entity | `concept` |
| H2 | **file paths** | Recognized paths in content (e.g. `internal/store/entity.go`) | `file` |
| H3 | **code symbols** | `func`/`type`/`struct` declarations in code blocks | `concept` |
| H4 | **wikilinks** | `[[topic_key]]` references in content | `concept` |

### Relation generation

Memories that share >= K entities (default K=2) receive a `related_to` relation with:

```
weight = min(0.5, shared_count * 0.1)
```

This means:
- 2 shared entities: weight = 0.2
- 3 entities: weight = 0.3
- 5+ entities: weight = 0.5 (cap)

### Usage

```bash
# Preview (dry run)
mneme graph rebuild --dry-run

# Normal rebuild
mneme graph rebuild

# Force: deletes existing related_to and regenerates
mneme graph rebuild --force

# Adjust threshold
mneme graph rebuild --min-shared 3
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--scope` | `-s` | `project` | project, global, or all |
| `--min-shared` | `-k` | `2` | Minimum shared entities required to create a relation |
| `--max-relations` | | `50` | Cap of relations per memory |
| `--batch-size` | `-b` | `500` | Memories per transaction |
| `--force` | `-f` | false | Delete existing related_to relations before regenerating |
| `--dry-run` | `-n` | false | Preview without writing |

### Output

```
Starting graph rebuild for project "wirvii-mneme"...
  Scope:       project
  Min shared:  2
  Force:       false
  Batch size:  500

Phase 1: Entity extraction
  [100%] (142/142)

Phase 2: Relation generation
  [100%] (142/142)

Rebuild complete in 1.234s:
  Memories scanned:        142
  Entities extracted:       89
  New entities:             67
  Existing entities:        22
  Memory-entity links:     234
  Relations created:        45
```

### Idempotency

- Existing entities are reused (matched by `(name, project)`)
- Existing memory-entity links are skipped
- Existing `related_to` relations are skipped (unless `--force`)
- Relations of other types (`depends_on`, `implements`, etc.) are **never** touched

---

## graph cleanup-orphan-relations

`mneme graph cleanup-orphan-relations` detects and optionally deletes **orphan relations**: those whose entities have no row in `memory_entities` and are therefore unreachable from `mem_explore`. Introduced in SPEC-031.

### Why it exists

Before SPEC-031, `mem_relate` did not resolve `topic_key` to a memory and never called `LinkMemoryEntity`. Result: relations created via `mem_relate` were disconnected from the memory_entities bridge and invisible to `mem_explore`. This command allows cleaning up the residue.

### Recommended recovery flow

For a project affected by the bug (relations exist but `mem_explore` returns 0 hops):

```bash
# 1) See what would be deleted (dry-run, default)
mneme graph cleanup-orphan-relations

# 2) Delete orphan relations
mneme graph cleanup-orphan-relations --apply --yes

# 3) Rebuild graph from wikilinks/heuristics
mneme graph rebuild --force

# 4) Verify
mneme explore <topic_key>
```

### mem_relate resolution post-fix

After SPEC-031, `mem_relate` resolves each endpoint in this order:

1. Full UUID or 8+ hex prefix of an existing memory → memory
2. If `*_kind` is omitted (default `concept`): exact `topic_key` in project store or global store → memory
3. Entity with `name == string` in project (reuse)
4. Create new entity with `kind` (default `concept`)

When resolution lands on a memory, `LinkMemoryEntity(memory.ID, proxy_entity.ID, "relate")` is called automatically so the relation is reachable by `mem_explore`'s BFS. Passing an explicit `*_kind` other than `concept` (e.g. `"service"`, `"library"`) preserves the legacy entity-only semantics.

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--scope` | `-s` | `project` | project, global, or all |
| `--apply` | | false | Default is dry-run; use `--apply` to delete |
| `--also-delete-entities` | | false | Deletes entities left with no references at all |
| `--output` | `-o` | `text` | text or json |
| `--yes` | `-y` | false | Confirms destructive deletion (required with `--apply`) |

### Output

```
Orphan relations found: 21
Relations deleted:      21
Entities deleted:       0

Examples:
  - architecture/backend-modular-hexagonal --[depends_on]--> architecture/event-system-detail
  - architecture/backend-modular-hexagonal --[references]--> architecture/bounded-contexts
  ...
```

### Idempotency

Re-running the command after a successful `--apply` reports 0 candidates. It is safe to run repeatedly.

---

## Related commands

| Command | Description |
|---------|-------------|
| `mneme explore <seed>` | BFS from seed (ASCII tree or JSON) |
| `mneme graph rebuild` | Backfill graph from existing memories |
| `mneme graph cleanup-orphan-relations` | Clean up orphan relations (SPEC-031) |
| `mneme gaps` | List knowledge gaps (unresolved wikilinks) |
| `mneme search --no-graph` | Search without graph expansion |
| `mneme consolidate` | Run pipeline including community detection + synthesis |
| `mem_relate` (MCP) | Create/update a relation between entities |
| `mem_explore` (MCP) | Graph exploration from MCP |
| `mem_gaps` (MCP) | List knowledge gaps |

Full reference for all endpoints: [docs/api/memory.md](api/memory.md).

---

## Configuration

The full reference for all graph parameters with types, ranges, and environment variables is in [CONFIG.md](CONFIG.md#graph).

Quick summary of the parameters available in `~/.mneme/config.toml`:

```toml
[graph]
# Graph mode (MNEME_GRAPH_MODE)
graph_mode = "ppr"            # ppr | 1hop | off

# Hebbian auto-strengthening (MNEME_GRAPH_HEBBIAN_*)
hebbian_window = 5            # Ring buffer size (0 = disabled)
hebbian_increment = 0.05      # Weight delta per co-access
hebbian_initial_weight = 0.1  # Weight for new Hebbian relations
hebbian_buffer_size = 1000    # Async channel capacity

# Edge decay (consolidation) (MNEME_GRAPH_EDGE_*)
edge_decay_rate = 0.02        # Daily exponential decay rate [0.0, 1.0]
edge_decay_after_days = 30    # Grace period before decay starts

# Expansion in mem_search (MNEME_GRAPH_EXPANSION_*)
expansion_enabled = true      # Toggle graph expansion
expansion_threshold = 0.3     # Min weight to follow
expansion_fan_out_cap = 50    # Max relations per entity
expansion_seed_top_k = 10     # Seeds for expansion

# mem_explore defaults (MNEME_GRAPH_EXPLORE_*)
explore_max_nodes = 200       # Hard cap on BFS nodes
explore_default_depth = 2     # Default depth when not specified
explore_default_budget = 4000 # Default token budget

# graph rebuild (MNEME_GRAPH_REBUILD_*)
rebuild_min_shared = 2        # K: min shared entities for co-mention
rebuild_max_relations = 50    # Cap per memory

# wikilinks (SPEC-011) (MNEME_GRAPH_WIKILINKS_*)
wikilinks_enabled = true          # Parse [[topic_key]] in mem_save/mem_update
wikilink_relation_weight = 0.6    # Weight for wikilink-created relations [0.0, 1.0]

# Synthesis (SPEC-021) (MNEME_GRAPH_SYNTHESIS_*)
synthesis_enabled = true      # Generate community summaries during consolidation
synthesis_max_members = 50    # Max members in synthesis content table
synthesis_top_n = 3           # Top members for title generation

[context]
# Context packing (SPEC-022) (MNEME_CONTEXT_*)
context_packing_mode = "auto"       # auto | communities | flat
cluster_overviews_budget = 1500     # Tokens for cluster overview phase
top_cluster_max_members = 10        # Max memories in top cluster deep-dive
```

To inspect the active configuration with provenance (default/file/env):

```bash
mneme config show graph
mneme config show context
```

---

## See also

- [API reference: Memory tools](api/memory.md) → -- full contract for `mem_relate`, `mem_explore`, and graph-aware fields on `mem_search`/`mem_context`
- [HOOKS.md](HOOKS.md) -- pre-tool-use hook and session hooks
- [CONFIG.md](CONFIG.md) -- full config reference including `[graph]` and `[context]`
