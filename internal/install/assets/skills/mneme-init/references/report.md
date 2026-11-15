# Step 6: final report and checks

## Checks before reporting

- `mneme skills validate mneme-init` runs this skill's deterministic validation script.
- After the core step, `mneme init --check` (or the `init` tool with `check:true`) reports the managed block present and lists any drift.
- The checks of each opt-in step sit at the end of that step in `references/subagents.md` and `references/opt-ins.md`.
- Confirm with the person that each opt-in step's outcome (done, skipped, deferred) matches what they asked for.

## Report

23. Summarize what ran in this format (core always runs; each opt-in is done, skipped, not requested or deferred):

```
mneme-init complete for {project}

Core: memories seeded (N), managed blocks applied, drift findings: N
Subagents: <done (roles: ...) | skipped | not requested>
Codegraph: <done | skipped | not requested>
Shared memory: <done (vault + hooks installed) | skipped | not requested>
SDD (backlog/specs as files): <done (N backlog items, N specs exported) | skipped | not requested>
```

24. Close by telling the person, in plain words, what to read next: the `mneme-flow` skill explains how backlog items and specs move from idea to done, and who may move them.
