# Rounds and the cap

A round is one rejection, its fix and the re-review of that fix. A spec gets at most three rounds. `spec_status` returns `qa_rounds`: how many times the spec went from qa back to implementing since it was last reopened from done (or since the start), counted by mneme from the history. Never count it yourself.

## The re-review looks only at the fix

A rejection saves the reviewed delivered end as the frontier. When the fix enters qa again, the new `review_range` starts there, so a new `mneme spec review-package <ID>` holds only the fix. Make a new package for every pass; never reuse the previous file. If the notice says the frontier was lost, the range starts again at the base commit: say so in the brief in mneme's words.

## What enters the loop

Only failing declared criteria. A finding outside the contract, minor or not, is a `backlog_add` and never starts a round.

## At the cap: "necesitas decidir"

When `qa_rounds` is 3 or more and the pass still has failing criteria, the qa-tester does not call `spec_reject`. Write to the person once, headed "necesitas decidir", with:

- each open finding in plain words, the criterion id it fails and its evidence;
- the options, each standing alone with what it costs: close the spec accepting the gap; one more round, as an exception for this spec only; or narrow the criteria (the architect edits `criteria.toml`, a visible act the person approves).

Nothing is dropped silently: every open finding is in that message. If they grant one more round, the orchestrator opens it: call `spec_reject` yourself, copying the open findings from the qa-tester's report into `findings` and the person's answer into `reason`. If that round comes back with failing criteria, the same message goes out again. Any other answer goes into the `reason` of the next `spec_advance`.
