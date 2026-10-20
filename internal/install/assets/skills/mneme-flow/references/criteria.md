# The criteria contract

`criteria.toml` is what the spec promises, and the only thing a rejection may claim was broken.

## Format

```toml
schema_version = 1

[[criterion]]
id = "C1"
text = "What must hold, checkable by running something: `go test ./pkg -run TestX -v` passes and prints PASS."
```

- `id` is unique inside the file. Findings in a rejection cite it.
- `text` says what to run and what to expect, so a reviewer can verify it without asking the architect.
- Older files may carry extra keys from earlier versions (`mode`, `evidence_required` and similar). They are accepted and never executed.

## Rules

- Write it only with `spec_doc_write` kind `criteria`. It is validated and refused before writing when invalid.
- A criterion that cannot be checked by running something is a wish, not a criterion. Rewrite it.
- Widening the criteria after the person approved the spec is a visible act: say it, and have the person approve again.
- A finding outside the criteria is not a reason to reject. Open a new `backlog_add` with the evidence.
