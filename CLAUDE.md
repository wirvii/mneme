# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build, test, run

mneme is a pure-Go build — no CGO, no C compiler, no build tags. SQLite (with FTS5 compiled in by default) is provided by `modernc.org/sqlite`, a pure-Go transpilation of the SQLite engine. `go install github.com/wirvii/mneme/cmd/mneme@latest` works standalone; the `Makefile` is the canonical entrypoint for local development:

```bash
make build         # go build -o mneme ./cmd/mneme
make test          # HOME/USERPROFILE-sandboxed go test ./... + scripts/testguard.sh (SPEC-085 G2)
make test-race     # same sandbox, go test -race ./...
make install       # build + sudo cp to /usr/local/bin/
make setup         # install + `mneme install claude-code` (configures MCP, hooks, manual, skills)
make release-local # ldflags-stripped build with Version=local
```

mneme also runs on Windows (EPIC-windows, SPEC-074/075-080): `go install` is
the only supported install *and* upgrade path there (`mneme upgrade` shells
out to it internally, SPEC-076) — see the `Windows` section in `README.md`.
OS-specific branches use `runtime.GOOS` checked inline (e.g.
`internal/enforcement.PathContext.GOOS`, the `goos` parameter of
`internal/cli/upgrade.go:performUpgrade`) rather than `_windows.go` files or
build tags — the same pure-Go, no-build-tags posture as the rest of the
build.

Run a single package or test:

```bash
go test ./internal/store/...
go test -run TestSaveMemory ./internal/store
```

Linting must pass with **zero warnings**: `golangci-lint run`. `gofmt` and `goimports` are enforced. A `pre-push` hook lives at `.githooks/pre-push`.

Module path is `github.com/wirvii/mneme`; entrypoint is `cmd/mneme`.

## Architecture

mneme is a single-binary persistent memory system for AI agents. The same service layer is exposed through three frontends — keep them in sync when adding capabilities.

### Layered packages (`internal/`)

```
cli/  mcp/  http/        ← three frontends (Cobra, JSON-RPC stdio, REST)
       │
       ▼
   service/              ← business logic orchestration (the only layer the frontends call)
       │
       ▼
   store/                ← repository pattern: CRUD, FTS5 search, entities, stats
       │
       ▼
   db/                   ← SQLite + embedded migrations
       │
       ▼
   model/                ← domain types — zero external imports (the leaf)
```

**Dependency rule:** imports flow inward only. `model` has no external deps. Adapters (`store`, `mcp`, `http`, `cli`) sit at the edges and are swappable. Don't let frontends call `store` or `db` directly — go through `service`.

