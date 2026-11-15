# Core: Step 0 and Step 0.6

## Step 0: core (always runs, no opt-in)

It replaces the native `/init` command; never run both.

1. Call `mem_context` and `mem_search` (keywords from the repo name and stack) to see what mneme already knows, so you do not seed duplicates.
2. Call the `init` MCP tool (`repo_root` = project root; `check:true` first if unsure) to apply the managed project instructions and get a drift report. Never create a repository `AGENTS.md`: it would suppress Codex's configured `CLAUDE.md` fallback. Touch CLAUDE.md only through this managed block.
3. Read CLAUDE.md and, if present, package.json, go.mod, Cargo.toml, tsconfig.json, docker-compose.yml, .env.example and a 2-level directory listing. Save ONE memory per fact (types architecture, config, convention; topic_keys like `architecture/overview`, `config/commands`, `convention/commits`), so a re-run upserts instead of duplicating. Never dump a whole file into one memory.
4. Classify each CLAUDE.md section as a behavior instruction (stays in CLAUDE.md) or project knowledge (migrate to mneme). Delete nothing from CLAUDE.md; the person cleans it up later if they want.
5. Report what was seeded and the drift findings of point 2.
6. Ask, one at a time, about each opt-in step (subagents, codegraph, shared memory, SDD files).

## Step 0.6: diagnose before offering anything (always runs, right after Step 0)

A committed SDD or shared-memory marker means someone on the team already decided to publish. The question here is never "should we turn this on?" (that is Step 3's or Step 5's, and only when the marker is absent); it is "what does THIS machine still need?". The subagent manifest is a memory, not a file: a database with nothing imported reads as "no agents" in every clone, even when several are committed.

- 0.6.1. Run `mneme sdd status` and `mneme team-memory status` (both read-only) and relay their output.
- 0.6.2. If SDD is enabled: run, without asking, `mneme sdd hooks install` (only if its output says this machine's hooks are missing), then `mneme sdd import`. Never run `mneme sdd enable --apply` here; that is Step 5's gate.
- 0.6.3. If the vault is present: run, without asking, `mneme team-memory hooks install` (only if missing), then `mneme team-memory import`.
- 0.6.4. Only after 0.6.2 and 0.6.3, run `mneme subagents fingerprint --json` (Step 1, phase 0).
- 0.6.5. For a mechanism whose marker is absent this step does nothing: continue with Step 3 or Step 5 as written, warnings verbatim and confirmation gate untouched. Never re-offer activation of a mechanism whose marker is committed; report what you completed instead.
