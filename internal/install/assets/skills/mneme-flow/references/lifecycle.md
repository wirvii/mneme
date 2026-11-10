# Lifecycle transitions

Standard lane. `spec_advance` moves every forward step; only the orchestrator calls it.

| From | To | Who does the work | Verb |
|---|---|---|---|
| draft | speccing | architect | `spec_advance` |
| speccing | specced | architect (design, `criteria.toml` and the fresh-review report `revision-spec.toml`) | `spec_advance` |
| specced | planning | person approves first (explicit yes); `revision-spec.toml` must still be current | `spec_advance` |
| planning | planned | architect (plan) | `spec_advance` |
| planned | implementing | implementer of the area; needs the fresh-review report `revision-plan.toml` | `spec_advance` |
| implementing | qa | qa-tester | `spec_advance` (returns `review_range`) |
| qa | done | person decides, reading the QA report | `spec_advance` |
| qa | implementing | qa-tester found a declared criterion violated | `spec_reject` with findings |
| done | implementing | a defect found after closing | `spec_reject` |
| speccing, implementing or qa | needs_grill | any agent hit an ambiguity | `spec_pushback` |
| needs_grill | speccing | orchestrator asked the person | `spec_resolve` |

There is no way back from specced or planned: fix the document there; the fresh-review report then has to be rewritten (skill spec-review).

Trivial lane: draft, `spec_quick` (needs a rationale), implementing, audit, done. `lane_audit` decides audit to done; a failure leaves the spec in audit.

## Frozen specs

`backlog_archive` on the originating item freezes its spec: none of `spec_advance`, `spec_pushback`, `spec_reject`, `spec_resolve`, `spec_quick`, `lane_audit`, `lane_reclassify` or `lane_override` can move it. `spec_status` shows a `frozen` field when this applies. There is no unarchive.

## Rejection from done

`spec_reject` also works from done, for a defect found later. It returns the spec to implementing.