Supporting packages: `scoring/` (decay, BM25 re-ranking, RRF fusion), `consolidation/` (background decay/dedup/budget sweeps + edge decay), `graph/` (Hebbian auto-strengthening: AccessTracker ring buffer + HebbianWorkerPool async worker), `rules/` (applies_to pattern matching engine for pre-tool-use hook), `enforcement/` (leaf: pure orchestrator-guard decision logic for `mneme hook enforce-delegation` — stdlib + `internal/shell` only, no internal deps, SPEC-069), `embed/` (TF-IDF baseline), `sync/` (JSONL.gz git-shareable export/import), `project/` (git-remote slug detection), `config/` (TOML + env overrides), `install/` (agent installer: MCP/hooks/manual/commands/skills — no global agent profiles since SPEC-073), `skill/` (leaf: SKILL.md parser, structural linter, validate runner — no internal deps), `conflicts/` (leaf: deterministic FTS5 candidate extraction + LLM judgment via claude CLI subprocess — no internal deps), `profile/` (leaf: manifest+pin parsing/validation, host-level store add/update/list over git, pin resolution, `Scaffold`/`RenderManifest` — a brand-new profile repo's scaffolder, SPEC-095 §5 — plus the project-scaffold catalog: `ParseScaffold`/`ListScaffolds` for `scaffolds/<name>/scaffold.toml` and the pure `AssemblyPlan`/`PlanNewProject` — the type is `ScaffoldDef`, not `Scaffold`, since the §5 free function already owns that identifier — SPEC-098 §7a; stdlib + `go-toml/v2` only, no internal deps, SPEC-091 §1), `testenv/` (leaf: `Isolate(m *testing.M)` sandboxes HOME/USERPROFILE for a test binary's TestMain, SPEC-085), `quality/` (leaf: the quality mechanism's pure half — strict `.mneme/quality.toml` parser (schema range `[MinSchemaVersion, CurrentSchemaVersion]`, currently 1..6, D5) with **no implicit defaults** (a default living in the binary is invisible doctrine an upgrade could move), a shell-free `Runner` over an argv list, git helpers (`HeadSHA`/`IsDirty`/`PathChangedInRange`/`FileAtRef`/`IsTracked`/`MergeBase`/`IsAncestor`/`ChangedLines`/`ChangedFilesInRange`/`ChangedFilePathsInRange`/`NumStat`), the *derived* verdict, (SPEC-116 S2) the coverage-of-the-diff half: `Profile`/`ProfileParser` registry (`lcov`, `go-cover`) + `Formats()`, `ParseUnifiedDiff`, `NormalizeSourcePath`/`ComputeDiffCoverage`/`ComputeGlobalStats`/`ScopeHash` (in `measure.go` — named to dodge `.gitignore`'s `coverage.*` rule, which would otherwise silently untrack a file literally named `coverage.go`), and `Baseline`/`ParseBaseline`/`RenderBaseline`/`CompareRatchet`/`CompareStaleness`/`BaselineDirection`; (SPEC-117 S3) the executable-criteria half (`criteria.go`: `ParseCriteria`, the four assert verbs, `command`/`manual` modes); (SPEC-118 S4) the budget-against-the-graph half — `budget.go` (`Budget`/`Quota`/`Revision`/`ParseBudget`/`ValidateBudgetAnchors`), `symbols.go` (`Symbol`/`CollectSymbols`/`DiffSymbols`, the exact `(file, qualifiedName)`-keyed delta), `budgeteval.go` (`EvaluateBudget`/`EvaluateRadius`, plus the migrated-verbatim `EvaluateTrivialBudget` that used to live in the now-deleted `internal/lane`), and `detections.go` (the 8-kind `GraphFacts`-driven detector, `DetectGraph`/`GraphFreshness`); and (SPEC-119 S5) the mutation-over-the-diff half — `mutants.go` (`MutantStatus`'s closed six-value vocabulary, `Mutant`/`MutantReport`, the `MutantReportParser` registry: `mutants-v1`/`gremlins`, `MutantFormats`/`ParseMutantReport`), `gremlins.go` (the go-gremlins/gremlins native parser, written against a REAL captured report in `testdata/`, never the tool's docs), `mutscope.go` (`ScopeMutants`/`Tally`/`EvaluateMutation`, `MaxSurvivorRows`=50), `signature.go` (`RequiresSignature`, the single predicate `Sign`/`Ack` now share, negated, extended by SPEC-120 S6 to return `false` for `visual`/`visual-target` too); and (SPEC-120 S6) the declarative visual-verification half — `visual.go` (`VisualReport`/`VisualTarget`/`A11yImpact`'s closed four-value vocabulary, the `VisualReportParser` registry: `visual-v1` ONLY — no native tool format, unlike S2/S5 — `VisualFormats`/`ParseVisualReport`), `pixel.go` (`ComparePNG`, pure over PNG bytes, `MaxComparePixels`), and `visualscope.go` (`ScopeTargets`/`EvaluateVisual`/`FilterUnderDir`, `MaxVisualTargetRows`=50); stdlib + `go-toml/v2` + `doublestar/v4` (SPEC-116, already a module dep via `internal/rules`), no internal deps, SPEC-115/SPEC-116/SPEC-117/SPEC-118/SPEC-119/SPEC-120), `tui/` (Bubble Tea), `upgrade/`, `export/`.

### The three frontends

> **v1.40 runtime-parity correction (SPEC-123):** Claude Code and Codex both
> use project roles, native role projections, mirrored skills, model mapping,
> and project enforcement hooks. Older Claude-only descriptions are
> superseded by this note.

- **MCP** (`internal/mcp`, primary) — JSON-RPC 2.0 over stdio, ProtocolVersion `2024-11-05`. Surface: 87 tools (15 `mem_*`, 6 `backlog_*`, 9 `spec_*`, 5 `lane_*`, 10 `codegraph_*`, 7 `skills_*`, 3 `model_*`, 5 `conflicts_*`, 6 `subagent_*`, 8 `profile_*`, 5 `quality_*`, 2 `speech_*`, 1 `project_*`, 1 `app_*`, 1 `scaffold_*`, 1 `init`, 2 `sdd_*`). `sdd_status`/`sdd_import` (SPEC-131 §2b, the 86th/87th tools) are the reading half of the SDD git-native mechanism — see the "SDD git-native" section below for what each one does; `sdd_import` is **denied to subagents** via `lifecycleTools`, the same "the author does not authorize their own change" family `spec_advance`/`backlog_archive` already belong to. `quality_*` (SPEC-115, EPIC-calidad S1, extended by SPEC-116 S2 with NO new tool, by SPEC-117 S3 with two, and by SPEC-118 S4/SPEC-119 S5/SPEC-120 S6 with NO new tool): quality_verify (emits the certificate — runs the gates declared in the repo's `.mneme/quality.toml`, plus (S2) up to seven `coverage`/`ratchet` rows, (S3) a `3 + N` set of `criteria`/`criterion*` rows, (S4) a `budget`/`detection` tramo, (S5) a `6 + N` set of `mutation`/`mutant` rows, and (S6) a `7 + N` set of `visual`/`visual-target` rows when their sections are declared, and persists the result bound to HEAD's SHA), quality_status (reads it back; response gains the registered ratchet baseline's path/SHA/date/percentage/staleness, SPEC-116 P10), quality_ack (signs a finding; **denied to subagents** alongside `spec_advance`/`spec_quick` in `lifecycleTools`; SPEC-117 denies it any row `quality.RequiresSignature` accepts — a `criterion*`-kind row, naming `quality_sign` instead, and since SPEC-119 S5 D8 also a `mutant` survivor row, via the SAME predicate negated), quality_sign (SPEC-117 S3 D11 — an ATTESTATION that an attested row holds, converting `finding`→`acked` via the untouched `store.AckCheck`; accepts a row iff `quality.RequiresSignature(kind)` — a `criterion*`-kind row, or (SPEC-119 S5) a `mutant` survivor row, the equivalent-mutant escape hatch, additionally gated by an absolute `max_equivalent` cupo read from that certificate's own `mutation/score` row; **restricted to the `qa-tester` role** for a subagent caller via `roleScopedTools` in `internal/cli/hook.go`, and **fails CLOSED** when that role cannot be resolved — the first hook rule in the repo to do so, deliberately breaking SPEC-086's fail-open posture for this one tool), quality_report (SPEC-117 S3 D12 — renders the QA report from the spec's latest certificate via the pure `quality.RenderReport`, never from `criteria.toml`; refuses to overwrite an existing `qa-report.md` lacking mneme's generation marker unless `force`). `mneme quality baseline update|show` (SPEC-116) is CLI-only, deliberately never an MCP tool — writing the ratchet's registered baseline is a governance act over a versioned file. The split into two verbs is deliberate: emitting is expensive and would time out a synchronous MCP call, a red certificate must be readable to be fixable, and `spec_advance` is denied to subagents — so an implementer could not verify its own work before handing it off if emission lived there. `spec_advance` only *checks* (milliseconds) that a green certificate exists for the exact current SHA. `backlog_list`/`spec_list`/`conflicts_list` share one acotado convention (SPEC-109): a `limit` param (default 20, capped to 50 in silence — safe only because `total` always reports the real match count before the limit), and `backlog_list` additionally projects `Description` into `excerpt`+`truncated` (200 runes, via the leaf `model.Excerpt`) since backlog descriptions are grill ledgers that can run to tens of KB — `backlog_get` (new, D2/D12) is the only way over MCP to read one in full. `backlog_archive` (SPEC-125, the 85th tool) requires a mandatory `reason`, refuses to re-archive an already-archived item or archive an item whose linked spec is already `done`, and — when the linked spec is still alive — FREEZES it: none of the eight verbs that change a spec's status (`spec_advance`, `spec_pushback`, `spec_reject`, `spec_resolve`, `spec_quick`, `lane_audit`, `lane_reclassify`, `lane_override`) can move it again, enforced by a single gate, `loadMutableSpec`, that every one of those eight now calls instead of `store.GetSpec` directly; there is no unarchive, by the owner's explicit decision — the agreed way back is a NEW backlog item referencing the archived one. **Denied to subagents** via `lifecycleTools`, alongside `spec_advance`/`spec_quick`/`quality_ack` (see the enforcement section below). `mem_*`: mem_save, mem_search, mem_get, mem_context, mem_update, mem_session_end, mem_suggest_topic_key, mem_relate, mem_timeline, mem_stats, mem_checkpoint, mem_forget, mem_promote, mem_gaps, mem_explore. `spec_*`: spec_new, spec_status, spec_advance (returns `{spec, executor}` — SPEC-068 advisory `ResolveStageExecutor` recommendation for the stage just entered; **blocked for subagents** — SPEC-087 D5), spec_pushback, spec_resolve, spec_doc_write (writes a spec's entregable — spec/plan/qa-report/changes/criteria/budget — to its workflow directory; directory+filename are never caller-supplied, SPEC-087 D3; the fifth kind, `criteria`, is SPEC-117 S3's — the architect's only write channel for the closed-vocabulary `criteria.toml`, since it is read-only on the repo, validated and refused BEFORE writing if invalid; the sixth, `budget`, is SPEC-118 S4's — same architect-only, validate-before-write posture, over `budget.toml`; both kinds are additionally role-scoped to `architect` for a subagent caller via `roleScopedDocKinds` in `internal/cli/hook.go`, a kind-indexed sibling of `roleScopedTools` that fails OPEN — deliberately asymmetric from `quality_sign`'s fail-CLOSED — when the caller's role cannot be resolved), spec_list (both `spec_status` and `spec_list` gain an additive `frozen` field — SPEC-126 — naming why a spec can no longer change status, present only when it cannot; computed by the single predicate `service.specFreeze`, the same one `loadMutableSpec` now calls, so a listing can never disagree with a verb), spec_quick (also blocked for subagents, D5), spec_reject (now also valid from `done`, SPEC-087 D6). `lane_*`: lane_audit (SPEC-118 P11 — response type renamed to `model.LaneAuditResult`, same JSON shape; the engine underneath is now `internal/quality`, see the "Quality gates..." section below), lane_reclassify, lane_override, lane_status, lane_stats. `skills_*`: skills_list, skills_install, skills_pin, skills_unpin, skills_remove, skills_lint, skills_validate. `model_*`: model_list, model_set, model_reset. `conflicts_*`: conflicts_candidates, conflicts_scan, conflicts_link, conflicts_unlink, conflicts_list. `subagent_*`: subagent_fingerprint, subagent_profile_get, subagent_profile_save, subagent_compose, subagent_write, subagent_manifest_list. `profile_*`: profile_new (SPEC-095 §5 — scaffolds a brand-new profile REPOSITORY: structure + `mneme-profile.toml` + `git init`, never touching the host-level store; the `mneme-profile-author` skill's first step), profile_add, profile_update, profile_list, profile_status — thin wiring over `service.ProfileService`/`internal/profile` (leaf: manifest+pin+host-level store, SPEC-091 §1); plus profile_use/profile_default (SPEC-093 §3) — `use` reconstructs a pin from the store's checkout (`Store.PinFromStore`), writes it (`profile.WritePin`), and now reconciles it (`Reconcile`, SPEC-105 §8 DD15 — a repeated `use` against an already-converged profile is a cheap noop instead of a redundant re-materialization); `default` reads/writes the host-level `[profiles].default` (`config.SetProfilesDefault`) and never materializes; plus profile_deactivate (SPEC-105 §8 DD21, 75→76 tools, `profile_*` 7→8) — computes the plan to undo the current repo's active profile and, with `apply:true`, executes it (restores/removes artifacts, purges provenance-marked rules including global-store orphans, deletes the activation lock) while never touching `.mneme-profile` (the pin, DD19); without `apply:true` returns the plan and mutates nothing. MCP always disables git's interactive credential prompt (`GIT_TERMINAL_PROMPT=0`) so an unattended session fails fast instead of hanging. `project_*`: project_new (SPEC-098 §7a — grows a brand-new project repo from a scaffold in the ACTIVE profile's catalog: copies the scaffold's skeleton with `{{var}}` substitution, `git init` (no commit/remote), and writes the fresh repo's `.mneme-profile` pin with `scaffold=<name>` + the active profile's identity; never activates — the `/new-project` skill chains `mneme-init`. Layout `single` only; `monorepo` arrives in §7b. The pinned bootstrap generator is executed via an injected `Bootstrapper` — never `@latest`). `app_*`: app_add (SPEC-099 §7b — adds a composable app to an existing MONOREPO grown from the active profile's scaffold: reads the monorepo pin's `scaffold`, copies a declared `_blueprints/<name>` archetype into the scaffold's apps dir with `{{var}}` substitution, and auto-wires it via a `Wirer` Strategy — the built-in `turborepoWirer` (updates `pnpm-workspace.yaml`, no-op when a glob already covers `apps/*`, never touches `turbo.json`) or `customWirer` interpreting the scaffold's `[wiring].on_add` closed vocabulary (`workspace:`/`json-merge:`/`copy:`, unknown verb → `ErrUnknownWiringAction` at parse time). Never `git init`s — the monorepo already has its `.git`; `single` layout → `ErrAppAddNotApplicable`. Leaf `internal/profile` PLANS the wiring (`PlanAddApp`, pure `WiringEdit`s); `internal/service` EXECUTES it). `scaffold_*`: scaffold_capture (SPEC-100 §7c — the AUTHORING half: captures an exemplar repo into a DRAFT scaffold in a profile repo, auto-detecting `apps/`/`packages/`/`turbo.json`/`pnpm-workspace.yaml` to infer `layout`/`toolchain`, reading `go.mod`/`package.json` for identity, then writing `scaffolds/<name>/scaffold.toml` + captured trees (`shell/`+`overlay/` or `skeleton/`, apps → `_blueprints/`) with the exemplar's project name / Go module path rewritten to `{{PROJECT_NAME}}`/`{{MODULE_PATH}}`. Never bootstraps/git/activates. Leaf `internal/profile` PLANS — `RepoStructure`→`PlanCapture` infers + drafts + renders a `ParseScaffold`-valid `scaffold.toml` (pure); `internal/service` DETECTS the repo structure and EXECUTES the copy with reverse-parametrization. Extends the `mneme-profile-author` skill with the §15.6 capture+curation grill). `init`: applies managed blocks + drift report (see `docs/init.md`). `handleMessage()` is exposed separately from `Run()` so unit tests can drive it without I/O loops.
- **HTTP** (`internal/http`, `mneme serve --addr :7437`) — stdlib `net/http`, graceful shutdown 10s, 10 endpoints under `/v1/`. Currently lacks SDD endpoints and a few mem tools (`mem_checkpoint`, `mem_timeline`, `mem_suggest_topic_key`); no profile endpoints either (SPEC-091 §1 AC12 — profile add/update are host-local, interactive-credential operations with no REST semantics); when adding service capabilities, decide explicitly whether HTTP gets parity.
- **CLI** (`internal/cli`, Cobra) — 43 top-level commands that mneme itself registers (`internal/cli/root.go`'s single `root.AddCommand(...)` call, SPEC-110 §0 — verified against source, not `--help`, which additionally lists Cobra's own `help` and `completion` for 45 total). Notable: `mneme sdd enable|disable|export|status|import|hooks` (SPEC-130 §2a + SPEC-131 §2b) turns the SDD git-native mechanism on for the current repository — the SAME backlog items and specs already in the local database, ALSO written as reviewable Markdown files under `.mneme/sdd/`, opt-in per repository, dry-run by default for `enable`/`disable` (`--apply` needed to write anything); `mneme sdd import [--dry-run]` reads those files back into the local database by anchor (never by correlative), and `mneme sdd hooks install|remove|run-import` installs the git hooks that run that same import automatically after every pull/checkout — see the "SDD git-native" section below for the full read path. `sync export|import|status` is the backup/restore path (no dedicated `restore` command); `mneme backlog get <id> [--json]` is a subcommand (not top-level) reading a backlog item's full description plus ALL of its refinements — an item now accepts N iterative refinements, stored as their own rows rather than concatenated into `description` (SPEC-110); `mneme init` sets up managed blocks, reports drift, and (with `--apply`) migrates legacy projects to the SDD engine (see `docs/init.md`); `mneme install <agent>` configures MCP config, hooks, the operating manual, slash commands and skills (no global agent profiles since SPEC-073) — supported agents: `claude-code` (multi-agent, full delegation) and `codex` (multi-agent parity; see `docs/codex.md`); `mneme skills` mirrors skills to Claude Code and Codex discovery paths; `mneme model` manages per-agent model assignments; `mneme conflicts` detects and manages memory conflict relations; `mneme subagents` composes/writes per-project subagent profiles and diagnoses/regenerates them (`doctor` reports `stale_agent_fixed` when a profile's `Version` is behind `subagents.AgentFixedVersion`; `regen [--role R] [--all] [--dry-run]` rewrites layer-1 content in place, preserving hand-authored areas — SPEC-087 D7); `mneme delegation-hook` toggles the project-scoped opt-in enforcement hook; `mneme codegraph hooks install|remove` installs/removes git hooks that auto-reindex the code graph after commits and checkouts (see `docs/codegraph.md`); `mneme team-memory enable` activates the git-native shared-knowledge vault (marker + bake/export existing durables + import hooks) and `mneme promote <id>` explicitly shares one memory regardless of type (see `docs/team-memory.md`); `mneme profile new|add|update|list|status|use|default|deactivate` manages profiles — a team's methodology packaged as a portable git repo, nvm-like semantics (`new` scaffolds a brand-new profile REPOSITORY — structure + manifest + `git init`, never touching the host-level store, SPEC-095 §5; host-level store + per-project pin; `use`/`default` are the two write verbs — SPEC-093 §3 — plus precedence and SessionStart auto-activation; activation is now a convergent `Reconcile`, not an unconditional event, and `deactivate` (dry-run by default, `--apply` to execute, never touches `.mneme-profile`) undoes it — SPEC-105 §8, see `docs/profiles.md`); `mneme project new <scaffold> --dir <path> [--var k=v]` grows a brand-new project repo from a scaffold in the active profile's catalog (SPEC-098 §7a — copy skeleton + `{{var}}` substitution + `git init` + pin with `scaffold=<name>`; the deterministic half of the `/new-project` skill); `mneme app add <blueprint> --name <app> [--dir <monorepo>] [--var k=v]` adds a composable app to an existing monorepo grown from the active profile's scaffold (SPEC-099 §7b — reads the monorepo pin's `scaffold`, drops a declared `_blueprints/<name>` archetype into the scaffold's apps dir with substitution, and auto-wires it via a `Wirer` Strategy: `turborepo` built-in adapter or the scaffold's declared `[wiring]`; the deterministic half of `/new-app`).

### Persistence

Two SQLite databases per host:
- `~/.mneme/global.db` — global + org scope memories
- `~/.mneme/projects/<slug>.db` — project-scoped memories (slug derived from git remote)

Scopes (`global` / `org` / `project`) never leak between projects. Migrations are embedded via `embed.FS`. Since migration 019 (SPEC-128), every `backlog_items`/`specs` row also carries its own UUIDv7 anchor (`uuid`, unique, immutable, invisible in every readable command) — the identity a memory's `SPEC-125`/`BL-001` mention resolves against on the machine that reads it, so a mention that was true when written stays honest (`local`/`foreign`/`unanchored`) instead of silently matching an unrelated row with the same correlative on a different machine. `internal/db.ensureSDDUUIDs` fills any row still missing one on every `Open`, not just once at migration time.

### Pre-tool-use hook (important when editing source)

`mneme hook pre-tool-use` is the active Claude Code `PreToolUse` hook. It evaluates **rules** from the mneme database against every `Edit`/`Write`/`MultiEdit` call. Rules carry `applies_to` patterns (path globs, tool selectors, negations) and a `severity` level (`info`/`warn`/`block`). When a `block`-severity rule matches, the hook exits with code 2 and Claude Code rejects the tool call. Source: `internal/cli/hook.go:runHookPreToolUse` + `internal/rules/match.go`. If a code edit gets blocked, that's a rule — check `mneme rule list` and delegate or adjust accordingly.

`mneme hook enforce-delegation` (SPEC-069) is the orchestrator-guard (Layer 2): a portable Go subcommand (registered with no path to the home directory) that blocks the orchestrator from editing, or running Bash against, a path outside the static whitelist and owned by an implementer subagent (or legacy deny-by-default when no manifest exists). It replaced the embedded ~640-line `enforce_delegation.sh` bash script — the decision logic now lives in the leaf package `internal/enforcement` (stdlib + `internal/shell` only: `IsWhitelisted`, `EvaluateFileTool`, `EvaluateBash`), wired up by `internal/cli/hook.go:runHookEnforceDelegation`, which injects an in-process `OwnershipFunc` closure over `resolvePathOwnership` — no subprocess spawn. The embedded `enforce_delegation.sh` asset is now a ~6-line compat shim (`exec mneme hook enforce-delegation`), kept only so a pre-existing absolute-path registration keeps working until re-registered. See `docs/HOOKS.md` for details.

`mneme hook path-owned <path>` (SPEC-068) is the standalone subcommand exposing the same manifest-aware ownership check `resolvePathOwnership` implements (general-purpose surface / backward compatibility — `enforce-delegation` calls the function in-process rather than spawning this subcommand): exit 2 (block, prints the owning role or `legacy`) when the path is owned by an implementer subagent's manifest `areas`, or when no manifest exists yet (deny-by-default, protects projects mid-migration); exit 0 (allow) when the path has no delegate, or on any hard failure (fail-open). This is the orchestrator-fallback mechanism — see "Enforcement Model" below and `docs/enforcement-model.md`.

The hook also emits a context-only reminder (exit 0, never blocking) when an agent calls `Read`/`Grep`/`Glob` on a project with an indexed code graph — see `docs/codegraph.md` for the adoption nudge (C1) and auto-reindex git hooks (C2). The nudge is controlled by:

```toml
[codegraph]
hook_nudge_enabled = true   # MNEME_CODEGRAPH_HOOK_NUDGE env var overrides
```

### Enforcement Model

mneme enforces role boundaries at two layers:

1. **Capability (primary)**: every subagent declares an explicit `tools:`
   allowlist in its YAML frontmatter (`internal/install/assets/agents/*.md`).
   `architect` physically cannot edit code or run Bash. `qa-tester` has
   `Bash` (+ `permissionMode: bypassPermissions`, SPEC-087 D2/D2b — so its
   own gates run unattended) to run its own gates, but no `Edit`/`Write`/
   `MultiEdit`/`NotebookEdit` — `IsImplementer` reads the toolset, not
   `permissionMode` (SPEC-087 D1). Implementer agents (`backend`,
   `frontend`, `bug-hunter`) have the full edit+execution toolset. The
   `diagnostician` agent has `Bash` for log reading but lacks
   Edit/Write/MultiEdit — it reads infra, never mutates code. Since
   SPEC-132, `qa-tester` and `frontend` (and only those two) also carry a
   browser-server MCP allowlist so they can open a real screen instead of
   trusting that "compiles" means "renders correctly" — `qa-tester` stops
   being read-only over *data* (though it stays read-only over *code*),
   and this capability exists only in Claude Code today, not in the second
   execution runtime (Codex).

2. **Hook (defense in depth)**: `mneme hook enforce-delegation` (a Go
   `PreToolUse` subcommand, SPEC-069) detects the orchestrator by the absence
   of `agent_id` in the hook payload (`CallerIdentity.IsSubagent`, SPEC-086).
   Orchestrator edit attempts against protected paths are blocked with
   exit code 2 and logged as `discovery` memories — except a path with no
   implementer subagent to own it (per `mneme hook path-owned`, SPEC-068),
   which is allowed through as a conscious fallback: the orchestrator supplies
   stages/areas that have no delegate rather than the flow breaking. This is a
   documented trade-off (correction, not excellence) — materialize the missing
   subagent via the `mneme-init` grill when quality/isolation matter.
   Since SPEC-086, a **subagent** invocation is no longer waved through
   unconditionally either: the payload's top-level `agent_type` field (real,
   confirmed 2026-07-15) identifies WHICH role is calling, and it is
   contained to its own manifest-declared, `areas_complete`-certified areas
   — `off`/`warn`/`block` per project (`[delegation] subagent_containment`
   in `~/.mneme/config.toml`, default `warn`). Separately, SPEC-087 D5
   **unconditionally** denies `spec_advance`/`spec_quick` to any resolved
   subagent (exact-match, no mode) — the lifecycle belongs to the
   orchestrator; `spec_pushback`/`spec_reject`/`spec_doc_write` stay
   allowed. SPEC-125 D11 adds `backlog_archive` to the same
   `lifecycleTools` map: discarding work and irreversibly freezing the
   spec it governs is the owner's call, channelled by the orchestrator,
   never a subagent's — the same "the author does not absolve/discard
   their own work" family `quality_ack` already belongs to. SPEC-117 D11
   adds a THIRD, sibling map (`roleScopedTools`)
   restricting `quality_sign` to the `qa-tester` role — and, unlike every
   other rule here, **fails CLOSED** when the caller's role cannot be
   resolved, a deliberate one-tool exception to this whole model's
   fail-open posture (D14: a signature whose signer cannot be identified
   is worse than none). See "Subagent containment", "Lifecycle-tool
   denial", and "Role-scoped tool denial" in `docs/enforcement-model.md`.

Every blocked attempt is queryable via:

```bash
mneme search "Blocked edit"
```

For the full reference — adding subagents, debugging blocks, allowlist tables —
see `docs/enforcement-model.md`.

## Testing approach

- `internal/store` tests run against a **real in-memory SQLite** — no mocks (per `docs/ARCHITECTURE.md`). Treat the DB as part of the unit under test.
- Table-driven tests are the default.
- Target >85% coverage on core packages (`model`, `store`, `service`, `scoring`).

### Test isolation from the real environment (SPEC-085)

mneme dogfoods itself: this repo's own `~/.mneme/projects/wirvii-mneme.db` and
`.mneme/shared/` vault are real, used daily. A test suite that resolves an
environment-derived path (HOME, git identity, git-repo-root-based vault
detection) instead of an injected one can silently write into them. SPEC-085
fixed a concrete case of this — `NewMemoryService` used to auto-detect
team-memory from the process cwd, and every test running inside this
dogfooding repo activated write-through materialization, corrupting the real
DB via the vault import round-trip (7752 of 9058 rows were test fixtures
before the cleanup). The fix is layered — know all four when writing a new
test that touches `service.NewMemoryService` or drives a real CLI command:

1. **`NewMemoryService` never auto-detects team-memory (D1/D2).** It takes a
   variadic `...service.Option`; team-memory defaults OFF (the zero value of
   `service.TeamMemoryState`). The only production call site allowed to opt
   in is `internal/cli.initService`, via
   `service.WithTeamMemory(service.DetectTeamMemory())`. A test that needs to
   exercise the real detection path (chdir + marker file) must opt in
   explicitly the same way — see `newRepoTestService` in
   `internal/service/teammemory_test.go`.
2. **`gitident.Reset()` in any test helper that chdirs into a fixture git
   repo.** `gitident.Author()` memoizes process-wide via `sync.Once`; without
   `Reset()`, an earlier test's resolved identity leaks into every later
   `Author()` call in the same test binary regardless of cwd.
3. **A CLI-level test that drives a real cobra command (`root.Execute()`)
   must also chdir into an isolated, non-git temp directory** — not just
   isolate `--data-dir`. `service.DetectTeamMemory()` resolves relative to
   the real process cwd via `git rev-parse --show-toplevel`, which
   `--data-dir` does nothing to isolate; see `runSubagentsCmd` in
   `internal/cli/subagents_test.go` and `runTeamMemoryEnableCmd`/
   `runTeamMemoryHooksCmd` for the pattern.
4. **`internal/testenv.Isolate(m *testing.M)` from `TestMain`** in every
   package that touches environment-derived paths directly or via a
   production constructor: `service`, `cli`, `mcp`, `http`, `install`,
   `upgrade`. This is what protects a bare `go test ./...` — exactly how an
   agent normally runs the suite — not just `make test`'s HOME sandbox (G2,
   below). `internal/testenv`'s own `TestAllIsolatedPackagesDeclareTestMain`
   fails if any of those six packages loses its `TestMain`.

