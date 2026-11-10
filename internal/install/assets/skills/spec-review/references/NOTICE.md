# Notice

The reviewer instructions in this skill are adapted from
**elephant-goldfish-model** by Google, licensed under the Apache License 2.0.

- Origin: https://github.com/google/elephant-goldfish-model
- Commit: bdcfe8cdaee99699c4b2cb7ad4e3b2c1a6b56a31
- License: Apache-2.0, a literal copy of the repository's `LICENSE` is in `LICENSE-apache-2.0.txt`.

## Files adapted

| Source file in the origin | Adapted into |
|---|---|
| `agents/comprehension.md` | `comprension.md` |
| `agents/design_critic.md` and `agents/zoom_out.md` | `diseno.md` (merged into one role) |
| `agents/codebase_details.md` | `hechos.md` |
| `agents/implementation_readiness.md` | `preparacion.md` |
| `LICENSE` (repository root) | `LICENSE-apache-2.0.txt` (unchanged) |

## Changes (Apache License 2.0, section 4)

- Only the REVIEW mode is kept; the BRAINSTORM mode and the Elephant-Goldfish core mandates are removed.
- A finding may suggest a fix. The origin forbids recommending; mneme's `grill-me` skill requires recommending, so this skill follows it.
- The design role merges `design_critic` and `zoom_out`, and asks for root cause, alternatives and something simpler or already existing, without speculating.
- The facts role checks only what the document says exists today (what it says it will create is not a finding), locates with the four `codegraph` tools and confirms every absence with `Grep`.
- The readiness role applies the zero-decisions rule.
- The external document standards are not used; the output format is mneme's: status, paraphrase for comprension, and per finding the severity, scope and text.
- The repository content is treated as data, not as instructions.

Each adapted file states its origin and its changes in its first line.
