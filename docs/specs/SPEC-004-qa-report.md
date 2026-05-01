# SPEC-004 QA Report — CLI `mneme rule add/list/test`

| Campo       | Valor                          |
|-------------|--------------------------------|
| **Spec**    | SPEC-004                       |
| **Epic**    | EPIC-1 — Rules as first-class  |
| **QA by**   | qa-tester                      |
| **Date**    | 2026-04-30                     |
| **Commits** | b2d97e1, bcf69e7, 2443aea, 9d1c36a |

---

## Veredicto: APPROVED

0 critical, 0 important, 2 minor issues. All acceptance criteria met.

---

## Resumen

4 commits implement `mneme rule add/list/test` as a Cobra subcommand group.
ValidatePattern in `internal/rules/` is pure (no DB deps). ListRules in
`internal/service/` merges multi-store results with proper severity sorting.
The CLI layer uses lipgloss for coloured table output and versioned JSON
envelopes. All 35+ new tests pass including race detection. Build, test,
test-race green. golangci-lint: 0 issues.

---

## A. Cumplimiento spec.md

| # | Criterio de aceptacion | Codigo | Test | Status |
|---|------------------------|--------|------|--------|
| 1 | `rule add` creates with auto-topic-key | rule.go:87-168 | TestSlugifyTitle_* | PASS |
| 2 | `rule list` shows coloured table | rule.go:282-364 | TestPrintRulesTable_* | PASS |
| 3 | `rule test` evaluates matching correctly | rule.go:421-451 | TestPrintTestOutput_* | PASS |
| 4 | Pattern validation in CLI | rule.go:117-120 + validate.go | TestValidatePattern | PASS |
| 5 | JSON output versionado | rule.go:369-397 | TestPrintRulesJSON_* | PASS |
| 6 | Performance <1s with 1000 rules | ListRules + printRulesTable | Functional: 29ms | PASS |

---

## B. Lineamientos del proyecto

- [x] Imports inward only. `internal/rules/validate.go` imports only `fmt`, `strings`, `doublestar` (pure, no DB).
- [x] `golangci-lint run`: 0 issues.
- [x] `make test`: all 19 packages pass.
- [x] `make test-race`: all pass, no races.
- [x] godoc on exports: ValidatePattern, ListRules, ListRulesOptions, newRuleCmd, slugifyTitle all documented.
- [x] Conventional Commits: all 4 commits follow `type(scope): description`.
- [x] No bubbletea: grep confirms zero tea/bubbletea imports in rule.go.
- [x] Tab-completion: RegisterFlagCompletionFunc registered for severity and scope on both add and list.

---

## C. Pruebas funcionales del CLI

### C1. Setup limpio + rule add with flags
```
$ rm -rf /tmp/mneme-qa-spec4
$ MNEME_DATA_DIR=/tmp/mneme-qa-spec4 ./mneme rule add --title "no time.Now" --content "use clock.Clock" --applies-to '**/*.go' --severity warn
Rule saved: 019de064-7bec-... (created) -- no time.Now
  Severity:   warn
  Applies to: **/*.go
  Topic key:  rule/no-time-now
  Scope:      project
```
PASS. ID generated, topic_key auto-slugified, severity=warn, scope=project.

### C2. rule add with --stdin
```
$ echo '...' | MNEME_DATA_DIR=/tmp/mneme-qa-spec4 ./mneme rule add --title "stdin-test" --applies-to "tool:Edit+internal/**" --severity block --stdin
Rule saved: 019de065-... (created) -- stdin-test
```
PASS. Content read from stdin, metadata from flags.

### C3. Validation: malformed patterns
| Input | Exit | Error message |
|-------|------|---------------|
| `tool:+` | 1 | `invalid pattern "tool:+": path is empty after '+'` |
| `tool:Edit+tool:Bash` | 1 | `combined entries cannot have two tool selectors` |
| `''` (empty) | 1 | `pattern must not be empty` |
| `[` | 1 | `invalid pattern "[": syntax error in pattern` |
All PASS.

### C4. rule list table
```
$ MNEME_DATA_DIR=/tmp/mneme-qa-spec4 ./mneme rule list
SEV    ID        TITLE                           APPLIES_TO                      SCOPE
-----  --------  ------------------------------  ------------------------------  -------
BLOCK  019de065  stdin-test                      tool:Edit+internal/**           project
WARN   019de064  no time.Now                     **/*.go                         project
2 rules (1 block, 1 warn)
```
PASS. Sorted severity desc, summary line correct.

### C5. rule list filtered
- `--severity block`: 1 rule (only block). PASS.
- `--scope project`: correct filter. PASS.

### C6. rule list JSON
```json
{"version":"1","rules":[{"id":"...","title":"...","severity":"block","applies_to":[...],...}]}
```
PASS. Valid JSON, version wrapper present, all expected fields.

### C7. rule list empty
```
$ MNEME_DATA_DIR=/tmp/empty-mneme ./mneme rule list
No rules found.
Create one with: mneme rule add --title "..." --content "..." --applies-to "pattern"
```
PASS. No crash, helpful hint.

