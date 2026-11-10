---
name: spec-review
description: "Use when a standard spec needs its fresh-context review: before the human approval gate (the spec) and before implementation (the plan). The orchestrator launches read-only reviewers, the architect resolves every finding and writes the report mneme requires."
version: 1.0.0
pinned: false
---

# spec-review

Whoever wrote a spec reads it with the conversation in their head. This skill has it read by reviewers who never saw that conversation. mneme requires the report on marked standard specs; it records the review, it cannot prove it happened.

## Who does what

- The orchestrator launches the reviewers and hands their answers, whole, to the architect.
- Each reviewer (role `spec-reviewer`, read-only) takes one role and answers in its final message.
- The architect resolves every finding and writes ONE report with `spec_doc_write`, after its last edit of `tarea.md` and `criteria.toml`.

## When

Every standard spec that entered speccing with this binary. When in doubt, review: the guard is the authority and its error names the file that is missing or stale.

- Moment 1 (the spec): kind `review-spec`, file `revision-spec.toml`; roles `comprension`, `diseno`, `hechos`. Required for speccing to specced and for specced to planning.
- Moment 2 (the plan): kind `review-plan`, file `revision-plan.toml`; roles `preparacion`, `hechos`. Required for planned to implementing.

One file per role in `references/`: `comprension.md`, `diseno.md`, `hechos.md`, `preparacion.md`.

## Launching

- Look for an archetype `spec-reviewer` in `mneme subagents manifest-list --json`. If there is none, offer the person to create it with the compose/write step of `mneme-init`. If they decline, the report records every role as `no_corrio` with that note.
- The brief carries only the spec id, the moment, the role, the absolute paths of `tarea.md` and `criteria.toml`, and the repo root. Nothing from the conversation.
- Each reviewer is a new subagent, never a fork of the conversation (Claude Code and Codex).
- A failed reviewer is retried once; if it fails again it is `no_corrio`, never zero findings.
- Caps: at most 10 blocker or major findings per role; nits grouped into one finding per role.

## Resolving

Each finding leaves by one outcome: `corregir`, `refutar` (only with evidence the reviewer could read), `escalar` to the person, or `descartar` (nits only). Never change a severity. Format and rules: `references/report.md`.

## Re-review

One per moment, only if at least one blocker or major was fixed. Relaunch only the affected roles; finding ids continue the numbering. What is still pending afterwards is `riesgo_aceptado`, visible at the gate.

## Changes after the report

Any later edit of `tarea.md` or `criteria.toml` makes the report stale.

- Re-review still unused: relaunch `hechos`, plus `comprension` (moment 1) or `preparacion` (moment 2) if `que` or `plan` changed; rewrite the report with `re_revision = true`.
- Already used: rewrite the report with the same findings and a `nota` on each affected role saying what changed after its review.

## Escalations

The paraphrase and the moment 1 escalations go to the person at the gate (`present-spec`). Moment 2 escalations are presented before implementing; if no reviewer completed, say so first. Record the person's answers in the `reason` of the advance.
