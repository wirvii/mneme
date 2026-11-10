---
name: present-spec
description: "Use at the human approval gate, when a spec is designed (specced) and the person must approve it before planning or implementation. Turns the spec into a plain-language decision and records the explicit answer."
version: 1.1.0
pinned: false
---

# present-spec

The gate is the point of the whole flow: the person decides with full knowledge, not out of trust. The operating manual is the authority on this gate; this skill never relaxes it.

## Steps

1. Read the design and `criteria.toml`, and the plan if there is one. Read `revision-spec.toml` too (the fresh-review report, skill `spec-review`).
2. Tell the person, in their own words and language (Plain language: no invented metaphors, acronyms expanded, jargon explained where it first appears):
   - what changes for them and what it costs in practice;
   - the risks, and what is left out on purpose;
   - the criteria as promises they can check, not as a list of ids;
   - each decision they are approving, with every option explained so it stands alone and what it costs;
   - from the report: the paraphrase (what the reviewer understood the spec proposes), in plain words so they can say whether it matches what they meant;
   - each escalated finding as a decision, with options that stand alone and what each costs;
   - one line: how many findings were corrected, refuted, discarded and accepted as risk;
   - which reviewer did not run and which changes were not reviewed (the `nota` fields).
3. Ask one question: do they approve this whole spec? Wait for an explicit yes or no.
4. On a yes, call `spec_advance` and record in its reason who approved and when, plus the person's answer to each escalated finding. On a no or a change request, send it to the architect: the spec stays in specced, the architect edits the documents there and the review report must be redone (see `spec-review`, "Changes after the report"); present the whole spec again.

## What is not approval

- An answer to a design question is not approval. Neither is "sounds good", a thumbs-up on one part, or silence.
- Approval is about the spec as a whole, given after hearing it.
- The person may skip the gate for one named spec. That is never inherited by the next spec and never becomes a default; every new flow asks again.

## Do not

- Do not paste the spec or the criteria file at the person. Rewrite them.
- Do not advance a spec past specced on your own reading that "they would obviously agree".