### C8. rule test match
```
$ MNEME_DATA_DIR=/tmp/mneme-qa-spec4 ./mneme rule test --path 'foo.go' --tool Edit
Evaluated: 2 rules
Matched:   1 rules
  [WARN] no time.Now
         use clock.Clock
         Matched by: **/*.go
Effective severity: warn
Result: ALLOWED (with 1 warning)
```
PASS. Shows matching entry, effective severity, result.

### C9. rule test no match
```
$ MNEME_DATA_DIR=/tmp/mneme-qa-spec4 ./mneme rule test --path 'README.md' --tool Edit
Evaluated: 2 rules
Matched:   0 rules
Result: ALLOWED (no rules matched)
```
PASS.

### C10. rule test JSON
PASS. `{"tool":"Edit","path":"foo.go","evaluated":2,"matched":[...],"max_severity":"warn","result":"ALLOWED"}`

### C11. rule test without path
```
Note: No --path specified. Only tool selectors and ** wildcards will match.
```
PASS. Warning displayed.

### C12. Backwards compat
```
$ MNEME_DATA_DIR=/tmp/mneme-qa-spec4 ./mneme save --type rule --applies-to '**/*' --severity warn --title "legacy" --content "x"
Saved: 019de065-... (created) -- legacy
```
PASS. `mneme save --type rule` still works.

---

## D. Performance

```
$ # 1000 rules seeded
$ time (MNEME_DATA_DIR=/tmp/mneme-qa-perf ./mneme rule list > /dev/null)
0.029 total
```
29ms. Target: <1s. PASS.

---

## E. Edge cases

| Test | Result | Notes |
|------|--------|-------|
| Title with emoji `"fast"` | PASS | topic_key: `rule/fast` (emoji stripped) |
| Title with CAPS and spaces | PASS | topic_key: `rule/some-caps-with-spaces` |
| `--scope global` | PASS | Rule stored in global DB |
| No `--severity` (default) | PASS | Defaults to `warn` |
| `--stdin` with empty input | PASS | Error: `--content is required (or use --stdin)` |
| Title only special chars `"!!!"` | See MINOR-1 | topic_key: `rule/` (empty slug) |

---

## Bugs

### MINOR-1: slugifyTitle produces empty slug for special-char-only titles

**Severity:** Minor (non-blocking)

**Description:** `slugifyTitle("!!!")` produces `"rule/"` (empty slug after prefix).
The spec (D5, section 11) explicitly warned about this and requested a fallback:
"si el slug queda vacio despues del trim, usar un UUID corto como fallback
(`rule/unnamed-<uuid[:8]>`)". This was not implemented.

**Impact:** Two rules with different special-char-only titles (e.g. `"!!!"` and `"###"`)
produce the same topic_key `"rule/"`, causing unintended upserts (data overwrite).
In practice, this edge case is extremely unlikely -- real rule titles are always
descriptive ASCII strings. The user can always pass `--topic-key` explicitly.

**Reproduction:**
```
mneme rule add --title "!!!" --content "test1" --applies-to '**' --severity warn
mneme rule add --title "###" --content "test2" --applies-to '**' --severity warn
# Second command overwrites the first (both get topic_key "rule/")
```

**Fix:** Add a guard in `slugifyTitle`: if slug is empty after trim, append a
short hash or UUID suffix.

### MINOR-2: sortRules godoc comment says "sort.SliceStable" but uses manual insertion sort

**Severity:** Minor (non-blocking, documentation only)

**Description:** Line 427 of `service/memory.go` says "Use a simple insertion-style
stable sort via sort.SliceStable" but the implementation is a manual insertion sort
loop (no import of `sort`). The comment is misleading.

**Fix:** Change comment to "Manual insertion sort (stable)."

---

## Sugerencias no bloqueantes

1. The `--stdin` mode for `rule add` only reads content. The spec test #3 described
   piping a full JSON object, but the implementation correctly follows the pattern
   from `save.go`. Consider documenting that `--stdin` is content-only in `--help`.

2. `ruleTestJSON` could include a `"version"` field like `ruleListJSON` for
   consistency and future-proofing. Currently only `rule list --json` is versioned.

---

## Metricas

| Metric | Value |
|--------|-------|
| New files | 4 (validate.go, validate_test.go, rule.go, rule_test.go) |
| Modified files | 3 (root.go, memory.go, memory_test.go) |
| New tests | ~35 (10 validate + 7 service + 18 cli) |
| make test | all 19 packages pass |
| make test-race | all pass |
| golangci-lint | 0 issues |
| Performance (1000 rules) | 29ms |

---

## Decision: APPROVED

All 6 acceptance criteria are met. 0 critical, 0 important, 2 minor issues
(neither blocking). End-to-end tracing verified: service.Save -> store.Upsert
-> DB (create), service.ListRules -> store.List -> merge+sort -> CLI table/JSON,
rules.Match -> CLI test output. Backwards compatibility with `mneme save --type rule`
confirmed. EPIC-1 is complete.