`make test`/`make test-race` additionally sandbox `HOME`/`USERPROFILE` to
`tmp/testhome` (gitignored) as defense-in-depth (G2), then run
`scripts/testguard.sh`, which fails if any `projects/*.db` or `global.db`
shows up inside that sandbox — proof no test resolved a real DB path.
`GOCACHE`/`GOMODCACHE` are captured via `$(shell go env …)` at Makefile parse
time (not inline in the recipe) specifically because bash evaluates same-line
`VAR=value` assignments left-to-right — putting `go env GOCACHE` after the
sandboxed `HOME=` on the same line makes it observe the *already-sandboxed*
HOME, forcing a full rebuild and module re-download on every run.

`scripts/cleanup-test-pollution.sh` is the one-off (not a `mneme` subcommand)
script meant to purge the pre-SPEC-085 pollution from this repo's real DB and
vault: dry-run by default, an explicit denylist of the 14 exact polluted
project slugs (no globs/heuristics), a precondition that aborts on any
unrecognised project, and a mandatory DB backup before `--apply`. As of this
writing `--apply` has not been run — the dry-run has been verified against
the real DB (it enumerates exactly 7752 rows / ~6700 notes) and against
synthetic fixtures, but the actual purge is the owner's call to make after
reviewing that output.

## Quality standards

