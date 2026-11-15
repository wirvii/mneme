---
name: mneme-init
description: "Use once, when you need to bootstrap or reconcile a project with mneme for both Claude Code and Codex. Seeds repo knowledge, applies managed instructions, generates both native agent projections, and offers codegraph and shared-memory setup. Trigger: mneme-init, initialize mneme, onboard this repo, generate subagents, reconcile agent profiles."
version: 2.0.0
pinned: false
---

# mneme-init

Run it when the person asks to initialize or onboard a repo, when `mem_context` returns nothing for this project, after large repo changes, or for one opt-in step alone. Open each reference when you reach its step.

## Firm rules

1. The core step always runs and replaces the native `/init`; never run both.
2. Subagents, codegraph, shared memory and SDD files are separate opt-ins. Ask about each one; a no skips only that one.
3. Use only the tools and commands named here and in the references. Never invent a tool, flag or schema.
4. Permissions (`tools:`, `permissionMode:`) are always Go-authored through `--archetype`. `mneme subagents compose` is a preview: nothing is written until the person confirms and you run `mneme subagents write`.
5. Layer 3 (`areas_layer3_md`) is project knowledge only: never `spec_advance`, `spec_quick`, `spec_reject`, capability lines or role doctrine. compose and write reject a leak; draft so it never happens.
6. One fact per `mem_save`, always with a `topic_key`.
7. Touch CLAUDE.md only through the `init` tool's managed block; report drift, never rewrite the person's prose.
8. Codegraph, shared memory and SDD files run the existing commands; never reimplement them, and relay their privacy notice and preview verbatim.
9. A committed marker means the team already decided: Step 0.6 completes this machine and never re-offers activation.
10. Large inputs travel by stdin or a temporary file outside the repo, never as an argument.
11. Plain language with the person: every question, option and the final report are Channels that reach a person, and each option says what it costs. No invented metaphor, no untranslated term, acronyms expanded; asked again, show the real file or command. The exemption never travels with the text: layer 3 stays precise, but whatever you read back you rewrite.

## Steps (numbering kept from the previous version; there is no Step 4)

| Step | What | Open |
|---|---|---|
| 0 | Core: memory, the `init` tool, seeding, drift | references/core.md |
| 0.6 | Diagnose before offering: SDD and team-memory status, hooks, import | references/core.md |
| 1 | Subagents: `mneme subagents fingerprint`, fusion, `mneme subagents profile save`, compose, write, `mneme subagents manifest-list` | references/subagents.md |
| 2, 3, 5 | Codegraph, shared memory, SDD files | references/opt-ins.md |
| 6 | Final report and how to check each step | references/report.md |

Run Step 0.6 before `mneme subagents fingerprint`: until the import runs, a clone always reads as "no agents".
