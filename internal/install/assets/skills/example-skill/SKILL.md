---
name: example-skill
description: "Example skill fixture demonstrating the SKILL.md schema. Use this as a reference when authoring new skills. This is NOT an architectural guide."
version: 0.0.1
pinned: false
---

<!-- fixture: example-skill. NOT a guide to real architecture. See docs/skills.md for authoring. -->

## When to Use

Use this skill as a structural reference when you need to:
- Understand the required sections of a SKILL.md file.
- Validate that your new skill conforms to the linter rules.
- Test that the skills framework tooling (lint, validate, install) works correctly.

Trigger keywords: "example", "fixture", "greet", "hello".

## Critical Rules

1. This skill is a FIXTURE only — it contains no real architectural guidance.
2. Never cite this skill's content as a project decision or convention.
3. All five H2 sections must be present in any conformant SKILL.md file.
4. The Automated Checks table must have exactly three columns: Check, What it verifies, How to fix.
5. The `name` field in the frontmatter must equal the directory name.

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| Frontmatter name matches directory | `name` field equals the skill directory name | Rename either the directory or the `name` field to match |
| Version is semver | `version` follows `X.Y.Z` format | Update `version` to a valid semver string like `1.0.0` |
| All required sections present | SKILL.md contains the five mandatory H2 headings | Add the missing section(s) as H2 headings |
| Automated Checks table headers | Table has exactly three columns: Check, What it verifies, How to fix | Fix the column headers in the Automated Checks table |
| validation/run.sh exits 0 | The validation script reports success | Inspect the script output and fix the underlying issue |

## Verification

Run the built-in validation script to confirm the skill is operational:

```sh
mneme skills validate example-skill
```

Expected output: `example-skill: validation passed` with exit code 0.

You can also lint the skill structurally without executing any scripts:

```sh
mneme skills lint example-skill
```

## Workflow

1. Confirm you need to create a new skill (see `docs/skills.md`).
2. Copy the `example-skill` directory as a starting template.
3. Rename the directory to your skill name (kebab-case, `[a-z0-9-]+`).
4. Update the frontmatter `name` field to match the directory name.
5. Fill in all five required sections with content relevant to your skill.
6. Write a deterministic `validation/run.sh` that exits 0 on success.
7. Run `mneme skills lint <name>` and fix any reported errors.
8. Run `mneme skills validate <name>` to confirm the script passes.
9. Install with `mneme skills install <name>`.