- **Clean Code**: single-responsibility functions, intention-revealing names, no dead code, no commented-out code, no magic numbers.
- **Clean Architecture**: strict dependency inversion as described above. Adapters are pluggable.
- **Documentation**: every exported type/function/package has a godoc comment explaining *why*, not just *what*.
- **Error handling**: wrap with context — `fmt.Errorf("store: save memory: %w", err)`. Never swallow. Use sentinel errors for expected conditions. `.golangci.yml` excludes a curated set of unactionable error returns (deferred `Close`, `fmt.Fprint*`, `os.Remove`); don't expand that list casually.
- **Design patterns in use**: Repository (storage), Strategy (retrieval backends), Observer (hooks), Command (CLI), Builder (complex constructors).

## Conventions

- **Commits**: [Conventional Commits](https://www.conventionalcommits.org/) — `type(scope): description`.
- **Branches**: `type/short-description` (lowercase, hyphens).
- **Go version**: 1.25+ (go.mod currently declares 1.25.8).

## Lanes (v1.5.0)

Every backlog item and spec carries a **lane** (`trivial` or `standard`) declared at creation. Lane is never inferred automatically.

**When adding an item without a lane, ask:** "Is this trivial (≤3 files, ≤20 lines, no SQL/migrations, no public API change) or standard?"

**Orchestrator rules:**
- May propose a lane based on the description but must never assign without confirmation.
- For trivial items: dispatch to implementers with `spec quick` and include in the task payload: *"This is a trivial-lane item. Stay strictly within the declared scope `<scope>`. Do not refactor adjacent code. Do not add tests beyond updating existing assertions. If the scope is insufficient, stop and report — do not expand."*
- Never edits code itself regardless of lane.

**Key commands:**
```bash
mneme backlog add "Fix typo" --lane trivial --scope "internal/model/*.go"
mneme spec quick SPEC-007 "One-line comment fix" --by orchestrator
mneme lane audit SPEC-007
mneme lane reclassify SPEC-007 standard --by orchestrator  # if audit fails
mneme lane override SPEC-007 --reason "autogenerated file" --by orchestrator  # last resort
mneme lane status SPEC-007
```

**Archiving a spec's backlog item freezes it permanently (SPEC-125).** If
`backlog archive` discards the item a spec came from while that spec is
still open, the spec can never move again — including `lane audit`/
`lane override`/`lane reclassify`, which used to be the way out of a failed
trivial audit. This is intentional: archiving is a deliberate, reasoned
decision, and a spec that cannot close is what "discarded" means. There is
no unarchive — the way back is a new backlog item referencing the archived
one.

Full reference: `docs/lanes.md`.

## Skills (v1.40 parity)

mneme is a **cross-runtime package manager** for skills. It embeds skills under
`internal/install/assets/skills/` and mirrors them to `~/.claude/skills/` and
`$HOME/.agents/skills/`. mneme does not implement either runtime's loader.

**Bundled skills (SPEC-037):** `example-skill` (structural fixture — NOT architectural guidance), `mneme-init` (project-level orchestrator skill, SPEC-058), and `mneme-profile-author` (profile-authoring grill, SPEC-095 §5 — sibling of `mneme-init`: authors a profile REPO's content, rather than onboarding a single project to one).

**Key commands:**
```bash
mneme skills list
mneme skills install example-skill
mneme skills pin example-skill        # protect from overwrite/remove
mneme skills unpin example-skill
mneme skills lint [<name>]            # deterministic structural check
mneme skills validate <name>          # run validation/run.sh
mneme skills remove <name> [--force]
```

**Rules:**
- `pinned: true` in an installed SKILL.md = only protection from overwrite/remove. No hook or capability coupling.
- `lint` is pure Go, deterministic, no LLM, no script execution.
- `validate` runs `validation/run.sh` with a 120s timeout; ErrNoValidation if absent.
- `internal/skill` is a leaf package — no imports of `internal/model` or other internal packages.
- SKILL.md requires 5 H2 sections and a 3-col Automated Checks table (see `docs/skills.md`).

Full reference: `docs/skills.md`.

## Models (v1.8.0)

mneme tracks a model alias per bundled agent (SPEC-038). Assignments are stored
in `~/.mneme/config.toml` under `[models.overrides]`. Config overrides are NOT
assets — they survive upgrades. Since SPEC-073, `mneme install claude-code` no
longer installs global agent profiles, so these overrides no longer rewrite any
`~/.claude/agents/<agent>.md`; per-project subagents pick their model at grill
time (`subagent_compose`). The apply-on-install machinery
(`ApplyAgentModels`) remains as dormant capacity for any future per-profile agent.

**Defaults (cost/quality rationale):**
- `architect` → `opus` (its output is the spec; errors propagate to all agents)
- `backend`, `frontend`, `qa-tester`, `bug-hunter` → `sonnet`

**Key commands:**
```bash
mneme model list                    # show effective model + origin for each agent
mneme model set bug-hunter opus     # override one agent
mneme model reset bug-hunter        # remove override, restore default
mneme model reset                   # remove all overrides
# (since SPEC-073 install no longer writes global agents, so overrides no
#  longer rewrite ~/.claude/agents; per-project subagents pick model at grill time)
```

**Rules:**
- Any non-empty string is accepted as a model string (open-ended).
- Unknown aliases produce a WARNING, never an error (`model set backend banana` warns).
- Empty model string → `ErrInvalidModel`.
- Unknown agent name → `ErrUnknownAgent` (rejected with CodeInvalidParams in MCP).
- Override in config survives `mneme install` (Install() never rewrites config.toml).
- The assign step runs automatically after WriteAgents in every install.

Full reference: `docs/models.md`.

## Conflicts (v1.9.0)

mneme surfaces memory conflicts via a two-phase workflow (SPEC-039):

1. **Detection (deterministic, FTS5):** `internal/conflicts/detect.go` extracts salient
   terms and builds a candidate FTS5 query. No LLM.
2. **Judgment (LLM via subprocess, explicit):** `internal/conflicts/judge.go` invokes
   `claude -p --output-format json` for each pair. $0 cost on subscription. Never automatic.

**Relation types:**
- `supersedes` → reuses `memories.superseded_by` + `SetSupersededBy`. Already excluded by retrieval.
- `conflicts_with` → `memory_relations` table (migration 013). Post-ranking `annotateConflicts` pass.
- `unrelated` → `memory_relations` table. Negative cache only; no retrieval effect.

**Key commands:**
```bash
mneme conflicts candidates <id>            # FTS5 candidates (deterministic)
mneme conflicts scan                       # dry-run judgment (needs claude CLI)
mneme conflicts scan --apply               # persist judgments
mneme conflicts link <from> <to> supersedes --rationale "..."
mneme conflicts link <from> <to> conflicts_with
mneme conflicts unlink <from> <to>
mneme conflicts list
```

**Rules:**
- `internal/conflicts/` is a leaf package — stdlib only, no model/store imports.
- CLI absent → `ErrCLIUnavailable`; MCP returns `IsError:true` with structured payload.
- Scan is dry-run by default; `--apply` persists. NEVER auto-judges on save.
- No auto-delete/edit of memories. No embeddings. No metered API.

Full reference: `docs/conflicts.md`.

## Team Memory (SPEC-053)

mneme lets a repository's durable knowledge flow between teammates entirely
through git — no server, no account, no network call.

**Activation is opt-in per repository:** the presence of
`<repo>/.mneme/shared/.mneme-vault` is the only flag; there is no other
config. `mneme team-memory enable` creates it, bakes/exports pre-existing
durable memories, and installs the import hooks in one idempotent step.

**Sharing levels** (`shared` column, layered on `scope=project`, not a new
scope): `0` local-only (default) · `1` auto-shared — **share-by-default**
(SPEC-071): every project-scoped type auto-shares *except* the two
auto-generated/ephemeral ones (`session_summary`, `synthesis`); i.e. decision,
discovery, config, preference, convention, architecture, pattern, bugfix, rule
all auto-share · `2` team-curated (`mneme promote <id>` / `mem_promote`, any
type).

**Key commands:**
```bash
mneme team-memory enable          # activate: marker + bake/export + hooks
mneme team-memory hooks install   # install only the import hooks
mneme team-memory hooks remove    # remove only the mneme-managed hook block
mneme promote <id>                # explicitly share one memory (shared=2)
```

**Rules:**
- WRITE is synchronous write-through inside `service.Save`/`Update` — no
  filesystem watcher exists between the vault and SQLite (see
  `docs/VAULT.md`). Best-effort: a materialization failure is logged, never
  fails the save.
- READ is a `post-merge`/`post-checkout` git hook (`mneme team-memory hooks
  run-import`, exit-0-always) that imports `.mneme/shared/` in the
  background after every pull/checkout.
- Every shared memory is its own file (`notes/<uuid>.md`) — concurrent
  creations by different teammates never collide at the git level.
- Since SPEC-128, a note's frontmatter may carry `sdd_refs:` — one
  `REF=UUID` line per anchored `BL-<n>`/`SPEC-<n>` mention the memory's
  text carries (e.g. `SPEC-125=<uuid>`), written last and omitted entirely
  when the memory anchors nothing, so a note with no mentions stays
  byte-identical to one written before this field existed. Import forces
  it verbatim onto the local row (never re-derives it), and an older
  mneme reading a newer note ignores the field for forward compatibility.
- Conflict detection after import is the same deterministic FTS5 candidate
  count `mneme conflicts` uses — judgment is always a separate, manual
  `mneme conflicts scan` step, never automatic.
- `mneme team-memory enable` always prints a privacy notice: this feature is
  offline/git-native, so mneme can never determine whether a remote is
  public without a network call it deliberately never makes.

Full reference: `docs/team-memory.md`.

## SDD git-native: the archive, the write, and the read (SPEC-130 §2a + SPEC-131 §2b)

mneme's backlog items and specs live in a local SQLite database — this
section is about a SEPARATE, opt-in mechanism that ALSO writes them as
plain Markdown files under `.mneme/sdd/` in the repository itself, so they
can be reviewed in a pull request the same way any other code change is,
and reads them back into a teammate's own local database.

**§2a and §2b are the first two thirds of a larger mechanism (BL-194).**
§2a (SPEC-130) covers the file format and the write path. §2b (SPEC-131,
this section's newer half) covers the READ path: `mneme sdd import`
(by hand or via `mneme sdd hooks install`'s automatic post-pull/checkout
hooks) brings a teammate's committed `.mneme/sdd/` files into their own
local database. The remaining third — reconciling two machines that
independently claimed the SAME correlative for two DIFFERENT items
(BL-202) — does not exist yet: today such a collision is reported, never
silently resolved. **A repository that enables this mechanism today gets a
reviewable archive AND synchronization between machines that both run
`mneme sdd import` (by hand or via the installed hooks) — reconciling a
genuine numbering collision is still a human's job.**

**Opt-in per repository**, same posture as team-memory: the presence of
`<repo>/.mneme/sdd/.mneme-sdd` (a small, committed JSON marker) is the only
flag. A repository that never runs `mneme sdd enable` is completely
unaffected — verified as an acceptance criterion, not merely claimed:
`git status --porcelain` stays empty through an entire backlog+spec cycle
when the marker is absent.

**The file format** (`internal/sddfile`, a leaf package importing only the
standard library plus `internal/model` — the same perimeter
`internal/vault` has) is a deliberate INVERSION of `internal/vault`'s own
naming rule: a vault memory is named by its UUID so two independent
creations can never collide; an SDD record is named by its human-readable
correlative (`.mneme/sdd/backlog/BL-050.md`,
`.mneme/sdd/specs/SPEC-130/record.md` — never `spec.md`, which belongs to
`model.SpecDocKind`'s closed vocabulary) precisely so that a COLLISION
between two machines' independently-numbered items becomes visible in git
instead of silently overwriting one of them. Every record carries a
frontmatter with `schema: 1` (a hard, range-checked gate: a file written by
a NEWER mneme is refused outright rather than silently losing whatever
section it does not recognise) and a body escaped against its own marker
syntax (`escapeContent`/`unescapeContent` — the literal text
`<!-- mneme:` appearing inside a description, which genuinely happens in
this repository's own backlog, is escaped on write and restored exactly on
read). Every `Marshal` call re-parses the bytes it just produced and
refuses to return them at all if the result does not match the input
field-for-field (`ErrRoundTripMismatch`) — the format can never silently
diverge from what mneme believes it wrote.

**The write path** is synchronous, write-through, and best-effort:
`internal/service/sdd_export.go` wraps the nine store mutations that must
travel to the repository (backlog create/update/refine, spec
create/status/base-SHA/lane-scope, pushback create/resolve); each wrapper
performs its database write and then re-reads the COMPLETE aggregate
(an item with all its refinements; a spec with all its history and
pushbacks) and rewrites its file whole. A structural guardian
(`internal/service/sdd_export_guard_test.go`) proves, by parsing the
source itself, that every one of those nine mutations is reachable ONLY
through its wrapper, and that the tenth write method the SDD engine
has — `InsertLaneAudit` — is a DECLARED exception with its reason on the
same line: `lane_audits` does not travel to the repository until BL-197
(etapa 4). A materialization failure (disk full, permissions) is logged
and never fails the caller's own request — the same posture
`materializeTeamMemory` already established for the shared vault.

**`mneme sdd enable`/`export` refuse to run over content they cannot make
sense of.** Before writing a single byte, both scan whatever `.mneme/sdd/`
already contains: an unparseable file, or one whose UUIDv7 anchor is not
known to the local database, stops the operation entirely and names the
offending files — overwriting a teammate's own item is exactly what this
guards against, and reading such a file is `mneme sdd import`'s job (below),
while reconciling a genuine numbering collision (BL-202) is still not this
mechanism's job.

**The read path** (`internal/service/sdd_import.go`, SPEC-131 §2b) walks
the ENTIRE `.mneme/sdd/` tree every time it runs, and decides what to do
with each file by its UUIDv7 anchor, never by its correlative (D50): a new
anchor is created; an anchor already held by this same correlative is
updated (with children — refinements, spec history, pushbacks — MERGED by
their own key, never deleted, D51); a correlative already claimed by a
DIFFERENT anchor is skipped and reported by title, never by anchor (C2 —
anchors are never printed in any human-readable output, SPEC-128 D9). A
spec whose originating backlog item was archived (SPEC-125's freeze) never
has its status moved by an import even if the incoming file disagrees
(D64) — evaluated against a SNAPSHOT of the freeze state taken BEFORE the
batch's own writes, specifically so an archived-item-plus-moved-spec pair
arriving in the SAME batch cannot produce a false positive. The importer
NEVER compares timestamps (unlike the shared-memory vault's own importer,
which does — see the Team Memory section above): every SDD write already
passed through a file, so a real local conflict would already have shown
up as a git merge conflict before import ever runs. `mneme sdd hooks
install` installs `post-merge`/`post-checkout` git hooks that run the same
import automatically (always exits 0, skips silently mid-rebase/merge,
logs to `~/.mneme/sdd-hooks.log` — a record, never a source of truth).
Once the mechanism is enabled, the next backlog/spec id is the LARGER of
the database's own next id and one past whatever correlative a committed
file already reserves (D55) — a teammate's committed-but-not-yet-imported
`BL-205.md` reserves `BL-205` for everyone, not only for whoever runs the
import first.

Full reference: `docs/sdd-git-native.md`.

## Quality gates, coverage ratchet, executable criteria, graph budget, mutation over the diff, and declarative visual verification (SPEC-115 S1 + SPEC-116 S2 + SPEC-117 S3 + SPEC-118 S4 + SPEC-119 S5 + SPEC-120 S6, EPIC-calidad)

A repository opts in by committing `.mneme/quality.toml` (this repo's own
copy is real, `schema_version = 6`, everything `enabled = false` including
`[budget]`, `[mutation]`, and `[visual]` — `[visual].targets = []` since
this repository has no graphical interface, D15 of S6). mneme runs the
declared gates itself and binds the result to the exact commit —
`spec_advance` only ever COMPARES an already-emitted certificate, never
executes anything.

**Schema versions only ever WIDEN, never narrow** (D5/R2/D9): a
`schema_version = 1` document (no `[coverage]`/`[ratchet]`/`[criteria]`/
`[budget]`/`[mutation]`/`[visual]`) keeps parsing exactly as it always did.
`CurrentSchemaVersion` bumping to 2 (S2), 3 (S3), 4 (S4), 5 (S5), and now
6 (S6) did not brick a single existing constitution — the S2 bump also CORRECTED
the comparison from an equality check (`!= 1 && != CurrentSchemaVersion`)
to a genuine range (`< MinSchemaVersion || > CurrentSchemaVersion`);
without that fix, the bump to 3 alone would have bricked every schema-2
constitution in the world, this repo's own included, before `Parse` ever
read `enabled`. The range check is what let S4's and S5's own bumps land
as pure additions too. S5's own two schema-fixture tests (the "rejected
schema_version" row and `PeekSchemaVersion`'s own fixture) now derive their
rejected value from `CurrentSchemaVersion + 1` instead of a literal — the
fourth spec in the EPIC to retarget this same pair of tests, and the last
one that should ever need to.

**S2 adds seven `quality_checks` rows — zero migrations, zero new
columns** (`kind=coverage`: `profile`, `changed-files-in-profile`,
`diff-lines`; `kind=ratchet`: `baseline-integrity`, `baseline-comparable`,
`global-line-pct`, `baseline-stale`) — the open-vocabulary `kind`/`detail`
design from S1 proven to absorb a whole spec's worth of new checks without
touching `internal/db/migrations`.

**Two coverage-profile formats**, registered the same way
`internal/codegraph/extractor.go` registers language extractors: `lcov`
(the ecosystem lingua franca) and `go-cover` (Go's own native
`go test -coverprofile`, an explicit exception to "stay ecosystem-agnostic"
approved for mneme's own dogfooding). `format` is a DECLARED, closed-set
constitution key — never sniffed from file content (a wrong guess produces
a profile with zero files, which reads as a false 100%).

**The ratchet's baseline** (`.mneme/quality-baseline.toml`, its own
`schema_version`, versioned like the constitution) is written ONLY by
`mneme quality baseline update <spec-id>` (CLI-only, **never an MCP tool**
— writing it is a governance act, the same class as hand-editing
`.mneme/quality.toml`), and only from a spec's latest **`pass`**
certificate — never a typed-in number. Baseline integrity is DIRECTIONAL
(D11): raising the registered mark is free; lowering or deleting it is a
`finding`. Staleness (D17) closes the companion loophole — a mark that
falls too far behind an improving repository is itself a `finding`, with a
1.0-point default margin.

**`internal/quality` gains its second non-stdlib dependency**:
`github.com/bmatcuk/doublestar/v4` (already a module dependency via
`internal/rules`, so nothing new at the PROJECT level) — used to validate
and evaluate `[coverage].exclude` glob patterns. The leaf guard
(`leaf_test.go`) now anchors a set of exactly two allowed imports
(`go-toml/v2`, `doublestar/v4`), not one.

**`make coverage`** produces this repository's own go-cover profile at
`tmp/coverage.out` (ignored three times over by `.gitignore`) — it **must**
inherit `$(TEST_ENV)` exactly like `test`/`test-race`, since the command IS
the entire suite with instrumentation on; without the sandbox it would
write into the real `~/.mneme` DB and vault (SPEC-085, again).

**S3 adds a `3 + N` set of `criteria`/`criterion*` rows — again zero
migrations, zero new columns.** `criteria.toml` (a spec's executable
acceptance criteria, written by the architect via the fifth
`spec_doc_write` kind, since the architect is read-only on the repo) is a
closed vocabulary of four verbs — `file_exists`, `pattern_count`,
`symbol_defined`, `symbol_referenced` — each a pure function of a git ref's
tree (`git ls-tree`/`git grep`, never a checkout or worktree), plus a
`mode = "command"` escape hatch and a `mode = "manual"` verb for the rest.
Every assert-mode criterion is evaluated **twice** — HEAD and the spec's
merge-base — and one that already held at base is `finding` `vacuous`: it
proves nothing about the work done (borrowed from TDD). `mneme quality
sign` (a qa-tester's ATTESTATION, disjoint from `ack`'s ABSOLUTION, both
reusing `store.AckCheck` verbatim) is the **first hook rule in the repo
that fails CLOSED** when a subagent's role cannot be resolved — a
deliberate exception to SPEC-086's fail-open posture, because a signature
whose signer cannot be identified is worse than none. `mneme quality
report` renders the QA report from the certificate, never from
`criteria.toml`, so editing the file after certifying cannot change what a
human reads.

**S4 adds a `budget` tramo (12 rows for standard, 14 for trivial-absorbed)
against the code graph — again zero migrations, zero new columns, and
NO new MCP tool** (D17). `budget.toml` (a spec's declared symbol/impact
budget, written by the architect via `spec_doc_write`'s sixth kind,
`budget` — resolved against real files/symbols and refused BEFORE writing
if invalid) is compared against the SAME symbol delta computed purely from
git blob content (`git show <ref>:<path>`, never a checkout/worktree),
keyed by `(file, qualifiedName)`. Six of the eight possible findings
(`orphan`/`test-only`/`dead`/`single-use-indirection`/`reinvention`/
`untested-reach`) depend on an injected `quality.GraphFacts` over the
project's own indexed code graph (`internal/service/graphfacts.go`'s
`graphFactsAdapter`, wired only by the CLI's `initQualityService`, SPEC-118
P14); the other two (`unbudgeted`/`out-of-radius`) are pure git/budget
arithmetic and need no graph at all. A dedicated freshness check
(`budget/graph-index`) compares the graph's indexed content hash against
each changed file's HEAD blob — a stale index skips the six graph-backed
detections with a `finding`, never silently trusting stale data.

**S4 also ABSORBS the trivial lane's own auditor** (D12): `internal/lane`
is gone entirely (a leaf package cannot import another leaf package, and
the trivial-lane auditor needed exactly what `internal/quality` already
built for S2/S3/S4) — `lane_audit`'s externally-visible contract
(`lane_audits` table, `lane_status`/`lane_stats`, the `audit` status,
`lane_override`/`lane_reclassify`, and the JSON shape `lane_audit`
returns, now `model.LaneAuditResult`) is preserved byte-for-byte, but the
engine underneath is now `internal/quality`'s `Git`/`CollectSymbols`/
`DiffSymbols`/`EvaluateTrivialBudget`, reusing the real cross-language
symbol extractor instead of the old package's Go-only `go/ast` walk +
TypeScript regex heuristic — and `PathChangedInRange` now diffs from
`git merge-base` instead of a raw two-dot range (BL-172). Absorption
itself is conditioned on `[budget].enabled` specifically, **not** the
constitution's top-level `enabled` (which keeps gating standard-lane
gates/coverage/criteria alone): with `[budget]` off, `lane_audit` runs
direct (today's behaviour, on the new engine, no certificate); with it
on, `ensureCertified` requires a usable HEAD certificate for the trivial
lane too, before `implementing → audit`. See `docs/lanes.md`.

**S5 adds a `6 + N` mutation tramo — again zero migrations, zero new
columns, NO new MCP tool.** `internal/quality/mutants.go` registers a
CLOSED six-value mutant vocabulary (`killed`/`lived`/`not_viable`/
`not_covered`/`timed_out`/`skipped`) behind a `MutantReportParser` registry
(`mutants-v1`, the franc format; `gremlins`, go-gremlins/gremlins's native
JSON) — the literal mold of S2's own `ProfileParser`/`registry`. The
central decision (D1) is that a mutation gate can produce the SAME
fabricated green a build-failure-classified-as-killed would: closed with
FOUR independent legs — (a) mutation never evaluates while ANY gate is
`fail`, stricter than the `gatesStopped` cascade every earlier stage
shares (a project may declare its test gate `required=false`); (b) the
registry's own contract test (`TestMutantFormats_RegistryContract`, G5)
refuses to admit a format whose fixture cannot produce a `not_viable`; (c)
`not_viable` never counts as a death; (d) `mutation/viability` — the
MOST IMPORTANT guardian in the spec — is a `finding` past
`max_not_viable_pct`, closing the one case where (a)-(c) alone still leave
open: an informe where EVERYTHING is `not_viable` has zero survivors and
would otherwise read as an unqualified pass. mneme RE-DERIVES the in-diff
mutant set from its own git primitives (`MergeBase`/`ChangedLines`/
`ListFilesAtRef`, ZERO new primitives, `NormalizeSourcePath` reused
verbatim) — a mutator's own `--diff` flag, wired via the single
`{{BASE_SHA}}` substitution token (`ExpandCommand`), is an OPTIMISATION,
never the boundary of correctness. **Verified during this spec's own real
dogfooding**: `gremlins`'s `--diff` flag was found UNUSABLE in this
environment (every candidate mutant came back `SKIPPED`, zero evaluated,
despite a correct `git diff` existing) — exactly the scenario D3 exists to
be safe against, at zero cost beyond a slower run. Also verified for real:
`gremlins` v0.6.0 cannot reliably produce `NOT VIABLE` on a modern Go
toolchain (`go test`'s exit code for a build failure is 1, not 2, on
Go 1.26 — gremlins' own `KILLED`/`NOT VIABLE` split depends on that exit
code). The limitation originates in the tool, but its consequence lands on
mneme's own design: leg (b) turns out to enforce only that a format *can
express* non-viability, not that the tool *emits* it faithfully at runtime,
and leg (d) (`max_not_viable_pct`) never fires because the percentage reads
~0 no matter how many mutants actually failed to build. **Every mutant that
does not compile is counted as `KILLED`, so the mutation score is inflated:
the effect is not a false red, it is false confidence** — the very thing this
mechanism exists to eliminate. Legs (a) and (c) still hold. Documented in
`internal/quality/testdata/README.md` and `docs/quality.md`; the real fix is
tracked as BL-178 (deriving the signal from `go test -json`, which separates
a build failure from a red test without reading the exit code).

A survivor is a `finding`, never a `fail` (`store.AckCheck` only ever
converts a `finding` row) — one row per survivor (`kind=mutant`), capped at
`MaxSurvivorRows` (50, a storage cap on the registry, past which
`mutation/score` itself fails naming the real total), in deterministic
`(file, line, column, mutator)` order. The escotilla reuses S3's `sign`
verb via a single generalized predicate, `quality.RequiresSignature(kind)`
— `Sign` accepts iff true (a criterion, OR now a `mutant` survivor), `Ack`
accepts iff false; before this predicate, the two verbs each carried their
own, independently-written condition that happened to agree. S3's own
sentinels (`ErrNotACriterion`/`ErrCriterionRequiresSign`) are now ALIASES
of the newly-generic `ErrNotSignable`/`ErrRequiresSign` — every existing
`errors.Is` check still holds, unmodified. Declaring a survivor
"semantically equivalent" costs an ABSOLUTE (never percentage)
`max_equivalent` cupo, enforced at signing time from THAT SAME
certificate's `mutation/score` row (`model.ErrEquivalentQuotaExceeded`
past the cupo, or when the row is altogether absent — never "unlimited").
`not_covered` mutants are informative-only (`mutation/not-covered`, always
`pass`, D10 — S2's own `min_diff_line_pct` already judges test coverage of
the diff; doubling that judgement here would impose a silent, binary-
authored 100%), while `timed_out` mutants ARE a `finding`
(`mutation/timeouts`) — neither a death nor a survival, but not nothing.

**SPEC-120 S6 adds `[visual]`/`[visual.compare]` and a `7 + N` row set —
zero migrations, zero new columns, zero new MCP tools, zero new
commands** (`kind=visual`: `report`, `scope`, `render`, `console`, `a11y`,
`compare`, `reference-drift`; `kind=visual-target`: one row per failing
target, id-named, always `fail`). **The decision that matters most: mneme
does NOT supervise a long-lived server process (D1).** `[visual].command`
runs through the SAME `Runner` a gate uses — no widening of
`internal/quality/runner.go`, which this spec does not open — because
killing a whole process tree portably requires `syscall.SysProcAttr.
Setpgid` (Unix-only) or Job Objects (Windows), i.e. per-OS files or build
tags, exactly what this repository's posture forbids. `GOOS=windows go
build ./...` staying green is the standing proof of that premise.
**`visual-v1` is the ONLY registered report format** (`internal/quality/
visual.go`) — unlike S2/S5, no native tool format is registered, because
the gap S6 cannot close is not a format, it is that mneme itself has no
graphical interface; a native browser-runner's own report is a test-result
report anyway and would still need a small reporter to add console/a11y
data. **Pixel comparison is nivel 2, Go-native, stdlib-only**
(`internal/quality/pixel.go`'s `ComparePNG`, `image/png`): dimension
mismatch is `fail` with no invented percentage, `png.DecodeConfig` runs
before `png.Decode` bounded by `MaxComparePixels` so a hostile PNG cannot
exhaust memory, and the tolerance comparison is strict (`>`). **mneme NEVER
writes a reference image** — the first one is approved by a human via
`cp` + `git commit`; a reference changed within the spec's own commit
range is a `finding` `reference-changed-in-range`, computed by the ONE new
git primitive this spec adds, `Git.ChangedFilePathsInRange`
(`internal/quality/git.go`) — necessary because `ChangedLines`/
`ParseUnifiedDiff` can never see a modified BINARY file (git emits `Binary
files … differ`, no `+++ b/`/`@@` for the parser to index), so computing
reference drift from `ChangedLines` would be a finding that can
structurally never fire. (Naming note: `ChangedFilesInRange` was already
taken by S4/SPEC-118's own file-level primitive for the budget mechanism,
with the opposite rename-detection default and a richer return shape —
discovered as a real collision during implementation, not assumed at
design time; this spec's primitive is named `ChangedFilePathsInRange`
instead, same substance, different name.) Console output splits a FACT
from an OPINION (D5): an uncaught exception always fails
`visual/console`; `console.error` only fails when the REQUIRED
`fail_on_console_error` key says so, with no binary default and no
built-in exclusion list. Accessibility (D6) measures against a
project-declared, closed vocabulary of impacts (`critical`/`serious`/
`moderate`/`minor`) that may legitimately be empty (measured, never
blocking), with "declared and not measured" always `fail`. The two
firmable findings (`reference-missing`, `reference-changed-in-range`) are
`ack`, never `sign` — `RequiresSignature` returns `false` for both
`visual` and `visual-target`, governance calls a human makes, not a
technical attestation a `qa-tester` reads code to verify.

Full reference: `docs/quality.md`, and the SDD-tool contract in
`docs/api/sdd.md`.

<!-- mneme:managed:start v=1 -->
Process and operating instructions are managed globally via mneme.
See the mneme operating manual in your global ~/.claude/CLAUDE.md.

Project scope: see this file's sections below for stack, conventions, and module structure.
<!-- mneme:managed:end -->
