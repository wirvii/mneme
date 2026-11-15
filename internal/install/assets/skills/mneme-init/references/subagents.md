# Step 1: per-project subagents (opt-in)

Only if the person opts in, and only after Step 0.6. Run the `mneme subagents …` CLI commands through Bash; they are the only data sources and writers. Every confirmed role is ONE canonical contract projected twice, `.claude/agents/<role>.md` and `.codex/agents/<role>.toml`: never run a second initialization for Codex, never create separate memory or SDD state, never hand-author one projection from the other. If either projection fails validation, report that role as failed; never claim partial success.

Every `--role` must match `^[a-z][a-z0-9-]*$`. `--archetype` is one of `architect, backend, frontend, qa-tester, bug-hunter, diagnostician, spec-reviewer`; it alone decides `tools:` and `permissionMode:` (the Go-authored `PermissionTable`).

## Phases

- **Phase 0, fingerprint.** `mneme subagents fingerprint --json`, plus `mem_context` and `mem_search`: project root, apps and packages, stack markers, existing typed memory, and `foreign_agents` (pre-existing `.claude/agents/*.md` mneme did not generate).
- **Phase 0.5, fusion** (only when `foreign_agents` is non-empty; point 6a). `foreign_agents` is the ONLY detection source: never `Read`/`Glob` `.claude/agents/` to find more.
- **Phase 1, repo and org knowledge, asked once** (commit convention, language, layout, cross-cutting rules): `mneme subagents profile get`, then `mneme subagents profile save --stdin --json` (writes `profile_json.repo` and `profile_json.org`).
- **Phase 2, roles.** Propose roles and map apps to them (one subagent per role, covering all its apps); the person adjusts. Always also propose one `spec-reviewer` role (archetype `spec-reviewer`, no write areas): the read-only fresh-context reviewer of the `spec-review` skill. Save with `mneme subagents profile save` (`profile_json.mapping`).
- **Phase 3, detail per role and area:** stack, architecture, commands, best practices. Brownfield: pre-seed from phase 0, `mem_search` and any phase 0.5 extraction. No code yet: ask the desired stack from scratch. This is the `areas_layer3_md` draft: project knowledge only, never `spec_advance`/`spec_quick`/`spec_reject`, never `tools:`/`permissionMode:`, never role doctrine. Layer 1 already governs the lifecycle.
- **Phase 4, compose then write** (points 7 to 10): preview first, write only on confirmation.

## Points

6a. Fusion, before proposing roles. For each foreign path, `Read` it and judge by its content (never by its name: `security-auditor` says nothing reliable about scope) whether it overlaps a role you are about to propose; tell the person what you found and offer exactly one of: (a) fuse: its project knowledge feeds that role's phase 3 draft, extracted and rewritten in your own words, never pasted (pasting is what the layer 2/3 guard exists to catch); (b) adopt as a custom role: compose and write with `--role` = its own name and `--archetype` = the closest of the seven built-ins above; it brings only its extracted knowledge, never its capabilities; (c) leave it alone and never raise it again this session. The person decides each one; never assume.

7. Save the phase 1-2 knowledge with `mneme subagents profile save --stdin --json` (compose reads the saved profile; `--profile-file` overrides it). Then per role run `mneme subagents compose --role <role> --archetype <archetype> --areas-stdin --json`, feeding the phase 3 draft (plus any extraction) on stdin, never as an argument; read `composed_md`, `valid` and `errors` and show the person the preview. A layer 2/3 leak exits non-zero and names the token and line: remove it from the draft and re-compose; never hide the token to get past the check.

7b. Before writing, ask each role the areas-completeness question. It feeds `areas_complete`, which lets the delegation hook's containment block instead of only observe:
    > "`<role>` hoy declara `<apps from Phase 2>`. **Además de las apps**, ¿en qué otras rutas puede escribir — `packages/*-go`, migraciones, config de raíz? Estas van a ser las **únicas** rutas donde podrá escribir."
    Set `areas_complete: true` ONLY when the person explicitly confirms the list is exhaustive. NEVER default it to true and NEVER infer it from phase 2's app mapping: that maps apps, not every path (`packages/*-go`, root config, migrations are easy to miss), and an incomplete complete list makes the role lose legitimate writes once the project promotes to block mode (`mneme delegation-hook promote`). In doubt, leave it false or omit it: an unverified role is observed (`would_block` telemetry), never blocked.

8. On confirmation, write the composed content to a file in the session's temporary directory (never inside the repository) and run once `mneme subagents write --role <role> --archetype <archetype> --composed-file <tmp> --areas <a,b> [--areas-complete] --engine passthrough --json`. Its `artifacts` must hold both runtime artifacts. When it overwrites a foreign Claude profile, its `backup_path` must be non-empty (the original was preserved): mention it.

9. After at least one implementer role is written, offer strict role enforcement. If yes, run `mneme delegation-hook enable`: it registers the two managed PreToolUse hooks in `.claude/settings.json` and `.codex/hooks.json`; check both files carry them. Record the answer with `--enforcement-hook` on `mneme subagents write`. `status` needs both runtimes configured; `disable` removes both.

10. Finish with `mneme subagents manifest-list --json`: each role lists two `artifacts` (`claude-code` and `codex`), its `engine` and `areas`, and an empty or understood `findings` list (the diagnostics of `mneme subagents doctor`). A fused agent now maps to a manifest entry; a declined one stays untouched.
